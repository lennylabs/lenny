// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/annotation"
	"github.com/lennylabs/lenny/scripts/specshift/ref"
)

// The coverage view of `lenny-test coverage --spec` (change 0116, CS-2):
// what the annotation index says about each requirement, operation,
// definition block, page, and numbered section. It is computed from the
// tree on each run and never committed. The harness is contributor
// tooling, so this file carries no specification annotation.

// testRef is one test in the view.
type testRef struct {
	Test string `json:"test"`
	Tier string `json:"tier"`
	// Section is the section citation through which an indirect test
	// reaches an identifier.
	Section string `json:"section,omitempty"`
}

// itemCoverage is the view of one requirement, operation, or definition
// block.
type itemCoverage struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Page     string    `json:"page"`
	File     string    `json:"file"`
	Tests    []testRef `json:"tests"`
	Indirect []testRef `json:"indirect_tests"`
	Code     []string  `json:"code"`
	Issues   []string  `json:"issues"`
	Covered  bool      `json:"covered"`
}

// pageCoverage is the view of one page: the annotations that cite its
// code, and its items.
type pageCoverage struct {
	Code   string    `json:"code"`
	Path   string    `json:"path"`
	Status string    `json:"status,omitempty"`
	Area   string    `json:"area,omitempty"`
	Sites  []string  `json:"sites"`
	Tests  []testRef `json:"tests"`
	Items  []string  `json:"identifiers"`
}

// sectionCoverage is the view of one numbered heading.
type sectionCoverage struct {
	Number     string    `json:"number"`
	Title      string    `json:"title"`
	File       string    `json:"file"`
	Stub       string    `json:"stub,omitempty"`
	Tests      []testRef `json:"tests"`
	Successors []string  `json:"successor_identifiers,omitempty"`
	Covered    bool      `json:"covered"`
}

// unresolvedCitation is a cited section number that names no heading, or
// a cited identifier the registry and the issues do not carry, with the
// annotations that cite it.
type unresolvedCitation struct {
	Cited string   `json:"cited"`
	Sites []string `json:"sites"`
}

// unannotatedCount counts the test functions with no annotation.
type unannotatedCount struct {
	Total     int            `json:"total"`
	Tests     int            `json:"tests"`
	ByTier    map[string]int `json:"by_tier"`
	ByPackage map[string]int `json:"by_package"`
}

// coverageReport is the whole view, or the part of it the arguments
// select.
type coverageReport struct {
	Items                 []itemCoverage       `json:"identifiers"`
	Pages                 []pageCoverage       `json:"pages"`
	Sections              []sectionCoverage    `json:"sections"`
	UnresolvedSections    []unresolvedCitation `json:"unresolved_sections"`
	UnresolvedIdentifiers []unresolvedCitation `json:"unresolved_identifiers"`
	Unannotated           *unannotatedCount    `json:"unannotated,omitempty"`
	Rows                  int                  `json:"rows"`
	Covered               int                  `json:"covered"`
	Rate                  float64              `json:"coverage_rate"`
	Threshold             float64              `json:"threshold"`
	Notes                 []string             `json:"notes,omitempty"`
	detailed              bool
}

// specCoverageOptions are the arguments of `coverage --spec`.
type specCoverageOptions struct {
	queries   []string
	area      string
	threshold float64
	jsonOut   bool
}

// reportSpecCoverage prints the coverage view of the tree at the
// repository root and applies the threshold to its rate.
func reportSpecCoverage(opts specCoverageOptions) int {
	m, err := loadSpecModel(context.Background(), repoRoot())
	if err != nil {
		fmt.Fprintf(os.Stderr, "coverage --spec: %v\n", err)
		return 1
	}
	return writeSpecCoverage(os.Stdout, os.Stderr, m, opts)
}

// writeSpecCoverage renders the view of a model and returns the exit
// code: 2 for a query the model does not know, 1 for a rate below the
// threshold, and 0 otherwise.
func writeSpecCoverage(stdout, stderr io.Writer, m *specModel, opts specCoverageOptions) int {
	rep, err := m.coverage(opts)
	if err != nil {
		fmt.Fprintf(stderr, "coverage --spec: %v\n", err)
		return 2
	}
	if opts.jsonOut {
		out, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Fprintln(stdout, string(out))
	} else {
		rep.writeText(stdout)
	}
	if opts.threshold > 0 && rep.Rate < opts.threshold {
		fmt.Fprintf(stderr, "\ncoverage --spec: rate %.1f%% below threshold %.1f%%\n", rep.Rate*100, opts.threshold*100)
		return 1
	}
	return 0
}

// coverage builds the report the options select. With no query and no
// area it covers every page, identifier, and numbered heading, and adds
// the unresolved citations and the unannotated tests.
func (m *specModel) coverage(opts specCoverageOptions) (*coverageReport, error) {
	rep := &coverageReport{Threshold: opts.threshold, detailed: len(opts.queries) > 0}
	if m.resolver == nil {
		rep.Notes = append(rep.Notes, "the tree carries no "+annotation.RegistryPath+", so no identifier is registered and no cited identifier is checked")
	}
	for _, u := range m.idx.Unread {
		rep.Notes = append(rep.Notes, "the annotation index left out "+u)
	}
	switch {
	case len(opts.queries) > 0:
		if err := m.addQueries(rep, opts.queries); err != nil {
			return nil, err
		}
	case opts.area != "":
		if err := m.addArea(rep, opts.area); err != nil {
			return nil, err
		}
	default:
		for _, p := range m.pages {
			m.addPage(rep, p, false)
		}
		for i := range m.items {
			rep.Items = append(rep.Items, m.itemRow(&m.items[i]))
		}
		for i := range m.headings {
			if m.headings[i].Stub != ref.FullStub {
				rep.Sections = append(rep.Sections, m.sectionRow(&m.headings[i]))
			}
		}
		rep.UnresolvedSections, rep.UnresolvedIdentifiers = m.unresolved()
		rep.Unannotated = m.unannotated()
	}
	rep.tally()
	return rep, nil
}

// addQueries adds the rows each query names: an identifier, a page code
// with its items, or a numbered section with the identifiers its stub
// lists.
func (m *specModel) addQueries(rep *coverageReport, queries []string) error {
	for _, q := range queries {
		q = strings.TrimSpace(q)
		if n, ok := sectionQuery(q); ok {
			h, ok := m.heading[n]
			if !ok {
				return fmt.Errorf("§%s names no numbered heading", n)
			}
			rep.Sections = append(rep.Sections, m.sectionRow(h))
			for _, it := range m.stubIdentifiers(h) {
				rep.Items = append(rep.Items, m.itemRow(it))
			}
			continue
		}
		if it := m.item(q); it != nil {
			rep.Items = append(rep.Items, m.itemRow(it))
			continue
		}
		if page, ok := m.pageOf[q]; ok {
			m.addPage(rep, page, true)
			continue
		}
		return fmt.Errorf("%s is not a registered identifier, page code, or numbered section", q)
	}
	return nil
}

// addArea adds the pages of an area, named by its folder name or by the
// code of one of its pages, with their items.
func (m *specModel) addArea(rep *coverageReport, area string) error {
	if page, ok := m.pageOf[area]; ok && page.Area != "" {
		area = page.Area
	}
	found := false
	for _, p := range m.pages {
		if p.Area == area {
			found = true
			m.addPage(rep, p, true)
		}
	}
	if !found {
		return fmt.Errorf("%s names no area: no page carries it as its area, and no page with that code has one", area)
	}
	rep.Notes = append(rep.Notes, "numbered sections are left out of an area's view; `coverage --spec §N.M` shows one")
	return nil
}

// addPage adds a page row, and with items the rows of the items it holds.
func (m *specModel) addPage(rep *coverageReport, p specPage, items bool) {
	row := pageCoverage{Code: p.Code, Path: p.Path, Status: p.Status, Area: p.Area, Sites: []string{}, Tests: []testRef{}, Items: []string{}}
	for _, s := range m.codeCitingID(p.Code) {
		row.Sites = append(row.Sites, s.String())
	}
	row.Tests = testRefs(m.testsCitingID(p.Code))
	for _, it := range m.pageItems(p.Code) {
		row.Items = append(row.Items, it.ID)
		if items {
			rep.Items = append(rep.Items, m.itemRow(it))
		}
	}
	rep.Pages = append(rep.Pages, row)
}

// itemRow builds the view of one item.
func (m *specModel) itemRow(it *specItem) itemCoverage {
	row := itemCoverage{ID: it.ID, Kind: it.Kind, Page: it.Code, File: it.File, Code: []string{}, Issues: []string{}, Indirect: []testRef{}}
	row.Tests = testRefs(m.testsCitingID(it.ID))
	for _, in := range m.indirectTests(it) {
		row.Indirect = append(row.Indirect, testRef{Test: in.Test.String(), Tier: testTier(in.Test), Section: "§" + in.Section})
	}
	for _, s := range m.codeCitingID(it.ID) {
		row.Code = append(row.Code, s.String())
	}
	for _, is := range m.issuesNaming(it.ID) {
		row.Issues = append(row.Issues, fmt.Sprintf("%s %s (%s)", is.ID, is.Path, is.Status))
	}
	row.Covered = len(row.Tests)+len(row.Indirect) > 0
	return row
}

// sectionRow builds the view of one numbered heading.
func (m *specModel) sectionRow(h *ref.NumberedHeading) sectionCoverage {
	row := sectionCoverage{Number: h.Number, Title: h.Title, File: h.File, Stub: h.Stub}
	row.Tests = testRefs(m.testsCitingSection(h.Number))
	for _, it := range m.stubIdentifiers(h) {
		row.Successors = append(row.Successors, it.ID)
	}
	row.Covered = len(row.Tests) > 0
	return row
}

// testRefs renders tests in tier order, then by path and name.
func testRefs(tests []annotation.TestFunc) []testRef {
	out := []testRef{}
	for _, t := range tests {
		out = append(out, testRef{Test: t.String(), Tier: testTier(t)})
	}
	order := map[string]int{}
	for i, t := range allTiers() {
		order[t] = i
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Tier != out[j].Tier {
			return order[out[i].Tier] < order[out[j].Tier]
		}
		return out[i].Test < out[j].Test
	})
	return out
}

// unresolved returns the cited section numbers that name no numbered
// heading and the cited identifiers that neither the registry nor an
// issue record carries, each with its sites. With no registry, no
// identifier is checked.
func (m *specModel) unresolved() (sections, identifiers []unresolvedCitation) {
	secSites := map[string][]string{}
	idSites := map[string][]string{}
	for _, c := range m.idx.Citations {
		for _, n := range c.Sections {
			if _, ok := m.heading[n]; !ok {
				secSites["§"+n] = append(secSites["§"+n], c.String())
			}
		}
		if m.resolver == nil {
			continue
		}
		for _, id := range c.Identifiers {
			if !m.resolver.Has(id) {
				idSites[id] = append(idSites[id], c.String())
			}
		}
	}
	return citationRows(secSites), citationRows(idSites)
}

// citationRows renders a map of cited values to sites, sorted.
func citationRows(sites map[string][]string) []unresolvedCitation {
	out := []unresolvedCitation{}
	for cited, s := range sites {
		out = append(out, unresolvedCitation{Cited: cited, Sites: s})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Cited < out[j].Cited })
	return out
}

// unannotated counts the test functions with no annotation, per tier and
// per package directory.
func (m *specModel) unannotated() *unannotatedCount {
	u := &unannotatedCount{Tests: len(m.idx.Tests), ByTier: map[string]int{}, ByPackage: map[string]int{}}
	for _, t := range m.idx.Tests {
		if annotated(t) {
			continue
		}
		u.Total++
		u.ByTier[testTier(t)]++
		u.ByPackage[t.Dir]++
	}
	return u
}

// tally counts the rows that have a citing test and computes the rate.
// The rows are the identifiers and the numbered headings in the report;
// a page row is a grouping and is not counted.
func (rep *coverageReport) tally() {
	for _, it := range rep.Items {
		rep.Rows++
		if it.Covered {
			rep.Covered++
		}
	}
	for _, s := range rep.Sections {
		rep.Rows++
		if s.Covered {
			rep.Covered++
		}
	}
	if rep.Rows > 0 {
		rep.Rate = float64(rep.Covered) / float64(rep.Rows)
	}
}
