// SPDX-License-Identifier: MIT

package tier0_static

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// specLineCitationChecks pairs a §25.4 line-number citation in product code
// with the spec sentence it is meant to point at. anchor locates the
// declaration whose preceding doc comment carries the citation, and
// wantSubstring is text that must appear on the spec line(s) the citation
// resolves to.
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

var citedLineNumbers = regexp.MustCompile(`§25\.4 lines? (\d+)(?:-(\d+))?`)

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
//	is not guaranteed" at spec/25_agent-operability.md:2057; the
//	ops.locks.memoryTier "always" replica-local warning — "lock acquisition
//	always proceeds, with a warning in `degradation.warnings` that
//	coordination is replica-local" at spec/25_agent-operability.md:2215)
//
// diagnosis: A `// spec: §25.4 line(s) N` citation in
//
//	pkg/ops/opsidem/writers.go or pkg/ops/coordination/service.go that no
//	longer points at the spec line carrying the quoted sentence has gone
//	stale (the spec section was edited and line numbers shifted). Run
//	`grep -n "<quoted phrase>" spec/25_agent-operability.md` and update the
//	citation to the reported line.
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
			m := citedLineNumbers.FindStringSubmatch(block)
			if m == nil {
				t.Fatalf("%s: no §25.4 line citation found above %q in comment: %s", tc.file, tc.anchor, block)
			}
			start, err := strconv.Atoi(m[1])
			if err != nil {
				t.Fatalf("parse start line %q: %v", m[1], err)
			}
			end := start
			if m[2] != "" {
				end, err = strconv.Atoi(m[2])
				if err != nil {
					t.Fatalf("parse end line %q: %v", m[2], err)
				}
			}
			found := false
			for ln := start; ln <= end; ln++ {
				if ln < 1 || ln > len(specLines) {
					continue
				}
				if strings.Contains(specLines[ln-1], tc.wantSubstring) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s: cited spec/25_agent-operability.md line(s) %d-%d do not contain %q (citation is stale); run `grep -n %q spec/25_agent-operability.md` to find the current line and update the comment",
					tc.file, start, end, tc.wantSubstring, tc.wantSubstring)
			}
		})
	}
}
