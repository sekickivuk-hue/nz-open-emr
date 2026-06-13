package eventstore_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/sekickivuk-hue/nz-open-emr/internal/eventstore"
	"github.com/sekickivuk-hue/nz-open-emr/internal/testutil"
)

func TestAppendAndListAfter(t *testing.T) {
	pool := testutil.RequireDB(t)
	ctx := context.Background()

	pid := uuid.New()
	payload, err := eventstore.Canonical(eventstore.PatientRegistered{
		ID: pid.String(), NHI: "ZZZ0016", NHIFormat: "legacy",
		FamilyName: "Demo", GivenName: "Pat", BirthDate: "1980-01-01",
	})
	if err != nil {
		t.Fatal(err)
	}

	tx, _ := pool.Begin(ctx)
	ev := &eventstore.Event{
		Type: eventstore.TypePatientRegistered, AggregateType: "Patient",
		AggregateID: pid, Payload: payload, ActorHPI: "99ZZZA",
	}
	if err := eventstore.Append(ctx, tx, ev); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if ev.Seq != 1 {
		t.Fatalf("seq = %d, want 1", ev.Seq)
	}

	got, err := eventstore.ListAfter(ctx, pool, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Type != eventstore.TypePatientRegistered ||
		string(got[0].Payload) != string(payload) {
		t.Fatalf("ListAfter: %+v", got)
	}
	if more, _ := eventstore.ListAfter(ctx, pool, 1, 10); len(more) != 0 {
		t.Fatalf("ListAfter(1) returned %d events, want 0", len(more))
	}
}
