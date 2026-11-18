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
	"syscall"
	"testing"
	"time"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
	"github.com/lennylabs/lenny/tests/testinfra/testtmp"
)

// stubGateRunner returns a runner whose every tier passes, with a plan
// that selects the unit, component, and integration tiers.
func stubGateRunner() gateRunner {
	pass := func(tierPlan) (string, string, *tierResult) {
		return verdictstatus.Pass, "", &tierResult{Total: 1, Passed: 1}
	}
	return gateRunner{
		static: func() (string, string, *tierResult) { return verdictstatus.Pass, "", &tierResult{Total: 2, Passed: 2} },
		plan: func(string, int, bool) (resolvedSelector, error) {
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
	return g.run(gateOptions{root: ".", base: "main", rdepsDepth: defaultRdepsDepth, environment: "lenny-gate:test"})
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
	g.plan = func(string, int, bool) (resolvedSelector, error) {
		return resolvedSelector{}, errors.New("no merge base")
	}
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
		imageID:      "sha256:feed",
		dockerSocket: "/var/run/docker.sock",
		passArgs:     []string{"--base", "main"},
	}
	args := strings.Join(gateDockerRunArgs(h), " ")
	for _, want := range []string{
		"-v /work/lenny/.claude/worktrees/a:/work/lenny/.claude/worktrees/a",
		"-w /work/lenny/.claude/worktrees/a",
		"-v /work/lenny/.git:/work/lenny/.git:ro",
		"-v " + gateModVolume + ":/go/pkg/mod",
		"-v " + gateBuildVolume + ":" + gateGoCache,
		"-e " + gateEnvImage + "=lenny-gate:0123456789ab",
		"-e " + gateEnvImageID + "=sha256:feed",
		"-v /var/run/docker.sock:/var/run/docker.sock",
		"--network host",
		"TESTCONTAINERS_HOST_OVERRIDE=127.0.0.1",
		"sha256:feed sh -c",
		"go mod verify\nmkdir -p \"$HOME/bin\"\ngo build",
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
	if strings.Contains(args, ":"+gateContainerSocket+" ") || strings.Contains(args, "TESTCONTAINERS") {
		t.Errorf("mounted a Docker socket the run was not given:\n%s", args)
	}
	if strings.Contains(args, gateEnvImageID) || !strings.Contains(args, "lenny-gate:x sh -c") {
		t.Errorf("a run with no recorded image ID does not run the image by its tag:\n%s", args)
	}
}

// TestGateDockerRunArgsSplitTheFixtureRootFromTheSharedDirectory checks
// where the container's fixtures live. TMPDIR and the fixture root are a
// path on the container's own file system, off every mount, so tests get
// local file-system behavior on any host. The shared fixture directory is
// a host directory mounted at its own path and named by
// LENNY_TEST_SHARED_TMPDIR, so a fixture a test bind-mounts into a
// testcontainer resolves on the host daemon. go build keeps its work
// directories off the mounts.
func TestGateDockerRunArgsSplitTheFixtureRootFromTheSharedDirectory(t *testing.T) {
	root := "/Users/alice/lenny"
	shared := "/tmp/lenny-gate-01234567"
	argv := gateDockerRunArgs(gateHost{root: root, image: "lenny-gate:x", tmpDir: shared})
	env := map[string]string{}
	mounts := map[string]bool{}
	for i := 0; i+1 < len(argv); i++ {
		switch argv[i] {
		case "-e":
			k, val, _ := strings.Cut(argv[i+1], "=")
			env[k] = val
		case "-v":
			mounts[argv[i+1]] = true
		}
	}
	if env["TMPDIR"] != gateFixtureRoot || env[testtmp.EnvRoot] != gateFixtureRoot {
		t.Errorf("TMPDIR %q and %s %q; want both %q", env["TMPDIR"], testtmp.EnvRoot, env[testtmp.EnvRoot], gateFixtureRoot)
	}
	for m := range mounts {
		if dst := m[strings.Index(m, ":")+1:]; pathWithin(gateFixtureRoot, strings.TrimSuffix(dst, ":ro")) {
			t.Errorf("the fixture root %q lies on the mount %q rather than the container's own file system", gateFixtureRoot, m)
		}
	}
	if env[testtmp.EnvShared] != shared || !mounts[shared+":"+shared] {
		t.Errorf("%s %q, mounts %v; want %q mounted at its host path", testtmp.EnvShared, env[testtmp.EnvShared], mounts, shared)
	}
	if g := env["GOTMPDIR"]; g == "" || pathWithin(g, root) || pathWithin(g, shared) || testtmp.UnderTmp(g) {
		t.Errorf("GOTMPDIR %q; want a container path off the mounts and off /tmp", g)
	}
	if !strings.Contains(gateContainerScript, `find "$LENNY_TEST_SHARED_TMPDIR" -mindepth 1 -delete`) {
		t.Error("the container does not empty the shared fixture directory before the run")
	}
	bare := strings.Join(gateDockerRunArgs(gateHost{root: root, image: "lenny-gate:x"}), " ")
	if strings.Contains(bare, "-e "+testtmp.EnvShared+"=") {
		t.Errorf("a run with no shared directory names one:\n%s", bare)
	}
}

// TestGateHostTmpDirIsOutsideTheWorktreeAndPerWorktree keeps the fixture
// root out of the trees the static checks walk, and apart for two
// worktrees.
func TestGateHostTmpDirIsOutsideTheWorktreeAndPerWorktree(t *testing.T) {
	a, err := gateHostTmpDir("/work/a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := gateHostTmpDir("/work/b")
	if err != nil {
		t.Fatal(err)
	}
	if a == b || pathWithin(a, "/work/a") {
		t.Errorf("fixture roots %q and %q", a, b)
	}
	if fi, err := os.Stat(a); err != nil || !fi.IsDir() {
		t.Errorf("the fixture root was not created: %v", err)
	}
	if again, err := gateHostTmpDir("/work/a"); err != nil || again != a {
		t.Errorf("a second run of the same worktree got %q (%v); want %q", again, err, a)
	}
}

// TestGateFixtureDirectoriesLeaveRoomForATestSocket keeps the fixture
// root and the shared fixture directory short enough that a Unix socket
// under a test's t.TempDir() has at least as many bytes below the root as
// on a macOS host, where lenny-test roots fixtures at $HOME/.lenny-tmp and
// sun_path holds 104 bytes. Linux's sun_path holds 108. A socket path
// that fits on a developer's host then fits in the gate, whatever the
// worktree's path.
func TestGateFixtureDirectoriesLeaveRoomForATestSocket(t *testing.T) {
	const linuxSunPath, macSunPath = 108, 104
	hostRoot := filepath.Join("/Users/alice", testtmp.HomeDirName)
	shared, err := gateHostTmpDir("/Users/alice/projects/lenny/.claude/worktrees/agent-0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(shared, gateTmpParent+"/"+gateTmpPrefix) {
		t.Errorf("shared fixture directory %q is not under %s", shared, gateTmpParent)
	}
	for _, root := range []string{gateFixtureRoot, shared} {
		if linuxSunPath-len(root) < macSunPath-len(hostRoot) {
			t.Errorf("%q leaves %d bytes for a socket path below it; a macOS host root %q leaves %d", root, linuxSunPath-len(root), hostRoot, macSunPath-len(hostRoot))
		}
	}
}

// TestGateDockerRunArgsGrantWhatTheTestsNeed checks the container
// options the tiers depend on: the Docker host's network namespace, so a
// fixture published on 127.0.0.1 is reachable; CAP_SYS_ADMIN with the
// /proc paths unmasked and no AppArmor profile, so unshare can create
// namespaces and mount /proc; and the worktree owner's IDs, which the
// container script runs the gate as.
func TestGateDockerRunArgsGrantWhatTheTestsNeed(t *testing.T) {
	argv := gateDockerRunArgs(gateHost{root: "/w", image: "lenny-gate:x", uid: 501, gid: 20})
	args := strings.Join(argv, " ")
	for _, want := range []string{
		"--network host",
		"--cap-add SYS_ADMIN",
		"--security-opt systempaths=unconfined",
		"--security-opt apparmor=unconfined",
		"-e " + gateEnvUID + "=501",
		"-e " + gateEnvGID + "=20",
		"-e GIT_OPTIONAL_LOCKS=0",
		"-e GOFLAGS=-buildvcs=false",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("docker run args lack %q:\n%s", want, args)
		}
	}
	if i := strings.Index(args, " lenny-gate:x "); i < 0 || strings.Contains(args[:i], "--privileged") {
		t.Errorf("the container runs privileged or not from the image:\n%s", args)
	}
}

// TestGateContainerScriptDropsToTheWorktreeOwner pins the steps of the
// container script that run the gate unprivileged: it creates the
// owner's user, adds the Docker socket's group, gives the user the cache
// volumes, and runs the gate through setpriv, and it keeps running as
// root when the owner is root.
func TestGateContainerScriptDropsToTheWorktreeOwner(t *testing.T) {
	for _, want := range []string{
		`if [ "$uid" = 0 ]; then exec sh -c "$run" lenny-test-gate "$@"; fi`,
		`useradd --uid "$uid" --gid "$gid"`,
		`stat -c %g /var/run/docker.sock`,
		`chown -R "$uid:$gid" "$d"`,
		`exec setpriv --reuid="$uid" --regid="$gid" --groups="$groups" sh -c "$run" lenny-test-gate "$@"`,
		`exec "$HOME/bin/lenny-test" gate --here "$@"`,
	} {
		if !strings.Contains(gateContainerScript, want) {
			t.Errorf("the container script lacks %q", want)
		}
	}
	if strings.Index(gateContainerScript, "-delete") > strings.Index(gateContainerScript, "setpriv") {
		t.Error("the container script empties the fixture root after dropping root, so it cannot remove what a root run left")
	}
}

// TestGateDockerfileKeepsTheBuildCacheWhereTheRunMountsIt keeps GOCACHE in
// the image on the path the build-cache volume is mounted at, outside
// /root, which the unprivileged gate user cannot enter.
func TestGateDockerfileKeepsTheBuildCacheWhereTheRunMountsIt(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(), gateDockerfile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "GOCACHE="+gateGoCache+" ") || strings.HasPrefix(gateGoCache, "/root") {
		t.Errorf("%s does not set GOCACHE=%s outside /root", gateDockerfile, gateGoCache)
	}
}

// TestGateAllSelectsEveryTierUpToIntegrationInFull checks that --all
// replaces the --changed selection with the whole unit tier and every
// test of the component, contract, and integration tiers, and that the
// verdict records it.
func TestGateAllSelectsEveryTierUpToIntegrationInFull(t *testing.T) {
	plan, err := gateChangedPlan("no-such-ref", 1, true)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range plan.tiers {
		names = append(names, p.name)
		if p.pkgs != nil || p.tests != nil || p.subsets != nil {
			t.Errorf("tier %s is narrowed: pkgs %v tests %v subsets %v", p.name, p.pkgs, p.tests, p.subsets)
		}
	}
	if got, want := strings.Join(names, ","), strings.Join([]string{tierStatic, tierUnit, tierComponent, tierContract, tierIntegration}, ","); got != want {
		t.Errorf("--all plan tiers %s; want %s", got, want)
	}

	g := stubGateRunner()
	var gotAll bool
	g.plan = func(_ string, _ int, all bool) (resolvedSelector, error) {
		gotAll = all
		return resolvedSelector{}, nil
	}
	v := g.run(gateOptions{root: ".", base: "main", rdepsDepth: defaultRdepsDepth, all: true, environment: "lenny-gate:test"})
	if !gotAll || v.Gate == nil || !v.Gate.All || !strings.HasSuffix(v.Command, " --all") {
		t.Errorf("--all did not reach the plan or the verdict: plan all %v, gate %+v, command %q", gotAll, v.Gate, v.Command)
	}
}

func TestGateDockerBuildArgsWriteTheTagIntoTheImage(t *testing.T) {
	args := strings.Join(gateDockerBuildArgs("/w", "lenny-gate:abc"), " ")
	if !strings.Contains(args, "--build-arg LENNY_GATE_TAG=lenny-gate:abc") || !strings.Contains(args, "-t lenny-gate:abc") {
		t.Errorf("build args %q", args)
	}
}

// TestGateDockerfileWritesTheMarkerAndPinsItsSources keeps the image
// definition in step with gateEnvironment and the supply-chain pins:
// every base image is pinned by digest, the marker file is written from
// the build argument, and envtest is checked against its SHA-512.
func TestGateDockerfileWritesTheMarkerAndPinsItsSources(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(), gateDockerfile))
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(line, "FROM ") && !strings.Contains(line, "@sha256:") {
			t.Errorf("base image not pinned by digest: %s", line)
		}
	}
	for _, want := range []string{"ARG LENNY_GATE_TAG", gateImageMarker, "sha512sum -c"} {
		if !strings.Contains(src, want) {
			t.Errorf("%s lacks %q", gateDockerfile, want)
		}
	}
	if strings.Contains(src, "apt-get install") {
		t.Errorf("%s installs an unpinned package from apt", gateDockerfile)
	}
}

// TestGateTakesTheMergeGateNameAndRefusesTheOthers pins the gate
// command's argument of change 0116 (CS-7): `lenny-test gate merge` runs
// the merge gate, a leading or trailing `merge` is accepted, the area and
// release gates are refused with the run command that bounds a selection
// by them, and any other word is refused rather than ignored. It tests
// contributor tooling, so it carries no spec annotation.
func TestGateTakesTheMergeGateNameAndRefusesTheOthers(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{nil, nil},
		{[]string{"merge"}, []string{}},
		{[]string{"merge", "--here", "--base", "main"}, []string{"--here", "--base", "main"}},
		{[]string{"--here"}, []string{"--here"}},
	} {
		got, err := gateCommandArgs(tc.args)
		if err != nil || strings.Join(got, " ") != strings.Join(tc.want, " ") {
			t.Errorf("gateCommandArgs(%q) = %q, %v; want %q", tc.args, got, err, tc.want)
		}
	}
	for _, name := range []string{gateArea, gateRelease} {
		_, err := gateCommandArgs([]string{name, "--here"})
		if err == nil || !strings.Contains(err.Error(), "run --gate "+name) {
			t.Errorf("gate %s: %v; want a refusal naming `run --gate %s`", name, err, name)
		}
	}
	if _, err := gateCommandArgs([]string{"nightly"}); err == nil || !strings.Contains(err.Error(), `"nightly"`) {
		t.Errorf("an unknown gate name is accepted or unnamed: %v", err)
	}
	if err := gateRejectPositional(nil); err != nil {
		t.Errorf("no arguments refused: %v", err)
	}
	if err := gateRejectPositional([]string{"merge"}); err != nil {
		t.Errorf("a trailing merge is refused: %v", err)
	}
	if err := gateRejectPositional([]string{"extra"}); err == nil || !strings.Contains(err.Error(), `"extra"`) {
		t.Errorf("a positional argument is accepted or unnamed: %v", err)
	}
	for _, args := range [][]string{{"area"}, {"release", "--here"}, {"--base", "main", "release"}, {"nightly"}} {
		if code := runGate(args); code != 2 {
			t.Errorf("runGate(%q) = %d; want the usage exit code 2", args, code)
		}
	}
}

// writeGateTree writes a tree holding a gate Dockerfile and returns its
// root and the tag the Dockerfile defines.
func writeGateTree(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	df := []byte("FROM scratch\n")
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(gateDockerfile)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, gateDockerfile), df, 0o644); err != nil {
		t.Fatal(err)
	}
	return root, gateImageTag(df)
}

func TestGateEnvironmentRecognizesOnlyTheGateImage(t *testing.T) {
	root, tag := writeGateTree(t)
	marker := filepath.Join(t.TempDir(), "image")
	envOf := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	env, err := gateEnvironment(root, marker, envOf(nil))
	if err == nil || !strings.HasPrefix(env, "host ") || !strings.Contains(err.Error(), gateEnvImage+" is not set") {
		t.Errorf("no image variable: env %q, err %v; want the host and an error", env, err)
	}
	if _, err := gateEnvironment(root, marker, envOf(map[string]string{gateEnvImage: "lenny-gate:other"})); err == nil || !strings.Contains(err.Error(), "is not the image") {
		t.Errorf("a tag the Dockerfile does not define was accepted: %v", err)
	}
	if _, err := gateEnvironment(root, marker, envOf(map[string]string{gateEnvImage: tag})); err == nil || !strings.Contains(err.Error(), "not the gate image") {
		t.Errorf("an environment without the marker was accepted: %v", err)
	}
	if err := os.WriteFile(marker, []byte("lenny-gate:other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gateEnvironment(root, marker, envOf(map[string]string{gateEnvImage: tag})); err == nil {
		t.Error("a marker naming another tag was accepted")
	}
	if err := os.WriteFile(marker, []byte(tag+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env, err = gateEnvironment(root, marker, envOf(map[string]string{gateEnvImage: tag, gateEnvImageID: "sha256:feed"}))
	if err != nil || env != tag+"@sha256:feed" {
		t.Errorf("the gate image: env %q, err %v", env, err)
	}
}

func TestGateOutsideTheGateImageIsUnverifiedWithTheReason(t *testing.T) {
	v := stubGateRunner().run(gateOptions{root: ".", base: "main", rdepsDepth: defaultRdepsDepth, environment: "host linux/amd64", environmentErr: errors.New("LENNY_GATE_IMAGE is not set")})
	if v.Verdict != verdictstatus.VerdictUnverified {
		t.Fatalf("verdict %q; want %q outside the gate image", v.Verdict, verdictstatus.VerdictUnverified)
	}
	if exitCodeFor(v.Verdict) == 0 {
		t.Error("an unverified gate exits 0")
	}
	if len(v.Gate.Unverified) != 1 || !strings.Contains(v.Gate.Unverified[0], "LENNY_GATE_IMAGE is not set") {
		t.Errorf("unverified reasons %q", v.Gate.Unverified)
	}
}

// TestGateWithANarrowedDependentWalkIsUnverified pins that a gate run with
// --rdeps-depth at or above zero cannot pass: the merge gate runs the
// changed packages and every dependent, and the verdict records the depth.
func TestGateWithANarrowedDependentWalkIsUnverified(t *testing.T) {
	v := stubGateRunner().run(gateOptions{root: ".", base: "main", rdepsDepth: 1, environment: "lenny-gate:test"})
	if v.Verdict != verdictstatus.VerdictUnverified || v.Gate.RdepsDepth != 1 {
		t.Fatalf("verdict %q, depth %d; want %q and 1", v.Verdict, v.Gate.RdepsDepth, verdictstatus.VerdictUnverified)
	}
	if len(v.Gate.Unverified) != 1 || !strings.Contains(v.Gate.Unverified[0], "--rdeps-depth 1") {
		t.Errorf("unverified reasons %q", v.Gate.Unverified)
	}
	if all := stubGateRunner().run(gateOptions{root: ".", base: "main", rdepsDepth: 0, all: true, environment: "lenny-gate:test"}); all.Verdict != verdictstatus.VerdictPass {
		t.Errorf("--all selects every package whatever the depth; verdict %q (%v)", all.Verdict, all.Gate.Unverified)
	}
}

func TestGateOnADirtyTreeIsUnverifiedWithTheReason(t *testing.T) {
	g := stubGateRunner()
	g.state = func(string) (string, string, bool) { return "abc123", "def456", true }
	v := runStubGate(g)
	if v.Verdict != verdictstatus.VerdictUnverified || exitCodeFor(v.Verdict) == 0 {
		t.Fatalf("verdict %q; want %q with a non-zero exit on a dirty tree", v.Verdict, verdictstatus.VerdictUnverified)
	}
	if !v.Gate.Dirty || len(v.Gate.Unverified) != 1 || !strings.Contains(v.Gate.Unverified[0], "uncommitted changes") {
		t.Errorf("gate record %+v", v.Gate)
	}
	var b strings.Builder
	v.finalize()
	printGateSummary(&b, v, "x")
	if !strings.Contains(b.String(), "unverified:  the worktree has uncommitted changes") {
		t.Errorf("summary does not give the reason:\n%s", b.String())
	}
}

func TestGateFailureStaysFailOnADirtyTreeOutsideTheImage(t *testing.T) {
	g := stubGateRunner()
	g.state = func(string) (string, string, bool) { return "abc123", "def456", true }
	g.static = func() (string, string, *tierResult) {
		return verdictstatus.Fail, "vet", &tierResult{Total: 1, Failed: 1}
	}
	v := g.run(gateOptions{root: ".", base: "main", rdepsDepth: defaultRdepsDepth, environmentErr: errors.New("outside")})
	if v.Verdict != verdictstatus.VerdictFail {
		t.Fatalf("verdict %q; want FAIL to outrank the unverified reasons", v.Verdict)
	}
	if len(v.Gate.Unverified) != 2 {
		t.Errorf("unverified reasons %q; want the dirty tree and the environment", v.Gate.Unverified)
	}
}

func TestGateImageNeedsBuildUnlessTheTagNamesTheRecordedImage(t *testing.T) {
	cases := []struct {
		rebuild           bool
		current, recorded string
		want              bool
	}{
		{false, "sha256:a", "sha256:a", false},
		{true, "sha256:a", "sha256:a", true},
		{false, "", "sha256:a", true},
		{false, "sha256:a", "", true},
		{false, "sha256:b", "sha256:a", true},
	}
	for _, c := range cases {
		got, why := gateImageNeedsBuild(c.rebuild, c.current, c.recorded)
		if got != c.want || (got && why == "") {
			t.Errorf("gateImageNeedsBuild(%v, %q, %q) = %v, %q; want %v with a reason", c.rebuild, c.current, c.recorded, got, why, c.want)
		}
	}
}

func TestGateImageStoreRecordsTheIDPerTag(t *testing.T) {
	s := gateImageStore{dir: filepath.Join(t.TempDir(), "gate-images")}
	if got := s.load("lenny-gate:a"); got != "" {
		t.Errorf("empty store loaded %q", got)
	}
	if err := s.save("lenny-gate:a", "sha256:1"); err != nil {
		t.Fatal(err)
	}
	if got := s.load("lenny-gate:a"); got != "sha256:1" {
		t.Errorf("loaded %q; want sha256:1", got)
	}
	if got := s.load("lenny-gate:b"); got != "" {
		t.Errorf("another tag loaded %q", got)
	}
}

func TestAppendHistoryDoesNotFollowASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, verdictHistoryFile)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := appendHistory(link, runStubGate(stubGateRunner())); err == nil {
		t.Error("appendHistory wrote through a symbolic link")
	}
	if b, _ := os.ReadFile(target); string(b) != "keep\n" {
		t.Errorf("the link target changed: %q", b)
	}
}

// TestMatchOwnerChangesALinkRatherThanItsTarget runs only as root, which
// is how the gate container runs it.
func TestMatchOwnerChangesALinkRatherThanItsTarget(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("blocked: matchOwner acts only as root; the gate container runs this test as root")
	}
	root := t.TempDir()
	if err := os.Chown(root, 4242, 4242); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	matchOwner(link, root)
	var tst, lst syscall.Stat_t
	if err := syscall.Stat(target, &tst); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Lstat(link, &lst); err != nil {
		t.Fatal(err)
	}
	if tst.Uid == 4242 {
		t.Error("matchOwner followed the link and changed its target's owner")
	}
	if lst.Uid != 4242 {
		t.Errorf("the link's owner is %d; want 4242", lst.Uid)
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
// documentation gates in step with tests/docs.
func TestGateDocsFilesExistInTheTree(t *testing.T) {
	names, err := gateDocsTestNames(filepath.Join(repoRoot(), "tests", "docs"), gateDocsFiles)
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
