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
	"sync"
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
	// the container, on the container's own file system and off /tmp
	// (testtmp.UnderTmp).
	gateGoTmpDir = "/var/tmp/lenny-go"
	// gateGoCache is where the container keeps the Go build cache. It
	// matches GOCACHE in tests/gate/Dockerfile and lies outside /root, so
	// the unprivileged gate user can use it.
	gateGoCache = "/var/cache/go-build"
	// gateLintCache is golangci-lint's cache inside the container, on the
	// build cache volume (GOLANGCI_LINT_CACHE).
	gateLintCache = gateGoCache + "/golangci-lint"
	// gateFixtureRoot is the fixture root inside the container (TMPDIR
	// and LENNY_TEST_TMPDIR). It lies on the container's own file system,
	// which behaves as a local Linux file system on every host: modes are
	// enforced, a rename over a file is atomic to a concurrent reader, and
	// a directory listing reflects an unlink at once. A Docker Desktop
	// file share does none of these reliably. The path is short so a Unix
	// socket under a test's t.TempDir() stays below the 108-byte limit.
	gateFixtureRoot = "/var/tmp/lenny-test"
	// gateTmpParent and gateTmpPrefix name the host directory the
	// container mounts at its own path as LENNY_TEST_SHARED_TMPDIR,
	// gateTmpParent/gateTmpPrefix<8 hex digits>, for the fixtures a test
	// bind-mounts into a testcontainer (testtmp.SharedDir). Docker Desktop
	// shares /tmp with its VM, and on Linux the daemon resolves the same
	// path, so such a bind mount resolves on the host daemon.
	gateTmpParent = "/tmp"
	gateTmpPrefix = "lenny-gate-"
	// gateEnvUID and gateEnvGID carry the worktree owner's IDs into the
	// container, which runs the gate as that user (gateContainerScript).
	gateEnvUID = "LENNY_GATE_UID"
	gateEnvGID = "LENNY_GATE_GID"
	// gateContainerSocket is where the container sees the Docker socket.
	gateContainerSocket = "/var/run/docker.sock"
	// gateNotRunPrefix opens the reason of a tier the gate selected but
	// could not run in its environment.
	gateNotRunPrefix = "not run: "
	// gateHarnessPath is where the container installs the lenny-test it
	// compiled, owned by root and read-only, before the cache fill and the
	// gate run it (gateContainerScript). gateEnvHarness names it to the
	// script's steps.
	gateHarnessPath = "/usr/local/libexec/lenny-gate/lenny-test"
	gateEnvHarness  = "LENNY_GATE_HARNESS"
	// gateEnvLintFill is set to gateLintFillFailed by the container
	// script when the golangci-lint cache fill did not finish, and the
	// static tier's golangci-lint runs then use a private cache
	// (privateLintCache).
	gateEnvLintFill    = "LENNY_GATE_LINT_CACHE_FILL"
	gateLintFillFailed = "failed"
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
	root       string
	base       string
	rdepsDepth int
	// all selects every package of the unit tier and every test of the
	// component, contract, and integration tiers, whatever the diff.
	all bool
	// layout names the arrangement of the tiers in phases and lanes
	// (gateLayouts); empty means defaultGateLayout.
	layout string
	// goTestP is the `go test -p` of each tier while it shares its phase
	// with another lane (parseGoTestP); a tier it does not name keeps the
	// go command's default.
	goTestP map[string]int
	// tiers restricts the run to these tiers of the merge gate
	// (parseGateTiers); nil runs every tier.
	tiers []string
	// cover writes the unit run's coverage profile to coverProfile
	// (prepareCoverProfile).
	cover        bool
	coverProfile string
	// commit is the commit the container script resolved HEAD to
	// (gateEnvCommit), or "" for a run that resolves HEAD itself.
	commit string
	// snapshot is the checkout of commit the container script created for
	// the static tier (gateEnvSnapshot), or "" for a run that creates its
	// own (prepareStaticSnapshot).
	snapshot    string
	environment string
	// environmentErr says why the environment is not the gate image, or
	// is nil when it is.
	environmentErr error
}

// gateRecord is the gate's own section of the verdict.
type gateRecord struct {
	// Base is the ref the branch is compared with, and MergeBase the
	// commit the static tier's changed-line lint and register raise check
	// compare with for it (resolveLintBase).
	Base      string `json:"base"`
	MergeBase string `json:"merge_base,omitempty"`
	// IntegrationMergeBase is the merge base of HEAD with the integration
	// branch, main or else origin/main (integrationMergeBase). A verdict
	// whose MergeBase is another commit is UNVERIFIED (GT-18).
	IntegrationMergeBase string `json:"integration_merge_base,omitempty"`
	// Dirty reports uncommitted changes in the worktree. A verdict for
	// a dirty tree is not a verdict for the branch head.
	Dirty bool `json:"dirty"`
	// Environment names the image the gate ran in, or the host.
	Environment string `json:"environment"`
	// All reports a run with --all, which selected every package of the
	// unit tier and every test of tiers 2 to 4 rather than the --changed
	// selection.
	All bool `json:"all,omitempty"`
	// RdepsDepth is the dependent depth the selection walked; -1 is the
	// full closure the merge gate runs.
	RdepsDepth int `json:"rdeps_depth"`
	// Layout names how the tiers ran (gateLayouts), and GoTestP the
	// `go test -p` each named tier got while it shared its phase with
	// another lane (change 0172, GS-3); gateRunOptions refuses a count
	// for a tier that runs alone in the layout.
	Layout  string         `json:"layout"`
	GoTestP map[string]int `json:"go_test_p,omitempty"`
	// WallTimeMS is the time from the start of the first tier to the end
	// of the last. Tiers in lanes overlap, so the tiers' own durations no
	// longer add up to it.
	WallTimeMS int64 `json:"wall_time_ms"`
	// RestrictedTo lists the tiers a run with --tier was restricted to;
	// it is absent for a run of every tier. Cover reports a run with
	// --cover, and CoverProfile the profile it wrote, relative to the
	// tree (change 0172, decision 9).
	RestrictedTo []string `json:"restricted_to,omitempty"`
	Cover        bool     `json:"cover,omitempty"`
	CoverProfile string   `json:"cover_profile,omitempty"`
	// StaticSnapshot is the checkout of the commit the static tier read
	// (change 0172, decision 14), or "" when the static tier did not run.
	StaticSnapshot string `json:"static_snapshot,omitempty"`
	// Selection records, for each test tier the plan selected, what the
	// gate ran of it (change 0172, GS-1), so a reader tells a selected run
	// from a full one (All) and sees how much a selected run covered. A
	// tier the plan did not select is absent.
	Selection map[string]gateTierSelection `json:"selection,omitempty"`
	// NotRun lists the tiers the gate selected but could not run, with
	// the reason. A gate with any entry here is not green.
	NotRun []string `json:"not_run,omitempty"`
	// Unverified lists why the verdict cannot stand for the commit even
	// when every tier passed: uncommitted changes in the tree, a run
	// outside the gate image, a narrowed dependent walk, or a --base whose
	// merge base is not the merge base with the integration branch. Any
	// entry keeps the verdict from PASS.
	Unverified []string `json:"unverified,omitempty"`
}

// gateTierSelection is what the gate ran of one tier: the whole tier, or
// the packages it ran whole and the test functions it ran by name in other
// packages. A count of zero is omitted.
type gateTierSelection struct {
	Whole     bool `json:"whole"`
	Packages  int  `json:"packages,omitempty"`
	Functions int  `json:"functions,omitempty"`
	// Excludes lists the package directories a whole tier left out, with
	// the packages below them (tierPlan.exclude).
	Excludes []string `json:"excludes,omitempty"`
}

// tierSelectionOf summarizes one tier's plan. A plan with no package scope,
// no named tests, and no subsets runs the whole tier. A scoped plan counts
// its package patterns and the test functions it runs by name.
func tierSelectionOf(p tierPlan) gateTierSelection {
	if p.pkgs == nil && len(p.tests) == 0 && len(p.subsets) == 0 {
		return gateTierSelection{Whole: true, Excludes: p.exclude}
	}
	s := gateTierSelection{Packages: len(p.pkgs), Functions: len(p.tests)}
	for _, names := range p.funcs {
		s.Functions += len(names)
	}
	return s
}

// String renders the selection for the gate's summary.
func (s gateTierSelection) String() string {
	if s.Whole && len(s.Excludes) > 0 {
		return "whole tier except " + strings.Join(s.Excludes, ", ")
	}
	if s.Whole {
		return "whole tier"
	}
	return fmt.Sprintf("%d package(s), %d function(s) by name", s.Packages, s.Functions)
}

// runGate handles `lenny-test gate`.
func runGate(args []string) int {
	fs := flag.NewFlagSet("gate", flag.ExitOnError)
	base := fs.String("base", "main", "the ref the branch merges into; the changed tiers are selected from the diff against its merge base, and a merge base other than the merge base of HEAD with main (or origin/main without main) makes the verdict UNVERIFIED")
	here := fs.Bool("here", false, "run the gate in this environment instead of the gate container (the container itself runs the gate this way); outside the gate image the verdict is at best UNVERIFIED")
	rebuild := fs.Bool("rebuild", false, "rebuild the gate image even when it exists")
	noSocket := fs.Bool("no-docker-socket", false, "do not give the gate container the host Docker socket; the tiers that need Docker are reported as not run")
	rdeps := fs.Int("rdeps-depth", defaultRdepsDepth, "how many hops of in-module dependents of a changed package the selection adds (0: the changed packages only; -1, the default: the full closure)")
	all := fs.Bool("all", false, "select every package of the unit tier and every test of the component, contract, and integration tiers, whatever the diff against --base")
	layout := fs.String("layout", defaultGateLayout, "how the tiers run: "+gateLayoutStaticFirst+" runs the static tier alone, then the contract tier beside the component and integration tiers, then the unit tier alone, then the documentation gates; "+gateLayoutUnitAlone+" runs the static and contract tiers beside the component and integration tiers, then the unit tier alone, then the documentation gates; "+gateLayoutSerial+" runs every tier one after another")
	tiers := fs.String("tier", "", "restrict the run to these tiers of the merge gate, as a comma-separated list such as static,unit,component,contract; the diff selection applies within them, and the verdict records the restriction")
	cover := fs.Bool("cover", false, "write the unit run's coverage profile to tests/results/cover.out, which `lenny-test coverage --diff` reads (with --here, LENNY_COVER_PROFILE can name another path); without it the gate's unit run writes no profile")
	goTestP := fs.String("go-test-p", "", "the go test -p of a tier that runs beside another lane, as tier=count pairs such as component=2,integration=2; a tier it does not name keeps the go command's default, and a tier that runs alone in the layout is refused")
	capGB := fs.Int("gocache-cap-gb", defaultGateGoCacheCapGB, "empty the build cache volume "+gateBuildVolume+" before the gate container starts when it holds more than this many GB (1 GB is 10^9 bytes) and no running container uses it; 0 turns the check off; without the flag, "+gateEnvGoCacheCap+" sets the cap when it is set; a --here run does not use the volume and skips the check")
	flagArgs, err := gateCommandArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test gate: %v\n", err)
		return 2
	}
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if err := gateRejectPositional(fs.Args()); err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test gate: %v\n", err)
		fs.Usage()
		return 2
	}
	if *base == "" {
		fmt.Fprintln(os.Stderr, "lenny-test gate: --base is empty; the merge gate compares with the merge base of a ref, main by default")
		return 2
	}
	opts, err := gateRunOptions(gateFlagValues{base: *base, rdeps: *rdeps, all: *all, layout: *layout, goTestP: *goTestP, tiers: *tiers, cover: *cover})
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test gate: %v\n", err)
		return 2
	}
	root := repoRoot()
	if opts.cover {
		if err := gateCoverProfileError(*here, root, os.LookupEnv); err != nil {
			fmt.Fprintf(os.Stderr, "lenny-test gate: %v\n", err)
			return 2
		}
		opts.coverProfile = coverProfilePath()
	}
	if *here {
		opts.root = root
		opts.commit, opts.snapshot = os.Getenv(gateEnvCommit), os.Getenv(gateEnvSnapshot)
		opts.environment, opts.environmentErr = gateEnvironment(root, gateImageMarker, os.Getenv)
		return finishGate(root, defaultGateRunner(root).run(opts))
	}
	passArgs := gatePassArgs(opts)
	capFlagSet := false
	fs.Visit(func(f *flag.Flag) { capFlagSet = capFlagSet || f.Name == "gocache-cap-gb" })
	cacheCap, err := resolveGateGoCacheCap(capFlagSet, *capGB, os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-test gate: %v\n", err)
		return 2
	}
	return runGateInContainer(root, passArgs, *rebuild, !*noSocket, cacheCap)
}

// gateFlagValues are the values of the gate's selection, layout, tier,
// and coverage flags as the command line gives them.
type gateFlagValues struct {
	base    string
	rdeps   int
	all     bool
	layout  string
	goTestP string
	tiers   string
	cover   bool
}

// gateRunOptions checks the gate's selection, layout, tier, and coverage
// flags and returns the options they set. An unknown layout, a malformed
// `--go-test-p`, and a `--tier` that names a tier outside the merge gate
// are usage errors, so the host half refuses them before it builds or
// starts the gate container.
func gateRunOptions(f gateFlagValues) (gateOptions, error) {
	phases, err := parseGateLayout(f.layout)
	if err != nil {
		return gateOptions{}, err
	}
	p, err := parseGoTestP(f.goTestP)
	if err != nil {
		return gateOptions{}, err
	}
	if err := checkGoTestP(f.layout, phases, p); err != nil {
		return gateOptions{}, err
	}
	tiers, err := parseGateTiers(f.tiers)
	if err != nil {
		return gateOptions{}, err
	}
	return gateOptions{base: f.base, rdepsDepth: f.rdeps, all: f.all, layout: f.layout, goTestP: p, tiers: tiers, cover: f.cover}, nil
}

// gatePassArgs returns the flags the host half passes to `lenny-test gate
// --here` in the gate container, so the run there uses the options the
// host half checked.
func gatePassArgs(o gateOptions) []string {
	args := []string{"--base", o.base, "--rdeps-depth", strconv.Itoa(o.rdepsDepth), "--layout", o.layout}
	if p := formatGoTestP(o.goTestP); p != "" {
		args = append(args, "--go-test-p", p)
	}
	if o.tiers != nil {
		args = append(args, "--tier", strings.Join(o.tiers, ","))
	}
	if o.cover {
		args = append(args, "--cover")
	}
	if o.all {
		args = append(args, "--all")
	}
	return args
}

// gateCommandArgs splits the arguments of `lenny-test gate [merge]
// [flags]`: an optional leading gate name, then the flags. The gate
// command runs the merge gate, which is `run --changed --gate merge` with
// the merge gate's verdict file (change 0116, CS-7). The area and release
// gates have no command yet, so their names are refused with the run
// command that bounds a selection by them.
func gateCommandArgs(args []string) ([]string, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return args, nil
	}
	if err := gateNameSupported(args[0]); err != nil {
		return nil, err
	}
	return args[1:], nil
}

// gateNameSupported refuses a gate name the gate command does not run.
func gateNameSupported(name string) error {
	switch name {
	case gateMerge:
		return nil
	case gateArea, gateRelease:
		return fmt.Errorf("the %s gate has no gate command yet: run its tiers with `lenny-test run --gate %s`", name, name)
	}
	return fmt.Errorf("unexpected argument %q: the gate command runs the merge gate (`lenny-test gate [merge] [flags]`)", name)
}

// gateRejectPositional refuses positional arguments after the flags,
// other than the merge gate's name, rather than ignoring them.
func gateRejectPositional(args []string) error {
	for _, a := range args {
		if err := gateNameSupported(a); err != nil {
			return err
		}
	}
	return nil
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

// runGateInContainer builds the gate image when needed, holds the build
// cache volume under capGB gigabytes (capGateGoCache), and runs the gate
// inside a container of the image, returning the container's exit code.
func runGateInContainer(root string, passArgs []string, rebuild, withSocket bool, capGB int) int {
	return runInGateContainer(root, gateContainerRun{
		label:    "lenny-test gate",
		passArgs: passArgs,
		hint:     "run `lenny-test gate --here` on a Linux host with the gate's toolchain",
		snapshot: gateSnapshotPath(root),
	}, rebuild, withSocket, capGB)
}

// gateContainerRun is what one gate-image container runs: the script and
// its $0 (gateContainerScript when script is empty), the arguments the
// script receives, and the label its host-side messages carry.
type gateContainerRun struct {
	label    string
	script   string
	name     string
	passArgs []string
	// hint is what the "docker not on PATH" message suggests instead.
	hint string
	// tmpPrefix names the shared fixture directory (hostTmpDir); empty
	// means the gate's, gateTmpPrefix.
	tmpPrefix string
	// volumes are extra `-v` mounts for the container.
	volumes []string
	// snapshot is the path of the static tier's checkout in the container
	// (gateSnapshotPath), or "" for a container that creates none.
	snapshot string
}

// runInGateContainer builds or reuses the gate image, caps the build cache
// volume, and runs c in a container with the gate's setup
// (gateDockerRunArgs), returning the container's exit code. The gate and
// `lenny-test run --container` share it.
func runInGateContainer(root string, c gateContainerRun, rebuild, withSocket bool, capGB int) int {
	if _, err := exec.LookPath("docker"); err != nil {
		fmt.Fprintln(os.Stderr, c.label+": docker not on PATH; install Docker, or "+c.hint)
		return 2
	}
	dockerfile, err := os.ReadFile(filepath.Join(root, gateDockerfile))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: read %s: %v\n", c.label, gateDockerfile, err)
		return 2
	}
	image := gateImageTag(dockerfile)
	store, err := defaultGateImageStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", c.label, err)
		return 2
	}
	id, err := ensureGateImage(root, image, rebuild, store)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", c.label, err)
		return 2
	}
	prefix := c.tmpPrefix
	if prefix == "" {
		prefix = gateTmpPrefix
	}
	tmp, err := hostTmpDir(prefix, root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", c.label, err)
		return 2
	}
	h := gateHost{root: root, image: image, imageID: id, tmpDir: tmp, passArgs: c.passArgs, script: c.script, scriptName: c.name, volumes: c.volumes, snapshot: c.snapshot}
	ctx, cancel := context.WithTimeout(context.Background(), gateGoCacheTimeout)
	capGateGoCache(ctx, execGateCommand, h.imageRef(), capGB, os.Stderr)
	cancel()
	h.uid, h.gid = pathOwner(root)
	if common, err := gitCommonDir(root); err == nil {
		h.gitCommonDir = common
	} else {
		fmt.Fprintf(os.Stderr, "%s: warning: locate the git directory: %v\n", c.label, err)
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
		fmt.Fprintf(os.Stderr, "%s: run the gate container: %v\n", c.label, err)
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

// gateHostTmpDir creates and returns the host directory the container
// mounts at its own path as LENNY_TEST_SHARED_TMPDIR for the worktree at
// root: /tmp/lenny-gate-<8 hex digits of the worktree path>, one per
// worktree. A test that bind-mounts a fixture into a testcontainer
// creates it there (testtmp.SharedDir), so the bind mount names a path
// the host daemon resolves; Docker Desktop shares /tmp with its VM. The
// path is short, so a Unix socket under it stays below the 108-byte
// limit. It lies outside the worktree, because the static checks that
// walk the tree (goimports, gofumpt, and the static-tier tree scans) run while
// other checks create and remove fixtures, and would read them
// half-written.
func gateHostTmpDir(root string) (string, error) {
	return hostTmpDir(gateTmpPrefix, root)
}

// hostTmpDir returns the shared fixture directory of a worktree for one
// kind of container: prefix and a digest of root under gateTmpParent. The
// gate and `run --container` use different prefixes, so each empties
// only its own directory when it starts.
func hostTmpDir(prefix, root string) (string, error) {
	sum := sha256.Sum256([]byte(root))
	tmp := filepath.Join(gateTmpParent, prefix+hex.EncodeToString(sum[:])[:8])
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return "", fmt.Errorf("create the shared fixture directory: %w", err)
	}
	return tmp, nil
}

// pathOwner returns the user and group IDs that own p, or 0 and 0 when
// they cannot be read.
func pathOwner(p string) (uid, gid int) {
	info, err := os.Stat(p)
	if err != nil {
		return 0, 0
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid), int(st.Gid)
	}
	return 0, 0
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
	tmpDir       string // host directory mounted at its own path as LENNY_TEST_SHARED_TMPDIR
	dockerSocket string // "" when the container gets no Docker socket
	// uid and gid own the worktree. The container runs the gate as that
	// user, and as root when uid is 0.
	uid, gid int
	passArgs []string
	// script and scriptName are the container's command and its $0;
	// an empty script runs gateContainerScript as lenny-test-gate.
	script, scriptName string
	// volumes are extra `-v` mounts, in docker's source:target form.
	volumes []string
	// snapshot is the path of the static tier's checkout in the container,
	// passed as gateEnvSnapshot, or "" for none.
	snapshot string
}

// gateDockerRunArgs is the argv of `docker run` for one gate run.
//
// The worktree is mounted at its host path, so absolute paths in the
// tree, in git's worktree metadata, and in tool output mean the same
// thing inside and outside the container. A linked worktree keeps its
// git metadata in the main checkout's git directory, which is mounted
// read-only at its host path for the same reason. TMPDIR and the fixture
// root are gateFixtureRoot, on the container's own file system. The
// shared fixture directory (gateHostTmpDir) is mounted at its host path
// as LENNY_TEST_SHARED_TMPDIR, so a fixture that a test bind-mounts into
// a testcontainer is a path the host daemon resolves. The module and
// build caches live in named volumes, so only the first run downloads
// and compiles from cold.
//
// The container shares the network namespace of the Docker host
// (--network host). A testcontainer publishes its ports there, on
// 127.0.0.1 or on every address, so a test reaches a fixture at
// 127.0.0.1 inside the gate as it does on a host, including the Redis
// cluster, whose nodes announce 127.0.0.1, and the Docker-backed k3s,
// whose kubeconfig names 127.0.0.1. Testcontainers detects that it runs
// in a container and would otherwise dial the container's default
// gateway, so TESTCONTAINERS_HOST_OVERRIDE points it at 127.0.0.1.
//
// The container gets CAP_SYS_ADMIN and unmasked /proc paths
// (systempaths=unconfined), and runs without an AppArmor profile, so the
// runtime supervisor tests can create user, PID, mount, and IPC
// namespaces with unshare and mount a fresh /proc in them. The container
// already holds the host Docker socket, which grants more than these.
//
// The container runs the image by the ID the gate recorded and runs
// gateContainerScript, which runs the gate as the worktree's owner.
func gateDockerRunArgs(h gateHost) []string {
	args := []string{
		"run", "--rm", "--init",
		"--network", "host",
		"--cap-add", "SYS_ADMIN",
		"--security-opt", "systempaths=unconfined",
		"--security-opt", "apparmor=unconfined",
		"-v", h.root + ":" + h.root,
		"-w", h.root,
	}
	if h.gitCommonDir != "" && !pathWithin(h.gitCommonDir, h.root) {
		args = append(args, "-v", h.gitCommonDir+":"+h.gitCommonDir+":ro")
	}
	args = append(
		args,
		"-v", gateModVolume+":/go/pkg/mod",
		"-v", gateBuildVolume+":"+gateGoCache,
		// golangci-lint keeps its analysis cache in a directory of the
		// build cache volume, and the static tier's check gives each
		// checkout a subdirectory of it named for a digest of the
		// checkout's path (golangciLintCache), so a gate run after the
		// first in the same worktree does not analyze the whole module
		// cold, and a new worktree's first run does. The Go build cache
		// uses only its two-hex-digit directories and leaves this one
		// alone. gateContainerScript fills the directory and then mounts
		// it read-only for the gate itself.
		"-e", "GOLANGCI_LINT_CACHE="+gateLintCache,
		"-e", gateEnvImage+"="+h.image,
		"-e", "GOTMPDIR="+gateGoTmpDir,
		"-e", "TMPDIR="+gateFixtureRoot,
		"-e", testtmp.EnvRoot+"="+gateFixtureRoot,
		// The worktree is a Docker Desktop file share on macOS, where a
		// rename over a file is not atomic to a concurrent reader. Every
		// `git status` rewrites .git/index by a rename when it can take
		// the lock, and every `go build` of a main package runs `git
		// status` for its VCS stamp, so parallel builds read a missing
		// index and fail with "error obtaining VCS status". The gate
		// turns off git's optional index refresh and the stamp, which no
		// test reads.
		"-e", "GIT_OPTIONAL_LOCKS=0",
		"-e", "GOFLAGS=-buildvcs=false",
		// git substitutes an object for any object a ref under
		// refs/replace/ names. When the gate runs in a main checkout,
		// its .git lies in the writable worktree, so a test could plant
		// a replace ref that changes what a later run reads at the merge
		// base, such as the golangci-lint register or the changed-line
		// diff. The gate tells git to ignore replace refs.
		"-e", "GIT_NO_REPLACE_OBJECTS=1",
		"-e", gateEnvUID+"="+strconv.Itoa(h.uid),
		"-e", gateEnvGID+"="+strconv.Itoa(h.gid),
	)
	if h.tmpDir != "" {
		args = append(
			args,
			"-v", h.tmpDir+":"+h.tmpDir,
			"-e", testtmp.EnvShared+"="+h.tmpDir,
		)
	}
	if h.imageID != "" {
		args = append(args, "-e", gateEnvImageID+"="+h.imageID)
	}
	if h.snapshot != "" {
		args = append(args, "-e", gateEnvSnapshot+"="+h.snapshot)
	}
	if h.dockerSocket != "" {
		args = append(
			args,
			"-v", h.dockerSocket+":"+gateContainerSocket,
			"-e", "TESTCONTAINERS_HOST_OVERRIDE=127.0.0.1",
		)
	}
	for _, vol := range h.volumes {
		args = append(args, "-v", vol)
	}
	script, name := gateContainerScript, "lenny-test-gate"
	if h.script != "" {
		script, name = h.script, h.scriptName
	}
	args = append(args, h.imageRef(), "sh", "-c", script, name)
	return append(args, h.passArgs...)
}

// imageRef is the reference the host half runs the gate image by: the
// recorded image ID, or the tag when no ID is known.
func (h gateHost) imageRef() string {
	if h.imageID != "" {
		return h.imageID
	}
	return h.image
}

// gateContainerScript is the container's command. It starts as root,
// empties the shared fixture directory (a killed earlier run leaves its
// fixtures behind), and creates the fixture root and the directories the
// Go toolchain writes. When
// LENNY_GATE_UID names a user other than root, it creates that user, with
// the group that owns the Docker socket as a supplementary group, gives
// it the fixture root and the cache volumes, and drops to it with
// setpriv. Running as the
// worktree's owner means the files the gate writes into the worktree,
// the verdict among them, belong to that owner, and the tests run
// unprivileged as they do on a developer host: initdb refuses to run as
// root, and the runtime supervisor tests derive the adapter UID from the
// test's own.
//
// The user first resolves HEAD to a commit, once (the resolve step), and
// the script exports it as gateEnvCommit: the build, the static tier's
// snapshot, and the gate all read that commit (change 0172, decision 14).
//
// The user then compiles lenny-test (the build step). It exports the
// commit with git archive into a new directory under GOTMPDIR, on the
// container's own file system, and checks that the export holds every
// file of the commit's tree, so an export-ignore attribute cannot drop a
// file from the harness. In the export it runs go mod verify and go build
// with GOWORK=off and -mod=readonly, so a go.work file and a vendor
// directory play no part, and with a build cache of its own: a new, empty
// directory under GOTMPDIR. The script removes the export and that build
// cache after the compile. The binary that runs the cache fill and the
// gate and writes the verdict is therefore compiled from the committed
// tree, the module cache, and the image's toolchain, and links no entry of
// the build cache volume, which the test tiers of every earlier gate run
// could write (owner's answer to GT-15, 2026-11-20). An uncommitted or
// untracked file, which a test of an earlier run in the same worktree
// could plant, is not compiled in. A cold compile of lenny-test took about
// nine seconds in the gate image when the owner measured it on 2026-11-20.
// Root then installs the binary at gateHarnessPath, owned by root and
// read-only, so no process of the gate user, a test among them, can
// replace the file that a later check of the gate runs again
// (os.Executable). Every other go command of the run, the fill's
// golangci-lint and the tiers' builds among them, still uses the volume,
// and the module cache volume stays writable to the gate user.
//
// Root then creates the directory gateEnvSnapshot names, under the
// root-owned gateSnapshotParent, and gives it to the user, who makes it a
// checkout of the commit with the installed binary (lenny-test
// static-snapshot; createStaticSnapshot). Root then gives the checkout to
// root, removes every write bit, refreshes its index, whose recorded file
// times the ownership and mode changes made stale, so each git read there
// does not rehash the whole tree, and bind-mounts it read-only over
// itself. The gate image's git configuration already lists every directory
// as safe, so the gate user's git reads the root-owned checkout. The
// static tier runs its checks there, so a test in a lane beside it, which
// runs as the gate user, cannot change the files or the refs it reads.
// Writers remain: a gate run as root, whose tests hold CAP_SYS_ADMIN and
// can remount the checkout; any process with the host Docker socket; and,
// in a main checkout, the gate user through the worktree's object store,
// which the checkout borrows through git's alternates file and whose
// objects git does not verify when it reads them (createStaticSnapshot).
//
// The user then fills golangci-lint's cache with the installed binary
// (lenny-test lint-cache-fill), in the checkout, because golangci-lint
// names its cache directory for the checkout's path (golangciLintCache)
// and the static tier's lint runs there. A fill that does not finish, such as one
// the kernel kills for memory, logs one line and sets gateEnvLintFill, and
// the static tier's golangci-lint runs then use a private, empty cache
// created for that run (privateLintCache) and report it in the tier's
// detail. Against the read-only shared cache, a run whose fill did not
// finish logs a warning for each result it cannot store and can exit
// non-zero on a clean tree. Root then bind-mounts golangci-lint's cache
// directory read-only over itself, and the user runs `lenny-test gate
// --here` with the installed binary, so the gate always runs the harness
// of the commit under test, and its static tier reads the filled cache
// while no test tier can write it (owner's answer to GT-11, 2026-11-20).
// The user holds no capability after setpriv, and a mount that a user
// namespace inherits is locked, so neither the user nor a namespace a
// test creates can remount the directory writable. Writers remain outside
// the mount: a gate run as root, whose tests hold CAP_SYS_ADMIN and can
// remount it; any process with the host Docker socket, which can mount
// the volume in another container; the fill itself, which runs lenny-test as the branch builds
// it, with the packages it imports; and the fill of a gate container
// another worktree runs at the same time. The owner accepted these limits
// (owner's answer to GT-16, 2026-11-20). The Go build cache on the same
// volume stays writable to the test tiers. A mount that fails stops the
// gate.
const gateContainerScript = `set -e
if [ -n "${LENNY_TEST_SHARED_TMPDIR:-}" ]; then find "$LENNY_TEST_SHARED_TMPDIR" -mindepth 1 -delete; fi
mkdir -p "$LENNY_TEST_TMPDIR" "$GOTMPDIR" "$GOCACHE" "${GOLANGCI_LINT_CACHE:?}" /go/pkg/mod
export ` + gateEnvHarness + `=` + gateHarnessPath + `
resolve='git rev-parse --verify --quiet "HEAD^{commit}" || { echo "lenny-test gate: HEAD does not name a commit" >&2; exit 1; }'
build='set -e
src="$(mktemp -d "$GOTMPDIR/lenny-test-src.XXXXXX")"
git archive -o "$src.tar" "${` + gateEnvCommit + `:?}"
tar -x -f "$src.tar" -C "$src"
rm -f "$src.tar"
want="$(git ls-tree -r -z --format="%(objecttype)" "$` + gateEnvCommit + `" | tr "\000" "\n" | grep -c "^blob$")"
got="$(find "$src" ! -type d -print0 | tr -cd "\000" | wc -c | tr -d " ")"
if [ "$want" != "$got" ]; then echo "lenny-test gate: the export of $` + gateEnvCommit + ` holds $got files and its tree holds $want; an export-ignore attribute kept files out of the harness build" >&2; exit 1; fi
cd "$src"
export GOWORK=off GOFLAGS="-buildvcs=false -mod=readonly"
go mod verify
build_cache="$(mktemp -d "$GOTMPDIR/lenny-test-build.XXXXXX")"
GOCACHE="$build_cache" go build -o "$GOTMPDIR/lenny-test" ./cmd/lenny-test
cd /
rm -rf "$build_cache" "$src"'
snapshot='"$` + gateEnvHarness + `" static-snapshot "$` + gateEnvCommit + `" "$` + gateEnvSnapshot + `"'
fill='cd "$` + gateEnvSnapshot + `" && "$` + gateEnvHarness + `" lint-cache-fill'
run='exec "$` + gateEnvHarness + `" gate --here "$@"'
install_harness() {
	mkdir -p "$(dirname "$` + gateEnvHarness + `")"
	install -o 0 -g 0 -m 0555 "$GOTMPDIR/lenny-test" "$` + gateEnvHarness + `"
	rm -f "$GOTMPDIR/lenny-test"
}
prepare_snapshot() {
	mkdir -p "$(dirname "${` + gateEnvSnapshot + `:?}")"
	rm -rf "$` + gateEnvSnapshot + `"
	mkdir "$` + gateEnvSnapshot + `"
	chown "$1" "$` + gateEnvSnapshot + `"
}
lock_snapshot() {
	chown -R 0:0 "$` + gateEnvSnapshot + `"
	chmod -R a-w "$` + gateEnvSnapshot + `"
	git -C "$` + gateEnvSnapshot + `" update-index -q --refresh || echo "lenny-test gate: warning: the static tier's snapshot index was not refreshed, so its git reads rehash the tree" >&2
	mount --bind "$` + gateEnvSnapshot + `" "$` + gateEnvSnapshot + `"
	mount -o remount,bind,ro "$` + gateEnvSnapshot + `"
}
fill_failed() {
	echo "lenny-test gate: warning: the golangci-lint cache fill did not finish; the static tier's golangci-lint runs use a private, empty cache for this run, and the shared cache stays read-only" >&2
	export ` + gateEnvLintFill + `=` + gateLintFillFailed + `
}
lock_lint_cache() {
	mount --bind "$GOLANGCI_LINT_CACHE" "$GOLANGCI_LINT_CACHE"
	mount -o remount,bind,ro "$GOLANGCI_LINT_CACHE"
}
uid="${LENNY_GATE_UID:-0}"
gid="${LENNY_GATE_GID:-0}"
if [ "$uid" = 0 ]; then
	` + gateEnvCommit + `="$(sh -c "$resolve")"
	export ` + gateEnvCommit + `
	sh -c "$build"
	install_harness
	prepare_snapshot 0:0
	sh -c "$snapshot"
	lock_snapshot
	sh -c "$fill" || fill_failed
	lock_lint_cache
	exec sh -c "$run" lenny-test-gate "$@"
fi
` + gateUserSetup + gateEnvCommit + `="$(setpriv --reuid="$uid" --regid="$gid" --groups="$groups" sh -c "$resolve")"
export ` + gateEnvCommit + `
setpriv --reuid="$uid" --regid="$gid" --groups="$groups" sh -c "$build"
install_harness
prepare_snapshot "$uid:$gid"
setpriv --reuid="$uid" --regid="$gid" --groups="$groups" sh -c "$snapshot"
lock_snapshot
setpriv --reuid="$uid" --regid="$gid" --groups="$groups" sh -c "$fill" || fill_failed
lock_lint_cache
exec setpriv --reuid="$uid" --regid="$gid" --groups="$groups" sh -c "$run" lenny-test-gate "$@"`

// gateUserSetup is the part of a gate-image container's script that
// creates the worktree owner's user, with the group that owns the Docker
// socket as a supplementary group, gives it the fixture root and the
// cache directories, and exports its HOME and USER. It reads uid and gid,
// which the script sets from LENNY_GATE_UID and LENNY_GATE_GID, and leaves
// groups set for setpriv. gateContainerScript and runContainerScript run
// it before they drop to the user.
const gateUserSetup = `getent group "$gid" >/dev/null || groupadd --gid "$gid" gate
getent passwd "$uid" >/dev/null || useradd --uid "$uid" --gid "$gid" --no-create-home --home-dir /home/gate --shell /bin/sh gate
mkdir -p /home/gate
chown "$uid:$gid" /home/gate
groups="$gid"
if [ -S /var/run/docker.sock ]; then groups="$groups,$(stat -c %g /var/run/docker.sock)"; fi
for d in "$LENNY_TEST_TMPDIR" /go/pkg/mod "$GOCACHE" "$GOLANGCI_LINT_CACHE" "$GOTMPDIR"; do
	if [ "$(stat -c %u "$d")" != "$uid" ]; then chown -R "$uid:$gid" "$d"; fi
done
export HOME=/home/gate USER="$(getent passwd "$uid" | cut -d: -f1)"
`

// gateSummaryNotes returns the lines of a tier's detail that the summary
// prints whatever the tier's status: each golangci-lint register raise
// (GT-8) and the golangci-lint runs' private cache (privateLintCache),
// which a passing static tier records only in its detail.
func gateSummaryNotes(detail string) []string {
	var out []string
	for _, line := range strings.Split(detail, "\n") {
		if strings.Contains(line, lintRaiseNote) || strings.Contains(line, lintCacheFallbackNote) {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
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
	// static runs the static tier in the checkout at dir, with the gate's
	// --base, which the changed-line lint compares with, and the commit
	// the secret scan reads.
	static func(dir, base, rev string) (string, string, *tierResult)
	// snapshot returns the checkout of commit the static tier reads: the
	// one given, which the container script created, or one the run
	// creates (prepareStaticSnapshot).
	snapshot func(commit, given string) (staticSnapshot, error)
	plan     func(base string, rdepsDepth int, all bool) (resolvedSelector, error)
	exec     map[string]tierExecFn
	// precheck returns why a tier cannot run in this environment, or
	// nil when it can.
	precheck map[string]func() error
	docs     func() (string, string, *tierResult)
	// state reads the commit and whether the tree is dirty.
	state func() (head string, dirty bool)
	// bases resolves the merge base the static tier compares with for
	// the gate's --base and the merge base with the integration branch.
	bases func(base string) gateBases
	// mu guards the verdict while lanes record tiers at the same time.
	// run sets it on its own copy of the runner; a runner used outside
	// run records from one goroutine and leaves it nil.
	mu *sync.Mutex
}

// gateBases are the two merge bases the gate compares (GT-18).
type gateBases struct {
	// base is the --base the merge bases were resolved for.
	base string
	// used is the commit the static tier's changed-line lint and its
	// golangci-lint register raise check compare with for the gate's
	// --base (resolveLintBase), or "" with usedErr set when it cannot be
	// resolved.
	used    string
	usedErr error
	// integration is the merge base of HEAD with the integration branch,
	// and integrationRef the ref it was taken against: main, or
	// origin/main in a clone without main (integrationMergeBase). It is ""
	// with integrationErr set when neither resolves.
	integration    string
	integrationRef string
	integrationErr error
}

// gateIntegrationRefs are the refs the gate takes the integration
// branch's merge base against, in order, each by its full name so that a
// tag or a file under the git directory named main cannot stand in for the
// branch, with the name the gate reports. They are the refs the static
// tier's other changed-line lints read (annotation.BaseRefs).
var gateIntegrationRefs = []struct{ full, name string }{
	{"refs/heads/main", "main"},
	{"refs/remotes/origin/main", "origin/main"},
}

// resolveGateBases resolves both merge bases for the checkout at root.
func resolveGateBases(ctx context.Context, root, base string) gateBases {
	b := gateBases{base: base}
	if lb, err := resolveLintBase(ctx, root, base); err != nil {
		b.usedErr = err
	} else {
		b.used = lb.commit
	}
	b.integration, b.integrationRef, b.integrationErr = integrationMergeBase(ctx, root)
	return b
}

// integrationMergeBase returns the merge base of HEAD with the first ref
// of gateIntegrationRefs the repository carries, and that ref's name. A
// repository that carries neither ref, and a git command that fails, are
// errors.
func integrationMergeBase(ctx context.Context, root string) (string, string, error) {
	for _, r := range gateIntegrationRefs {
		commit, err := gitOutput(ctx, root, "for-each-ref", "--format=%(objectname)", r.full)
		if err != nil {
			return "", "", fmt.Errorf("read %s: %w", r.full, err)
		}
		if commit == "" {
			continue
		}
		mb, err := gitOutput(ctx, root, "merge-base", "HEAD", r.full)
		if err != nil || mb == "" {
			return "", "", fmt.Errorf("find the merge base of HEAD with %s: %v", r.name, err)
		}
		return mb, r.name, nil
	}
	return "", "", errors.New("the repository carries neither refs/heads/main nor refs/remotes/origin/main")
}

// gateBaseError returns why the merge base the gate's --base gives cannot
// stand for the merge with the integration branch, or nil when it can.
// The changed-line lint reports findings only on the lines changed since
// that merge base, and the register raise check lists only the raises
// since it, so a --base that is a range, or a ref past the commit that
// raised the register, would let a PASS hide a raise or a finding (owner's
// answer to GT-18, 2026-11-20).
func gateBaseError(base string, b gateBases) error {
	suggest := "main"
	if b.integrationRef != "" {
		suggest = b.integrationRef
	}
	switch {
	case base == "":
		return errors.New("--base is empty, so the changed tiers were selected from the uncommitted changes alone; run the gate with --base " + suggest)
	case b.usedErr != nil:
		return fmt.Errorf("the merge base of --base %s could not be resolved (%v), so the changed-line lint and the golangci-lint register raise check did not compare with the merge base with the integration branch; run the gate with --base %s", base, b.usedErr, suggest)
	case b.integrationErr != nil:
		return fmt.Errorf("the merge base of HEAD with the integration branch could not be resolved (%v), so the gate cannot confirm that --base %s compares with it; fetch main or origin/main", b.integrationErr, base)
	case b.used != b.integration:
		return fmt.Errorf("--base %s compares with %s, and the merge base of HEAD with %s is %s; the changed-line lint and the golangci-lint register raise check stand for the merge only when they compare with the merge base with the integration branch, so run the gate with --base %s", base, shortCommit(b.used), b.integrationRef, shortCommit(b.integration), b.integrationRef)
	}
	return nil
}

// defaultGateRunner wires the gate to the harness's tier executors.
func defaultGateRunner(root string) gateRunner {
	steps := tierSteps()
	execs := map[string]tierExecFn{}
	for _, name := range gateChangedTiers {
		execs[name] = steps[name].exec
	}
	return gateRunner{
		static: runStaticTierAt,
		snapshot: func(commit, given string) (staticSnapshot, error) {
			return prepareStaticSnapshot(context.Background(), gitOutput, root, commit, given)
		},
		plan: gateChangedPlan,
		exec: execs,
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
		state: func() (string, bool) { return gitState(root) },
		bases: func(base string) gateBases { return resolveGateBases(context.Background(), root, base) },
	}
}

// gateChangedPlan resolves the --changed plan against base within the
// merge gate's tiers (`run --changed --gate merge`), keeping the tiers the
// gate takes from the plan (gateChangedTiers): the gate runs the static
// tier and its general documentation gates itself. With all, it resolves
// those tiers in full instead: the whole unit tier (./...) except the
// directories of gateFullRunUnitExcludes, and every test of the component,
// contract, and integration tiers. The unit tier runs
// without a coverage profile (gateUnitWithoutCoverage).
func gateChangedPlan(base string, rdepsDepth int, all bool) (resolvedSelector, error) {
	sel := selector{changed: true, since: base, gate: gateMerge, rdepsDepth: rdepsDepth}
	if all {
		sel = selector{gate: gateMerge}
	}
	r, err := sel.resolve()
	if err != nil {
		return r, err
	}
	r.tiers, _ = keepTiers(r.tiers, append([]string{tierStatic}, gateChangedTiers...))
	gateUnitWithoutCoverage(r.tiers)
	if all {
		gateFullRunExcludes(r.tiers)
	}
	return r, nil
}

// gateFullRunUnitExcludes are the package directories a full run's unit
// tier leaves out (change 0172, decision 16). tests/docs holds the docs
// tier's tests, which build without a tag and so fall under the unit
// tier's `./...`. A selected run places the package in the docs tier, of
// which the merge gate runs only the general gates (gateDocsFiles); the
// rest of the package runs at the area gate. Issue GATE-I9 records the
// gap-record reconciliation tests there that failed in the full run.
var gateFullRunUnitExcludes = []string{"tests/docs"}

// gateFullRunExcludes sets the unit plan of a full run to leave out
// gateFullRunUnitExcludes.
func gateFullRunExcludes(plans []tierPlan) {
	for i := range plans {
		if plans[i].name == tierUnit && plans[i].pkgs == nil {
			plans[i].exclude = append([]string{}, gateFullRunUnitExcludes...)
		}
	}
}

// gateUnitWithoutCoverage sets noCoverage on the unit tier's plan, so the
// gate's unit run keeps -race and -count=1 and writes no coverage profile
// (change 0172, GS-4). No check of the gate reads the profile, so the
// coverage build only lengthens the run. A gate run leaves an existing
// tests/results/cover.out as it is. A coverage report comes from
// `lenny-test --tier unit` or `run --changed`, followed by `lenny-test
// coverage --go` or `--diff`.
func gateUnitWithoutCoverage(plans []tierPlan) {
	for i := range plans {
		if plans[i].name == tierUnit {
			plans[i].noCoverage = true
		}
	}
}

// run executes the gate and returns its verdict. The plan is resolved
// first, then the tiers run in the phases and lanes of the layout
// (gateLayouts). Every tier runs whatever another tier reported, so the
// verdict carries every failure.
func (g gateRunner) run(o gateOptions) *verdict {
	g.mu = &sync.Mutex{}
	sel := selector{changed: true, since: o.base, rdepsDepth: o.rdepsDepth}
	v := newVerdict(sel)
	layout := o.layout
	if layout == "" {
		layout = defaultGateLayout
	}
	v.Command = gateCommandLine(o, layout)
	v.Trigger.Mode = "gate"
	head, dirty := g.state()
	// The gate resolves HEAD once: the container script resolves it before
	// it builds lenny-test and passes the commit, and a run without the
	// container takes the HEAD it read here. The static tier reads that
	// commit, and the end-of-run check compares HEAD with it.
	commit := o.commit
	if commit == "" {
		commit = head
	}
	if commit != "" {
		v.Trigger.GitRevision = commit
	}
	bases := g.bases(o.base)
	v.Gate = &gateRecord{Base: o.base, MergeBase: bases.used, IntegrationMergeBase: bases.integration, Dirty: dirty, Environment: o.environment, All: o.all, Layout: layout}
	if len(o.goTestP) > 0 {
		v.Gate.GoTestP = o.goTestP
	}
	v.Gate.RestrictedTo = o.tiers
	v.Gate.Cover = o.cover
	// The static tier and the plan compare with the commit resolved here
	// rather than with the ref, so the commit the verdict records and
	// checks is the one they read even when the ref moves during the run.
	compare := o.base
	if bases.used != "" {
		compare = bases.used
	}

	phases, layoutErr := parseGateLayout(layout)
	if layoutErr != nil {
		// runGate refuses an unknown layout before it gets here; a runner
		// called with one anyway runs the tiers serially and fails.
		phases = gateLayouts[gateLayoutSerial]
		v.recordTier(tierStatic, verdictstatus.Fail, 0, layoutErr.Error())
	}
	plans := g.resolvePlans(v, o, compare)
	if o.cover {
		if err := prepareCoverProfile(o.coverProfile, plans); err != nil {
			// The unit tier does not run, so its result cannot overwrite
			// the failure, and coverage --diff finds no fresh profile.
			delete(plans, tierUnit)
			v.recordTier(tierUnit, verdictstatus.Fail, 0, err.Error())
		}
	}

	var snap staticSnapshot
	var snapErr error
	if layoutErr == nil && gateRuns(o.tiers, tierStatic) {
		snap, snapErr = g.prepareSnapshot(v, commit, o.snapshot)
		if snap.cleanup != nil {
			defer snap.cleanup()
		}
	}

	start := time.Now()
	g.runPhases(v, phases, plans, o.goTestP, func(name string) {
		if !gateRuns(o.tiers, name) {
			return
		}
		switch name {
		case tierStatic:
			switch {
			case layoutErr != nil:
			case snapErr != nil:
				g.record(v, tierStatic, func() (string, string, *tierResult) {
					return verdictstatus.Fail, "the static tier did not run, because its snapshot of the commit is not ready: " + snapErr.Error(), nil
				})
			default:
				g.record(v, tierStatic, func() (string, string, *tierResult) { return g.static(snap.dir, compare, commit) })
			}
		case tierDocs:
			g.record(v, tierDocs, g.docs)
		}
	})
	v.Gate.WallTimeMS = time.Since(start).Milliseconds()
	if o.cover {
		if err := finishCoverProfile(o.coverProfile); err != nil {
			v.recordTier(tierUnit, verdictstatus.Fail, 0, err.Error())
		}
		v.Gate.CoverProfile = coverProfileRecord(o.root, o.coverProfile)
	}

	v.Gate.RdepsDepth = o.rdepsDepth
	if o.rdepsDepth >= 0 && !o.all {
		v.Gate.Unverified = append(v.Gate.Unverified, fmt.Sprintf("--rdeps-depth %d narrowed the selection to that many hops of dependents, and the merge gate runs the changed packages and every dependent; run the gate without --rdeps-depth", o.rdepsDepth))
	}
	if o.commit != "" && head != o.commit {
		v.Gate.Unverified = append(v.Gate.Unverified, fmt.Sprintf("HEAD was %s when the gate started, and the container resolved it to %s before it built lenny-test; run the gate again", shortCommit(head), shortCommit(o.commit)))
	}
	if why := gateStateChange(commit, dirty, bases, g); why != "" {
		v.Gate.Unverified = append(v.Gate.Unverified, why)
	}
	withholdGatePass(v, dirty, o.environmentErr, gateBaseError(o.base, bases))
	return v
}

// prepareSnapshot readies the checkout of commit the static tier reads and
// records it in the verdict. A checkout the gate's user can write keeps
// the verdict from PASS, because a test in another lane could then change
// what the static tier reads.
func (g gateRunner) prepareSnapshot(v *verdict, commit, given string) (staticSnapshot, error) {
	if commit == "" {
		return staticSnapshot{}, errors.New("git could not resolve HEAD")
	}
	snap, err := g.snapshot(commit, given)
	if err != nil {
		return snap, err
	}
	v.Gate.StaticSnapshot = snap.dir
	if snapshotWritable(snap.dir) {
		v.Gate.Unverified = append(v.Gate.Unverified, fmt.Sprintf("the static tier's snapshot %s is writable by the gate's user, so a test in another lane could change what the static tier read; the gate container's snapshot is read-only", snap.dir))
	}
	return snap, nil
}

// gateCommandLine renders the verdict's command for a run.
func gateCommandLine(o gateOptions, layout string) string {
	line := fmt.Sprintf("lenny-test gate --base %s --rdeps-depth %d --layout %s", o.base, o.rdepsDepth, layout)
	if p := formatGoTestP(o.goTestP); p != "" {
		line += " --go-test-p " + p
	}
	if o.tiers != nil {
		line += " --tier " + strings.Join(o.tiers, ",")
	}
	if o.cover {
		line += " --cover"
	}
	if o.all {
		line += " --all"
	}
	return line
}

// resolvePlans resolves the plan of the tiers the gate takes from the
// selection, records each one's selection in the verdict, and returns the
// plans by tier name. A plan that cannot be resolved fails the unit tier
// and leaves the map empty, so only the static tier and the documentation
// gates run.
func (g gateRunner) resolvePlans(v *verdict, o gateOptions, compare string) map[string]tierPlan {
	plans := map[string]tierPlan{}
	plan, err := g.plan(compare, o.rdepsDepth, o.all)
	if err != nil {
		v.recordTier(tierUnit, verdictstatus.Fail, 0, fmt.Sprintf("resolve the changed tiers against %s: %v", o.base, err))
		return plans
	}
	for _, n := range plan.notes {
		fmt.Fprintf(os.Stderr, "lenny-test gate: note: %s\n", n)
	}
	for _, p := range plan.tiers {
		if p.name == tierStatic || !gateRuns(o.tiers, p.name) {
			continue
		}
		if v.Gate.Selection == nil {
			v.Gate.Selection = map[string]gateTierSelection{}
		}
		v.Gate.Selection[p.name] = tierSelectionOf(p)
		plans[p.name] = p
	}
	return plans
}

// gateStateChange reads HEAD, the tree's state, and both merge bases again
// after the last tier, and returns why the verdict cannot stand when any
// of them differs from what the gate read before the static tier, or ""
// when none does. The tiers run the branch's code, which can move a ref,
// commit, or edit the tree and restore it, so a value read only at the
// start would not show it.
func gateStateChange(head string, dirty bool, bases gateBases, g gateRunner) string {
	head2, dirty2 := g.state()
	bases2 := g.bases(bases.base)
	var changed []string
	if head2 != head {
		changed = append(changed, fmt.Sprintf("HEAD from %s to %s", shortCommit(head), shortCommit(head2)))
	}
	if dirty2 != dirty {
		changed = append(changed, fmt.Sprintf("uncommitted changes from %t to %t", dirty, dirty2))
	}
	if bases2.used != bases.used {
		changed = append(changed, fmt.Sprintf("the merge base of --base from %s to %s", shortCommit(bases.used), shortCommit(bases2.used)))
	}
	if bases2.integration != bases.integration {
		changed = append(changed, fmt.Sprintf("the merge base with the integration branch from %s to %s", shortCommit(bases.integration), shortCommit(bases2.integration)))
	}
	if len(changed) == 0 {
		return ""
	}
	return "the state the gate read before the tiers changed while they ran (" + strings.Join(changed, "; ") + "); run the gate again"
}

// withholdGatePass records why the verdict cannot stand for the commit
// and keeps it from PASS. On a dirty tree the tiers ran on files the
// commit does not hold, outside the gate image they ran in an environment
// other than the canonical one, and with baseErr set the static tier
// compared with another commit than the merge base with the integration
// branch (gateBaseError). A FAIL or INCONCLUSIVE verdict stays as it is,
// because it already blocks the merge and outranks UNVERIFIED
// (verdictRank).
func withholdGatePass(v *verdict, dirty bool, envErr, baseErr error) {
	if dirty {
		v.Gate.Unverified = append(v.Gate.Unverified, "the worktree has uncommitted changes, so the tiers did not run on the commit; commit or remove them and run the gate again")
	}
	if envErr != nil {
		v.Gate.Unverified = append(v.Gate.Unverified, envErr.Error())
	}
	if baseErr != nil {
		v.Gate.Unverified = append(v.Gate.Unverified, baseErr.Error())
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
	dur := time.Since(start)
	logTierEnd(name, st, dur)
	if st == verdictstatus.Skipped {
		g.notRun(v, name, errors.New(msg))
		return
	}
	defer g.lock()()
	v.recordTierWithResult(name, st, dur, msg, result)
}

// lock takes the runner's verdict lock when it has one and returns the
// function that releases it.
func (g gateRunner) lock() func() {
	if g.mu == nil {
		return func() {}
	}
	g.mu.Lock()
	return g.mu.Unlock
}

// notRun records a selected tier the gate could not run. The tier is
// unverified, which keeps the verdict from passing.
func (g gateRunner) notRun(v *verdict, name string, why error) {
	reason := gateNotRunPrefix + why.Error()
	defer g.lock()()
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

// gitState returns HEAD and whether the worktree has uncommitted changes.
// A HEAD git cannot produce is left empty.
func gitState(root string) (head string, dirty bool) {
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
	dirty = git("status", "--porcelain", "--untracked-files=normal") != ""
	return head, dirty
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
		fmt.Fprintf(w, "  base:        %s (merge base %s; with the integration branch %s)\n", v.Gate.Base, v.Gate.MergeBase, v.Gate.IntegrationMergeBase)
		fmt.Fprintf(w, "  environment: %s\n", v.Gate.Environment)
		kind := "selected run (the --changed selection against the base)"
		if v.Gate.All {
			kind = "full run (--all)"
		}
		if len(v.Gate.Selection) == 0 {
			kind += "; no test tier selected"
		}
		fmt.Fprintf(w, "  selection:   %s\n", kind)
		if v.Gate.RestrictedTo != nil {
			fmt.Fprintf(w, "  tiers:       restricted to %s; this verdict does not stand for every tier of the merge gate\n", strings.Join(v.Gate.RestrictedTo, ", "))
		}
		if v.Gate.Cover {
			profile := v.Gate.CoverProfile
			if profile == "" {
				profile = "none written"
			}
			fmt.Fprintf(w, "  coverage:    %s\n", profile)
		}
		if v.Gate.Layout != "" {
			layout := v.Gate.Layout
			if p := formatGoTestP(v.Gate.GoTestP); p != "" {
				layout += ", go test -p " + p
			}
			fmt.Fprintf(w, "  layout:      %s; the tiers took %s\n", layout, (time.Duration(v.Gate.WallTimeMS) * time.Millisecond).Round(time.Second))
		}
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
		if v.Gate != nil {
			if sel, ok := v.Gate.Selection[name]; ok {
				line += " [" + sel.String() + "]"
			}
		}
		if strings.HasPrefix(t.Reason, gateNotRunPrefix) {
			line += ": " + t.Reason
		}
		fmt.Fprintln(w, line)
		for _, f := range t.Failures {
			fmt.Fprintf(w, "    FAIL %s\n", f.Test)
		}
		for _, note := range gateSummaryNotes(t.Detail) {
			fmt.Fprintf(w, "    NOTE %s\n", note)
		}
	}
	fmt.Fprintf(w, "  verdict:     %s\n", path)
}
