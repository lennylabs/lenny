---
layout: default
title: Quickstart
parent: Getting Started
nav_order: 1
---

# Quickstart

**Goal:** Install Lenny on your laptop, open a chat session against a real agent, and tear it down -- all in under 5 minutes.

This guide uses Lenny's **Tier 0 embedded stack** (`lenny up`), a single binary that boots k3s, Postgres, Redis, a KMS shim, an OIDC provider, the gateway, `lenny-ops`, and the reference runtime catalog in-process. No Docker, no Kubernetes, no external services required.

Tier 0 is the recommended starting point for every persona. Tier 1 (`make run`) and Tier 2 (`docker compose up`) remain available for contributors and CI scenarios; see [Local Development](../runtime-author-guide/local-development.html).

---

## Prerequisites

| Requirement | Version | Notes |
|------------|---------|-------|
| **Lenny CLI** | latest | Single binary; download from the [releases page](https://github.com/lenny-dev/lenny/releases) or `brew install lenny-dev/tap/lenny` |
| **macOS or Linux** | Any recent | k3s runs rootless where supported. Windows users should use WSL2 |

That is all. The first `lenny up` downloads k3s into `~/.lenny/k3s/` and seeds the embedded Postgres and Redis. Subsequent runs start in seconds.

---

## Step 1: Start the embedded stack

```bash
lenny up
```

You should see output similar to:

```
Tier 0 embedded mode. NOT for production use. Credentials, KMS master
key, and identities are insecure.

[✓] k3s started (127.0.0.1:6443)
[✓] postgres ready
[✓] redis ready
[✓] kms shim ready
[✓] oidc provider ready (issuer=https://localhost:8443/dev-oidc)
[✓] gateway listening on https://localhost:8443 (plain http on :8080)
[✓] lenny-ops listening on https://localhost:8443/ops
[✓] installed reference runtimes: chat, claude-code, gemini-cli, codex,
    cursor-cli, langgraph, mastra, openai-assistants, crewai
[✓] playground available at https://localhost:8443/playground

Ready in 47s. Try: lenny session new --runtime=chat --attach "hello"
```

The red production-warning banner is not suppressible -- Tier 0 is only for development and evaluation.

Under the hood, `lenny up` is *not* a simulator: it runs the same gateway, controllers, CRDs, and `lenny-ops` binaries that production Kubernetes deployments use. Every reference runtime is registered against the platform-global registry and granted access to the `default` tenant.

---

## Step 2: Open a chat session

Open a session against the bundled `chat` runtime. This is an LLM-only runtime with no tools -- the simplest possible useful agent. It requires no API keys; Tier 0 seeds a mock provider that replays deterministic responses.

```bash
lenny session new --runtime=chat --attach "What is Lenny?"
```

**Output (streamed):**

```
session.id=ses_01HN0J...                       state=running
assistant: Lenny is a Kubernetes-native, runtime-agnostic agent
           session platform. It manages the lifecycle of interactive
           agent sessions -- pod allocation, workspace materialization,
           streaming I/O, recursive delegation, credential leasing, and
           session recovery -- behind a unified gateway.
session.id=ses_01HN0J...                       state=suspended
```

`lenny session` commands route through the MCP client SDK, not REST. They exercise the same code path that a production MCP client uses.

---

## Step 3: Continue the conversation

While attached, type follow-up messages and press Enter. To detach without ending the session, press `Ctrl+]`. You can always re-attach:

```bash
lenny session attach ses_01HN0J...
```

Or send a one-off message without attaching:

```bash
lenny session send ses_01HN0J... "Give me three bullet points on delegation."
```

---

## Step 4: Try a coding agent

The reference runtime catalog includes four coding agents: `claude-code`, `gemini-cli`, `codex`, and `cursor-cli`. Each wraps an existing CLI inside a gVisor-isolated sandbox with a materialized workspace.

Point the agent at a local repository:

```bash
lenny session new --runtime=claude-code --workspace=./my-repo --attach \
  "Read the README and summarize the architecture."
```

The gateway materializes `./my-repo` into the pod's `/workspace/current/`, starts the runtime, and streams its output back to your terminal. Any files the agent writes to `/workspace/output/` are recoverable via the artifact API after the session ends.

Tier 0 uses mock LLM credentials by default. To exercise a real provider, set the corresponding environment variable before `lenny up`:

```bash
export ANTHROPIC_API_KEY=sk-ant-...
lenny down && lenny up
```

---

## Step 5: Open the web playground

Open [`https://localhost:8443/playground`](https://localhost:8443/playground) in your browser. The playground is a minimal single-page UI for picking a runtime, uploading a workspace, and chatting with the session. It speaks the same public MCP surface your CLI just used -- no private endpoints.

In Tier 0, the playground runs in `authMode=dev` (no auth) with a persistent red "DEV MODE -- NOT FOR PRODUCTION" banner. Production installations gate the playground behind OIDC or API keys.

---

## Step 6: Inspect and tear down

List your active sessions:

```bash
lenny session list
```

Inspect platform state without leaving the CLI:

```bash
lenny status                          # component health + active sessions
lenny-ctl admin pools list            # warm pool state
lenny-ctl admin diagnostics connectivity   # end-to-end dependency check
```

When you are done, shut everything down:

```bash
lenny down          # stops the stack; preserves data in ~/.lenny/
lenny down --purge  # also deletes ~/.lenny/ for a fresh start next time
```

---

## What just happened?

You exercised the full platform flow -- the same flow every Lenny client uses in production:

```
lenny up            →  Embedded k3s comes up; CRDs install; gateway,
                        lenny-ops, controllers, and reference runtimes
                        start; warm pool controller pre-warms pods.
session new         →  Gateway authenticates, claims a warm pod,
                        materializes the workspace, starts the runtime
                        adapter, and opens an MCP stream.
session attach      →  Bidirectional MCP stream carries messages,
                        tool-call events, elicitation prompts, and
                        lifecycle transitions.
session (implicit)  →  Runtime suspends between messages; pod remains
                        warm in task-mode pools.
lenny down          →  Gracefully terminates components. Session state
                        persists in embedded Postgres for next `lenny up`.
```

Design principles you touched:

- **Gateway-centric.** Every interaction went through the gateway. The pod was never directly exposed.
- **Pod-local workspace, gateway-owned state.** The repo was streamed into the pod through the gateway; no shared volume mounts.
- **Runtime-agnostic.** `chat`, `claude-code`, and any custom runtime you build all use the same adapter contract.
- **Secure by default.** Pods run non-root with all capabilities dropped, under gVisor, with default-deny networking.

---

## Next steps

### For runtime authors

You just saw the `chat` and `claude-code` reference runtimes. To build your own:

1. Scaffold a new runtime: `lenny runtime init my-agent --language go --template coding`.
2. Read the [Runtime Author Guide](../runtime-author-guide/) for the three integration tiers (Minimum, Standard, Full).
3. Use the first-party [Runtime Author SDKs](../runtime-author-guide/) for Go (`github.com/lenny-io/runtime-sdk-go`), Python (`lenny-runtime`), or TypeScript (`@lenny-io/runtime-sdk`) to skip the raw wire-protocol work.
4. `lenny runtime validate` your repo against the adapter specification before publishing.

### For platform operators

You ran Lenny as a single binary. To move toward a real cluster:

1. Run `lenny-ctl install` against your target cluster -- the interactive wizard detects capabilities, asks ~10 targeted questions, and produces a composed `values.yaml` you can commit to Git.
2. Or hand-write values using an answer-file base from [`spec/deployment-topology`](../reference/) (e.g., `eks-small-team.yaml`).
3. Read the [Operator Guide](../operator-guide/) for hardening, observability, `lenny-ops` agent operability endpoints, and the `lenny-ctl doctor --fix` diagnostics loop.

### For client developers

You used the CLI. To build a real integration:

1. Read the [Client Guide](../client-guide/) for the full MCP and REST API reference.
2. Use one of the bundled client SDKs (Go, Python, TypeScript) or any off-the-shelf MCP host.
3. Explore [delegation](../tutorials/recursive-delegation) for multi-agent workflows.

### For contributors

`lenny up` covers most day-to-day work. When you need to iterate on core components without repackaging the binary, drop to Tier 1 (`make run`) or Tier 2 (`docker compose up`) -- both documented in [Local Development](../runtime-author-guide/local-development.html).
