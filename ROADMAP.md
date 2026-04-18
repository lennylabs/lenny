# Roadmap

This is the short-horizon view. The full build sequence — which is directional and will evolve — lives in [`spec/18_build-sequence.md`](spec/18_build-sequence.md); current implementation state is tracked on the [Implementation Status](docs/about/status.md) page.

## Now — Design phase

The specification is complete and drives implementation under a spec- and test-driven workflow. The work in flight is:

- Closing gaps surfaced by the first round of spec review.
- Wiring up the repository, CI, and contributor on-ramps so the first working slice can land cleanly.
- Collecting early design feedback — issues and discussions are open.

## Next — First working slice

The next milestone is the first runnable slice of the platform:

- `make run` local development mode with embedded stores (SQLite, in-process KV, local filesystem).
- The echo runtime as the default runtime.
- Gateway skeleton with session create, stream, and complete.
- `CONTRIBUTING.md` fully opened for code PRs.
- Benchmark harness.

When this lands, code contributions against the core platform open up. [`CONTRIBUTING.md`](CONTRIBUTING.md) will be updated the day the policy changes.

## After the first working slice

The rest of the build sequence is described in [`spec/18_build-sequence.md`](spec/18_build-sequence.md) and is directional — surface order and timing may shift. Highlights of what comes next:

- **Warm pool controller** and workspace materialization.
- **Credential leasing and rotation.**
- **In-process native LLM translator** — Anthropic first, then Bedrock, Vertex AI, Azure OpenAI. No sidecar; upstream keys never leave the gateway.
- **Delegation graph** with budget enforcement, including MCP-reachable delegation.
- **Reference runtime catalog** — nine pre-registered runtimes (`claude-code`, `gemini-cli`, `codex`, `cursor-cli`, `chat`, `langgraph`, `mastra`, `openai-assistants`, `crewai`).
- **Tier 0 embedded stack (`lenny up`)** — single-binary evaluation experience with embedded Kubernetes, stores, KMS, identity provider, and the reference runtimes.
- **Runtime scaffolder** — `lenny runtime init` / `publish` for spinning up and distributing custom runtimes.
- **`lenny-ctl install` wizard** and Helm chart hardening.
- **`lenny-ops` management plane** with diagnostic endpoints, runbooks, backup and restore APIs, and drift detection.
- **`lenny-ctl doctor --fix`** — idempotent remediations for common misconfigurations.
- **Multi-tenancy** (Postgres RLS, audit log, RBAC, quotas).
- **Compliance controls** (erasure receipts, legal holds, data residency).
- **Security hardening** and SLO validation at Growth-sized load.
- **Community launch.**

## How this roadmap is maintained

- This file changes when milestones complete, when priorities shift, or when scope changes.
- The build sequence in the spec is the authoritative-but-evolving source for ordering; operational milestones are tracked on the Implementation Status page.
- Large scope changes go through an ADR in [`docs/adr/`](docs/adr/) before this file is updated.
