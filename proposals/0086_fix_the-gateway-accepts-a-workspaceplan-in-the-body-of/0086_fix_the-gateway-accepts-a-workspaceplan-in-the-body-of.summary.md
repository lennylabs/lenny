# Summary: Finalize-time WorkspacePlan bind has no specification contract

## Summary

**Problem statement.** The specification has no contract for how content uploaded during a session's `created` window reaches the workspace. §7.1, §7.4, and §29.2 describe finalize streaming every buffered upload with no plan reference. The §14 `uploadFile` and `uploadArchive` sources name uploads by an `uploadRef` that nothing defines, and a create-time plan cannot name an upload because the upload token is issued at creation. The tree binds a plan in an optional `POST /v1/sessions/{id}/finalize` body that no spec text, OpenAPI operation, or reference document defines, although the Go SDK, the CLI, and the playground depend on it. The spec contradicts that bind wherever it states pin timing, the plan envelope, or error scope. The `uploadRef` scope check runs only at finalize, so a create-time plan can name a blob staged for another session of the same tenant.

**What changes.**

- `spec/14_workspace-plan-schema.md`: a §14 `uploadRef` field note and a §14.1 **Plan binding.** paragraph become the homes of the upload contract and the finalize-time bind, and the `gitClone.ref` pin wording follows the bind (SPEC-1, SPEC-2). The §4.9 `hostPatterns` host match and the §6.1 SDK-warm demotion follow the bind, and §4.6.1 and §12.6 drop the claim-time `requiresDemotion` option and the pod-creation `WorkspacePlan` (SPEC-2).
- `spec/15_external-api-surface.md`: the §15.1 endpoint, precondition, derive-failure, and error-catalog rows and the §15.2 `finalize_workspace` row describe the bind and add `/upload-archive` (SPEC-3).
- `spec/07_session-lifecycle.md`, `spec/29_communication-scenarios.md`, `spec/24_lenny-ctl-command-reference.md`, and `spec/26_reference-runtime-catalog.md`: the flow narratives and the CLI reference describe the finalize-time bind the tree runs (SPEC-4, SPEC-5, SPEC-6).
- `pkg/gateway/sessionserver`: the `uploadRef` scope check moves into the shared plan resolver, so it applies to every client-submitted plan at every bind point (CODE-1).
- MCP tool schema, OpenAPI document, `schemas/workspaceplan-v1.json`, and SDK and gateway comments: the published surfaces declare the finalize body and the opaque `uploadRef` (CODE-2, CODE-3, CODE-4).
- `docs/` and the test suites: reader-facing pages and tests at tiers 1, 2, 3, and 9 follow the amended contract (DOCS-1, TEST-1).
- `spec/07_session-lifecycle.md`, `spec/05_runtime-registry-and-pool-model.md`, and `spec/16_observability.md`: §7.5 becomes the home of setup timing, and §5.1, §5.2, and §16.1 cite it (SPEC-7). `schemas/lenny-adapter.proto` and `pkg/adapter/workspace/setup.go` comments follow (CODE-5).
- `spec/14_workspace-plan-schema.md`, `spec/05_runtime-registry-and-pool-model.md`, and `spec/07_session-lifecycle.md`: a §14.1 **Runtime workspace defaults.** paragraph and edits to §5.1 **Derived Runtime Instantiation** and §7.5 **Command policy:** (SPEC-8).
- `pkg/gateway/runtime/runtimestore`, `pkg/gateway/sessionserver`, `pkg/gateway/mcpfabric/delegation`, and `pkg/gateway/externalapi`: binding requests compose the runtime defaults into the stored plan, and registration checks them (CODE-6). Tests at tiers 1 and 4 pin both (TEST-2).

**Decisions.**

- The spine is the minimal design: specify the finalize-time bind the tree runs and every shipped consumer uses, and change code only where the problem requires it.
- A session binds at most one plan, at creation or, when creation carried none, at finalize. `plan_already_set` stays.
- `uploadRef` is opaque and scoped to the session for which its content was stored. The spec does not declare it a §28.5.3 `LennyBlobURI`, because the emitted URI has four segments and §28.5.3 states three.
- The scope check lives once, in the shared resolver, and runs right after `workspaceplan.Parse`. Create-time callers pass an empty binding session ID, so every create-time upload source is rejected without a separate rule.
- No new error code or reason. Both `invalid_upload_ref` and `upload_ref_foreign_session` stay under `400 VALIDATION_ERROR`, because both are already emitted and tested. `WORKSPACE_PLAN_INVALID` stays reserved for inner-plan schema failures.
- Unreferenced uploads are not materialized, and nothing detects or reports them.
- `gitClone.ref` pins when the plan binds. The plan binds once, so per-session immutability is unchanged.
- One definitional sentence in **Plan binding.** moves every §14 and §14.1 inner-plan "at session creation" rule to the binding request. Only timing guarantees, envelope-naming text, and §15.1 catalog rows are edited individually, because catalog rows are read one at a time.
- A finalize-bound plan persists in the `created` → `finalizing` write, and a plan rejection leaves the session `created`.
- `/upload-archive` shares the `/upload` precondition row, matching the shared `session.EndpointUpload` rule.
- MCP parity uses the existing argument forwarding plus a hand-written finalize input schema. `mcpschemagen` would emit a required `id` where the tool takes `sessionId`.
- The OpenAPI document stays the authoritative list of `UploadResponse` fields, and the spec restates only `uploadRef`.
- Setup timing is stated once, as the per-session default at the opening of §7.5, with no pod-level prohibition, so an opt-in warm-time mode such as proposal 0077's adds a clause rather than reversing one. No production code changes, because both bind paths already run setup per session.
- Runtime `workspaceDefaults` are composed into the stored plan when the plan binds. Every binding request already writes the plan, and every materialization path and the §6.1 SDK-warm demotion check already read the stored plan, so the defaults are pinned with no new column and no reader change. Composing at each materialization would read live runtime configuration at resume, and a separate defaults snapshot would need a migration and a composition step in every reader.
- The defaults precede the plan's own entries inside one `sources` array, so the existing §14.1 **Path collision rule** settles a default-versus-client path conflict and its warning.
- `workspaceDefaults.files` entries carry inline content only. §12.8 states that v1 has no cross-session MinIO references, no API stores a runtime-scoped blob, and the §14 upload sources accept only an `uploadRef` scoped to the binding session. Larger runtime files belong in the image or in `sharedAssets`.
- `setupCommandPolicy` and `maxCommands` bound client-submitted commands only. The runtime author registers the policy and the defaults through the same admin API, so checking the defaults would fail every session of a runtime whose allowlist omits its own defaults, and counting them would shrink the client's budget by a deployer-set amount.
- A runtime-resolution error other than not-found fails the binding request, because a plan bound without its defaults stays without them for the session's life. A not-found runtime composes nothing, because the creation path validates the runtime's existence, as `enforceSetupCommandPolicy` already assumes.

**Watch out for.**

- Do not retire the finalize body to match the current spec text. The Go SDK, `lenny session new --workspace/--file`, and the playground all depend on it.
- The create and create-and-start callers of `resolveSubmittedPlan` must pass an empty binding session ID. The session ID does not exist yet when the plan is validated, and CODE-1's empty-ID guard is the fail-closed predicate.
- The `uploadRef` check must run before `checkGitCloneAuthBindings` and `PinCommitSHAs`, so a foreign ref fails before any `ls-remote`.
- Each binding request composes the defaults after `enforceSetupCommandPolicy`, so the policy sees only the client's commands, and after routing has fixed the session's runtime (see CODE-6 step 2 and step 3).
- `POST /v1/sessions` composes only when its request carries a plan. A defaults-only plan stored at create would make a later finalize body fail `plan_already_set`.
- Finalize composes only for a row without a plan, so a create-bound plan is never composed twice.
- Tier 1 cases that assert an empty `row.WorkspacePlan` after finalize must run against a runtime without `workspaceDefaults`.
- The definitional sentence in **Plan binding.** covers only the `WorkspacePlan`'s own fields. The `env` blocklist, `runtimeOptions`, and callback validation stay at session creation, which is where the code runs them.
- Every edited or added §15.1, §15.2, and §24.17 table row stays one physical line.
- The OpenAPI component is `FinalizeRequest`. `FinalizeWorkspaceRequest` is the adapter proto message, and reusing the name makes a search for one return the other.
- `pkg/gateway/podlifecycle/podsession/binder_test.go` is envtest-backed, so the unreferenced-upload test is tier 2 and needs a `// diagnosis:` comment.
- The tier 4 assertion on the `lenny-blob://` prefix in `eager_claim_lifecycle_test.go` is an implementation test and stays.
- §15.2.1 still calls the `GIT_CLONE_*` codes a "session-creation rejection family". They stay in that list, and SPEC-2 edit (e) item 7 removes only `SDK_DEMOTION_NOT_SUPPORTED`.
- SPEC-6 keeps the §26.2 attach clause "via the MCP WebSocket" verbatim. Correcting the attach transport is a separate defect listed below.

## Goals

- The spec states the upload response, the `uploadRef` contract, and its session scope in one place.
- The spec defines the finalize-time bind, its precondition, its validation timing, its persistence, and the outcome of a rejected finalize, and every spec site that describes the flow agrees with it.
- The `uploadRef` scope check applies to every client-submitted plan at every bind point.
- The OpenAPI document, the published plan schema, the MCP tool schema, and the reader-facing documentation describe the finalize body and the opaque `uploadRef`.
- Tests pin the create-time rejection, the finalize-time pin, the rejection-leaves-`created` outcome, the unreferenced-upload rule, and REST/MCP parity for the new rejections.
- Every spec site that states when setup commands run cites §7.5 (SPEC-7).
- A runtime's `workspaceDefaults` reach every session's workspace through the plan the session binds (SPEC-8, CODE-6).
- BUILD-GAPS F-5.1.23 and F-7.5.3 close when this proposal lands.

## Non-goals

- A create-only buffered-upload model, where finalize materializes every buffered upload with placement metadata on the upload request and the finalize body is retired. It adds `path`, `pathPrefix`, `format`, and `stripComponents` to two raw-body endpoints and duplicates the §14 source vocabulary. It also needs upload enumeration at finalize, breaks the Go SDK, the CLI, the playground, and the tier 4 and tier 9 finalize-bind tests, and still has to redefine the §14 upload sources that delegation export emits.
- No change, or code-only conformance to the current spec. The raw `/upload` body carries no placement, so "materialize every buffered upload" has no destination, and three shipped consumers depend on the body.
- Plan-declared upload slots, client-named `uploadRef` values, or pre-minted upload IDs in the create response. Each adds an upload parameter or response field and a reservation or collision rule to keep a create-time bind that no consumer needs.
- Pre-create or tenant-scoped uploads that a create-time plan could name. They need a new endpoint and a new authorization model, because the upload token is issued at creation.
- A dedicated bind endpoint, such as `PUT /v1/sessions/{id}/workspace-plan`. It adds REST and MCP surface and a second write outside the `created` → `finalizing` write that already binds atomically.
- Merging a finalize plan into, or replacing, a create-time plan. Either needs merge semantics and breaks the bind-once invariant that per-session `gitClone` immutability relies on.
- Moving the plan entirely to finalize. That breaks `POST /v1/sessions/start` and the MCP `create_and_start_session` inline-file plans.
- Declaring `uploadRef` a §28.5.3 `LennyBlobURI`. The opaque contract needs no scheme agreement.
- Collapsing `invalid_upload_ref` and `upload_ref_foreign_session` into one reason. Both are already emitted and tested, and keeping them costs no code or test churn.
- Moving `uploadRef` scope failures to `WORKSPACE_PLAN_INVALID` with a new reason. §14.1 layer 2 and the catalog row reserve that code for inner-plan schema failures, and the code emits `VALIDATION_ERROR`.
- New error codes, such as `UPLOAD_REF_INVALID` or `WORKSPACE_PLAN_ALREADY_BOUND`. The existing `VALIDATION_ERROR` reasons cover the cases.
- A separate rule forbidding upload sources at create, or keeping the scope check only at finalize. The single scope rule with an empty binding session ID already rejects every create-time upload source.
- Wrapping the binder's blob fetch in `blobstore.TenantScoped`, or adding a session check at materialization. It is a second enforcement point that fails late with a retryable-looking `SESSION_CREATION_FAILED`, while every client plan already passes the single intake check and delegation child plans never pass through it.
- A bind-time existence check on an own-session `uploadRef`. A missing blob already fails at materialization, and the check would add a store round trip to validation.
- Detecting, warning on, rejecting, or materializing unreferenced uploads. Detection needs per-session upload listing and a new reason or event, and materialization has no placement to apply.
- Forbidding `gitClone` sources in a finalize-bound plan so that refs pin literally at creation. It adds a rule and code where the chosen design changes only wording.
- Per-site "at plan binding" edits across every §14 "at session creation" clause. The definitional sentence in **Plan binding.** covers them.
- Generating the `finalize_workspace` MCP schema through `mcpschemagen`. The generator emits the OpenAPI path parameter `id` as a required property, while the tool takes `sessionId`.
- Adding finalize requirements to §15.2.1. Its items 4 and 5 already require schema consistency and parity tests.
- Adding a `workspacePlan` parameter to the Python and TypeScript SDK finalize methods. Those SDKs have no upload methods, so a plan parameter alone stages nothing. SDK upload coverage belongs to TEST-GAPS T-7.4.12.
- Replacing `finalizePlanMaxBytes` with `MaxJSONBodyBytes`, or specifying the finalize body cap and `PAYLOAD_TOO_LARGE`. The problem does not require it.
- Rejecting unknown fields in the finalize body. The decoder ignores them, and `FinalizeRequest` leaves top-level `additionalProperties` unset.
- A separate `created`-only precondition row and `Endpoint` constant for `/upload-archive`. The code shares `EndpointUpload`.
- A dedicated §7.4 paragraph restating the upload response. The §14 field note, the §15.1 rows, and the OpenAPI `UploadResponse` state the contract.
- Stating in the spec that a resend after a committed finalize returns `409` rather than `plan_already_set`, or that a rejected finalize leaves the upload token unconsumed. The `409` follows from the precondition check that runs before plan resolution, and token handling belongs to §7.4.
- Tier 4 recovery tests. The rejection-leaves-`created` rule is pinned at tier 1, the unreferenced-upload rule at tier 2, and the existing tier 4 finalize suites are re-run.
- Leaving §7.4 and §29.2 unedited. Their "buffered content" wording contradicts the not-materialized rule.
- Editing §19 decision 12. Its recipe names no bind point and stays true under **Plan binding.**
- Switching `lenny session new --workspace` to an MCP-only transport. §24.17 is corrected to describe the REST flow the CLI runs.
- Making the CLI send the `pool`, `env`, `timeouts`, and `runtimeOptions` fields the old §26.2 create envelope showed. SPEC-6 drops that envelope because the CLI does not send those fields.
- Specifying how a derived session's copied snapshot composes with a plan bound at finalize. The spec does not state it (see the defect **The spec does not state how a copied snapshot composes with a plan bound at finalize.**).
- Composing runtime defaults at each materialization, or storing them in a separate session column (see the decision on composing into the stored plan).
- Re-applying runtime defaults over a restored checkpoint. §7.3 step 3e replays the checkpoint and no plan.
- Materializing a `workspaceDefaults.files[].ref` by copying a runtime-scoped blob into the session's prefix (see the decision on inline content).
- A separate conflict rule for a default file and a client source at one path (see the decision on one `sources` array).
- Checking default setup commands against `setupCommandPolicy` at registration or at bind (see the decision on client-submitted commands).
- Reporting default-versus-client path collisions in the create or finalize response. Materialization emits the §14.1 collision warning.
- Editing BUILD-GAPS.md for F-24.17.4, F-26.2.4, and F-27.4.3. They are CLOSED, and this proposal records the specified contract in its own text.

## Open decisions for human to make

None.

## Defects in the shipped tree that this proposal does not stage

- **`handleFinalize` does not verify the upload token.** §7.4 **Upload authorization:** requires every finalize request to carry the `uploadToken`, but `handleFinalize` has no `verifyUploadToken` call and consumes the token digest only after the session is ready. It is a separate authorization defect with its own fix and tests, so it needs its own proposal or BUILD-GAPS finding rather than a widening of this one.
- **The §15.1 `/upload` precondition row and the tree disagree on `running`.** The row admits `running` when the runtime declares `capabilities.midSessionUpload`, but `admitUpload` (`pkg/gateway/sessionserver/upload.go`) checks the precondition without the session's capabilities, so `/upload` and `/upload-archive` reject every `running` session. Mid-session content travels through `/upload-to-session`, so a blob the row would admit in `running` has no plan source to place it. Either the row drops `running` or the endpoint gains a consumer; the choice belongs to the next proposal that edits §15.1 or mid-session upload. It predates this proposal and does not affect the bind.
- **The upload-blob TTL can expire before a snapshotless resume of a long session.** The 7-day retention gap exists under any binding model.
- **The emitted `lenny-blob://` URI diverges from §28.5.3.** The code emits `lenny-blob://{tenant}/{object_type}/{session}/{part}`, while §28.5.3 states `lenny-blob://{tenant_id}/{session_id}/{part_id}`. The opaque `uploadRef` contract does not depend on it.
- **The CLI attaches over SSE, while §26.2 and §24.17 say MCP.** `lenny session new --attach` and `lenny session attach` stream over `GET /v1/sessions/{id}/events`, while §26.2 says the CLI "attaches via the MCP WebSocket" and §24.17 maps `attach` to MCP connect. SPEC-6 keeps that clause verbatim because correcting the attach transport is a separate change.
- **Per-session MCP tool schemas are hand-written.** §15.2.1 item 4 requires MCP tool schemas generated from the same source as the OpenAPI document, but every per-session tool schema is hand-written, and CODE-2 adds another. Generating them needs a path-parameter rename option in `mcpschemagen`.
- **§26.3 bootstrap step 1 names an `INIT` state.** The `claude-code` bootstrap list says the warm-pool controller starts the pod "in INIT state", which the §6.2 pod state machine does not define. It concerns pod startup rather than setup timing.
- **The setup timeout has two names and two defaults.** The §6.2 `finalizing` timer row bounds setup with `runtime.setupTimeoutSeconds` at 300s and requires `maxFinalizingTimeoutSeconds` to be at least that value, while §5.1 names `setupPolicy.timeoutSeconds` and waits indefinitely when it is absent.
- **The spec does not state how a copied snapshot composes with a plan bound at finalize.** §7.1 **Derive session semantics** and §15.1 **Session Replay Semantics** pre-populate a derived or replayed session with the source session's workspace snapshot and state nothing about the plan that session binds at finalize. Under SPEC-8 that plan carries the runtime's `workspaceDefaults`, so a default file can write a path the snapshot holds. In the tree, `derive.go` and `replay.go` (`pkg/gateway/sessionserver`) write `ParentWorkspaceRef` on the new row, and no materialization path reads it.
- **The gateway returns `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED` at plan intake.** `writeWorkspacePlanError` (`pkg/gateway/sessionserver/workspace_plan.go`) returns `422 WORKSPACE_PLAN_SCHEMA_UNSUPPORTED` for a submitted plan at create and at finalize, as `schemas/workspaceplan-v1.json` states, while the §15.1 catalog row and §14.1 **Gateway reconciliation** place the code only on read-back of a stored plan.
- **`ClaimOpts.RequiresDemotion` is never set.** The `RequiresDemotion` field on `ClaimOpts` in `pkg/podlifecycle/interfaces.go` and `pkg/podregistry/podregistry.go` has no non-test setter, and SPEC-2 edits (f) and (g) remove it from the spec. Removing the field, the `ClaimPod` doc-comment sentence, and the setter in `tests/tier2_component/stores/crdpodregistry_claim_test.go` is a behavior-free refactor.
- **`PodSpec.WorkspacePlan` has no caller.** `CRDPodRegistry.CreatePod` (`pkg/podregistry/crd.go`) stamps the `WorkspacePlan` field of `PodSpec` (`pkg/podregistry/podregistry.go`) onto the Sandbox `spec.workspacePlan` field (`pkg/apis/lenny/v1alpha1/sandbox_types.go`). No non-test code calls `CreatePod`, and SPEC-2 edit (g) removes the field from the spec. Removing both fields, the generated CRD descriptions, and the plan assertion and `TestCreatePodLeavesWorkspacePlanNilWhenEmpty_spec_12_6_422` in `pkg/podregistry/crd_test.go` is a behavior-free refactor.

## Impacts on other proposals

| Proposal | Status | What 0086 does to it | What it must do |
|:--|:--|:--|:--|
| 0105_fix_the-per-state-session-deadlines-and-the-watchdog-sweeps | Approved | Both proposals edit `pkg/gateway/mcpfabric/delegation/service.go`. 0105 edits the delegation admission check that returns `ErrParentNotRunning`, and CODE-6 edits the child-row plan write. The edits are disjoint. The plan in `gateway-runtime-comms-remediation.md` §10.2 queues 0086's implementation after 0105's. | Nothing. Whichever proposal lands second locates its targets by symbol and quoted text. |
| 0085_fix_the-6-2-pre-attached-failure-retry-loop-has | Draft (drafted 2026-09-27) | Both proposals edit `spec/15_external-api-surface.md` §15.1 and `spec/29_communication-scenarios.md` §29.2. 0085 edits the `SETUP_COMMAND_FAILED` catalog row and step 23, and 0086 edits the `WORKSPACE_PLAN_INVALID` and `GIT_CLONE_*` catalog rows and steps 14, 15, and 17. The edits are disjoint. 0085's one-attempt rule for `POST /v1/sessions/{id}/finalize` composes with 0086's rule that a finalize rejected for its plan leaves the session `created`, because the plan rejection precedes any pre-attached work. Every 0085 deliverable keeps its subject. | Nothing. Whichever proposal lands second locates its targets by quoted text. |
| 0084_fix_on-a-pool-with-sessionpolicy-maxconcurrentsessions-1-one-run | Draft (drafted 2026-09-26) | Both proposals edit `spec/29_communication-scenarios.md` §29.2, where 0084 edits steps 24, 26a, and 26b and 0086 edits steps 14, 15, and 17. Both edit `spec/15_external-api-surface.md`, where 0084 edits §15.4.3, §15.4.4, §15.4.6, and §15.7 and 0086 edits §15.1 and §15.2. Both edit `pkg/gateway/externalapi/openapi/openapi.json`, where 0084 edits the `acknowledgeProcessLevelIsolation` description and 0086 edits the `/v1/sessions/{id}/finalize` operation, the `UploadResponse` `uploadRef` description, a new `FinalizeRequest` component, and the `ref` property of `workspaceDefaults.files`. Both edit `docs/getting-started/concepts.md`, where 0084 edits **Execution modes** and 0086 edits **Workspace lifecycle**, **Workspace sources**, **Setup commands**, the **Pool-level configuration** setup-command-policy bullet, and the derived-runtime `workspaceDefaults` bullet. Both edit `docs/runtime-author-guide/runtime-configuration.md`, where 0084 edits the acknowledgment paragraph and 0086 edits the paragraph after the **Policy and limits** table and the `workspaceDefaults` row. Both edit `spec/05_runtime-registry-and-pool-model.md` §5.2, where 0084 edits the **Concurrent sessions** and **Deployer acknowledgment (concurrent sessions).** paragraphs and 0086 edits the last sentence of the **Recycle lifecycle** paragraph. Both edit `schemas/lenny-adapter.proto`, where 0084 edits the connector RPC comment and 0086 edits the `WorkspacePlan`, `RunSetupRequest`, and `SetupPolicy` comments. The edits are disjoint. 0084 also edits `spec/28_communication-channels.md`, including the §28.5.3 `CH-RUNTIMEOPS` `llm_request_completed` row, and 0086 edits no part of that file. Every 0084 deliverable keeps its subject. | Nothing. Whichever proposal lands second locates its targets by quoted text. |
| 0083_fix_four-spec-sites-disagree-on-the-finalize-workspace-failure-envelope | Implemented | Resolves the observations its review log records about the undefined finalize-body plan and `WORKSPACE_PLAN_INVALID` at `/finalize`. CODE-3 lists only plan-bind responses and leaves the prepare-phase envelope 0083 settled untouched. | Nothing. It is immutable. |
| 0082_fix_two-concurrent-post-v1-sessions-id-finalize-calls-for | Implemented | SPEC-3 inserts text into the finalize precondition Notes cell before the credential sentence, leaving 0082's terminal-state sentence unchanged. The same-write persistence SPEC-2 states is the write 0082's guarded Update performs. | Nothing. It is immutable. |
| 0077_new_pre-materialize-a-workspace-on-a-warm-pod | Draft (dated 2026-08-19, last commit 2026-10-28) | 0077's §1.3 rationale restates the §14 rule that the gateway resolves each `gitClone.ref` to an immutable `resolvedCommitSha` "at session creation". SPEC-1 edit (c) rewords that timing to "when the plan binds", and a plan can bind in the finalize request. SPEC-1 leaves the per-session immutability sentence that 0077's argument rests on unchanged, so the argument and the design sketch stand, and only the restated timing becomes stale. SPEC-7 states per-session setup timing at the opening of §7.5 with no pod-level prohibition. | Reword the pin-timing sentence to "when the plan binds (§14.1 **Plan binding.**)" if 0077 is revived after 0086 lands, and add any warm-time setup mode as a clause of §7.5. |
| 0007_fix_eager-pod-claim-at-create-and-finalize-materialization | Implemented | Specifies the finalize plan binding and `uploadRef` validation that 0007 kept in `handleFinalize` without staging spec text. | Nothing. It is immutable. |

## Deliverable index

- SPEC-1 — `spec/14_workspace-plan-schema.md` — adds the §14 `uploadRef` field note, trims the upload sources from the canonical create example, rewords the `gitClone.ref` pin timing and the schema-encoding envelope text, and deletes the per-slot scope note.
- SPEC-2 — `spec/14_workspace-plan-schema.md`, `spec/04_system-components.md`, `spec/06_warm-pod-model.md`, `spec/12_storage-architecture.md`, `spec/15_external-api-surface.md`, `spec/16_observability.md` — adds the §14.1 **Plan binding.** paragraph, aligns layer-1 validation and replay wording, moves the §4.9 host match and the §6.1 SDK-warm demotion to the bind, and removes the claim-time demotion option and the pod-creation plan from §4.6.1 and §12.6.
- SPEC-3 — `spec/15_external-api-surface.md` — updates the §15.1 endpoint, precondition, derive-failure, and error-catalog rows and the §15.2 `finalize_workspace` row.
- SPEC-4 — `spec/07_session-lifecycle.md` — rewrites §7.1 steps 11 and 12 and the §7.4 pre-start upload sentence.
- SPEC-5 — `spec/29_communication-scenarios.md` — rewrites §29.2 steps 14, 15, and 17.
- SPEC-6 — `spec/24_lenny-ctl-command-reference.md`, `spec/26_reference-runtime-catalog.md` — describes the CLI's REST workspace flow in §24 and §24.17 and the finalize-body reference plan in §26.2, points the §26.2 and §26.3 examples at §24.17, and retimes §26.3 bootstrap step 3 to finalize and start.
- SPEC-7 — `spec/07_session-lifecycle.md`, `spec/05_runtime-registry-and-pool-model.md`, `spec/16_observability.md` — makes §7.5 the home of setup timing and points §5.1, the `setupCommandPolicy` merge row, §5.2, the §16.1 `setup_command_failed` error type, and the §16.5 `WarmPoolReplenishmentFailing` row at it.
- SPEC-8 — `spec/14_workspace-plan-schema.md`, `spec/05_runtime-registry-and-pool-model.md`, `spec/07_session-lifecycle.md` — adds the §14.1 **Runtime workspace defaults.** paragraph and edits §5.1 **Derived Runtime Instantiation** and §7.5 **Command policy:**.
- CODE-1 — `pkg/gateway/sessionserver` — moves the `uploadRef` scope check into the renamed shared resolver `resolveSubmittedPlan`.
- CODE-2 — `pkg/gateway/mcpfabric/mcptools/client_tools.go` — declares the optional `workspacePlan` in the `lenny/finalize_workspace` input schema.
- CODE-3 — `pkg/gateway/externalapi/openapi/openapi.json`, `schemas/workspaceplan-v1.json` — adds the finalize request body and plan-bind responses, and the opaque `uploadRef` and plan-binding descriptions.
- CODE-4 — `sdks/client/go/lenny/upload.go`, `pkg/gateway/sessionserver/upload.go`, `pkg/embedded/localcli/session.go`, `pkg/gateway/runtime/sdkwarm/matcher.go`, `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_sessionlifecycle.go`, `pkg/observability/metrics/catalog.go` — corrects stale comments and metric descriptions.
- CODE-5 — `schemas/lenny-adapter.proto`, `pkg/adapter/workspace/setup.go` — corrects the setup comments that describe pod-level setup and regenerates the bindings.
- CODE-6 — `pkg/gateway/runtime/runtimestore`, `pkg/gateway/sessionserver`, `pkg/gateway/session/sessionstore`, `pkg/gateway/mcpfabric/delegation/service.go`, `pkg/gateway/externalapi`, `pkg/ops/mcp/generated_tools.go` — composes runtime defaults into the bound plan and checks them at registration.
- DOCS-1 — `docs/` — rewrites the reader-facing upload, finalize, and CLI session documentation, the runtime author guide's setup-timing and `workspaceDefaults` text, and the error-catalog and metrics rows for SDK-warm demotion and `setup_command_failed`.
- TEST-1 — `pkg/gateway/...` tests, `tests/tier3_contract`, `tests/tier9_security` — pins the amended contract across tiers 1, 2, 3, and 9.
- TEST-2 — `pkg/gateway/...` tests, `tests/tier4_integration` — pins SPEC-8 and CODE-6.
