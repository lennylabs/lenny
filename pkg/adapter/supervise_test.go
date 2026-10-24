// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/supervisorselftest"
)

// awaitWith runs AwaitSupervisor on a fresh socket while dial runs against
// it, and returns AwaitSupervisor's result.
func awaitWith(t *testing.T, cfg AwaitSupervisorConfig, dial func(socket string)) (*SupervisorLink, error) {
	t.Helper()
	cfg.Socket = uniqueSocket("await")
	if cfg.Timeout == 0 {
		cfg.Timeout = 2 * time.Second
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if dial != nil {
			dial(cfg.Socket)
		}
	}()
	l, err := AwaitSupervisor(context.Background(), cfg)
	<-done
	if l != nil {
		t.Cleanup(func() { _ = l.Close() })
	}
	return l, err
}

// spec: 4.7.11 (Runtime supervisor, Property self-test), 28.5.3 (CH-SUPERVISE)
func TestAwaitSupervisorAppliesThePropertyGateOnRestartPodsOnly_spec_4_7_11(t *testing.T) {
	for _, prop := range supervisorselftest.Properties {
		for _, lifetime := range []RuntimeLifetime{LifetimeKeep, LifetimeRestart} {
			t.Run(prop+"/"+string(lifetime), func(t *testing.T) {
				auth := SocketPeerAuth{ExpectedUID: currentUID()}
				l, err := awaitWith(t, AwaitSupervisorConfig{Auth: auth, Lifetime: lifetime}, func(s string) {
					dialFakeSupervisor(t, s, readyLine(prop))
				})
				wantRefused := lifetime == LifetimeRestart && supervisorselftest.Gates(prop)
				if wantRefused != (err != nil) {
					t.Fatalf("AwaitSupervisor(failed %s, %s) error = %v, want refused %v", prop, lifetime, err, wantRefused)
				}
				if err == nil && l.Properties()[prop] {
					t.Fatalf("link reports %s passing", prop)
				}
			})
		}
	}
}

// spec: 4.7.9 (step 4), 28.5.3 (CH-SUPERVISE, Degradation)
func TestAwaitSupervisorRefusesAMissingLateOrMalformedReady_spec_28_5_3(t *testing.T) {
	auth := SocketPeerAuth{ExpectedUID: currentUID()}
	cases := map[string]func(t *testing.T, socket string){
		"no connection": nil,
		"connection with no ready": func(t *testing.T, s string) {
			dialFakeSupervisor(t, s, "")
		},
		"first message is not ready": func(t *testing.T, s string) {
			dialFakeSupervisor(t, s, `{"type":"pong","gen":0,"id":1}`)
		},
		"malformed ready": func(t *testing.T, s string) {
			dialFakeSupervisor(t, s, `{"type":"ready","gen":0}`)
		},
	}
	for _, lifetime := range []RuntimeLifetime{LifetimeKeep, LifetimeRestart} {
		for name, dial := range cases {
			t.Run(name+"/"+string(lifetime), func(t *testing.T) {
				var fn func(string)
				if dial != nil {
					fn = func(s string) { dial(t, s) }
				}
				_, err := awaitWith(t, AwaitSupervisorConfig{Auth: auth, Lifetime: lifetime, Timeout: 300 * time.Millisecond}, fn)
				if err == nil {
					t.Fatal("AwaitSupervisor succeeded, want an error")
				}
			})
		}
	}
}

// spec: 28.5.3 (CH-SUPERVISE, Endpoint; CH-SUPERVISE, Exclusivity), 4.7.11
// (Separate UIDs and connection authentication)
func TestAwaitSupervisorAcceptsOnlyTheAgentUIDOnceAndClosesItsListener_spec_28_5_3(t *testing.T) {
	t.Run("a foreign peer is refused and the next agent peer accepted", func(t *testing.T) {
		var calls atomic.Int32
		uid := func(net.Conn) (uint32, error) {
			if calls.Add(1) == 1 {
				return 4242, nil
			}
			return 1001, nil
		}
		l, err := awaitWith(t, AwaitSupervisorConfig{Auth: SocketPeerAuth{ExpectedUID: 1001}, Lifetime: LifetimeKeep, PeerUID: uid}, func(s string) {
			foreign := dialUntil(t, s)
			defer func() { _ = foreign.Close() }()
			dialFakeSupervisor(t, s, readyLine())
		})
		if err != nil || l == nil {
			t.Fatalf("AwaitSupervisor = %v", err)
		}
		if calls.Load() != 2 {
			t.Fatalf("peer check ran %d times, want 2", calls.Load())
		}
	})
	for _, mode := range []struct {
		name string
		auth SocketPeerAuth
	}{{"agent-UID check", SocketPeerAuth{ExpectedUID: currentUID()}}, {"nonce-only", SocketPeerAuth{NonceOnly: true, ExpectedUID: 4242}}} {
		t.Run("a dial after the accepted connection is refused/"+mode.name, func(t *testing.T) {
			var socket string
			_, err := awaitWith(t, AwaitSupervisorConfig{Auth: mode.auth, Lifetime: LifetimeKeep}, func(s string) {
				socket = s
				dialFakeSupervisor(t, s, readyLine())
			})
			if err != nil {
				t.Fatal(err)
			}
			if c, err := net.Dial("unix", socket); err == nil {
				_ = c.Close()
				t.Fatal("a second dial connected; the listener must be closed after the one accept")
			}
		})
	}
	t.Run("nonce-only makes no UID check", func(t *testing.T) {
		var calls atomic.Int32
		uid := func(net.Conn) (uint32, error) { calls.Add(1); return 4242, nil }
		_, err := awaitWith(t, AwaitSupervisorConfig{Auth: SocketPeerAuth{NonceOnly: true, ExpectedUID: 1}, Lifetime: LifetimeKeep, PeerUID: uid}, func(s string) {
			dialFakeSupervisor(t, s, readyLine())
		})
		if err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 0 {
			t.Fatal("nonce-only mode ran the UID check")
		}
	})
}

// linkPair returns a link over one end of a socket pair after a ready, and
// the peer end.
func linkPair(t *testing.T) (*SupervisorLink, *fakeSupervisor) {
	t.Helper()
	var sup *fakeSupervisor
	l, err := awaitWith(t, AwaitSupervisorConfig{Auth: SocketPeerAuth{ExpectedUID: currentUID()}, Lifetime: LifetimeKeep}, func(s string) {
		sup = dialFakeSupervisor(t, s, readyLine())
	})
	if err != nil {
		t.Fatal(err)
	}
	return l, sup
}

// spec: 28.5.3 (CH-SUPERVISE, Degradation; CH-SUPERVISE, Timing)
func TestSupervisorLinkFailsOnEveryLinkFailureAndThenSendsNothing_spec_28_5_3(t *testing.T) {
	cases := map[string]func(sup *fakeSupervisor){
		"unanswered request": func(sup *fakeSupervisor) { sup.react(supervisePing, fakeReaction{silent: true}) },
		"closed connection":  func(sup *fakeSupervisor) { sup.react(supervisePing, fakeReaction{closeConn: true}) },
		"reply with another id": func(sup *fakeSupervisor) {
			sup.react(supervisePing, fakeReaction{raw: `{"type":"pong","gen":0,"id":99}`})
		},
		"reply of another type": func(sup *fakeSupervisor) {
			sup.react(supervisePing, fakeReaction{raw: `{"type":"ended","gen":0,"id":1}`})
		},
		"second ready": func(sup *fakeSupervisor) { sup.react(supervisePing, fakeReaction{raw: readyLine()}) },
		"exited of another generation": func(sup *fakeSupervisor) {
			sup.react(supervisePing, fakeReaction{raw: `{"type":"exited","gen":7,"status":0}`})
		},
		"undecodable line": func(sup *fakeSupervisor) { sup.react(supervisePing, fakeReaction{raw: `{"type":"pong"`}) },
		"over-long line": func(sup *fakeSupervisor) {
			sup.react(supervisePing, fakeReaction{raw: `{"type":"pong","gen":0,"id":1,"x":"` + strings.Repeat("a", superviseMaxLine) + `"}`})
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			l, sup := linkPair(t)
			var failures atomic.Int32
			l.setHandlers(func(error) { failures.Add(1) }, nil)
			setup(sup)
			sup.serve()
			_, err := l.request(context.Background(), superviseRequest{Type: supervisePing}, 200*time.Millisecond)
			if !errors.Is(err, ErrSuperviseLinkFailed) {
				t.Fatalf("request = %v, want a link failure", err)
			}
			waitFor(t, "the failure handler", func() bool { return failures.Load() == 1 })
			before := sup.count(supervisePing)
			if _, err := l.request(context.Background(), superviseRequest{Type: supervisePing}, time.Second); !errors.Is(err, ErrSuperviseLinkFailed) {
				t.Fatalf("request after the failure = %v, want ErrSuperviseLinkFailed", err)
			}
			time.Sleep(20 * time.Millisecond)
			if sup.count(supervisePing) != before {
				t.Fatal("a request was written after the link failed")
			}
			if failures.Load() != 1 {
				t.Fatalf("failure handler ran %d times, want once", failures.Load())
			}
		})
	}
}

// spec: 28.5.3 (CH-SUPERVISE, Messages)
func TestSupervisorLinkDeliversAnExitedOfTheCurrentGeneration_spec_28_5_3(t *testing.T) {
	l, sup := linkPair(t)
	exits := make(chan RuntimeExit, 1)
	l.setHandlers(nil, func(gen uint64, e RuntimeExit) {
		if gen == 1 {
			exits <- e
		}
	})
	code := 3
	sup.react(superviseLaunch, fakeReaction{noProcess: true, exitAfterLaunch: &code})
	sup.serve()
	if _, err := l.request(context.Background(), superviseRequest{Type: superviseLaunch, Gen: 1}, time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-exits:
		if e.Code != 3 || e.Signaled {
			t.Fatalf("exit = %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no exited delivered")
	}
	if l.Failed() {
		t.Fatal("an exited of the current generation failed the link")
	}
}

// spec: 28.5.3 (CH-SUPERVISE, Preconditions; CH-SUPERVISE, Timing), 4.7.10
// (Runtime supervisor, Liveness)
func TestSupervisorLinkPingsWhileIdleAndFailsOnAMissedPong_spec_28_5_3(t *testing.T) {
	t.Run("pongs keep the link up", func(t *testing.T) {
		l, sup := linkPair(t)
		sup.serve()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); l.RunPing(ctx, 5*time.Millisecond, time.Second) }()
		waitFor(t, "three pings", func() bool { return sup.count(supervisePing) >= 3 })
		cancel()
		<-done
		if l.Failed() {
			t.Fatal("answered pings failed the link")
		}
	})
	t.Run("a missed pong is a link failure", func(t *testing.T) {
		l, sup := linkPair(t)
		var failed atomic.Bool
		l.setHandlers(func(error) { failed.Store(true) }, nil)
		sup.react(supervisePing, fakeReaction{silent: true})
		sup.serve()
		done := make(chan struct{})
		go func() {
			defer close(done)
			l.RunPing(context.Background(), 5*time.Millisecond, 30*time.Millisecond)
		}()
		<-done
		if !failed.Load() {
			t.Fatal("a missed pong did not fail the link")
		}
	})
	t.Run("no ping while another request is outstanding", func(t *testing.T) {
		l, sup := linkPair(t)
		sup.react(superviseSweep, fakeReaction{delay: 100 * time.Millisecond})
		sup.serve()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go l.RunPing(ctx, 2*time.Millisecond, time.Second)
		if _, err := l.request(context.Background(), superviseRequest{Type: superviseSweep}, time.Second); err != nil {
			t.Fatal(err)
		}
		// A ping written while the sweep was outstanding would have replaced
		// it as the outstanding request, and the sweep's late reply would
		// then match nothing and fail the link.
		waitFor(t, "a ping after the sweep", func() bool { return sup.count(supervisePing) >= 1 })
		if l.Failed() {
			t.Fatal("the link failed: a ping was sent while the sweep was outstanding")
		}
	})
}

// spec: 28.5.3 (CH-SUPERVISE, Messages; CH-SUPERVISE, Preconditions)
// diagnosis: the ping took its generation from the runtime, which a launch
// raises before the launch request is sent, so a ping in that window named a
// generation the supervisor had not been sent.
func TestSupervisorLinkPingCarriesTheGenerationOfTheLatestSentLaunch_spec_28_5_3(t *testing.T) {
	f := newSupervisedFixture(t, LifetimeKeep, LaunchSession, nil)
	// The launch hook runs after the runtime raised its generation and before
	// the launch request is sent; it holds that window open until a ping goes
	// out inside it.
	f.rt.setLaunchHook(func() {
		before := f.sup.count(supervisePing)
		waitFor(t, "a ping inside the launch window", func() bool { return f.sup.count(supervisePing) > before })
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); f.link.RunPing(ctx, 2*time.Millisecond, time.Second) }()
	if err := f.rt.Start(startCtx(t, 5*time.Second), "a"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a ping after the launch", func() bool { return f.sup.count(supervisePing) >= 2 })
	cancel()
	<-done
	f.sup.mu.Lock()
	defer f.sup.mu.Unlock()
	var sent uint64
	for i, req := range f.sup.requests {
		gen := uint64(req["gen"].(float64))
		switch req["type"] {
		case superviseLaunch:
			sent = gen
		case supervisePing:
			if gen != sent {
				t.Fatalf("request %d: ping carries generation %d, the latest sent launch assigned %d", i, gen, sent)
			}
		}
	}
	if l := f.link; l.Failed() {
		t.Fatal("the link failed")
	}
}
