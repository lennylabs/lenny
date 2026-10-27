# Deviations — 0100_fix_the-terminal-pipeline-what-happens-between-a-session-s

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Proposed: Exported accessor for the open terminal-claim count

**Status:** proposed

**Reported by:** step S9 (CODE-2: shutdown waits on the terminal claim record).

**What the proposal says.** Non-spec changes, CODE-2 item 1 adds one new method next to `Close`, `WaitTerminalWork(ctx) error`. Item 2(b) requires the shutdown helper to log the number of entries still open when the bound ends the wait, and it names no accessor for that count.

**What landed instead.** A second exported method, `(*Server).OpenTerminalWork() int`, returns `terminalClaims.size()`. It sits next to `WaitTerminalWork` in `pkg/gateway/sessionserver/sessionserver.go`. The shutdown helper `waitTerminalWork` in `cmd/lenny-gateway/shutdown.go` calls it to log the `open_entries` field.

**Why.** The `terminalClaims` field is unexported and the shutdown helper is in package `main`, so the helper needs an accessor to log the count that item 2(b) requires. The proposal does not mark the accessor as an implementor's choice.

**What a later reader would otherwise get wrong.** A reader of CODE-2 item 1 would expect `WaitTerminalWork` to be the only new exported method on `*Server` and would not find `OpenTerminalWork` in the proposal.

## Proposed: Additional shutdown-sequence details in the CODE-2 helper

**Status:** proposed

**Reported by:** step S9 (CODE-2: shutdown waits on the terminal claim record).

**What the proposal says.** Non-spec changes, CODE-2 item 2(a) runs `httpSrv.Shutdown` on a `--shutdown-timeout` context, and item 2(d) runs the final closes on a fresh `--shutdown-timeout` context. Item 2(d) does not list the OpenFeature provider close.

**What landed instead.** Items 2(a) and 2(d) landed as written. `cmd/lenny-gateway/shutdown.go` also carries the following additions:

- The listener stop runs on its own timeout context.
- The OpenFeature `experimentProviders.Close()` call remains the last final close, after the Redis close, which is its position in the sequence before the change.
- The `traceShutdown` call is nil-guarded.

**Why.** The OpenFeature close was part of the original shutdown sequence, and item 2(d) does not list it, so it keeps its original relative position. The nil guard lets the helper run with partial wiring in tests. The step reports that the run-time behavior matches the proposal.

**What a later reader would otherwise get wrong.** A reader of item 2(d) would conclude that the final closes end with the Redis close and would not expect the OpenFeature close, the separate listener-stop timeout context, or the nil guard on `traceShutdown` in the helper.

## Proposed: Real-Postgres commit-failure tests sit in the tier-2 and tier-8 packages

**Status:** proposed

**Reported by:** step S10 (CODE-3: pgstore.Update write-identifier check and migration).

**What the proposal says.** The step's Targets list `pkg/gateway/session/sessionstore/pgstore/pgstore_test.go` as the home of the real-Postgres commit-failure tests. Those tests cover the committed case, the aborted case, a read-back failure or timeout, an `Exec` error with no read-back, and a competing writer with the same `updated_at`.

**What landed instead.** The tests are at tier 2 in `tests/tier2_component/stores/sessionstore_commit_ack_test.go` and at tier 8 in `tests/tier8_chaos/session_commit_ack_lost_test.go`. Both use a harness in `tests/testinfra/commitfault`. Commit `cde1ca94a` landed this layout, and the S10 attempt left it unchanged.

**Why.** Real-Postgres tests go in the component and chaos tier packages, which are build-tagged and use testcontainers. They do not go in the `pgstore` unit package.

**What a later reader would otherwise get wrong.** A reader of the S10 Targets would look for the commit-failure tests in `pgstore_test.go` and would not find them there. The reader would need to look in the tier-2 and tier-8 test packages and in the `tests/testinfra/commitfault` harness.

## Proposed: Missing claimOrphanTimeout pool-admission enforcement is not filed as a new finding

**Status:** proposed

**Reported by:** step S18 (REC-1: BUILD-GAPS resolutions, departures, owner moves, claim map).

**What the proposal says.** REC-1 item 6 records the missing Go pool-admission enforcement of the §5.2 `claimOrphanTimeout` bound as a new OPEN finding in `BUILD-GAPS.md`.

**What landed instead.** No new finding was added. `BUILD-GAPS.md` already carries the defect as F-5.2.57, which commit `32a929b7f` filed from the unstaged-defects list of proposal 0100 with owner P3. The new tier-11 test checks that F-5.2.57 is still present and open.

**Why.** The records tests enforce a rule that one defect has one finding, so filing the defect again would create a duplicate finding.

**What a later reader would otherwise get wrong.** A reader of REC-1 item 6 would look for a finding filed by this implementation and would not find one. The defect is tracked as F-5.2.57, which predates the implementation.

## Proposed: BUILD-GAPS resolutions name spec text and test functions rather than proposal labels

**Status:** proposed

**Reported by:** step S18 (REC-1: BUILD-GAPS resolutions, departures, owner moves, claim map).

**What the proposal says.** REC-1 item 1 words each resolution by naming the closing deliverables with their proposal labels, such as "closed by the SPEC-2 slot clause", "closed by SPEC-3 (b)", "aligning §7.2 to §15.1 (SPEC-7)", and "the write-identifier check (CODE-3)". TEST-1 item numbers resolve to test function names.

**What landed instead.** The resolutions in `BUILD-GAPS.md` name the spec text, the code sites, and the test function names. Examples of the spec text are the slot clause of the Seal-and-export invariant in §7.1 and derive rule 1 of the Derive session semantics in §7.1. The resolutions do not use the CODE-, SPEC-, or TEST- labels. The new tier-11 test rejects such labels in a resolution belonging to proposal 0100.

**Why.** The rules of the implementation run forbid proposal-internal labels in durable records. The existing records tests also reject proposal step labels, because a reader of `BUILD-GAPS.md` cannot look them up.

**What a later reader would otherwise get wrong.** A reader who searches `BUILD-GAPS.md` for the labels that REC-1 item 1 quotes would find no matches. The resolutions identify each closing deliverable by its spec text, code site, or test function name instead.
