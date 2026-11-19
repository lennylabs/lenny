// SPDX-License-Identifier: MIT

package orphancleanup_test

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/orphancleanup"
)

// spec: §16.1 (Metrics), §8.10 (Delegation Tree Recovery)
func TestOrphanPromMetricsExposeZeroAtRegistrationAndRecordSweeps(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := orphancleanup.NewPromMetrics(reg)
	if err != nil {
		t.Fatalf("NewPromMetrics: %v", err)
	}
	// The unlabelled series are exposed at zero before the first sweep.
	atStart := `
# HELP lenny_orphan_cleanup_runs_total Background orphan cleanup job executions (§8.10 / §16.1).
# TYPE lenny_orphan_cleanup_runs_total counter
lenny_orphan_cleanup_runs_total 0
# HELP lenny_orphan_tasks_active Currently active orphan tasks awaiting cleanup, summed across tenants (§8.10 / §16.1).
# TYPE lenny_orphan_tasks_active gauge
lenny_orphan_tasks_active 0
# HELP lenny_orphan_tasks_terminated Orphan tasks terminated by the §8.10 cleanup job (§16.1).
# TYPE lenny_orphan_tasks_terminated counter
lenny_orphan_tasks_terminated 0
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(atStart),
		"lenny_orphan_cleanup_runs_total", "lenny_orphan_tasks_active", "lenny_orphan_tasks_terminated",
	); err != nil {
		t.Fatal(err)
	}

	m.IncOrphanCleanupRun()
	m.AddOrphanTasksTerminated(3)
	m.AddOrphanTasksTerminated(0) // a zero count adds nothing
	m.AddOrphanTasksTerminated(-1)
	m.SetOrphanTasksActive(2)
	m.SetOrphanTasksActivePerTenant("acme", 4)
	m.SetOrphanTasksActivePerTenant("globex", 0)

	want := `
# HELP lenny_orphan_cleanup_runs_total Background orphan cleanup job executions (§8.10 / §16.1).
# TYPE lenny_orphan_cleanup_runs_total counter
lenny_orphan_cleanup_runs_total 1
# HELP lenny_orphan_tasks_active Currently active orphan tasks awaiting cleanup, summed across tenants (§8.10 / §16.1).
# TYPE lenny_orphan_tasks_active gauge
lenny_orphan_tasks_active 2
# HELP lenny_orphan_tasks_active_per_tenant Per-tenant active orphan task count; drives the OrphanTasksPerTenantHigh alert (§8.10 / §16.1).
# TYPE lenny_orphan_tasks_active_per_tenant gauge
lenny_orphan_tasks_active_per_tenant{tenant_id="acme"} 4
lenny_orphan_tasks_active_per_tenant{tenant_id="globex"} 0
# HELP lenny_orphan_tasks_terminated Orphan tasks terminated by the §8.10 cleanup job (§16.1).
# TYPE lenny_orphan_tasks_terminated counter
lenny_orphan_tasks_terminated 3
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want),
		"lenny_orphan_cleanup_runs_total", "lenny_orphan_tasks_active",
		"lenny_orphan_tasks_active_per_tenant", "lenny_orphan_tasks_terminated",
	); err != nil {
		t.Fatal(err)
	}
}

func TestOrphanPromMetricsRefuseARegistryThatHoldsTheNames(t *testing.T) {
	reg := prometheus.NewRegistry()
	if _, err := orphancleanup.NewPromMetrics(reg); err != nil {
		t.Fatalf("first NewPromMetrics: %v", err)
	}
	if _, err := orphancleanup.NewPromMetrics(reg); err == nil {
		t.Fatal("second NewPromMetrics on the same registry returned no error")
	}
}

func TestNilOrphanPromMetricsIgnoreEveryCall(t *testing.T) {
	var m *orphancleanup.PromMetrics
	m.IncOrphanCleanupRun()
	m.AddOrphanTasksTerminated(1)
	m.SetOrphanTasksActive(0)
	m.SetOrphanTasksActivePerTenant("acme", 0)
}
