// SPDX-License-Identifier: MIT

package gatewaymetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// sessionLifecycleMetrics holds the §6.3 / §7.x startup, TTFT, warm-pool, retry, resume, and expiry metrics.
type sessionLifecycleMetrics struct {
	// sessionStartupDuration is the §16.1 line 14 / §6.3 line 348
	// end-to-end pod-warm startup histogram: pod claim through agent
	// session ready, excluding client file upload and workspace
	// materialization. Labels: `pool`, `runtime_class`,
	// `isolation_profile`. Consumed by the StartupLatencyBurnRate and
	// StartupLatencyGVisorBurnRate alerts via the
	// lenny_session_startup_duration_slow_ratio recording rule. Buckets
	// straddle the 2s (runc) and 5s (gVisor) SLO thresholds.
	sessionStartupDuration *prometheus.HistogramVec
	// sessionStartupPhaseDuration is the §6.3 line 372 per-phase
	// hot-path histogram. Labels: `phase` (pod_claim,
	// workspace_materialization, setup_commands, credential_assignment,
	// agent_session_start) and `runtime_class`. Each phase is observed
	// independently so a slow startup can be localized to one phase.
	sessionStartupPhaseDuration *prometheus.HistogramVec
	// sessionTimeToFirstToken is the §16.1 line 15 / §6.3 line 356
	// end-to-end TTFT histogram: session start request (POST
	// /v1/sessions admission) through the first agent-streamed
	// response event emitted to the SSE client. Labels: `pool`,
	// `runtime_class`, `isolation_profile`. Consumed by the
	// TTFTBurnRate alert via an inline expression against the le="10"
	// bucket boundary (the §6.3 10s SLO threshold).
	sessionTimeToFirstToken *prometheus.HistogramVec
	// warmpoolClaims is the §16.1 line 122 / §6.3 line 352
	// `lenny_warmpool_claims_total{pool,runtime_class}` counter:
	// incremented on each idle→claimed transition in the §6.1 warm
	// pool. It is the denominator of the §6.3 line 352 SDK-warm
	// demotion-rate ratio. spec: §6.3 line 352, §16.1 line 122.
	warmpoolClaims *prometheus.CounterVec
	// warmpoolSDKDemotions is the §6.1 line 34 / §16.1 line 121
	// `lenny_warmpool_sdk_demotions_total{pool}` counter: incremented each
	// time the binder demotes an SDK-warm pod to pod-warm because the
	// workspace plan matched a sdkWarmBlockingPaths pattern. It is the
	// numerator of the §6.3 line 352 demotion-rate ratio over
	// warmpoolClaims. spec: §6.1 line 34, §16.1 line 121.
	warmpoolSDKDemotions *prometheus.CounterVec
	// warmpoolSDKDemotionDuration is the §6.3 line 352
	// `lenny_warmpool_sdk_demotion_duration_seconds{pool}` histogram: the
	// per-demotion SDK teardown penalty (DemoteSDK wall-clock), which the
	// spec notes is "typically 1–3s". It lets deployers verify the
	// teardown penalty stays within the expected band when weighing
	// SDK-warm net benefit. spec: §6.3 line 352.
	warmpoolSDKDemotionDuration *prometheus.HistogramVec
	// sessionRetryTotal counts the §16.1 / §7.3
	// `lenny_session_retry_total{failure_class}` retries of a logical
	// session. Each successful pod recovery (the v1 retry path) bumps
	// the counter; the failure_class label echoes the row's §7.1
	// FailureClass at retry time. F-7.3.10.
	sessionRetryTotal *prometheus.CounterVec
	// deriveFailureAudit is the §16.1
	// `lenny_session_derive_failure_audit_total{outcome}` counter: one bump
	// per §7.1 derive rule 2 opt-in derive_failure audit-row write, labelled
	// by outcome (persisted | fenced | error). F-15.1.14.
	deriveFailureAudit *prometheus.CounterVec
	// sessionResumeAttempts counts the §16.1 / §7.3
	// `lenny_session_resume_attempts_total{pool, outcome}` counter:
	// every POST /v1/sessions/{id}/resume that passes the precondition
	// gate bumps it once with outcome "success" or "failure". F-7.3.10.
	sessionResumeAttempts *prometheus.CounterVec
	// sessionExpiry counts the §16.1
	// `lenny_session_expiry_total{pool, reason}` counter: a session
	// terminated by a platform expiry clock, by reason. The watchdog's
	// expiry sweeps bump it once per `→ expired` transition with reason
	// `max_idle_time` (the §6.2 maxClientIdleSeconds idle clock) or
	// `max_session_age` (the §11.3 maxSessionAge age cap and the §7.3
	// awaiting_client_action wall-clock deadline). spec: §16.1; §16.1.1
	// (reason vocabulary). F-11.3.7.
	sessionExpiry *prometheus.CounterVec
	// warmpoolWarmupFailure is the §16.1 line 124
	// `lenny_warmpool_warmup_failure_total{error_type}` counter:
	// incremented whenever a warm-pool-side §6.3 startup phase
	// (workspace prep, setup_command, credential assignment, etc.)
	// fails. error_type is the §7.3 non-retryable failure category
	// the gateway classified. F-7.5.9.
	// spec: §16.1 line 124, §7.3 line 387.
	warmpoolWarmupFailure *prometheus.CounterVec
	// injectionGateFailClosed is the
	// `lenny_injection_gate_failclosed_total{cause}` counter: incremented
	// once per §5.1 injection-gate fail-closed occurrence when a backing
	// store the gate consults returns a transient read error. cause is
	// "runtime_store" or "override_store", recording the granular cause
	// behind the coarse SERVICE_UNAVAILABLE client code as a metric so the
	// runtime-store-versus-override-store distinction stays observable.
	// spec: §5.1 (injection fail-closed), §15.1 (SERVICE_UNAVAILABLE) —
	// F-5.1.20.
	injectionGateFailClosed *prometheus.CounterVec
	// workspaceSealDuration is the §7.1 line 112 seal-and-export
	// completion-time histogram. Observed once per terminal session at
	// teardown. Labels: `pool` (the session runtime) and `outcome`
	// (success | timeout). The §16.5 WorkspaceSealStuck alert fires on a
	// nonzero count for outcome="timeout".
	workspaceSealDuration *prometheus.HistogramVec
	// checkpointStorageFailure counts the §4.4 line 262 non-eviction
	// MinIO-upload failures (all retries exhausted, the failed
	// checkpoint is discarded). Labels: `pool`, `level`, and `trigger`.
	checkpointStorageFailure *prometheus.CounterVec
	// checkpointEvictionFallback counts the §4.4 line 263 eviction-
	// fallback writes to Postgres. Labels: `pool` and
	// `had_prior_checkpoint`.
	checkpointEvictionFallback *prometheus.CounterVec
}

// newSessionLifecycleMetrics constructs, registers, and materializes the sessionlifecycle metric subsystem
// against reg. spec: §16 observability metrics.
func newSessionLifecycleMetrics(reg *prometheus.Registry) (sessionLifecycleMetrics, error) {
	var m sessionLifecycleMetrics
	// §16.1 line 14 / §6.3 line 348 — `lenny_session_startup_duration_seconds`
	// measures pod claim through agent session ready, excluding client
	// file upload and workspace materialization. Explicit buckets
	// include the 2s (runc) and 5s (gVisor) SLO thresholds so the
	// lenny_session_startup_duration_slow_ratio recording rule can read
	// the bucket boundary directly.
	sessionStartupDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_session_startup_duration_seconds",
		Help:    "End-to-end pod-warm session startup: pod claim through agent session ready, excluding upload and workspace materialization (§6.3 line 348).",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 1.5, 2, 3, 5, 8, 13},
	}, []string{"pool", "runtime_class", "isolation_profile"})
	if err != nil {
		return m, err
	}
	// §6.3 line 372 — `lenny_session_startup_phase_duration_seconds`
	// instruments each hot-path phase independently so a slow startup
	// can be localized. Buckets cover the per-phase budgets from the
	// §6.3 table (≤100ms claim/credential through ≤4.5s gVisor agent
	// start).
	sessionStartupPhaseDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_session_startup_phase_duration_seconds",
		Help:    "Per-phase pod-warm session startup latency (§6.3 line 372).",
		Buckets: []float64{0.025, 0.05, 0.1, 0.25, 0.5, 1, 1.5, 3, 4.5, 8},
	}, []string{"phase", "runtime_class"})
	if err != nil {
		return m, err
	}
	// §16.1 line 15 / §6.3 line 356 —
	// `lenny_session_time_to_first_token_seconds` measures session
	// start request (POST /v1/sessions admission) through the first
	// agent-streamed response event. Explicit buckets straddle the
	// 10s TTFT SLO threshold so the TTFTBurnRate alert reads the
	// le="10" bucket boundary directly. spec: §16.1 line 15; §6.3
	// line 356.
	sessionTimeToFirstToken, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_session_time_to_first_token_seconds",
		Help:    "End-to-end time to first token: session start request to first agent-streamed response event (§6.3 line 356, §16.1 line 15).",
		Buckets: []float64{0.1, 0.5, 1, 2, 3, 5, 8, 10, 15, 30, 60},
	}, []string{"pool", "runtime_class", "isolation_profile"})
	if err != nil {
		return m, err
	}
	// §16.1 line 122 / §6.3 line 352 — `lenny_warmpool_claims_total`
	// counts the idle→claimed transitions in the §6.1 warm pool and
	// is the denominator of the SDK-warm demotion-rate ratio
	// (`lenny_warmpool_sdk_demotions_total / lenny_warmpool_claims_total`).
	warmpoolClaims, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_warmpool_claims_total",
		Help: "Idle→claimed warm-pool transitions per pool (§6.3 line 352, §16.1 line 122).",
	}, []string{"pool", "runtime_class"})
	if err != nil {
		return m, err
	}
	// §6.1 line 34 / §16.1 line 121 — `lenny_warmpool_sdk_demotions_total`
	// counts SDK-warm pods demoted to pod-warm before session assignment
	// because the workspace plan matched sdkWarmBlockingPaths.
	warmpoolSDKDemotions, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_warmpool_sdk_demotions_total",
		Help: "SDK-warm pods demoted to pod-warm before session assignment per pool (§6.1 line 34, §16.1 line 121).",
	}, []string{"pool"})
	if err != nil {
		return m, err
	}
	// §6.3 line 352 — the SDK teardown penalty per demotion ("typically
	// 1–3s"). Buckets bracket that band plus the slow tail.
	warmpoolSDKDemotionDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_warmpool_sdk_demotion_duration_seconds",
		Help:    "SDK teardown penalty (DemoteSDK wall-clock) per demotion, per pool (§6.3 line 352).",
		Buckets: []float64{0.25, 0.5, 1, 1.5, 2, 3, 5, 10},
	}, []string{"pool"})
	if err != nil {
		return m, err
	}
	// §16.1 / §7.3 — `lenny_session_retry_total{failure_class}` counts
	// the retries of a logical session. Each pod-recovery retry bumps
	// the counter with the failure_class label echoing the row's §7.1
	// FailureClass at retry time. F-7.3.10.
	sessionRetryTotal, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_session_retry_total",
		Help: "Session-level retry attempts by failure class (§7.3, §16.1).",
	}, []string{"failure_class"})
	if err != nil {
		return m, err
	}
	// §7.1 derive rule 2 / §16.1 — `lenny_session_derive_failure_audit_total{outcome}`
	// counts derive_failure audit-row writes under the
	// `gateway.persistDeriveFailureRows` opt-in. F-15.1.14.
	deriveFailureAudit, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_session_derive_failure_audit_total",
		Help: "Derive-failure audit rows written by outcome (§7.1 derive rule 2, §16.1).",
	}, []string{"outcome"})
	if err != nil {
		return m, err
	}
	// §16.1 / §7.3 — `lenny_session_resume_attempts_total{pool, outcome}`
	// counts every POST /v1/sessions/{id}/resume call that passes the
	// precondition gate. Outcome is "success" when the row transitions
	// to running or "failure" when the pod-claim step fails. F-7.3.10.
	sessionResumeAttempts, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_session_resume_attempts_total",
		Help: "Session resume attempts by outcome (§7.3, §16.1).",
	}, []string{"pool", "outcome"})
	if err != nil {
		return m, err
	}
	// §16.1 — `lenny_session_expiry_total{pool, reason}` counts sessions
	// terminated by a platform expiry clock. The reason label is the §16.1.1
	// vocabulary (`max_session_age` | `max_idle_time`) the watchdog stamps on
	// each `→ expired` transition. F-11.3.7.
	sessionExpiry, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_session_expiry_total",
		Help: "Sessions terminated by a platform expiry clock, by reason (max_session_age | max_idle_time) (§16.1).",
	}, []string{"pool", "reason"})
	if err != nil {
		return m, err
	}
	// §16.1 line 124 — `lenny_warmpool_warmup_failure_total{error_type}`
	// counts warm-pool-side startup failures by §7.3 non-retryable
	// failure category. F-7.5.9.
	warmpoolWarmupFailure, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_warmpool_warmup_failure_total",
		Help: "Warm-pool warm-up failures by error_type (§16.1 line 124, §7.3 line 387).",
	}, []string{"error_type"})
	if err != nil {
		return m, err
	}
	// §5.1 injection fail-closed — `lenny_injection_gate_failclosed_total
	// {cause}` records the granular backing-store cause (runtime_store |
	// override_store) behind a coarse SERVICE_UNAVAILABLE when the §5.1
	// injection gate fails closed on a transient store read. F-5.1.20.
	injectionGateFailClosed, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_injection_gate_failclosed_total",
		Help: "§5.1 injection-gate fail-closed occurrences by backing-store cause (runtime_store | override_store).",
	}, []string{"cause"})
	if err != nil {
		return m, err
	}
	// §7.1 line 112 — `lenny_workspace_seal_duration_seconds` tracks
	// seal-and-export completion time across all terminal sessions,
	// labeled by `pool` (session runtime) and `outcome` (success |
	// timeout). The §16.5 WorkspaceSealStuck alert fires when the
	// outcome="timeout" count increases over a 5-minute window. Buckets
	// span the per-attempt 5s–60s backoff and the 300s default window.
	workspaceSealDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_workspace_seal_duration_seconds",
		Help:    "Workspace seal-and-export completion time by outcome (§7.1 line 112).",
		Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60, 120, 300},
	}, []string{"pool", "outcome"})
	if err != nil {
		return m, err
	}
	// §4.4 line 262 / §12.5 ll. 303 —
	// `lenny_checkpoint_storage_failure_total` counts non-eviction
	// MinIO-upload failures (all retries exhausted, the failed
	// checkpoint is discarded) AND the §12.5 fail-closed T4 KMS-
	// unavailable rejections. Labels: `pool`, `level`, `trigger`,
	// and `reason` — the §12.5 ll. 303 `{reason="kms_unavailable"}`
	// filter that the CheckpointStorageUnavailable alert reads.
	// Existing retry-exhaustion callers stamp `reason="retry_exhausted"`;
	// the blobstore's T4 fail-closed branch stamps
	// `reason="kms_unavailable"` and leaves `pool`/`level`/`trigger`
	// empty because the rejection fires before the adapter has the
	// wider context.
	checkpointStorageFailure, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_checkpoint_storage_failure_total",
		Help: "Checkpoint or artifact writes failed (§4.4 line 262 retry-exhausted, §12.5 ll. 303 kms_unavailable).",
	}, []string{"pool", "level", "trigger", "reason"})
	if err != nil {
		return m, err
	}
	// §4.4 line 263 — `lenny_checkpoint_eviction_fallback_total`
	// counts the entries into the eviction Postgres-fallback writer
	// when all MinIO retries for an eviction checkpoint failed.
	// Labels: `pool` (finite) and `had_prior_checkpoint` ("true" |
	// "false").
	checkpointEvictionFallback, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_checkpoint_eviction_fallback_total",
		Help: "Checkpoint storage fallbacks to Postgres minimal state (§4.4 line 263).",
	}, []string{"pool", "had_prior_checkpoint"})
	if err != nil {
		return m, err
	}
	reg.MustRegister(sessionStartupDuration,
		sessionStartupPhaseDuration,
		sessionTimeToFirstToken,
		warmpoolClaims,
		warmpoolSDKDemotions,
		warmpoolSDKDemotionDuration,
		sessionRetryTotal,
		sessionResumeAttempts,
		sessionExpiry,
		warmpoolWarmupFailure,
		injectionGateFailClosed,
		workspaceSealDuration,
		checkpointStorageFailure,
		checkpointEvictionFallback)
	m.sessionStartupDuration = sessionStartupDuration
	m.sessionStartupPhaseDuration = sessionStartupPhaseDuration
	m.sessionTimeToFirstToken = sessionTimeToFirstToken
	m.warmpoolClaims = warmpoolClaims
	m.warmpoolSDKDemotions = warmpoolSDKDemotions
	m.warmpoolSDKDemotionDuration = warmpoolSDKDemotionDuration
	m.sessionRetryTotal = sessionRetryTotal
	m.deriveFailureAudit = deriveFailureAudit
	m.sessionResumeAttempts = sessionResumeAttempts
	m.sessionExpiry = sessionExpiry
	m.warmpoolWarmupFailure = warmpoolWarmupFailure
	m.injectionGateFailClosed = injectionGateFailClosed
	m.workspaceSealDuration = workspaceSealDuration
	m.checkpointStorageFailure = checkpointStorageFailure
	m.checkpointEvictionFallback = checkpointEvictionFallback
	return m, nil
}
