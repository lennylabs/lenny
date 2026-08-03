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
// that point into file, together with the link title of the file's own
// unanchored section row. The section title is empty when the index
// carries no such row. The title is returned rather than a presence bit
// so the caller can hold the section row and the section's level-1
// heading to the same one-to-one title rule the anchored rows follow.
func specIndexRowsFor(index, file string) (rows []specIndexRow, sectionTitle string) {
	for _, line := range strings.Split(index, "\n") {
		m := indexRowPattern.FindStringSubmatch(line)
		if m == nil || m[2] != file {
			continue
		}
		if m[3] == "" {
			sectionTitle = m[1]
			continue
		}
		rows = append(rows, specIndexRow{title: m[1], anchor: m[3]})
	}
	return rows, sectionTitle
}

// sectionHeadingLevel is the heading level of the heading that names the
// section as a whole, the one the index's unanchored row links to.
const sectionHeadingLevel = 1

// sectionRowMismatches returns one message per disagreement between the
// index's unanchored section row and the section file's level-1
// heading. The section is named once by its own heading and once by the
// row that links to the file, and those two titles must match, exactly
// as an anchored row's title must match the subsection heading it points
// at. A file with no level-1 heading, or with more than one, is reported
// too: the index row would then name no heading or an arbitrary one.
//
// spec: §28
func sectionRowMismatches(headings []markdownHeading, sectionTitle string) []string {
	var out []string
	if sectionTitle == "" {
		out = append(out, "the spec index carries no unanchored section row for the section file")
	}

	var titles []string
	for _, h := range headings {
		if h.level == sectionHeadingLevel {
			titles = append(titles, h.text)
		}
	}
	switch {
	case len(titles) == 0:
		out = append(out, "the section file carries no level-1 heading for the index's section row to name")
	case len(titles) > 1:
		out = append(out, fmt.Sprintf("the section file carries more than one level-1 heading: %s",
			strings.Join(titles, ", ")))
	case sectionTitle != "" && titles[0] != sectionTitle:
		out = append(out, fmt.Sprintf("index section row %q names level-1 heading %q; the titles must match one to one",
			sectionTitle, titles[0]))
	}
	return out
}

// subsectionHeadingLevel is the heading level of the numbered
// subsections the index's anchored rows name, and the level that encloses
// the register tables.
const subsectionHeadingLevel = 2

// registerTableLevel is the heading level the register tables of §28.3
// are published at.
const registerTableLevel = 3

// registerSubsectionTitle is the level-2 heading that encloses the
// section's register tables. The exemption below applies under this
// heading only.
//
// spec: §28.3
const registerSubsectionTitle = "28.3 Registers"

// registerTableTitles are the headings inside §28.3 that name one of the
// section's register tables. They are the only headings in the section
// the published index carries no anchored row for, and the exemption is
// closed: a heading added or renamed under §28.3 is reported until this
// list and the index agree again, and a title listed here that does gain
// an index row is reported too, so the list cannot outlive the gap it
// records.
//
// spec: §28.3
var registerTableTitles = []string{
	"Link register",
	"Channel register",
	"Register-entry register",
	"Naming table",
}

// isRegisterTableHeading reports whether h is one of the register tables
// the published index carries no anchored row for. The exemption is
// scoped to the headings it describes rather than to their text: h must
// be a level-3 heading, and the level-2 heading enclosing it must be
// §28.3. A heading carrying a listed title at another level, or under
// another subsection, falls back to the coverage rule and is reported as
// carrying no index row, so the section can grow further subsections
// without a listed title silently exempting a heading in one of them.
//
// spec: §28.3
func isRegisterTableHeading(h markdownHeading, enclosing string, exempt map[string]bool) bool {
	return h.level == registerTableLevel && enclosing == registerSubsectionTitle && exempt[h.text]
}

// titleSet turns a list of heading titles into the membership set
// reconcileIndexRows takes, so a fixture case states its own exempt set
// instead of reading the landed one.
func titleSet(titles ...string) map[string]bool {
	set := make(map[string]bool, len(titles))
	for _, title := range titles {
		set[title] = true
	}
	return set
}

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
	uncovered []string // headings that carry no row and are not exempt
	ambiguous []string // anchors two headings derive, or two rows carry
	// staleExempt holds the titles listed as taking no index row that do
	// carry one, so the exempt list is closed in both directions.
	staleExempt []string
}

// reconcileIndexRows compares a section's headings with the anchored
// index rows that point into it. A row resolves against a heading of any
// level, because a fragment pointing at a deeper heading resolves in a
// rendered document. The coverage direction runs over every heading
// below the section's own level-1 heading, which the unanchored section
// row names instead, minus the register tables of §28.3 named by exempt.
// Coverage is tracked per
// heading rather than per anchor, so one row cannot cover two headings
// that collide on a slug.
//
// spec: §28
func reconcileIndexRows(headings []markdownHeading, rows []specIndexRow, exempt map[string]bool) indexReconciliation {
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

	enclosing := ""
	for i, h := range headings {
		switch h.level {
		case sectionHeadingLevel:
			enclosing = ""
			continue
		case subsectionHeadingLevel:
			enclosing = h.text
		}
		isExempt := isRegisterTableHeading(h, enclosing, exempt)
		switch {
		case isExempt && covered[i]:
			rec.staleExempt = append(rec.staleExempt, h.text)
		case !isExempt && !covered[i]:
			rec.uncovered = append(rec.uncovered, h.text)
		}
	}

	sort.Strings(rec.dangling)
	sort.Strings(rec.uncovered)
	sort.Strings(rec.ambiguous)
	sort.Strings(rec.staleExempt)
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
// section produces, or a heading was added or renamed without its index
// row and without joining the register-table headings the index carries
// no row for, or two headings derive one anchor, so a reader following
// the published index lands nowhere, never reaches the heading, or
// reaches the wrong one.
//
// spec: §28
func TestSection28IndexRowsResolve_spec_28(t *testing.T) {
	t.Run("landed section", func(t *testing.T) { assertSection28IndexReconciles(t) })
	t.Run("collapsed punctuation run", func(t *testing.T) { assertCollapsedAnchorIsReported(t) })
	t.Run("fenced example line", func(t *testing.T) { assertFencedLineIsNotAHeading(t) })
	t.Run("deep heading resolves", func(t *testing.T) { assertRowAtDeeperHeadingResolves(t) })
	t.Run("numbered deep heading needs a row", func(t *testing.T) { assertNumberedDeepHeadingNeedsAnIndexRow(t) })
	t.Run("register-table exemption is closed", func(t *testing.T) { assertRegisterTableExemptionIsClosed(t) })
	t.Run("register-table exemption is scoped", func(t *testing.T) { assertRegisterTableExemptionIsScopedToItsSubsection(t) })
	t.Run("ambiguous anchor", func(t *testing.T) { assertAmbiguousAnchorIsReported(t) })
	t.Run("underscore kept", func(t *testing.T) { assertUnderscoreSurvivesTheSlugRule(t) })
	t.Run("non-ascii letter kept", func(t *testing.T) { assertNonASCIILetterSurvivesTheSlugRule(t) })
	t.Run("section row title", func(t *testing.T) { assertSectionRowNamesTheLevel1Heading(t) })
}

// assertNonASCIILetterSurvivesTheSlugRule pins the letter class of the
// derivation. The rule keeps every letter, so a heading carrying a
// letter outside the ASCII range derives an anchor that still contains
// it. A derivation restricted to a-z deletes the letter instead and
// reports the row that points at such a heading as dangling.
//
// spec: §28
func assertNonASCIILetterSurvivesTheSlugRule(t *testing.T) {
	t.Helper()
	if got, want := slugify("28.6 Café registers"), "286-café-registers"; got != want {
		t.Errorf("slugify(%q) = %q, want %q", "28.6 Café registers", got, want)
	}
	doc := "## 28.6 Café registers\n"
	rows := []specIndexRow{{title: "28.6 Café registers", anchor: "286-café-registers"}}
	rec := reconcileIndexRows(scanMarkdownHeadings(doc), rows, nil)
	if len(rec.dangling) != 0 || len(rec.uncovered) != 0 {
		t.Errorf("non-ASCII letter dropped from the derived anchor: %+v", rec)
	}
}

// assertSectionRowNamesTheLevel1Heading pins that the index's unanchored
// section row and the section's level-1 heading are held to the same
// one-to-one title rule the anchored rows are. Reducing that row to a
// presence check lets the heading and its index row be renamed
// independently, which is exactly the index-versus-section title
// disagreement this test reports.
//
// spec: §28
func assertSectionRowNamesTheLevel1Heading(t *testing.T) {
	t.Helper()

	const index = "- [28. Communication Channels](28_communication-channels.md)\n" +
		"  - [28.1 Naming law](28_communication-channels.md#281-naming-law)\n"
	rows, sectionTitle := specIndexRowsFor(index, channelsSpecFile)
	if len(rows) != 1 || rows[0].anchor != "281-naming-law" {
		t.Fatalf("anchored rows = %+v", rows)
	}
	if sectionTitle != "28. Communication Channels" {
		t.Fatalf("section row title = %q, want %q", sectionTitle, "28. Communication Channels")
	}

	matching := scanMarkdownHeadings("# 28. Communication Channels\n\n## 28.1 Naming law\n")
	if got := sectionRowMismatches(matching, sectionTitle); len(got) != 0 {
		t.Errorf("matching section row and level-1 heading reported: %v", got)
	}

	for _, tc := range []struct {
		name    string
		doc     string
		title   string
		wantSub string
	}{
		{
			name:    "renamed heading",
			doc:     "# 28. Communication channels\n\n## 28.1 Naming law\n",
			title:   sectionTitle,
			wantSub: "must match one to one",
		},
		{
			name:    "missing section row",
			doc:     "# 28. Communication Channels\n",
			title:   "",
			wantSub: "no unanchored section row",
		},
		{
			name:    "no level-1 heading",
			doc:     "## 28.1 Naming law\n",
			title:   sectionTitle,
			wantSub: "no level-1 heading",
		},
		{
			name:    "two level-1 headings",
			doc:     "# 28. Communication Channels\n\n# 28. Communication Channels bis\n",
			title:   sectionTitle,
			wantSub: "more than one level-1 heading",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sectionRowMismatches(scanMarkdownHeadings(tc.doc), tc.title)
			if len(got) != 1 || !strings.Contains(got[0], tc.wantSub) {
				t.Errorf("sectionRowMismatches = %v, want one message containing %q", got, tc.wantSub)
			}
		})
	}
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
	rec := reconcileIndexRows(scanMarkdownHeadings(doc), rows, nil)
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
	rec := reconcileIndexRows(scanMarkdownHeadings(doc), rows, nil)
	if len(rec.dangling) != 0 {
		t.Errorf("row at a heading below the subsection level reported dangling: %v", rec.dangling)
	}
	if len(rec.uncovered) != 0 {
		t.Errorf("subsection left uncovered: %v", rec.uncovered)
	}
	if got := titleMismatches(scanMarkdownHeadings(doc), rows); len(got) != 0 {
		t.Errorf("title cross-check reported a correct row: %v", got)
	}

	// The coverage direction runs over every heading below the level-1
	// one, so dropping the two deeper rows leaves both headings reported.
	// An unnumbered heading is reachable from the index only through a row
	// of its own, and exempting it on its title stating no number lets a
	// heading added under §28.3 go unreported.
	rec = reconcileIndexRows(scanMarkdownHeadings(doc), rows[:1], nil)
	want := []string{"Gateway-to-pod", "Link register"}
	if !equalStrings(rec.uncovered, want) {
		t.Errorf("uncovered = %v, want %v", rec.uncovered, want)
	}
}

// equalStrings reports whether two sorted title lists hold the same
// titles in the same order.
func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// assertNumberedDeepHeadingNeedsAnIndexRow pins the coverage direction to
// the numbered-subsection property rather than to heading depth. A
// numbered subsection published below level 2, such as a contract card
// added under §28.5, is unreachable from the published index when no row
// names it, and that is the disagreement this file exists to report.
// Keying coverage on depth exempts every heading deeper than level 2 and
// leaves such a heading unreported.
//
// spec: §28
func assertNumberedDeepHeadingNeedsAnIndexRow(t *testing.T) {
	t.Helper()

	doc := "# 28. Communication Channels\n\n## 28.5 Contract cards\n\n### 28.5.1 Intra-pod\n\n### Naming table\n"
	rows := []specIndexRow{{title: "28.5 Contract cards", anchor: "285-contract-cards"}}

	exempt := titleSet(registerTableTitles...)

	// Both headings under §28.5 are reported: the numbered one because no
	// row names it, and "Naming table" because the register-table
	// exemption reaches under §28.3 only.
	rec := reconcileIndexRows(scanMarkdownHeadings(doc), rows, exempt)
	if !equalStrings(rec.uncovered, []string{"28.5.1 Intra-pod", "Naming table"}) {
		t.Errorf("headings below level 2 not reported as missing an index row: %v", rec.uncovered)
	}

	// The section's own level-1 heading states no subsection number and is
	// held to the unanchored section row instead, so it is never reported
	// here even though it carries no anchored row.
	for _, title := range rec.uncovered {
		if title == "28. Communication Channels" {
			t.Errorf("the section's level-1 heading was reported as missing an anchored index row")
		}
	}

	// Adding the rows clears the report.
	rows = append(rows,
		specIndexRow{title: "28.5.1 Intra-pod", anchor: "2851-intra-pod"},
		specIndexRow{title: "Naming table", anchor: "naming-table"})
	if rec := reconcileIndexRows(scanMarkdownHeadings(doc), rows, exempt); len(rec.uncovered) != 0 || len(rec.dangling) != 0 || len(rec.staleExempt) != 0 {
		t.Errorf("covered subsections still reported: %+v", rec)
	}
}

// assertRegisterTableExemptionIsScopedToItsSubsection pins that the
// register-table exemption is keyed on the heading's position rather than
// on its title alone. Only a level-3 heading enclosed by §28.3 takes it.
// A heading carrying one of the same titles at another level, or under
// another numbered subsection the section gains later, is unreachable
// from the published index unless a row names it, so it is reported. A
// membership test on the heading text alone exempts all of these and
// leaves the coverage direction open exactly where the section grows.
//
// spec: §28.3
func assertRegisterTableExemptionIsScopedToItsSubsection(t *testing.T) {
	t.Helper()

	exempt := titleSet(registerTableTitles...)
	const head = "# 28. Communication Channels\n\n"
	rows := []specIndexRow{
		{title: "28.3 Registers", anchor: "283-registers"},
		{title: "28.5 Contract cards", anchor: "285-contract-cards"},
	}

	for _, tc := range []struct {
		name string
		doc  string
		want []string
	}{
		{
			name: "level-3 under the registers subsection",
			doc:  head + "## 28.3 Registers\n\n### Link register\n",
			want: nil,
		},
		{
			name: "same title under another subsection",
			doc:  head + "## 28.3 Registers\n\n## 28.5 Contract cards\n\n### Naming table\n",
			want: []string{"Naming table"},
		},
		{
			name: "same title at the subsection level",
			doc:  head + "## 28.3 Registers\n\n## Link register\n",
			want: []string{"Link register"},
		},
		{
			name: "same title below the register-table level",
			doc:  head + "## 28.3 Registers\n\n### Link register\n\n#### Channel register\n",
			want: []string{"Channel register"},
		},
		{
			name: "same title before any subsection heading",
			doc:  head + "### Channel register\n\n## 28.3 Registers\n",
			want: []string{"Channel register"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := reconcileIndexRows(scanMarkdownHeadings(tc.doc), rows, exempt)
			if !equalStrings(rec.uncovered, tc.want) {
				t.Errorf("uncovered = %v, want %v", rec.uncovered, tc.want)
			}
			if len(rec.staleExempt) != 0 {
				t.Errorf("staleExempt = %v, want none", rec.staleExempt)
			}
		})
	}
}

// assertRegisterTableExemptionIsClosed pins that the set of headings the
// published index carries no row for is exactly the register-table
// headings of §28.3. The index names the section and its four numbered
// subsections, and the register tables inside §28.3 are the headings that
// stay out of it. Holding the exemption to that list keeps a heading added
// or renamed under §28.3 reported instead of silently unreachable from the
// index, and reports a listed title that does gain a row so the list
// cannot outlive the gap it records.
//
// spec: §28.3
func assertRegisterTableExemptionIsClosed(t *testing.T) {
	t.Helper()

	const doc = "# 28. Communication Channels\n\n## 28.3 Registers\n\n" +
		"### Link register\n\n### Channel register\n\n### Register-entry register\n\n### Naming table\n"
	rows := []specIndexRow{{title: "28.3 Registers", anchor: "283-registers"}}
	exempt := titleSet(registerTableTitles...)

	rec := reconcileIndexRows(scanMarkdownHeadings(doc), rows, exempt)
	if len(rec.uncovered) != 0 || len(rec.staleExempt) != 0 {
		t.Errorf("the landed register-table headings were reported: %+v", rec)
	}

	// A heading added under §28.3 and left out of both the index and the
	// list is reported, rather than exempted for stating no number.
	added := doc + "\n### Sense register\n"
	rec = reconcileIndexRows(scanMarkdownHeadings(added), rows, exempt)
	if len(rec.uncovered) != 1 || rec.uncovered[0] != "Sense register" {
		t.Errorf("added register heading not reported as missing an index row: %v", rec.uncovered)
	}

	// Renaming a listed heading without updating the index reports it too.
	renamed := strings.Replace(doc, "### Naming table\n", "### Naming tables\n", 1)
	rec = reconcileIndexRows(scanMarkdownHeadings(renamed), rows, exempt)
	if len(rec.uncovered) != 1 || rec.uncovered[0] != "Naming tables" {
		t.Errorf("renamed register heading not reported: %v", rec.uncovered)
	}

	// A listed title the index does carry a row for is reported as a stale
	// exemption, so the list is closed in both directions.
	withRow := append(rows, specIndexRow{title: "Naming table", anchor: "naming-table"})
	rec = reconcileIndexRows(scanMarkdownHeadings(doc), withRow, exempt)
	if len(rec.staleExempt) != 1 || rec.staleExempt[0] != "Naming table" {
		t.Errorf("exempt heading carrying an index row not reported: %v", rec.staleExempt)
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
	rec := reconcileIndexRows(scanMarkdownHeadings(doc), rows, nil)
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
	rec = reconcileIndexRows(scanMarkdownHeadings(doc), rows, nil)
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
	rec := reconcileIndexRows(scanMarkdownHeadings(doc), rows, nil)
	if len(rec.dangling) != 0 || len(rec.uncovered) != 0 {
		t.Errorf("underscore dropped from the derived anchor: %+v", rec)
	}
}

// assertSection28IndexReconciles asserts both directions over the
// tracked tree: every anchored index row for the section resolves to a
// heading, and every heading in the section carries an index row except
// the register-table headings of §28.3, which are held to that exact
// list.
//
// spec: §28
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

	rows, sectionTitle := specIndexRowsFor(string(index), channelsSpecFile)
	if len(rows) == 0 {
		t.Fatalf("spec/README.md carries no anchored index row for %s", channelsSpecFile)
	}

	headings := scanMarkdownHeadings(string(section))
	if len(headings) == 0 {
		t.Fatalf("%s carries no heading", channelsSpecFile)
	}

	rec := reconcileIndexRows(headings, rows, titleSet(registerTableTitles...))
	for _, anchor := range rec.dangling {
		t.Errorf("index row anchor #%s resolves to no heading in %s (headings: %v)", anchor, channelsSpecFile, headings)
	}
	for _, title := range rec.uncovered {
		t.Errorf("heading %q in %s carries no index row in spec/README.md", title, channelsSpecFile)
	}
	for _, title := range rec.staleExempt {
		t.Errorf("heading %q in %s carries an index row while it is listed as taking none", title, channelsSpecFile)
	}
	for _, msg := range rec.ambiguous {
		t.Errorf("%s and spec/README.md carry an ambiguous anchor: %s", channelsSpecFile, msg)
	}
	for _, msg := range titleMismatches(headings, rows) {
		t.Error(msg)
	}
	for _, msg := range sectionRowMismatches(headings, sectionTitle) {
		t.Errorf("%s and spec/README.md disagree: %s", channelsSpecFile, msg)
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
	headings := []markdownHeading{{level: 2, text: "28.9 Registers (v2): keys"}}
	rows := []specIndexRow{{title: "28.9 Registers (v2): keys", anchor: "289-registers-v2-keys"}}
	collapsed := []specIndexRow{{title: "28.9 Registers (v2): keys", anchor: "28-9-registers-v2-keys"}}

	if rec := reconcileIndexRows(headings, rows, nil); len(rec.dangling) != 0 || len(rec.uncovered) != 0 {
		t.Errorf("correct anchor reported broken: %+v", rec)
	}
	rec := reconcileIndexRows(headings, collapsed, nil)
	if len(rec.dangling) != 1 || rec.dangling[0] != "28-9-registers-v2-keys" {
		t.Errorf("collapsed anchor not reported dangling: %v", rec.dangling)
	}
	if len(rec.uncovered) != 1 || rec.uncovered[0] != "28.9 Registers (v2): keys" {
		t.Errorf("heading left uncovered by the collapsed anchor not reported: %v", rec.uncovered)
	}
}
