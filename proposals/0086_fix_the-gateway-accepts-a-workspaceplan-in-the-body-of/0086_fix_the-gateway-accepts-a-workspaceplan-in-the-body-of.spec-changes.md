# Spec changes: Finalize-time WorkspacePlan bind has no specification contract

These edits are staged. Applying them is the implementation's work, under the
implementation checklist.

## Design (as the spec must state it)

The specification adopts the plan-reference model with a finalize-time bind, which is the model the tree runs. Each rule below has one normative home in the staged text:

1. **Plan binding.** A session binds at most one `WorkspacePlan`, either in its `CreateSessionRequest` or, when that request carried none, in the optional body of `POST /v1/sessions/{id}/finalize`. The finalize body, the `plan_already_set` rejection, the timing of plan validation and `gitClone.ref` resolution, the same-write persistence, the outcome of a rejected finalize, and the rule that unreferenced uploads are not materialized all live in the new §14.1 **Plan binding.** paragraph (SPEC-2).
2. **The `uploadRef` contract.** An `uploadRef` is an opaque reference to stored upload content, scoped to the session for which the content was stored. The response it comes from, its scope, the two rejection reasons, and the consequence that a create-time plan carries no upload source live in the new §14 `uploadRef` field note (SPEC-1).
3. **Pin timing.** `gitClone.ref` pins when the plan binds. The plan binds once, so the per-session immutability guarantee is unchanged. The §14 bullet that states the guarantee carries the timing (SPEC-1), and the definitional sentence in **Plan binding.** covers every other §14 and §14.1 clause that says "at session creation".

Every other site cites these homes. §15.1 and §15.2 carry the endpoint, precondition, and catalog rows (SPEC-3), because catalog rows are read one at a time and the definitional sentence covers only §14 and §14.1. §7.1, §7.4, and §29.2 narrow "the buffered content" to the uploads the bound plan names (SPEC-4, SPEC-5). §24.17 and §26.2 describe the REST flow the CLI runs (SPEC-6).

The spec does not declare `uploadRef` a §28.5.3 `LennyBlobURI`, and it does not restate the `UploadResponse` fields other than `uploadRef`. The OpenAPI document is the authoritative REST schema under §15.2.1 item 4.

## Edge cases and accepted failure modes

- **A finalize body carries a plan for a session that already has one.** **Plan binding.** rejects it with `plan_already_set`. The session stays `created`.
- **A finalize body carries `"workspacePlan": null`, or no body is sent.** **Plan binding.** treats it as binding no plan.
- **A create-time plan names an upload source.** No upload for the session can exist when the create request is validated, so the `uploadRef` field note rejects every such source, with `invalid_upload_ref` or `upload_ref_foreign_session`.
- **A client uploads content that no source of the bound plan names.** **Plan binding.** states that it is not materialized. No warning or rejection is emitted.
- **A finalize rejected for its plan.** **Plan binding.** states that the rejection binds nothing and leaves the session `created`, so the client can correct the plan and finalize again within the `created` window.
- **A `gitClone` ref moves between creation and a finalize-time bind.** The ref pins at the bind, and later materializations of the session clone the pinned SHA.
- **A delegation child plan.** The gateway writes it onto the child row with `uploadRef` values it minted under the child session (§8.7). It is not a client-submitted plan, so the `uploadRef` rejection rule does not apply to it.
- **A derived or replayed session without a plan.** §7.1 derive rules and §15.1 **Session Replay Semantics** govern its workspace. **Plan binding.** states no workspace outcome for a session without a plan.

## Staged edits

### SPEC-1 · spec/14_workspace-plan-schema.md § 14 Workspace Plan Schema

SPEC-1 makes four edits.

**Edit (a), `uploadRef` field note.** In the **Field notes:** list, insert the bullet below as a new list item immediately before the bullet that begins "- `uploadArchive.stripComponents`: Non-negative integer".

```markdown
- `uploadFile.uploadRef` and `uploadArchive.uploadRef`: An opaque reference to stored upload content. A client obtains it from the `201` response of `POST /v1/sessions/{id}/upload` or `POST /v1/sessions/{id}/upload-archive` ([§15.1](15_external-api-surface.md#151-rest-api)). The gateway mints it when it persists a delegation file export for a child session ([§8.7](08_recursive-delegation.md#87-file-export-model)). Clients pass the value back verbatim and do not construct or parse it. An `uploadRef` belongs to the session for which its content was stored, and it is valid only in a plan bound to that session ([§14.1](#141-workspaceplan-schema-versioning) **Plan binding.**). The gateway rejects a client-submitted plan whose `uploadRef` does not belong to the session the plan binds to with `400 VALIDATION_ERROR` and `details.field = "sources[<n>].uploadRef"`. `details.reason` is `invalid_upload_ref` when the gateway cannot read the value as an upload reference, and `upload_ref_foreign_session` when the value names another session's content. An upload requires the `uploadToken` that session creation issues ([§7.4](07_session-lifecycle.md#74-upload-safety) **Upload authorization:**), so a plan in a `CreateSessionRequest` cannot carry an `uploadFile` or `uploadArchive` source.
```

**Edit (b), canonical example.** In the JSON block at the top of the section, delete the two `sources[]` entries below, together with the comma that follows the second entry's closing brace. The `inlineFile` entries before them and the `mkdir` entry after them stay, and the resulting array stays valid JSON.

```json
      {
        "type": "uploadFile",
        "path": "src/main.ts",
        "uploadRef": "upload_abc123"
      },
      {
        "type": "uploadArchive",
        "pathPrefix": ".",
        "uploadRef": "upload_def456",
        "format": "tar.gz"
      },
```

**Edit (c), `gitClone.ref` bullet.** In the bullet that begins "**`gitClone.ref` resolution (per-session immutability).**", make two replacements. Replace

```markdown
At session creation the gateway resolves each `gitClone.ref` to an immutable commit SHA
```

with

```markdown
When the plan binds ([§14.1](#141-workspaceplan-schema-versioning) **Plan binding.**), the gateway resolves each `gitClone.ref` to an immutable commit SHA
```

and replace

```markdown
clients MUST NOT set it in the `CreateSessionRequest` (any client-supplied value is rejected at session creation with `400 WORKSPACE_PLAN_INVALID`
```

with

```markdown
clients MUST NOT set it in a plan they submit (any client-supplied value is rejected when the plan binds with `400 WORKSPACE_PLAN_INVALID`
```

**Edit (d), schema-encoding sub-paragraph.** In the indented paragraph that begins "**Schema encoding of the request/response asymmetry.**", make two replacements. Replace

```markdown
rejects `resolvedCommitSha` when present on any `sources[<n>]` entry in `CreateSessionRequest` with the field-specific error above
```

with

```markdown
rejects `resolvedCommitSha` when present on any `sources[<n>]` entry of a client-submitted plan with the field-specific error above
```

and replace

```markdown
If `ls-remote` fails at session creation, the gateway rejects the request
```

with

```markdown
If `ls-remote` fails when the plan binds, the gateway rejects the request
```

Leave the rest of §14 unchanged. The definitional sentence in SPEC-2 covers the "at session creation" clauses of the `gitClone.url` restrictions, the `gitClone.auth` object, and the `mode` field note. The `env` field note describes an outer field, and its check stays at session creation. Before landing, confirm that the `#87-file-export-model`, `#74-upload-safety`, and `#141-workspaceplan-schema-versioning` anchors resolve.

### SPEC-2 · spec/14_workspace-plan-schema.md § 14.1 WorkspacePlan Schema Versioning

SPEC-2 makes three edits.

**Edit (a), new Plan binding paragraph.** Insert the paragraph below as a new paragraph immediately after the second **Envelope terminology.** bullet (the bullet that begins "- **`CreateSessionRequest` (outer, sibling fields).**") and before the paragraph that begins "**Published JSON Schema.**". Separate it from its neighbours with one blank line on each side.

```markdown
**Plan binding.** A session binds at most one `WorkspacePlan`. The plan binds in the `CreateSessionRequest` of `POST /v1/sessions` or `POST /v1/sessions/start`, or, when that request carried none, in the optional body `{"workspacePlan": <WorkspacePlan>}` of `POST /v1/sessions/{id}/finalize` ([§15.1](15_external-api-surface.md#151-rest-api)). A finalize request without a body, or whose `workspacePlan` is absent or `null`, binds no plan, and finalize materializes the plan bound at creation, if the session has one. A finalize request whose body carries a plan for a session that already has one is rejected with `400 VALIDATION_ERROR` and `details.reason = "plan_already_set"`. Each validation of the `WorkspacePlan`'s own fields, and each `gitClone.ref` resolution, that [§14](#14-workspace-plan-schema) or this section places at session creation applies at the request that binds the plan, with the same error code and details. Validation of the `CreateSessionRequest` outer fields, such as `env`, `runtimeOptions`, and `callbackUrl`, stays at session creation. The gateway persists a plan bound at finalize in the same write that moves the session from `created` to `finalizing`. A finalize request rejected for its plan binds nothing and leaves the session in `created`. Finalize materializes only the uploads that an `uploadFile` or `uploadArchive` source of the bound plan names, and an upload that no source names is not materialized. The scope of an `uploadRef` is stated in the `uploadRef` field note in [§14](#14-workspace-plan-schema).
```

**Edit (b), layer-1 validation.** Under **Gateway validation at `POST /v1/sessions`.**, item 1, replace

```markdown
The identical validation is also performed at `POST /v1/sessions/start`.
```

with

```markdown
The identical validation is also performed at `POST /v1/sessions/start` and on a plan bound in the `POST /v1/sessions/{id}/finalize` body (**Plan binding.** above).
```

**Edit (c), replay wording.** In the **Consumer obligations by consumer type.** bullet that begins "- **Gateway reconciliation (live consumer):**", replace

```markdown
(pinned at session creation — see the `gitClone.ref` resolution field note in [§14](#14-workspace-plan-schema))
```

with

```markdown
(pinned when the plan binds — see the `gitClone.ref` resolution field note in [§14](#14-workspace-plan-schema))
```

Leave the item 2 outer-envelope text unchanged: `WORKSPACE_PLAN_INVALID` stays reserved for inner-plan schema failures.

### SPEC-3 · spec/15_external-api-surface.md § 15.1 REST API and § 15.2 MCP API

SPEC-3 edits rows in four §15.1 tables and one §15.2 table. Every edited or added row stays on one physical line.

**Edit (a), Session lifecycle endpoint table.** Under **Session lifecycle:**, make four row edits.

1. In the `GET /v1/sessions/{id}` row, replace ``pinned at session creation per [§14](14_workspace-plan-schema.md) `gitClone.ref` resolution`` with ``pinned when the plan binds per [§14](14_workspace-plan-schema.md) `gitClone.ref` resolution``.
2. Replace the `/v1/sessions/{id}/upload` row with:

```markdown
| `POST`   | `/v1/sessions/{id}/upload`    | Upload workspace content (pre-start or mid-session if enabled). The `201` response carries the `uploadRef` that an `uploadFile` source names ([§14](14_workspace-plan-schema.md) `uploadRef` field note). |
```

3. Insert the row below immediately after the `/upload` row:

```markdown
| `POST`   | `/v1/sessions/{id}/upload-archive` | Upload a workspace archive for extraction in the gateway ([Section 7.4](07_session-lifecycle.md#74-upload-safety)). The `201` response carries the `uploadRef` that an `uploadArchive` source names ([§14](14_workspace-plan-schema.md) `uploadRef` field note). |
```

4. Replace the `/v1/sessions/{id}/finalize` row with:

```markdown
| `POST`   | `/v1/sessions/{id}/finalize`  | Finalize workspace and run setup. An optional body binds the session's `workspacePlan` (see the precondition table below). |
```

**Edit (b), State-mutating endpoint preconditions table.** Make two row edits.

1. In the upload row, replace the Endpoint cell `` `POST /v1/sessions/{id}/upload`    `` with `` `POST /v1/sessions/{id}/upload`, `POST /v1/sessions/{id}/upload-archive` ``. Leave the other cells of that row unchanged. The two endpoints share this row.
2. In the `POST /v1/sessions/{id}/finalize` row's Notes cell, insert the text below immediately after "Triggers workspace materialization, setup commands, and credential-lease assignment." and before "A credential failure can surface here". Leave the rest of the cell unchanged.

```markdown
The optional request body `{"workspacePlan": <WorkspacePlan>}` binds the session's plan when its `CreateSessionRequest` carried none ([Section 14.1](14_workspace-plan-schema.md#141-workspaceplan-schema-versioning) **Plan binding.**). A plan in the body that fails validation returns `WORKSPACE_PLAN_INVALID`, `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED`, one of the `GIT_CLONE_*` codes, or `400 VALIDATION_ERROR` with `details.reason` `plan_already_set`, `invalid_upload_ref`, or `upload_ref_foreign_session`, and each of these rejections leaves the session in `created`.
```

**Edit (c), derive-failure reachability table.** Under **Derive-failure audit rows (`failureClass = derive_failure`).**, in the row whose Endpoint cell begins `` `POST /v1/sessions/{id}/interrupt`, `/upload`, `/finalize` ``, replace `` `/upload`, `/finalize` `` with `` `/upload`, `/upload-archive`, `/finalize` ``. Leave the Behavior cell unchanged.

**Edit (d), error code catalog.** Under **Error code catalog:**, make five row edits.

1. `WORKSPACE_PLAN_INVALID` row. Replace

```markdown
Inner `workspacePlan` payload on `POST /v1/sessions` (or `POST /v1/sessions/start`) failed JSON Schema validation against the published `WorkspacePlan` schema.
```

with

```markdown
Inner `workspacePlan` payload of a request that binds a plan (`POST /v1/sessions`, `POST /v1/sessions/start`, or the `POST /v1/sessions/{id}/finalize` body; see [Section 14.1](14_workspace-plan-schema.md#141-workspaceplan-schema-versioning) **Plan binding.**) failed JSON Schema validation against the published `WorkspacePlan` schema.
```

2. In each of the rows `GIT_CLONE_AUTH_UNSUPPORTED_HOST`, `GIT_CLONE_AUTH_HOST_AMBIGUOUS`, `GIT_CLONE_REF_UNRESOLVABLE`, and `GIT_CLONE_REF_RESOLVE_TRANSIENT`, replace the Description cell's lead `Session creation rejected because` with:

```markdown
A request that binds a `WorkspacePlan` (`POST /v1/sessions`, `POST /v1/sessions/start`, or `POST /v1/sessions/{id}/finalize`; see [Section 14.1](14_workspace-plan-schema.md#141-workspaceplan-schema-versioning) **Plan binding.**) was rejected because
```

3. In the `GIT_CLONE_REF_UNRESOLVABLE` row, also replace ``via `git ls-remote` at session creation`` with ``via `git ls-remote` when binding the plan``.

**Edit (e), §15.2 MCP tools table.** Under **MCP tools (client-facing):**, replace the `finalize_workspace` row with:

```markdown
| `finalize_workspace`       | Seal workspace, run setup. The optional `workspacePlan` argument binds the session's plan as the REST finalize body does ([Section 14.1](14_workspace-plan-schema.md#141-workspaceplan-schema-versioning) **Plan binding.**). |
```

Leave §15.2.1 unchanged. Its "session-creation rejection family" list stays true because creation still emits those codes.

### SPEC-4 · spec/07_session-lifecycle.md § 7.1 Normal Flow and § 7.4 Upload Safety

SPEC-4 makes three edits. Step 10 and the **Upload authorization:** paragraph stay unchanged. No step is added, so the flow numbering and the "steps 11–13" reference in **Atomicity of session creation (steps 2–8).** still resolve.

**Edit (a), §7.1 step 11.** In the flow block, replace

```text
11. Client → Gateway:    FinalizeWorkspace()
```

with

```text
11. Client → Gateway:    FinalizeWorkspace(workspacePlan?), which binds the plan when the
                         CreateSessionRequest carried none (Section 14.1 Plan binding)
```

**Edit (b), §7.1 step 12.** In the flow block, replace

```text
12. Gateway → Pod:       Stream the buffered upload content into the claimed pod's
                         /workspace/slots/{sessionId}/staging, validate staging,
                         materialize to /workspace/slots/{sessionId}/current
```

with

```text
12. Gateway → Pod:       Stream the content of the uploads that the bound plan's uploadFile
                         and uploadArchive sources name (archives extracted in the gateway,
                         Section 7.4) into the claimed pod's
                         /workspace/slots/{sessionId}/staging, validate staging, and
                         materialize the plan's sources to /workspace/slots/{sessionId}/current
```

**Edit (c), §7.4 pre-start upload paragraph.** In the paragraph that begins "Pre-start uploads are buffered in the Artifact Store", replace

```markdown
The staging→validation→promotion model applies when the gateway streams the buffered content into the claimed pod's `/workspace/slots/{sessionId}/staging` at finalize:
```

with

```markdown
The staging→validation→promotion model applies when the gateway streams the content of the uploads that the bound plan names ([Section 14.1](14_workspace-plan-schema.md#141-workspaceplan-schema-versioning) **Plan binding.**) into the claimed pod's `/workspace/slots/{sessionId}/staging` at finalize:
```

Leave the rest of the paragraph unchanged. Its next sentence already places archive extraction in the gateway.

### SPEC-5 · spec/29_communication-scenarios.md § 29.2 Session start

SPEC-5 edits steps 14 and 15.

**Edit (a), step 14.** Replace

```markdown
14. `client` → `gateway`, no register entry, the client-to-gateway session REST surface. The client calls
    `FinalizeWorkspace`, which invalidates the upload token on success
    ([§7.1](07_session-lifecycle.md#71-normal-flow)).
```

with

```markdown
14. `client` → `gateway`, no register entry, the client-to-gateway session REST surface. The client calls
    `FinalizeWorkspace`, which binds the `WorkspacePlan` it carries when the session was created without
    one ([§14.1](14_workspace-plan-schema.md#141-workspaceplan-schema-versioning) **Plan binding.**) and
    invalidates the upload token on success ([§7.1](07_session-lifecycle.md#71-normal-flow)).
```

**Edit (b), step 15.** Replace

```markdown
    ([§15.3](15_external-api-surface.md#153-internal-control-api-custom-protocol)). The gateway streams the
    buffered content into the claimed pod with the `PrepareWorkspace` RPC, which accepts the streamed files
```

with

```markdown
    ([§15.3](15_external-api-surface.md#153-internal-control-api-custom-protocol)). The gateway streams the
    buffered uploads that the bound plan's sources name into the claimed pod with the `PrepareWorkspace`
    RPC, which accepts the streamed files
```

Leave the rest of step 15 unchanged. After both edits, run the naming lint (`scripts/specshift/name`) and the citation resolver over the file.

### SPEC-6 · spec/24_lenny-ctl-command-reference.md § 24 and § 24.17, spec/26_reference-runtime-catalog.md § 26.2

SPEC-6 makes three edits in §24 and four in §26.2. §19 decision 12 is not edited.

**Edit (a), §24 overview.** In the **Session commands** bullet near the top of §24, replace

```markdown
map to the **MCP** API ([§15.2](15_external-api-surface.md#152-mcp-api)).
```

with

```markdown
map to the **MCP** API ([§15.2](15_external-api-surface.md#152-mcp-api)), except where [§24.17](#2417-session-operations) names a REST mapping.
```

**Edit (b), §24.17 lead.** Replace the first sentence of §24.17

```markdown
Session commands route through the **MCP** client SDK, not REST.
```

with

```markdown
Session commands route through the **MCP** client SDK except where a row's mapping names REST endpoints; `lenny session new` with `--workspace` or `--file` stages the workspace over REST, because an upload requires the `uploadToken` that the REST create response issues ([§7.4](07_session-lifecycle.md#74-upload-safety)).
```

**Edit (c), §24.17 `lenny session new` row.** Replace the API / SDK mapping cell ``MCP `lenny/create_session` + stream`` with:

```markdown
MCP `lenny/create_session` + stream; with `--workspace` or `--file`: REST `POST /v1/sessions`, `/upload` or `/upload-archive`, `/finalize` with a `workspacePlan`, and `/start`
```

The row stays one physical line.

**Edit (d), §26.2 Reference WorkspacePlan lead.** Replace

```markdown
**Reference `WorkspacePlan`.** The following minimal plan is used by the `lenny session new --attach` CLI ([§24.17](24_lenny-ctl-command-reference.md#2417-session-operations)) when the client passes `--workspace=<local-path>`:
```

with

```markdown
**Reference `WorkspacePlan`.** The following minimal plan is used by the `lenny session new --attach` CLI ([§24.17](24_lenny-ctl-command-reference.md#2417-session-operations)) when the client passes `--workspace=<local-path>`. The CLI creates the session with `POST /v1/sessions` and binds this plan in the `POST /v1/sessions/{id}/finalize` body ([§14.1](14_workspace-plan-schema.md#141-workspaceplan-schema-versioning) **Plan binding.**):
```

**Edit (e), §26.2 JSON block.** Replace the whole JSON block that follows the lead (the block that begins `"pool": "<runtime-name>-default",`) with the block below. The create envelope is dropped because the CLI does not send its fields.

````markdown
```json
{
  "workspacePlan": {
    "$schema": "https://schemas.lenny.dev/workspaceplan/v1.json",
    "schemaVersion": 1,
    "sources": [
      { "type": "uploadArchive", "pathPrefix": ".", "uploadRef": "<uploadRef>", "format": "tar.gz" }
    ],
    "setupCommands": []
  }
}
```
````

**Edit (f), §26.2 paragraph after the JSON block.** Replace

```markdown
uploads it to the gateway via the upload API ([§15.1](15_external-api-surface.md#151-rest-api)), and references the resulting upload ID.
```

with

```markdown
uploads it with `POST /v1/sessions/{id}/upload-archive` ([§15.1](15_external-api-surface.md#151-rest-api)), and names the returned `uploadRef` in the `uploadArchive` source.
```

**Edit (g), §26.2 example paragraph.** In the paragraph after **Example session invocation (applies to all four).** and its code block, replace

```markdown
The CLI uploads the workspace, creates the session via the gateway REST API, then attaches
```

with

```markdown
The CLI creates the session via the gateway REST API, uploads the workspace, binds the plan at finalize, and starts the session, then attaches
```

Keep the rest of that sentence verbatim, including "via the MCP WebSocket". The attach transport is a recorded defect this proposal does not stage (see the summary). Before landing, confirm that the `#2417-session-operations`, `#74-upload-safety`, and `#141-workspaceplan-schema-versioning` anchors resolve.

## Spec files touched

- `spec/14_workspace-plan-schema.md` (SPEC-1, SPEC-2)
- `spec/15_external-api-surface.md` (SPEC-3)
- `spec/07_session-lifecycle.md` (SPEC-4)
- `spec/29_communication-scenarios.md` (SPEC-5)
- `spec/24_lenny-ctl-command-reference.md` (SPEC-6)
- `spec/26_reference-runtime-catalog.md` (SPEC-6)
