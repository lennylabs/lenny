// SPDX-License-Identifier: MIT

//go:build integration

package tier4_integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// statusChangeStates returns the states the status_change items of a
// session's event list carry, in order.
func statusChangeStates(items []any) []string {
	var out []string
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m["type"] != "status_change" {
			continue
		}
		var payload map[string]any
		switch d := m["data"].(type) {
		case string:
			_ = json.Unmarshal([]byte(d), &payload)
		case map[string]any:
			payload = d
		}
		st, _ := payload["state"].(string)
		out = append(out, st)
	}
	return out
}

// spec: 7.2 (Session state machine), 6.2 (Pod State Machine), 10.1.1
// (Stateless Replicas and Per-Session Coordination)
// diagnosis: a lenny/request_input wait served by a replica that does not
// hold the session's coordination lease was not persisted, so the lease
// holder's idle sweep suspended or expired a session whose runtime was only
// waiting for input; or the wait's entry and exit writes did not publish
// status_change(input_required) and then one status_change(running) on the
// client's event stream.
func TestRequestInputWaitOutlastsIdleBoundOffHolder_spec_7_2(t *testing.T) {
	const idle = 10
	h := newComposeWith(t, lifecycleharness.Options{Replicas: 2, Pods: 8, Events: true, MaxIdleSeconds: idle})
	holder, waiter := h.Replicas[0], h.Replicas[1]
	id := h.StartSession(holder)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan map[string]any, 1)
	go func() {
		done <- h.GatewayControlCall(ctx, waiter, id, "lenny/request_input",
			`{"requestId":"req-idle","parts":[{"type":"text","inline":"continue?"}]}`)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for h.Row(id).State != session.StateInputRequired {
		if time.Now().After(deadline) {
			t.Fatalf("the row never reached input_required; state %q", h.Row(id).State)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The waiting session outlasts the idle bound across several sweeper
	// and watchdog passes on every replica, the lease holder's included.
	for passed := time.Duration(0); passed <= 3*idle*time.Second; passed += lifecycleharness.SweepInterval {
		h.Pass()
	}
	if st := h.Row(id).State; st != session.StateInputRequired {
		t.Fatalf("after the idle bound the row is %q, want input_required: the idle sweep acted on a waiting session", st)
	}

	if err := waiter.InputWaits.Resolve(id, "req-idle", "yes"); err != nil {
		t.Fatalf("answer the wait: %v", err)
	}
	select {
	case res := <-done:
		if res["isError"] == true || res["_error"] != nil {
			t.Fatalf("lenny/request_input = %v, want the answer", res)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("lenny/request_input did not return after the answer")
	}
	h.Settle()
	if st := h.Row(id).State; st != session.StateRunning {
		t.Fatalf("after the answer the row is %q, want running", st)
	}
	_, items := h.Events(holder, id)
	got := fmt.Sprint(statusChangeStates(items))
	var waits, runs int
	for _, st := range statusChangeStates(items) {
		switch st {
		case string(session.StateInputRequired):
			waits++
		case string(session.StateRunning):
			if waits > 0 {
				runs++
			}
		case string(session.StateSuspended), string(session.StateExpired):
			t.Errorf("status_change(%s) on the stream %s, want none", st, got)
		}
	}
	if waits != 1 || runs != 1 {
		t.Errorf("status_change states = %s, want one input_required followed by one running", got)
	}
}
