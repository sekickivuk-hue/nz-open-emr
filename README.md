# nz-open-emr (working name)

**An open-source national Electronic Medical Record for Aotearoa New Zealand — free, crowd-sourced, and built to run on the hardware clinics already own.**

> **Status: walking skeleton is live.** `docker compose up` gives you a FHIR R4 API and a small web UI: register a synthetic patient, write a clinical note, and watch a tamper-evident audit chain grow in real time. The architecture plan is [PLAN.md](PLAN.md); the skeleton proves its riskiest seams.

> ⚠️ **Not official. Not affiliated with Health New Zealand.** This is an independent, personal, non-commercial open-source project by a practising health worker who got tired of slow, expensive, frustrating clinical software. It is **not** endorsed by, connected to, or representing Health New Zealand | Te Whatu Ora, the Ministry of Health, the NHI/HPI operators, or any official health system, agency, or commercial vendor. The word "national" describes the project's *ambition*, not any official status. It is **not approved for clinical use** and handles **synthetic test data only**. See the [Disclaimer](#disclaimer) below.

## Quickstart (2 minutes)

```bash
git clone https://github.com/sekickivuk-hue/nz-open-emr.git
cd nz-open-emr
docker compose up -d --build
open http://localhost:8080
```

Pick a synthetic clinician, register a patient (the NHI is generated in either the legacy `AAA111#` or post-July-2026 `AAA11A#` format — both validated per HISO 10046), write a note, and watch the audit panel: every read and write is an `AuditEvent`, BLAKE3-hash-chained to the previous one. Press **Verify chain**.

Prefer curl? The API is plain FHIR R4:

```bash
curl -s -X POST localhost:8080/fhir/r4/Patient \
  -H 'X-Actor-HPI: 99ZZZA' -H 'Content-Type: application/json' \
  -d '{"resourceType":"Patient",
       "identifier":[{"system":"https://standards.digital.health.nz/ns/nhi-id","value":"ZZZ0016"}],
       "name":[{"family":"Skeleton","given":["Walking"]}]}'
curl -s localhost:8080/audit/verify
```

### The tamper demo

The point of the audit chain is that **nobody — not even the database administrator — can silently alter the record**:

```bash
# Edit history behind the system's back:
docker compose exec db psql -U emr -d emr \
  -c "UPDATE audit_events SET actor_hpi = 'EVIL' WHERE seq = 2"

# Verification pinpoints the exact broken row:
curl -s localhost:8080/audit/verify
# → {"ok":false,"checked":1,"brokenSeq":2,"reason":"hash mismatch"}

# And emrd refuses to boot over a corrupted chain:
docker compose restart emrd && docker compose logs emrd | tail -2
# → "audit chain verification FAILED — refusing to start"

docker compose down -v   # reset the demo
```

### What the skeleton proves (and what it doesn't)

In: event-sourced clinical record (append-only `events` table is the source of truth; patient/note views are projections), atomic event+audit transactions, record-level read auditing, dual-NHI validation, FHIR R4 surface, one Go binary + Postgres — runs in ~20 MB of RAM. Out (for now, by design): OpenEHR storage (first public RFC), real OIDC login, consent, TLS, search. See [docs/superpowers/specs](docs/superpowers/specs/) for the spec and scope fences.

Run the tests: `docker compose up -d db && TEST_DATABASE_URL=postgres://emr:emr@localhost:5435/emr go test -p 1 ./...`

## The idea

New Zealand's health software is fragmented and expensive. Hospitals, GPs, and pharmacies run disconnected commercial systems with per-seat licensing; patient information moves between them by referral letters and faxes-with-extra-steps. This project exists to build the alternative:

- **A complete, free practice EMR** that an individual GP practice can install on a single Linux box and use as their day-to-day system — consults, notes, prescribing, results, referrals — replacing a paid commercial PMS.
- **A national, connected record** that those practices (and hospitals) join with one command, giving clinicians the whole picture and patients access to their own record.
- **A national research capability** over de-identified data, gated by explicit per-citizen consent, where queries run inside a trusted environment and only aggregates leave.

### Why I'm building this

Over fifteen years I've practised medicine in three countries and worked in at least ten different EMRs. That's an unusual vantage point: once you've used that many systems you stop assuming the software you're handed is the best anyone can do — because you've seen, first-hand, what good and bad actually look like.

In the United States, well-built software let me carry 15–20 patients largely on my own — admissions, discharge summaries, prescriptions — because it got out of my way. Working in New Zealand, I've leaned much more on the support of junior colleagues to manage a comparable load. The clinicians here are every bit as capable; the difference is the tooling, which too often adds friction where it should be removing it.

The lesson is **not** "American software is good." Those streamlined US systems come with enormous licence costs and absurd hardware requirements — not because the problem is hard, but because the code is written to generate revenue, not to be fast, frugal, or kind to the person using it. Bloat is a business model.

This project inverts that:

- The interface is **doctor-led** — designed by the people who actually live in it for ten hours a day, around clinical reality rather than billing codes.
- The code underneath is **led by security experts and engineers** with one obsession: do the most clinical work on the least hardware — and give it away for free.

### Why build it, instead of just paying Epic or Microsoft?

Because our data should stay ours, and the benefits should stay here. When a country buys a foreign platform it outsources two things at once: its patients' records *and* the engineering talent that could have been employed at home. Build it in the open instead, and the equation flips — for everyone:

- **Patients** keep ownership of their own record and a real say in how it's used.
- **Doctors** get safe, genuinely user-friendly software shaped around clinical work rather than around a licence fee — and a far lower barrier to independent practice. Opening a private practice today means climbing a wall of compliance and IT-infrastructure cost before you see a single patient; take the software cost off that wall and more clinicians can practise on their own terms, which ultimately means more affordable care.
- **The IT and software sector** gains skilled, onshore jobs building and maintaining national health infrastructure, instead of sending licence fees overseas year after year.
- **The country** keeps its health data sovereign and safe — *ours, and only ours* — and stays independent of any single corporation that could one day raise the price or pull the plug.

And the longer horizon is bigger than New Zealand. There is nothing NZ-specific about the core of this system. A free, frugal, open EMR that runs well on cheap, old hardware is exactly what under-resourced health systems everywhere need most — especially across the developing world, where commercial licences and data-centre requirements are simply out of reach. If it works here, it can be adapted anywhere.

That's the ultimate ambition: not just better software for one country, but a public good any country can pick up, translate to its own standards, and make its own — health software as shared human infrastructure rather than something rented from a handful of corporations.

Designed for a 20–50 year lifespan: OpenEHR as the canonical clinical model, FHIR R4 (NZ Base) for exchange, event-sourced storage in PostgreSQL, Go services, all on Linux, all open standards. Frugal by design — the target floor for a full GP-clinic deployment is a ten-year-old 4-core machine with 8 GB of RAM.

Read the full architecture: **[PLAN.md](PLAN.md)**

## Why AGPLv3?

This is a crowd-sourced public good. The AGPLv3 license means anyone can use, study, and improve the system for free — but anyone who modifies it and offers it as a service must publish their changes. You can build on it; you cannot capture it. Client SDKs and integration libraries will be Apache 2.0 so commercial lab, pharmacy, and practice systems can integrate freely. Specifications and clinical models will be CC-BY 4.0.

Contributions are accepted under the [Developer Certificate of Origin](https://developercertificate.org/) — contributors keep their copyright, which means no single entity (including the project founder) can ever relicense this to closed source.

## Where the project is and how to help

No single person can build a national EMR. Right now the most valuable contributions are:

1. **Review the architecture** — open an issue challenging any decision in [PLAN.md](PLAN.md). NZ health-IT experience especially welcome (FHIR, OpenEHR, HL7v2, NHI/HPI integration, Medtech/Indici internals).
2. **Clinical input** — GPs, SMOs, pharmacists, nurses: tell us what your software gets wrong. The plan's known gaps (clinical safety case, governance, migration strategy) are listed at the end of PLAN.md.
3. **Run the skeleton and break it** — `docker compose up`, try the tamper demo, file issues for anything surprising. First good contribution targets: the OpenEHR engine RFC, FHIR validation depth, and the items in PLAN.md's "Known gaps".

## Roadmap (high level)

| Phase | What |
|---|---|
| 0 (now) | Architecture review in the open; governance bootstrapping; walking skeleton |
| 1 | Core platform: identity, patient index (dual-NHI), terminology, FHIR gateway, audit, consent, clinical record core |
| 2 | Orders & results with closed-loop sign-off, medications, scheduling, referrals |
| 3 | Imaging, pharmacy app, patient portal |
| 4 | National research tier (consent-gated, trusted research environment) |

Full sequencing and rationale in [PLAN.md](PLAN.md).

## Disclaimer

This is an independent, non-commercial open-source project maintained in a personal capacity by a New Zealand health worker. It exists out of frustration with the clinical software we use every day, and a belief that we can do better in the open. It is a good-faith side project, nothing more official than that.

**It is not an official product.** It is not affiliated with, endorsed by, sponsored by, or connected to:

- Health New Zealand | Te Whatu Ora, the Ministry of Health, or any government agency;
- the National Health Index (NHI) or Health Provider Index (HPI) operators;
- any commercial EMR/PMS vendor (e.g. Medtech, Indici) or any officially sanctioned national health-record programme.

References to NZ health standards (HISO, HL7 NZ Base FHIR, NHI/HPI identifier formats) are included for interoperability and educational purposes only and do not imply any endorsement, partnership, or approval. The word "national" in the name reflects the project's aspiration, not any official designation.

This software is provided "as is", for research, evaluation, and educational use, and is **not certified or approved for use in real patient care**. It currently operates on **synthetic test data only — do not enter real patient information.** Any views expressed here are the author's own and not those of any employer.

## License

- Platform code: [AGPL-3.0-or-later](LICENSE)
- Documentation and specifications: CC-BY 4.0
- Contributions: [DCO](https://developercertificate.org/) sign-off required (`git commit -s`)
