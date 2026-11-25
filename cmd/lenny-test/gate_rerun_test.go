// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
)

// `lenny-test gate --rerun-failed` is contributor tooling (change 0172,
// decision 26), so these cases carry no specification annotation.

const rerunPkg = moduleImportPath + "/tests/component/securitycaches"

// failedComponentTier is a component tier that failed on one top-level
// test and its subtest, as go test -json reports them.
func failedComponentTier() tierStat {
	return tierStat{
		Status: verdictstatus.Fail, Total: 10, Passed: 8, Failed: 2,
		Detail: "2 failure(s): x",
		Failures: []failureEntry{
			{Test: "TestPropagation", Package: rerunPkg},
			{Test: "TestPropagation/replica_B", Package: rerunPkg},
		},
	}
}

// originalRun is a FAIL gate run on commit whose component tier failed
// on failedComponentTier, restricted to the tiers of the check before a
// merge, with a coverage profile of hash.
func originalRun(id, commit, hash string) *verdict {
	return &verdict{
		RunID:   id,
		Verdict: verdictstatus.VerdictFail,
		Trigger: trigger{Mode: "gate", GitRevision: commit},
		Tiers: map[string]tierStat{
			tierStatic:    {Status: verdictstatus.Pass, Total: 26, Passed: 26},
			tierUnit:      {Status: verdictstatus.Pass, Total: 100, Passed: 100},
			tierComponent: failedComponentTier(),
			tierContract:  {Status: verdictstatus.Pass, Total: 5, Passed: 5},
		},
		Gate: &gateRecord{
			Base: "main", MergeBase: "b", IntegrationMergeBase: "b", Environment: "lenny-gate:test",
			RdepsDepth: premergeRdepsDepth, RestrictedTo: []string{tierStatic, tierUnit, tierComponent, tierContract},
			Cover: true, CoverProfile: coverProfileFile, CoverProfileSHA256: hash, PackageFailuresRecorded: true,
		},
	}
}

// TestGoTestJSONRecordsPackagesThatFailedOutsideATest pins the parser's
// record of the packages a rerun cannot stand for: a build failure, a
// package that failed with no failed test, and a panic that ended the
// binary; and its record of the tests that passed.
func TestGoTestJSONRecordsPackagesThatFailedOutsideATest(t *testing.T) {
	m := moduleImportPath + "/"
	stdout := strings.Join([]string{
		`{"Action":"run","Package":"` + m + `a","Test":"TestOK"}`,
		`{"Action":"pass","Package":"` + m + `a","Test":"TestOK"}`,
		`{"Action":"pass","Package":"` + m + `a"}`,
		`{"Action":"run","Package":"` + m + `b","Test":"TestFlaky"}`,
		`{"Action":"fail","Package":"` + m + `b","Test":"TestFlaky"}`,
		`{"Action":"fail","Package":"` + m + `b"}`,
		`{"Action":"output","Package":"` + m + `c","Output":"exit status 1\n"}`,
		`{"Action":"fail","Package":"` + m + `c"}`,
		`{"Action":"run","Package":"` + m + `d","Test":"TestBoom"}`,
		`{"Action":"output","Package":"` + m + `d","Test":"TestBoom","Output":"panic: boom [recovered]\n"}`,
		`{"Action":"fail","Package":"` + m + `d","Test":"TestBoom"}`,
		`{"Action":"fail","Package":"` + m + `d"}`,
		`FAIL	` + m + `e [build failed]`,
		`{"Action":"output","Package":"` + m + `f","Output":"FAIL\t` + m + `f [setup failed]\n"}`,
	}, "\n")
	r := parseGoTestJSON([]byte(stdout), nil)
	want := []string{m + "c", m + "d", m + "e", m + "f"}
	if !reflect.DeepEqual(r.PackageFailures, want) {
		t.Errorf("package failures = %v, want %v", r.PackageFailures, want)
	}
	if !r.passed[passedKey(m+"a", "TestOK")] || r.passed[passedKey(m+"b", "TestFlaky")] {
		t.Errorf("passed = %v, want only a's TestOK", r.passed)
	}
	st, detail, _ := tierOutcome("unit", r, nil)
	if st != verdictstatus.Fail || !strings.Contains(detail, "4 package(s) failed outside a test") {
		t.Errorf("tierOutcome = %s, %q; want a failure naming the packages", st, detail)
	}
	clean := parseGoTestJSON([]byte(`{"Action":"fail","Package":"`+m+`x"}`), nil)
	if st, detail, _ := tierOutcome("unit", clean, nil); st != verdictstatus.Fail || !strings.Contains(detail, m+"x") {
		t.Errorf("a package that failed with no failed test and no exit error: %s, %q", st, detail)
	}
	merged := &tierResult{}
	mergeTierResult(merged, r)
	mergeTierResult(merged, clean)
	if len(merged.PackageFailures) != 5 || !merged.passed[passedKey(m+"a", "TestOK")] {
		t.Errorf("merged result = %v, %v", merged.PackageFailures, merged.passed)
	}
}

// TestRerunPlansRerunTheFailedTopLevelTests pins the plan a rerun runs:
// the failed top-level tests of each failed tier, by name in their
// packages, once each however many subtests failed, with no coverage
// profile of the tier's own.
func TestRerunPlansRerunTheFailedTopLevelTests(t *testing.T) {
	orig := originalRun("o1", "c1", "h")
	unit := orig.Tiers[tierUnit]
	unit.Status, unit.Failed, unit.Failures = verdictstatus.Fail, 1, []failureEntry{{Test: "TestA", Package: moduleImportPath + "/pkg/a"}}
	orig.Tiers[tierUnit] = unit
	plans, tests, err := rerunPlans(orig)
	if err != nil {
		t.Fatalf("rerunPlans: %v", err)
	}
	if got := plans[tierComponent].funcs; !reflect.DeepEqual(got, map[string][]string{"./tests/component/securitycaches": {"TestPropagation"}}) {
		t.Errorf("component funcs = %v", got)
	}
	if got := plans[tierUnit].funcs; !reflect.DeepEqual(got, map[string][]string{"./pkg/a": {"TestA"}}) {
		t.Errorf("unit funcs = %v", got)
	}
	if p := plans[tierUnit]; p.pkgs == nil || len(p.pkgs) != 0 || !p.noCoverage {
		t.Errorf("unit plan = %+v, want a scoped plan of by-name tests without a profile of its own", p)
	}
	if _, ok := plans[tierContract]; ok {
		t.Error("a tier that passed was planned")
	}
	want := []gateRerunTest{{Tier: tierUnit, Package: moduleImportPath + "/pkg/a", Test: "TestA"}, {Tier: tierComponent, Package: rerunPkg, Test: "TestPropagation"}}
	if !reflect.DeepEqual(tests, want) {
		t.Errorf("tests = %+v, want %+v", tests, want)
	}
}

// TestRerunPlansRefuseARunWhoseFailuresAreNotIndividualTests pins each
// refusal: a run that passed or did not fail, a dirty run, a run with a
// reason under gate.unverified, a run of an earlier lenny-test, a failed
// static or documentation tier, a tier with a package that failed outside
// a test, a tier whose failures are not all recorded, and a failure
// outside the module.
func TestRerunPlansRefuseARunWhoseFailuresAreNotIndividualTests(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(v *verdict)
		want string
	}{
		{"passed", func(v *verdict) { v.Verdict = verdictstatus.VerdictPass }, "nothing to rerun"},
		{"unverified", func(v *verdict) { v.Verdict = verdictstatus.VerdictUnverified }, "UNVERIFIED rather than FAIL"},
		{"inconclusive", func(v *verdict) { v.Verdict = verdictstatus.VerdictInconclusive }, "INCONCLUSIVE rather than FAIL"},
		{"not a gate run", func(v *verdict) { v.Gate = nil }, "is not a gate run"},
		{"dirty", func(v *verdict) { v.Gate.Dirty = true }, "uncommitted changes"},
		{"unverified reason", func(v *verdict) { v.Gate.Unverified = []string{"outside the gate image"} }, "outside the gate image"},
		{"earlier harness", func(v *verdict) { v.Gate.PackageFailuresRecorded = false }, "does not record the packages"},
		{"static", func(v *verdict) {
			v.Tiers[tierStatic] = tierStat{Status: verdictstatus.Fail, Detail: "go vet ./... failed"}
		}, "the static tier of gate run o1 failed"},
		{"docs", func(v *verdict) {
			v.Tiers[tierDocs] = tierStat{Status: verdictstatus.Fail, Detail: "1 failure(s)", Failed: 1, Failures: []failureEntry{{Test: "TestX", Package: moduleImportPath + "/tests/docs"}}}
		}, "the docs tier of gate run o1 failed"},
		{"inconclusive tier", func(v *verdict) {
			v.Tiers[tierComponent] = tierStat{Status: verdictstatus.Inconclusive, Detail: "docker daemon gone"}
		}, "is inconclusive"},
		{"package failure", func(v *verdict) {
			c := failedComponentTier()
			c.PackageFailures = []string{rerunPkg}
			v.Tiers[tierComponent] = c
		}, "failed outside a test"},
		{"unrecorded failure", func(v *verdict) {
			c := failedComponentTier()
			c.Failed = 3
			v.Tiers[tierComponent] = c
		}, "3 failed test(s) and 2 failure record(s)"},
		{"no failure records", func(v *verdict) {
			v.Tiers[tierComponent] = tierStat{Status: verdictstatus.Fail, Detail: "component suite failed"}
		}, "0 failed test(s) and 0 failure record(s)"},
		{"outside the module", func(v *verdict) {
			c := failedComponentTier()
			c.Failures[0].Package = "example.com/other"
			v.Tiers[tierComponent] = c
		}, "without a test or a package of this module"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := originalRun("o1", "c1", "h")
			tc.edit(v)
			_, _, err := rerunPlans(v)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("rerunPlans = %v, want a refusal containing %q", err, tc.want)
			}
		})
	}
}

// writeGateRuns writes each run's verdict file under root and its line in
// the run history, in order, as the gate writes them.
func writeGateRuns(t *testing.T, root string, runs ...*verdict) {
	t.Helper()
	for _, v := range runs {
		if _, err := writeGateVerdict(root, v); err != nil {
			t.Fatal(err)
		}
	}
}

// TestFindRerunOriginalTakesTheLatestGateRunOnTheCommit pins the lookup:
// the last gate run on the commit that is not itself a rerun, past a run on
// another commit and a rerun; no run on the commit is a refusal, and so is
// a verdict file that names another run.
func TestFindRerunOriginalTakesTheLatestGateRunOnTheCommit(t *testing.T) {
	root := t.TempDir()
	if _, err := findRerunOriginal(root, "c1"); err == nil || !strings.Contains(err.Error(), "no gate run is recorded") {
		t.Errorf("an absent history: %v", err)
	}
	first, second, other := originalRun("aa1", "c1", "h"), originalRun("aa2", "c1", "h"), originalRun("aa3", "c2", "h")
	rerun := originalRun("aa4", "c1", "h")
	rerun.Gate.RerunOf = &gateRerun{RunID: "aa2"}
	writeGateRuns(t, root, first, second, other, rerun)
	got, err := findRerunOriginal(root, "c1")
	if err != nil || got.RunID != "aa2" {
		t.Fatalf("findRerunOriginal = %v, %v; want run aa2", got, err)
	}
	if _, err := findRerunOriginal(root, "c9"); err == nil {
		t.Error("a commit with no gate run was not refused")
	}
	body, _ := json.Marshal(originalRun("zz9", "c1", "h"))
	if err := os.WriteFile(filepath.Join(root, gateResultsDir, "gate-aa2.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := findRerunOriginal(root, "c1"); err == nil || !strings.Contains(err.Error(), "names run zz9") {
		t.Errorf("a verdict file naming another run: %v", err)
	}
}

// TestPrepareRerunRefusesADirtyTreeAndARewrittenProfile pins the refusals
// prepareRerun adds to rerunPlans.
func TestPrepareRerunRefusesADirtyTreeAndARewrittenProfile(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, coverProfileFile)
	body := []byte("mode: atomic\nm/a.go:1.1,2.2 1 1\n")
	if err := os.MkdirAll(filepath.Dir(profile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profile, body, 0o644); err != nil {
		t.Fatal(err)
	}
	writeGateRuns(t, root, originalRun("bb1", "c1", bytesSHA256(body)))
	if _, err := prepareRerun(root, "c1", true); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("a dirty tree: %v", err)
	}
	if _, err := prepareRerun(root, "", false); err == nil {
		t.Error("an unresolved HEAD was not refused")
	}
	job, err := prepareRerun(root, "c1", false)
	if err != nil || job.profile != profile || job.orig.RunID != "bb1" {
		t.Fatalf("prepareRerun = %+v, %v", job, err)
	}
	if err := os.WriteFile(profile, []byte("mode: atomic\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareRerun(root, "c1", false); err == nil || !strings.Contains(err.Error(), "a later command rewrote it") {
		t.Errorf("a rewritten profile: %v", err)
	}
	if err := os.Remove(profile); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareRerun(root, "c1", false); err == nil || !strings.Contains(err.Error(), "is not the one gate run bb1 wrote") {
		t.Errorf("a missing profile: %v", err)
	}
}

// rerunStub returns a gate runner whose component executor passes the
// named tests when pass reports true for the run, and records each plan
// it is given.
func rerunStub(head string, plans *[]tierPlan, pass func(run int) bool) gateRunner {
	g := stubGateRunner()
	g.state = func() (string, bool) { return head, false }
	run := 0
	g.exec[tierComponent] = func(p tierPlan) (string, string, *tierResult) {
		run++
		*plans = append(*plans, p)
		r := &tierResult{Total: 1, passed: map[string]bool{}}
		if !pass(run) {
			r.Failed = 1
			r.Failures = []failureEntry{{Test: "TestPropagation", Package: rerunPkg}}
			return verdictstatus.Fail, "1 failure(s)", r
		}
		r.Passed = 1
		r.passed[passedKey(rerunPkg, "TestPropagation")] = true
		return verdictstatus.Pass, "", r
	}
	return g
}

// TestRerunFailedPassesWhenEveryRunPasses pins the verdict of a rerun
// whose tests pass each time: PASS, naming the original run, carrying its
// selection, tiers, and profile hash, with each test's runs and the
// original's passing tiers.
func TestRerunFailedPassesWhenEveryRunPasses(t *testing.T) {
	orig := originalRun("o1", "c1", "h1")
	plans, tests, err := rerunPlans(orig)
	if err != nil {
		t.Fatal(err)
	}
	var ran []tierPlan
	g := rerunStub("c1", &ran, func(int) bool { return true })
	v := g.rerunFailed(rerunJob{orig: orig, plans: plans, tests: tests}, rerunOptions{root: t.TempDir(), count: 3, environment: "lenny-gate:test"})
	if v.Verdict != verdictstatus.VerdictPass {
		t.Fatalf("verdict %s (tiers %+v, unverified %v)", v.Verdict, v.Tiers, v.Gate.Unverified)
	}
	if len(ran) != 3 || !reflect.DeepEqual(ran[0].funcs, map[string][]string{"./tests/component/securitycaches": {"TestPropagation"}}) {
		t.Errorf("the component executor ran %d plan(s): %+v", len(ran), ran)
	}
	r := v.Gate.RerunOf
	if r == nil || r.RunID != "o1" || r.Count != 3 || r.CoverProfileSHA256 != "h1" {
		t.Fatalf("rerun_of = %+v", r)
	}
	if want := []gateRerunTest{{Tier: tierComponent, Package: rerunPkg, Test: "TestPropagation", Runs: 3, Passed: 3}}; !reflect.DeepEqual(r.Tests, want) {
		t.Errorf("tests = %+v, want %+v", r.Tests, want)
	}
	if !reflect.DeepEqual(v.Gate.RestrictedTo, orig.Gate.RestrictedTo) || v.Gate.CoverProfileSHA256 != "h1" || !v.Gate.Cover || v.Gate.RdepsDepth != premergeRdepsDepth {
		t.Errorf("gate record = %+v, want the original's tiers, depth, and profile", v.Gate)
	}
	if got := v.Tiers[tierUnit]; got.Status != verdictstatus.Pass || got.Detail != "passed in gate run o1" {
		t.Errorf("unit tier = %+v, want the original's pass", got)
	}
	if !strings.Contains(v.Tiers[tierComponent].Detail, "passed in each of 3 run(s)") || !strings.Contains(v.Command, "--rerun-failed --rerun-count 3") {
		t.Errorf("component detail %q, command %q", v.Tiers[tierComponent].Detail, v.Command)
	}
}

// TestRerunFailedFailsOnAnyFailedRun pins that a run that fails, or that
// passes without running a named test, fails the rerun at that run, and
// that a precheck failure and a run outside the gate image keep the
// verdict from PASS.
func TestRerunFailedFailsOnAnyFailedRun(t *testing.T) {
	orig := originalRun("o1", "c1", "h1")
	plans, tests, err := rerunPlans(orig)
	if err != nil {
		t.Fatal(err)
	}
	job := rerunJob{orig: orig, plans: plans, tests: tests}
	o := rerunOptions{root: t.TempDir(), count: 3, environment: "lenny-gate:test"}

	var ran []tierPlan
	v := rerunStub("c1", &ran, func(run int) bool { return run != 2 }).rerunFailed(job, o)
	if v.Verdict != verdictstatus.VerdictFail || len(ran) != 2 || !strings.Contains(v.Tiers[tierComponent].Detail, "run 2 of 3") {
		t.Errorf("a failed second run: verdict %s after %d run(s), detail %q", v.Verdict, len(ran), v.Tiers[tierComponent].Detail)
	}
	if got := v.Gate.RerunOf.Tests[0]; got.Runs != 2 || got.Passed != 1 {
		t.Errorf("test record = %+v, want 2 runs and 1 pass", got)
	}

	g := rerunStub("c1", &ran, func(int) bool { return true })
	g.exec[tierComponent] = func(tierPlan) (string, string, *tierResult) {
		return verdictstatus.Pass, "", &tierResult{}
	}
	if v := g.rerunFailed(job, o); v.Verdict != verdictstatus.VerdictFail || !strings.Contains(v.Tiers[tierComponent].Detail, "without a pass: "+rerunPkg+"::TestPropagation") {
		t.Errorf("a run that matched no test: verdict %s, detail %q", v.Verdict, v.Tiers[tierComponent].Detail)
	}

	g = rerunStub("c1", &ran, func(int) bool { return true })
	g.precheck[tierComponent] = func() error { return errors.New("docker is not reachable") }
	if v := g.rerunFailed(job, o); v.Verdict != verdictstatus.VerdictUnverified || !strings.Contains(v.Tiers[tierComponent].Reason, "docker is not reachable") {
		t.Errorf("a failed precheck: verdict %s, tier %+v", v.Verdict, v.Tiers[tierComponent])
	}

	g = rerunStub("c1", &ran, func(int) bool { return true })
	outside := o
	outside.environmentErr = errors.New("LENNY_GATE_IMAGE is not set")
	if v := g.rerunFailed(job, outside); v.Verdict != verdictstatus.VerdictUnverified {
		t.Errorf("a rerun outside the gate image: verdict %s", v.Verdict)
	}

	moved := rerunStub("c1", &ran, func(int) bool { return true })
	calls := 0
	moved.state = func() (string, bool) {
		calls++
		if calls > 1 {
			return "c2", false
		}
		return "c1", false
	}
	if v := moved.rerunFailed(job, o); v.Verdict != verdictstatus.VerdictUnverified || !strings.Contains(strings.Join(v.Gate.Unverified, ";"), "changed while the tests ran") {
		t.Errorf("a HEAD that moved: verdict %s, unverified %v", v.Verdict, v.Gate.Unverified)
	}
}

// TestRerunFailedMergesTheUnitCoverage pins the coverage of a rerun of
// failed unit tests: the first run writes a profile of its own, and when
// every run passes it is merged into the original's profile, whose new
// hash the verdict records beside the original's.
func TestRerunFailedMergesTheUnitCoverage(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, coverProfileFile)
	base := []byte("mode: atomic\nm/a.go:1.1,2.2 1 0\nm/a.go:3.1,4.2 1 5\n")
	if err := os.MkdirAll(filepath.Dir(profile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profile, base, 0o644); err != nil {
		t.Fatal(err)
	}
	orig := originalRun("o1", "c1", bytesSHA256(base))
	orig.Tiers[tierComponent] = tierStat{Status: verdictstatus.Pass}
	orig.Tiers[tierUnit] = tierStat{Status: verdictstatus.Fail, Failed: 1, Detail: "1 failure(s)", Failures: []failureEntry{{Test: "TestA", Package: moduleImportPath + "/pkg/a"}}}
	plans, tests, err := rerunPlans(orig)
	if err != nil {
		t.Fatal(err)
	}
	g := stubGateRunner()
	g.state = func() (string, bool) { return "c1", false }
	var profiles []string
	g.exec[tierUnit] = func(p tierPlan) (string, string, *tierResult) {
		profiles = append(profiles, p.byNameProfile)
		if p.byNameProfile != "" {
			if err := os.WriteFile(p.byNameProfile, []byte("mode: atomic\nm/a.go:1.1,2.2 1 2\nm/b.go:1.1,2.2 1 1\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return verdictstatus.Pass, "", &tierResult{Total: 1, Passed: 1, passed: map[string]bool{passedKey(moduleImportPath+"/pkg/a", "TestA"): true}}
	}
	v := g.rerunFailed(rerunJob{orig: orig, plans: plans, tests: tests, profile: profile}, rerunOptions{root: root, count: 2, environment: "lenny-gate:test"})
	if v.Verdict != verdictstatus.VerdictPass {
		t.Fatalf("verdict %s: %+v", v.Verdict, v.Tiers[tierUnit])
	}
	if len(profiles) != 2 || profiles[0] == "" || profiles[1] != "" {
		t.Errorf("the unit runs wrote profiles %q, want the first run only", profiles)
	}
	got, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if want := "mode: atomic\nm/a.go:1.1,2.2 1 2\nm/a.go:3.1,4.2 1 5\nm/b.go:1.1,2.2 1 1\n"; string(got) != want {
		t.Errorf("merged profile:\n%s\nwant:\n%s", got, want)
	}
	if v.Gate.CoverProfileSHA256 != bytesSHA256(got) || v.Gate.RerunOf.CoverProfileSHA256 != bytesSHA256(base) {
		t.Errorf("hashes: gate %s, rerun_of %s", v.Gate.CoverProfileSHA256, v.Gate.RerunOf.CoverProfileSHA256)
	}
	if _, err := os.Stat(profiles[0]); !os.IsNotExist(err) {
		t.Errorf("the rerun's own profile %s was left behind: %v", profiles[0], err)
	}
}

// TestMergeCoverProfilesSumsCountsOfOneMode pins the merge: counts of a
// block both profiles name add up in atomic and count mode, set mode keeps
// 1 for a block either covers, and differing modes and malformed lines are
// refused.
func TestMergeCoverProfilesSumsCountsOfOneMode(t *testing.T) {
	got, err := mergeCoverProfiles([]byte("mode: set\na:1 1 0\nb:1 1 1\n"), []byte("mode: set\na:1 1 1\nc:1 1 0\n"))
	if err != nil || string(got) != "mode: set\na:1 1 1\nb:1 1 1\nc:1 1 0\n" {
		t.Errorf("set mode: %q, %v", got, err)
	}
	for _, tc := range []struct{ base, add, want string }{
		{"mode: set\n", "mode: atomic\n", "mode is set and the rerun's is atomic"},
		{"a:1 1 1\n", "mode: atomic\n", "does not open with a mode line"},
		{"mode: atomic\n", "", "does not open with a mode line"},
		{"mode: atomic\nbroken\n", "mode: atomic\n", "malformed profile line"},
		{"mode: atomic\na:1 1 x\n", "mode: atomic\n", "malformed count"},
	} {
		if _, err := mergeCoverProfiles([]byte(tc.base), []byte(tc.add)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("merge %q with %q: %v, want %q", tc.base, tc.add, err, tc.want)
		}
	}
}

// TestCoverageDiffAcceptsARerunOfTheSameCommitsRun pins the coverage check
// over a rerun: under --require-gate the profile of a PASS rerun of a
// failed gate run on HEAD is accepted with a note naming both runs, and a
// rerun that names a run on another commit, a run whose hash differs, a
// run of other tiers, or a run that is itself a rerun is refused.
func TestCoverageDiffAcceptsARerunOfTheSameCommitsRun(t *testing.T) {
	r := newLintRepo(t, "main")
	head := r.git("rev-parse", "HEAD")
	profile := filepath.Join(r.root, coverProfileFile)
	written := []byte("mode: atomic\nexample.com/m/a.go:1.1,2.2 1 1\n")
	hash := bytesSHA256(written)
	if err := os.MkdirAll(filepath.Dir(profile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profile, written, 0o644); err != nil {
		t.Fatal(err)
	}
	rerunOf := func(id, commit string, edit func(o *verdict)) *verdict {
		o := originalRun(id, commit, hash)
		if edit != nil {
			edit(o)
		}
		v := originalRun("rr"+id, head, hash)
		v.Verdict = verdictstatus.VerdictPass
		v.Tiers[tierComponent] = tierStat{Status: verdictstatus.Pass}
		v.Gate.RerunOf = &gateRerun{RunID: id, Count: 3, CoverProfileSHA256: hash}
		writeGateRuns(t, r.root, o, v)
		return v
	}

	rerunOf("ok1", head, nil)
	note, err := checkProfileAgainstGate(r.root, profile, written, true)
	if err != nil || !strings.Contains(note, "gate run rrok1") || !strings.Contains(note, "a rerun of the failed tests of gate run ok1") {
		t.Errorf("a rerun of the same commit's run: %q, %v", note, err)
	}

	for _, tc := range []struct {
		id   string
		edit func(o *verdict)
		want string
	}{
		{"other", func(o *verdict) { o.Trigger.GitRevision = "0000000000" }, "which ran on 0000000000"},
		{"hash", func(o *verdict) { o.Gate.CoverProfileSHA256 = "ffff" }, "did not record the profile hash"},
		{"tiers", func(o *verdict) { o.Gate.RestrictedTo = []string{tierStatic, tierUnit} }, "not the tiers the rerun records"},
		{"chain", func(o *verdict) { o.Gate.RerunOf = &gateRerun{RunID: "x"} }, "is itself a rerun"},
		{"dirty", func(o *verdict) { o.Gate.Dirty = true }, "uncommitted changes"},
	} {
		v := rerunOf(tc.id, head, tc.edit)
		_, err := checkProfileAgainstGate(r.root, profile, written, true)
		if err == nil || !strings.Contains(err.Error(), "reran the failed tests of gate run "+tc.id) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want a refusal of %s containing %q", tc.id, err, v.RunID, tc.want)
		}
	}

	// The original's verdict file is gone.
	rerunOf("gone", head, nil)
	if err := os.Remove(filepath.Join(r.root, gateResultsDir, "gate-gone.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := checkProfileAgainstGate(r.root, profile, written, true); err == nil || !strings.Contains(err.Error(), "has no readable verdict") {
		t.Errorf("a rerun whose original verdict is gone: %v", err)
	}
}

// TestTheRerunHistoryLineNamesTheOriginal pins that a rerun's line of the
// run history names the run it reran, so a later rerun and the coverage
// check tell it from the original.
func TestTheRerunHistoryLineNamesTheOriginal(t *testing.T) {
	root := t.TempDir()
	v := originalRun("cc2", "c1", "h")
	v.Gate.RerunOf = &gateRerun{RunID: "cc1"}
	writeGateRuns(t, root, v)
	body, err := os.ReadFile(filepath.Join(root, gateResultsDir, verdictHistoryFile))
	if err != nil {
		t.Fatal(err)
	}
	var line historyLine
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(body))), &line); err != nil || line.RerunOf != "cc1" {
		t.Errorf("history line %s: %+v, %v", body, line, err)
	}
}

// TestRerunRefusesTheGateFlagsItTakesFromTheRun pins the flags a rerun
// refuses, and that the flags it shares with the gate pass.
func TestRerunRefusesTheGateFlagsItTakesFromTheRun(t *testing.T) {
	fs := flag.NewFlagSet("gate", flag.ContinueOnError)
	for _, name := range append([]string{"here", "rebuild"}, rerunConflictFlags...) {
		fs.String(name, "", "")
	}
	if err := fs.Parse([]string{"--here", "x", "--tier", "unit", "--cover", "y", "--rebuild", "z"}); err != nil {
		t.Fatal(err)
	}
	if got := rerunFlagConflicts(fs); !reflect.DeepEqual(got, []string{"--cover", "--tier"}) {
		t.Errorf("conflicts = %v", got)
	}
	if code := runGate([]string{"--rerun-failed", "--tier", "unit"}); code != 2 {
		t.Errorf("--rerun-failed --tier exited %d, want 2", code)
	}
	if code := runGate([]string{"--rerun-failed", "--rerun-count", "0"}); code != 2 {
		t.Errorf("--rerun-count 0 exited %d, want 2", code)
	}
}

// TestTheRerunScriptSkipsTheStaticTiersPreparation pins the rerun's
// container script: the gate's script without the static tier's snapshot
// and the golangci-lint cache fill, keeping the resolve, the build from the
// commit, the read-only install, and the run.
func TestTheRerunScriptSkipsTheStaticTiersPreparation(t *testing.T) {
	for _, gone := range []string{"prepare_snapshot 0:0\n", `prepare_snapshot "$uid:$gid"` + "\n", `sh -c "$snapshot"`, `sh -c "$fill" || fill_failed`, "\tlock_lint_cache\n", "\nlock_lint_cache\n"} {
		if strings.Contains(gateRerunScript, gone) {
			t.Errorf("the rerun script still runs %q", gone)
		}
		if !strings.Contains(gateContainerScript, gone) {
			t.Errorf("the gate's script no longer holds %q, so the rerun script's replacement is stale", gone)
		}
	}
	for _, kept := range []string{`sh -c "$build"`, "install_harness\n", `exec sh -c "$run" lenny-test-gate "$@"`, `exec setpriv --reuid="$uid" --regid="$gid" --groups="$groups" sh -c "$run" lenny-test-gate "$@"`} {
		if !strings.Contains(gateRerunScript, kept) {
			t.Errorf("the rerun script lacks %q", kept)
		}
	}
}
