// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
)

// countingAppender records each audit append, or fails every append.
type countingAppender struct {
	mu   sync.Mutex
	n    int
	fail bool
}

func (c *countingAppender) Append(context.Context, string, string, json.RawMessage, time.Time) (audit.Row, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return audit.Row{}, errors.New("audit chain unavailable")
	}
	c.n++
	return audit.Row{}, nil
}

// TestHeldDeliveryRejectRemovesTheEntry_spec_4_8 asserts that a
// post-agent-output REJECT on a held entry records one audit row, publishes
// nothing for the entry, removes it, and lets later entries proceed, and that
// with the audit sink failing the entry stays at the head.
//
// spec: §7.2 (Interactive Session Model), §4.8 (Gateway Policy Engine)
func TestHeldDeliveryRejectRemovesTheEntry_spec_4_8(t *testing.T) {
	for _, failing := range []bool{false, true} {
		t.Run(map[bool]string{false: "audit recorded", true: "audit failing"}[failing], func(t *testing.T) {
			f := newHeldFixture(t, false)
			chain := interceptor.NewChain()
			if err := chain.Register(interceptor.PhasePostAgentOutput, rejectInterceptor{}); err != nil {
				t.Fatalf("register: %v", err)
			}
			f.srv.interceptors = chain
			app := &countingAppender{fail: failing}
			f.srv.policyAuditSink = policy.NewAuditSink(app, nil)
			base := time.Now().Add(-time.Minute)
			f.holdInbox(t, "m1", base, heldPayload(t, "m1", "", "a"))
			f.holdInbox(t, "m2", base.Add(time.Second), heldPayload(t, "m2", "", "b"))

			err := f.srv.deliverBuffered(context.Background(), f.row(t))
			if failing {
				if err == nil {
					t.Fatal("deliverBuffered = nil, want the audit error")
				}
				if n := f.remaining(t); n != 2 {
					t.Errorf("held = %d, want the rejected entry left at the head with its successor", n)
				}
				return
			}
			if err != nil {
				t.Fatalf("deliverBuffered: %v", err)
			}
			if app.n != 2 {
				t.Errorf("audit rows = %d, want one per rejected entry", app.n)
			}
			if n := f.remaining(t); n != 0 {
				t.Errorf("held = %d, want every rejected entry removed", n)
			}
		})
	}
}
