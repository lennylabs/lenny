// SPDX-License-Identifier: MIT

package gatewaymetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// elicitationMetrics holds the §16.5 elicitation content-integrity gauge
// that the gateway's tenant reconciliation loop sets. The elicitation
// drop, tamper, and lifecycle metrics are registered by the platform
// tools, which emit them.
type elicitationMetrics struct {
	// elicitationIntegrityWeakened is the §16.5 standing-alert
	// gauge: the count of active tenants whose §9.2 effective
	// elicitation content-integrity mode is weaker than enforce. A
	// gateway reconciliation loop refreshes it from the tenant store.
	// F-9.2.5.
	elicitationIntegrityWeakened prometheus.Gauge
}

// newElicitationMetrics constructs, registers, and materializes the elicitation metric subsystem
// against reg. spec: §16 observability metrics.
func newElicitationMetrics(reg *prometheus.Registry) (elicitationMetrics, error) {
	var m elicitationMetrics
	// spec: §16.5 — the standing ElicitationContentIntegrityWeakened
	// alert reads this gauge. It is a gateway-process count of active
	// tenants whose §9.2 effective elicitation content-integrity mode
	// (max(platformFloor, tenantStored)) is weaker than enforce. The
	// alert fires while the value is > 0 and resolves once every active
	// tenant's effective mode is enforce. Unlabelled to keep the gauge
	// cardinality-free; operators identify which tenants are weakened
	// from the paired tenant.elicitation_content_integrity_changed audit
	// events, per the §16.5 runbook. F-9.2.5.
	elicitationIntegrityWeakened, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_elicitation_content_integrity_effective_mode_weaker_than_enforce",
		Help: "Active tenants whose §9.2 effective elicitation content-integrity enforcement mode is weaker than enforce (§16.5). Standing-alert numerator; zero when every active tenant resolves to enforce.",
	}, nil)
	if err != nil {
		return m, err
	}
	reg.MustRegister(elicitationIntegrityWeakened)
	m.elicitationIntegrityWeakened = elicitationIntegrityWeakened.WithLabelValues()
	return m, nil
}
