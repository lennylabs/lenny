// SPDX-License-Identifier: MIT

package gatewaymetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// experimentMetrics holds the §10.7 / §16.1 experiment-targeting and variant rollback-trigger metrics.
type experimentMetrics struct {
	experimentIsoRej           *prometheus.CounterVec
	experimentTargetingDur     *prometheus.HistogramVec
	experimentTargetingErr     *prometheus.CounterVec
	experimentStickyInval      *prometheus.CounterVec
	experimentTargetingCircuit *prometheus.GaugeVec
	sessionTotal               *prometheus.CounterVec
	sessionError               *prometheus.CounterVec
	sessionDuration            *prometheus.HistogramVec
	evalScore                  *prometheus.HistogramVec
	noEnvPolicyAllowAll        *prometheus.CounterVec
	sessionBudgetExceeded      *prometheus.CounterVec
}

// newExperimentMetrics constructs, registers, and materializes the experiment metric subsystem
// against reg. spec: §16 observability metrics.
func newExperimentMetrics(reg *prometheus.Registry) (experimentMetrics, error) {
	var m experimentMetrics
	experimentIsoRej, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_experiment_isolation_rejections_total",
		Help: "Total sessions the §10.7 ExperimentRouter rejected closed because the variant pool's isolation profile was weaker than the session's.",
	}, []string{"tenant_id", "experiment_id", "variant_id"})
	if err != nil {
		return m, err
	}
	// spec: §10.7 line 833 / §16.1 lines 156-157 — external experiment
	// targeting observability. The `provider` label carries the
	// OpenFeature provider name; for provider:ofrep the OFREP endpoint
	// hostname is used (§16.1 line 156). Buckets resolve the sub-second
	// range the §10.7 200ms targeting timeout is sized against.
	experimentTargetingDur, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_experiment_targeting_duration_seconds",
		Help:    "§10.7 external experiment targeting evaluation latency by provider (§16.1 line 156).",
		Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.2, 0.5, 1, 2, 5},
	}, []string{"provider"})
	if err != nil {
		return m, err
	}
	// spec: §10.7 line 833 / §16.1 line 157 — targeting_failed counter.
	// error_type classifies the §10.7 failure cause (timeout, transport,
	// or the OFREP errorCode).
	experimentTargetingErr, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_experiment_targeting_error_total",
		Help: "§10.7 external experiment targeting evaluation failures by provider and error_type (§16.1 line 157).",
	}, []string{"provider", "error_type"})
	if err != nil {
		return m, err
	}
	// spec: §10.7 line 1096 / §16.1 line 159 — incremented once per
	// sticky-cache flush, i.e. each time an experiment transitions to
	// paused or concluded and the gateway DELs its
	// `t:{tenant}:exp:{exp}:sticky:*` keys. Labeled by experiment_id and
	// transition (the target status: paused or concluded).
	experimentStickyInval, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_experiment_sticky_cache_invalidations_total",
		Help: "§10.7 sticky-assignment cache flushes on experiment pause/conclude, by experiment and transition.",
	}, []string{"experiment_id", "transition"})
	if err != nil {
		return m, err
	}
	// spec: §10.7 lines 835-844 (SCL-023) / §16.1 line 64 — the per-tenant
	// targeting circuit-breaker gauge: 1 while the breaker is open (the
	// gateway is skipping the OpenFeature call), 0 when closed. The §16.5
	// ExperimentTargetingCircuitOpen alert fires on a sustained 1.
	experimentTargetingCircuit, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_experiment_targeting_circuit_open",
		Help: "§10.7 SCL-023 targeting circuit-breaker state by tenant and provider: 1=open, 0=closed.",
	}, []string{"tenant_id", "provider"})
	if err != nil {
		return m, err
	}
	// spec: §16.1 lines 161-163 / §10.7 lines 1120-1132 — the variant-labelled
	// rollback-trigger metric family. session_type carries the session's
	// §5.2 ExecutionMode ("session", "service"); variant_id carries
	// the §10.7 experiment enrollment ("" for control / un-enrolled sessions).
	// lenny_session_total is the denominator for the variant error rate
	// (§16.1 line 162); lenny_session_error_total is the numerator (line 161).
	sessionTotal, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_session_total",
		Help: "§16.1 line 162 sessions total by variant; denominator for the §10.7 rollback-trigger error rate.",
	}, []string{"tenant_id", "session_type", "variant_id"})
	if err != nil {
		return m, err
	}
	sessionError, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_session_error_total",
		Help: "§16.1 line 161 session errors by variant; numerator for the §10.7 rollback-trigger error rate.",
	}, []string{"tenant_id", "session_type", "variant_id"})
	if err != nil {
		return m, err
	}
	// spec: §16.1 line 163 — per-session wall-clock duration sampled at
	// completion. Buckets span the §10.7 / §6 session lifetime (1s to the
	// 4-hour cert-expiry bound) so histogram_quantile(0.95, ...) resolves
	// the variant-vs-control p95 comparison the rollback table cites.
	sessionDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_session_duration_seconds",
		Help:    "§16.1 line 163 per-session wall-clock duration by variant, sampled at completion.",
		Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600, 1800, 3600, 7200, 14400},
	}, []string{"tenant_id", "session_type", "variant_id"})
	if err != nil {
		return m, err
	}
	// spec: §16.1 line 164 — one observation per submitted eval run. Scores
	// are normalized 0.0-1.0; the 0.95 bucket resolves the §10.7 line 1128
	// safety-score-regression threshold.
	evalScore, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_eval_score",
		Help:    "§16.1 line 164 eval score by variant; one observation per submitted eval run.",
		Buckets: []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 0.95, 1.0},
	}, []string{"tenant_id", "scorer", "variant_id"})
	if err != nil {
		return m, err
	}
	noEnvPolicyAllowAll, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_noenvironmentpolicy_allowall_total",
		Help: "Total tenant rbac-config writes that set noEnvironmentPolicy to allow-all (§10.6).",
	}, []string{"tenant_id"})
	if err != nil {
		return m, err
	}
	// spec: §11.2 line 44 — count sessions terminated mid-flight because
	// their cumulative proxy-recorded LLM token usage exhausted the
	// session's token budget (the §4.9 LLM-proxy enforcer fast path).
	sessionBudgetExceeded, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_gateway_session_budget_exceeded_total",
		Help: "§11.2 sessions terminated mid-session for token-budget exhaustion, by tenant.",
	}, []string{"tenant_id"})
	if err != nil {
		return m, err
	}
	reg.MustRegister(experimentIsoRej,
		experimentTargetingDur,
		experimentTargetingErr,
		experimentStickyInval,
		experimentTargetingCircuit,
		sessionTotal,
		sessionError,
		sessionDuration,
		evalScore,
		noEnvPolicyAllowAll,
		sessionBudgetExceeded)
	m.experimentIsoRej = experimentIsoRej
	m.experimentTargetingDur = experimentTargetingDur
	m.experimentTargetingErr = experimentTargetingErr
	m.experimentStickyInval = experimentStickyInval
	m.experimentTargetingCircuit = experimentTargetingCircuit
	m.sessionTotal = sessionTotal
	m.sessionError = sessionError
	m.sessionDuration = sessionDuration
	m.evalScore = evalScore
	m.noEnvPolicyAllowAll = noEnvPolicyAllowAll
	m.sessionBudgetExceeded = sessionBudgetExceeded
	return m, nil
}
