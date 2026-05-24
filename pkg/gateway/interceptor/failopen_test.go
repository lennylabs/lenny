// SPDX-License-Identifier: MIT

package interceptor_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
)

// failingOpen is a fail-open interceptor whose Intercept errors while
// fail is true. Name is fixed so the chain tracks one rolling window.
type failingOpen struct {
	name string
	fail bool
}

func (f *failingOpen) Name() string                       { return f.name }
func (f *failingOpen) Priority() int32                    { return 500 }
func (f *failingOpen) Builtin() bool                      { return false }
func (f *failingOpen) FailPolicy() interceptor.FailPolicy { return interceptor.FailOpen }
func (f *failingOpen) Timeout() time.Duration             { return 0 }
func (f *failingOpen) Intercept(_ context.Context, _ interceptor.Request) (interceptor.Result, error) {
	if f.fail {
		return interceptor.Result{}, errors.New("boom")
	}
	return interceptor.Result{Action: interceptor.ActionAllow}, nil
}

type recordingObserver struct {
	escalated []interceptor.FailOpenEvent
	restored  []interceptor.FailOpenEvent
}

func (r *recordingObserver) FailOpenEscalated(_ context.Context, ev interceptor.FailOpenEvent) {
	r.escalated = append(r.escalated, ev)
}
func (r *recordingObserver) FailOpenRestored(_ context.Context, ev interceptor.FailOpenEvent) {
	r.restored = append(r.restored, ev)
}

// spec: §4.8 line 1030 — a fail-open interceptor below the ceiling is
// skipped (ALLOW); crossing the ceiling auto-escalates to fail-closed
// (REJECT) and emits interceptor.fail_open_escalated once.
func TestFailOpenEscalation(t *testing.T) {
	ic := &failingOpen{name: "guardrails", fail: true}
	c := interceptor.NewChain()
	obs := &recordingObserver{}
	c.SetFailOpenEscalation(3, time.Minute, obs, func() time.Time { return time.Unix(0, 0) })
	if err := c.Register(interceptor.PhasePreDelegation, ic); err != nil {
		t.Fatalf("register: %v", err)
	}

	// First 3 errors are within the ceiling → skipped (ALLOW on the
	// empty rest of the chain).
	for i := 0; i < 3; i++ {
		if got := c.Run(context.Background(), interceptor.Request{Phase: interceptor.PhasePreDelegation, TenantID: "acme"}); got.Action != interceptor.ActionAllow {
			t.Fatalf("call %d: action = %v, want ALLOW", i, got.Action)
		}
	}
	if len(obs.escalated) != 0 {
		t.Fatalf("escalated too early: %d", len(obs.escalated))
	}

	// The 4th error exceeds the ceiling of 3 → escalate to fail-closed.
	res := c.Run(context.Background(), interceptor.Request{Phase: interceptor.PhasePreDelegation, TenantID: "acme"})
	if res.Action != interceptor.ActionReject || res.Code != interceptor.CodeInterceptorTimeout {
		t.Fatalf("escalated call: %+v, want REJECT/INTERCEPTOR_TIMEOUT", res)
	}
	if res.RejectedBy != "guardrails" {
		t.Errorf("RejectedBy = %q, want guardrails", res.RejectedBy)
	}
	if len(obs.escalated) != 1 {
		t.Fatalf("escalated count = %d, want 1", len(obs.escalated))
	}
	if obs.escalated[0].TenantID != "acme" || obs.escalated[0].Phase != interceptor.PhasePreDelegation {
		t.Errorf("escalation event = %+v", obs.escalated[0])
	}

	// A further error stays escalated but does not re-emit.
	c.Run(context.Background(), interceptor.Request{Phase: interceptor.PhasePreDelegation, TenantID: "acme"})
	if len(obs.escalated) != 1 {
		t.Errorf("re-emitted escalation: %d", len(obs.escalated))
	}
}

// spec: §4.8 line 1030 — an error-free call after escalation restores
// the interceptor to fail-open and emits interceptor.fail_open_restored.
func TestFailOpenRestore(t *testing.T) {
	ic := &failingOpen{name: "g", fail: true}
	c := interceptor.NewChain()
	obs := &recordingObserver{}
	c.SetFailOpenEscalation(1, time.Minute, obs, func() time.Time { return time.Unix(0, 0) })
	_ = c.Register(interceptor.PhasePreDelegation, ic)

	c.Run(context.Background(), interceptor.Request{Phase: interceptor.PhasePreDelegation}) // err 1, within ceiling 1
	res := c.Run(context.Background(), interceptor.Request{Phase: interceptor.PhasePreDelegation}) // err 2 > 1 → escalate
	if res.Action != interceptor.ActionReject {
		t.Fatalf("expected escalation REJECT, got %v", res.Action)
	}

	// Interceptor recovers; next call succeeds → restore.
	ic.fail = false
	if got := c.Run(context.Background(), interceptor.Request{Phase: interceptor.PhasePreDelegation}); got.Action != interceptor.ActionAllow {
		t.Fatalf("recovered call: %v, want ALLOW", got.Action)
	}
	if len(obs.restored) != 1 {
		t.Fatalf("restored count = %d, want 1", len(obs.restored))
	}

	// After restore, errors are skipped again until the ceiling is re-crossed.
	ic.fail = true
	if got := c.Run(context.Background(), interceptor.Request{Phase: interceptor.PhasePreDelegation}); got.Action != interceptor.ActionAllow {
		t.Errorf("post-restore first error: %v, want ALLOW (skipped)", got.Action)
	}
}

// The rolling window prunes stale errors, so errors spread beyond the
// window never accumulate to escalation.
func TestFailOpenWindowPrunes(t *testing.T) {
	ic := &failingOpen{name: "g", fail: true}
	c := interceptor.NewChain()
	obs := &recordingObserver{}
	now := time.Unix(0, 0)
	c.SetFailOpenEscalation(2, 10*time.Second, obs, func() time.Time { return now })
	_ = c.Register(interceptor.PhasePreDelegation, ic)

	for i := 0; i < 6; i++ {
		c.Run(context.Background(), interceptor.Request{Phase: interceptor.PhasePreDelegation})
		now = now.Add(11 * time.Second) // each error falls outside the prior window
	}
	if len(obs.escalated) != 0 {
		t.Errorf("escalated despite pruning: %d", len(obs.escalated))
	}
}

// With escalation unconfigured (zero value) a fail-open error is always
// skipped — the default NewChain stays backward-compatible.
func TestFailOpenUnconfiguredSkips(t *testing.T) {
	ic := &failingOpen{name: "g", fail: true}
	c := interceptor.NewChain()
	_ = c.Register(interceptor.PhasePreDelegation, ic)
	for i := 0; i < 100; i++ {
		if got := c.Run(context.Background(), interceptor.Request{Phase: interceptor.PhasePreDelegation}); got.Action != interceptor.ActionAllow {
			t.Fatalf("call %d: action = %v, want ALLOW (no escalation configured)", i, got.Action)
		}
	}
}
