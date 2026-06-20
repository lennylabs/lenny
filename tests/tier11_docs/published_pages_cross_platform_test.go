// SPDX-License-Identifier: MIT

// Tier-11 documentation checks for proposal 0013 (cross-platform
// Embedded Mode) on the published, reader-facing pages — the quickstart
// and the Go/Python/TypeScript SDK examples. These pages do not live in
// spec/, so the spec-anchor tests in embedded_mode_anchors_test.go do
// not cover them. The applied §17.4/§17.9.6/§15.4.3/§3.10 edits make the
// pre-0013 claims on these pages ("No Docker ... required", a host-side
// "Linux-only" abstract-socket restriction, pods unconditionally
// "sandboxed under gVisor") false. These tests pin the reconciled text
// so a regression that reintroduces a contradiction fails here.
//
// The tests are NOT under a build tag because they exercise the
// repository state directly — no external infrastructure required.

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// quickstartPath is the published quickstart page.
func quickstartPath(root string) string {
	return filepath.Join(root, "docs", "getting-started", "quickstart.md")
}

// localDevelopmentPath is the published runtime-author local-development
// page. It is the primary reader-facing local-dev page for the §17.4
// Embedded Mode / `make run` / `docker compose up` comparison.
func localDevelopmentPath(root string) string {
	return filepath.Join(root, "docs", "runtime-author-guide", "local-development.md")
}

// installationPath is the published operator-guide installation page,
// whose "`lenny up` for local evaluation" section is a deployment-facing
// Embedded Mode description.
func installationPath(root string) string {
	return filepath.Join(root, "docs", "operator-guide", "installation.md")
}

// sdkExamplePages is the set of SDK-example pages whose Standard-level
// platform note proposal §3.5/§3.6 reconciliation applies to.
func sdkExamplePages(root string) []string {
	base := filepath.Join(root, "docs", "runtime-author-guide", "sdk-examples")
	return []string{
		filepath.Join(base, "go.md"),
		filepath.Join(base, "python.md"),
		filepath.Join(base, "typescript.md"),
	}
}

// diagnosis: the quickstart's Docker prerequisite reconciliation from
// proposal §3.2/§3.3 regressed. The page either reasserts that no Docker
// is required (false on macOS and Windows, where the embedded k3s runs
// under Docker Desktop's Linux VM) or drops the Docker Desktop
// prerequisite entirely, so a macOS or Windows reader follows `lenny up`
// without the one prerequisite it needs.
//
// spec: §17.4 (Embedded Mode prerequisite), §17.9.6 (embedded backends).
// Proposal 0013 §3.2, §3.3.
func TestQuickstartStatesDockerPrerequisiteOnMacOSAndWindows(t *testing.T) {
	root := repoRoot(t)
	content := readDocPage(t, quickstartPath(root))

	// The reconciled page must name Docker Desktop as the macOS/Windows
	// prerequisite that supplies the Linux kernel the embedded k3s needs.
	for _, want := range []string{
		"Docker Desktop",
		"Linux VM",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("quickstart.md missing Docker prerequisite text %q (proposal §3.2/§3.3 regression)", want)
		}
	}

	// The pre-0013 blanket "No Docker ... required" claim must be gone.
	// It is false on macOS and Windows under the Docker-backed substrate.
	for _, banned := range []string{
		"No Docker, no external services",
		"No Docker, no external service",
	} {
		if strings.Contains(content, banned) {
			t.Errorf("quickstart.md still asserts %q; the Docker-backed substrate makes this false on macOS and Windows (proposal §3.2)", banned)
		}
	}
}

// diagnosis: the quickstart's local-isolation-fidelity reconciliation
// from proposal §3.10 regressed. The page presents the embedded stack's
// pod isolation as identical to production ("sandboxed under gVisor"
// among properties that "apply equally to a production install") when the
// embedded single-node cluster degrades the gVisor profile to runc and
// disables NetworkPolicy. An evaluator would mistake local behavior for
// the production isolation boundary.
//
// spec: §17.4 (local isolation fidelity), §5.3 (isolation profiles),
// §13.2 (network isolation). Proposal 0013 §3.10.
func TestQuickstartDisclosesLocalIsolationDegradation(t *testing.T) {
	root := repoRoot(t)
	content := readDocPage(t, quickstartPath(root))

	// The page must disclose that the local cluster runs the sandboxed
	// profile under runc rather than gVisor and disables NetworkPolicy.
	for _, want := range []string{
		"runc",
		"NetworkPolicy",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("quickstart.md missing local-fidelity disclosure text %q (proposal §3.10 regression)", want)
		}
	}

	// The pre-0013 unqualified claim that pods are "sandboxed under
	// gVisor" as a property that holds in the local stack must not appear
	// without the local-degradation qualifier. We assert the exact
	// pre-0013 list fragment is gone; the reconciled prose names gVisor
	// only in the context of production or the runc-degradation note.
	if strings.Contains(content, "sandboxed under gVisor, and no network access") {
		t.Errorf("quickstart.md still presents pods as unconditionally 'sandboxed under gVisor' in the local stack; gVisor degrades to runc on the embedded cluster (proposal §3.10)")
	}

	// Proposal §3.10 keeps the gVisor and Kata runc-degradation causes
	// distinct: the `sandboxed` (gVisor) profile degrades because the
	// embedded cluster installs no gVisor runtime class, while the
	// `microvm` (Kata) profile degrades because it needs hardware
	// virtualization the single-node substrate cannot nest. The page must
	// carry the microvm-specific cause, not collapse both profiles under
	// the gVisor-runtime-class cause. §1.4 of the proposal flags conflating
	// the two under a single cause as a spec defect — and it is technically
	// wrong for `microvm`, whose runc fallback is not caused by a missing
	// gVisor runtime class.
	if !strings.Contains(content, "hardware virtualization the local substrate cannot nest") {
		t.Errorf("quickstart.md is missing the distinct `microvm`/Kata runc-degradation cause (hardware virtualization the local substrate cannot nest); proposal §3.10 keeps the gVisor and Kata causes distinct")
	}
	for _, conflated := range []string{
		"`sandboxed` and `microvm` profiles run under standard `runc` locally",
		"`sandboxed` and `microvm` profiles run under standard `runc`",
	} {
		if strings.Contains(content, conflated) {
			t.Errorf("quickstart.md conflates the gVisor and Kata causes under one cause (%q); proposal §3.10 requires each profile to carry its own distinct cause", conflated)
		}
	}
}

// diagnosis: the SDK-example Standard-level platform note reconciliation
// from proposal §3.5/§3.6 regressed on one of the published SDK pages.
// The note either reasserts a blanket "Linux-only" abstract-socket
// restriction that excludes macOS and Windows readers from the Standard
// level, or drops the Embedded-Mode carve-out, telling a macOS or Windows
// reader the Standard level is closed to them when Embedded Mode
// (`lenny up`) runs the adapter in an in-cluster Linux pod under Docker
// Desktop's Linux VM.
//
// spec: §15.4.3 (transport platform note), §17.4 (macOS/Windows note).
// Proposal 0013 §3.5, §3.6.
func TestSDKExamplesScopeAbstractSocketRestrictionToHostSide(t *testing.T) {
	root := repoRoot(t)
	for _, page := range sdkExamplePages(root) {
		content := readDocPage(t, page)
		name := filepath.Base(filepath.Dir(page)) + "/" + filepath.Base(page)

		// The reconciled note must name the Embedded-Mode in-cluster-pod
		// path as the way Standard-level authors work on macOS and Windows.
		for _, want := range []string{
			"in-cluster Linux pod",
			"Embedded Mode",
			"macOS and Windows",
		} {
			if !strings.Contains(content, want) {
				t.Errorf("%s missing abstract-socket reconciliation text %q (proposal §3.5/§3.6 regression)", name, want)
			}
		}

		// The pre-0013 over-broad claim — that the Standard level's
		// abstract sockets are flatly "Linux-only. Use `docker compose up`
		// on macOS" — must be gone; it tells a macOS reader the Standard
		// level excludes them.
		if strings.Contains(content, "which are Linux-only. Use `docker compose up` on macOS") {
			t.Errorf("%s still asserts the Standard level is flatly Linux-only; Embedded Mode runs the adapter in an in-cluster Linux pod on macOS and Windows (proposal §3.5/§3.6)", name)
		}
	}
}

// diagnosis: the local-development page's cross-platform substrate
// reconciliation from proposal 0013 (§3.1/§3.2/§3.7) regressed. The page
// either still describes `lenny up` as running the stack "in-process"
// (stale: k3s runs as a managed child process on Linux and as a Docker
// container on macOS/Windows, the gateway and controllers as host child
// processes), or omits the Docker Desktop prerequisite that macOS and
// Windows need, leaving a non-Linux runtime author following `lenny up`
// without the one prerequisite it requires on their host.
//
// spec: §17.4 (per-OS substrate, Docker prerequisite), §24.19 (`lenny up`
// stack composition). Proposal 0013 §3.1, §3.2, §3.7.
func TestLocalDevelopmentStatesPerOSSubstrateAndDockerPrerequisite(t *testing.T) {
	root := repoRoot(t)
	content := readDocPage(t, localDevelopmentPath(root))

	// The reconciled page must name the per-OS substrate (managed child
	// process on Linux, Docker Desktop's Linux VM on macOS/Windows) and the
	// Docker Desktop prerequisite.
	for _, want := range []string{
		"Docker Desktop",
		"Linux VM",
		"managed child process",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("local-development.md missing cross-platform substrate text %q (proposal §3.1/§3.2 regression)", want)
		}
	}

	// The stale "starts everything in-process" framing for `lenny up` must
	// be gone; it is false against the current per-OS substrate (proposal
	// §3.7 removes the "in-process" characterization of k3s/controllers).
	if strings.Contains(content, "starts everything in-process") {
		t.Errorf("local-development.md still describes `lenny up` as starting everything in-process; k3s runs as a managed child process or Docker container and the controllers as host child processes (proposal §3.7)")
	}
}

// diagnosis: the local-development page's local-isolation-fidelity
// disclosure from proposal §3.10 regressed. The page presents the
// embedded stack's pod isolation as production-equivalent, when the
// single-node cluster degrades the `sandboxed` (gVisor) and `microvm`
// (Kata) profiles to runc for distinct reasons and disables
// NetworkPolicy. A runtime author would mistake local behavior for the
// production isolation boundary.
//
// spec: §17.4 (local isolation fidelity), §5.3 (isolation profiles),
// §13.2 (network isolation). Proposal 0013 §3.10.
func TestLocalDevelopmentDisclosesLocalIsolationDegradation(t *testing.T) {
	root := repoRoot(t)
	content := readDocPage(t, localDevelopmentPath(root))

	// The page must disclose the runc degradation and the disabled
	// NetworkPolicy enforcement.
	for _, want := range []string{
		"runc",
		"NetworkPolicy",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("local-development.md missing local-fidelity disclosure text %q (proposal §3.10 regression)", want)
		}
	}

	// Proposal §3.10 keeps the gVisor and Kata runc-degradation causes
	// distinct. The page must carry the microvm-specific cause rather than
	// collapse both profiles under the gVisor-runtime-class cause.
	if !strings.Contains(content, "hardware virtualization the local substrate cannot nest") {
		t.Errorf("local-development.md is missing the distinct `microvm`/Kata runc-degradation cause; proposal §3.10 keeps the gVisor and Kata causes distinct")
	}
	if !strings.Contains(content, "no gVisor runtime class") {
		t.Errorf("local-development.md is missing the distinct `sandboxed`/gVisor runc-degradation cause (the cluster installs no gVisor runtime class); proposal §3.10 keeps the gVisor and Kata causes distinct")
	}
}

// diagnosis: the local-development page's abstract-socket reconciliation
// from proposal §3.5/§3.6 regressed. The page either keeps a blanket
// "Linux-only" Standard/Full restriction that excludes macOS and Windows
// authors, or drops the Embedded-Mode in-cluster-pod carve-out that makes
// abstract sockets available on those hosts under `lenny up`.
//
// spec: §15.4.3 (transport platform note), §17.4 (macOS/Windows note).
// Proposal 0013 §3.5, §3.6.
func TestLocalDevelopmentScopesAbstractSocketRestrictionToHostSide(t *testing.T) {
	root := repoRoot(t)
	content := readDocPage(t, localDevelopmentPath(root))

	// The reconciled note must name the Embedded-Mode in-cluster-pod path
	// as how Standard/Full authors work on macOS and Windows.
	if !strings.Contains(content, "in-cluster Linux pod") {
		t.Errorf("local-development.md missing the in-cluster Linux pod carve-out for abstract sockets on macOS/Windows (proposal §3.5/§3.6 regression)")
	}

	// The pre-0013 blanket claim that abstract sockets "exist only on
	// Linux, so on macOS or Windows use `make run` for Basic-level runtimes
	// and `lenny up` (or `docker compose up`) for Standard and Full" — which
	// asserted a flat host-OS restriction without the in-cluster-pod
	// explanation — must be gone.
	if strings.Contains(content, "Those sockets exist only on Linux, so on macOS or Windows use") {
		t.Errorf("local-development.md still asserts a flat host-OS abstract-socket restriction without the in-cluster-pod carve-out (proposal §3.5/§3.6)")
	}
}

// diagnosis: the operator-guide installation page's `lenny up` section
// reconciliation from proposal §3.2/§3.7/§3.10 regressed. The deployment
// reader either still sees `lenny up` described as running "in-process"
// (stale), or is not told Docker Desktop is required on macOS/Windows, or
// is not warned the local cluster does not reproduce production isolation.
//
// spec: §17.4 (per-OS substrate, Docker prerequisite, local isolation
// fidelity), §24.19 (`lenny up`). Proposal 0013 §3.2, §3.7, §3.10.
func TestInstallationLennyUpStatesSubstrateAndFidelity(t *testing.T) {
	root := repoRoot(t)
	content := readDocPage(t, installationPath(root))

	for _, want := range []string{
		"Docker Desktop",
		"managed child process",
		"runc",
		"NetworkPolicy",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("installation.md `lenny up` section missing cross-platform/fidelity text %q (proposal §3.2/§3.7/§3.10 regression)", want)
		}
	}

	// The stale "runs the entire platform in-process" framing must be gone.
	if strings.Contains(content, "runs the entire platform in-process") {
		t.Errorf("installation.md still describes `lenny up` as running the entire platform in-process; k3s runs as a managed child process or Docker container and the controllers as host child processes (proposal §3.7)")
	}
}

// readDocPage reads a documentation page and fails the test if it is
// missing.
func readDocPage(t testing.TB, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
