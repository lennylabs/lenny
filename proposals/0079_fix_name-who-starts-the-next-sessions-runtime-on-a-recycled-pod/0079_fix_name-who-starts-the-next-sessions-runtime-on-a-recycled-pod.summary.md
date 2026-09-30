# Summary: Name who starts the next session's runtime on a recycled pod

## Summary

**What changes.**

- The runtime process lives as long as the pod on every pool and in both deployment models. The sidecar runtime's `CH-MSGSOCK` connection is accepted at the pod's first session start and closed only by the pod-scope teardown `cmd/lenny-adapter` runs at process exit (D10). A later session on a recycled pod binds as a new slot on the live connection, which is the concurrent-pool model extended across occupancy zero. The embedded runtime's process is the adapter process, which already lives as long as the pod.
- `SocketRuntimeProcess.Close` and `Interrupt` stop tearing the transport down, and the active set that decided the teardown is deleted. The transport records a sticky ended state when the runtime closes its end, and `Start` and `Output` fail at once after it.
- The adapter reports on `ReportPodScrub`, in a new `runtime_live` field sampled after the whole-pod scrub, whether its runtime can serve the next session. `pkg/sandbox/podscrub.Decide` retires the pod with `runtime_not_live` when the report says it cannot or omits the field.
- `Decide` retires the pod with `process_reuse_unacknowledged` when the pool does not set `sessionPolicy.acknowledgeProcessLevelIsolation: true`. Both new reasons are non-counting and sit after the session-count and uptime retirements.
- The gateway reads the tenant pin on every idle-pod acquisition, including the Postgres fallback claim, and refuses a pod pinned to another tenant. On an `allowCrossTenantReuse` pool it also drains that pod. The fallback claim stamps the pin and leaves the mirror row unchanged when it refuses a pod. The warm-pool planner counts an idle pod pinned to a tenant toward `maxWarm` but not the unpinned warm target, so the pool provisions inventory for other tenants within its ceiling.
- The §15.4.2 terminate frame is no longer sent at occupancy zero, and the pod-exit path sends it once. The runtime generation no longer resets when `runtimeLive` empties, so `soleSession` is empty on every session after a kept process's first.
- §4.6.1, §4.6.3, §4.7, §4.7.9, §4.7.10, §5.1, §5.2, §6.1, §6.2, §7.1, §11.4, §13.1, §15.4, §15.4.1, §15.4.2, §15.4.3, §16.1, §28.5.3, §28.8, §29.2, §29.4, and §29.9 state the lifetime, the acknowledgment, the tenant rule, the new retire reasons, the scrub's reach, and the widened disclosure.

**Fixed decisions.**

- The runtime process is kept across occupancy zero, by the human decision of 2026-09-30 (problem statement §1.7). No step reintroduces a close at occupancy zero or a runtime redial.
- The adapter rule is unconditional. The adapter reads no recycle, acknowledgment, or cross-tenant setting, because every pool that must not reuse a pod retires it through the gateway after occupancy zero.
- `vm-restart` pools are untouched. The `VMRestart` branch runs before every new branch, and the per-release `maxSessionsPerPod` drain and its `vm-restart` exclusion (`pkg/gateway/session/recycle/scrubreporter_seams.go:373`) are not edited.
- New `Decide` and `PodRecyclePolicy` fields are true to retire, so every existing literal keeps its disposition. The wire field is positive, so an absent `runtime_live` reads as not live.
- This proposal reverses part of proposal 0073's SCHEMA-1 ("`Runtime.Close` is unchanged by this deliverable, in signature and in behavior") for `SocketRuntimeProcess`. Proposal 0073 is not edited.

**Watch out for.**

- **The earlier attempt.** Commits 8cdd5d6d through ccaeb30d, reverted by 39e08bb4, oscillated between keeping and closing the runtime inside proposal 0073, against spec text that required closing. Their facets (drain-frame suppression, a generation reset failing open, runtime redial, a phantom sibling in the active set, a reader closing the wrong subscribers, and a parked accept goroutine) are closed here by deletion or by the sticky ended state. None returns while nothing tears the transport down per session, the terminate frame is not sent at occupancy zero, the generation does not reset, and nothing redials.
- **Ordering against proposal 0078.** 0078 keeps the listener and leaves `Close`'s occupancy-zero connection close in place, so landed alone it turns a recycled sidecar pod's failed second `Start` from immediate into a 30-second accept wait. Land 0078 immediately before this proposal. CODE-4 extends 0078's `CloseListener` and keeps its name and signature, so 0078's callers and test cleanups compile unchanged. 0078's TEST-1 and TEST-9 assert a second accept after the last `Close`, which the kept connection removes; TEST-1 disposes of them.
- **Runtimes written for one session per process.** The Go runtime SDK binds `OnCreate`, the manifest session, and the credential path once per process (`sdks/runtime/go/runtime/runtime.go:76-84`), and no frame tells a runtime that a session ended. On an acknowledged pool such a runtime receives a later session's frames under the first session's context. The same gap exists on concurrent pools today (open decision 3).
- **The Postgres fallback claim reads and writes no tenant pin** (`pkg/gateway/podlifecycle/podsession/fallbackclaim.go:91-133`, `pkg/agentpodstate/pgstore/pgstore.go:245-279`). CODE-8 closes it in the same step that keeps a runtime across tenants' reach.
- **Scrub reach does not follow from `shareProcessNamespace: false` alone.** `buildSidecar` mounts `workspace`, the credential volume, `tmp`, `sessions`, `artifacts`, `/dev/shm`, and `shared` into both containers (`pkg/controller/sandbox/podspec/podspec.go:536-557`), so the filesystem steps cross the boundary. Step 1b removes only the adapter UID's segments.
- **The tier-11 pinned phrases.** `tests/tier11_docs/vm_restart_reprovision_consistency_test.go:176-209` pins phrases in the §5.2 recycle-lifecycle sentence and the §6.2 projection sentence. SPEC-5 and SPEC-9 append to those sentences without rewording them.
- **`cmd/lenny-adapter/main.go:86-88` states a "shared-uid pod layout".** The pod renders the adapter and runtime under distinct UIDs, and CODE-9 corrects the comment.
