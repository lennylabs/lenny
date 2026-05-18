// SPDX-License-Identifier: MIT

package diagnostics_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/diagnostics"
)

func TestPodFailureChainCleanExit(t *testing.T) {
	chain := diagnostics.PodFailureChain(diagnostics.Signals{ExitCode: 0})
	if chain != nil {
		t.Errorf("PodFailureChain of a clean exit = %v, want nil", chain)
	}
}

func TestPodFailureChainProximateCause(t *testing.T) {
	cases := []struct {
		name     string
		signals  diagnostics.Signals
		category diagnostics.Category
	}{
		{"OOM kill", diagnostics.Signals{ExitCode: 137, OOMKilled: true}, diagnostics.CategoryOOMKilled},
		{"image pull", diagnostics.Signals{ImagePullError: true}, diagnostics.CategoryImagePullFailure},
		{"resource pressure", diagnostics.Signals{ResourcePressure: true}, diagnostics.CategoryResourcePressure},
		{"setup failure", diagnostics.Signals{ExitCode: 1, InSetupPhase: true}, diagnostics.CategorySetupCommandFailed},
		{"generic crash", diagnostics.Signals{ExitCode: 2}, diagnostics.CategoryPodCrash},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			chain := diagnostics.PodFailureChain(c.signals)
			if len(chain) != 1 {
				t.Fatalf("chain length = %d, want 1 proximate-cause entry", len(chain))
			}
			entry := chain[0]
			if entry.Level != 0 {
				t.Errorf("level = %d, want 0 for the proximate cause", entry.Level)
			}
			if entry.Category != c.category {
				t.Errorf("category = %q, want %q", entry.Category, c.category)
			}
			if entry.Summary == "" {
				t.Errorf("category %q has no human-readable summary", entry.Category)
			}
		})
	}
}
