# Handoff — 2026-06-14

## What we built today

**Foundation: pluggable module system**
- `internal/projection/` — Handler interface + Register() dispatch table. Adding a module = one Go file + one import in main.go. Zero core changes.
- `internal/eventstore/` — now truly minimal: only Event/Append/ListAfter + PatientRegistered + NoteCreated. Module payloads live in modules.

**HISO 10046:2025 compliance**
- NHI check-digit algorithm fixed: mod 24 → mod 23 for new format (the exact bug the 2025 revision fixed)
- Patient model extended with: ethnicity (level-4, up to 6), iwi (Stats NZ), NZ citizenship (status + source), birth country/place, DHB code — all as FHIR extensions per NHI IG v1.6.5

**API surface (all callable via curl + demo UI)**
| Resource | Endpoints |
|---|---|
| Patient | POST, GET /{id}, GET (list) |
| Encounter | POST, GET /{id}, GET (list), POST /{id}/diagnosis, POST /{id}/close |
| AllergyIntolerance | POST, GET /{id}, GET (list), POST /{id}/remove |
| Condition | POST, GET /{id}, GET (list), POST /{id}/resolve |
| RelatedPerson | POST, GET (list), POST /{id}/remove |
| CareTeam | POST, GET (list), POST /{id}/remove |

**Demo UI** — tabs for Patient, Encounters, Allergies, Problems. Live at `http://localhost:8080`.

**Proven:** Encounter close → discharge diagnosis auto-promoted to problem list. Tamper-evident audit chain through all of it.

## Current state

```
Branch: main (c5a9ff8)
Docker: docker compose up at localhost:8080
Tests: 8 packages pass, 5 modules untested
```

## Where to continue

**Immediate (Path B — UI deepening):**
- The demo UI doesn't show diagnoses on reopened encounters (they're stored but the GET encounter response doesn't include the diagnosis list — need to join encounter_diagnoses in the API)
- Add a "Past History" view on the Problems tab filtering by `past_history` table
- Show family connections and care team in the UI

**Next module to API-wire:**
- Medications module (not yet created — follow patterns from `module/allergies/`)
- Social history module
- Family history (separate from family connections — this is about conditions running in the family)

**Infrastructure (Phase 1 prep):**
- OIDC identity to replace demo actors
- Terminology binding (SNOMED CT codes via NZHTS)
- FHIR conformance validation (HISO 10110)

**Known bugs:**
- Demo UI: encounter GET doesn't return diagnoses in the response (diagnoses are stored in encounter_diagnoses table but not joined in the API)
- Projection lag: 200ms polling means UI needs setTimeout(…, 400) retries after writes

## Running locally

```bash
cd /tmp/nz-open-emr
docker compose down -v   # clean reset
DOCKER_BUILDKIT=0 docker compose up -d --build
open http://localhost:8080
```

Run tests: `GOMODCACHE=/tmp/gomodcache GOCACHE=/tmp/gobuildcache GOPATH=/tmp/gopath go test -p 1 ./...`
