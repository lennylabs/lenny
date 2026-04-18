# Roadmap

This is the short-horizon view. The full phase-gated build sequence lives in [`spec/18_build-sequence.md`](spec/18_build-sequence.md); current implementation state is tracked on the [Implementation Status](docs/about/status.md) page.

## Now — Design phase

The specification is complete and drives implementation under a spec- and test-driven workflow. The work in flight is:

- Closing gaps surfaced by the first round of spec review.
- Wiring up the repository, CI, and contributor on-ramps so Phase 2 can land cleanly.
- Collecting early design feedback — issues and discussions are open.

## Next — Phase 2: first working slice

Phase 2 produces the first runnable slice of the platform:

- `make run` local development mode with embedded stores (SQLite, in-process KV, local filesystem).
- The echo runtime as the default runtime.
- Gateway skeleton with session create, stream, and complete.
- `CONTRIBUTING.md` fully opened for code PRs.
- Benchmark harness (TTHW and basic throughput).

When Phase 2 lands, code contributions against the core platform open up. [`CONTRIBUTING.md`](CONTRIBUTING.md) will be updated the day the policy changes.

## After Phase 2

The rest of phases are described in [`spec/18_build-sequence.md`](spec/18_build-sequence.md). Highlights of what comes next:

- **Warm pool controller** and workspace materialization.
- **Credential leasing and rotation.**
- **Delegation graph** with budget enforcement.
- **Reference runtime catalog** (nine pre-registered runtimes).
- **`lenny-ctl install` wizard** and Helm chart hardening.
- **`lenny-ops` management plane** with diagnostic endpoints and runbooks.
- **Multi-tenancy** (Postgres RLS, audit log, RBAC, quotas).
- **Compliance controls** (erasure receipts, legal holds, data residency).
- **Security hardening** and SLO validation at Growth-sized load.
- **Phase 17a community launch.**

## How this roadmap is maintained

- This file changes when phases complete, when priorities shift, or when the scope of a phase changes.
- Phase-level acceptance criteria are tracked in the spec; operational milestones are tracked on the Implementation Status page.
- Large scope changes go through an ADR in [`docs/adr/`](docs/adr/) before this file is updated.
