## 2. Goals and Non-Goals

### Goals

- Run agent runtimes on demand in Kubernetes with low startup latency
- Support full SDK-like interactive sessions (streaming, interrupts, follow-up prompts, tool use)
- Support multiple runtime binaries via a standard runtime adapter contract
- Support two runtime types: `type: agent` (full task lifecycle with sessions, delegation, elicitation, multi-turn dialog) and `type: mcp` (managed MCP server hosting with Lenny-managed pod lifecycle but no task lifecycle)
- Support multiple execution modes for agent runtimes: `session` (one session per pod), `task` (sequential pod reuse with workspace scrub), and `concurrent` (slot-multiplexed parallel tasks) — with mode-aware pool scaling
- Enable recursive orchestration: any pod can delegate to other pods
- Make gateway failure survivable and pod failure recoverable (bounded resume)
- Enforce least-privilege security: no standing credentials in pods, gateway-mediated file delivery
- Scale the gateway horizontally with externalized session state, designed to reach Tier 3 (10,000 concurrent sessions) with horizontal scaling only ([Section 16.5](16_observability.md#165-alerting-rules-and-slos); prerequisite: LLM Proxy subsystem extracted to a dedicated service before Tier 2→3 promotion — see [§4.1](04_system-components.md#41-edge-gateway-replicas) for extraction thresholds and the Tier 3 `maxSessionsPerReplica` revert-to-200 fallback)
- Support deployer-selectable isolation profiles (runc, gVisor, Kata)
- Provide rate limiting, token budgets, concurrency controls, and audit logging

### Non-Goals

- Shared RWX storage mounts across agent pods
- Git-based or object-store-based workspace population by pods
- Mid-session file uploads as a default (supported as opt-in capability per runtime)
- Arbitrary late-bound volume mounts on warm pods
- Live migration of in-flight agent processes
- KubeVirt/full VM workloads
- Direct pod-to-pod communication (all delegation goes through gateway)
- Making every internal edge speak MCP

Lenny is designed as an open-source project. The community strategy — including target personas, governance model (BDfN → steering committee), `GOVERNANCE.md`, `CONTRIBUTING.md`, and the Time to Hello World target — is documented in **[Section 23.2](23_competitive-landscape.md#232-community-adoption-strategy) (Community Adoption Strategy)**. `GOVERNANCE.md` and `CONTRIBUTING.md` are v1 launch deliverables. `CONTRIBUTING.md` is published in Phase 2 alongside the `make run` quick-start; `GOVERNANCE.md` is drafted in Phase 2 and finalized in Phase 17a.

