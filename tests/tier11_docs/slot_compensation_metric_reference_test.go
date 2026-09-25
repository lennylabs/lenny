// SPDX-License-Identifier: MIT

// Tier-11 documentation check for the gateway slot-compensation rows of the
// deployer-facing metrics reference under docs/reference/metrics.md. The
// §16.1 catalog defines lenny_slot_compensation_superseded_total, the count
// of compensating Shutdowns the adapter answered superseded after a failed
// bind, and lenny_adapter_leaked_slots, the gateway-emitted per-pod count of
// slots in the leaked sub-state. A deployer reads the reference to learn what
// each series means and how it is labeled, so the reference must carry both
// rows with the §16.1 semantics.
//
// This test is NOT under a build tag: it reads the repository state
// directly and needs no external infrastructure.

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// metricsReferenceRow returns the single table row of body that names
// metric in a backticked cell, or "" when no such row exists. Matching the
// backticked name inside a table row keeps the assertion on the reference
// row rather than on an incidental prose mention.
func metricsReferenceRow(body, metric string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "|") &&
			strings.Contains(line, "| `"+metric+"` |") {
			return line
		}
	}
	return ""
}

// spec: §16.1 (metrics reference mirrors the §16.1 catalog rows for
//
//	lenny_slot_compensation_superseded_total and lenny_adapter_leaked_slots)
//
// diagnosis: docs/reference/metrics.md omits the gateway row of
//
//	lenny_slot_compensation_superseded_total or lenny_adapter_leaked_slots,
//	or states a label set or a meaning that contradicts §16.1. The superseded
//	counter is labeled by pool, k8s_pod_name, and error_type, counts
//	compensating Shutdowns answered superseded (the reclaim released nothing
//	and the slot is not leaked), and splits error_type into refusal and
//	failure. The leaked-slots gauge is labeled by pod_id and pool and counts
//	slots in the leaked sub-state until the pod terminates. A dropped label,
//	a lost outcome, or an absent row is caught here.
func TestMetricsReferenceCarriesSlotCompensationRows(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "docs", "reference", "metrics.md"))
	if err != nil {
		t.Fatalf("read docs/reference/metrics.md: %v", err)
	}
	body := string(raw)

	cases := []struct {
		metric string
		want   []string
	}{
		{
			metric: "lenny_slot_compensation_superseded_total",
			want: []string{
				"Counter",
				"`pool`",
				"`k8s_pod_name`",
				"`error_type`",
				"`Shutdown`",
				"`superseded`",
				"released nothing",
				"not leaked",
				"`refusal`",
				"`failure`",
				"`SLOT_BIND_ATTEMPT_SUPERSEDED`",
				"`SLOT_BIND_ALREADY_STARTED`",
			},
		},
		{
			metric: "lenny_adapter_leaked_slots",
			want: []string{
				"Gauge",
				"`pod_id`",
				"`pool`",
				"`leaked`",
				"until the pod terminates",
			},
		},
	}
	for _, tc := range cases {
		row := metricsReferenceRow(body, tc.metric)
		if row == "" {
			t.Errorf("docs/reference/metrics.md has no table row for `%s`", tc.metric)
			continue
		}
		for _, want := range tc.want {
			if !strings.Contains(row, want) {
				t.Errorf("%s reference row does not state %q; row: %s", tc.metric, want, row)
			}
		}
	}
}
