package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sekickivuk-hue/nz-open-emr/internal/audit"
	"github.com/sekickivuk-hue/nz-open-emr/internal/eventstore"
	"github.com/sekickivuk-hue/nz-open-emr/internal/fhir"
	"github.com/sekickivuk-hue/nz-open-emr/internal/identity"
	"github.com/sekickivuk-hue/nz-open-emr/module/problems"
)

func (s *server) registerProblemRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /fhir/r4/Condition", s.createCondition)
	mux.HandleFunc("GET /fhir/r4/Condition/{id}", s.getCondition)
	mux.HandleFunc("GET /fhir/r4/Condition", s.listConditions)
	mux.HandleFunc("POST /fhir/r4/Condition/{id}/resolve", s.resolveCondition)
}

func (s *server) createCondition(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	var c fhir.ConditionFHIR
	if err := json.NewDecoder(limitReader(r)).Decode(&c); err != nil {
		fhir.WriteError(w, 400, "structure", "invalid JSON: "+err.Error())
		return
	}
	if c.Subject == nil || c.Subject.Reference == "" {
		fhir.WriteError(w, 400, "required", "subject.reference is required")
		return
	}
	patientID := extractID(c.Subject.Reference)
	display := ""
	code := ""
	if c.Code != nil && len(c.Code.Coding) > 0 {
		display = c.Code.Coding[0].Display
		code = c.Code.Coding[0].Code
	}
	category := "medical"
	if len(c.Category) > 0 && len(c.Category[0].Coding) > 0 {
		catCode := c.Category[0].Coding[0].Code
		if catCode == "surgical" || catCode == "procedure" {
			category = "surgical"
		}
	}
	id := uuid.New()
	now := time.Now().UTC().Format(time.RFC3339)
	onset := c.OnsetDateTime
	if onset == "" {
		onset = now
	}
	payload, err := eventstore.Canonical(problems.ProblemAdded{
		ID: id.String(), PatientID: patientID,
		Code: code, Display: display, Category: category,
		OnsetDate: onset[:10], RecordedAt: now,
	})
	if err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	ev := &eventstore.Event{
		Type: problems.TypeProblemAdded, AggregateType: "Condition",
		AggregateID: id, Payload: payload, ActorHPI: actor.HPI,
	}
	ae := audit.Entry{ActorHPI: actor.HPI, Action: "C", ResourceType: "Condition", ResourceID: id.String()}
	if err := s.appendEventAndAudit(r.Context(), ev, ae); err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	c.ID = id.String()
	c.RecordedDate = now
	w.Header().Set("Location", "/fhir/r4/Condition/"+c.ID)
	fhir.WriteJSON(w, 201, c)
}

func (s *server) getCondition(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	id := r.PathValue("id")
	var patientID, code, display, category, status string
	var onsetDate, resolvedAt *time.Time
	err := s.pool.QueryRow(r.Context(), `
		SELECT patient_id, COALESCE(code,''), display, COALESCE(category,''), status, onset_date, resolved_at
		FROM problems WHERE id = $1`, id).
		Scan(&patientID, &code, &display, &category, &status, &onsetDate, &resolvedAt)
	if err != nil {
		fhir.WriteError(w, 404, "not-found", "no such condition")
		return
	}
	if err := s.auditRead(r.Context(), actor, "Condition", id); err != nil {
		fhir.WriteError(w, 500, "exception", "audit failed")
		return
	}
	fhir.WriteJSON(w, 200, conditionResource(id, patientID, code, display, category, status, onsetDate))
}

func (s *server) listConditions(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	patientID := r.URL.Query().Get("patient")
	status := r.URL.Query().Get("status") // active (default), resolved, all
	if status == "" {
		status = "active"
	}
	q := `SELECT id, patient_id, COALESCE(code,''), display, COALESCE(category,''), status, onset_date
		FROM problems`
	args := []any{}
	var clauses []string
	if patientID != "" {
		clauses = append(clauses, fmt.Sprintf("patient_id = $%d", len(args)+1))
		args = append(args, patientID)
	}
	if status != "all" {
		clauses = append(clauses, fmt.Sprintf("status = $%d", len(args)+1))
		args = append(args, status)
	}
	if len(clauses) > 0 {
		q += " WHERE " + strings.Join(clauses, " AND ")
	}
	q += ` ORDER BY onset_date DESC LIMIT 50`
	rows, err := s.pool.Query(r.Context(), q, args...)
	if err != nil {
		fhir.WriteError(w, 500, "exception", "database error")
		return
	}
	defer rows.Close()
	var resources []any
	for rows.Next() {
		var cid, pid, code, display, category, cstatus string
		var onsetDate *time.Time
		if err := rows.Scan(&cid, &pid, &code, &display, &category, &cstatus, &onsetDate); err != nil {
			fhir.WriteError(w, 500, "exception", "database error")
			return
		}
		resources = append(resources, conditionResource(cid, pid, code, display, category, cstatus, onsetDate))
	}
	if err := s.auditRead(r.Context(), actor, "Condition", "search"); err != nil {
		fhir.WriteError(w, 500, "exception", "audit failed")
		return
	}
	fhir.WriteJSON(w, 200, fhir.NewSearchSet(resources))
}

func (s *server) resolveCondition(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	id := r.PathValue("id")
	var patientID string
	if err := s.pool.QueryRow(r.Context(),
		`SELECT patient_id FROM problems WHERE id = $1`, id).Scan(&patientID); err != nil {
		fhir.WriteError(w, 404, "not-found", "no such condition")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	payload, err := eventstore.Canonical(problems.ProblemResolved{
		ProblemID: id, PatientID: patientID, ResolvedAt: now,
	})
	if err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	ev := &eventstore.Event{
		Type: problems.TypeProblemResolved, AggregateType: "Condition",
		AggregateID: uuid.MustParse(id), Payload: payload, ActorHPI: actor.HPI,
	}
	ae := audit.Entry{ActorHPI: actor.HPI, Action: "C", ResourceType: "Condition", ResourceID: id}
	if err := s.appendEventAndAudit(r.Context(), ev, ae); err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	fhir.WriteJSON(w, 200, map[string]string{"conditionId": id, "status": "resolved"})
}

func conditionResource(id, patientID, code, display, category, status string, onsetDate *time.Time) fhir.ConditionFHIR {
	c := fhir.ConditionFHIR{
		ID:      id,
		Subject: &fhir.Reference{Reference: "Patient/" + patientID},
		Code:    &fhir.CodeableConcept{Coding: []fhir.Coding{{Code: code, Display: display}}},
		Category: []fhir.CodeableConcept{{Coding: []fhir.Coding{{Code: category}}}},
	}
	if status == "active" {
		c.ClinicalStatus = &fhir.CodeableConcept{Coding: []fhir.Coding{{Code: "active"}}}
	} else {
		c.ClinicalStatus = &fhir.CodeableConcept{Coding: []fhir.Coding{{Code: "resolved"}}}
	}
	if onsetDate != nil {
		c.OnsetDateTime = onsetDate.Format(time.RFC3339)
	}
	return c
}
