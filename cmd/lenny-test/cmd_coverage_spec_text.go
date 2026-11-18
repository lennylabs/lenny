// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// The text rendering of the coverage view. Without a query it prints one
// line per page, identifier, and numbered heading, then the unresolved
// citations and the unannotated tests; with a query it lists every test,
// code site, and issue of each selected row. The harness is contributor
// tooling, so this file carries no specification annotation.

// writeText renders the report.
func (rep *coverageReport) writeText(w io.Writer) {
	fmt.Fprintf(w, "spec coverage: %d of %d rows have a citing test (%.1f%%)\n", rep.Covered, rep.Rows, rep.Rate*100)
	if rep.Threshold > 0 {
		fmt.Fprintf(w, "threshold: %.1f%%\n", rep.Threshold*100)
	}
	for _, n := range rep.Notes {
		fmt.Fprintf(w, "note: %s\n", n)
	}
	if rep.detailed {
		rep.writeDetail(w)
		return
	}
	rep.writeSummary(w)
}

// writeSummary prints one line per row.
func (rep *coverageReport) writeSummary(w io.Writer) {
	if len(rep.Pages) > 0 {
		fmt.Fprintf(w, "\nPages (%d):\n", len(rep.Pages))
		for _, p := range rep.Pages {
			fmt.Fprintf(w, "  %-12s %s  identifiers %d, citing code %d, citing tests %d\n", p.Code, p.Path, len(p.Items), len(p.Sites), len(p.Tests))
		}
	}
	if len(rep.Items) > 0 {
		fmt.Fprintf(w, "\nIdentifiers (%d):\n", len(rep.Items))
		for _, it := range rep.Items {
			fmt.Fprintf(w, "  %-24s tests %s, indirect %d, code %d, issues %d\n", it.ID, tierCounts(it.Tests), len(it.Indirect), len(it.Code), len(it.Issues))
		}
	}
	if len(rep.Sections) > 0 {
		fmt.Fprintf(w, "\nNumbered headings that are not full successor stubs (%d):\n", len(rep.Sections))
		for _, s := range rep.Sections {
			fmt.Fprintf(w, "  §%-10s %s: tests %s\n", s.Number, headingText(s), tierCounts(s.Tests))
		}
	}
	writeUnresolved(w, "Cited section numbers that name no heading", rep.UnresolvedSections)
	writeUnresolved(w, "Cited identifiers the registry and the issues do not carry", rep.UnresolvedIdentifiers)
	if u := rep.Unannotated; u != nil {
		fmt.Fprintf(w, "\nTest functions with no annotation: %d of %d\n", u.Total, u.Tests)
		fmt.Fprintf(w, "  by tier: %s\n", countList(u.ByTier, allTiers()))
		fmt.Fprintln(w, "  by package:")
		for _, k := range countKeys(u.ByPackage) {
			fmt.Fprintf(w, "    %s %d\n", k, u.ByPackage[k])
		}
	}
}

// writeDetail prints every test, code site, and issue of each row.
func (rep *coverageReport) writeDetail(w io.Writer) {
	for _, p := range rep.Pages {
		fmt.Fprintf(w, "\n%s (page)\nPath: %s\n", p.Code, p.Path)
		if p.Status != "" || p.Area != "" {
			fmt.Fprintf(w, "Status: %s, area: %s\n", orNone(p.Status), orNone(p.Area))
		}
		writeList(w, "Identifiers", p.Items)
		writeList(w, "Code citing the page code", p.Sites)
		writeTests(w, "Tests citing the page code", p.Tests)
	}
	for _, it := range rep.Items {
		fmt.Fprintf(w, "\n%s (%s)\nPage: %s %s\n", it.ID, it.Kind, it.Page, it.File)
		writeTests(w, "Tests citing it", it.Tests)
		writeTests(w, "Tests reaching it through a successor stub", it.Indirect)
		writeList(w, "Code citing it", it.Code)
		writeList(w, "Open and planned issues naming it", it.Issues)
	}
	for _, s := range rep.Sections {
		fmt.Fprintf(w, "\n§%s %s\nFile: %s\n", s.Number, headingText(s), s.File)
		switch s.Stub {
		case "":
			fmt.Fprintln(w, "Not a successor stub: its content has not migrated.")
		default:
			fmt.Fprintf(w, "A %s successor stub.\n", s.Stub)
			writeList(w, "Identifiers its stub lists", s.Successors)
		}
		writeTests(w, "Tests citing it or a subsection", s.Tests)
	}
}

// writeTests prints a titled list of tests with their tiers.
func writeTests(w io.Writer, title string, tests []testRef) {
	fmt.Fprintf(w, "%s:\n", title)
	if len(tests) == 0 {
		fmt.Fprintln(w, "  none")
		return
	}
	for _, t := range tests {
		if t.Section != "" {
			fmt.Fprintf(w, "  %s: %s (through %s)\n", t.Tier, t.Test, t.Section)
			continue
		}
		fmt.Fprintf(w, "  %s: %s\n", t.Tier, t.Test)
	}
}

// writeList prints a titled list of lines.
func writeList(w io.Writer, title string, lines []string) {
	fmt.Fprintf(w, "%s:\n", title)
	if len(lines) == 0 {
		fmt.Fprintln(w, "  none")
		return
	}
	for _, l := range lines {
		fmt.Fprintf(w, "  %s\n", l)
	}
}

// writeUnresolved prints the unresolved citations with their sites.
func writeUnresolved(w io.Writer, title string, rows []unresolvedCitation) {
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s (%d):\n", title, len(rows))
	for _, r := range rows {
		fmt.Fprintf(w, "  %s (%d site(s))\n", r.Cited, len(r.Sites))
		for _, s := range r.Sites {
			fmt.Fprintf(w, "    %s\n", s)
		}
	}
}

// tierCounts renders a test list as its count and its count per tier,
// or as `none`.
func tierCounts(tests []testRef) string {
	if len(tests) == 0 {
		return "none"
	}
	by := map[string]int{}
	for _, t := range tests {
		by[t.Tier]++
	}
	return fmt.Sprintf("%d (%s)", len(tests), countList(by, allTiers()))
}

// countList renders counts in the given key order, then any other key in
// sorted order.
func countList(counts map[string]int, order []string) string {
	var parts []string
	seen := map[string]bool{}
	for _, k := range order {
		if n, ok := counts[k]; ok {
			parts = append(parts, fmt.Sprintf("%s %d", k, n))
			seen[k] = true
		}
	}
	for _, k := range countKeys(counts) {
		if !seen[k] {
			parts = append(parts, fmt.Sprintf("%s %d", k, counts[k]))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

// countKeys returns the keys of a count map in sorted order.
func countKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// headingText returns a heading's text without its number and the dot
// that may follow it.
func headingText(s sectionCoverage) string {
	t := strings.TrimPrefix(s.Title, s.Number)
	return strings.TrimSpace(strings.TrimPrefix(t, "."))
}

// orNone renders an empty value as `none`.
func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
