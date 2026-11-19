// SPDX-License-Identifier: MIT

package delegationbudget

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// PromMetrics is the delegation budget reconciler's own metric set. The
// composition root registers it on the gateway's metric registry, so the
// platform tools seam holds no value of the replica's gateway metrics,
// which a seam may not import. It implements MetricEmitter. The metric
// name, help text, and labels are the ones the gateway metrics registered
// before the reconciler owned them.
type PromMetrics struct {
	reconstruction *prometheus.CounterVec
}

var _ MetricEmitter = (*PromMetrics)(nil)

// NewPromMetrics builds the reconciler's metric set and registers it on
// reg. It fails when the collector cannot be built or registered, such as
// when reg already holds a metric of the same name.
func NewPromMetrics(reg prometheus.Registerer) (*PromMetrics, error) {
	// §16.1: tree budget reconstruction events on Redis recovery, by
	// outcome (success | irrecoverable).
	reconstruction, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_delegation_budget_reconstruction_total",
		Help: "Delegation budget reconstruction events by outcome (success | irrecoverable).",
	}, []string{"outcome"})
	if err != nil {
		return nil, err
	}
	if err := reg.Register(reconstruction); err != nil {
		return nil, fmt.Errorf("register delegation budget metric: %w", err)
	}
	return &PromMetrics{reconstruction: reconstruction}, nil
}

// IncDelegationBudgetReconstruction counts one reconstruction event by
// outcome.
func (m *PromMetrics) IncDelegationBudgetReconstruction(outcome string) {
	if m == nil {
		return
	}
	m.reconstruction.WithLabelValues(outcome).Inc()
}
