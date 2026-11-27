---
id: UPLOAD-I8
status: open
kind: build-gap
severity: low
requirements: []
---

# POST /v1/sessions/start never marks its upload token consumed

## Specification

§7.1 **`uploadToken` format and security properties**, single-use invalidation: the token is invalidated when the session leaves the upload phase.

## Evidence

- `pkg/gateway/core/session/sessionserver/create_and_start.go:309` writes the session in `session.StateRunning` directly.
- `UploadTokenConsumedAt` is set only in `transitionReady` (`finalize_handler.go:36`), which the create-and-start path does not run.
- No test covers consumption on this path.
- Found by the adversarial review of 0177 open decision O8 on 2026-11-26.

## Gap

The token of a session created through `POST /v1/sessions/start` stays unconsumed for its TTL. With mid-session upload enabled, the upload endpoint accepts it instead of answering 410, which interacts with UPLOAD-I4.

## Suggested resolution

Set `upload_token_consumed_at` in the write that commits a create-and-start session to `running`, and add a test that the token is refused after start.
