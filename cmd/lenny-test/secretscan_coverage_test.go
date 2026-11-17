// SPDX-License-Identifier: MIT

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The coverage half of the secret scan implements the merge gate rather
// than a specification behavior, so these tests carry no spec annotation.
// They need git but not gitleaks: runSecretScan checks coverage before it
// looks for gitleaks, so a coverage failure fails on any host.

// configSkippedEntry is the entry every fixture carries for its copy of
// .gitleaks.toml, which the default allowlist skips by name.
var configSkippedEntry = skippedPathEntry{Path: secretScanConfig, Reason: "the scan's configuration"}

// writeCoverageRegisters writes the two coverage registers under root with
// the given entries, plus the entry for .gitleaks.toml.
func writeCoverageRegisters(t *testing.T, root string, binaries []binaryFileEntry, skipped []skippedPathEntry) {
	t.Helper()
	if binaries == nil {
		binaries = []binaryFileEntry{}
	}
	bin, err := yaml.Marshal(scanCoverageRegister[binaryFileEntry]{Kind: "binary-files", Version: 1, Entries: binaries})
	if err != nil {
		t.Fatal(err)
	}
	skip, err := yaml.Marshal(scanCoverageRegister[skippedPathEntry]{Kind: "secret-scan-skipped-paths", Version: 1, Entries: append([]skippedPathEntry{configSkippedEntry}, skipped...)})
	if err != nil {
		t.Fatal(err)
	}
	writeScopeFile(t, filepath.Join(root, binaryFilesRegister), string(bin))
	writeScopeFile(t, filepath.Join(root, skippedPathsRegister), string(skip))
}

// newCoverageFixtureRepo builds the secret-scan fixture checkout. It skips
// when git is absent.
func newCoverageFixtureRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("blocked: precondition not met: git not on PATH")
	}
	return newSecretScanFixtureRepo(t)
}

// trackFile writes body at rel under root and stages it.
func trackFile(t *testing.T, root, rel, body string) {
	t.Helper()
	writeScopeFile(t, filepath.Join(root, filepath.FromSlash(rel)), body)
	gitIn(t, root, "add", rel)
}

// coverageOf runs the coverage half over the files the fixture tracks.
func coverageOf(t *testing.T, root string) (string, error) {
	t.Helper()
	files, err := trackedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	return checkSecretScanCoverage(root, files)
}

// entryFor builds the binary-files.yaml entry that matches body.
func entryFor(rel, class, body string) binaryFileEntry {
	sum := sha256.Sum256([]byte(body))
	return binaryFileEntry{Path: rel, Class: class, Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:]), Reason: "fixture"}
}

// requireScanFails runs the whole check and requires it to fail with
// output that holds every one of wants.
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
}

// requireCoveragePasses requires the coverage half to pass.
func requireCoveragePasses(t *testing.T, root string) {
	t.Helper()
	if out, err := coverageOf(t, root); err != nil {
		t.Fatalf("coverage failed: %v\n%s", err, out)
	}
}

// The binary body the fixtures use: a NUL byte inside text.
const nulBody = "header\x00payload\n"

// TestSecretScanFailsOnATrackedFileUnderASkippedPath pins decision 5: a
// tracked text file whose path matches a default allowlist pattern fails,
// and the report names the file and the pattern.
func TestSecretScanFailsOnATrackedFileUnderASkippedPath(t *testing.T) {
	root := newCoverageFixtureRepo(t)
	trackFile(t, root, "web/package-lock.json", "{}\n")
	requireScanFails(t, root, "web/package-lock.json", "package-lock", skippedPathsRegister)
}

// TestSecretScanFailsOnAFileInASkippedDirectory pins that a directory
// counts: gitleaks does not enter a directory whose path matches, so a
// file under it fails even when its own name matches no pattern, and the
// report names the directory.
func TestSecretScanFailsOnAFileInASkippedDirectory(t *testing.T) {
	root := newCoverageFixtureRepo(t)
	trackFile(t, root, "tools/node_modules/pkg/index.js", "module.exports = 1\n")
	trackFile(t, root, "data/blobs.bin/notes.txt", "notes\n")
	requireScanFails(t, root,
		"tools/node_modules/pkg/index.js", "its directory tools/node_modules",
		"data/blobs.bin/notes.txt", "its directory data/blobs.bin")
}

// TestSecretScanAcceptsAReviewedSkippedPath pins the reviewed entry: the
// same file passes once secret-scan-skipped-paths.yaml names it.
func TestSecretScanAcceptsAReviewedSkippedPath(t *testing.T) {
	root := newCoverageFixtureRepo(t)
	trackFile(t, root, "web/package-lock.json", "{}\n")
	writeCoverageRegisters(t, root, nil, []skippedPathEntry{{Path: "web/package-lock.json", Reason: "lockfile"}})
	requireCoveragePasses(t, root)
}

// TestSecretScanFailsOnAnUnlistedBinaryFile pins decision 7: a new
// tracked file with a NUL byte in its first 8,000 bytes fails until
// binary-files.yaml names it, and the report prints the entry to add.
func TestSecretScanFailsOnAnUnlistedBinaryFile(t *testing.T) {
	root := newCoverageFixtureRepo(t)
	trackFile(t, root, "fixtures/blob.dat", nulBody)
	want := entryFor("fixtures/blob.dat", classBinary, nulBody)
	requireScanFails(t, root, "fixtures/blob.dat: tracked binary file", want.SHA256, binaryFilesRegister)
}

// TestSecretScanReadsOnlyTheFirst8000BytesForBinariness pins git's rule: a
// NUL byte past the first 8,000 bytes leaves the file text.
func TestSecretScanReadsOnlyTheFirst8000BytesForBinariness(t *testing.T) {
	root := newCoverageFixtureRepo(t)
	trackFile(t, root, "fixtures/long.txt", strings.Repeat("a", binarySniffLen)+"\x00")
	requireCoveragePasses(t, root)
}

// TestSecretScanFailsOnAnUnlistedArchive pins the archive class: a file
// named with an archive extension fails whatever its content, and so does
// a file that starts with an archive's magic number whatever its name.
func TestSecretScanFailsOnAnUnlistedArchive(t *testing.T) {
	root := newCoverageFixtureRepo(t)
	trackFile(t, root, "fixtures/bundle.zip", "not really a zip\n")
	trackFile(t, root, "fixtures/payload", "\x1f\x8b\x08\x00compressed")
	requireScanFails(t, root, "fixtures/bundle.zip: tracked archive file", "fixtures/payload: tracked archive file")
}

// TestSecretScanTreatsAttributeBinaryAsBinary pins the attribute half of
// git's rule: a text file that .gitattributes marks `binary` or `-diff`
// is binary.
func TestSecretScanTreatsAttributeBinaryAsBinary(t *testing.T) {
	root := newCoverageFixtureRepo(t)
	trackFile(t, root, ".gitattributes", "*.dat binary\n*.raw -diff\n")
	trackFile(t, root, "fixtures/plain.dat", "plain text\n")
	trackFile(t, root, "fixtures/plain.raw", "plain text\n")
	requireScanFails(t, root, "fixtures/plain.dat: tracked binary file", "fixtures/plain.raw: tracked binary file")
}

// TestSecretScanAcceptsAListedBinaryFile pins the passing case: a binary
// file and an archive under a skipped path, each named in
// binary-files.yaml with its size and hash, pass without an entry in the
// skipped-paths register.
func TestSecretScanAcceptsAListedBinaryFile(t *testing.T) {
	root := newCoverageFixtureRepo(t)
	trackFile(t, root, "fixtures/blob.dat", nulBody)
	trackFile(t, root, "docs/logo.png", "\x89PNG\r\n\x1a\n\x00")
	writeCoverageRegisters(t, root, []binaryFileEntry{
		entryFor("fixtures/blob.dat", classBinary, nulBody),
		entryFor("docs/logo.png", classBinary, "\x89PNG\r\n\x1a\n\x00"),
	}, nil)
	requireCoveragePasses(t, root)
}

// TestSecretScanFailsOnAChangedListedBinaryFile pins the hash: a listed
// binary file whose content changed fails until the entry is updated, and
// the report prints the new hash.
func TestSecretScanFailsOnAChangedListedBinaryFile(t *testing.T) {
	root := newCoverageFixtureRepo(t)
	writeCoverageRegisters(t, root, []binaryFileEntry{entryFor("fixtures/blob.dat", classBinary, nulBody)}, nil)
	changed := "header\x00other payload\n"
	trackFile(t, root, "fixtures/blob.dat", changed)
	requireScanFails(t, root, "fixtures/blob.dat: content changed since review", entryFor("", "", changed).SHA256)
}

// TestSecretScanFailsOnAStaleEntry pins that both registers stay exact: an
// entry for an untracked file, for a file that is no longer binary, for a
// text file no pattern skips, and for a binary file named in both
// registers each fail.
func TestSecretScanFailsOnAStaleEntry(t *testing.T) {
	root := newCoverageFixtureRepo(t)
	trackFile(t, root, "fixtures/now-text.dat", "text now\n")
	trackFile(t, root, "assets/icon.png", nulBody)
	writeCoverageRegisters(t, root,
		[]binaryFileEntry{
			entryFor("fixtures/gone.dat", classBinary, nulBody),
			entryFor("fixtures/now-text.dat", classBinary, "text now\n"),
			entryFor("assets/icon.png", classBinary, nulBody),
		},
		[]skippedPathEntry{
			{Path: "README.md", Reason: "not skipped"},
			{Path: "web/yarn.lock", Reason: "not tracked"},
			{Path: "assets/icon.png", Reason: "binary"},
		})
	requireScanFails(t, root,
		"fixtures/gone.dat: "+binaryFilesRegister+" names a file the tree does not track",
		"fixtures/now-text.dat: "+binaryFilesRegister+" names a file that is no longer an archive or binary file",
		"README.md: "+skippedPathsRegister+" names a file no default allowlist pattern skips",
		"web/yarn.lock: "+skippedPathsRegister+" names a file the tree does not track",
		"assets/icon.png: named in both registers")
}

// TestSecretScanFailsOnABrokenRegister pins the fail-closed path: a
// missing register, an unknown field, a malformed hash, an entry with no
// reason, and a duplicate entry each fail rather than read as an empty
// list.
func TestSecretScanFailsOnABrokenRegister(t *testing.T) {
	cases := map[string]struct {
		file, body string
	}{
		"missing binary register":  {binaryFilesRegister, ""},
		"missing skipped register": {skippedPathsRegister, ""},
		"unknown field":            {binaryFilesRegister, "kind: binary-files\nversion: 1\nentries:\n  - path: a\n    class: binary\n    size: 1\n    sha256: " + strings.Repeat("0", 64) + "\n    reason: r\n    approved: true\n"},
		"wrong kind":               {skippedPathsRegister, "kind: binary-files\nversion: 1\nentries: []\n"},
		"malformed hash":           {binaryFilesRegister, "kind: binary-files\nversion: 1\nentries:\n  - {path: a, class: binary, size: 1, sha256: ABC, reason: r}\n"},
		"unknown class":            {binaryFilesRegister, "kind: binary-files\nversion: 1\nentries:\n  - {path: a, class: image, size: 1, sha256: " + strings.Repeat("0", 64) + ", reason: r}\n"},
		"no reason":                {skippedPathsRegister, "kind: secret-scan-skipped-paths\nversion: 1\nentries:\n  - {path: go.sum, reason: ''}\n"},
		"duplicate":                {skippedPathsRegister, "kind: secret-scan-skipped-paths\nversion: 1\nentries:\n  - {path: go.sum, reason: r}\n  - {path: go.sum, reason: r}\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			root := newCoverageFixtureRepo(t)
			target := filepath.Join(root, tc.file)
			if tc.body == "" {
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
			} else {
				writeScopeFile(t, target, tc.body)
			}
			if out, err := runSecretScan(root); err == nil {
				t.Fatalf("secret scan passed with a broken register:\n%s", out)
			}
		})
	}
}

// TestSecretScanCoveragePassesOnTheTree runs the coverage half over this
// checkout. A failure names a tracked archive, binary file, or file under
// a skipped path that lacks a matching reviewed entry: review the file and
// update tests/registers/binary-files.yaml or
// tests/registers/secret-scan-skipped-paths.yaml.
func TestSecretScanCoveragePassesOnTheTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("blocked: precondition not met: git not on PATH")
	}
	root := repoRoot()
	files, err := trackedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := checkSecretScanCoverage(root, files); err != nil {
		t.Fatalf("secret scan coverage of the tree failed: %v\n%s", err, out)
	}
}

// TestSkippedByDefaultAllowlistMatchesFilesAndDirectories pins the path
// matching against examples of the default patterns, including a match on
// a directory above the file and paths that no pattern matches.
func TestSkippedByDefaultAllowlistMatchesFilesAndDirectories(t *testing.T) {
	cases := []struct {
		rel, matched string
	}{
		{".gitleaks.toml", ".gitleaks.toml"},
		{"go.sum", "go.sum"},
		{"tools/go.mod", "tools/go.mod"},
		{"docs/Logo.PNG", "docs/Logo.PNG"},
		{"web/yarn.lock", "web/yarn.lock"},
		{"web/node_modules/a/b.js", "web/node_modules"},
		{"third/vendor/github.com/x/y.go", "third/vendor/github.com"},
		{"py/lib/python3.12/site.py", "py/lib/python3.12"},
		{"x/foo-1.2.dist-info/METADATA", "x/foo-1.2.dist-info"},
		{"data/blob.bin/notes.txt", "data/blob.bin"},
		{"README.md", ""},
		{"cmd/lenny-test/secretscan.go", ""},
		{"docs/vendor.md", ""},
	}
	for _, tc := range cases {
		matched, _, ok := skippedByDefaultAllowlist(tc.rel)
		if ok != (tc.matched != "") || matched != tc.matched {
			t.Errorf("skippedByDefaultAllowlist(%q) = %q, %v; want %q", tc.rel, matched, ok, tc.matched)
		}
	}
}

// TestGitleaksDefaultSkippedPathsMatchThePinnedRelease ties the copied
// patterns to the release the gate image installs and the configuration
// requires. A failure means the pin moved: read the `[allowlist]` paths of
// config/gitleaks.toml at the new release's tag, update
// gitleaksDefaultSkippedPaths and gitleaksDefaultsVersion, and re-run the
// coverage check over the tree.
func TestGitleaksDefaultSkippedPathsMatchThePinnedRelease(t *testing.T) {
	dockerfile, err := os.ReadFile(filepath.Join(repoRoot(), "tests", "gate", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^ARG GITLEAKS_VERSION=(\S+)$`).FindSubmatch(dockerfile)
	if m == nil || string(m[1]) != gitleaksDefaultsVersion {
		t.Errorf("tests/gate/Dockerfile pins gitleaks %q, the copied default allowlist is from %q", m, gitleaksDefaultsVersion)
	}
	config, err := os.ReadFile(filepath.Join(repoRoot(), secretScanConfig))
	if err != nil {
		t.Fatal(err)
	}
	m = regexp.MustCompile(`(?m)^minVersion = "v([^"]+)"$`).FindSubmatch(config)
	if m == nil || string(m[1]) != gitleaksDefaultsVersion {
		t.Errorf("%s names minVersion %q, the copied default allowlist is from %q", secretScanConfig, m, gitleaksDefaultsVersion)
	}
}
