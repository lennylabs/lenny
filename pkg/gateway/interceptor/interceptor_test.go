// SPDX-License-Identifier: MIT

package interceptor_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
)

// fakeInterceptor is a configurable interceptor.Interceptor. When fn
// is nil it returns ALLOW; calls, when set, records each invocation.
type fakeInterceptor struct {
	name       string
	priority   int32
	builtin    bool
	failPolicy interceptor.FailPolicy
	timeout    time.Duration
	fn         func(ctx context.Context, req interceptor.Request) (interceptor.Result, error)
	calls      *[]string
}

func (f *fakeInterceptor) Name() string           { return f.name }
func (f *fakeInterceptor) Priority() int32        { return f.priority }
func (f *fakeInterceptor) Builtin() bool          { return f.builtin }
func (f *fakeInterceptor) Timeout() time.Duration { return f.timeout }

func (f *fakeInterceptor) FailPolicy() interceptor.FailPolicy {
	if f.failPolicy == "" {
		return interceptor.FailClosed
	}
	return f.failPolicy
}

func (f *fakeInterceptor) Intercept(ctx context.Context, req interceptor.Request) (interceptor.Result, error) {
	if f.calls != nil {
		*f.calls = append(*f.calls, f.name)
	}
	if f.fn != nil {
		return f.fn(ctx, req)
	}
	return interceptor.Result{Action: interceptor.ActionAllow}, nil
}

func mustRegister(t *testing.T, c *interceptor.Chain, phase interceptor.Phase, ic interceptor.Interceptor) {
	t.Helper()
	if err := c.Register(phase, ic); err != nil {
		t.Fatalf("Register %s: %v", phase, err)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

const phase = interceptor.PhasePreDelegation

func TestChainEmptyAllows(t *testing.T) {
	c := interceptor.NewChain()
	res := c.Run(context.Background(), interceptor.Request{Phase: phase})
	if res.Action != interceptor.ActionAllow {
		t.Errorf("empty chain action = %v, want ALLOW", res.Action)
	}
}

func TestChainRejectShortCircuits(t *testing.T) {
	c := interceptor.NewChain()
	var calls []string
	mustRegister(t, c, phase, &fakeInterceptor{
		name: "reject", priority: 200, builtin: true, calls: &calls,
		fn: func(context.Context, interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{Action: interceptor.ActionReject, Reason: "blocked"}, nil
		},
	})
	mustRegister(t, c, phase, &fakeInterceptor{name: "after", priority: 300, builtin: true, calls: &calls})

	res := c.Run(context.Background(), interceptor.Request{Phase: phase})
	if res.Action != interceptor.ActionReject || res.Reason != "blocked" {
		t.Errorf("result = %+v, want REJECT/blocked", res)
	}
	if !equal(calls, []string{"reject"}) {
		t.Errorf("calls = %v, want [reject] — the chain must short-circuit on REJECT", calls)
	}
}

func TestChainModifyChainsContent(t *testing.T) {
	c := interceptor.NewChain()
	appendIC := func(name string, prio int32, suffix string) *fakeInterceptor {
		return &fakeInterceptor{
			name: name, priority: prio, builtin: true,
			fn: func(_ context.Context, req interceptor.Request) (interceptor.Result, error) {
				next := append(append([]byte(nil), req.Content...), suffix...)
				return interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: next}, nil
			},
		}
	}
	mustRegister(t, c, phase, appendIC("first", 200, "-a"))
	mustRegister(t, c, phase, appendIC("second", 300, "-b"))

	res := c.Run(context.Background(), interceptor.Request{Phase: phase, Content: []byte("in")})
	if res.Action != interceptor.ActionModify {
		t.Fatalf("action = %v, want MODIFY", res.Action)
	}
	if string(res.ModifiedContent) != "in-a-b" {
		t.Errorf("content = %q, want in-a-b — the second interceptor sees the first's output", res.ModifiedContent)
	}
}

func TestChainPriorityOrdering(t *testing.T) {
	c := interceptor.NewChain()
	var calls []string
	// Register out of priority order to prove the chain sorts.
	mustRegister(t, c, phase, &fakeInterceptor{name: "p300", priority: 300, builtin: true, calls: &calls})
	mustRegister(t, c, phase, &fakeInterceptor{name: "p100", priority: 100, builtin: true, calls: &calls})
	mustRegister(t, c, phase, &fakeInterceptor{name: "p200", priority: 200, builtin: true, calls: &calls})

	c.Run(context.Background(), interceptor.Request{Phase: phase})
	if want := []string{"p100", "p200", "p300"}; !equal(calls, want) {
		t.Errorf("execution order = %v, want %v", calls, want)
	}
}

func TestChainTieBreakBuiltinBeforeExternal(t *testing.T) {
	c := interceptor.NewChain()
	var calls []string
	// Register the external first; the built-in must still run first.
	mustRegister(t, c, phase, &fakeInterceptor{name: "external", priority: 500, builtin: false, calls: &calls})
	mustRegister(t, c, phase, &fakeInterceptor{name: "builtin", priority: 500, builtin: true, calls: &calls})

	c.Run(context.Background(), interceptor.Request{Phase: phase})
	if want := []string{"builtin", "external"}; !equal(calls, want) {
		t.Errorf("execution order = %v, want %v — a built-in runs first at equal priority", calls, want)
	}
}

func TestChainTieBreakRegistrationOrder(t *testing.T) {
	c := interceptor.NewChain()
	var calls []string
	mustRegister(t, c, phase, &fakeInterceptor{name: "ext-1", priority: 500, builtin: false, calls: &calls})
	mustRegister(t, c, phase, &fakeInterceptor{name: "ext-2", priority: 500, builtin: false, calls: &calls})

	c.Run(context.Background(), interceptor.Request{Phase: phase})
	if want := []string{"ext-1", "ext-2"}; !equal(calls, want) {
		t.Errorf("execution order = %v, want %v — registration order breaks an equal-priority tie", calls, want)
	}
}

func TestChainFailClosedRejects(t *testing.T) {
	c := interceptor.NewChain()
	mustRegister(t, c, phase, &fakeInterceptor{
		name: "boom", priority: 200, builtin: true, failPolicy: interceptor.FailClosed,
		fn: func(context.Context, interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{}, errors.New("upstream down")
		},
	})
	res := c.Run(context.Background(), interceptor.Request{Phase: phase})
	if res.Action != interceptor.ActionReject {
		t.Fatalf("action = %v, want REJECT", res.Action)
	}
	if res.Code != interceptor.CodeInterceptorTimeout {
		t.Errorf("code = %q, want %q", res.Code, interceptor.CodeInterceptorTimeout)
	}
}

func TestChainFailOpenSkips(t *testing.T) {
	c := interceptor.NewChain()
	var calls []string
	mustRegister(t, c, phase, &fakeInterceptor{
		name: "boom", priority: 200, builtin: true, failPolicy: interceptor.FailOpen, calls: &calls,
		fn: func(context.Context, interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{}, errors.New("upstream down")
		},
	})
	mustRegister(t, c, phase, &fakeInterceptor{name: "after", priority: 300, builtin: true, calls: &calls})

	res := c.Run(context.Background(), interceptor.Request{Phase: phase})
	if res.Action != interceptor.ActionAllow {
		t.Errorf("action = %v, want ALLOW — a fail-open interceptor is skipped", res.Action)
	}
	if want := []string{"boom", "after"}; !equal(calls, want) {
		t.Errorf("calls = %v, want %v — the chain continues past a fail-open error", calls, want)
	}
}

func TestChainTimeoutFailClosed(t *testing.T) {
	c := interceptor.NewChain()
	mustRegister(t, c, phase, &fakeInterceptor{
		name: "slow", priority: 200, builtin: true, failPolicy: interceptor.FailClosed,
		timeout: 10 * time.Millisecond,
		fn: func(ctx context.Context, _ interceptor.Request) (interceptor.Result, error) {
			<-ctx.Done()
			return interceptor.Result{}, ctx.Err()
		},
	})
	res := c.Run(context.Background(), interceptor.Request{Phase: phase})
	if res.Action != interceptor.ActionReject || res.Code != interceptor.CodeInterceptorTimeout {
		t.Errorf("result = %+v, want REJECT/INTERCEPTOR_TIMEOUT for a timed-out interceptor", res)
	}
}

func TestRejectCarriesAccumulatedContent(t *testing.T) {
	c := interceptor.NewChain()
	mustRegister(t, c, phase, &fakeInterceptor{
		name: "modify", priority: 200, builtin: true,
		fn: func(context.Context, interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: []byte("redacted")}, nil
		},
	})
	mustRegister(t, c, phase, &fakeInterceptor{
		name: "reject", priority: 300, builtin: true,
		fn: func(context.Context, interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{Action: interceptor.ActionReject, Reason: "still bad"}, nil
		},
	})
	res := c.Run(context.Background(), interceptor.Request{Phase: phase, Content: []byte("original")})
	if res.Action != interceptor.ActionReject {
		t.Fatalf("action = %v, want REJECT", res.Action)
	}
	if string(res.ModifiedContent) != "redacted" {
		t.Errorf("reject content = %q, want the post-MODIFY payload %q", res.ModifiedContent, "redacted")
	}
}

func TestPhaseChainsAreIndependent(t *testing.T) {
	c := interceptor.NewChain()
	var calls []string
	mustRegister(t, c, interceptor.PhasePreDelegation,
		&fakeInterceptor{name: "deleg", priority: 200, builtin: true, calls: &calls})

	c.Run(context.Background(), interceptor.Request{Phase: interceptor.PhasePreMessageDelivery})
	if len(calls) != 0 {
		t.Errorf("calls = %v, want none — a PreDelegation interceptor must not run for PreMessageDelivery", calls)
	}
}

func TestRegisterRejectsExternalLowPriority(t *testing.T) {
	c := interceptor.NewChain()
	err := c.Register(phase, &fakeInterceptor{name: "ext", priority: interceptor.ReservedPriorityCeiling, builtin: false})
	if !errors.Is(err, interceptor.ErrInvalidPriority) {
		t.Errorf("err = %v, want ErrInvalidPriority for an external interceptor at the reserved ceiling", err)
	}
}

func TestRegisterRejectsExternalPreAuth(t *testing.T) {
	c := interceptor.NewChain()
	err := c.Register(interceptor.PhasePreAuth, &fakeInterceptor{name: "ext", priority: 500, builtin: false})
	if !errors.Is(err, interceptor.ErrInvalidPhase) {
		t.Errorf("err = %v, want ErrInvalidPhase for an external interceptor at PreAuth", err)
	}
}

func TestRegisterAllowsBuiltinAtPreAuth(t *testing.T) {
	c := interceptor.NewChain()
	if err := c.Register(interceptor.PhasePreAuth,
		&fakeInterceptor{name: "auth", priority: 100, builtin: true}); err != nil {
		t.Errorf("a built-in at priority 100 / PreAuth should register: %v", err)
	}
}

func TestRegisterRejectsUnknownPhase(t *testing.T) {
	c := interceptor.NewChain()
	err := c.Register(interceptor.Phase("Bogus"), &fakeInterceptor{name: "x", priority: 500, builtin: true})
	if !errors.Is(err, interceptor.ErrUnknownPhase) {
		t.Errorf("err = %v, want ErrUnknownPhase", err)
	}
}

func TestPhaseIsValid(t *testing.T) {
	for _, p := range interceptor.AllPhases() {
		if !p.IsValid() {
			t.Errorf("AllPhases() entry %q reports invalid", p)
		}
	}
	if interceptor.Phase("Nope").IsValid() {
		t.Error("an unknown phase reports valid")
	}
}

func TestActionString(t *testing.T) {
	for action, want := range map[interceptor.Action]string{
		interceptor.ActionAllow:  "ALLOW",
		interceptor.ActionReject: "REJECT",
		interceptor.ActionModify: "MODIFY",
	} {
		if got := action.String(); got != want {
			t.Errorf("Action(%d).String() = %q, want %q", action, got, want)
		}
	}
}

// spec: §4.8 line 981 — Chain.Run stamps Result.RejectedBy with the
// rejecting interceptor's Name() so the §16.7 audit row identifies the
// actual rejector rather than assuming a fixed built-in.
func TestRunStampsRejectedByOnReject(t *testing.T) {
	c := interceptor.NewChain()
	mustRegister(t, c, phase, &fakeInterceptor{
		name: "blocker", priority: 200, builtin: true,
		fn: func(context.Context, interceptor.Request) (interceptor.Result, error) {
			// Return a REJECT that does not set RejectedBy itself.
			return interceptor.Result{Action: interceptor.ActionReject, Reason: "no"}, nil
		},
	})
	res := c.Run(context.Background(), interceptor.Request{Phase: phase})
	if res.Action != interceptor.ActionReject {
		t.Fatalf("Action = %v, want REJECT", res.Action)
	}
	if res.RejectedBy != "blocker" {
		t.Errorf("RejectedBy = %q, want %q", res.RejectedBy, "blocker")
	}
}

// spec: §4.8 line 1032 — a fail-closed timeout/error carries the
// rejecting interceptor's name, the INTERCEPTOR_TIMEOUT code, and the
// elapsed deadline in milliseconds for the §15.1 envelope.
func TestRunFailClosedCarriesNameAndTimeout(t *testing.T) {
	c := interceptor.NewChain()
	mustRegister(t, c, phase, &fakeInterceptor{
		name: "flaky", priority: 200, builtin: true, timeout: 250 * time.Millisecond,
		failPolicy: interceptor.FailClosed,
		fn: func(context.Context, interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{}, errors.New("boom")
		},
	})
	res := c.Run(context.Background(), interceptor.Request{Phase: phase})
	if res.Action != interceptor.ActionReject || res.Code != interceptor.CodeInterceptorTimeout {
		t.Fatalf("Action/Code = %v/%q, want REJECT/INTERCEPTOR_TIMEOUT", res.Action, res.Code)
	}
	if res.RejectedBy != "flaky" {
		t.Errorf("RejectedBy = %q, want %q", res.RejectedBy, "flaky")
	}
	if res.TimeoutMs != 250 {
		t.Errorf("TimeoutMs = %d, want 250", res.TimeoutMs)
	}
}

// A fail-open error skips the interceptor without stamping RejectedBy.
func TestRunFailOpenLeavesRejectedByEmpty(t *testing.T) {
	c := interceptor.NewChain()
	mustRegister(t, c, phase, &fakeInterceptor{
		name: "soft", priority: 200, builtin: true, failPolicy: interceptor.FailOpen,
		fn: func(context.Context, interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{}, errors.New("boom")
		},
	})
	res := c.Run(context.Background(), interceptor.Request{Phase: phase})
	if res.Action != interceptor.ActionAllow {
		t.Fatalf("Action = %v, want ALLOW", res.Action)
	}
	if res.RejectedBy != "" {
		t.Errorf("RejectedBy = %q, want empty", res.RejectedBy)
	}
}

// spec: §4.8 lines 1021, 1023 — Register sentinel errors map to the
// §15.1 INVALID_INTERCEPTOR_PRIORITY / INVALID_INTERCEPTOR_PHASE codes
// with HTTP 400; an unrelated error is not a registration sentinel.
func TestRegistrationErrorCode(t *testing.T) {
	if code, status, ok := interceptor.RegistrationErrorCode(interceptor.ErrInvalidPriority); !ok ||
		code != "INVALID_INTERCEPTOR_PRIORITY" || status != 400 {
		t.Errorf("priority: got (%q,%d,%v)", code, status, ok)
	}
	if code, status, ok := interceptor.RegistrationErrorCode(interceptor.ErrInvalidPhase); !ok ||
		code != "INVALID_INTERCEPTOR_PHASE" || status != 400 {
		t.Errorf("phase: got (%q,%d,%v)", code, status, ok)
	}
	// A wrapped priority sentinel still maps via errors.Is semantics.
	if _, _, ok := interceptor.RegistrationErrorCode(
		errors.Join(errors.New("register"), interceptor.ErrInvalidPriority),
	); !ok {
		t.Error("a wrapped ErrInvalidPriority did not map")
	}
	if _, _, ok := interceptor.RegistrationErrorCode(errors.New("x")); ok {
		t.Error("a non-sentinel error reported ok=true")
	}
	if _, _, ok := interceptor.RegistrationErrorCode(interceptor.ErrUnknownPhase); ok {
		t.Error("ErrUnknownPhase is not a priority/phase sentinel")
	}
}

// spec: §4.8 line 12, line 115 — RunRange executes only the interceptors
// whose priority falls in the [min,max) window, preserving ascending
// priority order. This is the building block for splitting the PreRoute
// chain around the ExperimentRouter built-in at priority 300: the below
// segment runs the 101–299 interceptors, the at-or-above segment runs
// the ≥300 interceptors.
func TestRunRangeExecutesOnlyPriorityWindow_spec_4_8(t *testing.T) {
	var calls []string
	c := interceptor.NewChain()
	mustRegister(t, c, interceptor.PhasePreRoute, &fakeInterceptor{name: "p150", priority: 150, calls: &calls})
	mustRegister(t, c, interceptor.PhasePreRoute, &fakeInterceptor{name: "p250", priority: 250, calls: &calls})
	mustRegister(t, c, interceptor.PhasePreRoute, &fakeInterceptor{name: "p300", priority: 300, calls: &calls})
	mustRegister(t, c, interceptor.PhasePreRoute, &fakeInterceptor{name: "p350", priority: 350, calls: &calls})

	// Below the pivot: only 150 and 250 run, in ascending order.
	c.RunRange(context.Background(), interceptor.Request{Phase: interceptor.PhasePreRoute}, -2147483648, 300)
	if !equal(calls, []string{"p150", "p250"}) {
		t.Fatalf("below-pivot calls = %v, want [p150 p250]", calls)
	}

	// At or above the pivot: 300 (an external at exactly the pivot) and
	// 350 run, in ascending order. An external at the pivot orders after
	// the built-in, which RunRange models by including the pivot value in
	// the at-or-above window.
	calls = nil
	c.RunRange(context.Background(), interceptor.Request{Phase: interceptor.PhasePreRoute}, 300, 2147483647)
	if !equal(calls, []string{"p300", "p350"}) {
		t.Fatalf("at-or-above-pivot calls = %v, want [p300 p350]", calls)
	}
}

// spec: §4.8 line 12 — a MODIFY inside a RunRange segment is threaded to
// the later interceptors of that same segment and surfaces as the
// segment result, exactly as Run does for the full chain.
func TestRunRangeThreadsModifyWithinSegment_spec_4_8(t *testing.T) {
	c := interceptor.NewChain()
	mustRegister(t, c, interceptor.PhasePreRoute, &fakeInterceptor{
		name: "p120", priority: 120,
		fn: func(context.Context, interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: []byte("rewritten")}, nil
		},
	})
	var seen string
	mustRegister(t, c, interceptor.PhasePreRoute, &fakeInterceptor{
		name: "p180", priority: 180,
		fn: func(_ context.Context, req interceptor.Request) (interceptor.Result, error) {
			seen = string(req.Content)
			return interceptor.Result{Action: interceptor.ActionAllow}, nil
		},
	})
	res := c.RunRange(context.Background(), interceptor.Request{Phase: interceptor.PhasePreRoute, Content: []byte("orig")}, -2147483648, 300)
	if seen != "rewritten" {
		t.Errorf("downstream saw %q, want rewritten", seen)
	}
	if res.Action != interceptor.ActionModify || string(res.ModifiedContent) != "rewritten" {
		t.Errorf("segment result = %v / %q, want MODIFY / rewritten", res.Action, res.ModifiedContent)
	}
}

// spec: §4.8 line 12 — LenRange counts only interceptors whose priority
// falls in the window, so a caller can skip a RunRange that would select
// none.
func TestLenRangeCountsWindow_spec_4_8(t *testing.T) {
	c := interceptor.NewChain()
	mustRegister(t, c, interceptor.PhasePreRoute, &fakeInterceptor{name: "p150", priority: 150})
	mustRegister(t, c, interceptor.PhasePreRoute, &fakeInterceptor{name: "p300", priority: 300})
	mustRegister(t, c, interceptor.PhasePreRoute, &fakeInterceptor{name: "p350", priority: 350})
	if got := c.LenRange(interceptor.PhasePreRoute, -2147483648, 300); got != 1 {
		t.Errorf("below-pivot count = %d, want 1", got)
	}
	if got := c.LenRange(interceptor.PhasePreRoute, 300, 2147483647); got != 2 {
		t.Errorf("at-or-above-pivot count = %d, want 2", got)
	}
	if got := c.LenRange(interceptor.PhasePostRoute, -2147483648, 2147483647); got != 0 {
		t.Errorf("other-phase count = %d, want 0", got)
	}
}
