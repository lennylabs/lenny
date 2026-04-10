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
| [Quickstart](quickstart.html) | Clone the repo, run `make run`, complete an echo session end-to-end | ~5 minutes |
| [Core Concepts](concepts.html) | Sessions, runtimes, pools, gateway, delegation, workspaces, MCP, tenants, and credentials -- explained in depth | ~20 minutes |
| [Architecture Overview](architecture.html) | Component diagram, data flow, storage architecture, security boundaries, internal vs external protocols | ~15 minutes |

---

## Choose your path

Lenny serves several personas. After completing the Quickstart, follow the reading path that matches your role.

### Runtime Author

You are building a custom agent binary or adapter to run on Lenny. You want to understand the contract your binary must implement and how Lenny manages your process.

**Recommended path:**
1. **Quickstart** -- get Lenny running locally and see the echo runtime in action.
2. **Core Concepts** -- focus on Sessions, Runtimes (especially integration tiers), and Workspaces.
3. **Architecture Overview** -- understand how the gateway communicates with your pod.
4. Continue to the **Runtime Author Guide** for the full adapter protocol, integration tiers (Minimum/Standard/Full), and the echo runtime sample you can copy as a starting point.

### Platform Operator

You are deploying Lenny to Kubernetes for your team or organization. You care about Helm configuration, scaling, observability, and security hardening.

**Recommended path:**
1. **Quickstart** -- verify your local setup works before moving to cluster deployment.
2. **Core Concepts** -- focus on Pools, Gateway, Tenants, and Credentials.
3. **Architecture Overview** -- understand all components, storage backends, and security boundaries.
4. Continue to the **Operator Guide** for Helm configuration, capacity planning, monitoring, and production hardening.

### Client Developer

You are building an application that creates and interacts with Lenny sessions via the API. You want to understand session lifecycle, file uploads, streaming, and how to use MCP or REST endpoints.

**Recommended path:**
1. **Quickstart** -- see the full session flow via curl.
2. **Core Concepts** -- focus on Sessions (lifecycle states), MCP, and Delegation.
3. **Architecture Overview** -- understand the gateway's external API surface and streaming model.
4. Continue to the **Client Guide** for the full API reference, SDK usage, and advanced patterns like delegation and elicitation.

### Contributor

You want to contribute to Lenny itself -- gateway code, controller logic, storage backends, or documentation.

**Recommended path:**
1. **Quickstart** -- run Lenny locally with `make run`.
2. **Core Concepts** -- read all sections to build a complete mental model.
3. **Architecture Overview** -- study component boundaries and internal protocols.
4. Read the full **Technical Design** document for the authoritative specification, then check `CONTRIBUTING.md` for development workflow and code conventions.
