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

Lenny is in the **design phase**. The [technical specification](https://github.com/lennylabs/lenny/tree/main/spec) is complete and drives implementation under a spec- and test-driven workflow. The documentation throughout this site describes the **v1 target surface** — the shape of the platform once the phase plan in [`spec/18_build-sequence.md`](https://github.com/lennylabs/lenny/blob/main/spec/18_build-sequence.md) lands.

This page tracks what is actually wired up today, so you know which parts of the docs describe running code and which parts describe work ahead.

## Legend

| Status | Meaning |
|:-------|:--------|
| **Not started** | No code yet. Design is in the spec. |
| **In design** | Design work in progress; ADRs may be open. No code yet. |
| **In progress** | Code landing on `main`. Not yet complete or not yet usable end-to-end. |
| **Shipped** | Usable against `main`. May still be pre-1.0. |

Phases refer to the build sequence in [`spec/18_build-sequence.md`](https://github.com/lennylabs/lenny/blob/main/spec/18_build-sequence.md).

---

## Platform surfaces

### Core runtime

| Surface | Status | Target phase | Notes |
|:--------|:-------|:-------------|:------|
| `make run` local dev mode | Not started | Phase 2 | First working slice with embedded SQLite, in-process KV, local FS. |
| Echo reference runtime | Not started | Phase 2 | Basic adapter. Used by the compliance suite. |
| Gateway skeleton (session create / stream / complete) | Not started | Phase 2 | REST surface first; other protocols layer in later phases. |
| Warm pod pool controller | Not started | Phase 5 | Workspace materialization at request time. |
| Credential leasing | Not started | Phase 5.5 | Short-lived leases; raw keys never enter the pod. |
| Credential rotation (Full integration level) | Not started | Phase 6+ | Zero-downtime rotation over the lifecycle channel. |
| Recursive delegation | Not started | Phase 9 | Parent spawns child; budgets, permissions, cycle detection. |
| Multi-tenancy (Postgres RLS, audit log, RBAC, quotas) | Not started | Phase 11–12 | Row-level security partitions every query by tenant. |
| Compliance controls (erasure receipts, legal holds, residency) | Not started | Phase 12 | GDPR-style erasure with cryptographic receipt. |
| Security hardening (signed images, admission, pentest) | Not started | Phase 14 | Sigstore/cosign + admission controller. |
| SLO validation at Growth-sized load | Not started | Phase 14.5 | Full security hardening active. |

### Gateway protocols

| Protocol | Status | Target phase | Notes |
|:---------|:-------|:-------------|:------|
| REST | Not started | Phase 2 | First protocol to land. |
| MCP (Streamable HTTP) | Not started | Phase 8–9 | Interactive streaming, delegation, MCP hosts. |
| OpenAI Chat Completions | Not started | Phase 10 | Drop-in base-URL swap for existing OpenAI SDK code. |
| Open Responses / OpenAI Responses | Not started | Phase 10 | Any Responses-API client. |

### LLM routing in the gateway

| Provider path | Status | Target phase | Notes |
|:--------------|:-------|:-------------|:------|
| In-process native Go translator (Anthropic, Bedrock, Vertex AI, Azure OpenAI) | In design | Phase 10 | No sidecar. Keys stay in gateway memory. |
| External LLM proxy integration (LiteLLM, Portkey) | In design | Phase 10 | For providers not covered by the built-in router. |

### Runtime catalog

| Runtime | Status | Target phase | Notes |
|:--------|:-------|:-------------|:------|
| `echo` (compliance reference) | Not started | Phase 2 | |
| `claude-code` | Not started | Phase 13 | |
| `gemini-cli` | Not started | Phase 13 | |
| `codex` | Not started | Phase 13 | |
| `cursor-cli` | Not started | Phase 13 | |
| `chat` | Not started | Phase 13 | Generic chat runtime. |
| `langgraph` | Not started | Phase 13 | |
| `mastra` | Not started | Phase 13 | |
| `openai-assistants` | Not started | Phase 13 | |
| `crewai` | Not started | Phase 13 | |

### SDKs and CLI

| Surface | Status | Target phase | Notes |
|:--------|:-------|:-------------|:------|
| Go SDK | Not started | Phase 2–3 | |
| Python SDK | Not started | Phase 3 | |
| TypeScript SDK | Not started | Phase 3 | |
| `lenny` CLI (user-facing) | Not started | Phase 2+ | `lenny up`, session ops. |
| `lenny-ctl` (operator CLI) | Not started | Phase 11+ | Install wizard, `doctor --fix`, diagnostics. |
| `lenny runtime init` / `publish` | Not started | Phase 13 | Scaffolding and one-step publish. |

### Management plane

| Surface | Status | Target phase | Notes |
|:--------|:-------|:-------------|:------|
| `lenny-ops` diagnostic endpoints | Not started | Phase 11 | Structured endpoints; no `kubectl`-scraping required. |
| Runbooks | Not started | Phase 11 | Machine-readable and human-readable. |
| Backup and restore APIs | Not started | Phase 12 | |
| Drift detection | Not started | Phase 11 | |
| `lenny-ctl install` wizard | Not started | Phase 11 | Reusable answer file. |
| Prometheus alerting rules, OpenSLO, Grafana dashboard | Not started | Phase 11 | |

### User-facing extras

| Surface | Status | Target phase | Notes |
|:--------|:-------|:-------------|:------|
| Browser playground (`/playground`) | Not started | Phase 15 | Drives sessions through the same public API. Off by default in production. |

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
- Phase numbers come from [`spec/18_build-sequence.md`](https://github.com/lennylabs/lenny/blob/main/spec/18_build-sequence.md); if a phase is renumbered, update here too.
- When a surface reaches "Shipped," link to the specific documentation page that describes the shipped behavior.
