---
id: UPLOAD-I1
status: planned
kind: build-gap
severity: high
requirements: []
change: 0177.S1
---

# The upload-token signing keys and the consumed-token record are per gateway replica, so a token works only on the replica that minted it

## Specification

[§7.1](../../spec/07_session-lifecycle.md#71-normal-flow) (Normal Flow), **`uploadToken` format and security properties.**: the token is signed "under a gateway-held signing key", and:

- "**Single-use invalidation:** The token is invalidated immediately upon successful `FinalizeWorkspace`. Any subsequent attempt to use the same token returns `410 UPLOAD_TOKEN_CONSUMED`."
- "**Cryptographic protection:** The HMAC prevents forgery of arbitrary `(session_id, expiry)` pairs. Signing keys are rotated on a deployer-configurable schedule (default: 24 hours); the gateway keeps the previous key valid during a short overlap window (default: 5 minutes) to avoid rejecting tokens issued just before rotation."

[§7.4](../../spec/07_session-lifecycle.md#74-upload-safety) (Upload Safety), **Upload authorization:**: "The gateway validates the token's HMAC signature, checks that it has not expired, confirms the embedded `session_id` matches the target session, and rejects any token that has already been consumed by a prior `FinalizeWorkspace` call."

[§10.1](../../spec/10_gateway-internals.md#101-horizontal-scaling) (Horizontal Scaling): "Gateway replicas are stateless proxies over externalized state", and "**Correctness rule:** Sticky routing is an optimization. A client can reconnect to any replica and resume."

§7.1 does not state where the signing keys and the consumed state live. §10.1 decides it, so the issue is filed as a build gap: neither piece of state may be held by one replica alone.

## Evidence

- `cmd/lenny-gateway/token_signing.go`, `buildTokenSigningStores`: the ring is seeded from `crypto/rand` at process start (`rand.Read(seed[:])`, then `uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "boot", Secret: seed[:]})`), the consumed tracker is `uploadtoken.NewMemoryTracker()`, and `uploadtoken.NewRotator` rotates the ring inside the process. Nothing shares the ring, the rotations, or the tracker between replicas.
- `pkg/gateway/core/session/sessionserver/finalize_handler.go`: `FinalizeWorkspace` marks the token consumed with `s.uploadVerifier.ConsumeDigest(uploadTokenDigest, uploadTokenExpiry)`, which writes to the tracker of the replica that served the call.
- `charts/lenny/values.yaml`: `gateway.replicas` defaults to 2, so a default install runs two replicas.
- Since archived change [0173](../../changes/archive/2026-11-25-0173-idempotency-stored-credentials/change.md), a stored idempotent response holds a redaction reference that a replay rebuilds with the key that signed the original. A replica whose ring lacks that key returns the reference in the token's place ([§11.5](../../spec/11_policy-and-controls.md#115-idempotency), **Stored credentials.**).
- The review of 0173.S1 found the gap (`changes/archive/2026-11-25-0173-idempotency-stored-credentials/digests/0173.S1.md`, "Pre-existing, for the owner"). The "Problem" and "Current state" sections of change [0177](../../changes/0177-shared-upload-token-keys/change.md) record the same evidence against `main` at `6bc1c2859`.

## Gap

With more than one replica, an upload or a `FinalizeWorkspace` that the load balancer sends to a replica other than the minting one fails the HMAC check and is refused with `401 UPLOAD_TOKEN_INVALID` (`pkg/uploadtoken`). Single use holds per replica: only the replica that served `FinalizeWorkspace` answers `410 UPLOAD_TOKEN_CONSUMED`, and another replica refuses a later upload with a different error, because the session row has left `created`. An idempotent replay of a session create that another replica serves returns the redaction reference in place of the original token.

## Suggested resolution

Share the signing-key ring and the consumed state between replicas, with the keys encrypted at rest under a KMS key and each new key published to every replica before any replica signs with it, and state the placement in §7.1. Change 0177 designs this, with the store open as its decision O1.
