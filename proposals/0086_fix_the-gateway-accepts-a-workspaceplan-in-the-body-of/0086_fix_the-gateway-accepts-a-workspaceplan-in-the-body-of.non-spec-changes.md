# Non-spec changes: Finalize-time WorkspacePlan bind has no specification contract

These changes are staged. Applying them is the implementation's work, under the
implementation checklist.

## Design (implementation-facing)

The finalize-time bind already runs in the tree, so most of the non-spec work aligns published surfaces, comments, and tests with the amended spec. CODE-1 and CODE-6 change production behavior:

- CODE-1 moves the `uploadRef` scope check into the shared plan resolver, so it runs on every client-submitted plan at every bind point. A create-time or create-and-start plan that names an upload source is now rejected. That path is a tenant-isolation path, which is why TEST-1 reaches tier 9.
- CODE-2 declares the `workspacePlan` argument the MCP `lenny/finalize_workspace` tool already forwards.
- CODE-3 publishes the finalize request body and its plan-bind responses in the OpenAPI document, and corrects the `uploadRef` and plan-binding descriptions in the OpenAPI document and the published `WorkspacePlan` schema.
- CODE-4 corrects SDK and gateway comments and metric descriptions that the amended spec makes false.
- CODE-5 corrects the adapter proto and setup comments that describe a once-per-pod setup gate or tie setup to pod startup. Neither bind path has such a gate, so no production behavior changes.
- CODE-6 composes the runtime `workspaceDefaults` into the plan at every binding request and checks them at registration (SPEC-8).
- DOCS-1 rewrites the reader-facing upload and finalize documentation, the runtime author guide's setup-timing and `workspaceDefaults` text, and the metrics pages' `setup_command_failed` description.
- TEST-1 pins each new spec statement and the CODE-1 behavior change.
- TEST-2 pins SPEC-8 and CODE-6.

Locate every code site by symbol and quoted text. The normative rules are in the spec-changes file: the `uploadRef` rule is SPEC-1's field note, and the binding rules are SPEC-2's **Plan binding.** paragraph. The setup-timing rule is SPEC-7 edit (a), and the runtime-defaults rules are SPEC-8's **Runtime workspace defaults.** paragraph. The deliverables below implement or cite those homes.

## Staged code changes

### CODE-1 · one `uploadRef` scope check in the shared plan resolver

Targets: `pkg/gateway/sessionserver/workspace_plan.go` (`resolvePlanForCreate`), `pkg/gateway/sessionserver/create_and_start.go` (its caller in the create-and-start path), `pkg/gateway/sessionserver/create.go` (the `resolvePlanForCreate` call in `validateAndBuildCreateRow`), `pkg/gateway/sessionserver/finalize.go` (`finalizeRequest`, `resolveFinalizePlan`, `validateFinalizeUploadRefs`), and `pkg/gateway/sessionserver/finalize_handler.go` (the comment block above the `resolveFinalizePlan` call in `handleFinalize`).

1. Rename `resolvePlanForCreate` to `resolveSubmittedPlan` and give it this signature:

   ```go
   func (s *Server) resolveSubmittedPlan(w http.ResponseWriter, r *http.Request, rawPlan json.RawMessage, tenantID, bindSessionID string) (
   	plan workspaceplan.Plan, storedJSON json.RawMessage, warnings []workspaceplan.Warning, ok bool,
   )
   ```

2. Inside it, call `s.validatePlanUploadRefs(w, tenantID, bindSessionID, parsed)` immediately after `workspaceplan.Parse` succeeds, and before `checkGitCloneAuthBindings` and `workspaceplan.PinCommitSHAs`. A foreign ref therefore fails before any `ls-remote` runs.
3. Rename `validateFinalizeUploadRefs` to `validatePlanUploadRefs`. Keep its body: a `blobstore.ParseURI` failure writes `400 VALIDATION_ERROR` with `details.reason = "invalid_upload_ref"`, and a tenant or session mismatch writes `400 VALIDATION_ERROR` with `details.reason = "upload_ref_foreign_session"`. Both carry `details.field` from `sourceUploadRefField`. Add one guard after `ParseURI` succeeds and before the tenant and session comparison: when `bindSessionID` is empty, write `upload_ref_foreign_session` for that source, whatever the parsed URI's session segment holds. A ref that fails `ParseURI` still returns `invalid_upload_ref`. The guard keeps the create path fail-closed if `ParseURI` ever admits an empty session segment.
4. Callers:
   - `validateAndBuildCreateRow` in `create.go` passes `(tenantID, "")`.
   - The create-and-start build in `create_and_start.go` passes `(tenantID, "")`.
   - `resolveFinalizePlan` passes `(tenantID, row.ID)` and deletes its own `validateFinalizeUploadRefs` call. The `plan_already_set` guard stays before the resolver call, and the `enforceSetupCommandPolicy` call stays after it.
   - `tenantID` is already in scope at each call site.
5. Doc comments:
   - `resolveSubmittedPlan`: state that `bindSessionID` is the ID of the session the plan binds to, and is empty for a plan bound at creation, because the session ID is minted after plan validation and no upload for the session can exist yet. Cite `// spec: §14 Workspace Plan Schema (uploadRef field note); §14.1 WorkspacePlan Schema Versioning (Plan binding)`.
   - `validatePlanUploadRefs`: replace "parses each uploadRef as a §4.5 lenny-blob:// URI" with a statement that the gateway reads its own `lenny-blob://` encoding while clients treat the value as opaque. Cite the same two headings and keep `§12.5`.
   - `finalizeRequest`, `resolveFinalizePlan`, and the `handleFinalize` comment block: retarget every "§7.1 step 11" bind-point citation to `§14.1 WorkspacePlan Schema Versioning (Plan binding); §7.1 Normal Flow`, and delete the prose that argues why the create-time plan cannot name an upload. The spec now carries that reasoning.
   - The `// spec: §12.5 / §13.4` comment above the deleted call goes with it.
6. Run `gofumpt` and `goimports`.

Delegation child plans do not pass through the resolver, and CODE-1 does not route them through it.

### CODE-2 · `lenny/finalize_workspace` declares `workspacePlan`

Target: `pkg/gateway/mcpfabric/mcptools/client_tools.go`, the `postByID` closure and the `lenny/finalize_workspace` registration.

1. Add an `inputSchema json.RawMessage` parameter to `postByID`, placed after `subpath`, and use it for `mcp.Tool.InputSchema`.
2. Every other `postByID` caller passes `sessionIDInputSchema`.
3. `lenny/finalize_workspace` passes a new package-level `finalizeWorkspaceInputSchema`:

   ```go
   // finalizeWorkspaceInputSchema declares the optional workspacePlan that
   // lenny/finalize_workspace forwards as the REST finalize body.
   // spec: §15.2.1 REST/MCP Consistency Contract; §14.1 WorkspacePlan Schema
   // Versioning (Plan binding).
   var finalizeWorkspaceInputSchema = json.RawMessage(`{"type":"object","required":["sessionId"],"properties":{"sessionId":{"type":"string","description":"The target session id."},"workspacePlan":{"type":["object","null"],"additionalProperties":true,"description":"Optional §14 WorkspacePlan bound at finalize when the session was created without one."}}}`)
   ```

4. Change the `lenny/finalize_workspace` description to "Seal the session workspace, binding an optional workspace plan, and run setup."

The handler and `extractSessionID` stay unchanged, so forwarding behavior does not change.

### CODE-3 · OpenAPI finalize request body and plan-bind responses; opaque `uploadRef` descriptions

Targets: `pkg/gateway/externalapi/openapi/openapi.json` and `schemas/workspaceplan-v1.json`. The OpenAPI document is hand-maintained for session routes; edit the JSON directly.

1. `paths./v1/sessions/{id}/finalize.post`: add

   ```json
   "requestBody": {"required": false, "content": {"application/json": {"schema": {"$ref": "#/components/schemas/FinalizeRequest"}}}}
   ```

2. Add `components.schemas.FinalizeRequest`:

   ```json
   "FinalizeRequest": {"type": "object", "properties": {"workspacePlan": {"type": ["object", "null"], "additionalProperties": true, "description": "WorkspacePlan bound at finalize when the session's CreateSessionRequest carried none (§14.1 Plan binding)."}}}
   ```

   Name the component `FinalizeRequest`, parallel to `DeriveRequest`. `FinalizeWorkspaceRequest` is the adapter proto message in `schemas/lenny-adapter.proto` and must not be reused on this boundary. Leave the component's top-level `additionalProperties` unset.

3. Add these responses to the finalize operation, each with `content.application/json.schema` set to `{"$ref": "#/components/schemas/Error"}` as `POST /v1/sessions` does. Keep the existing `200` and `409`.

   | Status | `description` |
   |:--|:--|
   | `400` | `VALIDATION_ERROR (plan_already_set, invalid_upload_ref, upload_ref_foreign_session, malformed body) / WORKSPACE_PLAN_INVALID` |
   | `422` | `GIT_CLONE_AUTH_UNSUPPORTED_HOST / GIT_CLONE_AUTH_HOST_AMBIGUOUS / GIT_CLONE_REF_UNRESOLVABLE` |
   | `503` | `GIT_CLONE_REF_RESOLVE_TRANSIENT` |

   Do not list `SETUP_COMMAND_FAILED`, `SESSION_CREATION_FAILED`, `CREDENTIAL_POOL_EXHAUSTED`, or `UPLOAD_ARCHIVE_LIMIT_EXCEEDED`. They are prepare-phase outcomes that arise whether or not a body is sent, and the finalize prepare-phase envelope belongs to §15.1.

4. `components.schemas.UploadResponse.properties.uploadRef.description` becomes "Opaque reference to the stored upload. Pass it verbatim as the uploadRef of an uploadFile or uploadArchive source in a plan bound to this session."
5. `schemas/workspaceplan-v1.json`: replace the `uploadFile.uploadRef` description "Reference to a previously uploaded blob (lenny-blob:// URI or upload id)." with "Opaque uploadRef returned by POST /v1/sessions/{id}/upload or /upload-archive for the session the plan binds to." Add the same `description` to `uploadArchive.uploadRef`, which has none today. In the `gitClone.ref` description, replace "at session creation" with "when the plan binds". In the top-level description, replace "current at pod claim time. Nested under CreateSessionRequest.workspacePlan." with "current. Carried in CreateSessionRequest.workspacePlan or the finalize request body's workspacePlan." Change nothing else in the schema.

### CODE-4 · stale SDK and gateway comments and metric descriptions

These edits change comments and metric description strings and no behavior. Run `gofumpt` and `goimports`.

- **`sdks/client/go/lenny/upload.go`, `FinalizeWorkspace` doc comment.** Replace "binding the plan onto a created session and transitioning it to ready" and the sentence "the gateway materializes them when the session starts" so the comment reads: "...binding the plan onto a created session (§14.1 Plan binding). The gateway validates the plan and, during finalize, materializes its sources, including the uploads it names; the call returns once the session is ready." Change "(via UploadArchive)" to "(via UploadArchive or UploadFile)". The citation line becomes `spec: §14.1 WorkspacePlan Schema Versioning (Plan binding); §7.1 Normal Flow; §26.2`.
- **`sdks/client/go/lenny/upload.go`, `UploadResult` doc comment.** Replace the `§4.5 lenny-blob:// URI` claim with: "UploadRef is the opaque reference the caller passes verbatim as sources[<n>].uploadRef in a plan bound to this same session at finalize." Cite `spec: §14 Workspace Plan Schema (uploadRef field note); §15.1`.
- **`pkg/gateway/sessionserver/upload.go`, `UploadResponse.UploadRef` comment.** Keep the statement that the gateway emits a `lenny-blob://` URI, which is implementation detail. Drop the `§4.5` attribution. State that clients treat the value as opaque and that it is valid only in a plan bound to the uploading session, which `validatePlanUploadRefs` enforces. Replace "in a subsequent finalize call" with "in a plan bound to this session". Cite `spec: §14 Workspace Plan Schema (uploadRef field note); §14.1 WorkspacePlan Schema Versioning (Plan binding)`.
- **`pkg/embedded/localcli/session.go`, `createSessionWithWorkspace` doc comment.** The prose is accurate. Append `§14.1 (Plan binding)` to its existing spec line and leave the prose unchanged.
- **`pkg/gateway/runtime/sdkwarm/matcher.go`, package doc comment.** Replace the quoted sentence from `§6.1 — "if the workspace plan` through `DemoteSDK RPC".` with `§6.1 What a Pre-Warmed Pod Looks Like (Demotion on demand); §14.1 WorkspacePlan Schema Versioning (Plan binding).` Keep the sentence that begins "Patterns follow Go path.Match".
- **`lenny_warmpool_sdk_demotions_total` description.** Delete " before session assignment" from the metric's `Help` string and from the comment above it in `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_sessionlifecycle.go`, and from its entry in `pkg/observability/metrics/catalog.go` (SPEC-2 edit (e) item 6).

### CODE-5 · adapter proto and setup comments

Comment-only changes. The rule is SPEC-7 edit (a).

- **`schemas/lenny-adapter.proto`, `WorkspacePlan` comment.** Replace "RunSetup RPC, which §5.2 specifies fires once per pod at start (a recycled pod skips the second RunSetup on its subsequent session assignments; the gateway owns the once-per-pod gate). See also F-5.2.24." with "RunSetup RPC. spec: §7.5 Setup Commands."
- **`schemas/lenny-adapter.proto`, `RunSetupRequest` comment.** Replace the paragraph from "Per §5.2 setup commands run once per pod at start." through "(§5.2, Runtime-process lifetime)." with "The adapter keeps no pod-level setup-completion state: it runs the commands of every RunSetup it receives. spec: §7.5 Setup Commands."
- **`schemas/lenny-adapter.proto`, `SetupPolicy.on_timeout` comment.** Replace the words `"fail" aborts pod startup,`, which wrap across two comment lines, with `"fail" fails the session's setup (spec: §7.5 Setup Commands; §6.2 Pod State Machine),` and re-wrap the comment.
- Run `make generate-proto` so `pkg/proto/adapter/v1/lenny-adapter.pb.go` carries the corrected comments. Leave the `lenny-adapter.proto` fixtures under `scripts/specshift/testdata/` unchanged.
- **`pkg/adapter/workspace/setup.go`, `RunSetup` doc comment.** Replace "The §4.7 adapter calls RunSetup from StartSession after the workspace is materialized and before the runtime binary is launched." with "The adapter's RunSetup RPC handler calls it after FinalizeWorkspace has materialized the session's slot and before StartSession. spec: §7.5 Setup Commands."

### CODE-6 · runtime `workspaceDefaults` composed into the bound plan

Implements SPEC-8 edit (a) and the registration check of SPEC-8 edit (b). The rules are SPEC-8's. This deliverable names the sites.

1. **Composer.** New file `pkg/gateway/runtime/runtimestore/workspace_defaults.go`. `runtimestore` gains an import of `pkg/workspaceplan`, which imports no Lenny package.

   ```go
   // spec: §14.1 WorkspacePlan Schema Versioning (Runtime workspace defaults); §5.1 Derived Runtime Instantiation
   func (w *WorkspaceDefaults) ComposeInto(plan workspaceplan.Plan) (workspaceplan.Plan, bool, error)
   func ComposeWorkspaceDefaults(ctx context.Context, s Store, runtimeRef string, plan workspaceplan.Plan) (workspaceplan.Plan, bool, error)
   ```

   - `ComposeInto` returns `(plan, false, nil)` for a nil receiver or for a block with no files and no setup commands. Otherwise:
     - It returns an error naming `files[<i>].ref` for an entry with a non-empty `Ref`.
     - It builds one `inlineFile` source per file, with `Raw` set to `type`, `path`, and `content` so that `workspaceplan.Marshal` emits it.
     - It places those sources and the converted setup commands ahead of `plan`'s own.
     - It sets `SchemaVersion` to `workspaceplan.SchemaVersion` when `plan`'s is zero.
     - It returns `workspaceplan.ParseStored` of the marshalled result with `true`, and wraps a parse error.
   - `ComposeWorkspaceDefaults` returns `(plan, false, nil)` when `s` is nil, `runtimeRef` is empty, or `Resolve` returns an error for which `errors.Is(err, ErrNotFound)` holds. It returns any other `Resolve` error. Otherwise it returns `rt.WorkspaceDefaults.ComposeInto(plan)`.
   - Update the `WorkspaceFile.Ref` doc comment: registration rejects it, and `ComposeInto` fails on a row stored before that check.
2. **Session-server binding requests** (`pkg/gateway/sessionserver`).
   - Each site calls `runtimestore.ComposeWorkspaceDefaults(r.Context(), s.runtimes, row.RuntimeRef, parsed)` once the session's runtime is final, and leaves its `enforceSetupCommandPolicy` call where it is. When the call reports `true`, the site replaces the stored JSON with `workspaceplan.Marshal` of the result.
   - An error writes `500 INTERNAL_ERROR` and returns before any write.
   - `validateAndBuildCreateRow` (`create.go`) composes after its `routeExperiment` call, which can set `row.RuntimeRef` to a variant's runtime, and only when `req.WorkspacePlan` is non-empty and not `null`. `createBuild.parsedPlan` carries the result.
   - The create-and-start build (`create_and_start.go`) always composes, after the `routeExperiment` call and the priority-300-and-above PreRoute range that follows it, both of which can rewrite `row.RuntimeRef`, and before the build returns. `build.parsedPlan` carries the result, so `claimAtCreate` and `startOnPod` receive it.
   - Reorder `resolveFinalizePlan` (`finalize.go`):
     - A row that already has a plan returns before resolution, with `plan_already_set` when the body carries a plan and with no binding otherwise.
     - For a row without a plan, the function resolves the body plan (the zero plan when the body is absent or `null`), enforces the policy, composes, and returns `hasPlan = len(storedJSON) > 0`.
     - `handleFinalize` and `storedWorkspacePlanForFinalize` stay unchanged.
3. **Delegated child.** In `buildChildSession` (`pkg/gateway/mcpfabric/delegation/service.go`), whose child row literal carries the `childPlan` that the `if len(req.FileExport) > 0` block sets:
   - After the `ApplyExperimentRouting` block, which can set `child.RuntimeRef` to a variant's runtime, parse `child.WorkspacePlan` with `workspaceplan.ParseStored`, using the zero plan when it is nil.
   - Call `runtimestore.ComposeWorkspaceDefaults(ctx, s.runtimes, child.RuntimeRef, ...)`. When it reports `true`, set `child.WorkspacePlan` to the marshalled result before `buildChildSession` returns.
   - An error returns through `retErr`, so the deferred `undoChildExport` runs.
4. **Readers.** `storedWorkspacePlan`, `storedWorkspacePlanForFinalize`, `exclusiveBindRequest`, `slotBindRequest`, `launchOnPod`, `resumeOnPod`, and `MaterializeDelegatedChild` stay unchanged. Retarget the `storedWorkspacePlan` comment ("recorded on the session row at create") and the `sessionstore.Session.WorkspacePlan` comment ("submitted with POST /v1/sessions or POST /v1/sessions/start") to the plan stored when it binds. Cite `§14.1 WorkspacePlan Schema Versioning (Plan binding; Runtime workspace defaults)`.
5. **Registration.**
   - In `validateWorkspaceDefaults` (`pkg/gateway/externalapi/admin/runtimes.go`, also reached from `bootstrap.go`), after the existing checks, call `w.ComposeInto(workspaceplan.Plan{})` and return its error with the prefix `workspaceDefaults: `.
   - Remove `"ref"` from `workspaceDefaults.files.items.properties` in `pkg/gateway/externalapi/openapi/openapi.json`.
   - Regenerate `pkg/ops/mcp/generated_tools.go` with `go generate ./pkg/ops/mcp/`.
6. No migration is needed, because the composed plan goes into the existing session-row `WorkspacePlan` field. Run `gofumpt` and `goimports`.

## Staged schema, chart, and migration changes

The `schemas/workspaceplan-v1.json` description edits are part of CODE-3, and the `schemas/lenny-adapter.proto` comment edits are part of CODE-5. No chart or migration changes.

## Staged docs changes

### DOCS-1 · reader-facing upload and finalize documentation

Reader-facing pages carry no spec section numbers (doc-content.md). Each page states the behavior and links to the page that owns the concept.

**Rules for every page in this deliverable:**

- Take the upload request header name, the request body format, and the `UploadResponse` fields from the OpenAPI document as amended by CODE-3. Do not restate them as spec facts.
- Keep the upload token header on every finalize example. The upload-authorization rule requires the token on every finalize request, although the handler does not check it today (see the summary's unstaged defects).
- List the archive formats as `tar`, `tar.gz`, and `zip`.
- Replace literal `upload_abc123`-style values with a placeholder such as `<uploadRef>` and say that it is the value from the upload response.
- `docs/getting-started/concepts.md` owns the workspace-sources concept. The other pages link to its **Workspace sources** section and do not re-explain materialization timing.

**Page changes:**

| Page | Change |
|:--|:--|
| `docs/getting-started/concepts.md`, **Workspace lifecycle** and **Workspace sources** | State that sources are materialized at finalize from the plan bound at creation or at finalize, that `gitClone.ref` is pinned when the plan binds, and that only uploads a source of the bound plan names are materialized. Replace "at session creation time" and "at session creation" where they describe materialization or ref resolution. |
| `docs/api/rest.md`, endpoint table, `POST /v1/sessions/{id}/upload`, `POST /v1/sessions/{id}/finalize`, and the `workspacePlan` row of `POST /v1/sessions` | Replace the multipart description with the raw-body request and the `201` `UploadResponse`. Add a `POST /v1/sessions/{id}/upload-archive` section. Document the optional finalize body and its errors (`plan_already_set`, `invalid_upload_ref`, `upload_ref_foreign_session`, `WORKSPACE_PLAN_INVALID`, and the `GIT_CLONE_*` codes), and replace "Moves uploaded files from the session's staging area" with the finalize-time materialization. State that a create-time plan cannot carry `uploadFile` or `uploadArchive` sources. |
| `docs/api/mcp.md`, `upload_files` | Replace the `files[]` input and `{uploaded,totalBytes,paths}` output with the tool's input (`sessionId`, `uploadToken`, `contentBase64`, optional `mimeType`) and its output, the REST upload response carrying `uploadRef`. |
| `docs/api/mcp.md`, `finalize_workspace` | Add the optional `workspacePlan` argument and the errors listed for the REST finalize body. |
| `docs/client-guide/session-lifecycle.md`, **b. Upload Files**, **c. Finalize Workspace**, and **State Transition Preconditions** | Rewrite the upload walkthrough to the raw-body upload with its `201` response, and the finalize walkthrough to a finalize body whose plan names the returned `uploadRef`. Add `/upload-archive` to the precondition table. |
| `docs/client-guide/wire-format.md` | Change the upload request row from `multipart/form-data` to the raw body, add `/upload-archive`, and rename the header to the OpenAPI name. Remove the upload sources from the create example. |
| `docs/client-guide/sdk-examples/curl.md` | Rewrite the upload and finalize examples to the create, upload, finalize-with-plan sequence. |
| `docs/client-guide/sdk-examples/mcp-sdk.md` | Capture `uploadRef` from `upload_files` and pass a `workspacePlan` to `finalize_workspace`. |
| `docs/tutorials/first-session.md`, step 4 | Replace the nonexistent `lenny session upload` with `lenny session new --workspace` or `--file`, or with the REST create, upload, finalize-with-plan sequence. |
| `docs/operator-guide/lenny-ctl.md`, **Session Operations** | Replace "Session commands route through the **MCP** client SDK, not REST." with a lead stating that session commands route through the MCP client SDK and that `lenny session new` with `--workspace` or `--file` stages the workspace over the REST API. Delete "The one session command that also works over REST for non-interactive callers." from the `lenny session list` row, and keep that row on one physical line. |
| `docs/tutorials/session-derive-replay.md`, `docs/tutorials/multi-tenant-setup.md`, and `docs/tutorials/agent-memory.md` | Correct the header name and replace multipart `-F files=@` with a raw body plus a finalize `workspacePlan` naming the returned `uploadRef`. Where a tutorial uploads nothing, change only the header. |
| `docs/reference/workspace-plan.md`, intro, example, and the `uploadFile` and `uploadArchive` rows | Move the upload sources out of the create example into a finalize-body example, and describe `uploadRef` as the opaque value from the upload response for the same session. |
| `docs/reference/error-catalog.md`, `WORKSPACE_PLAN_INVALID` row | Name the finalize body as a third place the code is returned. |
| `docs/reference/error-catalog.md`, `SDK_DEMOTION_NOT_SUPPORTED` row; `docs/reference/metrics.md` and `docs/operator-guide/observability.md`, the `lenny_warmpool_sdk_demotions_total` row | Replace "Session creation failed because" with "The session failed because" in the catalog row. In `metrics.md`, delete " before assignment". In `observability.md`, replace "demoted before assignment" with "demoted to pod-warm". Each row stays one physical line. |
| `docs/runtime-author-guide/runtime-configuration.md`, the paragraph after the **Policy and limits** table | Replace "Setup commands run once per pod after workspace materialization and before the runtime starts, while the pod is in its init state. Per-session setup belongs in the runtime's handling of the session's first message rather than in setup commands, because a runtime process can serve more than one session and initializes once per launch." with "Setup commands run for each session, in the session's workspace, after the workspace is materialized and before the session starts. A pod that serves more than one session runs each session's setup commands. Initialization of the runtime process that is specific to one session belongs in the runtime's handling of that session's first message, because a runtime process can serve more than one session and initializes once per launch." Leave the rest of the paragraph unchanged. |
| `docs/reference/metrics.md` and `docs/operator-guide/observability.md`, the `lenny_warmpool_warmup_failure_total` row | State that `setup_command_failed` counts a session whose setup commands fail, and that the other error types count a pod that fails to reach `idle` during warm-up. |
| `docs/runtime-author-guide/runtime-configuration.md`, the `workspaceDefaults` row | State that each file carries inline content, that the defaults are copied into a session's workspace plan when the plan binds and later runtime changes do not reach that session, that a client file at the same path replaces the default, and that the runtime's setup command policy neither checks nor counts the default setup commands. Point larger files to `sharedAssets` or the image. |
| `docs/getting-started/concepts.md`, **Setup commands** | Replace "Setup commands are strictly scoped by the runtime's `setupCommandPolicy`:" with "The runtime's `setupCommandPolicy` checks the setup commands that a client's workspace plan submits. The setup commands from the runtime's own `workspaceDefaults` are exempt from the allowlist and from the command count. The following limits apply:". Leave the bullet list unchanged. |
| `docs/getting-started/concepts.md`, the derived-runtime `workspaceDefaults` bullet | Link to the `workspaceDefaults` row in `runtime-configuration.md`. |
| `docs/getting-started/concepts.md`, **Pool-level configuration**, the **Setup command policy:** bullet | Replace "Allowlisted commands that may run during workspace finalization." with "The allowlist and limits that apply to the setup commands a client's workspace plan submits." |

**Optional:** document the Go SDK's `UploadFile`, `UploadArchive`, and `FinalizeWorkspace(plan)` methods in `docs/client-guide/sdk-examples/go.md`, which covers only `Finalize` today.

Run tier 11 after the rewrite and confirm that `tests/tier11_docs/workspace_path_literal_sweep_test.go` still passes against the rewritten `rest.md` finalize text.

## Testing

### TEST-1 · tests across the reached tiers

Every new test carries a `// spec:` annotation, and every tier-2-and-up test carries a `// diagnosis:` comment. The `// spec:` form for the plan-binding cases is `// spec: 14.1 (WorkspacePlan Schema Versioning), 14 (Workspace Plan Schema)`.

**Tier 1, `pkg/gateway/sessionserver`.**

1. **Create-time upload sources are rejected.** Put the cases in a new `plan_binding_internal_test.go`. For each of `POST /v1/sessions` and `POST /v1/sessions/start`: a plan with an `uploadFile` source whose `uploadRef` is a well-formed `lenny-blob://` URI returns `400 VALIDATION_ERROR` with `details.reason = "upload_ref_foreign_session"` and `details.field = "sources[0].uploadRef"`; the same with an `uploadArchive` source; a non-URI `uploadRef` returns `invalid_upload_ref`. Each case asserts that no session row was persisted. The persisted-row assertion discriminates, because it separates a rejection from an accepted create that fails later. One ordering case, on `POST /v1/sessions` and on finalize, submits a plan whose `sources[0]` is a foreign `uploadFile`, followed by a `gitClone` source with a branch `ref`, with more setup commands than the runtime's `setupCommandPolicy.maxCommands`. It asserts `upload_ref_foreign_session` and zero `Resolve` calls on a counting `workspaceplan.RefResolver` stub, and the zero-call assertion is the one that discriminates.
2. **Finalize rejection binds nothing.** Rename `TestFinalizeRejectsForeignUploadRef_spec_12_5` to `TestFinalizeRejectsForeignUploadRef_spec_14_1` and extend it to assert that `row.WorkspacePlan` stays empty, beside its existing assertion that the row stays `created`.
3. **A null plan binds the empty plan.** A finalize body `{"workspacePlan":null}` on a session created without a plan, against a runtime without `workspaceDefaults`, succeeds and leaves `row.WorkspacePlan` empty.
4. **A finalize-bound `gitClone` ref pins at finalize.** In `gitclone_pin_test.go`, create a session without a plan, then finalize with a plan carrying a `gitClone` source whose `ref` is a branch name. Reuse `pinStubResolver`, and assert through `storedPlanSource` that `GET /v1/sessions/{id}` returns the stub's `resolvedCommitSha`. Add a table-driven finalize case beside it that finalizes a plan-less session with a `gitClone` plan, once with `pinStubResolver` returning an unresolvable `ResolveError` (`422 GIT_CLONE_REF_UNRESOLVABLE`), once with a transient one (`503 GIT_CLONE_REF_RESOLVE_TRANSIENT`), and once against a `vcsPoolStore` matching no host (`422 GIT_CLONE_AUTH_UNSUPPORTED_HOST`), and asserts the code, that the row stays `created`, and that `row.WorkspacePlan` stays empty; the empty-plan assertion discriminates, because it catches a `resolveFinalizePlan` ordering that persists the plan before the resolver rejects it.
5. **Re-annotate the existing plan-binding cases** in `finalize_plan_internal_test.go`: `TestFinalizeBindsUploadArchivePlan`, `TestFinalizeRejectsForeignUploadRef`, `TestFinalizeRejectsForeignTenantRef`, `TestFinalizeRejectsPlanWhenAlreadySet`, `TestFinalizeNoBodyKeepsEmptyPlan`, and `TestFinalizeBindsUploadFilePlan`. Leave `TestMapFinalizeCredentialMismatch` and `TestFinalizeCredentialMismatchSurfacesPoolExhausted` on their §4.9 and §7.3 annotations.

**Tier 1, `pkg/gateway/mcpfabric/mcptools`.** In `client_tools_test.go`, assert that the registered `lenny/finalize_workspace` input schema requires `sessionId` and declares an optional `workspacePlan` typed `["object", "null"]`. `TestPostByIDToolRouting_spec_15_2_3` already covers forwarding. Annotate `// spec: 15.2.1 (REST/MCP Consistency Contract), 14.1 (WorkspacePlan Schema Versioning)`.

**Tier 2, `pkg/gateway/podlifecycle/podsession/binder_test.go`** (envtest-backed). Stage two blobs for one session in a `MemoryStore`, bind a plan that names one of them, and assert that `PrepareWorkspace` receives only the named one. Its `// diagnosis:` states that a second staged file means finalize materialized an upload the bound plan does not name, which the §14.1 **Plan binding.** rule forbids.

**Tier 2, `pkg/gateway/podlifecycle/podsession/slotbinder_test.go`** (envtest-backed). Add `TestBindSlotRunsEachSessionsSetupOnASharedPod_spec_7_5`. Bind `sess-1` and `sess-2` onto one pod with `BindSlot` as `TestBindSlotSecondSessionSharesThePod` does, each with a plan carrying one setup command. From the `concurrentAdapter` recorder, assert that each session carries exactly one `RunSetup` and that it follows that session's `FinalizeWorkspace`. Annotate `// spec: 7.5 (Setup Commands)`. Its `// diagnosis:` states that a session with no `RunSetup`, or with a `RunSetup` before its `FinalizeWorkspace`, means the bind path gated setup per pod or ran it before the slot was materialized.

**Tier 2, `pkg/gateway/podlifecycle/podsession/binder_test.go`** (envtest-backed). Add `TestBindRunsEachSequentialSessionsSetupOnARecycledPod_spec_7_5`. On a recycling pool with one pod, bind `sess-1` through the exclusive bind path, release it as `TestReleaseRecyclingPatchesClaimToRecycling_spec_6_2` does, return the pod to idle, and bind `sess-2` onto the same pod, each with a plan carrying one setup command. With an adapter fake that records the session ID of each `FinalizeWorkspace` and `RunSetup` call, assert that each session carries exactly one `RunSetup` and that it follows that session's `FinalizeWorkspace`. Annotate `// spec: 7.5 (Setup Commands), 5.2 (Execution Modes)`. Its `// diagnosis:` states that `sess-2` without a `RunSetup` means the exclusive bind path gated setup per pod.

**Tier 3.**

- `tests/tier3_contract/rest_sessions/openapi_document_test.go`: assert that the finalize operation declares an optional `requestBody` referencing `FinalizeRequest`, that `FinalizeRequest.properties.workspacePlan` is typed `["object", "null"]`, and that the `400`, `422`, and `503` responses are present. Assert that both `/upload` and `/upload-archive` return `UploadResponse` on `201`.
- `tests/tier3_contract/rest_mcp_consistency`: add `plan_already_set` and `upload_ref_foreign_session` cases that drive REST finalize and MCP `lenny/finalize_workspace` with the same body, and assert identical `code`, `category`, and `retryable`. Annotate `// spec: 15.2.1 (REST/MCP Consistency Contract)`.
- `tests/tier3_contract/rest_mcp_consistency/error_matrix_test.go`: delete `"SDK_DEMOTION_NOT_SUPPORTED"` from `matrixErrorClasses`, which §15.2.1 keeps in lockstep with its list.

**Tier 9, new `tests/tier9_security/upload_ref_scope_test.go`.** Within one tenant, create session A and upload content to it, then create session B with a plan whose `uploadFile` source names A's `uploadRef`, and finalize a third session C with a plan naming A's `uploadRef`. Both requests return `upload_ref_foreign_session`. Wrap the gateway's blob store in a counting wrapper and assert that it recorded zero `Get` calls for A's key. The zero-`Get` assertion discriminates: a regression that validates late would still fail the request, but only after fetching another session's content. Its `// diagnosis:` states that a nonzero count means a client plan reached the binder with another session's content.

**Re-run only:** the tier 4 suites `eager_claim_lifecycle_test.go`, `finalize_admission_race_test.go`, and `credential_delivery_gate_test.go` under `tests/tier4_integration`, which exercise the finalize bind end to end.

Run tiers 0, 1, 2, 3, 4 (re-run), and 9. Reap envtest orphans with `pkill -f kubebuilder-envtest` after tier 2.

### TEST-2 · runtime `workspaceDefaults` tests

Each test carries `// spec: 14.1 (WorkspacePlan Schema Versioning), 5.1 (Runtime)`, the policy case adds `7.5 (Setup Commands)`, the demotion case adds `6.1 (What a Pre-Warmed Pod Looks Like)`, and the routed-runtime cases add `10.7 (Experiment Primitives)`. The tier 4 test carries a `// diagnosis:` comment.

**Tier 1, `pkg/gateway/runtime/runtimestore/workspace_defaults_test.go`.**

1. `TestComposeIntoOrdersDefaultsFirst_spec_14_1`: a table over nil, empty, files-only, commands-only, and both, against an empty and a non-empty client plan. It asserts the returned `bool`, that the sources are the defaults followed by the client sources, and that the setup commands are the defaults followed by the client's, each keeping its `TimeoutSeconds`. A seeded randomized case (the seed is logged) asserts the same concatenation over generated inputs.
2. `TestComposeIntoRejectsRefAndInvalidPath_spec_5_1`: a `Ref` entry, an absolute path, and a `../` path each return an error.
3. `TestComposeIntoCollisionFollowsPathRule_spec_14_1`: a default and a client source share one path. `ParseStored` of the marshalled result reports `workspace_plan_path_collision`, with the default's index as the losing index.
4. `TestComposeWorkspaceDefaultsResolve_spec_14_1`: a nil store and `ErrNotFound` return the plan unchanged, another store error is returned, and a derived runtime composes the merged base-then-derived defaults.

**Tier 1, `pkg/gateway/externalapi/admin`.** Add `file-with-ref` and `file-path-escapes` cases to `TestCreateRuntimeRejectsInvalidWorkspaceBlocks`.

**Tier 1, `pkg/gateway/sessionserver`, new `workspace_defaults_bind_internal_test.go`.** These run against a `runtimestore.Memory` runtime that declares two files and one setup command.

5. `TestCreateWithPlanStoresComposedPlan_spec_14_1`: `POST /v1/sessions` with a plan stores the composed plan, and `GET` returns it.
6. `TestCreateWithoutPlanComposesAtFinalize_spec_14_1`: `POST /v1/sessions` without a plan stores none, and a no-body finalize stores the defaults-only plan. A second session finalized with a body plan stores the defaults followed by the body sources.
7. `TestCreateAndStartComposesDefaults_spec_14_1`: `POST /v1/sessions/start` without a plan stores the defaults-only plan.
8. `TestBoundDefaultsArePinned_spec_14_1`: after a create with a plan, replace the runtime's defaults in the store. A body-less finalize leaves the stored plan bytes equal to those stored at create, and `exclusiveBindRequest` built from `storedWorkspacePlan(row)` carries the original default content, once each.
9. `TestDefaultCommandsExemptFromSetupPolicy_spec_7_5`: the runtime declares `maxCommands: 1`, allowlist `make`, and two default commands outside the allowlist. A table runs each case against `POST /v1/sessions`, `POST /v1/sessions/start`, and a finalize body on a session created without a plan. A client plan with one `make` command is accepted, and this assertion discriminates, because composing before the policy check counts three commands. A client plan with two commands returns `setup_commands_max_exceeded` with `count` 2, and on finalize the row stays `created` with `row.WorkspacePlan` empty.
10. `TestDefaultsResolveFailureLeavesStateUnchanged_spec_14_1`: a runtime store that returns a non-not-found error makes create return `500` with no row persisted, makes finalize return `500` with the row still `created` and without a plan, and makes `POST /v1/sessions/start` without a plan return `500` with no row persisted and no `SandboxClaim` created (count claims as `lookupFixture.claimCount` does).
11. `TestRuntimeWithoutDefaultsStoresSubmittedBytes_spec_14_1`: the stored plan bytes equal the submitted bytes.
12. `TestRoutedRuntimeDefaultsAreComposed_spec_14_1`: an experiment assigns a variant whose runtime declares defaults that differ from the requested runtime's, with the harness of `TestExperimentRouterAllowsStrongerVariantPool`. For `POST /v1/sessions` with a plan and for `POST /v1/sessions/start` without one, the stored plan carries the variant runtime's defaults and none of the requested runtime's.
13. `TestDefaultsOnlyPlanTriggersSDKWarmDemotion_spec_6_1`: against a runtime with `preConnect: true`, the default `sdkWarmBlockingPaths`, and a default `CLAUDE.md` file, a no-body finalize and a `POST /v1/sessions/start` without a plan each yield an `exclusiveBindRequest`, built from `storedWorkspacePlan(row)`, for which `podsession.RequiresDemotion` returns true.

**Tier 1, `pkg/gateway/mcpfabric/delegation`.**

14. `TestDelegatedChildPlanComposesRuntimeDefaults_spec_14_1`: the child row's plan holds the target runtime's defaults followed by the export `uploadFile` sources, and with no `fileExport` it holds the defaults alone. A child that `ApplyExperimentRouting` assigns to a variant runtime holds that runtime's defaults. A resolve error fails the delegation, writes no child row, and undoes the export.

**Tier 4, new `tests/tier4_integration/workspace_defaults_test.go`.**

15. `TestRuntimeDefaultsMaterializeAtFinalize_spec_14_1` uses the gateway-and-adapter harness of `eager_claim_lifecycle_test.go`, with defaults `CLAUDE.md` and `defaults.txt` on its `echo` runtime. It creates a session without a plan, uploads, and finalizes with a plan whose upload writes `CLAUDE.md`. It asserts that `CLAUDE.md` holds the uploaded content and `defaults.txt` holds the default content under the slot's `current` directory. Its `// diagnosis:` states that a missing `defaults.txt` means a binding request dropped the defaults, and that default content in `CLAUDE.md` means the defaults were placed after the client source.

**Re-run:** `tests/tier0_static/api_codegen_drift_test.go` and `tests/tier3_contract/ops_endpoints/mcp_test.go` after the OpenAPI edit.

Run tiers 0, 1, 3 (re-run), and 4.

## Edge cases and accepted failure modes

- **An own-session `uploadRef` whose blob was never written, or has expired.** Bind-time validation does not check existence. Materialization fails, and the existing finalize prepare-phase error returns.
- **A finalize body larger than the body cap.** `readFinalizePlanBody` keeps returning `413 PAYLOAD_TOO_LARGE`. The cap is not specified here.
- **Unknown top-level fields in the finalize body.** The decoder ignores them, and `FinalizeRequest` leaves top-level `additionalProperties` unset to match.

## Files touched on application (non-spec)

- `pkg/gateway/sessionserver/workspace_plan.go` and `pkg/gateway/sessionserver/create_and_start.go` (CODE-1)
- `pkg/gateway/sessionserver/create.go` (CODE-1)
- `pkg/gateway/sessionserver/finalize.go` (CODE-1)
- `pkg/gateway/sessionserver/finalize_handler.go` (CODE-1)
- `pkg/gateway/mcpfabric/mcptools/client_tools.go` (CODE-2)
- `pkg/gateway/externalapi/openapi/openapi.json` (CODE-3)
- `schemas/workspaceplan-v1.json` (CODE-3)
- `sdks/client/go/lenny/upload.go` (CODE-4)
- `pkg/gateway/sessionserver/upload.go` (CODE-4)
- `pkg/embedded/localcli/session.go` (CODE-4)
- `pkg/gateway/runtime/sdkwarm/matcher.go` (CODE-4)
- `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_sessionlifecycle.go` and `pkg/observability/metrics/catalog.go` (CODE-4)
- `schemas/lenny-adapter.proto`, `pkg/proto/adapter/v1/lenny-adapter.pb.go` (regenerated), and `pkg/adapter/workspace/setup.go` (CODE-5)
- `pkg/gateway/runtime/runtimestore/workspace_defaults.go` (new), `pkg/gateway/runtime/runtimestore/runtimestore.go`, `pkg/gateway/sessionserver/create.go`, `pkg/gateway/sessionserver/create_and_start.go`, `pkg/gateway/sessionserver/finalize.go`, `pkg/gateway/sessionserver/workspace_plan.go`, `pkg/gateway/session/sessionstore/sessionstore.go`, `pkg/gateway/mcpfabric/delegation/service.go`, `pkg/gateway/externalapi/admin/runtimes.go`, `pkg/gateway/externalapi/openapi/openapi.json`, and `pkg/ops/mcp/generated_tools.go` (CODE-6)
- `docs/getting-started/concepts.md`, `docs/api/rest.md`, `docs/api/mcp.md`, `docs/client-guide/session-lifecycle.md`, `docs/client-guide/wire-format.md`, `docs/client-guide/sdk-examples/curl.md`, `docs/client-guide/sdk-examples/mcp-sdk.md`, `docs/tutorials/first-session.md`, `docs/operator-guide/lenny-ctl.md`, `docs/tutorials/session-derive-replay.md`, `docs/tutorials/multi-tenant-setup.md`, `docs/tutorials/agent-memory.md`, `docs/reference/workspace-plan.md`, `docs/reference/error-catalog.md`, `docs/reference/metrics.md`, `docs/operator-guide/observability.md`, `docs/runtime-author-guide/runtime-configuration.md`, and optionally `docs/client-guide/sdk-examples/go.md` (DOCS-1)
- `pkg/gateway/sessionserver/plan_binding_internal_test.go` (new), `pkg/gateway/sessionserver/finalize_plan_internal_test.go`, `pkg/gateway/sessionserver/gitclone_pin_test.go`, `pkg/gateway/mcpfabric/mcptools/client_tools_test.go`, `pkg/gateway/podlifecycle/podsession/binder_test.go`, `pkg/gateway/podlifecycle/podsession/slotbinder_test.go`, `tests/tier3_contract/rest_sessions/openapi_document_test.go`, `tests/tier3_contract/rest_mcp_consistency/`, and `tests/tier9_security/upload_ref_scope_test.go` (new) (TEST-1)
- `pkg/gateway/runtime/runtimestore/workspace_defaults_test.go` (new), `pkg/gateway/externalapi/admin/runtimes_workspace_test.go`, `pkg/gateway/sessionserver/workspace_defaults_bind_internal_test.go` (new), a new test in `pkg/gateway/mcpfabric/delegation`, and `tests/tier4_integration/workspace_defaults_test.go` (new) (TEST-2)
