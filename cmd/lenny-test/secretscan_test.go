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

// plantedLine is a line that holds the planted key.
var plantedLine = "aws_access_key_id = " + plantedAccessKey + "\n"

// requireGitleaks skips a test that runs the real scanner on a host
// without it. The gate image installs gitleaks, so the gate runs these
// tests.
func requireGitleaks(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("blocked: precondition not met: gitleaks not on PATH (the gate image installs it)")
	}
	requireGit(t)
}

// requireGit skips a test on a host without git.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("blocked: precondition not met: git not on PATH")
	}
}

// newSecretScanFixtureRepo builds a git repository holding the
// repository's .gitleaks.toml, the two coverage registers with the one
// entry the configuration file needs, and one clean file, commits them,
// and returns its root.
func newSecretScanFixtureRepo(t *testing.T) string {
	t.Helper()
	requireGit(t)
	config, err := os.ReadFile(filepath.Join(repoRoot(), secretScanConfig))
	if err != nil {
		t.Fatalf("read %s: %v", secretScanConfig, err)
	}
	root := t.TempDir()
	gitIn(t, root, "init", "-q", "-b", "main")
	writeScopeFile(t, filepath.Join(root, secretScanConfig), string(config))
	writeCoverageRegisters(t, root, nil, nil)
	writeScopeFile(t, filepath.Join(root, "README.md"), "A fixture with no secrets.\n")
	commitAll(t, root)
	return root
}

// commitAll stages every change under root and commits it.
func commitAll(t *testing.T, root string) {
	t.Helper()
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-q", "--allow-empty", "-m", "fixture")
}

// commitFile writes body at rel under root and commits it.
func commitFile(t *testing.T, root, rel, body string) {
	t.Helper()
	writeScopeFile(t, filepath.Join(root, filepath.FromSlash(rel)), body)
	commitAll(t, root)
}

// requireScanFails runs the whole check and requires it to fail with
// output that holds every one of wants and never the planted key.
func requireScanFails(t *testing.T, root string, wants ...string) {
	t.Helper()
	out, err := runSecretScan(root)
	if err == nil {
		t.Fatalf("secret scan passed, want a failure naming %q:\n%s", wants, out)
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("secret scan output does not contain %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, plantedAccessKey) || strings.Contains(err.Error(), plantedAccessKey) {
		t.Errorf("report repeats the secret value:\n%s", out)
	}
}

// requireScanPasses runs the whole check and requires a pass.
func requireScanPasses(t *testing.T, root string) string {
	t.Helper()
	out, err := runSecretScan(root)
	if err != nil {
		t.Fatalf("secret scan failed: %v\n%s", err, out)
	}
	return out
}

// TestSecretScanFailsOnAPlantedCommittedSecret pins the failure: a
// committed file holding a credential fails the check, the report names
// the file and the rule, and the report does not repeat the secret value,
// because the verdict file carries the check's output.
func TestSecretScanFailsOnAPlantedCommittedSecret(t *testing.T) {
	requireGitleaks(t)
	root := newSecretScanFixtureRepo(t)
	commitFile(t, root, "config/creds.env", plantedLine)
	requireScanFails(t, root, "config/creds.env:1", "aws-access-token")
}

// TestSecretScanReadsTheCommittedTree pins the scope: the scan reads the
// blobs at HEAD. A secret that is untracked or only staged does not fail
// it, and a committed secret fails it even after the working tree drops
// the file or replaces its content, as a skip-worktree entry would.
func TestSecretScanReadsTheCommittedTree(t *testing.T) {
	requireGitleaks(t)
	root := newSecretScanFixtureRepo(t)
	writeScopeFile(t, filepath.Join(root, "scratch/creds.env"), plantedLine)
	writeScopeFile(t, filepath.Join(root, "staged/creds.env"), plantedLine)
	gitIn(t, root, "add", "staged/creds.env")
	out := requireScanPasses(t, root)
	if !strings.Contains(out, "no secrets in 4 committed files") {
		t.Errorf("scan output = %q, want the count of the four committed files", out)
	}
	gitIn(t, root, "rm", "-q", "--cached", "staged/creds.env")
	for _, d := range []string{"scratch", "staged"} {
		if err := os.RemoveAll(filepath.Join(root, d)); err != nil {
			t.Fatal(err)
		}
	}

	commitFile(t, root, "config/creds.env", plantedLine)
	writeScopeFile(t, filepath.Join(root, "config/creds.env"), "clean now\n")
	gitIn(t, root, "update-index", "--skip-worktree", "config/creds.env")
	requireScanFails(t, root, "config/creds.env:1")
	if err := os.Remove(filepath.Join(root, "config/creds.env")); err != nil {
		t.Fatal(err)
	}
	requireScanFails(t, root, "config/creds.env:1")
}

// TestSecretScanReadsPathsThatCollideByCase pins that two committed paths
// differing only in case are both scanned, even on a case-insensitive file
// system, where one would otherwise overwrite the other in the scan tree.
func TestSecretScanReadsPathsThatCollideByCase(t *testing.T) {
	requireGitleaks(t)
	root := newSecretScanFixtureRepo(t)
	for _, f := range []struct{ rel, body string }{{"Notes.txt", "clean\n"}, {"notes.txt", plantedLine}} {
		cmd := exec.Command("git", "hash-object", "-w", "--stdin")
		cmd.Dir = root
		cmd.Stdin = strings.NewReader(f.body)
		oid, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		gitIn(t, root, "update-index", "--add", "--cacheinfo", "100644,"+strings.TrimSpace(string(oid))+","+f.rel)
	}
	gitIn(t, root, "commit", "-q", "-m", "case collision")
	requireScanFails(t, root, "notes.txt:1")
}

// TestSecretScanIgnoresInFileSuppressions pins that the allowlist lives in
// .gitleaks.toml alone: neither a gitleaks:allow comment on the line nor a
// .gitleaksignore file at the root silences a finding.
func TestSecretScanIgnoresInFileSuppressions(t *testing.T) {
	requireGitleaks(t)
	root := newSecretScanFixtureRepo(t)
	writeScopeFile(t, filepath.Join(root, ".gitleaksignore"), "config/creds.env:aws-access-token:1\n")
	commitFile(t, root, "config/creds.env", strings.TrimSuffix(plantedLine, "\n")+" # gitleaks:allow\n")
	requireScanFails(t, root, "config/creds.env:1")
}

// TestSecretScanFailsOnASecretInAnAllowlistedFile pins that an allowlist
// entry allows its exact value and nothing else: a new secret in a file
// that an entry of .gitleaks.toml names still fails. gitleaks skips a
// whole file named by a global allowlist entry without targetRules, so
// this fails if an entry loses its targetRules.
func TestSecretScanFailsOnASecretInAnAllowlistedFile(t *testing.T) {
	requireGitleaks(t)
	root := newSecretScanFixtureRepo(t)
	commitFile(t, root, "tests/integration/backup_binary_test.go", "package integration\n\nconst key = \"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=\"\n\n// "+plantedLine)
	requireScanFails(t, root, "tests/integration/backup_binary_test.go:5", "aws-access-token")
}

// TestSecretScanFailsWithoutItsConfiguration pins the fail-closed path: a
// commit without .gitleaks.toml fails rather than scanning with some
// other configuration.
func TestSecretScanFailsWithoutItsConfiguration(t *testing.T) {
	root := newSecretScanFixtureRepo(t)
	gitIn(t, root, "rm", "-q", secretScanConfig)
	writeCoverageRegisters(t, root, nil, nil)
	commitAll(t, root)
	out, err := runSecretScan(root)
	if err == nil || !strings.Contains(err.Error(), "secret scan configuration") {
		t.Fatalf("scan without its configuration = %v, want a configuration error:\n%s", err, out)
	}
}

// TestSecretScanRefusesAConfigurationThatSkipsOrWeakens pins the
// configuration check, which decodes the TOML and folds keys as gitleaks
// does: an allowlist entry with empty or missing targetRules or regexes,
// or another condition, fails however its header is written; an
// `allowlist` key fails in any spelling and inline; [extend] must set
// useDefault = true and nothing else; a [[rules]] entry may not reuse a
// default rule's id; and the repository's configuration passes.
func TestSecretScanRefusesAConfigurationThatSkipsOrWeakens(t *testing.T) {
	const extend = "[extend]\nuseDefault = true\n"
	const entry = "description = \"ok\"\ncondition = \"AND\"\ntargetRules = [\"generic-api-key\"]\npaths = ['''^a$''']\nregexes = ['''^b$''']\n"
	good := extend + "[[allowlists]]\n" + entry
	accepted := map[string]string{
		"rule-scoped entry":     good,
		"keys in another case":  extend + "[[AllowLists]]\n" + strings.Replace(strings.Replace(entry, "targetRules", "TARGETRULES", 1), `"AND"`, `"and"`, 1),
		"custom rule":           good + "[[rules]]\nid = \"acme-token\"\nregex = '''acme_[a-z0-9]{32}'''\n",
		"header with a comment": extend + "[[allowlists]] # note\n" + entry,
	}
	for name, config := range accepted {
		if err := checkSecretScanConfig([]byte(config)); err != nil {
			t.Errorf("%s: refused: %v", name, err)
		}
	}
	refused := map[string]string{
		"empty targetRules":           strings.Replace(good, `targetRules = ["generic-api-key"]`, "targetRules = []", 1),
		"no targetRules":              strings.Replace(good, "targetRules = [\"generic-api-key\"]\n", "", 1),
		"empty regexes":               strings.Replace(good, "regexes = ['''^b$''']", "regexes = []", 1),
		"condition OR":                strings.Replace(good, `condition = "AND"`, `condition = "OR"`, 1),
		"no condition":                strings.Replace(good, "condition = \"AND\"\n", "", 1),
		"commented header, no rules":  extend + "[[allowlists]] # note\npaths = ['''^a$''']\nregexes = ['''^b$''']\ncondition = \"AND\"\n",
		"spaced header, no rules":     extend + "[[ allowlists ]]\npaths = ['''^a$''']\nregexes = ['''^b$''']\ncondition = \"AND\"\n",
		"inline allowlists":           extend + "allowlists = [{ paths = ['''^a$'''], regexes = ['''^b$'''], condition = \"AND\" }]\n",
		"inline allowlist":            "allowlist = { paths = ['''^a$'''] }\n" + extend,
		"allowlist table":             extend + "[allowlist]\npaths = ['''^a$''']\n",
		"allowlist in another case":   extend + "[AllowList]\npaths = ['''^a$''']\n",
		"key spelled two ways":        extend + "[[allowlists]]\n" + entry + "[[Allowlists]]\n" + entry,
		"useDefault false":            strings.Replace(good, "useDefault = true", "useDefault = false", 1),
		"no extend":                   "[[allowlists]]\n" + entry,
		"disabledRules":               strings.Replace(good, "useDefault = true\n", "useDefault = true\ndisabledRules = [\"generic-api-key\"]\n", 1),
		"extend path":                 strings.Replace(good, "useDefault = true\n", "useDefault = true\npath = \"other.toml\"\n", 1),
		"extend url":                  strings.Replace(good, "useDefault = true\n", "useDefault = true\nurl = \"https://acme.com/x.toml\"\n", 1),
		"default rule override":       good + "[[rules]]\nid = \"generic-api-key\"\nregex = '''never'''\n",
		"unknown top-level key":       good + "allowlistz = 1\n",
		"unknown allowlist entry key": strings.Replace(good, "description = ", "skip = true\ndescription = ", 1),
		"not TOML":                    good + "[[allowlists\n",
	}
	for name, config := range refused {
		if err := checkSecretScanConfig([]byte(config)); err == nil {
			t.Errorf("%s: configuration accepted:\n%s", name, config)
		}
	}
	config, err := os.ReadFile(filepath.Join(repoRoot(), secretScanConfig))
	if err != nil {
		t.Fatal(err)
	}
	if err := checkSecretScanConfig(config); err != nil {
		t.Errorf("the repository's %s is refused: %v", secretScanConfig, err)
	}
}

// fakeGitleaks puts a gitleaks on PATH, ahead of the real one, that
// prints version and writes an empty report.
func fakeGitleaks(t *testing.T, version string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = version ]; then echo " + version + "; exit 0; fi\n" +
		"while [ $# -gt 0 ]; do if [ \"$1\" = --report-path ]; then echo '[]' > \"$2\"; fi; shift; done\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "gitleaks"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestSecretScanWithAnotherGitleaksIsUnverified pins the version check: a
// clean scan by a gitleaks other than the release the copied rules belong
// to reports unverified, and the same scan by that release passes.
func TestSecretScanWithAnotherGitleaksIsUnverified(t *testing.T) {
	root := newSecretScanFixtureRepo(t)
	fakeGitleaks(t, "8.29.0")
	out := requireScanPasses(t, root)
	if status, reason := classifyUnverified(out); status != verdictstatus.Unverified || !strings.Contains(reason, "8.29.0") {
		t.Fatalf("scan by gitleaks 8.29.0 classified %q (%q), want %q:\n%s", status, reason, verdictstatus.Unverified, out)
	}
	fakeGitleaks(t, "v"+gitleaksDefaultsVersion)
	out = requireScanPasses(t, root)
	if status, _ := classifyUnverified(out); status != verdictstatus.Pass {
		t.Fatalf("scan by gitleaks %s classified %q, want %q:\n%s", gitleaksDefaultsVersion, status, verdictstatus.Pass, out)
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
	// A PATH that holds git alone, so the coverage half reads the
	// committed tree and gitleaks is not found.
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

// TestSecretScanPassesOnTheTree runs the check over this checkout's HEAD
// with the repository's configuration, as the static tier does. A failure
// names a committed file that holds a possible secret: remove a real one,
// or give a fake one a reviewed entry in .gitleaks.toml.
func TestSecretScanPassesOnTheTree(t *testing.T) {
	requireGitleaks(t)
	out, err := runSecretScan(repoRoot())
	if err != nil {
		t.Fatalf("secret scan of the tree failed: %v\n%s", err, out)
	}
}
