// Package family defines family connection event types and projects them.
//
// Zero dependency on core beyond eventstore.Event and projection.Register.
package family

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/sekickivuk-hue/nz-open-emr/internal/eventstore"
	"github.com/sekickivuk-hue/nz-open-emr/internal/projection"
)

const (
	TypeFamilyConnectionAdded   = "FamilyConnectionAdded"
	TypeFamilyConnectionRemoved = "FamilyConnectionRemoved"
)

type FamilyConnectionAdded struct {
	ID           string `json:"id"`
	PatientID    string `json:"patientId"`
	RelativeID   string `json:"relativeId"`
	Relationship string `json:"relationship"`
	RecordedAt   string `json:"recordedAt"`
}

type FamilyConnectionRemoved struct {
	ConnectionID string `json:"connectionId"`
	PatientID    string `json:"patientId"`
	RelativeID   string `json:"relativeId"`
	RemovedAt    string `json:"removedAt"`
}

type connectionAddedHandler struct{}

func (connectionAddedHandler) EventTypes() []string { return []string{TypeFamilyConnectionAdded} }
func (connectionAddedHandler) ApplyEvent(ctx context.Context, tx pgx.Tx, ev eventstore.Event) error {
	var f FamilyConnectionAdded
	if err := json.Unmarshal(ev.Payload, &f); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO family_connections (id, patient_id, relative_id, relationship, recorded_at, last_event_seq)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (id) DO UPDATE SET
			relationship = EXCLUDED.relationship, removed_at = NULL,
			last_event_seq = EXCLUDED.last_event_seq`,
		f.ID, f.PatientID, f.RelativeID, f.Relationship, f.RecordedAt, ev.Seq)
	return err
}

type connectionRemovedHandler struct{}

func (connectionRemovedHandler) EventTypes() []string { return []string{TypeFamilyConnectionRemoved} }
func (connectionRemovedHandler) ApplyEvent(ctx context.Context, tx pgx.Tx, ev eventstore.Event) error {
	var f FamilyConnectionRemoved
	if err := json.Unmarshal(ev.Payload, &f); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		UPDATE family_connections SET removed_at = $1, last_event_seq = $2
		WHERE (patient_id = $3 AND relative_id = $4)
		   OR (patient_id = $4 AND relative_id = $3)`,
		f.RemovedAt, ev.Seq, f.PatientID, f.RelativeID)
	return err
}

func init() {
	projection.Register(connectionAddedHandler{})
	projection.Register(connectionRemovedHandler{})
}
