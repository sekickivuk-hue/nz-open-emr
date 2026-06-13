package audit_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/sekickivuk-hue/nz-open-emr/internal/audit"
	"github.com/sekickivuk-hue/nz-open-emr/internal/testutil"
)

func TestChainHashDeterministic(t *testing.T) {
	prev := make([]byte, 32)
	h1 := audit.ChainHash(prev, 1, "99ZZZA", "C", "Patient", "x", "2026-06-13T00:00:00.000000Z", "")
	h2 := audit.ChainHash(prev, 1, "99ZZZA", "C", "Patient", "x", "2026-06-13T00:00:00.000000Z", "")
	if !bytes.Equal(h1, h2) {
		t.Fatal("hash not deterministic")
	}
	h3 := audit.ChainHash(prev, 1, "99ZZZA", "R", "Patient", "x", "2026-06-13T00:00:00.000000Z", "")
	if bytes.Equal(h1, h3) {
		t.Fatal("different action must change hash")
	}
	if len(h1) != 32 {
		t.Fatalf("hash length %d, want 32", len(h1))
	}
}

func TestAppendAndVerify(t *testing.T) {
	pool := testutil.RequireDB(t)
	ctx := context.Background()

	for i, action := range []string{"C", "R", "R"} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		e, err := audit.Append(ctx, tx, audit.Entry{
			ActorHPI: "99ZZZA", Action: action,
			ResourceType: "Patient", ResourceID: "p1",
		})
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		if e.Seq != int64(i+1) {
			t.Fatalf("seq = %d, want %d", e.Seq, i+1)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}

	rep, err := audit.Verify(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK || rep.Checked != 3 {
		t.Fatalf("verify: %+v", rep)
	}
}

func TestVerifyDetectsTampering(t *testing.T) {
	pool := testutil.RequireDB(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		tx, _ := pool.Begin(ctx)
		if _, err := audit.Append(ctx, tx, audit.Entry{
			ActorHPI: "99ZZZA", Action: "C",
			ResourceType: "Patient", ResourceID: "p1",
		}); err != nil {
			t.Fatal(err)
		}
		tx.Commit(ctx)
	}
	// A malicious DBA edits history.
	if _, err := pool.Exec(ctx,
		`UPDATE audit_events SET actor_hpi = 'EVIL' WHERE seq = 2`); err != nil {
		t.Fatal(err)
	}
	rep, err := audit.Verify(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK || rep.BrokenSeq != 2 {
		t.Fatalf("tampering not pinpointed: %+v", rep)
	}
}
