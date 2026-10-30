# Deviations — 0109_fix_a-derived-or-replayed-session-starts-without-its-source

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Proposed: Adapter restore failure table omits the extraction-cap case

**Status:** proposed

**Reported by:** step S9 (CODE-2: adapter seed from restore, pairing refusal, error mapping, and seed-hook signature ripple).

**What the proposal says:** The non-spec Testing section, adapter tiers 1 and 10, calls for a failure table with one case per restore failure that CODE-2 step 4 names. That list includes the `maxExtractBytes` extraction cap.

**What landed instead:** `TestFinalizeWorkspaceRestoreFailuresFailTheBindAndLeaveCurrent_spec_7_1` in `pkg/adapter/staging_restore_test.go` has a case for every named failure except the extraction cap. The table also has a "no checkpoint transport configured" case, which the proposal does not name. That case answers `UNAVAILABLE` through `errRestore` in `restoreSeed` (`pkg/adapter/staging.go`).

**Why:** `maxExtractBytes` is a 2 GiB package constant in `pkg/adapter/workspace/extract.go`, so a cap case would write 2 GiB to disk during a tier-1 run. The cap error leaves `ExtractTree` through the same `errRestore` wrap as every other `ExtractTree` error. The step reports that the existing cases therefore pin the `UNAVAILABLE` mapping.

**What a later reader would otherwise get wrong:** A reader who checks the failure table against the proposal's Testing list would expect a test that exercises the `maxExtractBytes` cap and would find none. No test exercises the cap path directly. A reader would also not expect the "no checkpoint transport configured" case, because the proposal does not list it.

## Proposed: Binder restore closure invoked through a shared helper

**Status:** proposed

**Reported by:** step S10 (CODE-3: Binder restore closure call sites and demotion clause).

**What the proposal says:** CODE-3 step 2 shows the restore closure invoked inline in `Prepare` (`if req.RestoreChunks != nil { restore, err = req.RestoreChunks(ctx) ... }`). CODE-3 step 3 says to invoke it the same way in `materializeSlotStages`.

**What landed instead:** Both sites call one helper, `mintRestoreChunks(ctx, req.RestoreChunks)`, in `pkg/gateway/podlifecycle/podsession/bindrequest.go`. The helper returns nil grants when the closure is nil and otherwise invokes the closure. Each site keeps the error handling the proposal specifies. `Prepare` calls `reclaim` and returns `podsession: resolve workspace restore on pod %s: %w`. The slot path calls `recordSlotFailure(slotFailureWorkspaceFinalize)` and `slotBindError(slotFailureWorkspacePrep)`.

**Why:** The step reports that the project rule favours reuse over repeating the same nil-check block at the two call sites, and that the behaviour matches the proposal's inline form.

**What a later reader would otherwise get wrong:** A reader who looks for the inline `if req.RestoreChunks != nil` block in `Prepare` or `materializeSlotStages` will not find it. The nil check and the closure call are in `mintRestoreChunks` in `bindrequest.go`.

## Proposed: REC-1 files no new BUILD-GAPS entries for the unstaged defects

**Status:** proposed

**Reported by:** step S15 (REC-1: BUILD-GAPS and remediation-plan records).

**What the proposal says:** REC-1 in the non-spec changes says: "File one new entry for each defect listed in the summary's Defects in the shipped tree that this proposal does not stage, excluding the two already-filed findings above."

**What landed instead:** The step filed no new entries. Records commit cf02a2af8 had already filed every listed defect on 2026-10-29 as F-7.1.36, F-15.1.63, F-10.1.45, F-7.3.36, and F-5.2.63. Each of those entries says that 0109's REC-1 updates it rather than filing a duplicate. Each open entry received a dated "Note (proposal 0109, 2026-10-30)" in `BUILD-GAPS.md` that re-confirms the defect against 990cffb30. F-10.1.45 was already closed by a direct fix (e93d3edf6), and the step left it unchanged.

**Why:** The step reports that a second entry for the same defect would leave two records of it, and that the existing entries tell this step not to file one.

**What a later reader would otherwise get wrong:** A reader who follows REC-1 would look for new `BUILD-GAPS.md` entries filed by 0109 and would find none. The defects are recorded under the five identifiers above, filed before this step ran, and 0109's contribution is the dated note on each open entry.

## Proposed: F-4.5.24 stays closed instead of open

**Status:** proposed

**Reported by:** step S15 (REC-1: BUILD-GAPS and remediation-plan records).

**What the proposal says:** REC-1 says: "Leave F-4.5.24 and F-4.5.21 open as independent fixes."

**What landed instead:** F-4.5.21 stays OPEN and has a new note. F-4.5.24 stays CLOSED, as it was in the tree. A direct fix closed it on 2026-10-29 (`fix/direct-batch-2`, merged in db727ad46) before this step ran, and its entry already carries a 0109 note.

**Why:** The step reports that reopening a finding whose fix is merged would make `BUILD-GAPS.md` state something untrue, and that the REC-1 intent of not discharging the finding through 0109 still holds.

**What a later reader would otherwise get wrong:** A reader who checks `BUILD-GAPS.md` against REC-1 would expect F-4.5.24 to be OPEN and would find it CLOSED. The closure came from the direct fix merged in db727ad46 rather than from 0109.

## Proposed: blobstore.DerivedSnapshotTTL removed

**Status:** proposed

**Reported by:** design-conformance fix pass (CODE-5 step 4).

**What the proposal says:** CODE-5 step 4 says: "Keep the copy loop, Put → ConfirmChunk → Finalise, and DerivedSnapshotTTL unchanged."

**What landed instead:** Commit b050cedf5 deletes the exported constant `blobstore.DerivedSnapshotTTL` and its doc comment from `pkg/blobstore/blobstore.go`. `TestCopyKeepsTheSourceTTLWhenTheDestinationNamesNone_spec_7_1` in `pkg/blobstore/copy_ttl_test.go` uses the literal `7 * 24 * time.Hour` for its destination-TTL case. The fix pass kept the deletion.

**Why:** The constant's only users were `parseDeriveURIs` and the legacy single-object derive copy, which this proposal deletes. The chunked derive copy stamps no TTL of its own: each copied chunk keeps the source chunk's TTL, measured from the copy. Restoring the constant would leave an exported identifier that no code reads, and its original doc comment carried a specification line citation that the line-citation rule forbids. `TestSnapshotCopyChunkedCheckpoint_spec_10_1` in `pkg/gateway/sessionserver/snapshot_copy_test.go` already asserts that every derived chunk object expires at the copy time plus the source chunk's TTL, on derive and on both replay modes. That assertion pins the behavior that replaces the constant, so the fix pass added no new test.

**What a later reader would otherwise get wrong:** A reader who checks CODE-5 step 4 against the tree would look for `blobstore.DerivedSnapshotTTL` and find no such constant.

## Proposed: Proposal-internal change id in a landed commit message

**Status:** proposed

**Reported by:** design-conformance fix pass.

**What the proposal says:** The implementation names behavior and specification sections in commit messages and never a proposal-internal change id.

**What landed instead:** The body of commit 900e0d316 ("spec: object-store GET capabilities and egress cover a derived or replayed session's first-bind restore") begins "Applies proposal 0109 SPEC-5: ...". The same body lists the specification sections the edit touches (§13.2.3, §13.1, §13.2.2, §13.3, and §4.4.3).

**Why:** Committed history is not rewritten, so the fix pass left the commit as it is and added no commit-message gate.

**What a later reader would otherwise get wrong:** A reader of the first-parent history finds the label `SPEC-5`, which names an entry in the proposal's deliverable index rather than a part of the shipped system. The specification sections listed in the same body are the durable reference.

## Proposed: Tier-9 restore-leak case runs on a concurrent-workspace pool only

**Status:** proposed

**Reported by:** design-conformance fix pass (presigned URL in restore fetch errors).

**What the proposal says:** CODE-1 says restore grants are bearer URLs that are never logged, and CODE-3 step 1 says the restore result is never logged or persisted. The conformance finding asked for a tier-9 case covering the gRPC status, the span, and the gateway's 503 body on the exclusive Prepare path.

**What landed instead:** Commit fccdd76d2 removes the URL from every request error in the production object-store transport (`withoutCapability` in `pkg/adapter/checkpointtransport.go`), and `chunkStream` in `pkg/adapter/resume.go` names a failed chunk by index. The tier-1 cases in `pkg/adapter/capability_redaction_test.go` assert that the transport errors, the FinalizeWorkspace status message, and the `session.finalize_workspace` span carry no URL or signature. `TestFailedRestoreFetchLeaksNoCapability_spec_7_1` in `tests/tier9_security/restore_grant_scope_test.go` runs on the harness's concurrent-workspace pool and checks every gateway answer, log line, and ended span. It fails at the pre-fix revision on the span check and passes after the fix.

**Why:** On the lifecycle harness, an exclusive pool (`MaxConcurrentSessions: 1`) with an object store answers `/start` of a derived session with 503 `STARTING_FAILED` ("podsession: reconnect for session ... has no claimed sandbox binding") before any restore fetch runs, so the exclusive Prepare path cannot be driven at tier 9 without harness work outside this fix. The gateway's 503 body on that path copies the adapter's status text, which the tier-1 case shows no longer carries the URL.

**What a later reader would otherwise get wrong:** A reader would expect a tier-9 case that asserts the exclusive Prepare 503 body directly and would find none. The redaction is enforced at the adapter transport, which every bind path shares.
