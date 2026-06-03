// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// spec: §25.2 line 169 — lenny-ops connects to MinIO; the §25.6
// connectivity report names it. The MinIO probe GETs the liveness
// endpoint and treats a non-5xx response as reachable. F-25.2.10.
func TestMinIOProbe_spec_25_2_169(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	// Reachable endpoint (httptest URLs carry the http:// scheme).
	if err := MinIOProbe(ts.Client(), ts.URL)(context.Background()); err != nil {
		t.Fatalf("MinIOProbe reachable = %v, want nil", err)
	}
	if gotPath != "/minio/health/live" {
		t.Errorf("probed path = %q, want /minio/health/live", gotPath)
	}

	// A bare host:port (no scheme) is assumed plain HTTP.
	bare := strings.TrimPrefix(ts.URL, "http://")
	if err := MinIOProbe(ts.Client(), bare)(context.Background()); err != nil {
		t.Errorf("MinIOProbe bare host:port = %v, want nil", err)
	}

	// Unconfigured endpoint reports not-configured rather than dialing.
	if err := MinIOProbe(ts.Client(), "")(context.Background()); err == nil {
		t.Errorf("MinIOProbe empty = nil, want a not-configured error")
	}
}

// spec: §25.2 line 169, §25.16 — lenny-ops connects to Prometheus; the
// probe GETs /-/healthy. A 5xx counts as a failure; an empty URL
// reports not-configured. F-25.2.10.
func TestPrometheusProbe_spec_25_2_169(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	if err := PrometheusProbe(ts.Client(), ts.URL)(context.Background()); err != nil {
		t.Fatalf("PrometheusProbe reachable = %v, want nil", err)
	}
	if gotPath != "/-/healthy" {
		t.Errorf("probed path = %q, want /-/healthy", gotPath)
	}

	// A trailing slash on the base URL does not double up the separator.
	if err := PrometheusProbe(ts.Client(), ts.URL+"/")(context.Background()); err != nil {
		t.Errorf("PrometheusProbe trailing-slash = %v, want nil", err)
	}

	if err := PrometheusProbe(ts.Client(), "")(context.Background()); err == nil {
		t.Errorf("PrometheusProbe empty = nil, want a not-configured error")
	}
}

// A 5xx from a dependency health endpoint is a probe failure: the
// dependency answered but is unhealthy. F-25.2.10.
func TestHTTPHealthProbeRejects5xx(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()
	if err := MinIOProbe(ts.Client(), ts.URL)(context.Background()); err == nil {
		t.Errorf("MinIOProbe against a 503 = nil, want an error")
	}
	if err := PrometheusProbe(ts.Client(), ts.URL)(context.Background()); err == nil {
		t.Errorf("PrometheusProbe against a 503 = nil, want an error")
	}
}
