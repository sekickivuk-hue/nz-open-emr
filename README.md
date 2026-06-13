# nz-open-emr (working name)

**An open-source national Electronic Medical Record for Aotearoa New Zealand — free, crowd-sourced, and built to run on the hardware clinics already own.**

> ⚠️ **Status: design phase.** There is no code yet. What exists today is a detailed architecture plan ([PLAN.md](PLAN.md)) and an open invitation to help build it.

## The idea

New Zealand's health software is fragmented and expensive. Hospitals, GPs, and pharmacies run disconnected commercial systems with per-seat licensing; patient information moves between them by referral letters and faxes-with-extra-steps. This project exists to build the alternative:

- **A complete, free practice EMR** that an individual GP practice can install on a single Linux box and use as their day-to-day system — consults, notes, prescribing, results, referrals — replacing a paid commercial PMS.
- **A national, connected record** that those practices (and hospitals) join with one command, giving clinicians the whole picture and patients access to their own record.
- **A national research capability** over de-identified data, gated by explicit per-citizen consent, where queries run inside a trusted environment and only aggregates leave.

Designed for a 20–50 year lifespan: OpenEHR as the canonical clinical model, FHIR R4 (NZ Base) for exchange, event-sourced storage in PostgreSQL, Go services, all on Linux, all open standards. Frugal by design — the target floor for a full GP-clinic deployment is a ten-year-old 4-core machine with 8 GB of RAM.

Read the full architecture: **[PLAN.md](PLAN.md)**

## Why AGPLv3?

This is a crowd-sourced public good. The AGPLv3 license means anyone can use, study, and improve the system for free — but anyone who modifies it and offers it as a service must publish their changes. You can build on it; you cannot capture it. Client SDKs and integration libraries will be Apache 2.0 so commercial lab, pharmacy, and practice systems can integrate freely. Specifications and clinical models will be CC-BY 4.0.

Contributions are accepted under the [Developer Certificate of Origin](https://developercertificate.org/) — contributors keep their copyright, which means no single entity (including the project founder) can ever relicense this to closed source.

## Where the project is and how to help

No single person can build a national EMR. Right now the most valuable contributions are:

1. **Review the architecture** — open an issue challenging any decision in [PLAN.md](PLAN.md). NZ health-IT experience especially welcome (FHIR, OpenEHR, HL7v2, NHI/HPI integration, Medtech/Indici internals).
2. **Clinical input** — GPs, SMOs, pharmacists, nurses: tell us what your software gets wrong. The plan's known gaps (clinical safety case, governance, migration strategy) are listed at the end of PLAN.md.
3. **Code is coming** — the first milestone is a walking skeleton: create a synthetic patient, write a clinical note, see the tamper-evident audit event, all via `docker compose up`. Watch/star the repo if you want to be there when it lands.

## Roadmap (high level)

| Phase | What |
|---|---|
| 0 (now) | Architecture review in the open; governance bootstrapping; walking skeleton |
| 1 | Core platform: identity, patient index (dual-NHI), terminology, FHIR gateway, audit, consent, clinical record core |
| 2 | Orders & results with closed-loop sign-off, medications, scheduling, referrals |
| 3 | Imaging, pharmacy app, patient portal |
| 4 | National research tier (consent-gated, trusted research environment) |

Full sequencing and rationale in [PLAN.md](PLAN.md).

## License

- Platform code: [AGPL-3.0-or-later](LICENSE)
- Documentation and specifications: CC-BY 4.0
- Contributions: [DCO](https://developercertificate.org/) sign-off required (`git commit -s`)
