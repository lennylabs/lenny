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
