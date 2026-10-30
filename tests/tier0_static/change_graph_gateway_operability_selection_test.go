// SPDX-License-Identifier: MIT

package tier0_static

import "testing"

// spec: 25.2 (Architecture Overview — the split: "Health API ... Where it
// lives: Gateway", "Capacity Recommendations ... Where it lives: Gateway")
// and 25.3 (Gateway-Side Ops Endpoints, which the Section 25.2 split assigns
// pkg/gateway/operability/health and pkg/gateway/operability/recommendations
// to); TESTING.md §5 ("tests/change-graph.json maps source packages,
// schemas, migrations, and chart templates to the tests that exercise
// them.").
// diagnosis: pkg/gateway/operability/health and
//
//	pkg/gateway/operability/recommendations each own in-package unit
//	suites (service_test.go, metrics_test.go, pools_test.go,
//	runbook_links_test.go, runbook_bundle_test.go,
//	suggested_actions_test.go, alert_overlay_test.go, and
//	backends/backends_test.go under health; metricreader_test.go,
//	metrics_test.go, sampler_test.go, and service_test.go under
//	recommendations) that pin the §25.3 health-aggregation and
//	capacity-recommendation behavior the §25.2 split assigns to the
//	gateway. Neither package has a glob entry in
//	tests/change-graph.json, so a change to either resolves to an empty
//	tier set and `lenny-test run --changed`/`--since` never selects
//	the unit suite that pins it. Add a change-graph glob mapping each
//	package to at least the unit tier.
func TestChangeGraphGatewayOperabilityPackagesSelectUnitTier(t *testing.T) {
	t.Parallel()

	for _, pkg := range []string{
		"pkg/gateway/operability/health",
		"pkg/gateway/operability/recommendations",
	} {
		tiers := resolveChangeGraphTiers(t, pkg+"/change.go")
		if !tiers["unit"] {
			t.Errorf("a change to %s resolved to tiers %v; it owns an in-package unit suite pinning §25.3 behavior, so the resolution must include %q",
				pkg, sortedKeys(tiers), "unit")
		}
	}
}

// spec: 25.3 (Gateway-Side Ops Endpoints, the gateway binary that hosts the
// health and recommendations endpoints) and TESTING.md §5 ("tests/
// change-graph.json maps source packages ... to the tests that exercise
// them.").
// diagnosis: cmd/lenny-gateway carries 37 in-package *_test.go files
//
//	(including cmd/lenny-gateway/health_status_changed_payload_test.go and
//	cmd/lenny-gateway/health_without_prometheus_test.go, both registered
//	against §25.3 in tests/spec-map.json), but tests/change-graph.json
//	maps cmd/lenny-gateway only to the "static", "contract", and
//	"integration" tiers, omitting "unit". A change to the binary's own
//	package therefore never selects its in-package unit suite under
//	`lenny-test run --changed`/`--since`. Add the missing "unit" mapping
//	in tests/change-graph.json.
func TestChangeGraphLennyGatewayBinarySelectsUnitTier(t *testing.T) {
	t.Parallel()

	tiers := resolveChangeGraphTiers(t, "cmd/lenny-gateway/main.go")
	if !tiers["unit"] {
		t.Errorf("a change to cmd/lenny-gateway resolved to tiers %v; the gateway binary owns an in-package unit suite, so the resolution must include %q",
			sortedKeys(tiers), "unit")
	}
}
