# Deviations: Release ownership for pods, slots, per-pod claims, and credential leases at and after a terminal write

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Partial application of the regenerated SandboxClaim CRD in CODE-8

**Status:** accepted (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** step S5 (CODE-8: comments and regenerated descriptions for the retired end-of-occupancy outcome).

**What the proposal says:** CODE-8 item 1 directs the implementor to run `make generate` and copy the regenerated chart CRD to `pkg/embedded/crds/`.

**What landed instead:** `make generate` ran, but its output for `charts/lenny/crds/lenny.dev_sandboxclaims.yaml` was not committed as generated. Only the two regenerated description hunks, the `spec` description and the `status.phase` description, were applied to the committed file, and that file was then copied to `pkg/embedded/crds/`. The regenerated output for the other CRDs (runtimes, sandboxes, sandboxtemplates, and sandboxwarmpools) and for `zz_generated.deepcopy.go` was reverted.

**Why:** The step reports that the controller-gen output strips the hand-applied `lenny.dev/schema-version` annotation and the `x-kubernetes-preserve-unknown-fields` markers, and that existing tier-1 tests require both (`TestEmbeddedCRDsCarrySchemaVersionAnnotation` and the byte-copy invariant between the chart CRDs and the embedded CRDs). The step also reports that the import-alias change in `zz_generated.deepcopy.go` was generator drift unrelated to this proposal. Committing the raw output would have regressed those surfaces, which are outside the step's scope.

**What a later reader would otherwise get wrong:** A reader who follows the checklist literally would expect the committed chart CRDs and the embedded CRDs to equal the current `make generate` output. They do not. Rerunning `make generate` produces a diff in the SandboxClaim CRD (the schema-version annotation and the preserve-unknown-fields markers), in the other CRDs, and in `zz_generated.deepcopy.go`. That diff predates this proposal and is not evidence that CODE-8 was left incomplete.

## The bounded detached slot release retry is a Binder method behind a consumer-side interface

**Status:** accepted (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** step S6 (CODE-1: slot set primitive, keyed callers, removal-gated disposition, detached rollbacks, bounded retry, hash-tagged keys, outage rebuild, slot-holder predicate).

**What the proposal says:** The slot-set item of the staged code changes, on the bounded detached retry, names one helper in `podsession` with the signature `releaseSlotRetrying(ctx, claimer *podclaim.SlotClaimer, sandboxName, sessionID string, recycle, leaked bool) (bool, error)`.

**What landed instead:** The helper is `func (b *Binder) releaseSlotRetrying(ctx, claimer slotReleaser, ...)` in `pkg/gateway/podlifecycle/podsession/slotbinder.go`. `slotReleaser` is a consumer-side interface that `*podclaim.SlotClaimer` satisfies. The pause between attempts comes from the unexported `Binder` field `slotReleasePause` in `pkg/gateway/podlifecycle/podsession/binder.go`.

**Why:** The step reports that the proposal asks for an injectable pause and for a tier-1 assertion that a disposition error is followed by no second `ReleaseSlot` call. A `Binder` method carries the injected pause without a package-level mutable variable, and the interface lets the test count calls. The step reports that the behavior matches the proposal.

**What a later reader would otherwise get wrong:** A reader searching for the free function with the `*podclaim.SlotClaimer` parameter named in the proposal will not find it. The helper is a method on `Binder`, its claimer parameter has the interface type `slotReleaser`, and its pause is configured through the `Binder` field.

## Pause schedule for the bounded detached slot release retry

**Status:** accepted (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** step S6 (CODE-1: slot set primitive, keyed callers, removal-gated disposition, detached rollbacks, bounded retry, hash-tagged keys, outage rebuild, slot-holder predicate).

**What the proposal says:** The slot-set item of the staged code changes, on the bounded detached retry, marks the pause between attempts as IMPLEMENTOR'S CHOICE.

**What landed instead:** The pause starts at 100ms and doubles up to a 1s cap, inside the 5s bound of `detachedReleaseContext`. The schedule is implemented by `retryPause` and `sleepContext` in `pkg/gateway/podlifecycle/podsession/slotbinder.go`. At least two attempts run when the first attempt fails fast.

**Why:** The step reports this as a delegated choice that meets the stated constraint. The tests in `release_retry_internal_test.go` check the schedule without wall-clock sleeps.

**What a later reader would otherwise get wrong:** The proposal does not fix the pause values. A reader who needs the attempt count or the timing of the retry finds it in `retryPause` and the 5s bound of `detachedReleaseContext`, and those values originate in this implementation rather than in the proposal.

## Source of the pool label on the slot set rehydration metric

**Status:** accepted (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** step S6 (CODE-1: slot set primitive, keyed callers, removal-gated disposition, detached rollbacks, bounded retry, hash-tagged keys, outage rebuild, slot-holder predicate).

**What the proposal says:** The keyed-release item of the staged code changes marks as IMPLEMENTOR'S CHOICE whether the rehydration metric's pool label comes from the caller or from the Sandbox's `warmpool.LabelPool` label.

**What landed instead:** `SlotClaimer.ReleaseSlot` reads the Sandbox's `warmpool.LabelPool` label through `sandboxPool` in `pkg/gateway/podlifecycle/podclaim/slotclaimer.go`, and only when the release reports a rehydration. A failed read records an empty pool label.

**Why:** The step reports this as a delegated choice. The label equals the `req.Pool` that `reserveSlot` records, because `ClaimSlot` selects Sandboxes by that label.

**What a later reader would otherwise get wrong:** A reader would expect the pool label to be passed by the caller of `ReleaseSlot`. It is read from the Sandbox at release time, so a rehydration whose Sandbox read fails carries an empty pool label on the metric.

## Flag name and probe placement for the slot set outage rebuild

**Status:** accepted (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** step S6 (CODE-1: slot set primitive, keyed callers, removal-gated disposition, detached rollbacks, bounded retry, hash-tagged keys, outage rebuild, slot-holder predicate).

**What the proposal says:** The recovery-rebuild item of the staged code changes marks as IMPLEMENTOR'S CHOICE the flag name and whether the probe is a `Counter` method or a small type.

**What landed instead:** The flag is `--slot-counter-recovery-probe-interval` (environment variable `LENNY_SLOT_COUNTER_RECOVERY_PROBE_INTERVAL`, default 2s) in `cmd/lenny-gateway/flags.go`. The probe is `Counter.RunRecovery`, configured with `WithRecoveryProbe` and `WithRecoveryProbeInterval` in `pkg/gateway/storage/slotcounter/recovery.go`. It is wired in `cmd/lenny-gateway/pod_lifecycle.go` and started in `reconciler_workers.go` on `w.watchdogCtx`. Each probe pings Redis with `redisconn.PingWithTimeout` under a 2s bound. No chart value was added.

**Why:** The step reports this as a delegated choice. The proposal requires a gateway flag and does not mention a chart value.

**What a later reader would otherwise get wrong:** A reader would look for a chart value to tune the probe interval and find none. The interval is set only through the gateway flag or its environment variable, and the probe runs as a `Counter` method on the watchdog context.

## Recovery-rebuild cases run at tier 1 and the tier-8 Redis outage test only passes session identifiers

**Status:** accepted (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** step S6 (CODE-1: slot set primitive, keyed callers, removal-gated disposition, detached rollbacks, bounded retry, hash-tagged keys, outage rebuild, slot-holder predicate).

**What the proposal says:** The testing for the slot-set deliverable lists the tier-8 `slot_counter_redis_outage_test` as a retarget that passes the session identifier.

**What landed instead:** The tier-8 test was retargeted to pass session identifiers and nothing more. The case-table recovery-rebuild cases run at tier 1 against miniredis, using `Close` and `Restart`, which keep the data across the outage.

**Why:** The step reports that the tier-8 Redis container fixture cannot restart with its data intact, so the add-only rebuild cannot be observed there. The step reports this as consistent with the proposal, which places those cases at tier 1.

**What a later reader would otherwise get wrong:** A reader would expect the tier-8 outage test to exercise the add-only rebuild against a real Redis restart. It does not. The rebuild is covered only by the tier-1 miniredis cases.

## The fan-out test file kept its content and the full_revoke cases landed in the revoke hook test

**Status:** accepted (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** step S12 (CODE-7: full_revoke release routing).

**What the proposal says:** Among the tier-1 admin tests that land with CODE-7, `TestPartialFullRevokeFansOutOnlyTheCancelledSessions_spec_11_4` lives in `users_revoke_hook_test.go`, and `users_invalidate_fanout_test.go` drops its `USER_REVOKED` expectations.

**What landed instead:** `users_invalidate_fanout_test.go` held no `USER_REVOKED` expectation, so the step left it unchanged. The ordered recorder and the cancel-store-error cases, including the variant where the write commits and then returns an error, are in `pkg/gateway/externalapi/admin/users_revoke_hook_test.go`.

**Why:** The step reports that the fan-out test file had no `USER_REVOKED` expectation to drop at this revision.

**What a later reader would otherwise get wrong:** A reader would look for a CODE-7 edit to `users_invalidate_fanout_test.go` and find none. The file was already free of `USER_REVOKED` expectations, and the CODE-7 tier-1 cases are in `users_revoke_hook_test.go`.

## The full_revoke lease step is a library type in pkg/

**Status:** accepted (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** step S12 (CODE-7: full_revoke release routing), corrected by the design-conformance review.

**What the proposal says:** CODE-7 item 7 places the `full_revoke` lease step (Order step 2) in `userLeaseRevoker` in `cmd/lenny-gateway/user_revocation.go`. `userLeaseRevoker` takes the assigner through a consumer-side interface, and `userLeaseStore` drops `Remove`.

**What landed instead:** The lease step is `Revoker.RevokeUserLeases` in `pkg/gateway/credentials/leaserevoke/leaserevoke.go`. It takes the lease store, the assigner, and the deny list through the consumer-side interfaces `LeaseLister`, `Releaser`, and `DenyList`. `userLeaseRevoker` and `userLeaseStore` no longer exist. `cmd/lenny-gateway/adminrouter.go` wires the step with `leaserevoke.New(ls, w.credAssign, credRenewalProp, nil)`. The behavior matches the proposal: every `Release` runs before the first deny-list write, and a lease whose `Release` fails is logged and still deny-listed and counted. `tests/tier9_security/full_revoke_lease_return_test.go` drives the production `leaserevoke.Revoker` over an observed real `credassign.Service`, and the tier-4 and tier-8 `full_revoke` cases drive the same type. The tier-1 cases are in `pkg/gateway/credentials/leaserevoke/leaserevoke_test.go` and `cmd/lenny-gateway/user_revocation_rotation_test.go`.

**Why:** Reusable logic belongs under `pkg/` rather than in a `cmd/` main, and the tier-4, tier-8, and tier-9 packages cannot import package `main`. Moving the step lets those tiers exercise the production revoker instead of a test-side composition.

**What a later reader would otherwise get wrong:** A reader following CODE-7 item 7 would look for `userLeaseRevoker` in `cmd/lenny-gateway/user_revocation.go` and find no such type. The step is `leaserevoke.Revoker`, and the tier-9 case covers it directly. `tests/tier11_docs/release_ownership_deviations_currency_test.go` fails when this entry names a file or a symbol the tree no longer holds.

## A spec commit body names proposal scaffolding labels

**Status:** accepted (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** the design-conformance review.

**What the proposal says:** Commit messages describe a change by behavior and spec section, and keep the proposal path as the only link to the proposal.

**What landed instead:** The body of commit 699672d1b ("spec: a gateway release that ends the occupancy without reuse deletes the SandboxClaim") opens with "Proposal 0093 step S2 (SPEC-2a to SPEC-2u)." Those labels exist only inside the proposal. The commit changes the §4.6.3 SandboxClaim ownership row and the §4.6.3 `failed` binding state, and points the retired end-of-occupancy sites in §5.2, §6.2, §7, §8, §15.1, §24, §28, and §29 at §4.6.3.

**Why:** Committed history is not rewritten, so the commit keeps its message. No lint or gate over commit messages was added, because the proposal stages none.

**What a later reader would otherwise get wrong:** A reader of the first-parent history would find labels that resolve only against the proposal's own checklist and staged-edit list. The change the commit carries is the set of §4.6.3 edits and pointer sites named above.

## The admin router filters pre-running sessions and the full_revoke cancel write moved into a helper

**Status:** accepted (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** step S12 (CODE-7: full_revoke release routing).

**What the proposal says:** CODE-7 item 3 has the session server implement `ReclaimRevokedPreRunning` as `s.terminalReclaimPreRunning(ctx, from, row)`, which already filters pre-running states. Order step 4(a) applies it to each session whose pre-revoke state is `created`, `finalizing`, or `ready`.

**What landed instead:** The admin router also filters on the pre-revoke state before calling the hook, through `revokedPreRunning` in `pkg/gateway/externalapi/admin/users_invalidate.go`. The `full_revoke` cancel write moved into a helper, `cancelForRevoke` in `pkg/gateway/externalapi/admin/users.go`, which opens the terminal claim, runs the guarded write, and calls `AfterRevokeWrite`. The terminal-writer inventory row now names `cancelForRevoke`.

**Why:** The step reports that the ordered recorder must see only the pre-running sessions reach the hook. It also reports that the per-row helper keeps `terminateUserSessions` small and lets the inventory gate verify that `BeginTerminal` precedes the `Update`.

**What a later reader would otherwise get wrong:** A reader would expect the pre-running filter to exist only in the session server and the cancel write to sit inline in `terminateUserSessions`. The filter is applied in both the admin router and the session server, and the terminal-writer inventory names `cancelForRevoke` as the writer.

## The stale-slot reaper's integration tier maps to two tier-4 tests

**Status:** accepted (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** step S13 (CODE-9: stale-slot reaper, shared claimOrphanTimeout and checks, launch bound, per-row terminal stamp, and the adapter's absent or unclean answer).

**What the proposal says:** The CODE-9 Targets bullet for the stale-slot reaper states that the new glob key `pkg/gateway/podlifecycle/slotreaper` maps its `integration` tier to `tests/tier4_integration/slot_counter_redis_outage_fallback_test.go`.

**What landed instead:** The `integration` list under the key `pkg/gateway/podlifecycle/slotreaper` in `tests/change-graph.json` names both `tests/tier4_integration/slot_counter_redis_outage_fallback_test.go` and `tests/tier4_integration/slot_holder_pgstore_test.go`. A tier-0 guard pins both entries.

**Why:** The step reports that the finding permitted listing both files. The step kept the file the proposal names and added the pgstore test, which is the only tier-4 test of `ListStaleTerminalSessions`, a store method the reaper calls. Without that entry, `--changed` does not select the pgstore test when the reaper changes. The step asks a human to decide whether the proposal intended only the outage-fallback file.

**What a later reader would otherwise get wrong:** A reader would expect the reaper key to name only the outage-fallback test and would treat the pgstore entry, or the tier-0 guard that pins it, as an error to remove.

## binder_phases_test.go keeps the return-to-pool expectation for the created-state deadline

**Status:** accepted, fixed after implementation (the assertion message now says the claim delete drains the pod) (adjudicated 2026-10-21 under the owner's standing delegation)

**Reported by:** the step sweep for stale code the landed spec edits left behind.

**What the proposal says:** The proposal stages no change to `pkg/gateway/podlifecycle/podsession/binder_phases_test.go`.

**What landed instead:** `pkg/gateway/podlifecycle/podsession/binder_phases_test.go` is unchanged. Its assertion at the created-state deadline still states `t.Errorf("exclusive claim after the created-state deadline = %s, want gone (pod returned to the pool)", got)`.

**Why:** The sweep reports that the assertion contradicts the landed spec. The `created` row of the REST API table in `spec/15_external-api-surface.md` (§15.1) now states "On expiry the gateway transitions the session to `expired`, releases the pod claim (§4.6.3)", which replaced "releases the pod claim back to the pool". Under **Occupancy projection** in `spec/04_system-components.md` (§4.6.1), deleting a claim on a claimed pod drains the pod rather than returning it to idle. The sweep left the line in place because it is an assertion rather than a comment, and changing it changes test behavior the proposal did not stage.

**What a later reader would otherwise get wrong:** A reader of `pkg/gateway/podlifecycle/podsession/binder_phases_test.go` would take the return-to-pool expectation as the current contract for the created-state deadline.
