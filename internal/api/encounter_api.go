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
	"github.com/sekickivuk-hue/nz-open-emr/module/encounters"
)

func (s *server) registerEncounterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /fhir/r4/Encounter", s.createEncounter)
	mux.HandleFunc("GET /fhir/r4/Encounter/{id}", s.getEncounter)
	mux.HandleFunc("GET /fhir/r4/Encounter", s.listEncounters)
	mux.HandleFunc("POST /fhir/r4/Encounter/{id}/diagnosis", s.addDiagnosis)
	mux.HandleFunc("POST /fhir/r4/Encounter/{id}/close", s.closeEncounter)
}

func (s *server) createEncounter(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	var enc fhir.EncounterFHIR
	if err := json.NewDecoder(limitReader(r)).Decode(&enc); err != nil {
		fhir.WriteError(w, 400, "structure", "invalid JSON: "+err.Error())
		return
	}
	if enc.Subject == nil || enc.Subject.Reference == "" {
		fhir.WriteError(w, 400, "required", "subject.reference is required")
		return
	}
	patientID := extractID(enc.Subject.Reference)
	if patientID == "" {
		fhir.WriteError(w, 400, "required", "subject.reference must contain a valid patient ID")
		return
	}
	var exists bool
	if err := s.pool.QueryRow(r.Context(),
		`SELECT EXISTS (SELECT 1 FROM patients WHERE id = $1)`, patientID).Scan(&exists); err != nil || !exists {
		fhir.WriteError(w, 422, "not-found", "subject patient does not exist")
		return
	}
	id := uuid.New()
	now := time.Now().UTC().Format(time.RFC3339)
	classCode := enc.Class.Code
	if classCode == "" {
		classCode = "AMB" // default ambulatory
	}
	payload, err := eventstore.Canonical(encounters.EncounterOpened{
		ID: id.String(), PatientID: patientID,
		EncounterClass: classCode,
		Facility:       "",
		Ward:           "",
		OpenedAt:       now,
	})
	if err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	ev := &eventstore.Event{
		Type: encounters.TypeEncounterOpened, AggregateType: "Encounter",
		AggregateID: id, Payload: payload, ActorHPI: actor.HPI,
	}
	ae := audit.Entry{ActorHPI: actor.HPI, Action: "C", ResourceType: "Encounter", ResourceID: id.String()}
	if err := s.appendEventAndAudit(r.Context(), ev, ae); err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	enc.ID = id.String()
	enc.Status = "in-progress"
	w.Header().Set("Location", "/fhir/r4/Encounter/"+enc.ID)
	fhir.WriteJSON(w, 201, enc)
}

func (s *server) getEncounter(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	id := r.PathValue("id")
	var patientID, class, status, disposition string
	var openedAt, closedAt *time.Time
	err := s.pool.QueryRow(r.Context(), `
		SELECT patient_id, encounter_class, status, COALESCE(disposition,''),
			opened_at, closed_at
		FROM encounters WHERE id = $1`, id).
		Scan(&patientID, &class, &status, &disposition, &openedAt, &closedAt)
	if err != nil {
		fhir.WriteError(w, 404, "not-found", "no such encounter")
		return
	}
	if err := s.auditRead(r.Context(), actor, "Encounter", id); err != nil {
		fhir.WriteError(w, 500, "exception", "audit failed")
		return
	}
	enc := encounterResource(id, patientID, class, status, disposition, openedAt, closedAt)
	fhir.WriteJSON(w, 200, enc)
}

func (s *server) listEncounters(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	patientID := r.URL.Query().Get("patient")
	q := `SELECT id, patient_id, encounter_class, status, COALESCE(disposition,''),
		opened_at, closed_at FROM encounters`
	args := []any{}
	if patientID != "" {
		q += ` WHERE patient_id = $1`
		args = append(args, patientID)
	}
	q += ` ORDER BY opened_at DESC LIMIT 50`
	rows, err := s.pool.Query(r.Context(), q, args...)
	if err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	defer rows.Close()
	var resources []any
	for rows.Next() {
		var eid, pid, class, status, disposition string
		var openedAt, closedAt *time.Time
		if err := rows.Scan(&eid, &pid, &class, &status, &disposition, &openedAt, &closedAt); err != nil {
			fhir.WriteError(w, 500, "exception", err.Error())
			return
		}
		resources = append(resources, encounterResource(eid, pid, class, status, disposition, openedAt, closedAt))
	}
	if err := s.auditRead(r.Context(), actor, "Encounter", "search"); err != nil {
		fhir.WriteError(w, 500, "exception", "audit failed")
		return
	}
	fhir.WriteJSON(w, 200, fhir.NewSearchSet(resources))
}

func (s *server) addDiagnosis(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	encounterID := r.PathValue("id")
	var patientID string
	if err := s.pool.QueryRow(r.Context(),
		`SELECT patient_id FROM encounters WHERE id = $1`, encounterID).Scan(&patientID); err != nil {
		fhir.WriteError(w, 404, "not-found", "no such encounter")
		return
	}
	var diag struct {
		Code  string `json:"code"`
		Display string `json:"display"`
		Type  string `json:"type"` // working | discharge | billing
		Rank  int    `json:"rank"`
	}
	if err := json.NewDecoder(limitReader(r)).Decode(&diag); err != nil {
		fhir.WriteError(w, 400, "structure", "invalid JSON: "+err.Error())
		return
	}
	if diag.Display == "" {
		fhir.WriteError(w, 400, "required", "display is required")
		return
	}
	if diag.Type == "" {
		diag.Type = "working"
	}
	id := uuid.New()
	now := time.Now().UTC().Format(time.RFC3339)
	payload, err := eventstore.Canonical(encounters.DiagnosisRecorded{
		ID: id.String(), EncounterID: encounterID, PatientID: patientID,
		Code: diag.Code, Display: diag.Display, Type: diag.Type, Rank: diag.Rank,
		RecordedAt: now,
	})
	if err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	ev := &eventstore.Event{
		Type: encounters.TypeDiagnosisRecorded, AggregateType: "Encounter",
		AggregateID: uuid.MustParse(encounterID), Payload: payload, ActorHPI: actor.HPI,
	}
	ae := audit.Entry{ActorHPI: actor.HPI, Action: "C", ResourceType: "Diagnosis", ResourceID: id.String()}
	if err := s.appendEventAndAudit(r.Context(), ev, ae); err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	fhir.WriteJSON(w, 201, map[string]any{
		"id": id.String(), "encounterId": encounterID,
		"display": diag.Display, "type": diag.Type, "status": "recorded",
	})
}

func (s *server) closeEncounter(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	encounterID := r.PathValue("id")
	var patientID string
	if err := s.pool.QueryRow(r.Context(),
		`SELECT patient_id FROM encounters WHERE id = $1`, encounterID).Scan(&patientID); err != nil {
		fhir.WriteError(w, 404, "not-found", "no such encounter")
		return
	}
	var req struct {
		Disposition string `json:"disposition"` // home | transfer | deceased
	}
	json.NewDecoder(limitReader(r)).Decode(&req)
	now := time.Now().UTC().Format(time.RFC3339)
	payload, err := eventstore.Canonical(encounters.EncounterClosed{
		EncounterID: encounterID, PatientID: patientID,
		Disposition: req.Disposition, ClosedAt: now,
	})
	if err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	ev := &eventstore.Event{
		Type: encounters.TypeEncounterClosed, AggregateType: "Encounter",
		AggregateID: uuid.MustParse(encounterID), Payload: payload, ActorHPI: actor.HPI,
	}
	ae := audit.Entry{ActorHPI: actor.HPI, Action: "C", ResourceType: "Encounter", ResourceID: encounterID}
	if err := s.appendEventAndAudit(r.Context(), ev, ae); err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	fhir.WriteJSON(w, 200, map[string]string{"encounterId": encounterID, "status": "closed"})
}

func encounterResource(id, patientID, class, status, disposition string, openedAt, closedAt *time.Time) fhir.EncounterFHIR {
	enc := fhir.EncounterFHIR{
		ID:      id,
		Status:  status,
		Class:   fhir.Coding{Code: class},
		Subject: &fhir.Reference{Reference: "Patient/" + patientID},
	}
	if openedAt != nil {
		enc.Period = &fhir.Period{Start: openedAt.Format(time.RFC3339)}
		if closedAt != nil {
			enc.Period.End = closedAt.Format(time.RFC3339)
		}
	}
	return enc
}
