---
id: APICONV-I5
status: open
kind: build-gap
severity: medium
requirements: []
---

# A REST idempotency key replays one route's response for another route with an equal body

## Specification

§11.5 Idempotency, **Mechanism.**; change 0138 R5 plans the method, route template, and path parameters as dimensions of the scope.

## Evidence

- The REST middleware takes the key from the `Idempotency-Key` header and hashes only the request body (`pkg/idempotency/idempotencymw/idempotency.go:383`, `idempotency.HashBody(body)`).
- One key with equal bodies, often empty, on `POST /v1/sessions/A/finalize` and then `POST /v1/sessions/B/finalize` by the same caller replays A's stored response for B.
- The MCP path namespaces keys by tool (`mcpKey` in `pkg/gateway/platformtools/mcp/idempotency.go`).
- Found by the design review of the idempotency scope on 2026-11-26.

## Gap

A retry against a different session or route returns a response that belongs to another operation, and the operation for the second session never runs.

## Suggested resolution

Add the method, the route template, and the path parameters to the scope, as 0138 R5 states, ahead of 0138 if the redesign of the idempotency scope lands first.
