# Decisions: 0119 Agent runtimes only

## Decisions

| # | Question | Owner's answer | Recorded as | Applied in |
| :-- | :-- | :-- | :-- | :-- |
| D1 | Whether non-agent runtimes stay. | "Remove non-agent runtimes altogether, in a new design: `type: mcp` and service mode (`executionMode: service`)." After this change Lenny runs only `type: agent` runtimes in session mode. The owner asked this design to decide whether `type` and `executionMode` go entirely. | Decision file, "Owner decisions (2026-11-13)"; the owner's direction of 2026-11-13 | AR-1, AR-2, O1, O2 |
| D2 | What leaves with service mode. | "With service mode gone, `startId` goes (the owner's point), along with any remaining sessionId-on-frames multiplexing, service-mode tenant-affinity routing, `maxConcurrentRequestsPerPod`, the service-mode tool refusal, and the service-mode rows everywhere." | Decision file, "Owner decisions (2026-11-13)" | AR-2, AR-3 |
| D3 | The `type: mcp` supervisor questions. | The removal "makes the type: mcp supervisor questions moot." | Decision file, "Owner decisions (2026-11-13)" | AR-1, AR-5 |
| D4 | Open decisions O1 to O8 of revision 1 (2026-11-13). | "O5 (b), yes to all the others." O1 (a), O2 (a), O3 (b), O4 (a), O5 (b), O6 (a), O7 (a), and O8 (b). | Decision file, "AR decisions settled, 2026-11-13" | O1 to O8 below, AR-3, AR-4, AR-5 |
| D5 | Why `session_start` keeps `sessionId` under O5 (b) (2026-11-13). | The owner asked why the member stays on `session_start`. The answer given: it is the only carrier of the session's identifier to the runtime, it stays the same across restarts of the runtime process, the runtime uses it to correlate its logs with the platform's for support, to build idempotency keys for actions outside the session, and to detect that its session was derived. The owner answered "ok, let's keep it". The adapter never acts on the member. | Decision file, "AR decisions settled, 2026-11-13" | AR-3, ARR-6 |
| D6 | Whether O3's refusal is the rule for removed fields in every design (2026-11-13). | "ok", in the owner's answers to the consolidated list of open decisions of 2026-11-13: one rule for removed fields everywhere. A write that names a removed field is refused with `400 VALIDATION_ERROR` naming the field. The answer settles O3 of [`0132-llm-response-cache-removal`](../0132-llm-response-cache-removal/decisions.md#decisions), and LU-4 of [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md) applies it to `deliveryMode`, `credentialPolicy`, `supportedProviders`, `credentialCapabilities`, and the other fields it removes. | Decision file, "Owner answers to the consolidated list, 2026-11-13", item 6 | AR-4 |

D2 settles open decision C1 of the message delivery design as its option (c), removal of `startId`.

The owner settled open decisions O1 to O8 of revision 1 on 2026-11-13 with "O5 (b), yes to all the others" (D4). Each entry below keeps its options and reasoning as the record of the choice, and states the option taken.

**O1. Whether the `type` field stays.**

- (a) Remove `type` from the `Runtime` resource, the CRD, the admin API, the runtime row and its CHECK constraint, `GET /v1/runtimes`, `list_runtimes`, the runtime scaffolder, and the documentation. Every runtime is an agent runtime.
- (b) Keep `type` as an optional field whose only value is `agent`, defaulted when absent.
- (c) Keep `type` as an open enum reserved for a later runtime kind.

**Recommendation: (a).** A field with one legal value tells a reader nothing and costs a CHECK constraint, an enum, a validator, and a documentation entry. Under (b) every example and every reader still carries the word. (c) reserves a value space for a feature nobody has designed, and a later kind would need its own design and its own field name anyway. Lenny has no deployments, so no stored resource carries the field.

**Settled 2026-11-13: (a)** (D4).

**O2. Whether `executionMode` stays.**

- (a) Remove `executionMode` from the `Runtime` resource, `PodSet`, `AgentPod`, the `PodRecord` and `PodSpec` interfaces of [§12.6 Interface Design](../../spec/12_storage-architecture.md#126-interface-design), the runtime, session, pod-registry, and agent-pod-state rows, `sessionIsolationLevel`, the derive's equality check, and the `INCOMPATIBLE_RUNTIME` error.
- (b) Keep it with the single value `session`.
- (c) Keep it as an enum reserved for a later mode.

**Recommendation: (a)**, for the reasons of O1. [`ROADMAP.md`](../../ROADMAP.md) lists an "External-parent session mode" as `executionMode: external` on `POST /v1/sessions` **[source]**. That is a session request field rather than the runtime field, and a design for it chooses its own name, so (a) does not block it.

**Settled 2026-11-13: (a)** (D4). The derive's mode check and `INCOMPATIBLE_RUNTIME` leave with the field.

**O3. How a write that names a removed field is answered.** The admin API decodes JSON without refusing unknown members **[source]**, so a field that only disappears from the schema is ignored, and a `type: mcp` registration would register the MCP server image as an agent runtime. It would then fail at its first bind, when the runtime does not answer the handshake of [RP-1](../0139-runtime-protocol/change.md#rp-1-handshake) **[inference]**.

- (a) The removed fields disappear from the schemas and are ignored as any unknown member is.
- (b) The removed fields disappear from the schemas and the documentation, and the admin API, the bootstrap seed, and `lenny-ctl runtime validate` refuse a write that names one with `400 VALIDATION_ERROR`, whose message names the field and states that Lenny runs agent runtimes in session mode. The CRDs drop the fields from their structural schemas, so the API server's field validation reports them as unknown **[inference]**.
- (c) Keep the fields with one legal value and refuse any other value.

**Recommendation: (b).** It turns a late, confusing bind failure into an immediate error that names the cause, and it costs one list of three names in one validator. It adds no alias and no second code path, so it is a refusal rather than a compatibility mode.

**Settled 2026-11-13: (b)** (D4). A write that names a removed field is refused with `400 VALIDATION_ERROR` naming the field.

**O4. Whether `sessionIsolationLevel` keeps `executionMode` and `conversationContinuity`.** After the removal `executionMode` is always `session` and `conversationContinuity` is always `"platform"`.

- (a) Remove both fields from `sessionIsolationLevel`, the client SDK types, and the OpenAPI document. The hold rule of the Open Responses and Messages adapters reduces to its capability condition: the runtime declares `multi_turn` with injection.
- (b) Keep both with their constant values.

**Recommendation: (a).** Lenny has no deployed clients, and a constant field invites a client to branch on it.

**Settled 2026-11-13: (a)** (D4).

**O5. Whether a session-scoped frame may still carry `sessionId`.** RPR-18 takes a frame's session from its connection, and a frame that names another session is refused (SIR-3) **[source]**. RP-3 keeps the member optional "so that one schema serves every mode" **[source]**.

- (a) Keep `sessionId` optional on session-scoped frames, accepted when it names the slot's session and refused otherwise, with the reason restated: it lets a runtime log under the identifier and catches a runtime that confuses sessions.
- (b) Remove the member from every frame other than `session_start`. A runtime that still writes it on another frame is unaffected: the adapter treats it as an unknown member of a known frame, which the leniency rules of "MD-24" ignore silently. Only an unknown frame type produces a `runtime_warning`.

**Recommendation of revision 1: (a).** Revision 1 argued that the runtime SDKs and the frame examples of the message delivery design already send `sessionId`, so (b) would turn every such frame into a warning, and that (a) keeps the slot check that session isolation states for its own reasons. The first half rested on a misreading of "MD-24": an unknown member is ignored with no report, and only an unknown frame type is reported **[source]**. Under (b) a runtime that still sends the member therefore sees no change, and the slot check has nothing left to check, because the adapter takes every frame's session from its connection's slot and reads no session from the frame. The multiplexing rules leave under both options.

**Settled 2026-11-13: (b)** (D4). No frame carries `sessionId` except `session_start`, which keeps it as data the runtime reads (D5): the runtime correlates its logs with the platform's for support, keeps one identity across restarts of its process, builds idempotency keys for actions it takes outside the session, and detects that its session was derived. The adapter never acts on it. Session isolation's refusal of a frame that names another session (SIR-3) leaves with the member, because no frame names a session any more.

**O6. Whether a `type: mcp` use case needs a replacement now.**

- (a) No replacement. The documentation states the alternatives of "AR-6".
- (b) Record Lenny-hosted MCP servers as a candidate in [§21 Planned / Post-V1](../../spec/21_planned-post-v1.md).
- (c) Build a hosted-connector feature now, in which Lenny runs a connector's MCP server in a pod it manages.

**Recommendation: (a).** No user, test outside the reference binary, or reference runtime depends on the mode, and the endpoint has never served a call in production **[source]**. Connectors already cover MCP servers that run elsewhere, and an agent runtime can launch an MCP server inside its own slot. A later design can propose (c) from a stated need.

**Settled 2026-11-13: (a)** (D4).

The recommendation above names connectors as they stood on 2026-11-13. [`0124-connector-removal`](../0124-connector-removal/change.md) removed them on 2026-11-15, so an MCP server that runs elsewhere is an operator service that a runtime reaches through egress (CR-3), and option (c) has no connector feature to extend.

**O7. Headings that name the removed concepts.** [§5.2 Pool Configuration and Execution Modes](../../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) has about 168 inbound links, and [§4.7.9 Startup Sequence for `type: agent` Runtimes](../../spec/04_system-components.md#479-startup-sequence-for-type-agent-runtimes) has 19 **[source]**.

- (a) Rename §5.2 to "5.2 Pool Configuration" and §4.7.9 to "4.7.9 Runtime Startup Sequence", and move both anchors through the anchor-redirect map. §5.2's "Execution Modes" and "Execution Mode Scaling Implications" subsections become "Session policy" material and "Scaling Implications".
- (b) Keep both headings.

**Recommendation: (a).** A heading that names a removed concept misleads every reader who follows a link to it, and the anchor-redirect map exists to move anchors without breaking inbound links.

**Settled 2026-11-13: (a)** (D4). §5.2 becomes "5.2 Pool Configuration" and §4.7.9 becomes "4.7.9 Runtime Startup Sequence", each through the anchor-redirect map.

**O8. Where the fail-closed check on a stale runtime row lives.** SD-P4 now refuses only a target whose runtime row is not `registered`, because DR-4's mirror leaves a row stale when it refuses a `Runtime` resource (the "Supervisors" subsection of [`0144-session-derive`](../0144-session-derive/change.md) before its revision 18, now ["Final runtime row"](../0144-session-derive/change.md#final-runtime-row)) **[source]**. That concern is not specific to supervisors or to derive. Whether a create or a delegated child onto a row that is not `registered` is refused today is not established **[unverified]**.

- (a) Remove SD-P4 and `target_unsupervised` and leave stale rows to whatever the general target resolution does.
- (b) Remove SD-P4 and `target_unsupervised`, and require at the post-chain point, for every create, derive, and delegated child, that the final runtime row is `registered`, answered as an unknown runtime. DR-4 owns the rule.

**Recommendation: (b).** It keeps the fail-closed property SD-P4 provided and applies it to every path that can land on a stale row.

**Settled 2026-11-13: (b)** (D4). SD-P4 and `target_unsupervised` leave, and [`0147-tenant-derived-runtimes`, "DR-4"](../0147-tenant-derived-runtimes/change.md#dr-4-tenant-runtime-records) owns the check at the post-chain point that the final runtime row of every create, derive, and delegated child is `registered`, answered as an unknown runtime. DR-4's "Registered rows" paragraph states the details: the answer is `RESOURCE_NOT_FOUND`, or the surface's own not-found answer, which is `TARGET_NOT_IN_SCOPE` for a delegated child; a platform-derived runtime also needs its base's row to be `registered`; the admin API writes `true` on a row it registers with no `Runtime` resource; and a resume, which reads the session's stored runtime, is not refused.

## Open decisions

"Decisions" records O1 to O8, which the owner settled on 2026-11-13 (D4), with their options, their reasoning, and the option taken. The questions below are open:

1. **Error detail format for a removed field.** D6 of [`0119-agent-runtimes-only`](../0119-agent-runtimes-only/decisions.md#decisions) refuses a write that names a removed field with `400 VALIDATION_ERROR` whose `details.field` names it, and 0132's O3 and tests and 0133's D9 repeat that wording. §15.1's `VALIDATION_ERROR` example and `docs/reference/error-catalog.md` put the field in `details.fields[].field` instead. Options: (a) state the refusal as `details.fields[].field` in 0119's D6, 0132's O3 and tier 1 and tier 3 tests, and 0133's D9, matching §15.1; (b) keep `details.field` and change §15.1's format for this refusal. Recommendation: (a), because every other validation error already uses the array. Raised on 2026-11-16 by the review of 0132.
   - Extended by the M3 readiness review, 2026-11-19. The same wording appears in [`0124-connector-removal`](../0124-connector-removal/change.md) ("Removed fields (D7)": "refuse, with `400 VALIDATION_ERROR` whose `details.field` names the member"), and `details.field` also appears in 0126, 0137, 0140, 0141, 0144, 0147, and 0150, so option (a) applies to every member change that states a removed-field refusal, 0124 included, and the owner's answer is recorded in 0119, 0124, 0132, and 0133 together. The bootstrap seed answers `207` with a per-entry `SEED_VALIDATION` error (`pkg/gateway/api/admin/bootstrap.go`), not `400`, so the answer also states how the seed reports the removed field: under (a), as a per-entry error that names the field in the `207` body. Blocks: 0119.S4's refusal of the runtime fields, and the refusals of 0119.S1 and 0119.S2 under "AR-4" (**Which step refuses which field.**). (sources: 0119-F18, connectors-llm-billing-C9, program-C5)
2. **The `types` member of delegation targets and environment selectors.** O1 (a) removes the runtime `type` from the runtime row, and two policy inputs select runtimes by it: the `types` member of a `DelegationPolicy` target ([§8.3](../../spec/08_recursive-delegation.md#83-delegation-policy-and-lease), `types: [agent]`) and of an environment's `runtimeSelector` ([§10.6](../../spec/10_gateway-internals.md#106-environment-resource-and-rbac-model), `types: [agent, mcp]`), evaluated in `pkg/delegation/delegationpolicystore/evaluate.go` and `pkg/environment/environment.go` against candidates built with `Type: string(rt.Type)`. With no type on the row, a rule with `types: [agent]` and `allow: true` matches nothing and a rule with `allow: false` stops denying. [`0124-connector-removal`](../0124-connector-removal/change.md) removes the `connector` value and says nothing of the member. Options: (a) remove `types` from the delegation target and from the environment selector, refuse a write that names it under D6, and add the §8.3 and §10.6 edits, the code rows (`pkg/delegation/delegationpolicystore`, `pkg/environment`, `pkg/tenancy/envaccess`, and the admin payloads `SelectorPayload.Types` and `DelegationTargetPayload.Types`), and a test; (b) keep `types` with the one value `agent`, matched against a constant. Recommendation: (a), because it follows O1 (a), and after 0124 removes `connector` the member has `agent` as its only value. Blocks: 0119.S4. Names 0124, whose `DelegationPolicy` refusal of the `connector` target type reads together with the answer. (raised by the M3 readiness review, 2026-11-19; sources: 0119-F1)
3. **How the admin API and the seed recognise a removed field.** D6 and revision 3 state one list of removed field names in the admin validator. A list of bare names refuses valid writes, because `type` is a live member of a runtime document (`agentInterface.inputModes[].type`, `sharedAssets[].type`) and of other admin resources, so "AR-4" now keys the list by resource and JSON path. Issue `ADMIN-I1`, filed on 2026-11-18 after D6, records that the bootstrap ignores unknown fields and suggests refusing every unknown field with `VALIDATION_ERROR` naming it, which would refuse every removed field on the seed path without a list. Options: (a) decode admin writes and seeds strictly, so every unknown member is refused by path, and keep a per-resource path list only for the "was removed" message; (b) keep D6's list, keyed by resource and path, and decode leniently otherwise. Recommendation: (a), because it closes `ADMIN-I1` with the same code and removes the name-collision problem. Blocks: the refusal code of 0119.S1, 0119.S2, and 0119.S4. (raised by the M3 readiness review, 2026-11-19; sources: 0119-F2)
4. **The Kubernetes write path of a removed field.** ARR-3 covers the admin API, the bootstrap seed, and `lenny-ctl runtime validate`. A `Runtime` or `PodSet` resource applied to the cluster is a further write path, and the chart renders reference runtimes through it (`charts/lenny/templates/reference-runtimes.yaml`). When the CRDs drop the properties from their structural schemas, the API server prunes an unknown field and reports it only to a client that asks for strict field validation, so a resource written by another client is mirrored by the RuntimeReconciler as an agent runtime with the field gone, which is the outcome "AR-4" **Why a refusal rather than silence.** rejects. Options: (a) keep `type`, `executionMode`, and `maxConcurrent` declared in the CRD schemas with a validation rule that refuses their presence, so every client is refused, which amends O1 (a) and O2 (a) for the CRD schemas alone; (b) drop the properties, accept pruning for clients that do not ask for strict validation, and state in the documentation that only `kubectl` and the admin API refuse. Recommendation: (a), because it is the only variant that holds for every client. Blocks: 0119.S4 and its `component` test on envtest of the resource path. (raised by the M3 readiness review, 2026-11-19; sources: 0119-F3)
5. **Whether O7's heading renames stand.** O7 (a), settled on 2026-11-13, renames §5.2 to "5.2 Pool Configuration" and §4.7.9 to "4.7.9 Runtime Startup Sequence" through the anchor-redirect map. Change 0115 landed afterwards, and `spec/conventions.md`, "Successor stubs", now states: "The heading and the anchor of a numbered section do not change while any section citation points at them." About 2,800 Go lines cite §5.2, 18 cite §4.7.9, and 50 carry the heading text "Pool Configuration and Execution Modes" in the `§N.M (heading)` form. [`0127-egress-and-git`](../0127-egress-and-git/change.md) and [`0139-runtime-protocol`](../0139-runtime-protocol/change.md) repeat the rename. Options: (a) withdraw the renames: the numbered headings stay until their area migrates, 0119.S4 edits the section bodies, the pages that receive the content are titled without the removed concepts, and 0127 and 0139 drop their mentions; (b) keep O7 and amend "Successor stubs" in `spec/conventions.md` to allow a rename through the anchor-redirect map. Recommendation: (a), because the rename has no lasting effect once the sections are stubs, and it would touch annotations that the area change replaces. Blocks: 0119.S4 (the heading renames) and the changelog entry that names them. (raised by the M3 readiness review, 2026-11-19; sources: 0119-F9)
