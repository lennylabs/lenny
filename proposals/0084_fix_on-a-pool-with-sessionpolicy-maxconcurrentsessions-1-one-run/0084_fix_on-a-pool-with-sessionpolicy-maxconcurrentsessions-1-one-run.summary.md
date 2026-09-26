# Summary: Intra-pod MCP and direct-mode usage cannot attribute calls on concurrent pools

## Summary

**Problem statement.** On a pool with `maxConcurrentSessions > 1`, one runtime process serves every slot, and no intra-pod MCP connection and no `llm_request_completed` frame carries a session. The adapter infers the caller from occupancy through `soleSession`, so it refuses every platform and connector `tools/list` and `tools/call` from the moment a second session reaches the process until the process serves none, and it drops direct-mode token counts over the same window. The spec states the refusal as normative with a condition the adapter cannot check. Once the refusal is lifted, three masked defects become live: every start publishes a fresh nonce the running servers reject, connector servers serve only the arming session's set, and the surface never arms on a pod whose second slot was registered before the first start. No deployer-facing text names the loss. The full evidence is in the problem statement file.

**What changes.**

- `spec/15` §15.4.3 states the session address once: an optional `_lennySessionId` on `initialize`, confirmed per request against the slot registry, with today's sole-session rule for an unaddressed connection. `spec/04`, `spec/28`, `spec/29`, and `spec/05` cite it, and the manifest's `mcpNonce` and `connectorServers` become properties of the pod's servers (SPEC-1, SPEC-2, SPEC-3, SPEC-5).
- `llm_request_completed` gains an optional `sessionId` in `spec/28`, §11.2, and the schema (SPEC-4, SCHEMA-1).
- `pkg/adapter`: one resolver attributes MCP requests and token frames, and the pod-surface state pins the nonce, serves the connector union, arms when unarmed, and publishes through one helper (CODE-1 to CODE-4).
- §5.2, the admission message, and its carriers add runtime-asserted session attribution to the concurrent-sessions acknowledgment (SPEC-6, CODE-5).
- The runtime SDKs and the compliance harness tolerate a connector the gateway refuses for the session (CODE-6, CODE-7), and the documentation and the tier-11 nonce gate follow (DOCS-1). Tests span tiers 0, 1, 3, 4, 7a, 9, 10, and 11 (TEST-1 to TEST-5).

**Decisions.**

- The address rides on the connection as `params._lennySessionId` beside `_lennyNonce`, handled by the one function that already validates and strips Lenny-private params, so the `mcp.ToolProvider` interface is unchanged and client libraries need no per-request hook.
- The address is optional, and an unaddressed connection keeps the `soleSession` rule, following the `CH-MSGSOCK` precedent that lets an unambiguous frame omit its address; `maxConcurrentSessions: 1` pods, `git-credential-lenny`, and the in-tree runtimes behave as today.
- Confirmation happens per request, because a connection outlives its session, through one resolver `runtimeSession` built on `checkSessionBound`, the adapter's one live-binding check.
- `llm_request_completed` takes the optional `sessionId` and resolves it through the same resolver; `llm_request_started` and the pod-wide in-flight gate stay unchanged (proposal 0080 §1.5 owns that residue), and the in-flight decrement stays unconditional and first so an unattributed frame cannot wedge rotation.
- The claim that arms the surface mints the nonce and every later start publishes that value; a claim whose nonce was superseded publishes nothing and returns `Aborted`, so the manifest always carries the nonce the running servers validate.
- `connectorServers` lists every connector server opened since arming and only grows while armed; serving the union grants nothing, because the gateway authorizes each connector request against the attributed session's policy.
- The surface arms whenever it is unarmed, a takeover applies when the claimant is the only started slot, and the surface is cancelled only when the runtime is idle and no started slot remains; without these a busy concurrent pod never arms, or an arming rollback removes a co-tenant's surface.
- One ensure-and-publish helper replaces the three copies of the arm-and-publish block, and the surface fixes (CODE-4) land before the providers stop refusing (CODE-3).
- §15.4.3 **Session address.** is the single normative statement of the attribution rule, and every other spec site cites it.
- The acknowledgment item covers the intra-pod MCP surface only, because its servers accept any nonce-bearing connection; direct-mode cross-session charging is stated in §11.2, because `CH-RUNTIMEOPS` is one runtime-held connection with the same trust as `CH-MSGSOCK`.
- The first-party runtime SDKs stay single-session and unaddressed, because §15.7 binds an SDK instance to one session's credentials, workspace, and handler lifecycle; they change only to tolerate a refused connector.
- The change adds no metric, RPC, frame type, socket, file, or error code; it adds two optional fields.
- Ownership of proposal 0080 §1.3 and §1.4 is recorded in the impacts table, and no edit to 0080 is staged.

**Watch out for.**

- **The attribution exposure is pending human adjudication.** Any process in any slot that reads the manifest can call platform and connector tools as any co-resident session of the same tenant under this design; today's refusal prevents that. The review loop records the choice under Open decisions. Option (a) is the design as staged. Under fallback (b) only SPEC-4, SPEC-6c, SCHEMA-1, CODE-2, the fallback texts of SPEC-6b, SPEC-6d, and CODE-5, and the fallback subsets DOCS-1 and the Testing section name land; the other deliverables do not.
- **Gate anchors.** The tier-11 nonce gate locates each spec statement by the first line of an anchor sentence and requires "started at most once per pod", "carried at the start that bound", and "does not re-arm" in the same block. The staged edits keep every anchor line unwrapped and every pinned phrase in place; see the preamble of the spec changes' **Staged edits**.
- **Order of the code steps.** Lifting the refusal before CODE-4 lands turns the stale nonce, the missing connector servers, and the never-armed surface into live failures for every co-tenant.
- **Existing tests encode the old predicate.** They fail to compile or fail outright once `slotClaim.startMCP` is gone; CODE-4 names the ones it rewrites in the same commit.
- **The discriminating resolver case.** After alice and bob both start and alice is released, bob is the only live session and an unaddressed request is still refused, because the process has not served none since. A test that checks only the two-live-sessions case does not distinguish the cohort rule from a slot count.
- **The admission window.** The resolver confirms the binding when a request enters. A tier-7a test that asserts an admitted request never reaches the gateway after the release returns pins a property the design does not have.
- **SDK runtimes on an overlapped pod.** Their tool calls are refused, which is the specified outcome and reads like a regression in a concurrent-pool test.
- **CODE-6 before CODE-7.** The reference runtimes fail the new reachability case until the SDK tolerance lands.
- **`SoleSessionID` loses its production caller.** Keep it for the tests that read it and do not wire it back into a production path.
- **`MCPSocket == ""` no longer opens connector servers.** A development configuration that relied on connector servers without a platform socket loses them.
- **§11.2 pins a sentence.** `tests/tier11_docs/direct_usage_reportusage_consistency_test.go` requires the "accumulates a per-session cumulative total from the `llm_request_completed`" sentence verbatim.

## Goals

- A Standard- or Full-level runtime on a concurrent pool keeps its platform and connector MCP tools across session overlap when it names the session on its connections.
- The adapter forwards an intra-pod MCP request only under a session the connection named and the slot registry holds bound, or under the single session the process has been given, and refuses every other request.
- The published manifest names a nonce and connector sockets that running servers serve.
- A direct-mode runtime that names the session on `llm_request_completed` has its token counts attributed to that session.
- The concurrent-sessions acknowledgment and the admission rejection message name the property a deployer accepts.
- `maxConcurrentSessions: 1` pods and unaddressed runtimes behave as today.

## Non-goals

- **Disclosure only for MCP.** Keeping the refusal and adding the MCP loss to the acknowledgment is the recorded fallback (b) of the pending decision; the human chose the session-addressed direction.
- **Rejecting Standard- or Full-level runtimes, or `deliveryMode: direct`, at pool admission when `maxConcurrentSessions > 1`.** The human rejected it, and it contradicts §6.1's Full-level per-session rotation on concurrent pods.
- **Never arming the intra-pod MCP servers on a concurrent pool.** The adapter has no knowledge of `maxConcurrentSessions`, and it removes MCP from low-occupancy concurrent pods.
- **A pool status condition for MCP unavailability.** It is a new wire contract, and the acknowledgment already makes the property deployer-visible.
- **A per-call address in `params._meta.lenny.sessionId`.** It puts Lenny bytes on every request, needs a per-request hook in client libraries, and adds a second injection convention beside the `_lenny*` initialize params.
- **A flat `_meta.sessionId`, or an address in tool `arguments`.** The first risks collision with MCP-reserved metadata; the second pollutes every tool's input schema and forwards the address to the gateway as input.
- **A mandatory `_lennySessionId` and a required `sessionId` on `llm_request_completed`.** Either breaks every unaddressed runtime and `git-credential-lenny` on `maxConcurrentSessions: 1` pods, and `CH-MSGSOCK` already permits an unambiguous omission.
- **Replacing the unaddressed fallback with the `CH-MSGSOCK` slot-count rule.** A departed session's code can remain resident in the process, so the slot count is weaker than the cohort rule.
- **Validating the declared session only at handshake.** A connection outlives its session, so a per-request check is required anyway, and one check site suffices.
- **A live-binding check keyed on `runtimeLive`.** It would be a second definition of a live binding beside `checkSessionBound` and the `CH-MSGSOCK` rule.
- **Per-session nonces or credentials.** The shared process reads every session's secret, so they add no attribution strength against the runtime, and written to the group-readable slot tree they become a cross-slot credential.
- **A per-session secret delivered only over `CH-MSGSOCK`.** It strengthens attribution only against subprocesses that never receive the secret, which the spec cannot check, and it adds a frame field, connection state, and per-session server lifecycles.
- **Per-slot platform or connector sockets.** They multiply listeners and arming lifecycles and add nothing over a declared identifier.
- **A per-slot manifest copy under `/run/lenny/slots/{sessionId}/`.** It is a new file layout with mode and `fsGroup` readability hazards; a pinned nonce and a monotone served set in the one manifest give a running runtime what it needs.
- **A new `CH-MSGSOCK` start frame or envelope fields carrying the nonce or connector set.** It is new wire surface for data the manifest now carries correctly.
- **Keeping `connectorServers` per session while serving the union.** Under unordered concurrent renames, a re-read can observe another session's set and miss its own.
- **Closing a connector server when the last session permitted to use it leaves.** It needs reference-count state, the gateway refuses unpermitted calls, and the surface stops at the idle cancel.
- **An adapter-side per-session connector permission check.** It duplicates the gateway's authoritative check and goes stale on a mid-session policy change.
- **Closing accepted MCP connections on a cancel or takeover.** The gap predates this proposal, and the addressed rule does not widen it; it is listed under the defects below.
- **Serving platform `tools/list` unaddressed because the gateway catalog is uniform.** It is a second rule for one method and gives an unaddressed connection no working calls.
- **`sessionId` on `llm_request_started` and a per-session in-flight rotation gate.** Nothing reads it, the pod-wide gate over-waits safely, and proposal 0080 §1.5 owns the residue; an optional property can be added later.
- **Gating the in-flight decrement on attribution.** A late or misaddressed completion would wedge Full-level rotation.
- **Reusing a lease identifier on `llm_request_completed` for attribution.** The frame carries none, so it is equally a new field.
- **A metric for refused unaddressed MCP requests or dropped token folds.** The problem does not require one, the runtime receives a JSON-RPC error, and adapter metrics sit outside the default scrape set under channel-naming N4.
- **Bumping the adapter manifest `version`.** Field values on single-session pods do not change, and the platform is pre-deployment.
- **Deriving a session for `git-credential-lenny` from its working directory.** The helper stays unaddressed, so VCS credentials on an overlapped concurrent pod stay refused, as today.
- **Multi-session support in the first-party runtime SDKs.** §15.7 binds an SDK instance to one session through `CreateRequest.SessionID`, one `OnCreate`, the startup manifest's `credentialsPath` and workspace, and `Message.SessionID`. A per-session handler lifecycle needs its own spec change and a later proposal.
- **Editing the §15.4.3 `**Migration path — v2 out-of-band handshake.**` note.** It would add a field to a frame that does not exist, and nothing in the problem requires it.
- **The other `CH-RUNTIMEOPS` frames and the session-less control events.** The problem statement places them out of scope.
- **The gateway-side membership check of a forwarded session (proposal 0070 §5).** It composes with this design, because it validates the identifier this design produces.
- **Persisting attribution or surface state across an adapter restart.** An adapter crash is a pod loss.
- **Dropping the stale-surface takeover.** The takeover still retires a nonce a departed generation read.
- **Tier-10 conformance assertions for addressing.** The field is optional, so the observed-level rule is unchanged.
- **Editing implemented proposal 0073.** Its recorded limit stands as history, and this proposal records the change in its own text.

## Open decisions for human to make

This section is written by the review loops. It is empty at drafting.

## Defects in the shipped tree that this proposal does not stage

- **Two control-event emitters drop a session they hold.** `onSlotLeaseExpired` calls `EmitAuthExpired` with the slot identifier in scope, and `rotateProviderFull` calls `EmitLeaseRejected` with `sessionID` in scope, and both rely on the empty `soleSession` stamp. Passing the identifier through is a local adapter correction that needs no channel mechanism, so it stays out of this proposal.
- **`EmitRateLimited` and `EmitProviderUnavailable` have no production caller.** They are recorded for the same follow-up.
- **Accepted MCP connections survive a cancel or takeover.** `ServeConn` runs on `context.Background()` and closing a listener leaves accepted connections open, so a connection opened under a retired nonce keeps serving until the peer closes. The addressed rule does not widen the gap, because any connection holding the live nonce can already name any bound session.
- **The admission rejection message omits the network side-channels.** §5.2 states that the message enumerates cross-slot traffic observation, port binding conflicts, DNS resolver cache poisoning, and timing patterns, and `ValidateSessionPolicy`'s message names none of them. CODE-5 keeps the existing items and adds one.
- **The pod-wide per-provider in-flight gate.** A co-tenant's outstanding request gates another session's rotation. Proposal 0080 §1.5 inventories it.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0080 (inventory of the residues 0073 recorded and deferred) | Draft, headed `EARLY DRAFT, NOT CONVERGED`; stages no changes. | 0084 is the per-concern split of §1.3 and §1.4. SPEC-1, SPEC-2, and CODE-4 pin `mcpNonce` to the armed surface and make `connectorServers` the served union, and the session-address rule replaces occupancy inference. CODE-4's arm-when-unarmed predicate also takes the §1.2 MCP-arming clause that 0081's impacts row moved into §1.4. §1.5 keeps its subject: 0084 adds an optional `sessionId` to `llm_request_completed` only, and leaves `llm_request_started` and the pod-wide per-provider in-flight gate unchanged. | When 0080 is triaged, move §1.3 and §1.4, including the clause moved from §1.2, to §2 as taken by 0084, remove them from §1, and update the Scope sentence's owner list. Re-derive §1.5 knowing that `llm_request_completed` may carry `sessionId` and `llm_request_started` does not. No edit to 0080 is staged, because it stages none. |
| 0073 (give every session a slot and absence one meaning) | Implemented. | 0084 lifts the limit 0073 §9 "Recorded limits" states, that a Standard- or Full-level runtime remains unusable on a concurrent pod, and changes the pod-global manifest's `mcpNonce` and `connectorServers` semantics 0073 left in place. | Nothing. A landed proposal is not edited, and 0084 records the change in its own text. |
| 0070 (bind a platform tool call to the session that made it) | Applied to spec. | 0084 produces the forwarded session identifier that 0070 §5's later membership check validates, and it relies on that check as the backstop for a request admitted just before its session's release. | When the §5 membership check is implemented, validate the identifier the adapter forwards under the §15.4.3 session-address rule. No change to 0070's text. |

## Deliverable index

- **SPEC-1** (`spec/15_external-api-surface.md`): §15.4.3 session address and nonce lifetime pointer, §15.4.4 pseudocode, §15.4.6 reachability row, §15.7 SDK sentence.
- **SPEC-2** (`spec/04_system-components.md`): §4.7.5 manifest lead, §4.7.6 `mcpNonce`, `connectorServers`, and Standard reading rows, §4.7.11 items 1 and 7.
- **SPEC-3** (`spec/28_communication-channels.md`): `CH-MCP-PLATFORM` and `CH-MCP-CONNECTOR` cards, §28.6, and §28.8 cite the session-address rule.
- **SPEC-4** (`spec/28_communication-channels.md`, `spec/11_policy-and-controls.md`): `llm_request_completed` `sessionId` and attribution, and the §11.2 residual-risk sentence.
- **SPEC-5** (`spec/29_communication-scenarios.md`): §29.2 steps 24, 26a, and 26b, and the §29.10 co-tenancy entries.
- **SPEC-6** (`spec/05_runtime-registry-and-pool-model.md`, `spec/09_mcp-integration.md`, `spec/13_security-model.md`): §5.2 paragraph and acknowledgment item, §9.3 calling session, §13.1 enumeration.
- **SCHEMA-1** (`schemas/runtime-ops-events.schema.json`): optional `sessionId` on `llm_request_completed`, the tokens example, and the proto comment.
- **CODE-1** (`pkg/adapter/mcp/nonce.go`): `_lennySessionId` extraction and `CallerSession` on the connection context.
- **CODE-2** (`pkg/adapter/runtimegeneration.go`): `runtimeSession` and the addressed direct-mode token sink.
- **CODE-3** (`pkg/adapter/platformtoolprovider.go`): platform and connector providers attribute through `runtimeSession`.
- **CODE-4** (`pkg/adapter/slotsession.go`): pod-surface rules and the ensure-and-publish helper.
- **CODE-5** (`pkg/gateway/runtime/poolstore/poolstore.go`): admission message and its carriers name the new acknowledgment item.
- **CODE-6** (`sdks/runtime/go/runtime/mcp.go`): the runtime SDKs omit a connector refused at `tools/list`.
- **CODE-7** (`cmd/lenny-compliance/standard.go`): the reachability check exercises a refused connector.
- **DOCS-1** (`docs/runtime-author-guide/platform-tools.md`): runtime-author, reference, operator, and runbook pages and the tier-11 nonce gate.
- **TEST-1** (`tests/tier0_static/schemas_test.go`): tier-0 and tier-3 wire-contract cases.
- **TEST-2** (`pkg/adapter/`): tier-1 adapter and Go SDK cases and the claim-map row.
- **TEST-3** (`tests/tier4_integration/intra_pod_mcp_session_address_test.go`): tier-4 principal resolution.
- **TEST-4** (`tests/tier7a_load_local/`): tier-7a surface races, addressed-release race, and token contention.
- **TEST-5** (`tests/tier9_security/adapter_shared_mcp_surface_test.go`): tier-9 attribution boundaries and admission message.
