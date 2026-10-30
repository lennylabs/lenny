// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// spec: 4.7.9 (step 4), 6.1 (Adapter SIGTERM behavior during sdk_connecting),
// 28.5.3 (CH-SUPERVISE, Preconditions)
func TestAwaitSupervisorReadyReadEndsOnCancellation_spec_4_7_9(t *testing.T) {
	var accepted atomic.Bool
	uid := func(net.Conn) (uint32, error) { accepted.Store(true); return currentUID(), nil }
	socket := uniqueSocket("await-cancel")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		// The peer connects and never sends ready.
		dialFakeSupervisor(t, socket, "")
		waitFor(t, "the accept", accepted.Load)
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	began := time.Now()
	_, err := AwaitSupervisor(ctx, AwaitSupervisorConfig{
		Socket: socket, Auth: SocketPeerAuth{ExpectedUID: currentUID()}, Lifetime: LifetimeKeep,
		PeerUID: uid, Timeout: time.Minute,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("AwaitSupervisor error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(began); elapsed > 5*time.Second {
		t.Fatalf("AwaitSupervisor returned %s after the cancel, want it to return at once rather than at the wait timeout", elapsed)
	}
}

// spec: 4.7.9 (step 4), 4.7.10 (Runtime supervisor, Launch), 6.1 (Adapter
// SIGTERM behavior during sdk_connecting)
func TestRunPreReadySignalEndsTheWaitAndLaunchesNothing_spec_6_1(t *testing.T) {
	t.Run("a signal after the accept and before ready ends the supervisor wait", func(t *testing.T) {
		h := newPreReadyHarness(t)
		var accepted atomic.Bool
		socket := uniqueSocket("preready-cancel")
		h.cfg.AwaitSupervisor = func(ctx context.Context) error {
			_, err := AwaitSupervisor(ctx, AwaitSupervisorConfig{
				Socket: socket, Auth: SocketPeerAuth{ExpectedUID: currentUID()}, Lifetime: LifetimeKeep,
				PeerUID: func(net.Conn) (uint32, error) { accepted.Store(true); return currentUID(), nil },
				Timeout: time.Minute,
			})
			return err
		}
		warm := false
		h.cfg.WarmUp = func(context.Context) error { warm = true; return nil }
		go func() {
			dialFakeSupervisor(t, socket, "")
			waitFor(t, "the accept", accepted.Load)
			time.Sleep(50 * time.Millisecond)
			h.signals <- syscall.SIGTERM
		}()
		h.run()
		h.expect(0, 1, false)
		if warm {
			t.Fatal("a warm-up launch ran after the signal")
		}
	})
	t.Run("a signal that claims the exit as the gate returns sends no warm-up launch", func(t *testing.T) {
		h := newPreReadyHarness(t)
		h.cfg.AwaitSupervisor = func(context.Context) error {
			h.signals <- syscall.SIGTERM
			waitFor(t, "the signal path", func() bool { h.mu.Lock(); defer h.mu.Unlock(); return h.ends == 1 })
			return nil
		}
		warm := false
		h.cfg.WarmUp = func(context.Context) error { warm = true; return nil }
		h.run()
		h.expect(0, 1, false)
		if warm {
			t.Fatal("the warm-up launch ran after the signal path ran ExitOnSignal")
		}
	})
}

// spec: 4.7.10 (Runtime supervisor, Launch), 6.1 (Adapter SIGTERM behavior
// during sdk_connecting)
func TestSupervisedRuntimeSendsNoLaunchUnderACancelledContext_spec_4_7_10(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(rt *SupervisedRuntime, ctx context.Context) error
	}{
		{"warm-up", func(rt *SupervisedRuntime, ctx context.Context) error { return rt.WarmUp(ctx) }},
		{"start", func(rt *SupervisedRuntime, ctx context.Context) error { return rt.Start(ctx, "a") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSupervisedFixture(t, LifetimeKeep, LaunchService, nil)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			cancel()
			if err := tc.run(f.rt, ctx); err == nil {
				t.Fatal("a launch under a cancelled context succeeded")
			}
			if n := f.sup.count(superviseLaunch); n != 0 {
				t.Fatalf("launches = %d, want none under a cancelled context", n)
			}
			if f.rt.HasLaunched() {
				t.Fatal("HasLaunched reports a launch that was not sent")
			}
		})
	}
}

// spec: 4.7.10 (Runtime supervisor, Launch; Runtime supervisor, Liveness),
// 4.7.9 (step 7)
func TestSupervisedConnectionWaitIsBoundedByTheStartDeadline_spec_4_7_10(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(rt *SupervisedRuntime, ctx context.Context) error
	}{
		{"warm-up timeout", func(rt *SupervisedRuntime, ctx context.Context) error { return rt.WarmUp(ctx) }},
		{"session start deadline", func(rt *SupervisedRuntime, ctx context.Context) error { return rt.Start(ctx, "a") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSupervisedFixture(t, LifetimeKeep, LaunchService, nil)
			// The composed transport's own bound is far shorter than the
			// start's deadline; the connection arrives after that bound and
			// within the deadline.
			f.rt.mu.Lock()
			f.rt.transport.AcceptTimeout = 50 * time.Millisecond
			f.rt.mu.Unlock()
			f.sup.react(superviseLaunch, fakeReaction{noProcess: true})
			go func() {
				waitFor(t, "the launch", func() bool { return f.sup.count(superviseLaunch) == 1 })
				time.Sleep(300 * time.Millisecond)
				f.sup.startProcess()
			}()
			if err := tc.run(f.rt, startCtx(t, 5*time.Second)); err != nil {
				t.Fatalf("the wait ended before the start's deadline: %v", err)
			}
			requireLive(t, f.rt)
		})
	}
}
