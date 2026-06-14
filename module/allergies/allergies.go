// Package allergies defines allergy event types and projects them.
//
// AllergyAdded → allergies table
// AllergyRemoved → marks allergy as inactive
//
// Adding this module required:
//   1. Create this file
//   2. Add "CREATE TABLE allergies" to schema.sql
//   3. Import in cmd/emrd/main.go
// That's it. Zero changes to patients, eventstore, projector, or API.
package allergies

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/sekickivuk-hue/nz-open-emr/internal/eventstore"
	"github.com/sekickivuk-hue/nz-open-emr/internal/projection"
)

const (
	TypeAllergyAdded   = "AllergyAdded"
	TypeAllergyRemoved = "AllergyRemoved"
)

type AllergyAdded struct {
	ID           string `json:"id"`
	PatientID    string `json:"patientId"`
	Substance    string `json:"substance"`              // e.g. "Penicillin", "Peanuts"
	Reaction     string `json:"reaction,omitempty"`     // e.g. "Anaphylaxis", "Rash"
	Severity     string `json:"severity,omitempty"`     // mild | moderate | severe | life-threatening
	RecordedByHPI string `json:"recordedByHpi"`
	RecordedAt   string `json:"recordedAt"`
}

type AllergyRemoved struct {
	AllergyID  string `json:"allergyId"`
	PatientID  string `json:"patientId"`
	RemovedByHPI string `json:"removedByHpi"`
	RemovedAt  string `json:"removedAt"`
	Reason     string `json:"reason,omitempty"` // e.g. "retracted", "resolved"
}

type allergyAddedHandler struct{}

func (allergyAddedHandler) EventTypes() []string { return []string{TypeAllergyAdded} }
func (allergyAddedHandler) ApplyEvent(ctx context.Context, tx pgx.Tx, ev eventstore.Event) error {
	var a AllergyAdded
	if err := json.Unmarshal(ev.Payload, &a); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO allergies (id, patient_id, substance, reaction, severity, status, recorded_by_hpi, recorded_at, last_event_seq)
		VALUES ($1,$2,$3,$4,$5,'active',$6,$7,$8)
		ON CONFLICT (id) DO UPDATE SET
			substance = EXCLUDED.substance, reaction = EXCLUDED.reaction,
			severity = EXCLUDED.severity, status = 'active',
			last_event_seq = EXCLUDED.last_event_seq`,
		a.ID, a.PatientID, a.Substance, a.Reaction, a.Severity, a.RecordedByHPI, a.RecordedAt, ev.Seq)
	return err
}

type allergyRemovedHandler struct{}

func (allergyRemovedHandler) EventTypes() []string { return []string{TypeAllergyRemoved} }
func (allergyRemovedHandler) ApplyEvent(ctx context.Context, tx pgx.Tx, ev eventstore.Event) error {
	var a AllergyRemoved
	if err := json.Unmarshal(ev.Payload, &a); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		UPDATE allergies SET status = 'inactive', removed_at = $1, removed_by_hpi = $2, reason = $3, last_event_seq = $4
		WHERE id = $5 AND patient_id = $6`,
		a.RemovedAt, a.RemovedByHPI, a.Reason, ev.Seq, a.AllergyID, a.PatientID)
	return err
}

func init() {
	projection.Register(allergyAddedHandler{})
	projection.Register(allergyRemovedHandler{})
}
