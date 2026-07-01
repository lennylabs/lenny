// SPDX-License-Identifier: MIT

package capacityplanning_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/metrics/capacityplanning"
)

// TestShouldWarnRedisClusterRecommended exercises the §17.8.2 line 1164
// startup-warning heuristic across the tier, topology, and
// operator-intent axes. spec: spec/17_deployment-topology.md line 1164.
func TestShouldWarnRedisClusterRecommended_spec_17_8_2_1164(t *testing.T) {
	cases := []struct {
		name         string
		tier         string
		topology     string
		singleTenant string
		want         bool
	}{
		{"tier3 sentinel undocumented — warn", "tier3", "sentinel", "", true},
		{"tier3 sentinel documented — suppressed", "tier3", "sentinel", "sentinel", false},
		{"tier3 cluster — no warn", "tier3", "cluster", "", false},
		{"tier3 standalone — no warn", "tier3", "standalone", "", false},
		{"tier2 sentinel — below the ceiling, no warn", "tier2", "sentinel", "", false},
		{"tier1 sentinel — no warn", "tier1", "sentinel", "", false},
		{"empty tier sentinel — no warn", "", "sentinel", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := capacityplanning.ShouldWarnRedisClusterRecommended(tc.tier, tc.topology, tc.singleTenant)
			if got != tc.want {
				t.Errorf("ShouldWarnRedisClusterRecommended(%q,%q,%q) = %v, want %v",
					tc.tier, tc.topology, tc.singleTenant, got, tc.want)
			}
		})
	}
}

// TestRedisClusterRecommendedWarningMarker pins the log marker so a Tier 3
// operator's log scraper can match on it. spec:
// spec/17_deployment-topology.md line 1164.
func TestRedisClusterRecommendedWarningMarker_spec_17_8_2_1164(t *testing.T) {
	if !strings.Contains(capacityplanning.RedisClusterRecommendedWarning, "RedisClusterRecommended") {
		t.Errorf("warning %q does not carry the RedisClusterRecommended marker", capacityplanning.RedisClusterRecommendedWarning)
	}
	if !strings.HasPrefix(capacityplanning.RedisClusterRecommendedWarning, "[WARN]") {
		t.Errorf("warning %q must start with the [WARN] startup-log prefix", capacityplanning.RedisClusterRecommendedWarning)
	}
}
