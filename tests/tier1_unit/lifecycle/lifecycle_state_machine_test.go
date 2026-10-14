// SPDX-License-Identifier: MIT

package lifecycle_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// lifecycleOp is one operation the state machine applies to a session on
// one replica.
type lifecycleOp struct {
	name string
	run  func(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string)
}

func lifecycleOps() []lifecycleOp {
	return []lifecycleOp{
		{"DELETE", func(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string) {
			h.Do(r, http.MethodDelete, "/v1/sessions/"+id, nil)
		}},
		{"terminate", func(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string) {
			h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/terminate", nil)
		}},
		{"resume", func(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string) {
			h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/resume", nil)
		}},
		{"interrupt", func(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string) {
			h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/interrupt", nil)
		}},
		{"crash report", func(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string) {
			lifecycleharness.Report(t, r, id, string(session.FailureRuntimeCrash))
		}},
		{"non-retryable report", func(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string) {
			lifecycleharness.Report(t, r, id, string(session.FailureSetupCommandFailed))
		}},
		{"drive", func(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string) {
			r.Server.DriveResume(context.Background(), h.Row(id))
		}},
		{"tick", func(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string) {
			h.Pass()
		}},
	}
}

// TestLifecycleStateMachine_spec_7_2 applies random sequences of lifecycle
// operations (terminal writes, POST /resume, interrupts, failure reports,
// resume drives, and loop ticks) to a session on random replicas of a
// two-replica harness, one at a time, checks the invariants that need no
// quiescence after every step, runs the full checker at the end of each
// sequence, and shrinks any failing sequence.
//
// spec: §7.2 (Interactive Session Model), §7.3 (Resume driver), §6.2 (Pod
// State Machine)
func TestLifecycleStateMachine_spec_7_2(t *testing.T) {
	h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 2, Pods: 64, CredentialPool: true})
	ops := lifecycleOps()
	rapid.Check(t, func(rt *rapid.T) {
		id := h.StartSession(h.Replicas[0])
		steps := rapid.IntRange(1, 6).Draw(rt, "steps")
		var applied []string
		for i := 0; i < steps; i++ {
			op := ops[rapid.IntRange(0, len(ops)-1).Draw(rt, fmt.Sprintf("op%d", i))]
			r := h.Replicas[rapid.IntRange(0, len(h.Replicas)-1).Draw(rt, fmt.Sprintf("replica%d", i))]
			op.run(t, h, r, id)
			h.Settle()
			applied = append(applied, op.name+"@"+r.ID)
			if row := h.Row(id); row.RetryCount > lifecycleharness.MaxRetries {
				rt.Fatalf("after %v: RetryCount = %d over the budget", applied, row.RetryCount)
			}
		}
		h.RunToQuiescence()
		if v := h.Violations(); len(v) != 0 {
			rt.Fatalf("after %v:\n%s", applied, strings.Join(v, "\n"))
		}
		// Each sequence leaves its session terminal so the next sequence's
		// invariants are not affected by it.
		h.Do(h.Replicas[0], http.MethodDelete, "/v1/sessions/"+id, nil)
		h.RunToQuiescence()
	})
}
