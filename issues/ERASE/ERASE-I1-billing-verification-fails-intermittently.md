---
id: ERASE-I1
status: planned
kind: defect
severity: high
requirements: []
change: 0176.S1
---

# The erasure job's billing verification fails intermittently, because a drained session's terminal billing event lands after pseudonymization

## Specification

§12.8 requires the erasure job to verify, after pseudonymizing the user's billing events, that neither the erasure salt nor the original user id survives, and to fail the job when the check does not pass.

## Evidence

- Merge gate run `5fe88e7f64fb9c324fbd3653` (branch `change/0117.S4-seam-model`, commit `8d75a6144`), integration tier: `TestErasureJobJourney/a_legal_hold_blocks_erasure_and_a_platform-admin_override_proceeds` failed with `override erasure job phase = failed, want completed`, the job's error being `billing erasure verification failed: the erasure salt or the original user id survived pseudonymization` (`pkg/storage/erasurejob/runner.go`, `errBillingVerification`).
- The same test passed 4 of 4 runs on that commit and 4 of 4 on `main` (`c9e094733`) on a quiet host, and in every earlier gate run. The failing run overlapped other docker-backed tests on the host.
- The test runs with `t.Parallel()`.
- Merge gate run `f26af57d68d9f604063ec524` on `main` at `0b8f960b0` (2026-11-25), integration tier: `TestErasureDeletesTheErasedUsersSessionMessageQueues_spec_12_8` failed with the same error. It passed in gate run `ac5091b6f00e1bb35016156d` on the same commit. Each integration test clones a database of its own (`tests/testinfra/containers/postgres_shared.go`), so another test's rows cannot reach the verification.
- Investigation on 2026-11-25 (logs under the session scratchpad, `erasure-diag/`): the surviving row is `billing_events` with `event_type = session.completed` and the raw `user_id` of the erased user, for the session the job drained. With a delay before the terminal billing append and before verification, the job failed 3 of 3 runs with the gate's error. With a delay before the terminal billing append alone, the job reached `completed` 3 of 3 runs, and the raw `user_id` was in `billing_events` about 2 s later. Unmodified, the insert lands 9 to 21 ms before the pseudonymizing phase starts (n=50), and 0 of 70 runs failed on a quiet host.

## Gap

The erasure job drains the user's sessions (`admin.DrainSessions` → `ForceTerminate` → `OnSessionTerminal`, `force_terminate.go`), and `OnSessionTerminal` runs the terminal pipeline in a detached goroutine (`recordSessionCompleted`, `pkg/gateway/core/session/sessionserver/usage.go`): the terminal write, the pod release (`terminal_hook.go`), then `afterTerminalRelease`, which closes the session log, writes the quota checkpoint, and appends the `session.completed` billing event with the raw `user_id`. The job waits only on the release gate (`ReleasePending` → `InPostTerminalWindow`, `sessionstore/terminal_mark.go`), which reports a session as pending only while its terminal row names a pod and records no release. A session with no pod is never pending, and for a pod-backed session the window closes before `afterTerminalRelease` runs. `runStoreDeleting` and the one-shot pseudonymize `UPDATE` (`pgstore.go`) then miss the late row.

When the late row lands between the pseudonymize step and the verification, the job fails and the user stays restricted until a retry. When it lands after the verification, the job completes and writes a verified receipt while the raw `user_id` stays in `billing_events`, which breaks §12.8. The quota checkpoint and the session-log close in `afterTerminalRelease` probably have the same ordering against `DeleteByUser`; that is not tested.

§12.8 "Lifecycle fence and drain" defers store deletion only while a session is in the §7.1 post-terminal window, and §7.1 limits that window to a terminal session whose row names a pod, ending it when the release is recorded, while "Terminal pipeline context" lists steps after the release. The specification therefore leaves the terminal billing event outside the window, and the fix starts with a specification edit.

## Suggested resolution

Gate the erasure job's store deletion, and the tenant-deletion phase that uses the same predicate, on the end of the whole terminal pipeline rather than on the recorded pod release: for example a marker on the session row written at the end of `recordSessionCompleted`, with every terminal session pending until the marker is set or the terminal pipeline's budget has elapsed, with or without a pod. Edit §7.1 ("Post-terminal window", "Terminal pipeline context") and §12.8 ("Lifecycle fence and drain") first. Optional defence in depth, for the owner: re-pseudonymize when verification finds a row, or refuse a billing append for a user whose processing is restricted. Tests: unit (the marker's ordering and the predicate with and without a pod), load_local (drain and runner with injected delays under `-race`, no raw `user_id` after `completed`), integration (no raw `user_id` after completion plus the pipeline budget), and security (erasure completeness). The integration tests stay as written, because a wait for `session.completed` in the test would hide the defect.
