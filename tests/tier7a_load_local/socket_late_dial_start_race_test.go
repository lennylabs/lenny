// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for a runtime that dials the
// CH-MSGSOCK socket around the moment a Start's accept bound expires.
//
// The adapter's listener has one accept path for the pod's life. A Start that
// times out stops waiting but leaves that path running, so the runtime's
// connection, whether it arrives just before or just after the timeout, is
// installed once and serves the next Start. Each attempt races the runtime's
// dial against a Start whose bound is a few milliseconds, then requires a
// second Start to return on the single connection and carry a frame to it.
//
// spec: §4.7.10 (Runtime process lifetime), §28.5.3 (CH-MSGSOCK).
package tier7a_load_local_test

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

// lateDialStartBound is the first Start's accept bound, short enough that
// the runtime's dial lands on either side of it across the attempts.
const lateDialStartBound = 2 * time.Millisecond

// spec: §4.7.10 (Runtime process lifetime), §28.5.3 (CH-MSGSOCK)
// diagnosis: a failure means a runtime connection that arrived around a
// timed-out Start was lost: either an abandoned accept took the connection
// and nobody installed it, so the next Start waited out its bound, or the
// envelope did not reach the one runtime connection. On a pod this leaves a
// runtime that no session can reach. Check that SocketRuntimeProcess keeps
// one accept loop per listener and installs what it accepts for the next
// Start.
func TestLateDialRacingATimedOutStartServesTheNextStart_spec_4_7_10(t *testing.T) {
	for attempt := range raceAttempts {
		t.Run(fmt.Sprintf("attempt_%d", attempt), func(t *testing.T) {
			lateDialAttempt(t)
		})
	}
}

// lateDialAttempt races one runtime dial against a short-bounded Start, then
// starts the next session and checks it is served by that dial.
func lateDialAttempt(t *testing.T) {
	t.Helper()
	rt, socket := newListenerRaceRuntime(t)
	rt.AcceptTimeout = lateDialStartBound

	var runtimeConn net.Conn
	var firstErr error
	rendezvous := newRaceStart(2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		rendezvous.arrive()
		firstErr = rt.Start(context.Background(), "alice")
	}()
	go func() {
		defer wg.Done()
		rendezvous.arrive()
		time.Sleep(lateDialStartBound)
		runtimeConn = dialRuntimeSocket(t, socket)
	}()
	rendezvous.release(t)
	wg.Wait()
	_ = firstErr // Either outcome is legal: the dial won or lost the bound.

	rt.AcceptTimeout = listenerRaceAcceptTimeout
	if err := rt.Start(context.Background(), "bob"); err != nil {
		t.Fatalf("Start after the racing dial = %v, want it served by that dial", err)
	}
	const envelope = `{"type":"message","sessionId":"bob"}`
	if err := rt.WriteEnvelope("bob", []byte(envelope)); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	if !readsEnvelope(runtimeConn, envelope) {
		t.Fatal("the next session's envelope did not reach the runtime's one connection")
	}
}
