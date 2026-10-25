# Deviations: Sidecar runtime processes have no platform supervisor

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Proposed: New change-graph keys for the S14 test files

**Status:** proposed

**Reported by:** step S14 (CODE-1: command, env, and restartable).

**What the proposal says.** The step's task text says to add the new tier-2 and tier-3 test files to the existing `tests/change-graph.json` keys for `pkg/apis/lenny`, `pkg/controller/runtime`, and `pkg/gateway/runtime/runtimestore`.

**What landed instead.** Those keys did not exist, so `tests/change-graph.json` gains three new keys: `pkg/apis/lenny/`, `pkg/controller/runtime`, and `pkg/gateway/runtime/runtimestore`. Each key lists its unit package and the new tier-2 envtest file or `runtimestore_test.go`. The `pkg/apis/lenny/` key also lists `tests/tier3_contract/crd_schema/...`. The existing `migrations/` key already covered `tests/tier2_component/migrations/...` and `stores/...`, so it is unchanged.

**Why.** The step reports that the keys named in the task were absent and that creating them was the only way to map the new test files. `lenny-test validate-maps` passes with the new keys.

**What a later reader would otherwise get wrong.** A reader following the task text would assume the three keys predate this proposal and that S14 only extended them. The keys were created by S14.

## Proposed: Scope of the CODE-1 command backfill on test and fixture Runtimes

**Status:** proposed

**Reported by:** step S14 (CODE-1: command, env, and restartable).

**What the proposal says.** CODE-1 says to add `command`, and `env` where the image ENV matters, to every test or fixture Runtime that the CEL rule rejects, including Go-test objects.

**What landed instead.** `command` was added to every Runtime applied to an API server: envtest objects, Kind YAML, the k8s templates, YAML in the tier-4, tier-5, tier-8, and tier-9 tests, and the `ApplyRuntimeSetFromConfig` inputs. It was also added to the full sidecar agent `RuntimeSpec` literals in fake-client unit tests: the runtime controller tests, warmpool `concurrent_sharing`, and the sandbox `runtimeCR` helper. It was not added to partial `RuntimeSpec` fragments that set only `DeploymentModel` or `Capabilities` (`sdkwarm_internal_test.go` and `nonce_only_internal_test.go`), or to variable-driven literals (backup `export_test.go`). No fixture received `env`. Each fixture image is built from the generic distroless Dockerfile or the heartbeat-silence Dockerfile, neither sets an ENV the binary relies on, and the argv is the ENTRYPOINT (`/usr/local/bin/lenny`, or `heartbeat-silence` plus `lenny`).

**Why.** The step reports that the CEL rule rejects only objects that an API server validates, and that spec fragments passed to pure functions are not Runtime objects created anywhere.

**What a later reader would otherwise get wrong.** A reader of CODE-1 would expect every Go-test Runtime literal to carry `command`, and some fixtures to carry `env`. The partial fragments and variable-driven literals named above carry no `command`, and no fixture carries `env`.

## Proposed: Cleanup worker PATH search skips relative elements

**Status:** proposed

**Reported by:** step S15 (CODE-2: C supervisor, image, and binary check).

**What the proposal says.** The cleanup bullet in the non-spec changes says the cleanup worker behaves like today's `workspace.RunSetup`, which looks up `argv[0]` through Go's `exec.LookPath`.

**What landed instead.** `sv_execve_path` in `supervisor/procs.c` skips every PATH element that is not absolute, including empty elements. When Go's `exec.LookPath` finds a match in a relative element, it returns an `ErrDot` error. The supervisor instead continues the search with the next element.

**Why.** The step reports that a relative element resolves against the working directory, which the agent can write. Skipping the element fails closed and needs no additional error path. Behavior differs only when PATH holds relative elements, and the fixed PATH holds none.

**What a later reader would otherwise get wrong.** A reader of the cleanup bullet would expect a match in a relative PATH element to produce an `ErrDot`-style error, as in `exec.LookPath`. The supervisor ignores relative elements and continues to the next element without an error.

## Proposed: SIGKILL immunity probed on a throwaway PID-namespace init

**Status:** proposed

**Reported by:** step S15 (CODE-2: C supervisor, image, and binary check).

**What the proposal says.** Item 8 of §4.7.11 says the supervisor tests whether it is immune to SIGKILL and SIGSTOP as PID-namespace init.

**What landed instead.** `sv_probe_init_sigkill` in `supervisor/selftest.c` tests SIGKILL immunity on a throwaway PID-namespace init and never on the supervisor itself. It creates that init with `clone(CLONE_NEWUSER|CLONE_NEWPID)` and falls back to `clone(CLONE_NEWPID)`. When neither namespace can be created, for example when seccomp blocks user namespaces, the SIGKILL result is inferred from the SIGSTOP result.

**Why.** The step reports that a design-review finding asked for this method. A real SIGKILL sent to the live supervisor would end every pod whenever SIGKILL immunity fails, which contradicts the statement that immunity gates no pod.

**What a later reader would otherwise get wrong.** A reader of item 8 would expect the supervisor to send SIGKILL to itself. The SIGKILL probe runs against a separate throwaway init, and where no namespace can be created the SIGKILL result is not measured but inferred from the SIGSTOP result.

## D5 · step S16 · 2026-10-23 · accepted
**Status:** accepted
**Proposal says:** The "Adapter state for one pod" paragraph of the proposal's non-spec-changes file (line 69) says `ServesNextSession()` returns false "on a keep pod, when the composed SocketRuntimeProcess.ServesNextSession does". The paragraph omits the case where a launch is permitted.
**Implemented instead:** `SupervisedRuntime.ServesNextSession` in `pkg/adapter/supervisedruntime.go:808-819` returns false whenever the not-live state is set. On a keep pod it returns true when `launchPermitted` holds (`pkg/adapter/supervisedruntime.go:815-817`), and otherwise it returns the result of the composed transport's `ServesNextSession`. This matches §4.7.10 Runtime supervisor, **Launch.** (`spec/04_system-components.md:1002-1006`), which permits a launch when "the pod has launched no runtime process since its creation or its last attested end ... while the not-live state is clear". It also matches §4.7.10 **Liveness** and §5.2 Runtime not live (`spec/04_system-components.md:1131-1132`, with `ReportPodScrub` at `:695`), which make the not-live state the condition that retires the pod, and §4.7.10 **SDK-warm.**, which says that after DemoteSDK ends the process "the session's start then launches a new process at step 7".
**Why:** The judges agreed unanimously and with high confidence that no legal change closes the finding. The fixer may not edit the proposal text. Changing the code to follow the proposal's literal sentence would report `runtime_live` false, and retire the pod, in two states where the applied spec permits the next session's start to launch a process at §4.7.9 step 7: a keep pod after an attested supervised DemoteSDK end with no relaunch yet, and a keep pod whose only session was reclaimed before its first launch. That change would contradict the applied spec. Reviewers raised the finding after rounds 3, 4, and 5, and each time stated that the code is correct and the proposal wording is narrower than the spec. Only commit 7616bbd8e ever changed the function, and reviewers will keep reporting the gap between the proposal wording and the code until the proposal text changes.
**Consequence if the proposal is not corrected:** A later reader or implementor who follows the paragraph literally would make a keep pod report `runtime_live` false whenever the transport reports false, including in the launch-permitted states above. The pod would retire although §4.7.10 permits its next session to launch a new process.
**Suggested next step:** correct the proposal
**Evidence:** Commit 7616bbd8e introduced `SupervisedRuntime.ServesNextSession` with the launch-permitted branch. Rounds 2 to 5 (5d79bad4c, bf566494b, 71e77b97d, and efa3755ce) left the function unchanged, which `git log -L808,818:pkg/adapter/supervisedruntime.go` confirms. The finding was reported after bf566494b, 71e77b97d, and efa3755ce with the notes "code appears right" and "code looks right". All three judges returned the verdict unresolvable with high confidence and classified the finding as a defect in the proposal text for the proposal's owner to decide. The judges assessed two other outstanding findings separately. The step-0 credential-enumeration finding in `pkg/adapter/podscrub.go` and `pkg/adapter/scrub/scrub.go` is a confirmed gap that a legal code change can close, so it is resolvable. The "P2" label in the 5d79bad4c commit message is advisory and does not block the step.

## Proposed: Pod builder refuses additional supervised-pod inputs

**Status:** proposed

**Reported by:** step S18 (CODE-4: pod builder, controller flag, chart, label, pod security).

**What the proposal says.** The CODE-4 bullet on `Build` stages one refusal: `Build` returns an error for a set `RuntimeCommand` with an empty `SupervisorImage`. The `createPod` bullet stages two more refusals: `restart` on a non-restartable runtime, and a `type: agent` sidecar runtime without `command`.

**What landed instead.** `Build` refuses more inputs, in `validateSupervised` in `pkg/controller/sandbox/podspec/supervisor.go` and in `Build` in `pkg/controller/sandbox/podspec/podspec.go`. It refuses a supervised pod in each of the following cases: `command[0]` is not absolute; the launch mode is empty or unknown; a `restart` lifetime appears beside a launch mode other than `session`; the stage UID equals the resolved adapter or agent UID; an env name is empty or contains `=`; or a runtime command is set on the embedded model. It also refuses an unknown `RuntimeProcess` value on any pod.

**Why.** The step reports that each of these inputs makes the adapter exit at P1 or makes the supervisor exit with a usage error. Without the refusals, the controller would keep recreating a pod that never reaches READY. The step gives an example: a Sandbox whose `executionMode` is `service` while its Runtime declares `session`, which the unstaged `executionMode` defect noted in the summary allows. The CRD treats that pod as restartable, and the pod renders `--launch-mode=service` beside `--runtime-process=restart`. The step describes the refusals as fail-closed.

**What a later reader would otherwise get wrong.** A reader of CODE-4 would expect `Build` to refuse only an empty `SupervisorImage` beside a set `RuntimeCommand`, and would expect `Build` to render a pod for the other inputs listed above. `Build` returns an error for each of them, and no pod is created.

## Proposed: Template read error blocks pod creation

**Status:** proposed

**Reported by:** step S18 (CODE-4: pod builder, controller flag, chart, label, pod security).

**What the proposal says.** §5.2 and CODE-4 say the lifetime comes from "the SandboxTemplate's mirrored runtimeProcess". The proposal does not say what happens when that read fails. The existing `resolveTemplate` is best-effort and returns nil on any miss.

**What landed instead.** `poolRuntimeProcess` in `pkg/controller/sandbox/supervision.go` treats a missing pool or template (NotFound) as a pool without the field, which yields a `keep` pod. It returns any other read error, so `createPod` creates no pod and the reconcile retries.

**Why.** The step reports that the label is immutable, so a transient read error handled as a miss would fix a `restart` pool's pod as `keep` for the pod's whole life. The proposal leaves this case unstated, and the step handles it fail-closed.

**What a later reader would otherwise get wrong.** A reader who assumes the lifetime read follows `resolveTemplate`'s best-effort behavior would expect a read error to produce a `keep` pod. Only NotFound produces a `keep` pod. Any other read error stops pod creation until a retry succeeds.

## Proposed: Supervisor environment omits a manifest-path variable

**Status:** proposed

**Reported by:** step S18 (CODE-4: pod builder, controller flag, chart, label, pod security).

**What the proposal says.** Summary D-ENV says the supervisor launches with "the platform's runtime variables (the socket names and the manifest path)".

**What landed instead.** `platformRuntimeEnv` in `pkg/controller/sandbox/podspec/supervisor.go` renders only `LENNY_ADAPTER_SOCKET=@lenny-runtime`. It renders no manifest-path variable.

**Why.** The step reports that the runtime SDK reads the manifest from its default path `/run/lenny/adapter-manifest.json` when `LENNY_ADAPTER_MANIFEST` is unset. The CODE-4 deployment-boundary bullet requires that any platform name absent from the Runtime env be the SDK socket variable, and a manifest variable would violate that requirement. The unsupervised runtime container also carries only the socket variable today.

**What a later reader would otherwise get wrong.** A reader of D-ENV would expect the supervised runtime's environment to carry a manifest-path variable such as `LENNY_ADAPTER_MANIFEST`. The environment carries only `LENNY_ADAPTER_SOCKET`, and the SDK locates the manifest through its default path.

## Proposed: Stage container mounts the volume at /stage

**Status:** proposed

**Reported by:** step S18 (CODE-4: pod builder, controller flag, chart, label, pod security).

**What the proposal says.** The proposal's process model shows the stage container as `/lenny-supervisor stage DEST` and the runtime running `/lenny-supervisor/lenny-supervisor`. It does not state the stage container's mount path.

**What landed instead.** The stage init container mounts the volume at `/stage` and runs `Args [stage, /stage]` under the image entrypoint. The runtime container mounts the volume read-only at `/lenny-supervisor`. The code is `supervisorStageContainer` in `pkg/controller/sandbox/podspec/supervisor.go`.

**Why.** The step reports that `Dockerfile.supervisor` places the binary at the file path `/lenny-supervisor`. A directory mount at that path in the stage container would shadow the binary that the container runs.

**What a later reader would otherwise get wrong.** A reader of the process model would expect the stage container to mount the volume at `/lenny-supervisor`, as the runtime container does. The two containers mount the same volume at different paths: `/stage` in the stage container and `/lenny-supervisor` in the runtime container.

## Proposed: Change-graph keys added for pkg/admission/webhook and cmd/lenny-controller

**Status:** proposed

**Reported by:** step S18 (CODE-4: pod builder, controller flag, chart, label, pod security).

**What the proposal says.** The step task text says to name the new test files "under the existing tests/change-graph.json keys for pkg/controller/sandbox, pkg/podsecurity, pkg/admission, and cmd/lenny-controller".

**What landed instead.** `tests/change-graph.json` had no `pkg/admission` key and no `cmd/lenny-controller` key. The step adds the keys `pkg/admission/webhook` and `cmd/lenny-controller`, and it extends the existing `pkg/controller/sandbox` and `pkg/podsecurity` keys. `validate-maps` then retired the two matching baseline prefixes from `tests/registers/change-graph-coverage.yaml`.

**Why.** The step reports that the keys did not exist, and that step S14 met the same gap and handled it the same way.

**What a later reader would otherwise get wrong.** A reader of the step text would assume the four keys existed before CODE-4. Two of them were created by this step, under the name `pkg/admission/webhook` rather than `pkg/admission`, and the change-graph coverage register lost two baseline prefixes as a result.

## Proposed: Stage UID is a fixed constant

**Status:** proposed

**Reported by:** step S18 (CODE-4: pod builder, controller flag, chart, label, pod security).

**What the proposal says.** D-STAGE and CODE-4 call for "a dedicated stage UID that is neither reserved UID". The proposal does not say whether operators can tune it.

**What landed instead.** `podspec.SupervisorStageUID` is a fixed constant, 65531. `Build` refuses a pod whose resolved adapter or agent UID equals it.

**Why.** The step reports that operators tune the adapter, agent, and cred-readers identities through `security.podUIDs`. A tunable stage UID would need a new chart value, a controller flag, and webhook wiring, none of which the proposal stages. A collision makes `Build` refuse the pod rather than admit a reserved UID as the stage UID.

**What a later reader would otherwise get wrong.** A reader of D-STAGE would expect the stage UID to be configurable alongside `security.podUIDs`. It is fixed at 65531. An operator who sets the adapter or agent UID to 65531 gets a pod-build refusal, and no chart value moves the stage UID.

## D12 · step S20 · 2026-10-23 · accepted
**Status:** accepted
**Proposal says:** The proposal states that the interim restart-pod and R16 cases are "skipped through" or "skipped with a reason in" `tests/registers/skip-reasons.yaml`. The statement appears in the non-spec-changes file under "Interim skips." (line 262), "Tier 6." (line 265, "skip-register entry"), and "Tier 10." (line 269), in the summary under D-RESTARTGATE (line 26), and in checklist step S20 (implementation-checklist.md line 47). The 0084 row of the summary's impacts table (line 114) says to "remove the skip-register entries".
**Implemented instead:** Each interim skip is a `t.Skip` or `t.Skipf` call with a literal reason that opens with the `blocked:` category and names the interim restart refusal and proposal 0084, R16, or the missing RuntimeClass. The sites are `tests/tier5_e2e_kind/runtime_supervisor_test.go:262` and `:535`, `tests/tier10_conformance/supervised_recycle_conformance_test.go:85` and `:116`, `tests/tier9_security/runtime_supervisor_security_test.go:407`, and `tests/tier6_e2e_cloud/runtime_supervisor_isolation_test.go:115`. No entry was added to `tests/registers/skip-reasons.yaml`.
**Why:** The judges agreed unanimously and with high confidence that no legal change closes the finding. `tests/registers/skip-reasons.yaml` is a `kind: skip-reason-baseline` register whose header (lines 1-18) states that the classifier only rewrites it downward and that "no run adds one, so a site that is not here fails tier 0". The tier-0 gate accepts a new skip site only when its reason is a literal string carrying a category such as `blocked:`, and the classifier drops any register entry whose site has a categorized reason. An entry added for these sites would therefore fail the gate or be removed, and an uncategorized reason would fail tier 0. The proposal's own review log records the same constraint as a WATCHOUT (review-log.md line 553): "a skip whose reason opens with `blocked:` needs no entry and the classifier drops an entry for it". The fixer may not edit the proposal text, so the finding recurs every round while the wording stands.
**Consequence if the proposal is not corrected:** A later reader would look for these skips in `tests/registers/skip-reasons.yaml` and find no entries. An implementor of proposal 0084 who follows the impacts row would look for register entries to remove, while the work that row intends is the removal of the `blocked:` `t.Skip` calls at the sites listed above.
**Suggested next step:** correct the proposal
**Evidence:** Commit 28c42ac31 converted every restart, R16, and tier-8 skip to a literal `blocked:` reason, after the round-2 commit 24bed7c2b used non-literal reasons that the tier-0 classifier rejected. Rounds 4 (95fa0459b) and 5 (30e8d1c8f) worked on other findings, and no round touched `tests/registers/skip-reasons.yaml`. Reviewers reported the finding in round 1 (finding 7, labelled "code right, proposal wording wrong"), in round 2 as the non-literal-reason findings, and in round 5 (finding 1). All three judges returned the verdict unresolvable with high confidence and classified the finding as a defect in the proposal text. They recommended rewording the "Interim skips.", "Tier 6.", and "Tier 10." bullets to name the literal `blocked:` `t.Skip` reasons, and rewording the 0084 row to "remove these t.Skip calls". The judges assessed two other round-5 findings as resolvable: treating a successful open of `/proc/1/mem` as a breach in `tests/testinfra/superviseprobe/attacks_linux.go:67-76`, and skipping in `resolveRuntimeClass` only when every candidate returns NotFound, with `t.Fatalf` on any other error, in `tests/tier6_e2e_cloud/runtime_supervisor_isolation_test.go:103-117`.

## Proposed: Stale runtime-lifetime assertion in the tier-10 recycle scrub conformance test

**Status:** proposed

**Reported by:** step sweep (stale code the landed spec edits left behind).

**What the proposal says.** The proposal stages no change to `tests/tier10_conformance/recycle_scrub_conformance_test.go`.

**What landed instead.** `tests/tier10_conformance/recycle_scrub_conformance_test.go:402` is unchanged and still states `t.Error("the runtime's connection ended at the recycle Shutdown; the runtime process must live as long as the pod")`.

**Why.** The step reports that the assertion contradicts the landed spec. In §5.2 **Deployer acknowledgment (runtime process kept across sessions).** the sentence "The runtime process lives as long as the pod" was removed, and the paragraph now reads "On a `keep` pod ... a later session on a recycled pod can run in the process that served the earlier one". §5.2 **Runtime-process lifetime** states that a `restart` pod ends its runtime process in each whole-pod scrub. The line is test logic rather than a comment, so changing it would change behavior the proposal did not stage.

**What a later reader would otherwise get wrong.** A reader of `tests/tier10_conformance/recycle_scrub_conformance_test.go` would take the retired contract, in which the runtime process lives as long as the pod, as current.

## Proposed: Stale runtime-lifetime assertion in the tier-5 execution modes test

**Status:** proposed

**Reported by:** step sweep (stale code the landed spec edits left behind).

**What the proposal says.** The proposal stages no change to `tests/tier5_e2e_kind/execution_modes_test.go`.

**What landed instead.** `tests/tier5_e2e_kind/execution_modes_test.go:214` is unchanged and still states `"pod %s: runtime container restartCount = %q, want 0; the runtime process must live as long "+ "as the pod and serve every session the pod serves"`.

**Why.** The step reports that the assertion contradicts the landed spec. §5.2 **Runtime-process lifetime (`recycle.runtimeProcess`).** states that on a `restart` pod the next session runs in a newly launched process. The line is test logic rather than a comment, so changing it would change behavior the proposal did not stage.

**What a later reader would otherwise get wrong.** A reader of `tests/tier5_e2e_kind/execution_modes_test.go` would take the retired contract, in which the runtime process lives as long as the pod and serves every session the pod serves, as current.

## Proposed: Stale runtime-lifetime assertion at the session teardown in the adapter slot session test

**Status:** proposed

**Reported by:** step sweep (stale code the landed spec edits left behind).

**What the proposal says.** The proposal stages no change to `pkg/adapter/slotsession_test.go`.

**What landed instead.** `pkg/adapter/slotsession_test.go:156` is unchanged and still states `t.Errorf("CH-RUNTIMEOPS carried a %q frame at the session teardown; the runtime process "+ "lives as long as the pod and no teardown sends it a terminate frame", frame.Type)`.

**Why.** The step reports that the assertion contradicts the landed spec. §5.2 **Runtime-process lifetime (`recycle.runtimeProcess`).** states that a `restart` pod ends its runtime process in each whole-pod scrub. The line is test logic rather than a comment, so changing it would change behavior the proposal did not stage.

**What a later reader would otherwise get wrong.** A reader of `pkg/adapter/slotsession_test.go` would take the retired contract, in which the runtime process lives as long as the pod and no teardown sends it a terminate frame, as current.

## Proposed: Stale runtime-lifetime assertion at the occupancy-zero teardown in the adapter slot session test

**Status:** proposed

**Reported by:** step sweep (stale code the landed spec edits left behind).

**What the proposal says.** The proposal stages no change to `pkg/adapter/slotsession_test.go`.

**What landed instead.** `pkg/adapter/slotsession_test.go:265` is unchanged and still states `t.Errorf("CH-RUNTIMEOPS carried a %q frame at the occupancy-zero teardown; the runtime "+ "process lives as long as the pod", frame.Type)`.

**Why.** The step reports that the assertion contradicts the landed spec. §5.2 **Runtime-process lifetime (`recycle.runtimeProcess`).** states that a `restart` pod ends its runtime process in each whole-pod scrub. The line is test logic rather than a comment, so changing it would change behavior the proposal did not stage.

**What a later reader would otherwise get wrong.** A reader of `pkg/adapter/slotsession_test.go` would take the retired contract, in which the runtime process lives as long as the pod, as current.

## Proposed: Proposal-internal labels remain in three committed messages

**Status:** proposed

**Reported by:** design-conformance fix pass (commit-message hygiene finding).

**What the proposal says.** The implementation names behavior and spec sections in commit messages. The checklist step identifiers and the Pre-READY sequence table's phase identifiers name parts of the proposal document and do not belong in the shipped history.

**What landed instead.** Commit `e3ce5e825` has the subject "S20: record skip-register finding as unresolvable, left for review". Commit `64ab28d21` has the subject "0087 S16: keep-pod ServesNextSession finding left for human review". The body of commit `5d79bad4c` refers to "the P2 signal through RunPreReady". The messages are unchanged.

**Why.** Committed history is not rewritten on this branch. Rewording the messages takes an interactive rebase of a shared feature branch, and the fix pass is not permitted to rewrite history. No gate over commit messages was added, because the proposal stages none.

**What a later reader would otherwise get wrong.** A reader of `git log` would take S16, S20, and P2 for identifiers of the shipped system. S16 and S20 are checklist steps of proposal 0087. P2 is the Pre-READY sequence row for a signal that arrives while the adapter waits for the runtime supervisor (§4.7.9, §6.1). If the owner wants the messages reworded before the branch merges, the replacements are "proposal 0087: record the interim restart skips as literal blocked: reasons for review", "proposal 0087: record the keep-pod ServesNextSession wording finding for review", and "a signal during the supervisor wait".
