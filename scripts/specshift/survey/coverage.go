// SPDX-License-Identifier: MIT

package survey

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/scripts/specshift/annotation"
	"github.com/lennylabs/lenny/scripts/specshift/ref"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// RequirementCoverage is one requirement the branch adds or modifies,
// with the tests and issues that satisfy the coverage gate for it.
type RequirementCoverage struct {
	// ID is the requirement's identifier, File the page that holds it
	// after the change, and Change is Added or Modified.
	ID, File, Change string
	// Tests are the test functions whose doc-comment annotation cites the
	// requirement directly, rendered `path::Name`.
	Tests []string
	// Issues are the paths of the open and planned issue records whose
	// `requirements` list names the requirement.
	Issues []string
}

// Covered reports whether a test or an issue satisfies the gate for the
// requirement.
func (c RequirementCoverage) Covered() bool { return len(c.Tests) > 0 || len(c.Issues) > 0 }

// String renders the finding for an uncovered requirement.
func (c RequirementCoverage) String() string {
	return fmt.Sprintf("%s (%s on %s): no test function's doc comment cites it as `// spec: %s`, and no open or planned issue under issues/ lists it under `requirements`",
		c.ID, c.Change, c.File, c.ID)
}

// CoverageReport is what the coverage gate found on a branch.
type CoverageReport struct {
	// Base is the merge-base commit, and BaseRef the integration ref.
	Base, BaseRef string
	// Requirements holds every requirement the branch adds or modifies,
	// in the order the comparison reports them, with what covers it.
	Requirements []RequirementCoverage
	// Unread names each Go file the annotation index could not read, with
	// the reason. A test in such a file cannot satisfy the gate.
	Unread []string
	// Skipped names each issue record the gate did not count, with the
	// reason: a record outside a registered page code's folder, a symbolic
	// link, or front matter that does not parse. A skipped record covers
	// nothing, so a malformed record fails closed for the requirements it
	// names and does not stop a branch whose requirements it does not name.
	Skipped []string
}

// Uncovered returns the requirements no test and no issue covers.
func (r CoverageReport) Uncovered() []RequirementCoverage {
	var out []RequirementCoverage
	for _, c := range r.Requirements {
		if !c.Covered() {
			out = append(out, c)
		}
	}
	return out
}

// CheckCoverage runs the coverage gate of change 0116 over the working
// tree at root against its merge base with the integration branch. For
// each requirement the working tree adds, or whose text it modifies, it
// records the test functions that cite the requirement directly and the
// open or planned issues that list it under `requirements`. A requirement
// that moves to another page with its text unchanged is neither added nor
// modified, and a retired requirement needs nothing. It returns
// annotation.ErrNoBase when the branch has no merge base.
func CheckCoverage(ctx context.Context, root string) (CoverageReport, error) {
	var rep CoverageReport
	base, ref, err := annotation.MergeBase(ctx, root)
	if err != nil {
		return rep, err
	}
	rep.Base, rep.BaseRef = base, ref
	sc, err := CompareSpec(ctx, root, base)
	if err != nil {
		return rep, err
	}
	for _, c := range sc.Changes {
		if it := c.Item(); it.Kind == KindRequirement && textChanged(c) {
			rep.Requirements = append(rep.Requirements, RequirementCoverage{ID: it.ID, File: it.File, Change: c.Change})
		}
	}
	if len(rep.Requirements) == 0 {
		return rep, nil
	}
	r := repo{root: root}
	ids := make([]string, len(rep.Requirements))
	for i, c := range rep.Requirements {
		ids[i] = c.ID
	}
	tests, unread, err := citingTests(ctx, r, ids)
	if err != nil {
		return rep, err
	}
	rep.Unread = unread
	issues, skipped, err := issuesListing(ctx, r, sc.After.Pages)
	if err != nil {
		return rep, err
	}
	rep.Skipped = skipped
	for i := range rep.Requirements {
		key := strings.ToLower(rep.Requirements[i].ID)
		rep.Requirements[i].Tests = tests[key]
		rep.Requirements[i].Issues = issues[key]
	}
	return rep, nil
}

// textChanged reports whether a change adds an item or modifies its text.
// The comparison also reads a move to another page as a modification, and
// the gate leaves such a move out when the text is the same.
func textChanged(c ItemChange) bool {
	switch c.Change {
	case Added:
		return true
	case Modified:
		return c.Old.Text != c.New.Text
	}
	return false
}

// citingTests returns, by lower-cased identifier, the test functions of
// the working tree whose doc-comment annotations cite one of the wanted
// identifiers directly, and the files the index could not read. Only test
// files that mention an identifier are indexed. An annotation on a helper
// or on code in a test file is not a test function's and does not count,
// and neither does a test in a file the go tool never builds.
func citingTests(ctx context.Context, r repo, wanted []string) (map[string][]string, []string, error) {
	files, err := goFiles(ctx, r)
	if err != nil {
		return nil, nil, err
	}
	modules, err := nestedModules(ctx, r)
	if err != nil {
		return nil, nil, err
	}
	var testFiles []string
	for _, p := range files {
		if strings.HasSuffix(p, "_test.go") && !ignoredPath(p, modules) {
			testFiles = append(testFiles, p)
		}
	}
	want := map[string]bool{}
	needles := make([][]byte, 0, len(wanted))
	for _, w := range wanted {
		lower := strings.ToLower(w)
		want[lower] = true
		needles = append(needles, []byte(lower))
	}
	keep := func(_ string, src []byte) bool {
		return containsAny(bytes.ToLower(src), needles) && buildable(src)
	}
	idx := annotation.IndexFiles(testFiles, annotation.CheckedReader(r.root), keep)
	out := map[string][]string{}
	for _, t := range idx.Tests {
		for _, id := range t.Identifiers {
			if key := strings.ToLower(id); want[key] {
				out[key] = appendUnique(out[key], t.String())
			}
		}
	}
	return out, idx.Unread, nil
}

// issuesListing returns, by lower-cased identifier, the open and planned
// issue records of the working tree whose `requirements` list names it,
// and the records it did not count, each with the reason. A record is
// `issues/<CODE>/<file>.md`, where CODE is a page code or an alias the
// specification registers; the README, the generated index, and the
// records under issues/closed/ and under a reserved issue code
// (ref.ReservedCodes) are left out. A record with no status is
// open, as the coverage view of lenny-test reads it. A record that is a
// symbolic link, or whose front matter does not parse, covers nothing and
// is reported, so it fails closed for the requirements it names. An error
// is returned only when the records cannot be listed.
func issuesListing(ctx context.Context, r repo, pages map[string]PageInfo) (map[string][]string, []string, error) {
	files, err := r.worktreeFiles(ctx, issuesDir)
	if err != nil {
		return nil, nil, err
	}
	out := map[string][]string{}
	var skipped []string
	for _, p := range files {
		parts := strings.Split(p, "/")
		if len(parts) != 3 || parts[1] == "closed" || !strings.HasSuffix(parts[2], ".md") {
			continue
		}
		if _, ok := pages[parts[1]]; !ok {
			// A reserved issue code belongs to no page, so its records
			// name no requirement and are left out without a report.
			if ref.IsReservedCode(parts[1]) {
				continue
			}
			skipped = append(skipped, fmt.Sprintf("%s: %s is not a page code the specification registers", p, parts[1]))
			continue
		}
		f, err := readIssue(r.root, p)
		if err != nil {
			skipped = append(skipped, err.Error())
			continue
		}
		// The status is compared as written, as the coverage view of
		// lenny-test compares it, so both read the same records as open.
		if f.Status != "" && f.Status != "open" && f.Status != "planned" {
			continue
		}
		for _, id := range f.Requirements {
			key := strings.ToLower(strings.TrimSpace(id))
			out[key] = appendUnique(out[key], p)
		}
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out, skipped, nil
}

// readIssue reads the front matter of one issue record. The checked read
// refuses a symbolic link and a file whose folder resolves outside the
// repository, so a planted link cannot point the gate at a record outside
// the tree. A record with no front matter reads as empty.
func readIssue(root, p string) (issueFront, error) {
	var f issueFront
	b, err := scope.ReadChecked(root, p)
	if err != nil {
		return f, fmt.Errorf("read %s: %w", p, err)
	}
	if fm, ok := frontMatterText(string(b)); ok {
		if err := yaml.Unmarshal([]byte(fm), &f); err != nil {
			return f, fmt.Errorf("parse the front matter of %s: %w", p, err)
		}
	}
	return f, nil
}

// appendUnique appends a value not already in list.
func appendUnique(list []string, v string) []string {
	for _, have := range list {
		if have == v {
			return list
		}
	}
	return append(list, v)
}
