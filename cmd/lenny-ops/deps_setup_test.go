// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/opsservice"
)

// spec: §25.6 lines 2905-2906 (Connectivity, CheckConnectivity data
// source) — "Additionally, it probes the gateway admin API itself (GET
// /v1/admin/health/summary) — if the gateway is unreachable, this
// appears in the connectivity report as a failed dependency."
//
// TestBuildDependenciesProbesGatewayHealthSummaryEndpoint pins the §25.6
// gateway connectivity probe to the spec-named admin endpoint. A probe
// aimed at the wrong path (for example the liveness-only /healthz) would
// still often report the gateway reachable (any non-5xx status passes
// opsservice.GatewayProbe), so the defect would not surface as a
// probe-failure regression; it must be caught by asserting the exact
// request path the probe issues.
//
// diagnosis: exercises the real cmd/lenny-ops composition-root wiring
// (buildDependencies) against an httptest.Server standing in for the
// gateway admin API, so the assertion covers the actual probe
// registered for a deployment rather than opsservice.GatewayProbe in
// isolation.
func TestBuildDependenciesProbesGatewayHealthSummaryEndpoint(t *testing.T) {
	var mu sync.Mutex
	var gotMethod, gotPath string

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotMethod, gotPath = r.Method, r.URL.Path
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := &opsFlags{
		postgresDSN:         strPtr(""),
		redisURL:            strPtr(""),
		redisSentinelAddrs:  strPtr(""),
		gatewayURL:          strPtr(srv.URL),
		backupMinIOEndpoint: strPtr(""),
		prometheusURL:       strPtr(""),
		runbookDir:          strPtr(""),
		leaderElectNS:       strPtr("lenny-system"),
		leaderLeaseDuration: intPtr(0),
		leaderRenewDeadline: intPtr(0),
		leaderRetryPeriod:   intPtr(0),
	}
	w := &opsWiring{f: f}
	w.buildDependencies()

	probeFn, ok := w.probes[opsservice.ProbeGateway]
	if !ok {
		t.Fatal("buildDependencies did not register the gateway probe for a configured --gateway-url")
	}
	if err := probeFn(context.Background()); err != nil {
		t.Fatalf("gateway probe against a healthy test server returned an error: %v", err)
	}

	mu.Lock()
	method, path := gotMethod, gotPath
	mu.Unlock()

	if method != http.MethodGet {
		t.Errorf("gateway probe method = %q, want GET", method)
	}
	if path != "/v1/admin/health/summary" {
		t.Errorf("gateway probe requested path %q, want the spec-named /v1/admin/health/summary", path)
	}
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }
