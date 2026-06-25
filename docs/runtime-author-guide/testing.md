---
layout: default
title: "Testing"
parent: "Runtime Author Guide"
nav_order: 8
---

# Testing

`lenny runtime validate` is the conformance test suite that checks a runtime against the adapter contract. The suite is organized around integration levels (Basic, Standard, or Full), which apply to `type: agent` runtimes. A `type: mcp` runtime has no integration level; see [`type: mcp` runtimes](#type-mcp-runtimes) at the end of this page.

This page covers the conformance suite, the test categories per integration level, local testing, and CI integration. For the full message contract the suite asserts against, see the [Adapter Contract](../reference/adapter-contract.md).

---

## Conformance Suite Overview

`lenny runtime validate` exercises the adapter contract for your runtime's declared integration level. It reads the `integrationLevel` field from your `runtime.yaml` (defaulting to `basic` when the field is absent), builds your image or binary, feeds it messages on stdin, reads responses on stdout, and connects the platform-side fixtures the higher levels need. The fixtures (a fake adapter, a fake platform MCP server, fake connector servers, and sample manifests) ship inside the `lenny` binary, so no separate download or install is required.

### What it checks

| Category | What is validated |
|----------|-------------------|
| **Protocol framing** | JSON Lines format, message parsing, field presence, type correctness, stdout flushing |
| **Message handling** | Correct response to `message`, `tool_result`, `heartbeat`, `shutdown` |
| **Forward compatibility** | Unknown message types are ignored rather than rejected |
| **Heartbeat liveness** | `heartbeat_ack` arrives within 10 seconds |
| **Shutdown behavior** | Clean exit within `deadline_ms` on `shutdown` |
| **Schema compliance** | Every emitted frame and every `MessagePart` validates against the published JSON Schemas |
| **MCP integration** (Standard and Full) | Platform MCP server connection, nonce authentication, tool invocation, connector reachability |
| **Lifecycle channel** (Full) | Capability handshake, checkpoint, interrupt, credential rotation, deadline signal |

The validator also reconciles the level your runtime declares against the level it actually demonstrates. See [Declared versus observed level](#declared-versus-observed-level).

---

## Running the Conformance Suite

### Prerequisites

- The `lenny` binary, which carries the conformance fixtures and the `lenny runtime validate` subcommand.
- A runtime repository with a `runtime.yaml` that declares the `integrationLevel` you intend to validate, and a buildable image or binary.

### Usage

Run the validator from your runtime repository. It reads the declared level from `runtime.yaml` and runs the categories for that level:

```bash
# Validate against the level declared in runtime.yaml
lenny runtime validate

# Validate a repository at another path
lenny runtime validate ./my-agent
```

The command exits `0` on a full pass and non-zero with a structured failure report otherwise.

To emit a machine-readable JSON report for CI or for inclusion in release artifacts, pass `--report`:

```bash
lenny runtime validate --report results.json
```

To stabilize the conformance surface across releases, pin a specific `lenny` version. Each release pins the fixture version its `lenny runtime validate` runs against.

---

## Test categories by integration level

The validator runs a set of test categories for the declared level. Each higher level inherits every category from the levels below it: Standard runs the Basic categories plus its own, and Full runs the Standard categories plus its own. The tables below name the categories at each level.

### Basic-level categories

| Category | What it asserts |
|----------|-----------------|
| stdin/stdout protocol framing | The binary reads newline-delimited JSON on stdin and writes newline-delimited JSON on stdout, flushes every outbound message before the next read, and ignores unknown inbound `type` values rather than aborting. |
| `message` / `response` round-trip | A canonical `message` produces a structurally valid `response`, either the full form with an `output` array of `MessagePart` or the Basic-level shorthand `{"type":"response","text":"..."}`. The response validates against the published JSON Lines schema. |
| heartbeat ack | Within 10 seconds of receiving a `heartbeat`, the binary writes a `heartbeat_ack`. Missing the window triggers the adapter's unresponsive-agent escalation. |
| shutdown within `deadline_ms` | On `shutdown` with a `deadline_ms`, the binary exits cleanly before the deadline elapses. Failing this means the adapter SIGKILLs the process in production, losing unflushed output. |
| `MessagePart` schema compliance | Every `MessagePart` the runtime produces validates against the published `MessagePart` schema, including the canonical type registry and the `x-<vendor>/` namespace convention for custom types. |

### Standard-level categories (in addition to Basic)

| Category | What it asserts |
|----------|-----------------|
| MCP nonce handshake | On startup the runtime reads `/run/lenny/adapter-manifest.json`, connects to the platform MCP server, and presents `_lennyNonce` in the `initialize` params. The fixture's MCP server rejects any tool call without a valid nonce to verify enforcement. |
| platform MCP tool invocation | The runtime calls at least `lenny/output` and `lenny/request_input` through the MCP client, and the responses are processed correctly. |
| connector MCP server reachability | When `connectorServers` in the manifest is non-empty, the runtime connects to each with the same nonce and completes the `initialize` handshake. |
| `tool_call` / `tool_result` correlation | Each adapter-local `tool_call` carries a unique `id`, and the matching `tool_result` is read from stdin before the runtime emits its final `response`. |

### Full-level categories (in addition to Standard)

| Category | What it asserts |
|----------|-----------------|
| lifecycle channel opening | The runtime connects to the lifecycle channel named in the manifest (`@lenny-lifecycle`) and completes the `lifecycle_capabilities` / `lifecycle_support` exchange. |
| checkpoint quiesce/resume | On `checkpoint_request`, the runtime quiesces output, replies with `checkpoint_ready`, waits for `checkpoint_complete`, and resumes. |
| interrupt acknowledgement | On `interrupt_request`, the runtime reaches a safe stop point and replies with `interrupt_acknowledged` carrying the original `interruptId` within the deadline. |
| credential rotation handling | A runtime that declares `credential_rotation` support re-reads refreshed credentials on `credentials_rotated` and services the next message without a restart. |
| deadline signal handling | On `deadline_signal`, the runtime writes a final `response` (optionally carrying `error.code: "DEADLINE_EXCEEDED"`) and exits cleanly before the deadline elapses. |

Each failure is classified as `schema_violation`, `timeout`, `missing_capability`, or `unexpected_error`, and the report lists the failing category with its classification and a reproduction command.

---

## Declared versus observed level

Alongside running the declared level's categories, the validator probes the running runtime to determine the level it actually demonstrates, then compares the two:

- It starts the runtime with the full Full-level fixture set available: the lifecycle channel listening, the platform MCP server and connector fixtures reachable, and the manifest written.
- If the runtime completes the `lifecycle_capabilities` / `lifecycle_support` exchange on `@lenny-lifecycle` within the grace window, the observed level is at least Full.
- Otherwise, if it connects to the platform MCP server and presents a valid `_lennyNonce` during `initialize`, the observed level is at least Standard.
- Otherwise the observed level is Basic.

The outcome determines the exit behavior:

| Comparison | Exit | Meaning |
|------------|------|---------|
| Observed equals declared | `0` | The runtime meets the level it claims. |
| Observed above declared | `0` with a WARN | The runtime under-declared. Raise `integrationLevel` in `runtime.yaml` so callers and admission can rely on the higher level. |
| Observed below declared | non-zero | The runtime does not meet the level it published. The report names the missing capabilities, and the missing level's categories are reported as failed. |

---

## Local Testing

The conformance suite carries its own fixtures, so `lenny runtime validate` runs against your runtime without a separate platform stack. At every level it builds your image or binary, spawns it, and connects the fake platform-side servers from inside the `lenny` binary:

```bash
# Build, then validate against the declared level
lenny runtime validate
```

This needs no Docker, Kubernetes, or external infrastructure for any level. The Standard and Full categories connect to the fake MCP server and lifecycle channel the validator hosts itself.

Use a local development mode when you want to exercise your runtime end-to-end against the platform rather than against the conformance fixtures. The recommended path is `lenny up`, which runs the whole platform from one binary and starts your runtime in a real Kubernetes pod. `make run` and `docker compose up` give tighter loops in specific cases. For the differences and when to use each, see [Local Development](local-development.md).

The Standard and Full categories rely on Linux abstract Unix sockets for the platform MCP server and the lifecycle channel. When you exercise a runtime end-to-end on macOS or Windows, `make run` cannot host those sockets because it runs your runtime as a plain host process, so use `lenny up` or `docker compose up`, which provide a Linux environment.

### Smoke test

`lenny up`, `make run`, and `docker compose up` each include a built-in smoke test that exercises the whole pipeline:

```bash
# With `make run`
make test-smoke

# With `docker compose up`
docker compose run smoke-test
```

The smoke test creates a session with the echo runtime, sends a prompt, verifies a response, and exits in under 10 seconds.

---

## CI Integration

Because the conformance fixtures ship inside the `lenny` binary, CI runs `lenny runtime validate` directly. No platform stack is needed, and the same job covers Basic, Standard, or Full according to the `integrationLevel` your `runtime.yaml` declares. Install the `lenny` binary, then run the validator from the runtime repository.

### GitHub Actions example

```yaml
name: Runtime Conformance
on: [push, pull_request]

jobs:
  conformance:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Install lenny
        run: go install github.com/lennylabs/lenny/cmd/lenny-ctl@latest

      - name: Validate runtime
        run: lenny runtime validate --report results.json

      - name: Upload results
        uses: actions/upload-artifact@v4
        with:
          name: conformance-results
          path: results.json
```

`lenny runtime validate` runs on Linux, where the abstract Unix sockets the Standard and Full categories use are available. Run conformance CI on a Linux runner regardless of the level you target.

---

## Validation Gate

`lenny runtime validate` is the gate for runtime publication. Before publishing your runtime to the community registry (see [Publishing](publishing.md)), it must pass every category at its declared integration level. The exit code is the gate: `0` means a full pass, and non-zero means at least one category failed or the observed level is below the declared level.

```bash
# Required for publication; non-zero exit blocks the gate
lenny runtime validate --report results.json
```

The `--report` file records the declared-versus-observed reconciliation and the per-category result. The `integrationLevel` block reports `match` when the levels agree and `underdeclared` when the runtime demonstrates more than it claims. When the runtime falls short of its claim, the validator exits non-zero with a `runtime_level_underperforms` error that names the missing capabilities.

```json
{
  "integrationLevel": {
    "declared": "standard",
    "observed": "standard",
    "status": "match"
  }
}
```

When the registry CI validates a submission, it runs the same `lenny runtime validate` at your declared level and compares the report against your submission.

---

## Common Failures and Fixes

| Failure | Cause | Fix |
|---------|-------|-----|
| Heartbeat ack times out | Heartbeat handler does heavy work before responding | Move all non-trivial work out of the heartbeat handler. Respond immediately. |
| Output never arrives or the session hangs | stdout is buffered and not flushed | Add an explicit flush after every write. See the [Adapter Contract](../reference/adapter-contract.md) flushing guidance. |
| Forward-compatibility category fails | Runtime rejects or crashes on unknown message types | Add a `default` case in your message-type switch that silently ignores unknown types. |
| Shutdown exceeds `deadline_ms` | Runtime does not exit within the deadline | Ensure your shutdown handler finishes work and exits within `deadline_ms`. |
| `tool_call` fails schema validation | Missing required fields in `tool_call` | Ensure `type`, `id`, `name`, and `arguments` are all present. |
| MCP connection refused on macOS or Windows | A runtime run under `make run` reaches the platform MCP server over a Linux abstract Unix socket, which the host process cannot use off Linux | Validate with `lenny runtime validate`, which carries its own fixtures, or run the runtime end-to-end with `lenny up` or `docker compose up`. |
| MCP nonce rejected | The runtime cached a stale manifest | Re-read `/run/lenny/adapter-manifest.json` at startup. The nonce is regenerated per session. |
| Manifest not found | Manifest path incorrect | The manifest is at `/run/lenny/adapter-manifest.json`, not under `/workspace/`. |
| Checkpoint quiesce times out | `checkpoint_ready` not sent within the deadline | Ensure your checkpoint handler quiesces state and replies with `checkpoint_ready` within `deadlineMs`. |

---

## Writing Your Own Tests

The conformance suite checks the adapter contract. Add runtime-specific tests for your own business logic on top of it.

### Testing Message Handling

```go
func TestMyRuntime_ProcessesInput(t *testing.T) {
    // Start your runtime as a subprocess
    cmd := exec.Command("./my-agent")
    stdin, _ := cmd.StdinPipe()
    stdout, _ := cmd.StdoutPipe()
    cmd.Start()
    defer cmd.Process.Kill()

    // Send a message
    msg := `{"type":"message","id":"msg_001","input":[{"type":"text","inline":"Hello"}]}` + "\n"
    stdin.Write([]byte(msg))

    // Read response
    scanner := bufio.NewScanner(stdout)
    scanner.Scan()
    var resp map[string]interface{}
    json.Unmarshal(scanner.Bytes(), &resp)

    // Validate
    if resp["type"] != "response" {
        t.Errorf("expected response, got %s", resp["type"])
    }
}
```

### Testing Heartbeat

```go
func TestMyRuntime_RespondsToHeartbeat(t *testing.T) {
    cmd := exec.Command("./my-agent")
    stdin, _ := cmd.StdinPipe()
    stdout, _ := cmd.StdoutPipe()
    cmd.Start()
    defer cmd.Process.Kill()

    // Send heartbeat
    stdin.Write([]byte(`{"type":"heartbeat","ts":1234567890}` + "\n"))

    // Read response with timeout
    scanner := bufio.NewScanner(stdout)
    done := make(chan bool)
    go func() {
        scanner.Scan()
        done <- true
    }()

    select {
    case <-done:
        var resp map[string]interface{}
        json.Unmarshal(scanner.Bytes(), &resp)
        if resp["type"] != "heartbeat_ack" {
            t.Errorf("expected heartbeat_ack, got %s", resp["type"])
        }
    case <-time.After(10 * time.Second):
        t.Fatal("heartbeat_ack not received within 10 seconds")
    }
}
```

---

## `type: mcp` runtimes

The integration levels and the agent test categories on this page apply to `type: agent` runtimes. A `type: mcp` runtime hosts an MCP server behind the platform and does not participate in the stdin/stdout protocol, the platform tool client, or the lifecycle channel, so the Basic, Standard, and Full categories do not apply to it. For how `type: mcp` runtimes differ, see [Integration Levels](integration-levels.md#type-mcp-runtimes).

Test a `type: mcp` runtime the way you test any MCP server: exercise its `initialize`, `tools/list`, and `tools/call` handlers directly with an MCP client, and add your own functional tests for the tools it exposes.
