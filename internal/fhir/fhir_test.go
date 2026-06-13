package fhir_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sekickivuk-hue/nz-open-emr/internal/fhir"
)

func TestPatientJSONShape(t *testing.T) {
	p := fhir.Patient{
		ID:         "abc",
		Identifier: []fhir.Identifier{{Use: "official", System: fhir.SystemNHI, Value: "ZZZ0016"}},
		Name:       []fhir.HumanName{{Family: "Demo", Given: []string{"Pat"}}},
		BirthDate:  "1980-01-01",
	}
	b, _ := json.Marshal(p)
	s := string(b)
	for _, want := range []string{
		`"resourceType":"Patient"`, `"id":"abc"`,
		`"system":"https://standards.digital.health.nz/ns/nhi-id"`,
		`"family":"Demo"`, `"birthDate":"1980-01-01"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("Patient JSON missing %s in %s", want, s)
		}
	}
}

func TestNHIFromPatient(t *testing.T) {
	p := fhir.Patient{Identifier: []fhir.Identifier{
		{System: "urn:other", Value: "x"},
		{System: fhir.SystemNHI, Value: "ZZZ0016"},
	}}
	if got := p.NHI(); got != "ZZZ0016" {
		t.Fatalf("NHI() = %q", got)
	}
	if got := (fhir.Patient{}).NHI(); got != "" {
		t.Fatalf("empty patient NHI() = %q", got)
	}
}

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	fhir.WriteError(rec, 404, "not-found", "no such patient")
	if rec.Code != 404 {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/fhir+json" {
		t.Fatalf("content-type %q", ct)
	}
	var oo fhir.OperationOutcome
	if err := json.Unmarshal(rec.Body.Bytes(), &oo); err != nil ||
		oo.ResourceType != "OperationOutcome" || len(oo.Issue) != 1 {
		t.Fatalf("body: %s err: %v", rec.Body.String(), err)
	}
}
