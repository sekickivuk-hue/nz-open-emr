// Package projection derives read models (patients, notes) from the
// event log. Unknown event types are skipped — forward compatibility.
package projection

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sekickivuk-hue/nz-open-emr/internal/eventstore"
)

type Projector struct {
	Pool     *pgxpool.Pool
	Interval time.Duration // default 200ms
	Log      *slog.Logger
}

// Run polls until ctx is cancelled.
func (p *Projector) Run(ctx context.Context) {
	iv := p.Interval
	if iv == 0 {
		iv = 200 * time.Millisecond
	}
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := p.Step(ctx); err != nil && p.Log != nil {
				p.Log.Error("projection step failed; events are safe, will retry", "err", err)
			}
		}
	}
}

// Step drains all outstanding events. Exported so tests and callers can
// project deterministically.
func (p *Projector) Step(ctx context.Context) error {
	for {
		var last int64
		if err := p.Pool.QueryRow(ctx,
			`SELECT last_seq FROM projection_state WHERE id = 'main'`).Scan(&last); err != nil {
			return err
		}
		events, err := eventstore.ListAfter(ctx, p.Pool, last, 100)
		if err != nil {
			return err
		}
		if len(events) == 0 {
			return nil
		}
		tx, err := p.Pool.Begin(ctx)
		if err != nil {
			return err
		}
		for _, ev := range events {
			if err := apply(ctx, tx, ev); err != nil {
				tx.Rollback(ctx)
				return fmt.Errorf("apply seq %d (%s): %w", ev.Seq, ev.Type, err)
			}
			last = ev.Seq
		}
		if _, err := tx.Exec(ctx,
			`UPDATE projection_state SET last_seq = $1 WHERE id = 'main'`, last); err != nil {
			tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
}

func apply(ctx context.Context, tx pgx.Tx, ev eventstore.Event) error {
	switch ev.Type {
	case eventstore.TypePatientRegistered:
		var p eventstore.PatientRegistered
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return err
		}
		var birth *string
		if p.BirthDate != "" {
			birth = &p.BirthDate
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO patients (id, nhi, nhi_format, family_name, given_name, birth_date, last_event_seq)
			VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`,
			p.ID, p.NHI, p.NHIFormat, p.FamilyName, p.GivenName, birth, ev.Seq)
		return err
	case eventstore.TypeNoteCreated:
		var n eventstore.NoteCreated
		if err := json.Unmarshal(ev.Payload, &n); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO notes (id, patient_id, author_hpi, text, created_at, last_event_seq)
			VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`,
			n.ID, n.PatientID, n.AuthorHPI, n.Text, n.CreatedAt, ev.Seq)
		return err
	default:
		return nil // unknown events are future events; skip
	}
}
