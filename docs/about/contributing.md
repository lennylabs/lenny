---
layout: default
title: Contributing
parent: About
nav_order: 3
---

# Contributing to Lenny

{: .no_toc }

<details open markdown="block">
  <summary>Table of contents</summary>
  {: .text-delta }
- TOC
{:toc}
</details>

---

## Project status

Lenny is in **active pre-release development**. The project welcomes bug reports and discussion via the issue tracker at any phase. Unsolicited pull requests will not be reviewed or merged until the community launch phase. This notice will be updated when the project transitions to accepting external contributions.

---

## How to contribute

### Bug reports

File an issue on the GitHub issue tracker with:

- **Summary:** a concise description of the problem.
- **Steps to reproduce:** minimum steps to trigger the issue.
- **Expected behavior:** what should have happened.
- **Actual behavior:** what happened instead.
- **Environment:** Kubernetes version, Helm chart version, runtime adapter version, isolation profile.
- **Logs:** relevant structured log output with `session_id`, `tenant_id`, and `trace_id` correlation fields.

### Feature requests and design proposals

For small enhancements, open an issue with the `enhancement` label.

For larger changes that affect the platform's architecture, use the **Discussions** forum to propose your idea as an RFC-style conversation. Architectural changes above a defined scope threshold require a formal ADR (Architecture Decision Record) before implementation begins. See [ADR process](#adr-process) below.

### Code contributions

Once the project transitions to accepting external contributions (Phase 17a):

1. **Fork the repository** and create a feature branch from `main`.
2. **Follow the local development setup** below.
3. **Write tests.** See [test expectations](#test-expectations).
4. **Open a pull request** against `main` with a clear description of the change.

---

## Local development setup

Lenny's local development mode (`make run`) runs with embedded stores and the echo runtime, requiring zero cloud dependencies.

### Prerequisites

- **Go** 1.22+ (platform components)
- **Docker** or compatible container runtime
- **make** (build orchestration)
- **kubectl** (for integration tests, optional for `make run`)

### Quick start

```bash
# Clone the repository
git clone https://github.com/lenny-platform/lenny.git
cd lenny

# Run in local dev mode with embedded stores
make run

# In another terminal, send a test prompt to the echo runtime
curl -X POST http://localhost:8080/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{"runtime": "echo"}'
```

The `make run` target starts the gateway with:

- SQLite in place of Postgres
- In-process key-value store in place of Redis
- Local filesystem in place of MinIO
- The echo runtime sample as the default runtime
- 100% trace sampling with stdout exporter

**Target: under 5 minutes** from `git clone` to a completed round-trip echo session on a standard development machine. CI includes a TTHW smoke test that validates this path.

For full Kubernetes-based development, see the [Quickstart Guide](../getting-started/quickstart).

---

## PR process and review expectations

### Before opening a PR

1. **Ensure all tests pass locally.** Run `make test` for unit tests and `make test-integration` for integration tests.
2. **Run the linter.** `make lint` must pass with zero warnings.
3. **Check formatting.** `make fmt` should produce no diff.
4. **Update documentation** if your change affects configuration, API, or operator-facing behavior.

### PR description

Every PR should include:

- **What:** a concise summary of the change.
- **Why:** the motivation (link to issue or discussion).
- **How:** implementation approach and key design decisions.
- **Testing:** how the change was tested (unit, integration, manual).
- **Breaking changes:** any backward-incompatible changes, migration steps, or deprecations.

### Review process

- PRs require at least one maintainer approval before merge.
- CI must pass (unit tests, integration tests, linter, TTHW smoke test).
- During the BDfN governance phase, the maintainer has final decision authority on all merges.
- Reviewers focus on: correctness, test coverage, documentation, backward compatibility, and adherence to the design principles in [Why Lenny](why-lenny).

---

## Test expectations

### Unit tests

Every package must have unit tests covering:

- **Happy-path behavior:** normal operation.
- **Error paths:** each error code returned by the package.
- **Edge cases:** boundary conditions, empty inputs, concurrent access.

Unit tests must be fast (<100ms per test) and have no external dependencies. Use interfaces and mocks for database, Redis, and MinIO interactions.

### Integration tests

Integration tests run against real (embedded or containerized) stores:

- **Session lifecycle tests:** full session creation through completion.
- **Delegation tests:** parent-child delegation with budget enforcement.
- **Error handling tests:** each error code exercised with a canonical triggering input.
- **Admission policy tests:** verify controller-generated pod specs pass deployed admission policies (prevents policy/spec drift from causing warm pool deadlock).

### Contract tests

The runtime adapter contract has a compliance test suite:

- **`RegisterAdapterUnderTest`** compliance suite validates that an adapter correctly implements the Basic, Standard, or Full integration-level contract.
- All error classes (`VALIDATION_ERROR`, `QUOTA_EXCEEDED`, `RATE_LIMITED`, `RESOURCE_NOT_FOUND`, `INVALID_STATE_TRANSITION`, `PERMISSION_DENIED`, `CREDENTIAL_REVOKED`, `CREDENTIAL_POOL_EXHAUSTED`, `ISOLATION_MONOTONICITY_VIOLATED`) are exercised with canonical triggering inputs.
- For each error class, the test asserts identical `code`, `category`, and `retryable` values.

---

## Runtime adapter: the main extension point

The runtime adapter contract is how external agent code integrates with Lenny. If you are building a custom agent or integrating an existing framework with Lenny, start here.

### What a runtime adapter does

The adapter translates between Lenny's control protocol and your agent binary's native interface. It handles:

- Session start/stop lifecycle
- Workspace change notifications
- Credential delivery and rotation
- Checkpoint coordination (Full integration level)
- Streaming I/O relay (stdin/stdout or gRPC)

### Getting started

1. **Read the [Runtime Author Roadmap](../runtime-author-guide/)** for a guided reading path organized by integration level.
2. **Copy the echo runtime sample** as your starting point.
3. **Use `make run`** to test locally without Kubernetes.
4. **Run the compliance suite** (`RegisterAdapterUnderTest`) to validate your adapter.

### Integration levels

| Level         | Interface               | What you implement                                                                          | What the platform provides                                                             |
| :----------- | :---------------------- | :------------------------------------------------------------------------------------------ | :------------------------------------------------------------------------------------- |
| **Basic**    | stdin/stdout JSON Lines          | Read messages from stdin, write output to stdout. ~50 lines of code.                        | Basic session lifecycle, workspace delivery, credential injection (environment variables).   |
| **Standard** | stdin/stdout + MCP (Unix socket) | Basic + platform tool server over MCP (delegation, discovery, elicitation, output), connector tool access. | All of Basic, plus a platform tool server on a Unix socket and mid-session uploads.                     |
| **Full**     | stdin/stdout + MCP (Unix socket) | All of Standard, plus lifecycle channel (cooperative checkpointing, clean interrupts, credential rotation, graceful drain, task-mode pod reuse).  | Full platform integration including SDK-warm, checkpoint/restore, credential rotation. |

---

## ADR process

Architecture Decision Records (ADRs) are stored in `docs/adr/` and track all significant architectural decisions.

### When an ADR is required

An ADR is required for changes that:

- Introduce a new CRD or remove an existing one.
- Change the session or pod state machine.
- Modify the delegation policy model.
- Add or remove a gateway subsystem.
- Change the storage architecture (new store, new table, schema migration pattern).
- Alter the security model (new isolation profile, credential flow change, RLS policy change).
- Affect cross-cutting concerns used by multiple components.

### ADR format

```markdown
# ADR-NNN: Title

## Status

Proposed | Accepted | Deprecated | Superseded by ADR-NNN

## Context

What is the issue that we are seeing that is motivating this decision?

## Decision

What is the change that we are proposing and/or doing?

## Alternatives considered

What other approaches did we consider and why did we reject them?

## Consequences

What becomes easier or more difficult to do because of this change?
```

### Threshold for community-proposed ADRs

Community members can propose ADRs via the Discussions forum. The maintainer (or steering committee, post-transition) reviews and either accepts, requests modifications, or rejects with rationale.

---

## Community runtime registry

A community runtime registry, where runtime authors publish versioned adapter packages for operator discovery and installation, is planned as a post-v1 platform service.

In v1, runtime adapters are distributed via:

- Standard Go module hosting
- Container registries (Docker Hub, GitHub Container Registry, private registries)
- Helm chart repositories

The runtime adapter specification defines the interface contract for v1 adapter distribution. The registry will build on this contract to add discoverability, versioning, and compatibility metadata.

---

## Communication

| Channel                | Purpose                                                           |
| :--------------------- | :---------------------------------------------------------------- |
| **Issue tracker**      | Bug reports, feature requests, and task tracking.                 |
| **Discussions forum**  | Design proposals, RFC-style conversations, and general questions. |
| **ADRs** (`docs/adr/`) | Architectural decision records for significant design changes.    |

---

## Code of conduct

All participants in the Lenny project are expected to adhere to the project's Code of Conduct. The Code of Conduct is published in `CODE_OF_CONDUCT.md` at the repository root. It establishes expectations for respectful, constructive interaction and outlines the process for reporting and addressing violations.

Key principles:

- Be respectful and constructive in all interactions.
- Focus on technical merit in code reviews and design discussions.
- Welcome newcomers and help them get started.
- Report violations to the project maintainer.

---

## License

The open-source license for Lenny is a **Phase 0 gating item** (ADR-008). The license must be committed to the repository root before any contributor engagement, `CONTRIBUTING.md` publication, or external PR is accepted.

### Evaluation criteria

The license decision evaluates:

- **Competitive landscape alignment:** E2B uses Apache 2.0 with a commercial offering; Temporal and LangChain use MIT.
- **Enterprise legal review:** compatibility with enterprise adoption requirements.
- **Runtime author copyleft clarity:** whether the license creates obligations for runtime adapter authors.
- **Upstream compatibility:** compatibility with `kubernetes-sigs/agent-sandbox` and other dependencies.

### Candidate licenses

| License                       | Notes                                                                   |
| :---------------------------- | :---------------------------------------------------------------------- |
| MIT                           | Maximally permissive. Used by Temporal, LangChain.                      |
| Apache 2.0                    | Permissive with patent grant. Used by E2B, Kubernetes.                  |
| AGPL + commercial exception   | Copyleft with commercial flexibility. Less common in the K8s ecosystem. |
| BSL (Business Source License) | Time-delayed open source. Used by some database companies.              |

The decision and rationale are recorded as ADR-008 in `docs/adr/`. See [Governance](governance) for the decision-making process.
