// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// failureHandlerDelay holds the failure handler long enough that a request
// released before the handler finished would observe it unfinished.
const failureHandlerDelay = 100 * time.Millisecond

// spec: 4.7.10 (Runtime supervisor, Liveness), 28.5.3 (CH-SUPERVISE,
// Degradation)
// diagnosis: a link failure the read loop detected released the waiting
// request before the failure handler set the not-live state, so a caller
// that went on after the failed request could read the runtime as live.
func TestSupervisorLinkFailureHandlerFinishesBeforeAnyRequestReturns_spec_4_7_10(t *testing.T) {
	l, sup := linkPair(t)
	var handled atomic.Bool
	l.setHandlers(func(error) {
		time.Sleep(failureHandlerDelay)
		handled.Store(true)
	}, nil)
	sup.react(supervisePing, fakeReaction{closeConn: true})
	sup.serve()

	type result struct {
		err     error
		handled bool
	}
	first := make(chan result, 1)
	go func() {
		_, err := l.request(context.Background(), superviseRequest{Type: supervisePing}, 5*time.Second)
		first <- result{err: err, handled: handled.Load()}
	}()
	// A request issued once the failure is recorded fails fast without a
	// send, and it too waits for the handler.
	waitFor(t, "the link failure to be recorded", l.Failed)
	second := make(chan result, 1)
	go func() {
		_, err := l.request(context.Background(), superviseRequest{Type: supervisePing}, 5*time.Second)
		second <- result{err: err, handled: handled.Load()}
	}()
	for name, ch := range map[string]chan result{"the waiting request": first, "the later request": second} {
		select {
		case r := <-ch:
			if !errors.Is(r.err, ErrSuperviseLinkFailed) {
				t.Fatalf("%s = %v, want ErrSuperviseLinkFailed", name, r.err)
			}
			if !r.handled {
				t.Fatalf("%s returned before the failure handler finished", name)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s did not return", name)
		}
	}
}

// spec: 4.7.10 (Runtime supervisor, Liveness), 5.2 (Supervised scrub
// sequence)
// diagnosis: the supervised runtime read as able to serve the next session
// when the request a link failure ended had already returned.
func TestSupervisedRuntimeIsNotLiveWhenARequestReturnsALinkFailure_spec_4_7_10(t *testing.T) {
	for _, lifetime := range []RuntimeLifetime{LifetimeRestart, LifetimeKeep} {
		t.Run(string(lifetime), func(t *testing.T) {
			f := newSupervisedFixture(t, lifetime, LaunchSession, nil)
			delayLinkFailureHandler(f.rt, f.link, failureHandlerDelay)
			f.sup.react(supervisePing, fakeReaction{closeConn: true})
			_, err := f.link.request(context.Background(), superviseRequest{Type: supervisePing}, 5*time.Second)
			if !errors.Is(err, ErrSuperviseLinkFailed) {
				t.Fatalf("request = %v, want ErrSuperviseLinkFailed", err)
			}
			if nl, cause := f.rt.NotLive(); !nl || cause != CauseLinkFailure {
				t.Fatalf("not-live = %v (%s) when the request returned, want the link-failure cause", nl, cause)
			}
			if f.rt.ServesNextSession() {
				t.Fatal("the runtime reads as serving the next session after a link failure")
			}
		})
	}
}

// delayLinkFailureHandler reinstalls the runtime's link handlers with the
// failure handler held back by d before it sets the not-live state, which
// widens the interval between the read loop detecting a failure and the
// state being set.
func delayLinkFailureHandler(rt *SupervisedRuntime, link *SupervisorLink, d time.Duration) {
	link.setHandlers(
		func(error) {
			time.Sleep(d)
			rt.MarkNotLive(CauseLinkFailure)
		},
		func(gen uint64, exit RuntimeExit) { rt.endEvent(gen, RuntimeEndEvent{Kind: EndByExited, Exit: exit}) },
	)
}

// spec: 4.7.10 (Runtime supervisor, Liveness; Runtime supervisor, Launch),
// 5.2 (Pod retirement policy, Runtime not live)
// diagnosis: between the read loop recording a link failure and the failure
// handler setting the not-live state, a start joined the live process and
// the liveness sample reported the runtime as serving the next session.
func TestSupervisedRuntimeRefusesAStartOnceTheLinkHasFailed_spec_4_7_10(t *testing.T) {
	for _, lifetime := range []RuntimeLifetime{LifetimeRestart, LifetimeKeep} {
		t.Run(string(lifetime), func(t *testing.T) {
			e := newExitsFixture(t, lifetime, LaunchSession)
			release := make(chan struct{})
			e.link.setHandlers(func(error) {
				<-release
				e.rt.MarkNotLive(CauseLinkFailure)
			}, nil)
			e.sup.react(supervisePing, fakeReaction{closeConn: true})
			returned := make(chan error, 1)
			go func() {
				_, err := e.link.request(context.Background(), superviseRequest{Type: supervisePing}, 5*time.Second)
				returned <- err
			}()
			waitFor(t, "the link failure to be recorded", e.link.Failed)
			if e.rt.ServesNextSession() {
				t.Error("the runtime reads as serving the next session while its link has failed")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := e.rt.Start(ctx, "b"); !errors.Is(err, errRuntimeNotLive) {
				t.Errorf("start after the link failed = %v, want errRuntimeNotLive", err)
			}
			if err := e.rt.confirmStart("a"); !errors.Is(err, errRuntimeNotLive) {
				t.Errorf("recording a start after the link failed = %v, want errRuntimeNotLive", err)
			}
			close(release)
			if err := <-returned; !errors.Is(err, ErrSuperviseLinkFailed) {
				t.Fatalf("request = %v, want ErrSuperviseLinkFailed", err)
			}
		})
	}
}
