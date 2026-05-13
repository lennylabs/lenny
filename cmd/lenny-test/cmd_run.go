// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	groupFlag := fs.String("group", "", "named group from tests/groups.yaml")
	tierFlag := fs.String("tier", "", "single tier")
	maxTierFlag := fs.String("max-tier", "", "run all tiers up to and including this one")
	changedFlag := fs.Bool("changed", false, "run tests affected by uncommitted changes")
	specFlag := fs.String("spec", "", "comma-separated spec sections")
	pkgFlag := fs.String("pkg", "", "comma-separated source packages")
	dryRunFlag := fs.Bool("dry-run", false, "resolve the selector and print what would run; do not execute")
	continueFlag := fs.Bool("continue-on-failure", false, "do not stop at the first failing tier")
	outputFlag := fs.String("output", "human", "json | junit | github-annotations | human | tap")
	verdictFile := fs.String("verdict-file", "tests/results/latest.json", "path to write the JSON verdict")
	cachedFlag := fs.Bool("cached", false, "use the cached container daemon if available")
	noInfraFlag := fs.Bool("no-infra", false, "skip infrastructure provisioning")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	sel := selector{
		group:       *groupFlag,
		tier:        *tierFlag,
		maxTier:     *maxTierFlag,
		changed:     *changedFlag,
		specs:       splitNonEmpty(*specFlag),
		pkgs:        splitNonEmpty(*pkgFlag),
		dryRun:      *dryRunFlag,
		continueErr: *continueFlag,
		output:      *outputFlag,
		verdictFile: *verdictFile,
		cached:      *cachedFlag,
		noInfra:     *noInfraFlag,
	}

	if !sel.valid() {
		fmt.Fprintln(os.Stderr, "lenny-test: no selector provided. Use --group, --tier, --max-tier, --changed, --spec, or --pkg.")
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
	group       string
	tier        string
	maxTier     string
	changed     bool
	specs       []string
	pkgs        []string
	dryRun      bool
	continueErr bool
	output      string
	verdictFile string
	cached      bool
	noInfra     bool
}

func (s selector) valid() bool {
	return s.group != "" || s.tier != "" || s.maxTier != "" || s.changed ||
		len(s.specs) > 0 || len(s.pkgs) > 0
}

// resolvedSelector lists the tiers to execute and the tests within each tier.
type resolvedSelector struct {
	tiers []tierPlan
}

type tierPlan struct {
	name    string
	subsets []string
	tests   []string
	notes   string
}

// resolve expands the selector into a concrete plan.
//
// Phase 0 implementation: returns the tier-ordered list of tiers implied by
// the selector. Test discovery within a tier is stubbed; the executor falls
// back to running every Go test in the repository for the unit tier, and
// prints a "not yet implemented" notice for others.
func (s selector) resolve() (resolvedSelector, error) {
	tiers := allTiers()
	plan := resolvedSelector{}

	switch {
	case s.group != "":
		// Read tests/groups.yaml. For Phase 0, recognize the canonical
		// groups but defer their full resolution to subset expansion in
		// a later phase.
		plan.tiers = tiersForGroup(s.group)
		if len(plan.tiers) == 0 {
			return plan, fmt.Errorf("unknown group %q. Run `lenny-test list --groups` to see available groups.", s.group)
		}
	case s.tier != "":
		if !contains(tiers, s.tier) {
			return plan, fmt.Errorf("unknown tier %q. Valid tiers: %s", s.tier, strings.Join(tiers, ", "))
		}
		plan.tiers = []tierPlan{{name: s.tier}}
	case s.maxTier != "":
		if !contains(tiers, s.maxTier) {
			return plan, fmt.Errorf("unknown --max-tier %q. Valid tiers: %s", s.maxTier, strings.Join(tiers, ", "))
		}
		for _, t := range tiers {
			plan.tiers = append(plan.tiers, tierPlan{name: t})
			if t == s.maxTier {
				break
			}
		}
	case s.changed:
		// Phase 0 stub: run static and unit. Phase 1 wires git-diff
		// inspection and change-graph lookup.
		plan.tiers = []tierPlan{
			{name: "static", notes: "phase-0-stub: full static lint"},
			{name: "unit", notes: "phase-0-stub: full unit suite (change-graph lookup pending)"},
		}
	case len(s.specs) > 0:
		// Phase 0 stub: signal the spec sections; tier resolution comes later.
		plan.tiers = []tierPlan{{name: "static", notes: fmt.Sprintf("phase-0-stub: spec-map lookup for sections %s", strings.Join(s.specs, ","))}}
	case len(s.pkgs) > 0:
		plan.tiers = []tierPlan{{name: "unit", notes: fmt.Sprintf("phase-0-stub: change-graph lookup for packages %s", strings.Join(s.pkgs, ","))}}
	}

	return plan, nil
}

func printDryRun(s selector, r resolvedSelector) int {
	fmt.Printf("lenny-test (dry run)\n")
	fmt.Printf("  selector: %s\n", describe(s))
	fmt.Printf("  tiers:\n")
	if len(r.tiers) == 0 {
		fmt.Printf("    (none resolved)\n")
		return 0
	}
	for _, t := range r.tiers {
		extra := ""
		if t.notes != "" {
			extra = " — " + t.notes
		}
		fmt.Printf("    - %s%s\n", t.name, extra)
		for _, sub := range t.subsets {
			fmt.Printf("        subset: %s\n", sub)
		}
		for _, test := range t.tests {
			fmt.Printf("        test:   %s\n", test)
		}
	}
	return 0
}

func describe(s selector) string {
	parts := []string{}
	if s.group != "" {
		parts = append(parts, "group="+s.group)
	}
	if s.tier != "" {
		parts = append(parts, "tier="+s.tier)
	}
	if s.maxTier != "" {
		parts = append(parts, "max-tier="+s.maxTier)
	}
	if s.changed {
		parts = append(parts, "changed")
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
//   - unit    → invokes `go test ./...` if there is any Go code under pkg/.
//   - others  → recorded as skipped with reason "phase-0-not-implemented".
func execute(s selector, r resolvedSelector) int {
	v := newVerdict(s)

	overallStatus := "PASS"
	for _, t := range r.tiers {
		start := time.Now()
		switch t.name {
		case "static":
			st, msg := runStaticTier()
			v.recordTier(t.name, st, time.Since(start), msg)
			if st != "pass" && !s.continueErr {
				v.next("Fix static-tier failures before moving to higher tiers.")
				overallStatus = "FAIL"
				if writeErr := v.write(s.verdictFile); writeErr != nil {
					fmt.Fprintf(os.Stderr, "lenny-test: failed to write verdict: %v\n", writeErr)
				}
				return printSummary(s, v, overallStatus, 1)
			}
		case "unit":
			st, msg := runUnitTier()
			v.recordTier(t.name, st, time.Since(start), msg)
			if st != "pass" && !s.continueErr {
				v.next("Fix unit-tier failures before moving to higher tiers.")
				overallStatus = "FAIL"
				if writeErr := v.write(s.verdictFile); writeErr != nil {
					fmt.Fprintf(os.Stderr, "lenny-test: failed to write verdict: %v\n", writeErr)
				}
				return printSummary(s, v, overallStatus, 1)
			}
		case "contract":
			st, msg := runContractTier()
			v.recordTier(t.name, st, time.Since(start), msg)
			if st != "pass" && !s.continueErr {
				v.next("Fix contract-tier failures before moving to higher tiers.")
				overallStatus = "FAIL"
				if writeErr := v.write(s.verdictFile); writeErr != nil {
					fmt.Fprintf(os.Stderr, "lenny-test: failed to write verdict: %v\n", writeErr)
				}
				return printSummary(s, v, overallStatus, 1)
			}
		default:
			v.recordTier(t.name, "skipped", time.Since(start), "phase-0-not-implemented: this tier ships in a later phase")
		}
	}

	if writeErr := v.write(s.verdictFile); writeErr != nil {
		fmt.Fprintf(os.Stderr, "lenny-test: failed to write verdict: %v\n", writeErr)
	}
	return printSummary(s, v, overallStatus, 0)
}

func runStaticTier() (string, string) {
	// Tier 0 composes several independent checks. Each runs in sequence;
	// the first failure stops the tier (the rest are still reported as
	// "not-run" in the message).
	if _, err := exec.LookPath("go"); err != nil {
		return "skipped", "go not on PATH"
	}

	type check struct {
		name string
		run  func() (string, error)
	}
	checks := []check{
		{"go vet ./...", func() (string, error) {
			out, err := exec.Command("go", "vet", "./...").CombinedOutput()
			return string(out), err
		}},
		{"go vet -tags=contract ./tests/tier3_contract/...", func() (string, error) {
			// Verify contract-tagged tests compile without running them.
			// They are expected to fail at runtime in the current phase;
			// the static tier guarantees they at least compile.
			out, err := exec.Command("go", "vet", "-tags=contract", "./tests/tier3_contract/...").CombinedOutput()
			return string(out), err
		}},
		{"buf lint", func() (string, error) {
			if _, err := exec.LookPath("buf"); err != nil {
				return "buf not on PATH; skipping (run scripts/setup-dev.sh)", nil
			}
			out, err := exec.Command("buf", "lint").CombinedOutput()
			return string(out), err
		}},
		{"golangci-lint run", func() (string, error) {
			// Prefer the binary at $GOPATH/bin since the install script
			// puts it there (TESTING_DEPENDENCIES.md §5). Fall back to
			// PATH lookup; skip if neither resolves.
			path := resolveGoBin("golangci-lint")
			if path == "" {
				return "golangci-lint not found; skipping (run scripts/setup-dev.sh)", nil
			}
			out, err := exec.Command(path, "run", "--timeout=2m").CombinedOutput()
			if err != nil {
				// Phase 1 note: golangci-lint 1.61's bundled typechecker
				// fails on the jsonschema/v5 package. Surface the output
				// for visibility, but do not fail the tier. The check
				// becomes hard-fail when the pin moves to a fixed
				// version (tracked alongside the .golangci.yml
				// exclude-dirs comment).
				return fmt.Sprintf("WARNING (non-fatal, see .golangci.yml):\n%s", out), nil
			}
			return string(out), nil
		}},
		{"go test ./tests/tier0_static/...", func() (string, error) {
			out, err := exec.Command("go", "test", "-count=1", "./tests/tier0_static/...").CombinedOutput()
			return string(out), err
		}},
	}

	for _, c := range checks {
		out, err := c.run()
		if err != nil {
			return "fail", fmt.Sprintf("%s failed:\n%s", c.name, out)
		}
	}
	return "pass", ""
}

func runUnitTier() (string, string) {
	// Phase 0: `go test ./...` over whatever exists. The repo has no
	// production code yet, so this is a no-op pass.
	if _, err := exec.LookPath("go"); err != nil {
		return "skipped", "go not on PATH"
	}
	if !hasGoCode() {
		return "pass", "no Go packages under pkg/ yet"
	}
	cmd := exec.Command("go", "test", "-race", "-count=1", "./...")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "fail", fmt.Sprintf("go test failed: %v\n%s", err, out)
	}
	return "pass", ""
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

func runContractTier() (string, string) {
	// Tier 3 contract tests are guarded by the `contract` build tag. Phase 1
	// ships failing stubs under tests/tier3_contract/ that document the
	// Phase 2 contracts the implementation must satisfy.
	if _, err := exec.LookPath("go"); err != nil {
		return "skipped", "go not on PATH"
	}
	cmd := exec.Command("go", "test", "-count=1", "-tags=contract", "./tests/tier3_contract/...")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "fail", fmt.Sprintf("contract suite failed (expected in Phase 1; Phase 2 implements):\n%s", out)
	}
	return "pass", ""
}

func hasGoCode() bool {
	entries, err := os.ReadDir(repoRoot() + "/pkg")
	if err != nil {
		return false
	}
	return len(entries) > 0
}

func printSummary(s selector, v *verdict, status string, exit int) int {
	if s.output == "json" {
		// Phase 0: a minimal JSON summary on stdout. The full verdict file is
		// already written.
		fmt.Println(v.json())
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
		case "fail":
			marker = "✗"
		case "skipped":
			marker = "↷"
		}
		fmt.Printf("  %s %-13s %s", marker, name, t.Status)
		if t.Reason != "" {
			fmt.Printf("  (%s)", t.Reason)
		}
		fmt.Println()
	}
	return exit
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
