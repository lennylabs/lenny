---
layout: default
title: "Client SDK Examples"
parent: "Client Guide"
nav_order: 7
has_children: true
---

# Client SDK Examples

Usage guides for the Lenny client SDKs and the raw protocols.

Lenny ships an official client SDK for Go, TypeScript, and Python. Each SDK
wraps the gateway REST session API: the session lifecycle (create, get, list,
delete, finalize, start, interrupt, terminate, resume), the typed error
envelope with retryable-error backoff, per-request idempotency keys,
pluggable authentication, and an HMAC-SHA256 webhook signature verifier. The
SDK pages cover installation, constructing a client, running a session, and
verifying a webhook.

The curl and MCP SDK pages cover the same gateway from the raw protocols. The
curl page is a command reference for the REST endpoints. The MCP SDK page
covers the Model Context Protocol surface.

---

## Which page to use

| Page | Language | Covers |
|---|---|---|
| [Go](go.html) | Go 1.25+ | The Go client SDK. |
| [TypeScript](typescript.html) | TypeScript, Node.js 18+ | The TypeScript client SDK. |
| [Python](python.html) | Python 3.10+ | The Python client SDK. |
| [curl](curl.html) | Bash | A curl command reference for every REST endpoint. |
| [MCP SDK](mcp-sdk.html) | TypeScript and Python | The Model Context Protocol surface. |

---

## SDK scope

The Go, TypeScript, and Python SDKs wrap the REST session lifecycle and the
webhook signature verifier. They cover automation, backend services, CI
pipelines, and any code that drives sessions through the gateway REST API.

The SDKs do not yet implement the streaming and MCP-client surfaces. For
bidirectional streaming, delegation tree management, and mid-session prompts,
use the [MCP SDK](mcp-sdk.html). For live log output over Server-Sent Events,
call `GET /v1/sessions/{id}/logs` with `Accept: text/event-stream` directly;
see [Streaming](../streaming.html).

---

## Gateway base URL

Each SDK is constructed with a gateway base URL. The base URL is the gateway
origin, for example `https://gateway.acme.com`; the SDK appends the `/v1`
path prefix. Replace the example origin with the URL of your deployment's
gateway.

The OpenAPI description is served at `GET /openapi.yaml` (or `/openapi.json`)
on any Lenny gateway. Use it to generate a typed client for a language without
an official SDK.
