// SPDX-License-Identifier: MIT

// Tier-11 documentation check reconciling the spec index with the
// communication-channels section. These tests are NOT under a build tag
// because they exercise the repository state directly — no external
// infrastructure required.

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// channelsSpecFile is the section file whose index rows this test
// reconciles.
//
// spec: §28
const channelsSpecFile = "28_communication-channels.md"

// specIndexRow is one anchored row of spec/README.md: the link title
// and the fragment it points at inside the section file.
type specIndexRow struct {
	title  string
	anchor string
}

// specAnchorSlug derives a heading's anchor under the slug rule the
// tree's existing spec anchors follow: lowercase the title, delete
// every character that is not a letter, a digit, a space, a hyphen, or
// an underscore, and replace each remaining space with one hyphen.
// Deleting punctuation rather than collapsing a run of it to a single
// hyphen is what makes "28.1 Naming law" resolve to "281-naming-law".
//
// spec: §28
func specAnchorSlug(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		case r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// specSubsectionHeadings returns the level-2 heading titles of a spec
// markdown document, in document order. Level-3 headings name tables
// inside a subsection and carry no index row. A "## " line inside a
// fenced code block is example content rather than a heading: it
// produces no anchor and carries no index row, so the scan skips it.
func specSubsectionHeadings(doc string) []string {
	var titles []string
	inFence := false
	for _, line := range strings.Split(doc, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			titles = append(titles, strings.TrimSpace(strings.TrimPrefix(line, "## ")))
		}
	}
	return titles
}

// indexRowPattern matches one markdown list row of spec/README.md,
// capturing the link title, the target file, and the optional fragment.
var indexRowPattern = regexp.MustCompile(`^\s*- \[([^\]]*)\]\(([^)#]+)(?:#([^)]*))?\)\s*$`)

// specIndexRowsFor returns the anchored index rows of spec/README.md
// that point into file, and reports whether the file's own unanchored
// section row is present.
func specIndexRowsFor(index, file string) (rows []specIndexRow, sectionRow bool) {
	for _, line := range strings.Split(index, "\n") {
		m := indexRowPattern.FindStringSubmatch(line)
		if m == nil || m[2] != file {
			continue
		}
		if m[3] == "" {
			sectionRow = true
			continue
		}
		rows = append(rows, specIndexRow{title: m[1], anchor: m[3]})
	}
	return rows, sectionRow
}

// reconcileIndexRows compares a section's level-2 headings with the
// anchored index rows that point into it. It returns the anchors of
// rows that resolve to no heading and the titles of headings that carry
// no row.
func reconcileIndexRows(headings []string, rows []specIndexRow) (dangling, uncovered []string) {
	byAnchor := make(map[string]string, len(headings))
	for _, title := range headings {
		byAnchor[specAnchorSlug(title)] = title
	}
	covered := make(map[string]bool, len(rows))
	for _, row := range rows {
		if _, ok := byAnchor[row.anchor]; !ok {
			dangling = append(dangling, row.anchor)
			continue
		}
		covered[row.anchor] = true
	}
	for _, title := range headings {
		if !covered[specAnchorSlug(title)] {
			uncovered = append(uncovered, title)
		}
	}
	sort.Strings(dangling)
	sort.Strings(uncovered)
	return dangling, uncovered
}

// diagnosis: a failure means spec/README.md's index and
// spec/28_communication-channels.md disagree on a heading title or on
// an anchor. Either a row points at a fragment no heading in the
// section produces, or a subsection heading was added or renamed
// without its index row, so a reader following the published index
// lands nowhere or never reaches the subsection.
//
// spec: §28
func TestSection28IndexRowsResolve_spec_28(t *testing.T) {
	t.Run("landed section", func(t *testing.T) { assertSection28IndexReconciles(t) })
	t.Run("collapsed punctuation run", func(t *testing.T) { assertCollapsedAnchorIsReported(t) })
	t.Run("fenced example line", func(t *testing.T) { assertFencedLineIsNotAHeading(t) })
}

// assertFencedLineIsNotAHeading pins that a "## " line inside a fenced
// code block is example content and is never reported as a subsection
// heading missing its index row. Without the fence guard the fenced
// line is admitted as a heading, and the reconciliation blames the
// index for a heading the section does not publish.
//
// spec: §28
func assertFencedLineIsNotAHeading(t *testing.T) {
	t.Helper()
	for _, tc := range []struct {
		name string
		doc  string
		want []string
	}{
		{
			name: "backtick fence",
			doc:  "## 28.1 Naming law\n\n```markdown\n## 28.99 Example heading\n```\n\n## 28.2 Taxonomy\n",
			want: []string{"28.1 Naming law", "28.2 Taxonomy"},
		},
		{
			name: "tilde fence",
			doc:  "## 28.1 Naming law\n\n~~~\n## 28.99 Example heading\n~~~\n",
			want: []string{"28.1 Naming law"},
		},
		{
			name: "indented fence",
			doc:  "## 28.1 Naming law\n\n  ```\n## 28.99 Example heading\n  ```\n",
			want: []string{"28.1 Naming law"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := specSubsectionHeadings(tc.doc)
			if len(got) != len(tc.want) {
				t.Fatalf("specSubsectionHeadings = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("specSubsectionHeadings = %v, want %v", got, tc.want)
				}
			}
		})
	}

	// A fenced example line admitted as a heading is reported as
	// uncovered, blaming the index for content that is not a heading.
	doc := "## 28.1 Naming law\n\n```markdown\n## 28.99 Example heading\n```\n"
	rows := []specIndexRow{{title: "28.1 Naming law", anchor: "281-naming-law"}}
	dangling, uncovered := reconcileIndexRows(specSubsectionHeadings(doc), rows)
	if len(dangling) != 0 || len(uncovered) != 0 {
		t.Errorf("fenced example reported against the index: dangling=%v uncovered=%v", dangling, uncovered)
	}
}

// assertSection28IndexReconciles asserts both directions over the
// tracked tree: every anchored index row for the section resolves to a
// heading, and every heading in the section carries an index row.
func assertSection28IndexReconciles(t *testing.T) {
	t.Helper()
	root := repoRoot(t)

	index, err := os.ReadFile(filepath.Join(root, "spec", "README.md"))
	if err != nil {
		t.Fatalf("read spec index: %v", err)
	}
	section, err := os.ReadFile(filepath.Join(root, "spec", channelsSpecFile))
	if err != nil {
		t.Fatalf("read %s: %v", channelsSpecFile, err)
	}

	rows, sectionRow := specIndexRowsFor(string(index), channelsSpecFile)
	if !sectionRow {
		t.Errorf("spec/README.md carries no unanchored section row for %s", channelsSpecFile)
	}
	if len(rows) == 0 {
		t.Fatalf("spec/README.md carries no anchored index row for %s", channelsSpecFile)
	}

	headings := specSubsectionHeadings(string(section))
	if len(headings) == 0 {
		t.Fatalf("%s carries no level-2 heading", channelsSpecFile)
	}

	dangling, uncovered := reconcileIndexRows(headings, rows)
	for _, anchor := range dangling {
		t.Errorf("index row anchor #%s resolves to no heading in %s (headings: %v)", anchor, channelsSpecFile, headings)
	}
	for _, title := range uncovered {
		t.Errorf("heading %q in %s carries no index row in spec/README.md", title, channelsSpecFile)
	}

	// Every anchored row's title must be the heading title it points
	// at, so the index and the section name the subsection the same way.
	byAnchor := make(map[string]string, len(headings))
	for _, title := range headings {
		byAnchor[specAnchorSlug(title)] = title
	}
	for _, row := range rows {
		if title, ok := byAnchor[row.anchor]; ok && title != row.title {
			t.Errorf("index row %q points at heading %q; the titles must match", row.title, title)
		}
	}
}

// assertCollapsedAnchorIsReported pins the derivation rule against the
// error that collapses a run of punctuation to one hyphen, which would
// report the section's correct rows as broken.
func assertCollapsedAnchorIsReported(t *testing.T) {
	t.Helper()
	for _, tc := range []struct {
		name  string
		title string
		want  string
	}{
		{name: "subsection number", title: "28.1 Naming law", want: "281-naming-law"},
		{name: "punctuation run", title: "28.9 Registers (v2): keys", want: "289-registers-v2-keys"},
		{name: "hyphen kept", title: "28.10 Roll-forward notes", want: "2810-roll-forward-notes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := specAnchorSlug(tc.title); got != tc.want {
				t.Errorf("specAnchorSlug(%q) = %q, want %q", tc.title, got, tc.want)
			}
		})
	}

	// A fixture index row whose anchor collapses the punctuation run to
	// one hyphen must be reported as dangling. Were the derivation to
	// collapse instead of delete, this row would resolve and the real
	// rows would be the ones reported broken.
	headings := []string{"28.9 Registers (v2): keys"}
	rows := []specIndexRow{{title: "28.9 Registers (v2): keys", anchor: "289-registers-v2-keys"}}
	collapsed := []specIndexRow{{title: "28.9 Registers (v2): keys", anchor: "28-9-registers-v2-keys"}}

	if dangling, uncovered := reconcileIndexRows(headings, rows); len(dangling) != 0 || len(uncovered) != 0 {
		t.Errorf("correct anchor reported broken: dangling=%v uncovered=%v", dangling, uncovered)
	}
	dangling, uncovered := reconcileIndexRows(headings, collapsed)
	if len(dangling) != 1 || dangling[0] != "28-9-registers-v2-keys" {
		t.Errorf("collapsed anchor not reported dangling: %v", dangling)
	}
	if len(uncovered) != 1 || uncovered[0] != "28.9 Registers (v2): keys" {
		t.Errorf("heading left uncovered by the collapsed anchor not reported: %v", uncovered)
	}
}
