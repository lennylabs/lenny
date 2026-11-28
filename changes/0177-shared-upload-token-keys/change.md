# Shared signing keys for upload tokens and redaction receipts

Status: `In progress`, revision 10, 2026-11-27.

## Problem

Two gateway signers create their keys inside each process, so what one replica signs another replica cannot verify. The upload-token signer also keeps its record of consumed tokens per replica.

**Upload tokens ([UPLOAD-I1](../../issues/closed/UPLOAD/UPLOAD-I1-upload-token-keys-and-consumed-state-per-replica.md)).** `cmd/lenny-gateway/token_signing.go`, `buildTokenSigningStores`, seeds the [§7.1](../../spec/07_session-lifecycle.md#71-normal-flow) `uploadToken` key ring from `crypto/rand` at process start (`uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "boot", ...})`), starts a per-replica `uploadtoken.Rotator` for the 24-hour rotation and the 5-minute overlap, and builds a per-replica `uploadtoken.NewMemoryTracker()` for consumed tokens. Nothing shares the keys or the tracker between replicas. Three consequences follow:

- **A token verifies only on the replica that minted it.** An upload or a `FinalizeWorkspace` that the load balancer sends to another replica fails the HMAC check there and is refused with `401 UPLOAD_TOKEN_INVALID`. The chart's default `gateway.replicas` is 2 (`charts/lenny/values.yaml`), so a default install runs two replicas.
- **Single-use holds per replica.** `FinalizeWorkspace` marks the token's digest consumed in the tracker of the replica that served it (`ConsumeDigest` in `pkg/gateway/core/session/sessionserver/finalize_handler.go`). Another replica's tracker does not see the mark, so the `410 UPLOAD_TOKEN_CONSUMED` of §7.1 is returned only by that replica. A later upload is still refused elsewhere, because uploads are admitted only while the session row is `created`, but with a different error.
- **An idempotent replay on another replica loses the token.** Since change [0173](../archive/2026-11-25-0173-idempotency-stored-credentials/change.md), a stored response holds a redaction reference that a replay rebuilds with the key that signed the original. A replica whose ring lacks that key returns the reference in the token's place (0173, "Limits, and alternatives rejected").

**Redaction receipts ([AUDIT-I2](../../issues/closed/AUDIT/AUDIT-I2-redaction-receipt-signing-key-per-process.md)).** `pkg/storage/deadletterredaction/signer.go`, `NewKMSReceiptSigner`, draws a fresh HMAC-SHA256 key from `crypto/rand` when the gateway starts, seals it under the KMS alias `platform:audit-redaction-signing`, and discards the sealed key (`cipher.Seal`'s result is unused). `cmd/lenny-gateway/adminrouter.go` builds the signer with the key id `boot` on every replica. The erasure job signs each [§12.8](../../spec/12_storage-architecture.md#128-compliance-interfaces) `RedactionReceipt` in `audit_redaction_receipts` with that key, so a receipt signed by one replica cannot be verified by another, and a receipt signed before a restart cannot be verified by any process after it. Receipts are kept for `audit.gdprRetentionDays` (default 2555 days), and step 14 of the §12.8 `DeleteByUser` sequence requires a chain verifier to raise `broken` for a `redacted_gdpr` row whose receipt does not verify. The defect has no visible failure today only because the chain verifier in `pkg/audit/chain.go` checks that a signature is present and does not verify it.

[§10.1](../../spec/10_gateway-internals.md#101-horizontal-scaling) states that gateway replicas are stateless proxies over externalized state, and that a client can reach any replica. Both signers break that rule.

The owner chose to fix the upload-token gap with a change that shares the key ring and the consumed-token tracker, and asked to see the design, with the choice between Redis, Postgres, or both, before it is settled (decision 1). The owner then directed that the receipt signer be fixed in the same change, reusing the mechanism where possible (decision 2). The owner then chose Postgres only for the shared state (decision 3), a replica that stops minting when it cannot refresh its key ring (decision 4), and KMS signing for redaction receipts (decision 5). This revision states that design.

## Scope

The change covers:

- A shared signing-key store in Postgres, keyed by purpose: where the keys live, how they are protected at rest, how they rotate, how long a retired key keeps verifying, and how every replica learns of a new key before any replica signs with it. The store serves the upload-token purpose only (decision 5).
- The upload-token purpose: the §7.1 signing keys, and where the consumed state of an upload token lives and how one consume is atomic across replicas.
- The redaction receipt signer: signing each §12.8 `RedactionReceipt` inside the KMS, the key identifier recorded on each receipt, the lookup of the public key by that identifier, and the chain verifier's check of the signature.
- The behavior of minting, verifying, consuming, replaying, and signing when Postgres or the KMS is unavailable.
- §7.1 **`uploadToken` format and security properties**, §12.8 step 14 **`RedactionReceipt` schema.**, §12.3 for the key table, and §12.9 for the classification of the signing keys.

It leaves out:

- The upload-token format, its TTL, the rotation interval, and the overlap window, which keep their §7.1 values.
- The redaction of 0173, which keeps working once every replica holds every key in the ring.
- The receipt's fields, its table, its grants, and its retention, which keep their §12.8 text.
- Signing the §11.7 genesis nonce. Step 14 names the receipt key as the "same key as §11.7 genesis-nonce signing in regulated profiles", but §11.7 describes no such signing and no code signs the nonce. SK-6 states how the parenthetical is corrected.

## Terms

| Term | Meaning |
| :-- | :-- |
| Upload token | The `uploadToken` of [§7.1](../../spec/07_session-lifecycle.md#71-normal-flow), `<session_id>.<expiry_unix_seconds>.<hmac_hex>`, an HMAC-SHA256 under a gateway-held signing key. It carries no key identifier, so a verifier tries each key of its ring. |
| Redaction receipt | The signed `RedactionReceipt` of [§12.8](../../spec/12_storage-architecture.md#128-compliance-interfaces) step 14, one row of `audit_redaction_receipts` per dead-lettered `audit_log` row that user erasure redacts. Its `signature_kms_key_id` names the key that signed it. |
| Purpose | The use a signing key serves. Each purpose has its own keys and its own policy, and a key of one purpose never signs or verifies for another. The store holds one purpose, `upload-token`; redaction receipts are signed in the KMS and have no purpose in the store (decision 5). |
| Purpose policy | For one purpose: the signing algorithm, the KMS alias that protects the key, the rotation interval, the overlap window, and the time a retired key keeps verifying. |
| Signing-key ring | For one purpose, the active signing key and the earlier keys that still verify (`uploadtoken.KeyRing` for upload tokens today). |
| Consumed tracker | The record of token digests that `FinalizeWorkspace` invalidated (`uploadtoken.ConsumedTracker`). |
| Token digest | The SHA-256 of a token. The session row already stores it as `upload_token_digest`, with `upload_token_expiry`. |
| KEK and envelope encryption | A key-encryption key held by the KMS provider of §4 and §17.5, which wraps a data key (`kms.Provider.WrapDEK` and `UnwrapDEK`, `pkg/kms/envelope`). The plaintext data key exists only in process memory. |
| KMS signing | Signing inside the KMS with a key that never leaves it (`kms.Provider.Sign` and `PublicKey`, ES256), as the §10.2 JWT signer does through `jwt.NewRemoteSigner`. |
| Publish lead | The interval between the instant a new key is written to the shared store and the instant any replica may sign with it. It is longer than the interval at which replicas refresh their ring, so every replica can verify before any replica signs. |

## High-level design

All replicas read their upload-token signing keys from one shared table in Postgres (decision 3). The table is keyed by purpose, each purpose under its own policy, and it holds the `upload-token` purpose. Each key is stored encrypted under a KMS key and is decrypted only in memory. A new key is written ahead of its use, with the instant from which replicas sign with it, so every replica has loaded it by the time any replica signs with it. A retired key keeps verifying for the stale bound plus the longer of the §7.1 overlap window and the token lifetime, ten minutes at the defaults (decision 7).

The upload-token purpose also needs a consumed state. It is recorded on the session row, which already stores the token's digest, with a conditional write, so a second consume on any replica sees the first.

A replica that cannot refresh its key ring for longer than the overlap window stops minting upload tokens, so session creation on it answers `503`, and it keeps verifying with the ring it holds (decision 4).

Redaction receipts are signed inside the KMS with an ES256 key through `kms.Provider.Sign`, as §12.8 describes, and need no shared secret (decision 5). The shared store holds no receipt purpose and serves upload tokens only. The fix for the receipt signer ([AUDIT-I2](../../issues/closed/AUDIT/AUDIT-I2-redaction-receipt-signing-key-per-process.md), [AUDIT-I3](../../issues/closed/AUDIT/AUDIT-I3-chain-verifier-accepts-unverified-receipt-signatures.md), and [AUDIT-I4](../../issues/closed/AUDIT/AUDIT-I4-receipt-signing-key-reference-is-undefined.md)) therefore uses the KMS signing path of the §10.2 JWT signer rather than the shared store, as the owner chose.

SK-1 keeps the comparison of the three placements of the shared state that the owner chose from: Postgres only, Redis only, and both.

## Requirements

None by identifier. §7.1 is content of the placeholder pages SESS and UPLOAD. §12.8 step 14 is content of the placeholder pages for erasure, ERASE and PSEUDO, and the verification of a receipt belongs to the audit-log integrity of the placeholder page AUDIT.

## Current state

The state on `main` at `6bc1c2859`:

- `cmd/lenny-gateway/token_signing.go`: the per-replica ring, issuer, verifier, memory tracker, and rotator, as "Problem" states. In the same function, the §10.2 JWT key set is loaded from the KMS provider (`jwt.LoadSigning`), and the provider is resolved by `providerflags.Resolve`; a process that cannot load the JWT key set does not start.
- `pkg/uploadtoken/uploadtoken.go`: `KeyRing`, `Issuer`, `Verifier`, `ConsumedTracker`, and `MemoryTracker`. The package comment states that the `ConsumedTracker` interface exists so production can use a shared implementation, and that the package itself uses neither Redis nor Postgres. `rotator.go` holds the rotation and overlap sweep.
- `pkg/gateway/core/session/sessionserver/finalize_handler.go`: verifies the token, then reads the session row (`s.store.Get`), moves it `created` → `finalizing` → `ready`, and calls `ConsumeDigest` with the row's `UploadTokenDigest` and `UploadTokenExpiry` after the ready write.
- `pkg/gateway/core/session/sessionserver/upload_seam.go`: `verifyUploadTokenValue` and `UploadSeam.VerifyUploadToken` (`GWSEAM-VerifyUploadToken`) verify the token for the upload handler.
- `migrations/0001_initial_schema.up.sql`: `sessions.upload_token_digest` and `sessions.upload_token_expiry`.
- `pkg/storage/deadletterredaction/signer.go`: `HMACReceiptSigner` and `NewKMSReceiptSigner`, as "Problem" states. `redaction.go` takes a `Signer` interface, `Sign(ctx, message) (signature, keyID, err)`, so the signer is replaceable without touching the redaction service.
- `pkg/audit/chain.go`: `RedactionReceipt.Signature` is an opaque string, and the verifier accepts a receipt whose `OriginalHash` matches and whose signature is non-empty. `pkg/audit/auditstore` reads receipts from `audit_redaction_receipts` into that check.
- `pkg/kms/kms.go`: `Provider.Sign` returns an ES256 signature under the key an alias names, and `Provider.PublicKey` returns its public key; on a cloud provider the private key never leaves the KMS. `PublicKey` takes an alias and has no parameter for a key version.
- Precedents for secret material at rest: the §12.8 `erasure_salt` is envelope-encrypted under the tenant KEK in Postgres (`pkg/tenancy/tenantstore/pgstore`, `sealSalt`); credential leases are encrypted under the platform KEK alias `platform:credential-leases` (`pkg/gateway/credentials/credleasestore/pgstore`); the Token Service cache uses `platform:token-service-cache` (`pkg/tokenservice/cache`).
- Precedent for a single-use value across replicas: the connector-OAuth `state` is held in Redis under `conn:oauth:state:{state}` and consumed atomically, leaving a tombstone ([§12.4](../../spec/12_storage-architecture.md#124-redis-ha-and-failure-modes) key table).

What the specification assumes about the two stores:

- Postgres is the system of record ([§12.2](../../spec/12_storage-architecture.md#122-storage-roles), [§12.3](../../spec/12_storage-architecture.md#123-postgres-ha-requirements)). A session cannot be created, uploaded to, or finalized without it, because the session row lives there. The audit log and `audit_redaction_receipts` live there too.
- Redis is required in a production deployment by the minimum topology of §12.4, and it is ephemeral: "Redis is not a system of record, and total data loss is recoverable", and sensitive values are encrypted at the application layer before they are written. The gateway runs without Redis in single-replica development mode, where several roles fall back to memory (`--redis-url` unset), and §12.4 states a fallback or a bounded fail-open for each Redis role.

## Detailed design

### SK-1 Options compared

The owner chose option (i), Postgres only (decision 3). The comparison stays as the record of the choice.

| | (i) Postgres only | (ii) Redis only | (iii) Both: keys in Postgres, consumed set in Redis |
| :-- | :-- | :-- | :-- |
| Upload-token signing keys at rest | A platform table, each secret envelope-encrypted under a platform KEK alias, as the `erasure_salt` and the credential leases are. Durable. | A Redis key per signing key, envelope-encrypted under the same alias, as §12.4 requires for sensitive values. Lost with Redis data: every outstanding token stops verifying, and §7.1 does not reissue a token, so a session in `created` cannot be finalized until it expires. | As (i). |
| Redaction-receipt keys at rest, under the options of decision 5 not chosen | The same table, under the `redaction-receipt` purpose. Durable for as long as the receipts, which are in Postgres too. | Not viable. A key has to verify receipts for `audit.gdprRetentionDays`, and Redis is not a system of record, so a loss of Redis data would turn every receipt it signed into a `broken` row. The receipt keys would have to sit in Postgres, which splits the mechanism across two stores. | As (i). |
| Rotation and overlap | One replica inserts the next key with an activation instant one publish lead ahead, guarded by a unique constraint on the purpose and the activation slot so only one insert wins. Replicas refresh the ring on a timer. A retired key is deleted when its purpose's verification time ends. | The same protocol with `SET NX` on the slot and a TTL for expiry. Redis pub/sub could announce a new key, and the publish lead still has to cover a lost message. | As (i). |
| Atomic consume | A conditional update of the session row in the `finalizing` → `ready` write: `upload_token_consumed_at` is set where it is NULL. The row already holds the digest, so no new table is needed. | `SET NX` of the digest with a TTL to the token's expiry, as the OAuth `state` is consumed. | As (ii). |
| Check on upload | Reads `upload_token_consumed_at` from the session row, which the upload and finalize paths already need for the `created` state check. | One Redis read per upload request and one write per finalize. | One Redis read per upload request and one write per finalize. |
| Store unavailable | Postgres down: no session can be created, uploaded to, or finalized anyway, so the upload path adds no new failure, and the erasure job cannot run. A replica keeps verifying with its loaded rings; rotation waits. | Redis down: uploads and finalize fail closed while Postgres is up, which adds a failure the upload path does not have today. A Redis restart without persistence drops the keys, as above. | Redis down: as (ii) for consume. Keys as (i). |
| Development mode without Redis | Works unchanged. | Needs a memory fallback, which holds only for one replica. | Needs a memory fallback for consume. |
| Latency per upload | No added round trip when the consumed check rides on the row read; otherwise one indexed read by primary key. HMAC verification stays in memory. | One Redis round trip. | One Redis round trip. |
| New schema | A key table and one session column. | Two key prefixes in the §12.4 key table, and, for receipt keys, a Postgres table as in (i). | A key table, and one key prefix. |

A variant of (iii) with a Redis cache over the Postgres keys adds nothing, because each replica already holds its rings in memory and reads the store only on refresh.

Under options (b) and (c) of decision 5, which the owner did not choose, the receipt keys would sit in Postgres under every placement, so only (i) would keep both purposes in one store. Under the chosen option (a) the receipt key sits in the KMS, and the comparison concerns the upload-token purpose alone, as it did in revision 1.

### SK-2 The signing-key store

A platform-scoped table `platform_signing_keys` holds `purpose`, `key_id`, `algorithm`, the wrapped secret, the public key when the algorithm has one, the KEK alias, `active_from`, `retire_after`, and `delete_after`. The primary key is `(purpose, key_id)`. Each secret is wrapped under its purpose's platform KEK alias through `pkg/kms/envelope`, so the plaintext exists only in the gateway's memory. The table is platform-scoped because an upload token is not tenant-bound at verification time: the verifier knows the session id and the token.

A package for the store, separate from `pkg/uploadtoken`, loads, refreshes, rotates, and sweeps the keys of one purpose under that purpose's policy, and exposes a ring that signs with the active key and verifies with every loaded key. `pkg/uploadtoken` takes its ring from it. The receipt signer does not use the store (decision 5).

| Purpose | Algorithm | KEK alias | Rotation interval | Retired key verifies for | Row deleted |
| :-- | :-- | :-- | :-- | :-- | :-- |
| `upload-token` | HMAC-SHA256 | `platform:upload-token-signing` | 24 hours (§7.1, deployer-configurable) | The stale bound (the §7.1 overlap window) plus the longer of the overlap window and `maxCreatedStateTimeoutSeconds`, 10 minutes at the defaults (decision 7) | After that verification time |

At start, a replica loads every key of each purpose it serves whose `delete_after` is in the future, unwraps each secret it can still sign or verify with, and refuses to start when it cannot, as it does for the JWT key set. When a purpose has no key, the replica inserts the first one. A refresh loop reloads the rings on a fixed interval. When a purpose's active key is older than its rotation interval, a replica inserts the next key with `active_from` set one publish lead ahead; a unique constraint on the purpose and the rotation slot makes concurrent inserts from several replicas produce one key. Each replica signs with the newest key whose `active_from` has passed, and verifies with every loaded key of the same purpose. A key's `retire_after` is the next key's `active_from`, its `delete_after` is `retire_after` plus its purpose's verification time, and a sweep deletes rows after `delete_after`.

The refresh interval and the publish lead are gateway settings the specification does not fix, so each has a flag and an operator-tunable default. The publish lead is longer than the refresh interval, and the gateway refuses to start when it is not.

### SK-3 The consumed state

The `sessions` table gains `upload_token_consumed_at`. The `finalizing` → `ready` write of `FinalizeWorkspace` sets it in the same update, which is the point at which §7.1 says the token is invalidated. Verification reads the session row and returns `ErrConsumed` when the column is set, on every replica. `uploadtoken.MemoryTracker` stays for tests and the single-replica minimal gateway, and the production wiring uses the session-row check. The build records in the step's digest whether the upload path's existing row read carries the check or a new read is added.

### SK-4 Failure behavior

- Postgres unavailable at start, or the KMS unable to unwrap a key: the replica does not start.
- Postgres unavailable after start: the replica keeps its loaded rings, keeps signing with each active key, and does not rotate. Upload and finalize fail as they already do without the session row.
- A refresh that has failed for longer than the upload-token overlap window: the replica stops minting new upload tokens and answers session creation that needs a token with `503`, because it can no longer be sure that its peers hold the key it would sign with. Verification continues with the loaded ring (decision 4).
- The receipt signer holds no key in the gateway. A KMS `Sign` failure fails the erasure job's redaction step, which is retried under §12.8 **Erasure job failure alerting (CMP-026).**
- Every refusal fails closed, under `code-best-practices.md`.

### SK-5 The specification

§7.1 **`uploadToken` format and security properties** states that the signing keys are shared by every replica, stored encrypted under a KMS key, and rotated with a publish lead, and that the consumed state is recorded on the session row. §12.8 step 14 **`RedactionReceipt` schema.** names the platform audit signing alias in place of the §11.7 parenthetical (SK-6). §12.3 gains the key table, and §12.9 classifies the signing keys. §10.1 needs no edit, because the change brings both signers under its existing rule.

### SK-6 The redaction receipt signer

Step 14 of §12.8 states that a receipt is "Signed by the platform's KMS-held audit signing key (same key as §11.7 genesis-nonce signing in regulated profiles)", that "External verifiers fetch the public key from the admin API", and that `signature_kms_key_id` is "recorded inline so a later key rotation leaves prior receipts independently verifiable". Three properties follow from that text: the key is held by the KMS, the signature is checked with a public key, and the key that signed a receipt can be named and found after rotation. The shared store of SK-2 with an HMAC key gives only the third. The owner chose KMS signing, which gives all three (decision 5).

The erasure job signs each receipt with `kms.Provider.Sign` under a platform asymmetric key alias (ES256), through the same remote-signer path as the §10.2 JWT signer, and records the KMS key's identifier, including its version, in `signature_kms_key_id`. A verifier reads the public key for that identifier. `kms.Provider.PublicKey` takes an alias today, so the step adds a lookup of a public key by key identifier, which every provider supports for a prior key version. No secret is stored or shared, the shared store of SK-2 holds no receipt purpose, and every replica signs and verifies alike. The receipt fix therefore uses the KMS signing path rather than the shared store. §12.8 keeps its text, except that the §11.7 parenthetical becomes a named platform audit signing alias, because §11.7 defines no genesis-nonce signing.

Decision 5 names the options not chosen: an HMAC key under a `redaction-receipt` purpose of the shared store, which has no public key and would remove external verification from §12.8, and an ES256 key pair under such a purpose, whose private key would be wrapped under the KEK and used in gateway memory.

The chain verifier in `pkg/audit/chain.go` and `pkg/audit/auditstore` verifies each receipt's signature against the public key its `signature_kms_key_id` names, and a receipt that does not verify leaves the row `broken` and increments `lenny_audit_redaction_receipt_missing_total`. The trusted keys are the keys `platform:audit-signing` and the optional `platform:audit-verify` name, and the retired receipt keys the gateway setting `audit.retiredReceiptKeyIds` lists, each read through `kms.Provider.PublicKeyByID` (decision 9). The chain verifier and the periodic receipt scan also require the redacted row's recomputed content hash to equal the receipt's `new_hash`, for every row that has a receipt, so a redacted row rewritten again after its receipt is signed, including one written back to its pre-redaction content, reads `broken` (decision 8). A row is redacted at most once, so no chained receipt is needed (step 0177.S2). The admin API serves the public keys that step 14 says external verifiers fetch; no section of §15 defines that route today, so the step defines it.

## Worked example

`alice` creates session `s1` through replica `gw-1`, which signs the token with key `k7`, loaded by both replicas at start. Her first upload reaches `gw-2`, which verifies the HMAC with `k7` and admits it. Her `FinalizeWorkspace` reaches `gw-1`, which moves the row to `ready` and sets `upload_token_consumed_at` in the same write. A retry of the upload reaches `gw-2`, which reads the row, finds the column set, and answers `410 UPLOAD_TOKEN_CONSUMED`. Twenty-four hours after `k7` became active, `gw-2` inserts `k8` with `active_from` one publish lead ahead; `gw-1` loads it on its next refresh, and both sign with `k8` once `active_from` passes. `k7` verifies until ten minutes after that, and the sweep then deletes it.

Later, the erasure job for `bob` runs on `gw-2` and redacts a dead-lettered audit row, signing its receipt inside the KMS under the audit signing alias, whose current key version is `r3`, and recording `r3` on the receipt. `gw-2` restarts. The next chain verification runs on `gw-1`, reads the KMS public key for `r3`, verifies the receipt, and reports the row `redacted_gdpr`.

## Limits, and alternatives rejected

- A replica that cannot refresh its ring for longer than the upload-token overlap window stops minting upload tokens, so session creation on that replica fails with `503` while the others work (decision 4).
- The receipt's private key never leaves the KMS, so a compromised gateway process can sign receipts only while the compromise lasts. The options of decision 5 not chosen kept the receipt key in gateway memory, where a compromise could take a key that forges receipts until the key's row was deleted, years later, and the HMAC option also removed the external verification §12.8 states.
- The shared store does not serve redaction receipts, so the receipt fix reuses the KMS signing path of the §10.2 JWT signer rather than the mechanism this change builds for upload tokens, which departs from decision 2's preference as the owner chose in decision 5.
- Keeping per-replica keys with sticky routing for the upload window was not considered further: §10.1 states that sticky routing is an optimization and that correctness does not depend on it. Sticky routing does not apply to receipts at all.
- A KMS-held HMAC key with a KMS call per upload-token mint and per verification would keep the key out of the gateway's memory, at the cost of a KMS round trip on every upload request and an upload path that fails whenever the KMS breaker opens. The design does not propose it for upload tokens. Receipts are signed once per redacted row inside an asynchronous erasure job, so a KMS call per receipt, as decision 5 chose, does not have that cost.
- Persisting the sealed key that `NewKMSReceiptSigner` already produces, one row per process, would let old receipts verify, and it would leave one receipt key per replica start with no rotation policy and no external verification. The design does not propose it.

## Ordering

The change lands before any milestone whose tests run more than one gateway replica, as the owner's answer states (decision 1). The program does not yet name that milestone.

## Specification and non-specification changes

- `spec/07_session-lifecycle.md`: §7.1, **`uploadToken` format and security properties**.
- `spec/12_storage-architecture.md`: §12.8 step 14 **`RedactionReceipt` schema.** (SK-6), §12.3 for `platform_signing_keys`, and §12.9 for the signing keys' classification.
- `spec/15_external-api-surface.md`: the admin route that serves the receipt public keys.
- `migrations/`: `platform_signing_keys` and `sessions.upload_token_consumed_at`.
- A new package for the signing-key store (SK-2), and `pkg/uploadtoken`: a ring that loads from the store and refreshes, and a shared consumed check.
- `pkg/session/sessionstore` and its Postgres store: the consumed column.
- `pkg/gateway/core/session/sessionserver`: the consumed check and the mark in the finalize write.
- `pkg/storage/deadletterredaction`: the KMS receipt signer of SK-6 in place of `NewKMSReceiptSigner`.
- `pkg/audit` and `pkg/audit/auditstore`: the verification of the receipt signature.
- `pkg/kms`: a public-key lookup by key identifier.
- `cmd/lenny-gateway`: the wiring in `token_signing.go` and `adminrouter.go`, and the refresh and publish-lead flags.

## Documentation

- `docs/operator-guide/security.md` (operator): the upload-token signing keys are shared across replicas, encrypted under a KMS key, and rotated with a publish lead; the receipt key is held in the KMS and signs there; how long a prior version of the receipt key has to stay in the KMS; the flags that set the refresh interval and the publish lead.
- `docs/operator-guide/configuration.md` (operator): the new flags with their defaults.
- `docs/operator-guide/audit-ocsf.md` (operator): how an external verifier reads the receipt public key and checks a receipt.
- `docs/runbooks/audit-redaction-receipt-missing.md` (operator): a receipt whose signature does not verify fires the alert, as a missing receipt does; so do a receipt under a key outside the trusted set and a row whose content no longer matches its receipt's `new_hash`, and a key a rotation dropped is restored by listing it in `audit.retiredReceiptKeyIds`.
- `docs/operator-guide/security.md`, `docs/operator-guide/configuration.md`, and `docs/reference/configuration.md` (operator), step 0177.S2: the verification time of a replaced upload-token key and how `--max-created-state-timeout-seconds` lengthens it; the setting `audit.retiredReceiptKeyIds` (`--audit-retired-receipt-key-ids`, `LENNY_AUDIT_RETIRED_RECEIPT_KEY_IDS`, default empty), the key forms it accepts, the startup refusal when a listed key cannot be read or does not name one key version, `GetPublicKey` on each listed key, and keeping a listed key for `audit.gdprRetentionDays`; the `new_hash` check.
- `docs/operator-guide/audit-ocsf.md` (operator), step 0177.S2: an external verifier checks the receipt's `new_hash` against the row, and the trusted keys include the listed retired keys.
- `docs/reference/metrics.md` and `docs/api/admin.md` (operator), step 0177.S2: the conditions the receipt-missing counter counts, and the retired keys in the receipt key route.
- `docs/api/admin.md` (operator): the public-key route.
- No reader-facing behavior changes: UPLOAD. A client's token behaves as §7.1 already states, on every replica.

## Tests

- Unit, `pkg/uploadtoken` and the store package: a ring loaded from a fake store signs with the newest active key and verifies with every loaded key; a key whose `active_from` is in the future verifies and does not sign; a key of one purpose does not verify for another; a refresh failure longer than the upload-token overlap stops minting; two replicas racing a rotation produce one key; the sweep deletes a retired upload-token key after its `delete_after`. Cites §7.1 (Normal Flow) and §12.8 (Compliance Interfaces).
- Unit, `pkg/storage/deadletterredaction` and `pkg/audit`: a receipt signed by one signer instance verifies under another instance over the same KMS; a receipt signed before a rotation verifies after it; a receipt with a tampered field or an unknown key identifier leaves the row `broken`. Cites §12.8 (Compliance Interfaces).
- Component, `tests/component/stores`: the key table round-trips a wrapped secret under the local KMS provider, the unique constraint holds under concurrent inserts, and the consumed column is set by the finalize write. Cites §7.1 (Normal Flow).
- Contract: a token minted on one gateway instance verifies on another over the same store, and a token consumed on one is refused with `410 UPLOAD_TOKEN_CONSUMED` on the other. Cites §7.1 (Normal Flow).
- Load_local: concurrent finalize calls on two instances consume a token once, under `-race`. Cites §7.1 (Normal Flow).
- Integration: two gateway replicas on the compose stack; upload on one, finalize on the other, and an idempotent replay of the create on the other returns the original token. A user erasure that redacts a dead-lettered audit row on one replica, then a restart of that replica, then a chain verification on the other replica that reports the row `redacted_gdpr`. Cites §7.1 (Normal Flow), §11.5 (Idempotency), and §12.8 (Compliance Interfaces).
- Chaos: the store unavailable after start keeps verification working and stops rotation; a refresh outage longer than the overlap stops minting upload tokens. Cites §7.1 (Normal Flow).
- Security, `tests/security`: the key table holds no plaintext secret; a token signed with a key that has left every ring is refused; a forged receipt signature leaves the row `broken` and fires `AuditRedactionReceiptMissing`. Cites §7.1 (Normal Flow) and §12.8 (Compliance Interfaces).
