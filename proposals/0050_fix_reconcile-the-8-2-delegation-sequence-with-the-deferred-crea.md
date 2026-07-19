# Proposal: Reconcile the §8.2 delegation sequence with the deferred create-then-materialize model: allocate the pod and credential lease at the child's session-start rather than inline at delegation step 5

- **Status:** Verified (2026-07-19). Converged after 3 adversarial review rounds (1 finding fixed); awaiting sign-off. One open decision gates the spec edits (§9): whether the platform commits to the deferred create-then-materialize model (option b, recommended) or to inline delegation-time allocation with a new delegate-path pod claim (option a).
- **Date:** 2026-07-19.
- **Scope:** A spec-first reconciliation of the §8.2 delegation-flow allocation-timing wording in `spec/08_recursive-delegation.md` (the step-2a cycle-detection rationale at `:64` and the §8.3 credential-availability pre-check at `:470`), the `created`-state pod-claim notes in §8.8 (`:879`) and §15.1 (`spec/15_external-api-surface.md:630`), plus the code-comment and ledger updates that follow from it: the `taskHandle.State` doc comment in `pkg/gateway/mcpfabric/mcptools/mcptools.go`, and the TEST-GAPS / BUILD-GAPS / PROPOSAL-QUEUE ledger entries that track the divergence (T-8.2.17, T-ADV.14, C-42). The delegate handler admits a child and commits it in `session.StateCreated` with no pod assignment; it claims no warm pod, assigns no credential lease, and injects no virtual MCP child interface inline. The §8.2 flow text instead places pod allocation inline at step 5 and materialization at steps 6-9. This proposal reconciles the allocation-timing wording to the deferred model, records the still-unbuilt materialization as a BUILD-GAPS finding, and closes the reconciliation half of T-8.2.17. It changes no runtime behavior, touches no shared pod-claim primitive, and does not build the delegated-child materialization flow.

This document stages the proposed spec, code-comment, and ledger changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off, spec edit first, and only after the §9 open decision is ratified.

## 1. Problem

§8.2 defines a nine-step delegation flow in which the gateway allocates the child pod inline at step 5 (`spec/08_recursive-delegation.md:93`), streams rebased files into the child at step 6 (`:94`), starts the child at step 7 (`:95`), and creates and injects a virtual MCP child interface at steps 8 and 9 (`:96-97`). The step-2a cycle-detection paragraph asserts that the child session does not exist yet at cycle-detection time "because pod allocation happens in step 5" (`:64`). The §8.3 credential pre-check at `:470` narrates a post-pod-claim assignment race, a pod release, and a `CREDENTIAL_POOL_EXHAUSTED` rejection as though the pod claim occurs at delegation admission.

The implementation diverges. The delegate handler runs admission only (input validation, cycle detection, depth resolution, file export, and child-token minting) and commits the child via `insertChildSession` in `session.StateCreated` with no `PodAssignment` (`pkg/gateway/mcpfabric/delegation/service.go:855`, `:1173`, `:1425`). It claims no warm pod, assigns no credential lease, and creates no virtual MCP interface inline. The 0044 delegation-time availability pre-check (`pkg/gateway/mcpfabric/mcptools/mcptools_register.go:2051-2093`, ahead of the `Delegate` call at `:2278`) is a point-in-time credential read that claims no pod (`pkg/gateway/sessionserver/start.go:1305-1337`), so it cannot fail-fast on an actual pod claim, and the `:470` post-claim assignment race has no code path on the delegate handler.

The determination this reconciliation had to settle is whether the deferred model delivers steps 6 through 9. It does not. The materialization flow is unbuilt: the `taskHandle.State` doc comment states that "§8.2 step 7 (pod allocation + workspace materialization) is unbuilt; once the allocation flow lands, the state will be `submitted` or `running`" (`pkg/gateway/mcpfabric/mcptools/mcptools.go:924-927`), the T-ADV.14 dependency line records that "That flow does not exist; building it is a prerequisite" (`TEST-GAPS.md:12110`), `Executor.Send` presupposes an already-bound pod (`pkg/gateway/session/executor/pod.go:136`), and the virtual MCP child interface has a code manifestation as the always-on gateway MCP tool set plus the resume-time `children_reattached` reconstruction (`pkg/gateway/sessionserver/start.go:3217`, fired from the resume handler at `:3073`). A `StateCreated` delegated child does not currently traverse any start path.

The finding ledger records the divergence. T-8.2.17 (Medium, `TEST-GAPS.md:2592`) names the spec-vs-code allocation-timing divergence and defers to the proposal pipeline the choice between inline step-5 allocation and a spec amendment to the deferred model. T-ADV.14 (Low, `TEST-GAPS.md:12106`) records the post-pod-claim assignment-race coverage as unbuildable until a pod claim exists on the delegate path. C-42 (`PROPOSAL-QUEUE.md:476`) is the queue cluster for this reconciliation, rooted on T-8.2.17.

## 2. Decisions

- **The deferred model does not deliver §8.2 steps 6 through 9.** The child sits in `session.StateCreated` (`service.go:1425`); nothing in the delegate path allocates a pod, starts the child, or injects a virtual MCP interface. The materialization flow is unbuilt (`mcptools.go:924-927`; T-ADV.14 dependency line). The reconciliation is a wording alignment plus a build-gap filing, rather than a claim that the deferred flow is complete.
- **Recommend option (b): reconcile §8.2 to a two-phase create-then-materialize model.** Pod allocation, workspace stream, child start, and virtual-interface injection occur when the delegated child materializes through the shared canonical session-start and finalize path, the same path a top-level session uses. Building a second inline pod-claim, lease, and child-start path in the delegate handler (option a) would duplicate the session-start claim engine (`resolveCredentialPools` → `credrouter.PreClaim`; the create-time claim; the finalize-time lease; and the `assignment_race` path at `start.go:166`), against the single-canonical-implementation-per-concern and no-dual-modes principles in `code-best-practices.md`.
- **Neither option closes the materialization gap in this proposal.** Both leave steps 6 through 9 unbuilt. Option (a) would relocate the equally-unbuilt materialization inline into the delegate handler. The choice reduces to which architecture is the correct home for the yet-to-be-built materialization, which is why the decision is left to the reviewer in §9 rather than settled here.
- **§8.8's canonical session-state-machine framing supports the deferred model.** `spec/08_recursive-delegation.md:835` states that a session is the only unit of execution, that `lenny/delegate_task` creates a child session, and that the child flows through the canonical §7.2 session state machine (`created` → `running`). Option (b) aligns the §8.2 allocation-timing wording with that framing rather than inventing a parallel inline lifecycle. The `created`-state protocol mapping at `:879` and the parallel §15.1 external-state description at `spec/15_external-api-surface.md:630` both state that a session in `created` has a warm pod claimed. That statement is the top-level create-path invariant: the create atomic unit claims an idle warm pod at create before the row persist, so the invariant holds uniformly for a top-level session (`pkg/gateway/sessionserver/create.go:327-329`). A delegated child is committed to `created` with no `PodAssignment` (`service.go:1425`), so SPEC-6 below qualifies both created-state notes to place the delegated child's warm-pod claim at its session-start.
- **Under option (b) the `:470` assignment race becomes reachable at the child's session-start by reuse.** `start.go:159-166` already emits `CREDENTIAL_POOL_EXHAUSTED` with reason `assignment_race` after a pod claim, and `finalize.go` maps the finalize-time credential mismatch into that outcome. No second inline race implementation is needed, and the coverage re-filed as T-ADV.14 becomes buildable once the materialization build lands.
- **This proposal touches no shared pod-claim primitive.** It amends spec wording, one code comment, and the ledgers. It does not touch `poolstore`, `credrouter.PreClaim`, or the `start.go:166` `assignment_race` path. The C-42 coordination with proposal-B's C-22 (§4.6 eviction) and with C-01 over the shared warm-pool claim primitives (`PROPOSAL-QUEUE.md:477`, `:483`) applies to the follow-on materialization build rather than to this reconciliation. Only option (a) would touch those primitives and require the integrator-inbox serialization flag.
- **The 0044 delegation-time availability pre-check is correct as a point-in-time read and is unchanged** (`mcptools_register.go:2051-2093`, `start.go:1305-1337`). It is the pre-claim half of `:470`; the post-claim half moves to the child's session-start under option (b).
- **The reconciliation does not assert the deferred flow is complete.** The spec amendment reconciles the allocation-timing wording and re-homes the steps-6-through-9 materialization build from "inline at delegation step 5" to "at the child's session-start," recording that build as remaining unbuilt work through a BUILD-GAPS finding. This keeps option (b) a faithful reconciliation rather than a claim over a gap.
- **Spec-first, per `spec-driven-development.md`.** Land the §8.2 and §8.3 wording edits, then the code-comment update that cites the amended sections. No runtime behavior changes in this proposal; the materialization build is separately tracked follow-on work.

## 3. The delegate path after the reconciliation

The reconciliation changes wording and tracking rather than behavior. After it, the spec and code agree on where allocation happens:

- **Admission.** The delegate handler validates input, checks the runtime-lineage cycle, resolves depth, exports files, mints the child token, and commits the child row in `session.StateCreated` with no pod assignment (`service.go:855`, `:1425`). The 0044 point-in-time credential availability pre-check runs here and rejects with `CREDENTIAL_POOL_EXHAUSTED` before the child row is committed when the prospective child has no assignable credential (`mcptools_register.go:2051-2093`).
- **Materialization (unbuilt, re-homed).** When the delegated child materializes through the shared session-start and finalize path, the gateway claims a warm pod, assigns the credential lease (`resolveCredentialPools` → `credrouter.PreClaim`), streams the exported files into the child workspace via the §6.3 binder (already stamped on the child `WorkspacePlan`, F-8.2.4 CLOSED), starts the child, and exposes the parent-facing virtual child interface (the always-on gateway MCP tool set keyed by child session id, F-8.2.11 CLOSED). This flow does not exist for a delegated child today; a `StateCreated` delegated child traverses no start path. The BUILD-GAPS finding in DOC-1 tracks wiring it.
- **Post-claim assignment race.** The race becomes reachable at the child's session-start by reuse of `start.go:159-166` once the materialization build lands. No second inline race path is added.

## 4. Proposed changes

### SPEC-2. Tie the step-2a cycle-detection rationale to the child-row INSERT rather than step-5 pod allocation

**Target:** `spec/08_recursive-delegation.md` §8.2, the step-2a cycle-detection paragraph (`:64`).

**Rationale:** Line `:64` justifies runtime-identity (rather than `session_id`) cycle detection with the parenthetical "pod allocation happens in step 5." The child session id is minted at the atomic child-session INSERT that concludes admission (`service.go:1173`, `:1425`), and cycle detection runs before that INSERT. The correct reason the child does not exist yet at cycle-detection time is that the child row is not inserted until after cycle detection, which is accurate under both the inline (option a) and the deferred (option b) allocation models. The pod is a separate resource from the child session id under either model, so tying the rationale to the INSERT removes a brittle dependence on the pod-allocation step number.

**Anchor:** In the sentence beginning "Cycle detection uses runtime identity — not `session_id` — because the child session does not exist yet at this point," replace the trailing parenthetical only. Leave the runtime-identity rationale, the pool-differentiated-cycles paragraph, and the cycle-detection decision matrix unchanged.

**Change (staged text).** Replace:

```
because the child session does not exist yet at this point (pod allocation happens in step 5).
```

with:

```
because the child session does not exist yet at this point (the child-session INSERT that mints its id runs at the end of admission, after cycle detection).
```

**Preserved unchanged:** the runtime-identity rationale, the pool-differentiated-cycles paragraph, and the three-layer cycle-detection decision matrix.

### SPEC-4. Locate the §8.3 pre-check's post-claim pod claim and lease assignment at the child's session-start

**Target:** `spec/08_recursive-delegation.md` §8.3, the credential-availability pre-check paragraph (`:470`).

**Rationale:** The delegation-time pre-check is a point-in-time credential read that claims no pod (0044; `start.go:1305-1337`) and is correct as written. The paragraph's last sentence already delegates the post-pod-claim assignment race, pod release, and `CREDENTIAL_POOL_EXHAUSTED` to §7.1 session-creation behavior ("consistent with the session-creation behavior in Section 7.1"), so the text is already location-agnostic and its "after pod claim" phrasing already points to where the claim occurs. Under option (b) two clauses of `:470` still frame the pod claim as part of the inline `delegate_task` sequence: the opening "before claiming a warm pod" and the last sentence's "after pod claim." The minimal fix rewords the opening clause so its subject is the child's deferred claim, and inserts a clause into the last sentence locating that claim at the child's session-start, keeping the existing §7.1 cross-reference that already carries the meaning. This change is specific to option (b); under option (a) the pod claim would occur inline at delegation admission and neither clause would be reworded (see §9).

**Anchor:** Two edits within the pre-check paragraph. First, in the opening sentence ending "it performs the same pre-claim credential availability check described in [Section 4.9] before claiming a warm pod," reword the trailing clause so it no longer implies the claim is part of `delegate_task` processing. Second, in the last sentence, beginning "If the actual credential assignment fails after pod claim (due to this race), the gateway releases the pod back to the warm pool and returns `CREDENTIAL_POOL_EXHAUSTED`," add a clause locating the pod claim at the child's session-start before the existing §7.1 cross-reference. Do not introduce a "one-winner-of-N" phrase; that wording does not appear elsewhere in the spec and adds no normative content.

**Change (staged text), opening clause.** Replace:

```
it performs the same pre-claim credential availability check described in [Section 4.9](04_system-components.md#49-credential-leasing-service) before claiming a warm pod.
```

with:

```
it performs the same pre-claim credential availability check described in [Section 4.9](04_system-components.md#49-credential-leasing-service) before the child's warm pod is claimed.
```

**Change (staged text), last sentence.** Replace:

```
If the actual credential assignment fails after pod claim (due to this race), the gateway releases the pod back to the warm pool and returns `CREDENTIAL_POOL_EXHAUSTED`, consistent with the session-creation behavior in [Section 7.1](07_session-lifecycle.md#71-normal-flow).
```

with:

```
The warm pod is claimed and the credential lease assigned when the child materializes at its session-start ([Section 7.1](07_session-lifecycle.md#71-normal-flow)), not at delegation admission. If the actual credential assignment fails after that pod claim (due to this race), the gateway releases the pod back to the warm pool and returns `CREDENTIAL_POOL_EXHAUSTED`, consistent with the session-creation behavior in [Section 7.1](07_session-lifecycle.md#71-normal-flow).
```

**Preserved unchanged:** the point-in-time pre-check description, the `inherit` and `independent` pre-claim rules, the "before pod allocation" rejection phrasing, and the cross-environment compatibility check at `:472`.

### SPEC-6. Qualify the `created`-state pod-claim notes for a delegated child

**Targets:** `spec/08_recursive-delegation.md` §8.8 session-level state-mapping table, the `created` row (`:879`); `spec/15_external-api-surface.md` §15.1 external-session-state table, the `created` row (`:630`).

**Rationale:** Both created-state notes state that a session in `created` has a warm pod claimed. That is accurate for a top-level session: the create atomic unit runs the availability pre-check and claims an idle warm pod before the row persist, so the §15.1 created-state pod-claim invariant holds uniformly for the create path (`create.go:327-329`). A delegated child is committed to `created` at delegation admission with no `PodAssignment` (`service.go:1425`); under option (b) it claims its warm pod and is assigned its credential lease when it materializes at its own session-start. Left unedited, these two notes assert the pod is claimed at `created` while SPEC-4 defers the delegated child's claim to session-start, which leaves the spec internally inconsistent and a client querying a delegated child's task state seeing `submitted` (from `created`) carrying the documented meaning "warm pod claimed" while no pod is held. The fix adds one clause to each note qualifying the delegated-child case, keeping §8.8 and §15.1 consistent with §8.2 and §8.3.

**Anchor (§8.8:879).** In the `created` row's Notes cell, after the existing "awaiting workspace uploads or finalization" clause and the canonical-description cross-reference, add a sentence covering the delegated-child case. Leave the `submitted` mappings and every other row unchanged.

**Change (staged text, §8.8:879).** Replace:

```
Session created; warm pod claimed and credential availability pre-checked, the credential lease is assigned at finalize, awaiting workspace uploads or finalization (see [§15](15_external-api-surface.md#151-rest-api) for canonical description).
```

with:

```
Session created; warm pod claimed and credential availability pre-checked, the credential lease is assigned at finalize, awaiting workspace uploads or finalization (see [§15](15_external-api-surface.md#151-rest-api) for canonical description). A delegated child ([Section 8.2](#82-delegation-mechanism)) is the exception: it is committed to `created` at delegation admission with no warm pod claimed, and it claims its warm pod at its own session-start.
```

**Anchor (§15.1:630).** In the `created` row's Description cell, qualify the opening "a warm pod has been claimed" clause with the delegated-child exception. Leave the TTL, expiry, and pod-release wording unchanged.

**Change (staged text, §15.1:630).** Replace:

```
Session created; a warm pod has been claimed and credential availability has been pre-checked (see [§7.1](07_session-lifecycle.md#71-normal-flow) steps 3–4); the credential lease is assigned at finalize, awaiting workspace file uploads or finalization.
```

with:

```
Session created; for a top-level session a warm pod has been claimed and credential availability has been pre-checked (see [§7.1](07_session-lifecycle.md#71-normal-flow) steps 3–4), and the credential lease is assigned at finalize. A delegated child ([Section 8.2](08_recursive-delegation.md#82-delegation-mechanism)) is instead committed to `created` at delegation admission with no warm pod claimed, and claims its warm pod and is assigned its credential lease at its own session-start. The session is awaiting workspace file uploads or finalization.
```

**Preserved unchanged:** the `submitted` and `working` protocol mappings, the TTL and expiry behavior, the pod-release-on-expiry wording, and every other row of both tables.

### CODE-1. Correct the `taskHandle.State` doc comment's step citation and keep the unbuilt warning

**Target:** `pkg/gateway/mcpfabric/mcptools/mcptools.go`, the `taskHandle.State` doc comment (`:923-928`).

**Rationale:** The comment states that v1 returns `created` because "§8.2 step 7 (pod allocation + workspace materialization) is unbuilt." The parenthetical mis-attributes pod allocation to step 7; the §8.2 flow places pod allocation at step 5 (`:93`), the file stream at step 6 (`:94`), and step 7 is the child start (`:95`). The citation is brittle and inaccurate regardless of the §9 decision. The fix corrects the step attribution to the materialization span (steps 5 through 9) and keeps an explicit present-tense statement that the delegated-child materialization path is unbuilt and that a `created` child does not currently traverse a start path. The present-tense warning belongs in `mcptools.go` for the reader of that file; the DOC-1 BUILD-GAPS finding tracks the build in addition to, rather than in place of, the in-code warning.

**Change (staged text).** Replace the `State` field's doc comment:

```go
	// State is the child's §8.8 task state at admission. v1 returns
	// `created` (the §7 session create state) because §8.2 step 7 (pod
	// allocation + workspace materialization) is unbuilt; once the
	// allocation flow lands, the state will be `submitted` or `running`
	// per §8.8.
	State string `json:"state"`
```

with:

```go
	// State is the child's §8.8 task state at admission. v1 returns
	// `created` (the §7 session create state) because the §8.2
	// materialization span (steps 5-9: warm-pod allocation, workspace
	// stream, child start, and virtual-interface injection) is unbuilt for
	// a delegated child: the delegate handler commits the child in
	// session.StateCreated and it traverses no session-start path today, so
	// it never transitions to `submitted` or `running`. Once the
	// materialization flow lands at the child's session-start (§7.1), the
	// state will advance per §8.8. Tracked as a BUILD-GAPS finding.
	State string `json:"state"`
```

### DOC-1. Mark T-8.2.17 resolved, re-home T-ADV.14, and file the residual materialization build gap

**Targets:** `TEST-GAPS.md` T-8.2.17 (`:2592`) and T-ADV.14 (`:12106`, dependency line at `:12110`); `BUILD-GAPS.md` (new finding); `PROPOSAL-QUEUE.md` C-42 (`:476-483`).

**Rationale:** On application, the T-8.2.17 spec-vs-code allocation-timing divergence is reconciled under option (b). The steps-5-through-9 materialization for a delegated child remains unbuilt (`mcptools.go:924-927`) and is re-homed by the amendment from "inline at delegation step 5" to "at the child's session-start," so that build needs a tracked home rather than being masked by the reconciliation. T-ADV.14's dependency wording (its blocked-on line names a "future §8.2 delegate-path pod-claim") should place the pod claim at the child's session-start, so its race becomes reachable through `start.go:166` reuse rather than a new inline race.

**Change (staged description).**

1. **T-8.2.17 (`TEST-GAPS.md:2592`).** Flip the checkbox to resolved, referencing this proposal. Record that the reconciliation chose option (b), the deferred create-then-materialize model, and that the residual steps-5-through-9 materialization build is re-homed to the child's session-start and filed as the new BUILD-GAPS finding below.
2. **T-ADV.14 (`TEST-GAPS.md:12106`, `:12110`).** Reword the dependency and gap lines to place the pod claim and credential-lease assignment at the child's session-start on the shared canonical path, so the post-claim assignment race becomes reachable through `start.go:159-166` reuse once the materialization build lands, rather than requiring a new inline race implementation. Keep the finding OPEN and Low; it remains owned by the ADV battery.
3. **New BUILD-GAPS finding.** File a finding scoped to the actual residual unbuilt work: a `session.StateCreated` delegated child (`service.go:1425`) never enters the shared session-start and finalize path, so the §8.2 step-5 warm-pod claim and the child-start transition never occur. Word it as "wire a `StateCreated` delegated child into the shared session-start and finalize path so it claims a warm pod, assigns its credential lease, and starts." Cross-reference F-8.2.4 (`BUILD-GAPS.md:8109`, the file-export and stream plumbing already stamped on the child `WorkspacePlan`, CLOSED) and F-8.2.11 (`BUILD-GAPS.md:8292`, the parent-facing virtual MCP child interface already built as the gateway MCP tool set, CLOSED) as already satisfied, rather than re-listing the file stream and the virtual-interface injection as unbuilt. Note the C-42 coordination with proposal-B's C-22 and C-01 over the shared warm-pool claim primitives applies to this build.
4. **C-42 (`PROPOSAL-QUEUE.md:476-483`).** Record that this proposal converged the C-42 reconciliation on option (b), the deferred/session-start home, and that the follow-on materialization build (the pod-claim-touching work that triggers the C-22/C-01 coordination) is filed as the new BUILD-GAPS finding.

## 5. Non-goals

- **Do not build inline delegation-time pod allocation or credential-lease assignment in the delegate handler (option a).** It would duplicate the canonical session-start claim engine (`resolveCredentialPools` → `credrouter.PreClaim`; the create-time claim; the finalize-time lease; and the `assignment_race` path at `start.go:166`), against single-canonical-implementation and no-dual-modes. Recorded as the rejected alternative in §9.
- **Do not restructure the §8.2 numbered delegation flow into admission and materialization phases in this proposal.** A restructure that dissolves the numbered steps 5 through 9 into prose would codify the temporary `created` return as a permanent contract before the materialization feature is designed or built, and would strand the "§8.2 step N" citations across roughly a dozen code comments in `pkg/gateway/mcpfabric/delegation` (for example `service.go:1366`, `:1480`, `:1491`, and `export/export.go:12`, `:53`, `:69`, `:143`, `:176`, `:185`, `:270`, `:295`, `:305`). The reconciliation instead fixes the two genuinely loose wording sites (`:64`, `:470`) and leaves the numbered flow intact, so those citations remain valid and no code-comment sweep is needed. Whether to land a full §8.2 flow restructure under option (b) is the §9 open decision.
- **Do not build the steps-5-through-9 materialization flow for delegated children** (pod allocation, workspace stream, child start, virtual MCP child-interface injection). It is separately tracked (`mcptools.go:924-927`; T-ADV.14; the DOC-1 BUILD-GAPS finding). This proposal reconciles the sequence and re-homes the build to session-start.
- **Do not reword the failure-path phrases at `:61`, `:63`, and `:157`.** "No child pod is allocated" (`:61`, `:63`) and "before pod allocation" (`:157`) are true under the deferred model, cite no step number, and so are not stranded by the reconciliation. Rewording only `:157` would also introduce inconsistency with the six sibling sites carrying the identical "before pod allocation" phrasing (`:214`, `:317`, `:324`, `:332`, `:360`, `:472`).
- **Do not note virtual-interface creation timing in the §8.2 virtual-child-interface lifecycle paragraph (`:108-112`).** That paragraph already states storage in per-session memory and reconstruction from `SessionStore` on parent pod failure, is accurate against the implemented resume-time reconstruction and the always-on gateway MCP tools, and cites no inline creation step. It has nothing to reconcile, and duplicating a creation-timing statement across two sections would invite drift.
- **Do not change the 0044 delegation-time credential-availability pre-check** (`mcptools_register.go:2051-2093`, `start.go:1305-1337`). It is a correct point-in-time pre-claim read under the amended model and is unchanged.
- **Do not modify any shared pod-claim or pod-release primitive** (`poolstore`, `credrouter.PreClaim`) or the `start.go:159-166` `assignment_race` path. Because this proposal is spec-only for the reconciliation, the C-42 pod-claim-collision coordination with proposal-B's C-22 (§4.6 eviction) and C-01 is not triggered here; it applies to the follow-on materialization build.
- **Do not implement the T-ADV.14 post-claim assignment-race tier-4 or chaos test.** That coverage is owned by the ADV battery and becomes buildable once the materialization build lands. This proposal only re-homes where the race occurs.
- **No change to §8.4 `approvalMode` or §8.3 `credentialPropagation` (`inherit` / `independent` / `deny`) semantics.** Those enums are reconciled by proposals 0043, 0044, and 0049.

## 6. Testing

This reconciliation changes spec wording, one doc comment, and ledger entries. It carries no runtime behavior change, so it reaches tier 0 (static) only, per `.claude/rules/test-coverage.md`: the spec edits are validated by the tier-0 spec-map and cross-reference checks, and the code-comment edit by `go build`, `go vet`, and `golangci-lint`. No new tier-1 through tier-12 test is warranted, because no code path is added or altered.

The tests that the reconciliation makes buildable belong to the follow-on materialization build (the DOC-1 BUILD-GAPS finding) rather than to this proposal, and are listed here so the build carries them:

- **tier-4 delegated-child materialization (spec-named-failure), owned by the follow-on build:** once a `StateCreated` delegated child is wired into the shared session-start and finalize path, a test drives a delegation, materializes the child, and asserts it claims a warm pod, assigns its credential lease, and transitions to `running`. The non-happy path is a delegated child stuck in `created` with no start path, the exact state the reconciliation records as unbuilt. `// spec: 8.2 (delegated child materializes at session-start), 8.8 (child flows through the canonical session state machine)`.
- **tier-4 post-claim assignment race (spec-named-failure), owned by the ADV battery as T-ADV.14:** once the child claims a pod at session-start, a test seeds a pool with one assignable slot, fires N concurrent `inherit`-mode delegations, and asserts exactly one child materializes while the rest return `CREDENTIAL_POOL_EXHAUSTED` with the loser's warm pod released back to the pool. The non-happy path is the concurrent assignment race after the pod claim, reachable through `start.go:159-166` reuse rather than a new inline race. `// spec: 8.3 (post-claim assignment race at session-start), 8.2 (delegated child materializes at session-start)`.

## 7. Findings closed on application

This proposal resolves the reconciliation half of T-8.2.17 (Medium, `TEST-GAPS.md:2592`): the §8.2 allocation-timing wording is reconciled to the deferred create-then-materialize model (option b), and the §8.8 (`:879`) and §15.1 (`:630`) `created`-state pod-claim notes are qualified for a delegated child (SPEC-6), so the spec and the delegate code agree that pod allocation and credential-lease assignment happen at the child's session-start rather than inline at delegation step 5. The residual steps-5-through-9 materialization build for a delegated child is re-homed to session-start and filed as a new BUILD-GAPS finding (DOC-1). T-ADV.14 (Low, `TEST-GAPS.md:12106`) stays OPEN; its dependency line is re-homed so its post-claim race becomes buildable through `start.go:159-166` reuse once the materialization build lands. The changes apply at spec-edit, code-comment, and ledger time and need no operator hardware.

## 8. Resolved in adversarial review

Subsequent adversarial review rounds populate this section. The challenge-round revisions carried in the draft are folded into the staged changes above. First, the proposed restructure of the §8.2 numbered flow into admission and materialization phases (draft SPEC-1) was dropped to Non-goals: it would codify the temporary `created` return as a permanent contract before the materialization feature is built, and it rests on the unproven premise that a delegated child already traverses the shared session-start path. The reconciliation instead fixes the two genuinely loose wording sites (`:64`, `:470`) and leaves the numbered flow intact. Second, SPEC-4 was trimmed from a full-sentence rewrite to a single-clause insertion into the existing `:470` last sentence, dropping the net-new "one-winner-of-N" phrase and keeping the existing §7.1 cross-reference that already carries the meaning. Third, CODE-1 was narrowed to a citation-accuracy fix that corrects the mis-attributed "step 7 (pod allocation)" token and keeps the present-tense "materialization is unbuilt; the child traverses no start path" warning in the comment, rather than moving that honesty out to the ledger. Fourth, the proposed code-comment sweep of the "§8.2 step 6" citations in `delegation/service.go` and `export/export.go` (draft CODE-2) was dropped: because the §8.2 numbered flow is left intact, those citations remain valid and need no edit. Fifth, the failure-path rewording (draft SPEC-3) and the virtual-interface lifecycle note (draft SPEC-5) were dropped to Non-goals: the phrases at `:61`, `:63`, `:157` are true under the deferred model and cite no step number, and the `:108-112` lifecycle paragraph is already accurate and cites no inline creation step. Sixth, the DOC-1 BUILD-GAPS finding was narrowed to the actual residual unbuilt work (wiring a `StateCreated` delegated child into the shared session-start path) with F-8.2.4 and F-8.2.11 cross-referenced as already satisfied, rather than re-filing the file stream and the virtual-interface injection as unbuilt.

### Pass 1 (2026-07-19, automated)

- Reconciled the `created`-state pod-claim notes at `spec/08_recursive-delegation.md:879` and `spec/15_external-api-surface.md:630`, which both state that a session in `created` has a warm pod claimed. That statement is the top-level create-path invariant (`create.go:327-329`), and a delegated child is committed to `created` with no `PodAssignment` (`service.go:1425`). SPEC-4 deferred the delegated child's claim to session-start without touching these two notes, so applying the proposal would have left the spec internally inconsistent (created equals pod-claimed per §8.8/§15.1 versus created equals no-pod under the endorsed model) and a client querying a delegated child's task state would see `submitted` carrying the documented meaning "warm pod claimed" while no pod is held. Added SPEC-6, which qualifies both notes so "warm pod claimed" applies to the top-level create path and a delegated child sits in `created` with no pod until it materializes at its session-start. Added the two notes to the scope paragraph, the §7 findings-closed statement, and the §10 files-touched list.
- Corrected the §2 decision bullet that cited `spec/08_recursive-delegation.md:879` as evidence that §8.8 "already establishes the two-phase framing" and describes "a child that materializes after admission." Line `:879` places the warm-pod claim at `created`, so the citation inverted what the line says. Reworded the bullet to rely on §8.8's canonical session-state-machine framing at `:835` for the deferred model, and to note that the `:879` and `:630` created-state claims are the top-level create-path invariant qualified by SPEC-6.
- Reconciled the unedited opening clause of the §8.3 pre-check paragraph at `:470` ("before claiming a warm pod"), which still framed the pod claim as part of `delegate_task` processing and clashed with the SPEC-4 insertion "not at delegation admission." Extended SPEC-4 with a second replacement that rewords the opening clause to "before the child's warm pod is claimed," and updated the SPEC-4 rationale, anchor, and preserved-unchanged note accordingly.

## 9. Open decisions for review

### Allocation-timing home — deferred session-start (option b) versus inline delegation-time (option a)

This decision gates the spec edits. Option (b), recommended, reconciles §8.2 to defer pod allocation and credential-lease assignment (and the steps-5-through-9 materialization) to the child's session-start on the shared canonical path, on the grounds of single-canonical-implementation, alignment with §8.8 (`:835`), and reuse of the already-implemented `assignment_race` path at `start.go:166`. Option (a) would amend §8.2 toward inline delegation-time allocation and build a new delegate-path pod claim, turning the 0044 point-in-time pre-check into a true pre-allocation fail-fast; it would touch the shared warm-pool claim primitives and trigger the C-22/C-01 coordination. The reviewer ratifies the direction. SPEC-4 and the CODE-1 phrasing assume option (b); under option (a) the pod claim occurs at delegation admission and both would be reworded accordingly.

### Whether to file a distinct BUILD-GAPS finding or fold under existing tracking

The residual delegated-child materialization build (wiring a `StateCreated` child through session-start) is filed as a new BUILD-GAPS finding in DOC-1. The alternative is to fold it under the existing in-source acknowledgment (`mcptools.go:924-927`) and the T-ADV.14 dependency line, to avoid duplicate tracking of the same future build. The recommendation is a distinct finding, because BUILD-GAPS is the ledger the build loop drains and the in-source comment is not; the reviewer decides.

## 10. Files touched on application

- `spec/08_recursive-delegation.md`: SPEC-2 (§8.2 step-2a cycle-detection parenthetical at `:64`), SPEC-4 (§8.3 pre-check opening and post-claim clauses at `:470`), and SPEC-6 (§8.8 `created`-state note at `:879`).
- `spec/15_external-api-surface.md`: SPEC-6 (§15.1 `created`-state description at `:630`).
- `pkg/gateway/mcpfabric/mcptools/mcptools.go`: CODE-1 (correct the `taskHandle.State` step citation and keep the unbuilt warning, `:923-928`).
- `TEST-GAPS.md`: DOC-1 (mark T-8.2.17 resolved at `:2592`; re-home the T-ADV.14 dependency line at `:12110`).
- `BUILD-GAPS.md`: DOC-1 (new finding: wire a `StateCreated` delegated child through the shared session-start and finalize path).
- `PROPOSAL-QUEUE.md`: DOC-1 (record the C-42 convergence on option b and the re-filed materialization build, `:476-483`).
