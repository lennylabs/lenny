# Lenny

**Kubernetes-native, runtime-agnostic agent session platform.**

Lenny manages pools of pre-warmed, isolated AI agent pods on Kubernetes behind a unified gateway. It handles session lifecycle, workspace setup, credential leasing, recursive delegation, policy enforcement, and recovery — so your team can run any AI agents as a shared, on-demand cloud service.

[Documentation](docs/) | [Quickstart](#quickstart) | [Contributing](#contributing) | [Implementation Status](docs/about/status.md)

> **Status: design phase.** The technical specification is complete; implementation is in progress. The documentation describes the v1 surface and is a source of truth for spec-driven development — not yet a working product. Early feedback on the design is very welcome: open an [issue](https://github.com/lennylabs/lenny/issues) or a [discussion](https://github.com/lennylabs/lenny/discussions).

---

## Why Lenny?

Lenny is a self-hosted, runtime-agnostic agent platform built around security, isolation, and operational control — from single-team setups to large multi-tenant deployments.

1. **Runtime-agnostic** — any process, any framework, [leveled adapter contract](#runtime-adapter-contract)
2. **Self-hosted, Kubernetes-native** — your cluster, your data, standard K8s primitives
3. **Security by default** — pods run non-root, all capabilities dropped, read-only root filesystem, default-deny network policies. No standing credentials — only short-lived leases. Gateway-mediated file delivery — pods never fetch external data directly. Deployer-selectable isolation: gVisor, Kata microVM, or runc
4. **Recursive delegation** — agents spawn child agents with per-hop budget, scope narrowing, isolation monotonicity, content policy inheritance, and cycle detection at every hop
5. **Multi-protocol gateway** — REST, MCP, OpenAI Chat Completions, and Open Responses via a single infrastructure
6. **Enterprise controls** — multi-tenancy with Postgres RLS, per-tenant quotas, RBAC, audit with hash-chain integrity, GDPR erasure, legal holds, data residency
7. **Experimentation primitives** — pod variant pools and deterministic routing as infrastructure; plug in LaunchDarkly, Statsig, Unleash, or any OpenFeature-compatible platform for assignment
8. **Ecosystem-composable** — memory, caching, guardrails, evals, credential routing are all pluggable interfaces; bring the platform you already use

For comparisons with other projects, see [Section 23 of the spec](spec/23_competitive-landscape.md).

---

## Quickstart

> Commands below describe the target v1 developer experience. They will run against the first released drop; see [Implementation Status](docs/about/status.md) for what's wired up today.

```bash
# Install the single-binary CLI
brew install lennylabs/tap/lenny

# Start the whole platform on your laptop — embedded Kubernetes,
# gateway, management plane, and the nine reference runtimes.
lenny up

# Chat with the built-in mock-provider `chat` runtime (no API keys needed)
lenny session start --runtime chat --message "Hello, Lenny!"

# Tear it down (state is kept under ~/.lenny/ unless you pass --purge)
lenny down
```

`lenny up` runs the same gateway, controller, and management-plane binaries as a production install — only the external dependencies (Postgres, Redis, KMS, identity provider) are replaced with in-process equivalents. First boot downloads its dependencies into `~/.lenny/` and takes about a minute; subsequent starts are seconds.

To try a real LLM provider instead of the mock, export a key before `lenny up`:

```bash
export ANTHROPIC_API_KEY=sk-ant-...
lenny down && lenny up
lenny session start --runtime claude-code --workspace ./my-repo \
  --message "Summarize the architecture."
```

Contributors to the platform itself can skip the embedded Kubernetes layer and run `make run` for a native-process dev loop with SQLite and in-memory stores — see [Contributing](#contributing).

---

## What's included

Everything below ships with the same single binary. No post-install glue.

- **Reference runtime catalog.** Every install includes nine pre-registered runtimes — `claude-code`, `gemini-cli`, `codex`, `cursor-cli`, `chat`, `langgraph`, `mastra`, `openai-assistants`, `crewai` — plus the `echo` conformance reference. Use them as-is, fork them, or register your own alongside.
- **Bring your own agent in ~50 lines.** `lenny runtime init my-agent --language go --template coding` scaffolds a working adapter from a template; `lenny runtime publish` pushes the image and registers it against a gateway in one step. Official SDKs for Go, Python, and TypeScript hide the wire format.
- **One gateway, four protocols.** REST, MCP (Streamable HTTP), OpenAI Chat Completions, and Open Responses all terminate at the same gateway and share one auth, policy, routing, and audit path.
- **LLM routing in the gateway.** The gateway speaks to Anthropic, AWS Bedrock, Google Vertex AI, and Azure OpenAI through an in-process native Go translator — no sidecar, no loopback auth. Upstream API keys stay in the gateway's memory and never reach the pod. Other providers can be fronted by an external proxy (LiteLLM, Portkey) alongside the built-in router.
- **Isolation level per pool.** `runc` when you trust the code, gVisor (the default) for untrusted code, Kata microVMs for high-risk workloads — all selected as Kubernetes `RuntimeClass` on the pool.
- **Installable with a wizard.** `lenny-ctl install` inspects your cluster, asks about ten targeted questions, composes a Helm values file, previews the diff, runs the install, and smoke-tests it. The answer file is reusable — replay installs in CI.
- **Management plane included.** `lenny-ops` exposes structured diagnostic endpoints, runbooks, backup and restore APIs, and drift detection, so an on-call engineer — or an on-call AI agent — can investigate without scraping `kubectl` output. `lenny-ctl doctor --fix` applies idempotent remediations for common misconfigurations.
- **Browser-based playground.** Every install serves a web UI at `/playground` that drives sessions through the same public API every SDK uses. Turn it off in production with one Helm flag.
- **Bundled monitoring.** Prometheus alerting rules and OpenSLO definitions drop into any standard observability stack; a Grafana dashboard covers core health signals.

Every capability above is specified in [`spec/`](spec/) and covered by the integration test suite.

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
│  PoolScalingController (scaling + variant pool sizing)      │
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
| **Gateway**               | Only externally-facing component. Auth, protocol adaptation, session routing, credential leasing, delegation, variant routing, policy enforcement. Scales horizontally. |
| **Warm Pool Controller**  | Kubernetes controller managing `agent-sandbox` CRDs. Keeps pods pre-warmed, handles claim/release/drain.                                                                   |
| **PoolScalingController** | Scaling intelligence and variant pool sizing. Reconciles pool config from Postgres into CRDs.                                                                              |
| **Token Service**         | Separate process with its own KMS access. Manages OAuth tokens for external tools and LLM provider credentials.                                                            |
| **Runtime Adapter**       | Sidecar in each pod. Bridges between the gateway (gRPC) and the agent binary (stdin/stdout JSON Lines).                                                                    |
| **Agent Binary**          | Your code. Runs inside the pod, does the actual work.                                                                                                                      |

---

## Runtime Adapter Contract

Lenny is not tied to any specific agent runtime. It defines a leveled adapter contract:

| Level        | Interface                        | Effort                    | Capabilities                                                                                                                         |
| ------------ | -------------------------------- | ------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| **Basic**    | stdin/stdout JSON Lines          | ~50 lines of code, no SDK | Basic session lifecycle, text I/O                                                                                                    |
| **Standard** | stdin/stdout + MCP (Unix socket) | Moderate                  | Basic + platform MCP tools (delegation, discovery, elicitation, output), connector tool access                                       |
| **Full**     | stdin/stdout + MCP (Unix socket) | Significant               | Standard + lifecycle channel (cooperative checkpointing, clean interrupts, credential rotation, graceful drain, task-mode pod reuse) |

You can run Claude Code agents, LangChain agents, CrewAI agents, code review bots, research agents, or any long-lived process. Multiple runtime types can be registered and run simultaneously, each with their own pools and configuration.

Lenny supports two runtime types: **`type: agent`** (full task lifecycle with sessions, delegation, elicitation) and **`type: mcp`** (hosts MCP servers behind Lenny-managed infrastructure with zero code changes). Agent runtimes run in one of three execution modes: **`session`** (one session per pod), **`task`** (pod reuse with workspace scrub), or **`concurrent`** (slot multiplexing).

See [Core Concepts](docs/getting-started/concepts) for detailed coverage of runtime types and execution modes.

---

## Key Capabilities

### Sessions and workspaces

Pre-warmed pods eliminate cold-start latency — pod claim is in the millisecond range. Each session gets an isolated, sandboxed filesystem with deployer-selectable isolation (runc, gVisor, Kata microVM). Full bidirectional streaming with follow-up prompts, interrupts, tool use, and elicitation. If a pod dies, the gateway resumes the session from a workspace checkpoint. Sessions support derive (fork) and replay (regression testing).

### Credentials and connectors

**Credential leasing** — the platform manages LLM provider credentials in pools, assigns short-lived leases to sessions, and rotates automatically. Pods never see raw API keys. **External connectors** — agents call external tools (GitHub, Jira, Slack) through the gateway, which manages OAuth flows, stores tokens encrypted via KMS, and caches access tokens in Redis. Pods never see connector tokens.

### Recursive delegation

Agents spawn child agents through the gateway with enforced delegation leases: maximum depth and fan-out, token budgets, scope narrowing, isolation monotonicity, content policy inheritance, and cycle detection. Cross-delegation tracing via `tracingContext` propagation enables trace stitching across delegation chains in external observability platforms.

### Gateway

Multi-protocol: REST, MCP (Streamable HTTP), OpenAI Chat Completions, and Open Responses clients connect to the same infrastructure. 12-phase request interceptor chain for guardrails, content policy, custom routing, and LLM request/response inspection. Compatible with AWS Bedrock Guardrails, Azure Content Safety, Lakera Guard, or custom gRPC classifiers.

### Enterprise controls and security

Multi-tenancy with Postgres RLS, per-tenant quotas, RBAC, audit logging with hash-chained integrity, GDPR erasure, legal holds, and data residency. Pods run non-root with all capabilities dropped, read-only root filesystem, and default-deny network policies. mTLS between gateway and pods. Pluggable `MemoryStore` interface (default: Postgres + pgvector) for agent memory.

### Experimentation

Lenny's focus is infrastructure primitives: pools of pod variants and deterministic routing. A basic built-in variant assigner is included for simple rollouts, but most teams will plug in an external experimentation platform (LaunchDarkly, Statsig, Unleash, or any OpenFeature-compatible provider) for assignment. Experiment context is delivered to runtimes in the adapter manifest. See [Why Lenny — Experimentation](docs/about/why-lenny#experimentation) for full detail.

### Evaluation

Lenny is not an eval platform and does not ship one. It is compatible with any eval framework a runtime author chooses (LangSmith, Braintrust, Arize, Langfuse, home-grown). Lenny's only eval surface is a basic mechanism to store and retrieve scores alongside session state — useful for teams that just want to persist results without another system. `tracingContext` propagation lets external platforms stitch traces across delegation chains. See [Why Lenny — Evaluation](docs/about/why-lenny#evaluation) for full detail.

---

## Tech Stack

- **Go** — all platform components (gateway, controllers, runtime adapter, CLI)
- **Kubernetes** — CRDs (`kubernetes-sigs/agent-sandbox`), RuntimeClass, NetworkPolicy, PDB
- **gRPC** — internal gateway-to-pod protocol, external interceptors
- **MCP** — client-facing protocol for interactive sessions (Streamable HTTP)
- **Postgres** — session state, task metadata, audit logs, credential pools, score storage, memories (+ pgvector)
- **Redis** — distributed leases, routing cache, rate limit counters, token budgets, variant sticky cache
- **MinIO** — artifact and checkpoint storage (S3/GCS/Azure Blob compatible)
- **cert-manager** — mTLS certificate lifecycle

## Project Status

Lenny is in the **design phase**. The [technical specification](spec/) is complete and drives implementation under a spec- and test-driven workflow. The [Implementation Status](docs/about/status.md) page tracks what's wired up today; the full build sequence lives in [`spec/18_build-sequence.md`](spec/18_build-sequence.md) and is directional — surface order and timing may shift as implementation surfaces new constraints.

Early feedback is welcome right now. PR-level contributions open up once the core lands — see [Contributing](#contributing).

## Documentation

- [Technical Specification](spec/) — comprehensive architecture specification (split by section under `spec/`)
- [Documentation Site](docs/) — guides, tutorials, API reference (Jekyll/GitHub Pages)
- [Implementation Status](docs/about/status.md) — what's shipped, in progress, and planned
- [Roadmap](ROADMAP.md) — short-horizon priorities and the link to the full build sequence

## Contributing

Lenny is MIT-licensed and open source. While the project is in the design phase, the highest-leverage contributions are:

- **Spec feedback** — read [`spec/`](spec/) and open issues or discussions with questions, disagreements, and concrete suggestions.
- **Runtime adapter sketches** — prototype an adapter for your framework against the [adapter contract](spec/04_system-components.md). It doesn't need to run yet — contract pressure helps us find gaps.
- **Comparisons and use cases** — tell us where Lenny does or doesn't fit your workflow.
- **Security review** — threat-model the design; comment on [`spec/13_security.md`](spec/13_security.md) and related sections.

Code PRs against platform components open up once the core lands. When they do, the contributor dev loop is a zero-dependency `make run` (SQLite, in-memory stores, local filesystem, echo runtime) — no Kubernetes, no Docker, no external services. See [CONTRIBUTING.md](CONTRIBUTING.md) for the current policy and [GOVERNANCE.md](GOVERNANCE.md) for how decisions are made.

## License

[MIT](LICENSE)
