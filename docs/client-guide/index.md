---
layout: default
title: "Client Guide"
nav_order: 5
has_children: true
---

# Client Guide

This guide is for **developers building applications that interact with Lenny** -- whether you are integrating an AI agent workflow into a CI/CD pipeline, building an interactive chat UI, orchestrating multi-agent delegation trees, or connecting existing OpenAI SDK code to a Lenny deployment.

Lenny is a Kubernetes-native, runtime-agnostic agent session platform. You do not need to know anything about Kubernetes to use the client APIs. The platform handles pod lifecycle, credential management, workspace isolation, and delegation for you. From your perspective, Lenny exposes HTTP endpoints and streaming protocols that let you create sessions, send messages to agents, receive streaming output, and retrieve artifacts.

---

## Protocol Options

Lenny exposes multiple client-facing APIs simultaneously through its **ExternalAdapterRegistry**. Each protocol serves different use cases and all share the same underlying session engine.

### REST API (`/v1/...`)

The REST API covers all non-interactive operations and is the primary integration point for automation. It works with any HTTP client in any language.

- **Full session lifecycle**: create, upload, finalize, start, message, interrupt, resume, terminate, delete
- **Artifacts and introspection**: logs, transcripts, workspace snapshots, delegation trees, usage reports
- **Admin operations**: runtime management, pool configuration, credential pools, tenant management
- **Discovery**: `GET /v1/runtimes` returns available runtimes with capabilities
- **OpenAPI spec**: served at `GET /openapi.yaml` (or `/openapi.json`)

### MCP (`/mcp`)

The MCP (Model Context Protocol) interface is for **interactive streaming sessions** and **recursive delegation**. Lenny acts as an MCP server over Streamable HTTP.

- **Interactive sessions**: bidirectional streaming with SSE for server-to-client events
- **Delegation**: spawn child sessions, await results, handle elicitation chains
- **Elicitation**: human-in-the-loop prompts surface through the MCP task model
- **Version negotiation**: supports MCP 2025-03-26 and 2024-11-05

### OpenAI Completions (`/v1/chat/completions`)

Drop-in compatibility with the OpenAI Chat Completions API. Existing code using the OpenAI SDK can point at Lenny with minimal changes.

- **Model discovery**: `GET /v1/models` returns available runtimes as models
- **Streaming**: supports `stream: true` with SSE chunks
- **Minimal migration**: change the base URL, keep your existing SDK code

### Open Responses (`/v1/responses`)

Compatibility with the Open Responses specification (and OpenAI Responses API clients).

- **Model discovery**: `GET /v1/models` returns available runtimes
- **Open Responses-compliant**: works with any client implementing the open specification

---

## When to Use Which Protocol

| Use Case | Recommended Protocol | Why |
|---|---|---|
| CI/CD pipelines | **REST** | Simple HTTP calls, easy to script, no streaming needed |
| Admin dashboards | **REST** | Full CRUD for all resources, pagination, filtering |
| Simple session management | **REST** | Create, start, poll status, retrieve artifacts |
| Any language without SDK | **REST** | Works with `curl`, `httpx`, `fetch`, `net/http`, etc. |
| Interactive streaming sessions | **MCP** | Bidirectional streaming, real-time output |
| Multi-agent delegation | **MCP** | Task trees, elicitation chains, child session management |
| Human-in-the-loop workflows | **MCP** | Elicitation requests surface naturally in the MCP model |
| MCP-native clients | **MCP** | Direct MCP tool access, version negotiation |
| Existing OpenAI SDK code | **OpenAI Completions** | Change the base URL, keep everything else |
| Minimal migration from OpenAI | **OpenAI Completions** | Familiar API surface, streaming support |
| Open Responses-compliant clients | **Open Responses** | Standard-compliant, vendor-neutral |

Most applications will use the **REST API** for session management and artifact retrieval, optionally combined with **SSE streaming** (`Accept: text/event-stream` on `GET /v1/sessions/{id}/logs`) for real-time output. The MCP protocol is the right choice when you need interactive streaming, delegation, or elicitation.

---

## Reading Order

If you are new to Lenny, read the pages in this order:

1. **[Authentication](authentication.html)** -- how to obtain tokens and register credentials
2. **[Session Lifecycle](session-lifecycle.html)** -- the complete session state machine and API calls
3. **[Streaming](streaming.html)** -- how to receive real-time output from agents
4. **[Delegation & Tasks](delegation-and-tasks.html)** -- multi-agent delegation trees
5. **[Error Handling](error-handling.html)** -- error codes, retry strategies, ETags
6. **[Webhooks](webhooks.html)** -- async notifications and callback patterns
7. **[Client SDK Examples](sdk-examples/)** -- complete, runnable code in Python, TypeScript, Go, curl, and MCP SDK

Each page is self-contained with API reference tables, code examples, and expected responses. The SDK Examples section provides complete, runnable scripts that demonstrate the full session lifecycle in each language.
