# Lenny

**Kubernetes-native, runtime-agnostic agent session platform.**

Lenny manages pools of pre-warmed, isolated AI agent pods on Kubernetes behind a unified gateway. It handles session lifecycle, workspace setup, credential leasing, recursive delegation, experimentation, evaluation, policy enforcement, and recovery — so your team can run any AI agents as a shared, on-demand cloud service.

[Documentation](docs/) | [Quickstart](#quickstart) | [Contributing](#contributing)

---

## Why Lenny?

Lenny is a self-hosted, runtime-agnostic agent platform built around security, isolation, and operational control — from single-team setups to large multi-tenant deployments.

1. **Runtime-agnostic** — any process, any framework, [tiered adapter contract](#runtime-adapter-contract)
2. **Security by default** — pods run non-root, all capabilities dropped, read-only root filesystem, default-deny network policies. No standing credentials — only short-lived leases. Gateway-mediated file delivery — pods never fetch external data directly. Deployer-selectable isolation: gVisor, Kata microVM, or runc
3. **Recursive delegation** — agents spawn child agents with per-hop budget, scope narrowing, isolation monotonicity, content policy inheritance, and cycle detection at every hop
4. **Self-hosted, Kubernetes-native** — your cluster, your data, standard K8s primitives
5. **Multi-protocol gateway** — REST, MCP, OpenAI Chat Completions, and Open Responses via a single infrastructure
6. **Enterprise controls** — multi-tenancy with Postgres RLS, per-tenant quotas, RBAC, audit with hash-chain integrity, GDPR erasure, legal holds, data residency
7. **Experimentation and evaluation** — built-in A/B traffic routing; two-tier eval model with runtime-native platforms as the primary path
8. **Ecosystem-composable** — memory, caching, guardrails, eval, credential routing are all pluggable interfaces

For comparisons with other projects, see [Section 23 of the spec](SPEC.md#23-competitive-landscape).

---

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
  -d '{"input": [{"type": "text", "text": "Hello, Lenny!"}]}' | jq .

# Terminate
curl -s -X POST http://localhost:8080/v1/sessions/{id}/terminate | jq .
```

**Target: clone to echo session in under 5 minutes.**

---

## How It Works

```
┌─────────────────────────────────────────────────────────────┐
│                    Client / MCP Host                         │
└────────────────────────────┬────────────────────────────────┘
                             │ REST / MCP / OpenAI / Open Responses
                             ▼
┌────────────────────────────────────────────────────────────┐
│                   Gateway Edge Replicas                    │
│  ┌────────┐ ┌──────────┐ ┌─────────┐ ┌─────────────────┐   │
│  │ Auth / │ │ Policy   │ │ Session │ │ MCP Fabric      │   │
│  │ OIDC   │ │ Engine + │ │ Router  │ │ (tasks,         │   │
│  │        │ │ Intercep-│ │         │ │  elicitation,   │   │
│  │        │ │ tors     │ │         │ │  delegation)    │   │
│  └────────┘ └──────────┘ └─────────┘ └─────────────────┘   │
└────────┬─────────┬──────────┬──────────┬───────────────────┘
         │         │          │          │
    ┌────▼───┐ ┌───▼───┐ ┌───▼────┐ ┌───▼─────┐   ┌─────────────┐
    │Session │ │Token  │ │Event/  │ │Artifact │   │ External    │
    │Manager │ │Service│ │Checkpt │ │Store    │   │ Connectors  │
    │(PG+Red)│ │       │ │Store   │ │         │   │ (GitHub,    │
    └────────┘ └───┬───┘ └────────┘ └─────────┘   │  Jira, ...) │
                   │                              └──────▲──────┘
                   │    OAuth tokens (encrypted,          │
                   └────  cached in Redis) ───────────────┘
                        Gateway proxies all connector
                        calls — pods never see tokens

        Gateway ←── mTLS ──→ Pods (gRPC control protocol)

┌─────────────────────────────────────────────────────────────┐
│  Warm Pool Controller (pod lifecycle, agent-sandbox CRDs)   │
│  PoolScalingController (scaling intelligence, experiments)  │
└────────┬───────────────┬────────────────┬───────────────────┘
         │               │                │
    ┌────▼────┐    ┌─────▼─────┐    ┌─────▼─────┐
    │  Pod A  │    │   Pod B   │    │   Pod C   │
    │┌───────┐│    │┌─────────┐│    │┌─────────┐│
    ││Adapter││    ││ Adapter ││    ││ Adapter ││
    │├───────┤│    │├─────────┤│    │├─────────┤│
    ││Agent  ││    ││  Agent  ││    ││  Agent  ││
    │└───────┘│    │└─────────┘│    │└─────────┘│
    └─────────┘    └───────────┘    └───────────┘
```

| Component                 | Role                                                                                                                                                                       |
| ------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Gateway**               | Only externally-facing component. Auth, protocol adaptation, session routing, credential leasing, delegation, experiment routing, policy enforcement. Scales horizontally. |
| **Warm Pool Controller**  | Kubernetes controller managing `agent-sandbox` CRDs. Keeps pods pre-warmed, handles claim/release/drain.                                                                   |
| **PoolScalingController** | Scaling intelligence and experiment variant pool sizing. Reconciles pool config from Postgres into CRDs.                                                                   |
| **Token Service**         | Separate process with its own KMS access. Manages OAuth tokens for external tools and LLM provider credentials.                                                            |
| **Runtime Adapter**       | Sidecar in each pod. Bridges between the gateway (gRPC) and the agent binary (stdin/stdout JSON Lines).                                                                    |
| **Agent Binary**          | Your code. Runs inside the pod, does the actual work.                                                                                                                      |

---

## Runtime Adapter Contract

Lenny is not tied to any specific agent runtime. It defines a tiered adapter contract:

| Tier         | Interface                        | Effort                    | Capabilities                                                                                                                         |
| ------------ | -------------------------------- | ------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| **Minimum**  | stdin/stdout JSON Lines          | ~50 lines of code, no SDK | Basic session lifecycle, text I/O                                                                                                    |
| **Standard** | stdin/stdout + MCP (Unix socket) | Moderate                  | Minimum + platform MCP tools (delegation, discovery, elicitation, output), connector tool access                                     |
| **Full**     | stdin/stdout + MCP (Unix socket) | Significant               | Standard + lifecycle channel (cooperative checkpointing, clean interrupts, credential rotation, graceful drain, task-mode pod reuse) |

You can run Claude Code agents, LangChain agents, CrewAI agents, code review bots, research agents, or any long-lived process. Multiple runtime types can be registered and run simultaneously, each with their own pools and configuration.

Lenny supports two runtime types: **`type: agent`** (full task lifecycle with sessions, delegation, elicitation) and **`type: mcp`** (hosts MCP servers behind Lenny-managed infrastructure with zero code changes). Agent runtimes run in one of three execution modes: **`session`** (one session per pod), **`task`** (pod reuse with workspace scrub), or **`concurrent`** (slot multiplexing).

See [Core Concepts](docs/getting-started/concepts) for detailed coverage of runtime types and execution modes.

---

## Key Capabilities

### Sessions and workspaces

Pre-warmed pods eliminate cold-start latency — pod claim is in the millisecond range. Each session gets an isolated, sandboxed filesystem with deployer-selectable isolation (runc, gVisor, Kata microVM). Full bidirectional streaming with follow-up prompts, interrupts, tool use, and elicitation. If a pod dies, the gateway resumes the session from a workspace checkpoint. Sessions support derive (fork) and replay (regression testing).

### Recursive delegation

Agents spawn child agents through the gateway with enforced delegation leases: maximum depth and fan-out, token budgets, scope narrowing, isolation monotonicity, content policy inheritance, and cycle detection. Cross-delegation tracing via `tracingContext` propagation enables trace stitching across delegation chains in external observability platforms.

### Credentials and connectors

**Credential leasing** — the platform manages LLM provider credentials in pools, assigns short-lived leases to sessions, and rotates automatically. Pods never see raw API keys. **External connectors** — agents call external tools (GitHub, Jira, Slack) through the gateway, which manages OAuth flows, stores tokens encrypted via KMS, and caches access tokens in Redis. Pods never see connector tokens.

### Experimentation

Built-in A/B traffic routing for runtime version rollouts with variant pools, deterministic bucketing, and external targeting via LaunchDarkly, Statsig, or Unleash. Experiment context is delivered to runtimes in the adapter manifest. See [Why Lenny — Experimentation](docs/about/why-lenny#experimentation) for full detail.

### Evaluation

Evaluation is independent of experimentation — any session can be scored. Two-tier model: runtimes use their own eval platforms (LangSmith, Braintrust, etc.) as the primary scoring path; Lenny's built-in `/eval` endpoint provides a basic alternative. Cross-delegation `tracingContext` propagation for observability across delegation chains. See [Why Lenny — Evaluation](docs/about/why-lenny#evaluation) for full detail.

### Gateway

Multi-protocol: REST, MCP (Streamable HTTP), OpenAI Chat Completions, and Open Responses clients connect to the same infrastructure. 12-phase request interceptor chain for guardrails, content policy, custom routing, and LLM request/response inspection. Compatible with AWS Bedrock Guardrails, Azure Content Safety, Lakera Guard, or custom gRPC classifiers.

### Enterprise controls and security

Multi-tenancy with Postgres RLS, per-tenant quotas, RBAC, audit logging with hash-chained integrity, GDPR erasure, legal holds, and data residency. Pods run non-root with all capabilities dropped, read-only root filesystem, and default-deny network policies. mTLS between gateway and pods. Pluggable `MemoryStore` interface (default: Postgres + pgvector) for agent memory.

---

## Tech Stack

- **Go** — all platform components (gateway, controllers, runtime adapter, CLI)
- **Kubernetes** — CRDs (`kubernetes-sigs/agent-sandbox`), RuntimeClass, NetworkPolicy, PDB
- **gRPC** — internal gateway-to-pod protocol, external interceptors
- **MCP** — client-facing protocol for interactive sessions (Streamable HTTP)
- **Postgres** — session state, task metadata, audit logs, credential pools, eval results, memories (+ pgvector)
- **Redis** — distributed leases, routing cache, rate limit counters, token budgets, experiment sticky cache
- **MinIO** — artifact and checkpoint storage (S3/GCS/Azure Blob compatible)
- **cert-manager** — mTLS certificate lifecycle

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
