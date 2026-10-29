# Summary: Restore a derived or replayed session's copied snapshot at its first bind through FinalizeWorkspace

## Summary

**Problem statement.** A session created by `POST /v1/sessions/{id}/derive` or `POST /v1/sessions/{id}/replay` starts with only the bound plan's sources in its workspace, although the specification says it starts from the source session's workspace. Derive and replay copy the snapshot, but no bind path reads the copy and the adapter contract has no carrier for a bind-time restore. The workspace download serves the copied bytes while the pod holds a different tree, and the failure is silent. The specification also gives two conflicting delivery models: §19 Decision 12 routes the snapshot through an `uploadArchive` source, and §7.1 describes a chunk copy.

**What changes.**

- The specification gains one normative home for the restore, §7.1 **Derive restore semantics:**, with pointers from §4.7.1, §5.1, §14.1, §19, §13, §4.4.3, §28, and §6.1 (SPEC-1 to SPEC-7), and SPEC-1 also edits the `DERIVE_SNAPSHOT_UNAVAILABLE` trigger in §7.1 derive rule 2 and its §15.1 catalog row.
- `FinalizeWorkspaceRequest` gains `repeated ChunkGrant restore_chunks = 7` (CODE-1).
- The adapter seeds the build tree from the restore, workspace root only, before the plan's sources (CODE-2).
- The Binder mints grants through a lazy closure immediately before each bind-time `FinalizeWorkspace`, and the two composers set that closure from the row (CODE-3, CODE-4).
- Derive and replay copy chunked checkpoints only; the legacy single-object branch is deleted (CODE-5).
- Tests across tiers 1, 3, 4, and 10, and the BUILD-GAPS records (TEST-1, REC-1).

**Decisions.**

1. Route A: a restore carrier on `FinalizeWorkspace`. Route B (a synthetic `uploadArchive` source) is rejected because it runs platform output through the §7.4 untrusted-upload limits, whose 256 MiB ceiling sits below the 512 MB workspace tier, and makes the gateway read the whole snapshot into memory.
2. The carrier reuses `ChunkGrant` as field 7 and adds no size pre-check fields, because no runtime is running at bind; `ExtractTree`'s cap and the discarded build tree fail closed.
3. Trigger: `row.WorkspaceSnapshot != nil && Ref != ""`. Only derive and replay produce such a row before its first bind, and a row with a snapshot that resumes goes through the Resume RPC. `ParentWorkspaceRef` stays a lineage pointer only.
4. Fail closed with no fallback: nil resolver, any `Resolve` error, and zero grants all fail the bind. `resolveResumeChunks` is not reused because it returns nil with a log line.
5. Grants are minted inside the Binder at the moment of use through a closure on the bind request (the `OnClaimed` precedent), because `checkpointCapabilityTTLSeconds` defaults to 30 and the claim queue and `stageWorkspace` run between composition and `FinalizeWorkspace`. This needs no Binder-to-resolver dependency and no cmd wiring.
6. The two Binder sites, `Prepare` and `materializeSlotStages`, are the only bind-time `FinalizeWorkspace` issuers, and both carry the restore.
7. Restore root: only the workspace `NamedRoot`, so the bundle's `/sessions` tree is skipped by existing `ExtractTree` behaviour.
8. Order: snapshot first, then the plan's `sources` array as a whole under §14.1 last-writer-wins. Restored paths raise no collision warning because the warning's index fields name only sources entries.
9. The restore is the first tier of the §5.1 cross-tier materialization order. §14.1's parenthetical copy of that order is deleted, which leaves its §5.1 link as the only statement of the order.
10. A restore with a non-empty `sdkWarmBlockingPaths` list demotes a preConnect session, because restored paths cannot be enumerated before materialization and §6.1 makes SDK-warm safe only when no blocking file is injected.
11. The `mid_session` + `restore_chunks` refusal extends §4.7.1 rule 1 rather than adding a paragraph.
12. The legacy single-object copy is deleted because §7.1 defines only the chunked copy and no producer writes a single-object ref; its only live effect is misreporting a transient manifest `Get` error as 503. After deletion that error answers 500.
13. Restore errors split: a fetch error is `Unavailable`/transient; a property of the stored snapshot (corruption, escaping path or symlink, unsupported entry, size cap) is `FailedPrecondition`/permanent, because it recurs with fresh grants.
14. No new error code: restore failure surfaces through the existing failed-bind branches as `SESSION_CREATION_FAILED` and `STARTING_FAILED`.
15. The §7.4 exemption for the restored snapshot lives in SPEC-1, and §7.4's universal "All archive extraction runs inside the gateway" sentence is scoped to client uploads and delegation file exports in the same step, so §19 carries only a pointer.
16. F-4.5.24 is not a prerequisite: the memory and filesystem stores implement no Presigner, so no bind-time restore reads a copied chunk from them.

**Watch out for.**

- `buildResolvedTree` clears `buildDir`. A seed placed before the `RemoveAll`/`MkdirAll` is erased; it must run after them and before the first source (CODE-2 step 3).
- `MaterializeOverlayWithPolicy` shares `buildResolvedTree` and must pass a nil seed.
- Refactoring `restoreChunks` into `chunkStream` must keep the early `pr.Close()`, or the producer goroutine leaks when extraction stops early.
- The composers also run on claim-only and launch-only paths. Minting there, or eagerly in the composer, lets the 30 s grants lapse; only the Binder invokes the closure.
- `resolveResumeChunks` looks reusable and silently drops the restore; use `Resolver.Resolve` directly (CODE-4).
- `extractSymlink` confines symlinks to `buildDir`, so a source session's absolute symlink into its own slot fails the restore deterministically. That is the permanent class, not a transient one.
- On exclusive pools derive and replay never claim a pod at create, so a derived row cannot start there today. Tests that expect an exclusive-pool end-to-end restore will fail for that separate reason; the exclusive `Prepare` site is pinned at unit tier only.
- Only `miniostore` implements `ListByPrefix`, so the resolver is nil on s3, gcs, and azure. Tier 4 must run on MinIO, and on those backends a derived bind now fails closed where it used to start empty.
- `TestSnapshotCopyStoresRefOfCopiedObject_spec_7_1` pins the deleted legacy branch and will fail after CODE-5 until converted.
- `RequiresDemotion` must give the same answer from `Prepare` and from `launchPrepared`'s recomputation; route `Prepare`'s inline matcher through it.
- SPEC-3's §5.1 sentence sits in the paragraph proposal 0086 rewrites; apply it to the `Workspace materialization order:` sentence wherever it stands.

## Goals

- A derived or replayed session's pod workspace at its first bind equals the source snapshot overlaid with the bound plan's sources.
- A restore that cannot complete fails the bind and never starts a session with a different workspace.
- The specification states one delivery model for derive and replay.
- The restore carries no source session-file tree into the new session.

## Non-goals

- Route B (a synthetic `uploadArchive` or pre-extracted source through `stageWorkspace`): it runs platform output through the §7.4 and §13.4 untrusted-upload validators and makes the gateway read the snapshot into memory; exempting it would add a trusted mode to the untrusted path.
- Reusing the §7.3 Resume RPC for the first bind: it starts the runtime itself, skips PrepareWorkspace, FinalizeWorkspace, RunSetup, and AssignCredentials, and restores the `/sessions` tree.
- Streaming the snapshot over PrepareWorkspace under a reserved `upload_ref`: it needs a marker or a new source type, and the gateway would proxy every snapshot byte.
- A new WorkspacePlan source type such as `snapshot`: the plan is a client-visible §14 schema, and presigned bearer URLs would sit in a persisted plan.
- A new `RestoreWorkspace` RPC: it adds an RPC and admission row and sits outside FinalizeWorkspace's atomic build-and-promote.
- A checkpoint id or snapshot-ref field with adapter-side resolution: the pod holds no object-store credential or LIST capability, so the gateway mints the grants anyway.
- `expected_workspace_bytes` and `workspace_size_limit_bytes` on `FinalizeWorkspaceRequest`: see decision 2.
- Minting grants in the composers or at each sessionserver bind site: see decision 5.
- A `Binder.RestoreGrants` resolver dependency with a `RestoreCheckpointID` string on the request: it needs cmd wiring and a second resolver consumer, which the closure avoids.
- A strict flag on `resolveResumeChunks`: it would make a dual-mode function.
- Keying the restore on `ParentWorkspaceRef` or on state `created`: see decision 3; the derive-failure row also sets `ParentWorkspaceRef`.
- Restoring a legacy single-object snapshot as a one-grant set, or keeping the legacy copy: see decision 12.
- A full-checkpoint, partial-manifest, or empty fallback for the bind-time restore: the derived manifest is the session's only manifest, so a fallback silently substitutes a different workspace.
- A new error code for a restore failure, or reusing `DERIVE_SNAPSHOT_UNAVAILABLE` at bind: see decision 14.
- A `workspace_plan_path_collision` warning for a source overwriting a restored path: see decision 8.
- Restoring the bundle's `/sessions` tree: see decision 7.
- Fixing F-4.5.24 as a prerequisite: see decision 16. Its candidate fixes are left to that direct fix.
- The F-4.5.21 §4.5 lineage-writer rider: the restore does not depend on it.
- §15.1 replay pointers: §15.1 already states that the workspace is populated from the snapshot, and SPEC-1 covers replay by name.
- A §29.2 step 16 clause: the scenario describes the ordinary session start and stays true.
- A new §4.7.1 restore paragraph as the normative home: §7.1 owns derive behaviour, and §4.7.1 gets only the row and the rule 1 clause.
- A new §14.1 sentence ordering the snapshot before index 0: §14.1 defers cross-tier order to §5.1.
- Leaving §19 Decision 12 unedited: "performs steps 1-3 atomically" names `uploadArchive` delivery, which the new route does not use.
- Making derive and replay claim a pod at create: a separate defect that changes derive and replay responses and pod holding; filed below.
- Fixing the resume of a derived session before its own first checkpoint: pre-existing and needs its own mechanism; filed below.
- Implementing `ListByPrefix` on s3, gcs, and azure: a pre-existing resume gap; filed below.
- Per-chunk re-minting inside one FinalizeWorkspace, or a TTL scaled to chunk count: either adds protocol or changes a capability-model constant; every bind attempt already mints fresh grants.
- Persisting grants or a restore marker on the row or in replica memory: grants expire, and the row's `Ref` plus the derived manifest let any replica rebuild the restore.
- Editing `docs/reference/adapter-contract.md` and `docs/tutorials/session-derive-replay.md`: both stay true.

## Open decisions for human to make

None. The spec loop routed no decision to the human. Its remaining `OPEN` entries in the review log are pre-existing findings it scoped out or non-spec corrections owed to the non-spec loop (the docs pages the staged spec edits make stale), and neither kind needs a human answer.

## Defects in the shipped tree that this proposal does not stage

- **Derive and replay never claim a pod at create.** On an exclusive pool, `prepareAtFinalize` skips the derived row because its PodAssignment is empty, and the launch-only `/start` fails in `reconnect` because the sandbox binding is empty, contrary to the §15.1 `created` row and §7.1 derive rule 2. It stays out because the session cannot start at all, which differs from starting without its workspace, and the fix changes derive and replay responses (`SESSION_CREATION_FAILED`, `CREDENTIAL_POOL_EXHAUSTED`, pool-warming 503) and makes them hold a warm pod under `maxCreatedStateTimeoutSeconds`.
- **Derive and replay mint no `uploadToken`**, although the §15.1 replay response is "identical to POST /v1/sessions". It is unrelated to the restore.
- **Only `miniostore` implements `ListByPrefix`**, so the chunk resolver is nil on s3, gcs, and azure: resume restores nothing there, and after this proposal a derived bind fails closed there. Implementing listing per backend is a separate storage change.
- **A derived or replayed session resumed before its own first checkpoint** restores the derived manifest through the Resume RPC, carrying the source's `/sessions` tree and dropping the plan's sources. It exists independent of this design and needs its own mechanism.
- **F-4.5.24**: the memory and filesystem blob stores expire a copied chunk immediately because the copy sets no TTL. It stays a direct fix in `pkg/blobstore`; no bind-time restore reads from those stores.
- **F-4.5.21**: §4.5 omits replay from the lineage writer list. It is a text correction the restore does not depend on.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0086_fix_the-gateway-accepts-a-workspaceplan-in-the-body-of | Approved | Both edit the composer literals in `pkg/gateway/sessionserver/bind_request.go` and the §5.1 **Derived Runtime Instantiation** paragraph. 0086 rewrites the earlier sentences of that paragraph; 0109 rewrites only the `Workspace materialization order:` sentence. Both also edit different rows of the §15.1 **Error code catalog:**. No semantic dependency. | Whichever lands second rebases textually. |

## Deliverable index

- SPEC-1 — spec/07_session-lifecycle.md, spec/15_external-api-surface.md — new §7.1 **Derive restore semantics:** paragraph, §7.4 extraction-scope clause, §7.1 derive rule 2 503 trigger, and §15.1 `DERIVE_SNAPSHOT_UNAVAILABLE` catalog row.
- SPEC-2 — spec/04_system-components.md — §4.7.1 `FinalizeWorkspace` row and rule 1 pairing clause.
- SPEC-3 — spec/05_runtime-registry-and-pool-model.md, spec/14_workspace-plan-schema.md — snapshot tier in the §5.1 materialization order and deletion of the §14.1 parenthetical copy.
- SPEC-4 — spec/19_resolved-decisions.md — Decision 12 points at §7.1 instead of claiming `uploadArchive` delivery.
- SPEC-5 — spec/13_security-model.md, spec/04_system-components.md — §13.1, §13.2.2, §13.2.3, §13.3, and §4.4.3 capability and egress edits for restore GETs at a derived or replayed session's first bind.
- SPEC-6 — spec/28_communication-channels.md — §28.5.5 CH-OBJSTORE Messages, Preconditions, and failure-semantics edits, and the §28.8 CH-OBJSTORE row.
- SPEC-7 — spec/06_warm-pod-model.md — §6.1 matching-contract sentence for a snapshot restore and `sdkWarmBlockingPaths`.
- CODE-1 — schemas/lenny-adapter.proto, pkg/proto/adapter/v1, pkg/gateway/runtime/adapterclient/client.go — `restore_chunks = 7` and the client carrier.
- CODE-2 — pkg/adapter/staging.go, pkg/adapter/resume.go, pkg/adapter/workspace/materialize.go — build-tree seed, pairing refusal, and error mapping.
- CODE-3 — pkg/gateway/podlifecycle/podsession/ — Binder restore closure call sites and demotion clause.
- CODE-4 — pkg/gateway/sessionserver/bind_request.go, pkg/gateway/sessionserver/snapshot_restore.go — composer restore closure.
- CODE-5 — pkg/gateway/sessionserver/snapshot_copy.go, pkg/gateway/sessionserver/derive.go — chunked-only derive and replay copy.
- TEST-1 — test files across pkg/adapter, pkg/gateway, and tests/tier4_integration — tests listed under the non-spec Testing section.
- REC-1 — BUILD-GAPS.md, gateway-runtime-comms-remediation.md — close findings and file unstaged defects.
