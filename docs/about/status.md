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

Lenny is in the **design phase**. The [technical specification](https://github.com/lennylabs/lenny/tree/main/spec) is complete and drives implementation under a spec- and test-driven workflow. The documentation throughout this site describes the **v1 target surface** — the shape of the platform once the build sequence in [`spec/18_build-sequence.md`](https://github.com/lennylabs/lenny/blob/main/spec/18_build-sequence.md) lands.

This page tracks what is actually wired up today, so you know which parts of the docs describe running code and which parts describe work ahead.

The build sequence itself is directional. Surface ordering and timing will shift as implementation surfaces new constraints; treat `spec/18_build-sequence.md` as the authoritative but evolving source.

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
| Source Mode — `make run` contributor mode | Not started | SQLite + in-memory + local FS; gateway, controller-sim, and echo runtime run as goroutines in one process. |
| Compose Mode — `docker compose up` | Not started | Production-like local stack with real Postgres, Redis, MinIO. Integration testing and TLS exercise. |

### Core runtime

| Surface | Status | Notes |
|:--------|:-------|:------|
| Echo reference runtime | Not started | Basic adapter. Used by the compliance suite. |
| Gateway skeleton (session create / stream / complete) | Not started | First working slice against the wire-contract schemas. |
| Session lifecycle + REST API end-to-end | Not started | Full create → upload → attach → complete flow. |
| Warm pod pool controller | Not started | Keeps pods pre-warmed; handles claim, release, drain. |
| Workspace materialization | Not started | Files delivered through the gateway; no shared mounts. |
| Credential leasing (Basic) | Not started | Short-lived leases; raw keys never enter the pod. |
| Credential rotation (Full integration level) | Not started | Zero-downtime rotation over the lifecycle channel. |
| Checkpoint / resume | Not started | Sessions survive pod failure; artifacts retrievable. |
| Recursive delegation | Not started | Parent spawns child; budgets, permissions, cycle detection. |
| Recursive delegation with MCP semantics | Not started | Delegation reachable through MCP hosts. |
| Multi-tenancy (Postgres RLS, quotas, RBAC) | Not started | Auth foundation first; RBAC + environments follow. |
| Audit log with hash-chain integrity + SIEM | Not started | Durable append-only audit trail with integrity controls. |
| Compliance controls (erasure receipts, legal holds, residency) | Not started | GDPR-style erasure with cryptographic receipt. |
| Security hardening (signed images, admission, pentest) | Not started | Sigstore/cosign + admission controller. |
| SLO validation at Growth-sized load | Not started | Full security hardening active. |

### Gateway protocols

| Protocol | Status | Notes |
|:---------|:-------|:------|
| REST | Not started | First end-to-end session protocol. |
| MCP (Streamable HTTP) | Not started | Interactive streaming and MCP hosts. |
| OpenAI Chat Completions | Not started | Drop-in base-URL swap for existing OpenAI SDK code. |
| Open Responses / OpenAI Responses | Not started | Any Responses-API client. |

### LLM routing in the gateway

| Provider path | Status | Notes |
|:--------------|:-------|:------|
| In-process native Go translator — `anthropic_direct` | In design | `deliveryMode: proxy`. No sidecar, no loopback auth. Keys stay in the gateway's in-memory Token Service cache. |
| Native translator — AWS Bedrock, Vertex AI, Azure OpenAI | In design | Multi-provider coverage + deny-list enforcement + rotation. |
| External LLM proxy integration (LiteLLM, Portkey) | In design | For providers outside the built-in router. Runs alongside native routing. |

### Reference runtime catalog

| Runtime | Status | Notes |
|:--------|:-------|:------|
| `echo` (compliance reference) | Not started | Embedded in the platform repo. |
| `streaming-echo` (CI test runtime) | Not started | Simulated streaming, usage reporting, Full-level lifecycle. |
| `chat` | Not started | Generic LLM chat, no tools. Standard integration level. |
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
| `lenny` / `lenny-ctl` CLI (same binary) | Not started | `lenny up` / `lenny down` / session ops (short name) and operator-facing subcommands (long name). |
| `lenny runtime init` / `publish` scaffolder | Not started | Scaffolds a working runtime from a template; publishes image and registers it in one step. |
| `lenny-ctl install` wizard | Not started | Cluster inspection, guided questions, Helm values output, diff preview, smoke test. Reusable answer file. |
| `lenny-ctl doctor --fix` | Not started | Idempotent remediations for common misconfigurations. |

### Management plane (`lenny-ops`)

| Surface | Status | Notes |
|:--------|:-------|:------|
| Diagnostic endpoints | Not started | Structured endpoints — no `kubectl`-scraping required. |
| Runbook catalog | Not started | Machine-readable and human-readable. |
| Backup and restore APIs | Not started | Transient Jobs scheduled by `lenny-ops` (uses `lenny-backup` image). |
| Drift detection | Not started | Compares observed cluster state to declared configuration. |
| Prometheus alerting rules + OpenSLO + Grafana dashboard | Not started | Bundled artifacts drop into any standard observability stack. |
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
