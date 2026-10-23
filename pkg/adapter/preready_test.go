// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"
)

// preReadyHarness records what RunPreReady did.
type preReadyHarness struct {
	t       *testing.T
	signals chan os.Signal
	mu      sync.Mutex
	exits   []int
	ends    int
	bound   bool
	served  bool
	stopped bool
	stopCh  chan struct{}
	stopOne sync.Once
	cfg     PreReadyConfig
	observe func(LivenessCause)
}

func newPreReadyHarness(t *testing.T) *preReadyHarness {
	h := &preReadyHarness{t: t, signals: make(chan os.Signal, 1), stopCh: make(chan struct{})}
	h.cfg = PreReadyConfig{
		Signals: h.signals,
		OnSignal: func() {
			h.mu.Lock()
			h.ends++
			h.mu.Unlock()
			h.stop()
		},
		Listen: func() (net.Listener, error) {
			h.mu.Lock()
			h.bound = true
			h.mu.Unlock()
			return &fakeListener{}, nil
		},
		Serve: func(net.Listener) error {
			h.mu.Lock()
			h.served = true
			h.mu.Unlock()
			<-h.stopCh
			return nil
		},
		Stop: h.stop,
		Exit: func(code int) {
			h.mu.Lock()
			h.exits = append(h.exits, code)
			h.mu.Unlock()
		},
		Liveness: func(fn func(LivenessCause)) { h.observe = fn },
		Logf:     func(string, ...any) {},
	}
	return h
}

func (h *preReadyHarness) stop() {
	h.stopOne.Do(func() {
		h.mu.Lock()
		h.stopped = true
		h.mu.Unlock()
		close(h.stopCh)
	})
}

// run runs RunPreReady and waits for it to return.
func (h *preReadyHarness) run() {
	h.t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); RunPreReady(context.Background(), h.cfg) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		h.t.Fatal("RunPreReady did not return")
	}
}

// expect asserts the cell's exit status, the ends sent, and the bind.
func (h *preReadyHarness) expect(code, ends int, bound bool) {
	h.t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.exits) != 1 || h.exits[0] != code {
		h.t.Fatalf("exits = %v, want exactly [%d]", h.exits, code)
	}
	if h.ends != ends {
		h.t.Fatalf("ends = %d, want %d", h.ends, ends)
	}
	if h.bound != bound {
		h.t.Fatalf("bound = %v, want %v", h.bound, bound)
	}
}

type fakeListener struct{ closed bool }

func (l *fakeListener) Accept() (net.Conn, error) { return nil, errors.New("fake") }
func (l *fakeListener) Close() error              { l.closed = true; return nil }
func (l *fakeListener) Addr() net.Addr            { return &net.UnixAddr{} }

// blockUntilCancel is a gate that blocks until its context ends.
func blockUntilCancel(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

// spec: 4.7.9 (step 4), 4.7.10 (Runtime supervisor, Launch; Runtime
// supervisor, Liveness), 6.1 (Adapter SIGTERM behavior during sdk_connecting)
func TestRunPreReadyCells_spec_4_7_10(t *testing.T) {
	t.Run("P1/signal", func(t *testing.T) {
		h := newPreReadyHarness(t)
		h.signals <- syscall.SIGTERM
		h.cfg.AwaitSupervisor = blockUntilCancel
		h.cfg.OnSignal = func() { h.mu.Lock(); h.ends++; h.mu.Unlock() }
		h.run()
		h.expect(0, 1, false)
	})
	t.Run("P2/signal", func(t *testing.T) {
		h := newPreReadyHarness(t)
		h.cfg.AwaitSupervisor = func(ctx context.Context) error {
			h.signals <- syscall.SIGTERM
			return blockUntilCancel(ctx)
		}
		warm := false
		h.cfg.WarmUp = func(context.Context) error { warm = true; return nil }
		h.run()
		h.expect(0, 1, false)
		if warm {
			t.Fatal("a warm-up launch ran after the signal")
		}
	})
	t.Run("P2/failure", func(t *testing.T) {
		h := newPreReadyHarness(t)
		h.cfg.AwaitSupervisor = func(context.Context) error { return errors.New("gating property failed") }
		h.run()
		h.expect(1, 0, false)
	})
	t.Run("P2/timeout", func(t *testing.T) {
		h := newPreReadyHarness(t)
		h.cfg.AwaitSupervisor = func(context.Context) error { return context.DeadlineExceeded }
		h.run()
		h.expect(1, 0, false)
	})
	for _, mode := range []string{"preconnect", "service"} {
		t.Run("P3/signal/"+mode, func(t *testing.T) {
			h := newPreReadyHarness(t)
			h.cfg.ServiceMode = mode == "service"
			h.cfg.AwaitSupervisor = func(context.Context) error { return nil }
			h.cfg.WarmUp = func(ctx context.Context) error {
				h.signals <- syscall.SIGTERM
				return blockUntilCancel(ctx)
			}
			h.run()
			h.expect(0, 1, false)
		})
		t.Run("P4/signal/"+mode, func(t *testing.T) {
			h := newPreReadyHarness(t)
			h.cfg.ServiceMode = mode == "service"
			h.cfg.AwaitSupervisor = func(context.Context) error { return nil }
			h.cfg.WarmUp = func(context.Context) error { return nil }
			lis := &fakeListener{}
			h.cfg.Listen = func() (net.Listener, error) {
				h.signals <- syscall.SIGTERM
				waitFor(t, "the signal path", func() bool { h.mu.Lock(); defer h.mu.Unlock(); return h.ends == 1 })
				h.mu.Lock()
				h.bound = true
				h.mu.Unlock()
				return lis, nil
			}
			h.run()
			h.expect(0, 1, true)
			if h.served || !lis.closed {
				t.Fatalf("served = %v, listener closed = %v; want not served and closed", h.served, lis.closed)
			}
		})
	}
	t.Run("P3/failure", func(t *testing.T) {
		h := newPreReadyHarness(t)
		h.cfg.AwaitSupervisor = func(context.Context) error { return nil }
		h.cfg.WarmUp = func(context.Context) error { return errLaunchRefused }
		h.run()
		h.expect(1, 0, false)
	})
	t.Run("P3/timeout", func(t *testing.T) {
		h := newPreReadyHarness(t)
		h.cfg.AwaitSupervisor = func(context.Context) error { return nil }
		h.cfg.WarmUp = func(context.Context) error { return context.DeadlineExceeded }
		h.run()
		h.expect(1, 0, false)
	})
	t.Run("P4/failure", func(t *testing.T) {
		h := newPreReadyHarness(t)
		h.cfg.AwaitSupervisor = func(context.Context) error { return nil }
		h.cfg.WarmUp = func(context.Context) error { return nil }
		h.cfg.Listen = func() (net.Listener, error) {
			h.observe(CauseRuntimeExit)
			time.Sleep(20 * time.Millisecond)
			return &fakeListener{}, nil
		}
		h.run()
		h.expect(1, 0, false)
	})
	t.Run("P5/signal/before Serve", func(t *testing.T) {
		h := newPreReadyHarness(t)
		h.cfg.Serve = func(net.Listener) error { return errors.New("server stopped") }
		listen := h.cfg.Listen
		h.cfg.Listen = func() (net.Listener, error) {
			l, err := listen()
			h.signals <- syscall.SIGTERM
			waitFor(t, "the signal path", func() bool { h.mu.Lock(); defer h.mu.Unlock(); return h.ends == 1 })
			return l, err
		}
		h.run()
		h.expect(0, 1, true)
	})
	t.Run("P5/signal/while serving", func(t *testing.T) {
		h := newPreReadyHarness(t)
		serve := h.cfg.Serve
		h.cfg.Serve = func(l net.Listener) error {
			h.signals <- syscall.SIGTERM
			return serve(l)
		}
		h.run()
		h.expect(0, 1, true)
	})
	t.Run("P5/failure/listen error", func(t *testing.T) {
		h := newPreReadyHarness(t)
		h.cfg.Listen = func() (net.Listener, error) { return nil, errors.New("address in use") }
		h.run()
		h.expect(1, 0, false)
	})
	t.Run("P5/failure/serve error", func(t *testing.T) {
		h := newPreReadyHarness(t)
		h.cfg.Serve = func(net.Listener) error { return errors.New("serve failed") }
		h.run()
		h.expect(1, 0, true)
	})
	t.Run("P5/failure/cause between bind and Serve", func(t *testing.T) {
		h := newPreReadyHarness(t)
		listen := h.cfg.Listen
		h.cfg.Listen = func() (net.Listener, error) {
			l, err := listen()
			go h.observe(CauseLinkFailure)
			return l, err
		}
		h.cfg.ServiceMode = true
		h.run()
		h.mu.Lock()
		defer h.mu.Unlock()
		if len(h.exits) != 1 || h.exits[0] != 1 {
			t.Fatalf("exits = %v, want exactly one exit 1", h.exits)
		}
	})
	t.Run("after READY/session pod cause keeps running", func(t *testing.T) {
		h := newPreReadyHarness(t)
		serve := h.cfg.Serve
		h.cfg.Serve = func(l net.Listener) error {
			h.observe(CauseRuntimeExit)
			time.Sleep(30 * time.Millisecond)
			h.mu.Lock()
			exited := len(h.exits)
			h.mu.Unlock()
			if exited != 0 {
				t.Error("a session-mode pod exited on a cause after READY")
			}
			h.signals <- syscall.SIGTERM
			return serve(l)
		}
		h.run()
		h.expect(0, 1, true)
	})
	t.Run("after READY/service pod cause exits 1", func(t *testing.T) {
		h := newPreReadyHarness(t)
		h.cfg.ServiceMode = true
		serve := h.cfg.Serve
		h.cfg.Serve = func(l net.Listener) error {
			h.observe(CauseRuntimeExit)
			return serve(l)
		}
		h.run()
		h.expect(1, 0, true)
		if !h.stopped {
			t.Fatal("the server was not stopped")
		}
	})
}
