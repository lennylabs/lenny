---
layout: default
title: Home
nav_order: 0
permalink: /
---

# Lenny

**Kubernetes-native, runtime-agnostic agent session platform.**

Lenny gives your AI agents on-demand, pre-warmed, isolated cloud instances --
without locking you into a single framework, protocol, or cloud.
Deploy it on your own cluster, plug in any agent runtime, and expose sessions
through the protocols your clients already speak.

---

## Why Lenny?

{: .fs-5 }

| | |
|:--|:--|
| **Runtime-agnostic adapter contract** | Integrate any agent framework -- LangChain, CrewAI, Autogen, a plain shell script -- through a thin gRPC adapter. The platform handles lifecycle, networking, and state so your runtime does not have to. |
| **Security by default** | Pods run non-root, all capabilities dropped, read-only rootfs, default-deny networking. No standing credentials -- only short-lived leases. Gateway-mediated file delivery. Deployer-selectable isolation: gVisor, Kata microVM, or runc. |
| **Recursive delegation** | Agents spawn child sessions with per-hop budget, scope narrowing, isolation monotonicity, content policy inheritance, and cycle detection. The platform enforces the hierarchy; runtimes stay simple. |
| **Self-hosted, K8s-native** | CRDs, controllers, and Helm charts. No SaaS dependency. Runs wherever Kubernetes runs. |
| **Multi-protocol gateway** | A single gateway speaks **REST**, **MCP** (including Tasks and Elicitation), **OpenAI Chat Completions**, and **Open Responses**. Clients connect with the SDK they already have. |
| **Enterprise controls** | Multi-tenancy with row-level security, RBAC, audit logging with hash-chain integrity, budget enforcement, GDPR erasure, legal holds, and data residency. |
| **Ecosystem-composable** | Expose every session as an MCP server. Chain Lenny instances, connect to external tool servers, or nest sessions inside larger pipelines. |

---

## Where do I start?

Pick the card that matches your role.

<div class="grid-cards" markdown="1">

<div class="card" markdown="1">

### Deploy Lenny on your cluster
{: .text-purple-300 }

You run Kubernetes in production and want to offer managed agent sessions to
your teams.

[Operator Guide](/lenny/operator-guide/){: .btn .btn-purple }

</div>

<div class="card" markdown="1">

### Build a runtime adapter
{: .text-blue-300 }

You maintain an agent framework and want it to run inside Lenny pods.

[Runtime Author Guide](/lenny/runtime-author-guide/){: .btn .btn-blue }

</div>

<div class="card" markdown="1">

### Build an application
{: .text-green-300 }

You are writing a product that consumes agent sessions over MCP, OpenAI, or
REST.

[Client Guide](/lenny/client-guide/){: .btn .btn-green }

</div>

<div class="card" markdown="1">

### Evaluate Lenny
{: .text-yellow-300 }

You are comparing Lenny against E2B, Daytona, Runloop, Temporal, or building
your own.

[About](/lenny/about/){: .btn }

</div>

</div>

---

## Quick links

- [Quickstart -- hello world in 5 minutes](/lenny/getting-started/){: .btn .btn-outline }
- [API Reference](/lenny/api/){: .btn .btn-outline }
- [GitHub](https://github.com/your-org/lenny){: .btn .btn-outline }

---

## Feature highlights

{: .fs-4 }

**Pre-warmed pod pools**
:   Pods boot in advance and sit idle. When a session starts, the gateway
    assigns an already-running pod and materialises the workspace into it.
    Cold-start latency drops to single-digit seconds.

**Secure by default**
:   Pods run non-root with all capabilities dropped, read-only root filesystem,
    and default-deny network policies. No standing credentials -- only
    short-lived leases. Gateway-mediated file delivery ensures pods never
    fetch external data directly. mTLS between gateway and pods.

**MCP Tasks + Elicitation**
:   Long-running work is modelled as resumable tasks. When an agent needs
    human input it sends an elicitation request through the gateway; the client
    replies at its own pace. No polling, no webhooks -- just the MCP
    Streamable HTTP transport.

**Recursive delegation with budget and scope controls**
:   A parent session can spawn child sessions, each with a capped token budget,
    a scoped tool allowlist, and an independent isolation boundary. The
    platform tracks the tree and enforces limits at every level.

**Deployer-selectable isolation**
:   Choose the sandbox strength per workload: **runc** for trusted first-party
    code, **gVisor** for moderate isolation, or **Kata Containers** for
    full-VM boundaries. Switch tiers with a single field in the
    `SessionClass` CRD.

**Multi-tenancy with row-level security**
:   Every API object is scoped to an organisation and project. Postgres
    row-level security policies guarantee that one tenant's data is invisible
    to another, even if application code has a bug.

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
