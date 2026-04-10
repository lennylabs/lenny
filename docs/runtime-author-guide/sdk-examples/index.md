---
layout: default
title: "Runtime SDK Examples"
parent: "Runtime Author Guide"
nav_order: 10
has_children: true
---

# Runtime SDK Examples

This section provides complete, runnable runtime implementations in Go, Python, and TypeScript. Each example implements the same agent: a **file summarizer** that reads workspace files and produces summaries using an LLM.

---

## The File Summarizer Runtime

All three examples implement the same behavior:

1. **Receive a message** asking to summarize workspace files.
2. **Read files** from the workspace using the `read_file` adapter-local tool.
3. **Produce a summary** as a `response` on stdout.
4. **Handle heartbeats** by immediately writing `heartbeat_ack`.
5. **Handle shutdown** by exiting cleanly.

The examples start at Minimum tier (stdin/stdout only) and include guidance for upgrading to Standard tier (adding MCP tools).

---

## Language Choice Guidance

| Language | Best For | Minimum Tier Complexity | Standard Tier Complexity |
|----------|----------|------------------------|-------------------------|
| **Go** | Systems programming, high-performance runtimes, production agents | ~100 lines | ~200 lines + `mcp-go` dependency |
| **Python** | Rapid prototyping, ML/AI integrations, wrapping existing frameworks | ~80 lines | ~150 lines + `mcp` dependency |
| **TypeScript** | Web-oriented agents, Node.js ecosystem integrations | ~90 lines | ~180 lines + `@modelcontextprotocol/sdk` dependency |

All three languages work equally well with Lenny. The adapter contract is language-agnostic --- if your language can read lines from stdin and write lines to stdout, it works.

---

## Tier Coverage

Each example covers:

### Minimum Tier (Complete)
- Message handling with file reading via `tool_call`/`tool_result`
- Heartbeat/shutdown handling
- Proper stdout flushing
- Complete `go.mod`/`requirements.txt`/`package.json`
- Multi-stage Dockerfile
- Build and run instructions

### Standard Tier (Upgrade Guide)
- Reading the adapter manifest
- Connecting to the platform MCP server
- Using `lenny/output` for incremental streaming
- Using `lenny/delegate_task` for subtask delegation

---

## Quick Links

- **[Go Runtime SDK](go.md)** --- Complete Go implementation with commentary
- **[Python Runtime SDK](python.md)** --- Complete Python implementation with commentary
- **[TypeScript Runtime SDK](typescript.md)** --- Complete TypeScript implementation with commentary

---

## Running the Examples

All examples can be run locally with zero dependencies beyond the language toolchain:

```bash
# Go
cd examples/runtimes/file-summarizer-go
go build -o file-summarizer .
make run LENNY_AGENT_BINARY=./file-summarizer

# Python
cd examples/runtimes/file-summarizer-python
make run LENNY_AGENT_BINARY="python -u main.py"

# TypeScript
cd examples/runtimes/file-summarizer-ts
npm run build
make run LENNY_AGENT_BINARY="node dist/main.js"
```

For Standard tier features (MCP tools, delegation), use `docker compose up` instead of `make run`, since abstract Unix sockets require Linux.
