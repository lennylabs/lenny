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
| `audit` | 130 | `EventStore` (audit) | `auditstore.DeleteByTenant`, which lands with or after MIG-1. | User-scoped `auditstore.DeleteByUser`, the step-13 no-op. |
| `billing` | 150 | `EventStore` (billing) | `failover.Pipeline.DeleteByTenant` (CODE-2), gated by the exempt check of CODE-3 item 3. | Exempt: the erasure job's billing phase pseudonymizes billing events (`erasurejob.BillingEraser`, §12.8 step 15). |
| `eval_results` | 155 | `EvalResultStore` | Unchanged entry at the new rank. | Unchanged. |
| `delegation_budget` | 160 | Delegation budget keys | Session-keyed `treebudget.PurgeRoot` for each root session (CODE-3 item 4), then the `delegationbudget` pgstore `DeleteByTenant`. nil without the tree budget. | Unchanged. |
| `sessions` | 170 | `SessionStore` | Unchanged. | Unchanged. |
| `tokens` | 180 | `TokenStore` | `connectorcredstore` `DeleteByTenant`, then `credentialstore` `DeleteByTenant`. | `connectorcredstore` `DeleteByUser`, then `credentialstore` `DeleteByUser`. |
| `issued_tokens` | 185 | `TokenIssuanceStore` | `issuedtokenstore` `DeleteByTenant`. nil without Postgres. | `issuedtokenstore` `DeleteByUser` under CODE-4 item 3. nil without Postgres. |
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

Targets: `cmd/lenny-gateway/tenant_erasers.go` (`buildTenantErasers`, `tenantErasersBeforeSessions`, and a new `forEachTenantSession`), the `buildTenantErasers` call site in `cmd/lenny-gateway/adminrouter.go`, `cmd/lenny-gateway/billing_pipeline.go` (keep a handle on the billing stream), the billing retention pruner's tenant lister in `cmd/lenny-gateway/worker_seams.go`, `pkg/blobstore/artifactcatalog/catalog.go` (`PgStore.DeleteByTenant` and the `Store` interface comment), `pkg/blobstore/cataloging/cataloging_test.go`, and the F-12.8.37 entry of `BUILD-GAPS.md`.

1. `forEachTenantSession(ctx, tenantID, fn)` lists `sessions.List(ctx, tenantID, ListFilter{Primary: true})` and calls `fn` for each session ID, returning the first error. Every session-keyed entry of the **Store table** uses it.
2. Wire every tenant-path cell of the **Store table**. The call site passes the semantic cache, the sticky cache, the billing stream and pipeline, the audit store, the two `TokenStore` stores, the tenant access, user, custom role, connector, environment, experiment, and tenant stores, the coordination mirror store, the tree budget, the delegation budget pgstore, and the messaging coordinator.
3. **Exempt billing check.** The `billing_buffer` and `billing` entries each read the tenant row and return `(0, nil)` without touching any store when `BillingErasurePolicy` is `tenantstore.BillingErasureExempt`. A tenant-row read error is returned.
4. **Delegation budget roots.** The `delegation_budget` entry calls `PurgeRoot` only for a listed session whose `RootSessionID` is empty or equal to its `ID`.
5. **Environment idempotency.** The `tenant_definitions` entry treats `environmentstore.ErrNotFound` from `Delete` as deleted.
6. **Tenant configuration record.** The `tenant_config` entry calls `tenants.Update` with a mutate that nulls every field of `tenantstore.Tenant` except this keep list: `ID`, `State`, `DeletedAt`, `WorkspaceTier`, `DataResidencyRegion`, `BillingErasurePolicy`, `ForceDeleteHoldOverride`, `ForceDeleteJustification`, `ForceDeleteBy`, `ForceDeleteAt`, `CreatedAt`, `UpdatedAt`, and `Version`. Nulling `ErasureSalt` deletes the sealed salt ciphertext. `BillingErasurePolicy` stays on the list because a Phase 4 re-run reads it in item 3, and an empty policy reads as pseudonymize.
7. **Billing retention after the tombstone.** The billing retention pruner lists tenants with `ListFilter{IncludeDeleted: true}`, so an exempt tenant's billing events age out after its tombstone. `FlusherManager` keeps the current lister.
8. Audit and billing `DeleteByTenant` run on the connection the Phase 4 erasers already use, which is the posture `PruneRetention` runs under.
9. **F-12.8.37.** Close F-12.8.37 in `BUILD-GAPS.md` with a resolution note in this step. `TestFiledSuspendResumeFindingsMatchTheTree_spec_7_2` reads the finding's state against an `EraseSession` selector in the body of the methods `buildTenantErasers` or `tenantErasersBeforeSessions`, so the `session_message_queues` entry names `EraseSession` inside one of those two methods.
10. **Held catalog rows.** `artifactcatalog.PgStore.DeleteByTenant` drops its `legal_hold = false` guard, so the `artifacts` entry deletes the catalog rows of the originals Phase 3.5 escrowed (summary D5). Rewrite its doc comment and the `Store` interface's `DeleteByTenant` comment to name Phase 3.5 as the hold gate. In `pkg/blobstore/cataloging/cataloging_test.go`, change `fakeCatalog.DeleteByTenant` and `TestDeleteByTenantPreservesLegalHeldCatalogRow` so the held row is deleted.

### CODE-4 · User-path wiring

Targets: `cmd/lenny-gateway/adminrouter.go` (the user eraser list and `buildSessionScopedErasers`), `pkg/gateway/storage/leasestore/leasestore.go` and `pkg/gateway/storage/leasestore/pgstore/pgstore.go` (`DeleteByUser` doc comments only), and `pkg/gateway/storage/issuedtokenstore/issuedtokenstore.go` (`DeleteByUser`).

1. Wire every user-path cell of the **Store table**. Conditional appends become entries listed by name with a nil function in the posture that lacks the backend.
2. Rewrite both `DeleteByUser` comments in the lease stores. Each states that the drain runs each session's terminal pipeline before the erasers (0094 D21), and that a residual coordination lease expires within its own TTL, which is `sessionserver.DefaultCoordinationLeaseTTL` at bind and four `--coordination-interval` periods after a Sweeper takeover. Delete the claim that the orchestrator releases leases per session.
3. `issuedtokenstore.DeleteByUser` adds `AND exp <= now() - interval '1 hour'` to its delete, which deletes only the rows SPEC-5 step 18 deletes. Delete its doc comment's claim that a deleted token can no longer be presented.

### CODE-5 · Phase 3.5 escrow

Targets: `cmd/lenny-gateway/legalholdescrow.go` (`blobEscrowWriter`, `blobEscrowDeleter`, `resolveHolds`, `escrowLedger`, `tenantEscrowMigrator`, and their doc comments), the escrow wiring in `cmd/lenny-gateway/adminrouter.go`, `pkg/legalholdescrow/migrator.go` (`Migrator`, `Migrate`), and the doc comments in `pkg/legalholdescrow/escrow.go` and `pkg/legalholdescrow/records.go`.

1. At startup, build one escrow blob store with `blobproviderflags.Resolve`. It uses the artifact store's provider, credentials, and endpoint unless `--legal-hold-escrow-endpoint` overrides the endpoint, sets `Bucket` and `MinIOBucket` to the configured escrow bucket (`escrowCfg.Default.Bucket`), and passes a nil `SSEKeyResolver`, because the payload is already sealed under the escrow KEK. When no escrow bucket is configured, build no store; the migrator then fails closed with `ErrRegionUnresolvable`.
2. `blobEscrowWriter` and `blobEscrowDeleter` both use that store. The URI keeps `TenantID` set to the original tenant ID, which gives the §12.8 `legal-hold-escrow/{original_tenant_id}/...` layout inside a bucket that the artifact-store prefix delete never reaches. The deleter's `Tombstoner` assertion runs against that store.
3. `escrowLedger` appends each ledger event under `jwtaudit.PlatformTenantID` with the original tenant ID as the payload's `target_tenant_id` (§16.7), so the tenant `audit` entry never deletes it.
4. Do not write the escrow object under `jwtaudit.PlatformTenantID` or through `w.objectStore`. The tenant SSE resolver refuses a tenant with no row, and `platform` is a tenant ID an admin can create.
5. `resolveHolds` returns its error. A `ListLegalHeld` error is returned instead of dropped. For a session hold, it lists `m.artifacts.ListBySession` and appends one `HeldArtifact` per record not yet seen whose `State` is `artifactcatalog.StateLive`, populated as the artifact branch populates it (`ResourceID` the base64url of `rec.URI`, `BlobURI`, `HoldSetAt`, `SessionID`, and `ArtifactURI`). A listing error aborts Phase 3.5.
6. `legalholdescrow.Migrator` gains a `Records RecordStore` field, which `tenantEscrowMigrator` sets from `escrowRecords` in `buildAdminRouter`. Before reading a hold's original, `Migrate` calls `Records.ActiveForArtifact(ctx, in.TenantID, h.ArtifactURI)`. A non-empty result skips the hold and adds its `EscrowObjectKey` to the result, and a lookup error aborts the migration (§12.8 Phase 3.5 re-entry). A nil `Records` skips the lookup.
7. Rewrite each comment that describes a Phase 4 skip or a `legal_hold_escrow: true` marker to state that Phase 4 deletes the originals and that the ledger record is the release pointer.

Per-region escrow clients (`storage.regions.*`) stay out of scope, because `escrowConfigFromFlags` produces only `Default`.

### CODE-6 · One tenant registration predicate

Targets: `pkg/gateway/environment/tenantstore/tenantstore.go` (new `Tenant.Registered`, `Memory.IsRegistered`), `pkg/gateway/environment/tenantstore/pgstore/pgstore.go` (`IsRegistered`), `cmd/lenny-gateway/auth_seams.go` (`bearerTenantRegistry`, `authFailureAuditAdapter`), `cmd/lenny-gateway/playground_seams.go` (`playgroundTenantRegistry`), the `admin.NewRouter` call in `buildAdminRouter` (`cmd/lenny-gateway/adminrouter.go`), `pkg/auth/jwt/` (a new tenant-claim verifier wrapper beside `ClaimChecker`), `cmd/lenny-gateway/token_signing.go` (`embeddedHMACVerifier`), `pkg/gateway/middleware/auth/auth.go` (`serveBearer`, `Options.TenantClaimName`), the `authmw.Options` literal in `cmd/lenny-gateway/httpsurface.go`, `pkg/gateway/externalapi/admin/tenants.go` (`AuditEvent`) and `forcedelete.go` (`handleDeleteTenant`, `handleForceDeleteTenant`), `pkg/gateway/externalapi/admin/audit_sink.go` (`ChainAuditSink.EmitAdminEvent`), and the `auth.tenantIdClaim` comment in `charts/lenny/values.yaml`.

1. `Tenant.Registered()` returns true when `DeletedAt` is zero and `State` is neither `TenantStateDeleting` nor `TenantStateDeleted`. `IsActive` is unchanged.
2. `Memory.IsRegistered` returns `Registered()` of the row. The pgstore `IsRegistered` selects `deleted_at IS NULL AND state NOT IN ('deleting', 'deleted')`.
3. `bearerTenantRegistry` and `playgroundTenantRegistry` read the row first. A found row answers `Registered()`, the default tenant included. `ErrNotFound` answers true only for the default tenant. Any other error is returned.
4. `authFailureAuditAdapter` writes a `TENANT_NOT_FOUND` rejection to the platform chain (empty `ActorTenantID`) and sets the inferred tenant as the event's `TargetTenantID`. `admin.AuditEvent` gains `TargetTenantID`, which `ChainAuditSink.EmitAdminEvent` writes as the payload's top-level `target_tenant_id` when non-empty, because the §11.7 CMP-058 residency gate reads only that key and never one under `detail`. It never appends to the refused tenant's chain. The other rejection reasons keep their placement.
5. `Router` gains a `WithMultiTenant` setter, which `buildAdminRouter` wires from `--multi-tenant`. When the setting is false, `handleDeleteTenant` and `handleForceDeleteTenant` answer a request for `auth.DefaultTenantID` with `409 INVALID_STATE_TRANSITION` before any state write (SPEC-2 Edit 2b). A `Router` built without the setter behaves as multi-tenant.
6. The configured tenant claim (SPEC-3 Edit 3a) applies only to a token that an IdP key verified. A wrapper in `pkg/auth/jwt` beside `ClaimChecker` sets `Claims.TenantID` to `ClaimString(name)` of every token its inner verifier accepts, and to an empty value when that claim is absent, never keeping the token's own `tenant_id`. `embeddedHMACVerifier` wraps the embedded OIDC member with it under `--tenant-id-claim`, and the Token Service member is not wrapped. `serveBearer` reads `claims.TenantID` for every bearer, and `Options.TenantClaimName` is deleted. The discriminator is the verifying key, never a payload claim such as `iss` or `typ`. In the `auth.tenantIdClaim` comment, replace "on every Bearer JWT" with "on a bearer an IdP key verified; a gateway-issued token uses `tenant_id`".

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
   3. Call `connectorcredstore` `DeleteByTenant`, then `credentialstore` `DeleteByTenant`.
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

Targets: `docs/operator-guide/multi-tenancy.md` (the tenant deletion phase table rows for Phases 3 and 4, the bullet under **Legal Holds** on held resources during tenant deletion, and the roles sentence after the legal-hold commands) and `docs/operator-guide/lenny-ctl.md` (the **Min Role** column of the **Legal Hold Management** table).

1. Phase 3 row: "From this phase the gateway rejects every bearer token of the tenant with `403 TENANT_NOT_FOUND`. The tenant's connector tokens and registered credentials are deleted, and the credential leases its sessions still hold are returned." The row names no internal store method.
2. Phase 4 row: a summary in the spec's dependency order (caches and artifacts, then session-scoped stores, audit and billing events, sessions, tokens and credentials, and finally the tenant's access rows, roles, and configuration). Replace "(respecting `billingErasurePolicy` and legal holds)" with the billing rule of summary D18, and point to **Legal Holds** for force-delete behavior.
3. **Legal Holds** bullet: an active hold blocks Phase 4, and on force-delete the held evidence is copied to the legal-hold escrow before Phase 4 deletes the originals (summary D5).
4. After the roles sentence, and in the `lenny-ctl.md` role column: "A tenant-admin of a tenant in the `deleting` state cannot call the API. A platform-admin releases its holds or force-deletes it." (summary D11).

Each sentence that depends on D5, D11, or D18 follows the decision as it stands when DOCS-1 lands.

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
- **T1.3 (tier 1).** A builder-posture table (no Redis, no Postgres, semantic cache off, no messaging coordinator, and full posture over test doubles) builds both lists, passes both validators, and lists each absent backend by name with a nil function.
- **T1.4 (tier 1).** The Orchestrator and `tenantEraser` skip a nil-function entry and leave its name out of the count map, and a non-nil entry after it still runs.
- **T0.1 (tier 0).** The exemption guard of CODE-1 item 6, with a fixture importer that makes it fail.

### Tests that land with CODE-2

- **T1.5 (tier 1).** `MemStream.PurgeTenant` removes only the tenant's entries. `Pipeline.DeleteByTenant` purges the stream, the buffer, and the durable rows in that order, and a stream purge error leaves the durable rows untouched.
- **T2.2 (tier 2, Redis Cluster profile).** `RedisCache.DeleteByTenant` and `Tier.PurgeTenant` remove `acme`'s sticky keys, stream, and group and leave `globex`'s untouched. Each is idempotent, a purge of a tenant with no stream succeeds, and a `Flush` on the same `Tier`, built with an `Inserter` and published to before the purge, leaves no stream key whatever error it returns, which is the case that discriminates the kept `known` entry.

### Tests that land with CODE-3

- **T1.6 (tier 1).** An exempt tenant's `billing_buffer` and `billing` entries return `(0, nil)` and touch nothing. A non-exempt tenant's are purged. When the tenant read fails, both entries return the error and touch neither the stream nor the durable rows. A Phase 4 re-run after `tenant_config` leaves an exempt tenant's billing event count unchanged; this case is the one that discriminates the keep list.
- **T1.7 (tier 1).** `tenant_config` nulls every field outside the keep list, including `ErasureSalt`, and leaves every kept field equal. A reflection check over `tenantstore.Tenant` fails when a new field is neither nulled nor kept.
- **T1.8 (tier 1).** `delegation_budget` calls `PurgeRoot` only for root sessions, `tenant_definitions` treats `ErrNotFound` as deleted, and `forEachTenantSession` stops at the first error.
- **T1.20 (tier 1).** In `pkg/gateway/externalapi/admin/legalhold_escrow_test.go`, an artifact-hold clear whose catalog `Get` finds no row calls `ReleaseForArtifact` with the tenant and artifact ID and answers 200 with `escrowReleased`, zero releases answer 404, and a release error answers 500 with no legal-hold audit event.
- **T2.3 (tier 2).** Against real Postgres, Redis, and MinIO, each new tenant entry deletes `acme`'s rows, keys, or objects, leaves a `globex` twin untouched, and returns success on a second run. For `artifacts`, run through the cataloging wrapper, `acme` also has a `legal_hold = true` catalog row, and no `artifact_store` row of `acme` remains; this case discriminates CODE-3 item 10.
- **T2.4 (tier 2).** A `DeleteByTenant` subtest in `tests/tier2_component/stores/miniostore_test.go` (closes the MinIO half of T-4.5.4).
- **T2.5 (tier 2).** The billing pruner deletes an exempt tombstoned tenant's billing events past `billing.retentionDays`.

### Tests that land with CODE-4

- **T1.9 (tier 1).** User erasure over test doubles calls each checkpoint adapter, the `coordination_mirror` adapter, and both `TokenStore` stores once per listed session or once per user, as the **Store table** states.
- **T2.6 (tier 2).** `issuedtokenstore.DeleteByUser` keeps an unrevoked unexpired row and a revoked row inside the window, and deletes a row past it. After the delete, `Revoke` of the kept unrevoked row succeeds, and a rehydration still rejects the kept revoked token. The `Revoke` assertion is the one that tells the step 18 rule from the revoked-only rule.
- **T4.1 (tier 4).** Extend `tests/tier4_integration/erasure_job_test.go`: after erasure, no checkpoint row, connector refresh token, registered credential, or mirror row of the user remains, and `bob`'s rows are untouched. A replica restart after erasure rebuilds the deny list, and the case records the bound of non-spec Edge case 6.

### Tests that land with CODE-5

- **T1.10 (tier 1).** `resolveHolds` returns a `ListLegalHeld` error and a `ListBySession` error, escrows every live catalog record of a session-held session exactly once when an artifact hold overlaps it and skips a soft-deleted one, and each `escrowLedger` event is appended under `jwtaudit.PlatformTenantID` with `target_tenant_id` set to the tenant. A second `EscrowHolds` for the same tenant after the held originals are deleted succeeds without reading an original and returns the same escrow keys; this case discriminates the re-entry skip. An `ActiveForArtifact` error makes `Migrate` return the error with no `Source.Read`, `Escrow.Write`, or `Ledger.Escrowed` call for that hold. `EscrowHolds` over distinct artifact and escrow stores reads originals only from the artifact store and writes only to the escrow store. Rows in `cmd/lenny-gateway/release_wiring_test.go` pin `escrowRecords` and the escrow store in the `tenantEscrowMigrator` literal in `buildAdminRouter`, and the escrow store in the Releaser's `blobEscrowDeleter`.
- **T2.7 (tier 2, MinIO).** With an escrow bucket configured, for a tenant with no row in the escrow namespace and for a T4 tenant, escrow `Put` succeeds, `DeleteByTenant` on the original tenant's artifact store leaves the escrow object readable, and the `Releaser` deletes it after the hold clears.
- **T1.11 (tier 1, tripwire).** `handleSetLegalHold` accepts only `sessionId` or `artifactId`, and a `go/ast` scan of `pkg/gateway/externalapi/admin` finds every `emitLegalHold` and `writeLegalHoldCleared` call passing `"session"` or `"artifact"`, apart from the `emitLegalHold` call inside `writeLegalHoldCleared` that forwards its `resourceType` parameter. The failure message names the `audit` tenant entry as the eraser to revisit.

### Tests that land with CODE-6

- **T1.12 (tier 1).** Extend `TestIsRegisteredImplementsAuthRegistry` with a table over `active`, `disabling`, `deleting`, `deleted`, and suspended rows for `Memory` and `Registered()`, and add a test in `cmd/lenny-gateway/auth_seams_test.go` that runs the same table over both registry adapters, which are in package `main`: only `deleting` and `deleted` are refused. In the adapter test, a `default` row in `deleting` is refused, a missing `default` row is admitted, and a store error makes both adapters return an error, for `default` and for another tenant; the `default` store-error case is the one that discriminates item 3 from today's shortcut.
- **T1.13 (tier 1).** A `TENANT_NOT_FOUND` rejection emitted through `admin.NewAuditLogSink` is appended under the platform tenant with `target_tenant_id` as a top-level payload key; that top-level read is the assertion that discriminates item 4, and `TENANT_CLAIM_INVALID_FORMAT` keeps its placement. Retarget any test that asserts a `TENANT_NOT_FOUND` row on the inferred tenant's chain.
- **T2.8 (tier 2).** The pgstore `IsRegistered` agrees with `Registered()` for every state.
- **T1.18 (tier 1).** With the single-tenant setting, `DELETE` and force-delete of `default` return `409 INVALID_STATE_TRANSITION` and leave the row `active`. With the multi-tenant setting, both start the lifecycle; that case is the one that discriminates. A row in `cmd/lenny-gateway/release_wiring_test.go` pins the `WithMultiTenant(*multiTenant)` call in `buildAdminRouter`.
- **T1.19 (tier 1).** Under a non-default `--tenant-id-claim`, through the `token_signing.go` verifier composition and `authmw.Wrap`, the following hold. A Token Service `user_bearer` resolves from `tenant_id` and is not refused with `TENANT_CLAIM_MISSING`. An embedded-key token whose `tenant_id` differs from the configured claim resolves from the configured claim. An embedded-key token that carries `tenant_id` but lacks the configured claim is refused with `TENANT_CLAIM_MISSING`, and that is the assertion that discriminates item 6's fail-closed rule. Retarget `TestBearerHonoursConfigurableTenantClaim` to the wrapper.

### Tests that land with CODE-7

- **T1.14 (tier 1).** `leaserevoke_test.go` becomes a table over pool and user leases: every listed lease is released, and `Revoker` has no deny-list dependency. Rename the tests named "DenyLists...EveryListedLease".
- Retarget `tests/tier9_security/full_revoke_lease_return_test.go` to "returns every listed lease and deny-lists none". Its pool and user lease tokens are rejected with `LEASE_TOKEN_INVALID`, and `bob`'s proxy and direct leases on the pool credential are neither rejected nor rotated, with the rotation hook and `RevokeCredential` never called; that last assertion is the one that discriminates CODE-7.
- Retarget `tests/tier8_chaos/full_revoke_denylist_drop_test.go`: with no deny-list entry on any replica, a peer replica rejects the returned lease token of each source.
- Retarget `tests/tier4_integration/full_revoke_two_replica_test.go` and `cmd/lenny-gateway/user_revocation_rotation_test.go`: the deny list stays empty, and every Release precedes every cancel write.

### Tests that land with CODE-8

- **T1.15 (tier 1).** `pausePhase` in `pkg/controller/tenantdeletion/controller_test.go`: a Phase 3 or Phase 4 action error keeps the job in phase with `BlockedReason` set and neither `Phase` nor `State` changed, and the next pass that succeeds advances and clears it. A nil `Revoker` or nil `Eraser` fails the job.
- **T1.16 (tier 1).** `tenantCredentialRevoker` commits `deleting` before any other call, lists with `ListFilter{Primary: true}`, calls the lease step over every listed session, terminal ones included, then both `TokenStore` deletes, and returns a listing or delete error. A second run is idempotent.

### Tests that land with CODE-9

- **T1.17 (tiers 1 and 3).** A full_revoke matrix over session states (each non-terminal state and each terminal state), a per-session cancel error, a commit-then-error write whose re-read finds `cancelled` and one whose re-read fails, a client disconnect mid-call, a session created just before the call and visible only on the primary, an elicitation dismissal error, and a listing error. Each case asserts the counts, `failedSessions`, `elicitationDismissalError`, the status code, the audit event, and that the tokens are revoked. The re-read-finds-`cancelled` case also asserts one `AfterRevokeWrite` call carrying the re-read row, and the re-read-fails case asserts none; that is the assertion that discriminates CODE-9 item 3. The tier-3 half pins the response body fields.
- Extend `tests/tier4_integration/full_revoke_two_replica_test.go` with a case that cancels the request context after `users.Update` commits; the test's existing end-state assertions hold. The Postgres session store fails a write on a cancelled context and the tier-1 memory stores do not, so this case is the one that discriminates CODE-9 item 1.
- Update `users_invalidate_fanout_test.go` and `users_revoke_hook_test.go` for the primary listing, terminal-session lease return, and `failedSessions`.

### TEST-1 · Cross-cutting lifecycle scenarios

These scenarios exercise the deliverables together and land after the code steps. TEST-1 closes the MinIO half of T-4.5.4 (with T2.4) and extends T-12.8.9 with Phases 3 and 4 against real backends. T-12.1.3 and T-12.8.4 are already covered by `tests/tier4_integration/erasure_job_test.go` and `tests/tier9_security/erasure_isolation_test.go`, and REC-1 item 10 records all four entries.

- **Harness.** Extend `tests/testinfra/lifecyclefence`. `Deletion` gains `ThroughPhase4` and a takeover that rebuilds the job at Phase 3, as `ensureJob` does for a `deleting` tenant. Its Phase 3 revoker and Phase 4 eraser list are harness seams that follow `tenantCredentialRevoker` and the **Store table**, as its Phase 1 and Phase 2 seams follow cmd/lenny-gateway's, because package `main` cannot be imported; X1 and the cmd/lenny-gateway tier-1 tests pin the production builders. `World.Check` gains the residue invariant: no `acme` or `alice` row, key, or object remains outside the `gdpr.%` rows, the receipts, the tombstone, the escrow, and the audit rows on the platform chain, and `bob` and `globex` are unchanged.
- **X1 (tier 4).** Extend `tenant_deletion_phase4_test.go` with the residue check after Phase 6. A Lenny-issued bearer and a playground bearer, and an OIDC bearer where the tier-4 gateway harness issues one, are refused from Phase 3 and through a Phase 3.5 pause on a peer replica, and a refused bearer after the audit eraser leaves no non-`gdpr.%` row for `acme`.
- **X2 (tier 7a).** Add Phase 3, Phase 4, and full_revoke continuation operations to the lifecyclefence randomized interleavings under `-race`, against concurrent terminal writes and lease renewals.
- **X3 (tier 8).** Extend the `TestDeletionPhase2FaultsAndTakeover` pattern in `lifecycle_drain_fault_test.go` to faults at each Phase 3 step and each Phase 4 entry, a crash between entries, and a takeover after a Phase 3 step and after a Phase 4 entry. Each case asserts the job pauses in phase and the next pass completes with the residue invariant holding.
- **X4 (tier 9).** Cross-tenant isolation for every new tenant entry, `forEachTenantSession`, the sticky `ScanDelete`, the stream purge, and the registry refusal: an `acme` deletion never touches a `globex` row, key, object, or bearer.
- **X5 (tier 11).** DOCS-1 consistency checks.

## Edge cases and accepted failure modes

1. **A lease-store error is silent.** `LeasesBySession` and the lease store's `Remove` return no error, so a Postgres error during the lease step of CODE-8 item 1 or CODE-9 leaves a lease record in place, and its token stays valid until the lease expires, the stale-slot reaper reclaims it, or a repeated call returns it. REC-1 files the signature change.
2. **The access-token cache flush is unwired.** The cache has no production writer, so §12.8 Phase 3 and user-path step 3 flush nothing. The tier-0 guard of CODE-1 item 6 fails when a writer appears.
3. **F-12.8.35.** Without Postgres, the preStop barrier's private in-memory checkpoint metadata store is erased by no eraser. Its contents are lost at process restart.
4. **A paused Phase 4 reruns every entry.** The rerun happens every `--tenant-deletion-interval` with no backoff, as Phase 2 does. Counts from a failed attempt are not merged, so the receipt can show 0 for a store an earlier attempt emptied.
5. **A billing stream after the tombstone.** Staged events flush while the tenant's flusher runs, and the flusher stops at the tombstone, so events still staged then stay in the stream. After a purge, a replica whose flusher starts before the tombstone recreates an empty stream and its group, and a publish on any replica recreates a stream with no group, which the Phase 2 drain makes improbable.
6. **User erasure deletes revoked registered credentials.** The §4.9 startup deny-list rebuild reads revoked `credentials` rows, so a lease still live on an unreleased remote binding (F-12.8.43) loses its user-shaped deny entry after a replica restart. The bound is the lease TTL, or `claimOrphanTimeout` plus one GC sweep.

## Files touched on application (non-spec)

- `charts/lenny/values.yaml` (comment)
- `cmd/lenny-gateway/adminrouter.go`
- `cmd/lenny-gateway/auth_seams.go`
- `cmd/lenny-gateway/billing_pipeline.go`
- `cmd/lenny-gateway/credential_revocation.go`
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
- `pkg/gateway/externalapi/admin/audit_sink.go`, `forcedelete.go`, `tenants.go`, and `users.go`
- `pkg/gateway/middleware/auth/auth.go`
- `pkg/gateway/storage/erasure/erasure.go` and `order.go`
- `pkg/gateway/storage/issuedtokenstore/issuedtokenstore.go`
- `pkg/gateway/storage/leasestore/leasestore.go` and `pgstore/pgstore.go` (comments)
- `pkg/legalholdescrow/migrator.go`, and `escrow.go` and `records.go` (comments)
- `docs/operator-guide/multi-tenancy.md` and `docs/operator-guide/lenny-ctl.md`
- `BUILD-GAPS.md`, `TEST-GAPS.md`, `scripts/seed-claim-register.py`, `tests/claim-map.json`, and `gateway-runtime-comms-remediation.md`
- The tests named under **Testing**
