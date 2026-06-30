// SPDX-License-Identifier: MIT

package playground

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// Metrics is the §27.8 playground metric set. It is registered
// against a Prometheus registry the gateway owns. A nil *Metrics is a
// valid no-op: every increment method short-circuits, so the handler
// runs without branching on whether metrics are wired.
type Metrics struct {
	pageViews           *prometheus.CounterVec
	sessionsCreated     *prometheus.CounterVec
	wsConnect           *prometheus.CounterVec
	revocations         *prometheus.CounterVec
	revocationProp      *prometheus.HistogramVec
	devTenantNotSeededC prometheus.Counter

	// mintRejected counts §10.2 line 243 playground mint rejections,
	// labelled by the invariant id (subject_typ_invalid,
	// tenant_claim_missing, tenant_claim_invalid_format,
	// tenant_not_found, …).
	mintRejected *prometheus.CounterVec
}

// NewMetrics registers the §27.8 playground metrics against reg and
// returns the set. The caller passes the gateway's private registry.
func NewMetrics(reg prometheus.Registerer) (*Metrics, error) {
	pageViews, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_playground_page_views_total",
		Help: "Playground index loads, labelled by auth mode (§27.8).",
	}, []string{"authMode"})
	if err != nil {
		return nil, err
	}
	sessionsCreated, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_playground_sessions_created_total",
		Help: "Sessions initiated from the playground, labelled by runtime (§27.8).",
	}, []string{"runtime"})
	if err != nil {
		return nil, err
	}
	wsConnect, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_playground_ws_connect_total",
		Help: "MCP WebSocket connections opened from the playground, labelled by outcome (§27.8).",
	}, []string{"outcome"})
	if err != nil {
		return nil, err
	}
	revocations, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_playground_session_revocations_total",
		Help: "Playground session-record revocations, labelled by reason (§27.8).",
	}, []string{"reason"})
	if err != nil {
		return nil, err
	}
	revocationProp, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name: "lenny_playground_session_revocation_propagation_seconds",
		Help: "End-to-end revocation propagation latency across gateway replicas (§27.8).",
		Buckets: []float64{
			0.005, 0.010, 0.025, 0.050, 0.100, 0.250, 0.500, 1.0, 2.5,
		},
	}, []string{"outcome"})
	if err != nil {
		return nil, err
	}
	devTenantNotSeeded, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_playground_dev_tenant_not_seeded_total",
		Help: "Playground requests rejected with 503 LENNY_PLAYGROUND_DEV_TENANT_NOT_SEEDED (§27.8).",
	}, nil)
	if err != nil {
		return nil, err
	}
	// spec: §10.2 line 243 — emit on every rejected playground mint,
	// labelled by the §10.2 invariant id.
	mintRejected, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_playground_bearer_mint_rejected_total",
		Help: "Playground mints rejected at a §10.2 invariant, labelled by reason (§10.2 line 243).",
	}, []string{"reason"})
	if err != nil {
		return nil, err
	}
	for _, c := range []prometheus.Collector{
		pageViews, sessionsCreated, wsConnect, revocations, revocationProp, devTenantNotSeeded, mintRejected,
	} {
		if err := reg.Register(c); err != nil {
			return nil, err
		}
	}
	return &Metrics{
		pageViews:           pageViews,
		sessionsCreated:     sessionsCreated,
		wsConnect:           wsConnect,
		revocations:         revocations,
		revocationProp:      revocationProp,
		devTenantNotSeededC: devTenantNotSeeded.WithLabelValues(),
		mintRejected:        mintRejected,
	}, nil
}

// bearerMintRejected increments
// lenny_playground_bearer_mint_rejected_total{reason}. Nil-safe.
//
// spec: §10.2 line 243.
func (m *Metrics) bearerMintRejected(reason string) {
	if m == nil {
		return
	}
	m.mintRejected.WithLabelValues(reason).Inc()
}

// pageView increments lenny_playground_page_views_total for the
// supplied auth mode.
func (m *Metrics) pageView(mode AuthMode) {
	if m == nil {
		return
	}
	m.pageViews.WithLabelValues(string(mode)).Inc()
}

// sessionCreated increments lenny_playground_sessions_created_total
// for the supplied runtime. The gateway calls it from the
// session-creation path when the origin claim is playground.
func (m *Metrics) sessionCreated(runtime string) {
	if m == nil {
		return
	}
	m.sessionsCreated.WithLabelValues(runtime).Inc()
}

// SessionCreated is the exported §27.8 hook the gateway session server
// drives (via the sessionserver IncPlaygroundSessionCreated callback)
// once the §27.3 origin=playground claim is read on the create path, so
// lenny_playground_sessions_created_total{runtime} counts every
// playground-originated session the gateway admits. Nil-safe.
// spec: §27.8; F-27.6.11.
func (m *Metrics) SessionCreated(runtime string) { m.sessionCreated(runtime) }

// wsConnectOutcome increments lenny_playground_ws_connect_total for
// the supplied outcome (success or failure).
func (m *Metrics) wsConnectOutcome(outcome string) {
	if m == nil {
		return
	}
	m.wsConnect.WithLabelValues(outcome).Inc()
}

// revocation increments lenny_playground_session_revocations_total
// for the supplied §27.8 reason.
func (m *Metrics) revocation(reason string) {
	if m == nil {
		return
	}
	m.revocations.WithLabelValues(reason).Inc()
}

// revocationPropagation records a §27.8 propagation-latency sample
// for the supplied outcome.
func (m *Metrics) revocationPropagation(outcome string, seconds float64) {
	if m == nil {
		return
	}
	m.revocationProp.WithLabelValues(outcome).Observe(seconds)
}

// devTenantNotSeeded increments
// lenny_playground_dev_tenant_not_seeded_total.
func (m *Metrics) devTenantNotSeeded() {
	if m == nil {
		return
	}
	m.devTenantNotSeededC.Inc()
}
