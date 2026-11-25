// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
)

// This file holds `lenny-test gate --rerun-failed` (change 0172, decision
// 26). A gate run that failed only on individual tests, such as a test
// that times out under load, is followed by a rerun of those tests alone
// rather than of the whole gate. The rerun reads the latest gate run on
// the commit HEAD names, refuses unless that run failed only on tests it
// can rerun, runs each failed top-level test --rerun-count times in its
// tier, in the gate container like the gate, and writes a verdict that
// names the original run under gate.rerun_of and carries the original's
// selection, tier restriction, and coverage profile. The verdict is PASS
// when every run of every test passed, so `lenny-test coverage --diff
// --require-gate` accepts the profile through it (checkRerunOrigin).
//
// What a rerun accepts is a default of the step (its digest records it):
//
//   - The original run's verdict is FAIL, on a clean tree, with no reason
//     under gate.unverified, and it records the packages that failed
//     outside a test (gate.package_failures_recorded), which a run of an
//     earlier lenny-test does not.
//   - Every tier that did not pass is the unit, component, contract, or
//     integration tier, with status fail, a failure entry for each failed
//     test, and no package that failed outside a test: a build or setup
//     failure, a test binary that failed with no failed test, or a test
//     binary that exited, panicked, or timed out while a test ran, which
//     left the tests after it unrun.
//   - A failure of the static tier or the documentation gates is not
//     rerun. Their checks read the commit, so a rerun cannot change them.
//
// When the original run wrote a coverage profile and its unit tier
// failed, the profile lacks the lines the failed tests would have reached
// after the point where they failed. The first rerun of the failed unit
// tests writes a profile of its own, and when every rerun passes, it is
// merged into the original's profile (mergeCoverProfiles); the verdict
// records the merged profile's hash and names the original's under
// gate.rerun_of. The harness is contributor tooling, so this file carries
// no specification annotation.

// defaultRerunCount is how many times --rerun-failed runs each failed
// test when --rerun-count is not given. Every run must pass.
const defaultRerunCount = 3

// rerunTiers are the tiers whose failed tests --rerun-failed reruns, in
// the order it runs them. Their executors run test functions by name.
var rerunTiers = []string{tierUnit, tierComponent, tierContract, tierIntegration}

// gateRerun is the rerun section of a --rerun-failed verdict.
type gateRerun struct {
	// RunID is the gate run whose failed tests were rerun.
	RunID string `json:"run_id"`
	// Count is how many times each test ran, at most; a test's own Runs
	// is lower when an earlier run of its tier failed.
	Count int `json:"count"`
	// CoverProfileSHA256 is the hash the original run recorded for its
	// profile. It differs from gate.cover_profile_sha256 when the rerun
	// merged the coverage of the failed unit tests into the profile.
	CoverProfileSHA256 string `json:"cover_profile_sha256,omitempty"`
	// Tests lists each rerun test and its results.
	Tests []gateRerunTest `json:"tests"`
}

// gateRerunTest is one top-level test that --rerun-failed reran.
type gateRerunTest struct {
	Tier    string `json:"tier"`
	Package string `json:"package"`
	Test    string `json:"test"`
	// Runs is how many times the test ran, and Passed how many of those
	// runs it passed.
	Runs   int `json:"runs"`
	Passed int `json:"passed"`
}

// rerunOptions are the inputs of one --rerun-failed run.
type rerunOptions struct {
	root  string
	count int
	// commit is the commit the container script resolved HEAD to, or ""
	// for a run that takes the HEAD it reads.
	commit         string
	environment    string
	environmentErr error
}

// rerunJob is what a rerun runs: the original run, the plan of each tier
// it reruns, the tests, and the original's profile, or "" when the rerun
// does not touch a profile.
type rerunJob struct {
	orig    *verdict
	plans   map[string]tierPlan
	tests   []gateRerunTest
	profile string
}

// rerunConflictFlags are the gate flags --rerun-failed refuses: the rerun
// takes the selection, the tiers, the layout, and the coverage of the run
// it reruns.
var rerunConflictFlags = []string{"all", "base", "cover", "go-test-p", "layout", "rdeps-depth", "tier"}

// rerunFlagConflicts returns the flags of fs that --rerun-failed refuses,
// as the command line named them.
func rerunFlagConflicts(fs *flag.FlagSet) []string {
	var out []string
	fs.Visit(func(f *flag.Flag) {
		if contains(rerunConflictFlags, f.Name) {
			out = append(out, "--"+f.Name)
		}
	})
	sort.Strings(out)
	return out
}

// runGateRerun handles `lenny-test gate --rerun-failed`. On the host it
// refuses early what the container would refuse, then runs itself in the
// gate container (gateRerunScript); with --here it reruns the tests and
// writes the verdict. A refusal exits 2 and writes no verdict.
func runGateRerun(fs *flag.FlagSet, here bool, count int, rebuild, withSocket bool, capFlagSet bool, capGB int) int {
	if conflicts := rerunFlagConflicts(fs); len(conflicts) > 0 {
		fmt.Fprintf(os.Stderr, "lenny-test gate: --rerun-failed takes its selection, tiers, layout, and coverage from the gate run it reruns; drop %s\n", strings.Join(conflicts, ", "))
		return 2
	}
	if count < 1 {
		fmt.Fprintf(os.Stderr, "lenny-test gate: --rerun-count %d: each failed test runs at least once\n", count)
		return 2
	}
	root := repoRoot()
	head, dirty := gitState(root)
	commit := head
	if here && os.Getenv(gateEnvCommit) != "" {
		commit = os.Getenv(gateEnvCommit)
	}
	job, err := prepareRerun(root, commit, dirty)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test gate: --rerun-failed: %v\n", err)
		return 2
	}
	if here {
		o := rerunOptions{root: root, count: count, commit: os.Getenv(gateEnvCommit)}
		o.environment, o.environmentErr = gateEnvironment(root, gateImageMarker, os.Getenv)
		return finishGate(root, defaultGateRunner(root).rerunFailed(job, o))
	}
	cacheCap, err := resolveGateGoCacheCap(capFlagSet, capGB, os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test gate: %v\n", err)
		return 2
	}
	return runInGateContainer(root, gateContainerRun{
		label:    "lenny-test gate --rerun-failed",
		script:   gateRerunScript,
		name:     "lenny-test-gate",
		passArgs: []string{"--rerun-failed", "--rerun-count", strconv.Itoa(count)},
		hint:     "run `lenny-test gate --rerun-failed --here` on a Linux host with the gate's toolchain",
	}, rebuild, withSocket, cacheCap)
}

// gateRerunScript is gateContainerScript without the static tier's
// snapshot and the golangci-lint cache fill, which a rerun does not read:
// it resolves HEAD, compiles lenny-test from the commit, installs it
// read-only, and runs `lenny-test gate --here` with the rerun's flags.
var gateRerunScript = strings.NewReplacer(
	"\tprepare_snapshot 0:0\n\tsh -c \"$snapshot\"\n\tlock_snapshot\n\tsh -c \"$fill\" || fill_failed\n\tlock_lint_cache\n", "",
	"prepare_snapshot \"$uid:$gid\"\n"+
		"setpriv --reuid=\"$uid\" --regid=\"$gid\" --groups=\"$groups\" sh -c \"$snapshot\"\n"+
		"lock_snapshot\n"+
		"setpriv --reuid=\"$uid\" --regid=\"$gid\" --groups=\"$groups\" sh -c \"$fill\" || fill_failed\n"+
		"lock_lint_cache\n", "",
).Replace(gateContainerScript)

// prepareRerun finds the gate run a rerun on commit reruns and returns
// what the rerun runs, or why it refuses: a tree with uncommitted changes,
// no gate run on the commit, a run whose failures it cannot rerun
// (rerunPlans), or an original profile that a later command rewrote.
func prepareRerun(root, commit string, dirty bool) (rerunJob, error) {
	if dirty {
		return rerunJob{}, errors.New("the tree has uncommitted changes, so a rerun would not test the commit the gate ran on; commit or remove them first")
	}
	if commit == "" {
		return rerunJob{}, errors.New("git could not resolve HEAD")
	}
	orig, err := findRerunOriginal(root, commit)
	if err != nil {
		return rerunJob{}, err
	}
	plans, tests, err := rerunPlans(orig)
	if err != nil {
		return rerunJob{}, err
	}
	job := rerunJob{orig: orig, plans: plans, tests: tests}
	if orig.Gate.Cover && orig.Gate.CoverProfileSHA256 != "" {
		job.profile = orig.Gate.CoverProfile
		if !filepath.IsAbs(job.profile) {
			job.profile = filepath.Join(root, filepath.FromSlash(job.profile))
		}
		sum, err := fileSHA256(job.profile)
		if err != nil || sum != orig.Gate.CoverProfileSHA256 {
			return rerunJob{}, fmt.Errorf("the coverage profile %s is not the one gate run %s wrote (%v), so coverage --diff could not accept a rerun of it; run `lenny-test gate` again", job.profile, orig.RunID, profileMismatch(sum, err))
		}
	}
	return job, nil
}

// profileMismatch describes why a profile is not the recorded one.
func profileMismatch(sum string, err error) string {
	if err != nil {
		return err.Error()
	}
	return "a later command rewrote it: SHA-256 " + sum[:min(12, len(sum))]
}

// findRerunOriginal returns the latest gate run on commit that is not
// itself a rerun, read from its verdict file.
func findRerunOriginal(root, commit string) (*verdict, error) {
	id, err := latestGateRunOn(root, commit)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(filepath.Join(root, gateResultsDir, "gate-"+id+".json"))
	if err != nil {
		return nil, fmt.Errorf("read the verdict of gate run %s: %w", id, err)
	}
	var v verdict
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("parse the verdict of gate run %s: %w", id, err)
	}
	if v.RunID != id || v.Trigger.GitRevision != commit {
		return nil, fmt.Errorf("the verdict file of gate run %s names run %s on %s, not the run the history records on %s", id, v.RunID, shortCommit(v.Trigger.GitRevision), shortCommit(commit))
	}
	return &v, nil
}

// latestGateRunOn returns the identifier of the last gate run on commit in
// the run history that is not a rerun. A line that does not parse, or
// whose identifier has another form, is skipped, as readCoverHistory skips
// it.
func latestGateRunOn(root, commit string) (string, error) {
	f, err := os.Open(filepath.Join(root, gateResultsDir, verdictHistoryFile))
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("no gate run is recorded for %s; run `lenny-test gate` first", shortCommit(commit))
	}
	if err != nil {
		return "", fmt.Errorf("read the run history: %w", err)
	}
	defer f.Close()
	last := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var line historyLine
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		if line.TriggerMode == "gate" && line.GitRevision == commit && line.RerunOf == "" && runIDPattern.MatchString(line.RunID) {
			last = line.RunID
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("read the run history: %w", err)
	}
	if last == "" {
		return "", fmt.Errorf("no gate run is recorded for %s; run `lenny-test gate` first", shortCommit(commit))
	}
	return last, nil
}

// rerunPlans returns the plan of each tier whose failed tests a rerun of
// orig runs, with the tests, or why orig cannot be rerun (see the file
// comment for what a rerun accepts).
func rerunPlans(orig *verdict) (map[string]tierPlan, []gateRerunTest, error) {
	id := orig.RunID
	if orig.Gate == nil {
		return nil, nil, fmt.Errorf("run %s is not a gate run", id)
	}
	switch orig.Verdict {
	case verdictstatus.VerdictPass:
		return nil, nil, fmt.Errorf("gate run %s, the latest on %s, passed; there is nothing to rerun", id, shortCommit(orig.Trigger.GitRevision))
	case verdictstatus.VerdictFail:
	default:
		return nil, nil, fmt.Errorf("gate run %s is %s rather than FAIL; a rerun reruns failed tests, so fix what its verdict names and run `lenny-test gate` again", id, orig.Verdict)
	}
	g := orig.Gate
	switch {
	case g.Dirty:
		return nil, nil, fmt.Errorf("gate run %s ran on a tree with uncommitted changes; run `lenny-test gate` again", id)
	case len(g.Unverified) > 0:
		return nil, nil, fmt.Errorf("gate run %s could not stand for its commit even had its tests passed (%s); run `lenny-test gate` again", id, strings.Join(g.Unverified, "; "))
	case !g.PackageFailuresRecorded:
		return nil, nil, fmt.Errorf("gate run %s was written by a lenny-test that does not record the packages that failed outside a test, so its failures cannot be shown to be individual tests; run `lenny-test gate` again", id)
	}
	plans := map[string]tierPlan{}
	var tests []gateRerunTest
	for _, name := range allTiers() {
		t, ok := orig.Tiers[name]
		if !ok || t.Status == verdictstatus.Pass || t.Status == verdictstatus.NotSelected {
			continue
		}
		p, tierTests, err := rerunTierPlan(id, name, t)
		if err != nil {
			return nil, nil, err
		}
		plans[name] = p
		tests = append(tests, tierTests...)
	}
	if len(tests) == 0 {
		return nil, nil, fmt.Errorf("gate run %s failed with no failed test recorded in a tier whose tests a rerun can run", id)
	}
	return plans, tests, nil
}

// rerunTierPlan returns the plan that reruns the failed top-level tests of
// one tier of gate run id, or why the tier cannot be rerun.
func rerunTierPlan(id, name string, t tierStat) (tierPlan, []gateRerunTest, error) {
	switch {
	case name == tierStatic || name == tierDocs:
		return tierPlan{}, nil, fmt.Errorf("the %s tier of gate run %s failed (%s); its checks read the commit and a rerun cannot change them, so fix the failure", name, id, firstLine(t.Detail))
	case !contains(rerunTiers, name):
		return tierPlan{}, nil, fmt.Errorf("the %s tier of gate run %s failed, and --rerun-failed reruns only the %s tiers", name, id, strings.Join(rerunTiers, ", "))
	case t.Status != verdictstatus.Fail:
		return tierPlan{}, nil, fmt.Errorf("the %s tier of gate run %s is %s (%s); only a tier that failed on its tests can be rerun", name, id, t.Status, firstLine(t.Reason+t.Detail))
	case len(t.PackageFailures) > 0:
		return tierPlan{}, nil, fmt.Errorf("the %s tier of gate run %s has packages that failed outside a test (%s): a build or setup failure, a failure with no failed test, or a test binary that exited, panicked, or timed out before its tests ended, leaving later tests unrun; run `lenny-test gate` again", name, id, strings.Join(t.PackageFailures, ", "))
	case len(t.Failures) == 0 || t.Failed != len(t.Failures):
		return tierPlan{}, nil, fmt.Errorf("the %s tier of gate run %s failed with %d failed test(s) and %d failure record(s) (%s); a rerun needs a record of each failed test", name, id, t.Failed, len(t.Failures), firstLine(t.Detail))
	}
	byDir := map[string]map[string]bool{}
	seen := map[string]bool{}
	var tests []gateRerunTest
	for _, f := range t.Failures {
		if f.Test == "" || !strings.HasPrefix(f.Package, moduleImportPath+"/") {
			return tierPlan{}, nil, fmt.Errorf("the %s tier of gate run %s records a failure without a test or a package of this module (%q in %q)", name, id, f.Test, f.Package)
		}
		top, _, _ := strings.Cut(f.Test, "/")
		dir := "./" + strings.TrimPrefix(f.Package, moduleImportPath+"/")
		if byDir[dir] == nil {
			byDir[dir] = map[string]bool{}
		}
		byDir[dir][top] = true
		if key := passedKey(f.Package, top); !seen[key] {
			seen[key] = true
			tests = append(tests, gateRerunTest{Tier: name, Package: f.Package, Test: top})
		}
	}
	p := tierPlan{name: name, pkgs: []string{}, funcs: map[string][]string{}, noCoverage: true}
	for dir, names := range byDir {
		p.funcs[dir] = sortedKeys(names)
	}
	return p, tests, nil
}

// rerunFailed reruns the failed tests of job.orig and returns the verdict.
// Each tier of the plan runs its tests job's count times, stopping at the
// first run that fails or leaves a named test without a pass. The tiers the
// original run passed are recorded as passed in it. The verdict passes when
// every run of every test passed in the gate image on a clean tree whose
// HEAD did not move.
func (g gateRunner) rerunFailed(job rerunJob, o rerunOptions) *verdict {
	v := newVerdict(selector{})
	v.Command = fmt.Sprintf("lenny-test gate --rerun-failed --rerun-count %d", o.count)
	v.Trigger.Mode = "gate"
	head, dirty := g.state()
	commit := o.commit
	if commit == "" {
		commit = head
	}
	v.Trigger.GitRevision = commit
	og := job.orig.Gate
	v.Gate = &gateRecord{
		Base: og.Base, MergeBase: og.MergeBase, IntegrationMergeBase: og.IntegrationMergeBase,
		Dirty: dirty, Environment: o.environment, All: og.All, RdepsDepth: og.RdepsDepth,
		Layout: og.Layout, GoTestP: og.GoTestP, RestrictedTo: og.RestrictedTo,
		Cover: og.Cover, CoverProfile: og.CoverProfile, CoverProfileSHA256: og.CoverProfileSHA256,
		Selection: og.Selection, PackageFailuresRecorded: true,
		RerunOf: &gateRerun{RunID: job.orig.RunID, Count: o.count, CoverProfileSHA256: og.CoverProfileSHA256},
	}
	for name, t := range job.orig.Tiers {
		if t.Status == verdictstatus.Pass {
			t.Detail = "passed in gate run " + job.orig.RunID
			v.Tiers[name] = t
		}
	}
	tests := append([]gateRerunTest{}, job.tests...)
	partial := ""
	if _, ok := job.plans[tierUnit]; ok && job.profile != "" {
		partial = filepath.Join(filepath.Dir(job.profile), "cover-rerun-"+v.RunID+".out")
		defer os.Remove(partial)
	}
	start := time.Now()
	for _, name := range rerunTiers {
		p, ok := job.plans[name]
		if !ok {
			continue
		}
		profile := ""
		if name == tierUnit {
			profile = partial
		}
		g.rerunTier(v, job.orig.RunID, p, o.count, profile, tests)
	}
	v.Gate.WallTimeMS = time.Since(start).Milliseconds()
	v.Gate.RerunOf.Tests = tests
	if o.commit != "" && head != o.commit {
		v.Gate.Unverified = append(v.Gate.Unverified, fmt.Sprintf("HEAD was %s when the rerun started, and the container resolved it to %s; run the rerun again", shortCommit(head), shortCommit(o.commit)))
	}
	if head2, dirty2 := g.state(); head2 != head || dirty2 != dirty {
		v.Gate.Unverified = append(v.Gate.Unverified, fmt.Sprintf("HEAD or the tree changed while the tests ran (HEAD %s to %s, uncommitted changes %t to %t); run the rerun again", shortCommit(head), shortCommit(head2), dirty, dirty2))
	}
	withholdGatePass(v, dirty, o.environmentErr, nil)
	// The merge rewrites the profile, so it runs only for a verdict that
	// is still PASS after every check: a rerun that fails or cannot stand
	// for the commit leaves the original profile for the next rerun.
	if partial != "" && v.Verdict == verdictstatus.VerdictPass {
		sum, err := mergeRerunCoverage(job.profile, partial, og.CoverProfileSHA256, o.root)
		if err != nil {
			v.recordTier(tierUnit, verdictstatus.Fail, 0, err.Error())
		} else {
			v.Gate.CoverProfileSHA256 = sum
		}
	}
	return v
}

// rerunTier runs the plan of one tier up to count times and records the
// tier. tests are every rerun test; rerunTier counts the runs and passes
// of the ones in this tier. The first run of the unit tier writes the
// coverage profile profile, when it is not "".
func (g gateRunner) rerunTier(v *verdict, origID string, p tierPlan, count int, profile string, tests []gateRerunTest) {
	exec, ok := g.exec[p.name]
	if !ok {
		g.notRun(v, p.name, fmt.Errorf("the merge gate does not run the %s tier", p.name))
		return
	}
	if check := g.precheck[p.name]; check != nil {
		if err := check(); err != nil {
			g.notRun(v, p.name, err)
			return
		}
	}
	fmt.Fprintf(os.Stderr, "lenny-test gate: rerunning the failed %s tests of gate run %s\n", p.name, origID)
	start := time.Now()
	merged := &tierResult{}
	status := verdictstatus.Pass
	detail := ""
	ran := 0
	for i := 1; i <= count; i++ {
		run := p
		if i == 1 {
			run.rerunProfile = profile
		}
		st, msg, res := exec(run)
		if st == verdictstatus.Skipped {
			g.notRun(v, p.name, errors.New(msg))
			return
		}
		mergeTierResult(merged, res)
		ran = i
		var missing []string
		for j := range tests {
			if tests[j].Tier != p.name {
				continue
			}
			tests[j].Runs++
			if res != nil && res.passed[passedKey(tests[j].Package, tests[j].Test)] {
				tests[j].Passed++
			} else {
				missing = append(missing, tests[j].Package+"::"+tests[j].Test)
			}
		}
		if st != verdictstatus.Pass || len(missing) > 0 {
			status = verdictstatus.Fail
			detail = fmt.Sprintf("run %d of %d of the failed tests of gate run %s failed", i, count, origID)
			if len(missing) > 0 {
				detail += "; without a pass: " + strings.Join(missing, ", ")
			}
			if msg != "" {
				detail += "\n" + msg
			}
			break
		}
	}
	if status == verdictstatus.Pass {
		detail = fmt.Sprintf("the failed tests of gate run %s passed in each of %d run(s)", origID, ran)
	}
	logTierEnd(p.name, status, time.Since(start))
	defer g.lock()()
	v.recordTierWithResult(p.name, status, time.Since(start), detail, merged)
}

// mergeRerunCoverage merges the profile the first rerun of the failed unit
// tests wrote at partial into the original's profile at profile, after
// checking that the profile still has the original's hash want, and
// returns the merged profile's hash. A rerun that wrote no profile leaves
// the original's in place.
func mergeRerunCoverage(profile, partial, want, root string) (string, error) {
	add, err := os.ReadFile(partial)
	if errors.Is(err, fs.ErrNotExist) {
		return want, nil
	}
	if err != nil {
		return "", fmt.Errorf("read the rerun's coverage profile %s: %w", partial, err)
	}
	base, err := os.ReadFile(profile)
	if err != nil {
		return "", fmt.Errorf("read the coverage profile %s: %w", profile, err)
	}
	if sum := bytesSHA256(base); sum != want {
		return "", fmt.Errorf("the coverage profile %s changed while the tests reran (SHA-256 %s, not the original's %s); run `lenny-test gate` again", profile, sum[:12], want[:min(12, len(want))])
	}
	merged, err := mergeCoverProfiles(base, add)
	if err != nil {
		return "", fmt.Errorf("merge the rerun's coverage into %s: %w", profile, err)
	}
	if err := writeFileAtomic(profile, merged, 0o644); err != nil {
		return "", fmt.Errorf("write the merged coverage profile %s: %w", profile, err)
	}
	matchOwner(profile, root)
	return bytesSHA256(merged), nil
}

// mergeCoverProfiles merges two Go coverage profiles of the same mode: a
// block both name gets the sum of its counts (in set mode, a count of 1
// when either covers it), and a block only add names is appended. The
// order of base's blocks is kept.
func mergeCoverProfiles(base, add []byte) ([]byte, error) {
	baseMode, baseLines, err := splitCoverProfile(base)
	if err != nil {
		return nil, fmt.Errorf("the original profile: %w", err)
	}
	addMode, addLines, err := splitCoverProfile(add)
	if err != nil {
		return nil, fmt.Errorf("the rerun's profile: %w", err)
	}
	if baseMode != addMode {
		return nil, fmt.Errorf("the original profile's mode is %s and the rerun's is %s", baseMode, addMode)
	}
	counts := map[string]int64{}
	var order []string
	for _, lines := range [][]string{baseLines, addLines} {
		for _, line := range lines {
			cut := strings.LastIndexByte(line, ' ')
			if cut < 0 {
				return nil, fmt.Errorf("malformed profile line %q", line)
			}
			n, err := strconv.ParseInt(line[cut+1:], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("malformed count in profile line %q", line)
			}
			block := line[:cut]
			prev, seen := counts[block]
			if !seen {
				order = append(order, block)
			}
			switch {
			case baseMode == "set" && (prev > 0 || n > 0):
				counts[block] = 1
			case baseMode != "set":
				counts[block] = prev + n
			default:
				counts[block] = 0
			}
		}
	}
	var out bytes.Buffer
	out.WriteString("mode: " + baseMode + "\n")
	for _, block := range order {
		fmt.Fprintf(&out, "%s %d\n", block, counts[block])
	}
	return out.Bytes(), nil
}

// splitCoverProfile returns a profile's mode and its block lines.
func splitCoverProfile(body []byte) (string, []string, error) {
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	mode, ok := strings.CutPrefix(lines[0], "mode: ")
	if !ok || mode == "" {
		return "", nil, errors.New("it does not open with a mode line")
	}
	var blocks []string
	for _, l := range lines[1:] {
		if l != "" {
			blocks = append(blocks, l)
		}
	}
	return mode, blocks, nil
}

// bytesSHA256 returns the hex SHA-256 of body.
func bytesSHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
