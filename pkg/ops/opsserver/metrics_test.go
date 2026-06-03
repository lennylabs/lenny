// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// stubMetricsHandler renders a fixed Prometheus exposition body so the
// route tests assert wiring without depending on the process default
// registry's live series.
func stubMetricsHandler(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte(body))
	})
}

// spec: §16.8 / §16.9 line 720 — lenny-ops serves the Prometheus scrape
// surface at GET /metrics, the mandatory §16.9 scrape target. The handler
// supplied via Options.Metrics is mounted there.
func TestMetricsRouteServesSuppliedHandler_spec_16_9(t *testing.T) {
	srv := opsserver.New(opsserver.Options{
		Metrics: stubMetricsHandler("lenny_ops_self_health_status{check=\"postgres_pool\"} 0\n"),
	})
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got == "" || got[:10] != "lenny_ops_" {
		t.Fatalf("metrics body = %q, want the supplied exposition", got)
	}
}

// spec: §16.9 — a nil Metrics handler defaults to the process default
// registry exposition, so even a dev/embedded build with no handler wired
// still answers a scrape rather than 404.
func TestMetricsRouteDefaultsToDefaultRegistry_spec_16_9(t *testing.T) {
	srv := opsserver.New(opsserver.Options{})
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// spec: §16.9 — the scrape endpoint is exempt from the §25.4 OIDC gate
// because a Prometheus scrape carries no bearer token; the §13.2 NET-045
// NetworkPolicy is the access control. With Auth configured but no bearer
// presented, /metrics still serves (200), unlike the operability routes
// which return 401.
func TestMetricsRouteIsAuthExempt_spec_16_9(t *testing.T) {
	signer := jwt.NewHMACSigner("ops-test", []byte("ops-test-secret"))
	srv := opsserver.New(opsserver.Options{
		Locks: coordination.NewMemStore(),
		Auth: &opsserver.AuthConfig{
			Options:     authmw.Options{Verifier: signer},
			RateLimiter: opsserver.NewRateLimiter(1000, 1000),
		},
		Metrics: stubMetricsHandler("lenny_ops_rate_limited_total 0\n"),
	})
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (auth-exempt); body=%s", rec.Code, rec.Body.String())
	}
}
