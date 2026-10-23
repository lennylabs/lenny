# Deviations: Data lifecycle second half: remaining erasure stores, Phase 3 lease revocation, audit-receipt foreign key, and full_revoke

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## MIG-1 edits an existing tier-2 auditstore test outside its targets

**Status:** accepted (adjudicated 2026-10-23 under the owner's standing delegation)

**Reported by:** step S7 (MIG-1: migration 0188 drops the `audit_redaction_receipts` foreign key).

**What the proposal says.** The MIG-1 targets list, in the non-spec changes under MIG-1 and in the step's Targets, names only `migrations/0188_*.sql`, `migrations/audit_redaction_receipts_test.go`, `cmd/lenny-gateway/leader_sweeps.go`, and `pkg/gateway/audit/auditstore/auditstore.go`. The T2.1 tests named for MIG-1 do not mention any existing tier-2 auditstore test.

**What landed instead.** The step also edited `tests/tier2_component/auditstore/redaction_test.go`. It replaced `TestDeleteByTenantFKBlocksReceiptReferencedRow_spec_12_8_line840` with `TestDeleteByTenantKeepsReceiptOfDeletedRow_spec_12_8` and updated one comment that referenced the replaced test. T2.1 itself lives at `tests/tier2_component/migrations/audit_redaction_receipts_drop_fk_test.go`, which is the directory that rule 3 of lint-migrations scans.

**Why.** The existing test asserted the 23503 foreign-key abort that migration 0188 removes. The test failed once 0188 landed, and its own failure message instructed that it be changed to assert receipt retention when F-12.8.25 was fixed.

**What a later reader would otherwise get wrong.** A reader comparing the MIG-1 targets list against the diff would find an unlisted change to `tests/tier2_component/auditstore/redaction_test.go` and could treat it as scope creep or revert it. A reader searching for `TestDeleteByTenantFKBlocksReceiptReferencedRow_spec_12_8_line840` would find that it no longer exists, and that its assertion is now inverted under the name `TestDeleteByTenantKeepsReceiptOfDeletedRow_spec_12_8`. A reader looking for T2.1 under `tests/tier2_component/auditstore/` would not find it there, because it lives under `tests/tier2_component/migrations/`.

## CODE-5 escrow blob adapters and store builder live in a new pkg/ package

**Status:** accepted (adjudicated 2026-10-23 under the owner's standing delegation)

**Reported by:** step S9 (CODE-5: Phase 3.5 escrow into the escrow bucket).

**What the proposal says.** CODE-5 items 1 and 2, and the step's Targets, place `blobEscrowWriter`, `blobEscrowDeleter`, and the source reader in `cmd/lenny-gateway/legalholdescrow.go`, and build the escrow store there with `blobproviderflags.Resolve`.

**What landed instead.** The blob adapters and the store builder are in a new package, `pkg/legalholdescrow/blobescrow`, which exports `ObjectURI`, `StoreOptions`, `NewStore`, `Source`, `Writer`, and `Deleter`. `cmd/lenny-gateway/legalholdescrow.go` uses `blobescrow.Source` and `blobescrow.Writer` in `tenantEscrowMigrator.EscrowHolds`. `cmd/lenny-gateway/adminrouter.go` builds the store with `blobescrow.NewStore(context.Background(), objectStorageOptions(f), ...)` and passes `blobescrow.Deleter{Blobs: escrowBlobs}` to the Releaser. The rows of `release_wiring_test.go` pin these spellings.

**Why.** T2.7 is a tier-2 test. Tier-2 tests live under `tests/tier2_component`, and that directory cannot import package `main`. With the adapters under `pkg/`, the MinIO test runs the shipped writer, deleter, and store builder rather than a copy of them. The project rule that reusable logic belongs under `pkg/` leads to the same placement. The step reports that the behavior matches the proposal.

**What a later reader would otherwise get wrong.** A reader looking for `blobEscrowWriter`, `blobEscrowDeleter`, or a `blobproviderflags.Resolve` call in `cmd/lenny-gateway/legalholdescrow.go` would not find them. The same reader could treat `pkg/legalholdescrow/blobescrow` as an unlisted package and move its contents back into `cmd/`, which would leave T2.7 unable to exercise the shipped code.

## escrow object URIs carry a no-expiry TTL

**Status:** accepted (adjudicated 2026-10-23 under the owner's standing delegation)

**Reported by:** step S9 (CODE-5: Phase 3.5 escrow into the escrow bucket).

**What the proposal says.** CODE-5 item 2 states that the escrow URI keeps `TenantID` as the original tenant. It says nothing about the TTL, and the existing writer left the TTL at zero.

**What landed instead.** `blobescrow.ObjectURI` in `pkg/legalholdescrow/blobescrow/blobescrow.go` also sets the TTL to `RetainUntilRelease`, a value of 100 years.

**Why.** The memory and MinIO stores apply a URI's TTL on read. With a TTL of zero, every escrow object read as expired (`ErrNotFound`) as soon as it was written, so T2.7's assertion that the escrow object is readable after `DeleteByTenant` could not hold on a store with a real clock. The step reports that the change is confined to one package and changes no spec, flag, or schema, which the triage rule permits within the step. `RetainUntilRelease` is a sentinel that means the object has no expiry. It is not an operator default.

**What a later reader would otherwise get wrong.** A reader would read the 100-year value as a hard-coded non-spec default that the configuration rules require to be operator-tunable, and could add a flag for it or remove it. Removing it returns the defect in which escrow copies read as expired immediately after they are written.

## escrow store location for the filesystem provider and scope of the endpoint override

**Status:** accepted (adjudicated 2026-10-23 under the owner's standing delegation)

**Reported by:** step S9 (CODE-5: Phase 3.5 escrow into the escrow bucket).

**What the proposal says.** CODE-5 item 1 states that the escrow store uses the artifact store's provider and endpoint, with `Bucket` and `MinIOBucket` set to the escrow bucket. It does not cover the filesystem provider, which ignores `Bucket`. It does not state which endpoint field `--legal-hold-escrow-endpoint` overrides. The proposal does not mark either point as a gap.

**What landed instead.** `blobescrow.StoreOptions` sets `FilesystemRoot` to `<artifact root>.legal-hold-escrow/<bucket>`, a directory beside the artifact root and outside it. `--legal-hold-escrow-endpoint` overrides `MinIOEndpoint` only.

**Why.** If the filesystem provider used the artifact root, the escrow copies would sit under the tenant directory that the artifact store's prefix delete removes, which is the loss CODE-5 exists to prevent. `MinIOEndpoint` is the only endpoint field in `providerflags.Options`. S3 has no endpoint option, and Azure's account URL names an account rather than a bucket endpoint.

**What a later reader would otherwise get wrong.** A reader would expect the filesystem escrow store to share the artifact root, or would look for escrow copies inside it. A reader would also expect `--legal-hold-escrow-endpoint` to change the S3 or Azure endpoint, which it does not.

## escrow store construction failure is fatal at startup

**Status:** accepted (adjudicated 2026-10-23 under the owner's standing delegation)

**Reported by:** step S9 (CODE-5: Phase 3.5 escrow into the escrow bucket).

**What the proposal says.** CODE-5 item 1 states that when no escrow bucket is configured, the gateway builds no store and the migrator fails closed with `ErrRegionUnresolvable`. It does not state what happens when the store build itself fails.

**What landed instead.** In `cmd/lenny-gateway/adminrouter.go`, a `NewStore` error stops the gateway at startup through `log.Fatalf`, which matches the handling of an artifact store construction error. In addition, `tenantEscrowMigrator.EscrowHolds` treats a nil escrow store as an empty escrow configuration, so a migrator built without a store fails closed with an unresolvable region even when its configuration names a bucket.

**Why.** The step reports that both choices fail closed on a security-relevant path and match how the artifact store's own construction errors are handled.

**What a later reader would otherwise get wrong.** A reader would expect a gateway whose escrow store fails to build to start and fail each migration individually, when it instead refuses to start. A reader would also expect a configured bucket name alone to make the migrator resolve a region, when a nil store causes `ErrRegionUnresolvable` regardless of the configured bucket.

## platform-chain placement covers the escrow-released ledger event

**Status:** accepted (adjudicated 2026-10-23 under the owner's standing delegation)

**Reported by:** step S9 (CODE-5: Phase 3.5 escrow into the escrow bucket).

**What the proposal says.** CODE-5 item 3 states that `escrowLedger` appends each ledger event under `jwtaudit.PlatformTenantID` with `target_tenant_id`.

**What landed instead.** The placement applies to all of the events `escrowLedger` writes, including `legal_hold.escrow_released`, which the Releaser emits through the same ledger. The payload key is `target_tenant_id`, which is the key the auditstore residency gate reads. The existing camelCase `tenantId` field remains in the payload.

**Why.** The Releaser also writes `legal_hold.escrow_released` after the tenant tombstone, so the event needs the same placement on the platform chain as the other escrow events.

**What a later reader would otherwise get wrong.** A reader would expect `legal_hold.escrow_released` to be appended under the original tenant, and would look for it on that tenant's chain rather than on the platform chain. A reader seeing both `target_tenant_id` and `tenantId` in the payload could remove one of them as a duplicate, when the residency gate reads `target_tenant_id` and the existing field is retained.

## CODE-6 shared registration table lives in a new test-helper package

**Status:** accepted (adjudicated 2026-10-23 under the owner's standing delegation)

**Reported by:** step S11 (CODE-6: tenant registration predicate, auth-failure chain, claim selection, default deletion guard, seed refusal, platform reservation, and citation retarget).

**What the proposal says.** T1.12, under "Tests that land with CODE-6" in the non-spec Testing section, describes extending `TestIsRegisteredImplementsAuthRegistry` with a table and running the same table over the adapters. T2.8 runs the same table over the pgstore. The proposal does not say where the shared table lives.

**What landed instead.** The step added a new test-fixture package, `pkg/gateway/environment/tenantstore/tenantstoretest`, whose file `registration.go` exports `RegistrationCases` and `Seed`, written against the `tenantstore.Store` interface. The Memory test (`tenantstore_test.go`), the adapter test (`cmd/lenny-gateway/auth_seams_test.go`), and the Postgres test (`tests/tier2_component/stores/tenantstore_test.go`) all run this one table.

**Why.** Three packages need the same table, and the step did not duplicate it. The step ruled out placing a fixture in production code, so the table lives in a `*test` helper package that follows the existing `memorystoretest` convention. The package is absent from the proposal's lists.

**What a later reader would otherwise get wrong.** A reader comparing the CODE-6 targets against the diff would find `pkg/gateway/environment/tenantstore/tenantstoretest` as an unlisted package and could treat it as scope creep. Folding the table back into one test file would leave the other two packages without access to it, and the three tiers would no longer run one table.

## CODE-6 embedded bearer verifier composition is built by a new helper

**Status:** accepted (adjudicated 2026-10-23 under the owner's standing delegation)

**Reported by:** step S11 (CODE-6: tenant registration predicate, auth-failure chain, claim selection, default deletion guard, seed refusal, platform reservation, and citation retarget).

**What the proposal says.** CODE-6 item 6 states that `embeddedHMACVerifier` wraps the embedded OIDC member with the tenant-claim wrapper under `--tenant-id-claim`.

**What landed instead.** `embeddedHMACVerifier(trusted, tenantClaim)` in `cmd/lenny-gateway/token_signing.go` wraps the `ClaimChecker` in `jwt.NewTenantClaimVerifier`. A new helper, `embeddedBearerVerifier(primary, trusted, tenantClaim)`, builds the `MultiVerifier` that `buildTokenSigningStores` uses.

**Why.** The helper lets T1.19 run the gateway's shipped verifier composition through `authmw.Wrap` instead of a copy of that composition. The step reports that the behavior matches the proposal.

**What a later reader would otherwise get wrong.** A reader looking for the `MultiVerifier` construction inline in `buildTokenSigningStores` would find a call to `embeddedBearerVerifier` instead, and could treat the helper as unlisted and inline it again. Inlining it would leave T1.19 testing a copy of the composition rather than the code the gateway runs.

## CODE-7 deny-list interface moves under an unexported name

**Status:** accepted (adjudicated 2026-10-23 under the owner's standing delegation)

**Reported by:** step S12 (CODE-7: lease step without deny-listing).

**What the proposal says.** CODE-7 item 1 in the non-spec changes moves the `DenyList` interface to `cmd/lenny-gateway/credential_revocation.go` beside `poolCredentialRevoker`. The propagator assertion in `user_revocation.go` and the marker test move with it.

**What landed instead.** The interface moved under the unexported name `credentialDenyList`, near the top of `cmd/lenny-gateway/credential_revocation.go`. The propagator assertion moved into the same file. The marker test is now `TestPoolCredentialRevokerRequiresTheCrossReplicaPropagator` in `cmd/lenny-gateway/credential_revocation_test.go`.

**Why.** Package `main` exports nothing, and the `leaserevoke` package already uses the name `DenyList`. The step chose a descriptive unexported name, which follows Go convention for an interface defined at its consumer. The step reports that the behavior is unchanged.

**What a later reader would otherwise get wrong.** A reader searching `cmd/lenny-gateway` for `DenyList` would not find the interface the proposal names and could conclude that the move did not land. A reader looking for the marker test under its old name would not find it either.

## CODE-7 tier-4 full_revoke test drops its deny list instead of asserting it stays empty

**Status:** accepted (adjudicated 2026-10-23 under the owner's standing delegation)

**Reported by:** step S12 (CODE-7: lease step without deny-listing).

**What the proposal says.** Under "Tests that land with CODE-7" in the non-spec Testing section, the proposal retargets `tests/tier4_integration/full_revoke_two_replica_test.go` so that the deny list stays empty and every Release precedes every cancel write.

**What landed instead.** The tier-4 test (`observedReleaser` and `requireLeaseStepAccounting` in `tests/tier4_integration/full_revoke_two_replica_test.go`) drops its deny list entirely. It keeps the check that every Release precedes every cancel write, together with the release-once and `leasesRevoked` accounting. It makes no assertion that a deny list stays empty.

**Why.** After the change, the production lease step has no deny-list dependency, and the lifecycle harness wires no deny list. An empty-deny-list assertion in this test would pass regardless of what the code does. The step reports that the empty deny list is checked where a deny list is wired: tier 1 (`Revoker` has no deny-list dependency), the cmd rotation test, tier 8 (the binding replica's deny list), and tier 9 (the proxy's propagator deny list and its revoke hook).

**What a later reader would otherwise get wrong.** A reader comparing the tier-4 test against the proposal would find no empty-deny-list assertion and could treat it as a missing deliverable. Adding a deny list back to the tier-4 harness to assert it stays empty would produce an assertion that cannot fail.

## the tier-4 playground-bearer refusal case is skipped

**Status:** accepted (adjudicated 2026-10-23 under the owner's standing delegation)

**Reported by:** step S16 (TEST-1: lifecyclefence harness extension and cross-cutting scenarios). Step S17 records it under the triage rule of the non-spec Testing section (**Failures the tests find.**).

**What the proposal says.** The X1 intent under TEST-1 states that a Lenny-issued bearer and a playground bearer, and an OIDC bearer where the tier-4 gateway harness issues one, are refused from Phase 3 and through a Phase 3.5 pause on a peer replica. X1 mints its playground bearer before Phase 3, because the cookie mint runs no registry check.

**What landed instead.** The playground-bearer case is the subtest `playground bearer` of `TestTenantDeletionPhase4ClearsArchivedTreesAndCheckpoints_spec_12_8` in `tests/tier4_integration/tenant_deletion_phase4_test.go`, and it is skipped with `t.Skip("blocked: F-27.8.1 — a gateway started with --playground-enabled exits at startup, so no playground bearer can be minted")`. The defect is filed as F-27.8.1 in `BUILD-GAPS.md`, which cross-references this entry. The embedded OIDC bearer and the Lenny-issued bearers of the same test are probed through the Phase 3.5 pause and after the tombstone.

**Why.** A gateway started with `--playground-enabled` exits before it listens, because the playground registers `lenny_playground_page_views_total` with the label `authMode`, which the metric label validator refuses as not snake_case. The fix renames a label that §27.8 names, which changes specification text, so the triage rule files it rather than fixing it in this proposal.

**What a later reader would otherwise get wrong.** A reader would take the playground-bearer refusal as pinned at tier 4 when no tier-4 case exercises it. When F-27.8.1 is fixed, the skip is removed and the case mints its playground bearer before Phase 3, as X1 states.

## the tier-4 peer-replica refusal probe checks each Lenny-issued bearer only on its minting replica

**Status:** accepted (adjudicated 2026-10-23 under the owner's standing delegation)

**Reported by:** step S16 (TEST-1: lifecyclefence harness extension and cross-cutting scenarios). Step S17 records it under the triage rule of the non-spec Testing section (**Failures the tests find.**).

**What the proposal says.** The X1 intent under TEST-1 states that a Lenny-issued bearer is refused from Phase 3 and through a Phase 3.5 pause on a peer replica.

**What landed instead.** `TestTenantDeletionPhase4ClearsArchivedTreesAndCheckpoints_spec_12_8` in `tests/tier4_integration/tenant_deletion_phase4_test.go` mints one Lenny-issued bearer on each of its two replicas through `POST /v1/admin/impersonation`, and `lennyIssuedProbes` probes each bearer only on the replica that minted it. The embedded OIDC bearer is probed on both replicas. The case has no `t.Skip`: the peer-replica probe of a Lenny-issued bearer is left out of the probe set rather than skipped. The defect is filed as F-10.2.22 in `BUILD-GAPS.md`, which cross-references this entry.

**Why.** Each gateway replica generates and seals its own Token Service signing key at process start, so a Lenny-issued bearer verifies only on the replica that signed it, and a peer replica rejects it as unverifiable before the tenant registry check runs. Sharing the key across replicas changes the Token Service key loading, which is outside the members of this proposal and is not a one-package fix, so the triage rule files it.

**What a later reader would otherwise get wrong.** A reader would take the X1 intent as holding for Lenny-issued bearers on a peer replica. Each Lenny-issued bearer is refused on the replica that minted it, and `TestBearerOfDeletingTenantRefusedOnEveryVerifierMember_spec_12_8` in `cmd/lenny-gateway/auth_seams_test.go` pins the refusal through the Token Service member at tier 1. When F-10.2.22 is fixed, `lennyIssuedProbes` probes each bearer on every replica.

## Accepted departures recorded from the non-spec Edge cases

The six entries below are the non-spec **Edge cases and accepted failure modes**. Step S17 records each in `BUILD-GAPS.md` on the finding named with it. None of them carries a claim-register row, because a §28.4 row needs a §28 anchor and a closing remediation step and none of these has one.

### Departure: a lease-store error is silent

`LeasesBySession` and the lease store's `Remove` return no error, so a Postgres error during the lease step of the tenant-deletion Phase 3 revoker or of full_revoke leaves a lease record in place. **Bound:** the lease token stays valid until the lease expires, the stale-slot reaper reclaims it, or a repeated full_revoke call or a later Phase 3 pass returns it. Recorded on F-4.9.40, which files the signature change, and pointed to from F-12.8.30.

### Departure: the access-token cache flush is unwired

The token service constructs the access-token cache and never writes to it, so §12.8 Phase 3 and the user-erasure access-token step flush nothing. **Bound:** the cache holds no entry while it has no writer, and `TestErasureExemptionsHaveNoProductionWriter_spec_12_8` in `tests/tier0_static/erasure_exemption_guard_test.go` fails when a production writer appears. Recorded on F-12.8.30.

### Departure: checkpoint metadata of a gateway without Postgres (F-12.8.35)

Without Postgres, the preStop barrier's private in-memory checkpoint metadata store is erased by no eraser. **Bound:** its contents are lost when the gateway process restarts. Recorded on F-12.8.35, which stays open.

### Departure: a paused Phase 4 reruns every entry

A Phase 4 entry error pauses the job in phase, and the next pass reruns every entry every `--tenant-deletion-interval` with no backoff, as Phase 2 does. Counts from a failed attempt are not merged, so the receipt can report 0 for a store an earlier attempt emptied. **Bound:** each rerun repeats only idempotent deletes, and the receipt undercounts only the stores an earlier failed attempt emptied. Recorded on F-12.8.34.

### Departure: a billing stream after the tombstone

Staged events flush while the tenant's flusher runs, and the flusher stops at the tombstone because `FlusherManager` visits only listed tenants, so events still staged then stay in the stream. After a purge, a replica whose flusher started before the tombstone recreates an empty stream and its group, and a publish on any replica recreates a stream with no group. **Bound:** the Phase 2 drain ends the tenant's sessions before Phase 4, so a publish after the purge is improbable, and the residue is the events staged when the flusher stopped plus an empty stream key. Recorded on F-12.8.34.

### Departure: user erasure deletes revoked registered credentials

The §4.9 startup deny-list rebuild reads revoked `credentials` rows, so a lease still live on an unreleased remote binding (F-12.8.43) loses its user-shaped deny entry after a replica restart. **Bound:** the lease TTL, or `claimOrphanTimeout` plus one GC sweep interval. Recorded on F-12.8.43.

## REC-1 records gate edits the tier-0 slot-address case-file list

**Status:** accepted (adjudicated 2026-10-23 under the owner's standing delegation)

**Reported by:** step S17 (REC-1: records, departures, new findings, claim register, records gate, P5b bullet).

**What the proposal says.** REC-1 item 9 and the step's Targets name `tests/tier11_docs/build_gaps_data_lifecycle_records_currency_test.go` as the only test file. The step registers the gate in `documentConsistencyGates` only if the update adds a new file.

**What landed instead.** The gate stays in the existing file, which `documentConsistencyGates` already registers. The step also edited `tests/tier0_static/spec_map_slot_address_registration_test.go` to add that file to `slotAddressCaseFiles`.

**Why.** The new `TEST-GAPS.md` checks make the gate file read `TEST-GAPS.md`. The tier-0 gate `TestTheCreditInventoryCarriesEveryAddressGuardCaseFile` requires every tier-11 file that reads `TEST-GAPS.md` to appear in `slotAddressCaseFiles`, and that gate failed until the entry was added.

**What a later reader would otherwise get wrong.** A reader comparing the REC-1 targets against the diff would find an unlisted change to `tests/tier0_static/spec_map_slot_address_registration_test.go` and could treat it as scope creep or revert it, which would fail the tier-0 gate. A reader could also conclude from the targets that the records gate reads only `BUILD-GAPS.md`.

## placement of the non-spec Edge case departures in BUILD-GAPS.md

**Status:** accepted (adjudicated 2026-10-23 under the owner's standing delegation)

**Reported by:** step S17 (REC-1: records, departures, new findings, claim register, records gate, P5b bullet).

**What the proposal says.** REC-1 item 6 records each departure (non-spec Edge cases 1 to 6) in `BUILD-GAPS.md` with its bound. It does not name the finding that carries each departure.

**What landed instead.** Each departure is recorded on one finding as an `**Accepted departure (proposal 0095): ...**` paragraph with a `Bound:` clause. Edge case 1 is on the new F-4.9.40, with a pointer from F-12.8.30. Edge case 2 is on F-12.8.30 (closed). Edge case 3 is on F-12.8.35 (left open). Edge cases 4 and 5 are on F-12.8.34 (closed). Edge case 6 is on F-12.8.43. This file also carries a section titled "Accepted departures recorded from the non-spec Edge cases" with one bounded entry for each.

**Why.** The proposal left the placement open. The step placed each departure on the finding whose mechanism it bounds, so a reader of that finding sees the residue.

**What a later reader would otherwise get wrong.** A reader searching `BUILD-GAPS.md` for a single departures list would not find one, and would need the mapping above to locate each departure. A reader seeing the word "Accepted" in those paragraphs and in this file's section heading could take it as an adjudication of a deviation; it labels the departures the proposal's Edge cases already state, and the placement itself is the proposed deviation recorded here.

## REC-1 New findings filed by section, including entries outside the step's list

**Status:** accepted (adjudicated 2026-10-23 under the owner's standing delegation)

**Reported by:** step S17 (REC-1: records, departures, new findings, claim register, records gate, P5b bullet).

**What the proposal says.** REC-1 item 8 files each summary entry marked **New** under the next free identifier in its section. It does not assign the entries to `BUILD-GAPS.md` sections, and the step's own list in item (8) names a subset of the New entries.

**What landed instead.** The findings are filed by section: §4.9 gets F-4.9.40 and F-4.9.41, §10.2 gets F-10.2.23 and F-10.2.24 (after the existing F-10.2.22), §12.8 gets F-12.8.49 to F-12.8.59, §17.6 gets F-17.6.24, and §27.3 gets F-27.3.12 and F-27.3.13. F-10.2.22 and F-27.8.1 keep their numbers. All 18 New entries are filed, including the nine the step's list left out: ForceTerminate (owner P2), `delegation_tree_budget` (owner P4), the unnamed tenant-keyed tables, the playground mint check, the single-tenant playground revocation, the tenant-keyed writers, admin-token rotation, the single-tenant create and delete, and the dev-mode header.

**Why.** Each finding is filed in the section whose mechanism it concerns. REC-1 item 8 files every entry marked **New**, and the step's list is a subset of those entries.

**What a later reader would otherwise get wrong.** A reader checking the step's item (8) list against `BUILD-GAPS.md` would find nine additional findings and could treat them as unrequested or remove them. A reader could also expect every New finding under a single section, or expect F-10.2.22 or F-27.8.1 to have been renumbered.

## the legal-hold scope tripwire test still states the pre-0095 audit eraser contract

**Status:** accepted (adjudicated 2026-10-23 under the owner's standing delegation)

**Reported by:** the step sweep for stale code that the landed spec edits left behind.

**What the proposal says.** The proposal stages no change to `pkg/gateway/externalapi/admin/legalhold_scope_tripwire_test.go`.

**What landed instead.** The file is unchanged. Its comment and its `legalHoldScopeRevisit` constant, near line 27, still read as follows:

```go
// The tenant `audit` eraser in Phase 4 deletes the tenant's audit rows
// with no escrow skip, which is correct only while no audit-range hold can
// exist, so it has to be revisited before such a scope ships.
const legalHoldScopeRevisit = "the legal-hold setter accepts a scope beyond session and artifact; revisit the tenant deletion `audit` eraser, which deletes audit rows with no legal-hold escrow skip"
```

**Why.** The text contradicts the landed spec. In `spec/12_storage-architecture.md`, §12.8 Compliance Interfaces, the **Tenant deletion lifecycle** paragraph for Phase 4 states that "`DeleteByTenant` deletes the originals of resources that Phase 3.5 copied to the legal-hold escrow bucket. The escrow copy and its legal-hold ledger record are the retained evidence". Sub-step 3 of Phase 3.5 adds that "Phase 4 deletes the original rows". An eraser with no escrow skip is therefore the specified behavior, and its correctness no longer depends on the absence of audit-range holds. The constant is a string that the test passes to `t.Fatalf` as its failure message, so editing it changes test behavior that the proposal did not stage, and the sweep left it in place.

**What a later reader would otherwise get wrong.** A reader of `pkg/gateway/externalapi/admin/legalhold_scope_tripwire_test.go` would take the old contract as current. That reader would conclude that the tenant `audit` eraser must gain a legal-hold escrow skip before a hold scope beyond session and artifact ships, when the landed spec specifies that Phase 4 deletes the originals and that the Phase 3.5 escrow copy is the retained evidence.
