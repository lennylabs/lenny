// SPDX-License-Identifier: MIT

// Package gatewaymetrics registers the §16.1 gateway metrics and
// exposes the Prometheus `/metrics` scrape target. It composes the
// pkg/observability/metrics constructors (which enforce the §16.1.1
// label-hygiene rules) with a private prometheus.Registry so the
// gateway's metrics are isolated from the process-global default
// registry.
package gatewaymetrics

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds the registered §16.1 gateway metric vectors. The vectors are
// grouped into per-subsystem sub-structs (one per newXMetrics constructor)
// that Metrics embeds, so the emitter methods address each field by its
// promoted name. spec: §16 observability metrics.
type Metrics struct {
	reg *prometheus.Registry

	coreMetrics
	alertThresholdMetrics
	breakerUpgradeMetrics
	elicitationMetrics
	experimentMetrics
	erasureMetrics
	signingMetrics
	checkpointMetrics
	sessionLifecycleMetrics
	podClaimMetrics
	credentialMetrics
	podLifecycleMetrics
	retentionMetrics
	delegationMetrics
	quotaMetrics
	auditMetrics
	orphanMetrics
	miscMetrics

	// inflight tracks the number of HTTP requests currently being
	// handled by the §16.1 Middleware-wrapped mux. It is the source of
	// the lenny_gateway_request_queue_depth gauge (the §4.1 SCL-026
	// primary HPA scale-out trigger): incremented on entry and
	// decremented on exit so the watchdog poller's SetRequestQueueDepth
	// call reflects the instantaneous concurrent-request count.
	inflight int64
}

// New constructs and registers the gateway metric set against a
// fresh private registry.
func New() (*Metrics, error) {
	reg := prometheus.NewRegistry()
	m := &Metrics{reg: reg}
	var err error

	if m.coreMetrics, err = newCoreMetrics(reg); err != nil {
		return nil, err
	}
	if m.alertThresholdMetrics, err = newAlertThresholdMetrics(reg); err != nil {
		return nil, err
	}
	if m.breakerUpgradeMetrics, err = newBreakerUpgradeMetrics(reg); err != nil {
		return nil, err
	}
	if m.elicitationMetrics, err = newElicitationMetrics(reg); err != nil {
		return nil, err
	}
	if m.experimentMetrics, err = newExperimentMetrics(reg); err != nil {
		return nil, err
	}
	if m.erasureMetrics, err = newErasureMetrics(reg); err != nil {
		return nil, err
	}
	if m.signingMetrics, err = newSigningMetrics(reg); err != nil {
		return nil, err
	}
	if m.checkpointMetrics, err = newCheckpointMetrics(reg); err != nil {
		return nil, err
	}
	if m.sessionLifecycleMetrics, err = newSessionLifecycleMetrics(reg); err != nil {
		return nil, err
	}
	if m.podClaimMetrics, err = newPodClaimMetrics(reg); err != nil {
		return nil, err
	}
	if m.credentialMetrics, err = newCredentialMetrics(reg); err != nil {
		return nil, err
	}
	if m.podLifecycleMetrics, err = newPodLifecycleMetrics(reg); err != nil {
		return nil, err
	}
	if m.retentionMetrics, err = newRetentionMetrics(reg); err != nil {
		return nil, err
	}
	if m.delegationMetrics, err = newDelegationMetrics(reg); err != nil {
		return nil, err
	}
	if m.quotaMetrics, err = newQuotaMetrics(reg); err != nil {
		return nil, err
	}
	if m.auditMetrics, err = newAuditMetrics(reg); err != nil {
		return nil, err
	}
	if m.orphanMetrics, err = newOrphanMetrics(reg); err != nil {
		return nil, err
	}
	if m.miscMetrics, err = newMiscMetrics(reg); err != nil {
		return nil, err
	}

	return m, nil
}

// IncDelegationLeaseExtension records a §8.6 lease-extension
// decision against the §16 line 66
// `lenny_delegation_lease_extension_total` counter. The `outcome`
// label is the §8.6 line 743 audit classification
// (`approved`/`capped`/`denied`) — emitted by
// leasecontrol.Service alongside its audit record so the dashboard and
// the audit chain share one source of truth. spec: §16 line 66; §8.6
// line 743; F-8.6.13.
func (m *Metrics) IncDelegationLeaseExtension(tenantID, outcome string) {
	if m == nil {
		return
	}
	m.delegationLeaseExtension.WithLabelValues(tenantID, outcome).Inc()
}

// IncDelegationBudgetReconstruction records one §11.2 line 48 delegation
// tree budget reconstruction event. `outcome` is `success` (counters
// restored via the MAX rule) or `irrecoverable` (the tree root was moved
// to awaiting_client_action because the checkpoint was too stale and the
// live state could not be enumerated). spec: §11.2 line 48; §12.4 line
// 218; F-11.2.5.
func (m *Metrics) IncDelegationBudgetReconstruction(outcome string) {
	if m == nil {
		return
	}
	m.delegationBudgetReconstruction.WithLabelValues(outcome).Inc()
}

// IncQuotaCheckpointReconcile records one §11.2 line 48 / §24.6
// token-usage counter reconcile event. `outcome` is `restored` (the MAX
// rule was applied to a still-current window) or `skipped` (the
// checkpoint's window had already rolled over). spec: §11.2 line 48;
// §24.6 line 99; F-11.2.4 / F-24.6.3.
func (m *Metrics) IncQuotaCheckpointReconcile(outcome string) {
	if m == nil {
		return
	}
	m.quotaCheckpointReconcile.WithLabelValues(outcome).Inc()
}

// IncExportFileScan records one §8.7 PreExportMaterialization per-file
// scan outcome. `outcome` is one of admitted, modified, rejected,
// failed_open, or failed_closed. spec: §16.1 line 80; F-8.7.10.
func (m *Metrics) IncExportFileScan(pool, tenantID, policyName, interceptorRef, outcome string) {
	if m == nil {
		return
	}
	m.exportFileScans.WithLabelValues(pool, tenantID, policyName, interceptorRef, outcome).Inc()
}

// ObserveExportFileScanDuration records the per-file §8.7
// PreExportMaterialization interceptor latency in seconds. spec: §16.1
// line 81; F-8.7.10.
func (m *Metrics) ObserveExportFileScanDuration(pool, tenantID, interceptorRef string, seconds float64) {
	if m == nil {
		return
	}
	m.exportFileScanDuration.WithLabelValues(pool, tenantID, interceptorRef).Observe(seconds)
}

// ObserveInterceptorMTLSHandshake records a §10.3 NET-063
// gateway→in-cluster-interceptor TLS handshake outcome. `result` is one
// of success, san_mismatch, cert_expired, cert_missing, tls_error. spec:
// §10.3 line 332; §16.1 line 50; F-10.3.3.
func (m *Metrics) ObserveInterceptorMTLSHandshake(result string, seconds float64) {
	if m == nil {
		return
	}
	m.interceptorMTLSHandshake.WithLabelValues(result).Observe(seconds)
}

// IncStatelessRequest records a §5.2 service-mode-pool request that
// increments lenny_service_requests_total. `pool` is the SandboxTemplate
// name. spec: §5.2.
func (m *Metrics) IncStatelessRequest(pool string) {
	if m == nil {
		return
	}
	m.statelessRequests.WithLabelValues(pool).Inc()
}

// SetStatelessConcurrentActive sets the instantaneous concurrent active
// slot count for a service-mode pool, published as
// lenny_service_concurrent_active. spec: §5.2.
func (m *Metrics) SetStatelessConcurrentActive(pool string, value float64) {
	if m == nil {
		return
	}
	m.statelessConcurrentActive.WithLabelValues(pool).Set(value)
}

// ObserveSessionReuseCount records the served-session count of a retiring
// recycling session-mode pod into lenny_pod_session_reuse_count. `pool`
// is the SandboxTemplate name and `k8sPodName` is the pod whose
// retirement triggered the observation. spec: §5.2 / §16.1.
func (m *Metrics) ObserveSessionReuseCount(pool, k8sPodName string, count int) {
	if m == nil {
		return
	}
	m.sessionReuseCount.WithLabelValues(pool, k8sPodName).Observe(float64(count))
}

// SessionReuseQuantile reads the in-process median of the
// lenny_pod_session_reuse_count histogram for one pool. The
// PoolScalingController uses it as the mode-adjusted `mode_factor` for
// recycling session-mode pools (§5.2). q must be in (0,1]. ok is false
// until at least one observation has been recorded for the pool (cold
// start). spec: §5.2.
func (m *Metrics) SessionReuseQuantile(pool string, q float64) (value float64, ok bool) {
	if m == nil {
		return 0, false
	}
	if q <= 0 || q > 1 {
		return 0, false
	}
	families, err := m.reg.Gather()
	if err != nil {
		return 0, false
	}
	var totalCount uint64
	var buckets []bucketSample
	for _, fam := range families {
		if fam.GetName() != "lenny_pod_session_reuse_count" {
			continue
		}
		for _, mtr := range fam.GetMetric() {
			var match bool
			for _, lp := range mtr.GetLabel() {
				if lp.GetName() == "pool" && lp.GetValue() == pool {
					match = true
					break
				}
			}
			if !match {
				continue
			}
			h := mtr.GetHistogram()
			if h == nil {
				continue
			}
			totalCount += h.GetSampleCount()
			for _, b := range h.GetBucket() {
				buckets = append(buckets, bucketSample{ub: b.GetUpperBound(), count: b.GetCumulativeCount()})
			}
		}
	}
	if totalCount == 0 {
		return 0, false
	}
	// Buckets across pods are summed by upper bound; histogram_quantile
	// from Prometheus does the same accumulation pre-quantile.
	merged := mergeBuckets(buckets)
	if len(merged) == 0 {
		return 0, false
	}
	// totalCount equals merged[last].count when bucket UB is +Inf, which
	// promauto NewHistogram always includes; recompute defensively.
	totalCount = merged[len(merged)-1].count
	threshold := uint64(float64(totalCount) * q)
	for i, b := range merged {
		if b.count >= threshold {
			lower := 0.0
			if i > 0 {
				lower = merged[i-1].ub
			}
			lowerCount := uint64(0)
			if i > 0 {
				lowerCount = merged[i-1].count
			}
			band := float64(b.count - lowerCount)
			if band == 0 {
				return b.ub, true
			}
			frac := float64(threshold-lowerCount) / band
			return lower + (b.ub-lower)*frac, true
		}
	}
	return merged[len(merged)-1].ub, true
}

// bucketSample is one (upper_bound, cumulative_count) pair from a
// histogram sample. Used by SessionReuseQuantile to merge per-pod
// histograms before computing the in-process median. spec: §5.2.
type bucketSample struct {
	ub    float64
	count uint64
}

// mergeBuckets aggregates per-pod session-reuse bucket samples by upper
// bound, returning a sorted-by-UB slice with cumulative counts (across
// all pods that share the upper bound). The summed cumulative counts
// match Prometheus' histogram_quantile aggregation across series of
// the same histogram. spec: §5.2.
func mergeBuckets(in []bucketSample) []bucketSample {
	by := map[float64]uint64{}
	for _, b := range in {
		by[b.ub] += b.count
	}
	ubs := make([]float64, 0, len(by))
	for ub := range by {
		ubs = append(ubs, ub)
	}
	for i := 1; i < len(ubs); i++ {
		for j := i; j > 0 && ubs[j-1] > ubs[j]; j-- {
			ubs[j-1], ubs[j] = ubs[j], ubs[j-1]
		}
	}
	out := make([]bucketSample, 0, len(ubs))
	for _, ub := range ubs {
		out = append(out, bucketSample{ub: ub, count: by[ub]})
	}
	return out
}

// IncCheckpointOrphanedObjects increments the §4.4 line 248
// `lenny_checkpoint_orphaned_objects_total` counter labeled by pool
// and trigger. Called from the adapter Checkpoint RPC when a
// CheckpointAborter.AbortPartial call failed to delete a partially
// uploaded chunk object.
// spec: §4.4 line 248.
func (m *Metrics) IncCheckpointOrphanedObjects(pool, trigger string) {
	if m == nil {
		return
	}
	m.checkpointOrphanedObjects.WithLabelValues(pool, trigger).Inc()
}

// IncCheckpointSizeExceeded increments the §4.4 line 254
// `lenny_checkpoint_size_exceeded_total` counter labeled by pool and
// level. Called from the adapter Checkpoint RPC when the
// pre-checkpoint workspace-size probe rejects the run.
// spec: §4.4 line 254.
func (m *Metrics) IncCheckpointSizeExceeded(pool, level string) {
	if m == nil {
		return
	}
	m.checkpointSizeExceeded.WithLabelValues(pool, level).Inc()
}

// IncSessionEvictionTotalLoss increments the §4.4 lines 283–289
// `lenny_session_eviction_total_loss_total` counter labeled by pool
// and had_prior_checkpoint. Called from the eviction-fallback writer
// when both MinIO and Postgres failed for an eviction checkpoint.
// spec: §4.4 line 286.
func (m *Metrics) IncSessionEvictionTotalLoss(pool string, hadPriorCheckpoint bool) {
	if m == nil {
		return
	}
	label := "false"
	if hadPriorCheckpoint {
		label = "true"
	}
	m.sessionEvictionTotalLoss.WithLabelValues(pool, label).Inc()
}

// IncCheckpointEvictionPartialKeysLogged increments the §4.4 line 279
// `lenny_checkpoint_eviction_partial_keys_logged_total` counter
// labeled by pool and keys_committed ("0" for total-MinIO-failure, "1+"
// for partial-upload scenarios). Called from the eviction-fallback
// writer's WARN-log path before the total-loss orchestration fires.
// spec: §4.4 line 279.
func (m *Metrics) IncCheckpointEvictionPartialKeysLogged(pool, keysCommitted string) {
	if m == nil {
		return
	}
	m.checkpointEvictionPartialKeysLogged.WithLabelValues(pool, keysCommitted).Inc()
}

// IncPartialManifestCleanup increments the §4.4 line 236
// `lenny_partial_manifest_cleanup_total` counter labeled by outcome
// (`success`, `failed_deleted`, or `gc_collected`).
// spec: §4.4 line 236.
func (m *Metrics) IncPartialManifestCleanup(outcome string) {
	if m == nil {
		return
	}
	m.partialManifestCleanup.WithLabelValues(outcome).Inc()
}

// IncCheckpointPartialManifestsSuperseded increments the §10.1
// `lenny_checkpoint_partial_manifests_superseded_total` counter
// labeled by pool. Called once per prior partial manifest soft-deleted
// inside a supersede-on-write transaction.
// spec: §10.1 supersede-on-write.
func (m *Metrics) IncCheckpointPartialManifestsSuperseded(pool string) {
	if m == nil {
		return
	}
	m.checkpointPartialManifestsSuperseded.WithLabelValues(pool).Inc()
}

// ObserveCheckpointDuration observes one §4.4 line 254
// `lenny_checkpoint_duration_seconds` histogram value labeled by
// pool, level, and trigger. Called from the checkpointer at the
// end of every snapshot regardless of outcome. The §16.5
// CheckpointDurationHigh alert reads the P95 of this histogram.
// spec: §4.4 line 254.
func (m *Metrics) ObserveCheckpointDuration(pool, level, trigger string, seconds float64) {
	if m == nil {
		return
	}
	m.checkpointDuration.WithLabelValues(pool, level, trigger).Observe(seconds)
}

// ObserveSessionStartupDuration records the §6.3 line 348 end-to-end
// pod-warm startup latency (pod claim through agent session ready,
// excluding upload and workspace materialization) for a successful
// session start. The StartupLatencyBurnRate and
// StartupLatencyGVisorBurnRate alerts read the P95 of this histogram
// through the lenny_session_startup_duration_slow_ratio recording rule.
// spec: §16.1 line 14, §6.3 line 348.
func (m *Metrics) ObserveSessionStartupDuration(pool, runtimeClass, isolationProfile string, seconds float64) {
	if m == nil {
		return
	}
	m.sessionStartupDuration.WithLabelValues(pool, runtimeClass, isolationProfile).Observe(seconds)
}

// ObserveSessionStartupPhase records the §6.3 line 372 latency of one
// hot-path startup phase. phase is one of pod_claim,
// workspace_materialization, setup_commands, credential_assignment, or
// agent_session_start. Observed once per phase per successful start so
// the per-phase latency budget (§6.3 table) can be attributed.
// spec: §6.3 line 372.
func (m *Metrics) ObserveSessionStartupPhase(phase, runtimeClass string, seconds float64) {
	if m == nil {
		return
	}
	m.sessionStartupPhaseDuration.WithLabelValues(phase, runtimeClass).Observe(seconds)
}

// ObserveSessionTimeToFirstToken records the §6.3 line 356 / §16.1
// line 15 end-to-end TTFT: wall-clock seconds from session start
// request (POST /v1/sessions admission, i.e. session.CreatedAt) to
// the first agent-streamed response event emitted to the SSE client.
// Observed once per session — the first qualifying response event
// records, all subsequent events for the same session are ignored.
// The TTFTBurnRate alert reads the P95 of this histogram via an
// inline expression against the le="10" bucket (the 10s SLO
// threshold). spec: §6.3 line 356, §16.1 line 15.
func (m *Metrics) ObserveSessionTimeToFirstToken(pool, runtimeClass, isolationProfile string, seconds float64) {
	if m == nil {
		return
	}
	m.sessionTimeToFirstToken.WithLabelValues(pool, runtimeClass, isolationProfile).Observe(seconds)
}

// IncWarmpoolClaim increments the §6.3 line 352 / §16.1 line 122
// `lenny_warmpool_claims_total{pool,runtime_class}` counter on each
// idle→claimed transition in the §6.1 warm pool. This counter is the
// denominator of the §6.3 SDK-warm demotion-rate ratio
// (`lenny_warmpool_sdk_demotions_total / lenny_warmpool_claims_total`)
// that deployers must track to verify SDK-warm net benefit. The
// numerator (`lenny_warmpool_sdk_demotions_total`) is emitted by
// IncWarmpoolSDKDemotion on the §6.1 binder demotion path. spec: §6.3
// line 352, §16.1 line 122.
func (m *Metrics) IncWarmpoolClaim(pool, runtimeClass string) {
	if m == nil {
		return
	}
	m.warmpoolClaims.WithLabelValues(pool, runtimeClass).Inc()
}

// RecordSDKDemotion records one §6.1 SDK-warm demotion: it increments the
// §6.1 line 34 / §16.1 line 121 `lenny_warmpool_sdk_demotions_total{pool}`
// counter (the numerator of the §6.3 line 352 demotion-rate ratio over
// IncWarmpoolClaim) and observes the SDK teardown penalty into the §6.3
// line 352 `lenny_warmpool_sdk_demotion_duration_seconds{pool}` histogram.
// spec: §6.1 line 34, §6.3 line 352, §16.1 line 121.
func (m *Metrics) RecordSDKDemotion(pool string, teardownSeconds float64) {
	if m == nil {
		return
	}
	m.warmpoolSDKDemotions.WithLabelValues(pool).Inc()
	m.warmpoolSDKDemotionDuration.WithLabelValues(pool).Observe(teardownSeconds)
}

// IncSessionRetry increments the §16.1 / §7.3
// `lenny_session_retry_total{failure_class}` counter for one retry of
// a logical session. Each successful pod recovery (the v1 retry path,
// fired by sessionserver.bumpRecoveryGeneration) bumps it once. The
// failure_class label echoes the session's §7.1 FailureClass at retry
// time; the caller supplies "unknown" for a session with no recorded
// class so the label cardinality stays bounded by the §7.1 closed
// enum. spec: §7.3, §16.1. F-7.3.10.
func (m *Metrics) IncSessionRetry(failureClass string) {
	if m == nil {
		return
	}
	m.sessionRetryTotal.WithLabelValues(failureClass).Inc()
}

// IncDeriveFailureAudit increments the §16.1
// `lenny_session_derive_failure_audit_total{outcome}` counter for one
// §7.1 derive rule 2 derive_failure audit-row write. Outcome is
// "persisted", "fenced" (a coordinator handoff fenced the write out), or
// "error". spec: §7.1 derive rule 2, §16.1. F-15.1.14.
func (m *Metrics) IncDeriveFailureAudit(outcome string) {
	if m == nil {
		return
	}
	m.deriveFailureAudit.WithLabelValues(outcome).Inc()
}

// IncSessionResumeAttempt increments the §16.1 / §7.3
// `lenny_session_resume_attempts_total{pool, outcome}` counter for one
// POST /v1/sessions/{id}/resume call. Outcome is "success" when the
// row transitions to running or "failure" when the pod-claim step
// fails. The pool label echoes the session's §5.2 PoolRef at resume
// time (empty for a session whose pool was never resolved). spec:
// §7.3, §16.1. F-7.3.10.
func (m *Metrics) IncSessionResumeAttempt(pool, outcome string) {
	if m == nil {
		return
	}
	m.sessionResumeAttempts.WithLabelValues(pool, outcome).Inc()
}

// IncSessionExpiry increments the §16.1
// `lenny_session_expiry_total{pool, reason}` counter for one session the
// watchdog terminated on a platform expiry clock. reason is the §16.1.1
// vocabulary value the watchdog resolved from the expiry edge — "max_idle_time"
// for the §6.2 maxClientIdleSeconds idle clock or "max_session_age" for the
// §11.3 maxSessionAge age cap and the §7.3 awaiting_client_action wall-clock
// deadline. The pool label echoes the session's §5.2 PoolRef at expiry time
// (empty for a session whose pool was never resolved). spec: §16.1; §16.1.1.
// F-11.3.7.
func (m *Metrics) IncSessionExpiry(pool, reason string) {
	if m == nil {
		return
	}
	m.sessionExpiry.WithLabelValues(pool, reason).Inc()
}

// IncWarmpoolWarmupFailure increments the §16.1 line 124
// `lenny_warmpool_warmup_failure_total{error_type}` counter for one warm-
// pool startup failure. error_type is the §7.3 line 387 non-retryable
// failure category the gateway classified (`setup_command_failed`,
// `workspace_plan_invalid`, `runtime_image_pull_failed`,
// `network_policy_denied`). spec: §16.1 line 124, §7.3 line 387 — F-7.5.9.
func (m *Metrics) IncWarmpoolWarmupFailure(errorType string) {
	if m == nil {
		return
	}
	m.warmpoolWarmupFailure.WithLabelValues(errorType).Inc()
}

// IncInjectionGateFailClosed increments the
// `lenny_injection_gate_failclosed_total{cause}` counter once per §5.1
// injection-gate fail-closed occurrence. cause is "runtime_store" when the
// runtime-registry read failed and "override_store" when the per-tenant
// capability-override read failed, recording the granular backing-store
// cause behind the coarse SERVICE_UNAVAILABLE client code as a metric.
// spec: §5.1 (injection fail-closed), §15.1 (SERVICE_UNAVAILABLE) —
// F-5.1.20.
func (m *Metrics) IncInjectionGateFailClosed(cause string) {
	if m == nil {
		return
	}
	m.injectionGateFailClosed.WithLabelValues(cause).Inc()
}

// ObserveWorkspaceSealDuration records the §7.1 line 112
// `lenny_workspace_seal_duration_seconds{pool,outcome}` histogram for one
// terminal session's seal-and-export. outcome is "success" when the seal
// completed or "timeout" when the retry window was exhausted. The §16.5
// WorkspaceSealStuck alert fires on a nonzero outcome="timeout" count.
// spec: §7.1 line 112.
func (m *Metrics) ObserveWorkspaceSealDuration(pool, outcome string, seconds float64) {
	if m == nil {
		return
	}
	m.workspaceSealDuration.WithLabelValues(pool, outcome).Observe(seconds)
}

// IncCheckpointStorageFailure increments the §4.4 line 262
// `lenny_checkpoint_storage_failure_total` counter labeled by pool,
// level, and trigger. Called from the non-eviction MinIO-upload path
// when all retries are exhausted and the failed checkpoint is
// discarded. The fourth (`reason`) label is stamped
// `retry_exhausted` so the wider counter rolls up consistently with
// the §12.5 ll. 303 kms_unavailable rejection counted by
// IncCheckpointKMSUnavailable below.
// spec: §4.4 line 262.
func (m *Metrics) IncCheckpointStorageFailure(pool, level, trigger string) {
	if m == nil {
		return
	}
	m.checkpointStorageFailure.WithLabelValues(pool, level, trigger, "retry_exhausted").Inc()
}

// IncCheckpointKMSUnavailable increments the §12.5 ll. 303
// `lenny_checkpoint_storage_failure_total{reason="kms_unavailable"}`
// counter on a T4 fail-closed write rejection. The MinIO blobstore
// fires its SetOnKMSUnavailable callback whenever a Put rejects under
// ErrClassificationControlViolation; the gateway main wires the
// callback to this method so every rejection drives the
// CheckpointStorageUnavailable alert. The pool / level / trigger
// labels are left empty because the rejection happens before the
// adapter has the wider context.
//
// spec: §12.5 ll. 303.
func (m *Metrics) IncCheckpointKMSUnavailable() {
	if m == nil {
		return
	}
	m.checkpointStorageFailure.WithLabelValues("", "", "", "kms_unavailable").Inc()
}

// IncCheckpointTierStoreMismatch increments the §12.9 line 1048
// `lenny_checkpoint_storage_failure_total{reason="tier_store_mismatch"}`
// counter when a non-envelope-capable artifact store (the in-memory or
// §17.4 local-filesystem backend) rejects a T4 tenant's write because it
// cannot envelope-encrypt at rest. The store fires its
// SetOnTierStoreMismatch callback; the gateway main wires it here so the
// rejection drives the same CheckpointStorageUnavailable alert family as
// the KMS-unavailable case, with the reason label distinguishing them.
//
// spec: §12.9 line 1048; §15.1 line 1078.
func (m *Metrics) IncCheckpointTierStoreMismatch() {
	if m == nil {
		return
	}
	m.checkpointStorageFailure.WithLabelValues("", "", "", "tier_store_mismatch").Inc()
}

// AddGCTombstonesPruned bumps the §12.5 ll. 341
// `lenny_gc_tombstones_pruned_total{table}` counter by n. The §12.5
// hard-prune sweep emits the count of rows it removed for each table it
// sweeps; passing it through the gateway-side accessor keeps the metric
// registration centralised. table is `artifact_store` or
// `partial_manifest`.
//
// spec: §12.5 ll. 341.
func (m *Metrics) AddGCTombstonesPruned(table string, n int) {
	if m == nil || n <= 0 {
		return
	}
	m.gcTombstonesPruned.WithLabelValues(table).Add(float64(n))
}

// IncGCRun bumps the §12.5 ll. 321 `lenny_gc_runs_total` counter once
// per retention-GC sweep. outcome is `success` or `error`.
//
// spec: §12.5 line 321.
func (m *Metrics) IncGCRun(outcome string) {
	if m == nil {
		return
	}
	m.gcRuns.WithLabelValues(outcome).Inc()
}

// AddGCArtifactsDeleted bumps the §12.5 ll. 321
// `lenny_gc_artifacts_deleted` counter by n for the named per-store
// adapter. Called once per sweep per per-store result.
//
// spec: §12.5 line 321.
func (m *Metrics) AddGCArtifactsDeleted(store string, n int) {
	if m == nil || n <= 0 {
		return
	}
	m.gcArtifactsDeleted.WithLabelValues(store).Add(float64(n))
}

// IncGCError bumps the §12.5 ll. 321 `lenny_gc_errors_total` counter
// once per per-store adapter error observed in a sweep.
//
// spec: §12.5 line 321.
func (m *Metrics) IncGCError(store string) {
	if m == nil {
		return
	}
	m.gcErrors.WithLabelValues(store).Inc()
}

// ObserveGCDuration records the §12.5 ll. 321 retention-GC sweep
// duration. Called once per Tick regardless of outcome.
//
// spec: §12.5 line 321.
func (m *Metrics) ObserveGCDuration(seconds float64) {
	if m == nil {
		return
	}
	m.gcDuration.Observe(seconds)
}

// IncDrainReadinessCheck bumps the §12.5 ll. 291
// `lenny_drain_readiness_checks_total` counter once per webhook
// decision. outcome is `allowed`, `blocked`, or `forced`.
//
// spec: §12.5 line 291.
func (m *Metrics) IncDrainReadinessCheck(outcome string) {
	if m == nil {
		return
	}
	m.drainReadinessChecks.WithLabelValues(outcome).Inc()
}

// IncLegalHoldCheckpointGap bumps the §12.8 ll. 739
// `lenny_legal_hold_checkpoint_gaps_total` counter once per held
// session whose checkpoint sequence carries a gap.
//
// spec: §12.8 line 739.
func (m *Metrics) IncLegalHoldCheckpointGap(tenantID string) {
	if m == nil {
		return
	}
	m.legalHoldCheckpointGaps.WithLabelValues(tenantID).Inc()
}

// IncLegalHoldEscrowRegionUnresolvable bumps the §12.8 line 883
// `lenny_legal_hold_escrow_region_unresolvable_total` counter when a
// Phase 3.5 force-delete override aborts because the tenant's escrow
// region has no configuration. The §16.5 LegalHoldEscrowResidencyViolation
// alert reads it.
//
// spec: §12.8 line 883.
func (m *Metrics) IncLegalHoldEscrowRegionUnresolvable(tenantID string) {
	if m == nil {
		return
	}
	m.legalHoldEscrowRegionUnresolvable.WithLabelValues(tenantID).Inc()
}

// IncLegalHoldOverriddenTenant bumps the §12.8 line 887
// `lenny_gdpr_legal_hold_overridden_tenant_total` counter once per
// tenant-scope force-delete legal-hold override. The §16.5
// LegalHoldOverrideUsedTenant warning alert reads it.
//
// spec: §12.8 line 887.
func (m *Metrics) IncLegalHoldOverriddenTenant(tenantID string) {
	if m == nil {
		return
	}
	m.legalHoldOverriddenTenant.WithLabelValues(tenantID).Inc()
}

// IncArtifactUploadError bumps the §12.5 ll. 282
// `lenny_artifact_upload_error_total` counter on every
// ArtifactStore PUT that exhausts its retry budget (the storage
// backend's onArtifactUploadError callback). errorType is one of
// `minio_unreachable`, `auth`, `quota_exceeded`, `other`, matching
// the §16.5 alert's selector vocabulary; the
// `minio_unreachable` value also drives the
// `lenny_checkpoint_storage_failure_total{reason="minio_unreachable"}`
// rollup so the CheckpointStorageUnavailable alert fires from the
// same signal.
//
// spec: §12.5 line 282; §16.5 ArtifactUploadError.
func (m *Metrics) IncArtifactUploadError(tenantID, errorType string) {
	if m == nil {
		return
	}
	m.artifactUploadError.WithLabelValues(tenantID, errorType).Inc()
	// Roll the upload-error class into the checkpoint storage
	// failure rollup so a single counter feeds both alerts. The pool/
	// level/trigger labels are blank since the failure originates in
	// the blob store before the higher-level context is available.
	m.checkpointStorageFailure.WithLabelValues("", "", "", errorType).Inc()
}

// IncRateLimitRejected increments the §11.1 line 7
// `lenny_rate_limit_rejected_total` counter for one 429 admission
// rejection. scope is `global` or `user`. spec: §11.1 line 7.
func (m *Metrics) IncRateLimitRejected(scope string) {
	if m == nil {
		return
	}
	m.rateLimitRejected.WithLabelValues(scope).Inc()
}

// SetRateLimitFailopenActive flips the §16.5 RateLimitDegraded source
// gauge. The ratelimit middleware sets 1 once a counter error is
// observed and 0 once the next Incr succeeds, so the alert reflects
// the live degraded state. spec: §16.5 RateLimitDegraded; §11.1 line 7.
func (m *Metrics) SetRateLimitFailopenActive(active bool) {
	if m == nil {
		return
	}
	v := 0.0
	if active {
		v = 1
	}
	m.rateLimitFailopenActive.Set(v)
}

// IncRateLimitCounterFailure increments the §11.1
// `lenny_rate_limit_counter_failure_total` counter. The middleware
// calls this on every Incr error so an operator can rate-aggregate
// counter outages even when the failopen-active gauge is pinned to 1.
// spec: §11.1 line 7.
func (m *Metrics) IncRateLimitCounterFailure() {
	if m == nil {
		return
	}
	m.rateLimitCounterFailure.Inc()
}

// SetQuotaFailopenCumulativeSeconds records the §12.4 line 224 cumulative
// fail-open timer onto its gauge. The failopen.CumulativeTimer calls this
// on every fail-open transition so the §16.5 QuotaFailOpenCumulativeThreshold
// alert sees the live value. spec: §12.4 line 224; §16.1 / §16.5.
func (m *Metrics) SetQuotaFailopenCumulativeSeconds(seconds float64) {
	if m == nil {
		return
	}
	m.quotaFailopenCumulativeSeconds.Set(seconds)
}

// SetQuotaUserFailopenFraction exports the configured
// quotaUserFailOpenFraction. The gateway sets it once at startup so the
// §16.5 QuotaFailOpenUserFractionInoperative warning fires for a weakened
// (>= 0.5) per-user fail-open cap. spec: §12.4 line 222; §16.1 / §16.5.
func (m *Metrics) SetQuotaUserFailopenFraction(fraction float64) {
	if m == nil {
		return
	}
	m.quotaUserFailopenFraction.Set(fraction)
}

// SetDualStoreUnavailable flips the §10.1 line 45 DualStoreUnavailable
// source gauge. The dual-store monitor sets 1 the moment it declares
// both Postgres and Redis unreachable and clears to 0 once at least one
// store recovers, so the §16.5 alert reflects the live degraded state.
// spec: §10.1 line 45.
func (m *Metrics) SetDualStoreUnavailable(unavailable bool) {
	if m == nil {
		return
	}
	v := 0.0
	if unavailable {
		v = 1
	}
	m.dualStoreUnavailable.Set(v)
}

// IncIdempotencyCacheWriteFailure increments
// `lenny_idempotency_cache_write_failures_total{tenant_id}` once per
// §11.5 cache Put failure. The middleware calls this when the inner
// handler already executed (the client already got the response) and
// the durable store rejected the cache row, so the next retry with the
// same key WILL re-execute the operation. spec: §11.5 line 277;
// F-11.5.4.
func (m *Metrics) IncIdempotencyCacheWriteFailure(tenantID string) {
	if m == nil {
		return
	}
	m.idempotencyCacheWriteFailures.WithLabelValues(tenantID).Inc()
}

// IncIdempotencyCacheSkipped increments
// `lenny_idempotency_cache_skipped_total{tenant_id,reason}` once per
// §11.5 cache write the middleware declined by policy. reason is one
// of: "server_error" (inner-handler 5xx). spec: §11.5 line 277;
// F-11.5.3.
func (m *Metrics) IncIdempotencyCacheSkipped(tenantID, reason string) {
	if m == nil {
		return
	}
	m.idempotencyCacheSkipped.WithLabelValues(tenantID, reason).Inc()
}

// ObserveDelegationDepth records a §8.2 delegation-admission depth
// observation onto the `lenny_delegation_depth` histogram labeled by
// `pool`. spec: §8.2; §16.1 line 27.
func (m *Metrics) ObserveDelegationDepth(pool string, depth int) {
	if m == nil {
		return
	}
	m.delegationDepth.WithLabelValues(pool).Observe(float64(depth))
}

// IncDelegationWouldHaveBlocked increments the §8.2 three-layer-gate
// `lenny_delegation_would_have_blocked_total` counter for one
// (`layer`, `mode`) attribution row. Callers emit one row per failing
// layer: under `mode: enforce` this attributes the rejection causes
// for a rejected hop; under `mode: warn` it records the
// would-have-blocked layers for an admitted hop. The counter is not
// emitted under `mode: permissive` (the caller decides; this helper
// is unconditional). spec: §8.2 line 70; §16.1 line 79.
func (m *Metrics) IncDelegationWouldHaveBlocked(pool, tenantID, layer, mode string) {
	if m == nil {
		return
	}
	m.delegationWouldHaveBlocked.WithLabelValues(pool, tenantID, layer, mode).Inc()
}

// IncDelegationTreeCycleDetected increments the §8.9
// `lenny_delegation_tree_cycle_detected_total` counter when a tree
// walker hits a cycle in the §8.2 ParentSessionID lineage. `source`
// is `rest` for the /v1/sessions/{id}/tree handler and `mcp` for the
// lenny/get_task_tree platform-tool and lenny/await_children walks.
// spec: §8.9 line 1003; F-8.9.10.
func (m *Metrics) IncDelegationTreeCycleDetected(tenantID, source string) {
	if m == nil {
		return
	}
	m.delegationTreeCycleDetected.WithLabelValues(tenantID, source).Inc()
}

// IncDelegationDeadlockDetected increments the §8.8 line 981
// `lenny_delegation_deadlock_detected_total` counter once per
// newly-detected deadlocked subtree root, scoped by tenant. F-8.8.6.
func (m *Metrics) IncDelegationDeadlockDetected(tenantID string) {
	if m == nil {
		return
	}
	m.delegationDeadlockDetected.WithLabelValues(tenantID).Inc()
}

// ObserveDelegationDeadlockResolution closes out one tracked §8.8
// deadlock: it increments `lenny_delegation_deadlock_resolution_total`
// and records the detection-to-resolution latency on
// `lenny_delegation_deadlock_duration_seconds`, both labelled by the
// `resolution` outcome (`resolved` | `timeout`). F-8.8.6.
func (m *Metrics) ObserveDelegationDeadlockResolution(resolution string, seconds float64) {
	if m == nil {
		return
	}
	m.delegationDeadlockResolution.WithLabelValues(resolution).Inc()
	m.delegationDeadlockDuration.WithLabelValues(resolution).Observe(seconds)
}

// ObserveDelegationParallelChildrenHighWatermark records the §8.3 line
// 379 maximum simultaneous in-flight children for one delegation tree
// onto the `lenny_delegation_parallel_children_high_watermark`
// histogram, sampled once when the tree root settles. `pool` is the
// root session's assigned pool (empty when unresolved) and `tenant_id`
// scopes the observation. spec: §8.3 line 379; §16.1 line 73. F-8.9.6.
func (m *Metrics) ObserveDelegationParallelChildrenHighWatermark(pool, tenantID string, value int64) {
	if m == nil {
		return
	}
	m.delegationParallelChildrenHWM.WithLabelValues(pool, tenantID).Observe(float64(value))
}

// IncCheckpointEvictionFallback increments the §4.4 line 263
// `lenny_checkpoint_eviction_fallback_total` counter labeled by pool
// and had_prior_checkpoint. Called from the eviction-fallback writer
// at the entry to the Postgres minimal-state write.
// spec: §4.4 line 263.
func (m *Metrics) IncCheckpointEvictionFallback(pool string, hadPriorCheckpoint bool) {
	if m == nil {
		return
	}
	label := "false"
	if hadPriorCheckpoint {
		label = "true"
	}
	m.checkpointEvictionFallback.WithLabelValues(pool, label).Inc()
}

// IncPodClaimFallbackSkipped increments the §4.6.1
// `lenny_pod_claim_fallback_skipped_total` counter for reason
// (`mirror_stale` or `apiserver_unreachable`). Called by the gateway
// pod binder when a fallback precondition fails.
// spec: §4.6.1 "Fallback preconditions (mirror freshness and admission
// reachability)".
func (m *Metrics) IncPodClaimFallbackSkipped(reason string) {
	if m == nil {
		return
	}
	m.podClaimFallbackSkipped.WithLabelValues(reason).Inc()
}

// SetPodClaimQueueDepth publishes the §4.6.1 / §16.1
// `lenny_pod_claim_queue_depth{pool}` gauge as a pool's onPoolExhausted:
// queue FIFO grows and shrinks. Called by the gateway start path's claim
// queue. spec: §4.6.1 (Pool exhaustion behavior), §16.1.
func (m *Metrics) SetPodClaimQueueDepth(pool string, depth int) {
	if m == nil {
		return
	}
	m.podClaimQueueDepth.WithLabelValues(pool).Set(float64(depth))
}

// ObservePodClaimQueueWait observes the §4.6.1 / §16.1
// `lenny_pod_claim_queue_wait_seconds{pool}` histogram with the residency a
// queued request spent in the FIFO before acquiring a pod or timing out.
// spec: §4.6.1 (Pool exhaustion behavior), §16.1.
func (m *Metrics) ObservePodClaimQueueWait(pool string, seconds float64) {
	if m == nil {
		return
	}
	m.podClaimQueueWait.WithLabelValues(pool).Observe(seconds)
}

// IncPodClaimTimeout increments the §4.6.1 / §16.1
// `lenny_pod_claim_timeout_total{pool}` counter when an onPoolExhausted:
// queue request exhausts its maxQueueWaitSeconds bound. spec: §4.6.1 (Pool
// exhaustion behavior), §16.1.
func (m *Metrics) IncPodClaimTimeout(pool string) {
	if m == nil {
		return
	}
	m.podClaimTimeout.WithLabelValues(pool).Inc()
}

// IncSlotAssignmentConflict increments the §5.2 line 519
// `lenny_slot_assignment_conflict_total` counter for pool. Called by
// the gateway slot claimer when a concurrent-mode reservation found a
// candidate pod at its maxConcurrent bound.
// spec: §5.2 line 519 "atomic reservation failures due to slot contention".
func (m *Metrics) IncSlotAssignmentConflict(pool string) {
	if m == nil {
		return
	}
	m.slotAssignmentConflict.WithLabelValues(pool).Inc()
}

// IncCredentialPreclaimMismatch increments the §4.9 line 1220
// `lenny_credential_preclaim_mismatch_total` counter for the
// (pool, provider) pair. Called when the pre-claim credential
// availability check passed but the subsequent lease assignment failed,
// so the gateway released the claimed pod and returned
// CREDENTIAL_POOL_EXHAUSTED to the client.
// spec: §4.9 line 1220.
func (m *Metrics) IncCredentialPreclaimMismatch(pool, provider string) {
	if m == nil {
		return
	}
	m.credentialPreclaimMismatch.WithLabelValues(pool, provider).Inc()
}

// IncCredentialLeaseAssignment increments the §16.1
// `lenny_credential_lease_assignments_total` counter for the
// (provider, pool, source) tuple. Called by the credential-assignment
// service after each lease is minted and recorded; in v1 source is
// always `primary` (the §4.9 fallback chain is the source of `fallback`
// and the semantic cache the source of `cached`).
// spec: §16.1 line 51.
func (m *Metrics) IncCredentialLeaseAssignment(provider, pool, source string) {
	if m == nil {
		return
	}
	m.credentialLeaseAssignments.WithLabelValues(provider, pool, source).Inc()
}

// IncCredentialRotation increments the §16.1
// `lenny_credential_rotation_total` counter for errorType. Called by the
// §4.9 LLM-proxy Fallback Flow each time a faulted lease is rotated to
// the chain's next pool.
// spec: §16.1 line 118.
func (m *Metrics) IncCredentialRotation(errorType string) {
	if m == nil {
		return
	}
	m.credentialRotation.WithLabelValues(errorType).Inc()
}

// IncCredentialFallbackExhausted increments the §4.9
// `lenny_gateway_credential_fallback_exhausted_total` counter for the
// (pool, provider, errorType) tuple. Called when the fallback chain is
// exhausted and the session is terminated with
// CREDENTIAL_FALLBACK_EXHAUSTED.
// spec: §4.9 line 1395.
func (m *Metrics) IncCredentialFallbackExhausted(pool, provider, errorType string) {
	if m == nil {
		return
	}
	m.credentialFallbackExhausted.WithLabelValues(pool, provider, errorType).Inc()
}

// ObserveCredentialLeaseDuration records the §16.1
// `lenny_credential_lease_duration_seconds` histogram for a lease's
// wall-clock duration from assignment to release. Called by the
// credential-assignment service on Release.
// spec: §16.1 line 55.
func (m *Metrics) ObserveCredentialLeaseDuration(provider, pool string, seconds float64) {
	if m == nil {
		return
	}
	m.credentialLeaseDuration.WithLabelValues(provider, pool).Observe(seconds)
}

// SetCredentialPoolUtilization sets the §16.1
// `lenny_credential_pool_utilization` gauge for pool to ratio (in
// [0,1]). Called by the credential-assignment service after each assign
// or release recomputes in-use credentials over the pool size.
// spec: §16.1 line 53.
func (m *Metrics) SetCredentialPoolUtilization(pool string, ratio float64) {
	if m == nil {
		return
	}
	m.credentialPoolUtilization.WithLabelValues(pool).Set(ratio)
}

// IncLLMProxyConnections / DecLLMProxyConnections move the §16.1
// `lenny_gateway_llm_proxy_active_connections` gauge. The proxy handler
// increments on request entry and decrements on exit so the gauge
// reflects in-flight requests on the replica.
// spec: §16.1 line 97.
func (m *Metrics) IncLLMProxyConnections() {
	if m == nil {
		return
	}
	m.llmProxyActiveConnections.Inc()
}

// DecLLMProxyConnections decrements the §16.1 LLM-proxy active-connection
// gauge.
// spec: §16.1 line 97.
func (m *Metrics) DecLLMProxyConnections() {
	if m == nil {
		return
	}
	m.llmProxyActiveConnections.Dec()
}

// ObserveLLMTranslation records the §16.1
// `lenny_gateway_llm_translation_duration_seconds` histogram for one
// translator leg. direction is `request` or `response`. Called by the
// proxy handler around each TranslateRequest / TranslateResponse call.
// spec: §16.1 line 99.
func (m *Metrics) ObserveLLMTranslation(pool, provider, proxyDialect, direction string, seconds float64) {
	if m == nil {
		return
	}
	m.llmTranslationDuration.WithLabelValues(pool, provider, proxyDialect, direction).Observe(seconds)
}

// IncLLMTranslationError increments the §16.1
// `lenny_gateway_llm_translation_errors_total` counter for the
// (pool, provider, error_type) tuple. errorType is the §4.9 translator
// taxonomy value carried by a *llmproxy.TranslationError.
// spec: §16.1 line 100.
func (m *Metrics) IncLLMTranslationError(pool, provider, errorType string) {
	if m == nil {
		return
	}
	m.llmTranslationErrors.WithLabelValues(pool, provider, errorType).Inc()
}

// IncSlotFailure increments the §5.2 line 12 `lenny_slot_failure_total`
// counter for the (errorType, pool, podName) tuple. Called by the
// concurrent-mode slot binder when a slot bind stage failed after the
// slot was reserved.
// spec: §5.2 line 12.
func (m *Metrics) IncSlotFailure(errorType, pool, podName string) {
	if m == nil {
		return
	}
	m.slotFailure.WithLabelValues(errorType, pool, podName).Inc()
}

// IncSlotRehydration increments the §5.2 line 521
// `lenny_slot_rehydration_total` counter for the (pod, pool) pair.
// Called by the concurrent-mode slot claimer when a pod's slot counter
// was seeded from Postgres after a Redis restart — once per pod per
// Redis restart.
// spec: §5.2 line 521 ("lenny_slot_rehydration_total counter (labeled
// by pod, pool) is emitted on each rehydration event").
func (m *Metrics) IncSlotRehydration(pod, pool string) {
	if m == nil {
		return
	}
	m.slotRehydration.WithLabelValues(pod, pool).Inc()
}

// IncSlotPodReplacement increments the §5.2 `lenny_slot_pod_replacement_total`
// counter for pool. Called by the concurrent-workspace slot retry policy
// when a pod is marked unhealthy and drained because ceil(maxConcurrent/2)
// or more of its slots failed or leaked within the rolling 5-minute window.
// spec: §5.2 "whole-pod replacement trigger".
func (m *Metrics) IncSlotPodReplacement(pool string) {
	if m == nil {
		return
	}
	m.slotPodReplacement.WithLabelValues(pool).Inc()
}

// SetAdapterLeakedSlots sets the §6.2 line 179 `lenny_adapter_leaked_slots`
// gauge for podID to count: the number of slots on the pod whose cleanup
// timed out and remain counted in active_slots until the pod terminates.
// Called by the concurrent-workspace slot path when a slot is leaked (and
// to zero a pod's series when it terminates). spec: §6.2 line 179.
func (m *Metrics) SetAdapterLeakedSlots(podID, pool string, count float64) {
	if m == nil {
		return
	}
	m.adapterLeakedSlots.WithLabelValues(podID, pool).Set(count)
}

// IncScrubFailureTotal increments the §5.2 `lenny_pod_scrub_failure_total`
// aggregate counter for the (pool, runtimeClass) pair. The §4.7 recycle
// disposition calls it on every failed whole-pod scrub of a recycling
// session-mode pod, independent of whether the failure retires the pod.
// spec: §5.2 (warn-policy scrub failure increments the aggregate counter).
func (m *Metrics) IncScrubFailureTotal(pool, runtimeClass string) {
	if m == nil {
		return
	}
	m.podScrubFailureTotal.WithLabelValues(pool, runtimeClass).Inc()
}

// SetScrubFailureCount sets the §16.1 `lenny_pod_scrub_failure_count` gauge
// for podID to count: the pod's cumulative scrub-failure count, evaluated
// against recycle.maxScrubFailures at the recycle disposition. Labeled by
// k8s_pod_name (sanctioned for this metric by §16.1), pool, runtime_class.
// spec: §16.1.
func (m *Metrics) SetScrubFailureCount(podID, pool, runtimeClass string, count int) {
	if m == nil {
		return
	}
	m.podScrubFailureCount.WithLabelValues(podID, pool, runtimeClass).Set(float64(count))
}

// IncRetirement increments the §16.1 `lenny_gateway_pod_retirement_total`
// counter for the (reason, pool, runtimeClass) tuple. The gateway counter is
// scoped to gateway-decided retirements, so its `reason` is frozen to the two
// gateway-owned lifecycle-limit triggers (session_count_limit,
// scrub_failure_limit); the controller-owned
// `lenny_controller_pod_retirement_total` carries uptime_limit and
// applyDisposition suppresses the gateway's uptime_limit emission. The §6.39
// cordon-drain and the fail-policy termination drain without a
// retirement-counter increment. spec: §16.1, §16.1.1.
func (m *Metrics) IncRetirement(reason, pool, runtimeClass string) {
	if m == nil {
		return
	}
	m.podRetirement.WithLabelValues(reason, pool, runtimeClass).Inc()
}

// IncCheckpointPartial increments the §4.4 line 234
// `lenny_checkpoint_partial_total` counter labeled by pool. Called
// once per partial-manifest row written by the §10.1
// chunk-upload pipeline.
// spec: §4.4 line 234.
func (m *Metrics) IncCheckpointPartial(pool string) {
	if m == nil {
		return
	}
	m.checkpointPartialTotal.WithLabelValues(pool).Inc()
}

// IncPreStopCapSelection increments the §10.1
// `lenny_prestop_cap_selection_total` counter labeled by pool,
// service_instance_id, and source. Called once per preStop Stage 2
// tier selection.
// spec: §10.1 — preStop tier selection source.
func (m *Metrics) IncPreStopCapSelection(pool, serviceInstanceID, source string) {
	if m == nil {
		return
	}
	m.prestopCapSelection.WithLabelValues(pool, serviceInstanceID, source).Inc()
}

// IncPreStopBarrierTargetSource increments the §10.1 line 165
// `lenny_prestop_barrier_target_source_total` counter for one preStop
// barrier target-set read. The source label is `postgres` on the
// steady-state healthy path or `cache_fallback` when the
// coordination-lease read failed or exceeded its 2s deadline and the
// replica fell back to its in-memory lease cache.
// spec: §10.1 line 165.
func (m *Metrics) IncPreStopBarrierTargetSource(source string) {
	if m == nil {
		return
	}
	m.barrierTargetSource.WithLabelValues(source).Inc()
}

// IncCoordinatorHandoffStale increments the §10.1 line 61
// `lenny_coordinator_handoff_stale_total` counter for one generation-stale
// coordinator-handoff rejection: a CoordinatorFence the gateway issued was
// refused (FailedPrecondition) because the pod had already been fenced to
// an equal-or-higher generation by another coordinator.
// spec: §10.1 line 61.
func (m *Metrics) IncCoordinatorHandoffStale() {
	if m == nil {
		return
	}
	m.coordinatorHandoffStale.Inc()
}

// IncCoordinatorFenceRetry increments the §11.3 line 209
// `lenny_coordinator_fence_retry_total` counter for one CoordinatorFence
// retry after a stale rejection or a transient transport fault.
// spec: §11.3 line 209.
func (m *Metrics) IncCoordinatorFenceRetry() {
	if m == nil {
		return
	}
	m.coordinatorFenceRetry.Inc()
}

// IncCoordinatorFenceRelinquished increments the §11.3 line 209
// `lenny_coordinator_fence_relinquished_total` counter when the coordinator
// gives up leadership of a session after exhausting its fence retries and
// releases the coordination lease.
// spec: §11.3 line 209.
func (m *Metrics) IncCoordinatorFenceRelinquished() {
	if m == nil {
		return
	}
	m.coordinatorFenceRelinquished.Inc()
}

// IncSigkillStreams increments the §10.1 line 161
// `lenny_gateway_sigkill_streams_total` counter for one in-flight
// stream the kubelet SIGKILLs at the grace deadline because its
// eviction checkpoint did not finish in budget. Called once per
// deadline-exceeded session during the preStop staged drain.
// spec: §10.1 line 161 — SIGKILL-deadline stream counter.
func (m *Metrics) IncSigkillStreams(pool, serviceInstanceID string) {
	if m == nil {
		return
	}
	m.sigkillStreams.WithLabelValues(pool, serviceInstanceID).Inc()
}

// SetCheckpointStaleSessions sets the per-pool/level
// `lenny_checkpoint_stale_sessions` gauge value. The freshness reaper
// calls this once per sweep with the count of active sessions whose
// `last_successful_checkpoint_at` is older than
// `periodicCheckpointIntervalSeconds`. The §16.5 `CheckpointStale`
// alert keys on the per-label value.
// spec: §4.4 line 256.
func (m *Metrics) SetCheckpointStaleSessions(pool, level string, count int) {
	if m == nil {
		return
	}
	m.checkpointStaleSessions.WithLabelValues(pool, level).Set(float64(count))
}

// RecordKMSSigningError increments the §10.2 line 225
// `lenny_gateway_kms_signing_errors_total{reason}` counter. The
// `reason` label distinguishes a downstream KMS failure (`inner`) from
// a breaker short-circuit (`rejected`). F-10.2.6.
func (m *Metrics) RecordKMSSigningError(reason string) {
	if m == nil {
		return
	}
	m.kmsSigningErrors.WithLabelValues(reason).Inc()
}

// SetKMSSigningCircuitState publishes the §10.2 line 225 JWTSigner
// breaker state to `lenny_gateway_kms_signing_circuit_state`. The
// §16.5 KMSSigningUnavailable alert fires on the error-rate counter
// rather than the gauge, but the gauge is useful for dashboards.
// F-10.2.6.
func (m *Metrics) SetKMSSigningCircuitState(value int) {
	if m == nil {
		return
	}
	m.kmsSigningCircuitState.Set(float64(value))
}

// SetTokenServiceCircuitState updates the §4.3 / §4.1
// lenny_token_service_circuit_state gauge. The §16.5
// TokenServiceUnavailable alert fires when the value equals 2 (open).
// spec: §4.3 line 211.
func (m *Metrics) SetTokenServiceCircuitState(value int) {
	if m == nil {
		return
	}
	m.tokenServiceCircuitState.Set(float64(value))
}

// SetMaxOrphanTasksPerTenant publishes the deployer-configured §8.10
// line 1103 orphan-cap as the `lenny_max_orphan_tasks_per_tenant`
// scalar gauge so the §16.5 OrphanTasksPerTenantHigh alert
// (`lenny_orphan_tasks_active_per_tenant > 0.80 *
// scalar(lenny_max_orphan_tasks_per_tenant)`) reads the live value.
// Called once at gateway startup and again on a config-reload.
// F-8.10.13.
func (m *Metrics) SetMaxOrphanTasksPerTenant(value int) {
	if m == nil {
		return
	}
	m.maxOrphanTasksPerTenant.Set(float64(value))
}

// IncOrphanCleanupRun increments the §8.10 line 1091 / §16.1 line 146
// `lenny_orphan_cleanup_runs_total` counter — one tick per sweep
// invocation regardless of outcome. spec: §8.10 line 1091; F-8.10.7.
func (m *Metrics) IncOrphanCleanupRun() {
	if m == nil {
		return
	}
	m.orphanCleanupRuns.Inc()
}

// IncOrphanSessionReconciliation increments
// `lenny_orphan_session_reconciliations_total` once per session the
// §10.1 reconciler forces to `failed` after its bound pod terminated
// without a terminal event. spec: §10.1 line 51; F-10.1.5.
func (m *Metrics) IncOrphanSessionReconciliation() {
	if m == nil {
		return
	}
	m.orphanSessionReconciliations.Inc()
}

// SetAgentPodStateMirrorLag publishes the per-pool
// `lenny_agent_pod_state_mirror_lag_seconds` gauge — the staleness of
// the agent_pod_state mirror for poolID. The §10.1 reconciler emits it
// once per pool per pass; the §16.5 PodStateMirrorStale alert fires when
// it exceeds 60s. spec: §10.1 line 51; F-10.1.5.
func (m *Metrics) SetAgentPodStateMirrorLag(poolID string, seconds float64) {
	if m == nil {
		return
	}
	m.agentPodStateMirrorLag.WithLabelValues(poolID).Set(seconds)
}

// AddOrphanTasksTerminated bumps the §8.10 / §16.1 line 147
// `lenny_orphan_tasks_terminated` counter by the per-sweep terminated
// count (the cleanup tick's return value). A zero-count sweep is a no-op.
// spec: §8.10 / §16.1 line 147; F-8.10.7.
func (m *Metrics) AddOrphanTasksTerminated(n int) {
	if m == nil || n <= 0 {
		return
	}
	m.orphanTasksTerminated.Add(float64(n))
}

// SetOrphanTasksActive publishes the fleet-wide active orphan count as
// `lenny_orphan_tasks_active` per §8.10 / §16.1 line 148. Operators
// alert when the gauge exceeds a deployment-specific threshold
// (suggested 50 per §8.10 line 1101). spec: §16.1 line 148; F-8.10.7.
func (m *Metrics) SetOrphanTasksActive(value int) {
	if m == nil {
		return
	}
	m.orphanTasksActive.Set(float64(value))
}

// SetOrphanTasksActivePerTenant publishes the per-tenant active orphan
// count as `lenny_orphan_tasks_active_per_tenant{tenant_id}`. The §16.5
// OrphanTasksPerTenantHigh alert reads `value > 0.80 *
// scalar(lenny_max_orphan_tasks_per_tenant)`. The cleanup sweep calls
// this for every tenant on every Tick so a tenant whose orphan count
// drops to zero re-publishes a zero value. spec: §8.10 line 1103;
// §16.1 line 149; F-8.10.7.
func (m *Metrics) SetOrphanTasksActivePerTenant(tenantID string, value int) {
	if m == nil {
		return
	}
	m.orphanTasksActivePerTenant.WithLabelValues(tenantID).Set(float64(value))
}

// ObserveTreeRecoveryDuration records one wall-clock duration on the
// §8.10 / §16.1 line 144 `lenny_delegation_tree_recovery_duration_seconds`
// histogram. `pool` is the root session's pool; `outcome` is one of
// `full_success`, `partial_failure`, or `total_timeout`. spec: §8.10 /
// §16.1 line 144; F-8.10.7.
func (m *Metrics) ObserveTreeRecoveryDuration(pool, outcome string, seconds float64) {
	if m == nil {
		return
	}
	m.treeRecoveryDuration.WithLabelValues(pool, outcome).Observe(seconds)
}

// IncTreeRecoveryTimeout increments the §8.10 / §16.1 line 145
// `lenny_delegation_tree_recovery_timeout_total{pool, timeout_type}`
// counter. `timeout_type` is `level` (one tree-level budget exhausted)
// or `tree` (the whole-tree budget exhausted). spec: §8.10 / §16.1 line
// 145; F-8.10.7.
func (m *Metrics) IncTreeRecoveryTimeout(pool, timeoutType string) {
	if m == nil {
		return
	}
	m.treeRecoveryTimeout.WithLabelValues(pool, timeoutType).Inc()
}

// RecordElicitationDrop increments the §9.1 lenny_elicitation_dropped_total
// counter for the given drop reason (for example `budget_exceeded`).
func (m *Metrics) RecordElicitationDrop(reason string) {
	m.elicitationDropped.WithLabelValues(reason).Inc()
}

// RecordElicitationContentTamperDetected increments the §9.2 /
// §16.5 lenny_elicitation_content_tamper_detected_total counter
// when the §9.2 chain walk catches a forwarding hop that mutated
// the elicitation payload. Labelled by origin_pod (the pod that
// legitimately originated the elicitation), tampering_pod (the
// forwarding pod whose re-emission diverged), and enforcement_mode
// so the §16.5 ElicitationContentTamperDetected alert (which matches
// enforcement_mode="enforce") fires only when a tamper caused a hard
// drop; detect-only catches still bump the metric for visibility
// without firing the critical alert. spec: §16.1 line 64; §9.2 line
// 60. F-9.2.4.
func (m *Metrics) RecordElicitationContentTamperDetected(originPod, tamperingPod, enforcementMode string) {
	m.elicitationTamperDetected.WithLabelValues(originPod, tamperingPod, enforcementMode).Inc()
}

// SetElicitationIntegrityWeakened publishes the §16.5 line 460
// standing-alert gauge: the count of active tenants whose §9.2
// effective elicitation content-integrity mode is weaker than
// enforce. The gateway reconciliation loop calls this on every poll
// so the ElicitationContentIntegrityWeakened alert fires while any
// tenant is weakened and resolves once the count returns to zero.
// spec: §16.5 line 460. F-9.2.5.
func (m *Metrics) SetElicitationIntegrityWeakened(weakenedTenants int) {
	m.elicitationIntegrityWeakened.Set(float64(weakenedTenants))
}

// IncElicitationPending increments the §16.1 line 61
// `lenny_elicitation_pending` in-flight gauge. The §16.5
// ElicitationBacklogHigh alert reads `> 50 for 30s`. spec: §16.1 line 61.
// F-9.2.14.
func (m *Metrics) IncElicitationPending() {
	if m == nil {
		return
	}
	m.elicitationPending.Inc()
}

// DecElicitationPending decrements the §16.1 line 61 gauge on every
// terminal phase (responded | dismissed | timeout). spec: §16.1
// line 61. F-9.2.14.
func (m *Metrics) DecElicitationPending() {
	if m == nil {
		return
	}
	m.elicitationPending.Dec()
}

// IncElicitationTimeout increments the §16.1 line 63
// `lenny_elicitation_timeout_total` counter when the dispatcher
// drops a pending elicitation on the §9.1 maxElicitationWait
// deadline. spec: §16.1 line 63. F-9.2.14.
func (m *Metrics) IncElicitationTimeout() {
	if m == nil {
		return
	}
	m.elicitationTimeout.Inc()
}

// IncElicitationSuppressed increments the §16.1 line 62
// `lenny_elicitation_suppressed_total` counter for a §9.2 depth-policy
// suppression or a §9.1 per-session budget rejection. spec: §16.1
// line 62. F-9.2.14.
func (m *Metrics) IncElicitationSuppressed() {
	if m == nil {
		return
	}
	m.elicitationSuppressed.Inc()
}

// ObserveElicitationRoundtrip records the §16.1 line 60
// `lenny_elicitation_roundtrip_seconds` admit-to-terminal latency.
// spec: §16.1 line 60. F-9.2.14.
func (m *Metrics) ObserveElicitationRoundtrip(d time.Duration) {
	if m == nil {
		return
	}
	m.elicitationRoundtripSeconds.Observe(d.Seconds())
}

// RecordExperimentIsolationRejection increments the §16.1
// lenny_experiment_isolation_rejections_total counter when the §10.7
// ExperimentRouter fails a session closed because the variant pool's
// isolation profile is weaker than the session's.
func (m *Metrics) RecordExperimentIsolationRejection(tenantID, experimentID, variantID string) {
	m.experimentIsoRej.WithLabelValues(tenantID, experimentID, variantID).Inc()
}

// ObserveExperimentTargetingDuration records one §10.7 external
// experiment targeting evaluation against the §16.1 line 156
// lenny_experiment_targeting_duration_seconds histogram. provider is the
// OpenFeature provider name, or the OFREP endpoint hostname when
// provider:ofrep.
func (m *Metrics) ObserveExperimentTargetingDuration(provider string, seconds float64) {
	m.experimentTargetingDur.WithLabelValues(provider).Observe(seconds)
}

// RecordExperimentStickyCacheInvalidation increments the §16.1 line 159
// lenny_experiment_sticky_cache_invalidations_total counter once per §10.7
// line 1096 sticky-cache flush (an experiment transition to paused or
// concluded that DELs the experiment's `…:sticky:*` keys). transition is the
// target status ("paused" or "concluded").
func (m *Metrics) RecordExperimentStickyCacheInvalidation(experimentID, transition string) {
	m.experimentStickyInval.WithLabelValues(experimentID, transition).Inc()
}

// RecordExperimentTargetingError increments the §16.1 line 157
// lenny_experiment_targeting_error_total counter on a §10.7
// targeting_failed condition. errorType classifies the failure cause
// (timeout, transport, or the OFREP errorCode).
func (m *Metrics) RecordExperimentTargetingError(provider, errorType string) {
	m.experimentTargetingErr.WithLabelValues(provider, errorType).Inc()
}

// SetExperimentTargetingCircuitOpen sets the §10.7 SCL-023 targeting
// circuit-breaker gauge for (tenant, provider): true → 1 (open, the
// gateway skips the OpenFeature call), false → 0 (closed). spec: §16.1
// line 64, §10.7 line 838.
func (m *Metrics) SetExperimentTargetingCircuitOpen(tenantID, provider string, open bool) {
	v := 0.0
	if open {
		v = 1.0
	}
	m.experimentTargetingCircuit.WithLabelValues(tenantID, provider).Set(v)
}

// RecordSessionTerminal records the §16.1 lines 161-163 / §10.7
// rollback-trigger session metrics at a terminal session transition: it
// increments lenny_session_total, increments lenny_session_error_total when
// the terminal state is an error outcome, and observes the per-session
// wall-clock duration on lenny_session_duration_seconds. sessionType is the
// §5.2 ExecutionMode; variantID is the §10.7 enrollment ("" for
// control / un-enrolled).
func (m *Metrics) RecordSessionTerminal(tenantID, sessionType, variantID string, isError bool, seconds float64) {
	if sessionType == "" {
		sessionType = "session"
	}
	m.sessionTotal.WithLabelValues(tenantID, sessionType, variantID).Inc()
	if isError {
		m.sessionError.WithLabelValues(tenantID, sessionType, variantID).Inc()
	}
	m.sessionDuration.WithLabelValues(tenantID, sessionType, variantID).Observe(seconds)
}

// ObserveEvalScore records one §16.1 line 164 lenny_eval_score observation
// per submitted eval run. variantID is the §10.7 enrollment ("" when the
// scored session was not enrolled).
func (m *Metrics) ObserveEvalScore(tenantID, scorer, variantID string, score float64) {
	m.evalScore.WithLabelValues(tenantID, scorer, variantID).Observe(score)
}

// IncSessionBudgetExceeded increments
// lenny_gateway_session_budget_exceeded_total when the §11.2 mid-session
// enforcer terminates a session for token-budget exhaustion. spec: §11.2
// line 44.
func (m *Metrics) IncSessionBudgetExceeded(tenantID string) {
	m.sessionBudgetExceeded.WithLabelValues(tenantID).Inc()
}

// IncErasureJobFailed increments lenny_erasure_job_failed_total for a
// failed §12.8 user-level erasure job. failurePhase is the §12.8 CMP-026
// phase label (store_delete, pseudonymization, verification, or
// memory_store_preflight). The §16.5 ErasureJobFailed alert fires on any
// increase. spec: §12.8 CMP-026 / §16.1 line 262.
func (m *Metrics) IncErasureJobFailed(tenantID, failurePhase string) {
	m.erasureJobFailed.WithLabelValues(tenantID, failurePhase).Inc()
}

// IncErasureJobsActive increments the §12.8 line 768 in-progress
// erasure-job gauge when a job begins execution.
func (m *Metrics) IncErasureJobsActive() { m.erasureJobsActive.Inc() }

// DecErasureJobsActive decrements the in-progress erasure-job gauge when
// a job reaches a terminal phase.
func (m *Metrics) DecErasureJobsActive() { m.erasureJobsActive.Dec() }

// ObserveErasureJobDuration records a completed (or failed) erasure
// job's wall-clock duration in the §12.8 line 768 histogram.
func (m *Metrics) ObserveErasureJobDuration(seconds float64) {
	m.erasureJobDuration.Observe(seconds)
}

// SetErasureJobDeadlineSeconds publishes the §12.8 line 768 erasure SLA
// deadline the §16.5 ErasureJobOverdue alert compares against.
func (m *Metrics) SetErasureJobDeadlineSeconds(seconds float64) {
	m.erasureJobDeadlineSeconds.Set(seconds)
}

// SetErasureJobAge publishes the age of an in-progress erasure job so
// the §16.5 ErasureJobOverdue alert can detect a stalled job before the
// SLA breaches. spec: §12.8 line 768.
func (m *Metrics) SetErasureJobAge(tenantID, jobID string, ageSeconds float64) {
	m.erasureJobAgeSeconds.WithLabelValues(tenantID, jobID).Set(ageSeconds)
}

// ClearErasureJobAge removes the age series for a terminal job so a
// completed erasure no longer reads as in-progress.
func (m *Metrics) ClearErasureJobAge(tenantID, jobID string) {
	m.erasureJobAgeSeconds.DeleteLabelValues(tenantID, jobID)
}

// RecordNoEnvironmentPolicyAllowAll increments the §10.6
// lenny_noenvironmentpolicy_allowall_total counter when a tenant's
// rbac-config is written with noEnvironmentPolicy set to allow-all.
func (m *Metrics) RecordNoEnvironmentPolicyAllowAll(tenantID string) {
	m.noEnvPolicyAllowAll.WithLabelValues(tenantID).Inc()
}

// SetCircuitBreakerOpen updates the §16.1 per-breaker open gauge: 1
// when the named breaker is open, 0 when closed.
func (m *Metrics) SetCircuitBreakerOpen(name string, open bool) {
	v := 0.0
	if open {
		v = 1
	}
	m.circuitBreakerOpen.WithLabelValues(name).Set(v)
}

// SetPoolDrainingSessions updates the §15.1 line 797
// lenny_pool_draining_sessions_total gauge for a draining pool with the
// current in-flight (non-terminal) session count. A count of 0 means
// the drain has converged.
func (m *Metrics) SetPoolDrainingSessions(pool string, count int) {
	m.poolDrainingSessions.WithLabelValues(pool).Set(float64(count))
}

// runtimeUpgradePhases is the §10.5 / §16.1 line 184 state vocabulary.
// SetRuntimeUpgradeState publishes one series per state so exactly one
// reads 1 (the current phase) and the rest read 0, keeping the
// RuntimeUpgradeStuck alert's equality predicate well defined.
var runtimeUpgradePhases = []string{"pending", "expanding", "draining", "contracting", "complete", "paused"}

// SetRuntimeUpgradeState publishes the §16.1 line 184
// lenny_runtime_upgrade_state gauge for pool: the series matching phase
// reads 1 and every other state series reads 0.
func (m *Metrics) SetRuntimeUpgradeState(pool, phase string) {
	for _, s := range runtimeUpgradePhases {
		v := 0.0
		if s == phase {
			v = 1
		}
		m.runtimeUpgradeState.WithLabelValues(pool, s).Set(v)
	}
}

// SetRuntimeUpgradePhaseDuration updates the §16.1 line 185
// lenny_runtime_upgrade_phase_duration_seconds gauge for pool with the
// wall-clock seconds spent in the current phase.
func (m *Metrics) SetRuntimeUpgradePhaseDuration(pool, phase string, seconds float64) {
	m.runtimeUpgradePhaseDuration.WithLabelValues(pool, phase).Set(seconds)
}

// SetRuntimeUpgradeDrainingSessions updates the §16.1 line 186
// lenny_runtime_upgrade_draining_sessions gauge for pool.
func (m *Metrics) SetRuntimeUpgradeDrainingSessions(pool string, n int) {
	m.runtimeUpgradeDrainingSessions.WithLabelValues(pool).Set(float64(n))
}

// SetAuditPartitionDropBlocked updates the §16.4 line 378
// lenny_audit_partition_drop_blocked gauge for a partition (audit
// chain): 1 when the SIEM delivery guard is holding past-TTL rows the
// retention GC could otherwise drop, 0 once the forwarder catches up.
// The §16.5 AuditPartitionDropBlocked alert reads it.
func (m *Metrics) SetAuditPartitionDropBlocked(partition string, blocked bool) {
	v := 0.0
	if blocked {
		v = 1
	}
	m.auditPartitionDropBlocked.WithLabelValues(partition).Set(v)
}

// RecordCircuitBreakerRejection increments the §11.6
// lenny_circuit_breaker_rejections_total counter for one admission
// rejection caused by a tripped breaker. Every rejection is counted,
// including those whose audit row is elided by sampling.
func (m *Metrics) RecordCircuitBreakerRejection(tenantID, circuitName, limitTier string) {
	m.cbRejections.WithLabelValues(tenantID, circuitName, limitTier).Inc()
}

// RecordCircuitBreakerRejectionSuppressed increments the §11.6
// lenny_circuit_breaker_rejections_suppressed_total counter for a
// rejection whose audit row was elided by the per-(tenant_id,
// circuit_name, caller_sub) 10-second sampling window.
func (m *Metrics) RecordCircuitBreakerRejectionSuppressed(tenantID, circuitName, limitTier string) {
	m.cbRejectionsSuppressed.WithLabelValues(tenantID, circuitName, limitTier).Inc()
}

// RecordCircuitBreakerCacheStaleServe increments the §16.1 line 218
// lenny_circuit_breaker_cache_stale_serves_total counter for one
// admission decision served against a breaker cache that had not
// refreshed within the 5s poll interval. outcome is "rejected" when an
// open breaker matched and "admitted" otherwise (the security-salient
// case).
func (m *Metrics) RecordCircuitBreakerCacheStaleServe(outcome string) {
	m.cbCacheStaleServes.WithLabelValues(outcome).Inc()
}

// SetCircuitBreakerCache updates the §16.1 circuit-breaker cache
// gauges: the seconds since the last refresh and whether the cache has
// completed its first refresh.
func (m *Metrics) SetCircuitBreakerCache(staleSeconds float64, initialized bool) {
	m.cbCacheStale.Set(staleSeconds)
	if initialized {
		m.cbCacheInitialized.Set(1)
	} else {
		m.cbCacheInitialized.Set(0)
	}
}

// SetStorageQuota updates the §16.1 per-tenant storage-quota gauges:
// the bytes currently reserved-plus-committed and the tenant's
// configured quota. These drive the §16.5 StorageQuotaHigh alert.
func (m *Metrics) SetStorageQuota(tenantID string, used, limit int64) {
	m.storageQuotaUsed.WithLabelValues(tenantID).Set(float64(used))
	m.storageQuotaLimit.WithLabelValues(tenantID).Set(float64(limit))
}

// Handler returns the Prometheus `/metrics` scrape handler over the
// gateway's private registry.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// Registerer exposes the gateway's private metric registry so a
// gateway subsystem (for example the §27 playground) can register its
// own metric vectors against the same registry and have them surface
// on the shared `/metrics` scrape target.
func (m *Metrics) Registerer() prometheus.Registerer {
	return m.reg
}

// Gatherer exposes the gateway's private metric registry as a read-only
// gatherer. The §25.3 capacity-recommendation sampler reads gauge and
// counter values directly from the same in-process registry Prometheus
// scrapes, so it can feed the recommendation ring buffers without a
// Prometheus query (spec: §25.3 line 439 — "In-process metric registry
// ... works even when Prometheus is down").
func (m *Metrics) Gatherer() prometheus.Gatherer {
	return m.reg
}

// SetActiveSessions updates the active-session gauge. The gateway
// calls this from the watchdog sweep or a dedicated poller.
func (m *Metrics) SetActiveSessions(n int) {
	m.activeSessions.Set(float64(n))
}

// SetActiveStreams updates the §4.1 lenny_gateway_active_streams gauge,
// the secondary HPA metric (SCL-026): the count of in-flight streaming
// connections on this replica.
func (m *Metrics) SetActiveStreams(n int) {
	m.activeStreams.Set(float64(n))
}

// SetRequestQueueDepth updates the §4.1 lenny_gateway_request_queue_depth
// gauge, the primary HPA scale-out trigger (SCL-026): the number of
// requests queued on this replica awaiting a handler goroutine.
func (m *Metrics) SetRequestQueueDepth(n int) {
	m.requestQueueDepth.Set(float64(n))
}

// SetRejectionRate updates the §10.1 lenny_gateway_rejection_rate gauge,
// the second leading-indicator metric: requests rejected with 429/503
// per second on this replica.
func (m *Metrics) SetRejectionRate(perSecond float64) {
	m.rejectionRate.Set(perSecond)
}

// SetMaxSessionsPerReplica emits the §4.1 capacity ceiling on the
// lenny_gateway_max_sessions_per_replica gauge for the given
// delivery_mode ("proxy" or "direct"). The §16.5
// GatewaySessionBudgetNearExhaustion alert reads this value as the
// denominator of `lenny_gateway_active_sessions / value > 0.90`. The
// gateway calls this once at startup so the gauge is observable as
// soon as the /metrics endpoint serves the first scrape; the spec
// requires both delivery_mode values be reported per replica so a
// capacity-planning dashboard can compute the proxy/direct ratio.
func (m *Metrics) SetMaxSessionsPerReplica(deliveryMode string, value int) {
	m.maxSessionsPerReplica.WithLabelValues(deliveryMode).Set(float64(value))
}

// SetMinReplicas emits the §4.1 / §16.5 lenny_gateway_min_replicas
// gauge. The value is the configured HPA minReplicas floor (per the
// §17.8.2 SCL-036 burst-absorption formula); the
// `GatewayNoHealthyReplicas` alert in pkg/alerting/rules/rules.go
// reads it via scalar(lenny_gateway_min_replicas) and fires when the
// fleet-wide ready replica count drops below this floor.
func (m *Metrics) SetMinReplicas(value int) {
	m.minReplicas.Set(float64(value))
}

// SetStreamCeiling emits the §4.1 / §16.5 lenny_gateway_stream_ceiling
// gauge. The value is the per-replica configured ceiling on
// simultaneous streaming connections; the `GatewayActiveStreamsHigh`
// alert in pkg/alerting/rules/rules.go reads it via
// scalar(lenny_gateway_stream_ceiling) and fires when
// lenny_gateway_active_streams exceeds 80% of the ceiling on any
// replica.
func (m *Metrics) SetStreamCeiling(value int) {
	m.streamCeiling.Set(float64(value))
}

// SetReplicaCount emits the §4.1 / §16.1
// lenny_gateway_replica_count gauge. v1 sets the gauge to 1 per
// replica at startup; the Prometheus recording rule
// `sum(lenny_gateway_replica_count)` then yields the fleet-wide ready
// replica count used by the `GatewayNoHealthyReplicas` alert numerator.
// Production deployments may swap this constant for a controller-managed
// value sourced from kube-state-metrics; the gateway still emits the
// per-replica constant so the alert keeps a non-NaN reading on
// installs without kube-state-metrics.
func (m *Metrics) SetReplicaCount(value int) {
	m.replicaCount.Set(float64(value))
}

// SetBillingCorrectionRateThreshold emits the §11.2.1 / §16.5
// lenny_billing_correction_rate_threshold gauge. The value is the
// deployer-configurable percentage (default 0.05 = 5%) of total billing
// events allowed in a rolling 24h window before
// `BillingCorrectionRateHigh` fires. The alert reads it via
// scalar(lenny_billing_correction_rate_threshold). F-11.2.23.
func (m *Metrics) SetBillingCorrectionRateThreshold(value float64) {
	m.billingCorrectionRateThreshold.Set(value)
}

// SetEventBusDropAlertThreshold emits the §12.6 line 683 / §16.5
// lenny_event_bus_drop_alert_threshold gauge. The value is the
// deployer-configurable per-minute dropped-publish ceiling (default 10)
// above which `EventBusPublishDropped` fires; the alert reads it via
// scalar(lenny_event_bus_drop_alert_threshold). F-12.6.23.
func (m *Metrics) SetEventBusDropAlertThreshold(value float64) {
	if m == nil {
		return
	}
	m.eventBusDropAlertThreshold.Set(value)
}

// SetAuditSIEMConfigured emits the §16.4 / §16.5 lenny_audit_siem_configured
// gauge: 1 when audit.siem.endpoint is set, 0 otherwise. The
// AuditSIEMNotConfigured alert fires on `== 0 and lenny_env_production == 1`,
// and AuditRetentionLow uses the same term to suppress its warning once a
// SIEM is configured. F-16.4.9.
func (m *Metrics) SetAuditSIEMConfigured(configured bool) {
	m.auditSIEMConfigured.Set(boolGauge(configured))
}

// SetAuditRetentionDays emits the §16.4 lenny_audit_retention_days gauge:
// the resolved general (non-gdpr) audit-log retention window in days, from
// audit.retentionPreset or the explicit audit.retentionDays. The
// AuditRetentionLow alert fires on `< 365 and lenny_audit_siem_configured
// == 0 and lenny_env_production == 1`. F-16.4.9; F-16.4.10.
func (m *Metrics) SetAuditRetentionDays(days int) {
	m.auditRetentionDays.Set(float64(days))
}

// SetEnvProduction emits the §16.5 lenny_env_production gauge: 1 when
// LENNY_ENV=production, 0 otherwise. The production-only audit alerts gate
// on `lenny_env_production == 1` so they stay inert in dev and staging.
// F-16.4.9.
func (m *Metrics) SetEnvProduction(production bool) {
	m.envProduction.Set(boolGauge(production))
}

// boolGauge maps a boolean to the 1/0 gauge convention the §16.5 alert
// expressions compare against.
func boolGauge(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// SetGatewayQueueDepthThreshold emits the §25.13 line 4737 / §16.5
// GatewayQueueDepthHigh ceiling. The alert reads it via
// scalar(lenny_gateway_queue_depth_threshold); tier-2/3 presets tighten
// the value via monitoring.alertThresholds.gatewayQueueDepthHigh.value.
// F-25.13.2.
func (m *Metrics) SetGatewayQueueDepthThreshold(value float64) {
	m.gatewayQueueDepthThreshold.Set(value)
}

// SetGatewayLatencyThresholdSeconds emits the §25.13 line 4737 / §16.5
// GatewayLatencyHigh p95 ceiling, in seconds. The alert reads it via
// scalar(lenny_gateway_latency_threshold_seconds). F-25.13.2.
func (m *Metrics) SetGatewayLatencyThresholdSeconds(value float64) {
	m.gatewayLatencyThresholdSeconds.Set(value)
}

// SetSLOBurnRateMultipliers emits the §16.5 line 640 operator-configurable
// burn-rate window multipliers. Every burn-rate alert compares its
// budget-normalised ratio against
// scalar(lenny_slo_burn_rate_{fast,slow}_multiplier or vector(default)),
// so changing slo.burnRate.{fast,slow}Multiplier retunes when the bundled
// alerts page without regenerating the PrometheusRule. F-16.5.3.
func (m *Metrics) SetSLOBurnRateMultipliers(fast, slow float64) {
	m.sloBurnRateFastMultiplier.Set(fast)
	m.sloBurnRateSlowMultiplier.Set(slow)
}

// SetSessionUnavailabilityRatio publishes the §16.5
// SessionAvailabilityBurnRate SLI: the fraction of active sessions in a
// retry/recovery state. The gateway export loop computes it as
// recovery_sessions / active_sessions (0 when there are no active
// sessions). F-16.5.3.
func (m *Metrics) SetSessionUnavailabilityRatio(ratio float64) {
	m.sessionUnavailabilityRatio.Set(ratio)
}

// SetCredentialPoolLowThreshold emits the §25.13 line 4737 / §16.5
// CredentialPoolLow utilisation fraction. The alert reads it via
// scalar(lenny_credential_pool_low_threshold). F-25.13.2.
func (m *Metrics) SetCredentialPoolLowThreshold(value float64) {
	m.credentialPoolLowThreshold.Set(value)
}

// IncBillingFlushPressure advances the §12.3 line 76
// billing_flush_pressure counter. The failover Pipeline invokes it
// (via its OnFlushPressure callback) each time an Append leaves the
// Tier 2 write-ahead buffer over billingFlushMaxPending. F-12.3.13.
func (m *Metrics) IncBillingFlushPressure() {
	if m == nil {
		return
	}
	m.billingFlushPressure.Inc()
}

// IncAuditBatchingNoSIEM advances the §12.3 line 99 AuditBatchingNoSIEM
// counter. The gateway calls it once at startup when production has
// audit.batchingEnabled set but no SIEM endpoint, so buffered T2 audit
// events would be lost on a crash with no external durable copy.
// F-12.3.15.
func (m *Metrics) IncAuditBatchingNoSIEM() {
	if m == nil {
		return
	}
	m.auditBatchingNoSIEM.Inc()
}

// SetPostgresWriteIops sets the §12.3 lines 115-125 sustained Postgres
// write-IOPS gauge. The periodic sampler computes the rate from
// pg_stat_database row-write deltas and pushes it here so the §16.5
// PostgresWriteSaturation alert can evaluate. F-12.3.7.
func (m *Metrics) SetPostgresWriteIops(iops float64) {
	if m == nil {
		return
	}
	m.postgresWriteIops.Set(iops)
}

// SetPostgresWriteCeilingIops emits the §12.3 line 123 configured
// postgres.writeCeilingIops ceiling so the PostgresWriteSaturation
// alert resolves scalar(lenny_postgres_write_ceiling_iops) to an
// operator-tunable denominator. F-12.3.8.
func (m *Metrics) SetPostgresWriteCeilingIops(iops float64) {
	if m == nil {
		return
	}
	m.postgresWriteCeilingIops.Set(iops)
}

// SetSIEMDeliveryLagSeconds emits the §16.1 line 228
// lenny_audit_siem_delivery_lag_seconds gauge. The §12.3 outbox
// forwarder calls it after each delivery checkpoint so the §16.5
// AuditSIEMDeliveryLag alert can evaluate the lag against the
// configured max-lag scalar. It satisfies siem.LagGauge. F-12.3.6 /
// F-12.3.17.
func (m *Metrics) SetSIEMDeliveryLagSeconds(seconds float64) {
	if m == nil {
		return
	}
	m.siemDeliveryLag.Set(seconds)
}

// SetSIEMMaxDeliveryLagSeconds emits the §12.3 line 97 configured
// audit.siem.maxDeliveryLagSeconds threshold so the AuditSIEMDeliveryLag
// alert resolves scalar(lenny_audit_siem_max_delivery_lag_seconds) to an
// operator-tunable threshold. F-12.3.17.
func (m *Metrics) SetSIEMMaxDeliveryLagSeconds(seconds float64) {
	if m == nil {
		return
	}
	m.siemMaxDeliveryLag.Set(seconds)
}

// IncAuditChainIntegrity advances the §16.1
// lenny_audit_chain_integrity_total counter for one tenant's §11.7
// chain state. The §12.3 line 101 startup chain-continuity check calls
// it once per tenant; state="broken" drives the §16.5 AuditChainGap
// alert. F-12.3.9.
func (m *Metrics) IncAuditChainIntegrity(state string) {
	if m == nil {
		return
	}
	m.auditChainIntegrity.WithLabelValues(state).Inc()
}

// IncAuditGrantDrift advances the §11.7 item 2
// lenny_audit_grant_drift_total counter. The periodic background
// integrity check calls it when it detects a grant / trigger /
// erasure-guard drift after startup; the §16.5 AuditGrantDrift alert
// fires on `lenny_audit_grant_drift_total > 0`. F-11.7.3.
func (m *Metrics) IncAuditGrantDrift() {
	if m == nil {
		return
	}
	m.auditGrantDrift.Inc()
}

// AddAuditRedactionReceiptMissing advances the §16.1
// lenny_audit_redaction_receipt_missing_total counter for one tenant by
// n. The periodic background integrity check calls it once per cycle
// with the number of §12.8-redaction-marked audit_log rows that lack a
// signature-verifying RedactionReceipt; the §16.5
// AuditRedactionReceiptMissing critical alert fires on any non-zero
// increase. A non-positive n is a no-op so a clean cycle never touches
// the series. F-11.7.15.
func (m *Metrics) AddAuditRedactionReceiptMissing(tenantID string, n int) {
	if m == nil || n <= 0 {
		return
	}
	m.auditRedactionReceiptMissing.WithLabelValues(tenantID).Add(float64(n))
}

// IncAuditOCSFTranslationFailed advances the §16.1
// lenny_audit_ocsf_translation_failed_total counter for one per-row
// OCSF translation failure. The §11.7 translator's Metrics adapter
// calls it as the state machine advances the row toward retry_pending /
// dead_lettered; the (event_type, error_class) labels let operators
// localize an event-schema gap. F-11.7.1 / F-11.7.15.
func (m *Metrics) IncAuditOCSFTranslationFailed(eventType, errorClass string) {
	if m == nil {
		return
	}
	m.auditOCSFTranslationFailed.WithLabelValues(eventType, errorClass).Inc()
}

// ObserveAuditQueryDuration records one §25.9 audit-query latency,
// labeled by the endpoint (list/get/summary/verify) and the shard count
// the query fanned out across. F-25.9.13.
func (m *Metrics) ObserveAuditQueryDuration(endpoint string, shards int, seconds float64) {
	if m == nil {
		return
	}
	m.auditQueryDuration.WithLabelValues(endpoint, strconv.Itoa(shards)).Observe(seconds)
}

// IncAuditChainVerificationBroken increments the §25.9 broken-segment
// counter when a query surfaces a tampered chain segment. F-25.9.13.
func (m *Metrics) IncAuditChainVerificationBroken() {
	if m == nil {
		return
	}
	m.auditChainVerificationBroken.Inc()
}

// IncAuditChainRechainedPostOutage increments the §25.9 post-outage
// rechain counter when a query surfaces a segment rechained after a
// Postgres outage (not tamper evidence). F-25.9.13.
func (m *Metrics) IncAuditChainRechainedPostOutage() {
	if m == nil {
		return
	}
	m.auditChainRechainedPostOutage.Inc()
}

// IncAuditRateLimited counts one §25.9 audit event dropped by the
// diagnostics rate limiter, labeled by event_type and service_account.
// F-25.9.13.
func (m *Metrics) IncAuditRateLimited(eventType, serviceAccount string) {
	if m == nil {
		return
	}
	m.auditRateLimited.WithLabelValues(eventType, serviceAccount).Inc()
}

// IncMinioReplicationResidencyViolation records one §25.11 ArtifactStore
// cross-region replication residency violation for region. It advances
// both the region-scoped lenny_minio_replication_residency_violation_total
// and the shared lenny_data_residency_violation_total (operation
// "artifact_replication"), matching the replication Controller's
// Metrics.ResidencyViolation contract. F-12.5.20 / F-16.7.2.
func (m *Metrics) IncMinioReplicationResidencyViolation(region string) {
	if m == nil {
		return
	}
	m.minioReplicationResidencyViolation.WithLabelValues(region).Inc()
	m.dataResidencyViolation.WithLabelValues("artifact_replication").Inc()
}

// SetMinioReplicationLag sets the §17.3 / §25.11 ArtifactStore
// replication-lag gauge for a source region. The replication Controller's
// MeasureAll calls it each measurement tick. F-17.3.7.
func (m *Metrics) SetMinioReplicationLag(region string, seconds float64) {
	if m == nil {
		return
	}
	m.minioReplicationLagSeconds.WithLabelValues(region).Set(seconds)
}

// AddMinioReplicationFailed advances the §25.11 ArtifactStore
// replication-failure counter for a source region by delta. The caller
// converts the source cluster's cumulative failure total into a
// non-negative increment. F-17.3.7.
func (m *Metrics) AddMinioReplicationFailed(region string, delta float64) {
	if m == nil || delta <= 0 {
		return
	}
	m.minioReplicationFailed.WithLabelValues(region).Add(delta)
}

// IncDataResidencyViolation records one §16.1 data-residency violation
// for the named operation. It is the general entry point for the shared
// lenny_data_residency_violation_total series; the replication preflight
// uses IncMinioReplicationResidencyViolation, which calls through to this
// series with operation "artifact_replication". F-12.5.20.
func (m *Metrics) IncDataResidencyViolation(operation string) {
	if m == nil {
		return
	}
	m.dataResidencyViolation.WithLabelValues(operation).Inc()
}

// IncPlatformAuditRegionUnresolvable records one §11.7 line 433 CMP-058
// platform-tenant audit residency resolution failure: a platform-tenant
// audit write referencing a regulated target tenant could not resolve
// that tenant's regional platform-Postgres. region is the target
// tenant's requested dataResidencyRegion; failureMode is "missing_entry"
// (no storage.regions.<region>.postgresEndpoint entry) or
// "postgres_unreachable" (the entry exists but the pool is unreachable).
// The CMP-058 gate also bumps IncDataResidencyViolation("platform_audit_write").
// F-11.7.9.
func (m *Metrics) IncPlatformAuditRegionUnresolvable(region, failureMode string) {
	if m == nil {
		return
	}
	m.platformAuditRegionUnresolvable.WithLabelValues(region, failureMode).Inc()
}

// ObserveAuditScatterGatherShards records the §25.9 shard fan-out width
// for one audit query (1 in single-shard v1). F-25.9.13.
func (m *Metrics) ObserveAuditScatterGatherShards(shards int) {
	if m == nil {
		return
	}
	m.auditScatterGatherShards.Observe(float64(shards))
}

// SetExtractionThreshold emits the §4.1 configured per-subsystem
// extraction threshold on the lenny_gateway_extraction_threshold
// gauge. The subsystem label must be one of `stream_proxy`,
// `upload_handler`, `mcp_fabric`, `llm_proxy`; the metric label
// matches the `gateway.extractionThresholds.<subsystem>.<metric>`
// Helm key in snake_case form (e.g., `queue_depth`,
// `active_concurrent`). The gateway calls this once per configured
// threshold at startup so the values used for an extraction decision
// are auditable against /metrics.
func (m *Metrics) SetExtractionThreshold(subsystem, metric string, value float64) {
	m.extractionThreshold.WithLabelValues(subsystem, metric).Set(value)
}

// SetGCPauseP99Ms updates the §4.1 process-level GC pause p99 gauge
// (milliseconds). The periodic collector in cmd/lenny-gateway/main.go
// reads runtime/debug.ReadGCStats, computes the p99 over a sliding
// window, and pushes the value here so the §16.5 Tier3GCPressureHigh
// alert can evaluate.
func (m *Metrics) SetGCPauseP99Ms(value float64) {
	m.gcPauseP99Ms.Set(value)
}

// SetReplayBufferUtilization updates the §16 catalog
// lenny_event_bus_replay_buffer_utilization gauge. The caller samples
// the worst per-session ratio across the session SSE replay buffer
// periodically; ratio is in [0,1]. spec: §10.4 line 389. F-10.4.11.
func (m *Metrics) SetReplayBufferUtilization(ratio float64) {
	if m == nil {
		return
	}
	m.replayBufferUtilization.Set(ratio)
}

// IncPDBBlockedEvictions advances the §16 catalog
// lenny_pdb_blocked_evictions_total counter for a PDB observed as
// blocking by the periodic poller. spec: §10.4 / §16.5
// PDBBlockedEvictions. F-10.4.4.
func (m *Metrics) IncPDBBlockedEvictions(pdb, controller string) {
	if m == nil {
		return
	}
	m.pdbBlockedEvictions.WithLabelValues(pdb, controller).Inc()
}

// ObserveMemoryStoreOperation records one observation on the §9.4 /
// §16.1 line 151 lenny_memory_store_operation_duration_seconds
// histogram. The operation label is one of write, query, delete, list,
// delete_by_user, delete_by_tenant; backend is the implementation tag.
// F-9.4.1.
func (m *Metrics) ObserveMemoryStoreOperation(operation, backend string, seconds float64) {
	if m == nil {
		return
	}
	m.memoryStoreOperationDuration.WithLabelValues(operation, backend).Observe(seconds)
}

// IncMemoryStoreError advances the §9.4 / §16.1 line 152
// lenny_memory_store_errors_total counter. error_type is the caller's
// error classification. F-9.4.1.
func (m *Metrics) IncMemoryStoreError(operation, backend, errorType string) {
	if m == nil {
		return
	}
	m.memoryStoreErrors.WithLabelValues(operation, backend, errorType).Inc()
}

// SetMemoryStoreRecordCount updates the §9.4 / §16.1 line 153
// lenny_memory_store_record_count gauge for tenantID. The caller is the
// periodic sampler. F-9.4.1.
func (m *Metrics) SetMemoryStoreRecordCount(tenantID string, count int) {
	if m == nil {
		return
	}
	m.memoryStoreRecordCount.WithLabelValues(tenantID).Set(float64(count))
}

// IncMemoryStoreUserOverThreshold advances the §9.4 / §16.1 line 154
// lenny_memory_store_user_count_over_threshold_total counter. The
// MemoryStore.Write path increments it once per commit that leaves the
// writing user at >= 80% of memory.maxMemoriesPerUser. F-9.4.6.
func (m *Metrics) IncMemoryStoreUserOverThreshold(tenantID, backend string) {
	if m == nil {
		return
	}
	m.memoryStoreUserOverThreshold.WithLabelValues(tenantID, backend).Inc()
}

// SetTimeDrift publishes the §13.3 line 595 lenny_time_drift_seconds
// gauge. Driven by the pkg/driftmonitor sampler. Value is the signed
// offset in seconds (positive = ahead of NTP reference, negative =
// behind). The §16.5 GatewayClockDrift alert keys on
// `abs(lenny_time_drift_seconds) > 0.5`. F-13.3.5.
func (m *Metrics) SetTimeDrift(seconds float64) {
	if m == nil {
		return
	}
	m.timeDrift.Set(seconds)
}

// ObserveRevocationPropagation observes one token-revocation propagation
// latency onto the §16.1 / §16.5 lenny_token_revocation_propagation_seconds
// histogram keyed by the §16.7 propagation_mode outcome. The gateway calls
// it from the §17.6 in-process admin-credential rotation path with
// outcome=`postgres_only` (the gateway durably revokes the superseded token
// in Postgres and does not publish on the EventBus). The §16.5
// TokenRevocationPropagationLag alert keys on the `outcome="eventbus"`
// bucket, so the gateway's `postgres_only` observation does not fire the
// alert; it makes the rotation path a declared producer of the metric the
// SEC-TS-1 design names on both the Token Service and the gateway paths.
// spec: §16.5, §16.7 — SEC-TS-1.
func (m *Metrics) ObserveRevocationPropagation(outcome string, d time.Duration) {
	if m == nil {
		return
	}
	m.revocationPropagation.WithLabelValues(outcome).Observe(d.Seconds())
}

// Middleware returns an http.Handler that records the §16.1 request
// metrics for every request to inner. The route label is taken from
// the supplied routeOf function so high-cardinality path segments
// (session ids, blob refs) collapse to a stable route template.
//
// The middleware also tracks the in-flight request count exposed via
// InflightRequests so the §16.1 lenny_gateway_request_queue_depth
// gauge — the §4.1 SCL-026 primary HPA scale-out trigger — reflects
// the instantaneous concurrent-request count on this replica.
func (m *Metrics) Middleware(inner http.Handler, routeOf func(*http.Request) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := routeOf(r)
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		atomic.AddInt64(&m.inflight, 1)
		defer atomic.AddInt64(&m.inflight, -1)
		inner.ServeHTTP(rec, r)
		elapsed := time.Since(start).Seconds()

		m.requestsTotal.WithLabelValues(r.Method, route, statusClass(rec.status)).Inc()
		m.requestDuration.WithLabelValues(r.Method, route).Observe(elapsed)
	})
}

// InflightRequests returns the number of HTTP requests currently
// being handled by the metrics Middleware. The watchdog poller in
// cmd/lenny-gateway/main.go reads this value and pushes it through
// SetRequestQueueDepth to surface it on the
// lenny_gateway_request_queue_depth gauge (§4.1 SCL-026).
func (m *Metrics) InflightRequests() int {
	return int(atomic.LoadInt64(&m.inflight))
}

// statusRecorder captures the response status code so the metrics
// middleware can label by status class.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.wroteHeader = true
	}
	return s.ResponseWriter.Write(b)
}

// Flush forwards to the underlying http.Flusher when the embedded
// ResponseWriter supports streaming. The §15.1 SSE event stream at
// GET /v1/sessions/{id}/events and the §4.9 LLM-proxy streaming
// translators rely on http.Flusher to push partial bytes to the
// client; without this forwarder the metrics middleware hides the
// Flusher and the SSE handler returns 500
// ("response writer does not support streaming"). The §16.1
// request-metrics accounting is unaffected because Write already
// passes through.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		if !s.wroteHeader {
			s.wroteHeader = true
		}
		f.Flush()
	}
}

// Hijack forwards to the underlying http.Hijacker so a WebSocket upgrade
// survives the request-metrics wrapper. The §15.2 / §27.5 MCP WebSocket
// transport at /mcp/v1/ws upgrades through this middleware;
// nhooyr.io/websocket performs a direct http.Hijacker type assertion on
// the handed ResponseWriter, so without this forwarder the metrics
// wrapper hides the Hijacker and the upgrade fails. spec: §27.5 /
// §27.3.1 line 142.
func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := s.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, errors.New("gatewaymetrics: underlying ResponseWriter does not support hijacking")
}

// statusClass collapses an HTTP status to its §16.1.1 low-cardinality
// class label (2xx, 3xx, 4xx, 5xx) so the metric does not explode
// into one series per status code.
func statusClass(code int) string {
	switch {
	case code >= 500:
		return "5xx"
	case code >= 400:
		return "4xx"
	case code >= 300:
		return "3xx"
	case code >= 200:
		return "2xx"
	default:
		return strconv.Itoa(code/100) + "xx"
	}
}
