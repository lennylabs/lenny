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

// citedSection matches the anchor-form §25.4 citation, which is a bare
// section reference carrying no line number. retiredLineCitation matches
// the `§25.4 line N` and `§25.4 lines N-M` forms the naming law retires
// (§28.1 N8). Go's regexp has no negative lookahead, so the rejection is a
// second expression rather than a suffix guard inside the first: a word
// boundary alone sits between the "4" and the following space and so
// accepts the retired form.
var (
	citedSection        = regexp.MustCompile(`§25\.4(?:[^0-9.]|$)`)
	retiredLineCitation = regexp.MustCompile(`§25\.4\s+lines?\s+\d+`)
)

// citationForm reports whether a comment block carries an anchor-form
// §25.4 citation. It returns a message naming the defect when the block
// carries no §25.4 citation at all, or when it still carries the retired
// line-numbered form, and the empty string when the block passes.
func citationForm(block string) string {
	if m := retiredLineCitation.FindString(block); m != "" {
		return "carries the retired line-numbered citation " + strconv.Quote(m) + "; cite the §25.4 heading without a line number"
	}
	if !citedSection.MatchString(block) {
		return "carries no §25.4 citation"
	}
	return ""
}

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

// inertRun reports why a freshness run that selected declarations
// declarations and read a §25.4 body of bodyLines lines certifies nothing.
// It returns the empty string when the run inspected something.
//
// The freshness property is carried by the comparison between a citation and
// the section body it names, so a run whose declaration table selects nothing
// or whose §25.4 lookup returns an empty body performs no comparison and is
// green with the property gone. Both are failures rather than a silent pass.
func inertRun(declarations, bodyLines int) string {
	switch {
	case declarations == 0 && bodyLines == 0:
		return "inspected zero declarations and read an empty §25.4 body"
	case declarations == 0:
		return "inspected zero declarations"
	case bodyLines == 0:
		return "read an empty §25.4 body"
	}
	return ""
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
	body := sectionBody(t, specLines, "## 25.4 ")

	if defect := inertRun(len(specLineCitationChecks), len(body)); defect != "" {
		t.Fatalf("the §25.4 freshness run %s, so it certifies no citation; restore the declaration table or the §25.4 heading it reads", defect)
	}

	for _, tc := range specLineCitationChecks {
		tc := tc
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(root, tc.file)
			block := commentBlockAbove(t, path, tc.anchor)
			if defect := citationForm(block); defect != "" {
				t.Fatalf("%s: the comment above %q %s: %s", tc.file, tc.anchor, defect, block)
			}
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

// citationFormFixtures is the fixture root for the citation-form cases.
// Each fixture holds one comment block. The blocks live under testdata/
// because the retired spellings among them are input to this gate rather
// than pointers into the specification, and testdata/ sits outside the read
// domain of the line-citation ratchet and the citation resolver, which would
// otherwise count a fixture spelling as a citation this file carries.
const citationFormFixtures = "testdata/degradation-lock-citation-form"

// citationFormCases exercise the anchor-form predicate over fixture comment
// blocks. The retired spellings are the ones the freshness gate must reject,
// so the retirement of the line-numbered citation cannot be undone by editing
// a comment above one of the declarations the gate reads.
var citationFormCases = []struct {
	name     string
	fixture  string
	wantFail bool
}{
	{
		name:    "anchor-form citation passes",
		fixture: "anchor-form.txt",
	},
	{
		name:    "anchor-form citation inside a prose sentence passes",
		fixture: "anchor-form-in-prose.txt",
	},
	{
		name:     "retired single-line spelling fails",
		fixture:  "retired-single-line.txt",
		wantFail: true,
	},
	{
		name:     "retired line-range spelling fails",
		fixture:  "retired-line-range.txt",
		wantFail: true,
	},
	{
		name:     "retired spelling beside an anchor-form citation fails",
		fixture:  "retired-beside-anchor-form.txt",
		wantFail: true,
	},
	{
		name:     "a block carrying no citation fails",
		fixture:  "no-citation.txt",
		wantFail: true,
	},
	{
		name:     "a citation of another section fails",
		fixture:  "other-section.txt",
		wantFail: true,
	},
}

// fixtureCommentBlock reads a fixture comment block and joins its lines the
// way commentBlockAbove joins the lines it reads out of a Go source file, so
// the predicate sees the same input in both.
func fixtureCommentBlock(t *testing.T, fixture string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(citationFormFixtures, fixture))
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixture, err)
	}
	var block []string
	for _, l := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			continue
		}
		block = append(block, trimmed)
	}
	return strings.Join(block, " ")
}

// spec: §25.4 (degradation.warnings on optional-key idempotency endpoints and
//
//	the ops.locks.memoryTier replica-local warning, whose citations this
//	gate reads), §28.1 (N8, the citation rule: a specification citation
//	names a heading rather than a line, so the retired line-numbered form
//	may not be written)
//
// diagnosis: The §25.4 freshness gate's citation-form predicate accepts a
//
//	citation spelling it must reject, or rejects one it must accept. An
//	accepted line-numbered spelling means the gate no longer holds the
//	retirement of line citations, so a comment in pkg/ops/ can reintroduce
//	one without failing tier 0.
func TestSpec254CitationFormRejectsRetiredLineCitations(t *testing.T) {
	t.Parallel()

	for _, tc := range citationFormCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			block := fixtureCommentBlock(t, tc.fixture)
			defect := citationForm(block)
			if tc.wantFail && defect == "" {
				t.Errorf("the predicate accepted fixture %s; want it rejected: %s", tc.fixture, block)
			}
			if !tc.wantFail && defect != "" {
				t.Errorf("the predicate rejected fixture %s as %q; want it accepted: %s", tc.fixture, defect, block)
			}
		})
	}
}

// inertRunCases exercise the guard that keeps the freshness gate from
// reporting green on a run that compared nothing. The counts stand for the
// two selections the gate makes: the declaration table it ranges over and the
// §25.4 body it searches.
var inertRunCases = []struct {
	name         string
	declarations int
	bodyLines    int
	wantFail     bool
}{
	{
		name:         "a run with declarations and a section body passes",
		declarations: 2,
		bodyLines:    40,
	},
	{
		name:         "a run whose declaration table selects nothing fails",
		declarations: 0,
		bodyLines:    40,
		wantFail:     true,
	},
	{
		name:         "a run whose §25.4 lookup returns an empty body fails",
		declarations: 2,
		bodyLines:    0,
		wantFail:     true,
	},
	{
		name:         "a run that selected nothing on either side fails",
		declarations: 0,
		bodyLines:    0,
		wantFail:     true,
	},
}

// spec: §25.4 (degradation.warnings on optional-key idempotency endpoints and
//
//	the ops.locks.memoryTier replica-local warning, the citations whose
//	freshness this gate certifies), §28.1 (N8, the citation rule the gate
//	holds)
//
// diagnosis: The §25.4 freshness gate can report green on a run that inspected
//
//	no declaration or read no §25.4 body. The gate then certifies nothing
//	while passing, so a citation that drifted from the section it names
//	reaches the tree without failing tier 0. Restore the guard rather than
//	the count it reads.
func TestSpec254FreshnessRunFailsWhenItInspectsNothing(t *testing.T) {
	t.Parallel()

	for _, tc := range inertRunCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			defect := inertRun(tc.declarations, tc.bodyLines)
			if tc.wantFail && defect == "" {
				t.Errorf("the guard passed a run over %d declaration(s) and %d body line(s); want it failed", tc.declarations, tc.bodyLines)
			}
			if !tc.wantFail && defect != "" {
				t.Errorf("the guard failed a run over %d declaration(s) and %d body line(s) as %q; want it passed", tc.declarations, tc.bodyLines, defect)
			}
		})
	}
}

// spec: §25.4 (the section the freshness gate reads), §28.1 (N8)
//
// diagnosis: The declaration table the §25.4 freshness gate ranges over is
//
//	empty, or spec/25_agent-operability.md carries no §25.4 body, so the
//	gate's own inputs are the inert case its guard rejects.
func TestSpec254FreshnessGateInputsAreNonEmpty(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	specData, err := os.ReadFile(filepath.Join(root, "spec/25_agent-operability.md"))
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	body := sectionBody(t, strings.Split(string(specData), "\n"), "## 25.4 ")
	if defect := inertRun(len(specLineCitationChecks), len(body)); defect != "" {
		t.Errorf("the freshness gate's own inputs are inert: it %s", defect)
	}
}
