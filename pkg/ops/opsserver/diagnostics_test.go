// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/lennylabs/lenny/pkg/ops/diagnostics"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// fakeDiagSource is a test diagnostics DataSource returning fixed
// records.
type fakeDiagSource struct {
	session diagnostics.SessionRecord
	pool    diagnostics.PoolRecord
	creds   diagnostics.CredentialPoolRecord
}

func (f fakeDiagSource) Session(context.Context, string) (diagnostics.SessionRecord, error) {
	return f.session, nil
}

func (f fakeDiagSource) Pool(context.Context, string) (diagnostics.PoolRecord, error) {
	return f.pool, nil
}

func (f fakeDiagSource) CredentialPool(context.Context, string) (diagnostics.CredentialPoolRecord, error) {
	return f.creds, nil
}

func (f fakeDiagSource) Connectivity(context.Context) ([]diagnostics.ConnectivityDependency, error) {
	return nil, nil
}

func TestDiagnoseSessionEndpoint(t *testing.T) {
	src := fakeDiagSource{session: diagnostics.SessionRecord{
		SessionID: "sess-1", State: "failed", Runtime: "python", Pool: "default-gvisor",
		Signals: diagnostics.Signals{ExitCode: 137, OOMKilled: true}, Found: true,
	}}
	srv := opsserver.New(opsserver.Options{Diagnostics: diagnostics.NewService(src)})
	rec, body := doJSON(t, srv, http.MethodGet, "/v1/admin/diagnostics/sessions/sess-1", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	chain, _ := body["causeChain"].([]any)
	if len(chain) != 1 {
		t.Fatalf("cause chain has %d entries, want 1", len(chain))
	}
	first, _ := chain[0].(map[string]any)
	if first["category"] != "OOM_KILLED" {
		t.Errorf("cause category = %v, want OOM_KILLED", first["category"])
	}
}

func TestDiagnoseSessionNotFoundReturns404(t *testing.T) {
	srv := opsserver.New(opsserver.Options{
		Diagnostics: diagnostics.NewService(fakeDiagSource{session: diagnostics.SessionRecord{Found: false}}),
	})
	rec, body := doJSON(t, srv, http.MethodGet, "/v1/admin/diagnostics/sessions/sess-x", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "SESSION_NOT_FOUND" {
		t.Errorf("error code = %v, want SESSION_NOT_FOUND", errObj["code"])
	}
}

func TestDiagnosePoolEndpoint(t *testing.T) {
	src := fakeDiagSource{pool: diagnostics.PoolRecord{
		Name:    "default-gvisor",
		Signals: diagnostics.PoolSignals{ImagePullFailures: 2},
		Found:   true,
	}}
	srv := opsserver.New(opsserver.Options{Diagnostics: diagnostics.NewService(src)})
	rec, body := doJSON(t, srv, http.MethodGet, "/v1/admin/diagnostics/pools/default-gvisor", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	bottleneck, _ := body["bottleneck"].(map[string]any)
	if bottleneck == nil || bottleneck["category"] != "IMAGE_PULL" {
		t.Errorf("bottleneck = %v, want IMAGE_PULL", bottleneck)
	}
}

func TestDiagnoseCredentialPoolEndpoint(t *testing.T) {
	src := fakeDiagSource{creds: diagnostics.CredentialPoolRecord{
		Name: "anthropic", Utilization: 0.95, Found: true,
	}}
	srv := opsserver.New(opsserver.Options{Diagnostics: diagnostics.NewService(src)})
	rec, body := doJSON(t, srv, http.MethodGet, "/v1/admin/diagnostics/credential-pools/anthropic", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	if body["status"] != "unhealthy" {
		t.Errorf("status = %v, want unhealthy at 95%% utilization", body["status"])
	}
}

func TestDiagnosticsUnavailableWithoutService(t *testing.T) {
	srv := opsserver.New(opsserver.Options{})
	rec, _ := doJSON(t, srv, http.MethodGet, "/v1/admin/diagnostics/sessions/s", nil, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 with no diagnostic service configured", rec.Code)
	}
}

// TestDiagnosticsRequestDurationObserved covers §25.6 line 2926 — every
// diagnostic-endpoint call records a sample on the
// lenny_diagnostics_request_duration_seconds{endpoint} histogram.
func TestDiagnosticsRequestDurationObserved_spec_25_6_2926(t *testing.T) {
	src := fakeDiagSource{
		session: diagnostics.SessionRecord{SessionID: "sess-a", State: "running", Found: true},
		pool:    diagnostics.PoolRecord{Name: "pool-a", Found: true},
		creds:   diagnostics.CredentialPoolRecord{Name: "creds-a", Found: true},
	}
	svc := diagnostics.NewService(src)
	srv := opsserver.New(opsserver.Options{Diagnostics: svc})

	before := map[string]uint64{
		"session":         histCount(t, "session"),
		"pool":            histCount(t, "pool"),
		"credential-pool": histCount(t, "credential-pool"),
		"connectivity":    histCount(t, "connectivity"),
	}
	doJSON(t, srv, http.MethodGet, "/v1/admin/diagnostics/sessions/sess-a", nil, nil)
	doJSON(t, srv, http.MethodGet, "/v1/admin/diagnostics/pools/pool-a", nil, nil)
	doJSON(t, srv, http.MethodGet, "/v1/admin/diagnostics/credential-pools/creds-a", nil, nil)
	doJSON(t, srv, http.MethodGet, "/v1/admin/diagnostics/connectivity", nil, nil)
	for _, ep := range []string{"session", "pool", "credential-pool", "connectivity"} {
		if got := histCount(t, ep); got <= before[ep] {
			t.Errorf("endpoint %q: histogram count %d did not increment from %d", ep, got, before[ep])
		}
	}
}

// histCount returns the cumulative sample count on the §25.6 histogram
// for the named endpoint, gathered from the default Prometheus
// registry.
func histCount(t *testing.T, endpoint string) uint64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range families {
		if mf.GetName() != "lenny_diagnostics_request_duration_seconds" {
			continue
		}
		for _, m := range mf.GetMetric() {
			if labelValue(m, "endpoint") == endpoint {
				return m.GetHistogram().GetSampleCount()
			}
		}
	}
	return 0
}

func labelValue(m *dto.Metric, name string) string {
	for _, p := range m.GetLabel() {
		if p.GetName() == name {
			return p.GetValue()
		}
	}
	return ""
}
