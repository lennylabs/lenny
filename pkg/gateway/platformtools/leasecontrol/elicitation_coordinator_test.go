// SPDX-License-Identifier: MIT

package leasecontrol

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// coordElicitor is a per-call scripted Elicitor for coordinator tests.
// Each call takes the next step; a step's gate, when non-nil, holds the
// call open until closed. started receives the call index once the call
// has begun.
type coordElicitor struct {
	mu      sync.Mutex
	steps   []coordStep
	calls   []string
	started chan int
}

type coordStep struct {
	gate     chan struct{}
	approved bool
	err      error
}

func (e *coordElicitor) Elicit(ctx context.Context, _ /*tenantID*/, sessionID string) (bool, error) {
	e.mu.Lock()
	n := len(e.calls)
	e.calls = append(e.calls, sessionID)
	step := coordStep{approved: true}
	if n < len(e.steps) {
		step = e.steps[n]
	}
	e.mu.Unlock()
	if e.started != nil {
		e.started <- n
	}
	if step.gate != nil {
		select {
		case <-step.gate:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	return step.approved, step.err
}

func (e *coordElicitor) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.calls)
}

// coordBudgets is a BudgetSource whose Deny can block on a gate and fail.
// The coordinator calls only Deny and, through successCoolOffProvider,
// SuccessCoolOff; the other methods fail so an unexpected call is visible.
type coordBudgets struct {
	mu       sync.Mutex
	denies   []string
	denyErr  error
	denyGate chan struct{}
	coolOff  time.Duration
}

var errCoordUnexpected = errors.New("coordBudgets: unexpected call")

func (b *coordBudgets) TreeBudget(context.Context, string, string) (TreeBudget, error) {
	return TreeBudget{}, errCoordUnexpected
}

func (b *coordBudgets) ApplyGrant(context.Context, string, string, string, Dimensions) (AppliedGrant, error) {
	return AppliedGrant{}, errCoordUnexpected
}

func (b *coordBudgets) RejectionCoolOff(context.Context, string, string) time.Duration { return 0 }

func (b *coordBudgets) Deny(_ context.Context, _, _, requestingSessionID string) error {
	if b.denyGate != nil {
		<-b.denyGate
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.denies = append(b.denies, requestingSessionID)
	return b.denyErr
}

func (b *coordBudgets) SuccessCoolOff(context.Context, string, string) time.Duration {
	return b.coolOff
}

func (b *coordBudgets) denyCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.denies)
}

// manualTimers captures the coordinator's reclamation timers so a test
// fires them at a chosen point.
type manualTimers struct {
	mu    sync.Mutex
	fns   []func()
	delay []time.Duration
}

func (m *manualTimers) afterFunc(d time.Duration, f func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fns = append(m.fns, f)
	m.delay = append(m.delay, d)
}

func (m *manualTimers) fireAll() {
	m.mu.Lock()
	fns := m.fns
	m.fns = nil
	m.mu.Unlock()
	for _, f := range fns {
		f()
	}
}

type coordClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *coordClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *coordClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestCoordinator(el Elicitor, b BudgetSource) (*elicitCoordinator, *coordClock, *manualTimers) {
	clock := &coordClock{now: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)}
	timers := &manualTimers{}
	c := newElicitCoordinator(el, b, clock.Now)
	c.afterFunc = timers.afterFunc
	return c, clock, timers
}

func (c *elicitCoordinator) treeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.trees)
}

type consentResult struct {
	consent consent
	err     error
}

// startConsent runs requestConsent on its own goroutine and returns the
// channel its result lands on.
func startConsent(c *elicitCoordinator, sessionID string) <-chan consentResult {
	out := make(chan consentResult, 1)
	go func() {
		res, err := c.requestConsent(context.Background(), "acme", "root-1", sessionID)
		out <- consentResult{res, err}
	}()
	return out
}

// waitPending blocks until root-1 has a pending batch and returns it.
func waitPending(t *testing.T, c *elicitCoordinator) *consentBatch {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		tc := c.trees["root-1"]
		var b *consentBatch
		if tc != nil {
			b = tc.pending
		}
		c.mu.Unlock()
		if b != nil {
			return b
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no pending batch for root-1")
	return nil
}

func recv(t *testing.T, ch <-chan consentResult) consentResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("requestConsent did not return")
		return consentResult{}
	}
}

// nextStart waits for the next Elicit call to begin.
func nextStart(t *testing.T, started <-chan int) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("no further elicitation started")
	}
}

// settle gives goroutines started just before it time to reach their
// blocking point, so a waiter is parked on the batch before the driver
// resolves it.
func settle() { time.Sleep(30 * time.Millisecond) }

// TestCoordinatorDriverEndRedrivesWaiters_spec_8_6 pins the step 2 rule
// of the §8.6 elicitation flow: when the session that opened the pending
// elicitation ends before the user decides, the batched requests take no
// outcome from it and re-enter the flow, so the first opens a new
// elicitation and the other joins it.
//
// spec: §8.6 (Elicitation mode flow)
// diagnosis: a waiter returning the driver's ErrRequesterEnded means the
// batch stored the driver's error for its waiters instead of redriving
// them, so a live session is terminated because another session ended.
func TestCoordinatorDriverEndRedrivesWaiters_spec_8_6(t *testing.T) {
	driverGate, secondGate := make(chan struct{}), make(chan struct{})
	el := &coordElicitor{
		started: make(chan int, 4),
		steps: []coordStep{
			{gate: driverGate, err: fmt.Errorf("dismissed: %w", ErrRequesterEnded)},
			{gate: secondGate, approved: true},
		},
	}
	b := &coordBudgets{coolOff: time.Minute}
	c, _, _ := newTestCoordinator(el, b)

	driver := startConsent(c, "driver")
	<-el.started
	w1, w2 := startConsent(c, "waiter-1"), startConsent(c, "waiter-2")
	settle()
	close(driverGate)

	if r := recv(t, driver); !errors.Is(r.err, ErrRequesterEnded) {
		t.Fatalf("driver err = %v, want ErrRequesterEnded", r.err)
	}
	nextStart(t, el.started) // a redriven waiter opened the next elicitation.
	settle()
	close(secondGate)
	for name, ch := range map[string]<-chan consentResult{"waiter-1": w1, "waiter-2": w2} {
		r := recv(t, ch)
		if r.err != nil || !r.consent.approved {
			t.Errorf("%s = %+v, %v; want approved with no error (redriven)", name, r.consent, r.err)
		}
	}
	if n := el.callCount(); n != 2 {
		t.Errorf("elicitations = %d, want 2 (one redrive elicitation for both waiters)", n)
	}
}

// TestCoordinatorTimeoutPropagatesToWaiters_spec_8_6: a non-decision that
// is not a requester end still reaches every batched waiter, and the
// tree's entry is discarded.
//
// spec: §8.6 (Elicitation mode flow), §8.6 (Extension state)
// diagnosis: a waiter that redrives or succeeds on a timeout means the
// redrive flag is set for errors other than ErrRequesterEnded.
func TestCoordinatorTimeoutPropagatesToWaiters_spec_8_6(t *testing.T) {
	gate := make(chan struct{})
	timeout := errors.New("lease elicitation timed out")
	el := &coordElicitor{started: make(chan int, 2), steps: []coordStep{{gate: gate, err: timeout}}}
	c, _, _ := newTestCoordinator(el, &coordBudgets{})

	driver := startConsent(c, "driver")
	<-el.started
	waiter := startConsent(c, "waiter")
	settle()
	close(gate)

	for name, ch := range map[string]<-chan consentResult{"driver": driver, "waiter": waiter} {
		if r := recv(t, ch); !errors.Is(r.err, timeout) {
			t.Errorf("%s err = %v, want the timeout", name, r.err)
		}
	}
	if n := el.callCount(); n != 1 {
		t.Errorf("elicitations = %d, want 1", n)
	}
	if n := c.treeCount(); n != 0 {
		t.Errorf("trees = %d after a timed-out batch, want 0", n)
	}
}

// TestCoordinatorDenyFailureFailsBatch_spec_8_6 pins the §8.6 durability
// rule: the denial is persisted before REJECTED is returned, and a failed
// write fails the driver and every batched waiter with no rejection. The
// waiter does not return while the write is blocked.
//
// spec: §8.6 (Elicitation mode flow) step 4, §8.6 (Durability)
// diagnosis: REJECTED with a failed Deny means the denial error is
// dropped; a waiter returning while Deny is blocked means the batch is
// closed before the denial is written.
func TestCoordinatorDenyFailureFailsBatch_spec_8_6(t *testing.T) {
	gate := make(chan struct{})
	el := &coordElicitor{started: make(chan int, 2), steps: []coordStep{{gate: gate, approved: false}}}
	denyGate := make(chan struct{})
	b := &coordBudgets{denyErr: errors.New("postgres unreachable"), denyGate: denyGate}
	c, _, _ := newTestCoordinator(el, b)

	driver := startConsent(c, "driver")
	<-el.started
	waiter := startConsent(c, "waiter")
	settle()
	close(gate)

	select {
	case r := <-waiter:
		t.Fatalf("waiter returned %+v, %v while Deny was blocked", r.consent, r.err)
	case <-time.After(30 * time.Millisecond):
	}
	close(denyGate)
	for name, ch := range map[string]<-chan consentResult{"driver": driver, "waiter": waiter} {
		r := recv(t, ch)
		if r.err == nil || r.consent.approved {
			t.Errorf("%s = %+v, %v; want an error and no consent", name, r.consent, r.err)
		}
	}
	if n := b.denyCount(); n != 1 {
		t.Errorf("Deny calls = %d, want 1 (driver only)", n)
	}
}

// TestCoordinatorRejectionDeniesAndReclaims_spec_8_6: a rejection writes
// the denial once, every batched waiter is rejected, and the tree's entry
// is discarded with the batch.
//
// spec: §8.6 (Elicitation mode flow) step 4, §8.6 (Extension state)
// diagnosis: a retained entry means closing a batch with no success
// cool-off does not reclaim the tree, so consent state grows per tree.
func TestCoordinatorRejectionDeniesAndReclaims_spec_8_6(t *testing.T) {
	gate := make(chan struct{})
	el := &coordElicitor{started: make(chan int, 2), steps: []coordStep{{gate: gate, approved: false}}}
	b := &coordBudgets{}
	c, _, _ := newTestCoordinator(el, b)

	driver := startConsent(c, "driver")
	<-el.started
	waiter := startConsent(c, "waiter")
	settle()
	close(gate)
	for name, ch := range map[string]<-chan consentResult{"driver": driver, "waiter": waiter} {
		r := recv(t, ch)
		if r.err != nil || r.consent.approved || r.consent.approver != "client" {
			t.Errorf("%s = %+v, %v; want a client rejection", name, r.consent, r.err)
		}
	}
	if n := b.denyCount(); n != 1 {
		t.Errorf("Deny calls = %d, want 1", n)
	}
	if n := c.treeCount(); n != 0 {
		t.Errorf("trees = %d after a rejected batch, want 0", n)
	}
}

// TestCoordinatorRedriveWithoutWaitersReclaims_spec_8_6: a batch whose
// driver ended and that has no waiters leaves no entry behind.
//
// spec: §8.6 (Extension state)
// diagnosis: a retained entry means a redriven close skips reclamation.
func TestCoordinatorRedriveWithoutWaitersReclaims_spec_8_6(t *testing.T) {
	el := &coordElicitor{steps: []coordStep{{err: ErrRequesterEnded}}}
	c, _, timers := newTestCoordinator(el, &coordBudgets{})

	if _, err := c.requestConsent(context.Background(), "acme", "root-1", "driver"); !errors.Is(err, ErrRequesterEnded) {
		t.Fatalf("err = %v, want ErrRequesterEnded", err)
	}
	if n := c.treeCount(); n != 0 {
		t.Errorf("trees = %d after a redriven batch with no waiters, want 0", n)
	}
	if len(timers.fns) != 0 {
		t.Errorf("reclamation timers = %d, want 0 (no cool-off opened)", len(timers.fns))
	}
}

// TestCoordinatorCoolOffReclamation_spec_8_6: an approval keeps the
// tree's entry for its success cool-off and schedules reclamation at the
// cool-off end. The timer discards the entry only once the cool-off has
// elapsed and nothing is pending.
//
// spec: §8.6 (Extension state), §8.6 (Elicitation mode flow) step 3
// diagnosis: an entry deleted at approval loses the cool-off auto-grant;
// an entry kept after the timer fires with the cool-off elapsed leaks;
// an entry deleted while a batch is pending splits the tree into two
// batches.
func TestCoordinatorCoolOffReclamation_spec_8_6(t *testing.T) {
	const coolOff = time.Minute
	el := &coordElicitor{}
	c, clock, timers := newTestCoordinator(el, &coordBudgets{coolOff: coolOff})

	if r, err := c.requestConsent(context.Background(), "acme", "root-1", "s1"); err != nil || !r.approved {
		t.Fatalf("first consent = %+v, %v; want approved", r, err)
	}
	if n := c.treeCount(); n != 1 {
		t.Fatalf("trees = %d during the cool-off, want 1", n)
	}
	if len(timers.delay) != 1 || timers.delay[0] != coolOff {
		t.Fatalf("timers = %v, want one at %s", timers.delay, coolOff)
	}
	if r, _ := c.requestConsent(context.Background(), "acme", "root-1", "s2"); r.approver != "gateway-auto" {
		t.Errorf("cool-off request approver = %q, want gateway-auto", r.approver)
	}

	// A timer firing early leaves the entry.
	early := timers.fns[0]
	early()
	if n := c.treeCount(); n != 1 {
		t.Errorf("trees = %d after an early timer, want 1", n)
	}

	// After the cool-off, a pending batch keeps the entry until it closes.
	clock.advance(coolOff)
	gate := make(chan struct{})
	el.mu.Lock()
	el.steps = []coordStep{{}, {gate: gate, approved: false}}
	el.started = make(chan int, 1)
	el.mu.Unlock()
	driver := startConsent(c, "s3")
	<-el.started
	timers.fireAll()
	if n := c.treeCount(); n != 1 {
		t.Errorf("trees = %d while a batch is pending, want 1", n)
	}
	close(gate)
	if r := recv(t, driver); r.err != nil || r.consent.approved {
		t.Fatalf("second batch = %+v, %v; want a rejection", r.consent, r.err)
	}
	if n := c.treeCount(); n != 0 {
		t.Errorf("trees = %d after the second batch closed, want 0", n)
	}
}

// TestCoordinatorCoolOffElapsedTimerReclaims_spec_8_6: with nothing
// pending, the timer at the cool-off end discards the entry.
//
// spec: §8.6 (Extension state)
// diagnosis: a retained entry means the timer's re-check is wrong or no
// timer was scheduled.
func TestCoordinatorCoolOffElapsedTimerReclaims_spec_8_6(t *testing.T) {
	const coolOff = time.Minute
	c, clock, timers := newTestCoordinator(&coordElicitor{}, &coordBudgets{coolOff: coolOff})
	if _, err := c.requestConsent(context.Background(), "acme", "root-1", "s1"); err != nil {
		t.Fatalf("requestConsent: %v", err)
	}
	clock.advance(coolOff)
	timers.fireAll()
	if n := c.treeCount(); n != 0 {
		t.Errorf("trees = %d after the cool-off elapsed, want 0", n)
	}
}

// TestCoordinatorRedriveRaceKeepsOneBatch_spec_8_6 runs many waiters on a
// driver that ends, under the race detector, and checks that every
// redriven waiter lands on one new batch.
//
// spec: §8.6 (Elicitation mode flow)
// diagnosis: more than two elicitations means two redriven waiters each
// opened a batch, so the tree had two pending batches.
func TestCoordinatorRedriveRaceKeepsOneBatch_spec_8_6(t *testing.T) {
	const waiters = 16
	driverGate, secondGate := make(chan struct{}), make(chan struct{})
	el := &coordElicitor{
		started: make(chan int, waiters+2),
		steps: []coordStep{
			{gate: driverGate, err: ErrRequesterEnded},
			{gate: secondGate, approved: true},
		},
	}
	c, _, _ := newTestCoordinator(el, &coordBudgets{coolOff: time.Minute})
	driver := startConsent(c, "driver")
	<-el.started
	results := make([]<-chan consentResult, waiters)
	for i := range results {
		results[i] = startConsent(c, fmt.Sprintf("waiter-%d", i))
	}
	settle()
	first := waitPending(t, c)
	close(driverGate)
	recv(t, driver)
	nextStart(t, el.started)
	if waitPending(t, c) == first {
		t.Fatal("redriven waiters kept the closed batch")
	}
	close(secondGate)
	for i, ch := range results {
		if r := recv(t, ch); r.err != nil || !r.consent.approved {
			t.Errorf("waiter-%d = %+v, %v; want approved", i, r.consent, r.err)
		}
	}
	if n := el.callCount(); n != 2 {
		t.Errorf("elicitations = %d, want 2", n)
	}
}
