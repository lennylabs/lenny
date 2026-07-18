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

// TestSelfHealthCheckNamesAreSnakeCase pins the §25.4 self-health check
// identifiers — the names carried in the GET /v1/admin/ops/health
// "checks" array and in the "triggering check" field of
// ops_health_status_changed events — to the snake_case convention the
// spec itself uses for them: "immediate evaluation of the `postgres_pool`
// check", "...`redis_consumer_lag`", "...`webhook_backlog` evaluation",
// and "...`k8s_api` evaluation" (§25.4 "Event-driven supplements",
// spec lines 2494-2497).
//
// This is a distinct namespace from the §25.3 gateway health API's
// component names, which the runbook `components:` front matter and
// §25.8's Cert-Manager Integration text document as camelCase
// (`postgres`, `redis`, `objectStore`, `certManager`, `gateway`,
// `credentialPools`, `controllers`, `circuitBreakers` — §25.7 "Runbook
// Authoring Guidelines", spec line 3032: "`components` — maps to the
// component names used in the health API response"). That "health API"
// is the gateway's GET /v1/admin/health (§25.3), a different endpoint
// from lenny-ops's own self-health surface here; the camelCase
// certManager component name belongs to it and is pinned separately by
// pkg/alerting/rules.HealthComponentCertManager. §25.4's self-health
// checks, including CheckCertManager's cert-manager probe, follow the
// snake_case convention documented in this section instead.
func TestSelfHealthCheckNamesAreSnakeCase_spec_25_4_2494(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"postgres pool", CheckPostgresPool},
		{"redis consumer lag", CheckRedisLag},
		{"webhook backlog", CheckWebhookBacklog},
		{"k8s api", CheckK8sAPI},
		{"memory pressure", CheckMemoryPressure},
		{"gateway auth", CheckGatewayAuth},
		{"cert manager", CheckCertManager},
	}
	// spec: §25.4 lines 2494-2497 name postgres_pool, redis_consumer_lag,
	// webhook_backlog, and k8s_api verbatim in snake_case; the sibling
	// checks (memory_pressure, gateway_auth, cert_manager) follow the same
	// convention within this self-health surface.
	for _, c := range cases {
		if c.want == "" {
			t.Errorf("%s: check name constant is empty", c.name)
			continue
		}
		for _, r := range c.want {
			if r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				continue
			}
			t.Errorf("%s: check name %q is not snake_case (spec §25.4 lines 2494-2497)", c.name, c.want)
			break
		}
	}
	if CheckCertManager != "cert_manager" {
		t.Errorf("CheckCertManager = %q, want %q (§25.4 self-health snake_case convention; the camelCase certManager name belongs to the distinct §25.3 gateway health API component list, §25.7 line 3032)", CheckCertManager, "cert_manager")
	}
}
