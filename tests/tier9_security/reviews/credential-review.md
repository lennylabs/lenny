# Credential Subsystem — Targeted Security Design Review

> Phase 5.6 checklist per TESTING.md §13.13. Recorded findings link
> to the commits that resolved them.

## Scope

The §4.9 credential leasing service, §11.5 idempotency around credential operations, §12.4 Redis-backed lease state, and §13.3 credential flow before the credential subsystem is exposed to first-party tenants.

## Checklist

The reviewer confirms each item before checking off. A finding entry below records anything that needs follow-up.

- [ ] Token Service mTLS material rotates without dropping in-flight leases. (Unchecked: `cmd/lenny-token-service/main.go` constructs a plain `http.Server` with no `TLSConfig`; the service listens in plaintext and has no mTLS material to rotate. See Finding 1.)
- [ ] KMS-backed envelope keys are wrapped per tenant; no plain-text DEK persists to disk. (Unchecked: `credentialstore.Credential.Secret` is a plain `string` and `credentialstore/pgstore/pgstore.go` writes it verbatim to the `secret` column; `credcache.Cache` holds plain-text upstream keys. No DEK, KEK, or `key_version` exists. See Finding 2.)
- [ ] Lease IDs are uniformly random; predictable IDs do not leak the global counter. (Verified: `credential.MintLease` mints lease IDs via `randomString("cl", 16)` and lease tokens via `randomString("lt", 32)`, both reading `crypto/rand` with no counter. See Finding 5 for an adjacent informational note on the Token Service JTI.)
- [ ] Per-tenant rate limits on `acquire_lease` and `renew_lease` survive replica failover. (Unchecked: `credassign.Service.Assign` and `credrenewal.Worker` apply no rate limit; the credential subsystem has no per-tenant limiter on lease acquisition or renewal. See Finding 3.)
- [ ] Compromised lease detection wires into the §11.7 audit hash chain. (Unchecked: no credential package writes to the §11.7 `audit_log` table; `credrenewal` exposes `OnRenewed` and `OnExhausted` callbacks but no caller emits a `credential.*` audit event. See Finding 4.)
- [ ] The §4.9.1 key-rotation procedure is documented and rehearsed. (Unchecked: §4.9.1 documents the procedure in the spec, but no `key_version` column, re-encryption job, or rehearsal test exists in the implemented subsystem. See Finding 6.)

## Findings

Each finding lists a short title, a severity, the affected files or functions, a description of the risk, and a remediation line. Tracked items name the wave that resolves them; the commit link is filled when that wave lands.

### Finding 1 — Token Service serves plaintext HTTP with no mTLS

**Severity:** High

**Affected:** `cmd/lenny-token-service/main.go` (the `http.Server` construction); `pkg/tokenservice/tokenservice.go` (`Server.Handler`).

The Token Service binary builds an `http.Server` with `Addr`, `Handler`, and `ReadHeaderTimeout` set, and calls `ListenAndServe` (plaintext) rather than `ListenAndServeTLS`. No `tls.Config`, client-certificate verification, or SPIFFE peer check is wired. §13.3 requires the Token Service to authenticate callers over mTLS; the current binary authenticates only the `Authorization: Bearer` caller token over an unencrypted transport, so subject tokens, actor tokens, and the minted access token traverse the network in clear text and any network peer can reach `POST /v1/oauth/token`. Because no mTLS material exists, the checklist's rotation property cannot hold. The binary documents itself as the minimal dev service pending the Postgres-backed Token Service, so this is a known build-order gap rather than a regression.

**Remediation:** Tracked: Wave 2 (Phase 12a) credential hardening. The Phase 12a Token Service must terminate mTLS, verify the gateway client certificate, and source its key material from a rotation-aware provider so a certificate roll does not interrupt in-flight token exchanges.

### Finding 2 — User-credential secrets persist unencrypted; no KMS envelope

**Severity:** High

**Affected:** `pkg/gateway/credentialstore/credentialstore.go` (`Credential.Secret`, `Memory.Register`, `Memory.Rotate`); `pkg/gateway/credentialstore/pgstore/pgstore.go` (`Register`, `Rotate`, the `secret` column write); `pkg/gateway/credcache/credcache.go` (`Cache.creds`).

A registered user credential is held as a plain Go `string` on `credentialstore.Credential.Secret`, and the Postgres-backed store writes that string verbatim into the `secret` column. The package doc comment in `credentialstore/pgstore/pgstore.go` states plainly that "KMS-envelope encryption of the secret column is a later phase." §4.9 classifies user-supplied API keys as T4 Restricted and requires per-tenant DEK envelope encryption wrapped by a KMS KEK, matching OAuth refresh-token storage. The implemented subsystem has no DEK, no KEK, no `key_version` column, and no KMS client, so a Postgres dump, a backup tape, or a database superuser exposes every user's upstream API key in clear text. The same plaintext exposure applies to `credcache.Cache`, which holds upstream pool keys as plain strings in process memory; that is the intended in-memory residency per §4.9, but it depends on the durable store being encrypted, which it is not.

**Remediation:** Tracked: Wave 2 (Phase 12a) credential hardening. Phase 12a adds KMS envelope encryption to the Token Service; the `secret` column must store `{key_version, nonce, ciphertext, tag}` encrypted under a per-tenant DEK, and the §4.9.1 rotation procedure must apply to it.

### Finding 3 — No per-tenant rate limit on lease acquisition or renewal

**Severity:** Medium

**Affected:** `pkg/gateway/credassign/credassign.go` (`Service.Assign`); `pkg/gateway/credrenewal/credrenewal.go` (`Worker.Tick`, `Worker.Track`).

`credassign.Service.Assign` selects a credential, mints a lease, and records it with no per-tenant or per-pool admission limit beyond the `least-loaded` / `round-robin` / `sticky-until-failure` selection. `credrenewal.Worker` bounds retries per lease through `MaxRenewalRetries` but applies no rate limit across leases or tenants. §4.9 states the leasing path and the LLM proxy "enforce the same rate limits and budget constraints"; the implemented credential subsystem carries none, so a tenant that drives `acquire_lease` or `renew_lease` in a tight loop is bounded only by `MaxConcurrentSessions` per credential, and a renewal storm after a coordinator handoff is unthrottled. The checklist additionally requires the limiter to survive replica failover, which presupposes Redis-backed counters (§12.4 `LeaseStore`); no such counter is wired. The credential subsystem packages are pure in-process state, so the limiter belongs in the gateway integration layer that consumes them.

**Remediation:** Tracked: Wave 2 (Phase 12a) credential hardening. The gateway credential path must apply a per-tenant `acquire_lease` / `renew_lease` limiter backed by the §12.4 Redis `LeaseStore` so the counter is shared across replicas and a failover does not reset a tenant's budget. The §12.4 bounded fail-open rule applies when Redis is unavailable.

### Finding 4 — Compromised-lease signals do not reach the audit hash chain

**Severity:** Medium

**Affected:** `pkg/gateway/credrenewal/credrenewal.go` (`Worker.Revoke`, `Worker.exhaust`, the `OnExhausted` and `OnRenewed` callbacks); `pkg/gateway/credassign/credassign.go` (`Service.Assign`, `Service.Release`); `pkg/gateway/denylist/denylist.go` and `pkg/gateway/denylist/propagator/propagator.go` (`Revoke`).

§4.9.2 defines a `credential.*` audit-event catalogue, and §11.7 requires every such event to enter the per-tenant hash-chained `audit_log`. The `credential.rotation_ceiling_hit` event is named a Tier-1 compromise indicator that must stream to SIEM. None of the implemented credential packages write an audit event. `credrenewal.Worker.Revoke` and `exhaust` drop a lease and invoke the `OnExhausted` callback, `credassign` mints and releases leases, and `denylist` / `propagator` revoke credentials, but no path records `credential.leased`, `credential.revoked`, `credential.renewed`, `credential.rotation_ceiling_hit`, or `credential.lease_spiffe_mismatch`. A revoked or compromised credential therefore leaves no tamper-evident audit trail, and the hash chain cannot be used to detect a credential incident after the fact. The `llmproxy` handler rejects a `RejectSpiffeMismatch` request but emits no `credential.lease_spiffe_mismatch` event for it.

**Remediation:** Tracked: Wave 2 (Phase 12a) credential hardening. The audit `EventStore` integration is part of the deferred audit and erasure work; once it lands, the credential-assignment, renewal, revocation, and proxy-rejection paths must each emit their §4.9.2 event into the §11.7 hash-chained `audit_log`.

### Finding 5 — Token Service JTI carries a process-local monotonic counter (informational)

**Severity:** Informational

**Affected:** `pkg/tokenservice/tokenservice.go` (`newJTI`, `jtiCounter`).

Checklist item 3 is satisfied for lease identifiers: `credential.MintLease` derives both the lease ID and the lease token from `crypto/rand`. An adjacent identifier in the same subsystem does not follow that pattern. `tokenservice.newJTI` builds the issued-token JTI from a microsecond timestamp concatenated with a `sync.Mutex`-guarded `uint64` counter that starts at zero on every process start. The JTI is not a credential or a bearer secret, and it is not relied on for unguessability, so this is not a vulnerability. It does leak the per-process issuance ordering and count, and two replicas started at the same instant can mint the same JTI prefix, which would collide the `issued_tokens` primary key and surface as a spurious `ErrAlreadyExists`. The binary documents itself as the minimal dev Token Service.

**Remediation:** Tracked: Wave 2 (Phase 12a) credential hardening. The Phase 12a Token Service should mint the JTI from `crypto/rand` (a UUIDv4 or equivalent) so the identifier carries no process-ordering information and no cross-replica collision risk.

### Finding 6 — §4.9.1 key-rotation procedure is documented but not rehearsable

**Severity:** Medium

**Affected:** spec §4.9.1 (documentation only); `pkg/gateway/credentialstore/pgstore/pgstore.go` (absence of a `key_version` column); the credential subsystem (absence of a re-encryption job and a rotation test).

§4.9.1 documents the KMS key-rotation procedure: generate a new DEK, run an idempotent re-encryption job, track `key_version` per row, verify `SELECT COUNT(*) ... WHERE key_version < current_version` returns zero, then disable the old key. The documentation is complete. The procedure is not rehearsable against the implemented subsystem, because the prerequisites from Finding 2 are absent: there is no `key_version` column on the `credentials` table, no envelope encryption, no re-encryption job, and no test that exercises a rotation. The checklist asks for the procedure to be both documented and rehearsed; only the first half holds today.

**Remediation:** Tracked: Wave 2 (Phase 12a) credential hardening. After Phase 12a adds the envelope-encryption schema, a rehearsal test must exercise the §4.9.1 procedure end to end: encrypt under DEK v1, rotate to v2, run the re-encryption job, and assert the verification query returns zero before the old key is disabled.

## How this file is consumed

`phase-5.6-gate` (groups.yaml) asserts this file exists and parses as Markdown. The gate does not parse the checklist items; it pins the artifact in place so the review surface is discoverable. A separate human pass walks the checklist and edits the Findings section before the §5.6 phase gate closes.
