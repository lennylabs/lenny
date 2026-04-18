---
layout: default
title: Getting Started
nav_order: 1
has_children: true
---

# Getting Started with Lenny

Lenny is a **Kubernetes-native, runtime-agnostic agent session platform** that provides on-demand, pre-warmed, isolated cloud agent instances to clients. It manages the full lifecycle of interactive agent sessions -- from pod allocation and workspace setup to streaming I/O, recursive delegation, credential leasing, and session recovery -- behind a unified gateway that owns security, policy, and protocol translation.

Lenny is not tied to any single agent runtime. It defines a standard contract that any compliant pod binary can implement, whether that binary wraps Claude Code, a LangGraph agent, a custom Python script, or an MCP server. The platform handles everything around the agent: isolation, warm pools, file delivery, checkpointing, credential management, and multi-agent orchestration.

---

## What you will learn

This section walks you through the fundamentals of Lenny, from running your first session locally to understanding the architecture that powers production deployments.

| Page | What it covers | Time |
|------|---------------|------|
| [Quickstart](quickstart.html) | Install the `lenny` binary, run `lenny up`, attach to a `chat` session and a `claude-code` session, explore the web playground | ~5 minutes |
| [Core Concepts](concepts.html) | Sessions, runtimes, pools, gateway, delegation, workspaces, MCP, tenants, and credentials -- explained in depth | ~20 minutes |
| [Architecture Overview](architecture.html) | Component diagram, data flow, storage architecture, security boundaries, internal vs external protocols, `lenny-ops` operability control plane | ~15 minutes |

---

## Choose your path

Lenny serves several personas. After completing the Quickstart, follow the reading path that matches your role.

### Runtime Author

You are building a custom agent binary or adapter to run on Lenny. You want to understand the contract your binary must implement and how Lenny manages your process.

**Recommended path:**
1. **Quickstart** -- run `lenny up` and see the reference `chat` and `claude-code` runtimes in action.
2. Scaffold your own: `lenny runtime init my-agent --language go --template coding`.
3. **Core Concepts** -- focus on Sessions, Runtimes (especially integration tiers), and Workspaces.
4. **Architecture Overview** -- understand how the gateway communicates with your pod.
5. Continue to the **Runtime Author Guide** for the full adapter protocol, the three integration tiers (Minimum/Standard/Full), and the first-party Runtime Author SDKs for Go, Python, and TypeScript.

### Platform Operator

You are deploying Lenny to Kubernetes for your team or organization. You care about Helm configuration, scaling, observability, and security hardening.

**Recommended path:**
1. **Quickstart** -- run `lenny up` to understand the platform before deploying to a real cluster. Same code path, same CRDs.
2. **Core Concepts** -- focus on Pools, Gateway, Tenants, Credentials, and `lenny-ops`.
3. **Architecture Overview** -- understand all components, storage backends, and security boundaries.
4. Continue to the **Operator Guide** for the `lenny-ctl install` wizard, Helm configuration, `lenny-ops` agent operability, `doctor --fix` diagnostics, capacity planning, and production hardening.

### Client Developer

You are building an application that creates and interacts with Lenny sessions via the API. You want to understand session lifecycle, file uploads, streaming, and how to use MCP or REST endpoints.

**Recommended path:**
1. **Quickstart** -- see the full session flow via the embedded MCP CLI (`lenny session`).
2. **Core Concepts** -- focus on Sessions (lifecycle states), MCP, and Delegation.
3. **Architecture Overview** -- understand the gateway's external API surface and streaming model.
4. Try the bundled Web Playground at `https://localhost:8443/playground` while `lenny up` is running.
5. Continue to the **Client Guide** for the full API reference, the Go/Python/TypeScript client SDKs, and advanced patterns like delegation and elicitation.

### Contributor

You want to contribute to Lenny itself -- gateway code, controller logic, storage backends, or documentation.

**Recommended path:**
1. **Quickstart** -- run `lenny up` once to see the full system. Then drop to Tier 1 (`make run`) or Tier 2 (`docker compose up`) for day-to-day iteration.
2. **Core Concepts** -- read all sections to build a complete mental model.
3. **Architecture Overview** -- study component boundaries and internal protocols.
4. Read the canonical **spec/** (starting with `spec/README.md`) for the authoritative specification, then check `CONTRIBUTING.md` for development workflow and code conventions.
