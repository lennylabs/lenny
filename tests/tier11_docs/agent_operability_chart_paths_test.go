// SPDX-License-Identifier: MIT

// Tier-11 documentation check for the Helm chart paths §25.8 of the spec
// cites for the canonical values.yaml reference. Neither the tier1_unit/helm
// nor the tier0_static chart-render checks pin the file paths the prose in
// spec/25_agent-operability.md tells an operator or agent to open; this test
// closes that gap.
//
// These tests are NOT under a build tag: they read the repository state
// directly and need no external infrastructure.

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// chartPathBacktickRe matches backtick-quoted paths under charts/ or the
// stale deploy/helm/ tree so the test can locate every chart-file citation
// in the §25.8 prose without hand-listing them.
var chartPathBacktickRe = regexp.MustCompile("`((?:charts|deploy/helm)/[^`]+\\.ya?ml)`")

// spec: §25.8 ("The full canonical values.yaml reference is maintained at
//
//	`charts/lenny/values.yaml` in the repository. Tier-specific presets live
//	at `charts/lenny/presets/values-tier1.yaml`, `charts/lenny/presets/values-tier2.yaml`,
//	`charts/lenny/presets/values-tier3.yaml` and override a defined subset of
//	the base values (listed in the comment header of each preset file).")
//
// diagnosis: §25.8's Helm Values Hierarchy paragraph names the on-disk
//
//	location of the canonical values.yaml and its tier presets so an operator
//	or agent following the citation can open the file. The chart lives at
//	charts/lenny/ in this repository, not deploy/helm/lenny/ (that path does
//	not exist). A regression that reintroduces the deploy/helm/lenny/ prefix,
//	or that points at a preset filename the chart no longer ships, sends the
//	reader to a nonexistent path. This walks every backtick-quoted chart path
//	cited in spec/25_agent-operability.md and asserts it resolves to a real
//	file, and separately guards against the stale deploy/helm/ prefix
//	reappearing.
func TestSpecAgentOperabilityChartPathsResolve(t *testing.T) {
	root := repoRoot(t)
	specPath := filepath.Join(root, "spec", "25_agent-operability.md")
	b, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read spec/25_agent-operability.md: %v", err)
	}
	content := string(b)

	matches := chartPathBacktickRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		t.Fatalf("found no charts/ or deploy/helm/ path citations in spec/25_agent-operability.md; update this test if the §25.8 Helm Values Hierarchy prose changed")
	}

	seen := map[string]bool{}
	for _, m := range matches {
		rel := m[1]
		if seen[rel] {
			continue
		}
		seen[rel] = true

		if strings.HasPrefix(rel, "deploy/helm/") {
			t.Errorf("spec/25_agent-operability.md cites %q under the stale deploy/helm/ tree; the chart lives at charts/lenny/", rel)
			continue
		}

		info, err := os.Stat(filepath.Join(root, rel))
		if err != nil || info.IsDir() {
			t.Errorf("spec/25_agent-operability.md cites %q, but it does not resolve to a file on disk: %v", rel, err)
		}
	}
}
