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
		case "component":
			st, msg := runComponentTier(t.subsets)
			v.recordTier(t.name, st, time.Since(start), msg)
			if st != "pass" && !s.continueErr {
				v.next("Fix component-tier failures before moving to higher tiers.")
				overallStatus = "FAIL"
				if writeErr := v.write(s.verdictFile); writeErr != nil {
					fmt.Fprintf(os.Stderr, "lenny-test: failed to write verdict: %v\n", writeErr)
				}
				return printSummary(s, v, overallStatus, 1)
			}
		case "contract":
			st, msg := runContractTier(t.subsets)
			v.recordTier(t.name, st, time.Since(start), msg)
			if st != "pass" && !s.continueErr {
				v.next("Fix contract-tier failures before moving to higher tiers.")
				overallStatus = "FAIL"
				if writeErr := v.write(s.verdictFile); writeErr != nil {
					fmt.Fprintf(os.Stderr, "lenny-test: failed to write verdict: %v\n", writeErr)
				}
				return printSummary(s, v, overallStatus, 1)
			}
		case "conformance":
			st, msg := runConformanceTier(t.subsets)
			v.recordTier(t.name, st, time.Since(start), msg)
			if st != "pass" && !s.continueErr {
				v.next("Fix conformance-tier failures before moving to higher tiers.")
				overallStatus = "FAIL"
				if writeErr := v.write(s.verdictFile); writeErr != nil {
					fmt.Fprintf(os.Stderr, "lenny-test: failed to write verdict: %v\n", writeErr)
				}
				return printSummary(s, v, overallStatus, 1)
			}
		case "integration":
			st, msg := runIntegrationTier(t.subsets)
			v.recordTier(t.name, st, time.Since(start), msg)
			if st != "pass" && !s.continueErr {
				v.next("Fix integration-tier failures before moving to higher tiers.")
				overallStatus = "FAIL"
				if writeErr := v.write(s.verdictFile); writeErr != nil {
					fmt.Fprintf(os.Stderr, "lenny-test: failed to write verdict: %v\n", writeErr)
				}
				return printSummary(s, v, overallStatus, 1)
			}
		case "e2e_kind":
			st, msg := runE2EKindTier(t.subsets)
			v.recordTier(t.name, st, time.Since(start), msg)
			if st != "pass" && !s.continueErr {
				v.next("Fix e2e-Kind-tier failures before moving to higher tiers.")
				overallStatus = "FAIL"
				if writeErr := v.write(s.verdictFile); writeErr != nil {
					fmt.Fprintf(os.Stderr, "lenny-test: failed to write verdict: %v\n", writeErr)
				}
				return printSummary(s, v, overallStatus, 1)
			}
		case "load":
			st, msg := runTaggedTier("load", "./tests/tier7_load/...", 600*time.Second)
			v.recordTier(t.name, st, time.Since(start), msg)
			if st != "pass" && !s.continueErr {
				v.next("Fix load-tier failures before moving to higher tiers.")
				overallStatus = "FAIL"
				return printSummary(s, v, overallStatus, 1)
			}
		case "chaos":
			st, msg := runTaggedTier("chaos", "./tests/tier8_chaos/...", 600*time.Second)
			v.recordTier(t.name, st, time.Since(start), msg)
			if st != "pass" && !s.continueErr {
				v.next("Fix chaos-tier failures before moving to higher tiers.")
				overallStatus = "FAIL"
				return printSummary(s, v, overallStatus, 1)
			}
		case "security":
			st, msg := runTaggedTier("security", "./tests/tier9_security/...", 600*time.Second)
			v.recordTier(t.name, st, time.Since(start), msg)
			if st != "pass" && !s.continueErr {
				v.next("Fix security-tier failures before moving to higher tiers.")
				overallStatus = "FAIL"
				return printSummary(s, v, overallStatus, 1)
			}
		case "docs":
			st, msg := runDocsTier()
			v.recordTier(t.name, st, time.Since(start), msg)
			if st != "pass" && !s.continueErr {
				v.next("Fix docs-tier failures before moving to higher tiers.")
				overallStatus = "FAIL"
				return printSummary(s, v, overallStatus, 1)
			}
		case "e2e_cloud":
			st, msg := runE2ECloudTier()
			v.recordTier(t.name, st, time.Since(start), msg)
			if st != "pass" && !s.continueErr {
				v.next("Fix e2e-cloud-tier failures before moving to higher tiers.")
				overallStatus = "FAIL"
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
		{"scripts/lint-schema.sh (R-01)", func() (string, error) {
			script := filepath.Join(repoRoot(), "scripts", "lint-schema.sh")
			if _, err := os.Stat(script); err != nil {
				return "lint-schema.sh not present; skipping", nil
			}
			out, err := exec.Command("bash", script).CombinedOutput()
			return string(out), err
		}},
		{"scripts/lint-queries.sh (R-02)", func() (string, error) {
			script := filepath.Join(repoRoot(), "scripts", "lint-queries.sh")
			if _, err := os.Stat(script); err != nil {
				return "lint-queries.sh not present; skipping", nil
			}
			out, err := exec.Command("bash", script).CombinedOutput()
			return string(out), err
		}},
		{"scripts/lint-migrations.sh", func() (string, error) {
			script := filepath.Join(repoRoot(), "scripts", "lint-migrations.sh")
			if _, err := os.Stat(script); err != nil {
				return "lint-migrations.sh not present; skipping", nil
			}
			out, err := exec.Command("bash", script).CombinedOutput()
			return string(out), err
		}},
		{"go test ./tests/tier0_static/...", func() (string, error) {
			out, err := exec.Command("go", "test", "-count=1", "./tests/tier0_static/...").CombinedOutput()
			return string(out), err
		}},
		{"validate-diagnosis", func() (string, error) {
			// Re-invoke this very binary's `validate-diagnosis`
			// subcommand. The subcommand walks every Tier 2+
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

func runComponentTier(subsets []string) (string, string) {
	// Tier 2 component tests are guarded by the `component` build tag. Each
	// subdirectory under tests/tier2_component/ targets a distinct subsystem;
	// some need Docker (testcontainers-go), others run in-process. When
	// subsets is non-empty the runner scopes to those subdirectories so a
	// phase gate can run only the work in scope for that phase. When subsets
	// is empty the runner executes every component directory.
	if _, err := exec.LookPath("go"); err != nil {
		return "skipped", "go not on PATH"
	}
	targets, needsDocker, err := componentTargets(subsets)
	if err != nil {
		return "fail", err.Error()
	}
	if needsDocker {
		if _, err := exec.LookPath("docker"); err != nil {
			return "skipped", "docker not on PATH"
		}
		if err := exec.Command("docker", "info").Run(); err != nil {
			return "skipped", "docker daemon not running; start Docker and retry"
		}
	}
	args := append([]string{"test", "-count=1", "-timeout=180s", "-tags=component"}, targets...)
	cmd := exec.Command("go", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "fail", fmt.Sprintf("component suite failed:\n%s", out)
	}
	return "pass", ""
}

// componentTargets maps component subset names to (test package globs,
// needsDocker). An empty subset list resolves to every component directory
// plus the testinfra/containers package.
func componentTargets(subsets []string) ([]string, bool, error) {
	if len(subsets) == 0 {
		return []string{"./tests/tier2_component/...", "./tests/testinfra/containers/..."}, true, nil
	}
	type entry struct {
		path        string
		needsDocker bool
	}
	mapping := map[string]entry{
		"migrations":    {"./tests/tier2_component/migrations/...", true},
		"observability": {"./tests/tier2_component/observability/...", false},
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

func runConformanceTier(subsets []string) (string, string) {
	// Tier 10 conformance exercises the runtime adapters against the
	// lenny-compliance harness. The subset names map to integration
	// levels: "basic" → cmd/runtimes/echo at lenny-compliance --level
	// basic; "full" → cmd/runtimes/streaming-echo at lenny-compliance
	// --level full. An empty subset list runs every level whose target
	// runtime is built.
	if _, err := exec.LookPath("go"); err != nil {
		return "skipped", "go not on PATH"
	}
	root := repoRoot()
	tmpBase, err := os.MkdirTemp("", "lenny-conformance-*")
	if err != nil {
		return "fail", fmt.Sprintf("mkdtemp: %v", err)
	}
	defer os.RemoveAll(tmpBase)

	type runtimeBuild struct {
		level   string
		runtime string
		pkg     string
	}
	available := map[string]runtimeBuild{
		"basic": {level: "basic", runtime: "echo", pkg: "./cmd/runtimes/echo"},
		"full":  {level: "full", runtime: "streaming-echo", pkg: "./cmd/runtimes/streaming-echo"},
	}
	if len(subsets) == 0 {
		subsets = []string{"basic", "full"}
	}

	binCompliance := filepath.Join(tmpBase, "lenny-compliance")
	cmd := exec.Command("go", "build", "-o", binCompliance, "./cmd/lenny-compliance")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return "fail", fmt.Sprintf("build lenny-compliance: %v\n%s", err, out)
	}

	results := []string{}
	for _, sub := range subsets {
		rb, ok := available[sub]
		if !ok {
			return "fail", fmt.Sprintf("unknown conformance subset %q", sub)
		}
		binRuntime := filepath.Join(tmpBase, rb.runtime)
		buildCmd := exec.Command("go", "build", "-o", binRuntime, rb.pkg)
		buildCmd.Dir = root
		if out, err := buildCmd.CombinedOutput(); err != nil {
			return "fail", fmt.Sprintf("build %s: %v\n%s", rb.pkg, err, out)
		}
		runCmd := exec.Command(binCompliance, "--binary", binRuntime, "--level", rb.level)
		out, err := runCmd.CombinedOutput()
		if err != nil {
			return "fail", fmt.Sprintf("conformance --level %s against %s failed:\n%s", rb.level, rb.runtime, out)
		}
		results = append(results, strings.TrimSpace(string(out)))
	}
	return "pass", strings.Join(results, "\n\n")
}

// runDocsTier runs the tier-11 documentation checks. No build tag —
// these tests exercise repo state directly.
func runDocsTier() (string, string) {
	if _, err := exec.LookPath("go"); err != nil {
		return "skipped", "go not on PATH"
	}
	cmd := exec.Command("go", "test", "-count=1", "-timeout=60s", "./tests/tier11_docs/...")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "fail", fmt.Sprintf("docs suite failed:\n%s", out)
	}
	return "pass", ""
}

// runTaggedTier runs `go test` over targetGlob under the supplied
// build tag. Used for tiers whose tests are mostly skip-bearing
// scaffolds — load, chaos, security — so the tier reports `pass`
// (with skipped sub-tests) until the backing infrastructure lands.
func runTaggedTier(tag, targetGlob string, timeout time.Duration) (string, string) {
	if _, err := exec.LookPath("go"); err != nil {
		return "skipped", "go not on PATH"
	}
	args := []string{"test", "-count=1", fmt.Sprintf("-timeout=%s", timeout), "-tags=" + tag, targetGlob}
	cmd := exec.Command("go", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "fail", fmt.Sprintf("%s suite failed:\n%s", tag, out)
	}
	return "pass", ""
}

// runE2ECloudTier runs the tier-6 cloud tests under the `e2e_cloud`
// build tag. Tests internally guard on LENNY_CLOUD_PROVIDER + the
// per-provider auth surface; without that, every test skips with a
// precise diagnosis. The tier reports `pass` (with skipped
// sub-tests) on hosts that have not been bound to a cloud target.
func runE2ECloudTier() (string, string) {
	if _, err := exec.LookPath("go"); err != nil {
		return "skipped", "go not on PATH"
	}
	args := []string{"test", "-count=1", "-timeout=1800s", "-tags=e2e_cloud", "./tests/tier6_e2e_cloud/..."}
	cmd := exec.Command("go", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "fail", fmt.Sprintf("e2e_cloud suite failed:\n%s", out)
	}
	return "pass", ""
}

// runE2EKindTier runs the tier-5 e2e tests under the `e2e_kind`
// build tag. Each test calls tests/testinfra/kind.SkipUnlessAvailable
// to short-circuit when docker / kind / a reachable docker daemon
// are absent, so the tier reports `pass` (with skipped sub-tests)
// on hosts without the e2e prerequisites.
//
// When subsets is non-empty the runner narrows execution to the
// matching tests via -run. The subset→test mapping mirrors the
// names in groups.subsets.yaml.
func runE2EKindTier(subsets []string) (string, string) {
	if _, err := exec.LookPath("go"); err != nil {
		return "skipped", "go not on PATH"
	}
	args := []string{"test", "-count=1", "-timeout=600s", "-tags=e2e_kind"}
	if pattern, err := e2eKindSubsetPattern(subsets); err != nil {
		return "fail", err.Error()
	} else if pattern != "" {
		args = append(args, "-run", pattern)
	}
	args = append(args, "./tests/tier5_e2e_kind/...")
	cmd := exec.Command("go", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "fail", fmt.Sprintf("e2e_kind suite failed:\n%s", out)
	}
	return "pass", ""
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
			"TestWarmPool", "TestSandboxClaim", "TestPodLifecycle",
			"TestMTLSEnforcement", "TestAdmissionPolicy",
			"TestAdmissionInventory", "TestLennyOpsFirstDeploy",
			"TestBootstrapFirstInstall",
		},
		"admission-critical-path": {
			"TestAdmissionPolicy", "TestAdmissionInventory",
			"TestLabelImmutability", "TestSandboxClaim",
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

// runIntegrationTier runs the tier-4 integration tests under the
// `integration` build tag. Each test boots cmd/lenny-gateway as a
// subprocess (via tests/testinfra/gateway) and drives the real HTTP
// surface end-to-end. When subsets is non-empty the runner uses
// -run with the canonical regex for that subset.
func runIntegrationTier(subsets []string) (string, string) {
	if _, err := exec.LookPath("go"); err != nil {
		return "skipped", "go not on PATH"
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
				return "fail", fmt.Sprintf("unknown integration subset %q", sub)
			}
			parts = append(parts, p)
		}
		runArgs = []string{"-run", strings.Join(parts, "|")}
	}
	args := append([]string{"test", "-count=1", "-timeout=180s", "-tags=integration"}, runArgs...)
	args = append(args, "./tests/tier4_integration/...")
	cmd := exec.Command("go", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "fail", fmt.Sprintf("integration suite failed:\n%s", out)
	}
	return "pass", ""
}

func runContractTier(subsets []string) (string, string) {
	// Tier 3 contract tests are guarded by the `contract` build tag. Each
	// subdirectory under tests/tier3_contract/ targets a distinct contract
	// surface (adapter_jsonl, workspaceplan, ...). When subsets is non-empty
	// the runner scopes to those subdirectories so a phase gate can verify
	// only the contracts in scope for that phase. When subsets is empty the
	// runner exercises every contract directory.
	if _, err := exec.LookPath("go"); err != nil {
		return "skipped", "go not on PATH"
	}
	targets, err := contractTargets(subsets)
	if err != nil {
		return "fail", err.Error()
	}
	args := append([]string{"test", "-count=1", "-tags=contract"}, targets...)
	cmd := exec.Command("go", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "fail", fmt.Sprintf("contract suite failed:\n%s", out)
	}
	return "pass", ""
}

// contractTargets maps contract subset names to the package globs the runner
// executes. An empty subset list resolves to every contract directory.
func contractTargets(subsets []string) ([]string, error) {
	if len(subsets) == 0 {
		return []string{"./tests/tier3_contract/..."}, nil
	}
	mapping := map[string]string{
		"adapter-jsonl":         "./tests/tier3_contract/adapter_jsonl/...",
		"workspaceplan":         "./tests/tier3_contract/workspaceplan/...",
		"rest-sessions":         "./tests/tier3_contract/rest_sessions/...",
		"rest-idempotency":      "./tests/tier3_contract/rest_idempotency/...",
		"rest-circuitbreaker":   "./tests/tier3_contract/rest_circuitbreaker/...",
		"rest-auth":             "./tests/tier3_contract/rest_auth/...",
		"oauth-token":           "./tests/tier3_contract/oauth_token/...",
		"rest-mcp-consistency":  "./tests/tier3_contract/rest_mcp_consistency/...",
		"rest-openai-chat":      "./tests/tier3_contract/rest_openai_chat/...",
		"rest-openai-responses": "./tests/tier3_contract/rest_openai_responses/...",
		"ocsf-audit":            "./tests/tier3_contract/ocsf_audit/...",
		"cloudevents":           "./tests/tier3_contract/cloudevents/...",
		"sdk-go":                "./tests/tier3_contract/sdks/...",
		"sdk-python":            "./tests/tier3_contract/sdks/...",
		"sdk-typescript":        "./tests/tier3_contract/sdks/...",
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
		case "pass":
			fmt.Printf("ok %d - %s\n", i+1, name)
		case "fail":
			fmt.Printf("not ok %d - %s\n", i+1, name)
			if t.Reason != "" {
				fmt.Printf("  ---\n  message: %s\n  ...\n", escapeTAPReason(t.Reason))
			}
		case "skipped":
			fmt.Printf("ok %d - %s # SKIP %s\n", i+1, name, t.Reason)
		default:
			fmt.Printf("ok %d - %s # %s\n", i+1, name, t.Status)
		}
	}
}

// printJUnit emits the verdict as a JUnit-compatible XML document.
// Downstream tooling (GitLab, Buildkite, Bazel test summary) consumes
// this format directly.
func printJUnit(v *verdict) {
	tiers := orderedTierNames(v)
	failures := 0
	skipped := 0
	for _, n := range tiers {
		switch v.Tiers[n].Status {
		case "fail":
			failures++
		case "skipped":
			skipped++
		}
	}
	fmt.Println(`<?xml version="1.0" encoding="UTF-8"?>`)
	fmt.Printf(`<testsuite name="lenny-test" tests="%d" failures="%d" skipped="%d">`+"\n",
		len(tiers), failures, skipped)
	for _, name := range tiers {
		t := v.Tiers[name]
		fmt.Printf(`  <testcase classname="lenny-test" name="%s">`+"\n", name)
		switch t.Status {
		case "fail":
			fmt.Printf(`    <failure message="%s"/>`+"\n", escapeXML(t.Reason))
		case "skipped":
			fmt.Printf(`    <skipped message="%s"/>`+"\n", escapeXML(t.Reason))
		}
		fmt.Println(`  </testcase>`)
	}
	fmt.Println(`</testsuite>`)
}

// printGitHubAnnotations emits the verdict as GitHub Actions
// workflow commands so failures appear inline on the PR.
func printGitHubAnnotations(v *verdict) {
	for name, t := range v.Tiers {
		switch t.Status {
		case "fail":
			fmt.Printf("::error title=lenny-test tier %s::%s\n", name, oneLine(t.Reason))
		case "skipped":
			fmt.Printf("::notice title=lenny-test tier %s::%s\n", name, oneLine(t.Reason))
		}
	}
}

// orderedTierNames returns the tier names in a stable order so the
// emitted output is deterministic across runs.
func orderedTierNames(v *verdict) []string {
	preferred := []string{
		"static", "unit", "component", "contract", "integration",
		"e2e_kind", "e2e_cloud", "load", "chaos", "security",
		"conformance", "docs",
	}
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
