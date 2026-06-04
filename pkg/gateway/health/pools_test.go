// SPDX-License-Identifier: MIT

package health_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/alerting/rules"
	"github.com/lennylabs/lenny/pkg/gateway/health"
)

// poolFixture builds a FuncPoolHealthResolver whose pool exists with the
// given phase and whose firing alert set is fixed.
func poolFixture(phase string, last time.Time, firing ...string) health.FuncPoolHealthResolver {
	return health.FuncPoolHealthResolver{
		Pool: func(_ context.Context, name string) (string, time.Time, bool) {
			if name != "default-gvisor" {
				return "", time.Time{}, false
			}
			return phase, last, true
		},
		Firing: func() []string { return firing },
	}
}

// TestPoolHealthResolverResolvedAlert_spec_25_17_5254 covers the §25.17
// Step 6 recovery check: once the WarmPoolExhausted alert has resolved,
// GET /v1/admin/health/{pool} reports healthy with an empty activeAlerts
// list. spec: §25.17 line 5254.
func TestPoolHealthResolverResolvedAlert_spec_25_17_5254(t *testing.T) {
	r := poolFixture("active", time.Time{})
	ph, ok := r.PoolHealth(context.Background(), "default-gvisor")
	if !ok {
		t.Fatal("pool not resolved")
	}
	if ph.Pool != "default-gvisor" {
		t.Errorf("pool: %q", ph.Pool)
	}
	if ph.Status != string(health.StatusHealthy) {
		t.Errorf("status: %q, want healthy", ph.Status)
	}
	if len(ph.ActiveAlerts) != 0 {
		t.Errorf("activeAlerts: %v, want empty", ph.ActiveAlerts)
	}
}

// TestPoolHealthResolverFiringAlert_spec_25_17_5254 covers the pre-recovery
// state: a firing WarmPoolExhausted alert degrades the pool and appears in
// activeAlerts, while a co-firing non-pool alert is filtered out.
func TestPoolHealthResolverFiringAlert_spec_25_17_5254(t *testing.T) {
	r := poolFixture("active", time.Time{}, "WarmPoolExhausted", "RedisDown", "CredentialPoolExhausted")
	ph, ok := r.PoolHealth(context.Background(), "default-gvisor")
	if !ok {
		t.Fatal("pool not resolved")
	}
	if ph.Status != string(health.StatusDegraded) {
		t.Errorf("status: %q, want degraded", ph.Status)
	}
	if len(ph.ActiveAlerts) != 1 || ph.ActiveAlerts[0] != "WarmPoolExhausted" {
		t.Errorf("activeAlerts: %v, want [WarmPoolExhausted]", ph.ActiveAlerts)
	}
}

// TestPoolHealthResolverDrainingDegrades asserts a draining pool reports
// degraded even with no firing alert, and surfaces the drain timestamp.
func TestPoolHealthResolverDrainingDegrades(t *testing.T) {
	ts := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	r := poolFixture("draining", ts)
	ph, ok := r.PoolHealth(context.Background(), "default-gvisor")
	if !ok {
		t.Fatal("pool not resolved")
	}
	if ph.Phase != "draining" {
		t.Errorf("phase: %q", ph.Phase)
	}
	if ph.Status != string(health.StatusDegraded) {
		t.Errorf("status: %q, want degraded", ph.Status)
	}
	if !ph.LastTransition.Equal(ts) {
		t.Errorf("lastTransition: %v, want %v", ph.LastTransition, ts)
	}
}

// TestPoolHealthResolverUnknownPool asserts an unknown name does not
// resolve, so the handler can fall through to the 404 path.
func TestPoolHealthResolverUnknownPool(t *testing.T) {
	r := poolFixture("active", time.Time{})
	if _, ok := r.PoolHealth(context.Background(), "no-such-pool"); ok {
		t.Error("unknown pool resolved, want not-found")
	}
}

// TestPoolHealthResolverNilFiring asserts a nil Firing func (tracker
// disabled per §25.13) yields an empty, non-nil activeAlerts list.
func TestPoolHealthResolverNilFiring(t *testing.T) {
	r := health.FuncPoolHealthResolver{
		Pool: func(_ context.Context, _ string) (string, time.Time, bool) {
			return "active", time.Time{}, true
		},
	}
	ph, ok := r.PoolHealth(context.Background(), "default-gvisor")
	if !ok {
		t.Fatal("pool not resolved")
	}
	if ph.ActiveAlerts == nil {
		t.Error("activeAlerts is nil, want empty slice for JSON []")
	}
	if ph.Status != string(health.StatusHealthy) {
		t.Errorf("status: %q, want healthy", ph.Status)
	}
}

// TestHandlerResolvesPoolName_spec_25_17_5254 drives the §25.17 worked
// example end to end through the HTTP handler: GET /v1/admin/health/
// default-gvisor returns 200 with the pool health body when the name is
// not a registered subsystem. spec: §25.17 line 5254.
func TestHandlerResolvesPoolName_spec_25_17_5254(t *testing.T) {
	agg := health.NewAggregator()
	agg.Register(healthy("warmPools")) // the real §25.4 subsystem component
	resolver := poolFixture("active", time.Time{})

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/health/default-gvisor", nil)
	rr := httptest.NewRecorder()
	health.Handler(agg, resolver).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, want 200", rr.Code)
	}
	var ph health.PoolHealth
	if err := json.Unmarshal(rr.Body.Bytes(), &ph); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ph.Pool != "default-gvisor" || ph.Status != string(health.StatusHealthy) {
		t.Errorf("body: %+v", ph)
	}
}

// TestHandlerSubsystemTakesPrecedenceOverPool asserts a registered health
// subsystem still wins the {component} route even when a pool resolver is
// wired, so the §25.3 subsystem surface is unaffected.
func TestHandlerSubsystemTakesPrecedenceOverPool(t *testing.T) {
	agg := health.NewAggregator()
	agg.Register(failing("postgres", health.StatusUnhealthy))
	// A resolver that would resolve "postgres" as a pool, to prove the
	// subsystem path takes precedence.
	resolver := health.FuncPoolHealthResolver{
		Pool: func(_ context.Context, _ string) (string, time.Time, bool) {
			return "active", time.Time{}, true
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/health/postgres", nil)
	rr := httptest.NewRecorder()
	health.Handler(agg, resolver).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var comp health.Component
	if err := json.Unmarshal(rr.Body.Bytes(), &comp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if comp.Name != "postgres" || comp.Status != health.StatusUnhealthy {
		t.Errorf("expected subsystem component, got %+v", comp)
	}
}

// TestHandlerUnknownNameWithResolver404 asserts a name that is neither a
// subsystem nor a pool still returns the §25.3 line 547 404.
func TestHandlerUnknownNameWithResolver404(t *testing.T) {
	agg := health.NewAggregator()
	resolver := poolFixture("active", time.Time{}) // resolves only default-gvisor
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/health/missing", nil)
	rr := httptest.NewRecorder()
	health.Handler(agg, resolver).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d, want 404", rr.Code)
	}
}

// TestIsWarmPoolAlertAgainstCatalog guards the warm-pool alert classifier
// against the live §16.5 catalogue: every WarmPool*/Pool* alert is
// attributed to a pool, and credential-pool / connection-pool alerts are
// not. spec: §16.5; §25.17 line 5254.
func TestIsWarmPoolAlertAgainstCatalog(t *testing.T) {
	want := map[string]bool{
		"WarmPoolExhausted":         true,
		"WarmPoolLow":               true,
		"WarmPoolReplenishmentSlow": true,
		"PoolScalingAdmissionStuck": true,
		"PoolBootstrapMode":         true,
		"CredentialPoolExhausted":   false,
		"CredentialPoolLow":         false,
		"PgBouncerPoolSaturated":    false,
		"RedisDown":                 false,
	}
	for name, expect := range want {
		if got := health.IsWarmPoolAlert(name); got != expect {
			t.Errorf("IsWarmPoolAlert(%q) = %v, want %v", name, got, expect)
		}
	}

	// The catalogue must not contain a credential-pool or connection-pool
	// alert that the prefix test would mis-attribute to a warm pool.
	for _, rule := range rules.Catalog() {
		if health.IsWarmPoolAlert(rule.Name) {
			continue
		}
		// Sanity: nothing classified as non-warm-pool should start with
		// "WarmPool".
		if len(rule.Name) >= 8 && rule.Name[:8] == "WarmPool" {
			t.Errorf("rule %q starts with WarmPool but classifier excluded it", rule.Name)
		}
	}
}
