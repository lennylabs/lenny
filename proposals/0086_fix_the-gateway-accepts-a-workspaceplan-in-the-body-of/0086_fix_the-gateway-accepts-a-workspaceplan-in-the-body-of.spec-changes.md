# Spec changes: Finalize-time WorkspacePlan bind has no specification contract

These edits are staged. Applying them is the implementation's work, under the
implementation checklist.

## Design (as the spec must state it)

The specification adopts the plan-reference model with a finalize-time bind, which is the model the tree runs. Each rule below has one normative home in the staged text:

1. **Plan binding.** A session binds at most one `WorkspacePlan`. The bind points, the finalize body, the `plan_already_set` rejection, the timing of plan validation and `gitClone.ref` resolution, the same-write persistence, the outcome of a rejected finalize, and the rule that unreferenced uploads are not materialized all live in the new §14.1 **Plan binding.** paragraph (SPEC-2).
2. **The `uploadRef` contract.** An `uploadRef` is an opaque reference to stored upload content, scoped to the session for which the content was stored. The response it comes from, its scope, the two rejection reasons, and the consequence that a create-time plan carries no upload source live in the new §14 `uploadRef` field note (SPEC-1).
3. **Pin timing.** `gitClone.ref` pins when the plan binds. The plan binds once, so the per-session immutability guarantee is unchanged. The §14 bullet that states the guarantee carries the timing (SPEC-1), and the definitional sentence in **Plan binding.** covers every other §14 and §14.1 clause that says "at session creation".
4. **Setup timing.** Setup commands run per session in the session's slot. The opening of §7.5 is the home (SPEC-7 edit (a)), and §5.1, §5.2, and §16.1 cite it (SPEC-7 edits (b) to (e)).
5. **Runtime workspace defaults.** The bound plan carries the runtime's `workspaceDefaults`. The rules live in the new §14.1 **Runtime workspace defaults.** paragraph, §5.1 **Derived Runtime Instantiation**, and §7.5 **Command policy:** (SPEC-8).

Every other site cites these homes. §15.1 and §15.2 carry the endpoint, precondition, and catalog rows (SPEC-3), because catalog rows are read one at a time and the definitional sentence covers only §14 and §14.1. §4.9 **`hostPatterns`** and §6.1 **Demotion on demand.** cite **Plan binding.** for their timing, and §4.6.1 and §12.6 drop the claim-time `requiresDemotion` option and the pod-creation `WorkspacePlan` (SPEC-2 edits (d) to (g)). §7.1, §7.4, and §29.2 narrow "the buffered content" to the uploads the bound plan names (SPEC-4, SPEC-5). The §24.17 `lenny session new` row states the REST flow the CLI runs, and §26.2 and §26.3 cite it (SPEC-6).

The spec does not declare `uploadRef` a §28.5.3 `LennyBlobURI`, and it does not restate the `UploadResponse` fields other than `uploadRef`. The OpenAPI document is the authoritative REST schema under §15.2.1 item 4.

## Edge cases and accepted failure modes

- **A finalize body carries a plan for a session that already has one.** **Plan binding.** rejects it with `plan_already_set`. The session stays `created`.
- **A finalize body carries `"workspacePlan": null`, or no body is sent.** **Plan binding.** governs it.
- **A create-time plan names an upload source.** No upload for the session can exist when the create request is validated, so the `uploadRef` field note rejects every such source, with `invalid_upload_ref` or `upload_ref_foreign_session`.
- **A client uploads content that no source of the bound plan names.** **Plan binding.** states that it is not materialized. No warning or rejection is emitted.
- **A finalize rejected for its plan.** **Plan binding.** states that the rejection binds nothing and leaves the session `created`, so the client can correct the plan and finalize again within the `created` window.
- **A `gitClone` ref moves between creation and a finalize-time bind.** The ref pins at the bind, and later materializations of the session clone the pinned SHA.
- **A delegation child plan.** The gateway writes it onto the child row with `uploadRef` values it minted under the child session (§8.7). It is not a client-submitted plan, so the `uploadRef` rejection rule does not apply to it.
- **A derived or replayed session.** Its creating request binds no plan, so its plan binds at finalize (**Plan binding.**). How the copied snapshot composes with that plan is outside this proposal (see the summary defect **The spec does not state how a copied snapshot composes with a plan bound at finalize.**).
- **A runtime's `workspaceDefaults` change after a session's plan binds.** **Runtime workspace defaults.** rule 2 governs it.
- **A resume restores a checkpoint.** §7.3 step 3e replays the checkpoint without the plan, so the runtime defaults are not re-applied.

## Staged edits

### SPEC-1 · spec/14_workspace-plan-schema.md § 14 Workspace Plan Schema

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

**Edit (e), per-slot scope note.** Delete the paragraph that begins "**Per-slot shared-template scope note.**", together with the blank line that follows it.

Leave the rest of §14 unchanged. The definitional sentence in SPEC-2 covers the "at session creation" clauses of the `gitClone.url` restrictions, the `gitClone.auth` object, and the `mode` field note. The `env` field note describes an outer field, and its check stays at session creation. Before landing, confirm that the `#87-file-export-model`, `#74-upload-safety`, and `#141-workspaceplan-schema-versioning` anchors resolve.

### SPEC-2 · spec/14_workspace-plan-schema.md § 14.1 WorkspacePlan Schema Versioning, spec/04_system-components.md § 4.6.1 and § 4.9, spec/06_warm-pod-model.md § 6.1, spec/12_storage-architecture.md § 12.6, spec/15_external-api-surface.md § 15.1 and § 15.2.1, spec/16_observability.md § 16.1

**Edit (a), new Plan binding paragraph.** Insert the paragraph below as a new paragraph immediately after the second **Envelope terminology.** bullet (the bullet that begins "- **`CreateSessionRequest` (outer, sibling fields).**") and before the paragraph that begins "**Published JSON Schema.**". Separate it from its neighbours with one blank line on each side.

```markdown
**Plan binding.** A session binds at most one `WorkspacePlan`. The plan binds in the `CreateSessionRequest` of `POST /v1/sessions` or `POST /v1/sessions/start`, or, when the session was created without one, at `POST /v1/sessions/{id}/finalize`, whose optional body `{"workspacePlan": <WorkspacePlan>}` carries the client's plan ([§15.1](15_external-api-surface.md#151-rest-api)). A delegated child's plan binds when the gateway creates the child session ([§8.7](08_recursive-delegation.md#87-file-export-model)). A `POST /v1/sessions/start` request without a `workspacePlan` binds the empty plan. A finalize request without a body, or whose `workspacePlan` is absent or `null`, carries no client plan: finalize materializes the plan bound at creation when the session has one, and otherwise binds the empty plan (see **Runtime workspace defaults.** below). A finalize request whose body carries a plan for a session that already has one is rejected with `400 VALIDATION_ERROR` and `details.reason = "plan_already_set"`. Each validation of the `WorkspacePlan`'s own fields, and each `gitClone.ref` resolution, that [§14](#14-workspace-plan-schema) or this section places at session creation applies at the request that binds the plan, with the same error code and details. Validation of the `CreateSessionRequest` outer fields, such as `env`, `runtimeOptions`, and `callbackUrl`, stays at session creation. The gateway persists a plan bound at finalize in the same write that moves the session from `created` to `finalizing`. A finalize request rejected for its plan binds nothing and leaves the session in `created`. Finalize materializes only the uploads that an `uploadFile` or `uploadArchive` source of the bound plan names, and an upload that no source names is not materialized. The scope of an `uploadRef` is stated in the `uploadRef` field note in [§14](#14-workspace-plan-schema).
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

**Edit (d), §4.9 host match timing.** In spec/04_system-components.md, in the paragraph that begins "**`hostPatterns` — VCS-pool-only field.**", replace

```markdown
At session creation the gateway parses the HTTPS URL's host
```

with

```markdown
When the plan binds ([§14.1](14_workspace-plan-schema.md#141-workspaceplan-schema-versioning) **Plan binding.**), the gateway parses the HTTPS URL's host
```

**Edit (e), demotion timing.** Make the changes below in spec/06_warm-pod-model.md §6.1 (items 1 to 4), the spec/15_external-api-surface.md §15.1 **Error code catalog** (item 5) and §15.2.1 (item 7), and the spec/16_observability.md §16.1 metrics table (item 6). Leave the rest of each paragraph and row unchanged. Each edited table row stays one physical line.

1. In the paragraph that begins "**Demotion on demand.**", replace

   ```markdown
   if the workspace plan includes files matching any of these glob patterns, the gateway sets `requiresDemotion: true` on the `ClaimOpts` and the adapter calls the `DemoteSDK` RPC ([Section 4.7](04_system-components.md#47-runtime-adapter)) to tear down the pre-connected SDK process, transitions the pod back to `idle` (pod-warm),
   ```

   with

   ```markdown
   if the session's bound workspace plan ([Section 14.1](14_workspace-plan-schema.md#141-workspaceplan-schema-versioning) **Plan binding.**) includes files matching any of these glob patterns, the gateway, before it materializes the session's workspace, calls the `DemoteSDK` RPC ([Section 4.7](04_system-components.md#47-runtime-adapter)) to tear down the pre-connected SDK process,
   ```

2. In the paragraph that begins "**Existing idle SDK-warm pods are NOT drained on circuit-breaker activation.**", replace

   ```markdown
   served through the normal demotion path: the gateway sets `requiresDemotion: true` at claim time and the adapter calls `DemoteSDK` before workspace materialization.
   ```

   with

   ```markdown
   served through the normal demotion path (**Demotion on demand.** above).
   ```

3. In the `preConnect` compatibility table row that begins "| `session`, `maxConcurrentSessions: 1`, `recycle.enabled: false` |", delete the sentence " Demotion via `sdkWarmBlockingPaths` is evaluated at claim time.".

4. In the row that begins "| `session`, `maxConcurrentSessions: 1`, `recycle.enabled: true` |", replace

   ```markdown
   The `sdkWarmBlockingPaths` check is evaluated per session at dispatch time (each session may upload different files); if a file in the new session's workspace plan matches a blocking pattern, the gateway sets `requiresDemotion: true` on the session's `ClaimOpts` and the adapter calls `DemoteSDK` before materializing that session's workspace, then proceeds via the pod-warm path for that session only.
   ```

   with

   ```markdown
   Each session's bound plan is checked under **Demotion on demand.** above, and a demotion applies to that session only.
   ```

5. In the §15.1 row that begins "| `SDK_DEMOTION_NOT_SUPPORTED`", replace "Session creation failed because" with "The session failed because".

6. In the §16.1 row that begins "| SDK-warm demotions (`lenny_warmpool_sdk_demotions_total`", delete " before session assignment".

7. In the §15.2.1 list item that begins "- All error classes:", delete "`SDK_DEMOTION_NOT_SUPPORTED`, " from the session-creation rejection family list.

**Edit (f), §4.6.1 claim options.** In spec/04_system-components.md §4.6.1, in the `ClaimPod` bullet under "**`PodLifecycleManager`**", delete "`requiresDemotion` (true when workspace plan includes `sdkWarmBlockingPaths` — gateway signals that the claimed SDK-warm pod must be demoted before use), ", so that the bullet reads "`ClaimOpts` includes optional `priority`, optional `clusterID` …". In the paragraph that begins "**Scaling:**", delete " at claim time" from "demotion to pod-warm happens on demand at claim time".

**Edit (g), §12.6 `PodSpec` and `ClaimOpts` rows.** In spec/12_storage-architecture.md §12.6, in the table row that begins "| `PodSpec` |", delete "`WorkspacePlan`, ". In the table row that begins "| `ClaimOpts` |", delete "`RequiresDemotion` (bool), ". Each row stays one physical line.

Before landing, confirm that the `#47-runtime-adapter` and `#141-workspaceplan-schema-versioning` anchors resolve.

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
The optional request body `{"workspacePlan": <WorkspacePlan>}` binds the session's plan when its `CreateSessionRequest` carried none ([Section 14.1](14_workspace-plan-schema.md#141-workspaceplan-schema-versioning) **Plan binding.**).
```

**Edit (c), derive-failure reachability table.** Under **Derive-failure audit rows (`failureClass = derive_failure`).**, in the row whose Endpoint cell begins `` `POST /v1/sessions/{id}/interrupt`, `/upload`, `/finalize` ``, replace `` `/upload`, `/finalize` `` with `` `/upload`, `/upload-archive`, `/finalize` ``. Leave the Behavior cell unchanged.

**Edit (d), error code catalog.** Under **Error code catalog:**, make five row edits.

1. `WORKSPACE_PLAN_INVALID` row. Replace

```markdown
Inner `workspacePlan` payload on `POST /v1/sessions` (or `POST /v1/sessions/start`) failed JSON Schema validation against the published `WorkspacePlan` schema.
```

with

```markdown
Inner `workspacePlan` payload of a request that binds a plan ([Section 14.1](14_workspace-plan-schema.md#141-workspaceplan-schema-versioning) **Plan binding.**) failed JSON Schema validation against the published `WorkspacePlan` schema.
```

2. In each of the rows `GIT_CLONE_AUTH_UNSUPPORTED_HOST`, `GIT_CLONE_AUTH_HOST_AMBIGUOUS`, `GIT_CLONE_REF_UNRESOLVABLE`, and `GIT_CLONE_REF_RESOLVE_TRANSIENT`, replace the Description cell's lead `Session creation rejected because` with:

```markdown
A request that binds a `WorkspacePlan` ([Section 14.1](14_workspace-plan-schema.md#141-workspaceplan-schema-versioning) **Plan binding.**) was rejected because
```

3. In the `GIT_CLONE_REF_UNRESOLVABLE` row, also replace ``via `git ls-remote` at session creation`` with ``via `git ls-remote` when binding the plan``.

**Edit (e), §15.2 MCP tools table.** Under **MCP tools (client-facing):**, replace the `finalize_workspace` row with:

```markdown
| `finalize_workspace`       | Seal workspace, run setup. The optional `workspacePlan` argument binds the session's plan as the REST finalize body does ([Section 14.1](14_workspace-plan-schema.md#141-workspaceplan-schema-versioning) **Plan binding.**). |
```

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

SPEC-5 edits steps 14, 15, and 17.

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

Leave the rest of step 15 unchanged.

**Edit (c), step 17.** Replace

```markdown
    carries the setup commands the gateway has validated against the runtime's `setupCommandPolicy`
    ([§4.7](04_system-components.md#47-runtime-adapter),
    [§7.5](07_session-lifecycle.md#75-setup-commands)).
```

with

```markdown
    carries the session's setup commands, checked as
    [§7.5](07_session-lifecycle.md#75-setup-commands) **Command policy:** states
    ([§4.7](04_system-components.md#47-runtime-adapter)).
```

After the edits, run the naming lint (`scripts/specshift/name`) and the citation resolver over the file.

### SPEC-6 · spec/24_lenny-ctl-command-reference.md § 24 and § 24.17, spec/26_reference-runtime-catalog.md § 26.2 and § 26.3

SPEC-6 edits §24, §24.17, §26.2, and §26.3. §19 decision 12 is not edited.

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
Session commands route through the **MCP** client SDK except where a row's mapping names REST endpoints; `lenny session new` with `--workspace` or `--file` stages the workspace over REST.
```

**Edit (c), §24.17 `lenny session new` and `lenny session list` rows.** In the `lenny session new` row, replace the API / SDK mapping cell ``MCP `lenny/create_session` + stream`` with:

```markdown
MCP `lenny/create_session` + stream; with `--workspace` or `--file`: REST `POST /v1/sessions`, `/upload` or `/upload-archive`, `/finalize` with a `workspacePlan`, and `/start`
```

In the `lenny session list` row, delete the sentence "This is the one session command that also works over REST for non-interactive callers." Both rows stay one physical line.

**Edit (d), §26.2 Reference WorkspacePlan lead.** Replace

```markdown
**Reference `WorkspacePlan`.** The following minimal plan is used by the `lenny session new --attach` CLI ([§24.17](24_lenny-ctl-command-reference.md#2417-session-operations)) when the client passes `--workspace=<local-path>`:
```

with

```markdown
**Reference `WorkspacePlan`.** The following minimal plan is used by the `lenny session new --attach` CLI ([§24.17](24_lenny-ctl-command-reference.md#2417-session-operations)) when the client passes `--workspace=<local-path>`, as the `POST /v1/sessions/{id}/finalize` body ([§14.1](14_workspace-plan-schema.md#141-workspaceplan-schema-versioning) **Plan binding.**):
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
The CLI stages the workspace over the gateway REST API, then attaches
```

Keep the rest of that sentence verbatim, including "via the MCP WebSocket". The attach transport is a recorded defect this proposal does not stage (see the summary).

**Edit (h), §26.3 `claude-code` example paragraph.** In the paragraph after the `claude-code` **Example session invocation.** code block, replace

```markdown
The CLI uploads the repo, creates the session, and streams
```

with

```markdown
The CLI stages the repo over REST ([§24.17](24_lenny-ctl-command-reference.md#2417-session-operations)) and streams
```

Keep the rest of that sentence verbatim.

**Edit (i), §26.3 `claude-code` bootstrap step 3.** Under **Bootstrap behavior.**, in step 3, replace `On session claim:` with `At finalize and start ([§7.1](07_session-lifecycle.md#71-normal-flow)):`, replace the step's closing `` states; emits `session.created`. `` with `states.`, and keep the rest of the step verbatim.

Before landing, confirm that the `#2417-session-operations`, `#141-workspaceplan-schema-versioning`, and `#71-normal-flow` anchors resolve.

### SPEC-7 · spec/07_session-lifecycle.md § 7.5 Setup Commands, spec/05_runtime-registry-and-pool-model.md § 5.1 and § 5.2, spec/16_observability.md § 16.1 and § 16.5

Edit (a) is the normative home of setup timing, and edits (b) to (e) cite it.

**Edit (a), §7.5 opening sentence.** Under `### 7.5 Setup Commands`, replace

```markdown
Run after workspace finalization, before session start.
```

with

```markdown
Setup commands run in the session's `/workspace/slots/{sessionId}/current`, after the session's workspace is materialized there and before the runtime starts the session. A pod that serves more than one session, concurrently or in sequence, runs each session's setup commands for that session. [Section 7.3](#73-retry-and-resume) step 3e states when a resume runs them, and [Section 6.2](06_warm-pod-model.md#62-pod-state-machine) states the outcome of a setup failure.
```

Leave the **Constraints:** list unchanged except for the bullet SPEC-8 edit (c) replaces.

**Edit (b), §5.1 Setup Commands and Policy.** Under `#### Setup Commands and Policy`, replace

```markdown
Setup commands run after workspace materialization and before runtime starts. While executing, pod in INIT state, not READY. Pod failure during setup causes pod replacement before warm pool entry. Setup commands run once per pod, not per task. Per-task setup belongs in the runtime's handling of the session's first message ([Section 5.2](#52-pool-configuration-and-execution-modes)).
```

with

```markdown
Setup commands run as [Section 7.5](07_session-lifecycle.md#75-setup-commands) states. `setupPolicy` bounds a session's setup phase:
```

Leave the `setupPolicy` YAML block and the sentence after it unchanged.

**Edit (c), §5.1 Normative Merge Algorithm, `setupCommandPolicy` row.** In the Notes cell, replace `Derived may restrict allowlist; gateway enforces at pod startup` with `Derived may restrict allowlist; the gateway enforces the resolved policy as [Section 7.5](07_session-lifecycle.md#75-setup-commands) **Command policy:** states`. The row stays one physical line.

**Edit (d), §5.2 Execution Modes, Recycle lifecycle paragraph.** In the paragraph that begins "**Recycle lifecycle (`recycle.enabled: true`).**", replace its last sentence

```markdown
`setupCommands` run once per pod at start; per-session setup belongs in the runtime's handling of the session's first message, because a runtime process kept across sessions initializes once per pod.
```

with

```markdown
[Section 7.5](07_session-lifecycle.md#75-setup-commands) states when `setupCommands` run. Initialization of a runtime process kept across sessions that is specific to one session belongs in the runtime's handling of that session's first message, because the process does not reinitialize for each session.
```

**Edit (e), §16.1 warmup-failure row and §16.5 `WarmPoolReplenishmentFailing` row.** In the §16.1 row for `lenny_warmpool_warmup_failure_total`, replace

```markdown
`error_type`: `image_pull_error`, `setup_command_failed`, `resource_quota_exceeded`, `node_pressure` — increments each time a pod fails to reach `idle` state during warm-up;
```

with

```markdown
`error_type`: `image_pull_error`, `resource_quota_exceeded`, and `node_pressure` count a pod that fails to reach `idle` state during warm-up, and `setup_command_failed` counts a session whose setup commands fail ([Section 7.5](07_session-lifecycle.md#75-setup-commands));
```

In the §16.5 `WarmPoolReplenishmentFailing` row, replace

```markdown
pods are failing to reach `idle` state during warm-up (image pull errors, setup command failures, or resource pressure).
```

with

```markdown
[Section 16.1](#161-metrics) states what each `error_type` counts.
```

Both rows stay one physical line. Leave the rest of each row unchanged.

Before landing, run an exact search for `once per pod` and `INIT state` under `spec/` and confirm that no remaining hit concerns setup commands, and confirm that the `#73-retry-and-resume`, `#62-pod-state-machine`, `#75-setup-commands`, and `#161-metrics` anchors resolve.

### SPEC-8 · spec/14_workspace-plan-schema.md § 14.1, spec/05_runtime-registry-and-pool-model.md § 5.1, spec/07_session-lifecycle.md § 7.5

SPEC-8 makes three edits.

**Edit (a), new Runtime workspace defaults paragraph.** In §14.1, insert the paragraph below immediately after the **Plan binding.** paragraph that SPEC-2 edit (a) inserts, with one blank line on each side.

```markdown
**Runtime workspace defaults.** The plan a session binds carries the `workspaceDefaults` of the session's runtime ([§5.1](05_runtime-registry-and-pool-model.md#51-runtime)):

1. When the plan binds (**Plan binding.** above), the gateway reads the effective `workspaceDefaults` of the runtime the session runs, after the [§4.8](04_system-components.md#48-gateway-policy-engine) routing interceptors and [§10.7](10_gateway-internals.md#107-experiment-primitives) experiment routing have selected it and after the §5.1 base-then-derived merge and composes them into the plan. Each default file becomes an `inlineFile` source, and each default setup command becomes a `setupCommands` entry that keeps its `timeoutSeconds`. The defaults precede the plan's own sources and setup commands in the materialization order of §5.1 **Derived Runtime Instantiation**. A delegated child's file-export sources ([§8.7](08_recursive-delegation.md#87-file-export-model)) follow the defaults.
2. The gateway stores the composed plan, and `GET /v1/sessions/{id}` returns it. Every later materialization of the session reads the stored plan, including the rebuild in [§7.3](07_session-lifecycle.md#73-retry-and-resume) step 3e, so a change to the runtime's `workspaceDefaults` after the plan binds does not change the session's workspace.
3. A default file and a plan source that write the same path follow the **Path collision rule** below.

[§7.5](07_session-lifecycle.md#75-setup-commands) **Command policy:** states which setup commands `setupCommandPolicy` checks and counts.
```

**Edit (b), §5.1 Derived Runtime Instantiation.** Replace

```markdown
`workspaceDefaults` is the workspace plan the gateway materializes into every pod. Small files inline in `workspaceDefaults`, large files via MinIO reference.
```

with

```markdown
`workspaceDefaults` holds the files and setup commands the gateway composes into every session's workspace plan ([Section 14.1](14_workspace-plan-schema.md#141-workspaceplan-schema-versioning) **Runtime workspace defaults.**). Each `workspaceDefaults.files` entry carries its content inline, and registration rejects an entry that carries a `ref` or that is not a valid [Section 14](14_workspace-plan-schema.md#14-workspace-plan-schema) `inlineFile` source. A runtime ships larger files in its image or through `sharedAssets`.
```

**Edit (c), §7.5 Setup Commands.** Replace

```markdown
- Max commands per session enforced (`setupCommandPolicy.maxCommands`)
```

with

```markdown
- Max commands per session enforced (`setupCommandPolicy.maxCommands`; **Command policy:** below states which commands count)
```

and replace

```markdown
**Command policy:** The gateway validates every setup command against the Runtime's `setupCommandPolicy` before forwarding to the pod:
```

with

```markdown
**Command policy:** Before forwarding to the pod, the gateway validates every setup command of a client-submitted `WorkspacePlan` against the Runtime's `setupCommandPolicy` and counts those commands toward `maxCommands`. The setup commands that the runtime's own `workspaceDefaults` contributes ([Section 5.1](05_runtime-registry-and-pool-model.md#51-runtime)) are exempt from both checks, and **Shell-free execution (`shell: false`):** below applies to every setup command. The policy modes are:
```

Before landing, confirm that the `#51-runtime`, `#48-gateway-policy-engine`, `#107-experiment-primitives`, `#73-retry-and-resume`, `#75-setup-commands`, `#87-file-export-model`, `#14-workspace-plan-schema`, and `#141-workspaceplan-schema-versioning` anchors resolve.

## Spec files touched

- `spec/14_workspace-plan-schema.md` (SPEC-1, SPEC-2, SPEC-8)
- `spec/15_external-api-surface.md` (SPEC-2, SPEC-3)
- `spec/07_session-lifecycle.md` (SPEC-4, SPEC-7, SPEC-8)
- `spec/05_runtime-registry-and-pool-model.md` (SPEC-7, SPEC-8)
- `spec/29_communication-scenarios.md` (SPEC-5)
- `spec/16_observability.md` (SPEC-2, SPEC-7)
- `spec/04_system-components.md` (SPEC-2)
- `spec/06_warm-pod-model.md` (SPEC-2)
- `spec/12_storage-architecture.md` (SPEC-2)
- `spec/24_lenny-ctl-command-reference.md` (SPEC-6)
- `spec/26_reference-runtime-catalog.md` (SPEC-6)
