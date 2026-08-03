// SPDX-License-Identifier: MIT

// Tier-11 documentation check reconciling the spec index with the
// communication-channels section. These tests are NOT under a build tag
// because they exercise the repository state directly — no external
// infrastructure required.

package tier11_docs_test

import (
	"fmt"
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

// indexSubsectionLevel is the heading level of the subsections the index
// carries a row for. Deeper headings name tables inside a subsection and
// carry no row of their own, but a row that does point at one resolves,
// so the two directions run over different heading sets.
const indexSubsectionLevel = 2

// headingsByAnchor maps each derived anchor to the positions of every
// heading that derives it. A slug two headings share makes a published
// row ambiguous: a reader following it lands at the first occurrence and
// the second heading is unreachable from the index.
func headingsByAnchor(headings []markdownHeading) map[string][]int {
	byAnchor := make(map[string][]int, len(headings))
	for i, h := range headings {
		slug := slugify(h.text)
		byAnchor[slug] = append(byAnchor[slug], i)
	}
	return byAnchor
}

// headingTitlesAt returns the titles of the headings at the given
// positions, for an error message naming them.
func headingTitlesAt(headings []markdownHeading, idx []int) []string {
	titles := make([]string, 0, len(idx))
	for _, i := range idx {
		titles = append(titles, headings[i].text)
	}
	return titles
}

// indexReconciliation is the outcome of comparing a section's headings
// with the anchored index rows that point into it.
type indexReconciliation struct {
	dangling  []string // row anchors that resolve to no heading
	uncovered []string // subsection headings that carry no row
	ambiguous []string // anchors two headings derive, or two rows carry
}

// reconcileIndexRows compares a section's headings with the anchored
// index rows that point into it. A row resolves against a heading of any
// level, because a fragment pointing at a deeper heading resolves in a
// rendered document. The coverage direction runs over the subsection
// headings alone, since those are the headings the index carries a row
// for. Coverage is tracked per heading rather than per anchor, so one
// row cannot cover two headings that collide on a slug.
//
// spec: §28
func reconcileIndexRows(headings []markdownHeading, rows []specIndexRow) indexReconciliation {
	var rec indexReconciliation

	bySlug := headingsByAnchor(headings)
	for slug, idx := range bySlug {
		if len(idx) > 1 {
			rec.ambiguous = append(rec.ambiguous,
				fmt.Sprintf("anchor #%s is derived by more than one heading: %s",
					slug, strings.Join(headingTitlesAt(headings, idx), ", ")))
		}
	}

	seenRow := make(map[string]bool, len(rows))
	covered := make(map[int]bool, len(rows))
	for _, row := range rows {
		if seenRow[row.anchor] {
			rec.ambiguous = append(rec.ambiguous,
				fmt.Sprintf("index row anchor #%s appears in more than one row", row.anchor))
		}
		seenRow[row.anchor] = true

		idx, ok := bySlug[row.anchor]
		if !ok {
			rec.dangling = append(rec.dangling, row.anchor)
			continue
		}
		// On a collision the row can only reach the first heading, so
		// only that heading counts as covered.
		covered[idx[0]] = true
	}

	for i, h := range headings {
		if h.level == indexSubsectionLevel && !covered[i] {
			rec.uncovered = append(rec.uncovered, h.text)
		}
	}

	sort.Strings(rec.dangling)
	sort.Strings(rec.uncovered)
	sort.Strings(rec.ambiguous)
	return rec
}

// titleMismatches returns one message per anchored row whose title is
// not the title of the single heading it points at, so the index and the
// section name each subsection the same way. A row pointing at an
// ambiguous anchor is reported too: it names at most one of the headings
// it could reach.
func titleMismatches(headings []markdownHeading, rows []specIndexRow) []string {
	byAnchor := headingsByAnchor(headings)
	var out []string
	for _, row := range rows {
		idx, ok := byAnchor[row.anchor]
		if !ok {
			continue
		}
		titles := headingTitlesAt(headings, idx)
		if len(titles) != 1 || titles[0] != row.title {
			out = append(out, fmt.Sprintf("index row %q points at heading(s) %q; the titles must match one to one",
				row.title, strings.Join(titles, ", ")))
		}
	}
	sort.Strings(out)
	return out
}

// diagnosis: a failure means spec/README.md's index and
// spec/28_communication-channels.md disagree on a heading title or on
// an anchor. Either a row points at a fragment no heading in the
// section produces, or a subsection heading was added or renamed
// without its index row, or two headings derive one anchor, so a reader
// following the published index lands nowhere, never reaches the
// subsection, or reaches the wrong one.
//
// spec: §28
func TestSection28IndexRowsResolve_spec_28(t *testing.T) {
	t.Run("landed section", func(t *testing.T) { assertSection28IndexReconciles(t) })
	t.Run("collapsed punctuation run", func(t *testing.T) { assertCollapsedAnchorIsReported(t) })
	t.Run("fenced example line", func(t *testing.T) { assertFencedLineIsNotAHeading(t) })
	t.Run("deep heading resolves", func(t *testing.T) { assertRowAtDeeperHeadingResolves(t) })
	t.Run("ambiguous anchor", func(t *testing.T) { assertAmbiguousAnchorIsReported(t) })
	t.Run("underscore kept", func(t *testing.T) { assertUnderscoreSurvivesTheSlugRule(t) })
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
			var got []string
			for _, h := range scanMarkdownHeadings(tc.doc) {
				got = append(got, h.text)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("scanMarkdownHeadings = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("scanMarkdownHeadings = %v, want %v", got, tc.want)
				}
			}
		})
	}

	// A fenced example line admitted as a heading is reported as
	// uncovered, blaming the index for content that is not a heading.
	doc := "## 28.1 Naming law\n\n```markdown\n## 28.99 Example heading\n```\n"
	rows := []specIndexRow{{title: "28.1 Naming law", anchor: "281-naming-law"}}
	rec := reconcileIndexRows(scanMarkdownHeadings(doc), rows)
	if len(rec.dangling) != 0 || len(rec.uncovered) != 0 || len(rec.ambiguous) != 0 {
		t.Errorf("fenced example reported against the index: %+v", rec)
	}
}

// assertRowAtDeeperHeadingResolves pins that a row anchored at a heading
// below the subsection level resolves. §28.3 publishes its register
// tables under level-3 headings, and the index rows the section gains
// later point at headings at that depth. Resolving rows against the
// subsection headings alone reports such a row as dangling even though
// it resolves in a rendered document.
//
// spec: §28.3
func assertRowAtDeeperHeadingResolves(t *testing.T) {
	t.Helper()
	doc := "# 28. Communication Channels\n\n## 28.3 Registers\n\n### Link register\n\n#### Gateway-to-pod\n"
	rows := []specIndexRow{
		{title: "28.3 Registers", anchor: "283-registers"},
		{title: "Link register", anchor: "link-register"},
		{title: "Gateway-to-pod", anchor: "gateway-to-pod"},
	}
	rec := reconcileIndexRows(scanMarkdownHeadings(doc), rows)
	if len(rec.dangling) != 0 {
		t.Errorf("row at a heading below the subsection level reported dangling: %v", rec.dangling)
	}
	if len(rec.uncovered) != 0 {
		t.Errorf("subsection left uncovered: %v", rec.uncovered)
	}
	if got := titleMismatches(scanMarkdownHeadings(doc), rows); len(got) != 0 {
		t.Errorf("title cross-check reported a correct row: %v", got)
	}

	// The coverage direction stays at the subsection level: a deeper
	// heading with no row of its own is not reported.
	rec = reconcileIndexRows(scanMarkdownHeadings(doc), rows[:1])
	if len(rec.uncovered) != 0 {
		t.Errorf("heading below the subsection level reported as missing an index row: %v", rec.uncovered)
	}
}

// assertAmbiguousAnchorIsReported pins that two headings deriving one
// anchor, and two rows carrying one anchor, are both reported. The
// anchor rule rests on the section's titles deriving distinct slugs: on
// a collision a published row reaches the first heading only, and the
// second is unreachable from the index while both directions would
// otherwise read as satisfied.
//
// spec: §28
func assertAmbiguousAnchorIsReported(t *testing.T) {
	t.Helper()

	// Two subsection titles collapsing to one slug: "28.3 Registers" and
	// "28.3: Registers" both derive "283-registers".
	doc := "## 28.3 Registers\n\n## 28.3: Registers\n"
	rows := []specIndexRow{{title: "28.3 Registers", anchor: "283-registers"}}
	rec := reconcileIndexRows(scanMarkdownHeadings(doc), rows)
	if len(rec.ambiguous) != 1 || !strings.Contains(rec.ambiguous[0], "283-registers") {
		t.Errorf("colliding headings not reported: %v", rec.ambiguous)
	}
	if len(rec.uncovered) != 1 || rec.uncovered[0] != "28.3: Registers" {
		t.Errorf("the heading the single row cannot reach must still be reported uncovered: %v", rec.uncovered)
	}
	if got := titleMismatches(scanMarkdownHeadings(doc), rows); len(got) != 1 {
		t.Errorf("title cross-check accepted a row pointing at an ambiguous anchor: %v", got)
	}

	// Two rows carrying one anchor are ambiguous in the index itself.
	doc = "## 28.3 Registers\n"
	rows = []specIndexRow{
		{title: "28.3 Registers", anchor: "283-registers"},
		{title: "28.3 Registers", anchor: "283-registers"},
	}
	rec = reconcileIndexRows(scanMarkdownHeadings(doc), rows)
	if len(rec.ambiguous) != 1 || !strings.Contains(rec.ambiguous[0], "more than one row") {
		t.Errorf("duplicate index rows not reported: %v", rec.ambiguous)
	}
}

// assertUnderscoreSurvivesTheSlugRule pins the one character the slug
// rule keeps that a punctuation-dropping derivation would delete. An
// underscore is a letter-like character in an identifier title, and the
// rule the tree's anchors follow keeps it, so a heading naming an
// identifier resolves from its index row.
//
// spec: §28
func assertUnderscoreSurvivesTheSlugRule(t *testing.T) {
	t.Helper()
	if got, want := slugify("28.5 sessions_served"), "285-sessions_served"; got != want {
		t.Errorf("slugify(%q) = %q, want %q", "28.5 sessions_served", got, want)
	}
	doc := "## 28.5 sessions_served\n"
	rows := []specIndexRow{{title: "28.5 sessions_served", anchor: "285-sessions_served"}}
	rec := reconcileIndexRows(scanMarkdownHeadings(doc), rows)
	if len(rec.dangling) != 0 || len(rec.uncovered) != 0 {
		t.Errorf("underscore dropped from the derived anchor: %+v", rec)
	}
}

// assertSection28IndexReconciles asserts both directions over the
// tracked tree: every anchored index row for the section resolves to a
// heading, and every subsection heading in the section carries an index
// row.
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

	headings := scanMarkdownHeadings(string(section))
	if len(headings) == 0 {
		t.Fatalf("%s carries no heading", channelsSpecFile)
	}

	rec := reconcileIndexRows(headings, rows)
	for _, anchor := range rec.dangling {
		t.Errorf("index row anchor #%s resolves to no heading in %s (headings: %v)", anchor, channelsSpecFile, headings)
	}
	for _, title := range rec.uncovered {
		t.Errorf("heading %q in %s carries no index row in spec/README.md", title, channelsSpecFile)
	}
	for _, msg := range rec.ambiguous {
		t.Errorf("%s and spec/README.md carry an ambiguous anchor: %s", channelsSpecFile, msg)
	}
	for _, msg := range titleMismatches(headings, rows) {
		t.Error(msg)
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
			if got := slugify(tc.title); got != tc.want {
				t.Errorf("slugify(%q) = %q, want %q", tc.title, got, tc.want)
			}
		})
	}

	// A fixture index row whose anchor collapses the punctuation run to
	// one hyphen must be reported as dangling. Were the derivation to
	// collapse instead of delete, this row would resolve and the real
	// rows would be the ones reported broken.
	headings := []markdownHeading{{level: indexSubsectionLevel, text: "28.9 Registers (v2): keys"}}
	rows := []specIndexRow{{title: "28.9 Registers (v2): keys", anchor: "289-registers-v2-keys"}}
	collapsed := []specIndexRow{{title: "28.9 Registers (v2): keys", anchor: "28-9-registers-v2-keys"}}

	if rec := reconcileIndexRows(headings, rows); len(rec.dangling) != 0 || len(rec.uncovered) != 0 {
		t.Errorf("correct anchor reported broken: %+v", rec)
	}
	rec := reconcileIndexRows(headings, collapsed)
	if len(rec.dangling) != 1 || rec.dangling[0] != "28-9-registers-v2-keys" {
		t.Errorf("collapsed anchor not reported dangling: %v", rec.dangling)
	}
	if len(rec.uncovered) != 1 || rec.uncovered[0] != "28.9 Registers (v2): keys" {
		t.Errorf("heading left uncovered by the collapsed anchor not reported: %v", rec.uncovered)
	}
}
