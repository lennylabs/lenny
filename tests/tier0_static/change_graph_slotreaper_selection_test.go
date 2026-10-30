// SPDX-License-Identifier: MIT

package tier0_static

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: 5.2 (Pool Configuration and Execution Modes); TESTING.md §5
// "`tests/change-graph.json` maps source packages ... to the tests that
// exercise them."
// diagnosis: the stale-slot reaper under pkg/gateway/podlifecycle/slotreaper
//
//	releases slot set members whose session ended the terminal age ago.
//	It reads the ended sessions through the session store's
//	ListStaleTerminalSessions, whose only tier-4 test is
//	tests/tier4_integration/slot_holder_pgstore_test.go, and it reaps
//	through the slot counter, whose Postgres fallback is exercised by
//	tests/tier4_integration/slot_counter_redis_outage_fallback_test.go.
//	When the "pkg/gateway/podlifecycle/slotreaper" glob omits either
//	file, a change to the reaper never re-selects that integration
//	test under `lenny-test --changed`. Restore both entries under the
//	glob's "integration" tier.
func TestChangeGraphSlotReaperSelectsBothIntegrationTests(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "tests", "change-graph.json"))
	if err != nil {
		t.Fatalf("read change-graph.json: %v", err)
	}
	var doc struct {
		Globs map[string]map[string][]string `json:"globs"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse change-graph.json: %v", err)
	}

	integration := map[string]bool{}
	for _, f := range doc.Globs["pkg/gateway/podlifecycle/slotreaper"]["integration"] {
		integration[f] = true
	}
	for _, want := range []string{
		"tests/tier4_integration/slot_counter_redis_outage_fallback_test.go",
		"tests/tier4_integration/slot_holder_pgstore_test.go",
	} {
		if !integration[want] {
			t.Errorf(`"pkg/gateway/podlifecycle/slotreaper"."integration" is %v; it must list %q`, sortedKeys(integration), want)
		}
		if _, err := os.Stat(filepath.Join(root, want)); err != nil {
			t.Errorf("the mapped integration test %q is not in the tree: %v", want, err)
		}
	}
	if !resolveChangeGraphTiers(t, "pkg/gateway/podlifecycle/slotreaper/reaper.go")["integration"] {
		t.Errorf("a change to the stale-slot reaper does not resolve to the integration tier")
	}
}
