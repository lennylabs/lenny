// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// resolveChangedPlan resolves the working-tree diff with the default
// selection options. The pr-fast group resolves through it.
func resolveChangedPlan() []tierPlan {
	plan, _ := resolveChangedPlanFor("", selectOpts{rdepsDepth: defaultRdepsDepth})
	return plan
}

// resolveChangedPlanFor resolves the diff against the given git ref (see
// changedPathsFor) through the import graph, the specification edits, and
// the non-Go input list (see selectChanged); the static tier is always
// included. The second return value carries the operator-facing notes the
// selection raised.
func resolveChangedPlanFor(ref string, opts selectOpts) ([]tierPlan, []string) {
	ctx := context.Background()
	root := repoRoot()
	paths, err := changedPathsFor(ref)
	if err != nil {
		// A diff that cannot be read selects nothing it can name, so the
		// selection fails safe to every tier the merge gate runs rather
		// than to the static tier alone.
		sel := newChangeSelection()
		sel.addFailSafeTiers()
		return sel.plan(), []string{fmt.Sprintf("the diff is unreadable (%v); the unit, component, contract, and integration tiers run whole", err)}
	}
	if len(paths) == 0 {
		return nil, nil
	}
	var notes []string
	inputs, err := readNonGoInputs(root)
	if err != nil {
		notes = append(notes, fmt.Sprintf("the non-Go input list is unavailable (%v); a changed input no Go package owns selects the static tier only", err))
	}
	inputs = inputs.union(baseNonGoInputs(ctx, root, ref))
	g, err := loadPkgGraph(ctx, root)
	if err != nil {
		g = nil
		notes = append(notes, fmt.Sprintf("package graph unavailable (%v); the unit, component, contract, and integration tiers run whole", err))
	}
	sel := selectChanged(paths, g, inputs, selectSpecEdits(ctx, root, ref, paths), opts)
	if g == nil {
		// Without the package graph the selector cannot place a changed
		// Go file or find its dependents, so it fails safe to every tier
		// the merge gate runs rather than to what the other sources select.
		sel.addFailSafeTiers()
	}
	return sel.plan(), append(notes, sel.notes(opts)...)
}

// baseNonGoInputs reads the non-Go input list at the diff's base commit. A
// branch is selected by the union of its own rows and the base's, so a
// branch that deletes or narrows a row still runs what the row named
// before. It returns nil when the base has no list or the base cannot be
// resolved; the branch's own rows still apply.
func baseNonGoInputs(ctx context.Context, root, ref string) *nonGoInputs {
	sides, err := resolveDiffSides(ctx, root, ref)
	if err != nil {
		return nil
	}
	data := readAt(ctx, root, sides.base, nonGoInputsFile)
	if data == "" {
		return nil
	}
	n, err := parseNonGoInputs([]byte(data))
	if err != nil {
		return nil
	}
	return n
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

// resolvePkgsPlan resolves `--pkg` through the import graph (see
// selectPackages). The notes name a package the graph does not hold.
func resolvePkgsPlan(pkgs []string, opts selectOpts) ([]tierPlan, []string) {
	g, err := loadPkgGraph(context.Background(), repoRoot())
	if err != nil {
		return nil, []string{fmt.Sprintf("--pkg: package graph unavailable (%v)", err)}
	}
	sel, unknown := selectPackages(pkgs, g, opts)
	var notes []string
	if len(unknown) > 0 {
		notes = append(notes, fmt.Sprintf("--pkg %s: no Go package at or under the named path", strings.Join(unknown, ", ")))
	}
	return sel.plan(), notes
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

// changedPathsFor returns the changed paths between the revisions a
// `--since` value names (see resolveDiffSides): a plain ref diffs from its
// merge base with HEAD, and `A..B` or `A...B` spells the range. When ref
// is empty, it reads the working-tree diff (staged, unstaged, and
// untracked). Rename detection is off, so a moved file lists both its old
// and its new path and the package that lost the file is selected too. A
// git failure is returned rather than read as an empty diff.
func changedPathsFor(ref string) ([]string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := []string{}
	var sets [][]string
	if ref == "" {
		sets = [][]string{
			{"diff", "--name-only", "--no-renames"},
			{"diff", "--name-only", "--no-renames", "--staged"},
			{"ls-files", "--others", "--exclude-standard"},
		}
	} else {
		sides, err := resolveDiffSides(context.Background(), repoRoot(), ref)
		if err != nil {
			return nil, err
		}
		sets = [][]string{
			{"diff", "--name-only", "--no-renames", sides.base, sides.head},
		}
	}
	for _, args := range sets {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoRoot()
		body, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !seen[line] {
				seen[line] = true
				out = append(out, line)
			}
		}
	}
	return out, nil
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
// tests each tier listed. A tier's package scope and the test functions
// it runs by name are the union of the contributors'; an unscoped
// contributor (nil pkgs, such as --spec when the spec map lists a unit
// test for a query, or a tier a selection runs whole) makes the merged
// tier unscoped, and --all makes the unit tier unscoped, so a merge never
// narrows what any one selector asked to run.
func mergeDiscoveryPlans(plans [][]tierPlan, all bool) []tierPlan {
	tiers := map[string]bool{}
	tests := map[string][]string{}
	patterns := map[string]map[string]bool{}
	funcs := map[string]map[string]map[string]bool{}
	unscoped := map[string]bool{tierUnit: all}
	for _, plan := range plans {
		for _, t := range plan {
			tiers[t.name] = true
			tests[t.name] = append(tests[t.name], t.tests...)
			if t.pkgs == nil {
				unscoped[t.name] = true
				continue
			}
			if patterns[t.name] == nil {
				patterns[t.name] = map[string]bool{}
				funcs[t.name] = map[string]map[string]bool{}
			}
			for _, p := range t.pkgs {
				patterns[t.name][p] = true
			}
			for dir, names := range t.funcs {
				if funcs[t.name][dir] == nil {
					funcs[t.name][dir] = map[string]bool{}
				}
				for _, n := range names {
					funcs[t.name][dir][n] = true
				}
			}
		}
	}
	merged := planFromTierSet(tiers)
	for i := range merged {
		name := merged[i].name
		merged[i].tests = tests[name]
		if unscoped[name] || patterns[name] == nil {
			continue
		}
		merged[i].pkgs = collapsePatterns(patterns[name])
		for dir, names := range funcs[name] {
			if patterns[name][dir] {
				continue
			}
			if merged[i].funcs == nil {
				merged[i].funcs = map[string][]string{}
			}
			merged[i].funcs[dir] = sortedKeys(names)
		}
	}
	return merged
}
