// SPDX-License-Identifier: MIT

//go:build conformance

// TestImageDeploymentConformance exercises the container-image
// deployment path the §26.1 reference-runtime nightly run depends on:
// `lenny-compliance --image <ref> --level <level>`, documented at
// TESTING.md §12.10 ("lenny-compliance --image ghcr.io/example/my-runtime:1.0
// --level full --json"). The nine reference-runtime images ship from
// github.com/lennylabs/runtime-templates outside this repository; they
// are not published to a registry reachable from the in-process test
// runner (TestReferenceCatalogNightly documents that dependency and
// stays structural for that reason). This test cannot exercise those
// nine images, but it can and does exercise the mechanism itself: it
// builds a throwaway local image around the bundled echo runtime and
// drives it through the Basic battery via `lenny-compliance --image`,
// so at least one runtime-by-deployment conformance cell — image
// rather than local binary — actually runs end to end instead of zero.
package tier10_conformance_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// spec: 12.10 ("lenny-compliance --image ghcr.io/example/my-runtime:1.0 --level full --json")
// diagnosis: this test fails when the container-image deployment path
// the reference-runtime nightly run relies on is broken, so every
// image-deployed runtime — including all nine §26 reference runtimes —
// would silently never run the conformance battery in CI.
func TestImageDeploymentConformance(t *testing.T) {
	requireDocker(t)
	a := buildArtifacts(t)

	tag := buildEchoImage(t)
	report := runComplianceImage(t, a, tag, "basic")
	if report.Level != "basic" {
		t.Errorf("report.Level = %q, want basic", report.Level)
	}
	assertAllPass(t, "echo (image)", "basic", report)
	if report.Binary != tag {
		t.Errorf("report.Binary = %q, want the image reference %q", report.Binary, tag)
	}
}

// requireDocker skips the test when docker is not installed or its
// daemon is not reachable — a genuine external-dependency skip, not a
// stand-in for the missing nine reference-runtime images.
func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker not on PATH: %v", err)
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker daemon not reachable: %v", err)
	}
}

// buildEchoImage cross-compiles the bundled echo runtime for Linux
// (matching the container platform regardless of the host OS the test
// runs on) and builds a from-scratch image around it. The image is
// built directly into the local docker daemon and never touches a
// registry, so lenny-compliance's image-pull step (`docker image
// inspect`) finds it cached and the test needs no network access.
func buildEchoImage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	root := repoRoot(t)

	linuxEcho := filepath.Join(dir, "echo")
	cmd := exec.Command("go", "build", "-o", linuxEcho, "./cmd/runtimes/echo")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cross-compile echo for linux/%s: %v\n%s", runtime.GOARCH, err, out)
	}

	dockerfile := "FROM scratch\nCOPY echo /echo\nENTRYPOINT [\"/echo\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}

	tag := "lenny-conformance-echo:test"
	build := exec.Command("docker", "build", "--platform", "linux/"+runtime.GOARCH, "-t", tag, dir)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("docker build: %v\n%s", err, out)
	}
	t.Cleanup(func() { exec.Command("docker", "rmi", "-f", tag).Run() })
	return tag
}

// runComplianceImage runs the compliance harness against image at
// level via `--image` (rather than `--binary`) and decodes the JSON
// report, mirroring runCompliance's binary-path form.
func runComplianceImage(t *testing.T, a *builtArtifacts, image, level string) complianceReport {
	t.Helper()
	cmd := exec.Command(a.compliance, "--image", image, "--level", level, "--json")
	out, err := cmd.Output()
	if len(out) == 0 {
		t.Fatalf("lenny-compliance --image %s --level %s emitted no JSON report (err: %v)", image, level, err)
	}
	var report complianceReport
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("decode compliance report (image %s, level %s): %v\nraw: %s", image, level, err, out)
	}
	if report.Summary.Passed+report.Summary.Failed != report.Summary.Total {
		t.Errorf("report summary inconsistent: passed=%d failed=%d total=%d",
			report.Summary.Passed, report.Summary.Failed, report.Summary.Total)
	}
	return report
}
