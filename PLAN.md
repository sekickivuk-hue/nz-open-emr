# National EMR for Aotearoa New Zealand — Architecture Plan

## Context

Reference architecture for an open-source national Electronic Medical Record for New Zealand. Scope covers hospital clinicians, GPs, pharmacists, and patients, with modules for pharmacy, imaging integration, and other clinical workflows.

Hard constraints set by the requester:

- **FOSS, capture-resistant** — crowd-sourced national system; licensing chosen so people contribute back rather than take and commercialise (see *Licensing & community model*)
- **Efficient and minimal resources** — runs well on **old and recycled hardware**, not just current commodity gear; resource frugality is a first-class design constraint, not an optimisation
- **Reliable** and **military-grade secure**
- **Free** at point of use
- **Linux-only** — every component runs on Linux; no Windows or macOS server dependencies anywhere in the critical path
- **Backend in Go**, primary store in **PostgreSQL**
- **Future-proof for 20–50 years** — data model and APIs must remain evolvable without rewrites
- **Built-in national research queries** — de-identified data usable for population health research, governed by explicit per-citizen consent
- **Empower private practices** — an individual private physician practice gets a complete, free practice EMR (the software they'd otherwise license commercially), with national integration and collaboration built in — not merely an edge terminal of the national system

This document is the **high-level architecture map** — it names the subsystems, fixes the cross-cutting concerns, picks the stack, and sequences the build. Each subsystem will get its own detailed spec before implementation.

---

## NZ-specific context (current as of May 2026)

| Item | Status | Architectural implication |
|---|---|---|
| **NHI format change** | New `AAA11A#` format issued from **1 July 2026**; old `AAA111#` keeps validity | MPI + every API must validate and persist **both formats**; never reject `AAA111#`. Storage as 7-char string with format-version flag. |
| **NHI capacity** | New format = 33M unique IDs | Plenty of headroom; assume opaque identifiers, no semantic parsing |
| **FHIR baseline** | NZ Base IG on **FHIR R4** today; R5 not yet adopted | Build on R4 with NZ Base profiles; design data layer **FHIR-version-agnostic** so R5/R6 upgrade is a profile swap, not a rewrite |
| **Identifier services** | NHI FHIR API, HPI FHIR API live on Health NZ Digital Services Hub | Architecture treats these as authoritative external services; do not re-issue NHI/HPI |
| **Terminology** | NZ Health Terminology Service (NZHTS) — SNOMED CT NZ Edition, NZULM, NZPOCS, LOINC | Use NZHTS via FHIR terminology operations; do not host a parallel terminology authority |
| **Patient summary** | NZ Patient Summary (NZPS) = conforming NZ adaptation of HL7 International Patient Summary | NZPS is the export format for cross-organisation and cross-border summaries |
| **Hira / SDHR** | Hira federation funding withdrawn 2024; Shared Digital Health Record build underway with Middleware NZ | Greenfield reference architecture, but design must align cleanly with SDHR FHIR contracts |
| **Identity** | My Health Account (citizen) and My Health Account Workforce (clinician) provide federated identity via OIDC | Use as upstream identity provider where it fits; do not duplicate identity onboarding |

---

## Non-functional requirements

| NFR | Target |
|---|---|
| Population served | ~5.3 M (NZ) |
| Active clinicians | ~80 k |
| Concurrent users at peak | ~30 k |
| Clinical encounters/year | ~30 M |
| Availability | 99.99% target (≤ 53 min/yr unplanned downtime) for read; 99.95% for write |
| RPO (clinical data) | ≤ 60 seconds |
| RTO | ≤ 15 minutes |
| Read latency p95 | < 200 ms for a patient record summary |
| Write latency p95 | < 400 ms for a clinical note save |
| Data residency | All PHI stored in NZ; legal sovereign control |
| Audit | Every PHI read + write logged, tamper-evident, hash-chained, off-host within 60 s |
| Cost | FOSS; runs on commodity **and aged/recycled** x86_64 + ARM64 Linux; no per-seat licensing |
| Minimum edge hardware | Full GP-clinic edge profile must run on a ~10-year-old 4-core box with 8 GB RAM and SATA SSD |
| Longevity | Data readable in 50 years with no proprietary dependency |
| OS | 100% Linux (Debian stable or Ubuntu LTS); no Windows/macOS in production |

---

## Guiding principles

1. **OpenEHR archetypes are the canonical clinical model; FHIR is the exchange format.** OpenEHR is purpose-built for multi-decade clinical data persistence (used by Norway's national platform, Slovenia, parts of NHS). FHIR R4 is great for APIs but is a wire format, not a 50-year persistence model. Storing in OpenEHR with FHIR projection gives evolvable semantics without DB rewrites.
2. **Event sourcing for clinical records.** Append-only event log is the source of truth; current state is a projection. Schema changes never lose history.
3. **PostgreSQL is the database.** Clinical events in partitioned tables; OpenEHR compositions in `jsonb` + extracted indexed columns; FHIR projections built by background workers. Patroni for HA, pgBackRest for backups.
4. **Open standards only.** FHIR R4 (NZ Base), OpenEHR, SNOMED CT NZ, LOINC, NZULM, NZPOCS, ICD-10-AM, IHE imaging profiles, IPS/NZPS. No proprietary clinical schemas anywhere.
5. **Modular monolith first.** Each subsystem is a separate Go module behind a clear internal API; ship as one binary initially; split only when forced by scale or independent deploy cadence.
6. **Zero-trust networking.** mTLS everywhere, OAuth 2.1 + OIDC + SMART on FHIR for every API call. No "internal trusted network" assumption.
7. **Audit everything.** Append-only, hash-chained per stream, Merkle-rooted weekly, anchored to an external timestamping authority. Off-host within 60 s.
8. **Sovereign hosting on Linux.** NZ-based infrastructure (Catalyst Cloud or AoG-listed providers), all Debian stable or Ubuntu LTS.
9. **Boring, well-supported tech.** Nothing exotic on the PHI path.
10. **Plan for the language to change.** Go today is right, but a 50-year system will outlive any one language. Keep services single-purpose and replaceable; protocol contracts (FHIR / OpenEHR / protobuf) are the long-lived artefacts.

---

## Licensing & community model

Core goal: a **crowd-sourced national system**, free at every level, where the licensing actively pushes people to contribute code back rather than take it and commercialise it.

| Artefact | License | Why |
|---|---|---|
| **Core platform** (all server-side services, apps, edge node) | **AGPLv3** | Closes the SaaS loophole: anyone offering the system over a network — including a vendor selling a hosted fork — must publish their modifications. Deters proprietary capture; costs hospitals/clinics running it nothing (use is unrestricted). Same choice as Grafana, Mastodon, Nextcloud. |
| **Client SDKs, integration libraries, event/schema definitions** | **Apache 2.0** | Third parties (lab systems, pharmacy software, AI vendors) must be able to embed these in their own products or the interop ecosystem never forms. Apache over MIT for the explicit patent grant — health IT has real patent-litigation history. AGPL core can consume Apache code freely (compatibility is one-way in the direction we need). |
| **Specs, FHIR profiles, OpenEHR archetypes, clinical workflow docs** | **CC-BY 4.0** | The contracts are the 50-year artefact, not the code. Content license, maximal reuse. (openEHR International itself uses CC-BY.) |

Community mechanics that matter as much as the license:

- **Contributions via DCO, not CLA.** Contributors keep copyright (sign-off on commits). No single entity can ever relicense the project to closed — a credible 50-year commitment device, and a trust signal that attracts contributors.
- **Trademark held separately** (trust / incorporated society / Crown). AGPL lets anyone fork; the trademark guarantees that anything *calling itself* the national EMR passed conformance testing. (Mozilla/Rust model.)
- **NZGOAL-SE compatibility**: the NZ government open-licensing framework defaults to MIT but explicitly permits GPL-family where copyleft serves the public interest. The capture-prevention argument above is that written justification.

---

## Subsystem map

The system decomposes into 18 modules across five bands. Each module has a clear purpose, a small set of clinical resources it owns, and a stable external interface.

### Band 1 — Core platform (build first)

| # | Module | Purpose | Owns |
|---|---|---|---|
| 1 | **IAM** | Clinician + patient auth, RBAC/ABAC, break-glass, consent enforcement | `Practitioner`, `PractitionerRole`, `Consent` |
| 2 | **Patient Master Index (MPI)** | NHI assignment + lookup (both `AAA111#` and `AAA11A#` formats), deduplication, demographics | `Patient`, `RelatedPerson` |
| 3 | **Terminology service adapter** | Wraps NZ Health Terminology Service; provides FHIR `$validate-code`, `$expand`, `$translate` | (proxy to NZHTS) |
| 4 | **FHIR API gateway** | External FHIR R4 endpoint, NZ Base profile validation, search | (façade across all) |
| 5 | **Audit & security event log** | Tamper-evident, hash-chained, Merkle-anchored, off-host shipping | `AuditEvent`, `Provenance` |
| 6 | **Clinical record core** | Encounters, problems, allergies, observations, notes — OpenEHR canonical storage with FHIR projection | `Encounter`, `Condition`, `AllergyIntolerance`, `Observation`, `DocumentReference` |
| 7 | **Consent service** | Per-citizen consent: clinical sharing, research participation, secondary use; versioned, revocable | `Consent`, `ResearchConsent` (custom profile) |

### Band 2 — Clinical workflow modules

| # | Module | Purpose | Owns |
|---|---|---|---|
| 8 | **Orders & results** | Lab + imaging order entry, result delivery, ack | `ServiceRequest`, `DiagnosticReport`, `Specimen` |
| 9 | **Medications** | Prescribing, dispensing, interactions, NZULM lookup, PHARMAC funding flags | `MedicationRequest`, `MedicationDispense`, `MedicationStatement` |
| 10 | **Imaging integration** | DICOMweb gateway (QIDO/WADO/STOW-RS), study↔report linkage | `ImagingStudy`, `Media` |
| 11 | **Scheduling** | Appointments, theatre lists, bed management | `Appointment`, `Slot`, `Schedule` |

### Band 3 — User-facing apps

| # | Module | Purpose |
|---|---|---|
| 12 | **Clinician web app** | PWA: hospital clinicians + GPs; consult, document, order, prescribe |
| 13 | **Patient portal** | PWA: view record, repeat scripts, book, secure messaging, results, **manage research consent** |
| 14 | **Pharmacy app** | Community + hospital pharmacy: dispense, claim, stock, MUR |

### Band 4 — National research tier

| # | Module | Purpose | Owns |
|---|---|---|---|
| 15 | **De-identification pipeline** | Streams clinical events → research lakehouse with consent gating, pseudonymisation, k-anonymity bucketing | — |
| 16 | **Trusted Research Environment (TRE)** | Researchers run queries **inside** the secure environment; no row-level egress; differential privacy on aggregates; HDEC-approved protocols only | `ResearchStudy`, `ResearchSubject` |

### Band 5 — Cross-cutting / operational

| # | Module | Purpose |
|---|---|---|
| 17 | **Notifications / messaging** | Secure clinician messaging, patient comms (SMS/email/push) |
| 18 | **Deployment & observability** | CI/CD, infra-as-code, metrics/logs/traces, DR drills, key management |

---

## Tech stack

| Layer | Choice | Rationale |
|---|---|---|
| Operating system | **Debian stable** (production) / **Ubuntu LTS** (alternative) | Long support, FOSS, dominant in regulated environments |
| Primary backend language | **Go 1.22+** | Small binaries, low memory, fast compile, strong concurrency, simple operationally |
| **Critical-path language** | **Rust** for: audit chain, crypto envelope service, terminology engine hot path, FHIR validator core | Memory safety without GC, zero-cost abstractions, used in defence-grade systems. Where a memory bug equals a CVE, Rust earns its complexity. |
| Database | **PostgreSQL 16** | Mature, FOSS, partitioning, logical replication, `jsonb`+GIN for OpenEHR/FHIR storage |
| HA / replication | **Patroni** + **etcd** + **pgBackRest** | Battle-tested Postgres HA; PITR backups |
| Sharding (only if needed) | **Citus** extension | Stay in Postgres world; avoid second engine |
| Canonical clinical model | **OpenEHR** archetypes + templates | Designed for multi-decade clinical persistence; archetype evolution does not require DB migration |
| Exchange format | **HL7 FHIR R4** (NZ Base IG) | NZ standard; R5/R6 upgrade is profile swap on top of OpenEHR canonical storage |
| Search | **PostgreSQL FTS + `pg_trgm`** first; OpenSearch only if proven needed | Avoid second store unless forced |
| Cache | **Redis** (with persistence) or **Valkey** (FOSS fork) | Sessions, hot terminology lookups, idempotency keys |
| Message bus | **NATS JetStream** | Lighter than Kafka, durable, ordered, sufficient for clinical event flow |
| Identity provider | **Keycloak** (federate to My Health Account / My Health Account Workforce) | OIDC + SMART on FHIR + WebAuthn |
| DICOM gateway | **Orthanc** behind a thin Go façade | Battle-tested FOSS DICOMweb |
| Frontend | **TypeScript + SvelteKit** as PWA | Small bundles, good offline story for patchy hospital networks |
| Mobile | Same PWA, plus thin **Capacitor** wrappers for iOS/Android stores | One codebase, three surfaces |
| Container runtime | **Podman** (rootless) or containerd | Daemonless option; no proprietary dependency |
| Orchestration | **k3s** (lightweight Kubernetes) | HA-capable, low overhead, OCI-standard, runs on commodity hardware |
| Service mesh | **Linkerd** | Lighter than Istio; mTLS by default; written in Rust (control + proxy) |
| Secrets | **HashiCorp Vault** (BSL but FOSS-equivalent options: **OpenBao**, the FOSS fork) | HSM-backed root keys |
| Key management | **HSM** (Thales / YubiHSM / sovereign equivalent) for root-of-trust | Hardware-backed key custody |
| Observability | **OpenTelemetry → Grafana / Tempo / Loki / Prometheus / Mimir** | All FOSS, integrates cleanly |
| CI/CD | **Forgejo** (FOSS GitLab/GitHub alternative) on sovereign infra; mirror to GitHub for community PRs | Resilience against single-vendor disruption |
| Build provenance | **SLSA Level 4**, **reproducible builds**, SBOMs in CycloneDX | Supply-chain integrity |
| Formal methods | **TLA+** specs for the audit chain, consent enforcement, and break-glass state machines | Prove correctness of safety-critical state transitions |

---

## Future-proofing for 20–50 years

Decisions that make the system survive the languages, frameworks, and humans who built it.

1. **Canonical model lives in OpenEHR + an event log, not in language-specific structs.** OpenEHR archetypes are XML/JSON definitions maintained internationally; they are not coupled to Go, Rust, or any framework. The event log is append-only protobuf records. A future implementation in a not-yet-invented language can replay the same data.
2. **Event sourcing means schema changes are additive.** Adding a new field never requires migrating old rows. Removing one means writing a projection that ignores it. There are no destructive migrations on PHI.
3. **Versioned APIs forever.** Every external endpoint is `/fhir/r4/...`, `/fhir/r5/...` etc. We never break v1; we add v2. Deprecation cycles measured in years.
4. **No vendor lock-in.** Every component is FOSS with at least one viable fork or alternative. No SaaS in the critical path. No cloud-provider-specific services.
5. **Multi-implementation friendly.** Protocol specs (FHIR profiles, OpenEHR archetypes, audit log format) are the durable artefacts. The Go services are a *current* implementation, not the system itself. A future Rust or Zig or Whatever rewrite of any subsystem is feasible because the contracts are external.
6. **Plain-text-readable archives.** Annual archive snapshots written as FHIR R4 bundles + OpenEHR XML on encrypted append-only storage. Even if every running service died tomorrow, a clinician with the decryption keys could read the data with off-the-shelf tools.
7. **Hardware portability.** x86_64 and ARM64 from day one; no architecture-specific code outside clearly isolated SIMD paths.
8. **No mandatory binary blobs.** Every dependency must build from source on Debian stable.
9. **Documented physics, not just code.** Every clinical workflow has a written specification independent of the implementation — a doctor reading the spec in 2070 can reconstruct the intent.
10. **Cryptographic agility.** All crypto goes through a single envelope service (Rust). Algorithms are configurable. Post-quantum migration is a config change + key roll, not a rewrite. ML-KEM / ML-DSA support targeted for 2027.

---

## Cross-cutting concerns

### Identity

- **Citizens** identified by NHI. Both `AAA111#` (legacy) and `AAA11A#` (from 1 Jul 2026) formats stored as 7-char strings with a format-version flag. MPI calls the Health NZ NHI FHIR API; never re-issues an NHI.
- **Clinicians** identified by HPI-Person; **facilities** by HPI-CPN. Looked up via HPI FHIR API.
- Federate citizen auth to **My Health Account**; clinician auth to **My Health Account Workforce**. Keycloak sits in front as the local broker.
- Every API call carries a clinician identity + purpose-of-use + (where relevant) consent assertion. No anonymous internal traffic.

### Terminology

- All clinical concepts MUST reference SNOMED CT NZ, LOINC, NZULM, NZPOCS, or ICD-10-AM. Free text is supplementary, never primary.
- The terminology adapter calls **NZ Health Terminology Service** for binding validation, expansion, translation. Local cache is read-only.

### Audit

- Every read and write of PHI emits an `AuditEvent` with NHI, HPI, purpose-of-use, source IP, app ID, request ID.
- Logs are append-only, hash-chained per stream (BLAKE3), Merkle-rooted hourly, anchored weekly to an external timestamping authority (RFC 3161).
- Off-host within 60 s to a write-once tier.
- Break-glass access marks the event with a justification; auto-flagged for review within 24 h.
- Citizens can query the audit log of their own record via the patient portal.

### Consent (clinical and research)

- **Clinical consent**: per-record + per-category (mental health, sexual health, addiction default to tighter ACL). Versioned, revocable, never silently expires.
- **Research consent**: separate, explicit, granular. Each citizen chooses one of:
  - **Full participation** — de-identified data flows to the national research tier under HDEC-approved studies
  - **Specific categories only** — e.g., cancer research yes, genomic research no
  - **No participation** — no data used for secondary research
- **Default for a new record is "no decision yet"** — research tier excludes until the citizen makes an explicit choice. No opt-out-by-default for research.
- Consent withdrawal triggers a tombstone in the research lakehouse; future queries exclude that pseudonymous ID; published study results referencing the cohort remain (they were ethical at the time).

### Internationalisation

- Te Reo Māori + English UI everywhere, correct macron rendering throughout.
- Iwi affiliation, preferred name, pronouns, gender identity captured as first-class fields (not free-text notes).
- Pacific languages supported in the patient portal at a minimum: Samoan, Tongan, Cook Islands Māori, Niuean.

---

## Security — military-grade

| Control | Approach |
|---|---|
| Network | Zero-trust; mTLS between every service; Linkerd service mesh |
| TLS | TLS 1.3 only externally; internal mTLS with rotated short-lived certs (SPIFFE/SPIRE) |
| Encryption at rest | AES-256-GCM via crypto envelope service; per-record DEK wrapped by per-tenant KEK in HSM |
| HSM | Network HSM (Thales / sovereign equivalent) for root key custody; FIPS 140-3 Level 3+ |
| Authentication — clinicians | OIDC + WebAuthn with hardware FIDO2 keys (YubiKey or equivalent); no SMS-OTP |
| Authentication — patients | My Health Account federation; passkey + fallback |
| Authorization | RBAC + ABAC, consent-aware; every call evaluated through OPA (Open Policy Agent) |
| Break-glass | Justification mandatory; auto-audit; review within 24 h |
| Confidential computing | AMD SEV-SNP or Intel TDX for the **crypto envelope service** and **TRE query runners** so PHI is never visible in plaintext to host operators |
| Secrets | OpenBao (FOSS Vault fork) with HSM root |
| Supply chain | SLSA L4 builds, reproducible builds, signed artefacts (Sigstore/cosign), SBOM (CycloneDX) per release, vendored dependencies pinned by hash |
| Vulnerability mgmt | Dependabot-equivalent, weekly CVE scan, quarterly external pen test, public bug bounty |
| Threat model | Reviewed quarterly; covers insider abuse, ransomware, supply-chain, mass exfiltration, credential stuffing, nation-state targeting |
| Independent audit | Annual third-party security audit + formal NCSC engagement target |
| Formal methods | TLA+ for audit chain, consent enforcement, break-glass; reproducible model checking in CI |
| Backups | Encrypted, off-region, write-once (S3 Object Lock equivalent), air-gapped tier monthly |
| Disaster recovery | Two NZ regions, synchronous Postgres standby, RTO ≤ 15 min, monthly restore drills with timing audits |
| Insider mitigation | Two-person rule for key recovery; separation of duties; DBA never sees decrypted PHI thanks to envelope encryption |
| Logging | Append-only audit log is independent infrastructure from application logs |

---

## Interop

| Protocol | Use |
|---|---|
| **FHIR R4 (NZ Base profiles)** | All external clinical APIs |
| **SMART on FHIR** | Third-party clinician + patient apps |
| **DICOMweb (QIDO/WADO/STOW-RS)** | Imaging |
| **HL7v2 ingress adapter** | Legacy ingestion only; not strategic |
| **IHE XDS-I** | Cross-organisation imaging document sharing |
| **NZ Patient Summary (NZPS / IPS)** | Cross-organisation and cross-border summaries |
| **OpenEHR REST API** | Internal canonical access; external for research tier |

---

## National research tier

Designed so the entire system is **useful for research without ever leaking PHI**.

- **De-identification pipeline** streams clinical events to the research lakehouse. Each citizen's NHI is replaced by a pseudonymous ID derived from an HSM-protected key (so the link is unrecoverable without HSM access).
- **Consent gate** is the first stage of the pipeline. Only events from citizens with matching active research consent flow through.
- **Trusted Research Environment (TRE)** is where researchers work. Queries execute *inside* the TRE; no row-level data egress is possible. Aggregates leave only after differential-privacy noise is added and k-anonymity (k ≥ 5 by default, k ≥ 20 for sensitive categories) is enforced.
- **Confidential computing** for TRE query runners — even Health NZ operators cannot inspect the workload.
- **HDEC approval** required for every protocol. Study metadata is public; data access is logged and auditable by the public.
- **Citizens see who used their data** (de-identified, aggregated) in the patient portal — building trust in the consent contract.
- **Withdrawal** propagates within 24 h: pseudonymous ID is tombstoned, future queries exclude the record, previously published aggregate results stand.

---

## Deployment topology

- **Two NZ regions** (Auckland + Wellington). Stateless services active-active. Postgres primary/standby synchronous; failover within 15 min RTO.
- **Hospital edge nodes** — small Linux box per hospital with a read-only cache + write buffer; lets a hospital function read-only during a national-network outage and queue writes for replay.
- **k3s clusters** for application tier. **Postgres on dedicated bare metal Linux** (avoid container overhead for the database).
- **Sovereign hosting**: Catalyst Cloud or AoG-listed NZ providers. No PHI ever crosses the border, including for backups.
- **Air-gapped backup tier**: monthly snapshot to physically isolated infrastructure.

---

## Build sequence

| Phase | Months | Modules | Outcome |
|---|---|---|---|
| 1 — Foundations | 0–18 | IAM, MPI (dual-NHI support), Terminology adapter, FHIR gateway, Audit, Consent service, Clinical record core (OpenEHR + FHIR projection), Clinician web app (MVP) | A clinician can find a patient, write a note, save observations; full audit; consent enforced |
| 2 — Clinical workflow | 18–30 | Orders & results, Medications, Scheduling | Orders flow end-to-end; meds prescribed + dispensed via PHARMAC pipeline |
| 3 — Imaging + apps | 30–42 | Imaging integration, Pharmacy app, Patient portal (incl. research-consent management) | Imaging studies linked to records; community pharmacy live; citizens see their record |
| 4 — Research tier | 36–48 (overlaps) | De-identification pipeline, TRE | First HDEC-approved studies running on national data |
| 5 — Reach + advanced features | 48+ | GP workflows, IPS export refinements, post-quantum crypto migration, advanced analytics | National platform reaches full feature parity |

Each module gets its own detailed spec before implementation. Each phase rolls out behind feature flags, region by region.

---

## Open decisions deferred to module specs

| Decision | Where it gets resolved |
|---|---|
| FHIR storage strategy: native Go implementation vs HAPI sidecar vs Aidbox-style | Core FHIR gateway spec |
| OpenEHR engine: EHRbase (Java) sidecar vs custom Go OpenEHR component (Rust for parser?) | Clinical record core spec |
| Frontend framework final pick: SvelteKit vs Solid vs React | Clinician web app spec |
| DICOM vendor coverage matrix | Imaging spec |
| PHARMAC funding API integration mechanics | Medications + pharmacy app specs |
| TRE compute pattern: per-study container, JupyterHub, R Server, or thin custom | Research tier spec |
| Post-quantum algorithm choice (ML-KEM-768 vs ML-KEM-1024, hybrid scheme details) | Crypto envelope spec, before 2027 |

---

## Verification — is the architecture sound?

Walk three representative clinical journeys end-to-end. For each step, name the module, the resource, and the interface.

1. **ED admission → discharge**: NHI lookup (MPI; both formats) → encounter open (clinical core) → triage observation → bloods ordered (orders) → result delivered → meds prescribed (medications) → imaging ordered (orders → imaging) → discharge summary (clinical core, projected as FHIR + NZPS) → patient sees summary (patient portal). Audit chain unbroken throughout.
2. **GP repeat script**: patient request (portal) → GP review (clinician app) → prescription (medications) → community pharmacy dispense (pharmacy app) → PHARMAC claim.
3. **Outpatient imaging + research**: imaging order (orders) → appointment booked (scheduling) → study uploaded (imaging via DICOMweb) → radiologist report linked → ordering clinician notified → result reviewed. Later: a researcher queries radiology volumes by region (TRE) — only patients with active research consent are included, only aggregated results leave the TRE, citizens see "your radiology data contributed to study X" in the portal.

Additional checks:

- NFR sanity: do two regions sustain failover within RTO? Does the audit chain survive a region loss? Can a pharmacist's app authenticate via SMART on FHIR with a hardware key?
- Future-proofing sanity: if FHIR R6 ships in 2032, how much code changes? (Answer should be: profile bindings + projection workers, not canonical storage.)
- Security sanity: can a malicious DBA read PHI? (Answer: no — envelope encryption, HSM-held KEK, confidential computing for envelope service.)
- External review by a clinician with NZ health-IT experience; security review by an independent firm; OpenEHR architecture review by openEHR International.

---

## Recommended next step

Once this architecture is approved, write a **detailed spec for the core (IAM + MPI with dual-NHI support + Terminology adapter + FHIR gateway + Audit + Consent service + Clinical record core with OpenEHR backbone)**. That is the smallest subset that delivers value and the foundation everything else builds on.

---

# Addendum — 2026-05-28 requirement additions

The following requirements were added in a follow-up conversation. They extend (and in two cases reshape) the core architecture above. Each item is mapped to the modules it touches so the existing build sequence still holds.

## A1. Audit logging — "every detail, who accessed what"

Already covered by **Module 5 — Audit & security event log** and the *Audit* section under cross-cutting concerns. Confirming the granularity bar:

- **Field-level read logging**, not just record open. Viewing a patient's HIV status, mental-health note, or sexual-health history must each produce a distinct `AuditEvent`.
- Every event captures: NHI, HPI, purpose-of-use, source IP, device/app ID, request ID, exact resource + element path, query parameters, response size, latency.
- Patients can query their own access log via the patient portal (already specified).
- Retention: clinical-grade audit retained for the lifetime of the patient + 10 years minimum (NZ Health Information Privacy Code defaults; check current law before finalisation).

**No new module needed.** Add field-level granularity as a spec line in the Audit module spec.

## A2. Plug-and-play clinic onboarding

**New requirement.** A new clinic stands up Linux servers, runs an installer, and is connected to the mainframe with zero bespoke configuration.

Proposed treatment — extend **Module 18 (Deployment & observability)** with a sub-component:

- **Clinic Onboarding Service** (central): issues clinic ID, generates SPIFFE identity, provisions mTLS certs, registers HPI-CPN, allocates edge-node config bundle.
- **Edge-node installer** (Debian package + Ansible bootstrap): single command — `emr-edge join --token=<one-time-token>` — pulls config, joins service mesh, validates connectivity, runs smoke tests.
- **Edge-node profile** = minimal k3s + Postgres read replica + local write buffer + Orthanc DICOM cache (optional).
- **One-time enrolment tokens** issued by central operations team; bound to clinic ID; expire in 24 h.
- **Tiered profiles**: GP solo practice, multi-practitioner clinic, secondary hospital, tertiary hospital — each is a declarative bundle (resource sizing + which modules run locally vs. mainframe-only).
- **Offline-tolerant**: edge node continues read-only operation during national-network outage; queues writes; replays on reconnect (this was already in the deployment topology — formalising it as part of the onboarding profile).
- **Standalone practice mode (adoption strategy)**: the GP/clinic profile is a **complete free practice EMR on its own** — consults, notes, prescribing, recalls, referrals, results inbox — fully functional *before* (or even without) joining the national network. A practice installs it because it replaces a paid commercial PMS; `emr-edge join` then upgrades it to a connected node. National coverage grows bottom-up from practices that adopted it voluntarily, rather than top-down by mandate. Day-one data import from incumbent NZ practice systems (Medtech, Indici) is part of this profile's spec.

## A3. AI-friendly architecture

**New requirement.** Future AI integrations (summarisation, decision support, triage, ambient documentation) must plug in cleanly.

Approach — bake AI surfaces into the core, do **not** retrofit them:

- **Read API surface for AI agents**: read-only FHIR R4 + OpenEHR composition reads through a dedicated `/ai/` gateway with stricter rate limiting, separate audit category, and mandatory `purpose-of-use=AI-decision-support` or `purpose-of-use=AI-summarisation` claim on the token.
- **AI actions are first-class actors**: an AI suggesting a diagnosis or drafting a note writes through the same FHIR endpoints but with an `Provenance.agent.type = ai-system` and the human clinician as `Provenance.agent.who` co-signer. Nothing AI does is invisible.
- **Sign-off requirement for AI-authored content**: any AI-produced clinical content (note draft, suggested diagnosis, draft order) is created in a *proposed* state and requires explicit human clinician sign-off before becoming part of the record. This integrates with **A6** (sign-off subsystem) below.
- **No AI in the PHI critical path for the first 5 years.** AI surfaces are advisory until external safety review and HDEC equivalent give clearance. Hard policy gate in the IAM module.
- **Model provenance is recorded**: every AI output stores model name, version, hash of weights / API endpoint, and prompt template version. Reproducibility for medico-legal review.
- **Confidential-computing inference**: any AI service that sees PHI runs in the same SEV-SNP / TDX enclaves used for the crypto envelope service. No PHI ever leaves the sovereign environment for inference, including to external LLM APIs, without an explicit per-citizen consent flow analogous to research consent.

## A4. Git-style per-patient repository (innovation core)

**New requirement and the largest design implication.** Each patient's record is a versioned repository: every clinical event is a commit with author, parent, timestamp, and the ability to diff and time-travel.

Reconciliation with the existing plan:

- The existing plan already uses **event sourcing** (Guiding principle #2) and **OpenEHR contributions** (which are inherently versioned, immutable, and signed). This is *almost* the same model — but "git-shaped" implies first-class branching, diffing, blame, and human-readable navigation of the patient's history. Event sourcing as currently specified does not surface that.
- Architectural call: **keep OpenEHR + event sourcing as the canonical storage**, and add a **Patient History Service** (new sub-component of Module 6, Clinical record core) that exposes git-like operations over the event stream:
  - `GET /patients/{nhi}/history?at=<timestamp>` → reconstructed record as of date
  - `GET /patients/{nhi}/diff?from=<t1>&to=<t2>&scope=problems|meds|labs|imaging` → human-readable diff
  - `GET /patients/{nhi}/blame/{resource-id}` → who last changed each field, when, why
  - `GET /patients/{nhi}/timeline?scope=<problem|medication|investigation>` → chronological view of one clinical thread
- **Not actual `.git` directories per patient.** A real git repo per patient hits operational pain at 5M patients (filesystem inode pressure, replication, encryption-at-rest, search). We get the same UX from OpenEHR contributions + event log + projections, with proven scale characteristics.
- **Branching semantics**: a "branch" maps to a *provisional* version of a clinical resource — e.g. an AI-drafted note, an admission-in-progress problem list, a referral being assembled. Merging = sign-off (see A6). Aligns naturally with A3 (AI sign-off).
- Adds one new module spec to the build plan: **Patient History Service**, scheduled with Module 6 in Phase 1.

## A5. Medication history & allergies

Already covered by **Module 9 (Medications)** for prescribing/dispensing and the clinical record core (`AllergyIntolerance`) for allergies. Confirming requirements:

- **Lifetime medication record**, including: start/stop dates, dose changes, prescriber, indication, adverse reaction link, reason for stop, NZULM code, PHARMAC funding state at time of prescription.
- **Allergy record** is structured: substance (SNOMED), reaction (SNOMED), severity, certainty, source (patient-reported / clinician-verified / reaction-confirmed), date first noted, last verified date.
- **Both surfaced in patient summary at the top of every clinician view.** This is a UI requirement on Module 12; should be in the clinician web app spec.
- **Reconciliation event** at each transition of care: when a patient is admitted, discharged, or seen by a new GP, the system prompts the receiving clinician to confirm the medication list. Reconciliation produces a signed event in the patient history.

No new module — strengthens existing spec lines.

## A6. Investigation result sign-off (lab / imaging / pathology)

**New requirement, partially overlapping with existing plan.** Module 8 (Orders & results) mentions "ack" but the closed-loop, escalating, audited sign-off pattern needs to be specified explicitly:

- Every `ServiceRequest` (lab / imaging / pathology) has a **responsible clinician** (the orderer) — guaranteed not nullable.
- When a `DiagnosticReport` arrives, it enters the responsible clinician's **inbox** with an unsigned-off state.
- **Sign-off requires an action**: acknowledge (no further action), order follow-up, refer, document plan in note, declare critical and escalate. The action is logged.
- **Overdue queue**: results unsigned-off after a clinical-priority-based SLA (e.g. critical = 1 h, urgent = 4 h, routine = 5 working days) escalate to:
  - Clinician's nominated deputy
  - Department head dashboard
  - Patient-safety event if breach persists
- **Patient-visible status**: patient sees in portal whether their result has been reviewed by a clinician.
- **Coverage during leave**: clinicians declare leave and an alternate signatory in advance; orders auto-route during the leave window.
- **No silent drops**: a result with no identifiable orderer (e.g. legacy order, locum gone) routes to a "results without owner" queue at the facility level; cannot be discarded without a documented action.

Implementation: extends Module 8 spec; adds a small "Inbox & sign-off" sub-component owning state machine + escalation timers. UI lives in clinician web app (Module 12).

## A7. GP ↔ Tertiary referral system

**New requirement.** A referral is a structured, stateful, bidirectional document — not a fax-PDF-as-attachment.

Treatment — **new Module 19: Referrals** in Band 2 (Clinical workflow modules):

- **Referral object** (FHIR `ServiceRequest` of type referral, with `CommunicationRequest` thread and optional `Appointment` linkage). Stored in the patient repository (A4) like any other event.
- **States**: drafted → submitted → triaged → (advice-returned | rejected | scheduled) → completed.
- **Specialist response** is one of:
  - **Advice**: structured advice + optional attached patient summary, returned to the referring GP; no appointment created.
  - **Rejection**: structured reason (out-of-scope, insufficient information, alternative pathway suggested); thread can continue.
  - **Schedule**: appointment proposed via Module 11 (Scheduling); patient notified via patient portal.
- **Bidirectional thread**: referrer and specialist can attach further notes, additional results, ask follow-up questions; every message audited like any other PHI event.
- **Visibility to patient**: patient portal shows referral state, expected timeframes, and any returned advice (with the GP's consent for advice display).
- **SLA dashboards**: tertiary services see triage backlog by specialty; GPs see response times by service. Builds quality-improvement data without separate reporting infrastructure.
- **Integrates with sign-off (A6)**: incoming results post-referral attach to the specialist's inbox until acknowledged.

## Updated build sequence

The additions slot into the existing phases as follows:

| Phase | Modules added/extended |
|---|---|
| 1 — Foundations | **Patient History Service** (sub-module of #6); field-level audit granularity in #5; clinic onboarding sub-component of #18 |
| 2 — Clinical workflow | Sign-off / inbox sub-component of **#8**; new **Module 19 — Referrals** |
| 3 — Imaging + apps | UI for medication reconciliation, allergy display, sign-off inbox, referral threads in #12; patient-facing referral and result visibility in #13 |
| 4 — Research tier | (unchanged) |
| 5 — Reach + advanced features | AI surface (`/ai/` gateway), AI sign-off flow, model provenance recording. Hard-gated until external safety review. |

## Known gaps — organisational workstreams (added 2026-06-13)

The technical architecture above is necessary but not sufficient. These non-technical workstreams are acknowledged as open and need owners:

1. **Governance & legal entity** — incorporated society or charitable trust to hold trademark, domain, signing keys; technical steering committee + clinical governance board; decision-making and maintainer model.
2. **Clinical safety** — hazard log, clinical safety case (modelled on NHS DCB0129/0160), incident reporting, named clinical safety officer role. An EMR is safety-critical software; this is as important as the security section.
3. **Regulatory & privacy compliance** — Privacy Act 2020 / Health Information Privacy Code documentation; medico-legal position for clinicians relying on the software.
4. **Funding & sustainability** — audits, pen tests, HSMs, hosting and maintainer time cost money. Candidate models: grants, Health NZ service contracts, paid support/hosting offered by the trust itself (Nextcloud model — AGPL prevents parasitic third-party hosting), sponsorship.
5. **Migration & coexistence strategy** — 10+ year coexistence with Medtech, Indici, hospital PAS, TestSafe, HealthOne; not just the HL7v2 ingress adapter.
6. **Community mechanics** — RFC process for design decisions, security disclosure policy, code of conduct, good-first-issues, contributor onboarding docs.
7. **Design partners & UX** — 2–3 friendly practices as clinical design partners; WCAG 2.2 AA accessibility as an NFR.
8. **Support model** — who a practice calls when the software breaks mid-clinic.
9. **Project name** — `nz-open-emr` is a working name only.

## Notes / Future Additions

*User will keep adding to this section as ideas develop.*
