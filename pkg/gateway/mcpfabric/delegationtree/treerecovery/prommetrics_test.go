// SPDX-License-Identifier: MIT

package treerecovery_test

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treerecovery"
)

// TestPromMetricsRegisterTheRecoveryCollectorsOnce checks that the tree
// recovery collectors register on the given registry with their names and
// labels, record a duration and a timeout, refuse a second registration,
// and that a nil set records nothing.
//
// spec: §8.10 (Delegation Tree Recovery), §16.1 (Metrics)
func TestPromMetricsRegisterTheRecoveryCollectorsOnce(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := treerecovery.NewPromMetrics(reg)
	if err != nil {
		t.Fatal(err)
	}
	m.ObserveTreeRecoveryDuration("pool-a", "full_success", 12.5)
	m.IncTreeRecoveryTimeout("pool-a", "level")
	want := `
# HELP lenny_delegation_tree_recovery_timeout_total Delegation tree-recovery timeouts by timeout type (` + "`level` | `tree`" + `) (§8.10 / §16.1).
# TYPE lenny_delegation_tree_recovery_timeout_total counter
lenny_delegation_tree_recovery_timeout_total{pool="pool-a",timeout_type="level"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "lenny_delegation_tree_recovery_timeout_total"); err != nil {
		t.Error(err)
	}
	if n := testutil.CollectAndCount(reg, "lenny_delegation_tree_recovery_duration_seconds"); n != 1 {
		t.Errorf("duration series = %d, want 1", n)
	}
	if _, err := treerecovery.NewPromMetrics(reg); err == nil {
		t.Error("a second registration on the same registry succeeded")
	}
	var nilSet *treerecovery.PromMetrics
	nilSet.ObserveTreeRecoveryDuration("p", "o", 1)
	nilSet.IncTreeRecoveryTimeout("p", "tree")
}
