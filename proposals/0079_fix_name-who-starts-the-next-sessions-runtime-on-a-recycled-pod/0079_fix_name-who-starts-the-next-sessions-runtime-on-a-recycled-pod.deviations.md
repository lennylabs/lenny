# Deviations: Name who starts the next session's runtime on a recycled pod

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Proposed: Tier-9 kept-runtime harness sets bootstrapMinWarm in two stages

**Status:** proposed

**What the proposal says.** The non-spec changes TEST-14 and TEST-21 (tier 9, second subtest) leave acme's session-A pod as the pool's only idle Sandbox. Once A's pod is reserved, the harness sends `PUT /v1/admin/pools/{name}` with `bootstrapMinWarm: 0` and `warmCount` unchanged. The PoolScalingController renders that update as minWarm 0 and maxWarm equal to warmCount, so the pinned-idle bound keeps A's pod.

**What landed instead.** In `tests/tier9_security/tenant_isolation_test.go` (`newKeptRuntimeHarness`, `setBootstrapMinWarm`, and `keptRuntimeIdleSoloPod`), the harness creates the pool with `warmCount` 3. Once the pool is warm, it updates the pool to `bootstrapMinWarm` 2 and waits until the SandboxWarmPool renders minWarm 2 against maxWarm 3. Session A then runs, its hold expires with A's pod idle and pinned, and only after that does the harness update the pool to `bootstrapMinWarm` 0.

**Why.** On the live cluster the proposal's ordering drained A's pod. The PoolScalingController had not yet applied minWarm 0 when the 10-second hold expired, so the pinned-idle bound was still maxWarm minus minWarm, which was 0. A `bootstrapMinWarm` of 1 also fails: claiming the pool's only idle pod sets PoolWarmingUp, and the start path then aborts the claim. The admin create payload ignores `bootstrapMinWarm`, so the harness sets it with a PUT.

**What a later reader would otherwise get wrong.** A reader following the proposal would expect a single `bootstrapMinWarm: 0` update after A's pod is reserved and would treat the two-stage update, the `warmCount` of 3, and the separate PUT as unexplained harness complexity.

## Proposed: Tier-9 outside-rule hold case keeps the default claim hold TTL

**Status:** proposed

**What the proposal says.** In TEST-21, tier 9, first subtest, the case sets `gateway.claimHoldTTLSeconds` long enough to create session B inside the hold.

**What landed instead.** `TestPoolEditOutsideRuleNeverDispatchesIntoKeptRuntime/hold_ended_by_the_acquisition` in `tests/tier9_security/tenant_isolation_test.go` keeps the default 10-second hold. It creates session B as soon as the claim reads `reserved` and the pool update returns.

**Why.** Changing the TTL requires a Helm upgrade and a gateway restart on the shared long-lived Kind cluster. Every assertion in the case (B lands on another pod, A's claim is deleted, A's pod drains, and `podReuse` is false) holds whether B arrives inside or after the hold. The tier-2 case `TestOutsideRuleEndsReservedHoldInsteadOfRebinding_spec_4_6_1` pins the in-hold DELETE deterministically.

**What a later reader would otherwise get wrong.** A reader would assume the tier-9 case guarantees that session B arrives inside A's hold. It does not; the in-hold path is pinned only at tier 2.

## Proposed: Tier-2 resourceVersion race case patches claim metadata

**Status:** proposed

**What the proposal says.** In TEST-21 (tier 2), the resourceVersion race case uses an interceptor whose SandboxClaim Delete first patches the claim's status before delegating.

**What landed instead.** In `pkg/gateway/podlifecycle/podclaim/kept_runtime_rule_test.go`, `TestOutsideRuleHoldEndSkipsMovedClaimsAndHonoursPrecondition_spec_4_6_1` uses an interceptor that applies a metadata label merge patch to the claim before delegating the Delete.

**Why.** Any write changes the resourceVersion. A metadata patch fails the DELETE precondition in the same way and leaves the claim in `reserved`. The asserted outcome (the claim is kept and carries no annotation) is unchanged.

**What a later reader would otherwise get wrong.** A reader searching the test for a status patch would not find one and could conclude the race case is missing.

## Proposed: Checkpoint resume request construction extracted into a helper

**Status:** proposed

**What the proposal says.** For TEST-21 in `sessionserver`, an internal test asserts that `exclusiveBindRequest` and the `resumeOnPod` `ResumeRequest` carry `match.KeepsRuntime`.

**What landed instead.** In `pkg/gateway/sessionserver/resume_rebind.go`, the `ResumeRequest` literal moved unchanged into a new helper, `checkpointResumeRequest`, which `resumeOnPod` calls. `pkg/gateway/sessionserver/pool_policy_mirror_test.go` tests that helper and `exclusiveBindRequest`.

**Why.** `resumeOnPod` built the request inline and passed it directly to a concrete `*podsession.Binder`. A focused unit test can observe the request only once its construction is a separate function. The refactor changes no behavior.

**What a later reader would otherwise get wrong.** A reader would look for a test of `resumeOnPod` itself and would not see that the `match.KeepsRuntime` assertion runs against `checkpointResumeRequest`.

## Proposed: Unlisted 0078 CloseListener socket case rewritten for CODE-4

**Status:** proposed

**What the proposal says.** TEST-1 (non-spec changes §10) disposes of 0078's TEST-1 and TEST-9. Among 0078's socket cases, it amends only `TestSocketRuntimeCloseListenerUnbindsTheAddress_spec_4_7`.

**What landed instead.** Step S20 also rewrote 0078's `TestSocketRuntimeCloseListenerReleasesOnlyTheListenerAndIsIdempotent_spec_28_5_3` and renamed it `TestSocketRuntimeCloseListenerEndsTheConnectionAndIsIdempotent_spec_4_7_10` in `pkg/adapter/socketruntime_test.go`. The rewritten case asserts that the peer reads EOF, that Output closes, that the address is unbound, and that a second call is idempotent. `TestSocketRuntimeCloseReportsNoListenerErrorAcrossGenerations_spec_5_2` and the amended `UnbindsTheAddress` case now start the second session on the kept connection without a second dial.

**Why.** The original case asserted that `CloseListener` leaves the live connection serving, which CODE-4 inverts, so it fails under CODE-4 as written. The proposal does not list the case, and S20 rewrote it from the proposal's intent. The case had no per-test spec-map entry, so no map rename was needed.

**What a later reader would otherwise get wrong.** A reader comparing TEST-1 against the tree would find a renamed and inverted socket case the proposal never mentions, and could read it as an unauthorized change or search for the old test name without finding it.

## Proposed: Additional tier-1 cases for the teardown Warn path and repeated start

**Status:** proposed

**What the proposal says.** TEST-20 and TEST-3 name the tier-1 hold cases and their doubles. CODE-4 logs `runtime_teardown_failed` at Warn. TEST-4 names only the package for its case.

**What landed instead.** Step S20 added `TestCoordinatorHoldTimeoutLogsAFailedRuntimeTeardown_spec_10_1_4` in `pkg/adapter/holdstate_test.go` and `TestSoleSessionSurvivesARepeatedStartAndAStrayClose_spec_28` in `pkg/adapter/runtimegeneration_test.go`. TEST-4's case lives in the new file `pkg/adapter/runtimegeneration_test.go`.

**Why.** The first added case covers the Warn branch of CODE-4's teardown logging, and the second covers the idempotent-start path. The proposal leaves the file for TEST-4 unnamed.

**What a later reader would otherwise get wrong.** A reader would treat the proposal's named cases as the complete tier-1 set and would not know that two further cases exist or where TEST-4's case is located.

## Proposed: Register, credit, and comment edits outside the listed spec-map entries

**Status:** proposed

**What the proposal says.** TEST-5 asks for a `lenny-test stress` flake budget for the reader-stop race. The spec-map changes are limited to the entries the proposal lists.

**What landed instead.** Step S20 also removed the `pkg/adapter/drain_test.go` row from `tests/registers/identifier-senses.yaml`, credited `pkg/adapter/export_test.go` to §4.7.10 and `pkg/adapter/holdstate_test.go` to §28, and updated two comments in `cmd/lenny-adapter/main.go` that described `CloseListener`.

**Why.** The tier-0 register gate and the tier-0 credit gate fail without the register removal and the two credits. The `main.go` comments described `CloseListener` as releasing only the listener, which CODE-4 makes false.

**What a later reader would otherwise get wrong.** A reader auditing the change against the proposal's file list would find edits to `tests/registers/identifier-senses.yaml`, the spec-map credits for `export_test.go` and `holdstate_test.go`, and `cmd/lenny-adapter/main.go` with no staged deliverable behind them.

## Proposed: Pod lifetime passed to Retire as one struct parameter

**Status:** proposed

**What the proposal says.** CODE-7 in the non-spec changes passes the pod's lifetime session count and uptime from the Decide inputs through `applyDisposition` and the `Retire` interface. The proposal does not specify the parameter form.

**What landed instead.** Step S21 added the exported struct `leasecontrol.PodLifetime{SessionsServed int; UptimeSeconds int64}` in `pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go`, next to `ClaimDispositionDriver`. `Retire` takes it as one parameter placed before `detail`, and `applyDisposition` takes it after `policy`. `RecordPodScrub` builds it from `sessionsServed` and `policy.PodUptimeSeconds`.

**Why.** The proposal names the values to pass and leaves the parameter form open. A single struct parameter keeps the existing `Retire` signature from growing by two positional integers. The proposal marks no IMPLEMENTOR'S CHOICE for this detail, so the step records it here.

**What a later reader would otherwise get wrong.** A reader would look for two integer parameters on `Retire` and `applyDisposition`, and would not find where the lifetime values are defined or how they reach the retirement path.

## Proposed: TEST-9 case placed in a new tier-4 file

**Status:** proposed

**What the proposal says.** TEST-9 adds `TestRecyclePathRuntimeNotLiveRetires_spec_5_2` to `tests/tier4_integration/recycle_scrub_path_test.go`.

**What landed instead.** Step S21 placed the test in the new file `tests/tier4_integration/recycle_runtime_not_live_test.go` in the same package. The test reuses the helpers in `recycle_scrub_path_test.go`.

**Why.** The test adds its own doubles: an ended runtime, an inspector, a recording driver, and a bufconn `GatewayControl` server around the real `leasecontrol` Service. `code-best-practices.md` keeps one concern per file, and `recycle_scrub_path_test.go` already exceeds 1,300 lines.

**What a later reader would otherwise get wrong.** A reader would search `recycle_scrub_path_test.go` for the TEST-9 case, not find it, and conclude the case is missing.

## Proposed: Wording chosen for the missed-ack log lines and status message

**Status:** proposed

**What the proposal says.** The CODE-9 bullet in non-spec changes §8.2, which covers `pkg/adapter/heartbeat.go` and `pkg/adapter/attach.go`, asks for the doc comments, both log lines, and the `DeadlineExceeded` status message to say that a missed ack ends the session's stream and calls `Runtime.Interrupt`. It does not give the exact wording, and it does not mention the scrub helper's own doc comment or any reflow.

**What landed instead.** Step S22 changed the log lines in `onHeartbeatHung` in `pkg/adapter/heartbeat.go` to read "...missed the heartbeat ack deadline; ending the session's stream (§28.5.3)" and "interrupt of hung runtime ... failed". It changed the `DeadlineExceeded` message in `pkg/adapter/attach.go` to read "runtime missed heartbeat ack deadline; session stream ended (§28.5.3)". No test asserts either string.

**Why.** The proposal leaves the exact wording open. This entry records the strings that were chosen so a reviewer can check them against the proposal's intent.

**What a later reader would otherwise get wrong.** A reader would not know which strings the step chose, and because no test asserts them, a reader would have no test to consult when checking the wording against the proposal.

## Proposed: CRD descriptions regenerated and merged with hand-maintained lines kept

**Status:** proposed

**What the proposal says.** CODE-9 in non-spec changes §8.2 says to regenerate the `sandboxclaims` and `sandboxtemplates` CRDs rather than edit them by hand.

**What landed instead.** Step S22 ran `make generate` (controller-gen). The generator also strips hand-maintained content from every CRD: the `lenny.dev/schema-version` annotation and the `x-kubernetes-preserve-unknown-fields` lines. The step merged the regenerated description hunks for `sandboxclaims` and `sandboxtemplates` into the existing files under `charts/lenny/crds` with those hand-maintained lines kept, and copied the result to `pkg/embedded/crds`. The step reverted the generator's unrelated changes to the `runtimes`, `sandboxes`, and `sandboxwarmpools` CRDs and to `zz_generated.deepcopy.go`.

**Why.** Running the generator unmodified would have removed the §10.5 schema-version annotation and the preserve-unknown-fields markers that the tree maintains outside controller-gen, which would change behavior. The description text is exactly the controller-gen output.

**What a later reader would otherwise get wrong.** A reader would assume the CRD files are unmodified generator output, and a later `make generate` run committed as-is would drop the schema-version annotation and the preserve-unknown-fields markers from every CRD.

## Proposed: Tier-5 kept-runtime check also asserts the running state

**Status:** proposed

**What the proposal says.** TEST-11 in the non-spec changes says the tier-5 case checks three things: session B lands on A's pod, A's `/workspace/slots` content is gone, and the runtime container's restart count is still 0.

**What landed instead.** In step S23, `requireRuntimeContainerKept` in `tests/tier5_e2e_kind/execution_modes_test.go` checks that `restartCount` is 0, and it also checks that the runtime container's `state.running.startedAt` is set.

**Why.** The pod uses `RestartPolicy: Never`, so a runtime container that had exited would still report a restart count of 0. The step reports that the running-state check is the assertion that detects a runtime that ended across the recycle boundary.

**What a later reader would otherwise get wrong.** A reader comparing the test to TEST-11 would see an assertion the proposal does not list. A reader who trusted TEST-11 alone would assume a restart count of 0 proves the runtime stayed up, which it does not under `RestartPolicy: Never`.

## Proposed: Spec-map entry added for §15.4.2 and slotsession_test.go credited under it

**Status:** proposed

**What the proposal says.** The S24 step text and TEST-16 in the non-spec changes say to register the new runtime-process-lifetime doc reconciliation test in `tests/spec-map.json` under §4.7.10, §5.2, §15.4.2, §15.4.3, and §28.5.3, matching its `// spec:` annotations.

**What landed instead.** `tests/spec-map.json` had no entry for §15.4.2. Step S24 added a `"15.4.2"` entry with the title "RPC Lifecycle State Machine" and the spec file `spec/15_external-api-surface.md`, and listed the new test under it. Once the entry existed, `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` in `tests/tier0_static` required `pkg/adapter/slotsession_test.go` to be listed under the same entry, because that file's `// spec:` annotations also name §15.4.2. The step added that file to the entry as well.

**Why.** The step reports that the new test could not be credited to §15.4.2 without an entry for the section, and that adding the entry brought the existing tier-0 registration gate to bear on `slotsession_test.go`.

**What a later reader would otherwise get wrong.** A reader comparing the spec map to the proposal would find a new section entry and a credit for `pkg/adapter/slotsession_test.go` that the proposal does not list, and would not know that the tier-0 gate is what required the second credit.

## Proposed: Runtime-process acknowledgment paragraph placed in execution-modes.md and linked from state-machines

**Status:** proposed

**What the proposal says.** DOC-2 and DOC-4 say that the state-machines `idle → draining` addition links to the runtime-process acknowledgment statement rather than restating it. Neither names the docs page that holds that statement.

**What landed instead.** Step S24 added a short paragraph headed **Runtime process kept across sessions.** under Presets in `docs/reference/execution-modes.md`. The `idle → draining` row on the state-machines page links to `execution-modes#presets`, and the `claimed → draining` row links to `execution-modes#recycle-lifecycle`.

**Why.** The step reports that no reader-facing page stated the acknowledgment rule as a single linkable paragraph, and that `execution-modes.md` is the page DOC-2 already edits for that rule.

**What a later reader would otherwise get wrong.** A reader would not know where the link target for the state-machines rows was chosen, would find a paragraph in `execution-modes.md` that the proposal does not name, and would not know that the two state-machines rows link to different anchors on that page.

## Proposed: Heartbeat test still names and asserts a SIGTERM to the runtime

**Status:** proposed

**What the proposal says.** The proposal stages no change to `pkg/adapter/heartbeat_external_test.go`.

**What landed instead.** The step sweep reports that `pkg/adapter/heartbeat_external_test.go` is unchanged. The test is still named `TestAttachHeartbeatHungSendsSIGTERM_spec_15_4_1_1826`, and its failure message still reads `interrupts = %v, want a single clean (SIGTERM) interrupt`.

**Why.** The landed text of §28.5.3 (Intra-pod), under the CH-MSGSOCK **Timing.** paragraph, states that "the adapter ends that session's stream, and the runtime process receives no signal". The test name and the failure message both state that the runtime receives a SIGTERM, which contradicts that text. The step reports that the site is test code rather than a comment, so changing it changes behavior the proposal did not stage, and it left the file unchanged.

**What a later reader would otherwise get wrong.** A reader of `pkg/adapter/heartbeat_external_test.go` would take the SIGTERM-on-hung-heartbeat contract as current, when the landed spec states that the runtime process receives no signal.

## Proposed: SandboxClaim CRD description omits the hold the acquisition path ends

**Status:** proposed

**What the proposal says.** The proposal stages no change to `charts/lenny/crds/lenny.dev_sandboxclaims.yaml`.

**What landed instead.** The step sweep reports that `charts/lenny/crds/lenny.dev_sandboxclaims.yaml` is unchanged and still states that the claim's deletion "is an explicit step of the claim lifecycle (hold expiry, orphan GC, or pod termination)".

**Why.** The landed text of §4.6.1 (Warm Pool Controller (Pod Lifecycle)), in the `SandboxClaim` row of the CRD mapping, lists the deletion causes as "(hold expiry, a hold the acquisition path ends, orphan GC, or pod termination)". The CRD description omits the hold the acquisition path ends. The description is rendered from the Go doc comment on the CRD type. The step reports that the site is a rendered CRD manifest rather than a comment, so changing it changes behavior the proposal did not stage, and it left the file unchanged.

**What a later reader would otherwise get wrong.** A reader of `charts/lenny/crds/lenny.dev_sandboxclaims.yaml` would take the shorter list of deletion causes as the current contract and would not know that a hold ended by the acquisition path also deletes the claim.
