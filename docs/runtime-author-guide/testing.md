---
layout: default
title: "Testing"
parent: "Runtime Author Guide"
nav_order: 8
---

# Testing

Lenny provides a compliance test suite that validates your runtime against the adapter contract. This page covers the test framework, the compliance matrix by tier, local testing, and CI integration.

---

## Compliance Suite Overview

The compliance suite (`lenny-compliance`) is a standalone test harness that exercises every aspect of the adapter contract for your runtime's declared integration tier. It spawns your binary, feeds it messages on stdin, reads responses from stdout, and validates correctness.

### What It Tests

| Category | What Is Validated |
|----------|-------------------|
| **Protocol compliance** | JSON Lines format, message parsing, field presence, type correctness |
| **Message handling** | Correct response to `message`, `tool_result`, `heartbeat`, `shutdown` |
| **Forward compatibility** | Unknown message types are ignored (not rejected) |
| **Heartbeat liveness** | `heartbeat_ack` arrives within 10 seconds |
| **Shutdown behavior** | Clean exit within `deadline_ms` on `shutdown` |
| **Stdout flushing** | Output is readable immediately (not buffered) |
| **Tool call protocol** | Valid `tool_call` format, correlation with `tool_result` |
| **MCP integration** (Standard+) | Platform MCP server connection, nonce authentication, tool discovery |
| **Lifecycle channel** (Full) | Capability handshake, checkpoint, interrupt, credential rotation |

---

## Running the Compliance Suite

### Prerequisites

- Your runtime binary, built and ready to execute.
- The `lenny-compliance` binary (installed via `go install github.com/lenny-dev/lenny/cmd/lenny-compliance@latest`).

### Basic Usage

```bash
# Test a Minimum-tier runtime
lenny-compliance --binary ./my-agent --tier minimum

# Test a Standard-tier runtime
lenny-compliance --binary ./my-agent --tier standard

# Test a Full-tier runtime
lenny-compliance --binary ./my-agent --tier full
```

### Options

| Flag | Default | Description |
|------|---------|-------------|
| `--binary` | (required) | Path to your runtime binary |
| `--tier` | `minimum` | Integration tier to test: `minimum`, `standard`, `full` |
| `--timeout` | `30s` | Per-test timeout |
| `--verbose` | `false` | Show detailed test output including stdin/stdout traces |
| `--filter` | (all) | Run only tests matching this pattern |
| `--json` | `false` | Output results in JSON format (for CI) |

---

## Test Matrix by Tier

### Minimum Tier Tests

| Test | Description |
|------|-------------|
| `TestMessageEcho` | Send a `message`, verify a `response` arrives on stdout |
| `TestHeartbeatAck` | Send a `heartbeat`, verify `heartbeat_ack` within 10s |
| `TestHeartbeatTimeout` | Send a `heartbeat`, verify SIGTERM is sent if no ack within 10s |
| `TestShutdownClean` | Send `shutdown` with `deadline_ms`, verify clean exit |
| `TestShutdownDeadline` | Send `shutdown`, verify exit within `deadline_ms` |
| `TestUnknownTypeIgnored` | Send an unknown message type, verify it is silently ignored |
| `TestStdoutFlushing` | Verify output is readable immediately after write |
| `TestToolCallFormat` | Emit a `tool_call`, verify correct JSON format |
| `TestToolResultCorrelation` | Send `tool_result` matching a prior `tool_call` ID |
| `TestEmptyInput` | Send a `message` with empty `input` array |
| `TestLargeMessage` | Send a `message` exceeding 64KB to test scanner buffer |
| `TestResponseShorthand` | Verify `{"type":"response","text":"..."}` shorthand is accepted |
| `TestMultipleMessages` | Send multiple `message` payloads in sequence |
| `TestStdinClose` | Close stdin, verify the binary exits cleanly |

### Standard Tier Tests (in addition to Minimum)

| Test | Description |
|------|-------------|
| `TestManifestRead` | Verify the runtime reads `/run/lenny/adapter-manifest.json` |
| `TestMcpConnect` | Verify connection to platform MCP server via abstract Unix socket |
| `TestMcpNonce` | Verify `_lennyNonce` is presented in MCP `initialize` |
| `TestMcpToolDiscovery` | Verify `tools/list` returns platform tools |
| `TestDelegateTask` | Call `lenny/delegate_task` and verify the gateway processes it |
| `TestDiscoverAgents` | Call `lenny/discover_agents` and verify response format |
| `TestOutputTool` | Call `lenny/output` and verify parts are delivered |
| `TestRequestInput` | Call `lenny/request_input`, provide a response, verify unblock |
| `TestSendMessage` | Call `lenny/send_message` and verify delivery receipt |
| `TestMemoryWriteQuery` | Write a memory, query it, verify retrieval |
| `TestConnectorMcp` | Connect to a connector MCP server (if configured) |

### Full Tier Tests (in addition to Standard)

| Test | Description |
|------|-------------|
| `TestLifecycleConnect` | Verify connection to lifecycle channel (`@lenny-lifecycle`) |
| `TestCapabilityHandshake` | Verify `lifecycle_capabilities` / `lifecycle_support` exchange |
| `TestCheckpointCooperative` | `checkpoint_request` -> `checkpoint_ready` -> `checkpoint_complete` |
| `TestCheckpointTimeout` | Verify fallback behavior when `checkpoint_ready` is not sent in time |
| `TestInterruptRequest` | `interrupt_request` -> `interrupt_acknowledged` |
| `TestCredentialRotation` | `credentials_rotated` -> `credentials_acknowledged` |
| `TestDeadlineApproaching` | Verify `deadline_approaching` is handled without error |
| `TestTaskLifecycle` | `task_complete` -> `task_complete_acknowledged` -> `task_ready` |

---

## Local Testing

### Tier 1: Unit-Style Tests

Run the compliance suite directly against your binary:

```bash
# Build your runtime
go build -o my-agent .

# Run compliance tests
lenny-compliance --binary ./my-agent --tier minimum --verbose
```

This requires no Docker, Kubernetes, or infrastructure. The test harness spawns your binary and communicates via stdin/stdout.

### Tier 2: Integration Tests

For Standard and Full tier tests that require MCP servers:

```bash
# Start the full local stack
docker compose up -d

# Run compliance tests against the running stack
lenny-compliance --binary ./my-agent --tier standard \
  --manifest-path ./lenny-data/adapter-manifest.json
```

### Smoke Test

Both dev tiers include a built-in smoke test that validates the entire pipeline:

```bash
# Tier 1
make test-smoke

# Tier 2
docker compose run smoke-test
```

The smoke test creates a session with the echo runtime, sends a prompt, verifies a response, and exits in under 10 seconds.

---

## CI Integration

### GitHub Actions Example

```yaml
name: Runtime Compliance
on: [push, pull_request]

jobs:
  compliance:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.22'

      - name: Build runtime
        run: go build -o my-agent .

      - name: Install compliance suite
        run: go install github.com/lenny-dev/lenny/cmd/lenny-compliance@latest

      - name: Run Minimum tier tests
        run: lenny-compliance --binary ./my-agent --tier minimum --json > results.json

      - name: Upload results
        uses: actions/upload-artifact@v4
        with:
          name: compliance-results
          path: results.json
```

### Standard Tier in CI

Standard tier tests require the local stack. Use Docker Compose in CI:

```yaml
  compliance-standard:
    runs-on: ubuntu-latest
    services:
      # Use the docker-compose setup
    steps:
      - uses: actions/checkout@v4

      - name: Start local stack
        run: docker compose up -d

      - name: Wait for stack
        run: |
          for i in $(seq 1 30); do
            curl -s http://localhost:8080/healthz && break
            sleep 1
          done

      - name: Build and register runtime
        run: |
          docker build -t my-agent:dev .
          curl -X POST http://localhost:8080/v1/admin/runtimes \
            -H "Content-Type: application/json" \
            -d '{"name": "my-agent", "type": "agent", "image": "my-agent:dev"}'

      - name: Run Standard tier tests
        run: lenny-compliance --binary ./my-agent --tier standard --json > results.json
```

---

## Validation Gate

The compliance suite is designed as a **validation gate** for runtime publication. Before publishing your runtime to the community registry (see [Publishing](publishing.md)), your runtime must pass all tests at its declared tier:

```bash
# Required for publication
lenny-compliance --binary ./my-agent --tier standard --json | \
  jq '.summary.passed == .summary.total'
```

The compliance report includes:

```json
{
  "tier": "standard",
  "binary": "./my-agent",
  "summary": {
    "total": 25,
    "passed": 25,
    "failed": 0,
    "skipped": 0
  },
  "tests": [
    {
      "name": "TestMessageEcho",
      "status": "passed",
      "duration": "42ms"
    }
  ]
}
```

---

## Common Failures and Fixes

| Failure | Cause | Fix |
|---------|-------|-----|
| `TestHeartbeatAck` timeout | Heartbeat handler does heavy work before responding | Move all non-trivial work out of the heartbeat handler. Respond immediately. |
| `TestStdoutFlushing` hang | stdout is buffered and not flushed | Add explicit flush after every write. See the [Adapter Contract](adapter-contract.md) flushing table. |
| `TestUnknownTypeIgnored` failure | Runtime rejects or crashes on unknown message types | Add a `default` case in your message type switch that silently ignores unknown types. |
| `TestShutdownDeadline` timeout | Runtime does not exit within `deadline_ms` | Ensure your shutdown handler finishes work and calls `exit()` within the deadline. |
| `TestToolCallFormat` invalid JSON | Missing required fields in `tool_call` | Ensure `type`, `id`, `name`, and `arguments` are all present. |
| `TestMcpConnect` refused | Running on macOS with `make run` | Abstract Unix sockets require Linux. Use `docker compose up` instead. |
| `TestMcpNonce` rejected | Reading stale manifest | Re-read `/run/lenny/adapter-manifest.json` at startup --- nonce is regenerated per task. |
| `TestManifestRead` not found | Manifest path incorrect | Manifest is at `/run/lenny/adapter-manifest.json` (not `/workspace/`). |
| `TestCheckpointCooperative` timeout | `checkpoint_ready` not sent within deadline | Ensure your checkpoint handler quiesces state and responds within `deadlineMs`. |
| `TestResponseShorthand` rejected | Shorthand format not recognized | The shorthand `{"type":"response","text":"..."}` is normalized by the adapter --- ensure you are testing against the adapter, not directly. |

---

## Writing Your Own Tests

Beyond the compliance suite, you should write runtime-specific tests for your business logic:

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
