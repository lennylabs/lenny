# Problem: Uploaded workspace content has no specified binding contract, and the finalize-time WorkspacePlan bind fills the gap without a specification

## Statement

The specification does not define a single, consistent contract for how content uploaded during a session's `created` window reaches the session workspace. It describes two uncoordinated models.

- The buffered-upload model. §7.1 steps 9-12 (`UploadWorkspaceContent(files, archives)`, then an argument-free `FinalizeWorkspace()`), §7.4 (pre-start uploads buffered in the Artifact Store and streamed into staging at finalize), §29.2, and docs/api/rest.md ("Moves uploaded files from the session's staging area") describe finalize materializing every buffered upload with no plan reference.
- The plan-reference model. The §14 `uploadFile` and `uploadArchive` sources place a "previously uploaded" file or archive named by `uploadRef`, and the §26.2 reference plan for `lenny session new --workspace` carries an `uploadArchive` source whose `uploadRef` is the resulting upload ID.

The plan-reference model has no conformant path under the specification as written. §14.1 **Envelope terminology.** carries a plan only in the create-time `CreateSessionRequest` (`POST /v1/sessions` and `POST /v1/sessions/start`). The upload endpoints are session-scoped and require the `uploadToken` issued at creation (§7.1, §7.4 **Upload authorization:**, the §15.1 upload precondition row). A create-time plan therefore cannot name content uploaded after creation. §26.2 contradicts itself on the ordering: its reference plan is a create envelope carrying `<upload_id>`, and its example says the CLI uploads the workspace and then creates the session. The specification also never states what an upload returns, what an `uploadRef` looks like, or how a plan's `uploadRef` is scoped, and the §15.1 endpoint and precondition tables omit `POST /v1/sessions/{id}/upload-archive`, which §7.1 step 10 and §18 name.

The tree implements only the plan-reference model. The binder fetches and materializes only uploads that a plan source names, so a blob uploaded without a referencing plan is never materialized, which diverges from §7.1 step 12 and §7.4. To make the plan-reference model reachable, commit 9fb5e2411 (F-24.17.4 / F-26.2.4, 2026-06-08) added an optional `{workspacePlan}` body on `POST /v1/sessions/{id}/finalize`. `resolveFinalizePlan` rejects a plan when the row already carries one (400 `VALIDATION_ERROR`, `details.reason: plan_already_set`), validates the plan through `resolvePlanForCreate` (so a finalize-bound plan can return `WORKSPACE_PLAN_INVALID`, `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED`, and the `gitClone` auth-binding errors, and has its `gitClone` refs resolved and pinned at finalize), applies the setup-command policy, and requires every `uploadRef` to parse as a `lenny-blob://` URI under the finalizing session's tenant and session (reasons `invalid_upload_ref` and `upload_ref_foreign_session`). `handleFinalize` writes the plan into the row in the `created` → `finalizing` compare-and-swap Update. No specification text, reference document, or OpenAPI operation defines this body. The specification contradicts it at each point it touches: §7.1 step 11 takes no argument, the §14 **`gitClone.ref` resolution (per-session immutability).** bullet and the §15.1 GET row pin refs "at session creation", the §15.1 finalize precondition row describes no body, and the `WORKSPACE_PLAN_INVALID` catalog row is scoped to the create endpoints. The code comments cite §7.1 step 11 as the canonical bind point, and that citation does not support the claim.

The proposal must first state the upload endpoint contract (the `/upload` and `/upload-archive` responses, the `uploadRef` format, and its tenant and session scoping) and add `/upload-archive` to the §15.1 tables. It must then choose one canonical binding model and carry it through every surface. One option adopts the finalize-time bind and specifies its request body, the no-existing-plan precondition and its error, the validation and `uploadRef` scoping it runs, its error set including the scope of `WORKSPACE_PLAN_INVALID`, when a late-bound plan's `gitClone` refs are pinned together with the corresponding §14 and §15.1 wording, and MCP `lenny/finalize_workspace` parity under the §15.2.1 REST/MCP consistency contract. The other option keeps the plan create-only, makes finalize materialize buffered uploads with placement metadata (`pathPrefix`, `format`, `stripComponents`) carried on the upload request, and retires the finalize body; under that option the §14 upload sources and the §26.2 reference plan are redefined or confined to content uploaded before the plan is bound. Whichever model is chosen, the proposal resolves the divergence on unreferenced uploads, states whether the `uploadRef` scope check applies to a create-time plan as well as a finalize-time one, and rewrites the §7.1 steps 9-12, §24.17 `session new` (whose transport column reads MCP `lenny/create_session` + stream), §26.2, and §19 decision 12 (manual derive: create, then upload the snapshot as an `uploadArchive` source) flows to match.

The following are out of scope. The pgstore persistence fix (commit c7609e923, an accepted deviation of proposal 0082) has landed; this proposal at most restates the §14.1 persistence obligation for a late-bound plan. Proposal 0085 (Draft) edits the §6.2 retry scope and the §15.1 `SETUP_COMMAND_FAILED` row, so the two proposals only need sequencing when both land §15.1 edits.

## Evidence

Spec:

- (verified) spec/07_session-lifecycle.md §7.1 steps 8-12: step 10 buffers uploads via `POST /upload` and `POST /upload-archive`, step 11 is `FinalizeWorkspace()` with no argument, and step 12 streams "the buffered upload content" into staging and materializes it.
- (verified) spec/07_session-lifecycle.md §7.4: pre-start uploads are buffered during `created` and streamed into staging at finalize; **Upload authorization:** requires the `uploadToken` issued at session creation on every upload and finalize request.
- (verified) spec/14_workspace-plan-schema.md sources catalogue: `uploadFile` (`path`, `uploadRef`) and `uploadArchive` (`pathPrefix`, `uploadRef`, `format`) place previously uploaded content; the §14 example plan carries `uploadRef` values.
- (verified) spec/14_workspace-plan-schema.md **`gitClone.ref` resolution (per-session immutability).**: refs resolve "At session creation"; a client-supplied `resolvedCommitSha` is rejected in the `CreateSessionRequest`.
- (verified) spec/14_workspace-plan-schema.md §14.1 **Envelope terminology.**: the `POST /v1/sessions` and `POST /v1/sessions/start` body is the `CreateSessionRequest` envelope carrying `workspacePlan`.
- (verified) spec/14_workspace-plan-schema.md §14.1 **Consumer obligations by consumer type.**: the gateway reads back the stored plan for resumed or retried sessions, and replays `resolvedCommitSha` "pinned at session creation".
- (verified) spec/15_external-api-surface.md §15.1 endpoint table: lists `/upload` and `/finalize` ("Finalize workspace and run setup"), no `/upload-archive`; the GET row says `resolvedCommitSha` is "pinned at session creation".
- (verified) spec/15_external-api-surface.md §15.1 **State-mutating endpoint preconditions.**: the finalize row describes materialization, setup commands, and credential-lease assignment with no request body; there is no `/upload-archive` row.
- (verified) spec/15_external-api-surface.md §15.1 error catalog: `WORKSPACE_PLAN_INVALID` is scoped to `POST /v1/sessions` (or `POST /v1/sessions/start`); `VALIDATION_ERROR` is generic. No text under spec/ or docs/ defines `plan_already_set`, `upload_ref_foreign_session`, or `invalid_upload_ref`.
- (verified) `uploadRef` occurs in spec/ only in §14 and in the §26.2 reference plan; no text defines an upload response.
- (verified) spec/24_lenny-ctl-command-reference.md §24.17: `lenny session new` transport is "MCP `lenny/create_session` + stream".
- (verified) spec/26_reference-runtime-catalog.md §26.2 **Reference `WorkspacePlan`.**: a create envelope (`pool`, `workspacePlan`, `env`, `timeouts`, `runtimeOptions`) with `"uploadRef": "<upload_id>"`; the example text says the CLI uploads the workspace and then creates the session via the REST API.
- (verified) spec/19_resolved-decisions.md decision 12: the manual derive recipe creates a session and then uploads the snapshot as an `uploadArchive` source in the new session's plan.
- (verified) spec/12_storage-architecture.md §12.5: object keys follow `/{tenant_id}/{object_type}/{session_id}/{filename}` with tenant-prefix validation, the rule `validateFinalizeUploadRefs` cites.
- (verified) spec/18_build-sequence.md names `POST /v1/sessions/{id}/upload-archive` handlers.

Code:

- (verified) pkg/gateway/sessionserver/finalize.go:30-39 (`finalizeRequest{workspacePlan}`), 55-96 (`resolveFinalizePlan`: `plan_already_set`, `resolvePlanForCreate`, `enforceSetupCommandPolicy`, `validateFinalizeUploadRefs`), 99-119 (1 MiB body cap, `PAYLOAD_TOO_LARGE`, malformed JSON as `VALIDATION_ERROR`), 121-160 (`invalid_upload_ref`, `upload_ref_foreign_session`), 330-344 (`storedWorkspacePlanForFinalize`: no finalize plan and no create plan yields an empty workspace).
- (verified) pkg/gateway/sessionserver/sessionserver.go:3114-3141: `handleFinalize` calls `resolveFinalizePlan` on the pre-lock row and writes `row.WorkspacePlan = planJSON` in the guarded `created` → `finalizing` Update.
- (verified) pkg/gateway/sessionserver/start.go `resolvePlanForCreate` (called from create.go, start.go, and finalize.go): runs `workspaceplan.Parse`, `checkGitCloneAuthBindings`, and `PinCommitSHAs`.
- (verified) pkg/gateway/sessionserver/upload.go:54-58, 344-352: `UploadResponse.UploadRef` is a `lenny-blob://` URI built from the tenant, the session's `row.ID`, and a random `NewPartID()` (64 random bits); the comment says clients pass it "in a subsequent finalize call".
- (verified) pkg/gateway/podlifecycle/podsession/binder.go:1358-1400, 1636-1650: `stageWorkspace` fetches only plan-referenced `uploadRef`s through `b.Blobs.Get`, and stages only when uploads exist.
- (verified) cmd/lenny-gateway/stores.go: the binder's `Blobs` is the gateway's `blobs` store rather than a `blobstore.TenantScoped` wrapper.
- (verified) pkg/workspaceplan/plan.go `validateUploadFile` and `validateUploadArchive` check only that `uploadRef` is non-empty; `validateFinalizeUploadRefs` has no caller outside finalize.go.
- (verified) pkg/gateway/session/sessionstore/pgstore/pgstore.go: `Update` writes `workspace_plan = $55::jsonb`.
- (verified) sdks/client/go/lenny/upload.go:112-134: `FinalizeWorkspace(ctx, id, plan)` posts `{workspacePlan}`; its doc comment says materialization happens "when the session starts", which is stale because materialization runs at finalize.
- (verified) pkg/embedded/localcli/session.go:297, 341-399: without a workspace the CLI uses MCP `CreateSession`; with `--workspace`/`--file` it runs REST create, `UploadArchive`/`UploadFile`, `FinalizeWorkspace(plan)`, then `Start`.
- (verified) pkg/gateway/mcpfabric/playground/ui/app.js:592-670: the playground posts to `/upload-archive` and then to `/finalize` with `{ workspacePlan: plan }`.
- (verified) pkg/gateway/mcpfabric/mcptools/client_tools.go:60-72, 102, 294-316: `lenny/finalize_workspace` is a `postByID` tool whose `extractSessionID` strips `sessionId` and forwards every other argument as the REST body.
- (verified) sdks/client/python/lenny/client.py `finalize` and sdks/client/typescript/src/client.ts `finalize` send no body.

Published surfaces:

- (verified) pkg/gateway/externalapi/openapi/openapi.json `/v1/sessions/{id}/finalize`: no `requestBody`; responses 200 and 409 only. `/upload` and `/upload-archive` operations exist with request bodies. schemas/ holds no OpenAPI document.
- (verified) docs/api/rest.md `POST /v1/sessions/{id}/finalize`: no body; key errors `RESOURCE_NOT_FOUND` and `INVALID_STATE_TRANSITION` only.
- (verified) docs/api/mcp.md `finalize_workspace`: input schema is `sessionId` only.

Records:

- (verified) Commit 9fb5e2411 (2026-06-08, "F-24.17.4/F-26.2.4: workspace upload binding (finalize-with-plan + CLI)") changed BUILD-GAPS.md, pkg/embedded/localcli, pkg/gateway/sessionserver, and pkg/workspacepack, and nothing under spec/, docs/, or the OpenAPI document.
- (verified) Commit c7609e923 (2026-09-28 00:38 UTC) made pgstore `Update` persist `workspace_plan`; proposal 0082 (Implemented) records it as an accepted deviation.
- (verified) BUILD-GAPS.md F-24.17.4, F-26.2.4, and F-27.4.3 are CLOSED on the finalize-with-plan path.
- (verified) Proposal 0085 is Draft.

## Who observes it

- Clients of the Go SDK that call `FinalizeWorkspace(plan)`, users of `lenny session new --workspace` or `--file`, and users of the web playground upload. All three shipped consumers depend on a finalize body that no published contract defines.
- Clients built from the published contract. The OpenAPI document, docs/api/rest.md, docs/api/mcp.md, and the Python and TypeScript SDKs describe no finalize body and no `uploadRef` upload response, so those clients cannot stage uploaded content into a workspace. Only the in-tree Go and JavaScript consumers can.
- MCP callers. `lenny/finalize_workspace` advertises only `sessionId` but forwards extra arguments as the REST body, so an MCP caller can bind a plan through an undocumented argument.
- Implementers and reviewers working from the specification. §7.1, §7.4, §14, §24.17, and §26.2 describe incompatible flows, and the code comments cite §7.1 step 11 for behavior that section does not define.
- Tests. finalize_plan_internal_test.go pins `plan_already_set`, foreign-session and foreign-tenant refs, and malformed bodies to spec sections that do not define them.

## What breaks if nothing changes

- A conformance pass that aligns the code to the current specification (finalize takes no body) breaks the SDK, the reference CLI entry point that §26.2 names, and the playground.
- The §14 `uploadFile` and `uploadArchive` sources and the §26.2 reference CLI flow remain unimplementable on any specification-defined path.
- Uploads made without a referencing plan are silently never materialized, contrary to §7.1 step 12 and §7.4, and nothing reports it to the client.
- The `uploadRef` tenant and session scope check runs only on a finalize-bound plan. A create-time plan's `uploadRef` is checked for presence only, and the binder fetches it through the unwrapped blob store. A create-time plan can therefore name a blob staged for another session of the same tenant. A cross-tenant reference needs the other tenant's session ID and a 64-bit random part ID, so that exposure is implausible without a leaked URI; it has not been demonstrated.
- A late-bound plan's `gitClone` refs are pinned at finalize while §14 and the §15.1 GET row say "at session creation". A client observes the difference only when a ref moves during the `created` window, and no in-tree finalize-plan producer emits a `gitClone` source, so the impact is low.
- BUILD-GAPS records F-24.17.4, F-26.2.4, and F-27.4.3 as closed, so the audit trail treats the gap as resolved, and nothing in the tree flags the missing contract.

## Findings this unblocks

No open BUILD-GAPS.md or TEST-GAPS.md finding covers the missing contract. F-24.17.4, F-26.2.4, and F-27.4.3 are closed on the code-only fix. TEST-GAPS T-7.4.12 (SDK upload conformance placeholder) and the §14.13 SDK entry cover SDK test coverage only, and a specified upload and binding contract gives them a definition to test against.

## Prior art considered

- No proposal under proposals/ stages a finalize-time plan bind or an alternative deferred-upload mechanism.
- Proposal 0007 (Implemented) describes `handleFinalize` keeping "its plan-binding and uploadRef validation" and stages no specification text for it.
- Proposal 0083 (Implemented) records the undefined finalize-body plan and the `WORKSPACE_PLAN_INVALID` emission at `/finalize` as out-of-scope observations in its review log and does not resolve them.
- Proposal 0082 (Implemented) fixed only the pgstore persistence of `workspace_plan`, as an accepted deviation.
- Proposal 0085 (Draft) does not mention `workspacePlan`; its §15.1 edit targets the `SETUP_COMMAND_FAILED` row.
- Commit 9fb5e2411 landed the finalize bind as code ahead of the specification. Before it, the BUILD-GAPS deferral called the binding "a gateway design decision outside this client-facing finding's scope". No later commit touching spec/, docs/, or the OpenAPI document mentions a finalize-body plan.
- The specification's own buffered-upload model (§7.1 steps 9-12, §7.4, §29.2) is existing prior art for post-create uploads reaching the workspace without a plan reference. The tree does not implement it. It is the basis of the create-only alternative in the statement.

## Validated premises

Premise lens (verdict: stands).

- CONFIRMED: the code accepts and binds a finalize-body plan with the `plan_already_set` guard, create-path validation, setup-command policy, and `uploadRef` scope check. Verified in finalize.go and sessionserver.go.
- CONFIRMED: no specification text defines a finalize body or a finalize-time `workspacePlan`; §7.1 step 11, the §15.1 finalize rows, and the §15.2 MCP row describe none.
- CONFIRMED: the specification places the plan and its `gitClone` resolution at creation only (§14.1 envelope terminology and gateway validation, the §14 `gitClone.ref` bullet, the §15.1 GET row, the §14.1 replay bullet, and the `WORKSPACE_PLAN_INVALID` scope).
- CONFIRMED: the finalize path emits `WORKSPACE_PLAN_INVALID` and the `gitClone` auth and ref-resolve errors, and pins refs at finalize, through `resolvePlanForCreate`.
- CONFIRMED: `uploadRef` is minted only against an existing session. Evidence and prior-art lenses add that this is a code fact, because the specification defines no upload response, and that §26.2 itself contradicts the ordering.
- CONFIRMED: the SDK and CLI depend on the finalize-time bind; the CLI's REST flow diverges from §24.17; the SDK doc comment about materialization timing is stale.
- ADDED: the MCP `lenny/finalize_workspace` tool forwards a `workspacePlan` argument to `/finalize` through `extractSessionID`.
- CORRECTED: the OpenAPI document is pkg/gateway/externalapi/openapi/openapi.json; schemas/ holds none. The original statement pointed at schemas/.
- CONFIRMED: commit 9fb5e2411 introduced the bind; commit c7609e923 made pgstore persist the plan; BUILD-GAPS closes F-24.17.4 and F-26.2.4 on it.
- CONFIRMED: §14.1 requires the plan to persist and be replayed on resume, and resume, launch, and delegated-child materialization re-read the stored plan.
- ADDED: the `uploadRef` scope check exists only on the finalize path.
- CONFIRMED: the `plan_already_set` check reads the pre-lock row without a locked re-check. This is safe because the plan is written only at create or in the single admitted `created` → `finalizing` compare-and-swap.

Evidence lens (verdict: revise).

- VERIFIED: §14.1 envelope terminology, the §14 `gitClone.ref` bullet, §14.1 consumer obligations, the §15.1 GET, finalize, and error-catalog rows, and the absence of the reason codes from spec/ and docs/.
- VERIFIED: §7.1 step 11 takes no argument, so the code's citation of it as the canonical bind point is unsupported.
- VERIFIED: commit 9fb5e2411 added the finalize body and its guards; `resolvePlanForCreate` pins `gitClone` refs at finalize.
- DRIFTED: the ordering premise is half specification and half code. The specification requires the creation-issued `uploadToken` and never defines the upload response or `uploadRef` format, and omits `/upload-archive` from the §15.1 tables. Carried into the statement.
- VERIFIED with nuance: §26.2 presents the upload-referencing plan as a create body, so the ordering tension is internal to the specification; §24.17 maps `session new` to MCP create.
- VERIFIED: the SDK and CLI consumers; the playground (F-27.4.3) is an additional consumer.
- DRIFTED: commit c7609e923 is dated 2026-09-28 UTC; the original statement said 2026-09-27. Corrected.
- FALSE location: no OpenAPI document exists under schemas/. Corrected to pkg/gateway/externalapi/openapi/openapi.json.
- DRIFTED: proposal 0085's §15.1 edit targets the `SETUP_COMMAND_FAILED` row, so its overlap with a finalize-row edit is indirect. Carried into the out-of-scope paragraph.
- VERIFIED: §12.5 fixes the tenant and session object key layout that `validateFinalizeUploadRefs` cites.

Prior-art lens (verdict: revise).

- No proposal stages a finalize-time bind or alternative; proposals 0007, 0083, 0082, and 0085 touch it only as described under Prior art considered.
- Commit 9fb5e2411 touched no specification, documentation, or OpenAPI file, confirming no existing specification surface covers the bind.
- The specification's buffered-upload model in §7.1, §7.4, and §29.2 is a named alternative that the original statement omitted. Carried into the statement.
- The ordering contradiction is internal to the specification (§14 "previously uploaded", the §14 example, and §26.2), and the `uploadRef` minting is code-only.
- The documentation, OpenAPI, and MCP tool reference define no finalize body, while the MCP tool passes extra arguments through.
- The playground SPA is a consumer the original statement did not name.
- No open BUILD-GAPS or TEST-GAPS finding covers the missing contract.

Scope lens (verdict: revise).

- The decision items all follow from one choice of binding model and belong in one proposal.
- The framing was too narrow: the defect is the missing upload-to-workspace binding model, and the unreferenced-upload divergence belongs inside the proposal. Carried into the statement.
- MCP `finalize_workspace` parity under §15.2.1 is a decision item rather than a documentation check. Carried into the statement.
- The OpenAPI pointer was wrong. Corrected.
- Adding `/upload-archive` to the §15.1 tables is in scope because the plan-reference flow runs through it. Carried into the statement.
- The §24.17 transport discrepancy is in scope. Carried into the statement.
- The pgstore fix and proposal 0085 are context and out of scope apart from §15.1 sequencing. Carried into the statement.

Impact lens (verdict: revise).

- Three shipped consumers depend on the bind (SDK, CLI, playground); a conformance pass toward the current specification breaks all three.
- The §14 upload sources and the §26.2 flow have no conformant path under the specification alone.
- Third-party surfaces (OpenAPI, REST and MCP docs, Python and TypeScript SDKs) omit the finalize body, so only in-tree Go and JavaScript consumers can stage uploaded content.
- The `uploadRef` scope check runs only at finalize; a create-time plan's `uploadRef` is fetched through the unwrapped blob store. Verification here: the binder is wired with the plain `blobs` store, and part IDs carry 64 random bits, so same-tenant cross-session reference is reachable and cross-tenant reference is implausible without a leaked URI. Carried into What breaks.
- The `gitClone` pinning-time discrepancy is real and low impact.
- The persistence premise holds on Postgres after commit c7609e923.
- Tests pin the unspecified behavior to sections that do not define it, and BUILD-GAPS treats the gap as closed.

Alternatives lens (verdict: revise).

- No reading of the current specification makes a change unnecessary.
- The finalize body is one candidate resolution of a larger defect: the choice between the buffered-upload and plan-reference models comes first. Carried into the statement.
- The upload endpoint contract (response, `uploadRef` format, scoping, and the `/upload-archive` rows) is a prerequisite. Carried into the statement.
- The code materializes only plan-referenced uploads, diverging from §7.1 step 12 and rest.md. Carried into the statement.
- A create-only alternative (finalize materializes buffered uploads with placement metadata on the upload request) retires the finalize body and leaves the "at session creation" wording, the `WORKSPACE_PLAN_INVALID` scope, and the argument-free MCP tool unchanged, at the cost of redefining the §14 upload sources and the §26.2 reference plan. Carried into the statement as the second option.
- Adopting the finalize bind widens scope to MCP parity, the playground, and the §19 decision 12 derive recipe. Carried into the statement.
- The premises about commits 9fb5e2411 and c7609e923 and finalize-time pinning hold.

No premise the proposal depends on was refuted. The refutations and corrections were the OpenAPI location under schemas/, the date of commit c7609e923, the characterization of `uploadRef` minting as a specification fact, and the narrow framing around the finalize body alone.
