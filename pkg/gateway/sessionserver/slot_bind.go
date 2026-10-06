// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"log"
	"sync"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/slothealth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/slotstate"
)

// bindConcurrentSlot dispatches a §5.2 concurrent-workspace pool bind once the
// caller has built the slotReq. It is shared by the combined create-and-start
// path (startOnPod) and the two-step launch path (launchOnPod), which build
// the same request and dispatch identically: a concurrent pool materializes
// and launches the slot's workspace together at /start (it is not decomposed
// across finalize and start like the exclusive pool), so both callers reach
// here at launch time.
//
//   - When the row carries a slot reserved at /create (the §4.6 durable
//     binding: a non-empty PodAssignment on a non-recovery row, SlotID ==
//     SessionID == the reserved pod) the slot is not re-reserved. The gateway
//     reconnects to the reserved slot's pod and runs the materialize-and-launch
//     sequence against it (BindReservedSlot), the slot analog of the exclusive
//     reconnect.
//   - Otherwise (the §7.3 resume-rebuild path with a stale dead-pod
//     PodAssignment, or a slotless row) it reserves a fresh slot through the
//     §5.2 retry policy, holding an exhausted acquisition in the per-pool claim
//     FIFO on a `queue` pool and returning WARM_POOL_EXHAUSTED on the first
//     exhaustion of a `reject` pool. The slot retry policy surfaces the §5.2
//     exhaustion sentinels unwrapped, which is the queue's retry signal.
//
// spec: §4.1, §5.2 (proposal); §4.6.1 (pool exhaustion queue); §4.6 (durable
// binding reconnect).
func (s *Server) bindConcurrentSlot(ctx context.Context, row sessionstore.Session, match podsession.PoolMatch, slotReq podsession.SlotBindRequest) (*podsession.BindResult, error) {
	if row.PodAssignment != "" && !session.IsRecovery(row.State) {
		res, err := s.podBinder.BindReservedSlot(ctx, slotReq, row.PodAssignment, row.ID)
		if err != nil {
			return nil, failReservedSlot(ctx, s.podBinder, s.slotHealth, s.slotStates, s.slotReplacement,
				s.slotLeakGauge, slotReq, err)
		}
		return res, nil
	}
	return runWithQueue(ctx, s.claimQueue, match.Pool, match.OnPoolExhausted, match.MaxQueueWaitSeconds,
		func(ctx context.Context) (*podsession.BindResult, error) {
			return s.bindSlotWithRetry(ctx, slotReq)
		})
}

// failReservedSlot accounts and classifies a failed bind of a slot reserved at
// create. BindReservedSlot owns the reservation release and books a failed
// release onto sbe.Leaked, so the failure is accounted against the pod's §5.2
// health ledger with the disposition it carries. The reserved slot is not
// re-reserved and retried, so a post-reservation failure is terminal for this
// request: it is classified as the §5.2 client error so both the one-call and
// the two-step route answer a slot failure with the same envelope.
//
// spec: §5.2 (pool configuration and execution modes); §6.2 (pod state
// machine); §7.1 (normal flow).
func failReservedSlot(ctx context.Context, binder slotBinder, health *slothealth.Tracker,
	slots *slotstate.Registry, replacement func(pool string),
	leakGauge func(pod, pool string, leaked int),
	slotReq podsession.SlotBindRequest, err error,
) error {
	var sbe *podsession.SlotBindError
	if errors.As(err, &sbe) {
		accountSlotFailure(ctx, binder, health, slots, replacement, leakGauge,
			slotReq.Pool, slotReq.MaxConcurrentSessions, sbe, sbe.Leaked)
	}
	return classifySlotBindFailure(err, slotReq)
}

// maxSlotRetries is the §5.2 concurrent-workspace slot retry budget: one
// retry, two total attempts including the original. The retry always lands
// on a fresh slot (BindSlot re-reserves, materializing a fresh workspace),
// satisfying the §5.2 "fresh workspace guarantee".
const maxSlotRetries = 1

// slotBinder is the subset of *podsession.Binder the §5.2 slot retry
// policy drives. The interface seam lets applySlotRetryPolicy be unit
// tested with a fake binder rather than an envtest cluster.
type slotBinder interface {
	BindSlot(ctx context.Context, req podsession.SlotBindRequest) (*podsession.BindResult, error)
	ReleaseSlotReservation(ctx context.Context, sandboxName, slotID string, leaked bool) error
	DrainSandbox(ctx context.Context, sandboxName string) error
}

// bindSlotWithRetry applies the §5.2 concurrent-workspace slot retry
// policy around the gateway's pod binder. It is the production entry point
// that threads the server's binder, slot-health tracker, and replacement
// metric into applySlotRetryPolicy.
func (s *Server) bindSlotWithRetry(ctx context.Context, req podsession.SlotBindRequest) (*podsession.BindResult, error) {
	return applySlotRetryPolicy(ctx, s.podBinder, s.slotHealth, s.slotStates, s.slotReplacement, s.slotLeakGauge, req)
}

// classifySlotBindFailure maps a post-reservation slot failure onto the §5.2
// structured client error so the handler answers 422 SLOT_FAILED with the
// failure category and the session whose slot failed, rather than the
// retryable creation fallback. It is the no-retry counterpart of the tail of
// applySlotRetryPolicy, used on the two paths that bind a slot without a
// retry budget: the create-time reservation (ClaimSlot) and the reconnect to
// a slot reserved at create (BindReservedSlot). The binder has already
// released the reservation on both paths, so this only classifies.
//
// §5.2 conditions the client error on either no retry being attempted
// (a non-retryable category) or the retry budget being exhausted. These two
// paths carry no retry budget, so only the first half can hold: a
// non-retryable reason (oom, workspace_validation, policy_rejection) becomes
// the structured error, and a transient reason keeps the retryable
// SESSION_CREATION_FAILED/STARTING_FAILED fallback with its Retry-After
// (§15.1), because a dial or connect failure is recoverable and the client
// must not be told it is terminal. An error that is not a post-reservation
// slot failure (an exhaustion sentinel, a pool resolve failure) likewise
// passes through unchanged for its own mapping.
//
// spec: §5.2 "Client error on exhaustion"; §5.2 (non-retryable categories);
// §15.1 (retryable setup-window fallback).
func classifySlotBindFailure(err error, req podsession.SlotBindRequest) error {
	var sbe *podsession.SlotBindError
	if !errors.As(err, &sbe) {
		return err
	}
	reason := sbe.Reason()
	if !reason.NonRetryable() {
		return err
	}
	// The bind error stays in the chain rather than being replaced by its
	// own cause: the create-time rollback predicate reads it to tell a
	// binder-owned reservation release from a pre-bind leak, and the typed
	// setup/credential handlers match through it either way.
	return &podsession.SlotFailedError{
		Category:  string(reason),
		SessionID: req.SessionID,
		Pool:      req.Pool,
		Err:       sbe,
	}
}

// accountSlotFailure records one failed or leaked slot against the pod's §5.2
// health ledger and retires the pod when the combined windowed-failure plus
// persistent-leak count crosses the unhealthy threshold. Every bind path §7.1
// binds calls it, both concurrent bind paths and the §7.3 re-attach, so the
// create-time reserved path and the checkpoint restore reach the threshold
// §5.2 obliges them to reach, which the trigger states with no carve-out by
// code path. The failure funnel calls it too, through accountFailedSlot, for a
// slot whose session failed mid-session.
//
// The caller owns the reservation release and passes the slot's disposition
// as leaked: true when the compensating reclaim was not acknowledged clean or
// the release itself failed. A leaked slot stays counted in active_slots
// until the pod terminates, so it is recorded persistently (MarkLeaked, the
// leak gauge and RecordLeak, keyed by slot) rather than in the rolling
// window; any other failure is recorded once in the window (RecordFailure).
//
// The trigger fires once per pod. A pod the tracker has already tripped
// returns at entry, so a failure that arrives while its drain is in flight is
// neither counted nor drained again, and the replacement counter counts the
// pod once. A drain that cannot be stamped clears the trip so the pod's next
// accounted failure re-requests it.
//
// spec: §5.2 (pool configuration and execution modes, whole-pod replacement
// trigger); §6.2 (pod state machine).
func accountSlotFailure(ctx context.Context, binder slotBinder, health *slothealth.Tracker,
	slots *slotstate.Registry, replacement func(pool string),
	leakGauge func(pod, pool string, leaked int),
	pool string, maxConcurrentSessions int32,
	sbe *podsession.SlotBindError, leaked bool,
) {
	if health.Tripped(sbe.Pod) {
		return
	}
	recordSlotOutcome(health, slots, leakGauge, pool, sbe, leaked)
	if !health.Trip(sbe.Pod, maxConcurrentSessions) {
		return
	}
	// Retire the whole pod: the combined windowed-failure plus persistent-leak
	// count crossed the §5.2 unhealthy threshold.
	if drainErr := binder.DrainSandbox(ctx, sbe.Pod); drainErr != nil {
		log.Printf("sessionserver: §5.2 drain unhealthy pod %s: %v", sbe.Pod, drainErr)
		health.Untrip(sbe.Pod)
		return
	}
	if replacement != nil {
		replacement(pool)
	}
	health.Forget(sbe.Pod)
	// spec: §6.2 — the drained pod is being replaced; its leaked slots are
	// reclaimed with it, so drop the per-pod leaked tracking and zero the
	// gauge series.
	slots.ForgetPod(sbe.Pod)
	if leakGauge != nil {
		leakGauge(sbe.Pod, pool, 0)
	}
}

// recordSlotOutcome records one slot failure on the pod's §5.2 health ledger
// with the lifetime its disposition fixes: a leaked slot persistently, keyed
// by slot, and a reclaimed slot once in the rolling window.
func recordSlotOutcome(health *slothealth.Tracker, slots *slotstate.Registry,
	leakGauge func(pod, pool string, leaked int), pool string,
	sbe *podsession.SlotBindError, leaked bool,
) {
	if !leaked {
		// spec: §6.2 "`leaked` slot semantics" — the slot was reclaimed, so
		// this is a transient failure counted once in the rolling 5-minute
		// window.
		health.RecordFailure(sbe.Pod)
		return
	}
	// spec: §6.2 "`leaked` slot semantics" — the slot was not reclaimed,
	// so it remains counted in active_slots (the lenny_adapter_leaked_slots
	// gauge / the Redis slot-counter occupancy) until the pod terminates.
	// A leaked slot persists rather than aging out, so it is counted once
	// toward the threshold through the persistent RecordLeak rather than
	// the windowed RecordFailure.
	n := slots.MarkLeaked(sbe.SlotID, sbe.Pod, pool)
	if leakGauge != nil {
		leakGauge(sbe.Pod, pool, n)
	}
	health.RecordLeak(sbe.Pod, sbe.SlotID)
}

// slotFailureUnknownLabel is the lenny_slot_failure_total error_type value the
// failure funnel uses for a reason outside the §7.3 lists, so a deployer's
// unlisted label adds no series. spec: §5.2, §16.1.
const slotFailureUnknownLabel = "unknown"

// accountFailedSlot counts a slot whose session the failure funnel moved off
// an active state toward the pod's §5.2 whole-pod replacement trigger, and
// increments lenny_slot_failure_total once for it with the §7.3 failure
// reason as error_type. It runs before the funnel releases the slot: on a
// recycling concurrent pool the last ReleaseSlot patches the claim to
// recycling and starts the whole-pod scrub, so a drain stamped after it could
// reach a pod already handed on.
//
// A pool that cannot be resolved still records the failure in the window,
// unless the pod is already tripped, so the pod's next accounted failure on
// this replica counts it; the threshold is not evaluated and the counter is
// not incremented.
//
// spec: §5.2 (Failure isolation, whole-pod replacement trigger); §7.3 (retry
// and resume); §6.2 (pod crash during an active session).
func (s *Server) accountFailedSlot(ctx context.Context, row sessionstore.Session, bind *podsession.BindResult,
	reason string, classification session.FailureClassification,
) {
	if s.podBinder == nil || bind == nil {
		return
	}
	match, err := podsession.ResolvePool(ctx, s.podBinder.Client, s.poolPolicyReader(), s.agentNamespace,
		row.RuntimeRef, string(row.IsolationProfile), row.Pool)
	if err != nil {
		log.Printf("sessionserver: §5.2 resolve pool for failed slot of session %s on pod %s: %v",
			row.ID, bind.SandboxName, err)
		if !s.slotHealth.Tripped(bind.SandboxName) {
			s.slotHealth.RecordFailure(bind.SandboxName)
		}
		return
	}
	if s.podBinder.SlotFailure != nil {
		errorType := reason
		if classification == session.FailureUnknown {
			errorType = slotFailureUnknownLabel
		}
		s.podBinder.SlotFailure(errorType, match.Pool, bind.SandboxName)
	}
	accountSlotFailure(ctx, s.podBinder, s.slotHealth, s.slotStates, s.slotReplacement, s.slotLeakGauge,
		match.Pool, maxConcurrentSessions(match.MaxConcurrentSessions),
		&podsession.SlotBindError{Pod: bind.SandboxName, SlotID: bind.SlotID}, false)
}

// sessionLocks is a per-session mutex whose Lock honors the caller's context.
// It orders the failure funnel's slot accounting before a same-replica resume
// releases the session's earlier binding, so the drain the accounting requests
// is stamped before ReleaseSlot can reach the occupancy-zero recycle edge.
// Each entry is reference-counted by the goroutines holding or waiting on it
// and is deleted when the last one leaves, so idle sessions hold no entry. The
// zero value is ready to use. It has no wait budget of its own: the caller's
// context bounds the wait.
//
// spec: §5.2 (whole-pod replacement trigger).
type sessionLocks struct {
	mu    sync.Mutex
	locks map[string]*sessionLockEntry
}

// sessionLockEntry is one session's lock, a channel of capacity one that a
// holder fills, plus the count of goroutines holding or waiting on it. refs is
// mutated only under sessionLocks.mu.
type sessionLockEntry struct {
	sem  chan struct{}
	refs int
}

// Lock blocks until the caller holds sessionID's lock or ctx is done. On
// success it returns an unlock function that is safe to call more than once;
// on a done context it returns the context error and holds nothing.
func (l *sessionLocks) Lock(ctx context.Context, sessionID string) (func(), error) {
	e := l.ref(sessionID)
	select {
	case e.sem <- struct{}{}:
	case <-ctx.Done():
		l.unref(sessionID, e)
		return nil, ctx.Err()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			<-e.sem
			l.unref(sessionID, e)
		})
	}, nil
}

// ref fetches or creates sessionID's entry and counts one more referencing
// goroutine, under l.mu so the count and the map stay consistent with unref.
func (l *sessionLocks) ref(sessionID string) *sessionLockEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.locks == nil {
		l.locks = map[string]*sessionLockEntry{}
	}
	e, ok := l.locks[sessionID]
	if !ok {
		e = &sessionLockEntry{sem: make(chan struct{}, 1)}
		l.locks[sessionID] = e
	}
	e.refs++
	return e
}

// unref drops one reference and deletes the entry when the last referencing
// goroutine leaves, so a later Lock mints a fresh entry only when no goroutine
// still holds or waits on the old one.
func (l *sessionLocks) unref(sessionID string, e *sessionLockEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e.refs--
	if e.refs <= 0 && l.locks[sessionID] == e {
		delete(l.locks, sessionID)
	}
}

// applySlotRetryPolicy is the §5.2 "Concurrent-workspace slot retry
// policy":
//
//   - A reservation-exhaustion sentinel (no slot was reserved) is returned
//     unchanged so the handler maps it to WARM_POOL_EXHAUSTED.
//   - A slot failure after reservation is released so the retry lands on a
//     genuinely fresh slot and the pod's active_slots is not leaked. How the
//     failure counts toward the ceil(maxConcurrent/2) unhealthy threshold
//     depends on the release outcome, which covers both the reservation
//     release and the pod-side reclaim: a slot that released cleanly after
//     an acknowledged reclaim is a transient failure counted in the rolling
//     5-minute window (RecordFailure), and a slot whose release errored or
//     whose reclaim was not acknowledged clean is leaked permanently and
//     counted persistently (RecordLeak) so it does not age out of the window
//     before the threshold is reached. accountSlotFailure records either
//     arm and, when the pod crosses the threshold, drains it as a whole and
//     increments the replacement counter.
//   - A non-retryable reason (oom, workspace_validation, policy_rejection)
//     or an exhausted retry returns the §5.2 structured SlotFailedError.
//   - A transient reason retries once on a fresh slot.
//
// spec: §5.2 "Concurrent-workspace slot retry policy" (whole-pod
// replacement trigger); §6.2 (claimed → draining on the unhealthy-slot
// threshold); §6.2 "`leaked` slot semantics" (a failed slot is counted
// within the rolling window while a leaked slot, whose cleanup does not
// reclaim it, is counted persistently and feeds the per-pod
// lenny_adapter_leaked_slots gauge).
func applySlotRetryPolicy(ctx context.Context, binder slotBinder, health *slothealth.Tracker, slots *slotstate.Registry, replacement func(pool string), leakGauge func(pod, pool string, leaked int), req podsession.SlotBindRequest) (*podsession.BindResult, error) {
	var lastErr error
	for attempt := 0; attempt <= maxSlotRetries; attempt++ {
		result, err := binder.BindSlot(ctx, req)
		if err == nil {
			return result, nil
		}
		// Exhaustion sentinels are not slot failures (no slot was reserved):
		// surface them unchanged for the WARM_POOL_EXHAUSTED mapping.
		if errors.Is(err, podclaim.ErrNoConcurrentSlot) ||
			errors.Is(err, podclaim.ErrTenantMismatch) ||
			errors.Is(err, podclaim.ErrNoIdlePod) {
			return nil, err
		}
		var sbe *podsession.SlotBindError
		if !errors.As(err, &sbe) {
			// A failure outside the reserved-slot window (e.g. pool resolve)
			// is not subject to the slot retry policy.
			return nil, err
		}
		lastErr = err
		reason := sbe.Reason()
		// Release the failed slot so a retry re-reserves a fresh one and the
		// pod's active_slots is not leaked by the failed attempt. The slot's
		// disposition fixes how it is counted toward the ceil(maxConcurrent/2)
		// unhealthy threshold: a clean release after an acknowledged pod-side
		// reclaim makes the failure transient, and a failed release or a
		// reclaim not acknowledged clean leaks the slot permanently. The
		// release carries the disposition the binder's compensating reclaim
		// produced, so a leaked slot stays counted (§7.1, §6.2).
		relErr := binder.ReleaseSlotReservation(ctx, sbe.Pod, sbe.SlotID, sbe.Leaked)
		if relErr != nil {
			log.Printf("sessionserver: §5.2 release failed slot %s on pod %s: %v", sbe.SlotID, sbe.Pod, relErr)
		}
		accountSlotFailure(ctx, binder, health, slots, replacement, leakGauge,
			req.Pool, req.MaxConcurrentSessions, sbe, sbe.Leaked || relErr != nil)
		if reason.NonRetryable() || attempt == maxSlotRetries {
			return nil, &podsession.SlotFailedError{
				Category:  string(reason),
				SessionID: req.SessionID,
				Pool:      req.Pool,
				Err:       sbe.Err,
			}
		}
		// Transient with a retry remaining: loop to re-claim a fresh slot.
	}
	return nil, lastErr
}

// maxConcurrentSessions normalizes a pool's sessionPolicy bound to a
// minimum of 1, so a pool that records none reads as the exclusive
// default rather than as an invalid reservation request. spec: §5.2.
func maxConcurrentSessions(bound int32) int32 {
	if bound < 1 {
		return 1
	}
	return bound
}

// accountResumeSlotFailure accounts a failed §7.3 re-attach's slot against
// the pod's §5.2 health ledger. Binder.Resume has already sent the
// compensation and released the resume's slot reservation, and it carries
// the resulting disposition on a *podsession.SlotBindError in err's chain.
// The non-empty slot identifier guard is exactly the concurrent pool: an
// exclusive pool reserved nothing and has nothing to account. Without this
// the re-attach is the one bind path §7.1 binds whose leaked slot holds
// occupancy while the pod is never counted unhealthy or drained.
//
// spec: §5.2 (pool configuration and execution modes); §7.3 (retry and
// resume); §6.2 (pod state machine).
func accountResumeSlotFailure(ctx context.Context, binder slotBinder, health *slothealth.Tracker,
	slots *slotstate.Registry, replacement func(pool string),
	leakGauge func(pod, pool string, leaked int),
	match podsession.PoolMatch, err error,
) {
	var sbe *podsession.SlotBindError
	if !errors.As(err, &sbe) || sbe.SlotID == "" {
		return
	}
	accountSlotFailure(ctx, binder, health, slots, replacement, leakGauge,
		match.Pool, maxConcurrentSessions(match.MaxConcurrentSessions), sbe, sbe.Leaked)
}
