// Package eventstore owns the append-only events table — the source of
// truth. Projections are derived and disposable; this table is not.
//
// This package defines ONLY the generic event envelope and persistence
// layer plus the core identity event types (PatientRegistered, NoteCreated).
// Module-specific event types and payloads live in their module packages.
package eventstore

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// --- core event type constants --------------------------------------------

const (
	TypePatientRegistered = "PatientRegistered"
	TypeNoteCreated       = "NoteCreated"
)

// --- event envelope -------------------------------------------------------

type Event struct {
	Seq           int64
	Type          string
	AggregateType string
	AggregateID   uuid.UUID
	Payload       []byte // canonical json/v1 bytes
	ActorHPI      string
	At            time.Time
}

// --- core payload types ---------------------------------------------------

// PatientRegistered is the json/v1 payload for a new patient.
// Core identity per HISO 10046:2025 §2: NHI, name, DOB, gender,
// ethnicity, NZ citizenship, birthplace, iwi.
// Everything else (allergies, medications, history) is a module
// around this base.
type PatientRegistered struct {
	ID               string   `json:"id"`
	NHI              string   `json:"nhi"`
	NHIFormat        string   `json:"nhiFormat"`
	FamilyName       string   `json:"familyName"`
	GivenName        string   `json:"givenName"`
	BirthDate        string   `json:"birthDate,omitempty"`
	Gender           string   `json:"gender,omitempty"`
	EthnicityCodes   []string `json:"ethnicityCodes,omitempty"`   // level-4 codes (up to 6)
	NZCitizenship    string   `json:"nzCitizenship,omitempty"`    // status code
	CitizenshipSrc   string   `json:"citizenshipSource,omitempty"` // information source
	BirthCountry     string   `json:"birthCountry,omitempty"`     // ISO-3166 2-letter
	BirthPlace       string   `json:"birthPlace,omitempty"`       // free-text city/town
	DHBCode          string   `json:"dhbCode,omitempty"`          // HPI-ORG DHB code
	IwiCodes         []string `json:"iwiCodes,omitempty"`         // Stats NZ iwi codes
}

// NoteCreated is the json/v1 payload for a clinical note.
type NoteCreated struct {
	ID        string `json:"id"`
	PatientID string `json:"patientId"`
	AuthorHPI string `json:"authorHpi"`
	Text      string `json:"text"`
	CreatedAt string `json:"createdAt"`
}

// --- persistence ----------------------------------------------------------

// Canonical produces exact bytes persisted and replayed.
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
