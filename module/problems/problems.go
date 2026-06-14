// Package problems defines problem list event types and projects them.
//
// ProblemAdded → problems table (active)
// ProblemResolved → marks resolved, copies into past_history
//
// Zero dependency on core beyond eventstore.Event and projection.Register.
package problems

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/sekickivuk-hue/nz-open-emr/internal/eventstore"
	"github.com/sekickivuk-hue/nz-open-emr/internal/projection"
)

const (
	TypeProblemAdded    = "ProblemAdded"
	TypeProblemResolved = "ProblemResolved"
)

type ProblemAdded struct {
	ID         string `json:"id"`
	PatientID  string `json:"patientId"`
	Code       string `json:"code,omitempty"`
	Display    string `json:"display"`
	Category   string `json:"category,omitempty"`
	OnsetDate  string `json:"onsetDate,omitempty"`
	RecordedAt string `json:"recordedAt"`
}

type ProblemResolved struct {
	ProblemID  string `json:"problemId"`
	PatientID  string `json:"patientId"`
	ResolvedAt string `json:"resolvedAt"`
}

type problemAddedHandler struct{}

func (problemAddedHandler) EventTypes() []string { return []string{TypeProblemAdded} }
func (problemAddedHandler) ApplyEvent(ctx context.Context, tx pgx.Tx, ev eventstore.Event) error {
	var p ProblemAdded
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO problems (id, patient_id, code, display, category, status, onset_date, last_event_seq)
		VALUES ($1,$2,$3,$4,$5,'active',$6::date,$7)
		ON CONFLICT (id) DO UPDATE SET
			display = EXCLUDED.display, category = EXCLUDED.category,
			status = 'active', onset_date = EXCLUDED.onset_date,
			last_event_seq = EXCLUDED.last_event_seq`,
		p.ID, p.PatientID, p.Code, p.Display, p.Category, p.OnsetDate, ev.Seq)
	return err
}

type problemResolvedHandler struct{}

func (problemResolvedHandler) EventTypes() []string { return []string{TypeProblemResolved} }
func (problemResolvedHandler) ApplyEvent(ctx context.Context, tx pgx.Tx, ev eventstore.Event) error {
	var p ProblemResolved
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE problems SET status = 'resolved', resolved_at = $1, last_event_seq = $2
		WHERE id = $3 AND patient_id = $4`,
		p.ResolvedAt, ev.Seq, p.ProblemID, p.PatientID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO past_history (problem_id, patient_id, category, display, onset_date, resolved_at)
		SELECT id, patient_id, COALESCE(category,'medical'), display, onset_date, resolved_at
		FROM problems WHERE id = $1
		ON CONFLICT (problem_id) DO NOTHING`, p.ProblemID); err != nil {
		return err
	}
	return nil
}

func init() {
	projection.Register(problemAddedHandler{})
	projection.Register(problemResolvedHandler{})
}
