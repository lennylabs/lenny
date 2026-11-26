---
id: APICONV-I2
status: open
kind: build-gap
severity: low
requirements: []
---

# The MCP idempotency hook refuses an idempotencyKey of fewer than 128 characters because the cap counts the tool prefix

## Specification

[§11.5](../../spec/11_policy-and-controls.md#115-idempotency) (Idempotency), **Mechanism.**: "The key is an opaque string of at most 128 characters". The `lenny/create_session` and `lenny/delegate_task` input schemas declare `idempotencyKey` with `maxLength: 128` (`pkg/gateway/platformtools/mcptools/generated_schemas.go`, `pkg/gateway/platformtools/mcptools/mcptools_register.go`).

## Evidence

- `pkg/gateway/platformtools/mcp/idempotency.go`: `dispatchIdempotent` sets the record key to `mcpKey(tool, callerKey)`, which returns `"mcp/" + tool + "/" + caller`, and then calls `Validate` on it.
- `pkg/idempotency/idempotency.go`: `Key.Validate` compares the rune count of `Value` with `MaxKeyLength` (128).
- For `lenny/create_session` the prefix `mcp/lenny/create_session/` is 25 characters, so a caller key of 104 to 128 characters is refused with `INVALID_IDEMPOTENCY_KEY`. No test covers a long MCP key.
- The Postgres store's `Put` and `Claim` apply the same `Validate`, so the fix cannot be confined to the hook.

Found by the review of steps 0178.S1 and 0178.S2. The behavior predates change 0178.

## Gap

A client that sends an `idempotencyKey` the schema and §11.5 admit, of 104 to 128 characters, receives `INVALID_IDEMPOTENCY_KEY` on the MCP path.

## Suggested resolution

Validate the caller's key against the 128-character cap at the request boundary, and keep the namespace out of the capped value: store the tool name in its own part of the record key, or exempt the namespace prefix from the store's length check. Add a unit test with a 128-character MCP key.
