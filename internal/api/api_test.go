package api_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sekickivuk-hue/nz-open-emr/internal/api"
	"github.com/sekickivuk-hue/nz-open-emr/internal/projection"
	"github.com/sekickivuk-hue/nz-open-emr/internal/testutil"
)

func do(t *testing.T, srv *httptest.Server, method, path, actor string, body any) (*http.Response, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, srv.URL+path, rd)
	if actor != "" {
		req.Header.Set("X-Actor-HPI", actor)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, b
}

func TestClinicalJourneyAndTamperEvidence(t *testing.T) {
	pool := testutil.RequireDB(t)
	ctx := context.Background()
	proj := &projection.Projector{Pool: pool}
	srv := httptest.NewServer(api.New(pool, proj))
	defer srv.Close()

	// 1. Unauthenticated request is rejected.
	resp, _ := do(t, srv, "GET", "/fhir/r4/Patient", "", nil)
	if resp.StatusCode != 401 {
		t.Fatalf("no actor: %d", resp.StatusCode)
	}

	// 2. Create a patient.
	patient := map[string]any{
		"resourceType": "Patient",
		"identifier":   []map[string]any{{"system": "https://standards.digital.health.nz/ns/nhi-id", "value": "ZZZ0016"}},
		"name":         []map[string]any{{"family": "Demo", "given": []string{"Pat"}}},
		"birthDate":    "1980-01-01",
	}
	resp, body := do(t, srv, "POST", "/fhir/r4/Patient", "99ZZZA", patient)
	if resp.StatusCode != 201 {
		t.Fatalf("create patient: %d %s", resp.StatusCode, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	json.Unmarshal(body, &created)
	if created.ID == "" {
		t.Fatal("no id in created patient")
	}

	// 3. Duplicate NHI → 409 (needs projection to have landed).
	if err := proj.Step(ctx); err != nil {
		t.Fatal(err)
	}
	resp, _ = do(t, srv, "POST", "/fhir/r4/Patient", "99ZZZA", patient)
	if resp.StatusCode != 409 {
		t.Fatalf("duplicate NHI: %d", resp.StatusCode)
	}

	// 4. Bad NHI checksum → 400.
	bad := map[string]any{
		"resourceType": "Patient",
		"identifier":   []map[string]any{{"system": "https://standards.digital.health.nz/ns/nhi-id", "value": "ZZZ0017"}},
		"name":         []map[string]any{{"family": "X", "given": []string{"Y"}}},
	}
	resp, _ = do(t, srv, "POST", "/fhir/r4/Patient", "99ZZZA", bad)
	if resp.StatusCode != 400 {
		t.Fatalf("bad NHI: %d", resp.StatusCode)
	}

	// 5. Read the patient back (this audits a read by a second actor).
	resp, body = do(t, srv, "GET", "/fhir/r4/Patient/"+created.ID, "99ZZZB", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("get patient: %d %s", resp.StatusCode, body)
	}

	// 6. Write a clinical note.
	note := map[string]any{
		"resourceType": "DocumentReference",
		"subject":      map[string]any{"reference": "Patient/" + created.ID},
		"content": []map[string]any{{"attachment": map[string]any{
			"contentType": "text/plain",
			"data":        base64.StdEncoding.EncodeToString([]byte("Patient seen, well. Kia ora.")),
		}}},
	}
	resp, body = do(t, srv, "POST", "/fhir/r4/DocumentReference", "99ZZZA", note)
	if resp.StatusCode != 201 {
		t.Fatalf("create note: %d %s", resp.StatusCode, body)
	}
	if err := proj.Step(ctx); err != nil {
		t.Fatal(err)
	}
	resp, body = do(t, srv, "GET", "/fhir/r4/DocumentReference?patient="+created.ID, "99ZZZA", nil)
	var noteBundle struct {
		Total int `json:"total"`
	}
	json.Unmarshal(body, &noteBundle)
	if resp.StatusCode != 200 || noteBundle.Total != 1 {
		t.Fatalf("list notes: %d total=%d body=%s", resp.StatusCode, noteBundle.Total, body)
	}

	// 7. Audit log has events; chain verifies.
	resp, body = do(t, srv, "GET", "/fhir/r4/AuditEvent", "99ZZZA", nil)
	var auditBundle struct {
		Total int `json:"total"`
	}
	json.Unmarshal(body, &auditBundle)
	if resp.StatusCode != 200 || auditBundle.Total < 4 {
		t.Fatalf("audit bundle: %d total=%d", resp.StatusCode, auditBundle.Total)
	}
	resp, body = do(t, srv, "GET", "/audit/verify", "", nil)
	var rep struct {
		OK        bool  `json:"ok"`
		BrokenSeq int64 `json:"brokenSeq"`
	}
	json.Unmarshal(body, &rep)
	if resp.StatusCode != 200 || !rep.OK {
		t.Fatalf("verify before tamper: %d %s", resp.StatusCode, body)
	}

	// 8. THE FLAGSHIP: a DBA edits history; verification pinpoints it.
	if _, err := pool.Exec(ctx,
		`UPDATE audit_events SET actor_hpi = 'EVIL' WHERE seq = 2`); err != nil {
		t.Fatal(err)
	}
	_, body = do(t, srv, "GET", "/audit/verify", "", nil)
	json.Unmarshal(body, &rep)
	if rep.OK || rep.BrokenSeq != 2 {
		t.Fatalf("tamper not detected at seq 2: %s", body)
	}

	// 9. Health endpoint.
	resp, _ = do(t, srv, "GET", "/healthz", "", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("healthz: %d", resp.StatusCode)
	}

	// 10. Demo endpoints.
	resp, body = do(t, srv, "GET", "/demo/generate-nhi?format=new", "", nil)
	var gen struct{ NHI, Format string }
	json.Unmarshal(body, &gen)
	if resp.StatusCode != 200 || len(gen.NHI) != 7 || gen.Format != "new" {
		t.Fatalf("generate-nhi: %d %s", resp.StatusCode, body)
	}
	resp, body = do(t, srv, "GET", "/demo/actors", "", nil)
	var actors []struct{ HPI string `json:"hpi"` }
	json.Unmarshal(body, &actors)
	if resp.StatusCode != 200 || len(actors) < 2 {
		t.Fatalf("actors: %d %s", resp.StatusCode, body)
	}
}
