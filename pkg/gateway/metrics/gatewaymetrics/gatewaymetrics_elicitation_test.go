// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
)

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
	m.RecordElicitationContentTamperDetected("pod-origin", "pod-middle", "enforce")
	m.RecordElicitationContentTamperDetected("pod-origin", "pod-middle", "enforce")
	m.RecordElicitationContentTamperDetected("pod-origin", "pod-middle", "detect-only")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "lenny_elicitation_content_tamper_detected_total") {
		t.Fatalf("/metrics output missing lenny_elicitation_content_tamper_detected_total:\n%s", body)
	}
	if !strings.Contains(body, `lenny_elicitation_content_tamper_detected_total{enforcement_mode="enforce",origin_pod="pod-origin",tampering_pod="pod-middle"} 2`) {
		t.Errorf("/metrics output missing enforce-mode count of 2 with origin_pod/tampering_pod labels:\n%s", body)
	}
	if !strings.Contains(body, `lenny_elicitation_content_tamper_detected_total{enforcement_mode="detect-only",origin_pod="pod-origin",tampering_pod="pod-middle"} 1`) {
		t.Errorf("/metrics output missing detect-only count of 1 with origin_pod/tampering_pod labels:\n%s", body)
	}
	// §16.1 line 64 cardinality: tenant_id must NOT be a label on this
	// metric — the bounded labels are origin_pod and tampering_pod only.
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "lenny_elicitation_content_tamper_detected_total{") && strings.Contains(line, "tenant_id") {
			t.Errorf("tamper counter must not carry a tenant_id label (§16.1 line 64): %s", line)
		}
	}
}

// spec: §16.5 line 460 — the weakened-mode gauge is the standing
// ElicitationContentIntegrityWeakened alert numerator. It is exposed
// unlabelled and reports the count of active tenants whose effective
// §9.2 mode is weaker than enforce. F-9.2.5.
func TestSetElicitationIntegrityWeakenedExposesGauge(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetElicitationIntegrityWeakened(3)
	body := scrapeMetrics(t, m)
	if !strings.Contains(body, "lenny_elicitation_content_integrity_effective_mode_weaker_than_enforce 3") {
		t.Errorf("/metrics output missing weakened gauge value of 3:\n%s", body)
	}
	// The gauge resolves to zero once every tenant is on enforce; the
	// alert must clear, so the series must report exactly 0 (not absent).
	m.SetElicitationIntegrityWeakened(0)
	body = scrapeMetrics(t, m)
	if !strings.Contains(body, "lenny_elicitation_content_integrity_effective_mode_weaker_than_enforce 0") {
		t.Errorf("/metrics output missing weakened gauge value of 0 after resolve:\n%s", body)
	}
}

// TestElicitationLifecycleMetricsExposeCounters proves the §16.1
// lines 60-63 elicitation lifecycle metrics are registered and
// observable on /metrics: the in-flight gauge, the timeout counter,
// the suppressed counter, and the round-trip histogram. F-9.2.14.
//
// spec: §16.1 lines 60–63; §16.5 line 458 ElicitationBacklogHigh
// alert.
func TestElicitationLifecycleMetricsExposeCounters_spec_16_1_F_9_2_14(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// One admit, three drops, one round-trip observation.
	m.IncElicitationPending()
	m.IncElicitationPending()
	m.DecElicitationPending() // net pending = 1
	m.IncElicitationTimeout()
	m.IncElicitationTimeout()
	m.IncElicitationSuppressed()
	m.ObserveElicitationRoundtrip(45 * time.Second)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()

	for _, want := range []string{
		"lenny_elicitation_pending 1",
		"lenny_elicitation_timeout_total 2",
		"lenny_elicitation_suppressed_total 1",
		// Histogram count and sum lines.
		"lenny_elicitation_roundtrip_seconds_count 1",
		"lenny_elicitation_roundtrip_seconds_sum 45",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// TestElicitationLifecycleMetricsNilSafe proves the helpers are
// nil-safe so an absent metrics dependency does not panic. F-9.2.14.
func TestElicitationLifecycleMetricsNilSafe_spec_16_1_F_9_2_14(t *testing.T) {
	var m *gatewaymetrics.Metrics // nil
	m.IncElicitationPending()
	m.DecElicitationPending()
	m.IncElicitationTimeout()
	m.IncElicitationSuppressed()
	m.ObserveElicitationRoundtrip(1 * time.Second)
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
// `lenny_gc_tombstones_pruned_total{table}` counter once per row
// removed, labeled by the GC-managed row class it swept
// (`artifact_store` or `partial_manifest`).
func TestGCTombstonesPrunedCounter(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.AddGCTombstonesPruned("artifact_store", 0)  // no-op guard
	m.AddGCTombstonesPruned("artifact_store", -3) // no-op guard for negative input
	m.AddGCTombstonesPruned("artifact_store", 4)
	m.AddGCTombstonesPruned("artifact_store", 2)
	m.AddGCTombstonesPruned("partial_manifest", 5)

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	if !strings.Contains(body, `lenny_gc_tombstones_pruned_total{table="artifact_store"} 6`) {
		t.Errorf("/metrics missing the expected artifact_store counter value 6\n---\n%s", body)
	}
	if !strings.Contains(body, `lenny_gc_tombstones_pruned_total{table="partial_manifest"} 5`) {
		t.Errorf("/metrics missing the expected partial_manifest counter value 5\n---\n%s", body)
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

// spec: §12.8 lines 883/887 — the Phase 3.5 force-delete override
// counters. F-12.8.2, F-24.10.5.
func TestLegalHoldOverrideCounters_spec_12_8(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncLegalHoldEscrowRegionUnresolvable("acme")
	m.IncLegalHoldEscrowRegionUnresolvable("acme")
	m.IncLegalHoldOverriddenTenant("globex")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	for _, want := range []string{
		`lenny_legal_hold_escrow_region_unresolvable_total{tenant_id="acme"} 2`,
		`lenny_gdpr_legal_hold_overridden_tenant_total{tenant_id="globex"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}

	// Nil-safe: a non-gateway caller passing a nil *Metrics is a no-op.
	var nilM *gatewaymetrics.Metrics
	nilM.IncLegalHoldEscrowRegionUnresolvable("acme")
	nilM.IncLegalHoldOverriddenTenant("acme")
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

// spec: §16 line 66 — lenny_delegation_lease_extension_total is the §8.6
// per-decision counter labelled by tenant_id and outcome
// (approved/capped/denied). F-8.6.13.
func TestDelegationLeaseExtensionCounter_spec_16_line_66(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncDelegationLeaseExtension("acme", "approved")
	m.IncDelegationLeaseExtension("acme", "approved")
	m.IncDelegationLeaseExtension("acme", "denied")
	m.IncDelegationLeaseExtension("globex", "capped")

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()

	for _, want := range []string{
		`lenny_delegation_lease_extension_total{outcome="approved",tenant_id="acme"} 2`,
		`lenny_delegation_lease_extension_total{outcome="denied",tenant_id="acme"} 1`,
		`lenny_delegation_lease_extension_total{outcome="capped",tenant_id="globex"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}
}

// A nil *Metrics is a no-op for the delegation lease-extension counter so
// the leasecontrol path works even when metrics are unwired. F-8.6.13.
func TestDelegationLeaseExtensionCounterNilSafe(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncDelegationLeaseExtension("acme", "approved") // must not panic
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

// spec: §16.1 line 15 / §6.3 line 356 — the TTFT histogram registers
// and exposes its series under the
// pool/runtime_class/isolation_profile label triple.
func TestSessionTimeToFirstTokenExposed_spec_6_3_F_6_3_3(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ObserveSessionTimeToFirstToken("pool-a", "runc", "standard", 0.8)
	m.ObserveSessionTimeToFirstToken("pool-b", "gvisor", "sandboxed", 3.2)

	body := scrapeMetrics(t, m)
	for _, want := range []string{
		`lenny_session_time_to_first_token_seconds_count{isolation_profile="standard",pool="pool-a",runtime_class="runc"} 1`,
		`lenny_session_time_to_first_token_seconds_count{isolation_profile="sandboxed",pool="pool-b",runtime_class="gvisor"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q", want)
		}
	}
}

// spec: §16.5 line 637 / §6.3 line 356 — the TTFTBurnRate alert reads
// the histogram's le="10" (10s TTFT SLO) bucket boundary. The recorded
// buckets must carry exactly that le label or the alert PromQL silently
// selects no series.
func TestSessionTimeToFirstTokenBucketBoundary_spec_6_3_F_6_3_3(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ObserveSessionTimeToFirstToken("pool-a", "runc", "standard", 1.0)

	body := scrapeMetrics(t, m)
	needle := `lenny_session_time_to_first_token_seconds_bucket{`
	found := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, needle) && strings.Contains(line, `le="10"`) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("TTFT histogram has no bucket with le=\"10\"; the TTFTBurnRate alert expr would match no series")
	}
}

// spec: §6.3 line 352, §16.1 line 122 — lenny_warmpool_claims_total is
// emitted per pool/runtime_class so deployers can compute the §6.3
// SDK-warm demotion-rate ratio (denominator). The catalog declares the
// metric labels; the test confirms the production counter exposes the
// expected series.
func TestIncWarmpoolClaim_spec_6_3_F_6_3_6(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncWarmpoolClaim("pool-a", "runc")
	m.IncWarmpoolClaim("pool-a", "runc")
	m.IncWarmpoolClaim("pool-b", "gvisor")

	body := scrapeMetrics(t, m)
	for _, want := range []string{
		`lenny_warmpool_claims_total{pool="pool-a",runtime_class="runc"} 2`,
		`lenny_warmpool_claims_total{pool="pool-b",runtime_class="gvisor"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q", want)
		}
	}
}

// IncWarmpoolClaim is nil-safe so callers can pass a nil *Metrics
// without guarding (mirrors the pattern used by other emitters).
func TestIncWarmpoolClaimNilSafe_spec_6_3_F_6_3_6(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncWarmpoolClaim("pool-a", "runc") // must not panic
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
	// F-8.9.10 nil-safe coverage.
	m.IncDelegationTreeCycleDetected("acme", "rest")
}

// spec: §8.9 line 1003 / §16.1 — lenny_delegation_tree_cycle_detected_total
// carries (tenant_id, source) labels and increments once per repeated
// node hit by the tree walker. F-8.9.10.
func TestIncDelegationTreeCycleDetected_spec_8_9(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncDelegationTreeCycleDetected("acme", "rest")
	m.IncDelegationTreeCycleDetected("acme", "rest")
	m.IncDelegationTreeCycleDetected("acme", "mcp")
	m.IncDelegationTreeCycleDetected("globex", "rest")
	body := scrapeMetrics(t, m)
	for _, want := range []string{
		`lenny_delegation_tree_cycle_detected_total{source="rest",tenant_id="acme"} 2`,
		`lenny_delegation_tree_cycle_detected_total{source="mcp",tenant_id="acme"} 1`,
		`lenny_delegation_tree_cycle_detected_total{source="rest",tenant_id="globex"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %q in /metrics, body=%q", want, body)
		}
	}
}

// spec: §11.1 line 7 — lenny_rate_limit_rejected_total{scope} carries
// the §11.1 admission scope and bumps once per 429 rejection.
func TestIncRateLimitRejected_spec_11_1(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncRateLimitRejected("global")
	m.IncRateLimitRejected("user")
	m.IncRateLimitRejected("user")
	body := scrapeMetrics(t, m)
	for _, want := range []string{
		`lenny_rate_limit_rejected_total{scope="global"} 1`,
		`lenny_rate_limit_rejected_total{scope="user"} 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %q in /metrics, body=%q", want, body)
		}
	}
}

// spec: §16.5 RateLimitDegraded — the source gauge must flip 0→1 on
// SetRateLimitFailopenActive(true) and 1→0 on the recovery call so
// the alert resolves cleanly.
func TestSetRateLimitFailopenActive_spec_16_5(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body := scrapeMetrics(t, m)
	if !strings.Contains(body, "lenny_rate_limit_failopen_active 0") {
		t.Errorf("startup gauge sample = missing 0, body=%q", body)
	}
	m.SetRateLimitFailopenActive(true)
	body = scrapeMetrics(t, m)
	if !strings.Contains(body, "lenny_rate_limit_failopen_active 1") {
		t.Errorf("degraded gauge sample = missing 1, body=%q", body)
	}
	m.SetRateLimitFailopenActive(false)
	body = scrapeMetrics(t, m)
	if !strings.Contains(body, "lenny_rate_limit_failopen_active 0") {
		t.Errorf("recovery gauge sample = missing 0, body=%q", body)
	}
}

// spec: §11.1 line 7 — counter-failure counter is monotonic across
// the outage window so an operator can rate-aggregate even after the
// gauge edge has fired.
func TestIncRateLimitCounterFailure_spec_11_1(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncRateLimitCounterFailure()
	m.IncRateLimitCounterFailure()
	body := scrapeMetrics(t, m)
	if !strings.Contains(body, "lenny_rate_limit_counter_failure_total 2") {
		t.Errorf("expected counter_failure_total = 2, body=%q", body)
	}
}

// spec: §11.1 — nil receivers are no-ops.
func TestRateLimitMetricsNilSafe_spec_11_1(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncRateLimitRejected("global")
	m.SetRateLimitFailopenActive(true)
	m.IncRateLimitCounterFailure()
}

// TestStatelessMetricsRegistered_spec_5_2_573 covers the §5.2 line 573
// concurrent-stateless demand metrics — counter increment + gauge set,
// both labeled by pool, exposed on /metrics.
// spec: §16.1 lines 80-81 — lenny_export_file_scans_total (labelled
// pool, tenant_id, policy_name, interceptor_ref, outcome) and
// lenny_export_file_scan_duration_seconds (pool, tenant_id,
// interceptor_ref) are registered and emit. F-8.7.10.
func TestExportFileScanMetricsRegistered_spec_16_1_80(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncExportFileScan("orchestrator-pool", "acme", "orchestrator-policy", "export-scanner", "rejected")
	m.IncExportFileScan("orchestrator-pool", "acme", "orchestrator-policy", "export-scanner", "failed_open")
	m.ObserveExportFileScanDuration("orchestrator-pool", "acme", "export-scanner", 0.012)
	body := scrapeMetrics(t, m)
	for _, want := range []string{
		`lenny_export_file_scans_total{interceptor_ref="export-scanner",outcome="rejected",policy_name="orchestrator-policy",pool="orchestrator-pool",tenant_id="acme"} 1`,
		`lenny_export_file_scans_total{interceptor_ref="export-scanner",outcome="failed_open",policy_name="orchestrator-policy",pool="orchestrator-pool",tenant_id="acme"} 1`,
		`lenny_export_file_scan_duration_seconds_count{interceptor_ref="export-scanner",pool="orchestrator-pool",tenant_id="acme"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q", want)
		}
	}
}

func TestStatelessMetricsRegistered_spec_5_2_573(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncStatelessRequest("stateless-pool")
	m.IncStatelessRequest("stateless-pool")
	m.SetStatelessConcurrentActive("stateless-pool", 5)
	body := scrapeMetrics(t, m)
	for _, want := range []string{
		`lenny_service_requests_total{pool="stateless-pool"} 2`,
		`lenny_service_concurrent_active{pool="stateless-pool"} 5`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q", want)
		}
	}
}

// TestStatelessMetricsNilSafe_spec_5_2_573 confirms nil receivers do
// not panic for the stateless emitters — the producer (F-5.2.3) calls
// these from a hot path.
func TestStatelessMetricsNilSafe_spec_5_2_573(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.IncStatelessRequest("any")
	m.SetStatelessConcurrentActive("any", 7)
}

// spec: §6.2 line 179 — the lenny_adapter_leaked_slots gauge is per-pod
// (labeled pod_id, pool), set when a concurrent-workspace slot's cleanup
// does not reclaim it, and zeroed when the pod is drained for replacement.
func TestAdapterLeakedSlotsGauge_spec_6_2_179(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetAdapterLeakedSlots("pod-a", "default-gvisor", 2)
	body := scrapeMetrics(t, m)
	if want := `lenny_adapter_leaked_slots{pod_id="pod-a",pool="default-gvisor"} 2`; !strings.Contains(body, want) {
		t.Errorf("/metrics missing %q", want)
	}
	// The drain path zeroes the pod's series.
	m.SetAdapterLeakedSlots("pod-a", "default-gvisor", 0)
	body = scrapeMetrics(t, m)
	if want := `lenny_adapter_leaked_slots{pod_id="pod-a",pool="default-gvisor"} 0`; !strings.Contains(body, want) {
		t.Errorf("/metrics missing zeroed series %q", want)
	}
	// Nil receiver must not panic (the producer calls from the slot path).
	var nilM *gatewaymetrics.Metrics
	nilM.SetAdapterLeakedSlots("pod-a", "p", 1)
}

// TestSessionReuseHistogramRegistered_spec_5_2_569 covers the §5.2 / §16.1
// lenny_pod_session_reuse_count histogram registration + observation +
// the in-process SessionReuseQuantile helper the PoolScalingController
// consumes as mode_factor.
func TestSessionReuseHistogramRegistered_spec_5_2_569(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Two pods on the same pool: pod-1 retired after 4 sessions, pod-2
	// after 16. The cross-series median should be ~10 (the
	// linear-interpolation midpoint between 8 and 16 buckets).
	m.ObserveSessionReuseCount("tp", "pod-1", 4)
	m.ObserveSessionReuseCount("tp", "pod-2", 16)
	body := scrapeMetrics(t, m)
	if !strings.Contains(body, `lenny_pod_session_reuse_count_count{k8s_pod_name="pod-1",pool="tp"} 1`) {
		t.Errorf("/metrics missing pod-1 sample: %s", body)
	}
	med, ok := m.SessionReuseQuantile("tp", 0.5)
	if !ok {
		t.Fatal("SessionReuseQuantile reported !ok with observations recorded")
	}
	// With ExponentialBuckets(1, 2, 10) the buckets are 1,2,4,8,16,32,...
	// Pod-1 (4) lands in [2,4]; pod-2 (16) lands in [8,16]. Combined
	// cumulative counts: 4→1, 8→1, 16→2. Median at threshold 1 sits at
	// the 8 upper bound after interpolation.
	if med <= 0 {
		t.Errorf("SessionReuseQuantile returned non-positive median: %v", med)
	}
}

// TestSessionReuseQuantileBeforeObservation_spec_5_2_569 confirms a
// PoolScalingController querying the histogram before any observation
// sees ok=false — the bootstrap-mode fallback path.
func TestSessionReuseQuantileBeforeObservation_spec_5_2_569(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := m.SessionReuseQuantile("never-observed", 0.5); ok {
		t.Error("expected ok=false before any observation")
	}
}

// TestSessionReuseNilSafe_spec_5_2_569 confirms nil receivers do not panic.
func TestSessionReuseNilSafe_spec_5_2_569(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.ObserveSessionReuseCount("any", "pod", 5)
	if v, ok := m.SessionReuseQuantile("any", 0.5); ok || v != 0 {
		t.Errorf("nil receiver: got (%v, %v), want (0, false)", v, ok)
	}
}

// spec: §10.4 line 389 / §16 catalog — the gauge series exists at
// startup so /metrics never returns a missing series for the alert
// query. F-10.4.11.
func TestReplayBufferUtilizationExposedAtStartup_spec_10_4(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body := scrapeMetrics(t, m)
	if !strings.Contains(body, "lenny_event_bus_replay_buffer_utilization 0") {
		t.Errorf("/metrics missing replay-buffer utilization series at startup: %s", body)
	}
	m.SetReplayBufferUtilization(0.42)
	body = scrapeMetrics(t, m)
	if !strings.Contains(body, "lenny_event_bus_replay_buffer_utilization 0.42") {
		t.Errorf("/metrics did not reflect updated utilization: %s", body)
	}
}

// spec: §10.4 / §16.5 PDBBlockedEvictions — each increment surfaces on
// /metrics with the pdb and controller labels. F-10.4.4.
func TestIncPDBBlockedEvictions_spec_10_4(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.IncPDBBlockedEvictions("lenny-gateway", "poller")
	m.IncPDBBlockedEvictions("lenny-gateway", "poller")
	body := scrapeMetrics(t, m)
	if !strings.Contains(body, `lenny_pdb_blocked_evictions_total{controller="poller",pdb="lenny-gateway"} 2`) {
		t.Errorf("/metrics missing labelled PDB counter sample: %s", body)
	}
}

// Nil-receiver safety so a missing Metrics does not crash the watcher
// or the periodic poller. F-10.4.4 / F-10.4.11.
func TestReplayBufferAndPDBNilSafe_spec_10_4(t *testing.T) {
	var m *gatewaymetrics.Metrics
	m.SetReplayBufferUtilization(0.9)
	m.IncPDBBlockedEvictions("any", "poller")
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
