// SPDX-License-Identifier: MIT

package llmproxy

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// PromMetrics is the metering proxy's own metric set. The composition root
// registers it on the gateway's metric registry, as the other parts that
// own their collectors do, so the metering proxy holds no value of the
// replica's gateway metrics, which a seam may not import. It implements
// Metrics and FallbackMetrics, and counts the sessions the budget enforcer
// ends for token-budget exhaustion. The metric names, labels, and buckets
// are the ones the gateway metrics registered before the metering proxy
// owned them.
type PromMetrics struct {
	activeConnections           prometheus.Gauge
	translationDuration         *prometheus.HistogramVec
	translationErrors           *prometheus.CounterVec
	credentialRotation          *prometheus.CounterVec
	credentialFallbackExhausted *prometheus.CounterVec
	sessionBudgetExceeded       *prometheus.CounterVec
}

var (
	_ Metrics         = (*PromMetrics)(nil)
	_ FallbackMetrics = (*PromMetrics)(nil)
)

// NewPromMetrics builds the metering proxy's metric set and registers it
// on reg. It fails when a collector cannot be built or registered, such as
// when reg already holds a metric of the same name.
func NewPromMetrics(reg prometheus.Registerer) (*PromMetrics, error) {
	// §16.1: the in-flight LLM proxy requests on the replica.
	activeConnections, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_llm_proxy_active_connections",
		Help: "LLM Proxy active connections (§16.1).",
	}, nil)
	if err != nil {
		return nil, err
	}
	// §16.1: the native-translator CPU time per leg, upstream network
	// time excluded. direction is request or response.
	translationDuration, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_gateway_llm_translation_duration_seconds",
		Help:    "Native LLM translator CPU time per leg (§16.1).",
		Buckets: prometheus.ExponentialBuckets(0.0005, 2, 12),
	}, []string{"pool", "provider", "proxy_dialect", "direction"})
	if err != nil {
		return nil, err
	}
	// §16.1: native-translator failures by the §4.9 error taxonomy.
	translationErrors, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_gateway_llm_translation_errors_total",
		Help: "LLM translator failures by error type (§16.1).",
	}, []string{"pool", "provider", "error_type"})
	if err != nil {
		return nil, err
	}
	// §16.1: fault-driven credential rotations by error type, which the
	// §4.9 Fallback Flow counts when it rotates a faulted lease to the
	// chain's next pool.
	credentialRotation, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_credential_rotation_total",
		Help: "Credential rotations by error type (§16.1).",
	}, []string{"error_type"})
	if err != nil {
		return nil, err
	}
	// §4.9: fallback-chain exhaustions, labeled by pool, provider, and
	// error type. The CredentialFallbackExhausted condition is terminal for
	// the session.
	credentialFallbackExhausted, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_gateway_credential_fallback_exhausted_total",
		Help: "Credential fallback-chain exhaustions by pool, provider, and error type (§4.9).",
	}, []string{"pool", "provider", "error_type"})
	if err != nil {
		return nil, err
	}
	// §11.2: sessions the budget enforcer ends mid-flight because their
	// cumulative proxy-recorded token usage exhausted the session's token
	// budget.
	sessionBudgetExceeded, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_gateway_session_budget_exceeded_total",
		Help: "§11.2 sessions terminated mid-session for token-budget exhaustion, by tenant.",
	}, []string{"tenant_id"})
	if err != nil {
		return nil, err
	}
	for _, c := range []prometheus.Collector{
		activeConnections, translationDuration, translationErrors,
		credentialRotation, credentialFallbackExhausted, sessionBudgetExceeded,
	} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("register metering proxy metric: %w", err)
		}
	}
	return &PromMetrics{
		activeConnections:           activeConnections.WithLabelValues(),
		translationDuration:         translationDuration,
		translationErrors:           translationErrors,
		credentialRotation:          credentialRotation,
		credentialFallbackExhausted: credentialFallbackExhausted,
		sessionBudgetExceeded:       sessionBudgetExceeded,
	}, nil
}

// IncLLMProxyConnections raises the active-connection gauge when a proxy
// request enters the handler.
func (m *PromMetrics) IncLLMProxyConnections() {
	if m == nil {
		return
	}
	m.activeConnections.Inc()
}

// DecLLMProxyConnections lowers the active-connection gauge when a proxy
// request leaves the handler.
func (m *PromMetrics) DecLLMProxyConnections() {
	if m == nil {
		return
	}
	m.activeConnections.Dec()
}

// ObserveLLMTranslation records the translator CPU time of one leg.
func (m *PromMetrics) ObserveLLMTranslation(pool, provider, proxyDialect, direction string, seconds float64) {
	if m == nil {
		return
	}
	m.translationDuration.WithLabelValues(pool, provider, proxyDialect, direction).Observe(seconds)
}

// IncLLMTranslationError counts a translator failure by the error type a
// *TranslationError carries.
func (m *PromMetrics) IncLLMTranslationError(pool, provider, errorType string) {
	if m == nil {
		return
	}
	m.translationErrors.WithLabelValues(pool, provider, errorType).Inc()
}

// IncCredentialRotation counts a fault-driven credential rotation by error
// type.
func (m *PromMetrics) IncCredentialRotation(errorType string) {
	if m == nil {
		return
	}
	m.credentialRotation.WithLabelValues(errorType).Inc()
}

// IncCredentialFallbackExhausted counts a fallback-chain exhaustion, after
// which the session ends with CREDENTIAL_FALLBACK_EXHAUSTED.
func (m *PromMetrics) IncCredentialFallbackExhausted(pool, provider, errorType string) {
	if m == nil {
		return
	}
	m.credentialFallbackExhausted.WithLabelValues(pool, provider, errorType).Inc()
}

// IncSessionBudgetExceeded counts a session the budget enforcer ended for
// token-budget exhaustion.
func (m *PromMetrics) IncSessionBudgetExceeded(tenantID string) {
	if m == nil {
		return
	}
	m.sessionBudgetExceeded.WithLabelValues(tenantID).Inc()
}
