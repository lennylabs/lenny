// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
)

// The secret scan of the static tier runs gitleaks over the committed tree
// at HEAD. It implements the merge gate rather than a specification
// behavior, so it carries no spec annotation (TESTING.md, "Static
// (`static`)"; change 0158).
//
// Mode. gitleaks has two scanning modes. `gitleaks git` reads patches
// from `git log -p`, so it judges history: a secret that a later commit
// removed still fails, and a range such as `-1` judges one commit's diff
// rather than the tree. `gitleaks dir` reads files, so it judges the tree
// the gate claims to judge, and it is the mode this check uses. History
// scanning stays with the secret-scan workflow under .github/workflows.
//
// Scope. The check reads every blob of the tree at HEAD with git and writes
// it into a temporary tree, which gitleaks scans (secretscan_tree.go). The
// working tree and the index play no part, so untracked files, uncommitted
// edits, skip-worktree and assume-unchanged entries, and paths missing from
// disk cannot change what is scanned. Paths in the scan tree are the
// repository paths, so the path rules of the configuration match them,
// except for the renamed files below, which the report maps back.
//
// Skipped paths. gitleaks skips any file or directory whose path matches
// the global allowlist of its default configuration, which .gitleaks.toml
// extends (secretscan_skipped.go). The check writes each committed file
// under such a path into the scan tree under a renamed path no pattern
// matches, keeping its name or extension (renamedScanCandidates), so
// gitleaks reads it and the rules scoped to a name or an extension apply.
//
// Allowlist. The allowlist is .gitleaks.toml at HEAD, which is reviewed
// before it merges (.claude/rules/git-workflow.md). gitleaks skips a
// whole file when it matches the `paths` of any global `[[allowlists]]`
// entry, whatever its condition, so the check refuses an entry that
// names no targetRules: an entry with targetRules joins those rules'
// allowlists, which gitleaks evaluates per finding with the entry's
// condition. The check validates the configuration before the scan
// (secretscan_config.go). The check passes
// --ignore-gitleaks-allow, so a `gitleaks:allow` comment in a file does
// not silence a finding, and it points --gitleaks-ignore-path at an empty
// directory and renames a root .gitleaksignore in the scan tree, so a
// .gitleaksignore file does not either.
//
// Coverage. gitleaks does not read archives, files whose content it
// detects as binary, or symbolic links. Before it runs gitleaks, the check
// holds each committed file of those kinds, and each text file under a
// skipped path, to a reviewed entry in tests/registers, and fails on one
// with no matching entry (secretscan_coverage.go). That half needs no
// gitleaks, so it runs and can fail on a host without it.
//
// Version. The copied default patterns and the classification rules are
// those of gitleaks gitleaksDefaultsVersion. A gitleaks of another version
// still runs, and a clean scan then reports unverified rather than a pass.

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

// committedScan is what one pass over the committed tree produced: the
// scan tree, the facts the coverage half needs, and the committed content
// of the files the check itself reads.
type committedScan struct {
	tree    *scanTree
	facts   map[string]trackedFileFacts
	files   int
	config  []byte
	binReg  []byte
	skipReg []byte
}

// runSecretScan checks that every committed file gitleaks does not read
// has a reviewed entry, then runs gitleaks over the committed tree at HEAD
// under root. It reports the unverified marker when gitleaks is not
// installed or is not the release the check's copied rules belong to,
// because such a scan proves nothing, and it fails closed when the
// committed tree cannot be read, a register or the configuration is
// missing, the configuration has an allowlist that skips whole files, or
// a file gitleaks does not read lacks its entry.
func runSecretScan(root string) (string, error) {
	work, err := os.MkdirTemp("", "lenny-secretscan-")
	if err != nil {
		return "", fmt.Errorf("secret scan work directory: %w", err)
	}
	defer os.RemoveAll(work)
	scan, err := readCommittedScan(root, work)
	if err != nil {
		return "", err
	}
	coverage, err := checkSecretScanCoverage(scan.facts, scan.binReg, scan.skipReg)
	if err != nil {
		return coverage, err
	}
	if scan.config == nil {
		return "", fmt.Errorf("secret scan configuration: %s is not in the committed tree", secretScanConfig)
	}
	if err := checkSecretScanConfig(scan.config); err != nil {
		return "", err
	}
	bin, err := exec.LookPath("gitleaks")
	if err != nil {
		return verdictstatus.UnverifiedMarker + " gitleaks not on PATH; the secret scan did not run (the gate image installs it)\n" + coverage, nil
	}
	version, err := gitleaksVersion(bin)
	if err != nil {
		return "", err
	}
	findings, out, err := runGitleaks(bin, work, scan)
	if len(findings) > 0 {
		return formatSecretScanFindings(findings), fmt.Errorf("gitleaks: %d finding(s) in committed files", len(findings))
	}
	if err != nil {
		return out, err
	}
	summary := fmt.Sprintf("gitleaks %s: no secrets in %d committed files; %s", version, scan.files, coverage)
	if version != gitleaksDefaultsVersion {
		return verdictstatus.UnverifiedMarker + fmt.Sprintf(" gitleaks %s is not %s, the release whose default allowlist and file classification the check copies; the scan is not a verdict\n", version, gitleaksDefaultsVersion) + summary, nil
	}
	return summary, nil
}

// readCommittedScan reads every blob of the tree at HEAD, writes each
// regular file into the scan tree, and records the facts of each file.
func readCommittedScan(root, work string) (*committedScan, error) {
	blobs, err := committedTree(root)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(blobs))
	for _, b := range blobs {
		if !committedPathIsLocal(b.path) {
			return nil, fmt.Errorf("secret scan: the committed path %q is not a plain relative path", b.path)
		}
		if strings.HasPrefix(b.path, renamedScanDir+"/") {
			return nil, fmt.Errorf("secret scan: the committed path %s is under %s, which the scan tree reserves", b.path, renamedScanDir)
		}
		paths = append(paths, b.path)
	}
	attrBinary, err := attributeBinaryFiles(root, work, paths)
	if err != nil {
		return nil, err
	}
	scan := &committedScan{tree: newScanTree(filepath.Join(work, "tree")), facts: map[string]trackedFileFacts{}}
	if err := os.MkdirAll(scan.tree.dir, 0o755); err != nil {
		return nil, fmt.Errorf("secret scan work directory: %w", err)
	}
	err = readBlobs(root, blobs, func(b treeBlob, content []byte) error {
		scan.facts[b.path] = blobFacts(b.path, b.mode, content, attrBinary[b.path])
		switch b.path {
		case secretScanConfig:
			scan.config = content
		case binaryFilesRegister:
			scan.binReg = content
		case skippedPathsRegister:
			scan.skipReg = content
		}
		if b.mode == gitModeSymlink {
			return nil
		}
		scan.files++
		return scan.tree.place(b.path, content)
	})
	if err != nil {
		return nil, err
	}
	return scan, nil
}

// gitleaksVersion returns the version `gitleaks version` prints, without
// a leading "v".
func gitleaksVersion(bin string) (string, error) {
	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		return "", fmt.Errorf("gitleaks version: %w", err)
	}
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "v"), nil
}

// runGitleaks scans the scan tree with the committed configuration and
// returns the findings with their repository paths.
func runGitleaks(bin, work string, scan *committedScan) ([]secretScanFinding, string, error) {
	config := filepath.Join(work, "gitleaks.toml")
	if err := os.WriteFile(config, scan.config, 0o600); err != nil {
		return nil, "", fmt.Errorf("secret scan configuration: %w", err)
	}
	noIgnore := filepath.Join(work, "no-ignore")
	if err := os.MkdirAll(noIgnore, 0o755); err != nil {
		return nil, "", fmt.Errorf("secret scan work directory: %w", err)
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
	cmd.Dir = scan.tree.dir
	out, runErr := cmd.CombinedOutput()
	findings, readErr := readSecretScanReport(report)
	for i := range findings {
		findings[i].File = scan.tree.repositoryPath(findings[i].File)
	}
	if len(findings) > 0 {
		return findings, string(out), nil
	}
	if runErr != nil {
		return nil, string(out), fmt.Errorf("gitleaks: %w", runErr)
	}
	return nil, string(out), readErr
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
	fmt.Fprintf(&b, "gitleaks found %d possible secret(s) in committed files:\n", len(findings))
	for _, f := range findings {
		fmt.Fprintf(&b, "  %s:%d  %s (%s)\n", f.File, f.StartLine, f.RuleID, f.Description)
	}
	b.WriteString("Remove a real credential from the tree and rotate it. A value that is a fake secret by design, such as a test fixture or a documentation example, gets a narrow entry in " + secretScanConfig + " with the reason, and that file is reviewed before it merges.")
	return b.String()
}
