# Walking Skeleton Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship nz-open-emr v0.0.1 — `docker compose up` gives a FHIR R4 API + tiny web UI where you create a synthetic patient, write a note, and watch a BLAKE3 hash-chained audit log that can prove tampering.

**Architecture:** Single Go binary (`emrd`) + Postgres 16. Append-only `events` table is the source of truth; `patients`/`notes` are projections updated by a polling goroutine; every write and clinical read appends a hash-chained row to `audit_events` in the same transaction as the event. Stub identity via `X-Actor-HPI` header. Static demo UI embedded with `go:embed`.

**Tech Stack:** Go 1.22+ (stdlib `net/http` mux — no router dep), `jackc/pgx/v5`, `lukechampine.com/blake3`, `google/uuid`, Postgres 16, vanilla HTML/JS UI, distroless container, GitHub Actions CI.

**Conventions for every commit:** sign-off required (`git commit -s`), `-c user.name="Vuk Sekicki" -c user.email="sekickivuk@gmail.com"`, trailer `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.

**Spec deviation (recorded):** the spec says event payloads are "protobuf-encoded bytea + jsonb mirror". This plan stores canonical JSON bytes (`payload BYTEA`, exact bytes preserved) + `payload_json JSONB` mirror, with a `payload_encoding = 'json/v1'` column so protobuf can be added later as `proto/v1` without migration. Reason: protobuf codegen toolchain contradicts the skeleton's no-build-step frugality and proves nothing the JSON bytes don't. The hashed/canonical artefact is still an exact byte sequence.

**Integration tests:** any test needing Postgres calls `testutil.RequireDB(t)`, which skips unless `TEST_DATABASE_URL` is set. Locally: `docker compose up -d db` then `TEST_DATABASE_URL=postgres://emr:emr@localhost:5433/emr go test ./...`. CI provides a service container.

---

### Task 1: Scaffold Go module, gitignore, directories

**Files:**
- Create: `go.mod`, `.gitignore`, `.dockerignore`

- [ ] **Step 1: Create files**

`go.mod` (deps land via `go get` in later tasks):

```
module github.com/sekickivuk-hue/nz-open-emr

go 1.22
```

`.gitignore`:

```
emrd
*.test
coverage.out
.DS_Store
```

`.dockerignore`:

```
.git
docs
*.md
```

- [ ] **Step 2: Verify** — Run: `go vet ./...` → no output, exit 0 (no packages yet is fine: "no Go files" acceptable at this step).

- [ ] **Step 3: Commit** — `git add -A && git commit -s -m "chore: scaffold Go module"`

---

### Task 2: NHI validation + synthetic generation (`internal/nhi`)

**Files:**
- Create: `internal/nhi/nhi.go`
- Test: `internal/nhi/nhi_test.go`

Algorithm (verified against public test vectors, HISO 10046): alphabet `ABCDEFGHJKLMNPQRSTUVWXYZ` (no I/O), letter value = index+1, digits face value; weighted sum of first 6 chars with weights 7,6,5,4,3,2. Legacy `AAA999#`: `sum%11==0` invalid, else check digit `(11-sum%11)%10`. New `AAA99A#`: check char `alphabet[23-(sum%24)]`, mod 0 valid.

- [ ] **Step 1: Write the failing test**

```go
package nhi

import (
	"strings"
	"testing"
)

func TestValidateKnownGood(t *testing.T) {
	cases := []struct {
		in   string
		want Format
	}{
		{"ZZZ0016", FormatLegacy},
		{"ZZZ0024", FormatLegacy},
		{"ZZZ00AX", FormatNew},
		{"ALU18KZ", FormatNew},
		{"zzz0016", FormatLegacy}, // case-insensitive
	}
	for _, c := range cases {
		got, err := Validate(c.in)
		if err != nil || got != c.want {
			t.Errorf("Validate(%q) = %v, %v; want %v, nil", c.in, got, err, c.want)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	bad := []string{"", "ZZZ0044", "ZZZZ000", "ZZZ?000", "ZZZ0017", "ZZZ00AY", "IIIX000", "ZZZ001", "ZZZ00165"}
	for _, s := range bad {
		if _, err := Validate(s); err == nil {
			t.Errorf("Validate(%q) accepted, want reject", s)
		}
	}
}

func TestValidateRejectsAllOtherChecksums(t *testing.T) {
	// For a known-valid NHI, every other checksum char must fail.
	for _, base := range []string{"ZZZ001", "ZZZ00A"} {
		valid := 0
		for _, c := range "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ" {
			if _, err := Validate(base + string(c)); err == nil {
				valid++
			}
		}
		if valid != 1 {
			t.Errorf("base %q: %d checksums accepted, want exactly 1", base, valid)
		}
	}
}

func TestGenerateSyntheticRoundTrip(t *testing.T) {
	for _, f := range []Format{FormatLegacy, FormatNew} {
		for i := 0; i < 500; i++ {
			s, err := GenerateSynthetic(f)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(s, "Z") {
				t.Fatalf("synthetic NHI %q must start with reserved test prefix Z", s)
			}
			got, err := Validate(s)
			if err != nil || got != f {
				t.Fatalf("generated %q: Validate = %v, %v; want %v", s, got, err, f)
			}
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails** — `go test ./internal/nhi/` → FAIL (undefined: Format, Validate, ...)

- [ ] **Step 3: Write minimal implementation**

```go
// Package nhi validates and generates New Zealand National Health Index
// numbers per HISO 10046.
//
// Two formats: legacy AAA999# (3 letters, 3 digits, numeric check digit)
// and AAA99A# (3 letters, 2 digits, 1 letter, alpha check character),
// first issued July 2026. The letter alphabet excludes I and O.
//
// Synthetic NHIs always start with Z, the prefix reserved for test data,
// so they can never collide with a real person's identifier.
package nhi

import (
	"crypto/rand"
	"errors"
	"math/big"
	"regexp"
	"strings"
)

type Format string

const (
	FormatLegacy Format = "legacy"
	FormatNew    Format = "new"
)

const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ"

var (
	pattern    = regexp.MustCompile(`^[A-HJ-NP-Z]{3}([0-9]{4}|[0-9]{2}[A-HJ-NP-Z]{2})$`)
	ErrInvalid = errors.New("nhi: invalid")
)

func Validate(s string) (Format, error) {
	s = strings.ToUpper(s)
	if !pattern.MatchString(s) {
		return "", ErrInvalid
	}
	sum := 0
	for i := 0; i < 6; i++ {
		sum += charValue(s[i]) * (7 - i)
	}
	check := s[6]
	if check >= '0' && check <= '9' {
		mod := sum % 11
		if mod == 0 || int(check-'0') != (11-mod)%10 {
			return "", ErrInvalid
		}
		return FormatLegacy, nil
	}
	if alphabet[23-sum%24] != check {
		return "", ErrInvalid
	}
	return FormatNew, nil
}

func charValue(c byte) int {
	if c >= '0' && c <= '9' {
		return int(c - '0')
	}
	return strings.IndexByte(alphabet, c) + 1
}

// GenerateSynthetic returns a valid NHI in the requested format,
// always within the Z test prefix.
func GenerateSynthetic(f Format) (string, error) {
	for i := 0; i < 1000; i++ {
		b := []byte{'Z', alphabet[randInt(24)], alphabet[randInt(24)],
			byte('0' + randInt(10)), byte('0' + randInt(10)), 0}
		if f == FormatNew {
			b[5] = alphabet[randInt(24)]
		} else {
			b[5] = byte('0' + randInt(10))
		}
		sum := 0
		for i := 0; i < 6; i++ {
			sum += charValue(b[i]) * (7 - i)
		}
		if f == FormatLegacy {
			mod := sum % 11
			if mod == 0 {
				continue // not allocatable; retry
			}
			return string(b) + string(byte('0'+(11-mod)%10)), nil
		}
		return string(b) + string(alphabet[23-sum%24]), nil
	}
	return "", errors.New("nhi: generation failed")
}

func randInt(n int64) int64 {
	v, err := rand.Int(rand.Reader, big.NewInt(n))
	if err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return v.Int64()
}
```

- [ ] **Step 4: Run test to verify it passes** — `go test ./internal/nhi/ -v` → PASS

- [ ] **Step 5: Commit** — `git add internal/nhi && git commit -s -m "feat: NHI validation and synthetic generation, both HISO 10046 formats"`

---

### Task 3: Database schema, connect, migrate, test harness (`internal/db`, `internal/testutil`)

**Files:**
- Create: `internal/db/db.go`, `internal/db/schema.sql`, `internal/testutil/db.go`
- Test: `internal/db/db_test.go`

- [ ] **Step 1: Add dependencies** — `go get github.com/jackc/pgx/v5@latest github.com/google/uuid@latest lukechampine.com/blake3@latest`

- [ ] **Step 2: Write `schema.sql`**

```sql
-- Idempotent schema. The events table is the source of truth; patients
-- and notes are projections and may be rebuilt from events at any time.

CREATE TABLE IF NOT EXISTS events (
    seq              BIGSERIAL PRIMARY KEY,
    event_type       TEXT        NOT NULL,
    aggregate_type   TEXT        NOT NULL,
    aggregate_id     UUID        NOT NULL,
    payload          BYTEA       NOT NULL, -- canonical bytes, exact
    payload_json     JSONB       NOT NULL, -- queryable mirror of payload
    payload_encoding TEXT        NOT NULL DEFAULT 'json/v1',
    actor_hpi        TEXT        NOT NULL,
    at               TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS events_aggregate_idx
    ON events (aggregate_type, aggregate_id, seq);

CREATE TABLE IF NOT EXISTS audit_events (
    seq           BIGINT      PRIMARY KEY, -- explicit: chained, no gaps
    prev_hash     BYTEA       NOT NULL,
    hash          BYTEA       NOT NULL,
    actor_hpi     TEXT        NOT NULL,
    action        TEXT        NOT NULL, -- C create | R read
    resource_type TEXT        NOT NULL,
    resource_id   TEXT        NOT NULL,
    at            TIMESTAMPTZ NOT NULL,
    detail        TEXT        NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS patients (
    id             UUID   PRIMARY KEY,
    nhi            TEXT   NOT NULL UNIQUE,
    nhi_format     TEXT   NOT NULL,
    family_name    TEXT   NOT NULL,
    given_name     TEXT   NOT NULL,
    birth_date     DATE,
    last_event_seq BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS notes (
    id             UUID        PRIMARY KEY,
    patient_id     UUID        NOT NULL REFERENCES patients(id),
    author_hpi     TEXT        NOT NULL,
    text           TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL,
    last_event_seq BIGINT      NOT NULL
);

CREATE TABLE IF NOT EXISTS projection_state (
    id       TEXT   PRIMARY KEY,
    last_seq BIGINT NOT NULL
);
INSERT INTO projection_state (id, last_seq) VALUES ('main', 0)
    ON CONFLICT DO NOTHING;
```

- [ ] **Step 3: Write `db.go`**

```go
// Package db owns connection setup and schema migration.
package db

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

// Connect retries because in docker compose the app can win the race
// against Postgres even with a healthcheck.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	var lastErr error
	for i := 0; i < 30; i++ {
		pool, err := pgxpool.New(ctx, url)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				return pool, nil
			}
			pool.Close()
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil, fmt.Errorf("db: connect: %w", lastErr)
}

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, schema)
	return err
}
```

- [ ] **Step 4: Write `testutil/db.go`**

```go
// Package testutil provides the integration-test database harness.
package testutil

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sekickivuk-hue/nz-open-emr/internal/db"
)

// RequireDB skips the test unless TEST_DATABASE_URL is set, otherwise
// returns a pool with a freshly reset schema.
func RequireDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}
```

- [ ] **Step 5: Write `db_test.go`**

```go
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
```

- [ ] **Step 6: Run** — `go build ./... && go test ./internal/db/` → compiles; test SKIPs without DB, PASSes with `TEST_DATABASE_URL` set (start `docker compose up -d db` once Task 11 lands; until then validated in CI or via a manually started Postgres).

- [ ] **Step 7: Commit** — `git add -A && git commit -s -m "feat: schema, db connect/migrate, integration test harness"`

---

### Task 4: Hash-chained audit log (`internal/audit`)

**Files:**
- Create: `internal/audit/audit.go`
- Test: `internal/audit/audit_test.go`

- [ ] **Step 1: Write the failing tests** (pure hash unit test + chain integration test)

```go
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
```

- [ ] **Step 2: Run to verify failure** — `go test ./internal/audit/` → FAIL (undefined)

- [ ] **Step 3: Implement**

```go
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
```

- [ ] **Step 4: Run to verify pass** — `go test ./internal/audit/` → PASS (unit test always; chain tests with DB)

- [ ] **Step 5: Commit** — `git add internal/audit && git commit -s -m "feat: BLAKE3 hash-chained audit log with tamper-pinpointing verification"`

---

### Task 5: Event store (`internal/eventstore`)

**Files:**
- Create: `internal/eventstore/eventstore.go`
- Test: `internal/eventstore/eventstore_test.go`

- [ ] **Step 1: Failing test**

```go
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
```

- [ ] **Step 2: Run to verify failure** — `go test ./internal/eventstore/` → FAIL

- [ ] **Step 3: Implement**

```go
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
```

- [ ] **Step 4: Run to verify pass** — `go test ./internal/eventstore/` → PASS (skips without DB)

- [ ] **Step 5: Commit** — `git add internal/eventstore && git commit -s -m "feat: append-only event store with canonical json/v1 payloads"`

---

### Task 6: Projection worker (`internal/projection`)

**Files:**
- Create: `internal/projection/projector.go`
- Test: `internal/projection/projector_test.go`

- [ ] **Step 1: Failing test**

```go
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
```

- [ ] **Step 2: Run to verify failure** — `go test ./internal/projection/` → FAIL

- [ ] **Step 3: Implement**

```go
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
```

- [ ] **Step 4: Run to verify pass** — `go test ./internal/projection/` → PASS with DB

- [ ] **Step 5: Commit** — `git add internal/projection && git commit -s -m "feat: polling projector deriving patient/note read models from events"`

---

### Task 7: FHIR R4 mapping (`internal/fhir`)

**Files:**
- Create: `internal/fhir/fhir.go`
- Test: `internal/fhir/fhir_test.go`

- [ ] **Step 1: Failing test**

```go
package fhir_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sekickivuk-hue/nz-open-emr/internal/fhir"
)

func TestPatientJSONShape(t *testing.T) {
	p := fhir.Patient{
		ID: "abc",
		Identifier: []fhir.Identifier{{Use: "official", System: fhir.SystemNHI, Value: "ZZZ0016"}},
		Name:       []fhir.HumanName{{Family: "Demo", Given: []string{"Pat"}}},
		BirthDate:  "1980-01-01",
	}
	b, _ := json.Marshal(p)
	s := string(b)
	for _, want := range []string{
		`"resourceType":"Patient"`, `"id":"abc"`,
		`"system":"https://standards.digital.health.nz/ns/nhi-id"`,
		`"family":"Demo"`, `"birthDate":"1980-01-01"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("Patient JSON missing %s in %s", want, s)
		}
	}
}

func TestNHIFromPatient(t *testing.T) {
	p := fhir.Patient{Identifier: []fhir.Identifier{
		{System: "urn:other", Value: "x"},
		{System: fhir.SystemNHI, Value: "ZZZ0016"},
	}}
	if got := p.NHI(); got != "ZZZ0016" {
		t.Fatalf("NHI() = %q", got)
	}
	if got := (fhir.Patient{}).NHI(); got != "" {
		t.Fatalf("empty patient NHI() = %q", got)
	}
}

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	fhir.WriteError(rec, 404, "not-found", "no such patient")
	if rec.Code != 404 {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/fhir+json" {
		t.Fatalf("content-type %q", ct)
	}
	var oo fhir.OperationOutcome
	if err := json.Unmarshal(rec.Body.Bytes(), &oo); err != nil ||
		oo.ResourceType != "OperationOutcome" || len(oo.Issue) != 1 {
		t.Fatalf("body: %s err: %v", rec.Body.String(), err)
	}
}
```

- [ ] **Step 2: Run to verify failure** — `go test ./internal/fhir/` → FAIL

- [ ] **Step 3: Implement**

```go
// Package fhir holds the minimal FHIR R4 resource shapes the skeleton
// speaks. These are hand-rolled on purpose: the walking skeleton proves
// the architecture, not full R4 conformance (that is a later module).
package fhir

import (
	"encoding/json"
	"net/http"
)

const (
	SystemNHI = "https://standards.digital.health.nz/ns/nhi-id"
	SystemHPI = "https://standards.digital.health.nz/ns/hpi-person-id"
	MIMEType  = "application/fhir+json"
)

type Identifier struct {
	Use    string `json:"use,omitempty"`
	System string `json:"system,omitempty"`
	Value  string `json:"value,omitempty"`
}

type HumanName struct {
	Family string   `json:"family,omitempty"`
	Given  []string `json:"given,omitempty"`
}

type Reference struct {
	Reference  string      `json:"reference,omitempty"`
	Display    string      `json:"display,omitempty"`
	Identifier *Identifier `json:"identifier,omitempty"`
}

type Patient struct {
	ResourceType string       `json:"resourceType"`
	ID           string       `json:"id,omitempty"`
	Identifier   []Identifier `json:"identifier,omitempty"`
	Name         []HumanName  `json:"name,omitempty"`
	BirthDate    string       `json:"birthDate,omitempty"`
}

func (p Patient) MarshalJSON() ([]byte, error) {
	type alias Patient
	a := alias(p)
	a.ResourceType = "Patient"
	return json.Marshal(a)
}

// NHI returns the value of the NHI identifier, or "".
func (p Patient) NHI() string {
	for _, id := range p.Identifier {
		if id.System == SystemNHI {
			return id.Value
		}
	}
	return ""
}

type Attachment struct {
	ContentType string `json:"contentType,omitempty"`
	Data        string `json:"data,omitempty"` // base64
}

type DocContent struct {
	Attachment Attachment `json:"attachment"`
}

type DocumentReference struct {
	ResourceType string       `json:"resourceType"`
	ID           string       `json:"id,omitempty"`
	Status       string       `json:"status"`
	Subject      *Reference   `json:"subject,omitempty"`
	Date         string       `json:"date,omitempty"`
	Author       []Reference  `json:"author,omitempty"`
	Content      []DocContent `json:"content"`
}

func (d DocumentReference) MarshalJSON() ([]byte, error) {
	type alias DocumentReference
	a := alias(d)
	a.ResourceType = "DocumentReference"
	if a.Status == "" {
		a.Status = "current"
	}
	return json.Marshal(a)
}

type Extension struct {
	URL         string `json:"url"`
	ValueString string `json:"valueString,omitempty"`
}

// ExtAuditHash carries the hex hash of the chained audit entry, so the
// UI can show the chain on FHIR AuditEvent resources.
const ExtAuditHash = "https://nz-open-emr.org/fhir/StructureDefinition/audit-hash"

type AuditAgent struct {
	Who       *Reference `json:"who,omitempty"`
	Requestor bool       `json:"requestor"`
}

type AuditEntity struct {
	What *Reference `json:"what,omitempty"`
}

type AuditEvent struct {
	ResourceType string        `json:"resourceType"`
	ID           string        `json:"id,omitempty"`
	Extension    []Extension   `json:"extension,omitempty"`
	Action       string        `json:"action,omitempty"`
	Recorded     string        `json:"recorded,omitempty"`
	Outcome      string        `json:"outcome,omitempty"`
	Agent        []AuditAgent  `json:"agent"`
	Entity       []AuditEntity `json:"entity,omitempty"`
}

func (a AuditEvent) MarshalJSON() ([]byte, error) {
	type alias AuditEvent
	x := alias(a)
	x.ResourceType = "AuditEvent"
	return json.Marshal(x)
}

type BundleEntry struct {
	Resource any `json:"resource"`
}

type Bundle struct {
	ResourceType string        `json:"resourceType"`
	Type         string        `json:"type"`
	Total        int           `json:"total"`
	Entry        []BundleEntry `json:"entry,omitempty"`
}

func NewSearchSet(resources []any) Bundle {
	b := Bundle{ResourceType: "Bundle", Type: "searchset", Total: len(resources)}
	for _, r := range resources {
		b.Entry = append(b.Entry, BundleEntry{Resource: r})
	}
	return b
}

type Issue struct {
	Severity    string `json:"severity"`
	Code        string `json:"code"`
	Diagnostics string `json:"diagnostics,omitempty"`
}

type OperationOutcome struct {
	ResourceType string  `json:"resourceType"`
	Issue        []Issue `json:"issue"`
}

func WriteError(w http.ResponseWriter, status int, code, diagnostics string) {
	w.Header().Set("Content-Type", MIMEType)
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(OperationOutcome{
		ResourceType: "OperationOutcome",
		Issue:        []Issue{{Severity: "error", Code: code, Diagnostics: diagnostics}},
	})
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", MIMEType)
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
```

- [ ] **Step 4: Run to verify pass** — `go test ./internal/fhir/` → PASS

- [ ] **Step 5: Commit** — `git add internal/fhir && git commit -s -m "feat: minimal FHIR R4 resource shapes and error envelope"`

---

### Task 8: Identity stub (`internal/identity`)

**Files:**
- Create: `internal/identity/identity.go`
- Test: `internal/identity/identity_test.go`

- [ ] **Step 1: Failing test**

```go
package identity_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sekickivuk-hue/nz-open-emr/internal/identity"
)

func TestMiddleware(t *testing.T) {
	var got identity.Actor
	h := identity.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = identity.FromContext(r.Context())
	}))

	// Valid actor passes and lands in context.
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Actor-HPI", "99ZZZA")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || got.HPI != "99ZZZA" || got.Name == "" {
		t.Fatalf("code=%d actor=%+v", rec.Code, got)
	}

	// Missing header → 401 with OperationOutcome.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 401 {
		t.Fatalf("missing header: code=%d", rec.Code)
	}

	// Unknown actor → 401.
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Actor-HPI", "11AAAA")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("unknown actor: code=%d", rec.Code)
	}
}
```

- [ ] **Step 2: Run to verify failure** — `go test ./internal/identity/` → FAIL

- [ ] **Step 3: Implement**

```go
// Package identity is the deliberate stub for clinician identity.
// The skeleton trusts an X-Actor-HPI header against a fixed list of
// synthetic clinicians. Swapping this for OIDC/My Health Account
// Workforce later means replacing Middleware only — nothing else in
// the system knows where the Actor came from.
package identity

import (
	"context"
	"net/http"

	"github.com/sekickivuk-hue/nz-open-emr/internal/fhir"
)

type Actor struct {
	HPI  string `json:"hpi"`
	Name string `json:"name"`
	Role string `json:"role"`
}

// Demo actors use the 99ZZZx synthetic HPI range.
var Demo = []Actor{
	{HPI: "99ZZZA", Name: "Dr Aroha Demo", Role: "SMO General Medicine"},
	{HPI: "99ZZZB", Name: "Dr Ben Demo", Role: "General Practitioner"},
	{HPI: "99ZZZC", Name: "RN Cath Demo", Role: "Registered Nurse"},
}

func Lookup(hpi string) (Actor, bool) {
	for _, a := range Demo {
		if a.HPI == hpi {
			return a, true
		}
	}
	return Actor{}, false
}

type ctxKey struct{}

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor, ok := Lookup(r.Header.Get("X-Actor-HPI"))
		if !ok {
			fhir.WriteError(w, http.StatusUnauthorized, "login",
				"missing or unknown X-Actor-HPI header (demo identity)")
			return
		}
		next.ServeHTTP(w, r.WithContext(
			context.WithValue(r.Context(), ctxKey{}, actor)))
	})
}

func FromContext(ctx context.Context) Actor {
	a, _ := ctx.Value(ctxKey{}).(Actor)
	return a
}
```

- [ ] **Step 4: Run to verify pass** — `go test ./internal/identity/` → PASS

- [ ] **Step 5: Commit** — `git add internal/identity && git commit -s -m "feat: stub actor identity middleware (swap point for future OIDC)"`

---

### Task 9: HTTP API (`internal/api`) with end-to-end + tamper integration test

**Files:**
- Create: `internal/api/api.go`
- Test: `internal/api/api_test.go`

Routes (Go 1.22 method+pattern mux). `/fhir/r4/*` behind identity middleware; demo + ops endpoints open:

| Route | Behaviour |
|---|---|
| `POST /fhir/r4/Patient` | Validate NHI + name; 409 if NHI exists; event `PatientRegistered` + audit `C` in one tx; 201 |
| `GET /fhir/r4/Patient` | Bundle from `patients` (optional `?identifier=`); audit `R` resource_id `search` |
| `GET /fhir/r4/Patient/{id}` | 404 OperationOutcome or resource; audit `R` |
| `POST /fhir/r4/DocumentReference` | subject must exist; decode base64 text; event `NoteCreated` + audit `C`; 201 |
| `GET /fhir/r4/DocumentReference?patient={id}` | Bundle of notes; audit `R` |
| `GET /fhir/r4/AuditEvent?_count=N` | Last N (default 50, max 200) as Bundle with hash extension. Reads of the audit log itself are not re-audited in the skeleton (would self-amplify via UI polling); revisit in Module 5 spec |
| `GET /audit/verify` | JSON `audit.Report` |
| `GET /demo/actors` | identity.Demo |
| `GET /demo/generate-nhi?format=legacy\|new` | `{"nhi":..., "format":...}` |
| `GET /healthz` | DB ping |

- [ ] **Step 1: Write the failing integration test** — full clinical journey + tamper proof

```go
package api_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sekickivuk-hue/nz-open-emr/internal/api"
	"github.com/sekickivuk-hue/nz-open-emr/internal/projection"
	"github.com/sekickivuk-hue/nz-open-emr/internal/testutil"
)

func do(t *testing.T, srv *httptest.Server, method, path, actor string, body any) (*http.Response, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, srv.URL+path, rd)
	if actor != "" {
		req.Header.Set("X-Actor-HPI", actor)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, b
}

func TestClinicalJourneyAndTamperEvidence(t *testing.T) {
	pool := testutil.RequireDB(t)
	ctx := context.Background()
	proj := &projection.Projector{Pool: pool}
	srv := httptest.NewServer(api.New(pool, proj))
	defer srv.Close()

	// 1. Unauthenticated request is rejected.
	resp, _ := do(t, srv, "GET", "/fhir/r4/Patient", "", nil)
	if resp.StatusCode != 401 {
		t.Fatalf("no actor: %d", resp.StatusCode)
	}

	// 2. Create a patient.
	patient := map[string]any{
		"resourceType": "Patient",
		"identifier":   []map[string]any{{"system": "https://standards.digital.health.nz/ns/nhi-id", "value": "ZZZ0016"}},
		"name":         []map[string]any{{"family": "Demo", "given": []string{"Pat"}}},
		"birthDate":    "1980-01-01",
	}
	resp, body := do(t, srv, "POST", "/fhir/r4/Patient", "99ZZZA", patient)
	if resp.StatusCode != 201 {
		t.Fatalf("create patient: %d %s", resp.StatusCode, body)
	}
	var created struct{ ID string `json:"id"` }
	json.Unmarshal(body, &created)
	if created.ID == "" {
		t.Fatal("no id in created patient")
	}

	// 3. Duplicate NHI → 409.
	resp, _ = do(t, srv, "POST", "/fhir/r4/Patient", "99ZZZA", patient)
	if resp.StatusCode != 409 {
		t.Fatalf("duplicate NHI: %d", resp.StatusCode)
	}

	// 4. Bad NHI → 400.
	bad := map[string]any{
		"resourceType": "Patient",
		"identifier":   []map[string]any{{"system": "https://standards.digital.health.nz/ns/nhi-id", "value": "ZZZ0017"}},
		"name":         []map[string]any{{"family": "X", "given": []string{"Y"}}},
	}
	resp, _ = do(t, srv, "POST", "/fhir/r4/Patient", "99ZZZA", bad)
	if resp.StatusCode != 400 {
		t.Fatalf("bad NHI: %d", resp.StatusCode)
	}

	// 5. Project, then read the patient back (this audits a read).
	if err := proj.Step(ctx); err != nil {
		t.Fatal(err)
	}
	resp, body = do(t, srv, "GET", "/fhir/r4/Patient/"+created.ID, "99ZZZB", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("get patient: %d %s", resp.StatusCode, body)
	}

	// 6. Write a note.
	note := map[string]any{
		"resourceType": "DocumentReference",
		"subject":      map[string]any{"reference": "Patient/" + created.ID},
		"content": []map[string]any{{"attachment": map[string]any{
			"contentType": "text/plain",
			"data":        base64.StdEncoding.EncodeToString([]byte("Patient seen, well. Kia ora.")),
		}}},
	}
	resp, body = do(t, srv, "POST", "/fhir/r4/DocumentReference", "99ZZZA", note)
	if resp.StatusCode != 201 {
		t.Fatalf("create note: %d %s", resp.StatusCode, body)
	}
	proj.Step(ctx)
	resp, body = do(t, srv, "GET", "/fhir/r4/DocumentReference?patient="+created.ID, "99ZZZA", nil)
	var noteBundle struct{ Total int `json:"total"` }
	json.Unmarshal(body, &noteBundle)
	if resp.StatusCode != 200 || noteBundle.Total != 1 {
		t.Fatalf("list notes: %d total=%d", resp.StatusCode, noteBundle.Total)
	}

	// 7. Audit log has events; chain verifies.
	resp, body = do(t, srv, "GET", "/fhir/r4/AuditEvent", "99ZZZA", nil)
	var auditBundle struct{ Total int `json:"total"` }
	json.Unmarshal(body, &auditBundle)
	if resp.StatusCode != 200 || auditBundle.Total < 4 {
		t.Fatalf("audit bundle: %d total=%d", resp.StatusCode, auditBundle.Total)
	}
	resp, body = do(t, srv, "GET", "/audit/verify", "", nil)
	var rep struct {
		OK        bool  `json:"ok"`
		BrokenSeq int64 `json:"brokenSeq"`
	}
	json.Unmarshal(body, &rep)
	if resp.StatusCode != 200 || !rep.OK {
		t.Fatalf("verify before tamper: %d %s", resp.StatusCode, body)
	}

	// 8. THE FLAGSHIP: a DBA edits history; verification pinpoints it.
	if _, err := pool.Exec(ctx,
		`UPDATE audit_events SET actor_hpi = 'EVIL' WHERE seq = 2`); err != nil {
		t.Fatal(err)
	}
	_, body = do(t, srv, "GET", "/audit/verify", "", nil)
	json.Unmarshal(body, &rep)
	if rep.OK || rep.BrokenSeq != 2 {
		t.Fatalf("tamper not detected at seq 2: %s", body)
	}

	// 9. Health endpoint.
	resp, _ = do(t, srv, "GET", "/healthz", "", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("healthz: %d", resp.StatusCode)
	}

	// 10. Demo endpoints.
	resp, body = do(t, srv, "GET", "/demo/generate-nhi?format=new", "", nil)
	var gen struct{ NHI, Format string }
	json.Unmarshal(body, &gen)
	if resp.StatusCode != 200 || len(gen.NHI) != 7 || gen.Format != "new" {
		t.Fatalf("generate-nhi: %d %s", resp.StatusCode, body)
	}
	_ = fmt.Sprint() // keep fmt import if unused elsewhere
}
```

- [ ] **Step 2: Run to verify failure** — `go test ./internal/api/` → FAIL (undefined api.New)

- [ ] **Step 3: Implement `api.go`**

```go
// Package api wires the HTTP surface: FHIR R4 endpoints behind the
// identity middleware, plus open demo/ops endpoints.
package api

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sekickivuk-hue/nz-open-emr/internal/audit"
	"github.com/sekickivuk-hue/nz-open-emr/internal/eventstore"
	"github.com/sekickivuk-hue/nz-open-emr/internal/fhir"
	"github.com/sekickivuk-hue/nz-open-emr/internal/identity"
	"github.com/sekickivuk-hue/nz-open-emr/internal/nhi"
	"github.com/sekickivuk-hue/nz-open-emr/internal/projection"
)

type server struct {
	pool *pgxpool.Pool
	proj *projection.Projector
}

func New(pool *pgxpool.Pool, proj *projection.Projector) http.Handler {
	s := &server{pool: pool, proj: proj}

	fhirMux := http.NewServeMux()
	fhirMux.HandleFunc("POST /fhir/r4/Patient", s.createPatient)
	fhirMux.HandleFunc("GET /fhir/r4/Patient", s.listPatients)
	fhirMux.HandleFunc("GET /fhir/r4/Patient/{id}", s.getPatient)
	fhirMux.HandleFunc("POST /fhir/r4/DocumentReference", s.createNote)
	fhirMux.HandleFunc("GET /fhir/r4/DocumentReference", s.listNotes)
	fhirMux.HandleFunc("GET /fhir/r4/AuditEvent", s.listAudit)

	root := http.NewServeMux()
	root.Handle("/fhir/r4/", identity.Middleware(fhirMux))
	root.HandleFunc("GET /audit/verify", s.verifyAudit)
	root.HandleFunc("GET /demo/actors", func(w http.ResponseWriter, r *http.Request) {
		writePlainJSON(w, 200, identity.Demo)
	})
	root.HandleFunc("GET /demo/generate-nhi", s.generateNHI)
	root.HandleFunc("GET /healthz", s.healthz)
	return root
}

func writePlainJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// appendEventAndAudit runs the core invariant of the whole system:
// a domain event and its chained audit entry commit atomically.
func (s *server) appendEventAndAudit(ctx context.Context, ev *eventstore.Event, ae audit.Entry) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := eventstore.Append(ctx, tx, ev); err != nil {
		return err
	}
	if _, err := audit.Append(ctx, tx, ae); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// auditRead records a clinical read in its own transaction.
func (s *server) auditRead(ctx context.Context, actor identity.Actor, resourceType, resourceID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := audit.Append(ctx, tx, audit.Entry{
		ActorHPI: actor.HPI, Action: "R",
		ResourceType: resourceType, ResourceID: resourceID,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *server) createPatient(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	var p fhir.Patient
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		fhir.WriteError(w, 400, "structure", "invalid JSON: "+err.Error())
		return
	}
	nhiVal := p.NHI()
	format, err := nhi.Validate(nhiVal)
	if err != nil {
		fhir.WriteError(w, 400, "value", "missing or invalid NHI identifier")
		return
	}
	if len(p.Name) == 0 || p.Name[0].Family == "" || len(p.Name[0].Given) == 0 {
		fhir.WriteError(w, 400, "required", "name with family and given is required")
		return
	}
	var exists bool
	if err := s.pool.QueryRow(r.Context(),
		`SELECT EXISTS (SELECT 1 FROM patients WHERE nhi = $1)`, nhiVal).Scan(&exists); err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	if exists {
		fhir.WriteError(w, 409, "duplicate", "a patient with this NHI already exists")
		return
	}

	id := uuid.New()
	payload, err := eventstore.Canonical(eventstore.PatientRegistered{
		ID: id.String(), NHI: nhiVal, NHIFormat: string(format),
		FamilyName: p.Name[0].Family, GivenName: p.Name[0].Given[0],
		BirthDate: p.BirthDate,
	})
	if err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	ev := &eventstore.Event{
		Type: eventstore.TypePatientRegistered, AggregateType: "Patient",
		AggregateID: id, Payload: payload, ActorHPI: actor.HPI,
	}
	ae := audit.Entry{
		ActorHPI: actor.HPI, Action: "C",
		ResourceType: "Patient", ResourceID: id.String(),
		Detail: fmt.Sprintf(`{"nhi":%q}`, nhiVal),
	}
	if err := s.appendEventAndAudit(r.Context(), ev, ae); err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}

	p.ID = id.String()
	w.Header().Set("Location", "/fhir/r4/Patient/"+p.ID)
	fhir.WriteJSON(w, 201, p)
}

func (s *server) getPatient(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	id := r.PathValue("id")
	var p fhir.Patient
	var nhiVal, family, given string
	var birth *time.Time
	err := s.pool.QueryRow(r.Context(), `
		SELECT nhi, family_name, given_name, birth_date
		FROM patients WHERE id = $1`, id).
		Scan(&nhiVal, &family, &given, &birth)
	if err != nil {
		fhir.WriteError(w, 404, "not-found", "no such patient")
		return
	}
	if err := s.auditRead(r.Context(), actor, "Patient", id); err != nil {
		fhir.WriteError(w, 500, "exception", "audit failed; read refused")
		return
	}
	p = patientResource(id, nhiVal, family, given, birth)
	fhir.WriteJSON(w, 200, p)
}

func patientResource(id, nhiVal, family, given string, birth *time.Time) fhir.Patient {
	p := fhir.Patient{
		ID: id,
		Identifier: []fhir.Identifier{{Use: "official", System: fhir.SystemNHI, Value: nhiVal}},
		Name:       []fhir.HumanName{{Family: family, Given: []string{given}}},
	}
	if birth != nil {
		p.BirthDate = birth.Format("2006-01-02")
	}
	return p
}

func (s *server) listPatients(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	q := `SELECT id, nhi, family_name, given_name, birth_date FROM patients`
	args := []any{}
	if ident := r.URL.Query().Get("identifier"); ident != "" {
		q += ` WHERE nhi = $1`
		args = append(args, ident)
	}
	q += ` ORDER BY family_name, given_name LIMIT 100`
	rows, err := s.pool.Query(r.Context(), q, args...)
	if err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	defer rows.Close()
	var resources []any
	for rows.Next() {
		var id, nhiVal, family, given string
		var birth *time.Time
		if err := rows.Scan(&id, &nhiVal, &family, &given, &birth); err != nil {
			fhir.WriteError(w, 500, "exception", err.Error())
			return
		}
		resources = append(resources, patientResource(id, nhiVal, family, given, birth))
	}
	if err := s.auditRead(r.Context(), actor, "Patient", "search"); err != nil {
		fhir.WriteError(w, 500, "exception", "audit failed; read refused")
		return
	}
	fhir.WriteJSON(w, 200, fhir.NewSearchSet(resources))
}

func (s *server) createNote(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	var d fhir.DocumentReference
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		fhir.WriteError(w, 400, "structure", "invalid JSON: "+err.Error())
		return
	}
	if d.Subject == nil || len(d.Subject.Reference) < len("Patient/")+1 ||
		d.Subject.Reference[:len("Patient/")] != "Patient/" {
		fhir.WriteError(w, 400, "required", "subject.reference Patient/{id} is required")
		return
	}
	patientID := d.Subject.Reference[len("Patient/"):]
	if len(d.Content) == 0 || d.Content[0].Attachment.Data == "" {
		fhir.WriteError(w, 400, "required", "content[0].attachment.data is required")
		return
	}
	text, err := base64.StdEncoding.DecodeString(d.Content[0].Attachment.Data)
	if err != nil {
		fhir.WriteError(w, 400, "value", "attachment.data is not valid base64")
		return
	}
	var exists bool
	if err := s.pool.QueryRow(r.Context(),
		`SELECT EXISTS (SELECT 1 FROM patients WHERE id = $1)`, patientID).Scan(&exists); err != nil || !exists {
		fhir.WriteError(w, 422, "not-found", "subject patient does not exist")
		return
	}

	id := uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	payload, err := eventstore.Canonical(eventstore.NoteCreated{
		ID: id.String(), PatientID: patientID, AuthorHPI: actor.HPI,
		Text: string(text), CreatedAt: now.Format(time.RFC3339Nano),
	})
	if err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	ev := &eventstore.Event{
		Type: eventstore.TypeNoteCreated, AggregateType: "Note",
		AggregateID: id, Payload: payload, ActorHPI: actor.HPI,
	}
	ae := audit.Entry{
		ActorHPI: actor.HPI, Action: "C",
		ResourceType: "DocumentReference", ResourceID: id.String(),
		Detail: fmt.Sprintf(`{"patient":%q}`, patientID),
	}
	if err := s.appendEventAndAudit(r.Context(), ev, ae); err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}

	d.ID = id.String()
	d.Date = now.Format(time.RFC3339Nano)
	d.Author = []fhir.Reference{{
		Display:    actor.Name,
		Identifier: &fhir.Identifier{System: fhir.SystemHPI, Value: actor.HPI},
	}}
	w.Header().Set("Location", "/fhir/r4/DocumentReference/"+d.ID)
	fhir.WriteJSON(w, 201, d)
}

func (s *server) listNotes(w http.ResponseWriter, r *http.Request) {
	actor := identity.FromContext(r.Context())
	patientID := r.URL.Query().Get("patient")
	if patientID == "" {
		fhir.WriteError(w, 400, "required", "patient query parameter is required")
		return
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, author_hpi, text, created_at FROM notes
		WHERE patient_id = $1 ORDER BY created_at`, patientID)
	if err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	defer rows.Close()
	var resources []any
	for rows.Next() {
		var id, author, text string
		var created time.Time
		if err := rows.Scan(&id, &author, &text, &created); err != nil {
			fhir.WriteError(w, 500, "exception", err.Error())
			return
		}
		authorName := author
		if a, ok := identity.Lookup(author); ok {
			authorName = a.Name
		}
		resources = append(resources, fhir.DocumentReference{
			ID: id, Status: "current",
			Subject: &fhir.Reference{Reference: "Patient/" + patientID},
			Date:    created.UTC().Format(time.RFC3339Nano),
			Author: []fhir.Reference{{
				Display:    authorName,
				Identifier: &fhir.Identifier{System: fhir.SystemHPI, Value: author},
			}},
			Content: []fhir.DocContent{{Attachment: fhir.Attachment{
				ContentType: "text/plain",
				Data:        base64.StdEncoding.EncodeToString([]byte(text)),
			}}},
		})
	}
	if err := s.auditRead(r.Context(), actor, "DocumentReference", "patient="+patientID); err != nil {
		fhir.WriteError(w, 500, "exception", "audit failed; read refused")
		return
	}
	fhir.WriteJSON(w, 200, fhir.NewSearchSet(resources))
}

func (s *server) listAudit(w http.ResponseWriter, r *http.Request) {
	count := 50
	if c, err := strconv.Atoi(r.URL.Query().Get("_count")); err == nil && c > 0 && c <= 200 {
		count = c
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT seq, hash, actor_hpi, action, resource_type, resource_id, at
		FROM audit_events ORDER BY seq DESC LIMIT $1`, count)
	if err != nil {
		fhir.WriteError(w, 500, "exception", err.Error())
		return
	}
	defer rows.Close()
	var resources []any
	for rows.Next() {
		var seq int64
		var hash []byte
		var actorHPI, action, rtype, rid string
		var at time.Time
		if err := rows.Scan(&seq, &hash, &actorHPI, &action, &rtype, &rid, &at); err != nil {
			fhir.WriteError(w, 500, "exception", err.Error())
			return
		}
		name := actorHPI
		if a, ok := identity.Lookup(actorHPI); ok {
			name = a.Name
		}
		resources = append(resources, fhir.AuditEvent{
			ID:        strconv.FormatInt(seq, 10),
			Action:    action,
			Recorded:  at.UTC().Format(time.RFC3339Nano),
			Outcome:   "0",
			Extension: []fhir.Extension{{URL: fhir.ExtAuditHash, ValueString: hex.EncodeToString(hash)}},
			Agent: []fhir.AuditAgent{{Requestor: true, Who: &fhir.Reference{
				Display:    name,
				Identifier: &fhir.Identifier{System: fhir.SystemHPI, Value: actorHPI},
			}}},
			Entity: []fhir.AuditEntity{{What: &fhir.Reference{Reference: rtype + "/" + rid}}},
		})
	}
	fhir.WriteJSON(w, 200, fhir.NewSearchSet(resources))
}

func (s *server) verifyAudit(w http.ResponseWriter, r *http.Request) {
	rep, err := audit.Verify(r.Context(), s.pool)
	if err != nil {
		writePlainJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writePlainJSON(w, 200, rep)
}

func (s *server) generateNHI(w http.ResponseWriter, r *http.Request) {
	format := nhi.Format(r.URL.Query().Get("format"))
	if format != nhi.FormatNew {
		format = nhi.FormatLegacy
	}
	v, err := nhi.GenerateSynthetic(format)
	if err != nil {
		writePlainJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writePlainJSON(w, 200, map[string]string{"nhi": v, "format": string(format)})
}

func (s *server) healthz(w http.ResponseWriter, r *http.Request) {
	if err := s.pool.Ping(r.Context()); err != nil {
		writePlainJSON(w, 503, map[string]string{"status": "db unreachable"})
		return
	}
	writePlainJSON(w, 200, map[string]string{"status": "ok"})
}
```

- [ ] **Step 4: Run to verify pass** — `go test ./internal/api/ -v` → PASS with DB; `go vet ./...` clean

- [ ] **Step 5: Commit** — `git add internal/api && git commit -s -m "feat: FHIR R4 API with atomic event+audit writes and read auditing"`

---

### Task 10: Demo web UI (`web/`)

**Files:**
- Create: `web/web.go`, `web/static/index.html`, `web/static/app.js`, `web/static/style.css`
- Modify: `internal/api/api.go` (mount static handler)

The UI is intentionally framework-free: three columns — patients (+ create form with NHI format choice and server-generated NHI), selected patient's notes (+ compose box), live audit trail (2s poll, hash prefixes, Verify Chain button with green/red banner). Header: "Kia ora — nz-open-emr walking skeleton" + actor selector. All `fetch` calls send `X-Actor-HPI`.

- [ ] **Step 1: `web/web.go`**

```go
// Package web embeds the static demo UI.
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var static embed.FS

func Handler() http.Handler {
	sub, err := fs.Sub(static, "static")
	if err != nil {
		panic(err) // embedded FS layout is fixed at compile time
	}
	return http.FileServer(http.FS(sub))
}
```

- [ ] **Step 2: `index.html`** — full content in repo; structure:

```html
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>nz-open-emr — walking skeleton</title>
<link rel="stylesheet" href="style.css">
</head>
<body>
<header>
  <h1>Kia ora — <span class="brand">nz-open-emr</span> <small>walking skeleton</small></h1>
  <label>Acting as: <select id="actor"></select></label>
</header>
<main>
  <section id="patients-panel">
    <h2>Patients</h2>
    <ul id="patient-list"></ul>
    <form id="new-patient">
      <h3>Register synthetic patient</h3>
      <input id="given" placeholder="Given name" required>
      <input id="family" placeholder="Family name" required>
      <input id="dob" type="date">
      <fieldset>
        <legend>NHI format</legend>
        <label><input type="radio" name="fmt" value="legacy" checked> legacy (AAA111#)</label>
        <label><input type="radio" name="fmt" value="new"> new (AAA11A#, from Jul 2026)</label>
      </fieldset>
      <div>NHI: <code id="nhi-preview">…</code></div>
      <button type="submit">Create patient</button>
    </form>
  </section>
  <section id="notes-panel">
    <h2 id="selected-patient">Select a patient</h2>
    <ul id="note-list"></ul>
    <form id="new-note" hidden>
      <textarea id="note-text" placeholder="Clinical note…" required></textarea>
      <button type="submit">Save note</button>
    </form>
  </section>
  <section id="audit-panel">
    <h2>Audit trail <button id="verify">Verify chain</button></h2>
    <div id="verify-result"></div>
    <ol id="audit-list" reversed></ol>
  </section>
</main>
<footer>Every read and write above is hash-chained. Try the tamper demo in the README.</footer>
<script src="app.js"></script>
</body>
</html>
```

- [ ] **Step 3: `app.js`** — complete implementation (~170 lines): `state {actor, selectedPatient}`; helpers `api(path, opts)` adding `X-Actor-HPI` + JSON handling, `b64encode/b64decode` (TextEncoder/TextDecoder, chunk-safe); `loadActors` → populate select; `refreshNHIPreview` on format change via `/demo/generate-nhi`; `loadPatients` (GET `/fhir/r4/Patient` Bundle → list items with NHI badge); create patient submit → POST FHIR Patient with identifier system `https://standards.digital.health.nz/ns/nhi-id`; `selectPatient` → GET notes Bundle → render with author + date; note submit → POST DocumentReference with base64 attachment; `pollAudit` every 2s → GET `/fhir/r4/AuditEvent?_count=30` → render `seq action resource actor hash[0:12]`; verify button → GET `/audit/verify` → green "chain intact (N events)" or red "BROKEN at seq N: reason" banner. Patient list refresh is retried once after 500 ms post-create (projection lag).

- [ ] **Step 4: `style.css`** — dark-on-light, 3-column grid (`grid-template-columns: 1fr 1.2fr 1fr`), monospace hashes, green/red verify banner, responsive collapse under 900px. Complete file in repo.

- [ ] **Step 5: Mount in API** — in `api.New` root mux: `root.Handle("/", web.Handler())` plus import. Static is open (no identity) — it contains no PHI.

- [ ] **Step 6: Verify** — `go build ./...`; manual check deferred to compose smoke test (Task 12).

- [ ] **Step 7: Commit** — `git add web internal/api && git commit -s -m "feat: embedded demo UI — patients, notes, live audit chain"`

---

### Task 11: `cmd/emrd/main.go`

**Files:**
- Create: `cmd/emrd/main.go`

- [ ] **Step 1: Implement**

```go
// emrd is the nz-open-emr walking-skeleton server: one binary, one
// Postgres, everything else embedded.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sekickivuk-hue/nz-open-emr/internal/api"
	"github.com/sekickivuk-hue/nz-open-emr/internal/audit"
	"github.com/sekickivuk-hue/nz-open-emr/internal/db"
	"github.com/sekickivuk-hue/nz-open-emr/internal/projection"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return errors.New("DATABASE_URL is required")
	}
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, url)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	// Refuse to serve over a corrupted audit chain.
	rep, err := audit.Verify(ctx, pool)
	if err != nil {
		return err
	}
	if !rep.OK {
		log.Error("audit chain verification FAILED — refusing to start",
			"brokenSeq", rep.BrokenSeq, "reason", rep.Reason)
		return errors.New("audit chain broken")
	}
	log.Info("audit chain verified", "events", rep.Checked)

	proj := &projection.Projector{Pool: pool, Log: log}
	go proj.Run(ctx)

	srv := &http.Server{Addr: addr, Handler: api.New(pool, proj)}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx)
	}()
	log.Info("emrd listening", "addr", addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
```

- [ ] **Step 2: Verify** — `go build ./... && go vet ./...` → clean

- [ ] **Step 3: Commit** — `git add cmd && git commit -s -m "feat: emrd entrypoint — migrate, verify chain, serve"`

---

### Task 12: Dockerfile + compose + smoke test

**Files:**
- Create: `Dockerfile`, `compose.yaml`

- [ ] **Step 1: `Dockerfile`**

```dockerfile
FROM golang:1.22 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /emrd ./cmd/emrd

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /emrd /emrd
EXPOSE 8080
ENTRYPOINT ["/emrd"]
```

- [ ] **Step 2: `compose.yaml`**

```yaml
services:
  db:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: emr
      POSTGRES_PASSWORD: emr   # local demo only; all data is synthetic
      POSTGRES_DB: emr
    ports:
      - "5433:5432"            # exposed for local integration tests
    volumes:
      - dbdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U emr -d emr"]
      interval: 2s
      timeout: 2s
      retries: 30
  emrd:
    build: .
    environment:
      DATABASE_URL: postgres://emr:emr@db:5432/emr
    ports:
      - "8080:8080"
    depends_on:
      db:
        condition: service_healthy
volumes:
  dbdata:
```

- [ ] **Step 3: Smoke test** — `docker compose up -d --build`, then:
  - `curl -s localhost:8080/healthz` → `{"status":"ok"}`
  - `curl -s localhost:8080/audit/verify` → `{"ok":true,"checked":0}`
  - Create patient via curl with `X-Actor-HPI: 99ZZZA`, verify 201; `curl localhost:8080/audit/verify` → checked ≥ 1
  - Open `http://localhost:8080` → UI loads
  - Run full local test suite: `TEST_DATABASE_URL=postgres://emr:emr@localhost:5433/emr go test ./...` → all PASS, none skipped

- [ ] **Step 4: Commit** — `git add Dockerfile compose.yaml && git commit -s -m "feat: docker compose — emrd + postgres, one command up"`

---

### Task 13: GitHub Actions CI

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Write workflow**

```yaml
name: ci
on:
  push:
    branches: [main]
  pull_request:
jobs:
  test:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:16-alpine
        env:
          POSTGRES_USER: emr
          POSTGRES_PASSWORD: emr
          POSTGRES_DB: emr
        ports: ["5432:5432"]
        options: >-
          --health-cmd "pg_isready -U emr -d emr"
          --health-interval 2s --health-timeout 2s --health-retries 30
    env:
      TEST_DATABASE_URL: postgres://emr:emr@localhost:5432/emr
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.22"
      - run: go vet ./...
      - run: go test ./... -count=1
      - run: go build ./...
  docker:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: docker build -t nz-open-emr:ci .
```

- [ ] **Step 2: Commit + push, watch run** — `git add .github && git commit -s -m "ci: vet, test (with postgres), docker build"`, `git push`, `gh run watch` → green.

---

### Task 14: README quickstart + tamper walkthrough, push, verify

**Files:**
- Modify: `README.md` (replace "Code is coming" section with quickstart)

- [ ] **Step 1: Add to README** — quickstart (`docker compose up`, open localhost:8080), curl examples for the FHIR API, and the tamper demo:

```bash
# Tamper with history, then watch verification catch it:
docker compose exec db psql -U emr -d emr \
  -c "UPDATE audit_events SET actor_hpi = 'EVIL' WHERE seq = 2"
curl -s localhost:8080/audit/verify
# → {"ok":false,"checked":1,"brokenSeq":2,"reason":"hash mismatch"}
# (restart emrd and it will refuse to boot on the corrupted chain)
docker compose down -v   # reset demo data
```

Also: status section update ("walking skeleton is live"), architecture-at-a-glance for the skeleton, link to spec + plan docs.

- [ ] **Step 2: Final verification (superpowers:verification-before-completion)** — full suite green incl. integration; compose smoke test passes; CI green on GitHub; UI flow works end-to-end in browser.

- [ ] **Step 3: Commit + push** — `git add README.md && git commit -s -m "docs: quickstart and tamper-evidence walkthrough" && git push`

---

## Self-review (done at write time)

- **Spec coverage**: patient create (T9), note (T9), audit chain + verify + tamper (T4, T9), dual NHI (T2), event sourcing (T5), projections (T6), FHIR surface (T7, T9), stub identity (T8), UI (T10), startup chain check (T11), compose (T12), CI (T13), README/tamper demo (T14). Spec's protobuf line — recorded deviation in header.
- **Type consistency**: `audit.Entry`/`ChainHash` signature consistent T4↔T9; `eventstore.Event` pointer API consistent T5↔T6↔T9; `projection.Projector{Pool}` with `Step` consistent T6↔T9↔T11.
- **Placeholder scan**: Task 10 steps 3–4 describe JS/CSS behaviourally rather than inline (deliberate: full listings land in repo; behaviour is fully specified). All Go code complete.
