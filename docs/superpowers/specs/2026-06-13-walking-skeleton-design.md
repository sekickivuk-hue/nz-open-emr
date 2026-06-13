# Walking Skeleton — Design (approved 2026-06-13)

First runnable artefact for nz-open-emr: `v0.0.1` — "first heartbeat". Proves the riskiest architectural seams from PLAN.md (event sourcing, hash-chained audit, FHIR R4 surface, dual-NHI) in the smallest honest form.

## Goal

After `git clone && docker compose up`, a visitor can, in under two minutes on modest hardware:

1. Open `http://localhost:8080`, act as a synthetic clinician ("Dr Demo", stub HPI), create a **synthetic patient** with a fake NHI in either `AAA111#` (legacy) or `AAA11A#` (new) format.
2. Write a **clinical note** for that patient.
3. Watch the **audit panel** update live: every read and write is an `AuditEvent`, hash-chained (BLAKE3) to the previous event. A "verify chain" button re-computes the chain and shows it is intact. Docs include a tamper demo: hand-edit a row in Postgres → verification reports the exact break point.

Item 3 is the emotional core: *the system can prove nobody silently altered the record.*

## Decisions (made during brainstorming)

| Decision | Choice | Why |
|---|---|---|
| Demo surface | FHIR API + tiny embedded web UI | Developers get the API; clinicians get something they can see. |
| Internals | Event-sourced from day one | The skeleton exists to prove the architecture, not a CRUD stand-in. |
| Identity | Stub actor header, no login | Proves "every event has an actor" without dragging Keycloak into a frugal demo. Swappable package. |
| Composition | Single Go binary (`emrd`) + Postgres 16, two containers | PLAN.md principle #5 (modular monolith first); runs on old hardware. |
| Rejected | Mini-microservices (5+ containers, contradicts monolith-first); HAPI FHIR core (Java heavyweight, validates nothing of the Go/event design) | |

## Architecture

```
docker compose
├── emrd (Go 1.22+, one binary, distroless image)
│   ├── internal/eventstore   — append-only events table; the only writer
│   ├── internal/audit        — BLAKE3 hash chain; verification
│   ├── internal/projection   — goroutine: events → read models (patients, notes)
│   ├── internal/identity     — resolves X-Actor-HPI header → actor; future OIDC swap point
│   ├── internal/fhir         — FHIR R4 mapping: Patient, DocumentReference, AuditEvent, Bundle, OperationOutcome
│   ├── internal/api          — chi router: /fhir/r4/*, /audit/verify, /healthz, demo endpoints
│   └── web/                  — embedded static demo UI (vanilla HTML/JS, no build step)
└── postgres:16
```

### Write path
`POST /fhir/r4/Patient` → validate (incl. NHI format) → in one Postgres transaction: append domain event (`PatientRegistered`; payload protobuf-encoded `bytea` + `jsonb` mirror for inspectability) + append hash-chained `AuditEvent` → projection goroutine updates read table → `201` with FHIR resource.

### Read path
`GET /fhir/r4/Patient/{id}` reads the projection **and appends a read-access `AuditEvent`**. Record-level read auditing starts here (field-level comes later per PLAN.md A1).

### Audit chain
`audit_events(seq BIGSERIAL, prev_hash BYTEA, hash BYTEA, actor_hpi TEXT, action TEXT, resource_type TEXT, resource_id TEXT, at TIMESTAMPTZ, detail JSONB)` with `hash = BLAKE3(prev_hash ‖ canonical_event_bytes)`. `GET /audit/verify` walks the chain, returns OK or the first divergent seq. On startup, `emrd` verifies the persisted chain head and refuses to start on mismatch.

### NHI handling
7-char strings; validator accepts both `AAA111#` and `AAA11A#` formats (with check-digit logic per HISO spec); stored with a format-version flag. Synthetic NHI generator for demo data; clearly marked synthetic range.

## Scope fences (YAGNI)

**In**: the flow above; FHIR JSON validation for the three resources; dual-NHI generate/validate; chain verification; docker compose; GitHub Actions CI (build + test); README demo script incl. tamper walkthrough.

**Out** (explicitly): OpenEHR (subject of first RFC), search beyond `Patient?identifier=`, resource update/delete, consent, Te Reo UI (greeting only), TLS/mTLS, NATS, Keycloak, edge/offline mode, Rust components.

## Testing

TDD throughout. Unit: NHI validation (both formats, check digits, rejects), event append/replay determinism, hash-chain verify incl. tamper detection (corrupt a row → exact break point reported). Integration: full patient → note → read → verify flow against real Postgres in CI. The tamper test is the flagship.

## Error handling

- All API errors return FHIR `OperationOutcome` with correct HTTP status.
- Startup refuses to serve if audit chain head fails verification.
- Projection failures log loudly and retry; events are never lost (log is source of truth).
- Postgres unavailable → `/healthz` fails; compose `depends_on: condition: service_healthy`.

## License & provenance

Code AGPL-3.0-or-later; DCO sign-off on all commits.
