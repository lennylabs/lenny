# Summary: Finalize-time WorkspacePlan bind has no specification contract

## Summary

**Problem statement.** The specification has no contract for how content uploaded during a session's `created` window reaches the workspace. §7.1, §7.4, and §29.2 describe finalize streaming every buffered upload with no plan reference. The §14 `uploadFile` and `uploadArchive` sources name uploads by an `uploadRef` that nothing defines, and a create-time plan cannot name an upload because the upload token is issued at creation. The tree binds a plan in an optional `POST /v1/sessions/{id}/finalize` body that no spec text, OpenAPI operation, or reference document defines, although the Go SDK, the CLI, and the playground depend on it. The spec contradicts that bind wherever it states pin timing, the plan envelope, or error scope. The `uploadRef` scope check runs only at finalize, so a create-time plan can name a blob staged for another session of the same tenant.

**What changes.**

- `spec/14_workspace-plan-schema.md`: a §14 `uploadRef` field note and a §14.1 **Plan binding.** paragraph become the homes of the upload contract and the finalize-time bind, and the `gitClone.ref` pin wording follows the bind (SPEC-1, SPEC-2).
- `spec/15_external-api-surface.md`: the §15.1 endpoint, precondition, derive-failure, and error-catalog rows and the §15.2 `finalize_workspace` row describe the bind and add `/upload-archive` (SPEC-3).
- `spec/07_session-lifecycle.md`, `spec/29_communication-scenarios.md`, `spec/24_lenny-ctl-command-reference.md`, and `spec/26_reference-runtime-catalog.md`: the flow narratives and the CLI reference describe the finalize-time bind the tree runs (SPEC-4, SPEC-5, SPEC-6).
- `pkg/gateway/sessionserver`: the `uploadRef` scope check moves into the shared plan resolver, so it applies at every bind point (CODE-1).
- MCP tool schema, OpenAPI document, `schemas/workspaceplan-v1.json`, and SDK and gateway comments: the published surfaces declare the finalize body and the opaque `uploadRef` (CODE-2, CODE-3, CODE-4).
- `docs/` and the test suites: reader-facing pages and tests at tiers 1, 2, 3, and 9 follow the amended contract (DOCS-1, TEST-1).

**Decisions.**

- The spine is the minimal design: specify the finalize-time bind the tree runs and every shipped consumer uses, and change code only where the problem requires it.
- A session binds at most one plan, at creation or, when creation carried none, in the finalize body. `plan_already_set` stays.
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

**Watch out for.**

- Do not retire the finalize body to match the current spec text. The Go SDK, `lenny session new --workspace/--file`, and the playground all depend on it.
- The create and create-and-start callers of `resolveSubmittedPlan` must pass an empty binding session ID. The session ID does not exist yet when the plan is validated, and CODE-1's empty-ID guard is the fail-closed predicate.
- The `uploadRef` check must run before `checkGitCloneAuthBindings` and `PinCommitSHAs`, so a foreign ref fails before any `ls-remote`.
- The definitional sentence in **Plan binding.** covers only the `WorkspacePlan`'s own fields. The `env` blocklist, `runtimeOptions`, and callback validation stay at session creation, which is where the code runs them.
- Every edited or added §15.1, §15.2, and §24.17 table row stays one physical line.
- The OpenAPI component is `FinalizeRequest`. `FinalizeWorkspaceRequest` is the adapter proto message, and reusing the name makes a search for one return the other.
- `pkg/gateway/podlifecycle/podsession/binder_test.go` is envtest-backed, so the unreferenced-upload test is tier 2 and needs a `// diagnosis:` comment.
- The tier 4 assertion on the `lenny-blob://` prefix in `eager_claim_lifecycle_test.go` is an implementation test and stays.
- §15.2.1 still calls the `GIT_CLONE_*` codes a "session-creation rejection family". It stays true and is not edited.
- SPEC-6 keeps the §26.2 attach clause "via the MCP WebSocket" verbatim. Correcting the attach transport is a separate defect listed below.

## Goals

- The spec states the upload response, the `uploadRef` contract, and its session scope in one place.
- The spec defines the finalize-time bind, its precondition, its validation timing, its persistence, and the outcome of a rejected finalize, and every spec site that describes the flow agrees with it.
- The `uploadRef` scope check applies to every client-submitted plan at every bind point.
- The OpenAPI document, the published plan schema, the MCP tool schema, and the reader-facing documentation describe the finalize body and the opaque `uploadRef`.
- Tests pin the create-time rejection, the finalize-time pin, the rejection-leaves-`created` outcome, the unreferenced-upload rule, and REST/MCP parity for the new rejections.

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
- Editing §15.2.1. Its items 4 and 5 already require schema consistency and parity tests.
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
- Specifying how a derived session's copied snapshot composes with a plan bound at finalize. The §7.1 derive rules govern it.
- Editing BUILD-GAPS.md. F-24.17.4, F-26.2.4, and F-27.4.3 are CLOSED, and this proposal records the specified contract in its own text.

## Open decisions for human to make

None yet. The review loops write this section.

## Defects in the shipped tree that this proposal does not stage

- **`handleFinalize` does not verify the upload token.** §7.4 **Upload authorization:** requires every finalize request to carry the `uploadToken`, but `handleFinalize` has no `verifyUploadToken` call and consumes the token digest only after the session is ready. It is a separate authorization defect with its own fix and tests, so it needs its own proposal or BUILD-GAPS finding rather than a widening of this one.
- **A running-state blob upload has no consumer.** The `/upload` precondition row admits `running` under `midSessionUpload`, but mid-session content travels through `/upload-to-session`, so a blob uploaded in `running` is never placed. It predates this proposal and does not affect the bind.
- **The upload-blob TTL can expire before a snapshotless resume of a long session.** The 7-day retention gap exists under any binding model.
- **The emitted `lenny-blob://` URI diverges from §28.5.3.** The code emits `lenny-blob://{tenant}/{object_type}/{session}/{part}`, while §28.5.3 states `lenny-blob://{tenant_id}/{session_id}/{part_id}`. The opaque `uploadRef` contract does not depend on it.
- **The CLI attaches over SSE, while §26.2 and §24.17 say MCP.** `lenny session new --attach` and `lenny session attach` stream over `GET /v1/sessions/{id}/events`, while §26.2 says the CLI "attaches via the MCP WebSocket" and §24.17 maps `attach` to MCP connect. SPEC-6 keeps that clause verbatim because correcting the attach transport is a separate change.
- **Per-session MCP tool schemas are hand-written.** §15.2.1 item 4 requires MCP tool schemas generated from the same source as the OpenAPI document, but every per-session tool schema is hand-written, and CODE-2 adds another. Generating them needs a path-parameter rename option in `mcpschemagen`.

## Impacts on other proposals

| Proposal | Status | What 0086 does to it | What it must do |
|:--|:--|:--|:--|
| 0085_fix_the-6-2-pre-attached-failure-retry-loop-has | Draft | Both edit `spec/15_external-api-surface.md` §15.1 and `spec/29_communication-scenarios.md` §29.2. 0085 edits the `SETUP_COMMAND_FAILED` row and step 23; 0086 edits other rows and steps 14 and 15. The edits are disjoint. | Nothing. Whichever lands second locates its targets by quoted text. |
| 0084_fix_on-a-pool-with-sessionpolicy-maxconcurrentsessions-1-one-run | Draft | Both edit §29.2, at different steps. 0084 also edits §28.5.3, whose `LennyBlobURI` this proposal leaves unchanged. | Nothing. Whichever lands second locates its targets by quoted text. |
| 0083_fix_four-spec-sites-disagree-on-the-finalize-workspace-failure-envelope | Implemented | Resolves the observations its review log records about the undefined finalize-body plan and `WORKSPACE_PLAN_INVALID` at `/finalize`. CODE-3 lists only plan-bind responses and leaves the prepare-phase envelope 0083 settled untouched. | Nothing. It is immutable. |
| 0082_fix_two-concurrent-post-v1-sessions-id-finalize-calls-for | Implemented | SPEC-3 inserts text into the finalize precondition Notes cell before the credential sentence, leaving 0082's terminal-state sentence unchanged. The same-write persistence SPEC-2 states is the write 0082's guarded Update performs. | Nothing. It is immutable. |
| 0007_fix_eager-pod-claim-at-create-and-finalize-materialization | Implemented | Specifies the finalize plan binding and `uploadRef` validation that 0007 kept in `handleFinalize` without staging spec text. | Nothing. It is immutable. |

## Deliverable index

- SPEC-1 — `spec/14_workspace-plan-schema.md` — adds the §14 `uploadRef` field note, trims the upload sources from the canonical create example, and rewords the `gitClone.ref` pin timing and envelope.
- SPEC-2 — `spec/14_workspace-plan-schema.md` — adds the §14.1 **Plan binding.** paragraph and aligns layer-1 validation and replay wording.
- SPEC-3 — `spec/15_external-api-surface.md` — updates the §15.1 endpoint, precondition, derive-failure, and error-catalog rows and the §15.2 `finalize_workspace` row.
- SPEC-4 — `spec/07_session-lifecycle.md` — rewrites §7.1 steps 11 and 12 and the §7.4 pre-start upload sentence.
- SPEC-5 — `spec/29_communication-scenarios.md` — rewrites §29.2 steps 14 and 15.
- SPEC-6 — `spec/24_lenny-ctl-command-reference.md`, `spec/26_reference-runtime-catalog.md` — describes the CLI's REST workspace flow in §24 and §24.17 and the finalize-body reference plan in §26.2.
- CODE-1 — `pkg/gateway/sessionserver` — moves the `uploadRef` scope check into the renamed shared resolver `resolveSubmittedPlan`.
- CODE-2 — `pkg/gateway/mcpfabric/mcptools/client_tools.go` — declares the optional `workspacePlan` in the `lenny/finalize_workspace` input schema.
- CODE-3 — `pkg/gateway/externalapi/openapi/openapi.json`, `schemas/workspaceplan-v1.json` — adds the finalize request body and plan-bind responses, and the opaque `uploadRef` descriptions.
- CODE-4 — `sdks/client/go/lenny/upload.go`, `pkg/gateway/sessionserver/upload.go`, `pkg/embedded/localcli/session.go` — corrects stale comments.
- DOCS-1 — `docs/` — rewrites the reader-facing upload and finalize documentation.
- TEST-1 — `pkg/gateway/...` tests, `tests/tier3_contract`, `tests/tier9_security` — pins the amended contract across tiers 1, 2, 3, and 9.
