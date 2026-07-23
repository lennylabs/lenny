// SPDX-License-Identifier: MIT

//go:build load_local

// Package singleshot_admission_ordering exercises the §11.1
// concurrency-and-rate-before-policy admission ordering that the built-in
// OpenAI-dialect single-shot create-and-start path now enforces, under
// concurrent load.
//
// The two built-in adapters route each request through the shared §15.2.1
// create-and-start service (BindSingleShot → CreateAndStartService), which
// runs the §11.1 concurrent-session cap and the §11.1 requests-per-minute
// admission-rate cap before the §4.8 policy chain, matching the two-step
// create path. §11.1 requires that an over-limit create reserve no rate or
// token budget: it is denied at the first breached gate, so no later gate
// runs.
//
// This scenario seeds the per-runtime concurrent-session scope to its cap,
// then drives many concurrent create-and-start requests through the
// single-shot binder. Every request is over-limit and is denied at the
// concurrency gate. The scenario asserts the ordering invariant on those
// denied creates: the admission-rate counter is never incremented (the
// over-limit create reserves no rate budget because it is denied before the
// admission-rate gate), and the §4.8 policy chain never runs (it reserves no
// token budget because it is denied before the policy chain). A regression
// that reordered the admission-rate gate or the policy chain ahead of the
// concurrency gate would increment the rate counter or invoke the chain on
// an over-limit create and fail this scenario, even though the create is
// still ultimately denied.
//
// TESTING.md §12.7.a regression scenarios.
package singleshot_admission_ordering

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	rlcounter "github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const (
	name       = "singleshot_admission_ordering"
	tenantID   = "acme"
	userID     = "alice@acme.com"
	runtimeRef = "echo"
)

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// Scenario drives concurrent over-limit create-and-start requests through
// the single-shot binder and pins the §11.1 concurrency-and-rate-before-policy
// ordering by observing that neither the admission-rate counter nor the
// policy chain runs on a create the concurrency gate denies.
//
// spec: §11.1 (concurrency, admission-rate, over-limit reserves no budget), §15.2.1 rule 1
// diagnosis: the built-in single-shot create-and-start path reordered the
// §11.1 admission-rate gate or the §4.8 policy chain ahead of the §11.1
// concurrency gate, reserving rate or token budget on an over-limit create
// that is ultimately denied; the shared §15.2.1 create-and-start service the
// built-in adapters route through no longer enforces the
// concurrency-and-rate-before-policy ordering.
type Scenario struct {
	counters *scenkit.Counters

	binder      singleShotBinder
	rate        *countingRateCounter
	policyCalls *atomic.Int64
}

// countingRateCounter wraps the in-memory §11.1 admission-rate counter and
// records every Incr. checkAdmissionScope calls Incr only when the request
// reaches the admission-rate gate; an over-limit create denied at the earlier
// concurrency gate must never reach it, so a non-zero count is a
// gate-ordering regression that reserved rate budget on a rejected create.
type countingRateCounter struct {
	inner *rlcounter.Memory
	incrs atomic.Int64
}

func (c *countingRateCounter) Incr(ctx context.Context, key string, now time.Time) (int, error) {
	c.incrs.Add(1)
	return c.inner.Incr(ctx, key, now)
}

// probeInterceptor is a §4.8 PostAuth interceptor that admits every request
// and counts its invocations. The policy chain runs (and the built-in
// QuotaEvaluator reserves §11.2 token budget) only when the create reaches
// requirePolicyChain; an over-limit create denied at the concurrency gate
// must never invoke it, so a non-zero count is a gate-ordering regression
// that reserved token budget on a rejected create.
type probeInterceptor struct{ calls *atomic.Int64 }

func (probeInterceptor) Name() string                       { return "singleshot-ordering-probe" }
func (probeInterceptor) Priority() int32                    { return 150 }
func (probeInterceptor) Builtin() bool                      { return false }
func (probeInterceptor) FailPolicy() interceptor.FailPolicy { return interceptor.FailClosed }
func (probeInterceptor) Timeout() time.Duration             { return 0 }
func (p probeInterceptor) Intercept(context.Context, interceptor.Request) (interceptor.Result, error) {
	p.calls.Add(1)
	return interceptor.Result{Action: interceptor.ActionAllow}, nil
}

// singleShotBinder mirrors the cmd/lenny-gateway wiring adapter: it maps a
// translator.SingleShotSpec into a sessionserver create-and-start request,
// runs the shared §15.2.1 service, and maps a typed *ServiceError into a
// *translator.SingleShotError. Driving the load through the same consumer
// interface the built-in adapters call keeps this scenario on the real path.
type singleShotBinder struct{ srv *sessionserver.Server }

var _ translator.SingleShotBinder = singleShotBinder{}

func (b singleShotBinder) BindSingleShot(ctx context.Context, spec translator.SingleShotSpec) (string, error) {
	resp, serr := b.srv.CreateAndStartService(ctx, spec.TenantID, sessionserver.CreateSessionRequest{
		RuntimeRef:  spec.RuntimeRef,
		UserID:      spec.UserID,
		Environment: spec.Environment,
	})
	if serr != nil {
		return "", &translator.SingleShotError{
			HTTPStatus:        serr.HTTPStatus,
			Code:              serr.Code,
			Message:           serr.Message,
			RetryAfterSeconds: serr.RetryAfterSeconds,
			Retryable:         serr.Retryable,
		}
	}
	return resp.ID, nil
}

func (s *Scenario) Name() string { return name }

func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 24, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	store := memstore.New()
	// Seed the §11.1 per-runtime concurrent-session scope to its cap of 1, so
	// every create-and-start against this runtime is over-limit and denied at
	// the concurrency gate before the admission-rate gate or the policy chain.
	now := time.Now().UTC()
	if err := store.Create(ctx, sessionstore.Session{
		ID:         "seed-echo-0",
		TenantID:   tenantID,
		UserID:     userID,
		RuntimeRef: runtimeRef,
		State:      session.StateRunning,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		return fmt.Errorf("seed running row: %w", err)
	}

	s.rate = &countingRateCounter{inner: rlcounter.NewMemory()}
	s.policyCalls = new(atomic.Int64)
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePostAuth, probeInterceptor{calls: s.policyCalls}); err != nil {
		return fmt.Errorf("register probe interceptor: %w", err)
	}

	// The admission-rate gate is armed (a per-runtime limit and a counter are
	// wired) so that a reordering regression would actually increment the
	// counter; the limit is set well above the load so the gate would admit,
	// not deny, if reached. The concurrency cap is the only gate that denies.
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                          func() string { return "sess-singleshot-ordering" },
		DefaultIsolationProfile:         isolation.ProfileSandboxed,
		MaxConcurrentSessionsPerRuntime: 1,
		AdmissionRateLimitCounter:       s.rate,
		PerRuntimePerMinute:             1_000_000,
		Interceptors:                    chain,
	})
	s.binder = singleShotBinder{srv: srv}
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	_, err := s.binder.BindSingleShot(ctx, translator.SingleShotSpec{
		TenantID:   tenantID,
		UserID:     userID,
		RuntimeRef: runtimeRef,
	})
	if err == nil {
		// The concurrency cap is at its limit, so no create may be admitted.
		s.counters.Inc("admitted_over_limit")
		return fmt.Errorf("over-limit create was admitted; the §11.1 concurrency cap failed open")
	}
	if scenkit.IsBenignCancel(ctx, err) {
		// A run-boundary cancellation is not a real denial; drop it.
		return nil
	}
	var sse *translator.SingleShotError
	if errors.As(err, &sse) && sse.Code == "QUOTA_EXCEEDED" && sse.HTTPStatus == http.StatusTooManyRequests {
		s.counters.Inc("denied_concurrency")
		return nil
	}
	s.counters.Inc("unexpected_denial")
	return fmt.Errorf("unexpected denial for an over-limit create: %w", err)
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	rateIncrs := s.rate.incrs.Load()
	policyRuns := s.policyCalls.Load()
	r.AddCustom("admission_rate_incrs", float64(rateIncrs))
	r.AddCustom("policy_chain_runs", float64(policyRuns))

	denied := s.counters.Get("denied_concurrency")
	if denied == 0 {
		return fmt.Errorf("scenario never exercised the over-limit create-and-start path")
	}
	if admitted := s.counters.Get("admitted_over_limit"); admitted != 0 {
		return fmt.Errorf("§11.1 concurrency cap failed open: %d over-limit creates admitted, want 0", admitted)
	}
	if unexpected := s.counters.Get("unexpected_denial"); unexpected != 0 {
		return fmt.Errorf("%d over-limit creates denied with an unexpected code, want the §11.1 QUOTA_EXCEEDED concurrency denial", unexpected)
	}
	// spec: §11.1; §15.2.1 rule 1 — an over-limit create reserves no rate
	// budget. The admission-rate gate runs after the concurrency gate on the
	// shared §15.2.1 create-and-start path, so a create the concurrency gate
	// denied must never increment the rate counter.
	if rateIncrs != 0 {
		return fmt.Errorf("§11.1 ordering violated: the admission-rate counter was incremented %d times on over-limit creates, want 0 (an over-limit create is denied before the admission-rate gate and reserves no rate budget)", rateIncrs)
	}
	// spec: §11.1; §15.2.1 rule 1 — an over-limit create reserves no token
	// budget. The §4.8 policy chain (and its built-in §11.2 QuotaEvaluator)
	// runs after the concurrency gate on the shared §15.2.1 create-and-start
	// path, so a create the concurrency gate denied must never invoke the
	// chain.
	if policyRuns != 0 {
		return fmt.Errorf("§11.1 ordering violated: the §4.8 policy chain ran %d times on over-limit creates, want 0 (an over-limit create is denied before the policy chain and reserves no token budget)", policyRuns)
	}
	return nil
}
