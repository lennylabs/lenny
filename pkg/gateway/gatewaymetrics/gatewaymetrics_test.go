// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/gatewaymetrics"
)

func TestMetricsHandlerExposesRegisteredMetrics(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Drive one request so the request-counter + duration vecs have
	// a child series (a label-vec with no observations emits nothing
	// per the Prometheus exposition model). The gauge is registered
	// with a child series at construction and shows immediately.
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := m.Middleware(inner, func(*http.Request) string { return "/healthz" })
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"lenny_gateway_requests_total",
		"lenny_gateway_request_duration_seconds",
		"lenny_gateway_active_sessions",
		// §10.1 / §4.1 horizontal-scaling leading indicators are
		// registered with a child series at construction, so they
		// appear on /metrics immediately.
		"lenny_gateway_active_streams",
		"lenny_gateway_request_queue_depth",
		"lenny_gateway_rejection_rate",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q", want)
		}
	}
}

func TestHorizontalScalingGaugesExposeValues(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetActiveStreams(7)
	m.SetRequestQueueDepth(12)
	m.SetRejectionRate(3)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"lenny_gateway_active_streams 7",
		"lenny_gateway_request_queue_depth 12",
		"lenny_gateway_rejection_rate 3",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

func TestSetStorageQuotaExposesGauges(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetStorageQuota("acme", 500, 1000)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_storage_quota_bytes_used{tenant_id="acme"} 500`,
		`lenny_tenant_storage_quota_bytes{tenant_id="acme"} 1000`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

func TestCircuitBreakerMetricsExposeGauges(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetCircuitBreakerOpen("rt-emergency", true)
	m.SetCircuitBreakerOpen("rt-calm", false)
	m.SetCircuitBreakerCache(0, true)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_circuit_breaker_open{circuit_name="rt-emergency"} 1`,
		`lenny_circuit_breaker_open{circuit_name="rt-calm"} 0`,
		`lenny_circuit_breaker_cache_stale_seconds 0`,
		`lenny_circuit_breaker_cache_initialized 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

func TestMiddlewareRecordsRequests(t *testing.T) {
	m, _ := gatewaymetrics.New()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	h := m.Middleware(inner, func(*http.Request) string { return "/v1/sessions" })

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusCreated {
			t.Fatalf("inner status: %d", rr.Code)
		}
	}

	// The metrics endpoint should now report 3 requests with the
	// 2xx status class.
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, req)
	body := rr.Body.String()
	if !strings.Contains(body, `lenny_gateway_requests_total{method="POST",route="/v1/sessions",status_class="2xx"} 3`) {
		t.Errorf("requests_total not recorded as 3 2xx; body:\n%s", body)
	}
}

func TestMiddlewareLabelsStatusClass(t *testing.T) {
	m, _ := gatewaymetrics.New()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	h := m.Middleware(inner, func(*http.Request) string { return "/v1/sessions/{id}" })
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/x", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	mr := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	mrr := httptest.NewRecorder()
	m.Handler().ServeHTTP(mrr, mr)
	if !strings.Contains(mrr.Body.String(), `status_class="4xx"`) {
		t.Errorf("404 should be labelled 4xx; body:\n%s", mrr.Body.String())
	}
}

func TestSetActiveSessions(t *testing.T) {
	m, _ := gatewaymetrics.New()
	m.SetActiveSessions(42)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, req)
	if !strings.Contains(rr.Body.String(), "lenny_gateway_active_sessions 42") {
		t.Errorf("active sessions gauge not set; body:\n%s", rr.Body.String())
	}
}

// spec: §4.1 / §16.5 — the scalar configuration gauges are
// registered at construction so /metrics exposes them before the
// gateway main has called the setters. The
// `GatewayNoHealthyReplicas` and `GatewayActiveStreamsHigh` alert
// expressions read these via scalar(...); a missing child series
// resolves the scalar to NaN and the alert never fires.
func TestScalarGaugesRegisteredAtStartup(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"lenny_gateway_min_replicas 0",
		"lenny_gateway_stream_ceiling 0",
		"lenny_gateway_replica_count 0",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// spec: §4.1 / §16.5 — SetMinReplicas / SetStreamCeiling /
// SetReplicaCount drive the three scalar gauges referenced from the
// §16.5 alert rules. Each value must round-trip through /metrics so
// the scalar(...) lookups in the alert rules resolve to the
// configured operator value.
func TestSetScalarGaugesEmitsConfiguredValues(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetMinReplicas(5)
	m.SetStreamCeiling(400)
	m.SetReplicaCount(1)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"lenny_gateway_min_replicas 5",
		"lenny_gateway_stream_ceiling 400",
		"lenny_gateway_replica_count 1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// spec: §4.1 / §16.5 — concurrent setter invocations from the
// startup wiring path and a watchdog poller must not race or panic.
// The scalar gauges are plain prometheus.Gauge values; this test
// pins the no-panic property under concurrent writes.
func TestScalarGaugesAreSafeUnderConcurrentSetters(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	done := make(chan struct{})
	for i := 0; i < 16; i++ {
		go func(v int) {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 100; j++ {
				m.SetMinReplicas(v + j)
				m.SetStreamCeiling(v + j)
				m.SetReplicaCount(1)
			}
		}(i)
	}
	for i := 0; i < 16; i++ {
		<-done
	}
	// One terminal read; assert the gauges still emit cleanly.
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"lenny_gateway_min_replicas",
		"lenny_gateway_stream_ceiling",
		"lenny_gateway_replica_count",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q after concurrent writes", want)
		}
	}
}

// spec: §4.1 — lenny_gateway_max_sessions_per_replica is emitted at
// startup with delivery_mode labels so the §16.5
// GatewaySessionBudgetNearExhaustion alert has a denominator gauge.
func TestSetMaxSessionsPerReplicaEmitsBothDeliveryModes(t *testing.T) {
	m, _ := gatewaymetrics.New()
	m.SetMaxSessionsPerReplica("direct", 50)
	m.SetMaxSessionsPerReplica("proxy", 50)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_gateway_max_sessions_per_replica{delivery_mode="direct"} 50`,
		`lenny_gateway_max_sessions_per_replica{delivery_mode="proxy"} 50`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// spec: §4.1 SCL-026 — the metrics Middleware tracks in-flight
// requests so the HPA gauge exporter can read it through
// InflightRequests and publish to lenny_gateway_request_queue_depth.
func TestMiddlewareTracksInflightRequests(t *testing.T) {
	m, _ := gatewaymetrics.New()
	// While no handler is running, in-flight is 0.
	if got := m.InflightRequests(); got != 0 {
		t.Fatalf("InflightRequests() = %d at rest, want 0", got)
	}
	// Hold the handler so we can observe the in-flight count from
	// outside. The handler signals when it has incremented; the test
	// closes a channel to release it.
	started := make(chan struct{})
	release := make(chan struct{})
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	})
	h := m.Middleware(inner, func(*http.Request) string { return "/v1/test" })

	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
	}()
	<-started
	if got := m.InflightRequests(); got != 1 {
		t.Fatalf("InflightRequests() = %d while handler running, want 1", got)
	}
	close(release)
	<-done
	if got := m.InflightRequests(); got != 0 {
		t.Fatalf("InflightRequests() = %d after handler exit, want 0", got)
	}
}

func TestRecordElicitationDropExposesCounter(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.RecordElicitationDrop("budget_exceeded")
	m.RecordElicitationDrop("budget_exceeded")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "lenny_elicitation_dropped_total") {
		t.Fatalf("/metrics output missing lenny_elicitation_dropped_total:\n%s", body)
	}
	if !strings.Contains(body, `lenny_elicitation_dropped_total{reason="budget_exceeded"} 2`) {
		t.Errorf("/metrics output missing the budget_exceeded count of 2:\n%s", body)
	}
}

func TestRecordElicitationContentTamperDetectedExposesCounter(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.RecordElicitationContentTamperDetected("acme", "enforce")
	m.RecordElicitationContentTamperDetected("acme", "enforce")
	m.RecordElicitationContentTamperDetected("acme", "detect-only")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "lenny_elicitation_content_tamper_detected_total") {
		t.Fatalf("/metrics output missing lenny_elicitation_content_tamper_detected_total:\n%s", body)
	}
	if !strings.Contains(body, `lenny_elicitation_content_tamper_detected_total{enforcement_mode="enforce",tenant_id="acme"} 2`) {
		t.Errorf("/metrics output missing enforce-mode count of 2:\n%s", body)
	}
	if !strings.Contains(body, `lenny_elicitation_content_tamper_detected_total{enforcement_mode="detect-only",tenant_id="acme"} 1`) {
		t.Errorf("/metrics output missing detect-only count of 1:\n%s", body)
	}
}

func TestRecordExperimentIsolationRejectionExposesCounter(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.RecordExperimentIsolationRejection("acme", "exp_1", "treatment")
	m.RecordExperimentIsolationRejection("acme", "exp_1", "treatment")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	want := `lenny_experiment_isolation_rejections_total{experiment_id="exp_1",tenant_id="acme",variant_id="treatment"} 2`
	if !strings.Contains(body, want) {
		t.Errorf("/metrics output missing %q\n---\n%s", want, body)
	}
}

// spec: §15.1 GET /v1/sessions/{id}/events (SSE event stream)
// diagnosis: the §16.1 metrics middleware wraps the response writer
// in statusRecorder. When the wrapper does not forward http.Flusher,
// the SSE handler at pkg/gateway/sessionserver/events.go:50 fails its
// http.Flusher type assertion and returns 500 "response writer does
// not support streaming", breaking every streaming surface that
// passes through the middleware (SSE events, the §4.9 LLM-proxy
// streaming translators).
func TestMiddlewareForwardsFlusher(t *testing.T) {
	m, _ := gatewaymetrics.New()
	flushed := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("wrapper did not implement http.Flusher; SSE handlers will 500")
		}
		w.WriteHeader(http.StatusOK)
		f.Flush()
		flushed = true
	})
	h := m.Middleware(inner, func(*http.Request) string { return "/v1/sessions/{id}/events" })

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/x/events", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if !flushed {
		t.Fatal("inner handler did not reach Flush")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !rr.Flushed {
		t.Error("recorder reports the response was not flushed")
	}
}

// spec: §12.5 ll. 303 — the T4 fail-closed KMS-unavailable
// rejection emits to `lenny_checkpoint_storage_failure_total` with
// `reason="kms_unavailable"`. Existing retry-exhaustion calls stamp
// `reason="retry_exhausted"` so both flows aggregate into the same
// counter the `CheckpointStorageUnavailable` alert reads.
func TestCheckpointStorageFailureReasonLabel(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncCheckpointStorageFailure("pool-a", "full", "periodic")
	m.IncCheckpointKMSUnavailable()
	m.IncCheckpointKMSUnavailable()

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	for _, want := range []string{
		`lenny_checkpoint_storage_failure_total{level="full",pool="pool-a",reason="retry_exhausted",trigger="periodic"} 1`,
		`lenny_checkpoint_storage_failure_total{level="",pool="",reason="kms_unavailable",trigger=""} 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// spec: §12.5 ll. 341 — the hard-prune sweep increments the
// `lenny_gc_tombstones_pruned_total` counter once per row removed.
func TestGCTombstonesPrunedCounter(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.AddGCTombstonesPruned(0)  // no-op guard
	m.AddGCTombstonesPruned(-3) // no-op guard for negative input
	m.AddGCTombstonesPruned(4)
	m.AddGCTombstonesPruned(2)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	if !strings.Contains(body, "lenny_gc_tombstones_pruned_total 6") {
		t.Errorf("/metrics missing the expected counter value 6\n---\n%s", body)
	}
}

// spec: §12.5 line 321 — `lenny_gc_runs_total`,
// `lenny_gc_artifacts_deleted`, `lenny_gc_errors_total`, and
// `lenny_gc_duration_seconds` are emitted by the retention-GC sweep.
func TestGCRetentionMetricsEmit(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncGCRun("success")
	m.IncGCRun("error")
	m.AddGCArtifactsDeleted("artifacts", 3)
	m.AddGCArtifactsDeleted("transcripts", 2)
	m.IncGCError("artifacts")
	m.ObserveGCDuration(0.5)
	m.ObserveGCDuration(1.5)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	for _, want := range []string{
		`lenny_gc_runs_total{outcome="success"} 1`,
		`lenny_gc_runs_total{outcome="error"} 1`,
		`lenny_gc_artifacts_deleted{store="artifacts"} 3`,
		`lenny_gc_artifacts_deleted{store="transcripts"} 2`,
		`lenny_gc_errors_total{store="artifacts"} 1`,
		"lenny_gc_duration_seconds_count 2",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// spec: §12.5 line 291 — `lenny_drain_readiness_checks_total` records
// the webhook decision by outcome (allowed|blocked|forced).
func TestDrainReadinessCheckCounter(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncDrainReadinessCheck("allowed")
	m.IncDrainReadinessCheck("blocked")
	m.IncDrainReadinessCheck("forced")
	m.IncDrainReadinessCheck("allowed")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	for _, want := range []string{
		`lenny_drain_readiness_checks_total{outcome="allowed"} 2`,
		`lenny_drain_readiness_checks_total{outcome="blocked"} 1`,
		`lenny_drain_readiness_checks_total{outcome="forced"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// spec: §12.8 line 739 — `lenny_legal_hold_checkpoint_gaps_total`
// counts held sessions where the reconciler detects a checkpoint gap.
func TestLegalHoldCheckpointGapCounter(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncLegalHoldCheckpointGap("acme")
	m.IncLegalHoldCheckpointGap("acme")
	m.IncLegalHoldCheckpointGap("globex")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	for _, want := range []string{
		`lenny_legal_hold_checkpoint_gaps_total{tenant_id="acme"} 2`,
		`lenny_legal_hold_checkpoint_gaps_total{tenant_id="globex"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// spec: §12.5 line 282 — `lenny_artifact_upload_error_total` counts
// retry-exhausted PUT failures, labelled by tenant_id and error_type.
// The same call rolls into
// `lenny_checkpoint_storage_failure_total{reason=...}` so the
// MinIOUnavailable and CheckpointStorageUnavailable alerts fire from
// one source.
func TestArtifactUploadErrorCounter(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncArtifactUploadError("acme", "minio_unreachable")
	m.IncArtifactUploadError("acme", "auth")
	m.IncArtifactUploadError("acme", "quota_exceeded")
	m.IncArtifactUploadError("globex", "minio_unreachable")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	for _, want := range []string{
		`lenny_artifact_upload_error_total{error_type="minio_unreachable",tenant_id="acme"} 1`,
		`lenny_artifact_upload_error_total{error_type="auth",tenant_id="acme"} 1`,
		`lenny_artifact_upload_error_total{error_type="quota_exceeded",tenant_id="acme"} 1`,
		`lenny_artifact_upload_error_total{error_type="minio_unreachable",tenant_id="globex"} 1`,
		`lenny_checkpoint_storage_failure_total{level="",pool="",reason="minio_unreachable",trigger=""} 2`,
		`lenny_checkpoint_storage_failure_total{level="",pool="",reason="auth",trigger=""} 1`,
		`lenny_checkpoint_storage_failure_total{level="",pool="",reason="quota_exceeded",trigger=""} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// spec: §5.2 line 519 — lenny_slot_assignment_conflict_total is a
// per-pool counter of concurrent-mode slot-contention reservation
// failures, exposed on /metrics for the pool-under-sizing signal.
func TestSlotAssignmentConflictCounter(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncSlotAssignmentConflict("acme-agents")
	m.IncSlotAssignmentConflict("acme-agents")
	m.IncSlotAssignmentConflict("globex-agents")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	for _, want := range []string{
		`lenny_slot_assignment_conflict_total{pool="acme-agents"} 2`,
		`lenny_slot_assignment_conflict_total{pool="globex-agents"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// spec: §5.2 line 521 — lenny_slot_rehydration_total counts post-recovery
// slot-counter rehydration events, labeled by pod and pool.
func TestSlotRehydrationCounter(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncSlotRehydration("sbx-1", "acme-agents")
	m.IncSlotRehydration("sbx-2", "acme-agents")
	m.IncSlotRehydration("sbx-1", "acme-agents")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	for _, want := range []string{
		`lenny_slot_rehydration_total{pod="sbx-1",pool="acme-agents"} 2`,
		`lenny_slot_rehydration_total{pod="sbx-2",pool="acme-agents"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// A nil *Metrics is a no-op for the rehydration counter (the §5.2 hook
// is nil-safe when metrics are unwired).
func TestSlotRehydrationCounterNilSafe(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncSlotRehydration("sbx-1", "pool") // must not panic
}

// spec: §4.9 line 1220 — lenny_credential_preclaim_mismatch_total is a
// per-(pool,provider) counter of races where the pre-claim availability
// check passed but the lease assignment failed.
func TestCredentialPreclaimMismatchCounter(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncCredentialPreclaimMismatch("claude-prod", "anthropic_direct")
	m.IncCredentialPreclaimMismatch("claude-prod", "anthropic_direct")
	m.IncCredentialPreclaimMismatch("bedrock-prod", "aws_bedrock")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	for _, want := range []string{
		`lenny_credential_preclaim_mismatch_total{pool="claude-prod",provider="anthropic_direct"} 2`,
		`lenny_credential_preclaim_mismatch_total{pool="bedrock-prod",provider="aws_bedrock"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// A nil *Metrics must no-op rather than panic, matching the other
// counter helpers (the minimal gateway leaves metrics unwired).
func TestCredentialPreclaimMismatchNilSafe(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncCredentialPreclaimMismatch("p", "anthropic_direct") // must not panic
}

// spec: §16.1 lines 51, 53, 55, 97, 99, 100 and §5.2 line 12 — the
// credential, LLM-proxy, and slot-failure metrics register and emit
// through the gateway registry.
func TestCredentialAndLLMProxyAndSlotMetricsEmit(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncCredentialLeaseAssignment("anthropic_direct", "claude-prod", "primary")
	m.IncCredentialLeaseAssignment("anthropic_direct", "claude-prod", "primary")
	m.ObserveCredentialLeaseDuration("anthropic_direct", "claude-prod", 42)
	m.SetCredentialPoolUtilization("claude-prod", 0.5)
	m.IncLLMProxyConnections()
	m.DecLLMProxyConnections()
	m.ObserveLLMTranslation("claude-prod", "anthropic_direct", "anthropic", "request", 0.01)
	m.ObserveLLMTranslation("claude-prod", "anthropic_direct", "anthropic", "response", 0.02)
	m.IncLLMTranslationError("claude-prod", "anthropic_direct", "upstream_5xx")
	m.IncSlotFailure("session_start", "pool-a", "sbx-1")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`lenny_credential_lease_assignments_total{pool="claude-prod",provider="anthropic_direct",source="primary"} 2`,
		`lenny_credential_lease_duration_seconds_count{pool="claude-prod",provider="anthropic_direct"} 1`,
		`lenny_credential_pool_utilization{pool="claude-prod"} 0.5`,
		// Registered with a child series at construction, so the net-zero
		// gauge still appears on /metrics.
		"lenny_gateway_llm_proxy_active_connections 0",
		`lenny_gateway_llm_translation_duration_seconds_count{direction="request",pool="claude-prod",provider="anthropic_direct",proxy_dialect="anthropic"} 1`,
		`lenny_gateway_llm_translation_errors_total{error_type="upstream_5xx",pool="claude-prod",provider="anthropic_direct"} 1`,
		`lenny_slot_failure_total{error_type="session_start",k8s_pod_name="sbx-1",pool="pool-a"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q", want)
		}
	}
}

// A nil *Metrics no-ops on every new emitter rather than panicking.
func TestNewMetricsEmittersNilSafe(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncCredentialLeaseAssignment("anthropic_direct", "p", "primary")
	m.ObserveCredentialLeaseDuration("anthropic_direct", "p", 1)
	m.SetCredentialPoolUtilization("p", 0.5)
	m.IncLLMProxyConnections()
	m.DecLLMProxyConnections()
	m.ObserveLLMTranslation("p", "anthropic_direct", "anthropic", "request", 0.01)
	m.IncLLMTranslationError("p", "anthropic_direct", "upstream_5xx")
	m.IncSlotFailure("session_start", "p", "sbx-1")
	m.ObserveSessionStartupDuration("p", "runc", "standard", 1.0)
	m.ObserveSessionStartupPhase("pod_claim", "runc", 0.05)
}

// spec: §16.1 line 14 / §6.3 lines 348, 372 — the startup-latency
// histograms register and expose their series, the end-to-end metric
// carries the pool/runtime_class/isolation_profile labels, and the
// per-phase metric carries phase/runtime_class.
func TestSessionStartupMetricsExposed_spec_6_3(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ObserveSessionStartupDuration("pool-a", "runc", "standard", 1.3)
	m.ObserveSessionStartupDuration("pool-b", "gvisor", "sandboxed", 4.0)
	m.ObserveSessionStartupPhase("pod_claim", "runc", 0.05)
	m.ObserveSessionStartupPhase("agent_session_start", "gvisor", 4.2)

	body := scrapeMetrics(t, m)
	for _, want := range []string{
		`lenny_session_startup_duration_seconds_count{isolation_profile="standard",pool="pool-a",runtime_class="runc"} 1`,
		`lenny_session_startup_duration_seconds_count{isolation_profile="sandboxed",pool="pool-b",runtime_class="gvisor"} 1`,
		`lenny_session_startup_phase_duration_seconds_count{phase="pod_claim",runtime_class="runc"} 1`,
		`lenny_session_startup_phase_duration_seconds_count{phase="agent_session_start",runtime_class="gvisor"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q", want)
		}
	}
}

// spec: §16.5 lines 635-636 — the StartupLatency burn-rate alerts read
// the histogram's le="2" (runc, 2s SLO) and le="5" (gVisor, 5s SLO)
// bucket boundaries. The recorded buckets must carry exactly those le
// labels or the alert PromQL silently selects no series.
func TestSessionStartupDurationBucketBoundaries_spec_16_5(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ObserveSessionStartupDuration("pool-a", "runc", "standard", 0.5)

	body := scrapeMetrics(t, m)
	for _, le := range []string{`le="2"`, `le="5"`} {
		needle := `lenny_session_startup_duration_seconds_bucket{`
		found := false
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(line, needle) && strings.Contains(line, le) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("startup duration histogram has no bucket with %s; the StartupLatency alert expr would match no series", le)
		}
	}
}

// spec: §8.2 / §16.1 line 27 — lenny_delegation_depth histogram
// observation labelled by `pool`.
func TestObserveDelegationDepth_spec_8_2(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ObserveDelegationDepth("pool-a", 3)
	m.ObserveDelegationDepth("pool-a", 1)
	body := scrapeMetrics(t, m)
	if !strings.Contains(body, `lenny_delegation_depth_count{pool="pool-a"} 2`) {
		t.Errorf("expected lenny_delegation_depth_count for pool-a = 2, body=%q", body)
	}
	if !strings.Contains(body, `lenny_delegation_depth_sum{pool="pool-a"} 4`) {
		t.Errorf("expected lenny_delegation_depth_sum for pool-a = 4, body=%q", body)
	}
}

// spec: §8.2 line 70 / §16.1 line 79 —
// lenny_delegation_would_have_blocked_total carries (pool, tenant_id,
// layer, mode) labels.
func TestIncDelegationWouldHaveBlocked_spec_8_2(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncDelegationWouldHaveBlocked("pool-a", "acme", "platform", "enforce")
	m.IncDelegationWouldHaveBlocked("pool-a", "acme", "runtime", "warn")
	body := scrapeMetrics(t, m)
	for _, want := range []string{
		`lenny_delegation_would_have_blocked_total{layer="platform",mode="enforce",pool="pool-a",tenant_id="acme"} 1`,
		`lenny_delegation_would_have_blocked_total{layer="runtime",mode="warn",pool="pool-a",tenant_id="acme"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %q in /metrics, body=%q", want, body)
		}
	}
}

// spec: §8.2 / §16.1 — nil receivers are no-ops (caller-side guard).
func TestDelegationMetricsNilSafe_spec_8_2(t *testing.T) {
	var m *gatewaymetrics.Metrics
	// Must not panic.
	m.ObserveDelegationDepth("pool-a", 1)
	m.IncDelegationWouldHaveBlocked("pool-a", "acme", "policy", "enforce")
}

func scrapeMetrics(t *testing.T, m *gatewaymetrics.Metrics) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d", rr.Code)
	}
	return rr.Body.String()
}
