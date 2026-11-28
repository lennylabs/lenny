---
id: APICONV-I21
status: open
kind: build-gap
severity: medium
requirements: []
---

# lenny-ops cuts every POST and PUT body at 1 MiB without an error and runs the handler on an empty body after a read error

## Specification

[§25.4](../../spec/25_agent-operability.md#254-the-lenny-ops-service) (The `lenny-ops` Service), **Idempotency**: mutating endpoints (`POST`, `PUT`) accept an optional `Idempotency-Key` header. [§11.5](../../spec/11_policy-and-controls.md#115-idempotency) bounds the gateway's keyed request body and answers a larger body with `413`.

## Evidence

- `serve` in `pkg/ops/opsidem/opsidem.go` calls `bufferBody` on every POST and PUT before it reads the key, keyed or not.
- `bufferBody` reads through `io.LimitReader(r.Body, m.cfg.MaxBodyBytes)` (1 MiB by default), replaces `r.Body` with the bytes read, and drops the rest without an error. A read error sets the buffer to `nil`, so the handler reads an empty body.
- `MaxBodyBytes` has no flag in `cmd/lenny-ops/flags.go`, and `New` in `pkg/ops/opsserver/opsserver.go` does not set it.
- `decodeJSON` in `pkg/ops/opsserver/backup.go` treats an empty body (`io.EOF`) as a request with no fields, so `POST /v1/admin/backups` runs with its defaults after a read error. A body cut at 1 MiB reaches a handler such as `PUT /v1/admin/platform/config` as truncated JSON.
- The gateway's middleware answers an over-limit keyed body with `413` (`readBodyLimited` in `pkg/idempotency/idempotencymw/idempotency.go`).
- Found by the in-depth review of `pkg/ops/opsidem` against the gateway's idempotency design that decision 19 of change 0178 asked for, 2026-11-27 (finding F8), and checked against the code of `main` at `c173b3790` on the same day.

## Gap

A request over 1 MiB, or one whose body fails to read, reaches the handler altered, and a mutating handler that accepts an empty body runs an operation the client did not send.

## Suggested resolution

Buffer the body only for a keyed request or a route whose classification reads it, answer a body over the limit with `413` and a read error with `400`, as the gateway does, and make the limit a flag with its default documented. State the limit in §25.4. Add unit tests for an over-limit body and a failing reader.

Scheduling waits on the owner's answer to IP-21 on the M3 readiness decision page: whether step 0178.S6 takes the full list of findings of that review, keeps the scope that decision 19 of change 0178 approved, or splits off an urgent fix. The issue stays `open` with no `change` until that answer.
