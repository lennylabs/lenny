---
layout: default
title: Getting Started
nav_order: 1
has_children: true
description: Quickstart, core concepts, and architecture overview — the right entry point whether you're a client developer, operator, or runtime author.
---

# Getting Started with Lenny

Lenny runs interactive AI agent sessions in isolated sandboxes on your own Kubernetes cluster. Clients talk to a single gateway; each session gets its own pod with a fresh workspace and leased credentials. The agent itself can be anything from Anthropic's Claude Code CLI to a LangGraph graph to a custom program. A reference runtime catalog defines runtimes for common agents, and the [Runtime Author Guide](../runtime-author-guide/) covers building your own.

---

## Start here

Read the following pages in order. Together they take less than an hour and leave you with a working local install.

| Page | Contents | Time |
|------|----------|------|
| [Quickstart](quickstart.html) | Install the CLI, start the embedded stack with `lenny up`, run a session against the `echo` runtime, and open the web playground | ~5 minutes |
| [Core Concepts](concepts.html) | The vocabulary: sessions, runtimes, pools, the gateway, delegation, workspaces, tenants, and credentials | ~20 minutes |
| [Architecture Overview](architecture.html) | Component diagram, request flow, where state lives, and the trust boundaries between pods, the gateway, and clients | ~15 minutes |

---

## Where to go next

After the Quickstart, continue based on your role. The short next-steps map below picks up where the [home-page persona grid](../) left off.

| Role | Next | Why |
|:--|:--|:--|
| **Building an agent** | [Runtime Author Guide](../runtime-author-guide/) | The integration levels, the adapter protocol, and the Go/Python/TypeScript SDKs. Scaffold first with `lenny runtime init`. |
| **Deploying to a cluster** | [Operator Guide](../operator-guide/) | The install wizard, configuration reference, `lenny-ctl doctor --fix`, capacity planning, production hardening. |
| **Calling Lenny from code** | [Client Guide](../client-guide/) | REST, MCP, OpenAI, Open Responses. Start with Authentication and Session Lifecycle, then pick your protocol. |
| **On call for Lenny** | [Agent Operability](../operator-guide/agent-operability) | Diagnostic endpoints, runbook catalog, drift detection, backup/restore. Then [Runbooks](../runbooks/). |
| **Reviewing for security/compliance** | [Security Principles](../operator-guide/security-principles) | Design posture plus the SOC 2 / ISO 27001 / HIPAA / FedRAMP / PCI DSS / GDPR clause mapping. |
| **Contributing to Lenny** | [Contributing](../about/contributing) and the [`spec/`](https://github.com/lennylabs/lenny/tree/main/spec) | Development workflow, code conventions, ADR process, and the canonical spec. |
