// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// resolveChangedPlan walks `git diff --name-only` against the
// working tree with the default selection options. The pr-fast group
// resolves through it.
func resolveChangedPlan() []tierPlan {
	plan, _ := resolveChangedPlanFor("", selectOpts{rdepsDepth: defaultRdepsDepth})
	return plan
}

// resolveChangedPlanFor diffs against the given git ref (see
// changedPathsFor) and maps each changed path through
// tests/change-graph.json to a set of tiers; the static tier is always
// included. The unit tier is scoped to the packages the diff reaches
// (see selectChanged). The second return value carries the
// operator-facing notes the selection raised.
func resolveChangedPlanFor(ref string, opts selectOpts) ([]tierPlan, []string) {
	paths := changedPathsFor(ref)
	if len(paths) == 0 {
		return nil, nil
	}
	globs, err := readChangeGraphGlobs()
	var notes []string
	if err != nil {
		notes = append(notes, fmt.Sprintf("change graph unavailable (%v); every changed path is treated as unmapped", err))
	}
	g, err := loadPkgGraph(context.Background(), repoRoot())
	if err != nil {
		g = nil
		notes = append(notes, fmt.Sprintf("package graph unavailable (%v); the unit tier runs every package", err))
	}
	sel := selectChanged(paths, globs, g, opts)
	if g == nil {
		// Without the package graph the selector cannot place an
		// unmapped path or find importers, so it fails safe to the
		// whole module rather than to the graph's targets alone.
		sel.unitPkgs = nil
	}
	notes = append(notes, sel.notes(opts)...)
	plan := planFromTierSet(sel.tiers)
	for i := range plan {
		if plan[i].name == tierUnit {
			plan[i].pkgs = sel.unitPkgs
		}
	}
	return plan, notes
}

// readChangeGraphGlobs reads the change graph's globs block.
func readChangeGraphGlobs() (map[string]map[string][]string, error) {
	body, err := os.ReadFile(filepath.Join(repoRoot(), changeGraphFile))
	if err != nil {
		return nil, fmt.Errorf("read change graph: %w", err)
	}
	var doc struct {
		Globs map[string]map[string][]string `json:"globs"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse change graph: %w", err)
	}
	return doc.Globs, nil
}

// resolveSpecsPlan resolves identifiers, page codes, and numbered
// sections to the tests the annotation index and the spec map name for
// them (see specModel.selectFor), and plans the tiers of those tests. The
// notes carry a model that could not load; the error names each query
// the tree does not know.
func resolveSpecsPlan(specs []string) ([]tierPlan, []string, error) {
	m, err := loadSpecModel(context.Background(), repoRoot())
	if err != nil {
		return []tierPlan{{name: tierUnit, notes: "no test resolved; running unit for safety"}},
			[]string{fmt.Sprintf("the specification model is unavailable (%v)", err)}, nil
	}
	plan, err := specsPlanIn(m, specs)
	return plan, nil, err
}

// specsPlanIn is resolveSpecsPlan over a loaded model. Each tier that
// holds a selected test runs, and lists its tests. The unit tier runs
// whole when the spec map lists a unit test for a query, or carries the
// query as a key whose entries name no tier, as it did before the index,
// and is otherwise scoped to the packages of the unit tests
// the index resolved. When no test resolves, the unit tier runs whole, so
// the selection still gives feedback. A query that neither the index nor
// the spec map knows fails the selection.
func specsPlanIn(m *specModel, specs []string) ([]tierPlan, error) {
	byTier := map[string][]string{}
	unitPkgs := map[string]bool{}
	unitWhole := false
	var unknown []string
	for _, q := range specs {
		sel, ok := m.selectFor(q)
		if !ok {
			unknown = append(unknown, q)
			continue
		}
		for _, t := range sel.tests {
			tier := testTier(t)
			byTier[tier] = append(byTier[tier], t.String())
			if tier == tierUnit {
				unitPkgs["./"+t.Dir] = true
			}
		}
		mappedTiers := 0
		for _, entry := range sel.mapped {
			tier := inferTierFromPath(entry)
			if tier == "" {
				continue
			}
			mappedTiers++
			byTier[tier] = append(byTier[tier], entry)
			unitWhole = unitWhole || tier == tierUnit
		}
		// A spec-map key whose entries name no tier ran the whole unit
		// tier before the index, so it still does.
		if sel.inMap && mappedTiers == 0 {
			if _, ok := byTier[tierUnit]; !ok {
				byTier[tierUnit] = []string{}
			}
			unitWhole = true
		}
	}
	if len(unknown) > 0 {
		return nil, unknownSpecQueries(unknown)
	}
	if len(byTier) == 0 {
		return []tierPlan{{name: tierUnit, notes: "no test resolved; running unit for safety"}}, nil
	}
	tiers := map[string]bool{}
	for t := range byTier {
		tiers[t] = true
	}
	plan := planFromTierSet(tiers)
	for i := range plan {
		plan[i].tests = dedupeStrings(byTier[plan[i].name])
		if plan[i].name == tierUnit && !unitWhole {
			plan[i].pkgs = collapsePatterns(unitPkgs)
		}
	}
	return plan, nil
}

// unknownSpecQueries is the error for --spec queries that name no
// identifier, page code, numbered section, or spec-map key.
func unknownSpecQueries(queries []string) error {
	return fmt.Errorf("--spec %s: not a registered identifier, page code, numbered section, or spec-map key", strings.Join(queries, ", "))
}

// dedupeStrings drops repeated values and keeps the first order.
func dedupeStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// resolvePkgsPlan resolves package globs through the change graph.
// The unit tier is scoped to each named package, recursively, plus
// the unit targets of every change-graph key the package matches.
func resolvePkgsPlan(pkgs []string) []tierPlan {
	globs, err := readChangeGraphGlobs()
	if err != nil {
		return nil
	}
	return pkgsPlanIn(globs, pkgs)
}

// pkgsPlanIn is resolvePkgsPlan over an already-read change graph.
func pkgsPlanIn(globs map[string]map[string][]string, pkgs []string) []tierPlan {
	tiers := map[string]bool{}
	patterns := map[string]bool{}
	for _, pkg := range pkgs {
		pkg = strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(pkg, "./"), "/..."), "/")
		for key, perTier := range globs {
			if key == pkg || strings.HasPrefix(key, pkg) || strings.HasPrefix(pkg, key) {
				for t := range perTier {
					tiers[t] = true
				}
				for _, target := range perTier[tierUnit] {
					patterns[goTestPattern(target)] = true
				}
			}
		}
		patterns["./"+pkg+"/..."] = true
	}
	if len(tiers) == 0 {
		return nil
	}
	plan := planFromTierSet(tiers)
	for i := range plan {
		if plan[i].name == tierUnit {
			plan[i].pkgs = collapsePatterns(patterns)
		}
	}
	return plan
}

// diffRange returns the git diff range for a --since value. A plain
// ref diffs from its merge base with HEAD (`ref...HEAD`), so commits
// that landed on ref after the branch point do not count as this
// branch's changes; a two-dot diff against an advanced main would
// select every package main touched since. A value that already spells
// a range (`A..B` or `A...B`) passes through verbatim, which selects
// for an arbitrary branch or merge without checking it out.
func diffRange(ref string) string {
	if strings.Contains(ref, "..") {
		return ref
	}
	return ref + "...HEAD"
}

// changedPathsFor returns the changed paths between `ref` and HEAD
// (see diffRange). When ref is empty, falls back to the working-tree
// diff (staged + unstaged + untracked).
func changedPathsFor(ref string) []string {
	if _, err := exec.LookPath("git"); err != nil {
		return nil
	}
	seen := map[string]bool{}
	out := []string{}
	var sets [][]string
	if ref == "" {
		sets = [][]string{
			{"diff", "--name-only"},
			{"diff", "--name-only", "--staged"},
			{"ls-files", "--others", "--exclude-standard"},
		}
	} else {
		sets = [][]string{
			{"diff", "--name-only", diffRange(ref)},
		}
	}
	for _, args := range sets {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoRoot()
		body, err := cmd.Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !seen[line] {
				seen[line] = true
				out = append(out, line)
			}
		}
	}
	return out
}

// tiersForChangedPathIn resolves a changed path against the change
// graph's glob keys, reading each key through changeGraphGlobPrefix.
// That is the same reading the change-graph completeness gate applies
// when it decides a source path is covered, so a key that certifies
// coverage there selects tiers here. Without the shared reading a key
// spelled `pkg/foo/...` would pass the gate while matching no real
// path in this selector, and a change under pkg/foo/ would run no
// tests under `--changed`.
func tiersForChangedPathIn(globs map[string]map[string][]string, path string) []string {
	tiers := []string{}
	for key, perTier := range globs {
		if strings.HasPrefix(path, changeGraphGlobPrefix(key)) {
			for t := range perTier {
				tiers = append(tiers, t)
			}
		}
	}
	return tiers
}

// inferTierFromPath maps a test path back to a tier name: the tier whose
// directory holds it, or the unit tier for a test beside its code.
func inferTierFromPath(path string) string {
	if t := tierOfDir(path); t != "" {
		return t
	}
	if strings.HasPrefix(path, "pkg/") || strings.HasSuffix(path, "_test.go") {
		return tierUnit
	}
	return ""
}

// planFromTierSet emits tierPlans in canonical tier order (static
// first, docs last).
func planFromTierSet(tiers map[string]bool) []tierPlan {
	canonical := allTiers()
	plan := []tierPlan{}
	for _, t := range canonical {
		if tiers[t] {
			plan = append(plan, tierPlan{name: t})
		}
	}
	// Any custom tiers (theoretical future additions) tacked on
	// alphabetically.
	custom := []string{}
	for t := range tiers {
		known := false
		for _, c := range canonical {
			if c == t {
				known = true
				break
			}
		}
		if !known {
			custom = append(custom, t)
		}
	}
	sort.Strings(custom)
	for _, t := range custom {
		plan = append(plan, tierPlan{name: t})
	}
	return plan
}

// mergeDiscoveryPlans unions the plans the discovery selectors
// (--changed, --spec, --pkg) produced, in canonical tier order, with the
// tests each tier listed. The unit tier's package scope is the union of
// the contributors' scopes, and an unscoped contributor (nil pkgs, such
// as --spec when the spec map lists a unit test for a query, or a
// selection with no package scope) or --all makes the merged unit tier
// unscoped, so a merge never narrows what any one selector asked to run.
func mergeDiscoveryPlans(plans [][]tierPlan, all bool) []tierPlan {
	tiers := map[string]bool{}
	tests := map[string][]string{}
	unitPatterns := map[string]bool{}
	unitScoped := !all
	for _, plan := range plans {
		for _, t := range plan {
			tiers[t.name] = true
			tests[t.name] = append(tests[t.name], t.tests...)
			if t.name != tierUnit {
				continue
			}
			if t.pkgs == nil {
				unitScoped = false
			}
			for _, p := range t.pkgs {
				unitPatterns[p] = true
			}
		}
	}
	merged := planFromTierSet(tiers)
	for i := range merged {
		merged[i].tests = tests[merged[i].name]
		if merged[i].name == tierUnit && unitScoped {
			merged[i].pkgs = collapsePatterns(unitPatterns)
		}
	}
	return merged
}
