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
// gate's tiers and writes the verdict. The container is the canonical
// environment, so a --here run anywhere else, and a run on a tree with
// uncommitted changes, ends no better than UNVERIFIED.
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
	"github.com/lennylabs/lenny/tests/testinfra/testtmp"
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
	// gateEnvImageID carries the ID of the image the container runs.
	gateEnvImageID = "LENNY_GATE_IMAGE_ID"
	// gateImageMarker is the file the gate image writes its own tag to
	// at build time (the LENNY_GATE_TAG build argument). A --here run
	// whose environment lacks it, or whose marker names another tag, is
	// not in the gate image.
	gateImageMarker = "/etc/lenny-gate/image"
	// gateGoTmpDir is where go build keeps its work directories inside
	// the container. It stays off the fixture root's bind mount, where compiling is
	// slower, and off /tmp (testtmp.UnderTmp).
	gateGoTmpDir = "/var/tmp/lenny-go"
	// gateContainerSocket is where the container sees the Docker socket.
	gateContainerSocket = "/var/run/docker.sock"
	// gateNotRunPrefix opens the reason of a tier the gate selected but
	// could not run in its environment.
	gateNotRunPrefix = "not run: "
)

// gateChangedTiers are the tiers the gate takes from the --changed plan,
// in execution order. The static tier and the general documentation gates run on
// every branch whatever the diff touches.
var gateChangedTiers = []string{tierUnit, tierComponent, tierContract, tierIntegration}

// gateDocsFiles are the general documentation gates of the docs tier that the
// merge gate runs. They check the published documentation as a whole
// (links, code blocks, runbooks, alerts, ADRs, the quickstart, the
// embedded manifests, and the harness inventories) rather than the
// wording one past fix introduced, which is why they gate every merge
// while the rest of the docs tier runs at the area gate.
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
	// environmentErr says why the environment is not the gate image, or
	// is nil when it is.
	environmentErr error
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
	// Unverified lists why the verdict cannot stand for the commit even
	// when every tier passed: uncommitted changes in the tree, or a run
	// outside the gate image. Any entry keeps the verdict from PASS.
	Unverified []string `json:"unverified,omitempty"`
}

// runGate handles `lenny-test gate`.
func runGate(args []string) int {
	fs := flag.NewFlagSet("gate", flag.ExitOnError)
	base := fs.String("base", "main", "the ref the branch merges into; the changed tiers are selected from the diff against its merge base")
	here := fs.Bool("here", false, "run the gate in this environment instead of the gate container (the container itself runs the gate this way); outside the gate image the verdict is at best UNVERIFIED")
	rebuild := fs.Bool("rebuild", false, "rebuild the gate image even when it exists")
	noSocket := fs.Bool("no-docker-socket", false, "do not give the gate container the host Docker socket; the tiers that need Docker are reported as not run")
	rdeps := fs.Int("rdeps-depth", defaultRdepsDepth, "how many hops of in-module importers of a changed package join the unit tier (-1: the full transitive closure)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := gateRejectPositional(fs.Args()); err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test gate: %v\n", err)
		fs.Usage()
		return 2
	}
	root := repoRoot()
	if *here {
		env, envErr := gateEnvironment(root, gateImageMarker, os.Getenv)
		v := defaultGateRunner(root).run(gateOptions{root: root, base: *base, rdepsDepth: *rdeps, environment: env, environmentErr: envErr})
		return finishGate(root, v)
	}
	passArgs := []string{"--base", *base, "--rdeps-depth", strconv.Itoa(*rdeps)}
	return runGateInContainer(root, passArgs, *rebuild, !*noSocket)
}

// gateRejectPositional refuses positional arguments. The gate takes
// flags only. Change 0116 adds the choice of gate (merge, area, or
// release) as a flag; until then a word such as `merge` is refused
// rather than ignored.
func gateRejectPositional(args []string) error {
	if len(args) == 0 {
		return nil
	}
	return fmt.Errorf("unexpected argument %q: the gate takes flags only", args[0])
}

// gateEnvironment names the environment a --here run is in, and reports
// why it is not the gate image, or nil when it is. The gate image is the
// one tests/gate/Dockerfile at this commit defines: the container
// carries its tag in LENNY_GATE_IMAGE, the tag matches the Dockerfile in
// the tree, and the image's marker file names the same tag. The check
// catches a run outside the container. It does not authenticate the
// environment, because any process can set the variable and write the
// file.
func gateEnvironment(root, marker string, getenv func(string) string) (string, error) {
	image := getenv(gateEnvImage)
	if image == "" {
		return "host " + runtime.GOOS + "/" + runtime.GOARCH, fmt.Errorf("%s is not set, so the gate ran outside the gate image; the merge gate's environment is the gate container, which `lenny-test gate` without --here runs", gateEnvImage)
	}
	env := image
	if id := getenv(gateEnvImageID); id != "" {
		env += "@" + id
	}
	dockerfile, err := os.ReadFile(filepath.Join(root, gateDockerfile))
	if err != nil {
		return env, fmt.Errorf("read %s to check the gate image: %w", gateDockerfile, err)
	}
	if want := gateImageTag(dockerfile); image != want {
		return env, fmt.Errorf("%s=%s is not the image %s defines at this commit (%s)", gateEnvImage, image, gateDockerfile, want)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		return env, fmt.Errorf("the environment is not the gate image %s: read %s: %w", image, marker, err)
	}
	if name := strings.TrimSpace(string(got)); name != image {
		return env, fmt.Errorf("the environment is not the gate image %s: %s names %q", image, marker, name)
	}
	return env, nil
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
	store, err := defaultGateImageStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test gate: %v\n", err)
		return 2
	}
	id, err := ensureGateImage(root, image, rebuild, store)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test gate: %v\n", err)
		return 2
	}
	tmp, err := gateHostTmpDir(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test gate: %v\n", err)
		return 2
	}
	h := gateHost{root: root, image: image, imageID: id, tmpDir: tmp, passArgs: passArgs}
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

// ensureGateImage returns the ID of the gate image to run. It builds the
// image first when gateImageNeedsBuild says so, and records the ID of an
// image it builds in store.
func ensureGateImage(root, image string, rebuild bool, store gateImageStore) (string, error) {
	id := dockerImageID(image)
	build, why := gateImageNeedsBuild(rebuild, id, store.load(image))
	if !build {
		return id, nil
	}
	fmt.Fprintf(os.Stderr, "lenny-test gate: building %s from %s (%s)\n", image, gateDockerfile, why)
	if err := runStreaming("docker", gateDockerBuildArgs(root, image)...); err != nil {
		return "", fmt.Errorf("build the gate image: %w", err)
	}
	if id = dockerImageID(image); id == "" {
		return "", fmt.Errorf("the build left no image tagged %s", image)
	}
	if err := store.save(image, id); err != nil {
		return "", fmt.Errorf("record the gate image ID: %w", err)
	}
	return id, nil
}

// gateImageNeedsBuild reports whether the gate builds its image before
// running, and why. The gate reuses an image only when its tag names the
// image ID the gate recorded when it built that tag, so an image that
// another build or a pull put under the tag never runs as the gate image.
func gateImageNeedsBuild(rebuild bool, currentID, recordedID string) (bool, string) {
	switch {
	case rebuild:
		return true, "--rebuild"
	case currentID == "":
		return true, "no image has the tag"
	case recordedID == "":
		return true, "no image ID is recorded for the tag"
	case currentID != recordedID:
		return true, fmt.Sprintf("the tag names %s rather than the image the gate built, %s", currentID, recordedID)
	}
	return false, ""
}

// gateImageStore records, per image tag, the ID of the image the gate
// built under it. It lives in the user's cache directory, so every
// worktree on the machine shares it.
type gateImageStore struct{ dir string }

// defaultGateImageStore returns the store under the user cache directory.
func defaultGateImageStore() (gateImageStore, error) {
	d, err := os.UserCacheDir()
	if err != nil {
		return gateImageStore{}, fmt.Errorf("locate the cache directory for the gate image record: %w", err)
	}
	return gateImageStore{dir: filepath.Join(d, "lenny-test", "gate-images")}, nil
}

// path returns the record file of tag.
func (s gateImageStore) path(tag string) string {
	return filepath.Join(s.dir, strings.ReplaceAll(tag, ":", "_"))
}

// load returns the recorded image ID of tag, or "" when there is none.
func (s gateImageStore) load(tag string) string {
	b, err := os.ReadFile(s.path(tag))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// save records id as the image the gate built under tag.
func (s gateImageStore) save(tag, id string) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	return writeFileAtomic(s.path(tag), []byte(id+"\n"), 0o644)
}

// gateHostTmpDir creates and returns the host directory that serves as
// the container's fixture root for the worktree at root. It lies under
// the user cache directory, one per worktree, and the container mounts
// it at the same path. A test that bind-mounts a temporary directory
// into a testcontainer then names a path the host daemon resolves. It
// lies outside the worktree, because the static checks that walk the
// tree (goimports, gofumpt, and the static-tier tree scans) run while other
// checks create and remove fixtures, and would read them half-written.
func gateHostTmpDir(root string) (string, error) {
	d, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate the cache directory for the gate's fixture root: %w", err)
	}
	sum := sha256.Sum256([]byte(root))
	tmp := filepath.Join(d, "lenny-test", "gate-tmp", hex.EncodeToString(sum[:])[:12])
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return "", fmt.Errorf("create the gate's fixture root: %w", err)
	}
	return tmp, nil
}

// gateImageTag derives the image reference from the Dockerfile content.
func gateImageTag(dockerfile []byte) string {
	sum := sha256.Sum256(dockerfile)
	return gateImageRepo + ":" + hex.EncodeToString(sum[:])[:12]
}

// gateDockerBuildArgs is the argv of `docker build` for the gate image.
// The build context is the image's own directory, so a build never sends
// the repository to the daemon. The LENNY_GATE_TAG build argument writes
// the tag into the image's marker file, which gateEnvironment reads.
func gateDockerBuildArgs(root, image string) []string {
	dir := filepath.Join(root, filepath.Dir(gateDockerfile))
	return []string{"build", "-t", image, "--build-arg", "LENNY_GATE_TAG=" + image, "-f", filepath.Join(root, gateDockerfile), dir}
}

// gateHost holds what the host half of the gate passes to docker run.
type gateHost struct {
	root         string
	gitCommonDir string // "" when it lies inside root or is unknown
	image        string
	imageID      string // "" runs the image by its tag
	tmpDir       string // host directory mounted at its own path as TMPDIR
	dockerSocket string // "" when the container gets no Docker socket
	passArgs     []string
}

// gateDockerRunArgs is the argv of `docker run` for one gate run.
//
// The worktree is mounted at its host path, so absolute paths in the
// tree, in git's worktree metadata, and in tool output mean the same
// thing inside and outside the container. A linked worktree keeps its
// git metadata in the main checkout's git directory, which is mounted
// read-only at its host path for the same reason. The fixture root
// (gateHostTmpDir) is mounted at its host path too, and TMPDIR points at
// it, so a temporary directory that a test bind-mounts into a
// testcontainer is a path the host daemon resolves. The module and build
// caches live in named volumes, so only the first run downloads and
// compiles from cold. The container runs the image by the ID the gate
// recorded, checks the module cache against go.sum with `go mod verify`,
// builds lenny-test from the mounted tree, and runs `lenny-test gate
// --here`, so the gate always runs the harness of the commit under test.
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
		"-e", "GOTMPDIR="+gateGoTmpDir,
	)
	if h.tmpDir != "" {
		args = append(
			args,
			"-v", h.tmpDir+":"+h.tmpDir,
			"-e", "TMPDIR="+h.tmpDir,
			"-e", testtmp.EnvRoot+"="+h.tmpDir,
		)
	}
	if h.imageID != "" {
		args = append(args, "-e", gateEnvImageID+"="+h.imageID)
	}
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
	// The script empties the fixture root first, because a killed
	// earlier run leaves its fixtures behind, and verifies the module
	// cache before it compiles the harness from it.
	script := `set -e
if [ -n "${LENNY_TEST_TMPDIR:-}" ]; then find "$LENNY_TEST_TMPDIR" -mindepth 1 -delete; fi
mkdir -p "$GOTMPDIR"
go mod verify
go build -o /usr/local/bin/lenny-test ./cmd/lenny-test
exec lenny-test gate --here "$@"`
	ref := h.image
	if h.imageID != "" {
		ref = h.imageID
	}
	args = append(args, ref, "sh", "-c", script, "lenny-test-gate")
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

// dockerImageID returns the ID of the image the local daemon holds under
// image, or "" when it holds none.
func dockerImageID(image string) string {
	out, err := exec.Command("docker", "image", "inspect", "--format", "{{.Id}}", image).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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
	withholdGatePass(v, dirty, o.environmentErr)
	return v
}

// withholdGatePass records why the verdict cannot stand for the commit
// and keeps it from PASS. On a dirty tree the tiers ran on files the
// commit does not hold, and outside the gate image they ran in an
// environment other than the canonical one. A FAIL or INCONCLUSIVE
// verdict stays as it is, because it already blocks the merge and
// outranks UNVERIFIED (verdictRank).
func withholdGatePass(v *verdict, dirty bool, envErr error) {
	if dirty {
		v.Gate.Unverified = append(v.Gate.Unverified, "the worktree has uncommitted changes, so the tiers did not run on the commit; commit or remove them and run the gate again")
	}
	if envErr != nil {
		v.Gate.Unverified = append(v.Gate.Unverified, envErr.Error())
	}
	if len(v.Gate.Unverified) > 0 {
		v.promoteVerdict(verdictstatus.VerdictUnverified)
	}
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

// runGateDocs runs the general documentation gates of the docs tier.
func runGateDocs(root string) (string, string, *tierResult) {
	names, err := gateDocsTestNames(filepath.Join(root, "tests", "docs"), gateDocsFiles)
	if err != nil {
		return verdictstatus.Fail, err.Error(), nil
	}
	pattern := "^(" + strings.Join(names, "|") + ")$"
	return runTierArgs("docs", []string{"-count=1", fmt.Sprintf("-timeout=%s", tierDocsTimeout), "-run", pattern, "./tests/docs/"})
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
	hist := filepath.Join(dir, verdictHistoryFile)
	if err := appendHistory(hist, v); err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test gate: append history: %v\n", err)
	} else {
		matchOwner(hist, root)
	}
	return filepath.ToSlash(filepath.Join(gateResultsDir, name)), nil
}

// matchOwner gives p the owner of root when the gate runs as root, so a
// verdict the container writes into a Linux host's worktree belongs to
// the worktree's owner. It is best-effort. It changes a symbolic link
// at p rather than the link's target, so a link planted in the results
// directory cannot hand another file to the worktree's owner.
func matchOwner(p, root string) {
	if os.Geteuid() != 0 {
		return
	}
	info, err := os.Stat(root)
	if err != nil {
		return
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		_ = os.Lchown(p, int(st.Uid), int(st.Gid))
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
		for _, why := range v.Gate.Unverified {
			fmt.Fprintf(w, "  unverified:  %s\n", why)
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
