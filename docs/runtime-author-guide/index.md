---
layout: default
title: "Runtime Author Guide"
nav_order: 4
has_children: true
---

# Runtime Author Guide

## Who This Guide Is For

This guide is for developers who want to build **agent runtimes** that run on the Lenny platform. A runtime is any program that receives tasks, processes them (typically by invoking an LLM), and produces output. If you are building:

- A custom AI agent that needs to run inside Lenny's managed pod infrastructure
- An adapter that wraps an existing agent framework (LangChain, CrewAI, AutoGen, etc.) to work with Lenny
- A specialized worker that processes tasks without an LLM (file processors, code analyzers, test runners)
- A derived runtime that extends an existing Lenny runtime with custom configuration

...then this guide is for you.

You do **not** need this guide if you are a Lenny deployer (managing clusters and pools), a client SDK user (calling the Lenny API from an application), or a connector author (integrating external tools via MCP).

---

## What Is a "Runtime" in Lenny?

A **runtime** in Lenny is any process that implements the adapter contract --- a simple stdin/stdout JSON Lines protocol. Your runtime binary runs inside a Kubernetes pod alongside the Lenny **adapter sidecar**. The adapter handles all platform communication (gRPC to the gateway, file delivery, credential management, health checks) and translates everything into a stream of JSON messages on your binary's stdin. Your binary reads those messages, does its work, and writes JSON responses to stdout.

```
                    Lenny Pod
 ┌──────────────────────────────────────────────┐
 │                                              │
 │  ┌──────────────────┐  stdin   ┌───────────┐ │
 │  │                  │ ──────── │           │ │
 │  │  Adapter Sidecar │          │ Your      │ │
 │  │  (Lenny-managed) │ ──────── │ Runtime   │ │
 │  │                  │  stdout  │ Binary    │ │
 │  └────────┬─────────┘          └───────────┘ │
 │           │ gRPC (mTLS)                      │
 └───────────┼──────────────────────────────────┘
             │
       Lenny Gateway
```

The key properties of this architecture:

- **Language-agnostic.** Your runtime can be written in any language. If it can read lines from stdin and write lines to stdout, it works.
- **No Lenny SDK required, SDKs available.** The simplest runtimes need zero Lenny dependencies. For idiomatic integrations, use the first-party [Runtime Author SDKs](#first-party-sdks-and-scaffolding) for Go, Python, and TypeScript.
- **Progressive complexity.** Start with the simplest possible integration and add platform features incrementally as you need them.
- **Isolation by default.** Each runtime runs in its own pod with its own filesystem, network namespace, and (optionally) a gVisor or Kata sandbox. You never share memory or state with other sessions.

---

## First-Party SDKs and Scaffolding

You do not have to hand-roll the adapter wire format. Lenny ships official SDKs that encapsulate the stdin/stdout JSON Lines protocol, the abstract-Unix-socket MCP handshake for Standard tier, and the lifecycle channel for Full tier.

| Language | Import | Source |
|---|---|---|
| Go | `github.com/lenny-io/runtime-sdk-go` | `github.com/lenny-io/runtime-sdk-go` |
| Python | `lenny-runtime` (PyPI) | `github.com/lenny-io/runtime-sdk-python` |
| TypeScript / JavaScript | `@lenny-io/runtime-sdk` (npm) | `github.com/lenny-io/runtime-sdk-ts` |

The SDKs are thin -- they mirror the tier model, so you pay only for the features you opt into. You can drop down to raw JSON Lines at any point if your language is not represented or you prefer a minimal dependency footprint.

### Scaffolding a new runtime

`lenny runtime init` emits a complete repository skeleton with Dockerfile, entry point in your chosen language, `runtime.yaml`, Makefile, and a CI workflow:

```bash
lenny runtime init my-agent --language go --template coding
lenny runtime init my-chat  --language python --template chat
lenny runtime init hello    --language typescript --template minimal
```

Templates:

- `coding` -- pre-wires the shared coding-agent workspace plan (git materialization, pre-installed toolchains, sandboxed isolation).
- `chat` -- a minimal non-coding runtime that speaks to an LLM and streams responses.
- `minimal` -- a bare Hello World that echoes messages.

Validate and publish:

```bash
lenny runtime validate                       # checks runtime.yaml + adapter compliance
lenny runtime publish my-agent \
  --image ghcr.io/my-org/my-agent:0.1.0      # docker push + register against the gateway
```

### Reference runtime catalog

Lenny ships with nine production-shaped reference runtimes you can read, fork, or register as-is:

| Runtime | Category | Tier |
|---|---|---|
| `claude-code`, `gemini-cli`, `codex`, `cursor-cli` | Coding agents | Full |
| `chat` | General-purpose LLM | Standard |
| `langgraph`, `mastra`, `openai-assistants`, `crewai` | Framework runtimes | Full |

All four coding-agent runtimes share the same workspace layout, pre-installed toolchains, and gVisor isolation -- the only differences are image, LLM credential, and the shell command invoked inside the pod. Start there if you are wrapping an existing CLI.

---

## The Three Integration Tiers

Lenny defines three integration tiers. Each tier adds capabilities on top of the previous one. You choose the tier that matches your needs --- you can always upgrade later.

### Minimum Tier

**What it is:** stdin/stdout JSON Lines only. Your binary reads `message` objects from stdin and writes `response` objects to stdout. You also handle `heartbeat` (respond with `heartbeat_ack`) and `shutdown` (exit cleanly).

**What you get:**
- Task execution (receive input, produce output)
- Workspace file access via built-in adapter-local tools (`read_file`, `write_file`, `list_dir`, `delete_file`)
- Simplified response shorthand (`{"type": "response", "text": "hello"}`)

**What you do not get:**
- No delegation (cannot spawn child tasks)
- No platform MCP tools (`lenny/output`, `lenny/request_input`, etc.)
- No connector access (GitHub, Jira, etc.)
- No clean interrupt handling
- No cooperative checkpointing
- No advance deadline warnings

**When to use it:** For simple, stateless workers. For wrapping existing CLI tools. For getting started quickly. For runtimes that do not need LLM provider credentials managed by Lenny.

**Implementation effort:** ~50 lines of code in any language.

### Standard Tier

**What it is:** Minimum tier plus connections to the adapter's local MCP servers (platform MCP server and per-connector MCP servers) via abstract Unix sockets.

**What you get (in addition to Minimum):**
- All platform MCP tools: `lenny/delegate_task`, `lenny/await_children`, `lenny/cancel_child`, `lenny/discover_agents`, `lenny/output`, `lenny/request_elicitation`, `lenny/memory_write`, `lenny/memory_query`, `lenny/request_input`, `lenny/send_message`, `lenny/get_task_tree`
- Connector tool access (GitHub, Jira, Slack, etc.)
- Delegation: spawn child sessions on other runtimes
- Elicitation: request human input
- Memory: persistent cross-session memory
- Inter-session messaging

**What you do not get:**
- No cooperative checkpointing (best-effort only)
- No clean interrupt handling (SIGTERM-based)
- No in-place credential rotation (requires pod restart)
- No advance deadline warnings
- No task-mode pod reuse

**When to use it:** For agents that need to delegate work, call external tools, or interact with humans during execution.

**Implementation effort:** ~150-200 lines of code plus an MCP client library for your language.

### Full Tier

**What it is:** Standard tier plus the lifecycle channel --- a bidirectional JSON Lines socket (`@lenny-lifecycle`) for operational signals.

**What you get (in addition to Standard):**
- Cooperative checkpointing: `checkpoint_request` / `checkpoint_ready` / `checkpoint_complete` handshake for consistent snapshots
- Clean interrupt handling: `interrupt_request` / `interrupt_acknowledged` for safe pause points
- In-place credential rotation: `credentials_rotated` / `credentials_acknowledged` with zero session interruption
- Advance deadline warnings: `deadline_approaching` signal before session expiry
- Task-mode pod reuse: `task_complete` / `task_complete_acknowledged` / `task_ready` for sequential task execution without pod replacement
- Graceful drain coordination

**When to use it:** For production-grade agents that need session continuity across pod failures, clean interrupt semantics, and zero-downtime credential rotation.

**Implementation effort:** ~300-400 lines of code, including a background goroutine/thread for lifecycle signal handling.

---

## Reading Roadmap

### If you want the Minimum tier (get something running fast):

1. **Scaffold a runtime** --- `lenny runtime init <name> --language <lang> --template minimal` emits a working skeleton.
2. **[Echo Runtime Sample](echo-runtime.md)** --- Copy this working code as an alternative starting point.
3. **[Adapter Contract](adapter-contract.md)** --- The stdin/stdout JSON Lines protocol, message types, and wire format.
4. **[Integration Tiers](integration-tiers.md)** --- Confirms what you can skip at Minimum tier.
5. **[Local Development](local-development.md)** --- Use `lenny up` to test your runtime against a full platform; drop to `make run` when you need to iterate on the gateway itself.
6. **[Testing](testing.md)** --- Run the compliance suite against your runtime.

### If you want the Standard tier (add MCP tools and delegation):

Read everything above, then:

6. **[Platform MCP Tools](platform-tools.md)** --- Every platform tool with parameters, return values, and examples.
7. **[Delegation](delegation.md)** --- How to spawn child tasks, manage budgets, and handle results.
8. **[Pod Lifecycle](lifecycle.md)** --- Understand pod states, workspace materialization, and checkpointing.

### If you want the Full tier (production-grade lifecycle management):

Read everything above, then revisit:

9. **[Pod Lifecycle](lifecycle.md)** --- Focus on cooperative checkpointing, interrupt handling, and credential rotation.
10. **[Adapter Contract](adapter-contract.md)** --- Focus on the lifecycle channel message schemas.

### When you are ready to ship:

11. **[Testing](testing.md)** --- Full compliance suite for your tier.
12. **[Publishing](publishing.md)** --- Container packaging, Helm integration, and runtime registration.

---

## Quick Start: Scaffold, build, publish

The fastest path from zero to a registered runtime:

```bash
# 1. Scaffold a new runtime repo in your language of choice
lenny runtime init my-agent --language go --template chat
cd my-agent

# 2. Implement your logic against the SDK; the scaffold pre-wires
#    message handling, heartbeats, and graceful shutdown.

# 3. Build the container
make image

# 4. Register it against a live gateway (Tier 0 works for this)
lenny up
lenny runtime publish my-agent --image my-agent:dev

# 5. Open a session
lenny session new --runtime=my-agent --attach "Hello"
```

Prefer the raw protocol? The [Echo Runtime Sample](echo-runtime.md) is a complete ~80-line Go program that implements the Minimum tier with zero Lenny dependencies. Read that first if you want to understand the wire format before picking up the SDK.
