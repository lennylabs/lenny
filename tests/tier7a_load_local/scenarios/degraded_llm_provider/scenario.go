// SPDX-License-Identifier: MIT

//go:build load_local

// Package degraded_llm_provider asserts the §4.9 LLM Proxy circuit-
// breaker open-state behaviour: when the upstream provider is failing
// the breaker opens after the configured consecutive-failure count,
// and every subsequent call returns ErrCircuitOpen without dialing.
// The §4.9 contract is "all new LLM proxy requests are immediately
// rejected with PROVIDER_UNAVAILABLE; the gateway does NOT silently
// hang or wait for timeout."
//
// The scenario drives the real llmproxy.Forwarder + llmproxy.CircuitBreaker
// against an httptest.Server that always returns 503. Once the breaker
// trips, Forward() returns ErrCircuitOpen with no upstream dial; the
// assertion counts upstream requests and requires the count to plateau
// at the failure threshold (plus a small number of half-open probes).
//
// TESTING.md §12.7.a resiliency scenarios.
package degraded_llm_provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/llmproxy"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "degraded_llm_provider"

// failureThreshold is the consecutive-failure count that trips the
// breaker. A low value makes the open-state phase the dominant regime
// across the scenario.
const failureThreshold = 5

// breakerOpenLatencyCap is the upper bound on Forward()'s wall time
// when the breaker is open. The open path is one mutex acquire and
// one return, so any real I/O would blow past this.
const breakerOpenLatencyCap = 5 * time.Millisecond

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	counters     *scenkit.Counters
	server       *httptest.Server
	upstreamHits atomic.Int64
	forwarder    *llmproxy.Forwarder
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second}
}

func (s *Scenario) RampProfiles() []loadgen.Profile {
	return []loadgen.Profile{
		{Kind: loadgen.ConstantVU, VUs: 8, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 32, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 128, Duration: 1 * time.Second},
	}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.upstreamHits.Store(0)
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.upstreamHits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	breaker := &llmproxy.CircuitBreaker{
		FailureThreshold: failureThreshold,
		// Cooldown is long relative to a profile so the breaker stays
		// open for the rest of the run after it trips. A handful of
		// half-open probes may sneak through if the run runs long
		// enough; the assertion accounts for that.
		Cooldown: 30 * time.Second,
	}
	// Forward through the shared scenkit client rather than the default
	// httptest client, whose MaxIdleConnsPerHost=2 floor churns a fresh
	// socket per request under concurrent VUs and exhausts the loopback
	// ephemeral port range across the back-to-back SLO battery.
	s.forwarder = &llmproxy.Forwarder{Client: scenkit.HTTPClient(), Breaker: breaker}
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error {
	if s.server != nil {
		s.server.Close()
	}
	// Evict the shared client's idle connections to the now-closed test
	// server so they do not linger across the back-to-back battery.
	scenkit.CloseIdleConnections()
	return nil
}

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	req := &llmproxy.UpstreamRequest{
		URL:    s.server.URL,
		Body:   []byte(`{"model":"claude-3-5-sonnet"}`),
		Header: map[string]string{"x-api-key": "test"},
	}
	start := time.Now()
	resp, err := s.forwarder.Forward(ctx, req)
	elapsed := time.Since(start)

	switch {
	case errors.Is(err, llmproxy.ErrCircuitOpen):
		s.counters.Inc("rejected_breaker_open")
		if elapsed > breakerOpenLatencyCap {
			s.counters.Inc("slow_open_state_rejection")
		}
	case err != nil:
		// Transport failure — counted but not central to the §4.9
		// assertion (the in-process httptest server should not fail
		// transport-wise).
		s.counters.Inc("transport_error")
	case resp.StatusCode >= 500:
		s.counters.Inc("admitted_upstream_5xx")
	default:
		s.counters.Inc("admitted_unexpected_status")
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	hits := s.upstreamHits.Load()
	rejected := s.counters.Get("rejected_breaker_open")
	admitted := s.counters.Get("admitted_upstream_5xx")
	slow := s.counters.Get("slow_open_state_rejection")

	r.AddCustom("upstream_hits", float64(hits))

	if admitted == 0 {
		return fmt.Errorf("scenario did not exercise the upstream-failure path")
	}
	if rejected == 0 {
		return fmt.Errorf("§4.9 violated: breaker never opened (no ErrCircuitOpen rejections after %d upstream 5xx)", admitted)
	}
	// The §4.9 contract: open-state calls do not dial upstream. The
	// per-iteration ratio of upstream hits to total iterations must be
	// vanishingly small once the breaker is open. Closed-state hits
	// plus the at-trip-time in-flight over-shoot account for a small
	// constant; open-state hits are zero. Asserting the ratio captures
	// this without needing to know the in-flight count at trip time.
	totalIters := rejected + admitted + s.counters.Get("transport_error") + s.counters.Get("admitted_unexpected_status")
	if totalIters > 1000 && hits*100 > totalIters {
		return fmt.Errorf("§4.9 violated: %d upstream hits across %d iterations (>1%%) — open-state calls are dialing", hits, totalIters)
	}
	// Open-state rejection latency must stay below the cap on most
	// iterations. A single overshoot does not invalidate the contract
	// (scheduler stalls happen) but a measurable fraction does.
	if slow*20 > rejected {
		return fmt.Errorf("§4.9 violated: %d/%d open-state rejections exceeded %s (open path should not block)", slow, rejected, breakerOpenLatencyCap)
	}
	return nil
}
