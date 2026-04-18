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
| **Runtime-agnostic adapter contract** | Integrate any agent framework -- LangChain, CrewAI, AutoGen, a plain shell script -- through a thin gRPC adapter. Scaffold a new runtime with `lenny runtime init` and first-party SDKs in Go, Python, and TypeScript. |
| **Reference runtime catalog** | Ships with working agents out of the box: `claude-code`, `gemini-cli`, `codex`, `cursor-cli`, `chat`, `langgraph`, `mastra`, `openai-assistants`, and `crewai`. Usable on day one; copy them as templates for your own. |
| **Security by default** | Pods run non-root, all capabilities dropped, read-only rootfs, default-deny networking. No standing credentials -- only short-lived leases via a KMS-backed Token Service. Gateway-mediated file delivery. Deployer-selectable isolation: gVisor, Kata microVM, or runc. |
| **Recursive delegation** | Agents spawn child sessions with per-hop budget, scope narrowing, isolation monotonicity, content policy inheritance, and cycle detection. The platform enforces the hierarchy; runtimes stay simple. |
| **Self-hosted, K8s-native** | CRDs, controllers, Helm chart, and an answer-file installer wizard. No SaaS dependency. Runs wherever Kubernetes runs. Evaluate it in under a minute with `lenny up`. |
| **Multi-protocol gateway** | A single gateway speaks **REST**, **MCP** (including Tasks and Elicitation), **OpenAI Chat Completions**, and **Open Responses**. Clients connect with the SDK they already have. A native in-process LLM translator serves `anthropic_direct`, `aws_bedrock`, `vertex_ai`, and `azure_openai` without a sidecar. |
| **Agent-operable platform** | A mandatory `lenny-ops` control plane exposes structured diagnostics, runbooks, backup/restore, drift detection, and bundled alerting rules. `lenny-ctl doctor --fix` auto-remediates the common classes of misconfiguration. |
| **Enterprise controls** | Multi-tenancy with row-level security, RBAC, audit logging with hash-chain integrity, budget enforcement, GDPR erasure, legal holds, and data residency. OIDC and RFC 8693 token exchange via `POST /v1/oauth/token`. |
| **Ecosystem-composable** | Expose every session as an MCP server. Chain Lenny instances, connect to external tool servers, or nest sessions inside larger pipelines. An embedded web playground ships with every installation for zero-install evaluation. |

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

- [Quickstart -- `lenny up` in 5 minutes](/lenny/getting-started/){: .btn .btn-outline }
- [Reference Runtime Catalog](/lenny/reference/){: .btn .btn-outline }
- [API Reference](/lenny/api/){: .btn .btn-outline }
- [GitHub](https://github.com/lenny-dev/lenny){: .btn .btn-outline }

---

## Feature highlights

{: .fs-4 }

**`lenny up` -- evaluate in 60 seconds**
:   A single binary boots an embedded k3s, Postgres, Redis, KMS shim, OIDC
    provider, gateway, `lenny-ops`, and the entire reference runtime catalog
    in-process. No Docker, no cluster setup. Same code path as production --
    not a simulator. Tear down with `lenny down`.

**Reference runtime catalog**
:   Out of the box: `claude-code`, `gemini-cli`, `codex`, `cursor-cli`, `chat`,
    `langgraph`, `mastra`, `openai-assistants`, `crewai`. Coding agents ship
    with pre-installed `git`, `ripgrep`, language toolchains, and
    gVisor-enforced isolation. Copy any of them as a template.

**Installer wizard + answer files**
:   `lenny-ctl install` detects cluster capabilities, asks ~10 targeted
    questions, previews a composed `values.yaml`, runs preflight, invokes
    `helm install`, seeds bootstrap, and runs a smoke test. Capture the
    answers once with `--save-answers`, replay in CI with
    `--non-interactive`.

**Runtime Author SDKs + scaffolding**
:   First-party SDKs for Go (`github.com/lenny-io/runtime-sdk-go`), Python
    (`lenny-runtime`), and TypeScript (`@lenny-io/runtime-sdk`) wrap the
    stdin/stdout JSON Lines protocol, abstract-Unix-socket MCP, and lifecycle
    channel. `lenny runtime init <name> --language <go|python|typescript>
    --template <chat|coding|minimal>` emits a complete repo skeleton.

**Pre-warmed pod pools**
:   Pods boot in advance and sit idle. When a session starts, the gateway
    assigns an already-running pod and materialises the workspace into it.
    Cold-start latency drops to single-digit seconds.

**Secure by default**
:   Pods run non-root with all capabilities dropped, read-only root filesystem,
    and default-deny network policies. No standing credentials -- only
    short-lived leases. Gateway-mediated file delivery ensures pods never
    fetch external data directly. mTLS between gateway and pods. LLM API keys
    stay in gateway memory; the native Go translator rewrites upstream
    requests without a sidecar.

**Agent operability -- `lenny-ops` + `doctor --fix`**
:   A mandatory `lenny-ops` control plane hosts structured diagnostic
    endpoints (`/v1/admin/diagnostics/*`), operational runbooks, audit log
    queries, backup/restore API, drift detection, MCP Management server, and
    bundled Prometheus alerting rules. `lenny-ctl doctor --fix` closes the
    loop by auto-remediating common misconfigurations.

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
    full-VM boundaries. Switch tiers with a single field in the pool
    definition.

**Web Playground**
:   Every installation serves a minimal SPA at `/playground` for zero-install
    evaluation. Speaks the same public MCP surface as any client SDK (no
    private endpoints), gated by OIDC / API key / dev-mode per the
    `playground.authMode` Helm value.

**OpenSLO export**
:   SLO definitions ship as OpenSLO v1 manifests in addition to Prometheus
    alerting rules. Import them into an SLO platform of your choice.

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
