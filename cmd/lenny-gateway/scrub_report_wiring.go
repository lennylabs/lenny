// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/slothealth"
	"github.com/lennylabs/lenny/pkg/gateway/session/recycle"
)

// newScrubReportService builds the §4.7 ScrubReporter that backs the
// ReportSessionScrub and ReportPodScrub RPCs. It wires the five concrete
// recycle seams (pkg/gateway/recycle) onto the gateway's dependencies: the
// agent_pod_state recycle counters, the unhealthy-threshold drain ledger
// over the shared slothealth tracker (the same tracker the sessionserver
// slot-bind-failure path feeds, so adapter-reported leaks and slot-bind
// failures accumulate in one §5.2 rolling window), the §6.2 host-node
// schedulability pod inspector, the claim disposition driver, and the
// §16.1 retirement metrics. The drain ledger resolves each leaked pod's pool
// maxConcurrentSessions through the pool store, so a single-session
// recycling pod drains on the first leak while a recycling concurrent-session
// pool (the §5.2 "Concurrent" preset, maxConcurrentSessions: N with
// recycle.enabled) drains only at ceil(N/2) failed-or-leaked slots.
//
// spec: §4.7 (ReportSessionScrub/ReportPodScrub), §5.2 (scrub model,
// combined failed+leaked threshold), §6.2 (host-node schedulability
// retire), §16.1 (recycle metrics).
func newScrubReportService(cl client.Client, counters recycle.CounterStore, pools poolstore.Store, runtimes runtimestore.Store, metrics recycle.RetirementMetricsSink, slotHealth *slothealth.Tracker, agentNamespace string, holdTTL time.Duration, holds recycle.HoldRegistrar, boundary *recycle.RecycleBoundaryCoordinator, now func() time.Time) (leasecontrol.ScrubReportService, error) {
	ledger, err := recycle.NewDrainLedger(recycle.DrainLedgerOptions{
		Tracker:   slotHealth,
		Client:    cl,
		Namespace: agentNamespace,
		Pools:     pools,
		Now:       now,
	})
	if err != nil {
		return nil, fmt.Errorf("build drain ledger: %w", err)
	}
	sessionRetirer, err := recycle.NewSessionCountRetirer(recycle.SessionCountRetirerOptions{
		Client:    cl,
		Namespace: agentNamespace,
		Pools:     pools,
		Runtimes:  runtimes,
		Metrics:   metrics,
		Now:       now,
	})
	if err != nil {
		return nil, fmt.Errorf("build session-count retirer: %w", err)
	}
	inspector, err := recycle.NewPodInspector(recycle.PodInspectorOptions{
		Client:    cl,
		Namespace: agentNamespace,
		Pools:     pools,
		Runtimes:  runtimes,
		Now:       now,
	})
	if err != nil {
		return nil, fmt.Errorf("build pod inspector: %w", err)
	}
	driverOpts := recycle.ClaimDispositionDriverOptions{
		Client:    cl,
		Namespace: agentNamespace,
		HoldTTL:   holdTTL,
		Now:       now,
		Holds:     holds,
	}
	// The disposition driver signals the recycle-boundary coordinator on
	// every resolved ReportPodScrub so it cancels the missing-report timeout
	// and, on a preConnect recycle, drives recycling → reserved once the SDK
	// re-warm completes. Set only when the coordinator exists so a typed-nil
	// pointer is not wrapped into a non-nil interface (single-process dev leaves
	// the timeout to fire and the re-warm completion to the orphan GC).
	if boundary != nil {
		driverOpts.Boundary = boundary
	}
	driver, err := recycle.NewClaimDispositionDriver(driverOpts)
	if err != nil {
		return nil, fmt.Errorf("build claim disposition driver: %w", err)
	}
	reporter, err := leasecontrol.NewScrubReporter(leasecontrol.ScrubReporterOptions{
		Counters:       recycle.NewRecycleCounterStore(counters),
		Ledger:         ledger,
		SessionRetirer: sessionRetirer,
		Inspector:      inspector,
		Driver:         driver,
		Metrics:        recycle.NewRetirementMetrics(metrics),
	})
	if err != nil {
		return nil, fmt.Errorf("build scrub reporter: %w", err)
	}
	return reporter, nil
}
