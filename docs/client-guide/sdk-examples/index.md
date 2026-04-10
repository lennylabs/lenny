---
layout: default
title: "Client SDK Examples"
parent: "Client Guide"
nav_order: 7
has_children: true
---

# Client SDK Examples

Complete, runnable code examples for interacting with the Lenny API. Each example demonstrates the full session lifecycle:

1. **Authenticate** -- obtain an access token
2. **Create session** -- specify a runtime and pool
3. **Upload files** -- send workspace files to the session
4. **Start session** -- launch the agent runtime
5. **Send message** -- deliver a prompt to the agent
6. **Stream output** -- receive real-time agent output
7. **Retrieve artifacts** -- download results and transcripts
8. **Terminate** -- cleanly end the session

---

## Which Example to Use

| Example | Language | HTTP Client | Best For |
|---|---|---|---|
| [Python](python.html) | Python 3.10+ | httpx (async) + requests (sync) | Data pipelines, backend services, scripting |
| [TypeScript](typescript.html) | TypeScript / Node.js | fetch | Web backends, serverless functions, full-stack apps |
| [Go](go.html) | Go 1.21+ | net/http | High-performance services, CLI tools, infrastructure |
| [curl](curl.html) | Bash | curl | Quick testing, shell scripts, CI/CD pipelines |
| [MCP SDK](mcp-sdk.html) | TypeScript + Python | MCP SDK | Interactive streaming, delegation, elicitation |

---

## REST API vs. MCP SDK

The **REST API** examples (Python, TypeScript, Go, curl) use standard HTTP requests. They work with any HTTP client and are the simplest integration path.

The **MCP SDK** examples use the Model Context Protocol SDK for interactive streaming sessions. Use MCP when you need:

- Bidirectional streaming with real-time output
- Delegation tree management
- Elicitation (human-in-the-loop prompts)
- MCP-native clients

For most automation, CI/CD, and backend use cases, the REST API is sufficient and simpler.

---

## API Base URL

All examples use a configurable base URL. Replace `https://lenny.example.com` with your deployment's gateway URL.

The OpenAPI specification is available at `GET /openapi.yaml` (or `/openapi.json`) on any Lenny gateway -- use it to generate type-safe clients for your language of choice.
