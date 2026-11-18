// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
)

// runRun handles the default `lenny-test run` subcommand.
//
// Phase 0 behaviour:
//   - Parses flags and resolves the selector into a list of tiers and tests.
//   - In --dry-run mode, prints the resolved selector and the tests that would
//     run. Exits 0.
//   - Otherwise, dispatches the static and unit tiers to `go test` and writes
//     a minimal verdict. Higher tiers print a "not yet implemented" notice
//     and are recorded as skipped in the verdict.
//
// Phase 1+ extends this with real executors for each tier.
func runRun(args []string) int {
	// ContinueOnError so a removed flag such as --max-tier returns the
	// usage exit code to the caller rather than exiting the process.
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	groupFlag := fs.String("group", "", "named group from tests/groups.yaml")
	tierFlag := fs.String("tier", "", "comma-separated tiers; alone, runs each named tier whole; with --changed, --since, --spec, or --pkg, the selection restricted to these tiers")
	gateFlag := fs.String("gate", "", "merge | area | release: bound the selection at the tiers of that gate; alone, runs the gate's tiers whole")
	changedFlag := fs.Bool("changed", false, "run tests affected by uncommitted changes (working tree diff)")
	sinceFlag := fs.String("since", "", "diff from the merge base of the given git ref and HEAD (e.g. main, HEAD~5), or an explicit range (A..B, A...B), instead of the working tree")
	allFlag := fs.Bool("all", false, "with --changed or --pkg, run the unit tier over every package (./...) instead of the packages the selection reaches")
	rdepsFlag := fs.Int("rdeps-depth", defaultRdepsDepth, "with --changed or --pkg, how many hops of in-module dependents of a changed package the selection adds (0: changed packages only; -1, the default: the full closure)")
	specFlag := fs.String("spec", "", "comma-separated identifiers, page codes, or numbered sections (§N.M); runs the tests whose // spec: annotations cite them")
	pkgFlag := fs.String("pkg", "", "comma-separated package directories; runs the tests of the packages at or under them and of their dependents, through the import graph")
	subsetFlag := fs.String("subset", "", "comma-separated subset names of one tier (requires exactly one --tier)")
	dryRunFlag := fs.Bool("dry-run", false, "resolve the selector and print what would run; do not execute")
	continueFlag := fs.Bool("continue-on-failure", false, "do not stop at the first failing tier")
	outputFlag := fs.String("output", "human", "json | junit | github-annotations | human | tap")
	verdictFile := fs.String("verdict-file", latestVerdictFile, "path to write the JSON verdict")
	cachedFlag := fs.Bool("cached", false, "use the cached container daemon if available")
	noInfraFlag := fs.Bool("no-infra", false, "skip infrastructure provisioning")
	updateGoldenFlag := fs.Bool("update-golden", false, "rewrite golden files from test output (sets GOLDEN_UPDATE=1)")
	updateBaselineFlag := fs.Bool("update-baseline", false, "rewrite load baselines from test output (sets LENNY_UPDATE_BASELINE=1)")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	// Propagate update flags via environment so test binaries pick
	// them up. Side-effecting `os.Setenv` here is fine because the
	// run command is the top-level invocation.
	if *updateGoldenFlag {
		_ = os.Setenv("GOLDEN_UPDATE", "1")
	}
	if *updateBaselineFlag {
		_ = os.Setenv("LENNY_UPDATE_BASELINE", "1")
	}

	tiers, err := parseTierList(*tierFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test: %v\n", err)
		return 2
	}
	sel := selector{
		group:       *groupFlag,
		tiers:       tiers,
		gate:        *gateFlag,
		changed:     *changedFlag || *sinceFlag != "",
		since:       *sinceFlag,
		specs:       splitNonEmpty(*specFlag),
		pkgs:        splitNonEmpty(*pkgFlag),
		subsets:     splitNonEmpty(*subsetFlag),
		dryRun:      *dryRunFlag,
		continueErr: *continueFlag,
		output:      *outputFlag,
		verdictFile: *verdictFile,
		cached:      *cachedFlag,
		noInfra:     *noInfraFlag,
		all:         *allFlag,
		rdepsDepth:  *rdepsFlag,
	}

	if !sel.valid() {
		fmt.Fprintln(os.Stderr, "lenny-test: no selector provided. Use --group, --tier, --gate, --changed, --spec, or --pkg.")
		fmt.Fprintln(os.Stderr, "Run `lenny-test --help` for usage.")
		return 2
	}

	resolved, err := sel.resolve()
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test: %v\n", err)
		return 2
	}

	if sel.dryRun {
		return printDryRun(sel, resolved)
	}

	return execute(sel, resolved)
}

// selector captures the resolved test-selection criteria.
type selector struct {
	group string
	// tiers are the tiers --tier names, in execution order. Alone they
	// run whole; beside a discovery selector they restrict it.
	tiers []string
	// gate is the gate --gate names, whose tiers bound the selection.
	gate        string
	changed     bool
	since       string // git ref to diff against; empty means working tree
	specs       []string
	pkgs        []string
	subsets     []string // explicit subset filter when paired with --tier
	dryRun      bool
	continueErr bool
	output      string
	verdictFile string
	cached      bool
	noInfra     bool
	all         bool // run the unit tier over ./... whatever the selection
	rdepsDepth  int  // importer hops --changed adds; see defaultRdepsDepth
}

func (s selector) valid() bool {
	return s.group != "" || len(s.tiers) > 0 || s.gate != "" || s.changed ||
		len(s.specs) > 0 || len(s.pkgs) > 0
}

// resolvedSelector lists the tiers to execute and the tests within each tier.
type resolvedSelector struct {
	tiers []tierPlan
	// notes carries selection-level warnings (unmapped changed paths, a
	// changed module-wide build file) that dry-run and run both print.
	notes []string
}

type tierPlan struct {
	name    string
	subsets []string
	tests   []string
	notes   string
	// pkgs scopes a package-scoped tier (unit, component, or contract)
	// to these `go test` package patterns, which run whole. Nil runs the
	// tier's full target set; a non-nil value, even an empty one, is a
	// scope.
	pkgs []string
	// funcs maps a `./dir` package pattern to the test functions a
	// specification edit selects there, which a scoped plan runs by name
	// (`go test -run`). It is read only when pkgs is non-nil.
	funcs map[string][]string
}

// resolve expands the selector into a concrete plan.
//
// Per the selection algorithm:
//  1. Resolve every active selector into a candidate plan.
//  2. Bound the plan: by the tiers --tier names, by the tiers of the gate
//     --gate names, or, for a discovery selection with neither, by the
//     tiers that need no infrastructure (change 0116, CS-7).
//  3. Order by tier ascending; group by package within a tier.
//
// Selectors compose by intersection: --changed --gate merge runs the
// changed-only plan within the merge gate's tiers, and --tier unit
// --since main keeps the package scope the diff reaches. A tier --tier
// names that the discovery plan does not select runs nothing and carries
// a note saying so. --tier alone runs each named tier whole, --gate alone
// runs the gate's tiers whole, and --group names a complete plan that
// only --gate narrows.
func (s selector) resolve() (resolvedSelector, error) {
	if err := s.check(); err != nil {
		return resolvedSelector{}, err
	}

	var raw []tierPlan
	var notes []string
	switch {
	case s.group != "":
		raw = tiersForGroup(s.group)
		if len(raw) == 0 {
			return resolvedSelector{}, fmt.Errorf("unknown group %q: run `lenny-test list --groups` to see available groups", s.group)
		}
	case s.hasDiscovery():
		discovered, discoveryNotes, err := s.discoveryPlan()
		if err != nil {
			return resolvedSelector{}, err
		}
		notes = append(notes, discoveryNotes...)
		var boundNotes []string
		raw, boundNotes = s.boundDiscovery(discovered)
		notes = append(notes, boundNotes...)
	case len(s.tiers) > 0:
		raw = wholeTierPlans(s.tiers, s.subsets)
	case s.gate != "":
		tiers, _ := gateTiers(s.gate)
		raw = wholeTierPlans(tiers, nil)
	}

	if s.gate != "" {
		raw = boundByGate(raw, s.gate)
	}
	return resolvedSelector{tiers: raw, notes: notes}, nil
}

// check refuses selector values that cannot resolve: a --since revision
// git would read as an option, an unknown tier or gate, and a --subset
// that does not name exactly one tier.
func (s selector) check() error {
	for _, rev := range strings.Split(strings.ReplaceAll(s.since, "...", ".."), "..") {
		if strings.HasPrefix(rev, "-") {
			// git would read the value as an option.
			return fmt.Errorf("--since %q: a revision does not begin with '-'", s.since)
		}
	}
	known := allTiers()
	for _, t := range s.tiers {
		if !contains(known, t) {
			return fmt.Errorf("unknown tier %q. Valid tiers: %s", t, strings.Join(known, ", "))
		}
	}
	if s.gate != "" {
		if _, ok := gateTiers(s.gate); !ok {
			return fmt.Errorf("unknown --gate %q. Valid gates: %s", s.gate, strings.Join(gateNames(), ", "))
		}
	}
	if len(s.subsets) > 0 && len(s.tiers) != 1 {
		return fmt.Errorf("--subset names subsets of one tier, so it needs exactly one --tier (got %d)", len(s.tiers))
	}
	return nil
}

// boundDiscovery bounds a discovery plan. --tier restricts it to the
// named tiers; --gate is applied by resolve; with neither, the plan keeps
// the tiers that need no infrastructure. An empty unbounded plan falls
// back to the static tier, so the developer still gets feedback.
func (s selector) boundDiscovery(plan []tierPlan) ([]tierPlan, []string) {
	if len(s.tiers) > 0 {
		return restrictToTiers(plan, s.tiers, s.subsets)
	}
	if s.gate != "" {
		return plan, nil
	}
	if len(plan) == 0 {
		return []tierPlan{{name: tierStatic, notes: "no tiers resolved from selector; running static only"}}, nil
	}
	kept, note := boundToInfraFree(plan)
	if note == "" {
		return kept, nil
	}
	return kept, []string{note}
}

// hasDiscovery reports whether a discovery selector (--changed,
// --since, --spec, or --pkg) is active. --since sets changed.
func (s selector) hasDiscovery() bool {
	return s.changed || len(s.specs) > 0 || len(s.pkgs) > 0
}

// discoveryPlan unions the discovery selectors. Each contributes tiers
// and, for the unit tier, a package scope; see mergeDiscoveryPlans. A
// --spec query the tree does not know is an error.
func (s selector) discoveryPlan() ([]tierPlan, []string, error) {
	var plans [][]tierPlan
	var notes []string
	if s.changed {
		changed, changedNotes := resolveChangedPlanFn(s.since, selectOpts{rdepsDepth: s.rdepsDepth, all: s.all})
		plans = append(plans, changed)
		notes = append(notes, changedNotes...)
	}
	if len(s.specs) > 0 {
		specPlan, specNotes, err := resolveSpecsPlanFn(s.specs)
		if err != nil {
			return nil, notes, err
		}
		plans = append(plans, specPlan)
		notes = append(notes, specNotes...)
	}
	if len(s.pkgs) > 0 {
		pkgPlan, pkgNotes := resolvePkgsPlanFn(s.pkgs, selectOpts{rdepsDepth: s.rdepsDepth, all: s.all})
		plans = append(plans, pkgPlan)
		notes = append(notes, pkgNotes...)
	}
	return mergeDiscoveryPlans(plans, s.all), notes, nil
}

// resolveChangedPlanFn resolves the --changed and --since plan. Tests
// replace it to resolve a fixed diff without git.
var resolveChangedPlanFn = resolveChangedPlanFor

// resolvePkgsPlanFn resolves the --pkg plan. Tests replace it to resolve
// over a fixture graph rather than the module.
var resolvePkgsPlanFn = resolvePkgsPlan

// resolveSpecsPlanFn resolves the --spec plan. Tests replace it to
// resolve over a fixture model rather than the repository's tree.
var resolveSpecsPlanFn = resolveSpecsPlan

// indexOf returns the index of needle in haystack, or -1.
func indexOf(haystack []string, needle string) int {
	for i, h := range haystack {
		if h == needle {
			return i
		}
	}
	return -1
}

func printDryRun(s selector, r resolvedSelector) int {
	writeDryRun(os.Stdout, s, r)
	return 0
}

// writeDryRun prints the resolved plan: each tier, its subsets and
// tests, the unit tier's package scope, and the selection notes.
func writeDryRun(w io.Writer, s selector, r resolvedSelector) {
	fmt.Fprintf(w, "lenny-test (dry run)\n")
	fmt.Fprintf(w, "  selector: %s\n", describe(s))
	fmt.Fprintf(w, "  tiers:\n")
	if len(r.tiers) == 0 {
		fmt.Fprintf(w, "    (none resolved)\n")
	}
	for _, t := range r.tiers {
		extra := ""
		if t.notes != "" {
			extra = " — " + t.notes
		}
		fmt.Fprintf(w, "    - %s%s\n", t.name, extra)
		for _, sub := range t.subsets {
			fmt.Fprintf(w, "        subset: %s\n", sub)
		}
		for _, test := range t.tests {
			fmt.Fprintf(w, "        test:   %s\n", test)
		}
		if t.name == tierUnit || t.pkgs != nil {
			writeTierScope(w, t)
		}
	}
	for _, n := range r.notes {
		fmt.Fprintf(w, "  note: %s\n", n)
	}
}

// writeTierScope prints a tier's package patterns and the test functions
// it runs by name.
func writeTierScope(w io.Writer, t tierPlan) {
	if t.pkgs == nil {
		fmt.Fprintf(w, "        pkg:    ./... (every package)\n")
		return
	}
	fmt.Fprintf(w, "        packages: %d pattern(s)\n", len(t.pkgs))
	for _, p := range t.pkgs {
		fmt.Fprintf(w, "        pkg:    %s\n", p)
	}
	for _, dir := range sortedFuncDirs(t.funcs) {
		fmt.Fprintf(w, "        run:    %s: %s\n", dir, strings.Join(t.funcs[dir], ", "))
	}
}

// sortedFuncDirs returns the package patterns of a by-name selection in
// order.
func sortedFuncDirs(funcs map[string][]string) []string {
	out := make([]string, 0, len(funcs))
	for dir := range funcs {
		out = append(out, dir)
	}
	sort.Strings(out)
	return out
}

func describe(s selector) string {
	parts := []string{}
	if s.group != "" {
		parts = append(parts, "group="+s.group)
	}
	if len(s.tiers) > 0 {
		parts = append(parts, "tier="+strings.Join(s.tiers, ","))
	}
	if s.gate != "" {
		parts = append(parts, "gate="+s.gate)
	}
	if s.changed {
		parts = append(parts, "changed")
		if s.since != "" {
			parts = append(parts, "since="+s.since)
		}
		parts = append(parts, fmt.Sprintf("rdeps-depth=%d", s.rdepsDepth))
	}
	if s.all {
		parts = append(parts, "all")
	}
	if len(s.specs) > 0 {
		parts = append(parts, "spec="+strings.Join(s.specs, ","))
	}
	if len(s.pkgs) > 0 {
		parts = append(parts, "pkg="+strings.Join(s.pkgs, ","))
	}
	return strings.Join(parts, " ")
}

// execute runs the resolved plan and writes the verdict.
//
// Phase 0 executor:
//   - static  → invokes `go vet ./...`. (golangci-lint is the future Phase 1 addition.)
//   - unit    → invokes `go test` over the plan's package scope, or `./...`
//     when the plan is unscoped.
//   - others  → recorded as skipped with reason "phase-0-not-implemented".
//
// tierExecFn runs one tier's plan and returns (status, detail,
// *tierResult). All tier-specific executors implement this signature;
// runStaticTier (which has no tierResult) and the subset-driven
// executors are wrapped to fit.
type tierExecFn func(p tierPlan) (string, string, *tierResult)

// withSubsets adapts an executor that reads only the plan's subsets.
func withSubsets(fn func(subsets []string) (string, string, *tierResult)) tierExecFn {
	return func(p tierPlan) (string, string, *tierResult) { return fn(p.subsets) }
}

// tierStep is one entry in the execute() dispatch table. It pairs a
// tier-name string with the executor that runs it, an optional
// infra-field setup hook that stamps v.Infra before the executor
// fires, and a synthesizeNextAction fallback that names what the
// developer should fix.
type tierStep struct {
	name           string
	exec           tierExecFn
	infraSetup     func(v *verdict)
	nextActionHint string
	useSynthesize  bool // call v.synthesizeNextAction; otherwise use the literal hint
}

// tierSteps returns the table of executors for every canonical tier.
// Building the table here keeps execute()'s loop body small and the
// per-tier wiring readable from one place.
func tierSteps() map[string]tierStep {
	wrapStatic := func(_ tierPlan) (string, string, *tierResult) { return runStaticTier() }
	return map[string]tierStep{
		tierStatic: {
			name: tierStatic, exec: wrapStatic, useSynthesize: true,
			nextActionHint: "Fix static-tier failures before moving to higher tiers.",
		},
		tierUnit: {
			name: tierUnit, exec: runUnitTier, useSynthesize: true,
			nextActionHint: "Fix unit-tier failures before moving to higher tiers.",
		},
		tierComponent: {
			name: tierComponent, exec: runComponentTier, useSynthesize: true,
			nextActionHint: "Fix component-tier failures before moving to higher tiers.",
		},
		tierContract: {
			name: tierContract, exec: runContractTier, useSynthesize: true,
			nextActionHint: "Fix contract-tier failures before moving to higher tiers.",
		},
		tierConformance: {
			name: tierConformance, exec: withSubsets(runConformanceTier), useSynthesize: true,
			nextActionHint: "Fix conformance-tier failures before moving to higher tiers.",
		},
		tierIntegration: {
			name: tierIntegration, exec: runIntegrationTier, useSynthesize: true,
			nextActionHint: "Fix integration-tier failures before moving to higher tiers.",
			infraSetup: func(v *verdict) {
				// §7 infra fields: Phase 0 uses the default profile;
				// mtls is opt-in via the kind tier.
				v.Infra.ComposeProfile = "default"
			},
		},
		tierE2EKind: {
			name: tierE2EKind, exec: withSubsets(runE2EKindTier), useSynthesize: true,
			nextActionHint: "Fix e2e-Kind-tier failures before moving to higher tiers.",
			infraSetup: func(v *verdict) {
				if name := os.Getenv("LENNY_KIND_CLUSTER"); name != "" {
					v.Infra.KindCluster = name
				} else {
					v.Infra.KindCluster = "lenny"
				}
			},
		},
		tierLoadLocal: {
			name: tierLoadLocal, exec: func(_ tierPlan) (string, string, *tierResult) {
				return runTaggedTier("load_local", "./tests/load_local/...", tierLongTimeout, true)
			},
			nextActionHint: "Fix load_local-tier failures before moving to higher tiers.",
		},
		tierLoadKind: {
			name: tierLoadKind, exec: func(_ tierPlan) (string, string, *tierResult) {
				return runTaggedTier("load_kind", "./tests/load_kind/...", tierLongTimeout, false)
			},
			nextActionHint: "Fix load_kind-tier failures before moving to higher tiers.",
		},
		tierChaos: {
			name: tierChaos, exec: func(_ tierPlan) (string, string, *tierResult) {
				return runTaggedTier("chaos", "./tests/chaos/...", tierLongTimeout, false)
			},
			nextActionHint: "Fix chaos-tier failures before moving to higher tiers.",
		},
		tierSecurity: {
			name: tierSecurity, exec: func(_ tierPlan) (string, string, *tierResult) {
				return runTierArgs("security", securityTierArgs())
			},
			nextActionHint: "Fix security-tier failures before moving to higher tiers.",
		},
		tierDocs: {
			name: tierDocs, exec: func(_ tierPlan) (string, string, *tierResult) { return runDocsTier() },
			nextActionHint: "Fix docs-tier failures before moving to higher tiers.",
		},
		tierLoadCloud: {
			name: tierLoadCloud, exec: func(_ tierPlan) (string, string, *tierResult) {
				return runTaggedTier("load_cloud", "./tests/load_cloud/...", tierLongTimeout, false)
			},
			nextActionHint: "Fix load_cloud-tier failures; the load_cloud tier sits after every other tier and gates pre-release.",
		},
		tierE2ECloud: {
			name: tierE2ECloud, exec: func(_ tierPlan) (string, string, *tierResult) { return runE2ECloudTier() },
			nextActionHint: "Fix e2e-cloud-tier failures before moving to higher tiers.",
		},
	}
}

func execute(s selector, r resolvedSelector) int {
	v := newVerdict(s)

	// When --cached is requested and any tier in the plan needs the
	// compose stack, probe (and optionally spawn) the cached daemon
	// before running anything. The daemon brings the stack up once
	// and reuses it across tiers in the same run.
	if s.cached && !s.noInfra && planNeedsCompose(r) {
		if eps, err := cachedEnsure(); err != nil {
			fmt.Fprintf(os.Stderr, "lenny-test: cached daemon not reachable: %v\n", err)
			fmt.Fprintln(os.Stderr, "Continuing without cached infrastructure; tier-specific containers will be provisioned per test.")
		} else {
			fmt.Fprintf(os.Stderr, "lenny-test: using cached daemon (postgres=%v redis=%v minio=%v)\n",
				eps["postgres"], eps["redis"], eps["minio"])
		}
	}

	for _, n := range r.notes {
		fmt.Fprintf(os.Stderr, "lenny-test: note: %s\n", n)
	}
	for _, t := range r.tiers {
		if t.pkgs != nil {
			fmt.Fprintf(os.Stderr, "lenny-test: %s tier scoped to %d package pattern(s) and %d package(s) run by name; --dry-run lists them\n", t.name, len(t.pkgs), len(t.funcs))
		}
	}

	steps := tierSteps()
	overallStatus := verdictstatus.VerdictPass

	for _, t := range r.tiers {
		start := time.Now()
		step, ok := steps[t.name]
		if !ok {
			v.recordTier(t.name, verdictstatus.Skipped, time.Since(start), "phase-0-not-implemented: this tier ships in a later phase")
			continue
		}
		if step.infraSetup != nil {
			step.infraSetup(v)
		}
		st, msg, result := step.exec(t)
		if result != nil {
			v.recordTierWithResult(t.name, st, time.Since(start), msg, result)
		} else {
			v.recordTier(t.name, st, time.Since(start), msg)
		}
		if st == verdictstatus.Pass || s.continueErr {
			continue
		}
		// Failure path: synthesize the next-action message, write the
		// verdict, and return the failure summary.
		if step.useSynthesize {
			v.next(v.synthesizeNextAction(step.name, step.nextActionHint))
		} else {
			v.next(step.nextActionHint)
		}
		overallStatus = v.Verdict
		if writeErr := v.write(s.verdictFile); writeErr != nil {
			fmt.Fprintf(os.Stderr, "lenny-test: failed to write verdict to %s: %v\n", s.verdictFile, writeErr)
		}
		return printSummary(s, v, overallStatus, exitCodeFor(v.Verdict))
	}

	// printSummary fills in "not-selected" for every tier the
	// dispatch loop did not reach; we don't need to do it here.

	if writeErr := v.write(s.verdictFile); writeErr != nil {
		fmt.Fprintf(os.Stderr, "lenny-test: failed to write verdict to %s: %v\n", s.verdictFile, writeErr)
	}
	return printSummary(s, v, v.Verdict, exitCodeFor(v.Verdict))
}

// exitCodeFor maps the verdict value to the process exit code CI gates
// on: 0 for PASS, 1 for FAIL, 2 for INCONCLUSIVE so an infra-class
// failure is distinguishable from a real test failure, and 3 for
// UNVERIFIED so a check that could not reach a conclusion is
// distinguishable from both. Every non-PASS value is non-zero.
func exitCodeFor(verdict string) int {
	switch verdict {
	case verdictstatus.VerdictPass:
		return 0
	case verdictstatus.VerdictInconclusive:
		return 2
	case verdictstatus.VerdictUnverified:
		return 3
	default:
		return 1
	}
}

// staticCheck is one entry of the static-tier check table. run reports the
// check's output and, when the check failed outright, an error.
// classify carries the per-check status: when it is set and the check
// did not fail, it maps the output to a tier status, which lets a check
// report that it could not reach a conclusion (verdictstatus.Unverified)
// instead of being collapsed into a pass. The composer propagates
// whichever tier status it returns and treats an unrecognized value as
// unverified, so a classifier cannot leave the tier passing on a status
// nothing understands. A nil classify means the check only
// distinguishes pass from fail.
type staticCheck struct {
	name     string
	run      func() (string, error)
	classify func(out string) (status string, detail string)
	// budget is the wall time the check is expected to finish within.
	// A check that takes longer still reports its own result, and the
	// tier message names the overrun, so a slow gate is visible as a
	// defect to fix. Zero means the check has no budget.
	budget time.Duration
}

// staticPackageChecks returns one check per Go test package of the static
// tier, so the packages run concurrently and each reports its own result
// and its own time against its budget. A budget is the wall time the
// package is expected to finish within on a developer machine with a warm
// build cache (TESTING.md, "Static (`static`)"). The structure package
// carries the specification migration gates, and its budget is the larger
// while those gates remain. Each entry names its package literally, so the
// documentation test that reconciles TESTING.md with this table reads it.
func staticPackageChecks() []staticCheck {
	return []staticCheck{
		{name: "go test ./tests/static/build/...", run: goTestCheck("./tests/static/build/..."), classify: classifyUnverified, budget: 30 * time.Second},
		{name: "go test ./tests/static/chart/...", run: goTestCheck("./tests/static/chart/..."), classify: classifyUnverified, budget: 30 * time.Second},
		{name: "go test ./tests/static/lint/...", run: goTestCheck("./tests/static/lint/..."), classify: classifyUnverified, budget: 30 * time.Second},
		{name: "go test ./tests/static/structure/...", run: goTestCheck("./tests/static/structure/..."), classify: classifyUnverified, budget: 60 * time.Second},
	}
}

// goTestCheck returns a check body that runs the static-tier Go tests of
// pattern. A static-tier test that ran but could not reach a conclusion (a
// generator it needs is absent, for example) passes and writes the
// unverified marker to stdout, and the check's classifier, classifyUnverified,
// reads it, so such a test reports "proved nothing" rather than a green run
// that proved nothing silently.
func goTestCheck(pattern string) func() (string, error) {
	return func() (string, error) {
		out, err := exec.Command("go", goTestArgs(pattern)...).CombinedOutput()
		return string(out), err
	}
}

// runStaticTier runs the static-tier check table. The checks are
// independent, so they run concurrently, and every check runs whatever
// the others report: a failure early in the table does not hide the
// checks after it. The result carries one failure entry per failed
// check.
func runStaticTier() (string, string, *tierResult) {
	if _, err := exec.LookPath("go"); err != nil {
		return verdictstatus.Skipped, "go not on PATH", nil
	}

	checks := []staticCheck{
		{name: "go vet ./...", run: func() (string, error) {
			out, err := exec.Command("go", "vet", "./...").CombinedOutput()
			return string(out), err
		}},
		{name: "go vet -tags=contract ./tests/contract/...", run: func() (string, error) {
			// Verify contract-tagged tests compile without running them.
			// They are expected to fail at runtime in the current phase;
			// the static tier guarantees they at least compile.
			out, err := exec.Command("go", "vet", "-tags=contract", "./tests/contract/...").CombinedOutput()
			return string(out), err
		}},
		{name: "buf lint", run: func() (string, error) {
			if _, err := exec.LookPath("buf"); err != nil {
				return "buf not on PATH; skipping (run scripts/setup-dev.sh)", nil
			}
			out, err := exec.Command("buf", "lint").CombinedOutput()
			return string(out), err
		}},
		{name: "buf breaking", run: func() (string, error) {
			// Skip outside a git repo or when buf isn't installed.
			if _, err := exec.LookPath("buf"); err != nil {
				return "buf not on PATH; skipping breaking-change check", nil
			}
			if _, err := exec.LookPath("git"); err != nil {
				return "git not on PATH; skipping buf breaking", nil
			}
			// `buf breaking` exits 0 when no breaking changes; non-
			// zero on real regressions. Tolerate the "no .proto files"
			// case (pre-Phase 2) with a skip — buf reports it via the
			// "had no .proto files" message.
			cmd := exec.Command("buf", "breaking", "schemas/", "--against", ".git#branch=main")
			cmd.Dir = repoRoot()
			out, err := cmd.CombinedOutput()
			body := string(out)
			if err != nil && (strings.Contains(body, "does not exist") || strings.Contains(body, "no .proto files")) {
				return "schemas/ has no .proto files; skipping buf breaking (Phase 2 deliverable)", nil
			}
			// buf breaking protects external proto consumers from
			// accidental wire-incompatible changes by diffing against
			// `main`. v1 is built on a long-lived feature branch where
			// the proto is deliberately evolved from the Phase-1
			// skeleton committed to `main`, so the diff reports that
			// planned evolution rather than an accident. Off `main`,
			// surface the findings as advisory; on `main` itself the
			// check hard-fails so an accidental break still blocks a
			// release.
			if err != nil {
				branchCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
				branchCmd.Dir = repoRoot()
				branch, _ := branchCmd.Output()
				if strings.TrimSpace(string(branch)) != "main" {
					return "advisory: buf breaking diffs against `main`; on a feature branch the findings are the branch's deliberate proto evolution, not a regression. Not failing the static gate:\n" + body, nil
				}
			}
			return body, err
		}},
		{name: "gofumpt -l .", run: func() (string, error) {
			path := resolveGoBin("gofumpt")
			if path == "" {
				return "gofumpt not on PATH; skipping (install via go install mvdan.cc/gofumpt@latest)", nil
			}
			cmd := exec.Command(path, "-l", ".")
			cmd.Dir = repoRoot()
			out, err := cmd.CombinedOutput()
			// gofumpt walks every directory under the root, including
			// other agents' worktrees under .claude/worktrees/ and
			// untracked scratch trees, so judge only the files this
			// checkout tracks (see formatscope.go). An error whose output names
			// only out-of-tree files is the same walk and is dropped; an
			// error with no output at all still fails.
			body := scopeFormatterOutput(string(out), cmd.Dir)
			if err != nil && (body != "" || strings.TrimSpace(string(out)) == "") {
				return body, err
			}
			if body != "" {
				return fmt.Sprintf("gofumpt reports unformatted files:\n%s\n(run `gofumpt -w .` to fix)", body),
					fmt.Errorf("gofumpt: %d file(s) need formatting", strings.Count(body, "\n")+1)
			}
			return "gofumpt: all files formatted", nil
		}},
		{name: "goimports -l -local github.com/lennylabs/lenny .", run: func() (string, error) {
			path := resolveGoBin("goimports")
			if path == "" {
				return "goimports not on PATH; skipping (install via go install golang.org/x/tools/cmd/goimports@latest)", nil
			}
			cmd := exec.Command(path, "-l", "-local", "github.com/lennylabs/lenny", ".")
			cmd.Dir = repoRoot()
			out, err := cmd.CombinedOutput()
			// A testdata/ tree is fixture material rather than source: the go
			// tool ignores it by convention, and a fixture is often malformed
			// on purpose, so a parse failure there is the fixture working. Drop
			// every reported line whose path lies under a testdata/ directory,
			// for both the unordered-imports list and the parse errors, and
			// judge the check on what is left. A real source file is reported
			// and fails exactly as before.
			// The walk also reaches trees outside this checkout, as for
			// gofumpt above; scope the report to the checkout's files.
			body := scopeFormatterOutput(dropTestdataLines(string(out)), cmd.Dir)
			if err != nil && body != "" {
				return body, err
			}
			if body != "" {
				return fmt.Sprintf("goimports reports unordered imports:\n%s\n(run `goimports -w -local github.com/lennylabs/lenny .` to fix)", body),
					fmt.Errorf("goimports: %d file(s) need ordering", strings.Count(body, "\n")+1)
			}
			return "goimports: import ordering clean", nil
		}},
		{name: "golangci-lint run", run: func() (string, error) {
			// Prefer the binary at $GOPATH/bin since the install script
			// puts it there (TESTING_DEPENDENCIES.md §5). Fall back to
			// PATH lookup; skip if neither resolves.
			path := resolveGoBin("golangci-lint")
			if path == "" {
				return "golangci-lint not found; skipping (run scripts/setup-dev.sh)", nil
			}
			out, err := exec.Command(path, "run", "--timeout=2m").CombinedOutput()
			if err != nil {
				// golangci-lint is pinned to the v2 series (v1.x aborts on
				// Go 1.26 export-data). It now runs, but surfaces
				// pre-existing findings that were masked while the binary
				// could not load packages. Surface the output for
				// visibility without failing the tier; the check flips to
				// hard-fail once those findings are burned down (bd
				// lenny-vgl).
				return fmt.Sprintf("WARNING (non-fatal, see .golangci.yml):\n%s", out), nil
			}
			return string(out), nil
		}},
		{name: "scripts/lint-schema.sh (R-01)", run: func() (string, error) {
			script := filepath.Join(repoRoot(), "scripts", "lint-schema.sh")
			if _, err := os.Stat(script); err != nil {
				return "lint-schema.sh not present; skipping", nil
			}
			out, err := exec.Command("bash", script).CombinedOutput()
			return string(out), err
		}},
		{name: "scripts/lint-queries.sh (R-02)", run: func() (string, error) {
			script := filepath.Join(repoRoot(), "scripts", "lint-queries.sh")
			if _, err := os.Stat(script); err != nil {
				return "lint-queries.sh not present; skipping", nil
			}
			out, err := exec.Command("bash", script).CombinedOutput()
			return string(out), err
		}},
		{name: "scripts/lint-migrations.sh", run: func() (string, error) {
			script := filepath.Join(repoRoot(), "scripts", "lint-migrations.sh")
			if _, err := os.Stat(script); err != nil {
				return "lint-migrations.sh not present; skipping", nil
			}
			out, err := exec.Command("bash", script).CombinedOutput()
			return string(out), err
		}},
		{name: "scripts/check-adr-catalog.sh", run: func() (string, error) {
			script := filepath.Join(repoRoot(), "scripts", "check-adr-catalog.sh")
			if _, err := os.Stat(script); err != nil {
				return "check-adr-catalog.sh not present; skipping", nil
			}
			out, err := exec.Command("bash", script).CombinedOutput()
			return string(out), err
		}},
		{name: "scripts/check-doc-examples.sh", run: func() (string, error) {
			script := filepath.Join(repoRoot(), "scripts", "check-doc-examples.sh")
			if _, err := os.Stat(script); err != nil {
				return "check-doc-examples.sh not present; skipping", nil
			}
			out, err := exec.Command("bash", script).CombinedOutput()
			return string(out), err
		}},
		{name: "scripts/check-helm-charts.sh", run: func() (string, error) {
			script := filepath.Join(repoRoot(), "scripts", "check-helm-charts.sh")
			if _, err := os.Stat(script); err != nil {
				return "check-helm-charts.sh not present; skipping", nil
			}
			out, err := exec.Command("bash", script).CombinedOutput()
			return string(out), err
		}},
		{name: "scripts/check-schema-breaking.sh", run: func() (string, error) {
			script := filepath.Join(repoRoot(), "scripts", "check-schema-breaking.sh")
			if _, err := os.Stat(script); err != nil {
				return "check-schema-breaking.sh not present; skipping", nil
			}
			out, err := exec.Command("bash", script).CombinedOutput()
			return string(out), err
		}},
		{name: "scripts/check-action-pins.sh", run: func() (string, error) {
			script := filepath.Join(repoRoot(), "scripts", "check-action-pins.sh")
			if _, err := os.Stat(script); err != nil {
				return "check-action-pins.sh not present; skipping", nil
			}
			out, err := exec.Command("bash", script).CombinedOutput()
			return string(out), err
		}},
		{name: "scripts/check-tool-pins.sh", run: func() (string, error) {
			script := filepath.Join(repoRoot(), "scripts", "check-tool-pins.sh")
			if _, err := os.Stat(script); err != nil {
				return "check-tool-pins.sh not present; skipping", nil
			}
			out, err := exec.Command("bash", script).CombinedOutput()
			return string(out), err
		}},
		{name: "gitleaks dir (committed tree at HEAD)", run: func() (string, error) {
			// The secret scan over the committed tree at HEAD, with the
			// allowlist in .gitleaks.toml and the coverage registers
			// under tests/registers (see secretscan.go). A host without
			// gitleaks, or with another gitleaks release, reports
			// unverified rather than a pass.
			return runSecretScan(repoRoot())
		}, classify: classifyUnverified},
		{name: "scripts/lint-determinism.sh", run: func() (string, error) {
			script := filepath.Join(repoRoot(), "scripts", "lint-determinism.sh")
			if _, err := os.Stat(script); err != nil {
				return "lint-determinism.sh not present; skipping", nil
			}
			out, err := exec.Command("bash", script).CombinedOutput()
			if err != nil {
				// §17.4 violations are informational today —
				// pre-existing tests use real clocks for JWT expiry
				// and denylist TTLs. Each migrates to testinfra/timectl
				// individually. The linter surfaces the gap on every
				// run without breaking the gate.
				return fmt.Sprintf("WARNING (non-fatal, §17.4):\n%s", out), nil
			}
			return string(out), nil
		}},
		{name: "scripts/lint-test-conventions.sh", run: func() (string, error) {
			script := filepath.Join(repoRoot(), "scripts", "lint-test-conventions.sh")
			if _, err := os.Stat(script); err != nil {
				return "lint-test-conventions.sh not present; skipping", nil
			}
			out, err := exec.Command("bash", script).CombinedOutput()
			if err != nil {
				return fmt.Sprintf("WARNING (non-fatal, §17.6/§17.7):\n%s", out), nil
			}
			return string(out), nil
		}},
		{name: "scripts/check-markdown-links.sh", run: func() (string, error) {
			script := filepath.Join(repoRoot(), "scripts", "check-markdown-links.sh")
			if _, err := os.Stat(script); err != nil {
				return "check-markdown-links.sh not present; skipping", nil
			}
			out, err := exec.Command("bash", script).CombinedOutput()
			if err != nil {
				// Jekyll renders many internal links at site-build
				// time that markdown-link-check cannot resolve in
				// isolation. Surface the report but do not fail the
				// tier; the PR workflow keeps a separate hard gate
				// that runs against the rendered site.
				return fmt.Sprintf("WARNING (non-fatal):\n%s", out), nil
			}
			return string(out), nil
		}},
		{name: "validate-diagnosis", run: func() (string, error) {
			// Re-invoke this very binary's `validate-diagnosis`
			// subcommand. The subcommand walks every component-and-later
			// _test.go file and confirms each test function carries
			// the §17.2 annotation (or is a scaffold whose t.Skip
			// already names the spec section).
			self, err := os.Executable()
			if err != nil {
				return "", fmt.Errorf("locate self: %w", err)
			}
			out, err := exec.Command(self, "validate-diagnosis").CombinedOutput()
			return string(out), err
		}},
		{name: "validate", run: func() (string, error) {
			// The selection and register files parse and resolve, and
			// every component-and-up test carries a spec annotation.
			self, err := os.Executable()
			if err != nil {
				return "", fmt.Errorf("locate self: %w", err)
			}
			out, err := exec.Command(self, "validate").CombinedOutput()
			return string(out), err
		}},
	}

	checks = append(checks, staticPackageChecks()...)
	outcomes := runStaticChecks(checks)
	status, msg := reduceStaticOutcomes(outcomes)
	return status, msg, staticTierResult(outcomes)
}

// goTestArgs returns the argv (after the "go" binary itself) that runs
// the given package pattern as a static-tier check. The verbose flag is
// load-bearing rather than cosmetic: in package-list mode cmd/go
// buffers a test binary's output and drops it for a package that
// passes, so without -v the unverified marker a passing test writes to
// stdout never reaches classifyUnverified and a check that proved
// nothing would be read as a check that passed.
func goTestArgs(pattern string) []string {
	return []string{"test", "-count=1", "-v", pattern}
}

// classifyUnverified maps a check's output to Unverified when the
// output carries the shared marker, and to Pass otherwise. It is the
// producer side of the unverified tier status for checks that run a Go
// test binary.
func classifyUnverified(out string) (string, string) {
	reasons, ok := verdictstatus.ScanUnverified(out)
	if !ok {
		return verdictstatus.Pass, ""
	}
	return verdictstatus.Unverified, strings.Join(reasons, "; ")
}

// composeStaticChecks runs the static-tier check table and reduces it to one
// tier status and one message. See runStaticChecks and
// reduceStaticOutcomes.
func composeStaticChecks(checks []staticCheck) (string, string) {
	return reduceStaticOutcomes(runStaticChecks(checks))
}

// staticOutcome is what one static-tier check reported.
type staticOutcome struct {
	name     string
	out      string
	err      error
	duration time.Duration
	budget   time.Duration
	// status and detail are the classifier's normalized report. They
	// are empty when the check errored or carries no classifier.
	status string
	detail string
}

// staticCheckConcurrency bounds how many static-tier checks run at once.
// Several checks (go vet, golangci-lint, and the static-tier Go tests) are
// parallel themselves, so running every check at once oversubscribes the
// host without finishing sooner.
var staticCheckConcurrency = max(2, runtime.NumCPU()/2)

// runStaticChecks runs every check concurrently, bounded by
// staticCheckConcurrency, and returns the outcomes in table order so the
// tier message does not depend on which check finished first.
func runStaticChecks(checks []staticCheck) []staticOutcome {
	outcomes := make([]staticOutcome, len(checks))
	sem := make(chan struct{}, staticCheckConcurrency)
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			outcomes[i] = runStaticCheck(c)
		}()
	}
	wg.Wait()
	return outcomes
}

// runStaticCheck runs one check and applies its classifier when the
// check did not error.
func runStaticCheck(c staticCheck) staticOutcome {
	start := time.Now()
	out, err := c.run()
	o := staticOutcome{name: c.name, out: out, err: err, duration: time.Since(start), budget: c.budget}
	if err == nil && c.classify != nil {
		o.status, o.detail = normalizeCheckStatus(c.classify(out))
	}
	return o
}

// reduceStaticOutcomes reduces the outcomes to one tier status and one
// message. Any check that errored makes the tier fail, and the message
// names every such check with its output, so a run reports all of its
// failures at once. Otherwise the tier takes the strongest status any
// classifier reported, by the same ranking the verdict applies: fail
// outranks inconclusive, which outranks unverified, which outranks pass.
// A check that reports nothing leaves the tier at pass.
func reduceStaticOutcomes(outcomes []staticOutcome) (string, string) {
	var failed, notes []string
	status := verdictstatus.Pass
	for _, o := range outcomes {
		if note := budgetNote(o); note != "" {
			notes = append(notes, note)
		}
		if o.err != nil {
			failed = append(failed, fmt.Sprintf("%s failed:\n%s", o.name, o.out))
			continue
		}
		if o.status == "" || o.status == verdictstatus.Pass {
			continue
		}
		if tierStatusRank(o.status) > tierStatusRank(status) {
			status = o.status
		}
		note := fmt.Sprintf("%s reported %s", o.name, o.status)
		if o.detail != "" {
			note += ": " + o.detail
		}
		notes = append(notes, note)
	}
	if len(failed) > 0 {
		return verdictstatus.Fail, strings.Join(append(failed, notes...), "\n")
	}
	return status, strings.Join(notes, "\n")
}

// budgetNote names a check that took longer than its budget, or returns ""
// when the check has no budget or kept within it. The overrun does not
// change the check's status: the tier reports it so the slow package is
// split or sped up.
func budgetNote(o staticOutcome) string {
	if o.budget <= 0 || o.duration <= o.budget {
		return ""
	}
	return fmt.Sprintf("%s exceeded its time budget: %s, budget %s",
		o.name, o.duration.Round(time.Second), o.budget)
}

// staticTierResult converts the outcomes into the per-check counts and
// failure entries the verdict records: one failure entry per check that
// errored or whose classifier reported fail.
func staticTierResult(outcomes []staticOutcome) *tierResult {
	r := &tierResult{Total: len(outcomes)}
	for _, o := range outcomes {
		if o.err == nil && o.status != verdictstatus.Fail {
			r.Passed++
			continue
		}
		r.Failed++
		msg := o.detail
		if o.err != nil {
			msg = o.err.Error()
		}
		r.Failures = append(r.Failures, failureEntry{
			Test:       o.name,
			Error:      msg,
			DurationMS: o.duration.Milliseconds(),
			StdoutTail: tailLines(o.out, staticFailureTailLines),
		})
	}
	return r
}

// staticFailureTailLines is how many trailing lines of a failed check's
// output its failure entry keeps.
const staticFailureTailLines = 60

// tailLines returns the last n lines of s.
func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// normalizeCheckStatus maps a classifier's return value onto the tier
// statuses the composer propagates, failing closed on anything else.
// The composer starts at pass, so an unrecognized value would otherwise
// leave the tier passing while nothing established that the check
// passed. Such a value proves nothing, which is what unverified
// reports, and the value that was returned survives in the detail so it
// stays diagnosable. This mirrors what recordTier does one layer up for
// an unrecognized tier status.
func normalizeCheckStatus(status, detail string) (string, string) {
	switch status {
	case verdictstatus.Pass, verdictstatus.Fail, verdictstatus.Inconclusive, verdictstatus.Unverified:
		return status, detail
	default:
		return verdictstatus.Unverified, unrecognizedStatusDetail(status, detail)
	}
}

// tierStatusRank orders the statuses the composer propagates from the
// weakest to the strongest claim about the tier, matching verdictRank
// so a check's status and the verdict it produces cannot disagree about
// which of two findings wins.
func tierStatusRank(status string) int {
	switch status {
	case verdictstatus.Fail:
		return 3
	case verdictstatus.Inconclusive:
		return 2
	case verdictstatus.Unverified:
		return 1
	}
	return 0
}

// runUnitTier runs `go test` over the plan's scope: its package patterns
// whole and its by-name functions (see scopedRuns), or the whole module
// (`./...`) when the plan is unscoped. --changed and --pkg pass the scope
// they resolved, also when --tier unit restricts them; --tier unit alone,
// --gate alone, and --all leave it nil.
func runUnitTier(p tierPlan) (string, string, *tierResult) {
	// Race detection is on by default per §17.4. When
	// LENNY_COVER_PROFILE is set, coverage is emitted to that path so
	// `lenny-test coverage --go` can later roll it up.
	if _, err := exec.LookPath("go"); err != nil {
		return "skipped", "go not on PATH", nil
	}
	if !hasGoCode() {
		return "pass", "no Go packages under pkg/ yet", &tierResult{}
	}
	profile := coverProfilePath()
	if p.pkgs == nil {
		result, runErr := runGoTestJSONFn(unitTierArgs(nil, profile)...)
		return tierOutcome("unit", result, runErr)
	}
	tails := scopedRuns(p.pkgs, p.funcs)
	if len(tails) == 0 {
		// A scoped plan with nothing in scope. Running `go test` with no
		// package argument would test the working directory instead.
		return "pass", "no packages in the selected scope", &tierResult{}
	}
	result, runErr := runGoTestTails(tails, func(i int, tail []string) []string {
		// Only the invocation that runs whole packages writes the
		// coverage profile; a second invocation would overwrite it.
		if i == 0 && tail[0] != "-run" {
			return unitTierFlags(profile)
		}
		return unitTierFlags("")
	})
	return tierOutcome("unit", result, runErr)
}

// unitTierArgs returns the `go test` arguments for the unit tier: race
// on, coverage to profile when profile is non-empty, and pkgs as the
// package patterns, or `./...` when pkgs is nil. Coverage applies to
// whatever packages run, so a scoped run writes a scoped profile.
func unitTierArgs(pkgs []string, profile string) []string {
	args := unitTierFlags(profile)
	if pkgs == nil {
		return append(args, "./...")
	}
	return append(args, pkgs...)
}

// unitTierFlags returns the unit tier's `go test` flags: race on, and
// coverage to profile when profile is non-empty.
func unitTierFlags(profile string) []string {
	args := []string{"-race", "-count=1", fmt.Sprintf("-timeout=%s", tierUnitTimeout)}
	if profile != "" {
		if err := os.MkdirAll(filepath.Dir(profile), 0o755); err == nil {
			args = append(args, "-coverprofile="+profile, "-covermode=atomic")
		}
	}
	return args
}

// summarizeFailures produces a one-line summary of a tier's failed
// tests for the verdict.detail field. The full set lives in
// tierStat.Failures[].
func summarizeFailures(r *tierResult) string {
	if r == nil || len(r.Failures) == 0 {
		return ""
	}
	const max = 3
	names := make([]string, 0, max)
	for i, f := range r.Failures {
		if i >= max {
			break
		}
		names = append(names, f.Package+"::"+f.Test)
	}
	suffix := ""
	if len(r.Failures) > max {
		suffix = fmt.Sprintf(", ... (+%d more)", len(r.Failures)-max)
	}
	return fmt.Sprintf("%d failure(s): %s%s", len(r.Failures), strings.Join(names, ", "), suffix)
}

// planNeedsCompose reports whether any tier in r benefits from the
// compose stack (component / contract / integration / load_kind).
// The load_local tier runs in-process and does not need compose.
// The load_cloud tier runs against a real cloud cluster and does
// not need compose either.
func planNeedsCompose(r resolvedSelector) bool {
	for _, t := range r.tiers {
		switch t.name {
		case "component", "contract", "integration", "load_kind":
			return true
		}
	}
	return false
}

// coverProfilePath returns the canonical cover-profile location.
// Set LENNY_COVER_PROFILE=path to override; set LENNY_COVER_PROFILE=
// (empty) to disable.
func coverProfilePath() string {
	if v, ok := os.LookupEnv("LENNY_COVER_PROFILE"); ok {
		return v
	}
	return filepath.Join(repoRoot(), "tests", "results", "cover.out")
}

// resolveGoBin looks up a tool by name. Returns its path on PATH, or the
// path under $GOPATH/bin if it lives there but is not on PATH (which is
// common because setup-dev.sh installs go tools to GOPATH/bin and
// instructs the user to update PATH separately).
func resolveGoBin(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	gopath, err := exec.Command("go", "env", "GOPATH").Output()
	if err != nil {
		return ""
	}
	candidate := filepath.Join(strings.TrimSpace(string(gopath)), "bin", name)
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	return ""
}

func runComponentTier(p tierPlan) (string, string, *tierResult) {
	// Component tests are guarded by the `component` build tag. Each
	// subdirectory under tests/component/ targets a distinct subsystem;
	// some need Docker (testcontainers-go), others run in-process. When
	// subsets is non-empty the runner scopes to those subdirectories so a
	// phase gate can run only the work in scope for that phase. A scoped
	// plan (--changed, --pkg, or a specification edit) runs the packages
	// and functions it selected, and is assumed to need Docker. Otherwise
	// the runner executes every component directory.
	if _, err := exec.LookPath("go"); err != nil {
		return "skipped", "go not on PATH", nil
	}
	flags := []string{"-count=1", fmt.Sprintf("-timeout=%s", tierComponentTimeout), "-tags=component"}
	scoped := len(p.subsets) == 0 && p.pkgs != nil
	targets, needsDocker, err := componentTargets(p.subsets)
	if err != nil {
		return "fail", err.Error(), nil
	}
	if scoped {
		if len(scopedRuns(p.pkgs, p.funcs)) == 0 {
			return "pass", "no packages in the selected scope", &tierResult{}
		}
		needsDocker = true
	}
	if needsDocker {
		if _, err := exec.LookPath("docker"); err != nil {
			return "skipped", "docker not on PATH", nil
		}
		if err := exec.Command("docker", "info").Run(); err != nil {
			return "skipped", "docker daemon not running; start Docker and retry", nil
		}
	}
	if scoped {
		return runScopedTier("component", flags, p)
	}
	result, runErr := runGoTestJSONFn(append(flags, targets...)...)
	return tierOutcome("component", result, runErr)
}

// componentTargets maps component subset names to (test package globs,
// needsDocker). An empty subset list resolves to every component directory
// plus the testinfra/containers package and any cmd/ package that carries
// its own `component`-tagged tests in-package (for example cmd/lenny-ops,
// which tests Redis- and Postgres-backed unexported helpers directly
// against real containers rather than through an external tests/
// package). Without the cmd/lenny-ops/... target, files there guarded by
// `//go:build component` never compile under any tier: the unit tier's
// `go test ./...` carries no `-tags=component`, and no other tier target
// reaches cmd/lenny-ops.
func componentTargets(subsets []string) ([]string, bool, error) {
	if len(subsets) == 0 {
		targets := []string{"./tests/component/..."}
		for _, tree := range tierExtraTrees(tierComponent) {
			targets = append(targets, "./"+tree+"/...")
		}
		return targets, true, nil
	}
	type entry struct {
		path        string
		needsDocker bool
	}
	mapping := map[string]entry{
		"migrations":    {"./tests/component/migrations/...", true},
		"observability": {"./tests/component/observability/...", false},
	}
	seen := map[string]bool{}
	targets := []string{}
	needsDocker := false
	for _, s := range subsets {
		e, ok := mapping[s]
		if !ok {
			return nil, false, fmt.Errorf("unknown component subset %q", s)
		}
		if seen[e.path] {
			continue
		}
		seen[e.path] = true
		targets = append(targets, e.path)
		if e.needsDocker {
			needsDocker = true
		}
	}
	return targets, needsDocker, nil
}

func runConformanceTier(subsets []string) (string, string, *tierResult) {
	// The conformance tier exercises the runtime adapters against the
	// lenny-compliance harness. The subset names map to integration
	// levels: "basic" → cmd/runtimes/echo at lenny-compliance --level
	// basic; "standard" → cmd/runtimes/delegation-echo at
	// lenny-compliance --level standard; "full" →
	// cmd/runtimes/streaming-echo at lenny-compliance --level full. An
	// empty subset list runs every level whose target runtime is built.
	if _, err := exec.LookPath("go"); err != nil {
		return "skipped", "go not on PATH", nil
	}
	root := repoRoot()
	tmpBase, err := os.MkdirTemp("", "lenny-conformance-*")
	if err != nil {
		return "fail", fmt.Sprintf("mkdtemp: %v", err), nil
	}
	defer os.RemoveAll(tmpBase)

	type runtimeBuild struct {
		level   string
		runtime string
		pkg     string
	}
	available := map[string]runtimeBuild{
		"basic":    {level: "basic", runtime: "echo", pkg: "./cmd/runtimes/echo"},
		"standard": {level: "standard", runtime: "delegation-echo", pkg: "./cmd/runtimes/delegation-echo"},
		"full":     {level: "full", runtime: "streaming-echo", pkg: "./cmd/runtimes/streaming-echo"},
	}
	// "bundled-runtimes" is the pr-group alias for every in-tree
	// reference runtime. It expands to each available level whose
	// runtime package is present on disk, so the conformance gate
	// tracks the bundled runtimes as they land.
	expandBundled := func() []string {
		levels := []string{}
		for _, lvl := range []string{"basic", "standard", "full"} {
			rb, ok := available[lvl]
			if !ok {
				continue
			}
			if _, err := os.Stat(filepath.Join(root, rb.pkg)); err == nil {
				levels = append(levels, lvl)
			}
		}
		return levels
	}
	if len(subsets) == 0 {
		subsets = []string{"basic", "full"}
	}
	// "reference-catalog" is the nightly/weekly/pre-release subset for
	// the §26 first-party reference runtimes (claude-code, gemini-cli,
	// codex, and the rest). Those runtimes are a Phase 17a deliverable;
	// until cmd/runtimes/<name> exists for them the subset has nothing
	// to exercise. It expands, like bundled-runtimes, only over the
	// reference-catalog runtimes whose package is present on disk.
	referenceCatalog := []string{
		"claude-code", "gemini-cli", "codex", "cursor-cli", "chat",
		"langgraph", "mastra", "openai-assistants", "crewai",
	}
	expandReferenceCatalog := func() []string {
		built := []string{}
		for _, name := range referenceCatalog {
			if _, err := os.Stat(filepath.Join(root, "cmd", "runtimes", name)); err == nil {
				built = append(built, name)
			}
		}
		return built
	}
	expanded := []string{}
	for _, sub := range subsets {
		switch sub {
		case "bundled-runtimes", "bundled":
			expanded = append(expanded, expandBundled()...)
		case "reference-catalog":
			expanded = append(expanded, expandReferenceCatalog()...)
		default:
			expanded = append(expanded, sub)
		}
	}
	subsets = expanded
	if len(subsets) == 0 {
		// Every requested subset resolved to a runtime set that is not
		// yet built — the §26 reference catalog is a Phase 17a item.
		return verdictstatus.Skipped, "no conformance runtimes built for the requested subsets " +
			"(the §26 reference-catalog runtimes are a Phase 17a deliverable)", nil
	}

	binCompliance := filepath.Join(tmpBase, "lenny-compliance")
	cmd := exec.Command("go", "build", "-o", binCompliance, "./cmd/lenny-compliance")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return "fail", fmt.Sprintf("build lenny-compliance: %v\n%s", err, out), nil
	}

	results := []string{}
	tr := &tierResult{}
	for _, sub := range subsets {
		rb, ok := available[sub]
		if !ok {
			return "fail", fmt.Sprintf("unknown conformance subset %q", sub), tr
		}
		binRuntime := filepath.Join(tmpBase, rb.runtime)
		buildCmd := exec.Command("go", "build", "-o", binRuntime, rb.pkg)
		buildCmd.Dir = root
		if out, err := buildCmd.CombinedOutput(); err != nil {
			tr.Total++
			tr.Failed++
			tr.Failures = append(tr.Failures, failureEntry{
				Test: rb.runtime, Package: rb.pkg,
				Error: fmt.Sprintf("build failed: %v", err), StdoutTail: string(out),
				RerunCommand: fmt.Sprintf("go build -o /tmp/%s %s", rb.runtime, rb.pkg),
			})
			return "fail", fmt.Sprintf("build %s: %v\n%s", rb.pkg, err, out), tr
		}
		runCmd := exec.Command(binCompliance, "--binary", binRuntime, "--level", rb.level)
		out, err := runCmd.CombinedOutput()
		tr.Total++
		if err != nil {
			tr.Failed++
			tr.Failures = append(tr.Failures, failureEntry{
				Test: rb.runtime, Package: rb.pkg,
				Error: fmt.Sprintf("conformance --level %s failed", rb.level), StdoutTail: string(out),
				RerunCommand: fmt.Sprintf("%s --binary %s --level %s", binCompliance, binRuntime, rb.level),
			})
			return "fail", fmt.Sprintf("conformance --level %s against %s failed:\n%s", rb.level, rb.runtime, out), tr
		}
		tr.Passed++
		results = append(results, strings.TrimSpace(string(out)))
	}
	return "pass", strings.Join(results, "\n\n"), tr
}

// runDocsTier runs the docs checks. No build tag —
// these tests exercise repo state directly.
func runDocsTier() (string, string, *tierResult) {
	if _, err := exec.LookPath("go"); err != nil {
		return "skipped", "go not on PATH", nil
	}
	result, runErr := runGoTestJSON("-count=1", fmt.Sprintf("-timeout=%s", tierDocsTimeout), "./tests/docs/...")
	if runErr != nil && result.Failed == 0 {
		return "fail", fmt.Sprintf("docs suite failed: %v\n%s", runErr, result.RawOut), result
	}
	if result.Failed > 0 {
		return "fail", summarizeFailures(result), result
	}
	return "pass", "", result
}

// taggedTierArgs assembles the `go test` arguments for a tagged tier.
// race enables the detector: the local load tier asserts concurrency,
// ordering, and atomicity properties whose violations are data races on
// shared adapter state, so the detector is half of what those cases buy
// and the tier runs with it on.
func taggedTierArgs(tag, targetGlob string, timeout time.Duration, race bool) []string {
	args := []string{"-count=1", fmt.Sprintf("-timeout=%s", timeout)}
	if race {
		args = append(args, "-race")
	}
	return append(args, "-tags="+tag, targetGlob)
}

// runTaggedTier runs `go test` over targetGlob under the supplied
// build tag. Used for tiers whose tests are mostly skip-bearing
// scaffolds (load, chaos, and security) so the tier reports `pass`
// (with skipped sub-tests) until the backing infrastructure lands.
func runTaggedTier(tag, targetGlob string, timeout time.Duration, race bool) (string, string, *tierResult) {
	return runTierArgs(tag, taggedTierArgs(tag, targetGlob, timeout, race))
}

// securityTierArgs is the `go test` argv of the security tier. It runs
// the security-tier package under its own budget, tierSecurityTimeout, rather
// than the long-tier budget the load and chaos tiers share, because the
// package's runtime under a loaded cluster exceeds that budget and
// `go test -timeout` then aborts the whole binary (TEST-GAPS T-17.4.19).
func securityTierArgs() []string {
	return taggedTierArgs("security", "./tests/security/...", tierSecurityTimeout, false)
}

// runTierArgs runs `go test` with a tier's prepared argv and maps the
// outcome to a tier status. tag names the suite in a failure message.
func runTierArgs(tag string, args []string) (string, string, *tierResult) {
	if _, err := exec.LookPath("go"); err != nil {
		return "skipped", "go not on PATH", nil
	}
	result, runErr := runGoTestJSON(args...)
	if runErr != nil && result.Failed == 0 {
		return "fail", fmt.Sprintf("%s suite failed: %v\n%s", tag, runErr, result.RawOut), result
	}
	if result.Failed > 0 {
		return "fail", summarizeFailures(result), result
	}
	return "pass", "", result
}

// runE2ECloudTier runs the e2e_cloud tests under the `e2e_cloud`
// build tag. Tests internally guard on LENNY_CLOUD_PROVIDER + the
// per-provider auth surface; without that, every test skips with a
// precise diagnosis. The tier reports `pass` (with skipped
// sub-tests) on hosts that have not been bound to a cloud target.
func runE2ECloudTier() (string, string, *tierResult) {
	if _, err := exec.LookPath("go"); err != nil {
		return "skipped", "go not on PATH", nil
	}
	result, runErr := runGoTestJSON("-count=1", fmt.Sprintf("-timeout=%s", tierE2ECloudTimeout), "-tags=e2e_cloud", "./tests/e2e_cloud/...")
	if runErr != nil && result.Failed == 0 {
		return "fail", fmt.Sprintf("e2e_cloud suite failed: %v\n%s", runErr, result.RawOut), result
	}
	if result.Failed > 0 {
		return "fail", summarizeFailures(result), result
	}
	return "pass", "", result
}

// runE2EKindTier runs the e2e_kind tests under the `e2e_kind`
// build tag. Each test calls tests/testinfra/kind.SkipUnlessAvailable
// to short-circuit when docker / kind / a reachable docker daemon
// are absent, so the tier reports `pass` (with skipped sub-tests)
// on hosts without the e2e prerequisites.
//
// When subsets is non-empty the runner narrows execution to the
// matching tests via -run. The subset→test mapping mirrors the
// names in groups.subsets.yaml.
func runE2EKindTier(subsets []string) (string, string, *tierResult) {
	if _, err := exec.LookPath("go"); err != nil {
		return "skipped", "go not on PATH", nil
	}
	extra := []string{"-count=1", fmt.Sprintf("-timeout=%s", tierE2EKindTimeout), "-tags=e2e_kind"}
	if pattern, err := e2eKindSubsetPattern(subsets); err != nil {
		return "fail", err.Error(), nil
	} else if pattern != "" {
		extra = append(extra, "-run", pattern)
	}
	extra = append(extra, "./tests/e2e_kind/...")
	result, runErr := runGoTestJSON(extra...)
	if runErr != nil && result.Failed == 0 {
		return "fail", fmt.Sprintf("e2e_kind suite failed: %v\n%s", runErr, result.RawOut), result
	}
	if result.Failed > 0 {
		return "fail", summarizeFailures(result), result
	}
	return "pass", "", result
}

// e2eKindSubsetPattern joins the subset → test-name mapping into a
// regex suitable for `go test -run`. An empty subsets slice returns
// "" so the runner executes the full suite.
func e2eKindSubsetPattern(subsets []string) (string, error) {
	if len(subsets) == 0 {
		return "", nil
	}
	mapping := map[string][]string{
		"critical-path": {
			"TestWarmPool", "TestPodClaim", "TestPodLifecycle",
			"TestMTLSEnforcement", "TestAdmissionPolicy",
			"TestAdmissionInventory", "TestLennyOpsFirstDeploy",
			"TestBootstrapFirstInstall",
		},
		"admission-critical-path": {
			"TestAdmissionPolicy", "TestAdmissionInventory",
			"TestLabelImmutability", "TestPodClaim",
		},
		"pool-upgrade":                 {"TestPoolUpgrade"},
		"mtls":                         {"TestMTLSEnforcement"},
		"etcd-encryption":              {"TestEtcdEncryption"},
		"bootstrap-first-install":      {"TestBootstrapFirstInstall"},
		"llm-proxy-proxy-mode":         {"TestLLMProxyProxyMode", "TestAdmissionDirectModeIsolation"},
		"drain-readiness":              {"TestDrainReadinessWebhook"},
		"checkpoint-resume":            {"TestPodLifecycle", "TestNodeDrain"},
		"concurrent-modes":             {"TestConcurrentModes"},
		"audit-pipeline":               {"TestAuditPipeline"},
		"backup-restore":               {"TestBackupRestore"},
		"data-residency":               {"TestAdmissionDataResidency"},
		"t4-node-isolation":            {"TestAdmissionT4NodeIsolation"},
		"cross-environment-delegation": {"TestCrossEnvironmentDelegation"},
		"image-signing":                {"TestImageSigning"},
	}
	names := []string{}
	seen := map[string]bool{}
	for _, sub := range subsets {
		tests, ok := mapping[sub]
		if !ok {
			return "", fmt.Errorf("unknown e2e_kind subset %q", sub)
		}
		for _, n := range tests {
			if seen[n] {
				continue
			}
			seen[n] = true
			names = append(names, n)
		}
	}
	return "^(" + strings.Join(names, "|") + ")$", nil
}

// runIntegrationTier runs the integration tests under the
// `integration` build tag. Each test boots cmd/lenny-gateway as a
// subprocess (via tests/testinfra/gateway) and drives the real HTTP
// surface end-to-end. When subsets is non-empty the runner uses
// -run with the canonical regex for that subset.
//
// A plan with a scope runs the test functions a specification edit
// selected, by name. The tier is one Go package, so a selection that
// reaches it through an import runs it whole (change 0116, decision 3).
func runIntegrationTier(p tierPlan) (string, string, *tierResult) {
	if _, err := exec.LookPath("go"); err != nil {
		return "skipped", "go not on PATH", nil
	}
	subsets := p.subsets
	flags := []string{"-count=1", fmt.Sprintf("-timeout=%s", tierIntegrationTimeout), "-tags=integration"}
	if len(subsets) == 0 && p.pkgs != nil {
		return runScopedTier("integration", flags, p)
	}
	runArgs := []string{}
	if len(subsets) > 0 {
		mapping := map[string]string{
			"session-lifecycle": "TestSessionLifecycleAgainstRealGateway|TestCrossTenantLookupRejectedAgainstRealGateway",
			"idempotency":       "TestIdempotency.*ThroughBinary",
		}
		parts := []string{}
		for _, sub := range subsets {
			p, ok := mapping[sub]
			if !ok {
				return "fail", fmt.Sprintf("unknown integration subset %q", sub), nil
			}
			parts = append(parts, p)
		}
		runArgs = []string{"-run", strings.Join(parts, "|")}
	}
	extra := append(flags, runArgs...)
	extra = append(extra, "./tests/integration/...")
	result, runErr := runGoTestJSONFn(extra...)
	return tierOutcome("integration", result, runErr)
}

func runContractTier(p tierPlan) (string, string, *tierResult) {
	// Contract tests are guarded by the `contract` build tag. Each
	// subdirectory under tests/contract/ targets a distinct contract
	// surface (adapter_jsonl, workspaceplan, ...). When subsets is non-empty
	// the runner scopes to those subdirectories so a phase gate can verify
	// only the contracts in scope for that phase. When subsets is empty the
	// runner exercises every contract directory, and a scoped plan
	// (--changed, --pkg, or a specification edit) runs the packages and
	// functions it selected.
	if _, err := exec.LookPath("go"); err != nil {
		return "skipped", "go not on PATH", nil
	}
	flags := []string{"-count=1", "-tags=contract"}
	if len(p.subsets) == 0 && p.pkgs != nil {
		return runScopedTier("contract", flags, p)
	}
	targets, err := contractTargets(p.subsets)
	if err != nil {
		return "fail", err.Error(), nil
	}
	result, runErr := runGoTestJSONFn(append(flags, targets...)...)
	return tierOutcome("contract", result, runErr)
}

// contractTargets maps contract subset names to the package globs the runner
// executes. An empty subset list resolves to every contract directory.
func contractTargets(subsets []string) ([]string, error) {
	if len(subsets) == 0 {
		return []string{"./tests/contract/..."}, nil
	}
	mapping := map[string]string{
		"adapter-jsonl":         "./tests/contract/adapter_jsonl/...",
		"gatewaycontrol-scrub":  "./tests/contract/gatewaycontrol_scrub/...",
		"workspaceplan":         "./tests/contract/workspaceplan/...",
		"rest-sessions":         "./tests/contract/rest_sessions/...",
		"rest-idempotency":      "./tests/contract/rest_idempotency/...",
		"rest-circuitbreaker":   "./tests/contract/rest_circuitbreaker/...",
		"rest-auth":             "./tests/contract/rest_auth/...",
		"oauth-token":           "./tests/contract/oauth_token/...",
		"rest-mcp-consistency":  "./tests/contract/rest_mcp_consistency/...",
		"rest-openai-chat":      "./tests/contract/rest_openai_chat/...",
		"rest-openai-responses": "./tests/contract/rest_openai_responses/...",
		"ocsf-audit":            "./tests/contract/ocsf_audit/...",
		"cloudevents":           "./tests/contract/cloudevents/...",
		"sdk-go":                "./tests/contract/sdks/...",
		"sdk-python":            "./tests/contract/sdks/...",
		"sdk-typescript":        "./tests/contract/sdks/...",
	}
	seen := map[string]bool{}
	targets := []string{}
	for _, s := range subsets {
		g, ok := mapping[s]
		if !ok {
			return nil, fmt.Errorf("unknown contract subset %q", s)
		}
		if seen[g] {
			continue
		}
		seen[g] = true
		targets = append(targets, g)
	}
	return targets, nil
}

func hasGoCode() bool {
	entries, err := os.ReadDir(repoRoot() + "/pkg")
	if err != nil {
		return false
	}
	return len(entries) > 0
}

func printSummary(s selector, v *verdict, status string, exit int) int {
	// verdict.finalize fills in any "not-selected" tiers; the
	// summary just reads what's already there. Idempotent finalize
	// makes early-exit paths and the happy path emit the same map.
	v.fillNotSelected()
	switch s.output {
	case "json":
		fmt.Println(v.json())
		return exit
	case "tap":
		printTAP(v, status)
		return exit
	case "junit":
		printJUnit(v)
		return exit
	case "github-annotations":
		printGitHubAnnotations(v)
		return exit
	}

	fmt.Println()
	fmt.Printf("lenny-test verdict: %s\n", status)
	fmt.Printf("  run_id:      %s\n", v.RunID)
	fmt.Printf("  duration:    %.2fs\n", v.finishedAt.Sub(v.startedAt).Seconds())
	fmt.Printf("  verdict file: %s\n", s.verdictFile)
	for name, t := range v.Tiers {
		marker := "✓"
		switch t.Status {
		case verdictstatus.Fail:
			marker = "✗"
		case verdictstatus.Skipped:
			marker = "↷"
		case verdictstatus.Inconclusive, verdictstatus.Unverified:
			// Neither status establishes that the tier passed, so the
			// check mark would misreport it.
			marker = "?"
		}
		fmt.Printf("  %s %-13s %s", marker, name, t.Status)
		if t.Reason != "" {
			fmt.Printf("  (%s)", t.Reason)
		}
		fmt.Println()
	}
	return exit
}

// printTAP emits the verdict in TAP (Test Anything Protocol) v13
// format so downstream tooling can consume it without parsing the
// human-formatted summary.
func printTAP(v *verdict, _ string) {
	tiers := orderedTierNames(v)
	fmt.Println("TAP version 13")
	fmt.Printf("1..%d\n", len(tiers))
	for i, name := range tiers {
		t := v.Tiers[name]
		switch t.Status {
		case verdictstatus.Pass:
			fmt.Printf("ok %d - %s\n", i+1, name)
		case verdictstatus.Fail:
			fmt.Printf("not ok %d - %s\n", i+1, name)
			if t.Reason != "" {
				fmt.Printf("  ---\n  message: %s\n  ...\n", escapeTAPReason(t.Reason))
			}
		case verdictstatus.Unverified:
			// A tier that reached no conclusion proved nothing, so it
			// reports as a failing point rather than falling through to
			// the passing default below.
			fmt.Printf("not ok %d - %s # unverified %s\n", i+1, name, escapeTAPReason(t.Reason))
		case verdictstatus.Skipped:
			fmt.Printf("ok %d - %s # SKIP %s\n", i+1, name, t.Reason)
		default:
			fmt.Printf("ok %d - %s # %s\n", i+1, name, t.Status)
		}
	}
}

// printJUnit emits the verdict as a JUnit-compatible XML document.
// Downstream tooling (GitLab, Buildkite, Bazel test summary) consumes
// this format directly. JUnit has no "not-selected" element; we map
// not-selected and skipped through <skipped> with the reason text.
func printJUnit(v *verdict) {
	tiers := orderedTierNames(v)
	failures := 0
	errors := 0
	skipped := 0
	for _, n := range tiers {
		switch v.Tiers[n].Status {
		case verdictstatus.Fail:
			failures++
		case verdictstatus.Inconclusive, verdictstatus.Unverified:
			// Neither established that the tier passed, so both count
			// against the suite. A JUnit consumer reads a testcase with
			// no child element as a pass.
			errors++
		case verdictstatus.Skipped, verdictstatus.NotSelected:
			skipped++
		}
	}
	fmt.Println(`<?xml version="1.0" encoding="UTF-8"?>`)
	fmt.Printf(`<testsuite name="lenny-test" tests="%d" failures="%d" errors="%d" skipped="%d">`+"\n",
		len(tiers), failures, errors, skipped)
	for _, name := range tiers {
		t := v.Tiers[name]
		dur := float64(t.DurationMS) / 1000.0
		fmt.Printf(`  <testcase classname="lenny-test" name="%s" time="%.3f">`+"\n", name, dur)
		switch t.Status {
		case verdictstatus.Fail:
			fmt.Printf(`    <failure message="%s"/>`+"\n", escapeXML(t.Reason))
		case verdictstatus.Inconclusive:
			fmt.Printf(`    <error message="%s"/>`+"\n", escapeXML(t.Reason))
		case verdictstatus.Unverified:
			fmt.Printf(`    <error message="unverified: %s"/>`+"\n", escapeXML(t.Reason))
		case verdictstatus.Skipped:
			fmt.Printf(`    <skipped message="%s"/>`+"\n", escapeXML(t.Reason))
		case verdictstatus.NotSelected:
			fmt.Printf(`    <skipped message="not-selected: %s"/>`+"\n", escapeXML(t.Reason))
		}
		fmt.Println(`  </testcase>`)
	}
	fmt.Println(`</testsuite>`)
}

// printGitHubAnnotations emits the verdict as GitHub Actions
// workflow commands so failures appear inline on the PR. Iterates
// tiers in canonical order so the PR comment stream is stable.
func printGitHubAnnotations(v *verdict) {
	fail := 0
	inconclusive := 0
	unverified := 0
	skipped := 0
	for _, name := range orderedTierNames(v) {
		t := v.Tiers[name]
		switch t.Status {
		case verdictstatus.Fail:
			fail++
			fmt.Printf("::error title=lenny-test tier %s::%s\n", name, oneLine(t.Reason))
		case verdictstatus.Inconclusive:
			inconclusive++
			fmt.Printf("::warning title=lenny-test tier %s::%s\n", name, oneLine(t.Reason))
		case verdictstatus.Unverified:
			unverified++
			fmt.Printf("::error title=lenny-test tier %s::unverified: %s\n", name, oneLine(t.Reason))
		case verdictstatus.Skipped:
			skipped++
			fmt.Printf("::notice title=lenny-test tier %s::%s\n", name, oneLine(t.Reason))
		}
	}
	// Final summary line. Without it, a clean run prints nothing,
	// which is indistinguishable from "the harness didn't run."
	summary := fmt.Sprintf("verdict=%s fail=%d inconclusive=%d unverified=%d skipped=%d run_id=%s",
		v.Verdict, fail, inconclusive, unverified, skipped, v.RunID)
	if v.Verdict == verdictstatus.VerdictPass {
		fmt.Printf("::notice title=lenny-test summary::%s\n", summary)
	} else {
		fmt.Printf("::error title=lenny-test summary::%s\n", summary)
	}
}

// orderedTierNames returns the tier names in a stable order so the
// emitted output is deterministic across runs.
func orderedTierNames(v *verdict) []string {
	preferred := allTiers()
	out := make([]string, 0, len(v.Tiers))
	seen := map[string]bool{}
	for _, n := range preferred {
		if _, ok := v.Tiers[n]; ok {
			out = append(out, n)
			seen[n] = true
		}
	}
	for n := range v.Tiers {
		if !seen[n] {
			out = append(out, n)
		}
	}
	return out
}

func escapeTAPReason(s string) string {
	// TAP YAML diagnostic block — keep it single-line.
	return oneLine(s)
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return oneLine(s)
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

func splitNonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func repoRoot() string {
	// Walk upward looking for go.mod.
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return cwd
		}
		dir = parent
	}
}

// dropTestdataLines removes every line of a tool's output whose leading
// path lies under a testdata/ directory, and returns what is left with
// surrounding whitespace trimmed.
//
// A testdata/ tree is fixture material rather than compiled source. The go
// tool ignores it by convention, and a fixture is frequently malformed on
// purpose: scripts/specshift's confinement cases, for one, prove that a
// confined run never opens a carrier outside its confinement by making that
// carrier unparseable, so a run that opened it would fail. A repository-wide
// formatter walks those files anyway and reports them, which would fail a
// static-tier check on a fixture doing its job. Filtering by path keeps the check
// exact for real source.
func dropTestdataLines(out string) string {
	var kept []string
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		path := trimmed
		if i := strings.IndexByte(path, ':'); i >= 0 {
			path = path[:i]
		}
		path = filepath.ToSlash(path)
		if path == "testdata" || strings.HasPrefix(path, "testdata/") ||
			strings.Contains(path, "/testdata/") || strings.HasSuffix(path, "/testdata") {
			continue
		}
		kept = append(kept, trimmed)
	}
	return strings.Join(kept, "\n")
}
