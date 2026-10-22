# Non-spec changes: Data lifecycle second half: remaining erasure stores, Phase 3 lease revocation, audit-receipt foreign key, and full_revoke

## Design (implementation-facing)

The work splits into the two lanes of summary D20.

### Store table

The **Store table** is the only rule site for which eraser each path wires under each name, at which rank, over which tree method, and for which name a path records an exemption instead. `canonicalRank`, the two exemption maps, `buildTenantErasers`, the user eraser list in `buildAdminRouter`, and `buildSessionScopedErasers` each implement it. Change a row here first, then every one of those sites.

Column rules:

- **Rank** is the `canonicalRank` value. Ranks follow the §12.8 tenant Phase 4 order.
- **Tenant path** is the Phase 4 entry. "Session-keyed" means the entry runs its function for each session `forEachTenantSession` lists (CODE-3 item 1). "nil when …" means the entry is listed by name with a nil function in that posture (CODE-1 item 4).
- **User path** is the entry in the user eraser list or in `buildSessionScopedErasers`. "Session-scoped" means a `SessionEraser`. "Exempt" means the name is in `userExempt` with the reason given.
- "Unchanged" means the entry already exists at c9c706207 and keeps its function.

| Name | Rank | §12.8 role | Tenant path (Phase 4) | User path |
|:--|:--|:--|:--|:--|
| `leases` | 10 | `LeaseStore` | Unchanged. | Unchanged user-scoped `leasestore` `DeleteByUser`, which returns 0. CODE-4 corrects its comment. |
| `coordination_mirror` | 15 | `LeaseStore` Postgres mirror (§10.1.8) | `coordlease/pgstore` `DeleteByTenant`. nil without Postgres. | Session-scoped adapter over `coordlease/pgstore` `DeleteByUser(ctx, t, "", []string{sid})`. nil without Postgres. |
| `semantic_cache` | 20 | `SemanticCache` | `DeleteByTenant` on the erasure semantic cache, adapted with `countless`. nil when `--llm-semantic-cache` is off. | Unchanged. |
| `redis_caches` | 30 | Redis caches | Exempt: no production writer (`tokencache.Cache` is constructed and discarded in the `lenny-token-service` wiring, and no production package imports `routingcache`). | Exempt with the same reason. |
| `experiment_sticky` | 40 | Experiment sticky assignment cache | `experimentsticky.RedisCache.DeleteByTenant` (CODE-2). nil without Redis. | Unchanged. |
| `billing_buffer` | 50 | Billing write-ahead buffer | `StreamTier.PurgeTenant` on the wiring's billing stream (CODE-2), gated by the exempt check of CODE-3 item 3. | Unchanged. |
| `quota` | 60 | `QuotaStore` | Unchanged. | Unchanged. |
| `artifacts` | 70 | `ArtifactStore` | `DeleteByTenant` on `w.blobs`, the prefix delete, under CODE-3 item 10. | Unchanged session-scoped `DeleteBySession`. nil when the store does not implement `DeleteBySession`. |
| `checkpoint_manifests` | 71 | `ArtifactStore` checkpoints | Unchanged. | Session-scoped adapter over `partialmanifeststore` `DeleteByUser(ctx, t, "", []string{sid})`. nil when the store is absent. |
| `checkpoint_retention` | 72 | `ArtifactStore` checkpoints | Unchanged. | Session-scoped adapter over `checkpointretention` `DeleteByUser(ctx, t, "", []string{sid})`. nil when the store is absent. |
| `checkpoint_meta` | 73 | `ArtifactStore` checkpoints | Unchanged. nil without Postgres. | Session-scoped adapter over `sessioncheckpointmeta/pgstore` `DeleteByUser(ctx, t, "", []string{sid})`. nil without Postgres. |
| `transcripts` | 75 | `ArtifactStore` transcripts | Unchanged. | Unchanged. |
| `interactions` | 78 | Session interaction records | Unchanged. | Unchanged. |
| `memory` | 80 | `MemoryStore` | Unchanged. | Unchanged. |
| `eviction_state` | 90 | `EvictionStateStore` | Exempt: no production writer (`evictionstatestore` and its pgstore are imported only by `evictionfallback`, which no production package imports). | Exempt with the same reason. |
| `session_dlq_archive` | 100 | `session_dlq_archive` | Exempt: `ON DELETE CASCADE` from `sessions`. | Exempt with the same reason. |
| `session_message_queues` | 105 | Session message queues | Session-keyed `messagingCoord.EraseSession`. nil when the coordinator is nil. | Unchanged. |
| `session_tree_archive` | 110 | `session_tree_archive` | Unchanged. | Unchanged. |
| `audit` | 130 | `EventStore` (audit) | `auditstore.DeleteByTenant`, which lands with or after MIG-1. For `jwtaudit.PlatformTenantID` the entry returns `(0, nil)` without calling it, because that ID's rows are the platform chain, and Part A does not wait for CODE-6 item 7's reservation. | User-scoped `auditstore.DeleteByUser`, the step-13 no-op. |
| `billing` | 150 | `EventStore` (billing) | `failover.Pipeline.DeleteByTenant` (CODE-2), gated by the exempt check of CODE-3 item 3. | Exempt: the erasure job's billing phase pseudonymizes billing events (`erasurejob.BillingEraser`, §12.8 step 15). |
| `eval_results` | 155 | `EvalResultStore` | Unchanged entry at the new rank. | Unchanged. |
| `delegation_budget` | 160 | Delegation budget keys | Session-keyed `treebudget.PurgeRoot` for each root session (CODE-3 item 4), then the `delegationbudget` pgstore `DeleteByTenant`. nil without the tree budget. | Unchanged. |
| `sessions` | 170 | `SessionStore` | Unchanged. | Unchanged. |
| `tokens` | 180 | `TokenStore` | `connectorcredstore` `DeleteByTenant`, then `credentialstore` `DeleteByTenant`. | `connectorcredstore` `DeleteByUser`, then `credentialstore` `DeleteByUser`. |
| `issued_tokens` | 185 | `TokenIssuanceStore` | `issuedtokenstore` `DeleteByTenant` under CODE-4 item 3, which deletes only the rows the §13.3 retention rule (SPEC-5 Edit 5d) lets go. nil without Postgres. | `issuedtokenstore` `DeleteByUser` under CODE-4 item 3, which deletes only the rows the §13.3 retention rule (SPEC-5 Edit 5d) lets go. nil without Postgres. |
| `credential_pool` | 190 | `CredentialPoolStore` | Unchanged. | Unchanged. |
| `runtime_tenant_access` | 200 | `runtime_tenant_access` rows | `tenantaccessstore` `ListForTenant` for the runtime kind, then `Revoke` of each. | Exempt: tenant-scoped rows. |
| `pool_tenant_access` | 205 | `pool_tenant_access` rows | `tenantaccessstore` `ListForTenant` for the pool kind, then `Revoke` of each. | Exempt: tenant-scoped rows. |
| `user_roles` | 210 | User role mappings | `userstore` `DeleteByTenant`. | Exempt: §12.8 keeps the user row (**Processing restriction during erasure**). |
| `custom_roles` | 220 | Custom role definitions | `customrolestore` `DeleteByTenant`. | Exempt: tenant-scoped rows. |
| `tenant_definitions` | 230 | Connector, environment, and experiment definitions | `connectorstore` `DeleteByTenant`, then `environmentstore` `List` and `Delete` of each, then `experimentstore` `DeleteByTenant` (CODE-3 item 5). | Exempt: tenant-scoped rows. |
| `tenant_config` | 240 | Tenant configuration record | `tenants.Update` under CODE-3 item 6. | Exempt: tenant-scoped rows. |

## Staged code changes

### CODE-1 · Rank table, per-path exemptions, completeness gate, and nil-function skip

Targets: `pkg/gateway/storage/erasure/order.go`, `pkg/gateway/storage/erasure/erasure.go`, `cmd/lenny-gateway/tenantdeletion.go` (`tenantEraser`), `pkg/gateway/storage/erasure/order_test.go`, `pkg/gateway/storage/erasure/tenant_order_test.go`, and `tests/tier0_static/erasure_exemption_guard_test.go` (new).

1. `canonicalRank` holds exactly the names and ranks of the **Store table**: `eval_results` moves to 155, and `coordination_mirror`, `issued_tokens`, `runtime_tenant_access`, `pool_tenant_access`, `user_roles`, `custom_roles`, `tenant_definitions`, and `tenant_config` are added.
2. Add `userExempt` and `tenantExempt`, each a `map[string]string` from name to reason, holding the **Store table**'s exemptions. Each reason names symbols rather than file and line.
3. `ValidateOrder` and `ValidateTenantOrder` keep their current checks and add a completeness check: every `canonicalRank` name is wired on that path or in that path's exemption map, and a name that is both wired and exempt, or exempt with an empty reason, is an error. Each error names the store and the path. The startup call sites keep calling them under `log.Fatalf`.
4. An `Eraser` whose `DeleteByUser` is nil, a `SessionEraser` whose `DeleteBySession` is nil, and a `namedTenantEraser` whose `fn` is nil are skipped and left out of the per-store count map, so a receipt does not report an unrun store as having erased zero rows. The builders list such an entry by name instead of omitting it.
5. Rewrite the `canonicalRank` and `CanonicalOrder` doc comments: ranks follow the §12.8 tenant Phase 4 order, `ValidateTenantOrder` enforces strict rank, and `ValidateOrder` enforces only the `fkMustPrecede` edges, because the user sequence orders steps 12 and 13 differently. Drop the per-entry "step N" tags that no longer match and the "operator documentation" claim. Replace the line citation in the `fkMustPrecede` comment with a heading citation.
6. The tier-0 guard fails when `pkg/gateway/storage/evictionstatestore`, its pgstore, `evictionfallback`, `pkg/gateway/session/routingcache`, or the token-service `tokencache` gains a non-test importer outside its own subtree, or when the token service starts calling the cache's `Get`, `Put`, or `Invalidate`. Its failure message names the exemption to revisit.

CODE-1 lands in the same step as CODE-3 and CODE-4, because its completeness check refuses startup until both paths wire or exempt every name it adds. The completeness check catches a ranked store left out of a path. A store missing from `canonicalRank` itself is caught only by T1.2, which pins the name set.

### CODE-2 · Tenant-scope primitives for the sticky cache and the billing stream

Targets: `pkg/gateway/experiment/experimentsticky/experimentsticky.go`, `pkg/gateway/billing/billingstore/failover/failover.go`, `pkg/gateway/billing/billingstore/failover/memstream.go`, and `pkg/gateway/billing/billingstore/failover/redisstream/redisstream.go`.

1. `RedisCache.DeleteByTenant(ctx, tenantID) (int, error)` beside `DeleteByUser`: `redisconn.ScanDelete(ctx, c.client, rediskeys.TenantPrefix(t), rediskeys.TenantPrefix(t)+"exp:*:sticky:*")`. It records no invalidation metric.
2. `StreamTier` gains `PurgeTenant(ctx, tenantID) (int, error)`.
   - `redisstream.Tier.PurgeTenant` runs `XGROUP DESTROY` on the tenant's stream and group and then `DEL` of the stream key through the cluster-safe client. A missing key or group counts as success. It leaves `t.known[tenantID]` set, so this replica's flusher does not recreate the stream through `ensureGroup`.
   - `MemStream.PurgeTenant` drops the tenant's entries.
3. `Pipeline.DeleteByTenant` calls `p.stream.PurgeTenant` before `p.primary.DeleteByTenant` and keeps its Tier 2 buffer drop. A stream purge error returns before the primary delete.

### CODE-3 · Tenant-path Phase 4 wiring

Targets: `cmd/lenny-gateway/tenant_erasers.go` (`buildTenantErasers`, `tenantErasersBeforeSessions`, and a new `forEachTenantSession`), the `buildTenantErasers` call site in `cmd/lenny-gateway/adminrouter.go`, `cmd/lenny-gateway/billing_pipeline.go` (keep a handle on the billing stream), `tenantsLister` and the billing retention pruner's tenant lister in `cmd/lenny-gateway/worker_seams.go`, its `billingretention.New` call, and the revocation-cache rehydration lister in `cmd/lenny-gateway/billing_security_workers.go`, `pkg/blobstore/artifactcatalog/catalog.go` (`PgStore.DeleteByTenant` and the `Store` interface comment), `pkg/blobstore/cataloging/cataloging_test.go`, and the F-12.8.37 entry of `BUILD-GAPS.md`.

1. `forEachTenantSession(ctx, tenantID, fn)` lists `sessions.List(ctx, tenantID, ListFilter{Primary: true})` and calls `fn` for each session ID, returning the first error. Every session-keyed entry of the **Store table** uses it.
2. Wire every tenant-path cell of the **Store table**. The call site passes the semantic cache, the sticky cache, the billing stream and pipeline, the audit store, the two `TokenStore` stores, the tenant access, user, custom role, connector, environment, experiment, and tenant stores, the coordination mirror store, the tree budget, the delegation budget pgstore, and the messaging coordinator.
3. **Exempt billing check.** The `billing_buffer` and `billing` entries each read the tenant row and return `(0, nil)` without touching any store when `BillingErasurePolicy` is `tenantstore.BillingErasureExempt`. A tenant-row read error is returned.
4. **Delegation budget roots.** The `delegation_budget` entry calls `PurgeRoot` only for a listed session whose `RootSessionID` is empty or equal to its `ID`.
5. **Environment idempotency.** The `tenant_definitions` entry treats `environmentstore.ErrNotFound` from `Delete` as deleted.
6. **Tenant configuration record.** The `tenant_config` entry calls `tenants.Update` with a mutate that nulls every field of `tenantstore.Tenant` except this keep list: `ID`, `State`, `DeletedAt`, `WorkspaceTier`, `DataResidencyRegion`, `BillingErasurePolicy`, `ForceDeleteHoldOverride`, `ForceDeleteJustification`, `ForceDeleteBy`, `ForceDeleteAt`, `CreatedAt`, `UpdatedAt`, and `Version`. Nulling `ErasureSalt` deletes the sealed salt ciphertext. `BillingErasurePolicy` stays on the list because a Phase 4 re-run reads it in item 3, and an empty policy reads as pseudonymize.
7. **Tenant listers after the tombstone.** `tenantsLister` lists `ListFilter{}`, and a new `allTenantsLister` in `worker_seams.go` lists `ListFilter{IncludeDeleted: true}`. Each adds `default` only when the store has no `default` row: `tenantsLister` reads the row with `Get` only when its listing lacks `default`, because `pgstore.Get` decrypts the salt of an active row, and it returns any `Get` error other than `tenantstore.ErrNotFound`. A tombstoned `default` therefore leaves `tenantsLister`, so `FlusherManager` stops its flusher. The `billingretention.New` call, the `sweepIssuedTokens` start, and the revocation-cache rehydration in `billing_security_workers.go` pass `allTenantsLister{w.tenants}`, so an exempt tenant's billing events and a deleted tenant's kept issued-token rows age out after its tombstone, and a replica that restarts after the tombstone still loads the kept revoked rows (§13.3 reads every revoked row). `FlusherManager` and every other caller keep `tenantsLister{w.tenants}`. Rows 20 and 21 of **Tenant liveness and the default tenant** state what each lister returns, and its caller table gives each caller's behavior on the new `Get` error.
8. Audit and billing `DeleteByTenant` run on the connection the Phase 4 erasers already use, which is the posture `PruneRetention` runs under.
9. **F-12.8.37.** Close F-12.8.37 in `BUILD-GAPS.md` with a resolution note in this step. `TestFiledSuspendResumeFindingsMatchTheTree_spec_7_2` reads the finding's state against an `EraseSession` selector in the body of the methods `buildTenantErasers` or `tenantErasersBeforeSessions`, so the `session_message_queues` entry names `EraseSession` inside one of those two methods.
10. **Held catalog rows.** `artifactcatalog.PgStore.DeleteByTenant` drops its `legal_hold = false` guard, so the `artifacts` entry deletes the catalog rows of the originals Phase 3.5 escrowed (summary D5). Rewrite its doc comment and the `Store` interface's `DeleteByTenant` comment to name Phase 3.5 as the hold gate. In `pkg/blobstore/cataloging/cataloging_test.go`, change `fakeCatalog.DeleteByTenant` and `TestDeleteByTenantPreservesLegalHeldCatalogRow` so the held row is deleted.

### CODE-4 · User-path wiring

Targets: `cmd/lenny-gateway/adminrouter.go` (the user eraser list and `buildSessionScopedErasers`), `pkg/gateway/storage/leasestore/leasestore.go` and `pkg/gateway/storage/leasestore/pgstore/pgstore.go` (`DeleteByUser` doc comments only), `pkg/gateway/storage/issuedtokenstore/issuedtokenstore.go` (`DeleteByUser` and `DeleteByTenant`), `cmd/lenny-gateway/billing_security_workers.go` (the issued-token GC), and `cmd/lenny-gateway/flags.go` (its interval flag).

1. Wire every user-path cell of the **Store table**. Conditional appends become entries listed by name with a nil function in the posture that lacks the backend.
2. Rewrite both `DeleteByUser` comments in the lease stores. Each states that the drain runs each session's terminal pipeline before the erasers (0094 D21), and that a residual coordination lease expires within its own TTL, which is `sessionserver.DefaultCoordinationLeaseTTL` at bind and four `--coordination-interval` periods after a Sweeper takeover. Delete the claim that the orchestrator releases leases per session.
3. `issuedtokenstore.DeleteByUser` and `DeleteByTenant` each add `AND exp <= now() - interval '1 hour'` to their delete, which deletes only the rows past the trailing hour that the §13.3 retention rule (SPEC-5 Edit 5d) lets go; SPEC-5 step 18 and the Phase 4 `TokenIssuanceStore` entry cite that rule. Delete the doc-comment claims that a deleted token can no longer be presented and that every row of the tenant is hard-deleted.
4. Wire the §12.2 expired-row GC, which the §13.3 retention rule (SPEC-5 Edit 5d) bounds: `sweepIssuedTokens`, built like `sweepIdempotencyKeys` with its store narrowed to a one-method `DeleteExpired(ctx, tenantID, cutoff)` interface and started beside it when `pgPool` is set, calls `issuedtokenstore.DeleteExpired` for each tenant `allTenantsLister` returns with the cutoff `now() - 1h` every `--issued-token-gc-interval-seconds` (env `LENNY_ISSUED_TOKEN_GC_INTERVAL_SECONDS`, default 3600).

### CODE-5 · Phase 3.5 escrow

Targets: `cmd/lenny-gateway/legalholdescrow.go` (`blobEscrowWriter`, `blobEscrowDeleter`, `resolveHolds`, `escrowLedger`, `tenantEscrowMigrator`, and their doc comments), the escrow wiring in `cmd/lenny-gateway/adminrouter.go`, `pkg/legalholdescrow/migrator.go` (`Migrator`, `Migrate`), and the doc comments in `pkg/legalholdescrow/escrow.go` and `pkg/legalholdescrow/records.go`.

1. At startup, build one escrow blob store with `blobproviderflags.Resolve`. It uses the artifact store's provider, credentials, and endpoint unless `--legal-hold-escrow-endpoint` overrides the endpoint, sets `Bucket` and `MinIOBucket` to the configured escrow bucket (`escrowCfg.Default.Bucket`), and passes a nil `SSEKeyResolver`, because the payload is already sealed under the escrow KEK. When no escrow bucket is configured, build no store; the migrator then fails closed with `ErrRegionUnresolvable`.
2. `blobEscrowWriter` and `blobEscrowDeleter` both use that store. The URI keeps `TenantID` set to the original tenant ID, which gives the §12.8 `legal-hold-escrow/{original_tenant_id}/...` layout inside a bucket that the artifact-store prefix delete never reaches. The deleter's `Tombstoner` assertion runs against that store.
3. `escrowLedger` appends each ledger event under `jwtaudit.PlatformTenantID` with the original tenant ID as the payload's `target_tenant_id` (§16.7), so the tenant `audit` entry never deletes it.
4. Do not write the escrow object under `jwtaudit.PlatformTenantID` or through `w.objectStore`. The tenant SSE resolver refuses a tenant with no row.
5. `resolveHolds` returns its error. A `ListLegalHeld` error is returned instead of dropped. For a session hold, it lists `m.artifacts.ListBySession` and appends one `HeldArtifact` per record not yet seen whose `State` is `artifactcatalog.StateLive`, populated as the artifact branch populates it (`ResourceID` the base64url of `rec.URI`, `BlobURI`, `HoldSetAt`, `SessionID`, and `ArtifactURI`). A listing error aborts Phase 3.5.
6. `legalholdescrow.Migrator` gains a `Records RecordStore` field, which `tenantEscrowMigrator` sets from `escrowRecords` in `buildAdminRouter`. Before reading a hold's original, `Migrate` calls `Records.ActiveForArtifact(ctx, in.TenantID, h.ArtifactURI)`. A non-empty result skips the hold and adds its `EscrowObjectKey` to the result, and a lookup error aborts the migration (§12.8 Phase 3.5 re-entry). A nil `Records` skips the lookup.
7. Rewrite each comment that describes a Phase 4 skip or a `legal_hold_escrow: true` marker to state that Phase 4 deletes the originals and that the ledger record is the release pointer.

Per-region escrow clients (`storage.regions.*`) stay out of scope, because `escrowConfigFromFlags` produces only `Default`.

### CODE-6 · One tenant registration predicate

Targets: `pkg/gateway/environment/tenantstore/tenantstore.go` (new `Tenant.Registered`, `Memory.IsRegistered`), `pkg/gateway/environment/tenantstore/pgstore/pgstore.go` (`IsRegistered`), `cmd/lenny-gateway/auth_seams.go` (`bearerTenantRegistry`, `authFailureAuditAdapter`), `cmd/lenny-gateway/playground_seams.go` (`playgroundTenantRegistry`), `pkg/auth/jwt/` (a new tenant-claim verifier wrapper beside `ClaimChecker`), `cmd/lenny-gateway/token_signing.go` (`embeddedHMACVerifier`), `pkg/gateway/middleware/auth/auth.go` (`serveBearer`, `Options.TenantClaimName`), the `authmw.Options` literal in `cmd/lenny-gateway/httpsurface.go`, `pkg/gateway/externalapi/admin/tenants.go` (`AuditEvent`, `handleCreateTenant`, `handleDeleteTenant`), `forcedelete.go` (`handleForceDeleteTenant`), `bootstrap.go` (`upsertTenants`), `pkg/gateway/externalapi/admin/audit_sink.go` (`ChainAuditSink.EmitAdminEvent`), and the `auth.tenantIdClaim` comment in `charts/lenny/values.yaml`.

1. `Tenant.Registered()` implements the registered reading of **Tenant liveness and the default tenant**. `IsActive` is unchanged.
2. `Memory.IsRegistered` returns `Registered()` of the row. The pgstore `IsRegistered` selects the same reading in SQL: `deleted_at IS NULL AND state NOT IN ('deleting', 'deleted')`.
3. `bearerTenantRegistry` and `playgroundTenantRegistry` implement rows 3 and 4 of **Tenant liveness and the default tenant**: each reads the row first, answers with the registered reading for a found row, applies the missing-`default` fallback on `ErrNotFound`, and returns any other error.
4. `authFailureAuditAdapter` writes a `TENANT_NOT_FOUND` rejection to the platform chain (empty `ActorTenantID`) and sets the inferred tenant as the event's `TargetTenantID`. `admin.AuditEvent` gains `TargetTenantID`, which `ChainAuditSink.EmitAdminEvent` writes as the payload's top-level `target_tenant_id` when non-empty, because the §11.7 CMP-058 residency gate reads only that key and never one under `detail`. It never appends to the refused tenant's chain. The other rejection reasons keep their placement.
5. `handleDeleteTenant` and `handleForceDeleteTenant` read the `Router`'s existing `tenancyMode`, which `admin.NewRouter` copies from `Options.TenancyMode` and `buildAdminRouter` already sets from `--tenancy-mode`. When it is not `"multi"`, an empty value included, both answer a request for `auth.DefaultTenantID` with `409 INVALID_STATE_TRANSITION` before any state write (SPEC-2 Edit 2b, summary D28). The `Router` gains no setter, and this item leaves `adminrouter.go` unchanged.
6. The configured tenant claim (SPEC-3 Edit 3a) applies only to a token that an IdP key verified. A wrapper in `pkg/auth/jwt` beside `ClaimChecker` sets `Claims.TenantID` to `ClaimString(name)` of every token its inner verifier accepts, and to an empty value when that claim is absent, never keeping the token's own `tenant_id`. `embeddedHMACVerifier` wraps the embedded OIDC member with it under `--tenant-id-claim`, and the Token Service member is not wrapped. `serveBearer` reads `claims.TenantID` for every bearer, and `Options.TenantClaimName` is deleted. The discriminator is the verifying key, never a payload claim such as `iss` or `typ`. In the `auth.tenantIdClaim` comment, replace "on every Bearer JWT" with "on a bearer an IdP key verified; a gateway-issued token uses `tenant_id`".
7. After the format check, `handleCreateTenant` refuses the ID `jwtaudit.PlatformTenantID` with `422 RESERVED_IDENTIFIER`, `details.field` `id`, and `details.value` `platform`, as the §15.1 catalog row requires, and `upsertTenants` records it as an `error` entry (SPEC-3 Edit 3e, SPEC-2 Edit 2c). `auth.ValidateTenantID` is unchanged, because claim extraction also calls it.
8. **Bootstrap seed refusal.** In `upsertTenants`, after `r.tenants.Get` finds the row and before the workspace-tier checks, an existing row whose `State` is `TenantStateDeleting` or `TenantStateDeleted`, or whose `DeletedAt` is set, is recorded as an `error` entry with `seedValidationCode` and a message naming the tenant and its state. The entry runs no KMS probe, no `tenants.Update`, and no `provisionTenantSequences`, with and without `--force-update` and in a dry run (SPEC-2 Edit 2c, summary D29). Delete the force-update mutate's `t.DeletedAt = time.Time{}` and its comment, which describe reviving a soft-deleted tenant. This removes the only path in the tree that clears a tenant's `DeletedAt` (row 8 of **Tenant liveness and the default tenant**).

### Tenant liveness and the default tenant

This subsection is the one home for which tenant rows each site treats as live and for how each site treats the built-in `default` tenant. Summary D11, D27, D28, and D29, SPEC-2 Edits 2a and 2b, spec Edge case 1, DOCS-1 item 6, Edge case 5, CODE-3 item 7, and CODE-6 items 1 to 3, 5, and 8 point here. Change the predicate or a row here first, then the sites that implement it. The table was derived from the tree at merge 423e7240f, which includes the T-10.2.11 fix (merge 2177ac596), before it was mapped to the staging.

**Liveness predicate.** A tenant row carries a `TenantState` (`active`, `disabling`, `deleting`, or `deleted`, where an empty value reads as `active`) and a `DeletedAt`. The deletion lifecycle sets `deleted` and `DeletedAt` together at the Phase 6 tombstone (`SoftDelete`). Each site reads exactly one of three readings of the row:

- **Listed:** `DeletedAt` is zero, so the tenant is `active`, `disabling`, or `deleting`. `Tenant.IsActive` and `ListFilter{}` implement it, unchanged.
- **Registered:** the tenant is listed, and its `TenantState` is neither `deleting` nor `deleted`, so it is `active` or `disabling`. `Tenant.Registered` (CODE-6 item 1) implements it, and §10.2 states it (SPEC-3 Edit 3b).
- **Accepts new work:** the `TenantState` is `active` or empty. `Tenant.AcceptsNewWork` implements it, unchanged.

The `default` tenant follows the same readings when its row exists. When no `default` row exists, `default` counts as listed and registered. With Postgres that happens only before migrations 0053 and 0054 insert the row, so the fallback serves the in-memory tenant store. Only `tenantsLister`, `allTenantsLister`, and the two registry adapters apply the fallback. In multi-tenant mode `default` is an ordinary tenant (summary D27).

**Tenancy mode.** The registered reading refuses a request only in multi-tenant mode, where `applyTenantAuthOptions` wires the bearer registry (row 3) and SPEC-2 Edit 2a scopes the Phase 3 refusal. A single-tenant gateway ignores the tenant claim of every bearer and serves `default` (§10.2 single-tenant row; `ExtractTenant` returns `default` without a registry read). Outside dev mode the middleware also refuses a request that carries no bearer on a route that is not public, and the request's tenant comes only from the principal (the F-10.2.21 fix, merge a39e2c6df), so every single-tenant request outside dev mode runs as `default`. `default` stays `active` in that mode, because the admin API refuses to delete it (row 7). Other tenants can still be created and deleted in single-tenant mode, because only `default` is refused, and the dev and integration suites rely on them. A bearer whose claim names a deleted non-`default` tenant is therefore accepted, as it was before the deletion, and its requests run as `default`, so it reaches none of the deleted tenant's rows on any route that takes the tenant from the principal. The playground mint is the exception: in single-tenant mode `mintAPIKey` keeps the pasted bearer's tenant claim without a registry read, and `mintOIDC` keeps the cookie session's tenant (row 5), so each mints a bearer that names the deleted tenant and appends `playground.bearer_minted` to that tenant's chain. The minted bearer runs as `default` at use. The summary **New** entry for row 5 holds that residue. Its revocation still holds after Phase 4, because Phase 4 keeps the rows of the §13.3 retention rule (SPEC-5 Edit 5d) and the revocation-cache rehydration lists with `allTenantsLister` (row 21). Refusing non-`default` tenants in single-tenant mode is not staged: no spec site ties tenant creation to the tenancy mode, the refusal would break the dev and integration suites, and it would bound only the playground mint residue, which the row-5 direct fix closes at the mint itself. In dev mode the `X-Lenny-Tenant-ID` header names the tenant after the format check alone (`devHeaderTenant`), so a dev-mode request can name a deleted tenant and write rows under its ID. Dev mode trusts unauthenticated identity headers by design, and this proposal stages nothing for it.

**Paths that clear `DeletedAt`.** The pgstore `Update` writes `deleted_at` from the mutated row, so any mutate that zeroes `DeletedAt` returns a tombstone to the listed reading. In the tree the only such mutate is the `upsertTenants` force-update branch, which CODE-6 item 8 removes together with the seed's reach to a `deleting` or `deleted` row. The other `tenants.Update` callers are the Phase 1 disabler, `syncBack`, the Phase 3 commit (CODE-8), the `tenant_config` entry (CODE-3 item 6, whose keep list holds `State` and `DeletedAt`), suspend and resume, `PUT /v1/admin/tenants/{id}`, the compliance decommission writer, the rbac-config and elicitation-integrity writers, the user-erasure salt writes, and salt rotation. None of them zeroes `DeletedAt`, and only the lifecycle writers change `TenantState`. After CODE-6 item 8, a tombstone therefore never re-enters a listed or registered site.

**Sites.** "Today" describes the tree before this proposal where it differs from the behavior after the staging.

| # | Site | What it decides | Reading and behavior after the staging | Handling |
|:--|:--|:--|:--|:--|
| 1 | `tenantstore` `Tenant.IsActive`, `Memory.List`, pgstore `List` | Which rows a plain listing returns | Listed. A tombstone is omitted in both modes. | Unchanged (Non-goals). |
| 2 | `tenantstore` `Memory.IsRegistered`, pgstore `IsRegistered` | `auth.TenantRegistry` on the store | Registered. Today they read `DeletedAt` only and admit `deleting`. No production caller reads them directly; rows 3 and 4 serve the request path. | CODE-6 items 1 and 2. |
| 3 | `cmd/lenny-gateway/auth_seams.go` `bearerTenantRegistry` | Whether a bearer's or a dev header's tenant is admitted (§10.2) | Registered for a found row, `default` included; a missing row admits `default` only; any other store error refuses. Today `default` is admitted without a read, so a deleted `default` keeps its bearers. `applyTenantAuthOptions` wires it in multi-tenant mode only. | CODE-6 item 3. |
| 4 | `cmd/lenny-gateway/playground_seams.go` `playgroundTenantRegistry` | The OIDC callback admission and, in multi-tenant mode, the apiKey mint admission (§27.3.1), and the dev-mode Ready-gate | As row 3, wired in both modes. In dev mode a `deleting` dev tenant answers `503 LENNY_PLAYGROUND_DEV_TENANT_NOT_SEEDED`, which fails closed. | CODE-6 item 3. |
| 5 | `pkg/gateway/mcpfabric/playground/token.go` `mintOIDC` | Whether a playground cookie session mints a bearer | No reading: it mints in every state. Row 3 refuses the minted bearer at use from `deleting`, but `completeMint` appends `playground.bearer_minted` to the tenant's own chain, also after the Phase 4 audit entry, and Phase 4 erases no `t:{tenant_id}:pg:*` key. | Unhandled. Filed as a summary **New** entry. X1 mints its playground bearer before Phase 3. |
| 6 | `pkg/auth/auth.go` `ExtractTenant` single-tenant branch; `pkg/gateway/middleware/auth` `devHeaderTenant` | The tenant of a single-tenant request: `default`, or in dev mode the dev header's tenant | No reading. Outside dev mode every request runs as `default`, which row 7 keeps `active`, and a bearer naming a deleted tenant runs as `default` too (**Tenancy mode** above). In dev mode the header names any well-formed tenant, a deleted one included. | T-10.2.11 (landed) and CODE-6 item 5. The dev-mode residue is accepted (**Tenancy mode** above). |
| 7 | `pkg/gateway/externalapi/admin` `handleDeleteTenant`, `handleForceDeleteTenant` | Whether tenant deletion starts | Refuses a tombstone (today), and refuses `default` with `409 INVALID_STATE_TRANSITION` when the `Router`'s `TenancyMode` is not `multi`, an empty value included. | CODE-6 item 5 (summary D28), SPEC-2 Edit 2b. |
| 8 | `admin` `upsertTenants` | What the bootstrap seed does to an existing row | Refuses a row in `deleting` or `deleted`, or with `DeletedAt` set, as an `error` entry, with and without `--force-update`, and leaves the row and its sequences unchanged. Today it re-provisions such a row's sequences on every run, and with `--force-update` and a changed field it clears `DeletedAt` while `State` stays `deleted`, which returns the tombstone to every row-20 worker. | CODE-6 item 8 (summary D29), SPEC-2 Edit 2c. |
| 9 | `admin` `handleGetTenant` | `200` or `410 TENANT_DELETED` | Listed: `410` for a tombstone and `200` otherwise, as §12.8 requires. | Unchanged. |
| 10 | `admin` `handleCreateTenant` | Whether an ID can be created | The retained tombstone keeps its ID taken. `platform` is reserved. | Unchanged; CODE-6 item 7. |
| 11 | `admin` `handleSuspendTenant` | Whether a tenant can be suspended | Refuses `deleted` only. Suspending a `deleting` tenant changes no erasure outcome. | Unchanged. |
| 12 | `admin` `handleRotateErasureSalt`; `PUT /v1/admin/tenants/{id}`, the rbac-config writer, and the elicitation-integrity writer | Whether a tenant-row write proceeds | Salt rotation refuses a tombstone and admits `deleting`; the other writers read no state. A write after the Phase 4 `tenant_config` entry, or onto the tombstone, puts back a field the keep list nulls, such as a fresh erasure salt. | Unhandled. Filed as a summary **New** entry. |
| 13 | `admin` `ValidateSIEMForRegulatedTenants`, `ValidatePgauditForRegulatedTenants`, `EmitBillingErasureExemptRegulatedStartup` | The startup gates and the startup audit over regulated tenants | Listed. A `deleting` regulated tenant still counts, because its data still exists. | Unchanged. |
| 14 | `admin` `legalHoldTenantScope` | The tenants a platform-admin hold listing covers | Listed. | Unchanged. |
| 15 | `admin` `computeFloorClamps` | The tenants an elicitation floor raise clamps | Accepts new work. | Unchanged. |
| 16 | `sessionstore` `StoreMarkers` and the creation fence | Whether a session is created | Accepts new work. A missing row admits, which covers a missing `default`. | Unchanged. |
| 17 | `policy` `TenantStoreLimits.LookupLimits` | The quota limits of a tenant | Listed. A tombstone answers `ErrTenantNotFound`, which fails closed. | Unchanged. |
| 18 | `sessionserver` `requireTenantNotSuspended` | Whether a message is injected | Reads suspension only, and a missing row admits. Phase 2 drains a deleting tenant's sessions, and row 3 refuses its bearers. | Unchanged. |
| 19 | `cmd/lenny-gateway/tenantdeletion.go` `reconcileOnce` | Which tenants have a deletion job | Listed, then `disabling` or `deleting`. | Unchanged. |
| 20 | `cmd/lenny-gateway/worker_seams.go` `tenantsLister` | The tenants the background workers in the caller table below visit | Listed, with `default` added only when no `default` row exists. Today `default` is prepended unconditionally, so a deleted `default` stays in every worker, and with Postgres `default` is listed twice. | CODE-3 item 7. |
| 21 | `allTenantsLister` (new) | The tenants the billing retention pruner, `sweepIssuedTokens`, and the revocation-cache rehydration visit | Listed or tombstoned, with `default` added only when no `default` row exists. | CODE-3 item 7. |
| 22 | `cmd/lenny-gateway/executor_credentials.go` `resolveTokenServiceTenant` | The fallback tenant of the Token Service client | No reading: `default` under `single` and empty under `multi`. Correct because every single-tenant request outside dev mode runs as `default` (**Tenancy mode** above), and row 7 keeps `default` `active`. | Unchanged. |
| 23 | `pkg/gateway/externalapi/admintoken` `Provisioner.ensureUser`, which `Provision` runs at the end of every bootstrap run | Whether the `lenny-admin` user row exists under `--admin-token-tenant` (default `default`) | No reading: it recreates the user row under a `deleting` or `deleted` admin tenant. Row 3 refuses the `lenny-admin` token from Phase 3, so this happens only when the Job authenticates with another platform-admin's token. | DOCS-1 item 6 moves the setting before deletion. The residue is unhandled and filed as a summary **New** entry. |
| 24 | Request-path fallbacks: `sessionserver.resolveTenant`; `translator.resolveTenant`, which the OpenAI chat and Open Responses handlers call; `mcptools.callerTenantID` with the `Deps.TenantID: "default"` fallback in `cmd/lenny-gateway/mcpsurface.go` | The tenant of a request that reaches the handler with no principal | No reading. Outside dev mode the middleware refuses a request with no bearer on a route that is not public, the middleware strips client-sent identity headers, and each site reads the tenant only from the principal. The dev header's tenant and the `default` fallback apply only in dev mode. | Closed by the F-10.2.21 fix (merge a39e2c6df), which this tree includes. This proposal stages nothing for them. |

**`tenantsLister` callers and the `Get` error.** After CODE-3 item 7, `tenantsLister` reads the `default` row only when its listing lacks `default`. That happens on the in-memory tenant store with no `default` row, and after a multi-tenant deletion tombstones `default`. A `Get` error other than `ErrNotFound` comes back through the lister's existing error result. Every caller already has a branch for a listing error, so no caller's failure behavior changes in kind; the `Get` adds a second error source in those two postures only. In the table, a caller fails closed when the error withholds its work for the tick rather than running it over a wrong tenant set.

| Caller | On a lister error | Fails closed |
|:--|:--|:--|
| `FlusherManager` (`billing_security_workers.go`) | `reconcile` keeps its running set, so no flusher starts or stops in that tick. | Yes. Returning the error instead of omitting `default` keeps a live `default`'s flusher running. |
| Billing retention pruner; `sweepIssuedTokens` (CODE-4 item 4); revocation-cache rehydration | Uses `allTenantsLister`, whose `IncludeDeleted` listing needs no `Get`. | Not applicable. |
| Checkpoint freshness reaper | Reports the error and skips the pass. | Yes. |
| Idempotency-key GC | Logs and skips the pass. | Yes; reclamation waits for the next pass. |
| Watchdog; retention GC; coordination Sweeper; delegation-tree orphan cleanup; orphan-session reconciler | Returns or records the error and skips the pass. | Yes. |
| Suspended-backlog re-check | Logs a warning and resumes nothing. | Yes. |
| Quota checkpoint subject lister; delegation-budget tree lister; billing checkpoint session lister | Returns the error, so no checkpoint or reconcile runs in that tick. | Yes. |
| Storage-quota recovery reconciler | Logs and skips the rebuild. | Yes. |
| HPA gauge export | Logs and skips the export. | Yes. |
| Storage-quota startup rehydration (`store_router.go`) | Logs and starts with counters that were not rehydrated. | No. The counters under-count until a recovery rebuild. The posture predates this proposal for a listing error, and the `Get` reaches it only after a `default` deletion, because the rehydration runs only with the Postgres artifact catalog. Not filed. |

### CODE-7 · The lease step writes no deny-list entry

Targets: `pkg/gateway/credentials/leaserevoke/leaserevoke.go` (`RevokeUserLeases`, `Revoker`, `New`, and the package, `DenyList`, and method doc comments), `cmd/lenny-gateway/credential_revocation.go`, `cmd/lenny-gateway/user_revocation.go`, and the `leaserevoke.New` call in `buildAdminRouter` (`cmd/lenny-gateway/adminrouter.go`).

1. `RevokeUserLeases` releases every listed lease, writes no deny-list entry, and still returns `len(leases)`. `Revoker` and `New` drop the deny-list parameter. Update every caller, including the string row for `adminrouter.go` in `cmd/lenny-gateway/release_wiring_test.go`. Move the `DenyList` interface to `cmd/lenny-gateway/credential_revocation.go` beside `poolCredentialRevoker`, its remaining consumer, together with the propagator assertion in `user_revocation.go` and the cross-replica marker test in `leaserevoke_test.go`.
2. Rewrite the doc comments. `Release` revokes the lease and removes its record, which rejects the returned lease's token on every replica. The step deny-lists no credential (summary D15). Delete every sentence about `Release` preceding a deny-list write, and every sentence that calls the deny list what stops the token.

CODE-7 lands before or with CODE-8, because CODE-8 runs this function over every session of a deleted tenant.

### CODE-8 · Phase 3 tenant credential revoker, and Phase 3 and Phase 4 errors pause in phase

Targets: `cmd/lenny-gateway/tenantdeletion.go` (new `tenantCredentialRevoker` beside `tenantStateDisabler`), `cmd/lenny-gateway/adminrouter.go` (assign `deletionReconciler.Revoker`, and construct the `leaserevoke.Revoker` before the reconciler), and `pkg/controller/tenantdeletion/controller.go` (`ReconcileTenant`, `pauseDrain`, `runPhase`, and the Phase 3 comment).

1. `tenantCredentialRevoker.RevokeTenantCredentials(ctx, tenantID)` runs these steps in order and returns the first error:
   1. Commit `TenantState` `deleting` on the tenant row through `tenants.Update` unless the row is already `deleting` or `deleted`, following `tenantStateDisabler`. `syncBack` normally writes the state in the tick that completes Phase 2; this step makes the SPEC-2 ordering hold when `syncBack` failed.
   2. List the tenant's sessions with `ListFilter{Primary: true}` and call `leaserevoke.Revoker.RevokeUserLeases(tenantID, "", ids)` over every listed session, terminal ones included. A listing error is returned.
   3. Call the function of the `tokens` entry that `buildTenantErasers` builds, which implements the tenant-path cell of the **Store table**'s `tokens` row.
2. The access-token cache flush has no code, because the cache has no production writer (non-spec Edge case 2). The revoker's doc comment says so and names the tier-0 guard of CODE-1 item 6.
3. A lease-listing error inside `RevokeUserLeases` stays silent, because `LeasesBySession` returns no error. The doc comment of step 2 states this and its bound (non-spec Edge case 1). Do not describe step 2 as failing closed.
4. `runPhase` wraps the error the `Revoker` or `Eraser` call returns in a new sentinel, `errPhaseRetryable`. `ReconcileTenant` routes it to `pausePhase`, which generalizes `pauseDrain`: it records the error text in `BlockedReason`, logs a warning naming the tenant and the phase, writes neither `Phase` nor `State`, and returns nil. Phase 2 routes `errSessionsNotDrained` to `pausePhase` as well.
5. A nil `Revoker` fails the job with "Phase 3 requires a CredentialRevoker", and a nil `Eraser` keeps failing it, as a nil `Terminator` does at Phase 2. Neither error is wrapped in `errPhaseRetryable`. Rewrite the Phase 3 comment, which says Phase 4 covers a missing revoker.

### CODE-9 · full_revoke continuation

Targets: `pkg/gateway/externalapi/admin/users.go` (`terminateUserSessions`, `cancelForRevoke`, and `handleInvalidateUser`).

1. Every call after `users.Update` succeeds runs on `context.WithoutCancel(req.Context())` with no overall deadline, as `tenant_suspend.go` does. Each store call keeps its own bound.
2. `terminateUserSessions` lists with `ListFilter{UserID: userID, Primary: true}`, runs the lease step over every listed session, terminal ones included, and then calls `cancelForRevoke` for each non-terminal session. A cancel error does not stop the loop; the session ID and error text go into `failedSessions`, and the loop continues.
3. On a commit-then-error write, `cancelForRevoke` re-reads the row with `sessionstore.ReadPrimary`. When the row's state is `session.StateCancelled`, it runs `AfterRevokeWrite` on that row. The session is still reported in `failedSessions` and is not returned as revoked. A failed re-read is reported the same way.
4. A `DismissByUser` error answers `200` with the error text in `elicitationDismissalError`.
5. A session-listing error runs `runFullRevokeFanOut` with no sessions, then `DismissByUser`, then `r.emit` of `admin.user.invalidated` with the counts and a `sessionListingError` detail, and then answers `500 INTERNAL_ERROR` with the same counts in the error details.
6. The response and the audit detail carry `failedSessions` (an array of `{sessionId, error}`) when it is not empty, and `elicitationDismissalError` when dismissal failed. Every existing field keeps its name.

## Staged schema, chart, and migration changes

### MIG-1 · Migration 0188: drop the `audit_redaction_receipts` foreign key

Targets: `migrations/0188_audit_redaction_receipts_drop_fk.up.sql`, `migrations/0188_audit_redaction_receipts_drop_fk.down.sql`, `migrations/audit_redaction_receipts_test.go`, the comment in `cmd/lenny-gateway/leader_sweeps.go` that names the foreign key as the `audit_log` partitioning blocker, and the `DeleteByTenant` and `PruneRetention` doc comments in `pkg/gateway/audit/auditstore/auditstore.go`.

1. Up: `ALTER TABLE audit_redaction_receipts DROP CONSTRAINT IF EXISTS audit_redaction_receipts_audit_event_id_fkey;`. Keep `idx_audit_redaction_receipts_event` and every grant. The constraint name is the default for the inline `REFERENCES` in migration 0160, and T2.1 confirms it through `pg_constraint`.
2. Down: `ALTER TABLE audit_redaction_receipts ADD CONSTRAINT audit_redaction_receipts_audit_event_id_fkey FOREIGN KEY (audit_event_id) REFERENCES audit_log (id) NOT VALID;`.
3. Migration 0160 is applied and stays unedited. MIG-1 lands before or with the tenant `audit` entry of CODE-3.

## Staged docs changes

### DOCS-1 · Operator guide tenant deletion and legal-hold text

Targets: `docs/operator-guide/multi-tenancy.md` (the sentence that step 2 of **Multi-Tenant Mode** gained in the T-10.2.11 fix, the tenant deletion phase table rows for Phases 3 and 4, the tombstone sentence after the "Legal holds block Phase 4" callout, the bullet under **Legal Holds** on held resources during tenant deletion, and the roles sentence after the legal-hold commands), `docs/operator-guide/lenny-ctl.md` (the **Min Role** column of the **Legal Hold Management** table), `docs/operator-guide/security.md` (the multi-tenant row of **Tenant ID Extraction** and its `TENANT_NOT_FOUND` rejection row), `docs/client-guide/authentication.md` (the sentence that opens **Tenant Context**), and `docs/api/admin.md` (the `POST /v1/admin/tenants` and `DELETE /v1/admin/tenants/{id}` entries).

1. Phase 3 row: "From this phase a multi-tenant gateway rejects every bearer token of the tenant with `403 TENANT_NOT_FOUND`. The tenant's connector tokens and registered credentials are deleted, and the credential leases its sessions still hold are returned." The row names no internal store method.
2. Phase 4 row: a summary in the spec's dependency order (caches and artifacts, then session-scoped stores, audit and billing events, sessions, tokens and credentials, and finally the tenant's access rows, roles, and configuration). Replace "(respecting `billingErasurePolicy` and legal holds)" with the billing rule of summary D18, and point to **Legal Holds** for force-delete behavior.
3. **Legal Holds** bullet: an active hold blocks Phase 4, and on force-delete the held evidence is copied to the legal-hold escrow before Phase 4 deletes the originals (summary D5).
4. After the roles sentence, and in the `lenny-ctl.md` role column: "A tenant-admin of a tenant in the `deleting` state cannot call the API. A platform-admin releases its holds or force-deletes it." (summary D11).
5. Tombstone sentence: replace "and all mutable fields nulled" with the Edit 5c rule in reader terms, naming the billing erasure policy, workspace tier, and data residency region as kept fields. Leave its "after Phase 6" wording unchanged.
6. After the tombstone sentence: in a multi-tenant deployment `default` can be deleted like any other tenant. Before deleting it, set `gateway.adminToken.tenant` (the gateway's `--admin-token-tenant`) to another tenant, upgrade, and rotate the initial admin token as `installation.md` describes, because the token already in the `lenny-admin-token` Secret keeps its `default` scope until rotated and the gateway refuses it from Phase 3 onward; alternatively, provision another human platform-admin. Remove `default` from the bootstrap seed, because the seed reports an entry that names a deleting or deleted tenant as an error on every upgrade (summary D29). A single-tenant deployment refuses to delete `default` (summary D27).
7. `security.md` and `authentication.md`: the gateway reads the configured claim from an OIDC ID token and `tenant_id` from a gateway-issued token (SPEC-3 Edit 3a), and `TENANT_NOT_FOUND` also answers a bearer of a tenant in the `deleting` or `deleted` state (Edit 3b). In step 2 of **Multi-Tenant Mode** in `multi-tenancy.md`, replace "names a tenant with no active record" with "names a tenant that has no record or whose record is in the `deleting` or `deleted` state".
8. `admin.md`: the create entry states that the ID `platform` is rejected with `422 RESERVED_IDENTIFIER` (Edit 3e), and the delete entry states that a single-tenant deployment refuses to delete `default` with `409 INVALID_STATE_TRANSITION` (Edit 2b).

Each sentence that depends on D5, D11, D18, or D27 follows the decision as it stands when DOCS-1 lands.

### REC-1 · Records: closures, departures, filings, and gates

Targets: `BUILD-GAPS.md`; `TEST-GAPS.md`; `scripts/seed-claim-register.py`, with `tests/claim-map.json` regenerated from it; `tests/tier11_docs/build_gaps_data_lifecycle_records_currency_test.go`; the proposal deviations file; and the P5b bullet of `gateway-runtime-comms-remediation.md` (its Outcome and its tick).

1. **Close** F-11.4.12, F-11.4.13, the F-11.4.14 remainder, F-11.4.17, F-12.8.25, F-12.8.30, F-12.8.34, F-12.8.41, and F-12.8.42. F-11.4.10 gets a resolution note for its deny-list and session-set halves and stays open, because 0093's accepted departure on it still holds and `TestReleaseOwnershipDeparturesStayRecordedOnOpenFindings` keeps that departure on an open finding.
2. **N-4** has no BUILD-GAPS entry, so its closure goes only in the plan bullet's Outcome: step 1 closes by 0094 D21 and the CODE-4 comment fix, and step 3 closes by the `redis_caches` exemption and the SPEC-3 registry refusal.
3. **Leave F-12.8.46 open** and add one sentence: the tenant-path prefix delete removes such objects when the whole tenant is deleted, and the user-erasure case it records is unchanged.
4. **Narrow F-12.8.44** to Phase 1 issuance and sign-up refusal. A token minted for a deleting tenant is refused at use by the registry refusal.
5. **Annotate F-12.8.2**, which is closed and claims escrow in the escrow bucket: the tree wrote escrow into the artifact store until CODE-5, which places the single-region default in the escrow bucket.
6. **Departures** go in `BUILD-GAPS.md` and the deviations file, each with its bound, and add no claim-register row, because a §28.4 row needs a §28 anchor and a closing R step and none of these has one. They are non-spec Edge cases 1 to 6.
7. **Claim register.** In the generator, update the note of the row "User erasure deletes each session's message queues" to state that tenant deletion erases them too, and regenerate `tests/claim-map.json`.
8. **New findings.** File each entry of the summary's **Defects in the shipped tree that this proposal does not stage** that is marked **New**, with the owner the entry names, under the next free identifier.
9. **Records gate.** Update the tier-11 records test so it expects the closures as closed, F-12.8.46 open and unassigned, and each new finding with its owner. Register the gate in `documentConsistencyGates` if the update adds a new file.
10. **Test gaps.** In `TEST-GAPS.md`, close T-12.1.3 and T-12.8.4, and annotate T-4.5.4 and T-12.8.9, which stay open, each citing the tests the TEST-1 intro names.
11. **Plan.** Tick the P5b bullet and write its Outcome.

## Testing

Tests are stated as intents. Each intent is pinned by at least one test at each tier it names and carries a `// spec:` annotation naming the sections it exercises, such as `// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle)`. Every tier-2-and-higher test carries a `// diagnosis:` comment above the function stating what a failure means. Each intent listed under a deliverable lands in that deliverable's checklist step, together with every update to an existing test that the deliverable's code breaks. A step that adds a test file at tier 2 or above under `tests/` maps it in `tests/spec-map.json`.

**Failures the tests find.** The owner's triage rule applies to every failure a test exposes during implementation. Fix in this proposal a failure that is relevant to it, meaning the faulty code is a line this proposal changes or the defect stops one of its intents from holding, and a failure whose fix is straightforward and not owned elsewhere, meaning it changes no spec text, schema, migration, flag, or chart value and stays within one package. File everything else: add a `BUILD-GAPS.md` finding with the next free identifier, skip only the failing case with a `t.Skip` reason that opens with `blocked:` and the finding id, and record a deviation entry naming the intent, the finding, and the skip.

### Tests that land with MIG-1

- **T2.1 (tier 2).** Name migration 0188 so `scripts/lint-migrations.sh` rule 3 finds its test. After the up migration, `pg_constraint` holds no foreign key on `audit_redaction_receipts`. Redact a dead-lettered row, age it past `GeneralCutoff` with `SIEMConfigured` false, and run `PruneRetention`: the row is deleted, the receipt remains and its signature still verifies, and no 23503 is raised. `auditstore.DeleteByTenant` on the same tenant then succeeds and leaves the receipts and the `gdpr.%` rows. The down migration re-adds the constraint `NOT VALID`.
- Retarget the foreign-key assertions in `migrations/audit_redaction_receipts_test.go`.

### Tests that land with CODE-1

- **T1.1 (tier 1).** Table over both validators: a missing ranked name, a name both wired and exempt, an exempt name with an empty reason, an unknown name, a duplicate, and a rank inversion on the tenant path each fail with the store and path named; the full **Store table** wiring passes on both paths.
- **T1.2 (tier 1).** The `canonicalRank` name set equals an explicit list annotated with `// spec: 12.8`, so adding a store forces a reviewed edit of that list.
- **T1.3 (tier 1).** A builder-posture table (no Redis, no Postgres, semantic cache off, no messaging coordinator, and full posture over test doubles) builds both lists, passes both validators, and lists each absent backend by name with a nil function. A row in `release_wiring_test.go` pins the semantic cache argument of the `buildTenantErasers` call in `buildAdminRouter`, which X1 cannot observe because the cache is in-process.
- **T1.4 (tier 1).** The Orchestrator and `tenantEraser` skip a nil-function entry and leave its name out of the count map, and a non-nil entry after it still runs.
- **T0.1 (tier 0).** The exemption guard of CODE-1 item 6 fails on a fixture importer, and on a fixture in which token-service code calls `Put` on the access-token cache with a message naming the `redis_caches` exemption.

### Tests that land with CODE-2

- **T1.5 (tier 1).** `MemStream.PurgeTenant` removes only the tenant's entries. `Pipeline.DeleteByTenant` purges the stream before the durable rows and drops the tenant's buffered events, and a stream purge error leaves the durable rows untouched.
- **T2.2 (tier 2, Redis Cluster profile).** `RedisCache.DeleteByTenant` and `Tier.PurgeTenant` remove `acme`'s sticky keys, stream, and group and leave `globex`'s untouched. Each is idempotent, a purge of a tenant with no stream succeeds, and a `Flush` on the same `Tier`, built with an `Inserter` and published to before the purge, leaves no stream key whatever error it returns, which is the case that discriminates the kept `known` entry.

### Tests that land with CODE-3

- **T1.6 (tier 1).** An exempt tenant's `billing_buffer` and `billing` entries return `(0, nil)` and touch nothing. A non-exempt tenant's are purged. When the tenant read fails, both entries return the error and touch neither the stream nor the durable rows. A Phase 4 re-run after `tenant_config` leaves an exempt tenant's billing event count unchanged; this case is the one that discriminates the keep list.
- **T1.7 (tier 1).** `tenant_config` nulls every field outside the keep list, including `ErasureSalt`, and leaves every kept field equal. A reflection check over `tenantstore.Tenant` fails when a new field is neither nulled nor kept.
- **T1.8 (tier 1).** `delegation_budget` calls `PurgeRoot` only for root sessions, `tenant_definitions` treats `ErrNotFound` as deleted, `forEachTenantSession` stops at the first error, and `audit` makes no `DeleteByTenant` call for `jwtaudit.PlatformTenantID`.
- **T1.20 (tier 1).** In `pkg/gateway/externalapi/admin/legalhold_escrow_test.go`, an artifact-hold clear whose catalog `Get` finds no row calls `ReleaseForArtifact` with the tenant and artifact ID and answers 200 with `escrowReleased`, zero releases answer 404, and a release error answers 500 with no legal-hold audit event.
- **T2.3 (tier 2).** Against real Postgres, Redis, and MinIO, the store methods each new tenant entry calls delete `acme`'s rows, keys, or objects, leave a `globex` twin untouched, and return success on a second run. For `artifacts`, run through the cataloging wrapper, `acme` also has a `legal_hold = true` catalog row, and no `artifact_store` row of `acme` remains; this case discriminates CODE-3 item 10.
- **T2.4 (tier 2).** A `DeleteByTenant` subtest in `tests/tier2_component/stores/miniostore_test.go` (closes the MinIO half of T-4.5.4).
- **T1.21 (tier 1).** In `cmd/lenny-gateway`, `allTenantsLister` over the in-memory tenant store returns `default` and a tombstoned tenant that `tenantsLister` omits. For CODE-3 item 7: with a tombstoned `default` row, `tenantsLister` omits `default` and `allTenantsLister` returns it once; with no `default` row, both return `default`; and over a store whose `Get` fails with an error other than `ErrNotFound`, `tenantsLister` returns that error. The tombstoned-`default` omission is the assertion that discriminates item 7. Rows in `release_wiring_test.go` pin `allTenantsLister{w.tenants}` in the `billingretention.New` call and `tenantsLister{w.tenants}` in the `NewFlusherManager` call.
- **T1.25 (tier 1).** In `cmd/lenny-gateway`, the revocation cache rehydrated through `allTenantsLister` over the in-memory tenant store, seeded with a tombstoned tenant, and an issued-token fake whose `ListRevoked` returns a revoked `jti` inside the trailing hour for that tenant, reports the `jti` revoked; the same rehydration through `tenantsLister` does not, which is the assertion that discriminates CODE-3 item 7 for the §13.3 rehydration. A row in `release_wiring_test.go` pins `allTenantsLister{w.tenants}` as the lister the startup and resync `w.revCache.Rehydrate` calls in `billing_security_workers.go` use, and a second row pins `tenantsLister{w.tenants}` as the lister `sweepIdempotencyKeys` uses, which keeps it. The test carries `// spec: 13.3 (Credential Flow: Authoritative durability for revocation)`.

### Tests that land with CODE-4

- **T1.9 (tier 1).** User erasure over test doubles calls each checkpoint adapter, the `coordination_mirror` adapter, and both `TokenStore` stores once per listed session or once per user, as the **Store table** states.
- **T2.6 (tier 2).** `issuedtokenstore.DeleteByUser` and `DeleteByTenant` each keep an unrevoked unexpired row and a revoked row inside the window, and each delete a row past it. After the delete, `Revoke` of the kept unrevoked row succeeds, and a rehydration still rejects the kept revoked token. The `Revoke` assertion is the one that tells the step 18 rule from the revoked-only rule.
- **T1.22 (tier 1).** In `cmd/lenny-gateway`, one `sweepIssuedTokens` pass over a recording `DeleteExpired` fake and `allTenantsLister` over the in-memory tenant store, seeded with a tombstoned tenant, calls `DeleteExpired` once per returned tenant, the tombstoned one included, with CODE-4 item 4's cutoff, bracketed by the clock read before and after the pass; that bracket is the assertion that discriminates the cutoff. A `DeleteExpired` error for one tenant does not stop the pass. Visiting the tombstoned tenant is the assertion that discriminates the lister. A row in `release_wiring_test.go` pins the `sweepIssuedTokens` start in `billing_security_workers.go`.
- **T4.1 (tier 4).** Extend `tests/tier4_integration/erasure_job_test.go`: after erasure, no checkpoint row, connector refresh token, registered credential, or mirror row of the user remains, and `bob`'s rows are untouched. A replica restart after erasure rebuilds the deny list, and the case records the bound of non-spec Edge case 6.

### Tests that land with CODE-5

- **T1.10 (tier 1).** `resolveHolds` returns a `ListLegalHeld` error and a `ListBySession` error, escrows every live catalog record of a session-held session exactly once when an artifact hold overlaps it and skips a soft-deleted one, and each `escrowLedger` event is appended under `jwtaudit.PlatformTenantID` with `target_tenant_id` set to the tenant. A second `EscrowHolds` for the same tenant after the held originals are deleted succeeds without reading an original and returns the same escrow keys; this case discriminates the re-entry skip. An `ActiveForArtifact` error makes `Migrate` return the error with no `Source.Read`, `Escrow.Write`, or `Ledger.Escrowed` call for that hold. `EscrowHolds` over distinct artifact and escrow stores reads originals only from the artifact store and writes only to the escrow store. Rows in `cmd/lenny-gateway/release_wiring_test.go` pin `escrowRecords` and the escrow store in the `tenantEscrowMigrator` literal in `buildAdminRouter`, and the escrow store in the Releaser's `blobEscrowDeleter`.
- **T2.7 (tier 2, MinIO).** With an escrow bucket configured, for a tenant with no row in the escrow namespace and for a T4 tenant, escrow `Put` succeeds, `DeleteByTenant` on the original tenant's artifact store leaves the escrow object readable, and the `Releaser` deletes it after the hold clears.
- **T1.11 (tier 1, tripwire).** `handleSetLegalHold` accepts only `sessionId` or `artifactId`, and a `go/ast` scan of `pkg/gateway/externalapi/admin` finds every `emitLegalHold` and `writeLegalHoldCleared` call passing `"session"` or `"artifact"`, apart from the `emitLegalHold` call inside `writeLegalHoldCleared` that forwards its `resourceType` parameter. The failure message names the `audit` tenant entry as the eraser to revisit.

### Tests that land with CODE-6

- **T1.12 (tier 1).** Extend `TestIsRegisteredImplementsAuthRegistry` with a table over `active`, `disabling`, `deleting`, `deleted` (built by `SoftDelete`), and suspended rows, for `Memory` and `Registered()`, and add a test in `cmd/lenny-gateway/auth_seams_test.go` that runs the same table over both registry adapters, which are in package `main`: only `deleting` and `deleted` are refused, and the `deleting` row, whose `DeletedAt` is zero, is the case that discriminates the `State` half of the registered reading. In the adapter test, a `default` row in `deleting` is refused, a missing `default` row is admitted, and a store error makes both adapters return an error, for `default` and for another tenant; the `default` store-error case is the one that discriminates item 3 from today's shortcut.
- **T1.13 (tier 1).** A `TENANT_NOT_FOUND` rejection emitted through `admin.NewAuditLogSink` is appended under the platform tenant with `target_tenant_id` as a top-level payload key; that top-level read is the assertion that discriminates item 4, and `TENANT_CLAIM_INVALID_FORMAT` keeps its placement. Retarget any test that asserts a `TENANT_NOT_FOUND` row on the inferred tenant's chain.
- **T2.8 (tier 2).** The pgstore `IsRegistered` agrees with `Registered()` for every row of T1.12's table.
- **T1.18 (tier 1).** On a `Router` whose `Options.TenancyMode` is `single`, and on one where it is empty, `DELETE` and force-delete of `default` return `409 INVALID_STATE_TRANSITION` and leave the row `active`. With `multi`, both start the lifecycle; that case is the one that discriminates.
- **T1.19 (tier 1).** Under a non-default `--tenant-id-claim`, through the `token_signing.go` verifier composition and `authmw.Wrap`, the following hold. A Token Service `user_bearer` resolves from `tenant_id` and is not refused with `TENANT_CLAIM_MISSING`. An embedded-key token whose `tenant_id` differs from the configured claim resolves from the configured claim. An embedded-key token that carries `tenant_id` but lacks the configured claim is refused with `TENANT_CLAIM_MISSING`, and that is the assertion that discriminates item 6's fail-closed rule. Retarget `TestBearerHonoursConfigurableTenantClaim` to the wrapper.
- **T1.23 (tier 1).** `POST /v1/admin/tenants` with the ID `platform` returns `422 RESERVED_IDENTIFIER` and creates no row, a bootstrap seed entry `platform` is reported as an `error` and creates no row, and a tenant named `platform-eu` is created by both paths.
- **T1.24 (tier 1).** In `pkg/gateway/externalapi/admin`, a bootstrap seed names a tenant in `deleting`, a tenant tombstoned by `SoftDelete`, and an `active` twin, each with a `displayName` that differs from its row, and runs once without and once with `--force-update`. Both runs report the `deleting` and tombstoned entries as `error` with `SEED_VALIDATION`, leave their `State`, `DeletedAt`, and `Version` unchanged, and provision no sequence for them, and the force-update run updates the `active` twin. The force-update run on the tombstone is the case that discriminates item 8.

### Tests that land with CODE-7

- **T1.14 (tier 1).** `leaserevoke_test.go` becomes a table over pool and user leases: every listed lease is released, and `Revoker` has no deny-list dependency. Rename the tests named "DenyLists...EveryListedLease".
- Retarget `tests/tier9_security/full_revoke_lease_return_test.go` to "returns every listed lease and deny-lists none". Its pool and user lease tokens are rejected with `LEASE_TOKEN_INVALID`, and `bob`'s proxy and direct leases on the pool credential are neither rejected nor rotated, with the rotation hook and `RevokeCredential` never called; that last assertion is the one that discriminates CODE-7.
- Retarget `tests/tier8_chaos/full_revoke_denylist_drop_test.go`: with no deny-list entry on any replica, a peer replica rejects the returned lease token of each source.
- Retarget `tests/tier4_integration/full_revoke_two_replica_test.go` and `cmd/lenny-gateway/user_revocation_rotation_test.go`: the deny list stays empty, and every Release precedes every cancel write.

### Tests that land with CODE-8

- **T1.15 (tier 1).** `pausePhase` in `pkg/controller/tenantdeletion/controller_test.go`: a Phase 3 or Phase 4 action error keeps the job in phase with `BlockedReason` set and neither `Phase` nor `State` changed, and the next pass that succeeds advances and clears it. A nil `Revoker` or nil `Eraser` fails the job.
- **T1.16 (tier 1).** `tenantCredentialRevoker` commits `deleting` before any other call, lists with `ListFilter{Primary: true}`, calls the lease step over every listed session, terminal ones included, then both `TokenStore` deletes, and returns a listing or delete error. When `tenants.Update` fails, it returns that error and makes no session listing, no `RevokeUserLeases` call, and no `TokenStore` delete. A second run is idempotent.

### Tests that land with CODE-9

- **T1.17 (tiers 1 and 3).** A full_revoke matrix over session states (each non-terminal state and each terminal state), a per-session cancel error, a commit-then-error write whose re-read finds `cancelled` and one whose re-read fails, a client disconnect mid-call, a session created just before the call and visible only on the primary, an elicitation dismissal error, and a listing error. Each case asserts the counts, `failedSessions`, `elicitationDismissalError`, the status code, the audit event, and that the tokens are revoked. The re-read-finds-`cancelled` case also asserts one `AfterRevokeWrite` call carrying the re-read row, and the re-read-fails case asserts none; that is the assertion that discriminates CODE-9 item 3. The tier-3 half pins the response body fields.
- Extend `tests/tier4_integration/full_revoke_two_replica_test.go` with a case that cancels the request context after `users.Update` commits; the test's existing end-state assertions hold. The Postgres session store fails a write on a cancelled context and the tier-1 memory stores do not, so this case is the one that discriminates CODE-9 item 1.
- Update `users_invalidate_fanout_test.go` and `users_revoke_hook_test.go` for the primary listing, terminal-session lease return, and `failedSessions`.

### TEST-1 · Cross-cutting lifecycle scenarios

These scenarios exercise the deliverables together and land after the code steps. TEST-1 closes the MinIO half of T-4.5.4 (with T2.4) and extends T-12.8.9 with Phases 3 and 4 against real backends. T-12.1.3 and T-12.8.4 are already covered by `tests/tier4_integration/erasure_job_test.go` and `tests/tier9_security/erasure_isolation_test.go`, and REC-1 item 10 records all four entries.

- **Harness.** Extend `tests/testinfra/lifecyclefence`. `Deletion` gains `ThroughPhase4` and a takeover that rebuilds the job at Phase 3, as `ensureJob` does for a `deleting` tenant. Its Phase 3 revoker and Phase 4 eraser list are harness seams that follow `tenantCredentialRevoker` and the **Store table**, as its Phase 1 and Phase 2 seams follow cmd/lenny-gateway's, because package `main` cannot be imported; X1 and T1.3 pin the production builders. `World.Check` gains the residue invariant: no `acme` or `alice` row, key, or object remains outside the `gdpr.%` rows, the receipts, the tombstone, the escrow, and the audit rows on the platform chain, and `bob` and `globex` are unchanged.
- **X1 (tier 4).** Extend `tenant_deletion_phase4_test.go` with the residue check after Phase 6. A Lenny-issued bearer and a playground bearer, and an OIDC bearer where the tier-4 gateway harness issues one, are refused from Phase 3 and through a Phase 3.5 pause on a peer replica. X1 mints its playground bearer before Phase 3, because the cookie mint runs no registry check (row 5 of **Tenant liveness and the default tenant**). A refused bearer after the audit eraser leaves no non-`gdpr.%` row for `acme`. Its gateway starts with `--tenancy-mode=multi --multi-tenant=true`, which the T-10.2.11 startup gate requires (summary D26), so the test's `adminDo` sends `X-Lenny-Tenant-ID: default` in place of `acme`, which the registry refuses before the bootstrap seed creates it and again from Phase 3. The gateway also starts with `--redis-url` and `--messaging-durable-inbox`, as `TestErasureDeletesTheErasedUsersSessionMessageQueues_spec_12_8` does, and the residue check covers `acme`'s sticky keys, billing stream, delegation-budget keys, and session DLQ and durable-inbox keys against `globex` twins; those keys are the case that discriminates a nil Redis-backed argument at the `buildTenantErasers` call site.
- **X2 (tier 7a).** Add Phase 3, Phase 4, and full_revoke continuation operations to the lifecyclefence randomized interleavings under `-race`, against concurrent terminal writes and lease renewals.
- **X3 (tier 8).** Extend the `TestDeletionPhase2FaultsAndTakeover` pattern in `lifecycle_drain_fault_test.go` to faults at each Phase 3 step and each Phase 4 entry, a crash between entries, and a takeover after a Phase 3 step and after a Phase 4 entry. Each case asserts the job pauses in phase and the next pass completes with the residue invariant holding.
- **X4 (tier 9).** Cross-tenant isolation for every new tenant entry, `forEachTenantSession`, the sticky `ScanDelete`, the stream purge, and the registry refusal: an `acme` deletion never touches a `globex` row, key, object, or bearer.
- **X5 (tier 11).** A test in `tests/tier11_docs/` asserts that `multi-tenancy.md` names neither `RevokeByTenant` nor "all mutable fields nulled" and that its Phase 3 row names `TENANT_NOT_FOUND`, that the `TENANT_NOT_FOUND` row of `security.md` names the `deleting` state, and that `admin.md` names `RESERVED_IDENTIFIER` in the create entry and `INVALID_STATE_TRANSITION` in the delete entry. The test is registered in `documentConsistencyGates`.

## Edge cases and accepted failure modes

1. **A lease-store error is silent.** `LeasesBySession` and the lease store's `Remove` return no error, so a Postgres error during the lease step of CODE-8 item 1 or CODE-9 leaves a lease record in place, and its token stays valid until the lease expires, the stale-slot reaper reclaims it, or a repeated call returns it. REC-1 files the signature change.
2. **The access-token cache flush is unwired.** The cache has no production writer, so §12.8 Phase 3 and user-path step 3 flush nothing. The tier-0 guard of CODE-1 item 6 fails when a writer appears.
3. **F-12.8.35.** Without Postgres, the preStop barrier's private in-memory checkpoint metadata store is erased by no eraser. Its contents are lost at process restart.
4. **A paused Phase 4 reruns every entry.** The rerun happens every `--tenant-deletion-interval` with no backoff, as Phase 2 does. Counts from a failed attempt are not merged, so the receipt can show 0 for a store an earlier attempt emptied.
5. **A billing stream after the tombstone.** Staged events flush while the tenant's flusher runs, and the flusher stops at the tombstone, because `FlusherManager` visits only listed tenants and no path returns a tombstone to the listing (rows 8 and 20 of **Tenant liveness and the default tenant**), so events still staged then stay in the stream. After a purge, a replica whose flusher starts before the tombstone recreates an empty stream and its group, and a publish on any replica recreates a stream with no group, which the Phase 2 drain makes improbable.
6. **User erasure deletes revoked registered credentials.** The §4.9 startup deny-list rebuild reads revoked `credentials` rows, so a lease still live on an unreleased remote binding (F-12.8.43) loses its user-shaped deny entry after a replica restart. The bound is the lease TTL, or `claimOrphanTimeout` plus one GC sweep.

## Files touched on application (non-spec)

- `charts/lenny/values.yaml` (comment)
- `cmd/lenny-gateway/adminrouter.go`
- `cmd/lenny-gateway/auth_seams.go`
- `cmd/lenny-gateway/billing_pipeline.go`
- `cmd/lenny-gateway/billing_security_workers.go`
- `cmd/lenny-gateway/credential_revocation.go`
- `cmd/lenny-gateway/flags.go`
- `cmd/lenny-gateway/httpsurface.go`
- `cmd/lenny-gateway/leader_sweeps.go` (comment)
- `cmd/lenny-gateway/legalholdescrow.go`
- `cmd/lenny-gateway/playground_seams.go`
- `cmd/lenny-gateway/tenant_erasers.go`
- `cmd/lenny-gateway/tenantdeletion.go`
- `cmd/lenny-gateway/token_signing.go`
- `cmd/lenny-gateway/user_revocation.go`
- `cmd/lenny-gateway/worker_seams.go`
- `migrations/0188_audit_redaction_receipts_drop_fk.up.sql` and `.down.sql`
- `pkg/auth/jwt/` (new tenant-claim wrapper)
- `pkg/blobstore/artifactcatalog/catalog.go` and `pkg/blobstore/cataloging/cataloging_test.go`
- `pkg/controller/tenantdeletion/controller.go`
- `pkg/gateway/audit/auditstore/auditstore.go` (comments)
- `pkg/gateway/billing/billingstore/failover/failover.go`, `memstream.go`, and `redisstream/redisstream.go`
- `pkg/gateway/credentials/leaserevoke/leaserevoke.go`
- `pkg/gateway/environment/tenantstore/tenantstore.go` and `pgstore/pgstore.go`
- `pkg/gateway/experiment/experimentsticky/experimentsticky.go`
- `pkg/gateway/externalapi/admin/audit_sink.go`, `bootstrap.go`, `forcedelete.go`, `tenants.go`, and `users.go`
- `pkg/gateway/middleware/auth/auth.go`
- `pkg/gateway/storage/erasure/erasure.go` and `order.go`
- `pkg/gateway/storage/issuedtokenstore/issuedtokenstore.go`
- `pkg/gateway/storage/leasestore/leasestore.go` and `pgstore/pgstore.go` (comments)
- `pkg/legalholdescrow/migrator.go`, and `escrow.go` and `records.go` (comments)
- `docs/operator-guide/multi-tenancy.md`, `docs/operator-guide/lenny-ctl.md`, `docs/operator-guide/security.md`, `docs/client-guide/authentication.md`, and `docs/api/admin.md`
- `BUILD-GAPS.md`, `TEST-GAPS.md`, `scripts/seed-claim-register.py`, `tests/claim-map.json`, and `gateway-runtime-comms-remediation.md`
- The tests named under **Testing**
