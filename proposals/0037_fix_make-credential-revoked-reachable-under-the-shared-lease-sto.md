# Proposal: Make CREDENTIAL_REVOKED reachable under the shared lease store and complete the §4.9 user-credential deny-list startup rebuild

- **Status:** Draft revision (2026-07-16): expands scope to bound the deny-list and lease-table retention properly — implements the §4.9 lease-existence rebuild filter, a bounded expired-lease sweep with deny-entry expiry, and rewords the `active_leases_terminated` audit semantics; re-review pending.
- **Date:** 2026-07-16.
- **Scope:** A spec-to-code reconciliation that makes the §4.9 `CREDENTIAL_REVOKED` deny-list contract reachable in the multi-replica shared-Postgres topology and completes the §4.9 startup rebuild union across both credential stores. Two coupled code defects are fixed: revocation deletes the lease before the deny-list check can run (`CREDENTIAL_REVOKED` is unreachable under the shared lease store), and the startup rebuild seeds only the pool term of the two-store union on a stale premise (a restarted replica rebuilds a pool-only deny list). The change deletes two `leases.Remove` calls across the two credential-revocation paths whose backing credential is durably marked `status = 'revoked'` (**CODE-A**, `pkg/gateway/credentials/usercreds/usercreds.go`, `cmd/lenny-gateway/credential_revocation.go`); the §11.4 full_revoke path (`cmd/lenny-gateway/user_revocation.go`) is excluded because it marks no credential revoked, so the startup rebuild could not re-deny a retained lease there. The change adds a `RevokedCredentials` listing query to `TokenStore` on Memory and pgstore mirroring the pool store (**CODE-B**, `pkg/gateway/credentials/credentialstore`), seeds the user-credential term in the §4.9 rebuild union (**CODE-C**, `cmd/lenny-gateway/workers.go`), and retargets the spec-named `TestUserCredentialRevocationDenyListProxy` to the shared-store topology and adds the restart-rebuild path plus store-query unit tests (**TEST-E**). This revision expands scope to bound retention properly rather than perpetuate the shipped over-approximation: **CODE-D** applies the §4.9 lease-existence filter at the rebuild site so a deny-list entry is seeded only for a revoked credential that still has an active lease (both pool and user keys), bounding the per-replica deny list and the startup rebuild that otherwise grow monotonically with cumulative revocations; **CODE-E** adds a plain `expires_at` column to `credential_leases` (migration `0175`) and a `DeleteExpired` lease-store method; **CODE-F** adds a `DenyList.Remove` method and a periodic gateway sweep worker that deletes expired lease rows and drops each credential's deny entry once its last active lease lapses, implementing the `spec/04:1671` promise that entries "expire when the credential's natural lease TTL lapses"; **SPEC-E** rewords the `active_leases_terminated` audit count (leases-affected: terminated in direct mode, denied in place in proxy mode); and **SPEC-F** reconciles the §4.9 rebuild bullets (`:1694`/`:1695`) to "active lease" now that the filter is implemented. The change therefore edits the spec (SPEC-E/SPEC-F), adds one schema migration, two lease-store methods (`DeleteExpired`, and the `expires_at` projection on `Put`), and a background worker, on top of the deny-list-reachability fix.

This document stages the proposed code and test changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 1. Problem

Spec §4.9 mandates that a proxy request presenting a lease whose backing credential was revoked is rejected with `CREDENTIAL_REVOKED` (category `SECURITY`) before any upstream call, via a source-aware deny list (`spec/04_system-components.md:1672`, `:1690`), and that a restarted replica rebuilds a complete deny list from a union across both credential stores (`spec/04:1692-1697`, exercised by the spec-named `TestUserCredentialRevocationDenyListProxy` at `:1699`). Two coupled code defects make this contract unreachable in the multi-replica shared-Postgres topology that is v1's canonical production deployment.

### (A) `CREDENTIAL_REVOKED` is unreachable under the shared lease store

The LLM proxy resolves the lease before it consults the deny list. A `GetByToken` miss returns `LEASE_TOKEN_INVALID` (401) and returns early (`pkg/gateway/llmproxy/llmproxy/handler.go:319-323`), and only a resolved lease reaches `DenyList.Revoked(lease.CredentialKey())`, whose `RejectRevoked` result yields `CREDENTIAL_REVOKED` (403) at `handler.go:326-347`. Three revocation paths write the deny-list entry and then delete the lease. Two of them revoke a specific credential and durably mark it `status = 'revoked'` in a credential store, so a retained lease against that credential is re-deniable from the store on a restarted replica:

- `Materializer.RevokeUser` (`pkg/gateway/credentials/usercreds/usercreds.go:259-263`), reached from `POST /v1/credentials/{credentialRef}/revoke`, which marks the token-store credential revoked via `store.Revoke` before propagating (`credentialserver.go:313`, `:323`), and
- pool-credential revocation (`cmd/lenny-gateway/credential_revocation.go:59-62`), whose admin endpoint marks the pool credential revoked in the `CredentialPoolStore` (`credential_revocation.go:13-14`).

The third path, the §11.4 full_revoke lease revoker (`cmd/lenny-gateway/user_revocation.go:186-187`), revokes the user rather than a credential. It writes an in-memory deny-list entry keyed on `lease.CredentialKey()` and cancels the user's sessions, but no code on the full_revoke fan-out marks any credential `status = 'revoked'` (`users_invalidate.go:145-197`). A retained lease on that path is therefore not re-deniable from either store on restart, so CODE-A leaves the full_revoke path deleting its lease. The reasoning is developed in §2 and recorded as a non-goal in §5.

When the LLM-proxy lease store is the shared Postgres backend (`stores.go` substitutes `credleasestore/pgstore` when a `pgPool` is configured), `Store.Remove` is a global `DELETE FROM credential_leases WHERE lease_id = $1` (`pkg/gateway/credentials/credleasestore/pgstore/pgstore.go:190-193`) and `GetByToken` resolves against the same shared table (`pgstore.go:172-177`). A post-revocation proxy request therefore resolves no lease on every replica and returns `LEASE_TOKEN_INVALID` rather than `CREDENTIAL_REVOKED`; the deny-list entry is never consulted because lease resolution already failed. This contradicts the deny-list design in which an entry shadows a still-resolvable lease until its natural TTL lapses (`spec/04:1671`, `:1687-1688`).

The spec-named `TestUserCredentialRevocationDenyListProxy` passes today only because the existing tier-4 test uses two independent in-memory lease stores (`leasesOrigin`, `leasesPeer`) so the modeled peer retains the lease (`tests/tier4_integration/credential_denylist_proxy_test.go:94`, `:126-130`). That separation collapses under a shared store: the single `DELETE` removes the row every replica reads.

### (B) The user-credential startup rebuild is vacuous

The §4.9 rebuild worker seeds only the pool term. It calls `w.credentialPools.RevokedCredentials`, builds `{Source: SourcePool, ...}` keys, and `Reset(keys)`; it emits no `{source: user, tenantId, credentialRef}` entries, and its comment declares the user term vacuous because "no user-backed lease is minted at session creation yet" (`cmd/lenny-gateway/workers.go:1421-1453`, comment at `:1430-1432`). Proposal 0007 moved lease minting to `/finalize`, so user-backed leases are in fact minted (`pkg/gateway/credentials/usercreds/usercreds.go:190-209`), which makes the comment stale.

`TokenStore` (`credentialstore.Store`) exposes no revoked-credential listing query analogous to `credentialpoolstore.RevokedCredentials` on either Memory or pgstore (`credentialstore.go:92-148`). A replica restarted immediately after a user-credential revocation therefore rebuilds a pool-only deny list. Once defect A is fixed so the lease survives revocation, that replica resolves the retained lease, misses the absent user deny-list entry (`DenyList.Revoked` returns false), and accepts the revoked user credential on the upstream path until the lease TTL lapses. This is a fail-open `SECURITY` regression on restart.

### The shared root gap

Both defects share one root gap: revocation deletes the lease so the deny-list check never runs, and the rebuild has no store query to enumerate revoked user credentials. Defect A is latent as long as B is unfixed (a deleted lease also cannot be accepted), and B is latent as long as A is unfixed (a deleted lease is never resolved on restart either). Fixing one without the other either leaves `CREDENTIAL_REVOKED` unreachable or opens the restart fail-open, so the two are fixed together.

### (C) Retaining leases exposes an unbounded, unswept deny list and stale audit wording

Fixing defect A means revocation retains the lease instead of removing it, which surfaces two pre-existing gaps that CODE-A would otherwise make worse:

- **The startup deny-list rebuild is unbounded.** `RevokedCredentials` (pool, `credentialpoolstore.go:576-602`, and the new user query) returns every revoked credential with no lease-existence bound, and `DenyList` is a plain `map[CredentialKey]struct{}` with no TTL or prune (`denylist.go:26-83`). Because a revoked credential's `status = 'revoked'` row persists for audit, the authoritative startup `Reset` re-seeds an entry for every ever-revoked credential on every replica start, so the per-replica deny list and the rebuild query grow monotonically with the deployment's cumulative revocations. The `spec/04:1694`/`:1695` "active lease" clause names the bound, but neither the shipped pool query nor a naive user query implements it, and `spec/04:1671` promises entries "expire when the credential's natural lease TTL lapses" — an expiry the code does not perform.
- **No expired-lease garbage collector exists.** The `credential_leases` table has no sweep for any lease; a retained revoked lease now lingers (denied) up to its full TTL, following the same lifecycle as an already-expired lease that also never gets collected.
- **The `active_leases_terminated` count is now inaccurate for proxy mode.** With CODE-A denying proxy-mode leases in place rather than removing them, the audit field and endpoint count (`spec/04:1674`/`:1675`/`:1375`/`:1764`/`:1766`) counts leases that were denied, not terminated.

The entries are inert (a revoked credential with no live lease is never resolved and cannot be re-leased), so this is a resource and documentation-fidelity gap rather than a security one, but retaining leases makes it visible enough to close here: CODE-D bounds the rebuild, CODE-E/CODE-F implement the sweep and deny-entry expiry, and SPEC-E/SPEC-F reconcile the spec.

## 2. Decisions

- **Retain the revoked lease; reject via the deny list (CODE-A).** Revocation stops deleting the lease from the lease store and relies on the source-aware deny list to reject it. Scoping `CREDENTIAL_REVOKED` to a single-binary or in-memory topology is rejected: the multi-replica shared-Postgres store is v1's canonical production topology, §4.9's contract is unconditional, and the project ships one canonical implementation per concern with no tier-dependent code paths. Retention is what the deny-list design already assumes: an entry "expire[s] when the credential's natural lease TTL lapses" (`spec/04:1671`, `:1687-1688`) presupposes the lease remains resolvable through revocation. With the lease retained, the proxy's `GetByToken` hits on every replica (shared-store or per-replica), reaches `DenyList.Revoked`, and returns `CREDENTIAL_REVOKED` (`handler.go:326-347`). No spec edit is needed: step 3's "terminate it immediately via in-process signal or Redis pub/sub" (`spec/04:1671`) describes deny-list propagation, so the `leases.Remove` calls are the deviation from the spec rather than the spec being wrong.
- **Retention is scoped to the two credential-revocation paths and excludes the §11.4 full_revoke lease revoker (CODE-A scope).** CODE-A retains the lease on the two paths whose backing credential is durably marked `status = 'revoked'`: `Materializer.RevokeUser`, reached from `POST /v1/credentials/{credentialRef}/revoke` (which calls `store.Revoke` at `credentialserver.go:313` before propagating at `:323`), and `poolCredentialRevoker.RevokePoolCredentials`, whose admin endpoint marks the pool credential revoked in the `CredentialPoolStore` (`credential_revocation.go:13-14`). A retained lease on either path is re-denied on a restarted replica because CODE-C's rebuild enumerates the revoked credential from its store. The §11.4 full_revoke lease revoker (`user_revocation.go:183-190`) is excluded: it revokes the user, writes an in-memory deny-list entry keyed on `lease.CredentialKey()`, and cancels the user's sessions, but no code on the full_revoke fan-out marks any credential `status = 'revoked'` (`users_invalidate.go:145-197`, which drives pod termination, lease revocation, token revocation, and playground revocation without a `credentialstore.Revoke` call). Retaining a full_revoke lease would therefore open a restart fail-open: a replica restarted after a full_revoke resolves the retained lease from the shared Postgres store, the rebuild finds the credential still `active` in both stores and adds no deny-list entry, and the proxy has no session-tombstone gate, so the request reaches upstream. Deleting the lease on the full_revoke path keeps that path fail-closed on the shared store: the global `DELETE` removes the row every replica reads, so a post-revocation request resolves no lease and is rejected `LEASE_TOKEN_INVALID` on every replica and across restarts. `LEASE_TOKEN_INVALID` is a fail-closed rejection; the §4.9 `CREDENTIAL_REVOKED` contract governs credential revocation, and the §11.4 full_revoke path is a user-level revocation that does not require the `CREDENTIAL_REVOKED` category. Marking full_revoke credentials revoked so their leases become re-deniable is a larger change that would also have to durably revoke the pool credentials held by the user's pool-backed sessions without revoking them for other users, so it is out of scope; the minimal fail-closed choice is to leave the full_revoke path removing its lease.
- **The retained lease is never treated as live by any other path.** The deny check precedes budget and session accounting: the proxy returns `CREDENTIAL_REVOKED` at `handler.go:339-347` before the `BudgetGate` at `handler.go:353`, so session accounting never runs for a denied request. Renewal cannot resurrect the retained lease: the credential-lease revocation propagator already drops the renewal worker's tracked leases bound to the revoked credential on `Revoke` (`cmd/lenny-gateway/revocation.go:79-86`, `:152-155`; `pkg/gateway/credentials/credrenewal/credrenewal.go:170-174`, `:196-198`, feeding exhaust then fault rotation), and even absent that, a renewal re-resolves the backing credential, which is now revoked and is treated as not-found by the resolution path (`credentialstore.go:107-109`), so `Renew` fails, `ExpiresAt` is never extended, and the worker exhausts. The retained revoked lease follows the identical lifecycle as any already-expired lease, resolvable but rejected, until the existing session-teardown path removes it (`credassign` `ReleaseSession` calls `Release` over `LeasesBySession`, `pkg/gateway/credentials/credassign/client.go:340-347`), the bounded sweep (CODE-F) removes it once past `ExpiresAt`, or it lapses at its natural `ExpiresAt` and is rejected thereafter as `LEASE_EXPIRED`. Retaining revoked leases makes the pre-existing absence of an expired-lease garbage collector more visible, so this revision adds one (CODE-E/CODE-F) rather than leave it out of scope.
- **Add a revoked-listing query to `TokenStore` and wire it into the rebuild union (CODE-B, CODE-C).** Add `RevokedCredentials(ctx)` to `credentialstore.Store`, implemented on Memory and pgstore, mirroring `credentialpoolstore.RevokedCredentials` (`credentialpoolstore.go:274-283`, `:576-600`; `pgstore.go:301-340`). It returns every `(tenantId, credentialRef)` where `status = 'revoked'`, scanned across all tenants. The `workers.go` rebuild appends `{Source: SourceUser, TenantID, CredentialRef}` keys to the pool keys and `Reset`s the union, replacing the stale pool-only seed and its comment. The store query stays store-scoped (the `credentialstore` holds no lease index); the lease-existence bound is applied at the rebuild site by CODE-D, which has `w.llmLeases` in hand.
- **Bound the rebuild to credentials with an active lease (CODE-D); reconcile both §4.9 rebuild bullets (SPEC-F).** The §4.9 rebuild bullets (`spec/04:1694`/`:1695`) say `WHERE status = 'revoked' AND EXISTS an active lease`, but the shipped pool query and a naive user query both ignore that clause and return every revoked credential. Because `DenyList` has no TTL or prune (`denylist.go`, a plain `map[CredentialKey]struct{}`) and `RevokedCredentials` is unbounded, the startup `Reset` re-seeds an entry for every ever-revoked credential on every replica start, so the per-replica deny list and the rebuild query grow monotonically with the deployment's cumulative revocations. The entries are inert (a revoked credential with no live lease is never resolved, and cannot be re-leased), but their count is unbounded, which is exactly what the "active lease" clause exists to prevent. CODE-D applies the filter at the rebuild site for both pool and user keys, seeding a deny entry only when `w.llmLeases.LeasesByCredential(key)` holds a lease with `ExpiresAt > now`. SPEC-F reconciles both bullets to "active lease" so spec and code now agree (resolving the parallel-bullet asymmetry the prior draft avoided by leaving both unreconciled).
- **Bounded expired-lease sweep and deny-entry expiry (CODE-E/CODE-F), implementing `spec/04:1671`.** The spec says deny-list entries "expire when the credential's natural lease TTL lapses," but no such expiry exists and no lease-TTL GC exists for any lease. The lease body is a single envelope-encrypted blob with no plain `expires_at` column, so CODE-E adds that column (migration `0175`, populated on `Put` from `lease.ExpiresAt`; a one-time backfill decrypts pre-migration rows) and a `DeleteExpired(ctx, cutoff)` method returning the removed leases' plain credential-key columns (no decrypt needed for the key). CODE-F adds `DenyList.Remove(key)` and a periodic gateway sweep worker (modeled on the idempotency-key GC at `workers.go:1455`) that deletes lease rows past `ExpiresAt` and removes a credential's deny entry once its last active lease lapses (`LeasesByCredential(key)` empty). This bounds both the `credential_leases` table and the in-memory deny list within a replica's lifetime, and makes the `:1671` promise real.
- **Re-document the revocation `active_leases_terminated` count (SPEC-E).** With proxy-mode leases denied in place rather than removed, the count means leases-affected (terminated in direct mode, denied in place in proxy mode). SPEC-E rewords the field's description at `spec/04:1674`/`:1675`/`:1764`/`:1766`/`:1375`; the field is not renamed, so the wire and audit contract are unchanged.
- **Reconcile both §4.9 rebuild bullets together (SPEC-F), now that the filter is implemented.** The prior draft left `:1694` and `:1695` unreconciled to avoid an asymmetry between two parallel union terms while the code over-approximated. CODE-D implements the "active lease" clause for both pool and user keys at the rebuild site, so SPEC-F now reconciles both bullets together (tightening `:1694`'s "active-or-recent lease" and `:1695`'s "active lease" to the single implemented meaning: a lease not past `ExpiresAt`), keeping the two bullets parallel and matching the code. This is the "reconcile both" option the prior draft's Open decision 1 named.
- **Classified a `fix`-plus-`new` hybrid with spec edits.** The deny-list reachability (CODE-A/B/C) is the `fix` half — behavior already normative under §4.9 that the code failed to implement. The rebuild filter (CODE-D), the `expires_at` column and `DeleteExpired` (CODE-E), and the sweep with deny-entry expiry (CODE-F) are the `new` half: they implement §4.9 bounds (`:1671`, `:1694`/`:1695`) the code never honored, and they add a schema migration, two lease-store methods, a `DenyList.Remove` method, and a background worker. SPEC-E/SPEC-F edit the spec. No new RPC, endpoint, proto field, or CRD field is introduced.
- **Cite durable spec anchors only.** New and reworded comments carry `// spec: §4.9` rather than a coverage-tracker finding id; no finding id appears in spec text, code, test names, comments, or commit messages.

## 3. How the pieces fit at revocation and restart

A revocation writes the source-aware deny-list entry (`{source: user, tenantId, credentialRef}` for a user credential, `{source: pool, poolId, credentialId}` for a pool credential) and, with CODE-A applied, leaves the backing lease in the store. On the next proxy request, any replica resolves the retained lease through `GetByToken` (a hit on the shared Postgres table or on a per-replica store), reaches `DenyList.Revoked(lease.CredentialKey())`, and returns `CREDENTIAL_REVOKED` before the `BudgetGate` and any upstream call. The retained lease is denied on every request until session teardown removes it or it lapses at its natural `ExpiresAt`.

A replica that restarts after a revocation runs the §4.9 startup rebuild (`workers.go`). With CODE-B and CODE-C applied, the rebuild `Reset`s the union of the pool store's revoked `(poolId, credentialId)` rows and the token store's revoked `(tenantId, credentialRef)` rows, so the fresh replica denies a revoked user credential it resolves from the shared lease store rather than failing open. The `Reset` is authoritative and runs once at startup on an empty list, so deny-list entries stay bounded across restarts regardless of how many leases the shared table retains.

## 4. Proposed changes

### CODE-A. Retain the revoked lease on the two credential-revocation paths so the deny-list check is reachable on the shared store

**Target:**

- `pkg/gateway/credentials/usercreds/usercreds.go:256-267` (`Materializer.RevokeUser`), plus the doc comment at `:246-252`.
- `cmd/lenny-gateway/credential_revocation.go:51-66` (`poolCredentialRevoker.RevokePoolCredentials`), plus the doc comments at `:30-39` and `:45-50`.

The §11.4 full_revoke lease revoker (`cmd/lenny-gateway/user_revocation.go:183-190`, `userLeaseRevoker.RevokeUserLeases`) is deliberately not a target. It marks no credential `status = 'revoked'`, so a retained lease there is not re-deniable by CODE-C's rebuild on a restarted replica; deleting its lease keeps the full_revoke path fail-closed on the shared store (§2, §5). Its existing removal assertions in `cmd/lenny-gateway/user_revocation_test.go` stay green and are not touched.

**Anchor and change (`usercreds.go`).** `Materializer.RevokeUser` removes the leases this replica holds:

```go
	leases := m.leases.LeasesByCredential(key)
	for _, lease := range leases {
		m.leases.Remove(lease.LeaseID)
	}
	m.creds.Remove(key)
	return len(leases), nil
```

Delete the `m.leases.Remove(lease.LeaseID)` call (`:263`), keep the `LeasesByCredential` enumeration and the `m.creds.Remove(key)` cached-secret drop and the returned count. Reword the doc comment (`:246-252` "removes the leases this replica holds") to state the lease is retained and denied in place. Retaining the lease while dropping the cached upstream secret is consistent: the proxy denies the request at the deny-list check before it would inject a secret, so the dropped secret is a defense-in-depth backstop rather than the primary reject.

**Anchor and change (`credential_revocation.go`).** `RevokePoolCredentials` removes every lease against each revoked credential:

```go
		p.denyList.Revoke(key)
		for _, lease := range p.leases.LeasesByCredential(key) {
			p.leases.Remove(lease.LeaseID)
			total++
		}
```

Delete the `p.leases.Remove(lease.LeaseID)` call (`:61`) and keep incrementing `total` per enumerated lease, so the returned count remains leases-affected. Reword the doc comments (`:30-39`, `:45-50` "drops the leases this replica holds") to state the lease is retained and shadowed by the deny list. Note that the returned count is now leases-affected (denied) rather than leases-removed; the pool-revocation audit event's `active_leases_terminated` / `leasesTerminated` field keeps counting affected leases. Direct-mode `RotateCredentials` (`directModeRevocationRotator` via the propagator revoke hook, `revocation.go:113-123`) is unchanged and still reads the retained leases via `LeasesByCredential`.

**Rationale.** The proxy resolves the lease before consulting the deny list (`handler.go:319-347`), and under the shared Postgres store `leases.Remove` is a global `DELETE` (`pgstore.go:190-193`), so deleting the lease makes `CREDENTIAL_REVOKED` unreachable on every replica and forces `LEASE_TOKEN_INVALID`. Retaining the lease keeps it resolvable so `DenyList.Revoked(lease.CredentialKey())` fires and yields `CREDENTIAL_REVOKED`, matching the deny-list-shadows-lease-until-TTL design (`spec/04:1671`, `:1687-1688`). Renewal resurrection is prevented by the existing propagator `Revoke` drop (`revocation.go:79-86`, `:152-155`) and by revoked-credential resolution returning not-found (`credentialstore.go:107-109`), so no new gating code is required.

**Paired unit and integration test updates.** Each production path CODE-A changes has an existing test that asserts the lease is dropped from the store after revocation. Once the `leases.Remove` call is deleted the lease is retained, so each drop-the-lease assertion inverts and the suite goes red unless it is retargeted in the same change. The retargeting is specified in TEST-E and the files are listed in §10. The affected assertions are:

- `pkg/gateway/credentials/usercreds/usercreds_test.go:290-296` (`TestRevokeUser_spec_4_9_1351`, "Lease dropped and cached secret evicted on this replica") and `:313-314` (`TestRevokeUser_nilRevoker_spec_4_9_1351`, "RevokeUser must drop the lease even with no revoker"). Retarget the lease assertion to require the lease is retained and resolvable (`GetByID` hits), keep the leases-affected count (`n == 1`), and keep the cached-secret-eviction assertion at `:294-296` unchanged (`RevokeUser` still calls `m.creds.Remove(key)`).
- `cmd/lenny-gateway/credential_revocation_test.go:41-48` (`TestPoolCredentialRevokerRevokesAndDenies`) and `:76-78` (`TestPoolCredentialRevokerPoolWide`, `leases.Len() != 0`). Retarget the drop assertions to require the revoked leases are retained and resolvable while their credential is on the deny list, keep the leases-affected count (`n == 2`) and the untouched-lease assertion for `l3`, and change the pool-wide `leases.Len()` expectation from `0` to the retained count while keeping `deny.Len() == 2`.
- `tests/tier4_integration/credential_lifecycle_test.go:293-296` ("The session's active lease is terminated and the credential denied"), which exercises a reconstructed `lifecycleRevoker` test double (`:88-108`, wired into the admin router at `:257`) that mirrors the production `poolCredentialRevoker` because the production glue lives in package `main`. CODE-A does not touch the double, so first drop the double's own `r.leases.Remove(lease.LeaseID)` call at `:103` while keeping the `total++` increment, mirroring the `poolRevoker` `:78` edit, so it keeps mirroring the post-CODE-A production revoker. Then retarget the `GetByID(v2)` assertion to require the lease is retained while `deny.Revoked(credKey)` stays true (`:297-300`), and keep `summary.LeasesTerminated == 1` (the count is leases-affected).

The §11.4 full_revoke tests in `cmd/lenny-gateway/user_revocation_test.go` are not retargeted: CODE-A does not change `user_revocation.go`, so those removal assertions remain correct.

### CODE-B. Add a revoked-credential listing query to TokenStore (credentialstore.Store) on Memory and pgstore

**Target:**

- `pkg/gateway/credentials/credentialstore/credentialstore.go` (the `Store` interface at `:92-148`, a new `RevokedUserCredential` struct, and the Memory implementation).
- `pkg/gateway/credentials/credentialstore/pgstore/pgstore.go` (the pgstore implementation).

**Change.** Add a struct modeled on `credentialpoolstore.RevokedCredential` (`credentialpoolstore.go:250-262`), anchored `// spec: §4.9`:

```go
// RevokedUserCredential identifies one revoked user credential for the
// §4.9 startup deny-list rebuild. The §4.9 deny list keys a user-backed
// entry on (tenantId, credentialRef).
//
// spec: §4.9 — the startup rebuild's user-credential term.
type RevokedUserCredential struct {
	TenantID      string
	CredentialRef string
}
```

Add to the `Store` interface a method documented as scanning all tenants for the startup deny-list rebuild (the one non-tenant-scoped method), mirroring the pool-store wording at `credentialpoolstore.go:264-283`:

```go
	// RevokedCredentials returns every revoked user credential across
	// all tenants, for the §4.9 startup deny-list rebuild. It is the
	// only method that is not tenant-scoped.
	//
	// spec: §4.9 — a newly started gateway replica rebuilds its deny
	// list from the stores' revoked entries so no revoked credential
	// silently becomes accepted on a replica that missed the original
	// pub/sub notification.
	RevokedCredentials(ctx context.Context) ([]RevokedUserCredential, error)
```

Memory implementation: read-lock, iterate the stored credentials, emit `{TenantID, CredentialRef: Ref}` for every `Credential` whose `Status == StatusRevoked` (`credentialstore.go:37`, `:63`), sorted deterministically by `(TenantID, CredentialRef)` the way the pool Memory implementation sorts (`credentialpoolstore.go:592-600`).

pgstore implementation: scan across all tenants via `pgtenant.InAllTenants` (the pattern the pool pgstore uses at `pgstore.go:301-335`), running `SELECT tenant_id, ref FROM credentials WHERE status = 'revoked' ORDER BY tenant_id, ref` against the `credentials` table (columns confirmed at `pgstore.go:140-141`, `:204-206`; the revoked status is written as `string(StatusRevoked)` = `'revoked'` at `pgstore.go:402-408`). This is a platform-internal read with no per-tenant request scope. Return the collected slice.

**Rationale.** The §4.9 rebuild union's user term (`spec/04:1695`) requires enumerating revoked `(tenantId, credentialRef)` tuples, but `TokenStore` exposes no such query (`credentialstore.go:92-148`), unlike `credentialpoolstore.RevokedCredentials` (`credentialpoolstore.go:274-283`). This is the missing store query defect B needs. The query does not join the lease store, mirroring the shipped pool-store implementation, which returns all revoked rows without the lease-existence join its own spec bullet (`spec/04:1694`) names.

### CODE-C. Seed the user-credential term in the §4.9 deny-list startup rebuild union

**Target:** `cmd/lenny-gateway/workers.go:1421-1453` (the §4.9 credential deny-list startup rebuild block).

**Anchor and change.** The block seeds only the pool term and declares the user term vacuous on a stale premise:

```go
	// pool-credential revocation denies that credential on the upstream
	// path even if it missed the original Redis pub/sub notification. The
	// rebuild is authoritative (Reset) and runs once at startup, where
	// the list is empty; it is not periodic because a periodic Reset
	// would drop the entries the live §11.4 revocation path adds for
	// pool credentials not yet reflected in the store query. The §4.9
	// union's user-credential term is vacuous today — no user-backed
	// lease is minted at session creation yet — so only the
	// pool-credential side is seeded.
	//
	// spec: §4.9 lines 1668-1673.
	{
		revoked, err := w.credentialPools.RevokedCredentials(context.Background())
		if err != nil {
			log.Printf("lenny-gateway: §4.9 credential deny-list startup rebuild failed: %v", err)
		} else {
			keys := make([]credential.CredentialKey, 0, len(revoked))
			for _, rc := range revoked {
				keys = append(keys, credential.CredentialKey{
					Source:       credential.SourcePool,
					PoolID:       rc.PoolName,
					CredentialID: rc.CredentialID,
				})
			}
			w.credDeny.Reset(keys)
			if len(keys) > 0 {
				log.Printf("lenny-gateway: §4.9 credential deny list rebuilt with %d revoked credential(s)", len(keys))
			}
		}
	}
```

Replace the stale comment sentence (the "user-credential term is vacuous today ... only the pool-credential side is seeded" clause and the `// spec: §4.9 lines 1668-1673` line) with a statement that the rebuild seeds both the pool and user terms of the §4.9 union so a replica restarted after either revocation kind rebuilds a complete deny list, anchored `// spec: §4.9`. After building the pool keys and before `Reset`, query the token store and append the user keys into the same slice:

```go
		revokedUsers, uerr := w.credentials.RevokedCredentials(context.Background())
		if uerr != nil {
			log.Printf("lenny-gateway: §4.9 credential deny-list startup rebuild (user term) failed: %v", uerr)
		} else {
			for _, ru := range revokedUsers {
				keys = append(keys, credential.CredentialKey{
					Source:        credential.SourceUser,
					TenantID:      ru.TenantID,
					CredentialRef: ru.CredentialRef,
				})
			}
		}
		w.credDeny.Reset(keys)
```

The token-store field is `w.credentials credentialstore.Store` (`wiring_fields.go:249`). Log-and-continue on the user-term error the same way the pool query does, and call `w.credDeny.Reset(keys)` once on the combined union so the rebuild stays authoritative.

**Rationale.** The rebuild seeds only the pool term and declares the user term vacuous on a stale premise (`workers.go:1430-1432`); `spec/04:1692-1697` mandates a union across both stores so a restarted replica denies a revoked user credential it resolves from the shared lease store. Without CODE-A the omission is latent, because a deleted lease is never resolved on restart either; with CODE-A the retained lease is resolvable, so the missing user term is a fail-open regression on restart.

### SPEC-E. Re-document the revocation `active_leases_terminated` count

**Target:** `spec/04_system-components.md` — the pool-revocation audit event and response summary (`:1674`, `:1675`), the user-revocation endpoint (`:1375`), and the §4.9.2 audit-event catalog rows (`:1764` `credential.user_revoked`, `:1766` `credential.revoked`).

**Change.** CODE-A retains proxy-mode leases and denies them in place rather than deleting them, so a lease counted in `active_leases_terminated` / `leasesTerminated` is no longer necessarily terminated. Add a clarifying sentence at the deny-list section (near `:1674`) and adjust the field glosses so the count is documented as **leases affected** by the revocation: terminated via `RotateCredentials` in direct mode, denied in place via the deny list in proxy mode. Do not rename the field (`active_leases_terminated` stays the wire/audit key; a rename would break every audit consumer); only its description changes. The emergency-runbook step that verifies `leasesTerminated` against active sessions (`:1701` step 3) stays correct because the count still equals affected sessions.

**Rationale:** This revision is what makes "terminated" inaccurate for proxy-mode leases, so the doc drift it introduces is reconciled in the same change rather than deferred (the prior draft's Open decision 2).

### SPEC-F. Reconcile the §4.9 rebuild bullets to "active lease"

**Target:** `spec/04_system-components.md:1694` (pool term) and `:1695` (user term); the deny-list expiry sentence at `:1671`.

**Change.** With CODE-D implementing the lease-existence filter for both terms, tighten `:1694`'s "active-or-recent lease" and confirm `:1695`'s "active lease" to the single implemented meaning — a lease whose `ExpiresAt` is in the future — and state that the rebuild seeds a deny-list entry only for a revoked credential that still has such a lease. Confirm at `:1671` that the deny-list entry expiry ("expire when the credential's natural lease TTL lapses") is realized by the CODE-F sweep, which removes a credential's entry once its last active lease lapses. Both bullets are edited together so they stay parallel union terms.

**Rationale:** The spec text and the shipped code disagreed (the clause was normative but unimplemented). Implementing it (CODE-D) and reconciling both bullets closes the divergence rather than the prior draft's choice to leave both stale.

### CODE-D. Apply the §4.9 lease-existence filter at the rebuild site

**Target:** `cmd/lenny-gateway/workers.go` (the §4.9 deny-list startup rebuild block, `:1421-1453`).

**Change.** After building the union of pool keys (CODE-C) and user keys (from `credentialstore.RevokedCredentials`), filter it to keys that still have an active lease before `Reset`. `workers.go` already holds the lease store as `w.llmLeases` (`:550`). For each candidate key, keep it only if `w.llmLeases.LeasesByCredential(key)` returns at least one lease with `ExpiresAt.After(now)`:

```go
active := keys[:0]
for _, k := range keys {
	for _, l := range w.llmLeases.LeasesByCredential(k) {
		if l.ExpiresAt.After(now) {
			active = append(active, k)
			break
		}
	}
}
w.credDeny.Reset(active)
```

**Rationale:** `RevokedCredentials` (pool and user) returns every revoked credential unboundedly, so an unfiltered `Reset` re-seeds an entry for every ever-revoked credential on each replica start, growing the deny list and the rebuild without bound. The filter seeds only credentials with a live lease, which is the only case the deny list can ever reject (the proxy consults the list only after resolving a lease, `handler.go:319-347`). `LeasesByCredential` is on the `LeaseStore` interface and implemented by both the in-memory `Store` (`credleasestore.go:161`) and the Postgres `pgstore` (`pgstore.go:236`, an indexed `WHERE cred_source/pool_id/credential_id` or `cred_tenant_id/credential_ref` scan), so the filter works against the shared store the multi-replica rebuild reads.

### CODE-E. Add an `expires_at` projection to `credential_leases` and a `DeleteExpired` lease-store method

**Target:** `migrations/0175_credential_leases_expires_at.up.sql` / `.down.sql` (new); `pkg/gateway/credentials/credleasestore/credleasestore.go` (the `LeaseStore` interface and `Store`); `pkg/gateway/credentials/credleasestore/pgstore/pgstore.go` (`Put` and the new method).

**Change.** The lease body is a single envelope-encrypted blob (`pgstore.go:129-163`), so `ExpiresAt` is not queryable in SQL. Add a plain `expires_at timestamptz` column:

```sql
-- 0175_credential_leases_expires_at.up.sql
ALTER TABLE credential_leases ADD COLUMN expires_at timestamptz;
CREATE INDEX credential_leases_expires_at_idx ON credential_leases (expires_at);
```

Populate it in `pgstore.Put` from `lease.ExpiresAt` (add `expires_at` to the `INSERT ... ON CONFLICT DO UPDATE` column list). Add to the `LeaseStore` interface a `DeleteExpired(ctx context.Context, cutoff time.Time) ([]credential.CredentialKey, error)` method that removes rows past `cutoff` and returns the affected credential keys (the plain `cred_source`/`pool_id`/`credential_id`/`cred_tenant_id`/`credential_ref` columns, so no decrypt is needed). The pgstore runs `DELETE FROM credential_leases WHERE expires_at IS NOT NULL AND expires_at < $1 RETURNING cred_source, pool_id, credential_id, cred_tenant_id, credential_ref`; the in-memory `Store` iterates `byID` and drops leases with `ExpiresAt.Before(cutoff)`, mirroring `Remove`'s token-index cleanup. **Backfill:** rows written before the migration have a NULL `expires_at` and are skipped by the sweep; a one-time startup backfill decrypts each NULL-`expires_at` row and sets the column (or deletes the row if already expired), so pre-migration expired rows do not linger indefinitely. Active leases re-populate `expires_at` on their next renewal `Put` regardless.

**Rationale:** A plain `expires_at` column is the minimal enabler for an indexed SQL sweep and keeps `DeleteExpired` from decrypting every row. Returning the credential keys lets CODE-F expire the corresponding deny-list entries without a second query.

### CODE-F. Add `DenyList.Remove` and a bounded expired-lease sweep worker

**Target:** `pkg/gateway/credentials/denylist/denylist.go` (`Remove`); `cmd/lenny-gateway/workers.go` (a new sweep worker beside the §11.5 idempotency GC at `:1455`); a `--credential-lease-gc-interval-seconds` flag (default `3600`).

**Change.** Add `Remove(key credential.CredentialKey)` to `DenyList` (deletes the key under the write lock, the inverse of `Revoke`). Add a periodic worker that, every interval, calls `w.llmLeases.DeleteExpired(ctx, now)` and, for each returned credential key, calls `w.credDeny.Remove(key)` only if `w.llmLeases.LeasesByCredential(key)` is now empty (a credential with a still-active lease keeps its deny entry). Emit `lenny_gateway_credential_leases_swept_total` and log the count, matching the idempotency-GC cadence and the `pgPool != nil` guard for the durable path (the in-memory store sweeps unconditionally).

**Rationale:** This realizes the `spec/04:1671` promise that deny-list entries "expire when the credential's natural lease TTL lapses" and bounds the `credential_leases` table. Removing the deny entry only when the last active lease lapses is safe: while any active lease for a revoked credential remains, the entry must stay so the proxy keeps rejecting it; once none remains, no request can resolve to it and the entry is inert, so removing it cannot let a revoked credential through. The sweep is the single writer that shrinks the in-memory deny list within a replica's lifetime, complementing the authoritative startup `Reset`.

### TEST-E. Retarget the tier-4 deny-list test to the shared-store topology and add the restart-rebuild path; unit-test the new store query and the rebuild union

**Target:**

- `tests/tier4_integration/credential_denylist_proxy_test.go` (`TestUserCredentialRevocationDenyListProxy`).
- `tests/tier4_integration/credential_pool_revocation_propagation_test.go` (`TestPoolCredentialEmergencyRevocationPropagatesAcrossReplicas` and its reconstructed `poolRevoker`), for the pool-credential shared-store case.
- `pkg/gateway/credentials/usercreds/usercreds_test.go` (`TestRevokeUser_spec_4_9_1351`, `TestRevokeUser_nilRevoker_spec_4_9_1351`) and `cmd/lenny-gateway/credential_revocation_test.go` (`TestPoolCredentialRevokerRevokesAndDenies`, `TestPoolCredentialRevokerPoolWide`) and `tests/tier4_integration/credential_lifecycle_test.go`, retargeted from drop-the-lease to retained-and-denied per the CODE-A paired-test note.
- `pkg/gateway/credentials/credentialstore/credentialstore_test.go` and the pgstore package (`RevokedCredentials` unit and pg tests).
- A focused `cmd/lenny-gateway` rebuild-union test.

**Change.** Rewrite the pub/sub-propagation case to share one `credleasestore` across the revoke endpoint and the proxy `Handler`, dropping the `leasesOrigin` / `leasesPeer` split at `:94` and `:126-130` and wiring `handler.Leases` and the materializer to the same store. After the revoke, assert that a proxy request returns `http.StatusForbidden` with body code `CREDENTIAL_REVOKED` (not `401 LEASE_TOKEN_INVALID`) and that the upstream stub recorded no call after revoke, proving CODE-A against the topology that exposed the defect. Keep the pre-revocation 200 assertion.

Add a restart-rebuild sub-test: revoke the user credential, construct a fresh replica deny list seeded only by the rebuild union (call the new `credentialstore.RevokedCredentials` and build `SourceUser` keys exactly as CODE-C does, with an empty pool store) against the same shared lease store, and assert `CREDENTIAL_REVOKED`, proving CODE-B and CODE-C and that a fresh replica does not fail open.

Keep the name `TestUserCredentialRevocationDenyListProxy` so the spec-named test (`spec/04:1699`) exists and passes against the shared store.

**Pool-credential shared-store case (CODE-A, pool path).** The existing `tests/tier4_integration/credential_pool_revocation_propagation_test.go` gives each modeled replica its own lease store (`credleasestore.New()` at `:99`) and seeds the peer with an independent copy of the lease (`peer.leases.Put(lease)` at `:194`), so the origin's global `leases.Remove` never touches the row the peer proxy reads. That is the same two-independent-store pattern that made the user test pass spuriously, so it exercises neither the shared-store pool path nor the pool-path CODE-A fix. Add a case (or a sub-test) that shares one `credleasestore` between the emergency-revocation admin endpoint and the proxy `Handler` and asserts that a post-revocation proxy request on a pool-backed lease returns `http.StatusForbidden` with body code `CREDENTIAL_REVOKED` (not `401 LEASE_TOKEN_INVALID`) and that the upstream stub recorded no call after revoke. The pool path produces a distinct `{source: pool, poolId, credentialId}` deny key and pool-backed leases carry a different `CredentialKey()` shape, so the user-path case does not cover it. Update the test's reconstructed `poolRevoker` (`:68-83`) to drop the `p.leases.Remove(lease.LeaseID)` call at `:78` so it keeps mirroring the production `poolCredentialRevoker` after CODE-A; keep incrementing `total` so the returned count stays leases-affected. Anchor the new case `// spec: §4.9`.

**Retarget the drop-the-lease unit and integration assertions (CODE-A).** As specified in the CODE-A paired-test note, retarget `usercreds_test.go:290-296` and `:313-314`, `credential_revocation_test.go:41-48` and `:76-78`, and `credential_lifecycle_test.go:293-300` from asserting the lease is dropped to asserting the lease is retained and resolvable while its credential is on the deny list. In `credential_lifecycle_test.go`, first drop the reconstructed `lifecycleRevoker` double's own `r.leases.Remove(lease.LeaseID)` call at `:103` (keeping the `total++` increment) so the double keeps mirroring the post-CODE-A production `poolCredentialRevoker`, otherwise the double still removes the lease and the retained-lease assertion at `:294` fails. Keep every leases-affected count assertion (`n == 1`, `n == 2`, `summary.LeasesTerminated == 1`) and the cached-secret-eviction assertion at `usercreds_test.go:294-296`. Anchor each retargeted assertion with `// spec: §4.9`. The §11.4 full_revoke tests in `cmd/lenny-gateway/user_revocation_test.go` stay unchanged because CODE-A does not touch `user_revocation.go`.

Add Memory and pgstore unit tests for `RevokedCredentials` (revoked rows returned, active rows excluded, cross-tenant scan, deterministic order), mirroring the pool-store `RevokedCredentials` tests. Anchor all new tests with `// spec: §4.9` comments only.

**Rationale.** The existing user-path test passes only because it uses two independent in-memory lease stores so the modeled peer retains the lease (`credential_denylist_proxy_test.go:94`, `:126-130`); it exercises neither the shared-store path nor the restart rebuild the spec names (`spec/04:1699`). The existing pool-path test (`credential_pool_revocation_propagation_test.go`) uses the same two-independent-store pattern (`:99`, `:194`), so it too passes with or without the pool-path fix and its reconstructed `poolRevoker` (`:68-83`) would silently drift from production once CODE-A drops the production `Remove`. Both the user and pool paths change a fail-closed `SECURITY` control, so both are verified against the shared-store topology that exposed the defects, and every existing removal assertion the change inverts is retargeted in the same change so the suite stays green.

## 5. Non-goals

- **The store-scoped `RevokedCredentials` queries stay unfiltered; the lease-existence bound is applied at the rebuild site (CODE-D).** The pool and user `RevokedCredentials` store methods keep returning every revoked credential (they hold no lease index); the "active lease" filter is applied once, in `workers.go`, against `w.llmLeases`. This keeps the store queries simple and avoids a cross-store join inside a store method.
- **The sweep removes expired lease rows; it does not add a soft-delete or tombstone.** CODE-E adds a plain `expires_at` projection column and CODE-F hard-deletes past-`ExpiresAt` rows. No soft-delete flag, tombstone row, or lease-state machine is introduced.
- **No change to direct-mode revocation behavior.** The `RotateCredentials` push (`directModeRevocationRotator`) is unchanged; only proxy-mode deny-list reachability is fixed. Direct mode still reads the retained leases via `LeasesByCredential`.
- **No new RPC, endpoint, proto field, or CRD field.** The change adds one plain projection column, two lease-store methods, a `DenyList.Remove`, a background worker, and two spec-text reconciliations.
- **No change to the §11.4 full_revoke lease revoker.** `userLeaseRevoker.RevokeUserLeases` (`user_revocation.go:183-190`) continues to remove its leases. The full_revoke fan-out marks no credential `status = 'revoked'` (`users_invalidate.go:145-197`), so a retained lease there is not re-deniable from either store on restart; deleting the lease keeps the full_revoke path fail-closed on the shared store (a post-revocation request resolves no lease and is rejected `LEASE_TOKEN_INVALID` on every replica and across restarts). Making full_revoke credentials re-deniable would require durably revoking both the user credential and the pool credentials held by the user's pool-backed sessions, which is a larger change out of scope here.
- **No termination of a revoked user's sessions.** Session lifecycle is unchanged; the retained lease is denied on every request and cleaned up at the session's existing teardown.

## 6. Testing

The change reaches tier 0 and tier 1 for the touched packages plus tier 4 per `.claude/rules/test-coverage.md`: tier 1 for the new `credentialstore.RevokedCredentials` store query and the rebuild-union seed, and tier 4 for the multi-replica shared-store proxy and restart-rebuild flow. Each test pins one behavior the change introduces or a fail-open it closes and asserts the non-happy path.

- **tier-4 shared-store proxy reachability (spec-named-failure path, CODE-A):** In `tests/tier4_integration/credential_denylist_proxy_test.go`, share one `credleasestore` across the revoke endpoint and the proxy `Handler` (dropping the `leasesOrigin`/`leasesPeer` split at `:94`, `:126-130`). Assert that after `POST /v1/credentials/{ref}/revoke` a proxy request returns `http.StatusForbidden` with body code `CREDENTIAL_REVOKED` rather than `401 LEASE_TOKEN_INVALID`, and that the upstream stub recorded no call after revoke. The non-happy path is a post-revocation request that resolves no lease under the shared `DELETE` and returns `LEASE_TOKEN_INVALID`, so the deny-list entry is never consulted and the proxy silently degrades the reject category from `SECURITY` to an auth miss. Keep the pre-revocation 200 assertion and the name `TestUserCredentialRevocationDenyListProxy`. `// spec: §4.9 (CREDENTIAL_REVOKED reachable under the shared lease store; deny-list shadows a retained lease).`
- **tier-4 shared-store proxy reachability, pool path (CODE-A pool path):** In `tests/tier4_integration/credential_pool_revocation_propagation_test.go`, add a case that shares one `credleasestore` between the emergency-revocation admin endpoint and the proxy `Handler` (replacing the two-independent-store split at `:99` and `:194` for this case), and update the reconstructed `poolRevoker` at `:68-83` to drop the `p.leases.Remove` call at `:78` so it mirrors the production `poolCredentialRevoker` after CODE-A. Assert that after `POST /v1/admin/credential-pools/{name}/credentials/{credId}/revoke` a proxy request on a pool-backed lease returns `http.StatusForbidden` with body code `CREDENTIAL_REVOKED` rather than `401 LEASE_TOKEN_INVALID`, and that the upstream stub recorded no call after revoke. The non-happy path is a pool-path global `DELETE` that removes the lease the peer proxy reads, so a post-revocation request resolves no lease and returns `LEASE_TOKEN_INVALID`, degrading the `SECURITY` reject to an auth miss; the pool path is distinct from the user path because it produces a `{source: pool, poolId, credentialId}` deny key and pool-backed leases carry a different `CredentialKey()`. `// spec: §4.9 (CREDENTIAL_REVOKED reachable for a pool-backed lease under the shared store).`
- **tier-0/tier-1 retarget of the drop-the-lease assertions (CODE-A):** Retarget the existing removal assertions the change inverts so the suite stays green: `usercreds_test.go` (`TestRevokeUser_spec_4_9_1351`, `TestRevokeUser_nilRevoker_spec_4_9_1351`), `credential_revocation_test.go` (`TestPoolCredentialRevokerRevokesAndDenies`, `TestPoolCredentialRevokerPoolWide`), and `credential_lifecycle_test.go` (which also drops the reconstructed `lifecycleRevoker` double's `r.leases.Remove` call at `:103` so the double mirrors the post-CODE-A production revoker). Each now asserts the revoked lease is retained and resolvable while its credential is on the deny list, keeping the leases-affected count and the cached-secret-eviction assertion. The non-happy path is a retained-lease regression assertion left as drop-the-lease, which would fail against the retaining production paths. `// spec: §4.9 (revocation retains the lease and denies it in place).`
- **tier-4 restart-rebuild does not fail open (spec-named-failure path, CODE-B/CODE-C):** In the same test, add a sub-test that revokes the user credential, then constructs a fresh replica deny list seeded only by the rebuild union (calling `credentialstore.RevokedCredentials` and building `SourceUser` keys exactly as CODE-C does, with an empty pool store) against the same shared lease store, and asserts a proxy request is rejected `CREDENTIAL_REVOKED`. The non-happy path is a fresh replica that rebuilds a pool-only deny list, resolves the retained lease, misses the absent user entry, and accepts the revoked credential on the upstream path until TTL lapses. `// spec: §4.9 (startup rebuild union across both stores; a restarted replica denies a revoked user credential).`
- **tier-1 `credentialstore.RevokedCredentials` on Memory (boundary and empty path, CODE-B):** In `pkg/gateway/credentials/credentialstore/credentialstore_test.go`, assert Memory returns exactly the revoked `(tenantId, ref)` tuples, excludes active rows, scans across multiple tenants, and returns a deterministic `(TenantID, CredentialRef)` order; assert an empty store returns an empty slice and no error. The non-happy path is an active credential wrongly listed (a false deny) or a revoked credential in a second tenant missed (the cross-tenant scan regressing to tenant-scoped). Mirror the pool-store `RevokedCredentials` tests. `// spec: §4.9 (revoked-credential listing for the startup rebuild).`
- **tier-1 `credentialstore.RevokedCredentials` on pgstore (boundary path, CODE-B):** In the pgstore test package, assert the `InAllTenants` scan returns revoked rows across tenants in `tenant_id, ref` order, excludes `active` rows, and returns empty when no credential is revoked. The non-happy path is a query that runs inside a single tenant scope and misses another tenant's revoked credential, or one that lists active rows. `// spec: §4.9 (cross-tenant revoked-credential scan, platform-internal read).`
- **tier-1 rebuild-union seeds both terms (boundary path, CODE-C):** In a focused `cmd/lenny-gateway` test, seed the pool store with one revoked pool credential and the token store with one revoked user credential, run the rebuild block, and assert `credDeny` denies both `{source: pool, ...}` and `{source: user, ...}` keys after one `Reset`. Assert that a pool-store-only revocation still seeds the pool key and that a token-store-only revocation seeds the user key, so neither term shadows the other. The non-happy path is a rebuild that seeds only the pool term (the stale-comment behavior) and leaves the user key absent, or one that calls `Reset` twice and drops the first term. `// spec: §4.9 (both terms of the rebuild union seeded in one authoritative Reset).`
- **tier-1 rebuild lease-existence filter bounds the deny list (boundary/empty path, CODE-D):** In the same rebuild-union test, seed one revoked credential that has an active lease in `w.llmLeases` and one revoked credential whose only lease is past `ExpiresAt` (or absent), run the rebuild, and assert `credDeny` denies the first key and does NOT seed the second. Assert an all-expired-lease set produces an empty `Reset`. The non-happy path is the unbounded behavior: a rebuild that seeds an entry for every revoked credential regardless of lease existence, growing the deny list without bound. `// spec: §4.9 (rebuild seeds only revoked credentials with an active lease, spec/04:1694-1695).`
- **tier-1 / tier-2 `DeleteExpired` and the `expires_at` projection (boundary path, CODE-E):** In `credleasestore` Memory and pgstore tests, `Put` a lease and assert `expires_at` reflects `lease.ExpiresAt` (pgstore: the plain column; Memory: the struct), then `DeleteExpired(now)` removes a past-`ExpiresAt` lease and its token index, leaves a future-`ExpiresAt` lease, and returns exactly the removed credential keys. Assert a NULL-`expires_at` (pre-migration) row is skipped by the SQL delete and handled by the one-time backfill. The non-happy path is a sweep that deletes an active lease (`ExpiresAt` in the future) or fails to return the removed key so its deny entry leaks. `// spec: §4.9 (bounded expired-lease sweep; expires_at projection).`
- **tier-1 `DenyList.Remove` and sweep-driven deny expiry (boundary path, CODE-F):** In `denylist_test.go`, assert `Remove` deletes a key and `Revoked` returns false after. In a `cmd/lenny-gateway` sweep-worker test, revoke a credential (deny entry present), expire its only lease, run one sweep tick, and assert the lease row is deleted and the deny entry is removed; then assert that a credential with one expired and one still-active lease keeps its deny entry after the sweep (removed only when the last active lease lapses). The non-happy path is a sweep that removes a deny entry while an active lease for the same revoked credential still resolves, opening a `CREDENTIAL_REVOKED` bypass. `// spec: §4.9 (deny entries expire when the credential's last lease lapses, spec/04:1671).`
- **tier-11 audit-field consistency (SPEC-E):** Assert the §4.9 / §4.9.2 text describes `active_leases_terminated` / `leasesTerminated` as leases-affected (terminated in direct mode, denied in place in proxy mode) and does not describe proxy-mode revocation as removing the lease, matching CODE-A's retain-and-deny behavior. The non-happy path is spec text that still says proxy-mode revocation terminates/removes the lease. `// spec: §4.9 (active_leases_terminated counts affected leases).`

## 7. Findings closed on application

Applying CODE-A through CODE-F, SPEC-E/SPEC-F, and TEST-E closes the three defects §1 records: `CREDENTIAL_REVOKED` becomes reachable under the shared Postgres lease store (CODE-A retains the lease so the deny-list check runs); the §4.9 startup rebuild seeds both terms of the two-store union (CODE-B adds the missing `TokenStore` query, CODE-C wires it into the rebuild) so a restarted replica does not fail open on a revoked user credential; and the retention gaps defect (C) records are bounded (CODE-D filters the rebuild to credentials with an active lease, CODE-E/CODE-F add the `expires_at` projection, `DeleteExpired`, `DenyList.Remove`, and the sweep worker that expires deny entries when a credential's last lease lapses), with SPEC-E/SPEC-F reconciling the audit-count wording and the `:1671`/`:1694`/`:1695` spec text to the implemented behavior. CODE-A retains the lease on the two credential-revocation paths whose backing credential is durably marked `status = 'revoked'`; the §11.4 full_revoke path keeps removing its lease so it stays fail-closed on restart (§2, §5). TEST-E retargets the spec-named `TestUserCredentialRevocationDenyListProxy` (`spec/04:1699`) to the shared-store topology, adds a pool-credential shared-store case in `credential_pool_revocation_propagation_test.go`, and adds the restart-rebuild path, so both defects are verified against the topology that exposed them on both the user and pool paths. The existing removal assertions the change inverts are retargeted in the same change so the suite stays green. No coverage-tracker finding id is cited in code, tests, or comments.

## 8. Resolved in adversarial review

Subsequent adversarial review rounds populate this section. The drafting pass applied the following convergence revisions before first review:

- **SPEC-D (reconcile `spec/04:1695`) dropped in favor of a zero-spec-edit change.** The initial draft reconciled the `TokenStore` rebuild bullet's "active lease" clause to the store-scoped query CODE-B runs. Review found the edit both unnecessary and internally incoherent: the parallel `CredentialPoolStore` bullet (`spec/04:1694`) already carries the same clause while its shipped implementation ignores it, so the over-approximation is the canonical tolerated pattern, and editing only `:1695` while deferring `:1694` manufactures an asymmetry between two bullets meant to read as parallel union terms. CODE-B now mirrors the pool store's code exactly and leaves the spec unchanged; the reconciliation is recorded as a dropped alternative in Non-goals, and whether to reconcile both bullets together is deferred to the reviewer (Open decision 1).
- **CODE-A and CODE-B/CODE-C land as one change set.** Review confirmed each defect masks the other: a deleted lease is neither resolvable for the deny-list check (defect A) nor for the restart rebuild (defect B), so fixing A alone would open the restart fail-open and fixing B alone would leave `CREDENTIAL_REVOKED` unreachable. The two are folded into one change set.

### Pass 1 (2026-07-14, automated)

- **CODE-A retargeted to add the existing removal-asserting tests it inverts.** Each production path CODE-A changes has an existing test that asserts the lease is dropped after revocation (`usercreds_test.go:290-296` and `:313-314`, `credential_revocation_test.go:41-48` and `:76-78`, `credential_lifecycle_test.go:293-300`). Deleting the `leases.Remove` call retains the lease, so each assertion inverts and the suite would go red. CODE-A's paired-test note, TEST-E, and §10 now list these files and retarget each drop-the-lease assertion to require the lease is retained and resolvable while its credential is on the deny list, keeping every leases-affected count and the cached-secret-eviction assertion at `usercreds_test.go:294-296`, each tied to `// spec: §4.9`.
- **CODE-A scoped off the §11.4 full_revoke path to avoid a restart fail-open.** The full_revoke lease revoker (`user_revocation.go:183-190`) revokes the user rather than a credential, and no code on the full_revoke fan-out marks any credential `status = 'revoked'` (`users_invalidate.go:145-197`). Retaining its lease would let a replica restarted after a full_revoke resolve the retained lease from the shared store, rebuild a deny list that omits the still-`active` credential, and reach upstream. CODE-A now applies only to the two credential-revocation paths whose backing credential is durably marked revoked (`usercreds.go` via `credentialserver.go:313`, `:323`; `credential_revocation.go` via the pool store at `:13-14`); `user_revocation.go` keeps removing its lease so the full_revoke path stays fail-closed on the shared store. The scope reduction propagates through the scope summary, §1(A), §2, §4 CODE-A, §5 (new non-goal, "three" corrected to "two"), §7, and §10, and `user_revocation_test.go` is left unchanged.
- **Added a pool-credential shared-store test; fixed the propagation test's production drift.** No listed test covered pool-credential `CREDENTIAL_REVOKED` reachability under a shared lease store, and the existing `credential_pool_revocation_propagation_test.go` uses two independent lease stores (`:99`, `:194`) so it passes with or without the pool-path fix, while its reconstructed `poolRevoker` (`:68-83`) still calls `p.leases.Remove` at `:78` and would drift from production once CODE-A drops that call. TEST-E, §6, and §10 now add a shared-store pool case that asserts `403 CREDENTIAL_REVOKED` (not `401 LEASE_TOKEN_INVALID`) with no upstream call on a pool-backed lease, and update the reconstructed `poolRevoker` to drop the `Remove` call so it keeps mirroring production, tied to `// spec: §4.9`.

### Pass 2 (2026-07-14, automated)

- **Corrected the `credential_lifecycle_test.go` retarget: the double it drives, and the missing `Remove` drop.** The CODE-A paired-test note claimed the test "drives the production `poolCredentialRevoker` through the admin revoke endpoint." The test instead wires a reconstructed `lifecycleRevoker` double (`credential_lifecycle_test.go:88-108`, wired at `:257`) that mirrors the production revoker because the production glue lives in package `main`. CODE-A deletes only the production `p.leases.Remove` call in `cmd/lenny-gateway/credential_revocation.go`, so the double at `:103` still called `r.leases.Remove(lease.LeaseID)`; the retargeted retained-lease assertion at `:294` would then fail and leave the suite red. The CODE-A paired-test note (§4), TEST-E (§6), and the files-touched list (§10) now instruct dropping the double's own `r.leases.Remove` call at `:103` while keeping the `total++` increment, mirroring the `poolRevoker` `:78` edit already specified for `credential_pool_revocation_propagation_test.go`, and the citation is corrected to state the test drives a reconstructed `lifecycleRevoker` double rather than the production revoker.

### Revision (2026-07-16, manual scope expansion)

This revision supersedes the drafting-pass decision above to keep 0037 a zero-spec-edit change and the three deferrals in the prior Open-decisions section. Triple-checking the earlier "an entry with no live lease denies nothing… negligible" claim showed the deny-list entries, while inert, are re-seeded on every replica start and grow without bound (the `DenyList` has no TTL/prune and `RevokedCredentials` is unbounded), so the `spec/04:1694`/`:1695` "active lease" clause and the `:1671` TTL-expiry are real bounds the code never implemented. The revision therefore adds CODE-D (the rebuild lease-existence filter), CODE-E (the `expires_at` projection and `DeleteExpired`), CODE-F (`DenyList.Remove` and the sweep worker), SPEC-E (the `active_leases_terminated` reword), and SPEC-F (reconcile both rebuild bullets — the "reconcile both together" option the prior draft deferred). §1 gains defect (C), and the "zero spec edits" and "GC out of scope" framing is removed. The adversarial re-review that follows validates these edits.

## 9. Open decisions for review

All three prior open decisions are now resolved in this revision:

- **Rebuild-bullet reconciliation — resolved (reconcile both, SPEC-F).** CODE-D implements the "active lease" clause for both pool and user keys, so SPEC-F reconciles both `:1694` and `:1695` to the single implemented meaning, keeping them parallel. The remaining reviewer choice is the definition of "active": this proposal uses `ExpiresAt > now` (a lease not past its deadline); whether to also include a short "recent"/grace window before a just-expired lease's row is swept is a one-constant choice tied to the CODE-F sweep cadence.
- **Audit-field re-documentation — resolved (SPEC-E).** `active_leases_terminated` / `leasesTerminated` is reworded to leases-affected (terminated in direct mode, denied in place in proxy mode). The field is not renamed, so the audit/wire contract is unchanged.
- **Bounded lease-TTL sweep — resolved (CODE-E/CODE-F).** The sweep and deny-entry expiry are implemented rather than deferred. The remaining reviewer choices are the default GC interval (`3600s`) and whether the one-time backfill of pre-migration NULL-`expires_at` rows should run at startup (as staged) or as a separate offline job.

## 10. Files touched on application

- `pkg/gateway/credentials/usercreds/usercreds.go`: CODE-A (delete the `m.leases.Remove` call at `:263`; keep the cached-secret drop and the count; reword the `:246-252` doc comment).
- `cmd/lenny-gateway/credential_revocation.go`: CODE-A (delete the `p.leases.Remove` call at `:61`; keep incrementing `total`; reword the `:30-39` and `:45-50` doc comments; note the count is now leases-affected).
- `pkg/gateway/credentials/credentialstore/credentialstore.go`: CODE-B (add the `RevokedUserCredential` struct, the `Store.RevokedCredentials` interface method, and the Memory implementation with deterministic order).
- `pkg/gateway/credentials/credentialstore/pgstore/pgstore.go`: CODE-B (add the `RevokedCredentials` implementation, an `InAllTenants` scan of `SELECT tenant_id, ref FROM credentials WHERE status = 'revoked' ORDER BY tenant_id, ref`).
- `cmd/lenny-gateway/workers.go`: CODE-C (query `w.credentials.RevokedCredentials`, append `SourceUser` keys to the pool keys, `Reset` the union once; replace the stale vacuous-user-term comment at `:1428-1432` and update the `// spec:` anchor to the durable `// spec: §4.9` form).
- New and extended tests: `tests/tier4_integration/credential_denylist_proxy_test.go` (shared-store `TestUserCredentialRevocationDenyListProxy` plus the restart-rebuild sub-test); `tests/tier4_integration/credential_pool_revocation_propagation_test.go` (add the pool-credential shared-store case and drop `p.leases.Remove` at `:78` in the reconstructed `poolRevoker` so it mirrors production); `pkg/gateway/credentials/usercreds/usercreds_test.go`, `cmd/lenny-gateway/credential_revocation_test.go`, and `tests/tier4_integration/credential_lifecycle_test.go` (retarget the drop-the-lease assertions to retained-and-denied per the CODE-A paired-test note, keeping the leases-affected counts and the cached-secret-eviction assertion, and drop the reconstructed `lifecycleRevoker` double's `r.leases.Remove` call at `:103` so it mirrors the post-CODE-A production revoker); `pkg/gateway/credentials/credentialstore/credentialstore_test.go` and the pgstore test package (Memory and pgstore `RevokedCredentials` unit tests); and a focused `cmd/lenny-gateway` rebuild-union test (both terms seeded in one `Reset`), per Section 6.
- `cmd/lenny-gateway/user_revocation.go` and `cmd/lenny-gateway/user_revocation_test.go` are deliberately unchanged: the §11.4 full_revoke lease revoker keeps removing its leases (§2, §5), so its existing removal assertions stay correct.
- `cmd/lenny-gateway/workers.go`: CODE-D (filter the rebuild union to keys with an active lease via `w.llmLeases.LeasesByCredential`) and CODE-F (the periodic expired-lease sweep worker beside the §11.5 idempotency GC at `:1455`, plus the `--credential-lease-gc-interval-seconds` flag and the `lenny_gateway_credential_leases_swept_total` metric).
- `migrations/0175_credential_leases_expires_at.up.sql` / `.down.sql`: CODE-E (add and drop the `expires_at timestamptz` column and its index).
- `pkg/gateway/credentials/credleasestore/credleasestore.go` and `pkg/gateway/credentials/credleasestore/pgstore/pgstore.go`: CODE-E (the `expires_at` projection on `Put`, the `DeleteExpired(ctx, cutoff)` `LeaseStore` method on both backends, and the one-time NULL-`expires_at` backfill).
- `pkg/gateway/credentials/denylist/denylist.go`: CODE-F (the `Remove(key)` method).
- `spec/04_system-components.md`: SPEC-E (reword `active_leases_terminated` / `leasesTerminated` at `:1674`/`:1675`/`:1375` and the §4.9.2 catalog rows `:1764`/`:1766`) and SPEC-F (reconcile the rebuild bullets `:1694`/`:1695` to "active lease" and confirm the `:1671` deny-entry expiry is realized by the sweep).
- New and extended tests for CODE-D/E/F and SPEC-E: the rebuild lease-existence filter case, `credleasestore` Memory/pgstore `DeleteExpired` and `expires_at` tests, `denylist_test.go` `Remove`, a `cmd/lenny-gateway` sweep-worker test, and a tier-11 audit-field consistency assertion (per Section 6).
