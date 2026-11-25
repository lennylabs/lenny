// SPDX-License-Identifier: MIT

package revocation

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/storage/issuedtokenstore"
)

// countingSource is a Source that counts its reads, can hold each read
// until the test releases it, and returns err when it is set.
type countingSource struct {
	mu      sync.Mutex
	revoked []issuedtokenstore.IssuedToken
	err     error
	// hold, when non-nil, blocks each read until a value is received or
	// the read's context is cancelled.
	hold  chan struct{}
	reads atomic.Int32
}

func newCountingSource(jtis ...string) *countingSource {
	s := &countingSource{}
	for _, jti := range jtis {
		s.revoked = append(s.revoked, issuedtokenstore.IssuedToken{JTI: jti})
	}
	return s
}

func (s *countingSource) ListRevoked(ctx context.Context, _ string) ([]issuedtokenstore.IssuedToken, error) {
	s.reads.Add(1)
	if s.hold != nil {
		select {
		case <-s.hold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	return append([]issuedtokenstore.IssuedToken(nil), s.revoked...), nil
}

// set replaces the rows the source returns.
func (s *countingSource) set(err error, jtis ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
	s.revoked = nil
	for _, jti := range jtis {
		s.revoked = append(s.revoked, issuedtokenstore.IssuedToken{JTI: jti})
	}
}

// awaitRead waits until the source has started its nth read.
func awaitRead(t *testing.T, s *countingSource, n int32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for s.reads.Load() < n {
		if time.Now().After(deadline) {
			t.Fatalf("the source saw %d reads after 5 s, want %d", s.reads.Load(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// runLoop starts RunRehydration and returns a stop function that cancels
// it and waits for it to return.
func runLoop(t *testing.T, c *Cache, interval time.Duration, trigger *Trigger, src Source, onError func(error)) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.RunRehydration(ctx, interval, trigger, fakeLister{"acme"}, src, onError)
		close(done)
	}()
	stop := func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("RunRehydration did not return after its context was cancelled")
		}
	}
	t.Cleanup(stop)
	return stop
}

// TestTriggerRehydratesBeforeTheNextTick pins that a fired trigger, such
// as a revocation-subscription reconnect, rereads the issued-token index
// at once rather than at the next periodic tick, so a revocation missed
// while the subscription was down is applied as soon as it is back.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
func TestTriggerRehydratesBeforeTheNextTick(t *testing.T) {
	c := NewCache()
	src := newCountingSource("jti-missed")
	trigger := NewTrigger()
	runLoop(t, c, time.Hour, trigger, src, nil)

	if c.IsRevoked("jti-missed") {
		t.Fatal("the cache holds jti-missed before any rehydration")
	}
	trigger.Fire()
	awaitRead(t, src, 1)
	deadline := time.Now().Add(5 * time.Second)
	for !c.IsRevoked("jti-missed") {
		if time.Now().After(deadline) {
			t.Fatal("the triggered rehydration did not load jti-missed within 5 s")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if c.Stale() {
		t.Error("the cache is stale after a successful triggered rehydration")
	}
}

// TestABurstOfTriggersRunsAtMostOneExtraRehydration pins that triggers
// fired while a rehydration runs coalesce into one more rehydration, so
// a burst of reconnects does not queue one database read per reconnect.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
func TestABurstOfTriggersRunsAtMostOneExtraRehydration(t *testing.T) {
	c := NewCache()
	src := newCountingSource("jti-1")
	src.hold = make(chan struct{})
	trigger := NewTrigger()
	runLoop(t, c, time.Hour, trigger, src, nil)

	trigger.Fire()
	awaitRead(t, src, 1)
	for range 10 {
		trigger.Fire()
	}
	src.hold <- struct{}{}
	awaitRead(t, src, 2)
	src.hold <- struct{}{}

	time.Sleep(100 * time.Millisecond)
	if n := src.reads.Load(); n != 2 {
		t.Errorf("a burst of 11 triggers ran %d rehydrations, want 2", n)
	}
}

// TestRehydrationRunsOnEveryTickWithoutATrigger pins that the periodic
// rehydration runs with no trigger at all, which is how a revocation
// dropped on a connected subscription reaches the replica.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
func TestRehydrationRunsOnEveryTickWithoutATrigger(t *testing.T) {
	c := NewCache()
	src := newCountingSource("jti-dropped")
	runLoop(t, c, 10*time.Millisecond, nil, src, nil)
	awaitRead(t, src, 3)
	if !c.IsRevoked("jti-dropped") {
		t.Error("the periodic rehydration did not load jti-dropped")
	}
}

// TestFailedTriggeredRehydrationKeepsTheStalenessClock pins that a
// rehydration that fails reports its error, leaves the set unchanged,
// and does not reset the staleness clock, so a replica whose rehydration
// keeps failing refuses validation once its last success is older than
// the window.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
func TestFailedTriggeredRehydrationKeepsTheStalenessClock(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	var clockMu sync.Mutex
	clock := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}
	c := NewCache(WithClock(clock))
	src := newCountingSource("jti-kept")
	if err := c.Rehydrate(context.Background(), fakeLister{"acme"}, src); err != nil {
		t.Fatalf("startup Rehydrate: %v", err)
	}
	src.set(errors.New("postgres unreachable"))

	errs := make(chan error, 8)
	trigger := NewTrigger()
	runLoop(t, c, time.Hour, trigger, src, func(err error) { errs <- err })

	clockMu.Lock()
	now = now.Add(60 * time.Second)
	clockMu.Unlock()
	trigger.Fire()
	select {
	case err := <-errs:
		if err == nil || err.Error() != "postgres unreachable" {
			t.Errorf("onError got %v, want the source's error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a failed triggered rehydration did not report its error")
	}
	if !c.IsRevoked("jti-kept") {
		t.Error("a failed rehydration dropped a jti the last success loaded")
	}
	if c.Stale() {
		t.Error("the cache is stale 60 s after its last success, inside the 90 s window")
	}
	clockMu.Lock()
	now = now.Add(31 * time.Second)
	clockMu.Unlock()
	if !c.Stale() {
		t.Error("the cache is fresh 91 s after its last success, want stale")
	}
}

// TestCancelledRehydrationReportsNoError pins that a rehydration a
// shutdown interrupts is not reported as a failure.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
func TestCancelledRehydrationReportsNoError(t *testing.T) {
	c := NewCache()
	src := newCountingSource()
	src.hold = make(chan struct{})
	var reported atomic.Int32
	trigger := NewTrigger()
	stop := runLoop(t, c, time.Hour, trigger, src, func(error) { reported.Add(1) })
	trigger.Fire()
	awaitRead(t, src, 1)
	stop()
	if n := reported.Load(); n != 0 {
		t.Errorf("onError ran %d times for a rehydration cancelled by shutdown, want 0", n)
	}
}

// TestNilTriggerFireIsANoOp pins that a nil Trigger ignores Fire. It
// verifies a nil guard and cites no requirement.
func TestNilTriggerFireIsANoOp(t *testing.T) {
	var trigger *Trigger
	trigger.Fire()
	if trigger.requests() != nil {
		t.Error("a nil Trigger returned a request channel, want nil")
	}
}

// steppedClock is a test clock that a source can advance during a read.
type steppedClock struct {
	mu  sync.Mutex
	now time.Time
}

func (s *steppedClock) Now() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

func (s *steppedClock) advance(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = s.now.Add(d)
}

func (s *steppedClock) set(at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = at
}

// slowSource advances the clock by its delay on every read, standing in
// for a read of Postgres that takes that long.
type slowSource struct {
	clock *steppedClock
	delay time.Duration
}

func (s slowSource) ListRevoked(context.Context, string) ([]issuedtokenstore.IssuedToken, error) {
	s.clock.advance(s.delay)
	return nil, nil
}

// TestStalenessCountsFromTheStartOfTheRead pins that the staleness gate
// measures the age of a successful rehydration from the instant its read
// began, so a read that takes 40 seconds does not extend the 90-second
// window by 40 seconds for a revocation committed while it ran.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
func TestStalenessCountsFromTheStartOfTheRead(t *testing.T) {
	clock := &steppedClock{now: time.Unix(1_700_000_000, 0)}
	started := clock.Now()
	c := NewCache(WithClock(clock.Now))
	if err := c.Rehydrate(context.Background(), fakeLister{"acme"}, slowSource{clock: clock, delay: 40 * time.Second}); err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	clock.set(started.Add(89 * time.Second))
	if c.Stale() {
		t.Error("the cache is stale 89 s after its read began, want fresh")
	}
	clock.set(started.Add(91 * time.Second))
	if !c.Stale() {
		t.Error("the cache is fresh 91 s after its read began and 51 s after it finished, want stale")
	}
}

// TestAnOlderOverlappingReadDoesNotMoveTheLastSuccessBack pins that a
// read that began first and finishes last does not age the cache: the
// last success stays at the start of the later read that finished first.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
func TestAnOlderOverlappingReadDoesNotMoveTheLastSuccessBack(t *testing.T) {
	clock := &steppedClock{now: time.Unix(1_700_000_000, 0)}
	c := NewCache(WithClock(clock.Now))
	held := newCountingSource()
	held.hold = make(chan struct{})
	first := make(chan error, 1)
	go func() { first <- c.Rehydrate(context.Background(), fakeLister{"acme"}, held) }()
	awaitRead(t, held, 1)

	clock.advance(10 * time.Second)
	laterStart := clock.Now()
	if err := c.Rehydrate(context.Background(), fakeLister{"acme"}, newCountingSource()); err != nil {
		t.Fatalf("later Rehydrate: %v", err)
	}
	held.hold <- struct{}{}
	if err := <-first; err != nil {
		t.Fatalf("earlier Rehydrate: %v", err)
	}

	clock.set(laterStart.Add(89 * time.Second))
	if c.Stale() {
		t.Error("the earlier read moved the last success back to its own start")
	}
}

// TestAHangingRehydrationFailsAtTheIntervalDeadline pins that a read that
// never returns fails after one interval and is reported, so the loop
// does not block on an unresponsive connection.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
func TestAHangingRehydrationFailsAtTheIntervalDeadline(t *testing.T) {
	c := NewCache()
	src := newCountingSource()
	src.hold = make(chan struct{}) // never released: each read waits for its deadline
	errs := make(chan error, 8)
	trigger := NewTrigger()
	runLoop(t, c, 50*time.Millisecond, trigger, src, func(err error) {
		select {
		case errs <- err:
		default:
		}
	})
	select {
	case err := <-errs:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("onError got %v, want a deadline error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a hanging rehydration was not cut off at its deadline")
	}
}
