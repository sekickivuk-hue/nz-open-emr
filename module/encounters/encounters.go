// Package encounters defines encounter event types and projects them.
//
// EncounterOpened → encounters table
// DiagnosisRecorded → encounter_diagnoses table
// EncounterClosed → closes encounter, promotes diagnoses to problem list
//
// Zero dependency on core beyond eventstore.Event and projection.Register.
package encounters

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/sekickivuk-hue/nz-open-emr/internal/eventstore"
	"github.com/sekickivuk-hue/nz-open-emr/internal/projection"
)

// Event type constants — owned by this module, not core.
const (
	TypeEncounterOpened   = "EncounterOpened"
	TypeDiagnosisRecorded = "DiagnosisRecorded"
	TypeEncounterClosed   = "EncounterClosed"
)

// Payload types.
type EncounterOpened struct {
	ID             string `json:"id"`
	PatientID      string `json:"patientId"`
	EncounterClass string `json:"encounterClass"`
	Facility       string `json:"facility,omitempty"`
	Ward           string `json:"ward,omitempty"`
	OpenedAt       string `json:"openedAt"`
}

type DiagnosisRecorded struct {
	ID          string `json:"id"`
	EncounterID string `json:"encounterId"`
	PatientID   string `json:"patientId"`
	Code        string `json:"code,omitempty"`
	Display     string `json:"display"`
	Type        string `json:"type"` // working | discharge | billing
	Rank        int    `json:"rank,omitempty"`
	RecordedAt  string `json:"recordedAt"`
}

type EncounterClosed struct {
	EncounterID string `json:"encounterId"`
	PatientID   string `json:"patientId"`
	Disposition string `json:"disposition,omitempty"`
	ClosedAt    string `json:"closedAt"`
}

// Projection handlers.

type encounterOpenedHandler struct{}

func (encounterOpenedHandler) EventTypes() []string { return []string{TypeEncounterOpened} }
func (encounterOpenedHandler) ApplyEvent(ctx context.Context, tx pgx.Tx, ev eventstore.Event) error {
	var e EncounterOpened
	if err := json.Unmarshal(ev.Payload, &e); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO encounters (id, patient_id, encounter_class, status, facility, ward, opened_at, last_event_seq)
		VALUES ($1,$2,$3,'in-progress',$4,$5,$6,$7)
		ON CONFLICT (id) DO NOTHING`,
		e.ID, e.PatientID, e.EncounterClass, e.Facility, e.Ward, e.OpenedAt, ev.Seq)
	return err
}

type diagnosisRecordedHandler struct{}

func (diagnosisRecordedHandler) EventTypes() []string { return []string{TypeDiagnosisRecorded} }
func (diagnosisRecordedHandler) ApplyEvent(ctx context.Context, tx pgx.Tx, ev eventstore.Event) error {
	var d DiagnosisRecorded
	if err := json.Unmarshal(ev.Payload, &d); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO encounter_diagnoses (id, encounter_id, patient_id, code, display, type, rank, recorded_at, last_event_seq)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (id) DO UPDATE SET
			code = EXCLUDED.code, display = EXCLUDED.display, type = EXCLUDED.type,
			rank = EXCLUDED.rank, last_event_seq = EXCLUDED.last_event_seq`,
		d.ID, d.EncounterID, d.PatientID, d.Code, d.Display, d.Type, d.Rank, d.RecordedAt, ev.Seq)
	return err
}

type encounterClosedHandler struct{}

func (encounterClosedHandler) EventTypes() []string { return []string{TypeEncounterClosed} }
func (encounterClosedHandler) ApplyEvent(ctx context.Context, tx pgx.Tx, ev eventstore.Event) error {
	var e EncounterClosed
	if err := json.Unmarshal(ev.Payload, &e); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE encounters SET status = 'finished', disposition = $1, closed_at = $2, last_event_seq = $3
		WHERE id = $4`,
		e.Disposition, e.ClosedAt, ev.Seq, e.EncounterID); err != nil {
		return err
	}
	// Promote discharge/billing diagnoses to the problem list.
	// Query the EVENT STORE (not the projection) so this works even
	// before the diagnosis projection has caught up.
	rows, err := tx.Query(ctx, `
		SELECT payload_json->>'id',
		       payload_json->>'patientId',
		       COALESCE(payload_json->>'code',''),
		       payload_json->>'display',
		       payload_json->>'type',
		       payload_json->>'recordedAt'
		FROM events
		WHERE aggregate_type = 'Encounter'
		  AND aggregate_id = $1
		  AND event_type = 'DiagnosisRecorded'
		  AND payload_json->>'type' IN ('discharge','billing')
		ORDER BY seq`, e.EncounterID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type diagRow struct {
		id, patientID, code, display, diagType, recordedAt string
	}
	var diags []diagRow
	for rows.Next() {
		var d diagRow
		if err := rows.Scan(&d.id, &d.patientID, &d.code, &d.display, &d.diagType, &d.recordedAt); err != nil {
			return err
		}
		diags = append(diags, d)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	for _, d := range diags {
		category := "medical"
		if containsSurgical(d.display) {
			category = "surgical"
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO problems (id, patient_id, code, display, category, status, onset_date, last_event_seq)
			VALUES ($1,$2,$3,$4,$5,'active',$6::date,$7)
			ON CONFLICT (id) DO NOTHING`,
			d.id, d.patientID, d.code, d.display, category, d.recordedAt, ev.Seq); err != nil {
			return err
		}
	}
	return nil
}

func containsSurgical(display string) bool {
	for _, w := range []string{"appendectomy", "appendicectomy", "cholecystectomy", "surgery",
		"repair", "excision", "resection", "bypass", "graft", "amputation", "transplant",
		"replacement", "fusion", "fixation", "reconstruction", "debridement", "laparotomy"} {
		if containsStr(toLower(display), w) {
			return true
		}
	}
	return false
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		b[i] = c
	}
	return string(b)
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func init() {
	projection.Register(encounterOpenedHandler{})
	projection.Register(diagnosisRecordedHandler{})
	projection.Register(encounterClosedHandler{})
}
