// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
)

// The secret scan implements the merge gate rather than a specification
// behavior, so these tests carry no spec annotation.

// plantedAccessKey is an AWS access key ID in the form the default
// gitleaks rule matches. It is assembled at run time, so this file, which
// the scan of the tree reads, never holds the value in one piece.
var plantedAccessKey = "AKIA" + "Q7ZK" + "M2XW" + "R4TN" + "V6PB"

// requireGitleaks skips a test that runs the real scanner on a host
// without it. The gate image installs gitleaks, so the gate runs these
// tests.
func requireGitleaks(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("blocked: precondition not met: gitleaks not on PATH (the gate image installs it)")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("blocked: precondition not met: git not on PATH")
	}
}

// newSecretScanFixtureRepo builds a git checkout holding the repository's
// .gitleaks.toml, the two coverage registers with the one entry the
// configuration file needs, and one clean tracked file, commits them, and
// returns the checkout root.
func newSecretScanFixtureRepo(t *testing.T) string {
	t.Helper()
	config, err := os.ReadFile(filepath.Join(repoRoot(), secretScanConfig))
	if err != nil {
		t.Fatalf("read %s: %v", secretScanConfig, err)
	}
	root := t.TempDir()
	gitIn(t, root, "init", "-q", "-b", "main")
	writeScopeFile(t, filepath.Join(root, secretScanConfig), string(config))
	writeCoverageRegisters(t, root, nil, nil)
	writeScopeFile(t, filepath.Join(root, "README.md"), "A fixture with no secrets.\n")
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-q", "-m", "seed")
	return root
}

// plantSecret writes the planted key into a file under root, tracked or
// not, with an optional suffix on the key's line.
func plantSecret(t *testing.T, root, rel string, track bool, suffix string) {
	t.Helper()
	writeScopeFile(t, filepath.Join(root, filepath.FromSlash(rel)), "aws_access_key_id = "+plantedAccessKey+suffix+"\n")
	if track {
		gitIn(t, root, "add", rel)
	}
}

// TestSecretScanFailsOnAPlantedTrackedSecret pins the failure: a tracked
// file holding a credential fails the check, the report names the file
// and the rule, and the report does not repeat the secret value, because
// the verdict file carries the check's output.
func TestSecretScanFailsOnAPlantedTrackedSecret(t *testing.T) {
	requireGitleaks(t)
	root := newSecretScanFixtureRepo(t)
	plantSecret(t, root, "config/creds.env", true, "")

	out, err := runSecretScan(root)
	if err == nil {
		t.Fatalf("scan passed on a planted secret:\n%s", out)
	}
	if !strings.Contains(out, "config/creds.env:1") || !strings.Contains(out, "aws-access-token") {
		t.Errorf("report does not name the file, line, and rule:\n%s", out)
	}
	if strings.Contains(out, plantedAccessKey) || strings.Contains(err.Error(), plantedAccessKey) {
		t.Errorf("report repeats the secret value:\n%s", out)
	}
}

// TestSecretScanJudgesTrackedFilesOnly pins the scope: the same secret in
// an untracked file does not fail the check, because the gate judges a
// commit and an untracked file is part of none.
func TestSecretScanJudgesTrackedFilesOnly(t *testing.T) {
	requireGitleaks(t)
	root := newSecretScanFixtureRepo(t)
	plantSecret(t, root, "scratch/creds.env", false, "")

	out, err := runSecretScan(root)
	if err != nil {
		t.Fatalf("scan failed on an untracked secret: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no secrets in 4 tracked files") {
		t.Errorf("scan output = %q, want the count of the four tracked files", out)
	}
}

// TestSecretScanIgnoresInFileSuppressions pins that the allowlist lives in
// .gitleaks.toml alone: neither a gitleaks:allow comment on the line nor a
// .gitleaksignore file at the root silences a finding.
func TestSecretScanIgnoresInFileSuppressions(t *testing.T) {
	requireGitleaks(t)
	root := newSecretScanFixtureRepo(t)
	plantSecret(t, root, "config/creds.env", true, " # gitleaks:allow")
	writeScopeFile(t, filepath.Join(root, ".gitleaksignore"), "config/creds.env:aws-access-token:1\n")
	gitIn(t, root, "add", ".gitleaksignore")

	out, err := runSecretScan(root)
	if err == nil {
		t.Fatalf("an in-file suppression silenced the planted secret:\n%s", out)
	}
	if !strings.Contains(out, "config/creds.env:1") {
		t.Errorf("report does not name the planted file:\n%s", out)
	}
}

// TestSecretScanFailsWithoutItsConfiguration pins the fail-closed path: a
// checkout without .gitleaks.toml fails rather than scanning with some
// other configuration.
func TestSecretScanFailsWithoutItsConfiguration(t *testing.T) {
	requireGitleaks(t)
	root := newSecretScanFixtureRepo(t)
	if err := os.Remove(filepath.Join(root, secretScanConfig)); err != nil {
		t.Fatal(err)
	}
	if out, err := runSecretScan(root); err == nil {
		t.Fatalf("scan passed without its configuration:\n%s", out)
	}
}

// TestSecretScanWithoutGitleaksIsUnverified pins the report of a host
// without gitleaks: the check carries the unverified marker, so the tier
// reads "did not run" rather than a pass.
func TestSecretScanWithoutGitleaksIsUnverified(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("blocked: precondition not met: git not on PATH")
	}
	root := newSecretScanFixtureRepo(t)
	// A PATH that holds git alone, so the coverage half lists the tracked
	// files and gitleaks is not found.
	pathDir := t.TempDir()
	if err := os.Symlink(gitBin, filepath.Join(pathDir, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir)
	out, err := runSecretScan(root)
	if err != nil {
		t.Fatalf("scan without gitleaks returned an error: %v", err)
	}
	if status, _ := classifyUnverified(out); status != verdictstatus.Unverified {
		t.Fatalf("scan without gitleaks classified %q, want %q (output %q)", status, verdictstatus.Unverified, out)
	}
}

// TestSecretScanPassesOnTheTree runs the check over this checkout with
// the repository's configuration, as the static tier does. A failure
// names a tracked file that holds a possible secret: remove a real one,
// or give a fake one a reviewed entry in .gitleaks.toml.
func TestSecretScanPassesOnTheTree(t *testing.T) {
	requireGitleaks(t)
	out, err := runSecretScan(repoRoot())
	if err != nil {
		t.Fatalf("secret scan of the tree failed: %v\n%s", err, out)
	}
}
