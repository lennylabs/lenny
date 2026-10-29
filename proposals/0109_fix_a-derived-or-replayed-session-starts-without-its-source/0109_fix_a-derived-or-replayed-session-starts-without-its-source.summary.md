# Summary: Restore a derived or replayed session's copied snapshot at its first bind through FinalizeWorkspace

## Summary

**Problem statement.** A session created by `POST /v1/sessions/{id}/derive` or `POST /v1/sessions/{id}/replay` starts with only the bound plan's sources in its workspace, although the specification says it starts from the source session's workspace. Derive and replay copy the snapshot, but no bind path reads the copy and the adapter contract has no carrier for a bind-time restore. The workspace download serves the copied bytes while the pod holds a different tree, and the failure is silent. The specification also gives two conflicting delivery models: §19 Decision 12 routes the snapshot through an `uploadArchive` source, and §7.1 describes a chunk copy.

**What changes.**

- The specification gains one normative home for the restore, §7.1 **Derive restore semantics:**, with pointers from §4.7.1, §5.1, §14.1, §19, §13, §4.4.3, §28, and §6.1 (SPEC-1 to SPEC-7), and SPEC-1 also edits the `DERIVE_SNAPSHOT_UNAVAILABLE` trigger in §7.1 derive rule 2 and its §15.1 catalog row.
- `FinalizeWorkspaceRequest` gains `repeated ChunkGrant restore_chunks = 7` (CODE-1).
- The adapter seeds the build tree from the restore, workspace root only, before the plan's sources (CODE-2).
- The Binder mints grants through a lazy closure immediately before each bind-time `FinalizeWorkspace`, and the two composers set that closure from the row (CODE-3, CODE-4).
- Derive and replay copy chunked checkpoints only; the legacy single-object branch is deleted (CODE-5).
- Tests across tiers 1, 3, 4, and 10, the BUILD-GAPS records, and the docs pages, chart comment, and tier-11 docs check named in DOC-1 (TEST-1, REC-1, DOC-1).

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
13. A restore failure is an ordinary failed bind (SPEC-1), so the adapter answers every restore failure with one status (CODE-2 step 4), and the bind path's existing retry and §5.2 health-ledger accounting apply unchanged. No restore failure is non-retryable. §5.2's non-retryable categories describe the request rather than a stored snapshot, and a failure that fresh grants cannot cure costs only the slot retry budget's extra attempts. §5.2 charges the ledger for a non-retryable failure too, so a permanent class would save only the retried attempts and their ledger charges, at the cost of a second status and a fetch-versus-content boundary inside one concatenated stream.
14. No new error code: restore failure surfaces through `writePodClaimError`'s existing failed-bind branches.
15. The §7.4 exemption for the restored snapshot lives in SPEC-1, and §7.4's universal "All archive extraction runs inside the gateway" sentence is scoped to client uploads and delegation file exports in the same step, so §19 carries only a pointer.
16. F-4.5.24 is not a prerequisite: the memory and filesystem stores implement no Presigner, so no bind-time restore reads a copied chunk from them.
17. `restore_chunks` has no adapter gate. Field 7 joins the adapter protocol at its current version, `"1.0.0"` (§15.4.2 `INIT` row), with no `NegotiateVersion` capability token and no version bump. An adapter built before field 7 would drop it and promote a tree without the snapshot. That skew needs a warm pod stamped with an earlier adapter image to outlive a gateway upgrade, and no release of the protocol has shipped, so it arises only in development clusters. The project rule against backward-compatibility shims excludes a check whose only target is such an adapter. Proposal 0081 added `bind_attempt` to the same request without a gate. An adapter that ignores the field does not conform to the SPEC-2 `FinalizeWorkspace` row.

**Watch out for.**

- `buildResolvedTree` clears `buildDir`. A seed placed before the `RemoveAll`/`MkdirAll` is erased; it must run after them and before the first source (CODE-2 step 3).
- `MaterializeOverlayWithPolicy` shares `buildResolvedTree` and must pass a nil seed.
- Refactoring `restoreChunks` into `chunkStream` must keep the early `pr.Close()`, or the producer goroutine leaks when extraction stops early.
- The composers also run on claim-only and launch-only paths. Minting there, or eagerly in the composer, lets the 30 s grants lapse; only the Binder invokes the closure.
- `resolveResumeChunks` looks reusable and silently drops the restore; use `Resolver.Resolve` directly (CODE-4).
- On exclusive pools derive and replay never claim a pod at create, so a derived row cannot start there today. Tests that expect an exclusive-pool end-to-end restore will fail for that separate reason; the exclusive `Prepare` site is pinned at unit tier only.
- Only `miniostore` implements `ListByPrefix`, so the resolver is nil on s3, gcs, and azure. Tier 4 must run on MinIO, and on those backends derive and replay answer 500 (CODE-5 step 2).
- `RequiresDemotion` must give the same answer from `Prepare` and from `launchPrepared`'s recomputation (CODE-3 step 4).

## Goals

- A derived or replayed session's pod workspace at its first bind equals the source snapshot overlaid with the bound plan's sources.
- A restore that cannot complete fails the bind and never starts a session with a different workspace.
- The specification states one delivery model for derive and replay.
- The restore carries no source session-file tree into the new session.

## Non-goals

- Route B (a synthetic `uploadArchive` or pre-extracted source through `stageWorkspace`): see decision 1.
- Reusing the §7.3 Resume RPC for the first bind: it starts the runtime itself, skips PrepareWorkspace, FinalizeWorkspace, RunSetup, and AssignCredentials, and restores the `/sessions` tree.
- Streaming the snapshot over PrepareWorkspace under a reserved `upload_ref`: it needs a marker or a new source type, and the gateway would proxy every snapshot byte.
- A new WorkspacePlan source type such as `snapshot`: the plan is a client-visible §14 schema, and presigned bearer URLs would sit in a persisted plan.
- A new `RestoreWorkspace` RPC: it adds an RPC and admission row and sits outside FinalizeWorkspace's atomic build-and-promote.
- A checkpoint id or snapshot-ref field with adapter-side resolution: the pod holds no object-store credential or LIST capability, so the gateway mints the grants anyway.
- `expected_workspace_bytes` and `workspace_size_limit_bytes` on `FinalizeWorkspaceRequest`: see decision 2.
- Minting grants in the composers or at each sessionserver bind site: see decision 5.
- A `Binder.RestoreGrants` resolver dependency with a `RestoreCheckpointID` string on the request: it needs cmd wiring and a second resolver consumer, which the closure avoids.
- A strict flag on `resolveResumeChunks`: it would make a dual-mode function.
- A gate on the pod's adapter before minting, whether a `workspaceRestore` capability token in `NegotiateVersionResponse.capabilities` or a protocol bump to `1.1.0` with a selected-version check: see decision 17.
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
- Editing `docs/reference/adapter-contract.md`, the materialization descriptions in `docs/getting-started/concepts.md` (**What happens at each state**, **Workspace lifecycle**), and `docs/reference/state-machines.md`: each says the plan's sources are materialized and promoted, which stays true for a derived session.
- Editing the SDK-warm demotion pages (`docs/runtime-author-guide/lifecycle.md`, `docs/getting-started/concepts.md` **SDK-warm pools**, `docs/runtime-author-guide/publishing.md`, `docs/runtime-author-guide/runtime-configuration.md`, `docs/reference/configuration.md`, `docs/api/internal.md` **DemoteSDK**, and the glossary **SDK-warm** entry): each treats a file matching `sdkWarmBlockingPaths` as a sufficient condition for demotion or says an empty list disables it, and SPEC-7 adds a sufficient condition without changing either statement or the `DemoteSDK` contract.

## Open decisions for human to make

None.

## Defects in the shipped tree that this proposal does not stage

- **Derive and replay never claim a pod at create.** On an exclusive pool, `prepareAtFinalize` skips the derived row because its PodAssignment is empty, and the launch-only `/start` fails in `reconnect` because the sandbox binding is empty, contrary to the §15.1 `created` row and §7.1 derive rule 2. It stays out because the session cannot start at all, which differs from starting without its workspace, and the fix changes derive and replay responses (`SESSION_CREATION_FAILED`, `CREDENTIAL_POOL_EXHAUSTED`, pool-warming 503) and makes them hold a warm pod under `maxCreatedStateTimeoutSeconds`.
- **Derive and replay mint no `uploadToken`**, although the §15.1 replay response is "identical to POST /v1/sessions". It is unrelated to the restore.
- **Only `miniostore` implements `ListByPrefix`**, so the chunk resolver is nil on s3, gcs, and azure: resume restores nothing there, and after this proposal derive and replay answer 500 there. Implementing listing per backend is a separate storage change.
- **A derived or replayed session resumed before its own first checkpoint** restores the derived manifest through the Resume RPC, carrying the source's `/sessions` tree and dropping the plan's sources. It exists independent of this design and needs its own mechanism.
- **F-4.5.24**: the memory and filesystem blob stores expire a copied chunk immediately because the copy sets no TTL. It stays a direct fix in `pkg/blobstore`; no bind-time restore reads from those stores.
- **F-4.5.21**: §4.5 omits replay from the lineage writer list. It is a text correction the restore does not depend on.
- **§5.2 puts the slot failure reason in `error.category`.** §5.2 **Client error on exhaustion** sets `error.category` to the failure reason and names `error.retryable` and `error.sessionId`, while §15.1 and §15.2.1 reserve `error.category` for the §16.3 categories. The gateway writes the reason, `retryable`, and `sessionId` under `error.details` (`writeSlotFailed`), and `SLOT_FAILED` has no §15.1 catalog row, no `errorclassify` entry, and no docs entry. It predates this proposal; the tier-4 restore test asserts the shipped `details.category`.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0086_fix_the-gateway-accepts-a-workspaceplan-in-the-body-of | Approved | Both edit the composer literals in `pkg/gateway/sessionserver/bind_request.go` and the §5.1 **Derived Runtime Instantiation** paragraph. 0086 rewrites the earlier sentences of that paragraph; 0109 rewrites only the `Workspace materialization order:` sentence. Both also edit different rows of the §15.1 **Error code catalog:** and of `docs/reference/error-catalog.md`, different sections of `docs/api/rest.md`, and different operations of `openapi.json`. No semantic dependency. | Whichever lands second rebases textually. SPEC-3's §5.1 edit applies to the `Workspace materialization order:` sentence wherever it stands after 0086 lands. |
| 0094_fix_data-lifecycle-user-erasure-tenant-deletion-and-suspension-f | Implemented (2026-10-21) | CODE-5 deletes the legacy single-object branch of `copyWorkspaceSnapshot`, together with `parseDeriveURIs` and `derivedSnapshotPart`. That retires the half of the subject and key cells of 0094's Session-keyed write table row K2 that names the legacy single object `handleDerive` copies through `blobstore.Copier`. K2's chunk-object half and its `DeleteBySession(tenantID, derivedID)` undo stand, as do rows C8, C9, K1, K3, and K4. A transient manifest `Get` error now answers `500 INTERNAL_ERROR` where it answered `503 DERIVE_SNAPSHOT_UNAVAILABLE`, and C9's refusal surface already lists both codes. | Nothing. 0094 is landed and is not edited. |

## Deliverable index

- SPEC-1 — spec/07_session-lifecycle.md, spec/15_external-api-surface.md — new §7.1 **Derive restore semantics:** paragraph, §7.4 extraction-scope clause, §7.1 derive rule 2 503 trigger, and §15.1 `DERIVE_SNAPSHOT_UNAVAILABLE` catalog row.
- SPEC-2 — spec/04_system-components.md — §4.7.1 `FinalizeWorkspace` row and rule 1 pairing clause.
- SPEC-3 — spec/05_runtime-registry-and-pool-model.md, spec/14_workspace-plan-schema.md — snapshot tier in the §5.1 materialization order and deletion of the §14.1 parenthetical copy.
- SPEC-4 — spec/19_resolved-decisions.md — Decision 12 points at §7.1 instead of claiming `uploadArchive` delivery.
- SPEC-5 — spec/13_security-model.md, spec/04_system-components.md — §13.2.3, §13.1, §13.2.2, §13.3, and §4.4.3 capability and egress edits for restore `GET`s at a derived or replayed session's first bind.
- SPEC-6 — spec/28_communication-channels.md — §28.5.5 CH-OBJSTORE Messages, Preconditions, and failure-semantics edits, and the §28.8 CH-OBJSTORE row.
- SPEC-7 — spec/06_warm-pod-model.md — §6.1 matching-contract sentence for a snapshot restore and `sdkWarmBlockingPaths`.
- CODE-1 — schemas/lenny-adapter.proto, pkg/proto/adapter/v1, pkg/gateway/runtime/adapterclient/client.go, pkg/gateway/sessionserver/upload_to_session.go — `restore_chunks = 7`, the client carrier, and its callers.
- CODE-2 — pkg/adapter/staging.go, pkg/adapter/resume.go, pkg/adapter/workspace/materialize.go — build-tree seed, pairing refusal, and error mapping.
- CODE-3 — pkg/gateway/podlifecycle/podsession/bindrequest.go, slotbinder.go, bindprepare.go, sdkdemotion.go — Binder restore closure call sites and demotion clause.
- CODE-4 — pkg/gateway/sessionserver/bind_request.go, pkg/gateway/sessionserver/snapshot_restore.go (new) — composer restore closure.
- CODE-5 — pkg/gateway/sessionserver/snapshot_copy.go, pkg/gateway/sessionserver/derive.go, pkg/gateway/externalapi/openapi/openapi.json, the tests and test harnesses step 6 reaches, and tests/spec-map.json — chunked-only derive and replay copy.
- TEST-1 — test files across pkg/adapter, pkg/gateway, tests/testinfra/bindattempt, and tests/tier4_integration — tests listed under the non-spec Testing section.
- DOC-1 — docs/reference/error-catalog.md, docs/api/rest.md, docs/getting-started/concepts.md, docs/operator-guide/security.md, docs/reference/glossary.md, docs/tutorials/session-derive-replay.md, charts/lenny/templates/agent-network-policies.yaml, tests/tier11_docs/checkpoint_pipeline_consistency_test.go — client error pages, first-bind pod `GET` wording, glossary and tutorial derive wording, and the tier-11 mirror check.
- REC-1 — BUILD-GAPS.md, gateway-runtime-comms-remediation.md — close findings and file unstaged defects.
