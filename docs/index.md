---
layout: default
title: Home
nav_order: 0
permalink: /
---

# Lenny

**Run interactive AI agents in isolated sandboxes on your own Kubernetes cluster.**

Lenny gives every agent session its own locked-down pod. Your agent -- whether it's Claude Code, a LangGraph graph, or a custom binary -- runs with a fresh workspace, leased credentials, and a tight network perimeter. Clients talk to a single gateway that speaks the protocols you probably already use: REST, MCP, the OpenAI Chat Completions API, and the Open Responses API.

Lenny is self-hosted. There is no managed service and no telemetry sent to an external vendor; data stays inside the cluster you operate.

{: .note }
> **Status: design phase.** The specification is complete and drives implementation under a spec- and test-driven workflow. These docs describe the v1 surface; see [Implementation Status](about/status) for what's wired up today. Design feedback is welcome right now — open an [issue](https://github.com/lennylabs/lenny/issues) or a [discussion](https://github.com/lennylabs/lenny/discussions).

---

## What's included

**Run it on your laptop in one command.** `lenny up` is a single binary that starts an embedded Kubernetes cluster, the gateway, the management plane, and a catalog of ready-to-use agent runtimes. First boot takes about a minute; subsequent starts are seconds. It runs the same gateway, controller, and management-plane binaries as a production cluster.

**Reference runtime catalog.** Every install includes nine pre-registered runtimes: `claude-code`, `gemini-cli`, `codex`, `cursor-cli`, `chat`, `langgraph`, `mastra`, `openai-assistants`, and `crewai`. You can use them as-is, fork them, or register your own alongside.

**Bring your own agent in ~50 lines.** The minimum integration is a program that reads JSON from stdin and writes JSON to stdout. If you want more -- tool access, delegation, memory, clean interrupts, zero-downtime credential rotation -- there are higher integration levels that add them incrementally. Official SDKs for Go, Python, and TypeScript take care of the wire format.

**Scaffolding and publishing built in.** `lenny runtime init my-agent --language go --template coding` emits a working repo. `lenny runtime publish` pushes the image and registers it against a gateway in one step.

**One gateway, four protocols.** Your clients can use whichever they already have code for:

| Protocol | Use it for |
|:--|:--|
| REST | CI/CD, automation, admin dashboards, any HTTP client |
| MCP (Streamable HTTP) | Interactive streaming, multi-agent delegation, MCP hosts (Claude Desktop, Cursor) |
| OpenAI Chat Completions | Existing OpenAI SDK code -- change the base URL, keep the rest |
| Open Responses / OpenAI Responses | Any Responses-API client |

**LLM provider routing in the gateway.** The gateway routes requests to Anthropic, AWS Bedrock, Google Vertex AI, and Azure OpenAI directly, with no additional proxy process to run. Agents receive short-lived lease tokens; raw API keys stay in the gateway's memory and never reach the pod. For other providers, route through an external LLM proxy such as LiteLLM or Portkey alongside the built-in routing.

**Pick your isolation level per pool.** You can run first-party code under plain containers (`runc`) when you trust it, sandbox untrusted code under gVisor (the default), or put high-risk workloads in a microVM with Kata Containers. All three are selected as Kubernetes `RuntimeClass` on the pool.

**Agents can delegate to other agents.** A parent agent can spawn a child agent, pass work, enforce a token budget, narrow permissions, and collect the result. The gateway tracks the tree, blocks cycles, and ensures a child cannot become less isolated than its parent.

**Installable with a wizard.** `lenny-ctl install` inspects your cluster, asks about ten targeted questions, composes a Helm values file, previews the diff, runs the install, and smoke-tests it. The answer file is reusable -- you can replay installs in CI.

**Management plane included.** A dedicated management plane (`lenny-ops`) ships with every install. It exposes structured diagnostic endpoints, runbooks, backup and restore APIs, and drift detection, so an on-call engineer (or an on-call AI agent) can investigate without scraping `kubectl` output. `lenny-ctl doctor --fix` applies idempotent remediations for common misconfigurations.

**Monitoring is wired up.** Bundled Prometheus alerting rules and OpenSLO definitions drop into any standard observability stack. There's a Grafana dashboard for core health signals.

**A browser-based playground.** Every install serves a web UI at `/playground` that drives sessions through the same public API any client SDK uses. Demo a runtime without writing code. Turn it off in production with one Helm flag.

**Multi-tenancy.** Postgres row-level security partitions every query by tenant. A tamper-evident audit log records every state change. Per-tenant quotas, role-based access control, and signed JWT-based authentication are configurable.

**Compliance controls.** GDPR-style erasure that returns a cryptographic receipt. Legal holds. Configurable retention windows compatible with SOC 2, HIPAA, and FedRAMP control sets. Data residency policy that pins sessions to specific regions.

Every capability on this page is specified in [`spec/`](https://github.com/lennylabs/lenny/tree/main/spec) and covered by the integration test suite.

---

## Pick your entry point

<div class="grid-cards" markdown="1">

<div class="card" markdown="1">

### Evaluate Lenny
{: .text-yellow-300 }

Decide if it fits. Start with `lenny up` and the comparison guide.

[Quickstart](/lenny/getting-started/){: .btn .btn-outline }
[About](/lenny/about/){: .btn }

</div>

<div class="card" markdown="1">

### Build a client application
{: .text-green-300 }

You call Lenny from an app, a script, or an MCP host.

[Client Guide](/lenny/client-guide/){: .btn .btn-green }

</div>

<div class="card" markdown="1">

### Build a runtime
{: .text-blue-300 }

You are bringing your own agent binary or adapter.

[Runtime Author Guide](/lenny/runtime-author-guide/){: .btn .btn-blue }

</div>

<div class="card" markdown="1">

### Operate Lenny on a cluster
{: .text-purple-300 }

You install, configure, and scale the platform.

[Operator Guide](/lenny/operator-guide/){: .btn .btn-purple }

</div>

<div class="card" markdown="1">

### Keep Lenny running
{: .text-purple-300 }

You are on-call. `lenny-ops`, doctor, runbooks, alerts.

[Agent Operability](/lenny/operator-guide/agent-operability){: .btn .btn-purple }

</div>

<div class="card" markdown="1">

### Review security and compliance
{: .text-red-300 }

You assess Lenny for isolation, audit, and regulatory fit.

[Security](/lenny/operator-guide/security){: .btn }

</div>

</div>

---

## Quick links

- [Quickstart -- `lenny up` in 5 minutes](/lenny/getting-started/){: .btn .btn-outline }
- [Reference Runtime Catalog](/lenny/reference/){: .btn .btn-outline }
- [API Reference](/lenny/api/){: .btn .btn-outline }
- [GitHub](https://github.com/lennylabs/lenny){: .btn .btn-outline }

---

## Design invariants

The following properties are enforced by the code and the integration test suite:

1. **Every external request goes through the gateway.** Pods are never addressable from outside the cluster. The gateway is the single place authentication, rate limits, quotas, delegation budgets, and audit are enforced.
2. **Pod disks are scratch space.** The durable state of a session -- transcript, artifacts, checkpoints -- lives in Postgres, Redis, and object storage. If a pod dies, the platform resumes the session on a new pod from its last checkpoint.
3. **No standing credentials inside the pod.** Credentials are leased per session, scoped to what that session needs, and can rotate without restarting the agent. Workspaces have no shared mounts; files arrive through the gateway.
4. **Pods are kept warm.** The warm pool controller keeps idle pods ready so a new session does not wait for container boot. The hot path materializes the workspace and starts the agent, typically in a few seconds.
5. **Lenny does not implement AI behavior.** It does not score evaluations, extract memories, or classify content. It exposes interfaces where those tools can be plugged in.
6. **Every operator-visible state is available as an API.** Human on-call and AI on-call use the same endpoints; neither has to parse `kubectl` output.

---

<style>
.grid-cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 1rem;
  margin-top: 1rem;
}
.card {
  border: 1px solid var(--border-color, #e1e4e8);
  border-radius: 6px;
  padding: 1.25rem;
}
</style>
