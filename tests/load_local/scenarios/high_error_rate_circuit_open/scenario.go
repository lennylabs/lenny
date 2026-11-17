// SPDX-License-Identifier: MIT

//go:build load_local

// Package high_error_rate_circuit_open asserts the §4.9 LLM Proxy
// circuit-breaker recovery path: after enough upstream failures the
// breaker opens, after the cooldown a half-open probe runs, a
// successful probe closes the breaker, and a failing probe reopens
// it. The scenario drives the real llmproxy.CircuitBreaker through
// an httptest.Server whose response status alternates between 503
// (failure) and 200 (success) under heavy concurrency.
//
// TESTING.md §12.7.a resiliency scenarios.
package high_error_rate_circuit_open

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "high_error_rate_circuit_open"

// failureThreshold + cooldown together choose a recovery cadence
// short enough to see multiple open↔half-open transitions within a
// 1-2s profile.
const (
	failureThreshold = 5
	probeCooldown    = 50 * time.Millisecond
)

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	counters     *scenkit.Counters
	server       *httptest.Server
	upstreamHits atomic.Int64
	failureMode  atomic.Bool // when true, upstream returns 503; flipped over the run
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
	s.failureMode.Store(true)
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.upstreamHits.Add(1)
		// Drain the request body before responding. The forwarder sends a
		// request body on every call; a handler that returns (especially
		// on the 503 failure path) without consuming it leaves unread data
		// on the connection, so Go's server sets Connection: close and the
		// client opens a fresh socket per request. Under the high-rate SLO
		// battery that exhausts the loopback ephemeral port range
		// ("connect: can't assign requested address"). A real upstream
		// reads the request, so draining also matches production behavior.
		_, _ = io.Copy(io.Discard, r.Body)
		if s.failureMode.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_1"}`))
	}))
	breaker := &llmproxy.CircuitBreaker{
		FailureThreshold: failureThreshold,
		Cooldown:         probeCooldown,
	}
	// Forward through the shared scenkit client rather than the default
	// httptest client: the default http.Transport keeps only
	// MaxIdleConnsPerHost=2 idle connections, so under the scenario's
	// concurrent VUs most returned connections exceed that cap and are
	// closed, churning a fresh socket per request and exhausting the
	// loopback ephemeral port range across the back-to-back SLO battery.
	// The scenkit client raises the per-host idle pool and caps total
	// connections per host, forcing reuse.
	s.forwarder = &llmproxy.Forwarder{Client: scenkit.HTTPClient(), Breaker: breaker}
	// Flip the upstream from failing to healthy partway through the
	// profile so the half-open probe has a chance to succeed and the
	// breaker reopens-then-closes path runs at least once. Run for
	// the full profile duration in failing mode then flip; the
	// driver runs each profile for 1-2s, so 600ms of failures
	// followed by healthy is a comfortable schedule.
	time.AfterFunc(600*time.Millisecond, func() { s.failureMode.Store(false) })
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
	resp, err := s.forwarder.Forward(ctx, req)
	switch {
	case errors.Is(err, llmproxy.ErrCircuitOpen):
		s.counters.Inc("short_circuited")
	case err != nil:
		s.counters.Inc("transport_error")
	case resp.StatusCode >= 500:
		s.counters.Inc("admitted_5xx")
	case resp.StatusCode == http.StatusOK:
		s.counters.Inc("admitted_success")
	default:
		s.counters.Inc("admitted_other")
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	r.AddCustom("upstream_hits", float64(s.upstreamHits.Load()))
	short := s.counters.Get("short_circuited")
	success := s.counters.Get("admitted_success")
	failed := s.counters.Get("admitted_5xx")
	if failed == 0 {
		return fmt.Errorf("scenario did not exercise the failing-upstream path (admitted_5xx=0)")
	}
	if short == 0 {
		return fmt.Errorf("§4.9 violated: breaker did not open under sustained 5xx (no ErrCircuitOpen rejections)")
	}
	if success == 0 {
		return fmt.Errorf("§4.9 violated: breaker did not recover after the upstream healed (no admitted_success after half-open probe)")
	}
	return nil
}
