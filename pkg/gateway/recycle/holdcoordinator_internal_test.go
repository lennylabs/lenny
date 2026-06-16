// SPDX-License-Identifier: MIT

package recycle

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
)

// testNS is the agent namespace the coordinator unit tests operate in.
const testNS = "lenny-agents"

// testLogger returns a logger that discards output so the timer-mechanics
// tests stay quiet.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeTimer is an injectable timer the coordinator's afterFunc returns. It
// records the scheduled delay and the callback so a test can fire or cancel
// the timer deterministically without real wall-clock waits.
type fakeTimer struct {
	delay   time.Duration
	fn      func()
	stopped bool
}

func (t *fakeTimer) Stop() bool {
	already := t.stopped
	t.stopped = true
	return !already
}

// recordingDeleter records every hold-expiry DELETE and returns a scripted
// (aborted, err) outcome so a test can drive the precondition race.
type recordingDeleter struct {
	mu      sync.Mutex
	calls   []podclaim.ReservedHold
	aborted bool
	err     error
}

func (d *recordingDeleter) delete(_ context.Context, _ string, _ string, hold podclaim.ReservedHold) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, hold)
	return d.aborted, d.err
}

func (d *recordingDeleter) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.calls)
}

// recordingRebinder records every rebind patch and returns a scripted error.
type recordingRebinder struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (r *recordingRebinder) rebind(_ context.Context, _ string, claimName string, _ func() time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, claimName)
	return r.err
}

func (r *recordingRebinder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// newTestCoordinator wires a coordinator with injected rebind/delete seams
// and a capturable timer so the timer mechanics are tested without a real
// Kubernetes client or wall-clock waits. The returned timers slice is
// appended to on every afterFunc call.
func newTestCoordinator(now time.Time, del claimDeleter, reb claimRebinder) (*HoldCoordinator, *[]*fakeTimer) {
	var timers []*fakeTimer
	c := &HoldCoordinator{
		namespace: testNS,
		rebind:    reb,
		delete:    del,
		now:       func() time.Time { return now },
		afterFunc: func(d time.Duration, fn func()) timerHandle {
			ft := &fakeTimer{delay: d, fn: fn}
			timers = append(timers, ft)
			return ft
		},
		log:   testLogger(),
		holds: make(map[string]*activeHold),
	}
	return c, &timers
}

// TestHoldArmsTimerAndDeletesOnExpiry verifies the §3.2 hold-TTL timer fires
// the precondition-guarded DELETE at holdExpiresAt plus the grace, returning
// the pod to idle when no rebind raced.
// spec: 3.2 (reserved hold, precondition-guarded expiry DELETE), 4.6.1 (claimHoldTTLSeconds)
func TestHoldArmsTimerAndDeletesOnExpiry(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	del := &recordingDeleter{}
	c, timers := newTestCoordinator(now, del.delete, (&recordingRebinder{}).rebind)

	hold := podclaim.ReservedHold{UID: "uid-1", ResourceVersion: "100", HoldExpiresAt: now.Add(10 * time.Second)}
	c.Hold("pod-a", hold)

	if len(*timers) != 1 {
		t.Fatalf("Hold armed %d timers, want 1", len(*timers))
	}
	if got, want := (*timers)[0].delay, 10*time.Second+HoldExpiryGracePeriod; got != want {
		t.Errorf("timer delay = %v, want holdExpiresAt remaining + grace = %v", got, want)
	}
	if !c.Holds("pod-a") {
		t.Fatal("Holds(pod-a) = false after Hold, want true")
	}

	// Fire the timer: no rebind raced, so the DELETE deletes the claim.
	(*timers)[0].fn()
	if del.count() != 1 {
		t.Fatalf("expiry issued %d deletes, want 1", del.count())
	}
	if del.calls[0].ResourceVersion != "100" || del.calls[0].UID != "uid-1" {
		t.Errorf("expiry DELETE token = %+v, want the reserved-patch UID/resourceVersion", del.calls[0])
	}
	if c.Holds("pod-a") {
		t.Error("Holds(pod-a) = true after expiry, want the entry dropped")
	}
}

// TestHoldExpiredDeadlineFiresPromptly verifies a reserved patch whose
// holdExpiresAt is already in the past schedules the DELETE at the grace
// period alone rather than a non-positive (immediate-or-negative) delay.
// spec: 3.2 (per-claim hold-TTL timer)
func TestHoldExpiredDeadlineFiresPromptly(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c, timers := newTestCoordinator(now, (&recordingDeleter{}).delete, (&recordingRebinder{}).rebind)

	// holdExpiresAt is 5s in the past (a slow reserved patch).
	c.Hold("pod-a", podclaim.ReservedHold{HoldExpiresAt: now.Add(-5 * time.Second)})
	if got := (*timers)[0].delay; got != HoldExpiryGracePeriod {
		t.Errorf("expired-deadline timer delay = %v, want the grace period %v", got, HoldExpiryGracePeriod)
	}
}

// TestRebindCancelsLocalTimer verifies a same-tenant rebind within the hold
// window cancels this replica's expiry timer and patches the claim back to
// bound, so the timer never fires a DELETE.
// spec: 3.2 (reserved → bound rebind cancels the local timer)
func TestRebindCancelsLocalTimer(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	del := &recordingDeleter{}
	reb := &recordingRebinder{}
	c, timers := newTestCoordinator(now, del.delete, reb.rebind)

	c.Hold("pod-a", podclaim.ReservedHold{HoldExpiresAt: now.Add(10 * time.Second)})
	if err := c.Rebind(context.Background(), "pod-a"); err != nil {
		t.Fatalf("Rebind: %v", err)
	}
	if reb.count() != 1 {
		t.Fatalf("Rebind issued %d patches, want 1", reb.count())
	}
	if !(*timers)[0].stopped {
		t.Error("Rebind did not stop the local hold timer")
	}
	if c.Holds("pod-a") {
		t.Error("Holds(pod-a) = true after Rebind, want the entry dropped")
	}

	// Firing the (already-stopped) timer must not issue a DELETE: the entry
	// was removed under the lock, so the callback is a no-op.
	(*timers)[0].fn()
	if del.count() != 0 {
		t.Errorf("expiry issued %d deletes after a rebind cancelled the timer, want 0", del.count())
	}
}

// TestRebindErrorPropagates verifies a failed rebind patch surfaces an error
// (and still cancels the local timer, since the precondition guard is the
// authoritative race resolver and a stale timer must not linger).
// spec: 3.2 (within-hold rebind)
func TestRebindErrorPropagates(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reb := &recordingRebinder{err: errors.New("patch boom")}
	c, _ := newTestCoordinator(now, (&recordingDeleter{}).delete, reb.rebind)

	c.Hold("pod-a", podclaim.ReservedHold{HoldExpiresAt: now.Add(10 * time.Second)})
	err := c.Rebind(context.Background(), "pod-a")
	if err == nil {
		t.Fatal("Rebind returned nil on a patch failure, want an error")
	}
	if c.Holds("pod-a") {
		t.Error("Holds(pod-a) = true after a failed Rebind, want the local timer cancelled")
	}
}

// TestExpiryAbortedByCrossReplicaRebind verifies that when the DELETE
// precondition fails (a cross-replica rebind changed the resourceVersion),
// the expiry aborts without error and leaves the rebound claim intact.
// spec: 3.2 (rebind-vs-hold-expiry precondition race)
func TestExpiryAbortedByCrossReplicaRebind(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	del := &recordingDeleter{aborted: true}
	c, timers := newTestCoordinator(now, del.delete, (&recordingRebinder{}).rebind)

	c.Hold("pod-a", podclaim.ReservedHold{ResourceVersion: "100", HoldExpiresAt: now.Add(time.Second)})
	(*timers)[0].fn()
	if del.count() != 1 {
		t.Fatalf("expiry issued %d deletes, want 1 (the aborted attempt)", del.count())
	}
	// The entry is dropped on expiry regardless of the abort: the timer has
	// fired and the claim is now owned by the rebinding replica.
	if c.Holds("pod-a") {
		t.Error("Holds(pod-a) = true after an aborted expiry, want the entry dropped")
	}
}

// TestReHoldReArmsAgainstNewToken verifies a second Hold for the same claim
// cancels the prior timer and arms against the new token, so the expiry
// DELETE always carries the most recent reserved-patch resourceVersion.
// spec: 3.2 (per-claim hold-TTL timer)
func TestReHoldReArmsAgainstNewToken(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	del := &recordingDeleter{}
	c, timers := newTestCoordinator(now, del.delete, (&recordingRebinder{}).rebind)

	c.Hold("pod-a", podclaim.ReservedHold{ResourceVersion: "100", HoldExpiresAt: now.Add(time.Second)})
	c.Hold("pod-a", podclaim.ReservedHold{ResourceVersion: "200", HoldExpiresAt: now.Add(time.Second)})

	if len(*timers) != 2 {
		t.Fatalf("two Holds armed %d timers, want 2", len(*timers))
	}
	if !(*timers)[0].stopped {
		t.Error("the first timer was not cancelled on re-hold")
	}
	// Fire the second (current) timer: the DELETE must carry the second token.
	(*timers)[1].fn()
	if del.count() != 1 || del.calls[0].ResourceVersion != "200" {
		t.Fatalf("re-hold expiry DELETE token = %+v, want resourceVersion 200", del.calls)
	}
}

// TestStopCancelsAllTimers verifies Stop cancels every armed timer so a clean
// shutdown does not fire expiry DELETEs against a draining client.
// spec: 3.2 (reserved hold timer ownership)
func TestStopCancelsAllTimers(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	del := &recordingDeleter{}
	c, timers := newTestCoordinator(now, del.delete, (&recordingRebinder{}).rebind)

	c.Hold("pod-a", podclaim.ReservedHold{HoldExpiresAt: now.Add(time.Second)})
	c.Hold("pod-b", podclaim.ReservedHold{HoldExpiresAt: now.Add(time.Second)})
	c.Stop()

	for i, ft := range *timers {
		if !ft.stopped {
			t.Errorf("timer %d not stopped by Stop", i)
		}
	}
	if c.Holds("pod-a") || c.Holds("pod-b") {
		t.Error("Holds reported a live entry after Stop, want all dropped")
	}
}

// TestCancelIsNoOpOnPeerHeldClaim verifies Cancel on a claim this replica does
// not hold is a no-op: the acquisition-path rebind cancels only a local timer,
// and a peer's precondition guard resolves the cross-replica race.
// spec: 3.2 (any replica may rebind; precondition guard is authoritative)
func TestCancelIsNoOpOnPeerHeldClaim(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c, _ := newTestCoordinator(now, (&recordingDeleter{}).delete, (&recordingRebinder{}).rebind)
	// No Hold for pod-z: Cancel must not panic or error.
	c.Cancel("pod-z")
	if c.Holds("pod-z") {
		t.Error("Holds(pod-z) = true after Cancel on an unheld claim")
	}
}

// TestNewHoldCoordinatorRequiresClientAndNamespace pins the fail-closed
// construction guards.
// spec: 3.2 (reserved hold coordinator wiring)
func TestNewHoldCoordinatorRequiresClientAndNamespace(t *testing.T) {
	if _, err := NewHoldCoordinator(HoldCoordinatorOptions{Namespace: testNS}); err == nil {
		t.Error("NewHoldCoordinator with no Client returned nil error, want a guard error")
	}
	// A Client without a Namespace is equally fail-closed: a namespace-less
	// coordinator would address claims in the wrong (empty) namespace.
	if _, err := NewHoldCoordinator(HoldCoordinatorOptions{Client: fake.NewClientBuilder().Build()}); err == nil {
		t.Error("NewHoldCoordinator with no Namespace returned nil error, want a guard error")
	}
}

// TestExpiryDeleteErrorIsLoggedNotPanicked pins the §3.2 expiry-DELETE error
// branch: when the precondition-guarded DELETE fails for a reason other than a
// rebind abort, the coordinator logs the failure and drops the hold entry
// rather than panicking or retrying, so a transient API error during expiry
// does not strand the coordinator. A later sweep by the §4.6.1 orphan GC
// reclaims the reserved claim.
// spec: 3.2 (hold-expiry DELETE), 4.6.1 (orphan GC backstops a failed expiry)
func TestExpiryDeleteErrorIsLoggedNotPanicked(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	del := &recordingDeleter{err: errors.New("apiserver unreachable")}
	c, timers := newTestCoordinator(now, del.delete, (&recordingRebinder{}).rebind)

	c.Hold("pod-a", podclaim.ReservedHold{UID: "uid-1", ResourceVersion: "100", HoldExpiresAt: now.Add(10 * time.Second)})
	// Fire the timer: the DELETE errors, which must be logged and the hold
	// entry dropped without a panic.
	(*timers)[0].fn()
	if del.count() != 1 {
		t.Fatalf("expiry issued %d deletes, want 1", del.count())
	}
	if c.Holds("pod-a") {
		t.Error("Holds(pod-a) = true after a failed expiry DELETE, want the entry dropped")
	}
}

// realTimerCoordinator wires a coordinator with real (very short) timers and
// recording seams, for the §3.2 concurrency stress test. The DELETE is
// scripted to abort (a cross-replica rebind always wins), mirroring the
// precondition race the stress run exercises.
func realTimerCoordinator(del claimDeleter, reb claimRebinder) *HoldCoordinator {
	return &HoldCoordinator{
		namespace: testNS,
		rebind:    reb,
		delete:    del,
		now:       time.Now,
		afterFunc: func(d time.Duration, fn func()) timerHandle { return time.AfterFunc(d, fn) },
		log:       testLogger(),
		holds:     make(map[string]*activeHold),
	}
}

// TestConcurrentRebindVsExpiryIsRaceClean exercises the §3.2 reserved-hold
// coordinator under concurrent Hold / Rebind / expire across many claims with
// real short-lived timers. It is the tier-7a stress companion to the unit
// tests: it asserts the map-guarded timer state is -race clean and that no
// goroutine panics when a rebind and an expiry timer fire for the same claim
// at nearly the same instant. Run under `go test -race`.
// spec: 3.2 (rebind-vs-hold-expiry precondition race, multi-replica rebind)
func TestConcurrentRebindVsExpiryIsRaceClean(t *testing.T) {
	del := &recordingDeleter{aborted: true} // every expiry loses to a rebind
	reb := &recordingRebinder{}
	c := realTimerCoordinator(del.delete, reb.rebind)
	defer c.Stop()

	const claims = 64
	var wg sync.WaitGroup
	for i := 0; i < claims; i++ {
		podID := "pod-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		wg.Add(1)
		go func(pod string) {
			defer wg.Done()
			// Arm a hold with a near-immediate deadline so the expiry timer
			// fires concurrently with the rebind below.
			c.Hold(pod, podclaim.ReservedHold{
				ResourceVersion: "1",
				HoldExpiresAt:   time.Now().Add(time.Millisecond),
			})
			_ = c.Rebind(context.Background(), pod)
		}(podID)
	}
	wg.Wait()

	// Give any still-pending expiry timers a moment to fire, then assert all
	// entries are drained (every hold was either rebound or expired).
	done := make(chan struct{})
	go func() {
		for {
			c.mu.Lock()
			n := len(c.holds)
			c.mu.Unlock()
			if n == 0 {
				close(done)
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("coordinator did not drain all holds within 5s under concurrent rebind/expiry")
	}
}
