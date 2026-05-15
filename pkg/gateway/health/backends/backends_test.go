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
