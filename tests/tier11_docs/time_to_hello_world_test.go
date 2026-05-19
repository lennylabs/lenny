// SPDX-License-Identifier: MIT

// Tier-11 §22.7 time-to-hello-world documentation gate. The obligation
// is that a fresh operator can go from install to a working session in
// under five minutes of wall-clock, and that a runtime author can go
// from scaffold to a published runtime in the same budget. These tests
// exercise the steps whose backing command ships today (lenny up, lenny
// runtime init / validate, lenny-ctl install --non-interactive) and
// assert each completes well inside the budget.
//
// Steps whose backing deliverable is unbuilt carry a precise blocked:
// skip naming the missing piece:
//   - the brew-install step needs a published Homebrew tap;
//   - the session steps need the `lenny session` CLI subcommand and a
//     reachable gateway;
//   - the chat-runtime session step needs the §26 chat reference
//     runtime.
//
// These tests are NOT under a build tag: they build in-repo binaries
// and run them offline. Steps that would need Docker or a Kubernetes
// cluster gate on that tool being present.

package tier11_docs_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tthwBudget is the §22.7 five-minute wall-clock budget. The converted
// offline steps run in well under a second; the budget assertion guards
// against a regression that makes a step pathologically slow (for
// example a build that no longer caches).
const tthwBudget = 5 * time.Minute

// buildBinary builds a cmd/ package into a temp dir and returns the
// binary path. It skips the calling test when the Go toolchain is
// absent (a genuine external-dependency guard).
func buildBinary(t *testing.T, pkg, name string) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain not on PATH: %v", err)
	}
	root := repoRoot(t)
	bin := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", bin, pkg)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", pkg, err, out)
	}
	return bin
}

// spec: 22.7 (documentation gate — five-minute time-to-hello-world)
// diagnosis: A regression in the install / up / session / cleanup chain
//
//	pushes a new operator past the five-minute budget, or the lenny
//	CLI no longer exposes the documented Embedded Mode commands.
func TestTimeToHelloWorld(t *testing.T) {
	t.Parallel()
	start := time.Now()

	t.Run("step_1_brew_install", func(t *testing.T) {
		// `brew install lennylabs/tap/lenny` needs a published Homebrew
		// tap carrying the lenny formula. No tap repository or formula
		// exists in-repo yet, and there is no published tap to install
		// from.
		t.Skip("blocked: the lennylabs/tap Homebrew tap and the lenny formula are unbuilt")
	})

	t.Run("step_2_lenny_up", func(t *testing.T) {
		// `lenny up` is the §17.4 Embedded Mode entry point. The full
		// stack bring-up downloads and runs PostgreSQL and k3s, which
		// needs Docker/containerd and several minutes — a genuine
		// external dependency. The offline assertion here is that the
		// command is wired into the CLI with the documented flags: a
		// new operator following the quick-start must find `lenny up`
		// as a real command, not an "unknown command" error.
		lenny := buildBinary(t, "./cmd/lenny", "lenny")

		// `lenny up` is a recognized command. An unrecognized command
		// exits 2 with an "unknown command" diagnosis; `up` must not.
		// It is exercised via --help so the test does not block on the
		// minutes-long stack bring-up.
		var sb strings.Builder
		help := exec.Command(lenny, "help")
		help.Stdout = &sb
		help.Stderr = &sb
		if err := help.Run(); err != nil {
			t.Fatalf("lenny help: %v\n%s", err, sb.String())
		}
		usage := sb.String()
		for _, want := range []string{"lenny up", "--http-port", "--https-port", "Embedded Mode"} {
			if !strings.Contains(usage, want) {
				t.Errorf("lenny help output is missing %q; the §17.4 up command is not documented\n%s", want, usage)
			}
		}

		// An unknown command must be rejected so the contrast holds:
		// `up` is real, `frobnicate` is not.
		unknown := exec.Command(lenny, "frobnicate")
		if err := unknown.Run(); err == nil {
			t.Error("lenny accepted an unknown command; the CLI dispatch is wrong")
		}

		// The stack bring-up itself is a Docker-dependent step. When
		// Docker is absent the converted assertion above still holds;
		// the end-to-end bring-up is covered by the tier-7 tthw
		// load-tier benchmark on a host with Docker.
		if _, err := exec.LookPath("docker"); err != nil {
			t.Logf("docker absent: the lenny up stack bring-up is not exercised here (covered by the tthw load benchmark)")
		}
	})

	t.Run("step_3_lenny_session_start", func(t *testing.T) {
		// The quick-start's session step is `lenny session new --runtime
		// <name>` (docs/getting-started/quickstart.md). The lenny CLI
		// dispatches up, down, status, logs, token, and image; it has
		// no `session` subcommand. Starting a session also needs a
		// reachable gateway.
		t.Skip("blocked: the `lenny session` CLI subcommand is unbuilt")
	})

	t.Run("step_4_session_cleanup_within_5min", func(t *testing.T) {
		// The end-to-end wall-clock budget is asserted once the brew,
		// up, and session steps all run as real assertions. The session
		// steps depend on the unbuilt `lenny session` subcommand.
		t.Skip("blocked: the end-to-end budget needs steps 1-3 to run; " +
			"the `lenny session` CLI subcommand is unbuilt")
	})

	if elapsed := time.Since(start); elapsed > tthwBudget {
		t.Errorf("time-to-hello-world steps took %s, over the §22.7 budget of %s", elapsed, tthwBudget)
	}
}

// spec: 22.7 (documentation gate — runtime-author quick-start)
// diagnosis: The runtime-author onramp — lenny runtime init, the
//
//	scaffold build files, and lenny runtime validate — drifts past its
//	five-minute budget or no longer produces a usable runtime
//	repository.
func TestRuntimeAuthorQuickStart(t *testing.T) {
	t.Parallel()
	start := time.Now()
	lennyCtl := buildBinary(t, "./cmd/lenny-ctl", "lenny-ctl")

	// The scaffold is generated once and reused across the steps so the
	// later steps assert against the same repository the init step
	// produced.
	workDir := t.TempDir()
	const runtimeName = "alice-agent"

	t.Run("init_scaffold", func(t *testing.T) {
		// `lenny runtime init` scaffolds a runtime repository (§24.18).
		var sb strings.Builder
		init := exec.Command(lennyCtl, "runtime", "init", runtimeName,
			"--language", "go", "--template", "minimal")
		init.Dir = workDir
		init.Stdout = &sb
		init.Stderr = &sb
		if err := init.Run(); err != nil {
			t.Fatalf("lenny runtime init: %v\n%s", err, sb.String())
		}
		// The minimal Go skeleton must carry the runtime.yaml, the
		// entrypoint, the Dockerfile, and the Makefile (§24.18).
		repo := filepath.Join(workDir, runtimeName)
		for _, want := range []string{"runtime.yaml", "main.go", "Dockerfile", "Makefile", "go.mod"} {
			if _, err := os.Stat(filepath.Join(repo, want)); err != nil {
				t.Errorf("scaffold is missing %s: %v", want, err)
			}
		}
	})

	t.Run("make_image", func(t *testing.T) {
		// `make image` (the quick-start's image-build step) consumes the
		// scaffold's Makefile and Dockerfile. The docker build itself
		// needs Docker — a genuine external dependency. The offline
		// assertion is that the scaffold emitted the build inputs the
		// step runs: a Makefile with a build target and a Dockerfile.
		repo := filepath.Join(workDir, runtimeName)
		makefile, err := os.ReadFile(filepath.Join(repo, "Makefile"))
		if err != nil {
			t.Fatalf("read scaffold Makefile: %v", err)
		}
		if !strings.Contains(string(makefile), "build:") {
			t.Errorf("scaffold Makefile has no build target:\n%s", makefile)
		}
		if !strings.Contains(string(makefile), "docker build") {
			t.Errorf("scaffold Makefile build target does not invoke docker build:\n%s", makefile)
		}
		dockerfile := filepath.Join(repo, "Dockerfile")
		if _, err := os.Stat(dockerfile); err != nil {
			t.Errorf("scaffold has no Dockerfile for the image build: %v", err)
		}
		if _, err := exec.LookPath("docker"); err != nil {
			t.Logf("docker absent: the make image build is not exercised here")
		}
	})

	t.Run("publish_to_local_registry", func(t *testing.T) {
		// The full `lenny runtime publish` flow pushes the built image
		// to a registry with `docker push` and registers the runtime
		// against the gateway. Both halves are genuine external
		// dependencies: the docker push needs Docker and a registry,
		// and the register step needs a reachable gateway. The
		// offline-checkable half is `lenny runtime validate`, the
		// §24.18 pre-publish gate that statically checks the
		// runtime.yaml contract and the repository layout. A fresh
		// scaffold must pass it cleanly so the publish step does not
		// fail on a malformed scaffold.
		repo := filepath.Join(workDir, runtimeName)
		var sb strings.Builder
		validate := exec.Command(lennyCtl, "runtime", "validate", repo)
		validate.Stdout = &sb
		validate.Stderr = &sb
		if err := validate.Run(); err != nil {
			t.Fatalf("lenny runtime validate exited non-zero for a fresh scaffold: %v\n%s", err, sb.String())
		}
		if !strings.Contains(sb.String(), "pass") {
			t.Errorf("lenny runtime validate did not report a pass for a fresh scaffold:\n%s", sb.String())
		}
	})

	t.Run("session_against_new_runtime", func(t *testing.T) {
		// Running a session against the just-published runtime needs
		// the gateway to dispatch to it and the `lenny session` CLI
		// subcommand, neither of which is built.
		t.Skip("blocked: gateway session dispatch against a published runtime " +
			"needs the unbuilt `lenny session` CLI subcommand and a reachable gateway")
	})

	if elapsed := time.Since(start); elapsed > tthwBudget {
		t.Errorf("runtime-author quick-start steps took %s, over the §22.7 budget of %s", elapsed, tthwBudget)
	}
}

// spec: 22.7 (documentation gate — non-interactive operator install)
// diagnosis: lenny-ctl install --non-interactive with the documented
//
//	answer file fails to compose a usable install, so the IaC and CI
//	install path is broken.
func TestOperatorInstallNonInteractive(t *testing.T) {
	t.Parallel()
	start := time.Now()
	lennyCtl := buildBinary(t, "./cmd/lenny-ctl", "lenny-ctl")

	t.Run("install_with_answers_file", func(t *testing.T) {
		// `lenny-ctl install --non-interactive --answer-file <f>` reads
		// a YAML answer file and composes the Helm values (§17.6 /
		// §24.20). --dry-run stops before invoking helm, so this step
		// asserts the composition path — the part a CI pipeline depends
		// on — without needing a cluster or the helm binary.
		answerFile := filepath.Join(t.TempDir(), "answers.yaml")
		answers := strings.Join([]string{
			"release:",
			"  name: lenny",
			"  namespace: lenny-system",
			"environment: local",
			"tier: tier1",
			"auth:",
			"  mode: dev",
			"",
		}, "\n")
		if err := os.WriteFile(answerFile, []byte(answers), 0o600); err != nil {
			t.Fatalf("write answer file: %v", err)
		}

		var sb strings.Builder
		install := exec.Command(lennyCtl, "install",
			"--non-interactive", "--answer-file", answerFile, "--dry-run")
		install.Dir = repoRoot(t)
		install.Stdout = &sb
		install.Stderr = &sb
		if err := install.Run(); err != nil {
			t.Fatalf("lenny-ctl install --non-interactive: %v\n%s", err, sb.String())
		}
		out := sb.String()
		// The composed values name the dev-mode flag the answer file
		// selected, and the printed helm command targets the chart.
		for _, want := range []string{"devMode", "helm install", "values-tier1.yaml"} {
			if !strings.Contains(out, want) {
				t.Errorf("composed install output is missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("install_rejects_invalid_answers", func(t *testing.T) {
		// A non-interactive install with an invalid answer file must
		// fail with a diagnosis rather than compose a broken install:
		// dev auth mode is valid only for the local environment.
		answerFile := filepath.Join(t.TempDir(), "bad-answers.yaml")
		answers := strings.Join([]string{
			"release:",
			"  name: lenny",
			"  namespace: lenny-system",
			"environment: prod",
			"tier: tier1",
			"auth:",
			"  mode: dev",
			"",
		}, "\n")
		if err := os.WriteFile(answerFile, []byte(answers), 0o600); err != nil {
			t.Fatalf("write answer file: %v", err)
		}
		var sb strings.Builder
		install := exec.Command(lennyCtl, "install",
			"--non-interactive", "--answer-file", answerFile, "--dry-run")
		install.Dir = repoRoot(t)
		install.Stdout = &sb
		install.Stderr = &sb
		if err := install.Run(); err == nil {
			t.Errorf("lenny-ctl install accepted dev auth mode for a prod environment:\n%s", sb.String())
		}
	})

	t.Run("smoke_session_after_install", func(t *testing.T) {
		// The post-install smoke session needs a live gateway produced
		// by a real `helm install` (not the --dry-run path) and the
		// `lenny session` CLI subcommand to drive it.
		t.Skip("blocked: the post-install smoke session needs a live gateway " +
			"from a real helm install and the unbuilt `lenny session` CLI subcommand")
	})

	if elapsed := time.Since(start); elapsed > tthwBudget {
		t.Errorf("operator-install steps took %s, over the §22.7 budget of %s", elapsed, tthwBudget)
	}
}
