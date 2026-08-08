// SPDX-License-Identifier: MIT

package tier0_static

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// specLineCitationChecks pairs a §25.4 citation in product code with the
// spec sentence it is meant to point at. anchor locates the declaration
// whose preceding doc comment carries the citation, and wantSubstring is
// text that must appear inside the cited section.
//
// The check is against the section body rather than against a line number.
// The citation form this file once pinned named a line, and a line number
// goes stale on any edit above it, which is why the migration retired the
// form across the tree. What is worth pinning survives the change: a
// comment that attributes a sentence to §25.4 is wrong if §25.4 does not
// carry that sentence, and that is checkable without a line number.
var specLineCitationChecks = []struct {
	file          string
	anchor        string // literal substring marking the start of the cited declaration
	wantSubstring string // text the cited spec line(s) must contain
}{
	{
		file:          "pkg/ops/opsidem/writers.go",
		anchor:        "type degradingWriter struct",
		wantSubstring: "retry-safety is not guaranteed",
	},
	{
		file:          "pkg/ops/coordination/service.go",
		anchor:        "func (s *Service) MemoryTierWarning",
		wantSubstring: "coordination is replica-local",
	},
}

var citedSection = regexp.MustCompile(`§25\.4\b`)

// sectionBody returns the lines of spec/25 under the §25.4 heading, up to
// the next heading at the same level. The bound is the sibling heading
// rather than a line count so the body tracks edits to the section.
func sectionBody(t *testing.T, specLines []string, heading string) []string {
	t.Helper()
	start := -1
	for i, l := range specLines {
		if strings.HasPrefix(l, heading) {
			start = i + 1
			break
		}
	}
	if start == -1 {
		t.Fatalf("spec/25_agent-operability.md carries no %q heading", heading)
	}
	for i := start; i < len(specLines); i++ {
		if strings.HasPrefix(specLines[i], "## ") {
			return specLines[start:i]
		}
	}
	return specLines[start:]
}

// commentBlockAbove returns the contiguous run of `//` comment lines
// immediately preceding the first line containing anchor, joined with a
// space.
func commentBlockAbove(t *testing.T, path, anchor string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := strings.Split(string(data), "\n")
	anchorIdx := -1
	for i, l := range lines {
		if strings.Contains(l, anchor) {
			anchorIdx = i
			break
		}
	}
	if anchorIdx == -1 {
		t.Fatalf("%s: anchor %q not found", path, anchor)
	}
	var block []string
	for i := anchorIdx - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(trimmed, "//") {
			break
		}
		block = append([]string{trimmed}, block...)
	}
	return strings.Join(block, " ")
}

// spec: §25.4 (degradation.warnings on optional-key idempotency endpoints —
//
//	"the response includes `degradation.warnings` noting that retry-safety
//	is not guaranteed" at §25.4; the
//	ops.locks.memoryTier "always" replica-local warning — "lock acquisition
//	always proceeds, with a warning in `degradation.warnings` that
//	coordination is replica-local" at §25.4)
//
// diagnosis: A `// spec: §25.4` citation in pkg/ops/opsidem/writers.go or
//
//	pkg/ops/coordination/service.go attributes a sentence to §25.4 that
//	§25.4 no longer carries, so either the comment describes behaviour the
//	specification dropped or the sentence moved to another section. Run
//	`grep -n "<quoted phrase>" spec/25_agent-operability.md` to find where
//	it went, then correct the comment to cite the section that carries it.
func TestSpec254DegradationWarningLineCitationsAreFresh(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	specPath := filepath.Join(root, "spec/25_agent-operability.md")
	specData, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	specLines := strings.Split(string(specData), "\n")

	for _, tc := range specLineCitationChecks {
		tc := tc
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(root, tc.file)
			block := commentBlockAbove(t, path, tc.anchor)
			if !citedSection.MatchString(block) {
				t.Fatalf("%s: no §25.4 citation found above %q in comment: %s", tc.file, tc.anchor, block)
			}
			body := sectionBody(t, specLines, "## 25.4 ")
			for _, l := range body {
				if strings.Contains(l, tc.wantSubstring) {
					return
				}
			}
			t.Errorf("%s: the comment above %q cites §25.4, but §25.4 carries no line containing %q; run `grep -n %q spec/25_agent-operability.md` to find the section that does and correct the citation",
				tc.file, tc.anchor, tc.wantSubstring, tc.wantSubstring)
		})
	}
}
