// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/alerting/evaluator"
	"github.com/lennylabs/lenny/pkg/alerting/inproceval"
	"github.com/lennylabs/lenny/pkg/alerting/rules"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/operability/health"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

// TestHealthDegradesFromInProcessRegistryWithoutPrometheus_spec_25_3_450
// pins the §25.3 differentiating claim that GET /v1/admin/health derives
// component status from the in-process alert tracker, which reads the
// gateway's own metric registry rather than querying Prometheus, so the
// verdict stays accurate when Prometheus is unreachable.
//
// The test wires the exact production health-derivation chain with no
// Prometheus anywhere in it: a real gatewaymetrics registry, inproceval
// reading that registry, the real §16.5 rule catalogue driven by the real
// evaluator, the real alertHealthSource adapter, and the real health
// Aggregator + Handler. It drives lenny_gateway_active_streams past 80% of
// the compiled-in stream ceiling so the warning-severity
// GatewayActiveStreamsHigh alert fires, then asserts /v1/admin/health
// reports the gateway component degraded with degradation.level=degraded
// and thresholdSource=compiled-in-defaults. A regression that made health
// depend on a Prometheus query (rather than the in-process registry) would
// see no alert firing here, because no Prometheus exists in this test, and
// the gateway component would stay healthy — failing the assertion.
//
// spec: §25.3 lines 436-451 — "The HealthService reads from three sources,
// none of which require Prometheus. 1. In-process metric registry ... No
// Prometheus query needed — this works even when Prometheus is down ...
// This means /v1/admin/health returns accurate results even when
// Prometheus itself is unreachable."
// diagnosis: a failure means the gateway's /v1/admin/health verdict no
// longer derives from the in-process alert tracker reading the in-process
// metric registry. Either the alert overlay stopped reading the tracker,
// the tracker stopped reading the in-process registry, or the derivation
// no longer maps a firing warning alert to a degraded component with the
// compiled-in-defaults envelope. Health would then silently depend on
// Prometheus and go blind whenever Prometheus is unreachable.
func TestHealthDegradesFromInProcessRegistryWithoutPrometheus_spec_25_3_450(t *testing.T) {
	// The in-process metric registry the gateway both exposes to
	// Prometheus and reads directly. No Prometheus client is constructed.
	gwMetrics, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("construct gateway metrics: %v", err)
	}

	// Drive lenny_gateway_active_streams past 80% of the ceiling so the
	// warning GatewayActiveStreamsHigh alert (mapped to the gateway
	// component) evaluates active against the in-process registry:
	// 9 / 10 = 0.9 > 0.80.
	gwMetrics.SetStreamCeiling(10)
	gwMetrics.SetActiveStreams(9)

	// The real per-replica in-process alert tracker: inproceval reads the
	// in-process registry snapshot, the evaluator runs the real §16.5
	// catalogue. This is the §25.13 Prometheus fallback path.
	ev := evaluator.New(rules.Catalog(), inproceval.New(gwMetrics.Gatherer()), evaluator.Options{})
	// GatewayActiveStreamsHigh has For=0, so a single tick where the
	// expression is active promotes pending → firing.
	if firing := ev.Tick(context.Background(), overlayBase); firing == 0 {
		t.Fatalf("no alert firing after driving active streams past the ceiling; the in-process evaluator did not read the registry")
	}
	if st, ok := ev.State("GatewayActiveStreamsHigh"); !ok || st != evaluator.StateFiring {
		t.Fatalf("GatewayActiveStreamsHigh state = (%q, %v), want (firing, true)", st, ok)
	}

	// The real alertHealthSource adapter over the real tracker.
	var ptr atomic.Pointer[evaluator.Evaluator]
	ptr.Store(ev)
	src := alertHealthSource{eval: &ptr}

	// The real health aggregator: the dependency probe for the gateway
	// component reports healthy, and only the in-process alert overlay
	// degrades it — proving the verdict came from the metric, not a stub.
	agg := health.NewAggregator()
	agg.Register(staticHealthy(rules.HealthComponentGateway))
	agg.SetAlertSource(src)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/health", nil)
	rr := httptest.NewRecorder()
	health.Handler(agg, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/admin/health: status %d, want 200 (never 5xx)", rr.Code)
	}

	var report health.Report
	if err := json.Unmarshal(rr.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode health report: %v", err)
	}

	// The gateway component is degraded because a warning alert derived
	// from the in-process registry is firing.
	var gateway *health.Component
	for i := range report.Components {
		if report.Components[i].Name == rules.HealthComponentGateway {
			gateway = &report.Components[i]
		}
	}
	if gateway == nil {
		t.Fatalf("health report has no gateway component: %+v", report.Components)
	}
	if gateway.Status != health.StatusDegraded {
		t.Errorf("gateway component status = %q, want degraded (GatewayActiveStreamsHigh firing from the in-process registry)", gateway.Status)
	}
	if report.Status != health.StatusDegraded {
		t.Errorf("aggregate status = %q, want degraded", report.Status)
	}

	// The envelope reflects the in-process tracker's compiled-in defaults,
	// the signal that the verdict did not come from operator-customized
	// Prometheus rules.
	if report.Degradation == nil {
		t.Fatal("health report carried no degradation envelope")
	}
	if report.Degradation.Level != conventions.DegradationDegraded {
		t.Errorf("degradation.level = %q, want degraded", report.Degradation.Level)
	}
	if report.Degradation.ThresholdSource != conventions.ThresholdSourceCompiledInDefaults {
		t.Errorf("degradation.thresholdSource = %q, want compiled-in-defaults",
			report.Degradation.ThresholdSource)
	}

	// Negative control: with active streams back under the threshold and a
	// fresh tick, the alert resolves and the gateway component returns to
	// healthy — the degraded verdict tracked the registry value, not a
	// constant overlay.
	gwMetrics.SetActiveStreams(1)
	ev.Tick(context.Background(), overlayBase.Add(time.Minute))
	req2 := httptest.NewRequest(http.MethodGet, "/v1/admin/health", nil)
	rr2 := httptest.NewRecorder()
	health.Handler(agg, nil).ServeHTTP(rr2, req2)
	var report2 health.Report
	if err := json.Unmarshal(rr2.Body.Bytes(), &report2); err != nil {
		t.Fatalf("decode second health report: %v", err)
	}
	if report2.Status != health.StatusHealthy {
		t.Errorf("aggregate status after active streams fell below the ceiling = %q, want healthy", report2.Status)
	}
}
