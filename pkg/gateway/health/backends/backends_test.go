// SPDX-License-Identifier: MIT

package backends_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/health"
	"github.com/lennylabs/lenny/pkg/gateway/health/backends"
)

// spec: §11.7 / §25.3 — the gateway health service reports the
// circuit-breaker cache as degraded while it is stale.

// fakeBreakerCache is an in-test backends.BreakerCache.
type fakeBreakerCache struct{ last time.Time }

func (f fakeBreakerCache) LastRefresh() time.Time { return f.last }

func TestCircuitBreakerCacheFreshIsHealthy(t *testing.T) {
	c := backends.CircuitBreakerCache(fakeBreakerCache{last: time.Now()}, "cb-cache")
	got := c.Check(context.Background())
	if got.Status != health.StatusHealthy {
		t.Errorf("a freshly-refreshed cache: status %q, want healthy", got.Status)
	}
}

func TestCircuitBreakerCacheStaleIsDegraded(t *testing.T) {
	c := backends.CircuitBreakerCache(fakeBreakerCache{last: time.Now().Add(-30 * time.Second)}, "cb-cache")
	got := c.Check(context.Background())
	if got.Status != health.StatusDegraded {
		t.Errorf("a 30s-stale cache: status %q, want degraded", got.Status)
	}
	if got.SuggestedAction == "" {
		t.Error("a degraded component must carry a §25.3 suggested action")
	}
}

func TestCircuitBreakerCacheBeforeFirstRefreshIsDegraded(t *testing.T) {
	c := backends.CircuitBreakerCache(fakeBreakerCache{}, "cb-cache")
	got := c.Check(context.Background())
	if got.Status != health.StatusDegraded {
		t.Errorf("before the first refresh: status %q, want degraded", got.Status)
	}
}

// fakeSIEM is an in-test SIEMForwarder + SIEMFailureRate.
type fakeSIEM struct {
	healthy bool
	rate    float64
}

func (f fakeSIEM) Healthy() bool        { return f.healthy }
func (f fakeSIEM) FailureRate() float64 { return f.rate }

// spec: §11.7 item 4 line 372 — below the configured failure threshold
// and with a healthy last delivery, the SIEM component is healthy.
func TestSIEMHealthyBelowThreshold(t *testing.T) {
	c := backends.SIEM(fakeSIEM{healthy: true, rate: 1}, fakeSIEM{rate: 1}, 5, "siem")
	got := c.Check(context.Background())
	if got.Status != health.StatusHealthy {
		t.Errorf("1%% failure rate under a 5%% threshold: status %q, want healthy", got.Status)
	}
}

// spec: §11.7 item 4 line 372 — when the delivery failure rate exceeds
// the threshold, the §25.3 health API reports the siem component
// degraded with an operability hint. F-11.7.16.
func TestSIEMDegradedAboveThreshold(t *testing.T) {
	c := backends.SIEM(fakeSIEM{healthy: true, rate: 12}, fakeSIEM{rate: 12}, 5, "siem")
	got := c.Check(context.Background())
	if got.Status != health.StatusDegraded {
		t.Errorf("12%% failure rate over a 5%% threshold: status %q, want degraded", got.Status)
	}
	if got.Issue != "AUDIT_SIEM_DELIVERY_DEGRADED" {
		t.Errorf("issue = %q, want AUDIT_SIEM_DELIVERY_DEGRADED", got.Issue)
	}
	if got.SuggestedAction == "" || got.RunbookRef == "" {
		t.Error("a degraded SIEM component must carry a §25.3 suggested action and runbook ref")
	}
}

// spec: §11.7 item 4 — a failed most-recent delivery (failure rate not
// yet over the window threshold) still reports degraded so a fresh
// outage surfaces immediately.
func TestSIEMDegradedWhenLastDeliveryFailed(t *testing.T) {
	c := backends.SIEM(fakeSIEM{healthy: false, rate: 0}, fakeSIEM{rate: 0}, 5, "siem")
	got := c.Check(context.Background())
	if got.Status != health.StatusDegraded {
		t.Errorf("unhealthy forwarder: status %q, want degraded", got.Status)
	}
}

// A zero threshold falls back to the §11.7 default of 5%.
func TestSIEMDefaultThreshold(t *testing.T) {
	c := backends.SIEM(fakeSIEM{healthy: true, rate: 6}, fakeSIEM{rate: 6}, 0, "siem")
	if got := c.Check(context.Background()); got.Status != health.StatusDegraded {
		t.Errorf("6%% with the default 5%% threshold: status %q, want degraded", got.Status)
	}
}
