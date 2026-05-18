// SPDX-License-Identifier: MIT

package opsservice

import "testing"

// TestWebhookBacklogCheckThresholds covers the §25.4 webhook_backlog
// boundaries: healthy at or below 100, degraded above 100, unhealthy
// above 500.
func TestWebhookBacklogCheckThresholds(t *testing.T) {
	cases := []struct {
		backlog int
		want    HealthStatus
	}{
		{0, StatusHealthy},
		{100, StatusHealthy},
		{101, StatusDegraded},
		{500, StatusDegraded},
		{501, StatusUnhealthy},
	}
	for _, c := range cases {
		got := WebhookBacklogCheck(func() int { return c.backlog })()
		if got.Status != c.want {
			t.Errorf("backlog %d: status = %s, want %s", c.backlog, got.Status, c.want)
		}
		if got.Name != CheckWebhookBacklog {
			t.Errorf("check name = %q, want %q", got.Name, CheckWebhookBacklog)
		}
	}
}

// TestMemoryPressureCheckThresholds covers the §25.4 memory_pressure
// boundaries against a fixed limit. The check reads the process's own
// runtime memory, so the test drives the boundary by limit size: a
// very large limit keeps the fraction low (healthy), a tiny limit
// pushes it past 95% (unhealthy).
func TestMemoryPressureCheckThresholds(t *testing.T) {
	// A zero limit disables the check.
	if got := MemoryPressureCheck(0)(); got.Status != StatusHealthy {
		t.Errorf("zero limit: status = %s, want healthy (check disabled)", got.Status)
	}
	// A 1 TiB limit dwarfs the test process — well under 80%.
	if got := MemoryPressureCheck(1 << 40)(); got.Status != StatusHealthy {
		t.Errorf("huge limit: status = %s, want healthy", got.Status)
	}
	// A 1 MiB limit is far below the process RSS — over 95%.
	if got := MemoryPressureCheck(1 << 20)(); got.Status != StatusUnhealthy {
		t.Errorf("tiny limit: status = %s, want unhealthy", got.Status)
	}
}

// TestPostgresPoolCheckNilPoolUnhealthy confirms a missing Postgres
// pool reports unhealthy, matching §25.4's treatment of an absent
// required dependency.
func TestPostgresPoolCheckNilPoolUnhealthy(t *testing.T) {
	got := PostgresPoolCheck(nil)()
	if got.Status != StatusUnhealthy {
		t.Errorf("nil pool: status = %s, want unhealthy", got.Status)
	}
	if got.Name != CheckPostgresPool {
		t.Errorf("check name = %q, want %q", got.Name, CheckPostgresPool)
	}
}

// TestRedisLagCheckDisconnectedUnhealthy confirms a nil Redis client
// reports unhealthy (consumer disconnected) per §25.4.
func TestRedisLagCheckDisconnectedUnhealthy(t *testing.T) {
	got := RedisLagCheck(nil, nil)()
	if got.Status != StatusUnhealthy {
		t.Errorf("nil client: status = %s, want unhealthy", got.Status)
	}
}

// TestK8sAPICheckNilClientUnhealthy confirms a missing Kubernetes
// client reports unhealthy per §25.4 (K8s API is a required
// dependency).
func TestK8sAPICheckNilClientUnhealthy(t *testing.T) {
	got := K8sAPICheck(nil)()
	if got.Status != StatusUnhealthy {
		t.Errorf("nil client: status = %s, want unhealthy", got.Status)
	}
	if got.Name != CheckK8sAPI {
		t.Errorf("check name = %q, want %q", got.Name, CheckK8sAPI)
	}
}
