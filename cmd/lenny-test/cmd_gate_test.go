// SPDX-License-Identifier: MIT

package main

// Tests for the merge gate command. The gate implements the testing
// model rather than a specification behavior, so these tests carry no
// spec annotation.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
)

// stubGateRunner returns a runner whose every tier passes, with a plan
// that selects the unit, component, and integration tiers.
func stubGateRunner() gateRunner {
	pass := func(tierPlan) (string, string, *tierResult) {
		return verdictstatus.Pass, "", &tierResult{Total: 1, Passed: 1}
	}
	return gateRunner{
		static: func() (string, string, *tierResult) { return verdictstatus.Pass, "", &tierResult{Total: 2, Passed: 2} },
		plan: func(string, int) (resolvedSelector, error) {
			return resolvedSelector{tiers: []tierPlan{
				{name: tierStatic},
				{name: tierUnit, pkgs: []string{"./cmd/lenny-test"}},
				{name: tierComponent},
				{name: tierIntegration},
			}}, nil
		},
		exec:     map[string]tierExecFn{tierUnit: pass, tierComponent: pass, tierContract: pass, tierIntegration: pass},
		precheck: map[string]func() error{},
		docs:     func() (string, string, *tierResult) { return verdictstatus.Pass, "", &tierResult{Total: 3, Passed: 3} },
		state:    func(string) (string, string, bool) { return "abc123", "def456", false },
	}
}

func runStubGate(g gateRunner) *verdict {
	return g.run(gateOptions{root: ".", base: "main", rdepsDepth: 1, environment: "lenny-gate:test"})
}

func TestGatePassesWhenEveryTierPasses(t *testing.T) {
	v := runStubGate(stubGateRunner())
	if v.Verdict != verdictstatus.VerdictPass {
		t.Fatalf("verdict %q; want %q (tiers %+v)", v.Verdict, verdictstatus.VerdictPass, v.Tiers)
	}
	for _, name := range []string{tierStatic, tierUnit, tierComponent, tierIntegration, tierDocs} {
		if got := v.Tiers[name].Status; got != verdictstatus.Pass {
			t.Errorf("tier %s status %q; want pass", name, got)
		}
	}
	if v.Trigger.Mode != "gate" || v.Trigger.GitRevision != "abc123" {
		t.Errorf("trigger %+v; want mode gate at abc123", v.Trigger)
	}
	if v.Gate == nil || v.Gate.Base != "main" || v.Gate.MergeBase != "def456" || v.Gate.Environment != "lenny-gate:test" {
		t.Errorf("gate record %+v", v.Gate)
	}
}

func TestGateRunsEveryTierAfterAFailureAndReportsEachFailure(t *testing.T) {
	g := stubGateRunner()
	g.static = func() (string, string, *tierResult) {
		return verdictstatus.Fail, "go vet failed", &tierResult{Total: 2, Failed: 1, Failures: []failureEntry{{Test: "go vet ./..."}}}
	}
	var unitRan atomic.Bool
	g.exec[tierUnit] = func(tierPlan) (string, string, *tierResult) {
		unitRan.Store(true)
		return verdictstatus.Fail, "TestX failed", &tierResult{Total: 1, Failed: 1, Failures: []failureEntry{{Test: "TestX"}}}
	}
	v := runStubGate(g)
	if !unitRan.Load() {
		t.Fatal("a static failure stopped the gate before the unit tier")
	}
	if v.Verdict != verdictstatus.VerdictFail {
		t.Fatalf("verdict %q; want FAIL", v.Verdict)
	}
	if got := v.Tiers[tierStatic].Failures; len(got) != 1 || got[0].Test != "go vet ./..." {
		t.Errorf("static failures %+v", got)
	}
	if got := v.Tiers[tierUnit].Failures; len(got) != 1 || got[0].Test != "TestX" {
		t.Errorf("unit failures %+v", got)
	}
	if v.Tiers[tierDocs].Status != verdictstatus.Pass {
		t.Errorf("docs tier did not run after the failures: %+v", v.Tiers[tierDocs])
	}
}

func TestGateRecordsATierItCannotRunAsNotRunAndWithholdsAPass(t *testing.T) {
	g := stubGateRunner()
	g.precheck[tierIntegration] = func() error { return errors.New("docker daemon not reachable") }
	g.exec[tierIntegration] = func(tierPlan) (string, string, *tierResult) {
		t.Fatal("the gate ran a tier whose precondition failed")
		return "", "", nil
	}
	v := runStubGate(g)
	if v.Verdict != verdictstatus.VerdictUnverified {
		t.Fatalf("verdict %q; want %q", v.Verdict, verdictstatus.VerdictUnverified)
	}
	st := v.Tiers[tierIntegration]
	if st.Status != verdictstatus.Unverified || !strings.HasPrefix(st.Detail, gateNotRunPrefix) {
		t.Errorf("integration tier %+v; want unverified and not run", st)
	}
	if len(v.Gate.NotRun) != 1 || !strings.Contains(v.Gate.NotRun[0], "integration: docker daemon not reachable") {
		t.Errorf("not-run list %q", v.Gate.NotRun)
	}
}

func TestGateTreatsASkippedSelectedTierAsNotRun(t *testing.T) {
	g := stubGateRunner()
	g.exec[tierComponent] = func(tierPlan) (string, string, *tierResult) {
		return verdictstatus.Skipped, "docker not on PATH", nil
	}
	v := runStubGate(g)
	if v.Verdict == verdictstatus.VerdictPass {
		t.Fatal("a selected tier that skipped left the gate green")
	}
	if len(v.Gate.NotRun) != 1 || !strings.HasPrefix(v.Gate.NotRun[0], tierComponent+": ") {
		t.Errorf("not-run list %q", v.Gate.NotRun)
	}
}

func TestGateLeavesUnselectedTiersOutOfTheRun(t *testing.T) {
	g := stubGateRunner()
	g.exec[tierContract] = func(tierPlan) (string, string, *tierResult) {
		t.Fatal("the gate ran the contract tier the plan did not select")
		return "", "", nil
	}
	v := runStubGate(g)
	v.finalize()
	if got := v.Tiers[tierContract].Status; got != verdictstatus.NotSelected {
		t.Errorf("contract tier %q; want not-selected", got)
	}
}

func TestGatePlanErrorFailsTheGate(t *testing.T) {
	g := stubGateRunner()
	g.plan = func(string, int) (resolvedSelector, error) { return resolvedSelector{}, errors.New("no merge base") }
	v := runStubGate(g)
	if v.Verdict != verdictstatus.VerdictFail {
		t.Fatalf("verdict %q; want FAIL when the changed tiers cannot be resolved", v.Verdict)
	}
}

func TestWriteGateVerdictWritesTheRunFileTheLatestFileAndHistory(t *testing.T) {
	root := t.TempDir()
	v := runStubGate(stubGateRunner())
	rel, err := writeGateVerdict(root, v)
	if err != nil {
		t.Fatal(err)
	}
	if want := "tests/results/gate-" + v.RunID + ".json"; rel != want {
		t.Fatalf("verdict path %q; want %q", rel, want)
	}
	for _, p := range []string{rel, "tests/results/" + gateLatestFile} {
		data, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			RunID   string `json:"run_id"`
			Verdict string `json:"verdict"`
			Trigger struct {
				GitRevision string `json:"git_revision"`
			} `json:"trigger"`
			Tiers map[string]json.RawMessage `json:"tiers"`
			Gate  *gateRecord                `json:"gate"`
		}
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if got.RunID != v.RunID || got.Verdict != verdictstatus.VerdictPass || got.Trigger.GitRevision != "abc123" || got.Gate == nil {
			t.Errorf("%s: %+v", p, got)
		}
		if _, ok := got.Tiers[tierE2EKind]; !ok {
			t.Errorf("%s does not record the tiers outside the gate as not selected", p)
		}
	}
	hist, err := os.ReadFile(filepath.Join(root, "tests/results", verdictHistoryFile))
	if err != nil || !strings.Contains(string(hist), v.RunID) {
		t.Errorf("history does not carry the run: %v %q", err, hist)
	}
}

func TestGateImageTagFollowsTheDockerfile(t *testing.T) {
	a := gateImageTag([]byte("FROM golang:1.25.0\n"))
	b := gateImageTag([]byte("FROM golang:1.25.1\n"))
	if a == b {
		t.Fatal("a changed pin kept the image tag, so the gate would not rebuild")
	}
	if !strings.HasPrefix(a, gateImageRepo+":") || len(a) != len(gateImageRepo)+1+12 {
		t.Errorf("tag %q", a)
	}
	if a != gateImageTag([]byte("FROM golang:1.25.0\n")) {
		t.Error("the tag is not a function of the Dockerfile")
	}
}

func TestGateDockerRunArgsMountTheTreeTheGitDirAndTheCaches(t *testing.T) {
	h := gateHost{
		root:         "/work/lenny/.claude/worktrees/a",
		gitCommonDir: "/work/lenny/.git",
		image:        "lenny-gate:0123456789ab",
		dockerSocket: "/var/run/docker.sock",
		passArgs:     []string{"--base", "main"},
	}
	args := strings.Join(gateDockerRunArgs(h), " ")
	for _, want := range []string{
		"-v /work/lenny/.claude/worktrees/a:/work/lenny/.claude/worktrees/a",
		"-w /work/lenny/.claude/worktrees/a",
		"-v /work/lenny/.git:/work/lenny/.git:ro",
		"-v " + gateModVolume + ":/go/pkg/mod",
		"-v " + gateBuildVolume + ":/root/.cache/go-build",
		"-e " + gateEnvImage + "=lenny-gate:0123456789ab",
		"-v /var/run/docker.sock:/var/run/docker.sock",
		"TESTCONTAINERS_HOST_OVERRIDE=host.docker.internal",
		"lenny-gate:0123456789ab sh -c",
		"gate --here",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("docker run args lack %q:\n%s", want, args)
		}
	}
	if !strings.HasSuffix(args, "--base main") {
		t.Errorf("docker run args do not end with the passed gate flags:\n%s", args)
	}
}

func TestGateDockerRunArgsOmitTheSocketAndAGitDirInsideTheTree(t *testing.T) {
	h := gateHost{root: "/work/lenny", gitCommonDir: "/work/lenny/.git", image: "lenny-gate:x"}
	args := strings.Join(gateDockerRunArgs(h), " ")
	if strings.Contains(args, ".git:/work/lenny/.git") {
		t.Errorf("mounted a git directory the tree mount already carries:\n%s", args)
	}
	if strings.Contains(args, "docker.sock") || strings.Contains(args, "TESTCONTAINERS") {
		t.Errorf("mounted a Docker socket the run was not given:\n%s", args)
	}
}

func TestGateDocsTestNamesReadsEveryListedGate(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a_test.go", "package x\nimport \"testing\"\nfunc TestB(t *testing.T) {}\nfunc TestA(t *testing.T) {}\nfunc helper(t *testing.T) {}\nfunc TestMain(m *testing.M) {}\n")
	write("b_test.go", "package x\nimport \"testing\"\nfunc TestC(t *testing.T) {}\n")
	write("empty_test.go", "package x\n")
	got, err := gateDocsTestNames(dir, []string{"a_test.go", "b_test.go"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "TestA,TestB,TestC" {
		t.Errorf("names %q", got)
	}
	if _, err := gateDocsTestNames(dir, []string{"empty_test.go"}); err == nil {
		t.Error("a listed gate that declares no test was accepted")
	}
	if _, err := gateDocsTestNames(dir, []string{"missing_test.go"}); err == nil {
		t.Error("a listed gate that does not exist was accepted")
	}
}

// TestGateDocsFilesExistInTheTree keeps the gate's list of general
// documentation gates in step with tests/tier11_docs.
func TestGateDocsFilesExistInTheTree(t *testing.T) {
	names, err := gateDocsTestNames(filepath.Join(repoRoot(), "tests", "tier11_docs"), gateDocsFiles)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) < len(gateDocsFiles) {
		t.Errorf("%d tests from %d files", len(names), len(gateDocsFiles))
	}
}

func TestPrintGateSummaryNamesTheRunEveryFailureAndTheNotRunTiers(t *testing.T) {
	g := stubGateRunner()
	g.static = func() (string, string, *tierResult) {
		return verdictstatus.Fail, "", &tierResult{Total: 2, Failed: 1, Failures: []failureEntry{{Test: "go vet ./..."}}}
	}
	g.precheck[tierComponent] = func() error { return errors.New("no envtest") }
	v := runStubGate(g)
	v.finalize()
	var b strings.Builder
	printGateSummary(&b, v, "tests/results/gate-x.json")
	out := b.String()
	for _, want := range []string{v.RunID, "abc123", "FAIL go vet ./...", "not run: no envtest", "tests/results/gate-x.json"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lacks %q:\n%s", want, out)
		}
	}
}

func TestPathWithin(t *testing.T) {
	cases := []struct {
		p, root string
		want    bool
	}{
		{"/a/b/.git", "/a/b", true},
		{"/a/b", "/a/b", true},
		{"/a/.git", "/a/b", false},
		{"/a/bc", "/a/b", false},
	}
	for _, c := range cases {
		if got := pathWithin(c.p, c.root); got != c.want {
			t.Errorf("pathWithin(%q, %q) = %v; want %v", c.p, c.root, got, c.want)
		}
	}
}

func TestRunStaticChecksRunsChecksConcurrentlyAndKeepsTableOrder(t *testing.T) {
	old := staticCheckConcurrency
	staticCheckConcurrency = 4
	t.Cleanup(func() { staticCheckConcurrency = old })
	var running, peak atomic.Int32
	slow := func(name string) staticCheck {
		return staticCheck{name: name, run: func() (string, error) {
			n := running.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
			running.Add(-1)
			return name, nil
		}}
	}
	checks := []staticCheck{slow("a"), slow("b"), slow("c"), slow("d")}
	out := runStaticChecks(checks)
	if peak.Load() < 2 {
		t.Errorf("peak concurrency %d; the checks ran in sequence", peak.Load())
	}
	for i, o := range out {
		if o.name != checks[i].name || o.out != checks[i].name {
			t.Errorf("outcome %d is %+v; want check %s", i, o, checks[i].name)
		}
	}
}

func TestComposeStaticChecksReportsEveryFailedCheck(t *testing.T) {
	checks := []staticCheck{
		staticCheckStub("go vet ./...", "vet: bad", errors.New("exit status 1")),
		staticCheckStub("buf lint", "", nil),
		staticCheckStub("gofumpt -l .", "a.go", errors.New("gofumpt: 1 file(s) need formatting")),
	}
	status, msg := composeStaticChecks(checks)
	if status != verdictstatus.Fail {
		t.Fatalf("status %q; want fail", status)
	}
	for _, want := range []string{"go vet ./... failed", "gofumpt -l . failed"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
	r := staticTierResult(runStaticChecks(checks))
	if r.Total != 3 || r.Passed != 1 || r.Failed != 2 || len(r.Failures) != 2 {
		t.Fatalf("result %+v", r)
	}
	if r.Failures[0].Test != "go vet ./..." || r.Failures[1].Test != "gofumpt -l ." {
		t.Errorf("failure entries %+v", r.Failures)
	}
}

func TestTailLinesKeepsTheLastLines(t *testing.T) {
	if got := tailLines("a\nb\nc\n", 2); got != "b\nc" {
		t.Errorf("tailLines = %q", got)
	}
	if got := tailLines("a", 5); got != "a" {
		t.Errorf("tailLines = %q", got)
	}
}
