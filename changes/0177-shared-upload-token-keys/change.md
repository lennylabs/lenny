# A shared upload-token key ring and consumed-token tracker

Status: `Draft`, revision 1, 2026-11-25.

## Problem

Each gateway replica creates its own upload-token signing keys and keeps its own record of consumed tokens, so an upload token works only on the replica that minted it.

`cmd/lenny-gateway/token_signing.go`, `buildTokenSigningStores`, seeds the [§7.1](../../spec/07_session-lifecycle.md#71-normal-flow) `uploadToken` key ring from `crypto/rand` at process start (`uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "boot", ...})`), starts a per-replica `uploadtoken.Rotator` for the 24-hour rotation and the 5-minute overlap, and builds a per-replica `uploadtoken.NewMemoryTracker()` for consumed tokens. Nothing shares the keys or the tracker between replicas. Three consequences follow:

- **A token verifies only on the replica that minted it.** An upload or a `FinalizeWorkspace` that the load balancer sends to another replica fails the HMAC check there and is refused with `401 UPLOAD_TOKEN_INVALID`. The chart's default `gateway.replicas` is 2 (`charts/lenny/values.yaml`), so a default install runs two replicas.
- **Single-use holds per replica.** `FinalizeWorkspace` marks the token's digest consumed in the tracker of the replica that served it (`ConsumeDigest` in `pkg/gateway/core/session/sessionserver/finalize_handler.go`). Another replica's tracker does not see the mark, so the `410 UPLOAD_TOKEN_CONSUMED` of §7.1 is returned only by that replica. A later upload is still refused elsewhere, because uploads are admitted only while the session row is `created`, but with a different error.
- **An idempotent replay on another replica loses the token.** Since change [0173](../archive/2026-11-25-0173-idempotency-stored-credentials/change.md), a stored response holds a redaction reference that a replay rebuilds with the key that signed the original. A replica whose ring lacks that key returns the reference in the token's place (0173, "Limits, and alternatives rejected").

[§10.1](../../spec/10_gateway-internals.md#101-horizontal-scaling) states that gateway replicas are stateless proxies over externalized state, and that a client can reach any replica. The per-replica ring and tracker break that rule for the upload window.

The owner chose to fix the gap with a change that shares the key ring and the consumed-token tracker, and asked to see the design, with the choice between Redis, Postgres, or both, before it is settled (decision 1). This revision is that design. The store choice is open decision O1.

## Scope

The change covers:

- Where the upload-token signing keys live, how they are protected at rest, how they rotate, and how every replica learns of a new key before any replica signs with it.
- Where the consumed state of an upload token lives, and how one consume is atomic across replicas.
- The behavior of minting, verifying, consuming, and replaying a token when the chosen store is unavailable.
- §7.1 **`uploadToken` format and security properties**, and the §12 sections for the chosen store.

It leaves out:

- The upload-token format, its TTL, the rotation interval, and the overlap window, which keep their §7.1 values.
- The redaction of 0173, which keeps working once every replica holds every key in the ring.
- `deadletterredaction.NewKMSReceiptSigner` (`pkg/storage/deadletterredaction/signer.go`), which also draws a per-replica random HMAC key at start for the §12.8 redaction receipts. It is a separate signer with its own readers and is not part of the upload window.

## Terms

| Term | Meaning |
| :-- | :-- |
| Upload token | The `uploadToken` of [§7.1](../../spec/07_session-lifecycle.md#71-normal-flow), `<session_id>.<expiry_unix_seconds>.<hmac_hex>`, an HMAC-SHA256 under a gateway-held signing key. It carries no key identifier, so a verifier tries each key of its ring. |
| Signing-key ring | The active signing key and the previous keys still inside the overlap window (`uploadtoken.KeyRing`). |
| Consumed tracker | The record of token digests that `FinalizeWorkspace` invalidated (`uploadtoken.ConsumedTracker`). |
| Token digest | The SHA-256 of a token. The session row already stores it as `upload_token_digest`, with `upload_token_expiry`. |
| KEK and envelope encryption | A key-encryption key held by the KMS provider of §4 and §17.5, which wraps a data key (`kms.Provider.WrapDEK` and `UnwrapDEK`, `pkg/kms/envelope`). The plaintext data key exists only in process memory. |
| Publish lead | The interval between the instant a new key is written to the shared store and the instant any replica may sign with it. It is longer than the interval at which replicas refresh their ring, so every replica can verify a token before any replica issues one. |

## High-level design

All replicas read their signing keys from one shared store. Each key is stored encrypted under a KMS key and is decrypted only in memory. A new key is written ahead of its use, with the instant from which replicas sign with it, so every replica has loaded it by the time any replica mints a token under it. Old keys stay for the overlap window and are then deleted. The consumed state of a token is recorded in a shared store with an atomic write, so a second consume on any replica sees the first.

The design compares three placements of these two pieces of state: Postgres only, Redis only, and both. The recommendation is Postgres only, with the consumed state on the session row that already stores the token's digest. The detailed design below follows that recommendation, and names what changes under the other options.

## Requirements

None by identifier. §7.1 is content of the placeholder pages SESS and UPLOAD.

## Current state

The state on `main` at `6bc1c2859`:

- `cmd/lenny-gateway/token_signing.go`: the per-replica ring, issuer, verifier, memory tracker, and rotator, as "Problem" states. In the same function, the §10.2 JWT key set is loaded from the KMS provider (`jwt.LoadSigning`), and the provider is resolved by `providerflags.Resolve`; a process that cannot load the JWT key set does not start.
- `pkg/uploadtoken/uploadtoken.go`: `KeyRing`, `Issuer`, `Verifier`, `ConsumedTracker`, and `MemoryTracker`. The package comment states that the `ConsumedTracker` interface exists so production can use a shared implementation, and that the package itself uses neither Redis nor Postgres. `rotator.go` holds the rotation and overlap sweep.
- `pkg/gateway/core/session/sessionserver/finalize_handler.go`: verifies the token, then reads the session row (`s.store.Get`), moves it `created` → `finalizing` → `ready`, and calls `ConsumeDigest` with the row's `UploadTokenDigest` and `UploadTokenExpiry` after the ready write.
- `pkg/gateway/core/session/sessionserver/upload_seam.go`: `verifyUploadTokenValue` and `UploadSeam.VerifyUploadToken` (`GWSEAM-VerifyUploadToken`) verify the token for the upload handler.
- `migrations/0001_initial_schema.up.sql`: `sessions.upload_token_digest` and `sessions.upload_token_expiry`.
- Precedents for secret material at rest: the §12.8 `erasure_salt` is envelope-encrypted under the tenant KEK in Postgres (`pkg/tenancy/tenantstore/pgstore`, `sealSalt`); credential leases are encrypted under the platform KEK alias `platform:credential-leases` (`pkg/gateway/credentials/credleasestore/pgstore`); the Token Service cache uses `platform:token-service-cache` (`pkg/tokenservice/cache`).
- Precedent for a single-use value across replicas: the connector-OAuth `state` is held in Redis under `conn:oauth:state:{state}` and consumed atomically, leaving a tombstone ([§12.4](../../spec/12_storage-architecture.md#124-redis-ha-and-failure-modes) key table).

What the specification assumes about the two stores:

- Postgres is the system of record ([§12.2](../../spec/12_storage-architecture.md#122-storage-roles), [§12.3](../../spec/12_storage-architecture.md#123-postgres-ha-requirements)). A session cannot be created, uploaded to, or finalized without it, because the session row lives there.
- Redis is required in a production deployment by the minimum topology of §12.4, and it is ephemeral: "Redis is not a system of record, and total data loss is recoverable", and sensitive values are encrypted at the application layer before they are written. The gateway runs without Redis in single-replica development mode, where several roles fall back to memory (`--redis-url` unset), and §12.4 states a fallback or a bounded fail-open for each Redis role.

## Detailed design

### SK-1 Options compared

| | (i) Postgres only | (ii) Redis only | (iii) Both: keys in Postgres, consumed set in Redis |
| :-- | :-- | :-- | :-- |
| Signing keys at rest | A platform table, each secret envelope-encrypted under a platform KEK alias, as the `erasure_salt` and the credential leases are. Durable. | A Redis key per signing key, envelope-encrypted under the same alias, as §12.4 requires for sensitive values. Lost with Redis data: every outstanding token stops verifying, and §7.1 does not reissue a token, so a session in `created` cannot be finalized until it expires. | As (i). |
| Rotation and overlap | One replica inserts the next key with an activation instant one publish lead ahead, guarded by a unique constraint on the activation slot so only one insert wins. Replicas refresh the ring on a timer. Old keys are deleted after the overlap. | The same protocol with `SET NX` on the slot and a TTL for expiry. Redis pub/sub could announce a new key, and the publish lead still has to cover a lost message. | As (i). |
| Atomic consume | A conditional update of the session row in the `finalizing` → `ready` write: `upload_token_consumed_at` is set where it is NULL. The row already holds the digest, so no new table is needed. | `SET NX` of the digest with a TTL to the token's expiry, as the OAuth `state` is consumed. | As (ii). |
| Check on upload | Reads `upload_token_consumed_at` from the session row, which the upload and finalize paths already need for the `created` state check. | One Redis read per upload request and one write per finalize. | One Redis read per upload request and one write per finalize. |
| Store unavailable | Postgres down: no session can be created, uploaded to, or finalized anyway, so the upload path adds no new failure. A replica keeps verifying with its loaded ring; rotation waits. | Redis down: uploads and finalize fail closed while Postgres is up, which adds a failure the upload path does not have today. A Redis restart without persistence drops the keys, as above. | Redis down: as (ii) for consume. Keys as (i). |
| Development mode without Redis | Works unchanged. | Needs a memory fallback, which holds only for one replica. | Needs a memory fallback for consume. |
| Latency per upload | No added round trip when the consumed check rides on the row read; otherwise one indexed read by primary key. HMAC verification stays in memory. | One Redis round trip. | One Redis round trip. |
| New schema | A key table and one session column. | Two key prefixes in the §12.4 key table. | A key table, and one key prefix. |

A variant of (iii) with a Redis cache over the Postgres keys adds nothing, because each replica already holds the ring in memory and reads the store only on refresh.

### SK-2 Key storage and rotation (option (i))

A platform-scoped table `upload_token_signing_keys` holds `key_id`, the wrapped secret, the KEK alias, `active_from`, and `retire_after`. The secret is wrapped under a platform KEK alias, `platform:upload-token-signing`, through `pkg/kms/envelope`, so the plaintext exists only in the gateway's memory. The table is platform-scoped because a token is not tenant-bound at verification time: the verifier knows the session id and the token, and the key ring serves every tenant.

At start, a replica loads every key whose `retire_after` is in the future, unwraps each, and refuses to start when it cannot, as it does for the JWT key set. When the table holds no key, the replica inserts the first one. A refresh loop reloads the ring on a fixed interval. When the active key is older than the rotation interval, a replica inserts the next key with `active_from` set one publish lead ahead; a unique constraint on the rotation slot makes concurrent inserts from several replicas produce one key. Each replica signs with the newest key whose `active_from` has passed, and verifies with every loaded key. A key's `retire_after` is the next key's `active_from` plus the overlap window, and a sweep deletes rows after it.

The refresh interval and the publish lead are gateway settings the specification does not fix, so each has a flag and an operator-tunable default. The publish lead is longer than the refresh interval, and the gateway refuses to start when it is not.

### SK-3 The consumed state (option (i))

The `sessions` table gains `upload_token_consumed_at`. The `finalizing` → `ready` write of `FinalizeWorkspace` sets it in the same update, which is the point at which §7.1 says the token is invalidated. Verification reads the session row and returns `ErrConsumed` when the column is set, on every replica. `uploadtoken.MemoryTracker` stays for tests and the single-replica minimal gateway, and the production wiring uses the session-row check. The build records in the step's digest whether the upload path's existing row read carries the check or a new read is added.

### SK-4 Failure behavior

- Postgres unavailable at start, or the KMS unable to unwrap a key: the replica does not start.
- Postgres unavailable after start: the replica keeps its loaded ring, keeps signing with the active key, and does not rotate. Upload and finalize fail as they already do without the session row.
- A refresh that has failed for longer than the overlap window: the replica stops minting new tokens and answers session creation that needs a token with `503`, because it can no longer be sure that its peers hold the key it would sign with. Verification continues with the loaded ring.
- Every refusal fails closed, under `code-best-practices.md`.

### SK-5 The specification

§7.1 **`uploadToken` format and security properties** states that the signing keys are shared by every replica, stored encrypted under a KMS key, and rotated with a publish lead, and that the consumed state is recorded on the session row. The §12.4 or §12.3 text for the chosen store gains the table, or the key prefixes under option (ii) or (iii). §10.1 needs no edit, because the change brings the upload window under its existing rule.

## Worked example

`alice` creates session `s1` through replica `gw-1`, which signs the token with key `k7`, loaded by both replicas at start. Her first upload reaches `gw-2`, which verifies the HMAC with `k7` and admits it. Her `FinalizeWorkspace` reaches `gw-1`, which moves the row to `ready` and sets `upload_token_consumed_at` in the same write. A retry of the upload reaches `gw-2`, which reads the row, finds the column set, and answers `410 UPLOAD_TOKEN_CONSUMED`. Twenty-four hours after `k7` became active, `gw-2` inserts `k8` with `active_from` one publish lead ahead; `gw-1` loads it on its next refresh, and both sign with `k8` once `active_from` passes. `k7` verifies until five minutes after that, and the sweep then deletes it.

## Limits, and alternatives rejected

- Under option (i), a replica that cannot refresh its ring for longer than the overlap window stops minting tokens, so session creation on that replica fails while the others work.
- Keeping per-replica keys with sticky routing for the upload window was not considered further: §10.1 states that sticky routing is an optimization and that correctness does not depend on it.
- A KMS-held HMAC key with a KMS call per mint and per verification would keep the key out of the gateway's memory, at the cost of a KMS round trip on every upload request and an upload path that fails whenever the KMS breaker opens. The design does not propose it.

## Ordering

The change lands before any milestone whose tests run more than one gateway replica, as the owner's answer states (decision 1). The program does not yet name that milestone.

## Specification and non-specification changes

- `spec/07_session-lifecycle.md`: §7.1, **`uploadToken` format and security properties**.
- `spec/12_storage-architecture.md`: the section for the chosen store (§12.3 or §12.4), and §12.9 for the signing keys' classification.
- `migrations/`: the key table and `sessions.upload_token_consumed_at` under option (i).
- `pkg/uploadtoken`: a ring that loads from a store and refreshes, and a shared consumed check.
- `pkg/session/sessionstore` and its Postgres store: the consumed column under option (i).
- `pkg/gateway/core/session/sessionserver`: the consumed check and the mark in the finalize write.
- `cmd/lenny-gateway`: the wiring in `token_signing.go`, and the refresh and publish-lead flags.

## Documentation

- `docs/operator-guide/security.md` (operator): the upload-token signing keys are shared across replicas, encrypted under a KMS key, and rotated with a publish lead; the flags that set the refresh interval and the publish lead.
- `docs/operator-guide/configuration.md` (operator): the new flags with their defaults.
- No reader-facing behavior changes: UPLOAD. A client's token behaves as §7.1 already states, on every replica.

## Tests

- Unit, `pkg/uploadtoken`: a ring loaded from a fake store signs with the newest active key and verifies with every loaded key; a key whose `active_from` is in the future verifies and does not sign; a refresh failure longer than the overlap stops minting; two replicas racing a rotation produce one key. Cites §7.1 (Normal Flow).
- Component, `tests/component/stores`: the key table round-trips a wrapped secret under the local KMS provider, the unique constraint holds under concurrent inserts, and the consumed column is set by the finalize write. Cites §7.1 (Normal Flow).
- Contract: a token minted on one gateway instance verifies on another over the same store, and a token consumed on one is refused with `410 UPLOAD_TOKEN_CONSUMED` on the other. Cites §7.1 (Normal Flow).
- Load_local: concurrent finalize calls on two instances consume a token once, under `-race`. Cites §7.1 (Normal Flow).
- Integration: two gateway replicas on the compose stack; upload on one, finalize on the other, and an idempotent replay of the create on the other returns the original token. Cites §7.1 (Normal Flow) and §11.5 (Idempotency).
- Chaos: the store unavailable after start keeps verification working and stops rotation; a refresh outage longer than the overlap stops minting. Cites §7.1 (Normal Flow).
- Security, `tests/security`: the key table holds no plaintext secret, and a token signed with a key that has left every ring is refused. Cites §7.1 (Normal Flow).
