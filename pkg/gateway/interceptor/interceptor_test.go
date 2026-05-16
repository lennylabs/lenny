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
