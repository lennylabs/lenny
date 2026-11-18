// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/scripts/specshift/annotation"
	"github.com/lennylabs/lenny/scripts/specshift/defs"
	"github.com/lennylabs/lenny/scripts/specshift/ref"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// The specification model joins what the coverage view, `list --spec`,
// and `run --spec` read: the annotation index of the tracked Go files,
// the identifier registry, the numbered headings with their successor
// stubs, and the issue records. It is built from the tree on each run and
// never committed (change 0116, CS-1 and CS-2). The harness is
// contributor tooling, so this file carries no specification annotation.

// Kinds of a registered item.
const (
	itemRequirement = "requirement"
	itemOperation   = "operation"
	itemBlock       = "definition block"
)

// specPage is one page of the new specification tree.
type specPage struct {
	Code   string `json:"code"`
	Path   string `json:"path"`
	Status string `json:"status,omitempty"`
	Area   string `json:"area,omitempty"`
}

// specItem is one requirement, operation, or definition block.
type specItem struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Anchor string `json:"anchor"`
	File   string `json:"file"`
	Code   string `json:"code"`
}

// issueRecord is the part of an issue record the coverage view reads.
type issueRecord struct {
	ID           string   `json:"id" yaml:"id"`
	Path         string   `json:"path" yaml:"-"`
	Status       string   `json:"status" yaml:"status"`
	Requirements []string `json:"-" yaml:"requirements"`
}

// specModel is the specification model of one tree.
type specModel struct {
	idx      *annotation.Index
	pages    []specPage          // in code order
	pageOf   map[string]specPage // code or alias -> page
	byPath   map[string]specPage
	items    []specItem // in identifier order
	itemOf   map[string]*specItem
	headings []ref.NumberedHeading
	heading  map[string]*ref.NumberedHeading
	issues   []issueRecord
	// resolver answers whether a cited identifier is live; nil when the
	// tree carries no registry.
	resolver *annotation.Registry
}

// loadSpecModel builds the model of the tracked tree at root.
func loadSpecModel(ctx context.Context, root string) (*specModel, error) {
	return buildSpecModel(ctx, scope.GitLister(root), annotation.CheckedReader(root))
}

// buildSpecModel builds the model of the tree a lister and a reader
// present.
func buildSpecModel(ctx context.Context, list scope.Lister, read scope.FileReader) (*specModel, error) {
	all, err := list(ctx)
	if err != nil {
		return nil, fmt.Errorf("list the tracked tree: %w", err)
	}
	idx, err := annotation.BuildIndex(ctx, func(context.Context) ([]string, error) { return all, nil }, read)
	if err != nil {
		return nil, fmt.Errorf("build the annotation index: %w", err)
	}
	m := &specModel{idx: idx, pageOf: map[string]specPage{}, byPath: map[string]specPage{}, itemOf: map[string]*specItem{}, heading: map[string]*ref.NumberedHeading{}}
	if err := m.loadRegistry(read); err != nil {
		return nil, err
	}
	if m.resolver != nil {
		m.resolver.AddIssuePaths(all)
	}
	sort.Strings(all)
	for _, p := range all {
		switch {
		case ref.IsNumberedFile(p):
			if err := m.loadNumbered(read, p); err != nil {
				return nil, err
			}
		case isIssueRecord(p):
			if err := m.loadIssue(read, p); err != nil {
				return nil, err
			}
		}
	}
	m.indexHeadings()
	return m, nil
}

// loadRegistry reads spec/registry.json: the pages with their areas, and
// the requirements, operations, and definition blocks. A tree with no
// registry has no pages and no items, and no identifier resolves.
func (m *specModel) loadRegistry(read scope.FileReader) error {
	data, err := read(annotation.RegistryPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", annotation.RegistryPath, err)
	}
	var reg ref.Registry
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&reg); err != nil {
		return fmt.Errorf("parse %s: %w", annotation.RegistryPath, err)
	}
	if m.resolver, err = annotation.LoadRegistry(read, annotation.RegistryPath); err != nil {
		return err
	}
	for _, p := range reg.Pages {
		page := specPage{Code: p.Code, Path: p.Path, Status: p.Status}
		if content, err := read(p.Path); err == nil {
			if parsed, _ := defs.ParsePage(p.Path, string(content)); parsed != nil {
				page.Area = parsed.Front.Area
			}
		}
		m.pages = append(m.pages, page)
		m.byPath[p.Path] = page
		m.pageOf[p.Code] = page
		for _, a := range p.Aliases {
			m.pageOf[a] = page
		}
	}
	sort.Slice(m.pages, func(i, j int) bool { return m.pages[i].Code < m.pages[j].Code })
	add := func(id, kind, anchor, file string) {
		m.items = append(m.items, specItem{ID: id, Kind: kind, Anchor: anchor, File: file, Code: m.byPath[file].Code})
	}
	for _, r := range reg.Requirements {
		add(r.ID, itemRequirement, r.Anchor, r.File)
	}
	for _, o := range reg.Operations {
		add(o.ID, itemOperation, o.Anchor, o.File)
	}
	for _, b := range reg.Blocks {
		add(b.Anchor, itemBlock, b.Anchor, b.File)
	}
	sort.Slice(m.items, func(i, j int) bool { return m.items[i].ID < m.items[j].ID })
	for i := range m.items {
		it := &m.items[i]
		m.itemOf[strings.ToLower(it.ID)] = it
		m.itemOf[strings.ToLower(it.Anchor)] = it
	}
	return nil
}

// loadNumbered reads the numbered headings of one numbered file.
func (m *specModel) loadNumbered(read scope.FileReader, p string) error {
	content, err := read(p)
	if err != nil {
		return fmt.Errorf("read %s: %w", p, err)
	}
	m.headings = append(m.headings, ref.NumberedHeadings(p, string(content))...)
	return nil
}

// indexHeadings maps each section number to its heading. The first
// heading that carries a number wins, as a citation of the number reads
// it.
func (m *specModel) indexHeadings() {
	for i := range m.headings {
		if _, dup := m.heading[m.headings[i].Number]; !dup {
			m.heading[m.headings[i].Number] = &m.headings[i]
		}
	}
}

// loadIssue reads one issue record's front matter. A record with no
// front matter is read by its file name.
func (m *specModel) loadIssue(read scope.FileReader, p string) error {
	content, err := read(p)
	if err != nil {
		return fmt.Errorf("read %s: %w", p, err)
	}
	var rec issueRecord
	if front, ok := issueFrontMatter(string(content)); ok {
		if err := yaml.Unmarshal([]byte(front), &rec); err != nil {
			return fmt.Errorf("parse the front matter of %s: %w", p, err)
		}
	}
	if rec.ID == "" {
		rec.ID = strings.TrimSuffix(path.Base(p), ".md")
	}
	if rec.Status == "" {
		rec.Status = "open"
	}
	rec.Path = p
	if rec.Status == "open" || rec.Status == "planned" {
		m.issues = append(m.issues, rec)
	}
	return nil
}

// isIssueRecord reports whether a path is an issue record that is not
// closed: `issues/<CODE>/<file>.md`. The README, the generated index,
// and the records under `issues/closed/` are left out.
func isIssueRecord(p string) bool {
	parts := strings.Split(p, "/")
	return len(parts) == 3 && parts[0] == "issues" && parts[1] != "closed" && strings.HasSuffix(parts[2], ".md")
}

// issueFrontMatter returns the YAML between the opening `---` line and
// the next one.
func issueFrontMatter(content string) (string, bool) {
	rest, ok := strings.CutPrefix(content, "---\n")
	if !ok {
		return "", false
	}
	front, _, ok := strings.Cut(rest, "\n---")
	return front, ok
}

// item returns the registered item an identifier or anchor names.
func (m *specModel) item(id string) *specItem {
	return m.itemOf[strings.ToLower(id)]
}

// pageItems returns the items a page holds.
func (m *specModel) pageItems(code string) []*specItem {
	page, ok := m.pageOf[code]
	if !ok {
		return nil
	}
	var out []*specItem
	for i := range m.items {
		if m.items[i].File == page.Path {
			out = append(out, &m.items[i])
		}
	}
	return out
}

// citesID reports whether a list of cited identifiers names id.
func citesID(ids []string, id string) bool {
	for _, c := range ids {
		if strings.EqualFold(c, id) {
			return true
		}
	}
	return false
}

// withinSection reports whether a cited section number is the section or
// one of its subsections.
func withinSection(cited, section string) bool {
	return cited == section || strings.HasPrefix(cited, section+".")
}

// testsCitingID returns the tests whose annotation cites an identifier.
func (m *specModel) testsCitingID(id string) []annotation.TestFunc {
	var out []annotation.TestFunc
	for _, t := range m.idx.Tests {
		if citesID(t.Identifiers, id) {
			out = append(out, t)
		}
	}
	return out
}

// testsCitingSectionExactly returns the tests whose annotation cites a
// section itself, leaving out those that cite only a subsection.
func (m *specModel) testsCitingSectionExactly(section string) []annotation.TestFunc {
	var out []annotation.TestFunc
	for _, t := range m.idx.Tests {
		for _, s := range t.Sections {
			if s == section {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

// testsCitingSection returns the tests whose annotation cites a section
// or one of its subsections.
func (m *specModel) testsCitingSection(section string) []annotation.TestFunc {
	var out []annotation.TestFunc
	for _, t := range m.idx.Tests {
		for _, s := range t.Sections {
			if withinSection(s, section) {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

// codeCitingID returns the annotations outside test files that cite an
// identifier: the deciding code and the package doc comments.
func (m *specModel) codeCitingID(id string) []annotation.Site {
	var out []annotation.Site
	for _, s := range m.idx.Sites(id) {
		if s.Kind != annotation.SiteTest {
			out = append(out, s)
		}
	}
	return out
}

// issuesNaming returns the open and planned issues whose `requirements`
// list names an identifier.
func (m *specModel) issuesNaming(id string) []issueRecord {
	var out []issueRecord
	for _, is := range m.issues {
		if citesID(is.Requirements, id) {
			out = append(out, is)
		}
	}
	return out
}

// stubsListing returns the numbered headings whose successor stub lists
// an item: a successor link to the item's page with the item's anchor as
// its fragment.
func (m *specModel) stubsListing(it *specItem) []*ref.NumberedHeading {
	var out []*ref.NumberedHeading
	for i := range m.headings {
		h := &m.headings[i]
		for _, s := range h.Successors {
			if s.Path == it.File && strings.EqualFold(s.Fragment, it.Anchor) {
				out = append(out, h)
				break
			}
		}
	}
	return out
}

// stubIdentifiers returns the registered items a heading's successor stub
// lists.
func (m *specModel) stubIdentifiers(h *ref.NumberedHeading) []*specItem {
	var out []*specItem
	for _, s := range h.Successors {
		if s.Fragment == "" {
			continue
		}
		if it := m.item(s.Fragment); it != nil && it.File == s.Path {
			out = append(out, it)
		}
	}
	return out
}

// indirectTest is a test that reaches an identifier through a section
// citation of a section whose successor stub lists it.
type indirectTest struct {
	Test    annotation.TestFunc
	Section string
}

// indirectTests returns the tests that reach an item through a stub, with
// the section each one cites. A test reaches the item when it cites the
// stub's own section: a stub lists the successors of its own heading's
// body, and a subsection carries its own stub, so a test that cites only
// a subsection reaches that subsection's successors and not these. A
// test that also cites the item directly is listed once, as direct.
func (m *specModel) indirectTests(it *specItem) []indirectTest {
	var out []indirectTest
	seen := map[string]bool{}
	for _, h := range m.stubsListing(it) {
		for _, t := range m.testsCitingSectionExactly(h.Number) {
			if citesID(t.Identifiers, it.ID) || seen[t.String()] {
				continue
			}
			seen[t.String()] = true
			out = append(out, indirectTest{Test: t, Section: h.Number})
		}
	}
	return out
}

// sectionQuery strips the `§` sign from a section query and reports
// whether the rest is a section number.
func sectionQuery(q string) (string, bool) {
	n := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(q), "§"))
	if n == "" {
		return "", false
	}
	for _, part := range strings.Split(n, ".") {
		if part == "" || strings.Trim(part, "0123456789") != "" {
			return "", false
		}
	}
	return n, true
}

// testsFor resolves one query of `list --spec` or `run --spec` to the
// tests that verify it: for an identifier, the tests that cite it
// directly or through a stub; for a page code, those of every item on
// the page and the tests that cite the code; for a numbered section, the
// tests that cite it or a subsection, and those of the identifiers its
// stub lists. It reports false for a query that names none of these.
func (m *specModel) testsFor(q string) ([]annotation.TestFunc, bool) {
	q = strings.TrimSpace(q)
	var out []annotation.TestFunc
	addItem := func(it *specItem) {
		out = append(out, m.testsCitingID(it.ID)...)
		for _, in := range m.indirectTests(it) {
			out = append(out, in.Test)
		}
	}
	if n, ok := sectionQuery(q); ok {
		h, ok := m.heading[n]
		if !ok {
			return nil, false
		}
		out = append(out, m.testsCitingSection(n)...)
		for _, it := range m.stubIdentifiers(h) {
			addItem(it)
		}
		return dedupeTests(out), true
	}
	if it := m.item(q); it != nil {
		addItem(it)
		return dedupeTests(out), true
	}
	if page, ok := m.pageOf[q]; ok {
		out = append(out, m.testsCitingID(q)...)
		for _, it := range m.pageItems(page.Code) {
			addItem(it)
		}
		return dedupeTests(out), true
	}
	return nil, false
}

// queryNames returns every query the model resolves: the page codes, the
// identifiers, and the numbered sections as `§N.M`.
func (m *specModel) queryNames() []string {
	out := []string{}
	for _, p := range m.pages {
		out = append(out, p.Code)
	}
	for _, it := range m.items {
		out = append(out, it.ID)
	}
	for _, h := range m.headings {
		if m.heading[h.Number].Line == h.Line && m.heading[h.Number].File == h.File {
			out = append(out, "§"+h.Number)
		}
	}
	return out
}

// dedupeTests drops repeated tests and sorts the rest by path and name.
func dedupeTests(tests []annotation.TestFunc) []annotation.TestFunc {
	seen := map[string]bool{}
	var out []annotation.TestFunc
	for _, t := range tests {
		if !seen[t.String()] {
			seen[t.String()] = true
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// annotated reports whether a test's doc comment carries an annotation
// that cites anything.
func annotated(t annotation.TestFunc) bool {
	return len(t.Identifiers) > 0 || len(t.Sections) > 0
}

// testTier returns the tier of a test: the tier whose directory holds its
// package, or the unit tier for a test beside its code.
func testTier(t annotation.TestFunc) string {
	if tier := tierOfDir(t.Dir); tier != "" {
		return tier
	}
	return tierUnit
}
