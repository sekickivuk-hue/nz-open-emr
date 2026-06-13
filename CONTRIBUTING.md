# Contributing

Thank you for considering contributing — this project only works as a crowd-sourced effort.

## Current phase: design review

There is no code yet. The highest-value contributions right now:

- **Architecture review**: open a GitHub issue challenging or improving any decision in [PLAN.md](PLAN.md). Please reference the specific section.
- **Clinical input**: if you work in NZ healthcare, issues describing real workflow problems (results sign-off failures, referral black holes, medication reconciliation pain) are gold.
- **Domain expertise**: NHI/HPI integration, NZ Base FHIR profiles, OpenEHR modelling, Medtech/Indici data export — if you know these, we need you.

## How decisions get made

Significant design decisions will go through public RFC issues before being merged into PLAN.md or implemented. Until a formal governance structure exists (see "Known gaps" in PLAN.md), the maintainer decides after public discussion; the intent is to move to a technical steering committee under a charitable trust.

## Developer Certificate of Origin (DCO)

All contributions require a DCO sign-off, certifying you have the right to submit the work under the project license:

```
git commit -s
```

This adds a `Signed-off-by:` line. **You keep your copyright.** There is no CLA, and there never will be — that is deliberate: it makes it permanently impossible for anyone to relicense this project to closed source.

## Licensing of contributions

- Code: AGPL-3.0-or-later
- Client SDKs / integration libraries (when they exist): Apache-2.0
- Documentation, specs, clinical models: CC-BY 4.0

## Conduct

Be kind, be honest, assume good faith. A formal code of conduct will be adopted as part of governance bootstrapping; until then, the maintainer reserves the right to remove participants who make the space hostile.

## Security

Do not open public issues for security vulnerabilities. A formal disclosure policy is pending; for now, contact the maintainer directly via the email on their GitHub profile.
