// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local entry point. Each scenario under
// tests/tier7a_load_local/scenarios/<name>/scenario.go registers itself
// with loadgen.DefaultRegistry() via init(); TestScenarios iterates the
// registry and runs every entry as a sub-test under the load_local
// build tag with the race detector enabled.
//
// TESTING.md §12.7.a defines this tier's contract: per-scenario
// budget ≤ 15s, total tier ≤ 5 min.

package tier7a_load_local_test

import (
	"context"
	"testing"
	"time"

	// Importing the scenarios package triggers every scenario
	// subpackage's init() through blank imports.
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
)

// perScenarioBudget bounds a single scenario's wall-clock per TESTING.md §12.7.a.
const perScenarioBudget = 15 * time.Second

func TestScenarios(t *testing.T) {
	// Scenarios run sequentially. Each scenario boots its own
	// in-process surfaces (miniredis, fakekube, the inproc gateway
	// HTTP listener) and many of them are network-bound on
	// loopback; running 18 in parallel under -race exhausts OS
	// resources on a developer laptop. Sequential execution still
	// fits within the §12.7.a 5-minute wall-clock budget.
	registry := loadgen.DefaultRegistry()
	if registry.Len() == 0 {
		t.Skip("no tier-7a scenarios registered; scenarios land in Wave 2 and Wave 3")
	}
	for _, name := range registry.Names() {
		name := name
		t.Run(name, func(t *testing.T) {
			scenario := registry.MustGet(name)
			ctx, cancel := context.WithTimeout(context.Background(), perScenarioBudget)
			defer cancel()
			result, err := loadgen.Run(ctx, scenario, scenario.DefaultProfile())
			if err != nil {
				t.Fatalf("loadgen.Run %s: %v", name, err)
			}
			if err := scenario.Assert(result); err != nil {
				t.Fatalf("SLO assertion failed for %s:\n%v\n\nresult:\n%s", name, err, result.Summary())
			}
			t.Logf("\n%s", result.Summary())
		})
	}
}
