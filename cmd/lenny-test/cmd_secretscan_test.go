// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `lenny-test secretscan` implements the merge gate rather than a
// specification behavior, so these tests carry no spec annotation.

// runSecretScanCLI runs the subcommand against root and returns its exit
// code and standard output.
func runSecretScanCLI(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runSecretScanCommand(args, &stdout, &stderr)
	return code, stdout.String() + stderr.String()
}

// TestSecretScanCommandReportsTheThreeOutcomes pins the standalone entry
// point a merger runs on a records-only commit: a clean commit passes with
// exit code 0, a commit the check fails exits 1, a scan by another gitleaks
// release exits 3 as unverified, so it never counts as passing, and each
// verdict line names the commit.
func TestSecretScanCommandReportsTheThreeOutcomes(t *testing.T) {
	root := newSecretScanFixtureRepo(t)
	head := gitOut(t, root, "", "rev-parse", "HEAD")

	fakeGitleaks(t, gitleaksDefaultsVersion)
	code, out := runSecretScanCLI(t, "--root", root)
	if code != exitSecretScanPass || !strings.Contains(out, "lenny-test secretscan: PASS on "+head) {
		t.Fatalf("clean commit: exit %d, want %d:\n%s", code, exitSecretScanPass, out)
	}

	fakeGitleaks(t, "8.29.0")
	code, out = runSecretScanCLI(t, "--root", root)
	if code != exitSecretScanUnverified || !strings.Contains(out, "lenny-test secretscan: UNVERIFIED on "+head) {
		t.Fatalf("another gitleaks release: exit %d, want %d:\n%s", code, exitSecretScanUnverified, out)
	}

	commitFile(t, root, "fixtures/blob.dat", nulBody)
	head = gitOut(t, root, "", "rev-parse", "HEAD")
	fakeGitleaks(t, gitleaksDefaultsVersion)
	code, out = runSecretScanCLI(t, "--root", root)
	if code != exitSecretScanFail || !strings.Contains(out, "lenny-test secretscan: FAIL on "+head) || !strings.Contains(out, "fixtures/blob.dat") {
		t.Fatalf("unlisted binary file: exit %d, want %d:\n%s", code, exitSecretScanFail, out)
	}
}

// TestSecretScanCommandFailsOnAPlantedSecret runs the subcommand with the
// real gitleaks over a commit holding a secret, and requires FAIL.
func TestSecretScanCommandFailsOnAPlantedSecret(t *testing.T) {
	requireGitleaks(t)
	root := newSecretScanFixtureRepo(t)
	commitFile(t, root, "config/creds.env", plantedLine)
	code, out := runSecretScanCLI(t, "--root", root)
	if code != exitSecretScanFail || !strings.Contains(out, "config/creds.env:1") {
		t.Fatalf("planted secret: exit %d, want %d:\n%s", code, exitSecretScanFail, out)
	}
	if strings.Contains(out, plantedAccessKey) {
		t.Errorf("output repeats the secret value:\n%s", out)
	}
}

// TestSecretScanCommandRefusesBadInvocations pins the usage and
// fail-closed paths: an unknown flag or a stray argument exits 2, and a
// directory that is not a git checkout fails.
func TestSecretScanCommandRefusesBadInvocations(t *testing.T) {
	if code, out := runSecretScanCLI(t, "--bogus"); code != exitSecretScanUsage {
		t.Errorf("unknown flag: exit %d, want %d:\n%s", code, exitSecretScanUsage, out)
	}
	if code, out := runSecretScanCLI(t, "extra"); code != exitSecretScanUsage {
		t.Errorf("stray argument: exit %d, want %d:\n%s", code, exitSecretScanUsage, out)
	}
	requireGit(t)
	notRepo := t.TempDir()
	if err := os.WriteFile(filepath.Join(notRepo, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(notRepo))
	if code, out := runSecretScanCLI(t, "--root", notRepo); code != exitSecretScanFail {
		t.Errorf("not a checkout: exit %d, want %d:\n%s", code, exitSecretScanFail, out)
	}
}
