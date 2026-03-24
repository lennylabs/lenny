# Lenny

**Kubernetes-native platform for running cloud agent sessions on demand.**

Lenny manages pools of pre-warmed, isolated agent pods on Kubernetes behind a unified gateway. It handles session lifecycle, workspace setup, credential leasing, recursive delegation, policy enforcement, and recovery — so your team can run AI coding agents (or any long-lived agent workload) as a shared cloud service.

## What Lenny Does

Lenny runs agents **as a shared, on-demand cloud service**:

- **Pre-warmed pods** — agent containers are already running and waiting when a request arrives, eliminating cold-start latency
- **Isolated workspaces** — each session gets its own sandboxed filesystem, populated with exactly the files it needs
- **Interactive sessions** — full bidirectional streaming with follow-up prompts, interrupts, tool use, and elicitation (not just request/response)
- **Recursive delegation** — an agent can spawn child agents, which can spawn their own children, with gateway-enforced budgets and scope at every level
- **Credential leasing** — the platform manages LLM provider credentials (API keys, AWS Bedrock roles, etc.) and assigns short-lived leases to sessions, with automatic rotation on rate limiting
- **Recovery** — if a pod dies, the gateway can resume the session on a new pod from a checkpoint, within configurable retry limits
- **Policy and controls** — rate limits, token budgets, concurrency caps, delegation depth limits, audit logging, and user invalidation, all enforced at the gateway

## Runtime Agnostic

Lenny is **not tied to any specific agent runtime**. It defines a standard [runtime adapter contract](docs/technical-design.md#47-runtime-adapter) that any compliant binary can implement. You can run:

- Claude Code agents
- Custom LLM-powered agents
- Code review bots
- Research agents
- Any long-lived process that benefits from managed lifecycle, isolation, and delegation

Multiple runtime types can be registered and run simultaneously, each with their own pools and configuration.

## Architecture Overview

```
Clients (MCP / REST)
        |
   [ Gateway ]  ── policy, auth, routing, credential leasing
        |
   [ Warm Pool Controller ]  ── keeps pre-warmed pods ready
        |
   ┌────┴────┐
 [Pod A]   [Pod B]  ── isolated agent sessions
   |          |
 [Adapter] [Adapter]  ── standard contract (gRPC)
   |          |
 [Agent]   [Agent]   ── any runtime binary
```

**Gateway** — the only externally-facing component. Handles authentication (OIDC/OAuth), MCP protocol, REST API, session routing, file uploads, credential leasing, delegation mediation, and policy enforcement. Scales horizontally with externalized state.

**Warm Pool Controller** — a Kubernetes operator (kubebuilder) that manages custom CRDs (`AgentPool`, `AgentPod`, `AgentSession`). Keeps pods pre-warmed and handles claim/release/drain lifecycle.

**Runtime Adapter** — a sidecar container in each agent pod that speaks the Lenny gRPC protocol. Bridges between the gateway and the agent binary. Third-party authors only need to implement a binary that communicates over a local Unix socket.

**Agent Binary** — your code. Runs inside the pod, does the actual work.

## Key Features

### Session Lifecycle

Create a session, upload workspace files, run setup commands, start the agent, interact via streaming, and retrieve artifacts when done. The gateway manages the full lifecycle including checkpointing and resume.

### Credential Leasing

The platform manages LLM provider credentials (Anthropic API keys, AWS Bedrock roles, Vertex AI service accounts, etc.) in admin-configured pools. Sessions receive short-lived credential leases — never raw API keys. When a credential is rate-limited, the gateway automatically rotates to a healthy one.

### Recursive Delegation

Any agent can spawn child agents through gateway-mediated delegation tools. The gateway enforces delegation leases (max depth, max children, token budgets, allowed runtimes, isolation minimums) and tracks the full task tree. Children can themselves delegate further, with strictly narrowing scope at each level.

### Isolation Profiles

Choose per-pool isolation via Kubernetes `RuntimeClass`:

| Profile               | Runtime         | Use Case                                   |
| --------------------- | --------------- | ------------------------------------------ |
| `sandboxed` (default) | gVisor          | Most workloads                             |
| `microvm`             | Kata Containers | Higher-risk or multi-tenant workloads      |
| `standard`            | runc            | Development/testing only (explicit opt-in) |

### Dual API

- **REST API** — for session lifecycle, admin operations, CI/CD integration, and any language
- **MCP API** — for interactive streaming sessions, delegation, and elicitation (Model Context Protocol over Streamable HTTP)

### Security

- Pods run non-root with all capabilities dropped, read-only root filesystem
- Default-deny network policies — pods can only reach the gateway
- No standing credentials in pods — only short-lived leases and projected SA tokens
- Gateway-mediated file delivery — pods never fetch external data directly
- Image signature verification via cosign/Sigstore
- Append-only audit logging
- Pod Security Standards (Restricted) enforced at namespace level

## Tech Stack

- **Go** — all platform components (gateway, controller, runtime adapter)
- **Kubernetes** — CRDs, operators (kubebuilder), RuntimeClass, NetworkPolicy
- **gRPC** — internal gateway-to-pod protocol
- **MCP** — client-facing protocol for interactive sessions (Streamable HTTP)
- **Postgres** — session state, task metadata, audit logs, credential pools
- **Redis** — distributed leases, routing cache, rate limit counters
- **MinIO** — artifact and checkpoint storage (S3-compatible)
- **cert-manager** — mTLS certificate lifecycle

## Project Status

Lenny is in the **design phase**. The [technical design](docs/technical-design.md) is complete and covers the full architecture. Implementation has not started yet.

We welcome feedback on the design and early contributors. See [Contributing](#contributing) below.

## Documentation

- [Technical Design](docs/technical-design.md) — comprehensive architecture specification
- [Review Findings](docs/claude/review-findings.md) — security, K8s, DevOps, and architecture review results

## Getting Started

> Coming soon. The first milestone is a working `lenny-dev` mode (Docker Compose) for local development and runtime adapter authoring.

## Contributing

Lenny is open source and we welcome contributions. Areas where help is especially valuable:

- **Runtime adapters** — implement adapters for your favorite agent framework
- **Kubernetes expertise** — CRD design, controller logic, networking
- **Security review** — threat modeling, policy design, credential management
- **Documentation** — guides, tutorials, migration paths

Please open an issue to discuss before submitting large changes.

## License

[MIT](LICENSE)
