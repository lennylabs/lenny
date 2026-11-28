---
id: APICONV-I4
status: open
kind: build-gap
severity: low
requirements: []
---

# The idempotency middleware and the MCP hook return the store's error text in a 500 envelope

## Specification

[§11.5](../../spec/11_policy-and-controls.md#115-idempotency) (Idempotency) states the idempotency mechanism and its refusals. [§15.1](../../spec/15_external-api-surface.md#151-rest-api) defines `INTERNAL_ERROR` as the error a client receives for a server-side failure. Neither states that a client receives the text of a storage error.

## Evidence

- `pkg/idempotency/idempotencymw/idempotency.go`: when the store's `Claim` or `Get` fails, the middleware answers `writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)`, so the response message is the Postgres driver's error text, such as a failed `ON CONFLICT` target or a connection error.
- `pkg/gateway/platformtools/mcp/idempotency.go`: the MCP hook answers `NewToolError("INTERNAL_ERROR", "idempotency claim: "+err.Error(), nil)`.
- Both predate change 0178. The security review of step 0178.S3 found them while checking the upgrade window of the scope-digest migration (0208, numbered 0207 when this issue was filed), during which a replica on the earlier version fails every keyed request this way.

## Gap

A client receives internal storage error text, which can name tables, columns, constraints, and hosts, in the message of a `500 INTERNAL_ERROR` response.

## Suggested resolution

Log the store error with the tenant and the key's scope, and answer the client with a fixed message for `INTERNAL_ERROR` on both paths. Add a unit test that a failing store yields the fixed message.
