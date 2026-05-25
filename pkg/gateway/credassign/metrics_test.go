// SPDX-License-Identifier: MIT

package credassign_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
)

// spec: §16.1 lines 51, 53, 55 — the assignment service emits the
// credential lease-assignment counter (provider, pool, source), the
// lease-duration histogram (provider, pool), and the pool-utilization
// gauge (pool, in [0,1]).

type assignCall struct{ provider, pool, source string }

type durationCall struct {
	provider, pool string
	seconds        float64
}

// spyMetrics records every credassign.Metrics call for assertions.
type spyMetrics struct {
	assignments []assignCall
	durations   []durationCall
	utilization map[string]float64
}

func (s *spyMetrics) IncCredentialLeaseAssignment(provider, pool, source string) {
	s.assignments = append(s.assignments, assignCall{provider, pool, source})
}

func (s *spyMetrics) ObserveCredentialLeaseDuration(provider, pool string, seconds float64) {
	s.durations = append(s.durations, durationCall{provider, pool, seconds})
}

func (s *spyMetrics) SetCredentialPoolUtilization(pool string, ratio float64) {
	if s.utilization == nil {
		s.utilization = map[string]float64{}
	}
	s.utilization[pool] = ratio
}

func TestAssignEmitsLeaseAssignmentCounter_spec_16_1(t *testing.T) {
	svc, _, _ := newService(t)
	m := &spyMetrics{}
	svc.SetMetrics(m)
	svc.RegisterPool(proxyPool("claude-prod", credential.StrategyLeastLoaded,
		healthyCred("key-1", "sk-ant-real")))

	if _, err := svc.Assign("claude-prod", "s_1", ""); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if len(m.assignments) != 1 {
		t.Fatalf("assignments = %d, want 1", len(m.assignments))
	}
	got := m.assignments[0]
	// v1 has no §4.9 fallback chain, so the source is always "primary".
	if got.provider != string(credential.ProviderAnthropicDirect) || got.pool != "claude-prod" || got.source != "primary" {
		t.Errorf("assignment = %+v, want %s/claude-prod/primary", got, credential.ProviderAnthropicDirect)
	}
	// The pool's only credential is now in use: utilization 1.0.
	if m.utilization["claude-prod"] != 1.0 {
		t.Errorf("utilization = %v, want 1.0", m.utilization["claude-prod"])
	}
}

func TestReleaseObservesLeaseDuration_spec_16_1(t *testing.T) {
	svc, _, _ := newService(t)
	m := &spyMetrics{}
	svc.SetMetrics(m)
	svc.RegisterPool(proxyPool("claude-prod", credential.StrategyLeastLoaded,
		healthyCred("key-1", "sk-ant-real")))

	lease, err := svc.Assign("claude-prod", "s_1", "")
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	svc.Release(lease.LeaseID)

	if len(m.durations) != 1 {
		t.Fatalf("durations = %d, want 1", len(m.durations))
	}
	d := m.durations[0]
	if d.provider != string(credential.ProviderAnthropicDirect) || d.pool != "claude-prod" {
		t.Errorf("duration labels = %+v, want %s/claude-prod", d, credential.ProviderAnthropicDirect)
	}
	if d.seconds < 0 {
		t.Errorf("duration seconds = %v, want >= 0", d.seconds)
	}
	// Releasing the only outstanding lease frees the credential: 0.0.
	if m.utilization["claude-prod"] != 0 {
		t.Errorf("post-release utilization = %v, want 0", m.utilization["claude-prod"])
	}
}

func TestPoolUtilizationTracksInUseCredentials_spec_16_1(t *testing.T) {
	svc, _, _ := newService(t)
	m := &spyMetrics{}
	svc.SetMetrics(m)
	// Least-loaded spreads the first two assignments across both
	// credentials, so utilization climbs 0.5 → 1.0 and falls back on
	// release.
	svc.RegisterPool(proxyPool("claude-prod", credential.StrategyLeastLoaded,
		healthyCred("key-1", "sk-1"), healthyCred("key-2", "sk-2")))

	l1, err := svc.Assign("claude-prod", "s_1", "")
	if err != nil {
		t.Fatalf("Assign s_1: %v", err)
	}
	if m.utilization["claude-prod"] != 0.5 {
		t.Errorf("after 1 assign utilization = %v, want 0.5", m.utilization["claude-prod"])
	}
	if _, err := svc.Assign("claude-prod", "s_2", ""); err != nil {
		t.Fatalf("Assign s_2: %v", err)
	}
	if m.utilization["claude-prod"] != 1.0 {
		t.Errorf("after 2 assigns utilization = %v, want 1.0", m.utilization["claude-prod"])
	}
	svc.Release(l1.LeaseID)
	if m.utilization["claude-prod"] != 0.5 {
		t.Errorf("after release utilization = %v, want 0.5", m.utilization["claude-prod"])
	}
}

func TestNilMetricsIsNoOp_spec_16_1(t *testing.T) {
	svc, _, _ := newService(t)
	// No SetMetrics call: Assign and Release must not panic.
	svc.RegisterPool(proxyPool("claude-prod", credential.StrategyLeastLoaded,
		healthyCred("key-1", "sk-ant-real")))
	lease, err := svc.Assign("claude-prod", "s_1", "")
	if err != nil {
		t.Fatalf("Assign with nil metrics: %v", err)
	}
	svc.Release(lease.LeaseID)
}
