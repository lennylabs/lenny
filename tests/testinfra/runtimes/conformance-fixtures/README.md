# Conformance fixtures

§11 of TESTING.md names this directory: runtime adapters that
**intentionally violate** the conformance contract. The §12.10
conformance harness runs each fixture against `lenny-compliance` and
asserts the documented diagnostic.

Each fixture is a tiny Go program under `<name>/main.go` that
follows the adapter binary protocol up to a deliberate breakage:

| Fixture | Violation | Expected diagnostic |
|:--------|:----------|:--------------------|
| `missing-heartbeat-ack` | Receives `heartbeat` from the gateway but never replies with `heartbeat_ack`. | `ADAPTER_HEARTBEAT_TIMEOUT` after 10s |
| `malformed-jsonl` | Emits a JSON Lines record that is not valid JSON (trailing comma). | `ADAPTER_PROTOCOL_VIOLATION` |
| `unknown-message-type` | Emits an `unknown` top-level message type. | Adapter must ignore; compliance asserts no protocol error |
| `oversize-payload` | Sends a payload above the documented size cap. | `ADAPTER_PAYLOAD_TOO_LARGE` |
| `blocked-stdin` | Spawns a goroutine that consumes stdin but never returns. | `ADAPTER_HANDSHAKE_TIMEOUT` |
| `late-shutdown` | Receives `shutdown` but takes longer than the deadline to exit. | `ADAPTER_SHUTDOWN_TIMEOUT` |

Build all fixtures:

```bash
go build -o ./bin/ ./tests/testinfra/runtimes/conformance-fixtures/...
```

Drive a fixture through compliance:

```bash
lenny-compliance --image file://./bin/missing-heartbeat-ack --level basic
```
