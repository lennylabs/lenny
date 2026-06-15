---
layout: default
title: "Runtime SDK Examples"
parent: "Runtime Author Guide"
nav_order: 10
has_children: true
---

# Runtime SDK Examples

This section provides complete, runnable `type: agent` runtime implementations in Go, Python, and TypeScript. Each example implements the same runtime, a file summarizer that reads workspace files and returns their contents in a single summary response. The examples speak the adapter binary protocol directly, so they show the protocol the [runtime SDKs](../runtime-sdk.md) wrap. The integration-level model these pages follow applies to `type: agent` runtimes; `type: mcp` runtimes host an MCP server and do not use the stdin/stdout contract, as described in [Integration Levels](../integration-levels.md).

Read the pages in the order Go, Python, and TypeScript. Each is self-contained, so a reader who wants one language can read that page alone.

---

## The file summarizer runtime

All three examples implement the same behavior:

1. **Receive a message** asking to summarize workspace files.
2. **Read files** from the workspace using the `list_dir` and `read_file` adapter-local tools.
3. **Produce a summary** as a `response` on stdout. The examples concatenate truncated file contents; a production summarizer would pass the contents to an LLM.
4. **Handle heartbeats** by immediately writing `heartbeat_ack`.
5. **Handle shutdown** by exiting cleanly.

The examples start at the Basic integration level (stdin/stdout only) and include guidance for upgrading to Standard, which adds the platform MCP tools.

---

## Language choice

| Language | Suited to | Basic level | Standard level |
|----------|-----------|-------------|----------------|
| **Go** | Systems programming, high-performance runtimes, production agents | around 100 lines, standard library only | adds the `mcp-go` dependency |
| **Python** | Rapid prototyping, ML and AI integrations, wrapping existing frameworks | around 80 lines, standard library only | adds the `mcp` dependency |
| **TypeScript** | Web-oriented agents, Node.js ecosystem integrations | around 90 lines, standard library only | adds the `@modelcontextprotocol/sdk` dependency |

The adapter contract is language-agnostic. Any language that can read lines from stdin and write lines to stdout can implement a runtime, so the line counts above are approximate and reflect the example code rather than a requirement.

---

## What Each Example Covers

Each example covers:

### Basic level (complete)
- Message handling with file reading via `tool_call`/`tool_result`
- Heartbeat/shutdown handling
- Proper stdout flushing
- Complete `go.mod`/`requirements.txt`/`package.json`
- Multi-stage Dockerfile
- Build and run instructions

### Standard level (upgrade guide)
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

For Standard-level features (MCP tools, delegation), use `docker compose up` instead of `make run`, since abstract Unix sockets require Linux.
