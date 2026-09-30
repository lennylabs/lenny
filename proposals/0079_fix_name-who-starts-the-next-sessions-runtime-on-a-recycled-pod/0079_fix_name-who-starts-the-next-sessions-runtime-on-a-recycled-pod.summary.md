# Summary: Name who starts the next session's runtime on a recycled pod

## Summary

**What changes.**

- `spec/04` §4.7.9 step 7 and §4.7.10 state which component creates the runtime process under each deployment model and how long that process lives. The sidecar model's runtime container is started once by the kubelet and no component re-creates its process inside the pod's lifetime.
- `spec/05` §5.2 states what each whole-pod scrub step reaches across the container boundary, narrows the recycling promise from an unconditional one to one conditioned on the deployment model, and adds the `no_successor_runtime` retirement trigger with its `mode_factor` consequence. `spec/06` §6.1 and §6.2 and `spec/15` §15.4 take the matching qualifier, and `spec/16` §16.1 records the reason.
- `pkg/controller/sandbox` stamps a `lenny.dev/runtime-recreatable` pod label at render, and `pkg/sandbox/podscrub.Decide` gains a retire branch keyed on it, so a pod whose runtime cannot be re-created drains at each occupancy-zero boundary instead of being held for a session it cannot serve.
- `pkg/adapter/scrub`, `pkg/adapter/socketruntime.go`, and the Go runtime SDK lose the doc comments that claim the scrub terminates the runtime's process and that a recycling pool re-invokes the runtime after it exits.
- The tier-10 recycle-scrub conformance case is driven against a real `SocketRuntimeProcess` so its pod-survival property can fail, and gains a property pinning the second `Start`'s failure. The tier-5 case is split into a sidecar retirement case and an embedded reuse case, and its skip-register row is deleted.

**Fixed decisions.**

- The runtime process exits at occupancy zero. Nothing here keeps a runtime alive across a recycle boundary, and no implementation step may reintroduce one.
- §5.2's "keeps the process alive and reuses it" is the adapter process. Proposals 0031 and 0034 authored that clause and state the reading.
- `releaseActiveLocked` and the occupancy-zero gate (`pkg/adapter/socketruntime.go:384`) are correct and are not touched.
- The listener-teardown fix in `SocketRuntimeProcess.Close` (`pkg/adapter/socketruntime.go:467`) is proposal 0078's and lands first. Nothing here restages it.
- A kubelet container restart and an adapter-side spawn are both rejected on verified grounds (§9). The successor-runtime capability on the sidecar transport is deferred to a named follow-up whose mechanism is outlined in §9.4.
- The gate is a pod label evaluated at the recycle boundary. It is not an admission-time refusal, because the gateway runtime registry does not carry `deploymentModel` and the admin registration payload does not model it (§2, D2).
- The retire reason is non-counting. §16.1 freezes the `lenny_gateway_pod_retirement_total{reason}` vocabulary to the three limit triggers, and `no_successor_runtime` is a per-boundary structural reprovision like `vm_restart_reprovision`.

**Watch out for.**

- **Ordering against proposal 0078.** Today a second `StartSession` on a sidecar recycling pod fails at once, because `Close` closed the listener (`pkg/adapter/socketruntime.go:467`) and `Accept` on a closed listener returns immediately. Once 0078 removes that close, the same call blocks for the full 30-second accept bound (`pkg/adapter/socketruntime.go:181`, default resolved at `:193`). Landing 0078 without this proposal makes the defect slower and worse.
- **`SpawnPath` has no production caller.** The finding's dossier attributes it to the `cmd/lenny-adapter --runtime-bin` developer loop, and the field's own doc comment says the same (`pkg/adapter/socketruntime.go:38`). That flag builds a `SubprocessExecutor` instead; the only assignments to `SpawnPath` are `pkg/adapter/socketruntime_e2e_test.go:56`, `:198`, `tests/tier4_integration/concurrent_workspace_test.go:123`, and `tests/tier4_integration/concurrent_delegation_proxy_test.go:165`. A design that generalises the developer loop is generalising a test-only field.
- **`deploymentModel` is deliberately absent from the gateway registry.** `pkg/controller/runtime/controller.go:237` states that it "is a CRD-only field with no registry counterpart and is intentionally not mirrored", and the admin runtime registration payload does not model it, so every admin-registered runtime resolves to the `sidecar` default. A gate placed in `validateRecyclePolicy` (`pkg/gateway/runtime/poolstore/poolstore.go:591`) would refuse recycling on every admin-registered runtime in the tree, fixtures included.
- **The `Decide` branch order carries a reason-stability argument.** The `VMRestart` branch at `pkg/sandbox/podscrub/podscrub.go:388` documents why it sits before the session-count branch: otherwise a `maxSessionsPerPod: 1` pool retires with a different reason than an otherwise-identical `maxSessionsPerPod: 2` pool. The new branch inherits that argument verbatim and sits in the same window.
- **Scrub reach does not follow from `shareProcessNamespace: false` alone.** `buildSidecar` mounts `workspace`, the credential volume, `tmp`, `sessions`, `artifacts`, `/dev/shm`, and `shared` into both containers (`pkg/controller/sandbox/podspec/podspec.go:536-557`), so the filesystem steps do cross the boundary. Only the process-scoped and adapter-local steps do not.
- **The tier-10 double cannot fail.** `scrubConformanceRuntime.Start` returns `nil` unconditionally (`tests/tier10_conformance/recycle_scrub_conformance_test.go:61`), so Property 2 at `:231` asserts a pod survival the double could not have refuted. Substituting a real transport changes what that case asserts; read §10 before editing it.
- **A prior attempt burned four hours and six reverted commits** (`8cdd5d6d` through `ccaeb30d`, reverted by `39e08bb4`) making the runtime survive the boundary. Any edit whose effect is that a runtime process outlives occupancy zero is that attempt returning.
