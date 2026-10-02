# Deviations: Keep the pod's runtime listener across a session teardown

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Accepted: TEST-1 and TEST-9 landed in S1 without listener cleanups

**Status:** accepted

**What the proposal says.** The implementation checklist scopes S1 to code only (CODE-1 and CODE-3). It assigns the tier-1 cases TEST-1 and TEST-9 to S3, together with the conversion of the existing socket cases' cleanups to `CloseListener` (non-spec-changes.md §9 and §7.5).

**What landed instead.** TEST-1 (`TestSocketRuntimeListenerOutlivesTheLastSessionAndAcceptsTheNext_spec_15_4_3`) and TEST-9 (`TestSocketRuntimeDepartingReaderLeavesTheNextConnectionsSubscribers_spec_5_2`) landed in S1 in `pkg/adapter/socketruntime_test.go`. Neither case registers a listener cleanup, because `CloseListener` does not exist until S2.

**Why.** The build harness requires every behavioral fix to ship with a regression test that fails against the pre-fix code. TEST-1 and TEST-9 are the proposal's own regression tests for CODE-1 and CODE-3, so the step that landed those fixes also landed the tests.

**What a later reader would otherwise get wrong.** A reader following the checklist would expect TEST-1 and TEST-9 to be absent until S3, and would expect every socket case in the file to close its listener. S3 still has to add the `CloseListener` cleanups to TEST-1 and TEST-9 and to land TEST-2, TEST-3, and TEST-4.

## Accepted: TEST-6 dials the arriving runtime before the rendezvous

**Status:** accepted

**What the proposal says.** non-spec-changes.md §9 TEST-6 describes a race in which one goroutine closes the last session while another goroutine dials the runtime socket and calls `Start`, both released from a common rendezvous.

**What landed instead.** In `tests/tier7a_load_local/socket_listener_close_start_race_test.go`, the arriving runtime dials the socket before the rendezvous. Only `rt.Start` runs inside the race against the close of the last session. Step S5 left this ordering unchanged, as the finding directs.

**Why.** Step S5 reported that a dial inside the race adds a syscall ahead of `Start`, so the close wins every attempt and the reuse ordering is never reached. Dialing first still reaches both orderings. A close that unbinds the listener still fails the case, because closing the listener drops the connection queued in its backlog. Step S5 left the choice between amending the proposal and keeping this entry to a human.

**What a later reader would otherwise get wrong.** A reader comparing TEST-6 with the proposal would expect the dial to happen inside the race and could treat the pre-rendezvous dial as a defect, or move the dial into the race and lose coverage of the ordering in which `Start` reuses the listener.
