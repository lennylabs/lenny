# Problem: Sidecar pods have no platform-controlled runtime restart

## Statement

Under the default sidecar deployment model (§4.7.10), no platform mechanism starts a fresh runtime process inside a running agent pod. The pod builder sets `RestartPolicy: Never`, so the kubelet starts the runtime container once and runs the image entrypoint. The adapter drops every capability, cannot escalate privilege, and cannot exec a binary that lives in the runtime container's separate image and mount namespace. §13.1 forbids `shareProcessNamespace`, so the adapter cannot see or signal runtime processes, and the agent-UID halves of the whole-pod scrub (step 1 `kill -9 -1` and step 1b `ipcrm`) reach only the adapter container. This is part (b) of BUILD-GAPS F-5.2.33. Proposal 0079 (status Draft, still converging) claims that part and resolves it by keeping the runtime process for the pod's life, pinning it to one tenant, and requiring `acknowledgeProcessLevelIsolation` on every recycling pool that keeps its runtime (`vm-restart` exempt). This proposal assumes 0079 has landed and adds an opt-in alternative to that keep resolution. It is a new capability rather than a defect fix, because after 0079 the specification is self-consistent: it states that a recycled pod keeps its runtime and that no pod is reused across tenants.

The capability is a platform supervisor as PID 1 of the runtime container, deferred by the original 0079 draft (§9.4) as a named follow-up and worked out as option B in the design exploration at `scratchpad/sidecar-restart/design.md`. An init container stages a static first-party supervisor binary into a shared volume, and the pod builder makes it the runtime container's command. On the adapter's instruction, the supervisor starts each runtime generation after the final adapter manifest is written. At the recycle boundary it ends the generation (SIGTERM, then a kill of every remaining process in the container), reports the exit status with proof that no runtime process remains, and performs the agent-UID halves of the scrub. Platform-versus-author identity rests on platform-controlled ordering (the pod spec makes the supervisor PID 1, the adapter reports READY only after the supervisor connects, and the adapter accepts exactly one supervisor connection per pod) and on kernel properties (a PID-namespace init ignores SIGKILL and SIGSTOP from inside the namespace, and a non-dumpable process blocks ptrace, `/proc/<pid>/fd`, and `pidfd_getfd` from same-UID code without capabilities). Both rest on assumptions that need validation per isolation runtime. A per-pool runtime-process lifetime (working name `recycle.runtimeProcess: keep | restart`) selects between 0079's keep behavior and a fresh runtime per session.

The work has two parts with different motivations, and the source design asks that each be its own proposal (design.md, **Phased plan**). The human decides whether this proposal carries both or only the first. If it carries both, the second part is a severable tail of the checklist with its own gate.

- **Part 1: the supervisor and the restart lifetime for same-tenant pools.** This part serves per-session runtime freshness on every isolation profile. A restart pool needs no `acknowledgeProcessLevelIsolation` for the runtime process (whether it is still required for other shared state is an open decision), keeps the single-session runtime SDKs correct, and does not depend on the not-yet-written multi-session runtime SDK proposal. The part covers the supervisor binary and image, the init container and volume, the runtime-command source, a new intra-pod channel with its §28 register row and card, a new adapter transport, the scrub split into adapter and agent-UID halves, and the pool field. The gVisor validation of PID-namespace init signal immunity, `PR_SET_DUMPABLE`, `si_pid`, and `kill(-1)` gates this part on `sandboxed` pools. Kata runs an ordinary guest kernel, and runc needs a separate trust decision.
- **Part 2: cross-tenant reuse on `scrubProfile: in-place` pools.** `recycle.allowCrossTenantReuse` is admissible only with `isolationProfile: microvm`, which maps only to the `kata` RuntimeClass, so this part is Kata-only by construction, and the gVisor gate never applies to it. It is gated on the open decision whether a fresh process, the scrub, and the supervisor's agent-UID cleanup together suffice across tenants, given the guest-kernel residue §5.2 documents and the adapter process that persists. It should also wait on 0079's open decision 11 (whether the capped cross-tenant drain stays).

Part 2 needs more than performing the `{tenant} -> unassigned` pin reset. In the specification and the tree, `unassigned` is a terminal label value. §5.2 **Tenant pinning** item 2 permits only `unset -> {tenant_id}` and `{tenant_id} -> unassigned`, §18's package list restates that, and the `lenny-tenant-label-immutability` webhook's default branch rejects `unassigned -> {tenant_id}` with 403. The gateway re-stamps the tenant label on every claim with a merge patch and tolerates only NotFound or Conflict, so a pod reset to `unassigned` could never be claimed by a second tenant. 0079's staged `AdmitTenantPin` and planner also treat a pod labelled `unassigned` as pinned and refuse it. Part 2 must therefore stage a new permitted label edge (for example `unassigned -> {tenant_id}` written by the gateway SA) or a different reset scheme. That edge changes a tier-9 defense-in-depth control and goes to the human as an open decision. Part 2 must also amend the §4.6.3 RBAC text and restate 0079's acquisition predicates. Cross-tenant reuse has never worked in code (nothing writes `unassigned`), so this part builds it for the first time.

Mapping onto 0079 requires more care than reusing its existing values:

- `KeepsRuntime: false` cannot represent a restart pool unchanged. Under 0079 that value makes `AdmitTenantPin` refuse every pinned pod, including pods pinned to the same tenant, stamps a `kept_runtime_outside_rule` drain on each refused pod, and replaces the reserved-hold rebind with `endReservedHolds`. A restart pool mapped to it would lose same-tenant reuse and the reserved hold. The predicate has to be split.
- 0079's `SocketRuntimeProcess` records a sticky `ended` state that nothing clears, and it never redials (D4, D10). A restarted generation cannot reconnect through it. The proposal needs a new transport (the design's `SupervisedRuntimeProcess`) or a stated reversal of D4 and D10 for restart pools, with each accepted `CH-MSGSOCK` connection bound to one generation.
- `runtime_live` changes meaning on a restart pool: the supervisor is connected and the previous generation is proven gone.
- The proposal must state its scope on concurrent pools. Either restart applies only at occupancy zero, with `KeepsRuntime` true for the concurrent co-tenancy, or concurrent pools are excluded.
- The proposal must state how restart interacts with `preConnect` (SDK-warm) recycling pools. §6.1 and §6.2 require the SDK process to be re-established on the `claimed -> sdk_connecting` re-warm edge before the claim enters `reserved`, which is before the next session's manifest exists. The proposal either refuses restart on `preConnect` pools or states the ordering. `DemoteSDK` for sidecar runtimes is the existing §6.1 counterpart and stays a phase 3 follow-up.

Because 0079 is assumed landed, the proposal reopens points 0079 records as settled. It must name each one in its own text and must not edit 0079:

- 0079's review log names the PID-1 supervisor a rejected design ("a finding that proposes one re-derives a rejected design"). The human directed this follow-up, so the reopening is deliberate.
- 0079 does more than leave the pin reset granted but unused. Its SPEC-8(c) states that a recycled pod never takes the `{tenant_id} -> unassigned` transition. Its SPEC-8 text states that a pod on an `allowCrossTenantReuse` pool is never reused across tenants, and its Kata in-place rewrite states that neither profile reuses a pod across tenants. Its §4.6.3 RBAC sentence states that no component performs the reset. Part 2 amends each of these for restart pools.
- An admission rule refusing `in-place` with `allowCrossTenantReuse` unless `runtimeProcess: restart` conditionally restages a validation rejection that 0079 records as lost by human decision 4 ("Do not restage the validation rejection"). It also makes 0079's capped cross-tenant drain (decision 8) unreachable, so the proposal records that it retires that drain and relates the retirement to 0079's open decision 11.
- §5.2 recycle step 7 states that a pod is never returned to cross-tenant reuse without a fresh guest. 0079 does not edit that sentence, and part 2 must reconcile it.
- 0079 deletes the current per-session runtime promises (§15.4 "starts a fresh runtime process for each session", §4.7.9 step 7 "Adapter spawns runtime binary", the §6.1 recycle row, and the runtime-author guide's "the runtime exits at each session end") and pins their absence with the tier-11 test `TestRuntimeProcessLifetimeStatementsAgreeAcrossSpecAndDocs`. Restart-pool wording must be restored in terms that test does not ban, or the test must be amended in this proposal's own text.

Further constraints the proposal must account for:

- The gateway registry does not carry the runtime's `deploymentModel` (the RuntimeReconciler leaves it CRD-only). Any admission rule or default keyed on sidecar versus embedded needs that registry field or a rule that does not depend on it. The admission rule tying `in-place` cross-tenant pools to restart also reaches embedded pools, so the embedded question belongs with whichever part carries that rule.
- The ordering identity holds only if no other container runs at the agent UID. The pod builder already injects a test-only egress-capture container at the agent UID. Init containers pass through the pod-security and cosign webhooks, so the supervisor image must be signed and compliant. Under nonce-only mode (`requireSoPeercred: false`) ordering is the only identity control on the supervisor connection.
- Proposal 0084 makes the manifest `mcpNonce` a pod-scoped value minted by the arming claim and republished by every later start. The supervisor's per-generation launch nonce must be reconciled with it in whichever proposal lands second.
- The supervisor launches each generation after the final manifest is written, which removes the first-session manifest-ordering defect on supervised pods. The separate ordering fix remains a prerequisite for keep pools and for the F-4.7.25 nonce handshake. The proposal states this so the defect is not fixed twice in two ways.

Restart after a crash and `DemoteSDK` for sidecar runtimes are phase 3 follow-ups. Pre-starting the next generation before its session arrives is phase 4 and depends on the multi-session runtime SDK proposal or a narrower SDK change. The open decisions for the human, each with a recommendation, are: where the lifetime is selected (pool, Runtime, or both); the default for new sequential recycling pools; the runtime-command source given digest-pinned images and a Runtime type with no command field; whether the predecessor is ended before deployer `cleanupCommands`; whether a platform process at the agent UID is acceptable, with gVisor validation as a hard gate for part 1 on `sandboxed` pools; whether `acknowledgeProcessLevelIsolation` remains required on restart pools for other shared state; the concurrent-pool and `preConnect` scope; for part 2, cross-tenant sufficiency, the new label edge, and what restart means for embedded runtimes; and the channel name (the design suggests `CH-SUPERVISE`).

## Evidence

Prerequisites assumed implemented before this proposal. The proposal is written against the tree as it will be after them, cites staging where a behavior is only staged, and names them as prerequisites rather than restaging them:

- (verified) Proposal 0078 keeps the pod-scoped `CH-MSGSOCK` listener across a session teardown. Status: Reviewed (`proposals/0078_fix_keep-the-pod-listener-across-a-session-teardown/*.status.md`).
- (verified) Proposal 0079 keeps the runtime process across sessions on recycling pools, retires a pod when its runtime is gone (`runtime_live`, `runtime_not_live`), requires `acknowledgeProcessLevelIsolation` with `vm-restart` exempt (D5, CODE-11), pins the tenant for life (D6), refuses a used pod outside the reuse rule (D16), adds interim pinned-idle inventory (D14), caps the cross-tenant drain at one per acquisition (decision 8), and sends no terminate frame at pod exit or hold timeout (D8, decision 10). Status: Draft; its spec loop stopped on the round budget, so the proposal reads 0079's current staging. Its open decision 11 (keep or delete the capped cross-tenant drain) is unadjudicated (`proposals/0079_*/*.summary.md`, open decision 11).
- (verified) Proposal 0084 covers session attribution on intra-pod MCP and `llm_request_completed` on concurrent pools, and makes the manifest `mcpNonce` pod-scoped. Status: Draft (`proposals/0084_*/*.summary.md`).
- (verified, not a dependency of the restart lifetime) A not-yet-written runtime-SDK proposal for sequential sessions per `sessionId` in one process. No proposal in `proposals/` covers it. The restart lifetime keeps the single-session SDKs correct, so this is related work for keep pools and for phase 4.
- (verified, not yet written) A pinned-idle inventory proposal adding a per-pool bound beyond `maxWarm`, summed by the §17 quota floor, with an idle TTL. 0079 names it as a follow-up.
- (verified, partly superseded on supervised pods) A fix for the first-session manifest-ordering defect on sidecar pods, recorded in 0079's summary under **Defects in the shipped tree that this proposal does not stage**.
- (verified) A fix for BUILD-GAPS F-4.7.25 (OPEN): `SocketRuntimeProcess.accept` performs a bare `listener.Accept` with no `SO_PEERCRED` check and no nonce (`pkg/adapter/socketruntime.go`, the `accept` goroutine), while `peerCheckedListener` is used only by the connector MCP listener (`pkg/adapter/connectormcp.go`). The §28.5.3 `CH-MSGSOCK` card states both protections.

Tree and specification:

- (verified) `pkg/controller/sandbox/podspec/podspec.go`: `basePod` sets `RestartPolicy: corev1.RestartPolicyNever`; the runtime container has no `Command` or `Args`; `containerSecurityContext` drops ALL capabilities and sets `AllowPrivilegeEscalation: false`; `shareProcessNamespace` is left unset; there are no `InitContainers`; the egress-capture container runs at `in.agentUID()`.
- (verified) `pkg/adapter/socketruntime.go` doc comment: "The adapter never spawns the runtime in this model — the kubelet starts the runtime container". `SpawnPath` exists only for the `cmd/lenny-adapter --runtime-bin` developer loop.
- (verified) `pkg/adapter/scrub/defaultops.go`: scrub step 1 runs `kill -9 -1` from the adapter container.
- (verified) `spec/13_security-model.md` §13.1 **Pod-namespace isolation**: `shareProcessNamespace` is forbidden on every Lenny pod template.
- (verified) `spec/05_runtime-registry-and-pool-model.md` §5.2 **Tenant pinning** item 2: the only permitted transitions are unset -> `{tenant_id}` and `{tenant_id}` -> `unassigned`. `spec/18_build-sequence.md` restates `unset → {tenant_id} → unassigned` in the package list.
- (verified) `pkg/admission/label_immutability/label_immutability.go`: the `{tenant_id} -> unassigned` case is allowed only for the WarmPoolController SA, and the default branch rejects `unassigned -> {tenant_id}`. `UnassignedTenantID` has no writer in `pkg/` or `cmd/`.
- (verified) `pkg/gateway/podlifecycle/podclaim/claimer.go` calls `stampPodTenant` on each claim; `slotclaimer.go` tolerates only NotFound and Conflict.
- (verified) §5.2: the pool controller rejects `allowCrossTenantReuse` unless `isolationProfile` is `microvm`; `standard` scrub is rejected with it; recycle step 7 states "A pod is never returned to cross-tenant reuse without a fresh guest". `pkg/gateway/runtime/poolstore/poolstore.go` enforces the microvm rule. The §5.3 profile table maps `microvm` to `kata` and `sandboxed` to `gvisor`.
- (verified) `spec/06_warm-pod-model.md` §6.1: the `preConnect` recycle row re-warms on the `claimed -> sdk_connecting` edge before `reserved`, and `DemoteSDK` tears down and restarts the agent process.
- (verified) `spec/04_system-components.md` §4.7.9 step 7 "Adapter spawns runtime binary"; §4.7.11 item 5 "does not restart the agent"; `spec/15_external-api-surface.md` §15.4 "starts a fresh runtime process for each session". 0079 deletes the first and third.
- (verified) `pkg/apis/lenny/v1alpha1/runtime_types.go`: `Runtime.spec.image` is digest-pinned by a kubebuilder pattern, and the type has no command field. `charts/lenny/Chart.yaml`: `kubeVersion: ">=1.27.0-0"`; §17.6 preflight requires 1.27.
- (verified) `pkg/controller/runtime/controller.go` `applyCRDFields`: `DeploymentModel` is CRD-only and is not mirrored into the registry.
- (verified) 0079 staging: `*.spec-changes.md` D4 and D10 (sticky `ended`, no redial), D6, SPEC-4(d) (§4.6.3 RBAC sentence), SPEC-8(c) ("never takes the `{tenant_id} → unassigned` transition"); `*.non-spec-changes.md` `AdmitTenantPin` (refuses `unassigned`; refuses every pinned pod when `KeepsRuntime` is false), `kept_runtime_outside_rule`, `endReservedHolds`, the planner rule that a pod labelled `unassigned` counts as pinned, and `TestRuntimeProcessLifetimeStatementsAgreeAcrossSpecAndDocs`; `*.review-log.md` WATCHOUTs on the deleted supervisor and on the validation rejection lost by decision 4.
- (verified) `gateway-runtime-comms-remediation.md`: R6 (podspec and chart decoupling) with serialization rule S-4 for podspec-touching steps; R8 (host-conformance battery); R14 (agent-pod mTLS); R16 and R17 (eviction on the holder path, `CH-RUNTIMEOPS` enablement); S-2 (R1b is the only step editing `schemas/lenny-adapter.proto`, and a later field opens a narrow window). No step restarts a runtime or adds a supervisor. The proposal records its ordering against each.
- (verified, untracked) `scratchpad/sidecar-restart/design.md` (option B, phased plan, open questions) and `scratchpad/sidecar-restart/0079-original-draft.md` (§9.1 to §9.3 rejected alternatives, §9.4 the deferred supervisor). Both sit under `scratchpad/`, which `.git/info/exclude` ignores, so they are absent from git history. The proposal carries their substance in its own text rather than citing them as durable evidence.

## Who observes it

The platform is pre-deployment, so no deployer observes this today. The populations below are those that would observe it once 0079 lands.

- Any deployer who enables recycling on a sidecar pool with `maxSessionsPerPod > 1` and `scrubProfile: standard` or `in-place`. Under 0079 admission refuses the pool unless it sets `acknowledgeProcessLevelIsolation: true`, so recycling forces the deployer to accept runtime-process state carrying between sessions, including sessions of different users in one tenant. Keeping the runtime also depends on the runtime-SDK change for sequential sessions, which does not exist yet. This is the larger population and the one part 1 serves.
- A deployer running a Kata pool with `recycle.enabled`, `maxConcurrentSessions: 1`, `allowCrossTenantReuse: true`, and `scrubProfile: in-place`. Under 0079 the field yields no cross-tenant reuse and at most a capped acquisition-time drain, which 0079's open decision 11 may delete. This narrow configuration is the one part 2 serves. The Kind test cluster provisions no Kata runtime, so tier 5 cannot exercise it.
- Runtime authors who read the current §15.4 and runtime-author-guide promise of a fresh runtime per session. 0079 removes that promise, and the restart lifetime is the only path that makes it true again.

## What breaks if nothing changes

Nothing is unsafe. After 0079 the specification states the keep behavior consistently, and the baseline meets every security goal: a deployer who needs fresh state at each boundary or a pod usable by any tenant sets `scrubProfile: vm-restart` and pays the warm-pod reprovision cost, or relies on 0079's drain-and-replace. The costs of not building the capability are:

- Every reusing recycling sidecar pool must accept process-level state carrying between sessions through `acknowledgeProcessLevelIsolation`, and depends on an unwritten SDK change.
- `recycle.allowCrossTenantReuse` on `in-place` Kata pools has close to no effect. Idle microVM pods stay pinned to tenants that cannot use them, which increases cold starts and drains.
- The agent-UID halves of the whole-pod scrub stay unreachable on sidecar pods.

Smaller alternatives are coherent and the proposal records them: leave the keep behavior as the only lifetime, refuse the `in-place` plus `allowCrossTenantReuse` combination at validation, or delete the field. The reason recorded for proceeding is the owner's instruction to build the restart capability. The statement that the human kept `allowCrossTenantReuse` so that this proposal would make it meaningful is not recorded in the repository and is cited as an owner instruction.

## Findings this unblocks

- BUILD-GAPS F-5.2.33 part (b) (OPEN): 0079 resolves it with the keep lifetime. This proposal adds the restart lifetime that the finding's suggested resolution lists as a candidate, as an opt-in on top of that resolution.
- BUILD-GAPS F-4.7.25 (OPEN) is a prerequisite rather than a finding this closes. Its text states that a future runtime-restart mechanism depends on binding each accepted `CH-MSGSOCK` connection to one runtime generation through authenticated connections.

## Prior art considered

- No specification mechanism, landed proposal, or code path starts a fresh runtime process inside a running sidecar pod. The only supervisor code in the tree is the unrelated embedded-stack host supervisor under `pkg/embedded`, and the specification does not use the word "supervisor".
- The original 0079 draft deferred this design in its §9.4 as a named follow-up, and its §9.1 to §9.3 rejected the restartable runtime container, the adapter-side spawn generalizing `SpawnPath`, and a rule keyed on `deploymentModel` (which the registry does not carry). The draft also observed that the supervisor repairs the first-session manifest-ordering defect as a side effect. This proposal is that follow-up.
- Kubernetes-native restart was examined and rejected on grounds that hold at the 1.27 floor. Pod-level `OnFailure` does not restart a clean exit, and `Always` also restarts the adapter. Native sidecar containers need 1.29 or later, restart on every exit, and fire before the scrub. None of them terminates a non-cooperating predecessor.
- `DemoteSDK` and the `preConnect` re-warm edge (§6.1) are the existing per-session agent-process teardown and restart for SDK-warm pods. No sidecar transport implements `SDKWarmRuntime`. The phase 3 follow-up extends that mechanism rather than introducing a new one.
- BUILD-GAPS F-5.2.33 is the audit finding behind this problem, and 0079 records it as answered by the keep lifetime.
- The remediation plan in `gateway-runtime-comms-remediation.md` anticipates neither a runtime restart nor a supervisor. Its relevant constraints are R6 with S-4, the S-2 proto window, R14, and the R16 and R17 pod-exit drain.

## Validated premises

Premise lens (verdict: revise):

- REFUTED, load-bearing: performing the `{tenant} -> unassigned` pin reset restores cross-tenant reuse on `in-place` pools. `unassigned` is terminal: the webhook rejects `unassigned -> {tenant_id}`, the gateway re-stamps the tenant on every claim and fails the claim on a 403, and 0079's `AdmitTenantPin` and planner treat `unassigned` as pinned. Part 2 needs a new label edge or reset scheme and restated 0079 predicates.
- REVISED: 0079 does not take away a working cross-tenant reuse path. The tree has never performed cross-tenant reuse, and sidecar pods never serve a second session today (F-5.2.33). Part 2 builds it for the first time.
- REVISED: "Kata first, gVisor gated on validation" misattributes the gate. `allowCrossTenantReuse` is microvm-only and microvm maps to Kata, so the gVisor gate applies to part 1 on `sandboxed` pools.
- STANDS, load-bearing: the kubelet starts the runtime container once under `RestartPolicyNever`; the runtime container runs the image entrypoint; the adapter never spawns it in a pod; both containers drop ALL capabilities.
- STANDS, load-bearing: `shareProcessNamespace` is forbidden and unset, so scrub step 1 and step 1b reach only the adapter container.
- STANDS: 0079 keeps the runtime for the pod's life, never lets a kept runtime serve a second tenant, and requires the acknowledgment with `vm-restart` exempt. 0079 is Draft and 0078 is Reviewed, so both are assumptions.
- REVISED: `KeepsRuntime: false` for restart pools would also refuse same-tenant pinned pods, stamp the `kept_runtime_outside_rule` drain, and replace the rebind with `endReservedHolds`. The predicate must be split.
- REVISED: 0079's transport has a sticky `ended` state and no redial (D4, D10), so a restarted generation needs a new transport or a stated reversal.
- REVISED (gap): starting each generation after the final manifest conflicts with `preConnect` re-warm before `reserved`, and the statement did not limit restart on concurrent pools.
- STANDS with caveats: identity by ordering holds only if no other container runs at the agent UID; the test-only egress-capture container does; init containers pass the pod-security and cosign webhooks; nonce-only mode leaves ordering as the only identity control.
- STANDS: `CH-MSGSOCK` accepts connections with no peer check; the runtime image is digest-pinned and the Runtime type has no command field; the Kubernetes floor is 1.27.
- WEAKENED: the reason given for avoiding a "spawn" stem. §8 uses "spawn" only in prose, and §4.7.9 and §28.5.3 use the same verb for the adapter launching the runtime. No identifier contains SPAWN. Avoiding the stem as ambiguous still holds, and `CH-SUPERVISE` is free.

Evidence lens (verdict: stands):

- VERIFIED: the podspec, capability, and `SpawnPath` facts; §13.1 and §4.7.10 on `shareProcessNamespace`; the microvm-only and sequential-only `allowCrossTenantReuse` rules; the unused pin-reset grant; the 0079 mechanisms (D4 to D16, decisions 8 and 10, CODE-11) and its follow-ups; the prerequisite statuses and F-4.7.25; the remediation-plan steps; the chart floor, image digest pin, scrub step 1, and §4.7.11 item 5.
- DRIFTED: the "no mechanism" premise holds in code today, but the current spec still says the adapter spawns the runtime (§4.7.9 step 7) and that the agent process is not started on a warm pod (§6.1). The §4.7.10 **Runtime process lifetime** paragraph the statement relies on is 0079 staging (SPEC-1, SPEC-2), consistent with assuming 0079 has landed.
- DRIFTED: the claim that §8 binds "spawn" exclusively; see the premise lens.
- VERIFIED with a lead: the design documents are untracked under `scratchpad/`, so the proposal carries their substance itself.
- LEAD: 0079's review log records the supervisor as a rejected design and the validation rejection as lost by decision 4; §5.2 step 7's fresh-guest sentence is unedited by 0079. The proposal names all three.

Prior-art lens (verdict: revise):

- No existing mechanism solves the problem.
- The original 0079 draft §9.4 is the origin of the design, and the draft notes the supervisor fixes manifest ordering as a side effect.
- F-5.2.33 is the finding behind the problem, and 0079 claims its part (b).
- 0079 deletes the current per-session runtime promises and bans the phrases with a tier-11 test; restored wording must account for it.
- Load-bearing: 0079 stages a normative prohibition on the pin reset (SPEC-8(c), the SPEC-8 never-reused text, the Kata in-place rewrite, the §4.6.3 RBAC sentence), which part 2 amends.
- Load-bearing: the admission rule restages a rejection 0079 recorded as a human-decided loss, and the supervisor is a recorded rejected design. Both are deliberate reopenings that the proposal names.
- The registry does not carry `deploymentModel`.
- `DemoteSDK` is the existing counterpart for the phase 3 item.

Scope lens (verdict: revise):

- Load-bearing: the source design asks for each phase as a separate proposal, and part 2 depends on a sufficiency decision that could be no. The statement now presents part 2 as separable and leaves the split to the human.
- Load-bearing: the recommended cut places the supervisor, the channel, the transport, the scrub split, the pool field, and the restated 0079 predicates in part 1, and the pin reset, the admission rule, the client disclosure, and tier-9 cross-tenant tests in part 2.
- The gVisor gate belongs to part 1.
- Restart after a crash is a phase 3 follow-up rather than an open decision; the embedded question belongs with the part that carries the admission rule.
- The multi-session SDK proposal is independent of the restart lifetime, and the manifest-ordering fix is needed only for keep pools and the F-4.7.25 handshake.
- 0084 is a dependency because of the manifest-nonce contract.
- 0079 is still converging, and its open decision 11 concerns the behavior part 2 redefines.
- Concurrent-pool semantics must be stated.
- The core premise checks out against the tree.

Impact lens (verdict: revise):

- The cross-tenant configuration is narrow (Kata, sequential, `in-place`, `allowCrossTenantReuse`), unobserved before deployment, and not testable on Kind.
- The cross-tenant consequence is cost and has no safety effect, and `vm-restart` is a working option.
- Same-tenant recycling pools carry the broader cost (the mandatory acknowledgment and the SDK dependency), which part 1 removes.
- Cross-tenant reuse was never meaningful in code.
- Load-bearing: the pin reset alone cannot restore reuse; a new label edge weakens a tier-9 control and goes to the human.
- The premise that a sidecar pod cannot restart its runtime holds.
- The baseline is moving with 0079's open decision 11.

Alternatives lens (verdict: revise):

- After 0079 the problem is a missing capability rather than a defect. The zero-mechanism baseline and the smaller alternatives are recorded, with owner preference as the reason to proceed.
- The claim that the human kept the field for this proposal is not recorded in the repository and is cited as an owner instruction.
- Load-bearing: part 2 is Kata-only, and the gVisor gate belongs to part 1.
- Load-bearing: part 1 serves a separate motivation (per-session freshness and SDK independence) that the statement now names; narrowing part 1 to microvm pools is the minimal alternative.
- The admission rule makes 0079's capped cross-tenant drain unreachable, and the proposal records its retirement.
- The manifest-ordering fix, the multi-session SDK proposal, and 0084 are assumed landed but are not dependencies of the restart lifetime itself (0084 remains relevant through the nonce contract).
- Kubernetes-native restart alternatives were rejected on grounds that still hold.
