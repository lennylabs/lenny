---
id: UPLOAD-I19
status: planned
kind: build-gap
severity: low
requirements: []
change: 0182.S1
---

# The finalize audit event and the idempotency 422 details carry material derived from the upload credential

## Specification

§7.1 **`uploadToken` format and security properties.**: clients treat the token as a secret credential that is not logged. Under change 0182 the client chooses the secret and the gateway stores its SHA-256 digest.

## Evidence

- `pkg/gateway/core/session/sessionserver/finalize_handler.go` writes the session row's `UploadTokenDigest` as the detail of the audit event `session.finalize_workspace` (the constant `auditSessionWorkspaceFinalized` in `lifecycle.go`). Its comment cites §16.6, which is the operational events catalog and does not list the event; no section of the specification lists it, and §16.3 names `session.finalize_workspace` only as a tracing span. The comment states that the digest lets analysts correlate the audit row with the rejected uploads that follow it.
- The `422 IDEMPOTENCY_KEY_REUSED` details carry `storedHash`, the hash of the stored request body (`pkg/idempotency/idempotencymw/idempotency.go`); under change 0182 that body contains the upload digest.
- Found by the security review of design C of change 0178 on 2026-11-27 (L3).

## Gap

While the gateway mints the token, the digest of a 256-bit HMAC value gives an audit reader nothing to guess. Once the client chooses the secret, the gateway cannot check its entropy, and the digest in the audit log and in the `422` details is offline guessing material against a weak or reused secret.

## Suggested resolution

Drop the digest from the `session.finalize_workspace` detail and `storedHash` from the `422` details. The row keeps `session_id`, which identifies the same session the digest did, so the correlation with the session's uploads runs on `session_id`, which the `session.upload` audit rows carry. State in §7.1 that the finalize audit row carries no material derived from the upload credential, and correct the code comment's citation.

Change 0182 does this in step 0182.S1 (US-3 of 0182). The issue stays `open` until the owner answers O1 of change 0182, the choice of design; under an answer that keeps a gateway-minted token, the digest of a 256-bit value gives a reader nothing to guess, and the issue is reassessed.

The owner chose design C on 2026-11-27 (decision 1 of change 0182, UC-1 (a)). Step 0182.S1 drops the digest from the audit detail and `storedHash` from the `422` details and closes this issue.
