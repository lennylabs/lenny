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

Lenny is in **early implementation**. The [technical specification](https://github.com/lennylabs/lenny/tree/main/spec) is complete and drives implementation under a spec- and test-driven workflow. The documentation throughout this site describes the **v1 target surface** — the shape of the platform once the build sequence in [`spec/18_build-sequence.md`](https://github.com/lennylabs/lenny/blob/main/spec/18_build-sequence.md) lands.

This page tracks what is actually wired up today, so you know which parts of the docs describe running code and which parts describe work ahead.

The build sequence enumerates the v1 application-code phases from Phase 0 (repository bootstrap) through Phase 17b (memory, semantic caching, eval hooks), with Deliverables, Prerequisites, and Exit criteria for each. The sequence is directional: surface ordering and timing will shift as implementation surfaces new constraints. Treat [`spec/18_build-sequence.md`](https://github.com/lennylabs/lenny/blob/main/spec/18_build-sequence.md) as the authoritative but evolving source.

## Legend

| Status | Meaning |
|:-------|:--------|
| **Not started** | No code yet. Design is in the spec. |
| **In design** | Design work in progress; ADRs may be open. No code yet. |
| **In progress** | Code landing on `main`. Not yet complete or not yet usable end-to-end. |
| **Shipped** | Usable against `main`. May still be pre-1.0. |

---

## Platform surfaces

### Local developer experience

| Surface | Status | Notes |
|:--------|:-------|:------|
| Embedded Mode — `lenny up` single-binary stack | Not started | Single binary: embedded k3s, Postgres, Redis, KMS, OIDC, object storage. Same binaries as production, only external dependencies swapped. Reference runtimes pre-installed. |
| Source Mode — `make run` contributor mode | In progress | `make run` builds the echo runtime and the gateway and runs the gateway in dev mode with in-memory stores and the echo runtime wired as a subprocess. |
| Compose Mode — `docker compose up` | Not started | Production-like local stack with real Postgres, Redis, MinIO. Integration testing and TLS exercise. |

### Core runtime

| Surface | Status | Notes |
|:--------|:-------|:------|
| Echo reference runtime | Shipped | Basic-level adapter at `cmd/runtimes/echo`. Driven by the conformance suite and by the gateway's subprocess executor. |
| Gateway skeleton (session create / stream / complete) | In progress | `cmd/lenny-gateway` serves the §15.1 REST surface, the SSE event stream, and the §15.2 MCP and OpenAI adapters. It persists to Postgres and Redis when `--postgres-dsn` and `--redis-url` are set, and uses in-memory stores otherwise. |
| Session lifecycle + REST API end-to-end | In progress | Create, finalize, start, interrupt, terminate, resume, derive, upload, messages, transcript, tree, and events are all served. Postgres-backed session, transcript, tenant, runtime, user, and connector stores are available via `--postgres-dsn`. |
| Database schema and migrations | Shipped | The `migrations/` schema applied by the golang-migrate framework. Covers the §12 tables, the §12.3 row-level security and `lenny_tenant_guard` trigger, the §11.7 ledger-immutability triggers, and the `lenny_app` and `lenny_erasure` role separation. |
| Warm pod pool controller | Not started | Keeps pods pre-warmed; handles claim, release, drain. |
| Workspace materialization | In progress | The §14 workspace plan is parsed and validated on session creation; uploaded files are stored in the blob store. Pod-side materialization is pending the pod model. |
| Credential leasing (Basic) | In progress | The §4.9 end-user credential registry and `/v1/credentials` endpoints are served. Lease assignment on session creation is pending. |
| Credential rotation (Full integration level) | Not started | Zero-downtime rotation over the lifecycle channel. |
| Checkpoint / resume | Not started | Sessions survive pod failure; artifacts retrievable. |
| Recursive delegation | In progress | The §8 delegation service enforces cycle detection, isolation monotonicity, and the depth limit. Budgets and lease extension are pending. |
| Recursive delegation with MCP semantics | In progress | `lenny/delegate_task` and the other §8.5 platform tools are served by the MCP adapter. |
| Multi-tenancy (Postgres RLS, quotas, RBAC) | In progress | RBAC role enforcement and the §5.75 quota interceptor are active. The §12.3 row-level security policies and the `lenny_tenant_guard` transaction-isolation trigger ship in the `migrations/` schema. |
| Audit log with hash-chain integrity + SIEM | In progress | The §11.7 per-tenant audit hash chain records every admin mutation and is queryable. The chain is durable in the Postgres `audit_log` table under `--postgres-dsn`, the ledger-immutability triggers and the startup integrity check are active, and SIEM streaming is pending. |
| Compliance controls (erasure receipts, legal holds, residency) | In progress | GDPR redaction receipts are modelled on the audit chain. The erasure pipeline and residency controls are pending. |
| Security hardening (signed images, admission, pentest) | Not started | Sigstore/cosign + admission controller. |
| SLO validation at Growth-sized load | Not started | Full security hardening active. |

### Gateway protocols

| Protocol | Status | Notes |
|:---------|:-------|:------|
| REST | In progress | The §15.1 session, blob, and admin endpoints are served end-to-end, against Postgres-backed or in-memory stores. |
| MCP (Streamable HTTP) | In progress | The `/mcp` JSON-RPC adapter serves initialize, tools/list, and tools/call. Streaming over SSE is pending. |
| OpenAI Chat Completions | In progress | `/v1/chat/completions` translates to the session surface, including streaming responses (`stream: true`). |
| Open Responses / OpenAI Responses | In progress | `/v1/responses` translates to the session surface, including streaming responses (`stream: true`). |

### LLM routing in the gateway

| Provider path | Status | Notes |
|:--------------|:-------|:------|
| In-process native Go translator — `anthropic_direct` | In design | `deliveryMode: proxy`. No sidecar, no loopback auth. Keys stay in the gateway's in-memory Token Service cache. |
| Native translator — AWS Bedrock, Vertex AI, Azure OpenAI | In design | Multi-provider coverage + deny-list enforcement + rotation. |
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
| Go SDK | Not started | Official client SDK. |
| TypeScript SDK | Not started | Official client SDK. |
| Python SDK | Not started | Official client SDK. |
| `lenny` / `lenny-ctl` CLI (same binary) | In progress | `lenny-ctl` serves the resource-management subset (health, version, tenants, runtimes, bootstrap) over the admin API. The `lenny up` / `lenny down` Embedded Mode commands are pending. |
| `lenny runtime init` / `publish` scaffolder | Not started | Scaffolds a working runtime from a template; publishes image and registers it in one step. |
| `lenny-ctl install` wizard | Not started | Cluster inspection, guided questions, Helm values output, diff preview, smoke test. Reusable answer file. |
| `lenny-ctl doctor --fix` | Not started | Idempotent remediations for common misconfigurations. |

### Management plane (`lenny-ops`)

| Surface | Status | Notes |
|:--------|:-------|:------|
| Diagnostic endpoints | In progress | The gateway serves the §25.3 Platform Health API (`/v1/admin/health`), which probes the live Postgres and Redis backends, and the §25.4 self-introspection endpoints (`/v1/admin/me`). The remaining diagnostic endpoints are pending. |
| Runbook catalog | Not started | Machine-readable and human-readable. |
| Backup and restore APIs | Not started | Transient Jobs scheduled by `lenny-ops` (uses `lenny-backup` image). |
| Drift detection | Not started | Compares observed cluster state to declared configuration. |
| Prometheus alerting rules + OpenSLO + Grafana dashboard | In progress | The gateway serves a Prometheus `/metrics` scrape target; the bundled alerting rules and dashboards are pending. |
| `EventEmitter` + correlated traces/logs | Not started | Correlation fields across all components. |

### User-facing extras

| Surface | Status | Notes |
|:--------|:-------|:------|
| Browser playground (`/playground`) | Not started | Same public API every SDK uses. Off by default in production (one Helm flag). |
| Experimentation primitives (pod variant pools, deterministic routing, basic assignment) | Not started | Infrastructure primitives for rolling runtime versions; most teams will plug in LaunchDarkly, Statsig, Unleash, or any OpenFeature-compatible provider for assignment. |
| Score storage and retrieval | Not started | Basic `/eval` endpoint for persisting scores alongside session state. Lenny is compatible with any eval framework (LangSmith, Braintrust, Arize, Langfuse, home-grown) — it does not ship one. |
| Memory, semantic caching, guardrail hooks | Not started | Pluggable `MemoryStore`, caching, and extensibility hooks. |

---

## Documentation

| Area | Status | Notes |
|:-----|:-------|:------|
| v1 technical specification | Shipped | [`spec/`](https://github.com/lennylabs/lenny/tree/main/spec). Source of truth. |
| Public docs (this site) | Shipped | Describes the v1 target surface. |
| ADRs | In progress | [`docs/adr/`](https://github.com/lennylabs/lenny/tree/main/docs/adr). New decisions added as they are made. |
| Contributor on-ramp (root files) | Shipped | `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, `GOVERNANCE.md`, `ROADMAP.md`, `CHANGELOG.md`. |

---

## How this page is maintained

- Updates land with the work that changes the status — not in a separate pass.
- The build sequence in [`spec/18_build-sequence.md`](https://github.com/lennylabs/lenny/blob/main/spec/18_build-sequence.md) is directional; surface ordering may shift as implementation surfaces new constraints. Treat it as a plan, not a commitment.
- When a surface reaches "Shipped," link to the specific documentation page that describes the shipped behavior.
