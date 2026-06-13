package projection_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/sekickivuk-hue/nz-open-emr/internal/eventstore"
	"github.com/sekickivuk-hue/nz-open-emr/internal/projection"
	"github.com/sekickivuk-hue/nz-open-emr/internal/testutil"
)

func TestStepProjectsPatientAndNote(t *testing.T) {
	pool := testutil.RequireDB(t)
	ctx := context.Background()
	pid, nid := uuid.New(), uuid.New()

	appendEvent := func(typ, aggType string, aggID uuid.UUID, payload []byte) {
		t.Helper()
		tx, _ := pool.Begin(ctx)
		ev := &eventstore.Event{Type: typ, AggregateType: aggType,
			AggregateID: aggID, Payload: payload, ActorHPI: "99ZZZA"}
		if err := eventstore.Append(ctx, tx, ev); err != nil {
			t.Fatal(err)
		}
		tx.Commit(ctx)
	}

	pp, _ := eventstore.Canonical(eventstore.PatientRegistered{
		ID: pid.String(), NHI: "ZZZ0016", NHIFormat: "legacy",
		FamilyName: "Demo", GivenName: "Pat", BirthDate: "1980-01-01"})
	np, _ := eventstore.Canonical(eventstore.NoteCreated{
		ID: nid.String(), PatientID: pid.String(), AuthorHPI: "99ZZZA",
		Text: "kia ora", CreatedAt: "2026-06-13T00:00:00Z"})
	appendEvent(eventstore.TypePatientRegistered, "Patient", pid, pp)
	appendEvent(eventstore.TypeNoteCreated, "Note", nid, np)

	p := &projection.Projector{Pool: pool}
	if err := p.Step(ctx); err != nil {
		t.Fatal(err)
	}

	var family, noteText string
	if err := pool.QueryRow(ctx,
		`SELECT family_name FROM patients WHERE id = $1`, pid).Scan(&family); err != nil {
		t.Fatalf("patient not projected: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT text FROM notes WHERE id = $1`, nid).Scan(&noteText); err != nil {
		t.Fatalf("note not projected: %v", err)
	}
	if family != "Demo" || noteText != "kia ora" {
		t.Fatalf("projected wrong data: %q %q", family, noteText)
	}

	// Step must be idempotent.
	if err := p.Step(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM patients`).Scan(&n)
	if n != 1 {
		t.Fatalf("patients = %d after re-step, want 1", n)
	}
}
