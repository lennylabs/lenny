---
layout: default
title: "Runtime Author Guide"
nav_order: 4
has_children: true
description: Build your own agent runtime — the adapter protocol, the three integration levels (Basic, Standard, Full), the Go/Python/TypeScript SDKs, and publishing.
---

# Runtime Author Guide

## Who this guide is for

You're writing a program that Lenny will run as an agent. That could be:

- A custom agent you want to host inside Lenny's sandboxed pods.
- An adapter that wraps an existing framework -- LangChain, CrewAI, AutoGen -- and lets it run under Lenny.
- A specialized worker that processes tasks without calling an LLM at all (file processing, code analysis, test running).
- A small variation on one of the reference runtimes that changes configuration or wraps a different CLI.

Any of those, and this is the right guide.

If you're deploying Lenny to a cluster, calling its API from an application, or adding a connector to an external tool, you want the Operator Guide, the Client Guide, or the connector documentation instead.

---

## What "runtime" means in Lenny

A runtime is a program that reads JSON lines from standard input and writes JSON lines to standard output. That's the whole contract.

Your program runs inside a Kubernetes pod next to a small sidecar that Lenny provides. The sidecar takes care of all the platform plumbing: the connection back to the gateway, delivering the session's files into the pod, managing credentials, answering health checks. Messages from the user arrive on your program's stdin as JSON; your replies go to stdout.

```
                    Pod
 ┌──────────────────────────────────────────────┐
 │                                              │
 │  ┌──────────────────┐  stdin   ┌───────────┐ │
 │  │                  │ ──────── │           │ │
 │  │ Adapter sidecar  │          │ Your      │ │
 │  │ (Lenny-provided) │ ──────── │ program   │ │
 │  │                  │  stdout  │           │ │
 │  └────────┬─────────┘          └───────────┘ │
 │           │ encrypted back to the gateway    │
 └───────────┼──────────────────────────────────┘
             │
         Gateway
```

A few consequences of this design:

- **Any language works.** If it can read and write JSON lines, it can be a Lenny runtime.
- **You don't need a Lenny dependency.** The simplest integration has zero imports from Lenny. If you'd like more ergonomic code, official SDKs are available for Go, Python, and TypeScript (covered below).
- **You integrate deeper only when you need to.** The basic integration is about 50 lines. More capabilities are available when you want them, without rewriting what you have.
- **Sessions are isolated by default.** Every session gets its own pod with its own filesystem and network namespace. Depending on the pool's configuration, it can also be sandboxed under gVisor or run in a microVM via Kata Containers. Sessions never share memory or state.

---

## SDKs and scaffolding

You don't have to implement the wire format yourself. Lenny publishes official SDKs that handle stdin/stdout message parsing, the extended protocol features you opt into, and the lifecycle signals:

| Language | Install |
|---|---|
| Go | `go get github.com/lennylabs/runtime-sdk-go` |
| Python | `pip install lenny-runtime` |
| TypeScript / JavaScript | `npm install @lennylabs/runtime-sdk` |

The SDKs are thin wrappers, not frameworks. They expose whichever integration level you want; you can always drop to raw JSON if your language isn't represented or you'd rather keep the dependency footprint to zero.

### Scaffold a new runtime

`lenny runtime init` emits a working repository -- Dockerfile, entry point in your language, runtime manifest, Makefile, and CI workflow:

```bash
lenny runtime init my-agent --language go --template coding
lenny runtime init my-chat  --language python --template chat
lenny runtime init hello    --language typescript --template minimal
```

The three templates cover the common starting points:

- `coding` -- a coding agent. Pre-wired for a git-backed workspace, common toolchains inside the container, and sandboxed isolation.
- `chat` -- a minimal non-coding runtime that talks to an LLM and streams replies.
- `minimal` -- a plain hello-world that echoes each message.

Validate and publish:

```bash
lenny runtime validate                                    # checks your manifest and adapter compliance
lenny runtime publish my-agent \
  --image ghcr.io/my-org/my-agent:0.1.0                   # pushes the image and registers it
```

### Reference runtimes you can learn from (or fork)

Lenny ships with nine built-in runtimes. Read the source, fork one, or register them as-is:

| Runtime | Category | Integration level |
|---|---|---|
| `claude-code`, `gemini-cli`, `codex`, `cursor-cli` | Coding agents | Full |
| `chat` | General-purpose LLM | Standard |
| `langgraph`, `mastra`, `openai-assistants`, `crewai` | Framework adapters | Full |

The four coding agents are nearly identical -- same workspace layout, same pre-installed toolchains, same sandbox profile. They differ only in the image, the LLM credential, and the command the container runs. If you're wrapping a CLI, start there.

---

## The three integration levels

There are three levels of integration. Each adds capabilities on top of the previous one. Start at the level that covers what you need; you can always move up later.

### Basic

Your program reads messages from stdin and writes replies to stdout. Each line is a JSON object. You also respond to the occasional heartbeat (so the platform knows you're alive) and exit cleanly when you get a shutdown signal.

You get:

- Messages in, responses out.
- File access to the session's workspace through a small built-in tool vocabulary (`read_file`, `write_file`, `list_dir`, `delete_file`).
- A shortcut response format for simple replies: `{"type": "response", "text": "hello"}`.

You don't get:

- Delegation to other agents.
- Asking the user for input mid-session.
- Access to connectors (GitHub, Jira, Slack, etc.).
- Clean interrupt handling or graceful checkpoints.
- Advance warning before a deadline.

Use this level for stateless workers, simple wrappers around existing CLIs, and anything you want to prototype fast. About 50 lines of code in any language.

### Standard

On top of Basic, your program also opens a connection to a local tool server that the sidecar exposes. Through that connection you get:

- Delegation -- spawn a child session on another runtime and await its result.
- Mid-session user input -- ask the human a question and wait for their reply.
- Persistent memory that survives beyond the current session.
- Inter-session messaging.
- Connector tool access (GitHub, Jira, Slack, and so on).

What you still don't get at this level: clean interrupt handling (you'll get a SIGTERM when interrupted), graceful checkpointing (best-effort only), in-place credential rotation (a credential change requires a restart), advance deadline warnings, or pod reuse between tasks.

Use this level when your agent needs to call out to tools, delegate work, or talk to a human while it's running. About 150-200 lines of code plus an MCP client library.

### Full

On top of Standard, your program also opens a lifecycle channel -- a second connection that carries operational signals from the platform.

With it, you can support:

- Graceful checkpoints, where the platform asks your agent to pause at a consistent point and take a snapshot.
- Clean interrupts, where the agent is told to stop and acknowledges when it's reached a safe point.
- Credential rotation without restarting: the platform hands you a new credential and you acknowledge the swap.
- Advance warning before a deadline, so you can wrap up gracefully instead of being terminated.
- Pod reuse across sequential tasks in task-mode pools.
- Coordinated draining when the pool is shutting down.

Use this level for agents that need to survive pod failures, handle interrupts, and rotate credentials without restarting. About 300-400 lines of code, including a small background goroutine or thread to handle lifecycle signals.

---

## Where to read next

### To get something running fast (Basic level)

1. Scaffold a runtime: `lenny runtime init <name> --language <lang> --template minimal`.
2. Read the [Echo Runtime Sample](echo-runtime.md) for a complete working example you can copy.
3. Skim the [Adapter Contract](adapter-contract.md) for the exact message formats.
4. Use the [Integration Levels](integration-levels.md) reference to confirm what you can ignore at this level.
5. Use [Local Development](local-development.md) to run your runtime against `lenny up`.
6. Run the [compliance tests](testing.md) before you publish.

### To add delegation, connectors, or mid-session prompts (Standard level)

Read everything above, then:

7. [Platform Tools](platform-tools.md) -- every tool Lenny exposes to your agent, with parameters and examples.
8. [Delegation](delegation.md) -- spawning child tasks, enforcing budgets, handling results.
9. [Pod Lifecycle](lifecycle.md) -- what happens when a pod starts and stops.

### To support checkpoints, clean interrupts, and credential rotation (Full level)

Read everything above, then revisit:

10. [Pod Lifecycle](lifecycle.md) -- this time with attention to checkpoints, interrupts, and credential rotation.
11. [Adapter Contract](adapter-contract.md) -- the lifecycle channel message formats.

### When you're ready to ship

12. [Testing](testing.md) -- the full compliance suite for your integration level.
13. [Publishing](publishing.md) -- packaging the container, registering the runtime, and Helm integration.

---

## Fast path: scaffold, build, publish

The shortest route from an empty directory to a runtime running on Lenny:

```bash
# 1. Scaffold a repo in the language you want
lenny runtime init my-agent --language go --template chat
cd my-agent

# 2. Fill in your agent logic. The scaffold already handles message
#    parsing, heartbeats, and clean shutdown.

# 3. Build the container
make image

# 4. Register it against a running gateway -- the embedded stack is fine
lenny up
lenny runtime publish my-agent --image my-agent:dev

# 5. Try it
lenny session new --runtime my-agent --message "Hello"
```

Want to see the raw protocol first? The [Echo Runtime Sample](echo-runtime.md) is an ~80-line Go program that implements the Basic level with zero Lenny dependencies. Read it before picking up the SDK if you like understanding the wire format up front.
