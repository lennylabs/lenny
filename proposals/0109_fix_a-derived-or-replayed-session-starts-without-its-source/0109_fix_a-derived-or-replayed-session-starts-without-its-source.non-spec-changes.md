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

Targets: `pkg/adapter/staging.go` (`FinalizeWorkspace`), `pkg/adapter/resume.go` (`restoreChunks`), and `pkg/adapter/workspace/materialize.go` (`MaterializeWithPolicy`, `MaterializeOverlayWithPolicy`, `buildResolvedTree`).

1. **Factor the fetch stream.** Extract from `restoreChunks` a `chunkStream` helper that fetches the grants in ascending index order through `CheckpointTransport.GetChunk` and concatenates the bodies into one reader. Keep the early `pr.Close()` behaviour so the producer goroutine is unblocked when the consumer stops reading. `restoreChunks` calls the helper and is otherwise unchanged.
2. **Pairing refusal.** Beside `validateBindFields`, before `ensureSlotEntry`, answer a request with `mid_session` set and non-empty `restore_chunks` with `codes.InvalidArgument`, creating, resolving, and stamping nothing (SPEC-2 rule 1).
3. **Seed hook.** Give `MaterializeWithPolicy` and `buildResolvedTree` a nil-able seed function. `buildResolvedTree` calls it after its `RemoveAll`/`MkdirAll` of `buildDir` and before the sources loop. Seeded paths are not entered in the `seen` collision map. `MaterializeOverlayWithPolicy` (the mid-session path) passes a nil seed.
4. **Seed body and error mapping.** Run the seed as `workspace.ExtractTree([]workspace.NamedRoot{{Prefix: workspace.WorkspacePrefix, Root: buildDir}}, chunkStream(...))`. Passing only the workspace root makes `ExtractTree` skip the bundle's `/sessions` entries. The seed function `FinalizeWorkspace` passes wraps every error it returns in the sentinel `errRestore`, and `buildResolvedTree` returns that error wrapped with `%w`. The restore failures are a chunk fetch error (`CheckpointTransport.GetChunk`), a chunk body read error, gzip or tar corruption, an escaping entry path, a symlink whose target leaves `buildDir` or enters a forbidden mount (absolute targets into the source session's slot included), an unsupported entry type, and the `maxExtractBytes` cap. `FinalizeWorkspace` answers an `errRestore` error with `codes.Unavailable` `"restore workspace snapshot: ..."` and span category `CategoryTransient`, in an arm ahead of its existing `InvalidArgument` arm, which source materialization errors keep. `SlotBindError.Reason` reads `codes.Unavailable` as transient at every stage, so the failure is an ordinary failed bind on the slot path (decision 13). In every case `buildDir` is removed and `current` is left untouched.

### CODE-3 · Binder calls the restore closure immediately before FinalizeWorkspace; demotion clause

Targets: `pkg/gateway/podlifecycle/podsession/bindrequest.go` (`BindRequest`), `slotbinder.go` (`SlotBindRequest`, `materializeSlotStages`), `bindprepare.go` (`Prepare`), and `sdkdemotion.go` (`RequiresDemotion`).

1. **Field.** Add to both request types, documented like `OnClaimed`:

   ```go
   // RestoreChunks mints the presigned GET capabilities for the session's
   // starting snapshot. nil means no restore. The Binder calls it at most once
   // per bind attempt, after stageWorkspace and immediately before
   // FinalizeWorkspace, because the capabilities expire after
   // checkpointCapabilityTTLSeconds. The result is never logged or persisted.
   // spec: §7.1 (Derive restore semantics).
   RestoreChunks func(context.Context) ([]adapterclient.ChunkGrant, error)
   ```

2. **Prepare.** Immediately before the `FinalizeWorkspace` call, invoke the closure when it is non-nil. No Binder signature changes; the closure travels on the request (decision 17 records why the pod's adapter is not gated).

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
4. **Demotion.** An unexported `demotionReason(req BindRequest) (string, bool)` in `sdkdemotion.go` returns `false` when `!req.PreConnect`; returns `true` with the reason `"workspace starts from a restored snapshot"` when `req.RestoreChunks != nil && len(req.SDKWarmBlockingPaths) > 0`; otherwise runs the existing `sdkwarm.RequiresDemotion` path match with its existing reason. `RequiresDemotion` returns its bool, and `Prepare` takes both the decision and the `DemoteSDK` reason from it, so `Prepare`, `/finalize`, and `launchPrepared`'s recomputation reach the same answer. `// spec: §7.1 (Derive restore semantics); §6.1.`

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

Targets: `pkg/gateway/sessionserver/snapshot_copy.go` (`copyWorkspaceSnapshot` and its doc comment), `pkg/gateway/sessionserver/derive.go` (`deriveChunkedCheckpoint`, `parseDeriveURIs`, `derivedSnapshotPart`), `pkg/gateway/externalapi/openapi/openapi.json`, the tests and test harnesses step 6 reaches, and `tests/spec-map.json`.

1. Delete the legacy single-object branch of `copyWorkspaceSnapshot`, drop the `handled` return of `deriveChunkedCheckpoint`, and delete `parseDeriveURIs` and `derivedSnapshotPart`.
2. Error mapping after the deletion:
   - manifest `ErrNotFound`, or `ChunkCount <= 0`: `errSnapshotRefUnresolvable`, answered `503 DERIVE_SNAPSHOT_UNAVAILABLE` through the existing `writeSnapshotCopyError`/`snapshotMissing`;
   - a chunk `Copy` returning `blobstore.ErrNotFound`: 503, as today;
   - unwired manifests, an unwired writer, no `Copier`, or no chunk resolver (`resumeChunkResolver`, which CODE-4's restore reads): `errSnapshotCopierUnwired`, 500, checked before any chunk is copied;
   - any other error, including a transient manifest `Get` error: 500 `INTERNAL_ERROR`.
3. Rewrite the `copyWorkspaceSnapshot` doc comment to describe only the chunked copy and the mapping above, and widen the `errSnapshotCopierUnwired` message and doc comment to "cannot copy or restore the source workspace snapshot".
4. Keep the copy loop, `Put` → `ConfirmChunk` → `Finalise`, and `DerivedSnapshotTTL` unchanged. `session_subresources.go` is untouched.
5. Add a `503` response described as `DERIVE_SNAPSHOT_UNAVAILABLE`, with the `Error` schema, to the POST operations of `/v1/sessions/{id}/derive` and `/v1/sessions/{id}/replay` in the hand-maintained `openapi.json`.
6. `TestSnapshotCopyFailsClosed_spec_7_1` carries one case per condition step 2 names, on derive and both replay modes, with its existing no-row, no-object, and released-reservation assertions. Each unwired case leaves only its own dependency unwired, so no other case reaches its status through the unwired arm. `TestSnapshotCopyStoresRefOfCopiedObject_spec_7_1` and `TestDeriveCopiesParentBytesIntoDerivedURI` pin the deleted branch and are deleted, and the `derive_test.go::TestDeriveCopiesParentBytesIntoDerivedURI` entry is removed from `tests/spec-map.json`. Every other derive or replay test seeded with a path-form or URI-form source ref keeps its subject and is re-seeded as a chunked checkpoint, with its chunk objects present and a manifest reader and writer wired. Every session server a test or test harness builds to serve a derive or replay that reaches the copy wires a chunk resolver (step 2). `runDerive` in `tests/testinfra/lifecyclefence/derive_path.go` is one: its memory store cannot presign and it never binds the derived session, so it wires a stub. The shared seeds are `newSourceSession` and `snapshotBlobs` in `derive_test.go`, `seedSourceWithSnapshot` in `creation_fence_test.go`, and `seedSnapshotObject` in `tests/tier4_integration/gateway_postgres_e2e_test.go`. An assertion on the legacy derived object (`derivedObject`) moves to the derived chunk objects.

## Staged schema, chart, and migration changes

The proto change is part of CODE-1. The chart comment edit is part of DOC-1. No chart template change and no migration.

## Staged docs changes

### DOC-1 · Docs pages, chart comment, and tier-11 mirror check

Targets: `docs/reference/error-catalog.md`, `docs/api/rest.md`, `docs/getting-started/concepts.md`, `docs/operator-guide/security.md`, `docs/reference/glossary.md`, `docs/tutorials/session-derive-replay.md`, `charts/lenny/templates/agent-network-policies.yaml`, and `tests/tier11_docs/checkpoint_pipeline_consistency_test.go`. Do not cite § numbers in the docs text.

- In the error-catalog `DERIVE_SNAPSHOT_UNAVAILABLE` row, replace "The referenced workspace snapshot object was not found in object storage." with "The source session's workspace snapshot was not found on derive or replay." The rest of the row is unchanged, and the row stays one physical line.
- In the `POST /v1/sessions/{id}/replay` section of `docs/api/rest.md`, replace "**Key error codes:** `REPLAY_ON_LIVE_SESSION` (409), `INCOMPATIBLE_RUNTIME` (400)." with "**Key error codes:** `REPLAY_ON_LIVE_SESSION` (409), `INCOMPATIBLE_RUNTIME` (400), and `DERIVE_SNAPSHOT_UNAVAILABLE` (503)."
- In the **Gateway-mediated file delivery** paragraph of `docs/getting-started/concepts.md` and in the `File delivery` row of the **Security Context** table of `docs/operator-guide/security.md`, replace "and on resume `GET`s them from" with "and on resume and at a derived or replayed session's first bind `GET`s them from". The security.md row stays one physical line. Both restate SPEC-5(b).
- In the **Workspace Plan** entry of `docs/reference/glossary.md`, replace "Includes file sources (client uploads, derived session snapshots, runtime defaults), directory structure, and environment variable configuration." with "It holds a `sources` array of inline files, uploaded files and archives, directories, and Git clones to materialize, and a `setupCommands` array." A derived session's snapshot is not a plan source (SPEC-1), and `env` is a session-creation field outside the plan (§14.1 **Envelope terminology**).
- In **Step 2: Derive a New Session** of `docs/tutorials/session-derive-replay.md`, replace "The derived session starts in `created` state with the source's workspace already staged. You can upload additional files, finalize, and start it:" with "The derived session starts in `created` state and holds a copy of the source's workspace. Files you upload are applied over that copy, so an uploaded file replaces the copied file at the same path. You can upload additional files, finalize, and start it:". "Already staged" names the staging delivery that SPEC-4 retires.
- In the comment above `allow-pod-egress-objectstore` in `charts/lenny/templates/agent-network-policies.yaml`, replace
  ```
  object storage so the pod can PUT checkpoint chunks and GET them back on
  resume against gateway-minted presigned capabilities. It renders from the
  ```
  with
  ```
  object storage so the pod can PUT checkpoint chunks and, on resume and at a
  derived or replayed session's first bind, GET them against gateway-minted
  presigned capabilities. It renders from the
  ```
  No template or chart test changes.
- In `tests/tier11_docs/checkpoint_pipeline_consistency_test.go`, `TestCheckpointDocsMirrorsAgreeWithSpec131`:
  - In the `// diagnosis:` comment, replace "and on resume GETs" / "them from, object storage" (two wrapped comment lines) with "and on resume and at" / "a derived or replayed session's first bind GETs them from, object storage", re-wrapping the comment as needed.
  - Add `at a derived or replayed session's first bind` to the substrings required of the §13.1 `Checkpoint transfer` row.
  - Assert the same substring in the security.md `File delivery` row and in the concepts.md **Gateway-mediated file delivery** prose. The architecture.md row names no phase and is not asserted.
  - `// spec: 13.1` is unchanged.

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
- Failure table, one case per restore failure CODE-2 step 4 names (the fetch-error case failing after the first chunk has streamed), each asserting the status step 4 assigns, `current` byte-identical to its pre-call state, and `buildDir` removed, plus one case of a source materialization failure on a request that carries a restore, asserting the status step 4 leaves to source errors.
- Add a `FinalizeWorkspace mid-session with restore_chunks` case to `bindattempt.PairingRule` (`tests/testinfra/bindattempt/admission.go`), sending an empty `bind_attempt` so that only the restore pairing refuses it, and name the case in the function doc comment. It runs at tier 10 (`slot_bind_attempt_conformance_test.go`) and at tier 3 over gRPC (`bind_attempt_cascade_wire_test.go`). (§4.7.1 rule 1)
- Empty `restore_chunks` on a non-mid-session request materializes exactly as today.
- `resume_test.go` regression after the `chunkStream` refactor, including a consumer that stops early (no goroutine leak under `-race`).

**Binder, tier 1** (`pkg/gateway/podlifecycle/podsession/binder_phases_test.go`, `slotbinder_test.go`, `sdkwarm_bind_test.go`):

- The closure is invoked after `stageWorkspace` and immediately before `FinalizeWorkspace`, once per attempt; a retried bind invokes it again. Assert call order with a recording fake, and, with a closure returning distinct grants per call, that each recorded `FinalizeWorkspaceRequest.restore_chunks` equals that attempt's closure result in index order, on both `Prepare` and `materializeSlotStages`, and is empty when `RestoreChunks` is nil.
- Closure error: no `FinalizeWorkspace` is sent; `Prepare` reclaims; the slot path records `slotFailureWorkspaceFinalize` and returns `slotFailureWorkspacePrep`.
- The closure is never invoked on claim-only or launch-only composition.
- `RequiresDemotion` with a restore and non-empty blocking paths is `true`; with an empty list it is `false`; with `PreConnect` false it is `false`. `Prepare` and `launchPrepared` agree for the same row. (§6.1)
- Exclusive-pool `Prepare` restore is pinned at this tier only until the create-time claim defect is fixed.

**Sessionserver, tier 1** (`snapshot_restore_test.go` new, `bind_request_test.go` new):

- `workspaceRestore` returns nil for a nil snapshot and for an empty `Ref`. For a row with a `Ref`, the closure fails on a nil resolver, on each resolver error (contiguity, missing manifest, presign), and on zero grants; it never returns a nil error with no grants. With a recording resolver, the closure passes the derived row's `TenantID` and `ID` and the `Ref`'s checkpoint id, which is the discriminating assertion for the SPEC-5(a) key scope; with a `resumechunks.Resolver` over in-memory fakes, a `Ref` naming a checkpoint outside the derived session's prefix fails contiguity and mints nothing.
- Both composers set `RestoreChunks` for a derived row and leave it nil for an ordinary row.

**Contract, tier 3:** `FinalizeWorkspaceRequest.restore_chunks` round-trips through `adapterclient` and the adapter server with grant URLs and headers intact, and in index order.

**Integration, tier 4** (`tests/tier4_integration/checkpoint_workspace_derive_test.go`, beside `TestDeriveCopiesEveryChunkIntoDerivedPrefix` and `TestReplayCopiesEveryChunkIntoReplayedPrefix`; add `§7.1 Derive restore semantics` to the file header spec line): derive, replay `prompt_history`, and replay `workspace_derive` onto a concurrent-workspace pool using the in-process adapter harness `concurrent_workspace_test.go` uses, against MinIO. Assert the bound pod's `current` equals the source snapshot overlaid with the plan's sources, including when the source session's chunk objects are deleted before `/start`, and that a derived chunk overwritten in place with the same number of bytes that are not a gzip stream answers `/start` with 422 `SLOT_FAILED` (`details.category` `transient`; see the summary's defects list on `error.category`) after the §5.2 slot retry and leaves no started session. The corrupted chunk passes the gateway's resolver, so this case pins CODE-2 step 4's status through the gateway's slot classification. Tier 4 runs on MinIO because only `miniostore` implements `ListByPrefix`.

## Edge cases and accepted failure modes

- On s3, gcs, and azure backends the chunk resolver is nil, so derive and replay answer 500 there (CODE-5 step 2). This is recorded as a defect, not fixed here.
- On exclusive pools a derived or replayed session cannot start until the create-time claim defect is fixed, so the restore is observable end to end only on concurrent-workspace pools.
- A large snapshot with many chunks must be fetched within one `checkpointCapabilityTTLSeconds` window per grant; a lapse fails the attempt like any other restore failure (CODE-2 step 4), and a later attempt mints fresh grants.

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
- pkg/gateway/externalapi/openapi/openapi.json
- Test files named under Testing, and the tests and test harnesses CODE-5 step 6 reaches
- tests/spec-map.json
- docs/reference/error-catalog.md
- docs/api/rest.md
- docs/getting-started/concepts.md
- docs/operator-guide/security.md
- docs/reference/glossary.md
- docs/tutorials/session-derive-replay.md
- charts/lenny/templates/agent-network-policies.yaml
- tests/tier11_docs/checkpoint_pipeline_consistency_test.go
- BUILD-GAPS.md
- gateway-runtime-comms-remediation.md
