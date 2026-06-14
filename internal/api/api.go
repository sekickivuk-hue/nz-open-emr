// Package api wires the HTTP surface: FHIR R4 endpoints behind the
// identity middleware, plus open demo/ops endpoints.
package api

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sekickivuk-hue/nz-open-emr/internal/audit"
	"github.com/sekickivuk-hue/nz-open-emr/internal/eventstore"
	"github.com/sekickivuk-hue/nz-open-emr/internal/fhir"
	"github.com/sekickivuk-hue/nz-open-emr/internal/identity"
	"github.com/sekickivuk-hue/nz-open-emr/internal/nhi"
	"github.com/sekickivuk-hue/nz-open-emr/internal/projection"
	"github.com/sekickivuk-hue/nz-open-emr/web"
)

type server struct {
	pool *pgxpool.Pool
	proj *projection.Projector
}

func New(pool *pgxpool.Pool, proj *projection.Projector) http.Handler {
	s := &server{pool: pool, proj: proj}

	fhirMux := http.NewServeMux()
	fhirMux.HandleFunc("POST /fhir/r4/Patient", s.createPatient)
	fhirMux.HandleFunc("GET /fhir/r4/Patient", s.listPatients)
	fhirMux.HandleFunc("GET /fhir/r4/Patient/{id}", s.getPatient)
	fhirMux.HandleFunc("POST /fhir/r4/DocumentReference", s.createNote)
	fhirMux.HandleFunc("GET /fhir/r4/DocumentReference", s.listNotes)
	fhirMux.HandleFunc("GET /fhir/r4/AuditEvent", s.listAudit)

	root := http.NewServeMux()
	root.Handle("/fhir/r4/", identity.Middleware(fhirMux))
	root.HandleFunc("GET /audit/verify", s.verifyAudit)
	root.HandleFunc("GET /demo/actors", func(w http.ResponseWriter, r *http.Request) {
		writePlainJSON(w, 200, identity.Demo)
	})
	root.HandleFunc("GET /demo/generate-nhi", s.generateNHI)
	root.HandleFunc("GET /healthz", s.healthz)
	// Static demo UI at the root; it contains no PHI.
	root.Handle("/", web.Handler())

	// Module API endpoints — each module registers its own routes.
	s.registerEncounterRoutes(fhirMux)
	s.registerAllergyRoutes(fhirMux)
	s.registerProblemRoutes(fhirMux)
	s.registerFamilyRoutes(root)
	s.registerCareTeamRoutes(root)

	return root
}

// extractID strips the resource type prefix from a FHIR reference
// (e.g. "Patient/abc-123" → "abc-123").
func extractID(ref string) string {
	for i := len(ref) - 1; i >= 0; i-- {
		if ref[i] == '/' {
			return ref[i+1:]
		}
	}
	return ref
}

func writePlainJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// appendEventAndAudit runs the core invariant of the whole system:
// a domain event and its chained audit entry commit atomically.
func (s *server) appendEventAndAudit(ctx context.Context, ev *eventstore.Event, ae audit.Entry) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := eventstore.Append(ctx, tx, ev); err != nil {
		return err
	}
	if _, err := audit.Append(ctx, tx, ae); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// auditRead records a clinical read in its own transaction.
func (s *server) auditRead(ctx context.Context, actor identity.Actor, resourceType, resourceID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := audit.Append(ctx, tx, audit.Entry{
		ActorHPI: actor.HPI, Action: "R",
		ResourceType: resourceType, ResourceID: resourceID,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *server) createPatient(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	var p fhir.Patient
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		fhir.WriteError(w, 400, "structure", "invalid JSON: "+err.Error())
		return
	}
	nhiVal := strings.ToUpper(p.NHI())
	format, err := nhi.Validate(nhiVal)
	if err != nil {
		fhir.WriteError(w, 400, "value", "missing or invalid NHI identifier")
		return
	}
	if len(p.Name) == 0 || p.Name[0].Family == "" || len(p.Name[0].Given) == 0 {
		fhir.WriteError(w, 400, "required", "name with family and given is required")
		return
	}
	var exists bool
	if err := s.pool.QueryRow(r.Context(),
		`SELECT EXISTS (SELECT 1 FROM patients WHERE nhi = $1)`, nhiVal).Scan(&exists); err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	if exists {
		fhir.WriteError(w, 409, "duplicate", "a patient with this NHI already exists")
		return
	}

	id := uuid.New()
	ethCodes := make([]string, 0)
	for _, c := range fhir.EthnicityCodes(p.Extension) {
		ethCodes = append(ethCodes, c.Code)
	}
	citStatus, citSrc := fhir.NZCitizenship(p.Extension)
	birthCountry, birthPlace := fhir.BirthPlace(p.Extension)
	dhb := fhir.DHB(p.Extension)
	iwiCodes := fhir.IwiCodes(p.Extension)

	payload, err := eventstore.Canonical(eventstore.PatientRegistered{
		ID: id.String(), NHI: nhiVal, NHIFormat: string(format),
		FamilyName: p.Name[0].Family, GivenName: p.Name[0].Given[0],
		BirthDate: p.BirthDate, Gender: p.Gender,
		EthnicityCodes: ethCodes, NZCitizenship: citStatus, CitizenshipSrc: citSrc,
		BirthCountry: birthCountry, BirthPlace: birthPlace, DHBCode: dhb, IwiCodes: iwiCodes,
	})
	if err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	ev := &eventstore.Event{
		Type: eventstore.TypePatientRegistered, AggregateType: "Patient",
		AggregateID: id, Payload: payload, ActorHPI: actor.HPI,
	}
	ae := audit.Entry{
		ActorHPI: actor.HPI, Action: "C",
		ResourceType: "Patient", ResourceID: id.String(),
		Detail: fmt.Sprintf(`{"nhi":%q}`, nhiVal),
	}
	if err := s.appendEventAndAudit(r.Context(), ev, ae); err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}

	p.ID = id.String()
	w.Header().Set("Location", "/fhir/r4/Patient/"+p.ID)
	fhir.WriteJSON(w, 201, p)
}

func patientResource(id, nhiVal, family, given, gender string, birth *time.Time,
	ethCodes []string, citizenship, citizenshipSrc, birthCountry, birthPlace, dhb string, iwiCodes []string) fhir.Patient {

	p := fhir.Patient{
		ID:         id,
		Identifier: []fhir.Identifier{{Use: "official", System: fhir.SystemNHI, Value: nhiVal}},
		Name:       []fhir.HumanName{{Family: family, Given: []string{given}}},
		Gender:     gender,
	}
	if birth != nil {
		p.BirthDate = birth.Format("2006-01-02")
	}
	// Build FHIR extensions per NHI IG.
	var exts []fhir.Extension
	for _, c := range ethCodes {
		exts = append(exts, fhir.Extension{
			URL: fhir.ExtEthnicity,
			ValueCodeableConcept: &fhir.CodeableConcept{
				Coding: []fhir.Coding{{Code: c}},
			},
		})
	}
	if citizenship != "" || citizenshipSrc != "" {
		inner := []fhir.Extension{}
		if citizenship != "" {
			inner = append(inner, fhir.Extension{
				URL: "status",
				ValueCodeableConcept: &fhir.CodeableConcept{
					Coding: []fhir.Coding{{Code: citizenship}},
				},
			})
		}
		if citizenshipSrc != "" {
			inner = append(inner, fhir.Extension{
				URL: "source",
				ValueCodeableConcept: &fhir.CodeableConcept{
					Coding: []fhir.Coding{{Code: citizenshipSrc}},
				},
			})
		}
		exts = append(exts, fhir.Extension{URL: fhir.ExtNZCitizen, Extension: inner})
	}
	if birthCountry != "" || birthPlace != "" {
		inner := []fhir.Extension{}
		if birthCountry != "" {
			inner = append(inner, fhir.Extension{
				URL: "country",
				ValueCodeableConcept: &fhir.CodeableConcept{
					Coding: []fhir.Coding{{Code: birthCountry}},
				},
			})
		}
		if birthPlace != "" {
			s := birthPlace
			inner = append(inner, fhir.Extension{URL: "place", ValueString: &s})
		}
		exts = append(exts, fhir.Extension{URL: fhir.ExtBirthPlace, Extension: inner})
	}
	if dhb != "" {
		exts = append(exts, fhir.Extension{
			URL: fhir.ExtDHB,
			ValueCodeableConcept: &fhir.CodeableConcept{
				Coding: []fhir.Coding{{Code: dhb}},
			},
		})
	}
	for _, c := range iwiCodes {
		exts = append(exts, fhir.Extension{
			URL: fhir.ExtIwi,
			ValueCodeableConcept: &fhir.CodeableConcept{
				Coding: []fhir.Coding{{Code: c}},
			},
		})
	}
	if len(exts) > 0 {
		p.Extension = exts
	}
	return p
}

func (s *server) getPatient(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	id := r.PathValue("id")
	var nhiVal, family, given, gender, citizenship, citizenshipSrc, birthCountry, birthPlace, dhb string
	var birth *time.Time
	var ethCodes, iwiCodes []string
	err := s.pool.QueryRow(r.Context(), `
		SELECT nhi, family_name, given_name, gender, birth_date,
			ethnicity_codes, nz_citizenship, citizenship_src, birth_country, birth_place, dhb_code, iwi_codes
		FROM patients WHERE id = $1`, id).
		Scan(&nhiVal, &family, &given, &gender, &birth,
			&ethCodes, &citizenship, &citizenshipSrc, &birthCountry, &birthPlace, &dhb, &iwiCodes)
	if err != nil {
		fhir.WriteError(w, 404, "not-found", "no such patient")
		return
	}
	if err := s.auditRead(r.Context(), actor, "Patient", id); err != nil {
		fhir.WriteError(w, 500, "exception", "audit failed; read refused")
		return
	}
	if ethCodes == nil { ethCodes = []string{} }
	if iwiCodes == nil { iwiCodes = []string{} }
	fhir.WriteJSON(w, 200, patientResource(id, nhiVal, family, given, gender, birth,
		ethCodes, citizenship, citizenshipSrc, birthCountry, birthPlace, dhb, iwiCodes))
}

func (s *server) listPatients(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	q := `SELECT id, nhi, family_name, given_name, gender, birth_date,
		ethnicity_codes, nz_citizenship, citizenship_src, birth_country, birth_place, dhb_code, iwi_codes
		FROM patients`
	args := []any{}
	if ident := r.URL.Query().Get("identifier"); ident != "" {
		q += ` WHERE nhi = $1`
		args = append(args, strings.ToUpper(ident))
	}
	q += ` ORDER BY family_name, given_name LIMIT 100`
	rows, err := s.pool.Query(r.Context(), q, args...)
	if err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	defer rows.Close()
	var resources []any
	for rows.Next() {
		var id, nhiVal, family, given, gender, citizenship, citizenshipSrc, birthCountry, birthPlace, dhb string
		var birth *time.Time
		var ethCodes, iwiCodes []string
		if err := rows.Scan(&id, &nhiVal, &family, &given, &gender, &birth,
			&ethCodes, &citizenship, &citizenshipSrc, &birthCountry, &birthPlace, &dhb, &iwiCodes); err != nil {
			fhir.WriteError(w, 500, "exception", err.Error())
			return
		}
		if ethCodes == nil { ethCodes = []string{} }
		if iwiCodes == nil { iwiCodes = []string{} }
		resources = append(resources, patientResource(id, nhiVal, family, given, gender, birth,
			ethCodes, citizenship, citizenshipSrc, birthCountry, birthPlace, dhb, iwiCodes))
	}
	if err := s.auditRead(r.Context(), actor, "Patient", "search"); err != nil {
		fhir.WriteError(w, 500, "exception", "audit failed; read refused")
		return
	}
	fhir.WriteJSON(w, 200, fhir.NewSearchSet(resources))
}

func (s *server) createNote(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	var d fhir.DocumentReference
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		fhir.WriteError(w, 400, "structure", "invalid JSON: "+err.Error())
		return
	}
	if d.Subject == nil || !strings.HasPrefix(d.Subject.Reference, "Patient/") {
		fhir.WriteError(w, 400, "required", "subject.reference Patient/{id} is required")
		return
	}
	patientID := strings.TrimPrefix(d.Subject.Reference, "Patient/")
	if len(d.Content) == 0 || d.Content[0].Attachment.Data == "" {
		fhir.WriteError(w, 400, "required", "content[0].attachment.data is required")
		return
	}
	text, err := base64.StdEncoding.DecodeString(d.Content[0].Attachment.Data)
	if err != nil {
		fhir.WriteError(w, 400, "value", "attachment.data is not valid base64")
		return
	}
	var exists bool
	if err := s.pool.QueryRow(r.Context(),
		`SELECT EXISTS (SELECT 1 FROM patients WHERE id = $1)`, patientID).Scan(&exists); err != nil || !exists {
		fhir.WriteError(w, 422, "not-found", "subject patient does not exist")
		return
	}

	id := uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	payload, err := eventstore.Canonical(eventstore.NoteCreated{
		ID: id.String(), PatientID: patientID, AuthorHPI: actor.HPI,
		Text: string(text), CreatedAt: now.Format(time.RFC3339Nano),
	})
	if err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	ev := &eventstore.Event{
		Type: eventstore.TypeNoteCreated, AggregateType: "Note",
		AggregateID: id, Payload: payload, ActorHPI: actor.HPI,
	}
	ae := audit.Entry{
		ActorHPI: actor.HPI, Action: "C",
		ResourceType: "DocumentReference", ResourceID: id.String(),
		Detail: fmt.Sprintf(`{"patient":%q}`, patientID),
	}
	if err := s.appendEventAndAudit(r.Context(), ev, ae); err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}

	d.ID = id.String()
	d.Date = now.Format(time.RFC3339Nano)
	d.Author = []fhir.Reference{{
		Display:    actor.Name,
		Identifier: &fhir.Identifier{System: fhir.SystemHPI, Value: actor.HPI},
	}}
	w.Header().Set("Location", "/fhir/r4/DocumentReference/"+d.ID)
	fhir.WriteJSON(w, 201, d)
}

func (s *server) listNotes(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	patientID := r.URL.Query().Get("patient")
	if patientID == "" {
		fhir.WriteError(w, 400, "required", "patient query parameter is required")
		return
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, author_hpi, text, created_at FROM notes
		WHERE patient_id = $1 ORDER BY created_at`, patientID)
	if err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	defer rows.Close()
	var resources []any
	for rows.Next() {
		var id, author, text string
		var created time.Time
		if err := rows.Scan(&id, &author, &text, &created); err != nil {
			fhir.WriteError(w, 500, "exception", err.Error())
			return
		}
		authorName := author
		if a, ok := identity.Lookup(author); ok {
			authorName = a.Name
		}
		resources = append(resources, fhir.DocumentReference{
			ID: id, Status: "current",
			Subject: &fhir.Reference{Reference: "Patient/" + patientID},
			Date:    created.UTC().Format(time.RFC3339Nano),
			Author: []fhir.Reference{{
				Display:    authorName,
				Identifier: &fhir.Identifier{System: fhir.SystemHPI, Value: author},
			}},
			Content: []fhir.DocContent{{Attachment: fhir.Attachment{
				ContentType: "text/plain",
				Data:        base64.StdEncoding.EncodeToString([]byte(text)),
			}}},
		})
	}
	if err := s.auditRead(r.Context(), actor, "DocumentReference", "patient="+patientID); err != nil {
		fhir.WriteError(w, 500, "exception", "audit failed; read refused")
		return
	}
	fhir.WriteJSON(w, 200, fhir.NewSearchSet(resources))
}

// listAudit serves the audit trail as FHIR AuditEvents. Reads of the
// audit log itself are not re-audited in the skeleton (the UI polls it;
// self-amplification would drown the log). Revisit in the Module 5 spec.
func (s *server) listAudit(w http.ResponseWriter, r *http.Request) {
	count := 50
	if c, err := strconv.Atoi(r.URL.Query().Get("_count")); err == nil && c > 0 && c <= 200 {
		count = c
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT seq, hash, actor_hpi, action, resource_type, resource_id, at
		FROM audit_events ORDER BY seq DESC LIMIT $1`, count)
	if err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	defer rows.Close()
	var resources []any
	for rows.Next() {
		var seq int64
		var hash []byte
		var actorHPI, action, rtype, rid string
		var at time.Time
		if err := rows.Scan(&seq, &hash, &actorHPI, &action, &rtype, &rid, &at); err != nil {
			fhir.WriteError(w, 500, "exception", err.Error())
			return
		}
		name := actorHPI
		if a, ok := identity.Lookup(actorHPI); ok {
			name = a.Name
		}
		resources = append(resources, fhir.AuditEvent{
			ID:        strconv.FormatInt(seq, 10),
			Action:    action,
			Recorded:  at.UTC().Format(time.RFC3339Nano),
			Outcome:   "0",
			Extension: []fhir.Extension{{URL: fhir.ExtAuditHash, ValueString: strPtr(hex.EncodeToString(hash))}},
			Agent: []fhir.AuditAgent{{Requestor: true, Who: &fhir.Reference{
				Display:    name,
				Identifier: &fhir.Identifier{System: fhir.SystemHPI, Value: actorHPI},
			}}},
			Entity: []fhir.AuditEntity{{What: &fhir.Reference{Reference: rtype + "/" + rid}}},
		})
	}
	fhir.WriteJSON(w, 200, fhir.NewSearchSet(resources))
}

func (s *server) verifyAudit(w http.ResponseWriter, r *http.Request) {
	rep, err := audit.Verify(r.Context(), s.pool)
	if err != nil {
		writePlainJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writePlainJSON(w, 200, rep)
}

func (s *server) generateNHI(w http.ResponseWriter, r *http.Request) {
	format := nhi.Format(r.URL.Query().Get("format"))
	if format != nhi.FormatNew {
		format = nhi.FormatLegacy
	}
	v, err := nhi.GenerateSynthetic(format)
	if err != nil {
		writePlainJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writePlainJSON(w, 200, map[string]string{"nhi": v, "format": string(format)})
}

func (s *server) healthz(w http.ResponseWriter, r *http.Request) {
	if err := s.pool.Ping(r.Context()); err != nil {
		writePlainJSON(w, 503, map[string]string{"status": "db unreachable"})
		return
	}
	writePlainJSON(w, 200, map[string]string{"status": "ok"})
}

func strPtr(s string) *string { return &s }
