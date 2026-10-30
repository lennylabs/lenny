# Problem: A derived or replayed session starts without its source workspace

## Statement

A derived or replayed session starts without its source's workspace. This is the restore half of BUILD-GAPS F-7.1.34 (High, open). The specification states in several places that the copied snapshot is the new session's starting workspace: §7.1 "Derive session semantics" ("pre-populated with the source session's workspace snapshot"), the §15.1 derive endpoint row, §15.1 "Session Replay Semantics" (`prompt_history` populates the workspace from the source's sealed final snapshot or last checkpoint, and `workspace_derive` "starts with the source workspace"), §4.5 "Workspace lineage:", and §19 Resolved Decision 12.

The copy half landed in commit 3be845f0b. Derive and replay share `copyWorkspaceSnapshot`, replay reserves its id before copying, both fail closed on a copy error, and both write the copy's ref to `WorkspaceSnapshot.Ref` and the source ref to `ParentWorkspaceRef` on a new row in state `created`. That row reaches the pod through the ordinary create, finalize, and start bind paths. No bind path reads either field: `exclusiveBindRequest` and `slotBindRequest` build the bind request from the row, the match, and the plan alone, and `finalize.go`, `start.go`, `pod_launch.go`, and `pod_claim.go` never reference the snapshot. The adapter contract has no carrier for a restore on this path. `FinalizeWorkspaceRequest` carries `session_id`, `workspace_plan`, `archive_policy`, `mid_session`, and `bind_attempt`, and §4.7.1 describes `FinalizeWorkspace` only as "Validate, materialize to `/workspace/slots/{sessionId}/current`". The only chunk restore, `ResumeRequest.chunks` applied by the adapter's `restoreChunks`, belongs to the §7.3 resume path, which starts the runtime itself and so cannot replace the §4.7.9 step 5 sequence PrepareWorkspace → FinalizeWorkspace → RunSetup → AssignCredentials → StartSession.

The specification does describe a delivery model, and it contradicts itself. §19 Resolved Decision 12 defines derive as performing atomically what a client does by hand: download the snapshot, create a session, and upload the snapshot as an `uploadArchive` source in the new session's WorkspacePlan. §7.1 "Derive copy semantics" and §4.5 "Workspace lineage:" instead describe a chunk-by-chunk copy with a derived manifest row. The code follows the second model for the copy and implements neither for delivery.

The proposal stages the restore end to end: a delivery route for the snapshot on a non-mid-session bind, the §4.7.1, §7.1, and §19 text that names that route, the gateway filling it on every bind path that binds a `created` row with a parent snapshot (exclusive finalize and start, combined `POST /v1/sessions/start`, and slot bind), and the pod-side restore. It chooses between two candidate routes and records why it rejects the other:

- Route A adds a restore carrier to `FinalizeWorkspaceRequest` (for example, repeated `ChunkGrant` plus the expected workspace bytes for the size pre-check), filled by the gateway from the copied manifest and applied by the adapter's trusted chunk restore before the plan's sources.
- Route B injects the reassembled snapshot as a synthetic `uploadArchive` or pre-extracted source at the head of the bound plan, through the existing gateway staging path (`stageWorkspace` and `rewriteExtractedSources` stream extracted entries over PrepareWorkspace ahead of FinalizeWorkspace), with no proto change. This route matches §19 Decision 12 but runs a platform-produced snapshot through the §7.4 and §13.4 untrusted-upload validators: a 256 MiB decompressed ceiling against a 512 MB workspace hard limit, a 10 000-entry limit, symlink default-deny, and full in-memory reads (`io.ReadAll`) in the gateway.

Within that scope the proposal decides:

- (a) The fail-closed rule. `resolveResumeChunks` returns no chunks with only a log line when the resolver is unwired, when no checkpoint id exists, or on any resolve error other than contiguity or threshold. That is acceptable on resume, and it would silently lose the snapshot on a bind-time restore.
- (b) How a legacy single-object snapshot is restored. `copyWorkspaceSnapshot` copies a pre-chunked snapshot as one object with no manifest row, and `restoreChunks` reads only an ordered `ChunkGrant` stream.
- (c) The restore root. `restoreChunks` extracts the whole checkpoint bundle into both `/workspace/slots/{id}/current` and `/sessions/{id}`, so reusing it unchanged would carry the source's runtime session file into a session that §15.1 says "starts fresh" and that may target a different runtime. The restore limits itself to the workspace root.
- (d) The order between the snapshot and the bound plan's `sources` array (F-7.1.33), stated as "snapshot first, then the bound plan's `sources` array as a whole under the §14.1 path collision rule". That statement does not depend on approved proposal 0086's runtime `workspaceDefaults` text, which has not landed in `spec/` and which places the defaults inside the same array.
- (e) Sequencing against proposal 0086 (Approved, not implemented), whose code changes edit the same bind-request composer in `bind_request.go`.

Two related findings sit at the edge of the scope. F-4.5.24 (memory and filesystem blob stores expire a copied chunk immediately because the copy sets no TTL) is a direct non-spec fix in `pkg/blobstore`. It is a prerequisite, because a fail-closed restore in the memory and filesystem postures, and any test that reads restored chunks back, fails without it. F-4.5.21 (replay omitted from the §4.5 lineage writer list) is a text-only correction that the restore does not depend on and that the proposal may carry as an optional rider.

## Evidence

- verified: §7.1 "Derive session semantics" says derive creates a session "pre-populated with the source session's workspace snapshot" (spec/07_session-lifecycle.md:87).
- verified: §7.1 "Derive copy semantics" describes a chunk-by-chunk copy plus a derived manifest row and does not itself state the starting workspace (spec/07_session-lifecycle.md:85).
- verified: §15.1 "Session Replay Semantics" says `prompt_history` populates the workspace from the sealed final snapshot or last checkpoint and that the replayed session "starts fresh", and says `workspace_derive` "starts with the source workspace" (spec/15_external-api-surface.md:770-771). The replay endpoint row states its purpose as re-running "using the same workspace and prompt history" (spec/15_external-api-surface.md:750).
- verified: §4.5 "Workspace lineage:" names only "uploads or a derived session" as sources of `parent_workspace_ref` (spec/04_system-components.md:332).
- verified: §19 Resolved Decision 12 defines derive as download, create, and upload as an `uploadArchive` source, performed atomically by `POST /v1/sessions/{id}/derive` (spec/19_resolved-decisions.md:18).
- verified: §4.7.1 describes `FinalizeWorkspace` only as "Validate, materialize to `/workspace/slots/{sessionId}/current`" (spec/04_system-components.md:670).
- verified: §4.7.9 step 5 states the PrepareWorkspace → FinalizeWorkspace → RunSetup → AssignCredentials → StartSession sequence (spec/04_system-components.md:911).
- verified: §14.1 "Path collision rule" orders entries only within one `sources` array (spec/14_workspace-plan-schema.md:338).
- verified: §7.4 archive limits are 256 MiB decompressed, 100:1 ratio, 10 000 entries, and 64 MiB per entry (spec/07_session-lifecycle.md:512-515). §10 gives a 512 MB workspace hard limit tier (spec/10_gateway-internals.md:123).
- verified: `FinalizeWorkspaceRequest` has fields 1 `session_id`, 2 `workspace_plan`, 3 `archive_policy`, 4 `mid_session`, reserved 5, and 6 `bind_attempt`, with no chunk, checkpoint, or snapshot field (schemas/lenny-adapter.proto:754-785). `restore_chunks` occurs nowhere in `spec/`, `schemas/`, or `pkg/`.
- verified: `ResumeRequest` carries `checkpoint_id` and repeated `ChunkGrant chunks` (schemas/lenny-adapter.proto:1395-1462).
- verified: `copyWorkspaceSnapshot` copies a chunked checkpoint through `deriveChunkedCheckpoint` and otherwise copies a legacy snapshot as one object, returning the destination URI (pkg/gateway/sessionserver/snapshot_copy.go:28-72).
- verified: derive and replay call `copyWorkspaceSnapshot`, fail closed, write `ParentWorkspaceRef`, and create the row in state `created` (pkg/gateway/sessionserver/derive.go:342-377, pkg/gateway/sessionserver/replay.go:185-208).
- verified: commit 3be845f0b's message says the finalize-time restore "needs an adapter contract change and is routed to a proposal (F-7.1.34 stays open)" and that it files F-4.5.24.
- verified: `grep -nE "WorkspaceSnapshot|ParentWorkspaceRef|resolveResumeChunks"` over `bind_request.go`, `finalize.go`, `start.go`, `pod_launch.go`, and `pod_claim.go` in `pkg/gateway/sessionserver` returns no hits.
- verified: `resolveResumeChunks` returns nil in three places with only logging (pkg/gateway/sessionserver/resume_checkpoint.go:30-73).
- verified: the adapter's `Resume` calls `restoreChunks` and then starts the runtime, and `restoreChunks` extracts into the session's checkpoint roots, `/workspace/slots/{sessionId}/current` and `/sessions/{sessionId}` (pkg/adapter/resume.go:120-130, 196-225).
- verified: `stageWorkspace` extracts every `uploadArchive` source in the gateway, rewrites it into file, directory, and symlink sources, reads upload bodies with `io.ReadAll`, and streams them over PrepareWorkspace before FinalizeWorkspace (pkg/gateway/podlifecycle/podsession/workspacestage.go:16-70).
- verified: `MemoryStore.Copy` sets `ExpiresAt: now.Add(dst.TTL)` (pkg/blobstore/blobstore.go:865-882).
- verified: derive and replay are registered public routes (pkg/gateway/sessionserver/route_table.go:60-61).
- verified: `proposal-status.mjs` reports proposal 0086 as Approved. The exact phrase "Runtime workspace defaults." occurs nowhere in `spec/`.
- verified (lead, from the lenses): BUILD-GAPS entries F-7.1.34, F-7.1.33, F-4.5.21, and F-4.5.24 exist and are open (BUILD-GAPS.md:7277-7289, 7270-7275, 1345, 1367-1372). F-7.1.34's text says replay copies no bytes, which 3be845f0b made stale.

## Who observes it

Any authenticated client that calls `POST /v1/sessions/{id}/derive` or `POST /v1/sessions/{id}/replay`. Both are public routes on the session mux. The agent running in the derived or replayed session observes the defect directly: its workspace holds only the bound plan's sources. Operators and clients comparing runtime versions through replay observe it as divergent results that have no visible cause.

## What breaks if nothing changes

- Derive does not deliver its documented contract. The new session does not start from the source workspace.
- Replay loses its main purpose, which is re-running identical inputs against a different runtime version. A replay compares runs on different filesystems.
- The failure is silent. Derive and replay return success with a ref to a real copied object, and the workspace download for the new session serves the copied bytes (pkg/gateway/sessionserver/snapshot_copy_test.go:86-91), while the pod holds a different tree. The platform's two views of the session's workspace disagree, and the symptom surfaces as wrong agent output.
- The copied snapshot is stored and billed against storage without being used.
- §19 Decision 12 and §7.1 continue to describe two different derive mechanisms.

## Findings this unblocks

- F-7.1.34 (High), restore half.
- F-7.1.33 (Medium), order between the restored snapshot and the bound plan's sources.

## Prior art considered

- Commit 3be845f0b landed the copy half and routed the restore to a proposal.
- §19 Resolved Decision 12 already names a delivery model, an `uploadArchive` source in the new session's WorkspacePlan. The gateway has the matching path in `stageWorkspace` and `rewriteExtractedSources`, and delegation export uses it to turn an archive into a child `uploadArchive` source (pkg/gateway/mcpfabric/delegation/export/export.go). This is Route B. The proposal weighs it against Route A rather than presenting Route A as the only option.
- The adapter's `workspace.Extract` is documented as the restore for a §4.4 checkpoint or a §7.1 snapshot and treats its input as trusted gateway output (pkg/adapter/workspace/extract.go:19-30), and the adapter rejects `uploadArchive` sources directly because the gateway extracts them (pkg/adapter/workspace/materialize.go:484-495).
- No other proposal stages this change. The `restoreChunks` mentions in proposals 0050, 0076, 0081, and 0092 concern the §7.3 resume path. Proposal 0086 defers snapshot and plan composition to F-7.1.33. Proposal 0106 touches `ParentWorkspaceRef` only for delegation-parent lineage.
- BUILD-GAPS F-7.1.34 suggests Route A and names the silent empty result and the legacy single-object snapshot as open points. The remediation plan routes F-7.1.34, F-7.1.33, F-4.5.21, and F-4.5.24 to this proposal.

## Validated premises

- Premise lens, verdict stands. P1, the specification says the copied snapshot is the starting workspace: confirmed. P2, the copy half exists and fails closed: confirmed. P3, derived and replayed rows are created in state `created` and bind through the ordinary paths: confirmed. P4, no bind path carries the snapshot: confirmed and load-bearing. P5, the adapter contract has no restore field: confirmed. P6, the resume path starts the runtime and cannot replace the bind sequence: confirmed. Settle items (a) and (b) are accurate. The lens added the restore-root issue, now item (c), and qualified "the spec names no mechanism" by pointing to §19 Decision 12.
- Evidence lens, verdict stands. Every cited specification passage, the proto fields, the copy code, the absence of snapshot references in the bind files, and the BUILD-GAPS entries were verified. Drift: §7.1 "Derive copy semantics" does not itself state the starting workspace, the bind sequence is written out in §4.7.9 step 5 rather than §7.1, and the original field list omitted `session_id`. None of these changes the substance.
- Prior-art lens, verdict revise. Confirmed that no other proposal stages the change and that the gap tracking matches. Refuted "the spec names no mechanism": §19 Decision 12 names the `uploadArchive` model, and the gateway staging path implements it for client uploads and delegation export. The statement now presents Route A and Route B as candidates and records the §19 and §7.1 contradiction. This lens also listed the costs of Route B (untrusted-upload validators, trusted adapter restore, and reassembly of a chunked snapshot).
- Scope lens, verdict revise. The restore is one problem, and items (a) and (b) are design decisions inside it. F-7.1.33 is split: the order against the bound plan's `sources` array stays in scope and is stated without citing 0086's text, which has not landed. The overlap with 0086's composer edit is recorded as a sequencing point. F-4.5.24 moves out of the settle list to a prerequisite direct fix. F-4.5.21 becomes an optional rider. The original statement fixed Route A before review, and it now lists Route A as one candidate. All of these are applied above.
- Impact lens, verdict stands. The routes are public, the client contract is explicit, the failure is silent, and the download view and the pod view of the workspace disagree. The lens noted a stale BUILD-GAPS lead about replay copying no bytes, which leaves the impact unchanged, and judged F-4.5.24 low-impact because MinIO ignores the URI TTL.
- Alternatives lens, verdict stands. No reading makes this a no-change problem, and no smaller upstream problem removes it. The lens raised Route B independently and argued that it fails on the 256 MiB archive ceiling against the 512 MB workspace limit, on symlink default-deny, and on full in-memory reads, so Route A remains the smaller option that works. The proposal still cites §19 Decision 12 and updates it, because after the fix that row would otherwise describe a different mechanism from the one the gateway uses. On F-4.5.24 this lens kept it in scope for tests, and the scope lens moved it to a prerequisite. The statement records it as a prerequisite direct fix, which satisfies both.
