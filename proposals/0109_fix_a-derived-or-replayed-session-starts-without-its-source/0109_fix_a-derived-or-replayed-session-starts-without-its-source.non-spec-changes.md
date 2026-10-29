# Non-spec changes — A derived or replayed session starts without its source workspace

## Design (implementation-facing)

The rule being implemented is SPEC-1, §7.1 **Derive restore semantics:**. The code path is:

1. The sessionserver composers `exclusiveBindRequest` and `slotBindRequest` set a lazy restore closure on the bind request when the row carries a snapshot (CODE-4). They mint nothing.
2. The Binder calls the closure after `stageWorkspace` and immediately before its two bind-time `FinalizeWorkspace` calls, in `Prepare` and in `materializeSlotStages`, and passes the grants on the call (CODE-3). Claim-only and launch-only paths never reach those calls, so the closure is never invoked there.
3. `FinalizeWorkspaceRequest.restore_chunks` carries the grants (CODE-1).
4. The adapter seeds the build tree from the grants, workspace root only, after the build tree is cleared and before the first source, then promotes once (CODE-2).
5. Derive and replay copy chunked checkpoints only (CODE-5).

## Staged code changes

### CODE-1 · Proto field and adapter client carrier

Targets: `schemas/lenny-adapter.proto` (message `FinalizeWorkspaceRequest`), `pkg/proto/adapter/v1` (regenerated), `pkg/gateway/runtime/adapterclient/client.go` (`Client.FinalizeWorkspace`), and every caller of `Client.FinalizeWorkspace` (the Binder sites in CODE-3, and `pkg/gateway/sessionserver/upload_to_session.go`, which passes `nil`).

Add to `FinalizeWorkspaceRequest`, after `bind_attempt = 6`:

```proto
// restore_chunks carries one presigned single-key GET capability per
// chunk of a derived or replayed session's starting snapshot, in
// ascending index order, minted immediately before this call. Empty on
// every other request. A request marked mid_session that carries it is
// answered INVALID_ARGUMENT.
// spec: §4.7.1 (pairing rule); §7.1 (Derive restore semantics).
repeated ChunkGrant restore_chunks = 7;
```

Regenerate the Go bindings; do not hand-edit `*.pb.go`. Change the client signature to

```go
func (c *Client) FinalizeWorkspace(ctx context.Context, sessionID string, plan *adapterv1.WorkspacePlan, archive *adapterv1.ArchivePolicy, restore []ChunkGrant, bindAttempt string, midSession bool) ([]*adapterv1.WorkspacePlanWarning, error)
```

and set `RestoreChunks` with the existing Go-to-proto `ChunkGrant` conversion that the `Resume` path uses. Grants are bearer URLs: never log them.

### CODE-2 · Adapter seeds the build tree from the restore, workspace root only

Targets: `pkg/adapter/staging.go` (`FinalizeWorkspace`), `pkg/adapter/resume.go` (`restoreChunks`), `pkg/adapter/workspace/materialize.go` (`MaterializeWithPolicy`, `MaterializeOverlayWithPolicy`, `buildResolvedTree`).

1. **Factor the fetch stream.** Extract from `restoreChunks` a `chunkStream` helper that fetches the grants in ascending index order through `CheckpointTransport.GetChunk` and concatenates the bodies into one reader. Keep the early `pr.Close()` behaviour so the producer goroutine is unblocked when the consumer stops reading. `restoreChunks` calls the helper and is otherwise unchanged.
2. **Pairing refusal.** Beside `validateBindFields`, before `ensureSlotEntry`, answer a request with `mid_session` set and non-empty `restore_chunks` with `codes.InvalidArgument`, creating, resolving, and stamping nothing (SPEC-2 rule 1).
3. **Seed hook.** Give `MaterializeWithPolicy` and `buildResolvedTree` a nil-able seed function. `buildResolvedTree` calls it after its `RemoveAll`/`MkdirAll` of `buildDir` and before the sources loop. Seeded paths are not entered in the `seen` collision map. `MaterializeOverlayWithPolicy` (the mid-session path) passes a nil seed.
4. **Seed body and error mapping.** Run the seed as `workspace.ExtractTree([]workspace.NamedRoot{{Prefix: workspace.WorkspacePrefix, Root: buildDir}}, chunkStream(...))`. Passing only the workspace root makes `ExtractTree` skip the bundle's `/sessions` entries. A chunk fetch error (`CheckpointTransport.GetChunk`, or a body read error from the chunk stream) is wrapped as `errRestoreFetch` and maps to `codes.Unavailable` `"restore workspace snapshot: ..."` with `CategoryTransient`. Any other restore failure is a property of the stored snapshot and recurs with fresh grants, so it maps to `codes.FailedPrecondition` `"restore workspace snapshot: ..."` with `CategoryPermanent`. That covers gzip or tar corruption, an escaping entry path, a symlink whose target leaves `buildDir` or enters a forbidden mount (absolute targets into the source session's slot included), an unsupported entry type, and the `maxExtractBytes` cap. Source materialization errors keep `InvalidArgument`/`CategoryPermanent`. In every case `buildDir` is removed and `current` is left untouched.

### CODE-3 · Binder calls the restore closure immediately before FinalizeWorkspace; demotion clause

Targets: `pkg/gateway/podlifecycle/podsession/bindrequest.go` (`BindRequest`, `SlotBindRequest`), `bindprepare.go` (`Prepare`), `slotbinder.go` (`materializeSlotStages`), `sdkdemotion.go` (`RequiresDemotion`).

1. **Field.** Add to both request types, documented like `OnClaimed`:

   ```go
   // RestoreChunks mints the presigned GET capabilities for the session's
   // starting snapshot. nil means no restore. The Binder calls it once per
   // bind attempt, after stageWorkspace and immediately before
   // FinalizeWorkspace, because the capabilities expire after
   // checkpointCapabilityTTLSeconds. The result is never logged or persisted.
   // spec: §7.1 (Derive restore semantics).
   RestoreChunks func(context.Context) ([]adapterclient.ChunkGrant, error)
   ```

2. **Prepare.** Immediately before the `FinalizeWorkspace` call:

   ```go
   var restore []adapterclient.ChunkGrant
   if req.RestoreChunks != nil {
       if restore, err = req.RestoreChunks(ctx); err != nil {
           reclaim(err)
           return nil, fmt.Errorf("podsession: resolve workspace restore on pod %s: %w", sandboxName, err)
       }
   }
   // then: cl.FinalizeWorkspace(ctx, req.SessionID, stagedPlan, req.ArchivePolicy, restore, bindAttempt, false)
   ```

3. **Slot path.** The same in `materializeSlotStages` immediately before its `FinalizeWorkspace` call, failing through `recordSlotFailure(slotFailureWorkspaceFinalize)` and `slotBindError(slotFailureWorkspacePrep)`.
4. **Demotion.** `RequiresDemotion` returns `false` when `!req.PreConnect`; returns `true` when `req.RestoreChunks != nil && len(req.SDKWarmBlockingPaths) > 0`; otherwise runs the existing path match. `Prepare`'s inline matcher calls `RequiresDemotion` so `Prepare`, `/finalize`, and `launchPrepared`'s recomputation reach the same answer, and passes the `DemoteSDK` reason `"workspace starts from a restored snapshot"` when the restore clause decided it. `// spec: §7.1 (Derive restore semantics); §6.1.`

The closure is never invoked on a claim-only or launch-only path, and a retried bind calls it again for fresh grants.

### CODE-4 · Sessionserver: one restore decision in the two composers

Targets: `pkg/gateway/sessionserver/bind_request.go` (`exclusiveBindRequest`, `slotBindRequest`), `pkg/gateway/sessionserver/snapshot_restore.go` (new).

```go
// workspaceRestore returns the closure that mints the starting snapshot's
// GET capabilities for a derived or replayed session's first bind, or nil
// when the row carries no snapshot. It has no fallback.
// spec: §7.1 (Derive restore semantics).
func (s *Server) workspaceRestore(row sessionstore.Session) func(context.Context) ([]adapterclient.ChunkGrant, error) {
    if row.WorkspaceSnapshot == nil || row.WorkspaceSnapshot.Ref == "" {
        return nil
    }
    tenant, id, cp := row.TenantID, row.ID, checkpointIDFromSnapshotRef(row.WorkspaceSnapshot.Ref)
    return func(ctx context.Context) ([]adapterclient.ChunkGrant, error) {
        if s.resumeChunkResolver == nil {
            return nil, errRestoreUnwired
        }
        res, err := s.resumeChunkResolver.Resolve(ctx, tenant, id, cp)
        if err != nil {
            return nil, fmt.Errorf("resolve starting snapshot %s: %w", cp, err)
        }
        if len(res.Grants) == 0 {
            return nil, errRestoreEmpty
        }
        return res.Grants, nil
    }
}
```

`errRestoreUnwired` and `errRestoreEmpty` are new sentinels in the same file. Both composers set `RestoreChunks: s.workspaceRestore(row)`.

Trap: do not call `resolveResumeChunks` here. It returns nil with only a log line on an unwired resolver, a missing checkpoint id, or a resolve error, which would start the session silently without its workspace. `resolveResumeChunks` keeps its resume behaviour unchanged.

### CODE-5 · Derive and replay copy chunked checkpoints only

Targets: `pkg/gateway/sessionserver/snapshot_copy.go` (`copyWorkspaceSnapshot` and its doc comment), `pkg/gateway/sessionserver/derive.go` (`deriveChunkedCheckpoint`, `parseDeriveURIs`, `derivedSnapshotPart`), and the tests that seed path-form or URI-form source refs.

1. Delete the legacy single-object branch of `copyWorkspaceSnapshot`, drop the `handled` return of `deriveChunkedCheckpoint`, and delete `parseDeriveURIs` and `derivedSnapshotPart`.
2. Error mapping after the deletion:
   - manifest `ErrNotFound`, or `ChunkCount <= 0`: `errSnapshotRefUnresolvable`, answered `503 DERIVE_SNAPSHOT_UNAVAILABLE` through the existing `writeSnapshotCopyError`/`snapshotMissing`;
   - a chunk `Copy` returning `blobstore.ErrNotFound`: 503, as today;
   - unwired manifests, an unwired writer, or no `Copier`: `errSnapshotCopierUnwired`, 500;
   - any other error, including a transient manifest `Get` error: 500 `INTERNAL_ERROR`.
3. Rewrite the `copyWorkspaceSnapshot` doc comment to describe only the chunked copy and the mapping above.
4. Keep the copy loop, `Put` → `ConfirmChunk` → `Finalise`, and `DerivedSnapshotTTL` unchanged. `session_subresources.go` is untouched.

## Staged schema, chart, and migration changes

The proto change is part of CODE-1. No chart change and no migration.

## Staged docs changes

### REC-1 · Records

Targets: `BUILD-GAPS.md`, `gateway-runtime-comms-remediation.md` §10.2.

- Close F-7.1.34 (restore half) and correct its stale lead that says replay copies no bytes. Close F-7.1.33.
- Leave F-4.5.24 and F-4.5.21 open as independent fixes.
- File one new entry for each defect listed in the summary's **Defects in the shipped tree that this proposal does not stage**, excluding the two already-filed findings above.
- Update the remediation plan's §10.2 position for this proposal.

## Testing

Every test carries `// spec:` naming §7.1 (Derive restore semantics) plus the section in parentheses below; tier-2-and-up tests carry `// diagnosis:`. The tests below are TEST-1.

**Adapter, tiers 1 and 10** (`pkg/adapter/workspace/materialize_test.go`, `pkg/adapter/staging_test.go` or a new `staging_restore_test.go`, `pkg/adapter/resume_test.go`):

- Order: a restored `a.txt` and `keep.txt`, a source writing `a.txt`. After promotion `a.txt` holds the source bytes, `keep.txt` the restored bytes, and no `workspace_plan_path_collision` warning is emitted. (§5.1, §14.1)
- Workspace root only: a bundle carrying both workspace and `/sessions` entries. After promotion no `/sessions/{id}` path exists. This is the discriminating assertion for the restore root.
- Failure table, each leaving `current` byte-identical to its pre-call state and `buildDir` removed: a mid-stream `GetChunk` error gives `Unavailable`; a truncated gzip, an escaping `..` entry, an absolute symlink into another session's slot, and an over-cap bundle each give `FailedPrecondition`.
- `mid_session` plus `restore_chunks` gives `InvalidArgument` with no slot entry created, including when the adapter holds no entry for the slot. (§4.7.1 rule 1)
- Empty `restore_chunks` on a non-mid-session request materializes exactly as today.
- `resume_test.go` regression after the `chunkStream` refactor, including a consumer that stops early (no goroutine leak under `-race`).

**Binder, tier 1** (`pkg/gateway/podlifecycle/podsession/binder_phases_test.go`, `slotbinder_test.go`, `sdkwarm_bind_test.go`):

- The closure is invoked after `stageWorkspace` and immediately before `FinalizeWorkspace`, once per attempt; a retried bind invokes it again. Assert call order with a recording fake.
- Closure error: no `FinalizeWorkspace` is sent; `Prepare` reclaims; the slot path records `slotFailureWorkspaceFinalize` and returns `slotFailureWorkspacePrep`.
- The closure is never invoked on claim-only or launch-only composition.
- `RequiresDemotion` with a restore and non-empty blocking paths is `true`; with an empty list it is `false`; with `PreConnect` false it is `false`. `Prepare` and `launchPrepared` agree for the same row. (§6.1)
- Exclusive-pool `Prepare` restore is pinned at this tier only until the create-time claim defect is fixed.

**Sessionserver, tiers 1 and 4** (`snapshot_restore_test.go` new, `bind_request_test.go` new, `snapshot_copy_test.go`, `derive_test.go`, `replay_test.go`):

- `workspaceRestore` returns nil for a nil snapshot and for an empty `Ref`. For a row with a `Ref`, the closure fails on a nil resolver, on each resolver error (contiguity, missing manifest, presign), and on zero grants; it never returns a nil error with no grants.
- Both composers set `RestoreChunks` for a derived row and leave it nil for an ordinary row.
- CODE-5: a transient non-`ErrNotFound` manifest `Get` error answers 500 `INTERNAL_ERROR` with no derived row or objects left behind. `TestSnapshotCopyStoresRefOfCopiedObject_spec_7_1` and the path-form ref fixture in `derive_test.go` are converted into the `503 DERIVE_SNAPSHOT_UNAVAILABLE` no-residue assertion.

**Contract, tier 3:** `FinalizeWorkspaceRequest.restore_chunks` round-trips through `adapterclient` and the adapter server with grant URLs and headers intact, and in index order.

**Integration, tier 4** (`tests/tier4_integration/checkpoint_workspace_derive_test.go`, beside `TestDeriveCopiesEveryChunkIntoDerivedPrefix` and `TestReplayCopiesEveryChunkIntoReplayedPrefix`; add `§7.1 Derive restore semantics` to the file header spec line): derive, replay `prompt_history`, and replay `workspace_derive` onto a concurrent-workspace pool using the in-process adapter harness `concurrent_workspace_test.go` uses, against MinIO. Assert the bound pod's `current` equals the source snapshot overlaid with the plan's sources, that the workspace download and the pod tree agree, and that a deleted derived chunk fails the bind with `STARTING_FAILED` and leaves no started session. Tier 4 runs on MinIO because only `miniostore` implements `ListByPrefix`.

## Edge cases and accepted failure modes

- On s3, gcs, and azure backends the chunk resolver is nil, so a derived or replayed bind fails closed there. This is recorded as a defect, not fixed here.
- On exclusive pools a derived or replayed session cannot start until the create-time claim defect is fixed, so the restore is observable end to end only on concurrent-workspace pools.
- A large snapshot with many chunks must be fetched within one `checkpointCapabilityTTLSeconds` window per grant; a lapse fails the attempt as `Unavailable`, and the next attempt mints fresh grants.

## Files touched on application (non-spec)

- schemas/lenny-adapter.proto
- pkg/proto/adapter/v1/ (regenerated)
- pkg/gateway/runtime/adapterclient/client.go
- pkg/gateway/sessionserver/upload_to_session.go
- pkg/adapter/staging.go
- pkg/adapter/resume.go
- pkg/adapter/workspace/materialize.go
- pkg/gateway/podlifecycle/podsession/bindrequest.go
- pkg/gateway/podlifecycle/podsession/bindprepare.go
- pkg/gateway/podlifecycle/podsession/slotbinder.go
- pkg/gateway/podlifecycle/podsession/sdkdemotion.go
- pkg/gateway/sessionserver/bind_request.go
- pkg/gateway/sessionserver/snapshot_restore.go (new)
- pkg/gateway/sessionserver/snapshot_copy.go
- pkg/gateway/sessionserver/derive.go
- Test files named under Testing
- BUILD-GAPS.md
- gateway-runtime-comms-remediation.md
