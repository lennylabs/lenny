---
layout: default
title: Quickstart
parent: Getting Started
nav_order: 1
---

# Quickstart

**Goal:** install Lenny on your laptop, chat with an agent, and shut it down -- in under five minutes.

You'll use `lenny up`, a single binary that runs the whole platform on your machine: an embedded Kubernetes cluster, the database and cache it needs, a stand-in key-management service and identity provider for development, the gateway, the management plane, and the nine reference runtimes. No Docker, no external services, no prior Kubernetes setup required.

This is the recommended starting point for anyone evaluating Lenny. If you're going to contribute to the gateway or controllers themselves and want faster iteration, see [Local Development](../runtime-author-guide/local-development.html) for `make run` (native process) and `docker compose up` (containerized) alternatives.

---

## Prerequisites

| Requirement | Version | Notes |
|------------|---------|-------|
| **Lenny CLI** | latest | `brew install lenny-dev/tap/lenny`, or download a single binary from the [releases page](https://github.com/lenny-dev/lenny/releases) |
| **macOS or Linux** | anything recent | Windows works through WSL2 |

That's it. The first `lenny up` downloads its dependencies into `~/.lenny/` and takes about a minute. Every subsequent start is a few seconds.

---

## Step 1: Start the embedded stack

```bash
lenny up
```

You should see something like:

```
Embedded mode. NOT for production use. Credentials, encryption keys,
and identities are insecure.

[✓] Kubernetes cluster ready
[✓] database ready
[✓] cache ready
[✓] key management ready
[✓] identity provider ready
[✓] gateway listening on https://localhost:8443
[✓] management plane ready
[✓] runtimes installed: chat, claude-code, gemini-cli, codex,
    cursor-cli, langgraph, mastra, openai-assistants, crewai
[✓] playground available at https://localhost:8443/playground

Ready in 47s. Try: lenny session start --runtime chat --message "hello"
```

The warning banner is deliberate: the embedded stack uses stub credentials and is meant for development and evaluation, not production. It cannot be suppressed.

`lenny up` runs the same gateway, controller, and management-plane binaries a production cluster runs. Only the external dependencies (Postgres, Redis, KMS, identity provider) are replaced with in-process equivalents.

---

## Step 2: Open a chat session

Start a session with the built-in `chat` runtime. It's a plain LLM chat, no tools -- the simplest useful agent. No API keys needed; the embedded stack ships with a mock provider that returns deterministic replies.

```bash
lenny session start --runtime chat --message "What is Lenny?"
```

Output (streamed):

```
session.id=ses_01HN0J...                       state=running
assistant: Lenny is a self-hosted platform for running interactive
           AI agents in isolated sandboxes on your own Kubernetes
           cluster...
session.id=ses_01HN0J...                       state=suspended
```

Behind the scenes, `lenny session` is a thin CLI over the same public API every other client uses -- the platform doesn't have a back door for the CLI.

---

## Step 3: Continue the conversation

Send a follow-up message:

```bash
lenny session message ses_01HN0J... --message "Give me three bullet points on delegation."
```

Stream the live log feed from the session in a second terminal:

```bash
lenny session logs ses_01HN0J... --follow
```

`Ctrl+C` detaches the log stream without affecting the session.

---

## Step 4: Try a coding agent

The runtime catalog includes four coding agents: `claude-code`, `gemini-cli`, `codex`, and `cursor-cli`. Each one wraps its respective CLI inside a gVisor sandbox with a fresh workspace prepared from a directory you give it.

Point one at a local repository:

```bash
lenny session start --runtime claude-code --workspace ./my-repo \
  --message "Read the README and summarize the architecture."
```

The gateway copies your repo into the pod's workspace, starts the agent, and streams its output back. Anything the agent writes into its output directory is downloadable as an artifact after the session ends.

By default the embedded stack uses mock LLM credentials. To try a real provider, export its key before you bring the stack up:

```bash
export ANTHROPIC_API_KEY=sk-ant-...
lenny down && lenny up
```

---

## Step 5: Open the web playground

In a browser, visit [`https://localhost:8443/playground`](https://localhost:8443/playground). The playground is a small web UI that lets you pick a runtime, upload a workspace, and chat with a session -- useful when you want to demo something to a colleague without handing them a terminal. It uses the same public API the CLI and SDKs use.

On the embedded stack it runs open with a red "not for production" banner. In a real cluster it's gated behind your chosen authentication, and can be turned off entirely with one flag.

---

## Step 6: Inspect and tear down

List your sessions:

```bash
lenny session list
```

Check platform state:

```bash
lenny status                                # overall health and active sessions
lenny-ctl admin pools list                  # warm pool state
lenny-ctl admin diagnostics connectivity    # end-to-end dependency check
```

When you're done:

```bash
lenny down          # shuts down the stack; keeps your data in ~/.lenny/
lenny down --purge  # also deletes ~/.lenny/ for a clean slate
```

---

## What just happened

You exercised the same flow every client uses in production:

```
lenny up             →  Kubernetes cluster, gateway, controllers, and
                         management plane come up; the warm pool
                         controller starts pre-warming pods.
session start        →  Gateway authenticates you, claims a warm pod,
                         copies your files in, starts the agent, and
                         opens a streaming connection.
session message     →  Your message and the agent's reply flow through
                         the gateway, which also routes tool calls and
                         any mid-session user prompts.
(idle)               →  Between messages the agent stays warm -- no
                         re-initialization cost for the next turn.
lenny down           →  Everything shuts down gracefully. Your session
                         history lives in the embedded database and is
                         still there the next time you run `lenny up`.
```

A few properties you saw in action, which apply equally to a production install:

- **The gateway is the only entrance.** Your pod never had an IP address you could reach.
- **Workspace files go through the gateway.** No shared disks, no mounted volumes.
- **The same platform runs any agent.** `chat`, `claude-code`, and whatever you build yourself all integrate the same way.
- **Pods are locked down by default.** Non-root, no extra Linux capabilities, sandboxed under gVisor, and no network access except back to the gateway.

---

## Next steps

### If you're building an agent

You saw `chat` and `claude-code` in action. To build your own:

1. Scaffold a new runtime: `lenny runtime init my-agent --language go --template coding`.
2. Read the [Runtime Author Guide](../runtime-author-guide/) for the three integration levels and what each one gives you.
3. Use the Go (`github.com/lenny-io/runtime-sdk-go`), Python (`lenny-runtime`), or TypeScript (`@lenny-io/runtime-sdk`) SDK to skip the wire format.
4. Run `lenny runtime validate` before publishing to catch integration problems early.

### If you're deploying to a real cluster

You ran Lenny as a single binary. To move to a cluster:

1. Run `lenny-ctl install` against the target cluster -- an interactive wizard that inspects what your cluster can do, asks about ten targeted questions, and produces a Helm values file you can commit.
2. Or hand-write values using the [examples under `spec/deployment-topology`](../reference/).
3. Read the [Operator Guide](../operator-guide/) for hardening, observability, the management plane, and `lenny-ctl doctor --fix`.

### If you're writing a client

You used the CLI. To build a real integration:

1. Read the [Client Guide](../client-guide/) for the API reference and protocol choices.
2. Pick an SDK (Go, Python, TypeScript) or use any MCP host.
3. Work through [delegation](../tutorials/recursive-delegation) if you're building multi-agent workflows.

### If you're contributing to Lenny itself

`lenny up` is enough for most development. When you're iterating on the gateway or controllers and don't want to rebuild the binary, use `make run` (native process) or `docker compose up` (containerized) -- both documented in [Local Development](../runtime-author-guide/local-development.html).
