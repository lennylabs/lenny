# Deviations: Gateway does not survive or react to Attach stream failure

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Proposed: Line break position in the recycle-lifecycle diagram footer

**Status:** proposed

**Reported by:** step S4 (Land DOCS-1: reader docs follow SPEC-1, SPEC-2, and SPEC-4).

**What the proposal says.** DOCS-1 edit 20 asks for the footer of `docs/assets/diagrams/recycle-lifecycle.svg` to be split into two `foot` lines. The edit does not state where the line break goes. The most likely reading places the existing preConnect sentence on the first line and the new retirement sentence on the second line.

**What landed instead.** The break falls after the words "A failed or crashed session on a pool". The two footer lines sit at y=324 and y=344, and the viewBox and background height are now 364. The footer wording matches the staged text exactly.

**Why.** At 13 px, the retirement sentence on its own line measures about 1073 px. Starting at x=40, that line extends past the 1120 px canvas margin, which `doc-diagram-style.md` forbids. The chosen break produces two lines of about 850 px each, and both fit inside the margin. Renders made with resvg and Liberation Sans, which is metric-compatible with Helvetica, confirmed the fit.

**What a later reader would otherwise get wrong.** A reader who compares the diagram with DOCS-1 edit 20 and assumes the break falls between the two sentences would treat the landed footer as a misapplied edit. The wording is unchanged. Only the break position and the resulting canvas height of 364 differ from that reading.

## Proposed: File placement of the binding-generation publish-site test

**Status:** proposed

**Reported by:** step S8 (CODE-3: stream-failure handler, generation stamp, coordination check, runtime_crash report, report timeout, and path-6 fallback).

**What the proposal says.** The tests that land with CODE-3 list `TestPublishedBindingCarriesRowGeneration_spec_10_1_1` under the tier-1 file `pkg/gateway/sessionserver/stream_failure_test.go`.

**What landed instead.** The test is in a separate file, `pkg/gateway/sessionserver/binding_generation_test.go`, in package `sessionserver_test`. The other stream-failure tests are in `stream_failure_test.go`, in package `sessionserver`.

**Why.** The publish-site test drives the create, start, delegated-child, and resume handlers through the external-package fixtures `podResumeServer`, `materializeCluster`, and `seedAwaitingSession`. The coordination-check tests use the internal funnel fixture and unexported fields. A Go file belongs to exactly one package, so the tests are split across two files.

**What a later reader would otherwise get wrong.** A reader who looks for `TestPublishedBindingCarriesRowGeneration_spec_10_1_1` in `stream_failure_test.go`, as the proposal lists it, would conclude the test is missing. The test exists in `binding_generation_test.go`.

## Proposed: Shared outcome helpers in deliverMessageBatch

**Status:** proposed

**Reported by:** step S8 (CODE-3: stream-failure handler, generation stamp, coordination check, runtime_crash report, report timeout, and path-6 fallback).

**What the proposal says.** CODE-3 item 6 changes only the `ActionResumeAndDeliver` Send-error case of `deliverMessageBatch`.

**What landed instead.** `pkg/gateway/sessionserver/messages_delivery.go` adds the helpers `bufferOutcome` and `writeTargetTerminal`. The new fallback uses them, and the existing resume-failure, `ActionBufferInbox`, `ActionBufferDLQ`, and `ActionRejectTerminal` cases also use them. The receipts those existing cases return are unchanged.

**Why.** The re-classified fallback produces the same inbox, DLQ, and terminal outcomes that the existing cases already produce. Writing those outcomes a second time would have duplicated the blocks, which the reuse rule in `code-best-practices.md` forbids.

**What a later reader would otherwise get wrong.** A reader who compares the diff with CODE-3 item 6 would see edits to the resume-failure, `ActionBufferInbox`, `ActionBufferDLQ`, and `ActionRejectTerminal` cases and treat them as unscoped behavior changes. Those cases were refactored onto the shared helpers, and their receipts are unchanged.

## Proposed: retries_exhausted derived from the retryable classification in usage.go

**Status:** resolved

**Resolved by:** commit e96070b46

**Reported by:** step sweep (stale code the landed spec edits left behind).

**What the proposal says.** The proposal stages no change to `pkg/gateway/sessionserver/usage.go`.

**What landed instead.** `pkg/gateway/sessionserver/usage.go` is unchanged. The `child_failed` event payload still sets `RetriesExhausted: transient`, so the code derives `retries_exhausted` from the retryable classification of the failure.

**Why.** The landed spec in §8.8 (TaskRecord and TaskResult Schema) states: "`retriesExhausted` records whether the session's retry budget was spent, and the failure classification in the `child_failed` event (Section 8.10) records whether Section 7.3 classified the failure as retryable." The code contradicts that sentence because it reads the classification rather than the spent budget. The line is executable code rather than a comment, so changing it changes behavior that the proposal did not stage, and the sweep left it as it was.

**What a later reader would otherwise get wrong.** A reader of `pkg/gateway/sessionserver/usage.go` takes the old contract, in which `retries_exhausted` equals a transient classification, as the current one.

## Proposed: child_failed fixture asserts retries_exhausted true with an unspent budget

**Status:** resolved

**Resolved by:** commit e96070b46

**Reported by:** step sweep (stale code the landed spec edits left behind).

**What the proposal says.** The proposal stages no change to `pkg/gateway/sessionserver/child_failed_internal_test.go`.

**What landed instead.** `pkg/gateway/sessionserver/child_failed_internal_test.go` is unchanged. It still asserts `ev["retries_exhausted"] == true` and reports `retries_exhausted = %v, want true` otherwise.

**Why.** The landed spec in §8.8 (TaskRecord and TaskResult Schema) states that "`retriesExhausted` records whether the session's retry budget was spent". The fixture child has a `RetryCount` of 0, so its budget was not spent, yet the assertion requires `true`. The assertion is executable test code rather than a comment, so changing it changes behavior that the proposal did not stage, and the sweep left it as it was.

**What a later reader would otherwise get wrong.** A reader of `pkg/gateway/sessionserver/child_failed_internal_test.go` takes the old contract, in which a retryable classification implies `retries_exhausted` is `true`, as the current one.
