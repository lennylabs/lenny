// SPDX-License-Identifier: MIT

package main

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationbudget"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/deadlock"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/orphancleanup"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
)

// platformToolsMetrics holds the metric sets of the platform tools seam's
// packages. Each package registers its own collectors on the gateway's
// metric registry, which the composition root hands it as a
// prometheus.Registerer, so the seam holds no value of the replica's
// gateway metrics (decision 21 of change 0117). A zero value holds nil
// sets, whose methods record nothing.
type platformToolsMetrics struct {
	delegation           *delegation.PromMetrics
	deadlock             *deadlock.PromMetrics
	elicitation          *mcptools.PromMetrics
	leaseExtension       *leasecontrol.PromMetrics
	orphanCleanup        *orphancleanup.PromMetrics
	budgetReconstruction *delegationbudget.PromMetrics
}

// newPlatformToolsMetrics builds and registers every metric set of the
// platform tools seam on reg. It fails when a set cannot be registered,
// such as when reg already holds a metric of the same name.
func newPlatformToolsMetrics(reg prometheus.Registerer) (platformToolsMetrics, error) {
	var (
		m   platformToolsMetrics
		err error
	)
	if m.delegation, err = delegation.NewPromMetrics(reg); err != nil {
		return platformToolsMetrics{}, err
	}
	if m.deadlock, err = deadlock.NewPromMetrics(reg); err != nil {
		return platformToolsMetrics{}, err
	}
	if m.elicitation, err = mcptools.NewPromMetrics(reg); err != nil {
		return platformToolsMetrics{}, err
	}
	if m.leaseExtension, err = leasecontrol.NewPromMetrics(reg); err != nil {
		return platformToolsMetrics{}, err
	}
	if m.orphanCleanup, err = orphancleanup.NewPromMetrics(reg); err != nil {
		return platformToolsMetrics{}, err
	}
	if m.budgetReconstruction, err = delegationbudget.NewPromMetrics(reg); err != nil {
		return platformToolsMetrics{}, err
	}
	return m, nil
}
