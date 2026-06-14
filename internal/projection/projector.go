// Package projection derives read models from the event log.
// Modules register projection handlers for event types they own;
// the projector dispatches events to all matching handlers.
package projection

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sekickivuk-hue/nz-open-emr/internal/eventstore"
)

// Handler processes one event type inside a projection transaction.
// A single event can have multiple handlers (e.g. an EncounterClosed
// event might update the encounter table AND the problem list).
type Handler interface {
	// EventTypes returns the event type strings this handler processes.
	EventTypes() []string
	// ApplyEvent applies one event inside tx. The projector has already
	// verified the event type matches; the handler should not re-check.
	ApplyEvent(ctx context.Context, tx pgx.Tx, ev eventstore.Event) error
}

var (
	mu       sync.RWMutex
	registry = map[string][]Handler{}
)

// Register adds h to the dispatch table for each type it claims.
// Safe to call from init() in any package.
func Register(h Handler) {
	mu.Lock()
	defer mu.Unlock()
	for _, t := range h.EventTypes() {
		registry[t] = append(registry[t], h)
	}
}

func handlersFor(eventType string) []Handler {
	mu.RLock()
	defer mu.RUnlock()
	return registry[eventType]
}

// Projector polls the event log and updates all registered projections.
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

// Step drains all outstanding events. Exported for deterministic tests.
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
			if err := dispatch(ctx, tx, ev); err != nil {
				tx.Rollback(ctx)
				return fmt.Errorf("dispatch seq %d (%s): %w", ev.Seq, ev.Type, err)
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

func dispatch(ctx context.Context, tx pgx.Tx, ev eventstore.Event) error {
	handlers := handlersFor(ev.Type)
	if len(handlers) == 0 {
		return nil // unknown event type — forward compatible
	}
	for _, h := range handlers {
		if err := h.ApplyEvent(ctx, tx, ev); err != nil {
			return err
		}
	}
	return nil
}

// --- built-in handlers --------------------------------------------------

// patientHandler projects PatientRegistered events into the patients table.
type patientHandler struct{}

func (patientHandler) EventTypes() []string { return []string{eventstore.TypePatientRegistered} }
func (patientHandler) ApplyEvent(ctx context.Context, tx pgx.Tx, ev eventstore.Event) error {
	var p eventstore.PatientRegistered
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		return err
	}
	var birth *string
	if p.BirthDate != "" {
		birth = &p.BirthDate
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO patients (id, nhi, nhi_format, family_name, given_name, birth_date, gender,
			ethnicity_codes, nz_citizenship, citizenship_src, birth_country, birth_place, dhb_code, iwi_codes, last_event_seq)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT (id) DO UPDATE SET
			nhi = EXCLUDED.nhi, nhi_format = EXCLUDED.nhi_format,
			family_name = EXCLUDED.family_name, given_name = EXCLUDED.given_name,
			birth_date = EXCLUDED.birth_date, gender = EXCLUDED.gender,
			ethnicity_codes = EXCLUDED.ethnicity_codes, nz_citizenship = EXCLUDED.nz_citizenship,
			citizenship_src = EXCLUDED.citizenship_src, birth_country = EXCLUDED.birth_country,
			birth_place = EXCLUDED.birth_place, dhb_code = EXCLUDED.dhb_code,
			iwi_codes = EXCLUDED.iwi_codes,
			last_event_seq = EXCLUDED.last_event_seq`,
		p.ID, p.NHI, p.NHIFormat, p.FamilyName, p.GivenName, birth, p.Gender,
		p.EthnicityCodes, p.NZCitizenship, p.CitizenshipSrc, p.BirthCountry, p.BirthPlace, p.DHBCode, p.IwiCodes, ev.Seq)
	return err
}

// noteHandler projects NoteCreated events into the notes table.
type noteHandler struct{}

func (noteHandler) EventTypes() []string { return []string{eventstore.TypeNoteCreated} }
func (noteHandler) ApplyEvent(ctx context.Context, tx pgx.Tx, ev eventstore.Event) error {
	var n eventstore.NoteCreated
	if err := json.Unmarshal(ev.Payload, &n); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO notes (id, patient_id, author_hpi, text, created_at, last_event_seq)
		VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`,
		n.ID, n.PatientID, n.AuthorHPI, n.Text, n.CreatedAt, ev.Seq)
	return err
}

func init() {
	Register(patientHandler{})
	Register(noteHandler{})
}
