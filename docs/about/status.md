---
layout: default
title: Implementation Status
parent: About
nav_order: 2
---

# Implementation status
{: .no_toc }

<details open markdown="block">
  <summary>Table of contents</summary>
  {: .text-delta }
- TOC
{:toc}
</details>

---

Last updated: 2026-10-27

Lenny is at an **early, pre-release stage**. There is no tagged release, and no binaries, container images, Helm chart, Homebrew formula, or SDK packages are published; the platform is built from source. The [technical specification](https://github.com/lennylabs/lenny/tree/main/spec) is complete and drives implementation under a spec- and test-driven workflow. The documentation throughout this site describes the **target surface**: the platform as it stands once the build sequence in [`spec/18_build-sequence.md`](https://github.com/lennylabs/lenny/blob/main/spec/18_build-sequence.md) lands.

This page tracks what is actually wired up today, so readers can tell which parts of the docs describe running code and which parts describe work ahead.

The build sequence enumerates the application-code phases from Phase 0 (repository bootstrap) through Phase 17b (memory, semantic caching, eval hooks), with Deliverables, Prerequisites, and Exit criteria for each. The sequence is directional: surface ordering and timing will shift as implementation surfaces new constraints. Treat [`spec/18_build-sequence.md`](https://github.com/lennylabs/lenny/blob/main/spec/18_build-sequence.md) as the authoritative but evolving source.

## Legend

| Status | Meaning |
|:-------|:--------|
| **Not started** | No code yet. Design is in the spec. |
| **In design** | Design work in progress; ADRs may be open. No code yet. |
| **In progress** | Code is in the repository. Not yet complete or not yet usable end-to-end. |
| **Shipped** | Usable when built from source. Pre-release and unpublished; open defects may remain. |

---

## Platform surfaces

### Local developer experience

| Surface | Status | Notes |
|:--------|:-------|:------|
| Embedded Mode — `lenny up` single-binary stack | In progress | `lenny up` brings up an embedded k3s cluster (in Docker on macOS and Windows) and runs the gateway, controllers, and management plane in it, with in-memory application stores. Running sessions requires the platform and echo runtime image bundles built from source. Hosts other than Linux are partly verified. |
| Source Mode — `make run` contributor mode | Shipped | `make run` builds the echo runtime and the gateway and runs the gateway on `:8080` in dev mode with SQLite (`lenny-data/lenny.db`), filesystem artifact storage, and the echo runtime. `LENNY_AGENT_BINARY` selects a different runtime binary. |
| Compose Mode — `docker compose up` | Shipped | `make compose` runs `docker-compose.yml` with Postgres, Redis, MinIO, migrations, the gateway, a smoke test, Prometheus, Grafana, and Jaeger. |

### Core runtime

| Surface | Status | Notes |
|:--------|:-------|:------|
| Echo reference runtime | Shipped | Basic-level adapter at `cmd/runtimes/echo`. Driven by the conformance suite and by the gateway's subprocess executor. |
| Gateway (session create / stream / complete) | Shipped | `cmd/lenny-gateway` serves the REST surface, the SSE event stream, and the MCP, OpenAI Chat Completions, and Open Responses adapters. It persists to Postgres and Redis when `--postgres-dsn` and `--redis-url` are set, and to SQLite under `make run`. |
| Session lifecycle + REST API end-to-end | Shipped | Create, finalize, start, interrupt, terminate, resume, derive, upload, messages, transcript, tree, and events are all served. Postgres-backed session, transcript, tenant, runtime, user, and connector stores are available via `--postgres-dsn`. Open defects remain in multi-replica session coordination. |
| Database schema and migrations | Shipped | The `migrations/` schema (up and down migrations) applied by the golang-migrate framework. Covers the platform tables, the row-level security policies and `lenny_tenant_guard` trigger, the audit ledger-immutability triggers, and the `lenny_app` and `lenny_erasure` role separation. |
| Warm pod pool controller | Shipped | `lenny-controller` runs the warm pool, sandbox, and pool scaling controllers, which keep pods pre-warmed and handle claim, release, and drain. Open defects remain in concurrent-pool recycling. |
| Workspace materialization | In progress | The gateway parses and validates the workspace plan on session creation and stores uploaded files in the blob store. The adapter materializes the workspace on the pod. Workspace upload fails under load. |
| Credential leasing (Basic) | In progress | The Token Service, credential leases, and the end-user credential registry with the `/v1/credentials` endpoints are present. Open defects remain in lease handling. |
| Credential rotation (Full integration level) | In progress | Rotation without restart over the lifecycle-signal socket. Code is present; rotation is not yet verified end to end. |
| Checkpoint / resume | In progress | Checkpoint and resume code is present in the gateway and the adapter. The checkpoint load scenario is skipped because workspace upload fails under load. |
| Recursive delegation | In progress | The delegation service enforces cycle detection, isolation monotonicity, and the depth limit. Open defects remain in delegation budgets and lease extension. |
| Recursive delegation with MCP semantics | In progress | `lenny/delegate_task` and the other platform tools are served by the MCP adapter. |
| Multi-tenancy (Postgres RLS, quotas, RBAC) | Shipped | RBAC role enforcement, custom roles, and the quota interceptor are active. The row-level security policies and the `lenny_tenant_guard` transaction-isolation trigger ship in the `migrations/` schema. Open defects remain in session-owner attribution and impersonation revocation. |
| Audit log with hash-chain integrity + SIEM | In progress | The per-tenant audit hash chain records every admin mutation and is queryable. The chain is durable in the Postgres `audit_log` table under `--postgres-dsn`, the ledger-immutability triggers and the startup integrity check are active, and the SIEM forwarding and OCSF mapping code is present. |
| Compliance controls (erasure receipts, legal holds, residency) | In progress | Erasure, legal hold, and residency code and migrations are present. Erasure does not yet delete objects on S3, GCS, or Azure Blob Storage. |
| Security hardening (signed images, admission, pentest) | In progress | The admission webhooks, including image signature verification, are present. Open defect: the webhook serving certificate is not reloaded after renewal. |
| SLO validation at Growth-sized load | Not started | Full security hardening active. |

### Gateway protocols

| Protocol | Status | Notes |
|:---------|:-------|:------|
| REST | Shipped | The session, blob, and admin endpoints are served end-to-end, against Postgres-backed stores or the SQLite store that `make run` uses. |
| MCP (Streamable HTTP) | Shipped | The `/mcp` adapter serves initialize, tools/list, and tools/call, with streaming over SSE. A WebSocket transport is served at `/mcp/v1/ws`. |
| OpenAI Chat Completions | Shipped | `/v1/chat/completions` translates to the session surface, including streaming responses (`stream: true`). Open defects remain in terminal-state writes. |
| Open Responses / OpenAI Responses | Shipped | `/v1/responses` translates to the session surface, including streaming responses (`stream: true`). |

### LLM routing in the gateway

| Provider path | Status | Notes |
|:--------------|:-------|:------|
| In-process native Go translator — `anthropic_direct` | In progress | `deliveryMode: proxy`. The gateway translates requests in process, and keys stay in the gateway's in-memory Token Service cache. |
| Native translator — AWS Bedrock, Vertex AI, Azure OpenAI | In progress | Translators for AWS Bedrock, Vertex AI, Azure OpenAI, and OpenAI are present. |
| External LLM proxy integration (LiteLLM, Portkey) | In design | For providers outside the built-in router. Runs alongside native routing. |

### Reference runtime catalog

| Runtime | Status | Notes |
|:--------|:-------|:------|
| `echo` (compliance reference) | Shipped | Basic-level adapter embedded in the platform repo at `cmd/runtimes/echo`. |
| `streaming-echo` (CI test runtime) | Shipped | Full-level lifecycle runtime at `cmd/runtimes/streaming-echo`. Passes `lenny runtime validate` at the Basic and Full levels. |
| `chat` | Not started | Generic LLM chat, no tools. Full integration level. |
| `claude-code` | Not started | Anthropic Claude Code CLI under gVisor. |
| `gemini-cli` | Not started | Google Gemini CLI under gVisor. |
| `codex` | Not started | OpenAI Codex CLI under gVisor. |
| `cursor-cli` | Not started | Cursor agent CLI under gVisor. |
| `langgraph` | Not started | LangGraph graph-based agents (Python). |
| `mastra` | Not started | Mastra framework (TypeScript). |
| `openai-assistants` | Not started | OpenAI Assistants-compatible runtime. |
| `crewai` | Not started | CrewAI with delegation wired to `lenny/delegate_task`. |

### SDKs and CLI

| Surface | Status | Notes |
|:--------|:-------|:------|
| Go SDK | Shipped | Official client SDK at `sdks/client/go`. Not published to a package registry. |
| TypeScript SDK | Shipped | Official client SDK (`@lennylabs/client-sdk`) at `sdks/client/typescript`. Not published to npm. |
| Python SDK | Shipped | Client SDK (`lenny-client`) at `sdks/client/python`. Not published to PyPI. |
| Runtime author SDKs (Go, Python, TypeScript) | Shipped | At `sdks/runtime/go`, `sdks/runtime/python`, and `sdks/runtime/typescript`. Not published to a package registry. |
| `lenny` / `lenny-ctl` CLI (same binary) | In progress | `lenny-ctl` serves the resource-management and admin commands over the admin API, plus `preflight`, `values validate`, and `bootstrap`. The Embedded Mode commands (`lenny up`, `lenny down`, `lenny status`, `lenny logs`, `lenny image`, and `lenny session`) are present. |
| `lenny runtime init` / `publish` scaffolder | In progress | `lenny runtime init`, `lenny runtime validate`, and `lenny runtime publish` are present, with templates for scaffolding a runtime. |
| `lenny-ctl install` wizard | In progress | The `lenny-ctl install` command is present. |
| `lenny-ctl doctor --fix` | Not started | Idempotent remediations for common misconfigurations. |

### Management plane (`lenny-ops`)

| Surface | Status | Notes |
|:--------|:-------|:------|
| Diagnostic endpoints | In progress | The gateway serves the Platform Health API (`/v1/admin/health`), which probes the live Postgres and Redis backends, and the self-introspection endpoints (`/v1/admin/me`). The `lenny-ops` service is present. |
| Runbook catalog | Not started | Machine-readable and human-readable. |
| Backup and restore APIs | In progress | Backup code and the `lenny-backup` binary are present. Backups run as transient Jobs scheduled by `lenny-ops`. |
| Drift detection | In progress | Drift detection code is present. It compares observed cluster state to declared configuration. |
| Prometheus alerting rules + OpenSLO + Grafana dashboard | Shipped | The gateway serves a Prometheus `/metrics` scrape target. The chart bundles the alerting rules and OpenSLO definitions, and Compose Mode includes a Grafana dashboard. |
| `EventEmitter` + correlated traces/logs | In progress | Metrics, OpenTelemetry tracing, logging, and correlation packages are present. |

### User-facing extras

| Surface | Status | Notes |
|:--------|:-------|:------|
| Browser playground (`/playground`) | In progress | The gateway serves the `/playground/` routes. Same public API every SDK uses. `playground.enabled` defaults to `false`; `lenny up` enables it. |
| Experimentation primitives (pod variant pools, deterministic routing, basic assignment) | In progress | Experiment code and migrations are present. Infrastructure primitives for rolling runtime versions; most teams will plug in LaunchDarkly, Statsig, Unleash, or any OpenFeature-compatible provider for assignment. |
| Score storage and retrieval | In progress | Basic `/eval` endpoint for persisting scores alongside session state; code and migrations are present. Lenny is compatible with any eval framework (LangSmith, Braintrust, Arize, Langfuse, or a home-grown one) and does not ship one. |
| Memory, semantic caching, guardrail hooks | In progress | Pluggable `MemoryStore`, caching, and extensibility hooks. The memory store code is present. |

---

## Documentation

| Area | Status | Notes |
|:-----|:-------|:------|
| Technical specification | Shipped | [`spec/`](https://github.com/lennylabs/lenny/tree/main/spec). Source of truth. |
| Public docs (this site) | Shipped | Describes the target surface. |
| ADRs | In progress | [`docs/adr/`](https://github.com/lennylabs/lenny/tree/main/docs/adr). New decisions added as they are made. |
| Contributor on-ramp (root files) | Shipped | `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, `GOVERNANCE.md`, `ROADMAP.md`, `CHANGELOG.md`. |

---

## How this page is maintained

- Updates land with the work that changes the status, rather than in a separate pass. Update the "Last updated" date with each change.
- The build sequence in [`spec/18_build-sequence.md`](https://github.com/lennylabs/lenny/blob/main/spec/18_build-sequence.md) is directional; surface ordering may shift as implementation surfaces new constraints. Treat it as a plan rather than a commitment.
- When a surface reaches "Shipped," link to the specific documentation page that describes the shipped behavior.
