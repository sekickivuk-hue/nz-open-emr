// Package eventstore owns the append-only events table — the source of
// truth. Projections are derived and disposable; this table is not.
package eventstore

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	TypePatientRegistered = "PatientRegistered"
	TypeNoteCreated       = "NoteCreated"
)

type Event struct {
	Seq           int64
	Type          string
	AggregateType string
	AggregateID   uuid.UUID
	Payload       []byte // canonical json/v1 bytes
	ActorHPI      string
	At            time.Time
}

// PatientRegistered is the json/v1 payload for a new patient.
// Field order is canonical; do not reorder.
type PatientRegistered struct {
	ID         string `json:"id"`
	NHI        string `json:"nhi"`
	NHIFormat  string `json:"nhiFormat"`
	FamilyName string `json:"familyName"`
	GivenName  string `json:"givenName"`
	BirthDate  string `json:"birthDate,omitempty"`
}

// NoteCreated is the json/v1 payload for a clinical note.
type NoteCreated struct {
	ID        string `json:"id"`
	PatientID string `json:"patientId"`
	AuthorHPI string `json:"authorHpi"`
	Text      string `json:"text"`
	CreatedAt string `json:"createdAt"`
}

// Canonical produces the exact bytes that are persisted and replayed.
// encoding/json with fixed struct field order is deterministic.
func Canonical(v any) ([]byte, error) { return json.Marshal(v) }

func Append(ctx context.Context, tx pgx.Tx, ev *Event) error {
	ev.At = time.Now().UTC().Truncate(time.Microsecond)
	return tx.QueryRow(ctx, `
		INSERT INTO events
		  (event_type, aggregate_type, aggregate_id, payload, payload_json, actor_hpi, at)
		VALUES ($1,$2,$3,$4, convert_from($4,'UTF8')::jsonb, $5,$6)
		RETURNING seq`,
		ev.Type, ev.AggregateType, ev.AggregateID, ev.Payload, ev.ActorHPI, ev.At).
		Scan(&ev.Seq)
}

func ListAfter(ctx context.Context, pool *pgxpool.Pool, after int64, limit int) ([]Event, error) {
	rows, err := pool.Query(ctx, `
		SELECT seq, event_type, aggregate_type, aggregate_id, payload, actor_hpi, at
		FROM events WHERE seq > $1 ORDER BY seq LIMIT $2`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.Seq, &e.Type, &e.AggregateType, &e.AggregateID,
			&e.Payload, &e.ActorHPI, &e.At); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
