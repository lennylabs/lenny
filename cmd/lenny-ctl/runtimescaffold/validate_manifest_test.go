// SPDX-License-Identifier: MIT

package runtimescaffold

import (
	"strings"
	"testing"
)

// agentManifest returns a minimal valid agent runtimeManifest with the
// given executionMode, so a test can vary only the mode under check.
func agentManifest(mode string) *runtimeManifest {
	return &runtimeManifest{
		Name:          "acme-agent",
		Type:          "agent",
		Image:         "registry.example.com/acme@sha256:" + strings.Repeat("a", 64),
		ExecutionMode: mode,
		Capabilities:  map[string]any{"interaction": "multi_turn"},
	}
}

// spec: §5.1 (runtime manifest executionMode), §5.2 (session | service
// execution modes). The runtime-scaffold validator accepts the two
// surviving modes and rejects any other value, including the removed
// `task` mode. The mode enum collapsed from `session | task | concurrent`
// to `session | service`, so the CLI validator must track the new set or
// it accepts a removed mode and rejects a current one.
func TestCheckManifestExecutionMode(t *testing.T) {
	cases := []struct {
		mode       string
		wantReject bool
	}{
		{"session", false},
		{"service", false},
		{"", false}, // unset inherits the §5.1 default; not a finding
		{"task", true},
		{"concurrent", true},
		{"bogus", true},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			findings := checkManifest(agentManifest(tc.mode))
			var modeFinding string
			for _, f := range findings {
				if strings.Contains(f, "executionMode") {
					modeFinding = f
				}
			}
			if tc.wantReject && modeFinding == "" {
				t.Fatalf("executionMode %q: want a rejection finding, got none; findings=%v", tc.mode, findings)
			}
			if !tc.wantReject && modeFinding != "" {
				t.Fatalf("executionMode %q: want no finding, got %q", tc.mode, modeFinding)
			}
			if tc.wantReject && !strings.Contains(modeFinding, "session or service") {
				t.Fatalf("executionMode %q: finding %q does not name the session|service set", tc.mode, modeFinding)
			}
		})
	}
}
