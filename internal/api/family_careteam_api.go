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
	"github.com/sekickivuk-hue/nz-open-emr/module/careteam"
	"github.com/sekickivuk-hue/nz-open-emr/module/family"
)

func (s *server) registerFamilyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /fhir/r4/RelatedPerson", s.createFamilyConnection)
	mux.HandleFunc("GET /fhir/r4/RelatedPerson", s.listFamilyConnections)
	mux.HandleFunc("POST /fhir/r4/RelatedPerson/{id}/remove", s.removeFamilyConnection)
}

func (s *server) registerCareTeamRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /fhir/r4/CareTeam", s.createCareTeam)
	mux.HandleFunc("GET /fhir/r4/CareTeam", s.listCareTeam)
	mux.HandleFunc("POST /fhir/r4/CareTeam/{id}/remove", s.removeCareTeamMember)
}

func (s *server) createFamilyConnection(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	var req struct {
		PatientID    string `json:"patientId"`
		RelativeID   string `json:"relativeId"`
		Relationship string `json:"relationship"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fhir.WriteError(w, 400, "structure", "invalid JSON: "+err.Error())
		return
	}
	if req.PatientID == "" || req.RelativeID == "" || req.Relationship == "" {
		fhir.WriteError(w, 400, "required", "patientId, relativeId, and relationship are required")
		return
	}
	id := uuid.New()
	now := time.Now().UTC().Format(time.RFC3339)
	payload, err := eventstore.Canonical(family.FamilyConnectionAdded{
		ID: id.String(), PatientID: req.PatientID, RelativeID: req.RelativeID,
		Relationship: req.Relationship, RecordedAt: now,
	})
	if err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	ev := &eventstore.Event{
		Type: family.TypeFamilyConnectionAdded, AggregateType: "RelatedPerson",
		AggregateID: id, Payload: payload, ActorHPI: actor.HPI,
	}
	ae := audit.Entry{ActorHPI: actor.HPI, Action: "C", ResourceType: "RelatedPerson", ResourceID: id.String()}
	if err := s.appendEventAndAudit(r.Context(), ev, ae); err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	fhir.WriteJSON(w, 201, map[string]string{
		"id": id.String(), "patientId": req.PatientID,
		"relativeId": req.RelativeID, "relationship": req.Relationship,
	})
}

func (s *server) listFamilyConnections(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	patientID := r.URL.Query().Get("patient")
	if patientID == "" {
		fhir.WriteError(w, 400, "required", "patient query parameter is required")
		return
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, patient_id, relative_id, relationship, recorded_at
		FROM family_connections
		WHERE (patient_id = $1 OR relative_id = $1) AND removed_at IS NULL
		ORDER BY recorded_at`, patientID)
	if err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	defer rows.Close()
	var items []map[string]string
	for rows.Next() {
		var id, pid, rid, rel string
		var recAt time.Time
		if err := rows.Scan(&id, &pid, &rid, &rel, &recAt); err != nil {
			fhir.WriteError(w, 500, "exception", err.Error())
			return
		}
		items = append(items, map[string]string{
			"id": id, "patientId": pid, "relativeId": rid,
			"relationship": rel, "recordedAt": recAt.Format(time.RFC3339),
		})
	}
	if err := s.auditRead(r.Context(), actor, "RelatedPerson", "search"); err != nil {
		fhir.WriteError(w, 500, "exception", "audit failed")
		return
	}
	fhir.WriteJSON(w, 200, items)
}

func (s *server) removeFamilyConnection(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	id := r.PathValue("id")
	var pid, rid string
	if err := s.pool.QueryRow(r.Context(),
		`SELECT patient_id, relative_id FROM family_connections WHERE id = $1`, id).
		Scan(&pid, &rid); err != nil {
		fhir.WriteError(w, 404, "not-found", "no such connection")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	payload, err := eventstore.Canonical(family.FamilyConnectionRemoved{
		ConnectionID: id, PatientID: pid, RelativeID: rid, RemovedAt: now,
	})
	if err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	ev := &eventstore.Event{
		Type: family.TypeFamilyConnectionRemoved, AggregateType: "RelatedPerson",
		AggregateID: uuid.MustParse(id), Payload: payload, ActorHPI: actor.HPI,
	}
	ae := audit.Entry{ActorHPI: actor.HPI, Action: "C", ResourceType: "RelatedPerson", ResourceID: id}
	if err := s.appendEventAndAudit(r.Context(), ev, ae); err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	fhir.WriteJSON(w, 200, map[string]string{"connectionId": id, "status": "removed"})
}

func (s *server) createCareTeam(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	var req struct {
		PatientID    string `json:"patientId"`
		ClinicianHPI string `json:"clinicianHpi"`
		Role         string `json:"role"`
		StartDate    string `json:"startDate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fhir.WriteError(w, 400, "structure", "invalid JSON: "+err.Error())
		return
	}
	if req.PatientID == "" || req.ClinicianHPI == "" || req.Role == "" {
		fhir.WriteError(w, 400, "required", "patientId, clinicianHpi, and role are required")
		return
	}
	if req.StartDate == "" {
		req.StartDate = time.Now().UTC().Format("2006-01-02")
	}
	id := uuid.New()
	payload, err := eventstore.Canonical(careteam.CareTeamMemberAdded{
		ID: id.String(), PatientID: req.PatientID, ClinicianHPI: req.ClinicianHPI,
		Role: req.Role, StartDate: req.StartDate,
	})
	if err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	ev := &eventstore.Event{
		Type: careteam.TypeCareTeamMemberAdded, AggregateType: "CareTeam",
		AggregateID: id, Payload: payload, ActorHPI: actor.HPI,
	}
	ae := audit.Entry{ActorHPI: actor.HPI, Action: "C", ResourceType: "CareTeam", ResourceID: id.String()}
	if err := s.appendEventAndAudit(r.Context(), ev, ae); err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	fhir.WriteJSON(w, 201, map[string]string{
		"id": id.String(), "patientId": req.PatientID,
		"clinicianHpi": req.ClinicianHPI, "role": req.Role,
	})
}

func (s *server) listCareTeam(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	patientID := r.URL.Query().Get("patient")
	if patientID == "" {
		fhir.WriteError(w, 400, "required", "patient query parameter is required")
		return
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, clinician_hpi, role, start_date, end_date
		FROM care_team
		WHERE patient_id = $1
		ORDER BY start_date DESC`, patientID)
	if err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id, hpi, role string
		var startDate time.Time
		var endDate *time.Time
		if err := rows.Scan(&id, &hpi, &role, &startDate, &endDate); err != nil {
			fhir.WriteError(w, 500, "exception", err.Error())
			return
		}
		item := map[string]any{
			"id": id, "clinicianHpi": hpi, "role": role,
			"startDate": startDate.Format("2006-01-02"), "active": endDate == nil,
		}
		if endDate != nil {
			item["endDate"] = endDate.Format("2006-01-02")
		}
		items = append(items, item)
	}
	if err := s.auditRead(r.Context(), actor, "CareTeam", "search"); err != nil {
		fhir.WriteError(w, 500, "exception", "audit failed")
		return
	}
	fhir.WriteJSON(w, 200, items)
}

func (s *server) removeCareTeamMember(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	id := r.PathValue("id")
	var patientID, clinicianHPI string
	if err := s.pool.QueryRow(r.Context(),
		`SELECT patient_id, clinician_hpi FROM care_team WHERE id = $1`, id).
		Scan(&patientID, &clinicianHPI); err != nil {
		fhir.WriteError(w, 404, "not-found", "no such team member")
		return
	}
	now := time.Now().UTC().Format("2006-01-02")
	payload, err := eventstore.Canonical(careteam.CareTeamMemberRemoved{
		MembershipID: id, PatientID: patientID, ClinicianHPI: clinicianHPI, EndDate: now,
	})
	if err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	ev := &eventstore.Event{
		Type: careteam.TypeCareTeamMemberRemoved, AggregateType: "CareTeam",
		AggregateID: uuid.MustParse(id), Payload: payload, ActorHPI: actor.HPI,
	}
	ae := audit.Entry{ActorHPI: actor.HPI, Action: "C", ResourceType: "CareTeam", ResourceID: id}
	if err := s.appendEventAndAudit(r.Context(), ev, ae); err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	fhir.WriteJSON(w, 200, map[string]string{"membershipId": id, "status": "ended"})
}
