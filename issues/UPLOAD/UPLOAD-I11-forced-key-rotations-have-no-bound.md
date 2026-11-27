---
id: UPLOAD-I11
status: planned
kind: question
severity: low
requirements: []
change: 0177.S4
---

# Forced upload-token key rotations have no bound, so a looping caller can stop minting on every replica

## Specification

§7.1 (Normal Flow), **`uploadToken` format and security properties.**, **Forced rotation**, and the §15.1 row of `POST /v1/admin/upload-token-keys/rotate`: the route writes the next key at once, whatever the age of the active key. Neither states a limit on how often it may be called.

## Evidence

- `pkg/signingkeys/ring.go`, `RotateNow`: each call writes one row of `platform_signing_keys`, which lives for the publish lead plus the verification time (about 12 minutes at the defaults).
- `pkg/signingkeys/ring.go`, `reload`: each replica unwraps every key it does not yet hold through the KMS on its next refresh, and returns on the first unwrap failure without moving `lastReload`.
- When the KMS throttles those unwraps, reloads fail on every replica; after the stale bound (the overlap window, five minutes) `SigningKey` returns `ErrSigningUnavailable` and session creation answers `503 SESSION_CREATION_FAILED` on every replica.
- `pkg/ops/mcp/mcptoolgen/mcptoolgen.go` classifies `admin.rotate_upload_token_keys` as `categoryMutation`, so an operability agent can call it in a loop without a destructive-action confirmation.
- Found by the security review of steps 0177.S3 and 0177.S4 on 2026-11-26.

## Gap

The route is `platform-admin` only, so the caller is trusted, but a runaway caller (an agent loop, a script) can stop upload-token minting fleet-wide for as long as the loop runs plus the stale bound.

## Suggested resolution

The owner chooses a bound. Options:

- (a) Refuse a forced rotation with `409` while the newest key is a forced rotation's key that is not active yet (at most one forced rotation per publish lead). The incident procedure's two calls then run one publish lead apart.
- (b) A per-gateway rate limit on the route.
- (c) Classify the MCP tool as destructive, so an agent needs confirmation.
- (d) Document the cost and add no bound.

Recommendation: (a), because it bounds the key count by construction and matches the publish lead.

The owner chose (a) on 2026-11-27 (decision 13 of change 0177, answer to UK-9). Step 0177.S4 builds it.
