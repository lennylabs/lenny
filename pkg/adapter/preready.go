// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"log"
	"net"
	"os"
	"sync"
	"sync/atomic"
)

// This file holds RunPreReady, the adapter's startup order from the CH-SUPERVISE
// gate to READY and its exits. The paths that can end the adapter (a gate's
// failure or timeout, a listen or serve error, a SIGTERM or SIGINT, and on a
// service-mode pod a not-live cause after READY) race for one exit: the first
// path to claim ownership exits with its status, and every other path waits
// for the owner's exit instead of exiting itself. A path waits only for an
// event the owning path produces, and only after ownership is decided, so
// every wait ends.
//
// spec: §4.7.9 (step 4); §4.7.10 (Runtime supervisor, Launch; Runtime
// supervisor, Liveness); §6.1 (Adapter SIGTERM behavior during
// sdk_connecting).

// PreReadyConfig injects RunPreReady's dependencies.
type PreReadyConfig struct {
	// Signals delivers SIGTERM and SIGINT. main calls signal.Notify before
	// it builds the server, so a signal that arrives earlier waits here.
	Signals <-chan os.Signal
	// AwaitSupervisor is the CH-SUPERVISE gate; nil on a pod that runs no
	// supervisor.
	AwaitSupervisor func(ctx context.Context) error
	// WarmUp is the warm-up launch and its CH-MSGSOCK connection; nil when
	// the launch mode names no warm-up launch.
	WarmUp func(ctx context.Context) error
	// OnSignal is ExitOnSignal: it ends any launched process through the
	// launch mode's branch and stops the server gracefully.
	OnSignal func()
	// Listen binds the gRPC listener; the bind marks READY.
	Listen func() (net.Listener, error)
	// Serve serves on the listener until the server stops.
	Serve func(net.Listener) error
	// Stop stops the server at once, for an exit after the bind that a
	// signal did not cause.
	Stop func()
	// Exit ends the process with a status. The owning path calls it once.
	Exit func(code int)
	// Liveness registers the observer of the not-live state; nil on a pod
	// that runs no supervisor.
	Liveness func(observer func(LivenessCause))
	// ServiceMode records a service-mode pod, on which a not-live cause after
	// READY also exits the adapter.
	ServiceMode bool
	// Logf logs; nil logs through the standard logger.
	Logf func(format string, args ...any)
}

// preReady is the state of one RunPreReady.
type preReady struct {
	cfg    PreReadyConfig
	cancel context.CancelFunc
	owned  atomic.Bool
	ready  atomic.Bool
	bound  atomic.Bool
	done   chan struct{}
	once   sync.Once
}

// RunPreReady runs the gates, binds the listener, and serves, and returns
// once the path that owns the exit has called cfg.Exit.
func RunPreReady(ctx context.Context, cfg PreReadyConfig) {
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	gctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p := &preReady{cfg: cfg, cancel: cancel, done: make(chan struct{})}
	if cfg.Liveness != nil {
		cfg.Liveness(p.onCause)
	}
	go p.handleSignals()
	p.run(gctx)
	<-p.done
}

// claim takes ownership of the exit and reports whether this caller won.
func (p *preReady) claim() bool {
	return p.owned.CompareAndSwap(false, true)
}

// finish exits with code and releases every waiting path.
func (p *preReady) finish(code int) {
	p.once.Do(func() {
		p.cfg.Exit(code)
		close(p.done)
	})
}

// handleSignals is the signal path: it claims the exit, cancels the gates,
// runs ExitOnSignal, and exits 0 after the end completes.
func (p *preReady) handleSignals() {
	select {
	case <-p.cfg.Signals:
	case <-p.done:
		return
	}
	if !p.claim() {
		return
	}
	p.cancel()
	if p.cfg.OnSignal != nil {
		p.cfg.OnSignal()
	}
	p.finish(0)
}

// onCause observes a not-live cause. Before READY, and on a service-mode pod
// at any time, the cause exits the adapter with status 1. It must not block.
func (p *preReady) onCause(cause LivenessCause) {
	if p.ready.Load() && !p.cfg.ServiceMode {
		return
	}
	go p.fail("runtime not live: " + string(cause))
}

// fail is a path that ends the adapter with status 1 when it owns the exit.
func (p *preReady) fail(reason string) {
	if !p.claim() {
		return
	}
	p.cancel()
	p.cfg.Logf("lenny-adapter: exiting: %s", reason)
	if p.bound.Load() && p.cfg.Stop != nil {
		p.cfg.Stop()
	}
	p.finish(1)
}

// run is the main path: the gates, the bind, and the serve.
func (p *preReady) run(ctx context.Context) {
	if p.cfg.AwaitSupervisor != nil {
		if err := p.cfg.AwaitSupervisor(ctx); err != nil {
			p.fail("supervisor gate: " + err.Error())
			return
		}
	}
	// A signal that claimed the exit while the gate was returning has
	// already decided what ExitOnSignal ends, so the warm-up launch is not
	// sent. spec: §6.1 (Adapter SIGTERM behavior during sdk_connecting).
	if p.owned.Load() {
		return
	}
	if err := ctx.Err(); err != nil {
		p.fail("supervisor gate: " + err.Error())
		return
	}
	if p.cfg.WarmUp != nil {
		if err := p.cfg.WarmUp(ctx); err != nil {
			p.fail("warm-up launch: " + err.Error())
			return
		}
	}
	if p.owned.Load() {
		return
	}
	lis, err := p.cfg.Listen()
	if err != nil {
		p.fail("listen: " + err.Error())
		return
	}
	p.bound.Store(true)
	p.ready.Store(true)
	if p.owned.Load() {
		_ = lis.Close()
		return
	}
	p.serve(lis)
}

// serve runs Serve. A Serve error the exit's owner did not cause exits 1; a
// Serve that returns because the owner stopped the server leaves the exit to
// the owner.
func (p *preReady) serve(lis net.Listener) {
	err := p.cfg.Serve(lis)
	if p.owned.Load() {
		return
	}
	if err != nil {
		p.fail("serve: " + err.Error())
		return
	}
	if p.claim() {
		p.finish(0)
	}
}
