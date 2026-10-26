// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// shutdownLog records the order of shutdown calls.
type shutdownLog struct {
	mu     sync.Mutex
	events []string
}

func (l *shutdownLog) add(e string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *shutdownLog) list() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

// fakeListener records its Shutdown and signals stopped once.
type fakeListener struct {
	log     *shutdownLog
	stopped chan struct{}
	once    sync.Once
}

func (f *fakeListener) Shutdown(context.Context) error {
	f.log.add("http")
	if f.stopped != nil {
		f.once.Do(func() { close(f.stopped) })
	}
	return nil
}

// fakeTerminalWork records the wait and Close, and lets a test choose how
// long the wait lasts and whether it ends at its bound.
type fakeTerminalWork struct {
	log       *shutdownLog
	budget    time.Duration
	hold      time.Duration
	untilDone bool
	mu        sync.Mutex
	deadline  time.Duration
}

func (f *fakeTerminalWork) WaitTerminalWork(ctx context.Context) error {
	f.log.add("wait")
	if dl, ok := ctx.Deadline(); ok {
		f.mu.Lock()
		f.deadline = time.Until(dl)
		f.mu.Unlock()
	}
	if f.untilDone {
		<-ctx.Done()
		return ctx.Err()
	}
	time.Sleep(f.hold)
	return nil
}

func (f *fakeTerminalWork) TerminalPipelineBudget() time.Duration { return f.budget }
func (f *fakeTerminalWork) OpenTerminalWork() int                 { return 1 }
func (f *fakeTerminalWork) Close()                                { f.log.add("close") }

// recordingSteps returns final-stage steps that log their names and whether
// their context was live when they ran.
func recordingSteps(log *shutdownLog, names ...string) []shutdownStep {
	steps := make([]shutdownStep, 0, len(names))
	for _, n := range names {
		steps = append(steps, shutdownStep{name: n, run: func(ctx context.Context) {
			if ctx.Err() != nil {
				log.add(n + ":expired")
				return
			}
			log.add(n)
		}})
	}
	return steps
}

// TestShutdownWaitsOnTerminalWorkBeforeCloseAndFlushesSpansAfter_spec_10_1_7
// asserts the gateway's post-signal shutdown order: the HTTP listener stops,
// the wait on the terminal claim record runs on a context bounded by the
// terminal pipeline budget, the session server's lifetime ends, and only
// then do the final closes run, starting with the span flush, on a fresh
// shutdown-timeout context that a wait longer than that timeout leaves live.
// A wait that ends at its bound still closes the server and runs every
// final close. With no session server, the wait and Close are skipped.
//
// spec: 10.1.7 (preStop Hook Drain), 10.1.7 (Terminal pipelines at process
// exit.), 29.7
func TestShutdownWaitsOnTerminalWorkBeforeCloseAndFlushesSpansAfter_spec_10_1_7(t *testing.T) {
	finals := []string{"trace", "llm-proxy", "gateway-control", "postgres", "sqlite", "redis", "openfeature"}
	t.Run("drained wait", func(t *testing.T) {
		log := &shutdownLog{}
		sessions := &fakeTerminalWork{log: log, budget: 5 * time.Second, hold: 150 * time.Millisecond}
		shutdownSequence{
			timeout:  100 * time.Millisecond,
			http:     &fakeListener{log: log},
			sessions: sessions,
			closers:  recordingSteps(log, finals...),
		}.run()
		want := append([]string{"http", "wait", "close"}, finals...)
		if got := log.list(); !reflect.DeepEqual(got, want) {
			t.Fatalf("shutdown order = %v, want %v", got, want)
		}
		sessions.mu.Lock()
		dl := sessions.deadline
		sessions.mu.Unlock()
		if dl <= 4*time.Second || dl > 5*time.Second {
			t.Errorf("wait deadline %v from its start, want the terminal pipeline budget (5s)", dl)
		}
	})
	t.Run("wait ended at its bound", func(t *testing.T) {
		log := &shutdownLog{}
		shutdownSequence{
			timeout:  time.Second,
			http:     &fakeListener{log: log},
			sessions: &fakeTerminalWork{log: log, budget: 100 * time.Millisecond, untilDone: true},
			closers:  recordingSteps(log, finals...),
		}.run()
		want := append([]string{"http", "wait", "close"}, finals...)
		if got := log.list(); !reflect.DeepEqual(got, want) {
			t.Fatalf("shutdown order = %v, want %v", got, want)
		}
	})
	t.Run("no session server", func(t *testing.T) {
		log := &shutdownLog{}
		shutdownSequence{
			timeout: time.Second,
			http:    &fakeListener{log: log},
			closers: recordingSteps(log, finals...),
		}.run()
		want := append([]string{"http"}, finals...)
		if got := log.list(); !reflect.DeepEqual(got, want) {
			t.Fatalf("shutdown order = %v, want %v", got, want)
		}
	})
	t.Run("wiring keeps the final closes in order", func(t *testing.T) {
		w := &gatewayWiring{}
		q := w.shutdownSequence(time.Second)
		if q.sessions != nil {
			t.Error("a wiring with no session server yields a non-nil session collaborator")
		}
		names := make([]string, 0, len(q.closers))
		for _, s := range q.closers {
			names = append(names, s.name)
		}
		if !reflect.DeepEqual(names, finals) {
			t.Errorf("final closes = %v, want %v with the span flush first", names, finals)
		}
	})
}

// releaseRecorder is an executor whose Release records the disposition and
// whether the release context was live.
type releaseRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *releaseRecorder) Send(context.Context, string, []executor.Message) (executor.Response, error) {
	return executor.Response{}, errors.New("not used")
}
func (r *releaseRecorder) Close(context.Context, string) error { return nil }
func (r *releaseRecorder) Release(ctx context.Context, _ string, d executor.Disposition) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ctx.Err() != nil {
		r.calls = append(r.calls, string(d)+":cancelled")
		return ctx.Err()
	}
	r.calls = append(r.calls, string(d))
	return nil
}

func (r *releaseRecorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// gatedSealer blocks its Seal until open is closed.
type gatedSealer struct{ open chan struct{} }

func (g *gatedSealer) Seal(ctx context.Context, _, _ string) error {
	select {
	case <-g.open:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TestShutdownLetsAnOpenTerminalPipelineReleaseItsPod_spec_10_1_7 drives the
// shutdown sequence over a real session server whose post-terminal hook is
// still sealing when the signal arrives. The seal finishes during the wait,
// the hook acquires the slot-accounting lock on the server-lifetime context
// and releases the pod with the session's disposition on a live context,
// and the trace flush runs only after the hook's claim closed and Close ran.
// A shutdown that closed the server first ends that lock wait and skips the
// release.
//
// spec: 10.1.7 (Terminal pipelines at process exit.), 7.1 (Terminal
// pipeline context), 29.7
func TestShutdownLetsAnOpenTerminalPipelineReleaseItsPod_spec_10_1_7(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	if err := store.Create(ctx, sessionstore.Session{ID: "s_1", TenantID: "acme", State: session.StateRunning}); err != nil {
		t.Fatalf("create: %v", err)
	}
	row, err := store.Update(ctx, "acme", "s_1", func(r *sessionstore.Session) error {
		r.State = session.StateCompleted
		r.TerminatedAt = time.Now()
		return nil
	})
	if err != nil {
		t.Fatalf("terminal write: %v", err)
	}
	sealer := &gatedSealer{open: make(chan struct{})}
	exec := &releaseRecorder{}
	srv := sessionserver.New(store, sessionserver.Options{
		Sealer:                     sealer,
		Executor:                   exec,
		WorkspaceSealMaxDuration:   10 * time.Second,
		StreamFailureReportTimeout: 5 * time.Second,
	})
	defer srv.Close()

	end := srv.BeginTerminal("s_1")
	hookDone := make(chan struct{})
	go func() {
		defer close(hookDone)
		defer end()
		srv.OnSessionTerminal(ctx, session.StateRunning, row)
	}()

	log := &shutdownLog{}
	listener := &fakeListener{log: log, stopped: make(chan struct{})}
	go func() {
		<-listener.stopped
		time.Sleep(150 * time.Millisecond)
		close(sealer.open)
	}()
	steps := []shutdownStep{{name: "trace", run: func(context.Context) {
		select {
		case <-hookDone:
			log.add("trace")
		default:
			log.add("trace:before-hook-end")
		}
	}}}
	shutdownSequence{timeout: time.Second, http: listener, sessions: closeRecorder{srv, log}, closers: steps}.run()

	if got, want := log.list(), []string{"http", "close", "trace"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("shutdown events = %v, want %v", got, want)
	}
	if got := exec.list(); len(got) != 1 || got[0] != string(executor.DispositionCompleted) {
		t.Errorf("pod releases = %v, want one %q release on a live context", got, executor.DispositionCompleted)
	}
	if n := srv.OpenTerminalWork(); n != 0 {
		t.Errorf("open terminal work after shutdown = %d, want 0", n)
	}
}

// closeRecorder wraps the real session server and logs its Close.
type closeRecorder struct {
	*sessionserver.Server
	log *shutdownLog
}

func (c closeRecorder) Close() {
	c.log.add("close")
	c.Server.Close()
}
