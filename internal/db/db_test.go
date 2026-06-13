package db_test

import (
	"context"
	"testing"

	"github.com/sekickivuk-hue/nz-open-emr/internal/db"
	"github.com/sekickivuk-hue/nz-open-emr/internal/testutil"
)

func TestMigrateIsIdempotent(t *testing.T) {
	pool := testutil.RequireDB(t)
	// RequireDB already migrated once; a second run must not error.
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	var n int
	err := pool.QueryRow(context.Background(),
		`SELECT last_seq FROM projection_state WHERE id = 'main'`).Scan(&n)
	if err != nil || n != 0 {
		t.Fatalf("projection_state seed: n=%d err=%v", n, err)
	}
}
