// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
)

// The secret scan of the static tier runs gitleaks over the files git
// tracks, as they stand in the working tree. It implements the merge gate
// rather than a specification behavior, so it carries no spec annotation
// (TESTING.md, "Static (`static`)"; change 0158).
//
// Mode. gitleaks has two scanning modes. `gitleaks git` reads patches
// from `git log -p`, so it judges history: a secret that a later commit
// removed still fails, and a range such as `-1` judges one commit's diff
// rather than the tree. `gitleaks dir` reads files, so it judges the tree
// the gate claims to judge, and it is the mode this check uses. History
// scanning stays with the secret-scan workflow under .github/workflows.
//
// Scope. `gitleaks dir` walks every file under its target and does not
// consult git, so from the repository root it would also read untracked
// scratch trees, the verdicts under tests/results, and, in the main
// checkout, every nested worktree under .claude/worktrees. The check
// therefore places the files `git ls-files --cached` lists into a temporary
// tree, by hard link or by copy, and scans that tree. Untracked files that
// other static checks write while they run stay out of the scan. Paths in the report are
// relative to the copy's root, so they read as repository paths and the
// path rules of the configuration match them.
//
// Allowlist. The allowlist is .gitleaks.toml at the repository root, which
// is reviewed before it merges (.claude/rules/git-workflow.md), together
// with the global allowlist of the default configuration it extends. The
// check passes --ignore-gitleaks-allow, so a `gitleaks:allow` comment in a
// file does not silence a finding, and it points --gitleaks-ignore-path at
// an empty directory and leaves a root .gitleaksignore out of the scanned
// tree, so a .gitleaksignore file does not either.
//
// Coverage. gitleaks skips archives (no --max-archive-depth is passed, and
// the tracked upload fixtures include archive bombs), files it detects as
// binary, and the paths the global allowlist of its default configuration
// skips. Before it runs gitleaks, the check holds every tracked file of
// those kinds to a reviewed entry in tests/registers/binary-files.yaml or
// tests/registers/secret-scan-skipped-paths.yaml, and fails on one with no
// matching entry (secretscan_coverage.go). That half needs no gitleaks, so
// it runs and can fail on a host without it.

// secretScanConfig is the gitleaks configuration at the repository root.
const secretScanConfig = ".gitleaks.toml"

// secretScanFinding is the part of a gitleaks JSON report entry the check
// prints. The scan runs with --redact, and the check never prints the
// Secret or Match fields, so no secret value reaches the verdict file.
type secretScanFinding struct {
	RuleID      string `json:"RuleID"`
	Description string `json:"Description"`
	File        string `json:"File"`
	StartLine   int    `json:"StartLine"`
}

// runSecretScan checks that every tracked file gitleaks does not read has
// a reviewed entry, then runs gitleaks over the files git tracks under
// root. It reports the unverified marker when gitleaks is not installed,
// because a scan that did not run proves nothing, and it fails closed when
// the tracked files cannot be listed, a register or the configuration is
// missing, or a file gitleaks does not read lacks its entry.
func runSecretScan(root string) (string, error) {
	files, err := trackedFiles(root)
	if err != nil {
		return "", err
	}
	coverage, err := checkSecretScanCoverage(root, files)
	if err != nil {
		return coverage, err
	}
	bin, err := exec.LookPath("gitleaks")
	if err != nil {
		return verdictstatus.UnverifiedMarker + " gitleaks not on PATH; the secret scan did not run (the gate image installs it)\n" + coverage, nil
	}
	config := filepath.Join(root, secretScanConfig)
	if _, err := os.Stat(config); err != nil {
		return "", fmt.Errorf("secret scan configuration: %w", err)
	}
	work, err := os.MkdirTemp("", "lenny-secretscan-")
	if err != nil {
		return "", fmt.Errorf("secret scan work directory: %w", err)
	}
	defer os.RemoveAll(work)
	tree := filepath.Join(work, "tree")
	noIgnore := filepath.Join(work, "no-ignore")
	if err := os.MkdirAll(noIgnore, 0o755); err != nil {
		return "", fmt.Errorf("secret scan work directory: %w", err)
	}
	copied, err := copyTrackedFiles(root, tree, files)
	if err != nil {
		return "", err
	}
	report := filepath.Join(work, "report.json")
	cmd := exec.Command(
		bin, "dir", ".",
		"--config", config,
		"--gitleaks-ignore-path", noIgnore,
		"--ignore-gitleaks-allow",
		"--redact",
		"--no-banner",
		"--no-color",
		"--log-level", "warn",
		"--exit-code", "1",
		"--report-format", "json",
		"--report-path", report,
	)
	cmd.Dir = tree
	out, runErr := cmd.CombinedOutput()
	findings, readErr := readSecretScanReport(report)
	if len(findings) > 0 {
		return formatSecretScanFindings(findings), fmt.Errorf("gitleaks: %d finding(s) in tracked files", len(findings))
	}
	if runErr != nil {
		return string(out), fmt.Errorf("gitleaks: %w", runErr)
	}
	if readErr != nil {
		return string(out), readErr
	}
	return fmt.Sprintf("gitleaks: no secrets in %d tracked files; %s", copied, coverage), nil
}

// trackedFiles returns the slash-separated, root-relative paths git lists
// in the index under root.
func trackedFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "-z", "--cached")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list tracked files: %w", err)
	}
	var files []string
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) > 0 {
			files = append(files, string(p))
		}
	}
	return files, nil
}

// copyTrackedFiles places the working-tree content of each listed regular
// file from root into dst at the same relative path, and returns how many
// it placed. A listed path that the working tree no longer holds is
// skipped, because a deletion carries no secret. A symbolic link is
// skipped, because its content is a path and following it could read a
// file outside the checkout. A directory is a submodule, whose files
// belong to another repository. A .gitleaksignore at the root is left
// out, because gitleaks reads that file from the root of the tree it scans
// whatever --gitleaks-ignore-path names, and the allowlist lives in
// .gitleaks.toml alone.
func copyTrackedFiles(root, dst string, files []string) (int, error) {
	copied := 0
	for _, rel := range files {
		src := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(src)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return copied, fmt.Errorf("secret scan: %w", err)
		}
		if !info.Mode().IsRegular() || rel == ".gitleaksignore" {
			continue
		}
		if err := copyFile(src, filepath.Join(dst, filepath.FromSlash(rel))); err != nil {
			return copied, fmt.Errorf("secret scan: copy %s: %w", rel, err)
		}
		copied++
	}
	return copied, nil
}

// copyFile places one regular file at dst, creating the parent
// directories. It hard-links the file when src and dst share a file system,
// which costs no copy, and copies it otherwise, as in the gate container,
// where the temporary directory and the mounted worktree are on different
// file systems. gitleaks only reads the file, so a link is as good as a
// copy.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if os.Link(src, dst) == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// readSecretScanReport parses the JSON report gitleaks wrote. A report
// that is absent or does not parse is an error, so a scan that wrote no
// report is never read as a clean one.
func readSecretScanReport(path string) ([]secretScanFinding, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("gitleaks report: %w", err)
	}
	var findings []secretScanFinding
	if err := json.Unmarshal(body, &findings); err != nil {
		return nil, fmt.Errorf("gitleaks report: %w", err)
	}
	return findings, nil
}

// formatSecretScanFindings lists each finding by file, line, and rule, with
// the remedy. It prints no secret value.
func formatSecretScanFindings(findings []secretScanFinding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "gitleaks found %d possible secret(s) in tracked files:\n", len(findings))
	for _, f := range findings {
		fmt.Fprintf(&b, "  %s:%d  %s (%s)\n", f.File, f.StartLine, f.RuleID, f.Description)
	}
	b.WriteString("Remove a real credential from the tree and rotate it. A value that is a fake secret by design, such as a test fixture or a documentation example, gets a narrow entry in " + secretScanConfig + " with the reason, and that file is reviewed before it merges.")
	return b.String()
}
