// SPDX-License-Identifier: MIT

package tier0_static

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: §4.1 lines 86-94 and 136-144 — the Phase 2 calibration harness
// for the gateway capacity budget and the four subsystem extraction
// thresholds. Both methodology blocks require empirically validated
// values to replace the provisional defaults before any Tier 2
// production deployment. This tier-0 static check asserts the
// calibration scenario scaffold is present under tier7b so a
// pre-Tier-2 audit can't ship without one.
// diagnosis: the Phase 2 calibration harness has been deleted or
// moved. The §4.1 capacity-budget and extraction-threshold methodology
// requires the scenario, its README, and a baseline placeholder so the
// values that go into the Helm release at Tier 2 promotion can be
// traced back to a measured run. Restore the files under
// tests/tier7b_load_kind/scenarios/gateway_capacity_calibration/ and
// tests/tier7b_load_kind/baselines/gateway_capacity_calibration.json.
func TestGatewayCapacityCalibrationScaffoldExists(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	required := []string{
		"tests/tier7b_load_kind/scenarios/gateway_capacity_calibration/main.js",
		"tests/tier7b_load_kind/scenarios/gateway_capacity_calibration/README.md",
		"tests/tier7b_load_kind/baselines/gateway_capacity_calibration.json",
	}
	for _, rel := range required {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			t.Parallel()
			full := filepath.Join(root, rel)
			info, err := os.Stat(full)
			if err != nil {
				t.Fatalf("§4.1 Phase 2 calibration scaffold missing: %s: %v", rel, err)
			}
			if info.Size() == 0 {
				t.Errorf("§4.1 Phase 2 calibration scaffold is empty: %s", rel)
			}
		})
	}

	// The baseline placeholder must parse as JSON so the operator
	// workflow that overwrites it on a successful calibration run does
	// not start from a malformed file.
	t.Run("baseline parses as JSON", func(t *testing.T) {
		t.Parallel()
		full := filepath.Join(root, "tests/tier7b_load_kind/baselines/gateway_capacity_calibration.json")
		data, err := os.ReadFile(full)
		if err != nil {
			t.Fatalf("read baseline: %v", err)
		}
		var parsed map[string]any
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Fatalf("parse baseline as JSON: %v", err)
		}
		// The scaffold tracks the calibration outputs the operator
		// writes on a successful run; assert the key exists so the
		// downstream consumer can read against a stable schema.
		if _, ok := parsed["calibration_outputs"]; !ok {
			t.Errorf("baseline missing 'calibration_outputs' key — the scaffold tracks the suggested-values payload the operator writes on a successful calibration run")
		}
	})
}
