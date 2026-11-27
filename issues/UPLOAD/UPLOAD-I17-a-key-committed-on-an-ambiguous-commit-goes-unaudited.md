---
id: UPLOAD-I17
status: open
kind: build-gap
severity: low
requirements: []
---

# A forced rotation whose commit succeeds on the server but fails on the client writes a key without an audit event

## Specification

The §15.1 row of `POST /v1/admin/upload-token-keys/rotate`: "The rotation runs to completion when the client disconnects, so a key it writes is always audited." `docs/api/admin.md` states the same.

## Evidence

- `pkg/signingkeys/pgstore/pgstore.go`, `Insert`: the insert runs in `pgx.BeginFunc`; a connection reset during `COMMIT` returns an error to the gateway although the server may have committed the row.
- `pkg/signingkeys/ring.go`, `RotateNow`: an error from `writeKey` is returned as is.
- `pkg/gateway/api/admin/upload_token_keys.go`: an error answers `503 SERVICE_UNAVAILABLE` and emits no `platform.upload_token_key_rotated` event.
- Since decision 13 of change 0177, the retry that the `503` invites answers `409 INVALID_STATE_TRANSITION` naming the unaudited key, which an operator reads as another caller's rotation.
- Found by the security review of decision 13 of change 0177 on 2026-11-27. The gap predates decision 13.

## Gap

A key the gateway wrote can lack its audit event when the commit outcome is ambiguous to the client.

## Suggested resolution

When `Insert` returns an error, `RotateNow` lists the purpose's keys again and, when the row of the key it tried to write exists, returns that key as written, so the route audits it.
