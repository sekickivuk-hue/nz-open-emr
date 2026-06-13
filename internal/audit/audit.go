// Package audit implements the tamper-evident audit log: every entry is
// hash-chained to its predecessor with BLAKE3, so any retroactive edit
// of a row breaks verification at exactly that row.
package audit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"lukechampine.com/blake3"
)

// TimeLayout fixes microsecond precision: Postgres timestamptz stores
// microseconds exactly, so the hashed string round-trips.
const TimeLayout = "2006-01-02T15:04:05.000000Z"

// chainLockKey serialises appends; the chain is inherently sequential.
const chainLockKey = 730_001

var genesis = make([]byte, 32)

type Entry struct {
	Seq          int64
	PrevHash     []byte
	Hash         []byte
	ActorHPI     string
	Action       string // "C" create, "R" read
	ResourceType string
	ResourceID   string
	At           time.Time
	Detail       string
}

func ChainHash(prev []byte, seq int64, actorHPI, action, resourceType, resourceID, atStr, detail string) []byte {
	h := blake3.New(32, nil)
	h.Write(prev)
	fmt.Fprintf(h, "%d\n%s\n%s\n%s\n%s\n%s\n%s", seq, actorHPI, action, resourceType, resourceID, atStr, detail)
	return h.Sum(nil)
}

// Append writes the next chained entry inside tx. The advisory lock
// makes concurrent appends queue rather than fork the chain.
func Append(ctx context.Context, tx pgx.Tx, e Entry) (Entry, error) {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, chainLockKey); err != nil {
		return e, err
	}
	var prevSeq int64
	prev := genesis
	err := tx.QueryRow(ctx,
		`SELECT seq, hash FROM audit_events ORDER BY seq DESC LIMIT 1`).
		Scan(&prevSeq, &prev)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return e, err
	}
	e.Seq = prevSeq + 1
	e.PrevHash = prev
	e.At = time.Now().UTC().Truncate(time.Microsecond)
	e.Hash = ChainHash(prev, e.Seq, e.ActorHPI, e.Action, e.ResourceType,
		e.ResourceID, e.At.Format(TimeLayout), e.Detail)
	_, err = tx.Exec(ctx, `
		INSERT INTO audit_events
		  (seq, prev_hash, hash, actor_hpi, action, resource_type, resource_id, at, detail)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		e.Seq, e.PrevHash, e.Hash, e.ActorHPI, e.Action,
		e.ResourceType, e.ResourceID, e.At, e.Detail)
	return e, err
}

type Report struct {
	OK        bool   `json:"ok"`
	Checked   int64  `json:"checked"`
	BrokenSeq int64  `json:"brokenSeq,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// Verify recomputes the whole chain from genesis.
func Verify(ctx context.Context, pool *pgxpool.Pool) (Report, error) {
	rows, err := pool.Query(ctx, `
		SELECT seq, prev_hash, hash, actor_hpi, action, resource_type,
		       resource_id, at, detail
		FROM audit_events ORDER BY seq`)
	if err != nil {
		return Report{}, err
	}
	defer rows.Close()

	prev := genesis
	var want int64 = 1
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.Seq, &e.PrevHash, &e.Hash, &e.ActorHPI,
			&e.Action, &e.ResourceType, &e.ResourceID, &e.At, &e.Detail); err != nil {
			return Report{}, err
		}
		broken := func(reason string) Report {
			return Report{Checked: want - 1, BrokenSeq: e.Seq, Reason: reason}
		}
		if e.Seq != want {
			return broken("sequence gap"), nil
		}
		if !bytes.Equal(e.PrevHash, prev) {
			return broken("prev_hash mismatch"), nil
		}
		expect := ChainHash(prev, e.Seq, e.ActorHPI, e.Action, e.ResourceType,
			e.ResourceID, e.At.UTC().Format(TimeLayout), e.Detail)
		if !bytes.Equal(expect, e.Hash) {
			return broken("hash mismatch"), nil
		}
		prev = e.Hash
		want++
	}
	return Report{OK: true, Checked: want - 1}, rows.Err()
}
