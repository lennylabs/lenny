// SPDX-License-Identifier: MIT

package leasecontrol

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// PromMetrics is the lease-extension service's own metric set. The
// composition root registers it on the gateway's metric registry, so the
// platform tools seam holds no value of the replica's gateway metrics,
// which a seam may not import. It implements MetricEmitter. The metric
// name, help text, and labels are the ones the gateway metrics registered
// before the service owned them.
type PromMetrics struct {
	extension *prometheus.CounterVec
}

var _ MetricEmitter = (*PromMetrics)(nil)

// NewPromMetrics builds the service's metric set and registers it on reg.
// It fails when the collector cannot be built or registered, such as when
// reg already holds a metric of the same name.
func NewPromMetrics(reg prometheus.Registerer) (*PromMetrics, error) {
	// §16.1: lease-extension decisions by tenant and the audit's outcome
	// (approved | capped | denied).
	extension, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_delegation_lease_extension_total",
		Help: "§8.6 delegation lease-extension decisions by tenant and §8.6 outcome.",
	}, []string{"tenant_id", "outcome"})
	if err != nil {
		return nil, err
	}
	if err := reg.Register(extension); err != nil {
		return nil, fmt.Errorf("register lease extension metric: %w", err)
	}
	return &PromMetrics{extension: extension}, nil
}

// IncDelegationLeaseExtension counts one lease-extension decision.
func (m *PromMetrics) IncDelegationLeaseExtension(tenantID, outcome string) {
	if m == nil {
		return
	}
	m.extension.WithLabelValues(tenantID, outcome).Inc()
}
