// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/lennylabs/lenny/pkg/adapter"
)

var socketSeq atomic.Uint64

// abstractSocket returns an abstract Unix socket name unique to this test
// process, so concurrent packages never collide on a bind.
func abstractSocket(t *testing.T, stem string) string {
	t.Helper()
	return fmt.Sprintf("@lenny-adapter-test-%s-%d-%d", stem, os.Getpid(), socketSeq.Add(1))
}

func noNonce() string { return "" }

// parsedSuperviseFlags is a supervised flag set after a parse.
type parsedSuperviseFlags struct {
	set   *flag.FlagSet
	flags *superviseFlags
}

// newSuperviseFlagSet parses args through the adapter's supervised flag set.
func newSuperviseFlagSet(t *testing.T, args ...string) parsedSuperviseFlags {
	t.Helper()
	fs := flag.NewFlagSet("lenny-adapter", flag.ContinueOnError)
	f := registerSuperviseFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return parsedSuperviseFlags{set: fs, flags: f}
}

// supervisedInputs returns validated supervised inputs for mode on a socket
// unique to the test, with a short supervisor wait.
func supervisedInputs(t *testing.T, mode adapter.LaunchMode) superviseInputs {
	t.Helper()
	return superviseInputs{
		Socket: abstractSocket(t, "supervise"), Lifetime: adapter.LifetimeKeep, Mode: mode,
		PingInterval: time.Second, PingTimeout: time.Second,
		WaitTimeout: 50 * time.Millisecond, WarmUpTimeout: time.Second,
	}
}

// newTestSupervisedRuntime composes a supervised runtime for in and installs
// it on a fresh server, closing its listener at test end.
func newTestSupervisedRuntime(t *testing.T, in superviseInputs) (*adapter.Server, *adapter.SupervisedRuntime) {
	t.Helper()
	srv := adapter.New("test")
	rt, err := newSupervisedRuntime(in, abstractSocket(t, "runtime"), adapter.SocketPeerAuth{NonceOnly: true}, noNonce, nil)
	if err != nil {
		t.Fatalf("newSupervisedRuntime: %v", err)
	}
	t.Cleanup(func() { _ = rt.CloseListener() })
	srv.UseSupervisedRuntime(rt)
	return srv, rt
}

// spec: 4.7.10 (Runtime supervisor), 28.5.3 (CH-SUPERVISE, Endpoint)
func TestSuperviseSocketRequiresTheRuntimeSocket_spec_4_7_10(t *testing.T) {
	parsed := newSuperviseFlagSet(t, withFlags()...)
	if _, err := parseSuperviseInputs(parsed.set, parsed.flags, ""); err == nil {
		t.Fatal("--supervise-socket without --runtime-socket was accepted")
	}
	in, err := parseSuperviseInputs(parsed.set, parsed.flags, "@lenny-runtime")
	if err != nil || !in.Supervised() {
		t.Fatalf("with --runtime-socket = %+v, %v; want a supervised pod", in, err)
	}
	unsupervised := newSuperviseFlagSet(t)
	if in, err := parseSuperviseInputs(unsupervised.set, unsupervised.flags, ""); err != nil || in.Supervised() {
		t.Fatalf("no supervised flags and no runtime socket = %+v, %v; want unsupervised", in, err)
	}
}

// spec: 15.4.6, 4.7.10 (Runtime supervisor)
func TestAttachLifecycleBindsOnlyAConfiguredSocket_spec_15_4_6(t *testing.T) {
	t.Run("empty socket binds no CH-RUNTIMEOPS listener", func(t *testing.T) {
		srv := adapter.New("test")
		ops, err := attachLifecycle(srv, "", adapter.SocketPeerAuth{NonceOnly: true}, noNonce)
		if err != nil || ops != nil || srv.Lifecycle != nil {
			t.Fatalf("= %v, %v, server lifecycle %v; want nothing bound", ops, err, srv.Lifecycle)
		}
	})
	t.Run("configured socket binds and installs CH-RUNTIMEOPS", func(t *testing.T) {
		srv := adapter.New("test")
		socket := abstractSocket(t, "lifecycle")
		ops, err := attachLifecycle(srv, socket, adapter.SocketPeerAuth{NonceOnly: true}, noNonce)
		if err != nil {
			t.Fatalf("attachLifecycle: %v", err)
		}
		t.Cleanup(func() { _ = ops.Close() })
		if srv.Lifecycle != ops || ops.SocketPath() != socket {
			t.Fatalf("server lifecycle %v, socket %q; want %v on %q", srv.Lifecycle, ops.SocketPath(), ops, socket)
		}
	})
	t.Run("a socket already bound is refused", func(t *testing.T) {
		socket := abstractSocket(t, "lifecycle")
		held, err := net.Listen("unix", socket)
		if err != nil {
			t.Fatalf("hold %s: %v", socket, err)
		}
		t.Cleanup(func() { _ = held.Close() })
		srv := adapter.New("test")
		if ops, err := attachLifecycle(srv, socket, adapter.SocketPeerAuth{NonceOnly: true}, noNonce); err == nil || ops != nil || srv.Lifecycle != nil {
			t.Fatalf("= %v, %v; want the bind refused and nothing installed", ops, err)
		}
	})
}

// spec: 4.7.10 (Runtime supervisor)
func TestNewSupervisedRuntimeBindsTheRuntimeSocket_spec_4_7_10(t *testing.T) {
	in := supervisedInputs(t, adapter.LaunchSession)
	socket := abstractSocket(t, "runtime")
	rt, err := newSupervisedRuntime(in, socket, adapter.SocketPeerAuth{NonceOnly: true}, noNonce, nil)
	if err != nil {
		t.Fatalf("newSupervisedRuntime: %v", err)
	}
	t.Cleanup(func() { _ = rt.CloseListener() })
	if rt.Lifetime() != in.Lifetime || rt.Mode() != in.Mode {
		t.Fatalf("lifetime %s, mode %s; want %s, %s", rt.Lifetime(), rt.Mode(), in.Lifetime, in.Mode)
	}
	if _, err := net.Listen("unix", socket); err == nil {
		t.Fatalf("the runtime socket %s was not bound before READY", socket)
	}
	if _, err := newSupervisedRuntime(in, socket, adapter.SocketPeerAuth{NonceOnly: true}, noNonce, nil); err == nil {
		t.Fatal("a second runtime bound the socket the first holds")
	}
}

// spec: 4.7.9 (step 4), 4.7.10 (Runtime supervisor, Launch), 6.1 (Adapter
// SIGTERM behavior during sdk_connecting)
func TestPreReadyConfigWiresTheLaunchMode_spec_4_7_10(t *testing.T) {
	signals := make(chan os.Signal, 1)
	t.Run("unsupervised pod has no gate and no warm-up launch", func(t *testing.T) {
		srv := adapter.New("test")
		cfg := preReadyConfig(srv, grpc.NewServer(), superviseInputs{}, adapter.SocketPeerAuth{}, "127.0.0.1:0", signals, context.Background())
		if cfg.AwaitSupervisor != nil || cfg.WarmUp != nil || cfg.Liveness != nil || cfg.ServiceMode {
			t.Fatalf("unsupervised config carries supervised wiring: %+v", cfg)
		}
		if cfg.Signals == nil || cfg.OnSignal == nil || cfg.Serve == nil || cfg.Stop == nil || cfg.Exit == nil {
			t.Fatalf("unsupervised config is missing its base wiring: %+v", cfg)
		}
		lis, err := cfg.Listen()
		if err != nil {
			t.Fatalf("Listen: %v", err)
		}
		_ = lis.Close()
		cfg.Exit(0)
		// The signal path on an unsupervised pod stops the server and ends no
		// process.
		cfg.OnSignal()
	})
	for _, tc := range []struct {
		mode        adapter.LaunchMode
		wantWarmUp  bool
		wantService bool
	}{
		{adapter.LaunchSession, false, false},
		{adapter.LaunchService, true, true},
		{adapter.LaunchPreconnectSDKWarm, true, false},
		{adapter.LaunchPreconnectPodWarm, false, false},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			in := supervisedInputs(t, tc.mode)
			srv, _ := newTestSupervisedRuntime(t, in)
			cfg := preReadyConfig(srv, grpc.NewServer(), in, adapter.SocketPeerAuth{NonceOnly: true}, "127.0.0.1:0", signals, context.Background())
			if cfg.AwaitSupervisor == nil || cfg.Liveness == nil {
				t.Fatalf("supervised config lacks the gate or the liveness observer: %+v", cfg)
			}
			if (cfg.WarmUp != nil) != tc.wantWarmUp || cfg.ServiceMode != tc.wantService {
				t.Fatalf("warm-up %t, service %t; want %t, %t", cfg.WarmUp != nil, cfg.ServiceMode, tc.wantWarmUp, tc.wantService)
			}
			// No supervisor dials the socket, so the gate ends at its wait
			// timeout with an error rather than attaching a link.
			if err := cfg.AwaitSupervisor(context.Background()); err == nil {
				t.Fatal("the supervisor gate passed with no supervisor connected")
			}
		})
	}
}

// spec: 4.7.10 (Runtime supervisor, Launch)
func TestBoundedWarmUpHoldsTheLaunchToTheWarmUpTimeout_spec_4_7_10(t *testing.T) {
	want := errors.New("warm-up failed")
	var deadline time.Time
	var hasDeadline bool
	start := time.Now()
	err := boundedWarmUp(time.Minute, func(ctx context.Context) error {
		deadline, hasDeadline = ctx.Deadline()
		return want
	})(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want the warm-up launch's error", err)
	}
	if !hasDeadline || deadline.Before(start) || deadline.After(start.Add(time.Minute+time.Second)) {
		t.Fatalf("deadline = %v (set %t); want within --warm-up-timeout of the start", deadline, hasDeadline)
	}
	err = boundedWarmUp(10*time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("an overrunning warm-up launch ended with %v, want context.DeadlineExceeded", err)
	}
}
