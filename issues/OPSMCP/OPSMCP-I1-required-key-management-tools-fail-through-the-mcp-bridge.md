---
id: OPSMCP-I1
status: open
kind: build-gap
severity: medium
requirements: []
---

# The management MCP tools whose REST call requires an idempotency key always fail at Tier 2 and Tier 3

## Specification

[§25.4](../../spec/25_agent-operability.md#254-the-lenny-ops-service) (The `lenny-ops` Service), **Idempotency**: at Tier 2/3, `POST /v1/admin/platform/upgrade/start`, `POST /v1/admin/restore/execute`, and `POST /v1/admin/backups` for `type: "full"` return `400 IDEMPOTENCY_KEY_REQUIRED` when `Idempotency-Key` is omitted. [§25.12](../../spec/25_agent-operability.md#2512-mcp-management-server) (MCP Management Server › Tool Schema Details): each tool carries `x-lenny-idempotency-key`, `"required"`, `"recommended"`, or `"ignored"`; a tool invocation is translated into a REST call that passes the standard middleware. [§11.5](../../spec/11_policy-and-controls.md#115-idempotency) **Mechanism.** takes the gateway's MCP idempotency key from the `idempotencyKey` field of the tool input.

## Evidence

- `invokeLocal` in `pkg/ops/opsserver/mcp.go` replays an ops-owned tool as a request built with `httptest.NewRequest` and sets `Content-Type`, `X-Lenny-Caller`, and the correlation headers. It never sets `Idempotency-Key`, and `buildToolRequest` puts every argument that is not a path parameter into the JSON body, so no tool argument reaches the header.
- The replay target `s.mcpReplay` is `requireAdminRole(s.scopeEnforce(mux))`, where `mux` is the `opsidem`-wrapped mux (`withOpsAuth` in `pkg/ops/opsserver/auth.go`). With `IdempotencyKeyRequired`, which `--platform-tier` `tier2` and `tier3` select (`cmd/lenny-ops/flags.go`), `classify` in `pkg/ops/opsidem/classify.go` marks the replayed calls of `lenny_platform_upgrade_start`, `lenny_restore_execute`, and `lenny_backup_create` with `type: "full"` as required, and `opsidem` answers `400 IDEMPOTENCY_KEY_REQUIRED` before the handler runs, a dry-run preview included.
- `toolCallResult` in `pkg/ops/mcp/server.go` returns a status of 400 or more as `isError: true` inside an HTTP 200 JSON-RPC response. When the outer `POST /mcp/management` carried an `Idempotency-Key`, `opsidem` stores that 200 and replays the tool error for 24 hours.
- `toolDescriptor` in `pkg/ops/mcp/server.go` emits `x-lenny-category`, `x-lenny-required-role`, `x-lenny-scope`, and `x-lenny-dry-run-support`. `mcp.Tool` (`pkg/ops/mcp/tools.go`) has no field for `x-lenny-idempotency-key`, and the generated tools (`pkg/ops/mcp/generated_tools.go`) declare no idempotency key in their input schemas.
- The tests that set `IdempotencyKeyRequired: true` (`tests/contract/ops_endpoints/upgrade_test.go`, `tests/contract/ops_endpoints/upgrade_idempotency_test.go`, `pkg/ops/opsserver/idempotency_test.go`, and `tests/component/opsidem/outage_test.go`) call the REST routes only.
- Found by the in-depth review of `pkg/ops/opsidem` against the gateway's idempotency design that decision 19 of change 0178 asked for, 2026-11-27 (finding F3), and checked against the code of `main` at `c173b3790` on the same day.

## Gap

At Tier 2 and Tier 3 an agent that uses the management MCP server cannot start an upgrade, execute a restore, or take a full backup, and a keyed `tools/call` keeps returning the stored failure for 24 hours. An agent cannot learn from `tools/list` which tools need a key.

## Suggested resolution

Specify in §25.12 an `idempotencyKey` tool argument, as §11.5 does for the gateway's MCP tools, which the bridge removes from the body and forwards as `Idempotency-Key` on the replayed REST call, and emit `x-lenny-idempotency-key` for each tool from the §25.4 classification. Stop storing `/mcp/management` at the HTTP layer, so a tool result is stored only through the mapped REST call. Add a contract test that runs each required-key tool through `/mcp/management` with `IdempotencyKeyRequired: true`, with and without the argument.

Scheduling waits on the owner's answer to IP-21 on the M3 readiness decision page: whether step 0178.S6 takes the full list of findings of that review, keeps the scope that decision 19 of change 0178 approved, or splits off an urgent fix. The issue stays `open` with no `change` until that answer.
