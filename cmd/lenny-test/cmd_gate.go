// SPDX-License-Identifier: MIT

package main

// The merge gate: `lenny-test gate` runs the tiers a branch must pass
// before it merges, in one canonical Linux environment, and writes a
// verdict file whose run identifier the step's digest records.
//
// The command has two halves. On the developer's host it builds the gate
// image from tests/gate/Dockerfile when that image is absent, then runs
// itself inside a container of it with the worktree mounted at the same
// path. Inside the container (or on any host, with --here) it runs the
// gate's tiers and writes the verdict.
//
// The gate implements the testing model rather than a specification
// behavior, so nothing here carries a spec annotation; the merge policy
// lives in .claude/rules/test-coverage.md and .claude/rules/git-workflow.md.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
)

const (
	// gateDockerfile is the repo-relative image definition that pins
	// the gate's toolchain.
	gateDockerfile = "tests/gate/Dockerfile"
	// gateImageRepo names the local image. The tag is a digest of the
	// Dockerfile, so editing a pinned version builds a new image.
	gateImageRepo = "lenny-gate"
	// gateModVolume and gateBuildVolume are the named volumes that keep
	// the Go module cache and the build cache between gate runs.
	gateModVolume   = "lenny-gate-gomod"
	gateBuildVolume = "lenny-gate-gocache"
	// gateResultsDir is where the verdict files go, relative to the
	// repository root.
	gateResultsDir = "tests/results"
	// gateLatestFile is overwritten by every gate run.
	gateLatestFile = "gate-latest.json"
	// gateEnvImage carries the image reference into the container so
	// the verdict names the environment it ran in.
	gateEnvImage = "LENNY_GATE_IMAGE"
	// gateContainerSocket is where the container sees the Docker socket.
	gateContainerSocket = "/var/run/docker.sock"
	// gateNotRunPrefix opens the reason of a tier the gate selected but
	// could not run in its environment.
	gateNotRunPrefix = "not run: "
)

// gateChangedTiers are the tiers the gate takes from the --changed plan,
// in execution order. Tier 0 and the general documentation gates run on
// every branch whatever the diff touches.
var gateChangedTiers = []string{tierUnit, tierComponent, tierContract, tierIntegration}

// gateDocsFiles are the general documentation gates of tier 11 that the
// merge gate runs. They check the published documentation as a whole
// (links, code blocks, runbooks, alerts, ADRs, the quickstart, the
// embedded manifests, and the harness inventories) rather than the
// wording one past fix introduced, which is why they gate every merge
// while the rest of tier 11 runs at the area gate.
var gateDocsFiles = []string{
	"adr_test.go",
	"alert_runbook_test.go",
	"alerting_docs_reference_test.go",
	"code_blocks_test.go",
	"conformance_readme_skip_state_test.go",
	"docs_test.go",
	"embedded_manifests_sync_test.go",
	"production_mode_docs_test.go",
	"published_pages_cross_platform_test.go",
	"reference_document_freeze_test.go",
	"runbook_index_count_test.go",
	"runbook_steps_test.go",
	"runbooks_test.go",
	"static_tier_check_inventory_test.go",
	"time_to_hello_world_test.go",
	"verdict_enum_test.go",
}

// gateOptions are the inputs of one gate run.
type gateOptions struct {
	root        string
	base        string
	rdepsDepth  int
	environment string
}

// gateRecord is the gate's own section of the verdict.
type gateRecord struct {
	// Base is the ref the branch is compared with, and MergeBase the
	// commit the comparison starts from.
	Base      string `json:"base"`
	MergeBase string `json:"merge_base,omitempty"`
	// Dirty reports uncommitted changes in the worktree. A verdict for
	// a dirty tree is not a verdict for the branch head.
	Dirty bool `json:"dirty"`
	// Environment names the image the gate ran in, or the host.
	Environment string `json:"environment"`
	// NotRun lists the tiers the gate selected but could not run, with
	// the reason. A gate with any entry here is not green.
	NotRun []string `json:"not_run,omitempty"`
}

// runGate handles `lenny-test gate`.
func runGate(args []string) int {
	fs := flag.NewFlagSet("gate", flag.ExitOnError)
	base := fs.String("base", "main", "the ref the branch merges into; the changed tiers are selected from the diff against its merge base")
	here := fs.Bool("here", false, "run the gate in this environment instead of the gate container (the container itself runs the gate this way)")
	rebuild := fs.Bool("rebuild", false, "rebuild the gate image even when it exists")
	noSocket := fs.Bool("no-docker-socket", false, "do not give the gate container the host Docker socket; the tiers that need Docker are reported as not run")
	rdeps := fs.Int("rdeps-depth", defaultRdepsDepth, "how many hops of in-module importers of a changed package join the unit tier (-1: the full transitive closure)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	root := repoRoot()
	if *here {
		env := os.Getenv(gateEnvImage)
		if env == "" {
			env = "host " + runtime.GOOS + "/" + runtime.GOARCH
		}
		v := defaultGateRunner(root).run(gateOptions{root: root, base: *base, rdepsDepth: *rdeps, environment: env})
		return finishGate(root, v)
	}
	passArgs := []string{"--base", *base, "--rdeps-depth", strconv.Itoa(*rdeps)}
	return runGateInContainer(root, passArgs, *rebuild, !*noSocket)
}

// runGateInContainer builds the gate image when needed and runs the gate
// inside a container of it, returning the container's exit code.
func runGateInContainer(root string, passArgs []string, rebuild, withSocket bool) int {
	if _, err := exec.LookPath("docker"); err != nil {
		fmt.Fprintln(os.Stderr, "lenny-test gate: docker not on PATH; install Docker, or run `lenny-test gate --here` on a Linux host with the gate's toolchain")
		return 2
	}
	dockerfile, err := os.ReadFile(filepath.Join(root, gateDockerfile))
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test gate: read %s: %v\n", gateDockerfile, err)
		return 2
	}
	image := gateImageTag(dockerfile)
	if rebuild || !dockerImageExists(image) {
		fmt.Fprintf(os.Stderr, "lenny-test gate: building %s from %s\n", image, gateDockerfile)
		if err := runStreaming("docker", gateDockerBuildArgs(root, image)...); err != nil {
			fmt.Fprintf(os.Stderr, "lenny-test gate: build the gate image: %v\n", err)
			return 2
		}
	}
	h := gateHost{root: root, image: image, passArgs: passArgs}
	if common, err := gitCommonDir(root); err == nil {
		h.gitCommonDir = common
	} else {
		fmt.Fprintf(os.Stderr, "lenny-test gate: warning: locate the git directory: %v\n", err)
	}
	if withSocket {
		h.dockerSocket = hostDockerSocket()
	}
	err = runStreaming("docker", gateDockerRunArgs(h)...)
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test gate: run the gate container: %v\n", err)
		return 2
	}
	return 0
}

// gateImageTag derives the image reference from the Dockerfile content.
func gateImageTag(dockerfile []byte) string {
	sum := sha256.Sum256(dockerfile)
	return gateImageRepo + ":" + hex.EncodeToString(sum[:])[:12]
}

// gateDockerBuildArgs is the argv of `docker build` for the gate image.
// The build context is the image's own directory, so a build never sends
// the repository to the daemon.
func gateDockerBuildArgs(root, image string) []string {
	dir := filepath.Join(root, filepath.Dir(gateDockerfile))
	return []string{"build", "-t", image, "-f", filepath.Join(root, gateDockerfile), dir}
}

// gateHost holds what the host half of the gate passes to docker run.
type gateHost struct {
	root         string
	gitCommonDir string // "" when it lies inside root or is unknown
	image        string
	dockerSocket string // "" when the container gets no Docker socket
	passArgs     []string
}

// gateDockerRunArgs is the argv of `docker run` for one gate run.
//
// The worktree is mounted at its host path, so absolute paths in the
// tree, in git's worktree metadata, and in tool output mean the same
// thing inside and outside the container. A linked worktree keeps its
// git metadata in the main checkout's git directory, which is mounted
// read-only at its host path for the same reason. The module and build
// caches live in named volumes, so only the first run downloads and
// compiles from cold. The container builds lenny-test from the mounted
// tree and runs `lenny-test gate --here`, so the gate always runs the
// harness of the commit under test.
func gateDockerRunArgs(h gateHost) []string {
	args := []string{
		"run", "--rm", "--init",
		"-v", h.root + ":" + h.root,
		"-w", h.root,
	}
	if h.gitCommonDir != "" && !pathWithin(h.gitCommonDir, h.root) {
		args = append(args, "-v", h.gitCommonDir+":"+h.gitCommonDir+":ro")
	}
	args = append(
		args,
		"-v", gateModVolume+":/go/pkg/mod",
		"-v", gateBuildVolume+":/root/.cache/go-build",
		"-e", gateEnvImage+"="+h.image,
	)
	if h.dockerSocket != "" {
		// Testcontainers started from inside the container publish
		// their ports on the Docker host, which the container reaches
		// as host.docker.internal.
		args = append(
			args,
			"-v", h.dockerSocket+":"+gateContainerSocket,
			"--add-host", "host.docker.internal:host-gateway",
			"-e", "TESTCONTAINERS_HOST_OVERRIDE=host.docker.internal",
		)
	}
	script := `go build -o /usr/local/bin/lenny-test ./cmd/lenny-test && exec lenny-test gate --here "$@"`
	args = append(args, h.image, "sh", "-c", script, "lenny-test-gate")
	return append(args, h.passArgs...)
}

// pathWithin reports whether p is root or lies under it.
func pathWithin(p, root string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

// hostDockerSocket returns the host path of the Docker socket to mount,
// or "" when none is found. Docker Desktop serves /var/run/docker.sock to
// containers whatever the macOS filesystem holds at that path, so on
// macOS that path is always used.
func hostDockerSocket() string {
	if runtime.GOOS == "darwin" {
		return gateContainerSocket
	}
	if h := os.Getenv("DOCKER_HOST"); strings.HasPrefix(h, "unix://") {
		if p := strings.TrimPrefix(h, "unix://"); fileExists(p) {
			return p
		}
	}
	if fileExists(gateContainerSocket) {
		return gateContainerSocket
	}
	return ""
}

// fileExists reports whether p exists.
func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// gitCommonDir returns the absolute path of the repository's common git
// directory, which a linked worktree's .git file points into.
func gitCommonDir(root string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --git-common-dir: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// dockerImageExists reports whether the local daemon holds image.
func dockerImageExists(image string) bool {
	return exec.Command("docker", "image", "inspect", image).Run() == nil
}

// runStreaming runs a command with the caller's standard streams.
func runStreaming(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// gateRunner holds the tier executors one gate run calls. Tests replace
// them to run the gate's control flow without go test or Docker.
type gateRunner struct {
	static func() (string, string, *tierResult)
	plan   func(base string, rdepsDepth int) (resolvedSelector, error)
	exec   map[string]tierExecFn
	// precheck returns why a tier cannot run in this environment, or
	// nil when it can.
	precheck map[string]func() error
	docs     func() (string, string, *tierResult)
	// state reads the commit, the merge base, and whether the tree is
	// dirty.
	state func(base string) (head, mergeBase string, dirty bool)
}

// defaultGateRunner wires the gate to the harness's tier executors.
func defaultGateRunner(root string) gateRunner {
	steps := tierSteps()
	execs := map[string]tierExecFn{}
	for _, name := range gateChangedTiers {
		execs[name] = steps[name].exec
	}
	return gateRunner{
		static: runStaticTier,
		plan:   gateChangedPlan,
		exec:   execs,
		precheck: map[string]func() error{
			tierComponent: func() error {
				if err := dockerReachable(); err != nil {
					return err
				}
				return envtestAssetsPresent()
			},
			tierIntegration: dockerReachable,
		},
		docs:  func() (string, string, *tierResult) { return runGateDocs(root) },
		state: func(base string) (string, string, bool) { return gitState(root, base) },
	}
}

// gateChangedPlan resolves the --changed plan against base, capped at
// the integration tier.
func gateChangedPlan(base string, rdepsDepth int) (resolvedSelector, error) {
	sel := selector{changed: true, since: base, maxTier: tierIntegration, rdepsDepth: rdepsDepth}
	return sel.resolve()
}

// run executes the gate and returns its verdict. Every tier runs whatever
// an earlier tier reported, so the verdict carries every failure.
func (g gateRunner) run(o gateOptions) *verdict {
	sel := selector{changed: true, since: o.base, rdepsDepth: o.rdepsDepth}
	v := newVerdict(sel)
	v.Command = fmt.Sprintf("lenny-test gate --base %s --rdeps-depth %d", o.base, o.rdepsDepth)
	v.Trigger.Mode = "gate"
	head, mergeBase, dirty := g.state(o.base)
	if head != "" {
		v.Trigger.GitRevision = head
	}
	v.Gate = &gateRecord{Base: o.base, MergeBase: mergeBase, Dirty: dirty, Environment: o.environment}

	g.record(v, tierStatic, g.static)

	plan, err := g.plan(o.base, o.rdepsDepth)
	if err != nil {
		v.recordTier(tierUnit, verdictstatus.Fail, 0, fmt.Sprintf("resolve the changed tiers against %s: %v", o.base, err))
	}
	for _, n := range plan.notes {
		fmt.Fprintf(os.Stderr, "lenny-test gate: note: %s\n", n)
	}
	for _, p := range plan.tiers {
		if p.name == tierStatic {
			continue
		}
		g.runPlannedTier(v, p)
	}

	g.record(v, tierDocs, g.docs)
	return v
}

// runPlannedTier runs one tier of the changed plan, or records it as not
// run when the environment cannot run it.
func (g gateRunner) runPlannedTier(v *verdict, p tierPlan) {
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
	g.record(v, p.name, func() (string, string, *tierResult) { return exec(p) })
}

// record runs one tier executor and records its outcome. A tier the gate
// selected that reports skipped did not run, so it is recorded as not
// run rather than as a pass.
func (g gateRunner) record(v *verdict, name string, fn func() (string, string, *tierResult)) {
	start := time.Now()
	fmt.Fprintf(os.Stderr, "lenny-test gate: running the %s tier\n", name)
	st, msg, result := fn()
	if st == verdictstatus.Skipped {
		g.notRun(v, name, errors.New(msg))
		return
	}
	v.recordTierWithResult(name, st, time.Since(start), msg, result)
}

// notRun records a selected tier the gate could not run. The tier is
// unverified, which keeps the verdict from passing.
func (g gateRunner) notRun(v *verdict, name string, why error) {
	reason := gateNotRunPrefix + why.Error()
	v.recordTier(name, verdictstatus.Unverified, 0, reason)
	v.Gate.NotRun = append(v.Gate.NotRun, name+": "+why.Error())
}

// dockerReachable reports why the Docker daemon cannot be used, or nil.
func dockerReachable() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return errors.New("docker CLI not on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "docker", "info").CombinedOutput(); err != nil {
		return fmt.Errorf("docker daemon not reachable (run the gate without --no-docker-socket): %s", firstLine(string(out)))
	}
	return nil
}

// envtestAssetsPresent reports why the envtest binaries are missing, or
// nil. The gate image sets KUBEBUILDER_ASSETS.
func envtestAssetsPresent() error {
	dir := os.Getenv("KUBEBUILDER_ASSETS")
	if dir == "" {
		return errors.New("KUBEBUILDER_ASSETS is not set, so envtest has no kube-apiserver or etcd")
	}
	if !fileExists(filepath.Join(dir, "kube-apiserver")) {
		return fmt.Errorf("no kube-apiserver under KUBEBUILDER_ASSETS=%s", dir)
	}
	return nil
}

// firstLine returns the first non-empty line of s.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// gitState returns HEAD, the merge base of HEAD and base, and whether
// the worktree has uncommitted changes. A value git cannot produce is
// left empty.
func gitState(root, base string) (head, mergeBase string, dirty bool) {
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	head = git("rev-parse", "HEAD")
	mergeBase = git("merge-base", base, "HEAD")
	dirty = git("status", "--porcelain", "--untracked-files=normal") != ""
	return head, mergeBase, dirty
}

// runGateDocs runs the general documentation gates of tier 11.
func runGateDocs(root string) (string, string, *tierResult) {
	names, err := gateDocsTestNames(filepath.Join(root, "tests", "tier11_docs"), gateDocsFiles)
	if err != nil {
		return verdictstatus.Fail, err.Error(), nil
	}
	pattern := "^(" + strings.Join(names, "|") + ")$"
	return runTierArgs("docs", []string{"-count=1", fmt.Sprintf("-timeout=%s", tierDocsTimeout), "-run", pattern, "./tests/tier11_docs/"})
}

// gateDocsTestNames returns the top-level test functions the given files
// in dir declare, sorted. A listed file that is missing or declares no
// test is an error, so a renamed gate cannot drop out of the merge gate
// silently.
func gateDocsTestNames(dir string, files []string) ([]string, error) {
	var names []string
	for _, f := range files {
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, f), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("read the documentation gate %s: %w", f, err)
		}
		found := 0
		for _, d := range file.Decls {
			name := testFuncName(d)
			if name == "" {
				continue
			}
			names = append(names, name)
			found++
		}
		if found == 0 {
			return nil, fmt.Errorf("the documentation gate %s declares no test", f)
		}
	}
	sort.Strings(names)
	return names, nil
}

// testFuncName returns the name of a top-level `func TestXxx(t
// *testing.T)` declaration, or "" for any other declaration.
func testFuncName(d ast.Decl) string {
	fn, ok := d.(*ast.FuncDecl)
	if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") {
		return ""
	}
	if fn.Name.Name == "TestMain" || fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
		return ""
	}
	return fn.Name.Name
}

// finishGate writes the verdict, prints the summary, and returns the
// exit code.
func finishGate(root string, v *verdict) int {
	path, err := writeGateVerdict(root, v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test gate: write the verdict: %v\n", err)
		return 2
	}
	printGateSummary(os.Stdout, v, path)
	return exitCodeFor(v.Verdict)
}

// writeGateVerdict writes the verdict to tests/results/gate-<run>.json
// and to tests/results/gate-latest.json, appends it to the run history,
// and returns the repo-relative path of the run's own file.
func writeGateVerdict(root string, v *verdict) (string, error) {
	v.finalize()
	dir := filepath.Join(root, gateResultsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	payload, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	payload = append(payload, '\n')
	name := "gate-" + v.RunID + ".json"
	for _, f := range []string{name, gateLatestFile} {
		p := filepath.Join(dir, f)
		if err := writeFileAtomic(p, payload, 0o644); err != nil {
			return "", err
		}
		matchOwner(p, root)
	}
	if err := appendHistory(filepath.Join(dir, verdictHistoryFile), v); err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test gate: append history: %v\n", err)
	}
	return filepath.ToSlash(filepath.Join(gateResultsDir, name)), nil
}

// matchOwner gives p the owner of root when the gate runs as root, so a
// verdict the container writes into a Linux host's worktree belongs to
// the worktree's owner. It is best-effort.
func matchOwner(p, root string) {
	if os.Geteuid() != 0 {
		return
	}
	info, err := os.Stat(root)
	if err != nil {
		return
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		_ = os.Chown(p, int(st.Uid), int(st.Gid))
	}
}

// printGateSummary writes the human summary of a gate run.
func printGateSummary(w io.Writer, v *verdict, path string) {
	fmt.Fprintf(w, "\nlenny-test gate: %s\n", v.Verdict)
	fmt.Fprintf(w, "  run:         %s\n", v.RunID)
	fmt.Fprintf(w, "  commit:      %s\n", v.Trigger.GitRevision)
	if v.Gate != nil {
		fmt.Fprintf(w, "  base:        %s (merge base %s)\n", v.Gate.Base, v.Gate.MergeBase)
		fmt.Fprintf(w, "  environment: %s\n", v.Gate.Environment)
		if v.Gate.Dirty {
			fmt.Fprintln(w, "  tree:        dirty; this verdict is not a verdict for the commit")
		}
	}
	for _, name := range allTiers() {
		t, ok := v.Tiers[name]
		if !ok || t.Status == verdictstatus.NotSelected {
			continue
		}
		line := fmt.Sprintf("  %-12s %s", name, t.Status)
		if t.Total > 0 {
			line += fmt.Sprintf(" (%d passed, %d failed, %d skipped)", t.Passed, t.Failed, t.Skipped)
		}
		if strings.HasPrefix(t.Reason, gateNotRunPrefix) {
			line += ": " + t.Reason
		}
		fmt.Fprintln(w, line)
		for _, f := range t.Failures {
			fmt.Fprintf(w, "    FAIL %s\n", f.Test)
		}
	}
	fmt.Fprintf(w, "  verdict:     %s\n", path)
}
