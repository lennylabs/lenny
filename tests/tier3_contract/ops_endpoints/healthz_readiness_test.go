// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract test pinning the §25.4 permissive /healthz contract of
// lenny-ops: liveness, startup, and production readiness pass while at
// least one of Postgres and the Kubernetes API is reachable, and fail with
// a body naming the degraded dependencies when neither is.
package ops_endpoints_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/ops/probe"
)

// healthzResponse is the decoded §25.4 /healthz response body.
type healthzResponse struct {
	Status       string                    `json:"status"`
	Degraded     []string                  `json:"degraded"`
	Dependencies map[string]map[string]any `json:"dependencies"`
}

// getHealthz serves the opsserver over HTTP with the given dependency
// probes and returns the permissive /healthz status and decoded body.
func getHealthz(t *testing.T, probes map[string]probe.Func) (int, healthzResponse) {
	t.Helper()
	srv := httptest.NewServer(opsserver.New(opsserver.Options{Probes: probes}))
	t.Cleanup(srv.Close)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/healthz", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	var body healthzResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /healthz body: %v", err)
	}
	return resp.StatusCode, body
}

func reachable(context.Context) error   { return nil }
func unreachable(context.Context) error { return errors.New("connection refused") }

// TestHealthzPermissiveEitherDependencyContract pins the §25.4 permissive
// /healthz contract the chart's liveness, startup, and production
// readiness probes rely on.
//
// spec: §25.4 (The lenny-ops Service) — "/healthz (permissive): 200 if the
// process is alive and at least one of {Postgres, K8s API} is reachable.
// Used by liveness, startup, and readiness."
//
// diagnosis: the permissive /healthz returns 200 regardless of dependency
// state (so a production readiness probe on it never fails and a pod that
// reaches no dependency is never restarted), or returns 503 while one of
// Postgres and the Kubernetes API is still reachable (so a Postgres outage
// removes every replica from the Service), or its 503 body omits status,
// degraded, or dependencies.
func TestHealthzPermissiveEitherDependencyContract(t *testing.T) {
	cases := []struct {
		name     string
		postgres probe.Func
		k8s      probe.Func
		wantCode int
	}{
		{"both reachable", reachable, reachable, http.StatusOK},
		{"postgres down, kubernetes reachable", unreachable, reachable, http.StatusOK},
		{"postgres reachable, kubernetes down", reachable, unreachable, http.StatusOK},
		{"both down", unreachable, unreachable, http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := getHealthz(t, map[string]probe.Func{
				opsserver.ProbePostgresName: tc.postgres,
				opsserver.ProbeK8sAPIName:   tc.k8s,
			})
			if code != tc.wantCode {
				t.Fatalf("GET /healthz = %d, want %d", code, tc.wantCode)
			}
			if code == http.StatusOK {
				if body.Status != "ok" {
					t.Errorf("status = %q, want ok", body.Status)
				}
				return
			}
			assertDegradedHealthzBody(t, body)
		})
	}
}

// assertDegradedHealthzBody checks that a 503 /healthz body carries
// status, degraded naming both dependencies, and a dependency report for
// each.
func assertDegradedHealthzBody(t *testing.T, body healthzResponse) {
	t.Helper()
	if body.Status != "degraded" {
		t.Errorf("status = %q, want degraded", body.Status)
	}
	named := map[string]bool{}
	for _, d := range body.Degraded {
		named[d] = true
	}
	for _, dep := range []string{opsserver.ProbePostgresName, opsserver.ProbeK8sAPIName} {
		if !named[dep] {
			t.Errorf("degraded = %v, want it to name %s", body.Degraded, dep)
		}
		if _, ok := body.Dependencies[dep]; !ok {
			t.Errorf("dependencies = %v, want an entry for %s", body.Dependencies, dep)
		}
	}
}
