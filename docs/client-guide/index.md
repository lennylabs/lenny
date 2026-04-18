---
layout: default
title: "Client Guide"
nav_order: 5
has_children: true
---

# Client Guide

For developers integrating Lenny into an application, a script, a CI pipeline, an IDE extension, or an MCP host. You only ever talk to one thing -- the Lenny gateway. The pods, pools, and controllers behind it are implementation details; you don't need to know Kubernetes to use any of the APIs on this page.

The gateway exposes everything a client needs -- creating and ending sessions, sending and streaming messages, uploading workspaces, downloading artifacts, delegating to child agents -- over four interchangeable protocols. Pick whichever one fits the code you already have.

---

## Pick how you want to drive a session

Every option below ultimately exercises the same session lifecycle. You can mix them in one application.

### The `lenny session` CLI

The CLI that ships with Lenny includes a `session` subcommand. It's the fastest way to drive a session without writing any client code -- useful for scripts, smoke tests, and day-to-day development.

```shell
lenny session start --runtime chat --message "hello"
lenny session message $SESSION_ID --message "continue the thought"
lenny session logs $SESSION_ID --follow
lenny session terminate $SESSION_ID
```

Running against `lenny up` locally? No flags needed. Running against a remote cluster? Add `--gateway https://lenny.example.com --token $TOKEN`.

### The web playground

Every installation serves a small web UI at `/playground`. You pick a runtime, upload a workspace, and chat with the session in the browser. It uses the exact same public API your SDK would -- there's no back channel. Useful when you want to demo a runtime to someone without putting them in a terminal.

You can turn the playground off in production (one Helm flag) or restrict it to authenticated tenants.

### Client SDKs and raw protocols

When you're ready to embed Lenny in code, pick whichever protocol matches what you already have:

| Protocol | Path | Use it when |
|:--|:--|:--|
| **REST** | `/v1/...` | Automation, CI/CD, admin dashboards, or any language that doesn't have a Lenny SDK. Covers everything. |
| **MCP (Streamable HTTP)** | `/mcp` | Interactive streaming, delegating to child agents, mid-session user prompts, and MCP hosts like Claude Desktop or Cursor. |
| **OpenAI Chat Completions** | `/v1/chat/completions` | You already use the OpenAI SDK. Change the `base_url`, keep the rest. |
| **Open Responses** | `/v1/responses` | You're using an Open Responses or OpenAI Responses API client. |

The OpenAPI description is served at `GET /openapi.yaml`. The MCP endpoint supports the 2025-03-26 and 2024-11-05 protocol versions.

---

## Protocol details

All four protocols are live at the same time on the same gateway. Pick based on what your client code already knows how to speak.

### REST (`/v1/...`)

A conventional HTTP API. Any language, any client library. Covers:

- Every session action -- create, upload a workspace, start, send messages, interrupt, resume, terminate, delete.
- Reading back logs, transcripts, artifacts, workspace snapshots, delegation trees, and usage reports.
- Admin operations -- runtime and pool configuration, credential pools, tenant management.
- Discovery -- `GET /v1/runtimes` lists the runtimes available to you, with their capabilities.
- OpenAPI at `GET /openapi.yaml` or `/openapi.json`.

### MCP (`/mcp`)

Lenny speaks MCP (Model Context Protocol) as a server over Streamable HTTP. This is the right choice when:

- You want bidirectional streaming and real-time output.
- You want an agent to spawn child agents and track them as tasks.
- You want mid-session prompts ("the agent asked the user a question -- here is the question, please collect an answer") handled naturally by the protocol.
- You're integrating an MCP host like Claude Desktop.

### OpenAI Chat Completions (`/v1/chat/completions`)

A drop-in for existing OpenAI SDK code. Point your `openai` client at Lenny's base URL and each Lenny runtime shows up as a model (via `GET /v1/models`). Streaming works the same way it does against OpenAI.

### Open Responses (`/v1/responses`)

A drop-in for the OpenAI Responses API and for clients that target the Open Responses specification.

---

## Which protocol should I pick?

| If you're... | Use | Because |
|---|---|---|
| Scripting a CI job | **REST** | Simple HTTP, easy to test, no streaming machinery required |
| Building an admin dashboard | **REST** | Full CRUD with pagination and filtering |
| Using a language without an SDK | **REST** | Works with `curl`, `httpx`, `fetch`, `net/http`, anything |
| Building an interactive UI with live output | **MCP** | Bidirectional streaming is built into the protocol |
| Building a multi-agent system | **MCP** | Delegation and task tracking are first-class |
| Asking the user questions mid-session | **MCP** | Mid-session prompts fit the protocol naturally |
| Already using the OpenAI SDK | **OpenAI Chat Completions** | Change one URL, done |
| Using an Open Responses / OpenAI Responses client | **Open Responses** | Standards-compliant, no vendor lock-in |

For most applications, REST + the log streaming endpoint (`GET /v1/sessions/{id}/logs` with `Accept: text/event-stream`) is the simplest combination. Reach for MCP when you need delegation, mid-session prompts, or live bidirectional I/O.

---

## The fast path from zero to a session

1. Install the CLI: `brew install lenny-dev/tap/lenny` (or grab a binary from the releases page).
2. Start the stack locally: `lenny up`. This runs the whole platform on your machine.
3. Open a session: `lenny session start --runtime chat --message "hello"`.
4. Prefer clicking? Visit `https://localhost:8443/playground`.

For a guided walkthrough, see the [`lenny up` walkthrough](../tutorials/lenny-up-walkthrough) and [Your First Session](../tutorials/first-session).

## The runtimes available out of the box

Every installation comes with these pre-registered:

| Runtime | What it is |
|:--|:--|
| `chat` | A minimal LLM chat. Useful for smoke tests and the tutorial path. |
| `claude-code` | Anthropic's Claude Code CLI, running in a sandboxed workspace. |
| `gemini-cli` | Google's Gemini CLI, running in a sandboxed workspace. |
| `codex` | OpenAI's Codex CLI, running in a sandboxed workspace. |
| `cursor-cli` | Cursor's CLI, running in a sandboxed workspace. |
| `langgraph` | A LangGraph graph runner. |
| `mastra` | A Mastra agent runner. |
| `openai-assistants` | An OpenAI Assistants adapter. |
| `crewai` | A CrewAI crew runner. |

List what's available and what each can do with `GET /v1/runtimes` or `lenny runtime list`.

---

## Reading order

If this is your first pass, read the pages in the order below:

1. [**Authentication**](authentication.html) -- how to get a token and register credentials
2. [**Session Lifecycle**](session-lifecycle.html) -- the state machine every session goes through
3. [**Streaming**](streaming.html) -- how to receive live output as the agent produces it
4. [**Delegation & Tasks**](delegation-and-tasks.html) -- multi-agent workflows
5. [**Error Handling**](error-handling.html) -- error codes, retry strategy, optimistic concurrency
6. [**Webhooks**](webhooks.html) -- asynchronous notifications and callbacks
7. [**Client SDK Examples**](sdk-examples/) -- runnable code in Python, TypeScript, Go, curl, and the MCP SDK

Each page stands alone -- tables of endpoints, code examples, and the exact responses you should expect. The SDK Examples section has end-to-end scripts that take a session from creation to teardown in every language.
