---
id: APICONV-I15
status: open
kind: spec-defect
severity: low
requirements: []
---

# Stored idempotency responses outlive the data they describe and have no size limit

## Specification

§11.5 Idempotency stores the response of a critical operation for 24 hours. §12.8 deletes a user's idempotency records by `(tenant_id, subject)` on user erasure.

## Evidence

- A create response echoes `env`, `runtimeOptions`, and `setupOutput` (`pkg/gateway/core/session/sessionserver/session_response.go`), and its record stays replayable after the session is deleted.
- A record that `alice` stores through a `/mcp` read tool can hold data of `bob`'s session, which `bob`'s erasure does not delete, because erasure finds records by the subject that stored them ([APICONV-I14](APICONV-I14-mcp-read-tool-results-are-stored-and-replayed-without-the-tools-checks.md) removes the read tools from storage).
- The middleware stores a response with no size limit (`pkg/idempotency/idempotencymw/idempotency.go`); `MaxBodyBytes` bounds the request body only.
- Found by the security review of design C of change 0178 on 2026-11-27 (L5). It holds for every design of 0178.

## Gap

A replay can return data that a deletion or an erasure removed elsewhere, and one stored response can be as large as the largest response of a stored route.

## Suggested resolution

Delete or invalidate a session's idempotency records when the session is deleted, state in §12.8 which records an erasure of a user named in a stored response reaches, and bound the size of a stored response, answering a larger one without storing it and counting it in `lenny_idempotency_cache_skipped_total`.
