# Non-spec changes: Finalize-time WorkspacePlan bind has no specification contract

These changes are staged. Applying them is the implementation's work, under the
implementation checklist.

## Design (implementation-facing)

The finalize-time bind already runs in the tree, so most of the non-spec work aligns published surfaces, comments, and tests with the amended spec. One production behavior changes:

- CODE-1 moves the `uploadRef` scope check into the shared plan resolver, so it runs on every client-submitted plan at every bind point. A create-time or create-and-start plan that names an upload source is now rejected. That path is a tenant-isolation path, which is why TEST-1 reaches tier 9.
- CODE-2 declares the `workspacePlan` argument the MCP `lenny/finalize_workspace` tool already forwards.
- CODE-3 publishes the finalize request body and its plan-bind responses in the OpenAPI document, and replaces the `uploadRef` descriptions in the OpenAPI document and the published `WorkspacePlan` schema with the opaque contract.
- CODE-4 corrects SDK and gateway comments that the amended spec makes false.
- DOCS-1 rewrites the reader-facing upload and finalize documentation.
- TEST-1 pins each new spec statement and the CODE-1 behavior change.

Locate every code site by symbol and quoted text. The normative rules are in the spec-changes file: the `uploadRef` rule is SPEC-1's field note, and the binding rules are SPEC-2's **Plan binding.** paragraph. The deliverables below implement or cite those homes.

## Staged code changes

### CODE-1 · one `uploadRef` scope check in the shared plan resolver

Targets: `pkg/gateway/sessionserver/start.go` (`resolvePlanForCreate` and its caller in the create-and-start path), `pkg/gateway/sessionserver/create.go` (the `resolvePlanForCreate` call in `validateAndBuildCreateRow`), `pkg/gateway/sessionserver/finalize.go` (`finalizeRequest`, `resolveFinalizePlan`, `validateFinalizeUploadRefs`), and `pkg/gateway/sessionserver/sessionserver.go` (the comment block above the `resolveFinalizePlan` call in `handleFinalize`).

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
   - The create-and-start build in `start.go` passes `(tenantID, "")`.
   - `resolveFinalizePlan` passes `(tenantID, row.ID)` and deletes its own `validateFinalizeUploadRefs` call. The `plan_already_set` guard stays before the resolver call, and the `enforceSetupCommandPolicy` call stays after it. The finalize rejection order becomes: body read, `plan_already_set`, `Parse`, `uploadRef` scope, `gitClone` auth binding, ref pinning, then setup-command policy.
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
2. `lenny/start_session`, `lenny/terminate_session`, and `lenny/resume_session` pass `sessionIDInputSchema`.
3. `lenny/finalize_workspace` passes a new package-level `finalizeWorkspaceInputSchema`:

   ```go
   // finalizeWorkspaceInputSchema declares the optional workspacePlan that
   // lenny/finalize_workspace forwards as the REST finalize body.
   // spec: §15.2.1 REST/MCP Consistency Contract; §14.1 WorkspacePlan Schema
   // Versioning (Plan binding).
   var finalizeWorkspaceInputSchema = json.RawMessage(`{"type":"object","required":["sessionId"],"properties":{"sessionId":{"type":"string","description":"The target session id."},"workspacePlan":{"type":"object","additionalProperties":true,"description":"Optional §14 WorkspacePlan bound at finalize when the session was created without one."}}}`)
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
   "FinalizeRequest": {"type": "object", "properties": {"workspacePlan": {"type": "object", "additionalProperties": true, "description": "WorkspacePlan bound at finalize when the session's CreateSessionRequest carried none (§14.1 Plan binding)."}}}
   ```

   Name the component `FinalizeRequest`, parallel to `DeriveRequest`. `FinalizeWorkspaceRequest` is the adapter proto message in `schemas/lenny-adapter.proto` and must not be reused on this boundary. Leave the component's top-level `additionalProperties` unset.

3. Add these responses to the finalize operation, each with `content.application/json.schema` set to `{"$ref": "#/components/schemas/Error"}` as `POST /v1/sessions` does. Keep the existing `200` and `409`.

   | Status | `description` |
   |:--|:--|
   | `400` | `VALIDATION_ERROR (plan_already_set, invalid_upload_ref, upload_ref_foreign_session, malformed body) / WORKSPACE_PLAN_INVALID` |
   | `422` | `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED / GIT_CLONE_AUTH_UNSUPPORTED_HOST / GIT_CLONE_AUTH_HOST_AMBIGUOUS / GIT_CLONE_REF_UNRESOLVABLE` |
   | `503` | `GIT_CLONE_REF_RESOLVE_TRANSIENT` |

   Do not list `SETUP_COMMAND_FAILED`, `SESSION_CREATION_FAILED`, `CREDENTIAL_POOL_EXHAUSTED`, or `UPLOAD_ARCHIVE_LIMIT_EXCEEDED`. They are prepare-phase outcomes that arise whether or not a body is sent, and the finalize prepare-phase envelope belongs to §15.1.

4. `components.schemas.UploadResponse.properties.uploadRef.description` becomes "Opaque reference to the stored upload. Pass it verbatim as the uploadRef of an uploadFile or uploadArchive source in a plan bound to this session."
5. `schemas/workspaceplan-v1.json`: replace the `uploadFile.uploadRef` description "Reference to a previously uploaded blob (lenny-blob:// URI or upload id)." with "Opaque uploadRef returned by POST /v1/sessions/{id}/upload or /upload-archive for the session the plan binds to." Add the same `description` to `uploadArchive.uploadRef`, which has none today. Change nothing else in the schema.

### CODE-4 · stale SDK and gateway comments

Comment-only changes. Run `gofumpt` and `goimports`.

- **`sdks/client/go/lenny/upload.go`, `FinalizeWorkspace` doc comment.** Replace "binding the plan onto a created session and transitioning it to ready" and the sentence "the gateway materializes them when the session starts" so the comment reads: "...binding the plan onto a created session (§14.1 Plan binding). The gateway validates the plan and, during finalize, materializes its sources, including the uploads it names; the call returns once the session is ready." Change "(via UploadArchive)" to "(via UploadArchive or UploadFile)". The citation line becomes `spec: §14.1 WorkspacePlan Schema Versioning (Plan binding); §7.1 Normal Flow; §26.2`.
- **`sdks/client/go/lenny/upload.go`, `UploadResult` doc comment.** Replace the `§4.5 lenny-blob:// URI` claim with: "UploadRef is the opaque reference the caller passes verbatim as sources[<n>].uploadRef in a plan bound to this same session at finalize." Cite `spec: §14 Workspace Plan Schema (uploadRef field note); §15.1`.
- **`pkg/gateway/sessionserver/upload.go`, `UploadResponse.UploadRef` comment.** Keep the statement that the gateway emits a `lenny-blob://` URI, which is implementation detail. Drop the `§4.5` attribution. State that clients treat the value as opaque and that it is valid only in a plan bound to the uploading session, which `validatePlanUploadRefs` enforces. Replace "in a subsequent finalize call" with "in a plan bound to this session". Cite `spec: §14 Workspace Plan Schema (uploadRef field note); §14.1 WorkspacePlan Schema Versioning (Plan binding)`.
- **`pkg/embedded/localcli/session.go`, `createSessionWithWorkspace` doc comment.** The prose is accurate. Append `§14.1 (Plan binding)` to its existing spec line and leave the prose unchanged.

## Staged schema, chart, and migration changes

The `schemas/workspaceplan-v1.json` description edit is part of CODE-3. No chart or migration changes.

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
| `docs/api/rest.md`, endpoint table, `POST /v1/sessions/{id}/upload`, `POST /v1/sessions/{id}/finalize`, and the `workspacePlan` row of `POST /v1/sessions` | Replace the multipart description with the raw-body request and the `201` `UploadResponse`. Add a `POST /v1/sessions/{id}/upload-archive` section. Document the optional finalize body and its errors (`plan_already_set`, `invalid_upload_ref`, `upload_ref_foreign_session`, `WORKSPACE_PLAN_INVALID`, `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED`, and the `GIT_CLONE_*` codes), and replace "Moves uploaded files from the session's staging area" with the finalize-time materialization. State that a create-time plan cannot carry `uploadFile` or `uploadArchive` sources. |
| `docs/api/mcp.md`, `upload_files` | Replace the `files[]` input and `{uploaded,totalBytes,paths}` output with the tool's input (`sessionId`, `uploadToken`, `contentBase64`, optional `mimeType`) and its output, the REST upload response carrying `uploadRef`. |
| `docs/api/mcp.md`, `finalize_workspace` | Add the optional `workspacePlan` argument and the errors listed for the REST finalize body. |
| `docs/client-guide/session-lifecycle.md`, **b. Upload Files**, **c. Finalize Workspace**, and **State Transition Preconditions** | Rewrite the upload walkthrough to the raw-body upload with its `201` response, and the finalize walkthrough to a finalize body whose plan names the returned `uploadRef`. Add `/upload-archive` to the precondition table. |
| `docs/client-guide/wire-format.md` | Change the upload request row from `multipart/form-data` to the raw body, add `/upload-archive`, and rename the header to the OpenAPI name. Remove the upload sources from the create example. |
| `docs/client-guide/sdk-examples/curl.md` | Rewrite the upload and finalize examples to the create, upload, finalize-with-plan sequence. |
| `docs/client-guide/sdk-examples/mcp-sdk.md` | Capture `uploadRef` from `upload_files` and pass a `workspacePlan` to `finalize_workspace`. |
| `docs/tutorials/first-session.md`, step 4 | Replace the nonexistent `lenny session upload` with `lenny session new --workspace` or `--file`, or with the REST create, upload, finalize-with-plan sequence. |
| `docs/tutorials/session-derive-replay.md`, `docs/tutorials/multi-tenant-setup.md`, and `docs/tutorials/agent-memory.md` | Correct the header name and replace multipart `-F files=@` with a raw body plus a finalize `workspacePlan` naming the returned `uploadRef`. Where a tutorial uploads nothing, change only the header. |
| `docs/reference/workspace-plan.md`, intro, example, and the `uploadFile` and `uploadArchive` rows | Move the upload sources out of the create example into a finalize-body example, and describe `uploadRef` as the opaque value from the upload response for the same session. |
| `docs/reference/error-catalog.md`, `WORKSPACE_PLAN_INVALID` row | Name the finalize body as a third place the code is returned. |

**Optional:** document the Go SDK's `UploadFile`, `UploadArchive`, and `FinalizeWorkspace(plan)` methods in `docs/client-guide/sdk-examples/go.md`, which covers only `Finalize` today.

Run tier 11 after the rewrite and confirm that `tests/tier11_docs/workspace_path_literal_sweep_test.go` still passes against the rewritten `rest.md` finalize text.

## Testing

### TEST-1 · tests across the reached tiers

Every new test carries a `// spec:` annotation, and every tier-2-and-up test carries a `// diagnosis:` comment. The `// spec:` form for the plan-binding cases is `// spec: 14.1 (WorkspacePlan Schema Versioning), 14 (Workspace Plan Schema)`.

**Tier 1, `pkg/gateway/sessionserver`.**

1. **Create-time upload sources are rejected.** Put the cases in a new `plan_binding_internal_test.go`. For each of `POST /v1/sessions` and `POST /v1/sessions/start`: a plan with an `uploadFile` source whose `uploadRef` is a well-formed `lenny-blob://` URI returns `400 VALIDATION_ERROR` with `details.reason = "upload_ref_foreign_session"` and `details.field = "sources[0].uploadRef"`; the same with an `uploadArchive` source; a non-URI `uploadRef` returns `invalid_upload_ref`. Each case asserts that no session row was persisted. The persisted-row assertion discriminates, because it separates a rejection from an accepted create that fails later.
2. **Finalize rejection binds nothing.** Rename `TestFinalizeRejectsForeignUploadRef_spec_12_5` to `TestFinalizeRejectsForeignUploadRef_spec_14_1` and extend it to assert that `row.WorkspacePlan` stays empty, beside its existing assertion that the row stays `created`.
3. **A null plan binds nothing.** A finalize body `{"workspacePlan":null}` on a session created without a plan succeeds and leaves `row.WorkspacePlan` empty.
4. **A finalize-bound `gitClone` ref pins at finalize.** In `gitclone_pin_test.go`, create a session without a plan, then finalize with a plan carrying a `gitClone` source whose `ref` is a branch name. Reuse `pinStubResolver`, and assert through `storedPlanSource` that `GET /v1/sessions/{id}` returns the stub's `resolvedCommitSha`.
5. **Re-annotate the existing plan-binding cases** in `finalize_plan_internal_test.go`: `TestFinalizeBindsUploadArchivePlan`, `TestFinalizeRejectsForeignUploadRef`, `TestFinalizeRejectsForeignTenantRef`, `TestFinalizeRejectsPlanWhenAlreadySet`, `TestFinalizeNoBodyKeepsEmptyPlan`, and `TestFinalizeBindsUploadFilePlan`. Leave `TestMapFinalizeCredentialMismatch` and `TestFinalizeCredentialMismatchSurfacesPoolExhausted` on their §4.9 and §7.3 annotations.

**Tier 1, `pkg/gateway/mcpfabric/mcptools`.** In `client_tools_test.go`, assert that the registered `lenny/finalize_workspace` input schema requires `sessionId` and declares an optional object-typed `workspacePlan`. `TestPostByIDToolRouting_spec_15_2_3` already covers forwarding. Annotate `// spec: 15.2.1 (REST/MCP Consistency Contract), 14.1 (WorkspacePlan Schema Versioning)`.

**Tier 2, `pkg/gateway/podlifecycle/podsession/binder_test.go`** (envtest-backed). Stage two blobs for one session in a `MemoryStore`, bind a plan that names one of them, and assert that `PrepareWorkspace` receives only the named one. Its `// diagnosis:` states that a second staged file means finalize materialized an upload the bound plan does not name, which the §14.1 **Plan binding.** rule forbids.

**Tier 3.**

- `tests/tier3_contract/rest_sessions/openapi_document_test.go`: assert that the finalize operation declares an optional `requestBody` referencing `FinalizeRequest`, that `FinalizeRequest.properties.workspacePlan` exists, and that the `400`, `422`, and `503` responses are present. Assert that both `/upload` and `/upload-archive` return `UploadResponse` on `201`.
- `tests/tier3_contract/rest_mcp_consistency`: add `plan_already_set` and `upload_ref_foreign_session` cases that drive REST finalize and MCP `lenny/finalize_workspace` with the same body, and assert identical `code`, `category`, and `retryable`. Annotate `// spec: 15.2.1 (REST/MCP Consistency Contract)`.

**Tier 9, new `tests/tier9_security/upload_ref_scope_test.go`.** Within one tenant, create session A and upload content to it, then create session B with a plan whose `uploadFile` source names A's `uploadRef`, and finalize a third session C with a plan naming A's `uploadRef`. Both requests return `upload_ref_foreign_session`. Wrap the gateway's blob store in a counting wrapper and assert that it recorded zero `Get` calls for A's key. The zero-`Get` assertion discriminates: a regression that validates late would still fail the request, but only after fetching another session's content. Its `// diagnosis:` states that a nonzero count means a client plan reached the binder with another session's content.

**Re-run only:** the tier 4 suites `eager_claim_lifecycle_test.go`, `finalize_admission_race_test.go`, and `credential_delivery_gate_test.go` under `tests/tier4_integration`, which exercise the finalize bind end to end.

Run tiers 0, 1, 2, 3, 4 (re-run), and 9. Reap envtest orphans with `pkill -f kubebuilder-envtest` after tier 2.

## Edge cases and accepted failure modes

- **An own-session `uploadRef` whose blob was never written, or has expired.** Bind-time validation does not check existence. Materialization fails, and the existing finalize prepare-phase error returns.
- **A finalize body larger than the body cap.** `readFinalizePlanBody` keeps returning `413 PAYLOAD_TOO_LARGE`. The cap is not specified here.
- **Unknown top-level fields in the finalize body.** The decoder ignores them, and `FinalizeRequest` leaves top-level `additionalProperties` unset to match.

## Files touched on application (non-spec)

- `pkg/gateway/sessionserver/start.go` (CODE-1)
- `pkg/gateway/sessionserver/create.go` (CODE-1)
- `pkg/gateway/sessionserver/finalize.go` (CODE-1)
- `pkg/gateway/sessionserver/sessionserver.go` (CODE-1)
- `pkg/gateway/mcpfabric/mcptools/client_tools.go` (CODE-2)
- `pkg/gateway/externalapi/openapi/openapi.json` (CODE-3)
- `schemas/workspaceplan-v1.json` (CODE-3)
- `sdks/client/go/lenny/upload.go` (CODE-4)
- `pkg/gateway/sessionserver/upload.go` (CODE-4)
- `pkg/embedded/localcli/session.go` (CODE-4)
- `docs/getting-started/concepts.md`, `docs/api/rest.md`, `docs/api/mcp.md`, `docs/client-guide/session-lifecycle.md`, `docs/client-guide/wire-format.md`, `docs/client-guide/sdk-examples/curl.md`, `docs/client-guide/sdk-examples/mcp-sdk.md`, `docs/tutorials/first-session.md`, `docs/tutorials/session-derive-replay.md`, `docs/tutorials/multi-tenant-setup.md`, `docs/tutorials/agent-memory.md`, `docs/reference/workspace-plan.md`, `docs/reference/error-catalog.md`, and optionally `docs/client-guide/sdk-examples/go.md` (DOCS-1)
- `pkg/gateway/sessionserver/plan_binding_internal_test.go` (new), `pkg/gateway/sessionserver/finalize_plan_internal_test.go`, `pkg/gateway/sessionserver/gitclone_pin_test.go`, `pkg/gateway/mcpfabric/mcptools/client_tools_test.go`, `pkg/gateway/podlifecycle/podsession/binder_test.go`, `tests/tier3_contract/rest_sessions/openapi_document_test.go`, `tests/tier3_contract/rest_mcp_consistency/`, and `tests/tier9_security/upload_ref_scope_test.go` (new) (TEST-1)
