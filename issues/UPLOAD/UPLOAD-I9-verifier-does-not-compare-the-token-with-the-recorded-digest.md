---
id: UPLOAD-I9
status: open
kind: build-gap
severity: low
requirements: []
---

# The upload-token check does not compare the presented token with the digest recorded on the session row

## Specification

§7.4: an upload or finalize request must carry the `uploadToken` issued at session creation.

## Evidence

- `verifyUploadTokenValue` in `pkg/gateway/core/session/sessionserver/upload_seam.go:280-300` verifies the signature and reads the session row for the consumed mark, but never compares the presented token with `row.UploadTokenDigest`.
- The digest is recorded at creation (`create.go:515`), and the row's comment says it exists for this check (`pkg/session/sessionrow/sessionrow.go:299-304`).
- `Remint` reproduces the identical token on an idempotent replay (`pkg/uploadtoken/redact.go:93-125`), so the comparison stays compatible with replay.
- Found by the adversarial review of 0177 open decision O8 on 2026-11-26.

## Gap

Any token that verifies under a ring key for the session id is accepted, including one minted by a holder of a leaked key, rather than only the token issued at creation.

## Suggested resolution

Refuse unless the presented token's digest equals `row.UploadTokenDigest`, compared in constant time, and settle the rule for an empty digest, which only test paths write today.
