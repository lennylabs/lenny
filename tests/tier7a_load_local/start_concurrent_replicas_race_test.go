// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local coverage for concurrent POST /start calls on one
// session spread across two gateway replicas that share one session store
// and one coordination lease store. The ready → starting write re-validates
// the start precondition on the locked row, so exactly one call reaches
// running and every other call is refused with 409 INVALID_STATE_TRANSITION
// carrying starting or running: the state its entry read found, or the
// state its own starting write found on the locked row.
//
// spec: §15.1 (/start), §7.2 (Session state machine: ready → starting),
// §10.1.1 (Primary:)
package tier7a_load_local_test

import (
	"math/rand"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// startAnswer is one concurrent /start call's status and refusal state.
type startAnswer struct {
	code  int
	state string
}

// spec: §15.1 (/start), §7.2 (Session state machine: ready → starting),
// §10.1.1 (Primary:)
// diagnosis: two concurrent /start calls on one session both reached
// running, or a losing call answered something other than 409 with starting
// or running, or the session ended outside running, or a harness invariant
// failed (two replicas binding one session, or a lease held by a replica
// with no binding). The ready → starting write is not serializing starts on
// the locked row, or a losing start wrote over the winner's row.
func TestConcurrentStartsAcrossReplicasOneWins_spec_15_1(t *testing.T) {
	const rounds, callers = 6, 4
	h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 2, Pods: rounds})
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	for round := 0; round < rounds; round++ {
		id := h.CreateSession(h.Replicas[0])
		if code, body := h.Do(h.Replicas[0], http.MethodPost, "/v1/sessions/"+id+"/finalize", nil); code != http.StatusOK {
			t.Fatalf("round %d: finalize = %d %v", round, code, body)
		}
		answers := make([]startAnswer, callers)
		var mu sync.Mutex
		actors := make([]func(), callers)
		for i := range actors {
			i, r := i, h.Replicas[i%len(h.Replicas)]
			actors[i] = func() {
				code, body := h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/start", nil)
				a := startAnswer{code: code}
				if e, ok := body["error"].(map[string]any); ok {
					if d, ok := e["details"].(map[string]any); ok {
						a.state, _ = d["currentState"].(string)
					}
				}
				mu.Lock()
				answers[i] = a
				mu.Unlock()
			}
		}
		raceJittered(rng, actors...)
		requireOneStartWon(t, round, answers)
		h.Settle()
		if st := h.Row(id).State; st != session.StateRunning {
			t.Fatalf("round %d: session = %s, want running", round, st)
		}
		requireNoViolations(t, h, round, "after the concurrent starts")
	}
	h.Check(t)
}

// requireOneStartWon asserts one 200 and a 409 with starting or running for
// every other call.
func requireOneStartWon(t *testing.T, round int, answers []startAnswer) {
	t.Helper()
	won := 0
	for _, a := range answers {
		switch {
		case a.code == http.StatusOK:
			won++
		case a.code == http.StatusConflict && (a.state == string(session.StateStarting) || a.state == string(session.StateRunning)):
		default:
			t.Fatalf("round %d: a losing /start answered %d with %q, want 409 with starting or running", round, a.code, a.state)
		}
	}
	if won != 1 {
		t.Fatalf("round %d: %d starts reached running, want 1 (answers %v)", round, won, answers)
	}
}
