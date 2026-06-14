package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/sekickivuk-hue/nz-open-emr/internal/audit"
	"github.com/sekickivuk-hue/nz-open-emr/internal/eventstore"
	"github.com/sekickivuk-hue/nz-open-emr/internal/fhir"
	"github.com/sekickivuk-hue/nz-open-emr/internal/identity"
	"github.com/sekickivuk-hue/nz-open-emr/module/allergies"
)

func (s *server) registerAllergyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /fhir/r4/AllergyIntolerance", s.createAllergy)
	mux.HandleFunc("GET /fhir/r4/AllergyIntolerance/{id}", s.getAllergy)
	mux.HandleFunc("GET /fhir/r4/AllergyIntolerance", s.listAllergies)
	mux.HandleFunc("POST /fhir/r4/AllergyIntolerance/{id}/remove", s.removeAllergy)
}

func (s *server) createAllergy(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	var a fhir.AllergyIntoleranceFHIR
	if err := json.NewDecoder(limitReader(r)).Decode(&a); err != nil {
		fhir.WriteError(w, 400, "structure", "invalid JSON: "+err.Error())
		return
	}
	if a.Patient == nil || a.Patient.Reference == "" {
		fhir.WriteError(w, 400, "required", "patient.reference is required")
		return
	}
	patientID := extractID(a.Patient.Reference)
	substance := ""
	if a.Code != nil && len(a.Code.Coding) > 0 {
		substance = a.Code.Coding[0].Display
		if substance == "" {
			substance = a.Code.Coding[0].Code
		}
	}
	reaction := ""
	severity := ""
	if len(a.Reaction) > 0 {
		if len(a.Reaction[0].Manifestation) > 0 {
			reaction = a.Reaction[0].Manifestation[0].Text
			if reaction == "" && len(a.Reaction[0].Manifestation[0].Coding) > 0 {
				reaction = a.Reaction[0].Manifestation[0].Coding[0].Display
			}
		}
		severity = a.Reaction[0].Severity
	}
	id := uuid.New()
	now := time.Now().UTC().Format(time.RFC3339)
	payload, err := eventstore.Canonical(allergies.AllergyAdded{
		ID: id.String(), PatientID: patientID,
		Substance: substance, Reaction: reaction, Severity: severity,
		RecordedByHPI: actor.HPI, RecordedAt: now,
	})
	if err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	ev := &eventstore.Event{
		Type: allergies.TypeAllergyAdded, AggregateType: "AllergyIntolerance",
		AggregateID: id, Payload: payload, ActorHPI: actor.HPI,
	}
	ae := audit.Entry{ActorHPI: actor.HPI, Action: "C", ResourceType: "AllergyIntolerance", ResourceID: id.String()}
	if err := s.appendEventAndAudit(r.Context(), ev, ae); err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	a.ID = id.String()
	a.RecordedDate = now
	w.Header().Set("Location", "/fhir/r4/AllergyIntolerance/"+a.ID)
	fhir.WriteJSON(w, 201, a)
}

func (s *server) getAllergy(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	id := r.PathValue("id")
	var patientID, substance, reaction, severity, status string
	var recordedAt time.Time
	err := s.pool.QueryRow(r.Context(), `
		SELECT patient_id, substance, COALESCE(reaction,''), COALESCE(severity,''), status, recorded_at
		FROM allergies WHERE id = $1`, id).
		Scan(&patientID, &substance, &reaction, &severity, &status, &recordedAt)
	if err != nil {
		fhir.WriteError(w, 404, "not-found", "no such allergy")
		return
	}
	if err := s.auditRead(r.Context(), actor, "AllergyIntolerance", id); err != nil {
		fhir.WriteError(w, 500, "exception", "audit failed")
		return
	}
	fhir.WriteJSON(w, 200, allergyResource(id, patientID, substance, reaction, severity, status, recordedAt))
}

func (s *server) listAllergies(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	patientID := r.URL.Query().Get("patient")
	q := `SELECT id, patient_id, substance, COALESCE(reaction,''), COALESCE(severity,''), status, recorded_at
		FROM allergies`
	args := []any{}
	if patientID != "" {
		q += ` WHERE patient_id = $1 AND status = 'active'`
		args = append(args, patientID)
	}
	q += ` ORDER BY recorded_at DESC LIMIT 50`
	rows, err := s.pool.Query(r.Context(), q, args...)
	if err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	defer rows.Close()
	var resources []any
	for rows.Next() {
		var aid, pid, substance, reaction, severity, status string
		var recordedAt time.Time
		if err := rows.Scan(&aid, &pid, &substance, &reaction, &severity, &status, &recordedAt); err != nil {
			fhir.WriteError(w, 500, "exception", err.Error())
			return
		}
		resources = append(resources, allergyResource(aid, pid, substance, reaction, severity, status, recordedAt))
	}
	if err := s.auditRead(r.Context(), actor, "AllergyIntolerance", "search"); err != nil {
		fhir.WriteError(w, 500, "exception", "audit failed")
		return
	}
	fhir.WriteJSON(w, 200, fhir.NewSearchSet(resources))
}

func (s *server) removeAllergy(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	id := r.PathValue("id")
	var patientID string
	if err := s.pool.QueryRow(r.Context(),
		`SELECT patient_id FROM allergies WHERE id = $1`, id).Scan(&patientID); err != nil {
		fhir.WriteError(w, 404, "not-found", "no such allergy")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	json.NewDecoder(limitReader(r)).Decode(&req)
	now := time.Now().UTC().Format(time.RFC3339)
	payload, err := eventstore.Canonical(allergies.AllergyRemoved{
		AllergyID: id, PatientID: patientID,
		RemovedByHPI: actor.HPI, RemovedAt: now, Reason: req.Reason,
	})
	if err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	ev := &eventstore.Event{
		Type: allergies.TypeAllergyRemoved, AggregateType: "AllergyIntolerance",
		AggregateID: uuid.MustParse(id), Payload: payload, ActorHPI: actor.HPI,
	}
	ae := audit.Entry{ActorHPI: actor.HPI, Action: "C", ResourceType: "AllergyIntolerance", ResourceID: id}
	if err := s.appendEventAndAudit(r.Context(), ev, ae); err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	fhir.WriteJSON(w, 200, map[string]string{"allergyId": id, "status": "removed"})
}

func allergyResource(id, patientID, substance, reaction, severity, status string, recordedAt time.Time) fhir.AllergyIntoleranceFHIR {
	a := fhir.AllergyIntoleranceFHIR{
		ID:     id,
		Patient: &fhir.Reference{Reference: "Patient/" + patientID},
		Code:   &fhir.CodeableConcept{Coding: []fhir.Coding{{Display: substance}}},
		RecordedDate: recordedAt.Format(time.RFC3339),
	}
	if status == "active" {
		a.ClinicalStatus = &fhir.CodeableConcept{Coding: []fhir.Coding{{Code: "active"}}}
	} else {
		a.ClinicalStatus = &fhir.CodeableConcept{Coding: []fhir.Coding{{Code: "inactive"}}}
	}
	if reaction != "" {
		a.Reaction = []fhir.AllergyReaction{{
			Manifestation: []fhir.CodeableConcept{{Text: reaction}},
			Severity:      severity,
		}}
	}
	return a
}
