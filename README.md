# Lenny

**Kubernetes-native, runtime-agnostic agent session platform.**

Lenny manages pools of pre-warmed, isolated agent pods on Kubernetes behind a unified gateway. It handles session lifecycle, workspace setup, credential leasing, recursive delegation, experimentation, evaluation, policy enforcement, and recovery — so your team can run AI agents as a shared, on-demand cloud service.

[Documentation](docs/) | [Quickstart](#quickstart) | [Why Lenny?](#why-lenny) | [Contributing](#contributing)

---

## What Lenny Does

- **Pre-warmed pods** — agent containers are already running when a request arrives, eliminating cold-start latency. Pod claim is in the millisecond range; workspace setup is the only hot-path work.
- **Runtime-agnostic** — any process that implements the [adapter contract](#runtime-adapter-contract) can run as a Lenny agent. Claude Code, LangChain, CrewAI, custom scripts — no SDK lock-in.
- **Two runtime types** — `type: agent` runtimes participate in the full task lifecycle (sessions, delegation, elicitation, multi-turn dialog). `type: mcp` runtimes host MCP servers with Lenny-managed pods (isolation, credentials, lifecycle) but no task lifecycle — the runtime binary is oblivious to Lenny.
- **Three execution modes** — `session` (one session per pod, default), `task` (pod reuses across sequential tasks with workspace scrub), and `concurrent` (multiple simultaneous tasks via slot multiplexing). Each mode has distinct scaling, isolation, and lifecycle characteristics.
- **Isolated workspaces** — each session gets its own sandboxed filesystem with deployer-selectable isolation (runc, gVisor, Kata microVM).
- **Interactive sessions** — full bidirectional streaming with follow-up prompts, interrupts, tool use, and elicitation — not just request/response.
- **Recursive delegation** — agents spawn child agents through the gateway with enforced token budgets, scope narrowing, and lineage tracking at every hop.
- **Multi-protocol gateway** — REST, MCP, OpenAI Chat Completions, and Open Responses clients connect to the same infrastructure via the `ExternalAdapterRegistry`.
- **Credential leasing** — the platform manages LLM provider credentials in pools, assigns short-lived leases to sessions, and rotates automatically on rate limiting. Pods never see raw API keys.
- **A/B experimentation** — built-in experiment primitives for runtime version rollouts with variant pools, deterministic bucketing, and automatic eval attribution.
- **Evaluation hooks** — pull-based, multi-dimensional scoring that integrates with any external eval pipeline. Session replay for regression testing across runtime versions.
- **Agent memory** — pluggable `MemoryStore` interface (default: Postgres + pgvector) replaceable with Mem0, Zep, or any vector database.
- **Request interceptors** — 12-phase hook chain for guardrails, content policy, custom routing, and LLM request/response inspection. Compatible with AWS Bedrock Guardrails, Azure Content Safety, Lakera Guard, or custom gRPC classifiers.
- **Enterprise controls** — multi-tenancy with Postgres RLS, per-tenant quotas, RBAC, audit logging with hash-chained integrity, GDPR erasure, legal holds, and data residency.
- **Recovery** — if a pod dies, the gateway resumes the session on a new pod from a workspace checkpoint, within configurable retry limits.

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                    Client / MCP Host                         │
└────────────────────────────┬────────────────────────────────┘
                             │ REST / MCP / OpenAI / Open Responses
                             ▼
┌─────────────────────────────────────────────────────────────┐
│                   Gateway Edge Replicas                      │
│  ┌────────┐ ┌──────────┐ ┌─────────┐ ┌─────────────────┐   │
│  │ Auth / │ │ Policy   │ │ Session │ │ MCP Fabric      │   │
│  │ OIDC   │ │ Engine + │ │ Router  │ │ (tasks,         │   │
│  │        │ │ Intercep-│ │         │ │  elicitation,   │   │
│  │        │ │ tors     │ │         │ │  delegation)    │   │
│  └────────┘ └──────────┘ └─────────┘ └─────────────────┘   │
└────────┬─────────┬──────────┬──────────┬───────────────────┘
         │         │          │          │
    ┌────▼───┐ ┌───▼───┐ ┌───▼────┐ ┌───▼─────┐
    │Session │ │Token/ │ │Event/  │ │Artifact │
    │Manager │ │Connec-│ │Checkpt │ │Store    │
    │(PG+Red)│ │tor Svc│ │Store   │ │         │
    └────────┘ └───────┘ └────────┘ └─────────┘

        Gateway ←── mTLS ──→ Pods (gRPC control protocol)

┌─────────────────────────────────────────────────────────────┐
│  Warm Pool Controller (pod lifecycle, agent-sandbox CRDs)    │
│  PoolScalingController (scaling intelligence, experiments)   │
└────────┬───────────────┬────────────────┬───────────────────┘
         │               │                │
    ┌────▼────┐    ┌─────▼─────┐    ┌─────▼─────┐
    │  Pod A  │    │   Pod B   │    │   Pod C   │
    │┌───────┐│    │┌─────────┐│    │┌─────────┐│
    ││Adapter││    ││ Adapter  ││    ││ Adapter  ││
    │├───────┤│    │├─────────┤│    │├─────────┤│
    ││Agent  ││    ││  Agent   ││    ││  Agent   ││
    │└───────┘│    │└─────────┘│    │└─────────┘│
    └─────────┘    └───────────┘    └───────────┘
```

**Gateway** — the only externally-facing component. Handles authentication (OIDC/OAuth 2.1), protocol adaptation (REST, MCP, OpenAI, Open Responses), session routing, file uploads, credential leasing via LLM Proxy, delegation mediation, experiment routing, and policy enforcement. Internally partitioned into four subsystems (Stream Proxy, Upload Handler, MCP Fabric, LLM Proxy) with independent concurrency limits and circuit breakers. Scales horizontally with externalized state.

**Warm Pool Controller** — a Kubernetes controller that manages `kubernetes-sigs/agent-sandbox` CRDs. Keeps pods pre-warmed and handles claim/release/drain lifecycle.

**PoolScalingController** — manages desired pool configuration, scaling intelligence, and experiment variant pool sizing. Reconciles pool config from Postgres into CRDs.

**Token/Connector Service** — separate process with its own ServiceAccount and KMS access. Manages OAuth tokens for external tools and LLM provider credentials. Gateway replicas call it over mTLS.

**Runtime Adapter** — a sidecar container in each agent pod that speaks the Lenny gRPC protocol. Bridges between the gateway and the agent binary via stdin/stdout JSON Lines.

**Agent Binary** — your code. Runs inside the pod, does the actual work.

## Runtime Adapter Contract

Lenny is not tied to any specific agent runtime. It defines a tiered adapter contract:

| Tier         | Interface               | Effort            | Capabilities                                                           |
| ------------ | ----------------------- | ----------------- | ---------------------------------------------------------------------- |
| **Minimum**  | stdin/stdout JSON Lines | ~50 lines, no SDK | Basic session lifecycle, text I/O                                      |
| **Standard** | gRPC adapter            | Moderate          | Health checks, tool reporting, credential flows                        |
| **Full**     | gRPC adapter            | Significant       | Cooperative checkpointing, delegation, elicitation, interrupt handling |

You can run Claude Code agents, LangChain agents, CrewAI agents, code review bots, research agents, or any long-lived process. Multiple runtime types can be registered and run simultaneously, each with their own pools and configuration.

## Key Features

### Runtime Types and Execution Modes

Lenny supports two runtime types:

- **`type: agent`** — full task lifecycle. Receives tasks via stdin, supports sessions, workspaces, delegation, elicitation, and multi-turn dialog. Callable via `lenny/delegate_task`. Integration depth is tiered (Minimum → Standard → Full).
- **`type: mcp`** — hosts an MCP server behind Lenny-managed infrastructure (isolation, credentials, pool scaling, audit). No task lifecycle — the runtime binary is oblivious to Lenny. Each `type: mcp` runtime gets a dedicated endpoint at `/mcp/runtimes/{runtime-name}`.

Agent runtimes run in one of three execution modes:

| Mode | Pod usage | Use case |
|------|-----------|----------|
| **`session`** (default) | One session per pod. Pod terminated after session ends. | Most workloads. Strongest isolation — no cross-session data leakage. |
| **`task`** | Pod reused across sequential tasks with workspace scrub between tasks. Tenant-pinned. | High-throughput batch workloads where pod startup cost matters. Requires Full-tier adapter for between-task lifecycle signaling. |
| **`concurrent`** | Multiple simultaneous tasks on one pod via slot multiplexing. Two sub-variants: `workspace` (per-slot workspace directories) and `stateless` (no workspace, Service-routed). | Parallel processing, semi-stateless workloads. Each slot gets independent credentials and lifecycle. |

Execution mode is declared on the `Runtime` definition and determines pool scaling formulas, checkpoint behavior, and pod retirement policies.

### Session Lifecycle

Create a session, upload workspace files, run setup commands, start the agent, interact via streaming, and retrieve artifacts when done. The gateway manages the full lifecycle including periodic checkpointing and resume after pod failure.

Sessions support derive (fork from a completed session's workspace) and replay (re-run prompt history against a different runtime version for regression testing).

### Credential Leasing and LLM Proxy

The platform manages LLM provider credentials (Anthropic API keys, AWS Bedrock roles, Vertex AI service accounts) in admin-configured pools. Sessions receive short-lived credential leases — never raw API keys. The LLM Proxy gateway subsystem injects real credentials into upstream LLM requests on behalf of pods, with automatic rotation on rate limiting and SPIFFE-bound lease tokens in multi-tenant deployments.

A pluggable `CredentialRouter` interface supports cost-aware, latency-based, or intent-based routing across providers.

### Recursive Delegation

Any agent can spawn child agents through gateway-mediated platform tools (`lenny/delegate_task`). The gateway enforces delegation leases at every hop:

- Maximum depth and fan-out limits
- Token budget (allocated from parent, tracked via Redis Lua scripts)
- Scope narrowing (children can only have equal or fewer permissions)
- Isolation monotonicity (children must be at least as isolated as parents)
- Content policy inheritance (can only be made stricter, never relaxed)
- Cycle detection (prevents A → B → A runtime loops)

### Experimentation

Built-in A/B experiment primitives for runtime version rollouts:

- `ExperimentDefinition` as a first-class admin API resource (`active` / `paused` / `concluded`)
- `ExperimentRouter` with deterministic HMAC-SHA256 bucketing and sticky assignment (per-user/per-session/none)
- Automatic variant pool sizing via PoolScalingController (adjusts both variant and base pools)
- External targeting integration (LaunchDarkly, Statsig, Unleash, generic webhook)
- Delegation propagation modes: `inherit`, `control`, `independent`

### Evaluation Hooks

Pull-based evaluation framework that integrates with any external scoring pipeline:

- `POST /v1/sessions/{id}/eval` — multi-dimensional scoring (`score` + `scores` breakdown)
- Automatic experiment attribution — gateway populates `experiment_id` and `variant_id`
- `GET /v1/admin/experiments/{name}/results` — per-variant aggregation (mean, p50, p95, per-dimension)
- `POST /v1/sessions/{id}/replay` — session replay for regression testing
- Delegation-aware: `delegation_depth` and `inherited` fields for sample contamination filtering

Lenny does not build statistical significance testing, automatic winner declaration, or LLM-as-judge integration. Eval computation is the deployer's responsibility.

### Memory Store

Pluggable `MemoryStore` interface scoped by tenant, user, agent type, and session. Default implementation: Postgres + pgvector with full RLS tenant isolation. Replaceable with Mem0, Zep, or any vector database. Accessed by runtimes via `lenny/memory_write` and `lenny/memory_query` platform MCP tools.

### Gateway Request Interceptors

12-phase hook chain for custom logic at every stage of request processing:

`PreAuth` → `PostAuth` → `PreRoute` → `PreDelegation` → `PreMessageDelivery` → `PostRoute` → `PreToolResult` → `PostAgentOutput` → `PreLLMRequest` → `PostLLMResponse` → `PreConnectorRequest` → `PostConnectorResponse`

Built-in interceptors: `AuthEvaluator`, `QuotaEvaluator`, `DelegationPolicyEvaluator`, `ExperimentRouter`, `GuardrailsInterceptor` (disabled by default), `RetryPolicyEvaluator`. External interceptors are invoked via gRPC and can `ALLOW`, `DENY`, or `MODIFY` content.

### Isolation Profiles

Choose per-pool isolation via Kubernetes `RuntimeClass`:

| Profile               | Runtime         | Use Case                              |
| --------------------- | --------------- | ------------------------------------- |
| `sandboxed` (default) | gVisor          | Most workloads                        |
| `microvm`             | Kata Containers | Higher-risk or multi-tenant workloads |
| `standard`            | runc            | Development/testing (explicit opt-in) |

### Multi-Protocol Gateway

| Protocol                | Path                        | Status  |
| ----------------------- | --------------------------- | ------- |
| MCP (Streamable HTTP)   | `/mcp`                      | v1      |
| OpenAI Chat Completions | `/v1`                       | v1      |
| Open Responses          | `/responses`                | v1      |
| REST API                | `/v1/sessions`, `/v1/admin` | v1      |
| A2A (Agent-to-Agent)    | `/a2a`                      | Post-v1 |

Third-party adapters can be built and validated via the `RegisterAdapterUnderTest` compliance suite.

### Enterprise Controls

- **Multi-tenancy** — Postgres row-level security with `SET LOCAL`, per-tenant quotas, RBAC
- **Audit logging** — hash-chained integrity, configurable retention (SOC2, HIPAA, FedRAMP, NIS2/DORA), SIEM forwarding
- **GDPR erasure** — 19-step `DeleteByUser` with billing pseudonymization, processing restriction (Article 18), erasure receipts
- **Legal holds** — suspend artifact retention for compliance investigations
- **Data residency** — per-tenant/per-environment region constraints with fail-closed storage routing
- **Metering** — append-only billing event stream with gap detection for external billing integration

### Security

- Pods run non-root with all capabilities dropped and read-only root filesystem
- Default-deny network policies — pods can only reach the gateway
- No standing credentials in pods — only short-lived leases and projected SA tokens
- Gateway-mediated file delivery — pods never fetch external data directly
- mTLS between gateway and pods with per-replica identity
- Token/Connector Service runs as a separate process with its own KMS access
- URL-mode elicitation security (domain allowlists, agent vs. connector trust indicators)
- Admission policies (OPA/Gatekeeper or Kyverno) with `failurePolicy: Fail`

## Tech Stack

- **Go** — all platform components (gateway, controllers, runtime adapter, CLI)
- **Kubernetes** — CRDs (`kubernetes-sigs/agent-sandbox`), RuntimeClass, NetworkPolicy, PDB
- **gRPC** — internal gateway-to-pod protocol, external interceptors
- **MCP** — client-facing protocol for interactive sessions (Streamable HTTP)
- **Postgres** — session state, task metadata, audit logs, credential pools, eval results, memories (+ pgvector)
- **Redis** — distributed leases, routing cache, rate limit counters, token budgets, experiment sticky cache
- **MinIO** — artifact and checkpoint storage (S3/GCS/Azure Blob compatible)
- **cert-manager** — mTLS certificate lifecycle

## Quickstart

```bash
git clone https://github.com/your-org/lenny.git
cd lenny
make run
```

`make run` starts a single binary with embedded SQLite (replacing Postgres), in-memory caches (replacing Redis), and local filesystem (replacing MinIO). The gateway, controller simulator, and echo runtime run as goroutines in one process.

Then in another terminal:

```bash
# Create a session
curl -s -X POST http://localhost:8080/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{"runtime": "echo"}' | jq .

# Start the session (use the session ID from above)
curl -s -X POST http://localhost:8080/v1/sessions/{id}/start | jq .

# Send a message
curl -s -X POST http://localhost:8080/v1/sessions/{id}/messages \
  -H "Content-Type: application/json" \
  -d '{"content": "Hello, Lenny!"}' | jq .

# Terminate
curl -s -X POST http://localhost:8080/v1/sessions/{id}/terminate | jq .
```

**Target: clone to echo session in under 5 minutes.**

## Why Lenny?

Lenny occupies a distinct point in the agent infrastructure design space:

1. **Runtime-agnostic adapter contract** — any process, any framework, tiered integration (Section 15.4)
2. **Flexible runtime types and execution modes** — `agent` and `mcp` runtime types; `session`, `task`, and `concurrent` execution modes with mode-aware pool scaling (Section 5.2)
3. **Recursive delegation as a platform primitive** — per-hop budget, scope, and policy enforcement (Section 8)
4. **Self-hosted, Kubernetes-native** — your cluster, your data, standard K8s primitives (Section 17)
5. **Multi-protocol gateway** — REST + MCP + OpenAI + Open Responses via ExternalAdapterRegistry (Section 15)
6. **Enterprise controls at the platform layer** — RBAC, budgets, audit, isolation, compliance (Section 11)
7. **Ecosystem-composable via hooks-and-defaults** — memory, caching, guardrails, eval are all pluggable interfaces (Section 22.6)
8. **Built-in experimentation** — A/B testing with variant pools, deterministic bucketing, eval attribution (Section 10.7)
9. **Pull-based evaluation hooks** — multi-dimensional scoring, session replay, experiment-aware results API (Section 10.7)
10. **Compliance and data governance** — GDPR erasure, legal holds, data residency, audit with hash-chain integrity (Section 12.8)

For detailed comparisons against E2B, Daytona, Fly.io Sprites, Temporal, Modal, and LangGraph/LangSmith, see [Section 23 of the spec](SPEC.md#23-competitive-landscape).

## Project Status

Lenny is in the **design phase**. The [technical specification](SPEC.md) is complete and covers the full architecture. Implementation has not started yet.

We welcome feedback on the design and early contributors. See [Contributing](#contributing) below.

## Documentation

- [Technical Specification](SPEC.md) — comprehensive architecture specification
- [Documentation Site](docs/) — guides, tutorials, API reference (Jekyll/GitHub Pages)
- [Agent Operability](AGENTIC_OPERABILITY.md) — design addendum for AI DevOps agent integration

## Contributing

Lenny is open source and we welcome contributions. Areas where help is especially valuable:

- **Runtime adapters** — implement adapters for your favorite agent framework
- **Kubernetes expertise** — CRD design, controller logic, networking
- **Security review** — threat modeling, policy design, credential management
- **Documentation** — guides, tutorials, comparison guides

Please open an issue to discuss before submitting large changes.

## License

[MIT](LICENSE)
