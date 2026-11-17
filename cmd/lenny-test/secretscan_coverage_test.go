// SPDX-License-Identifier: MIT

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The coverage half of the secret scan implements the merge gate rather
// than a specification behavior, so these tests carry no spec annotation.
// Most need git but not gitleaks: runSecretScan checks coverage before it
// looks for gitleaks, so a coverage failure fails on any host.

// configSkippedEntry is the entry a fixture carries for its copy of
// .gitleaks.toml, which the default allowlist skips by name.
var configSkippedEntry = skippedPathEntry{Path: secretScanConfig, Reason: "the scan's configuration"}

// writeCoverageRegisters writes the two coverage registers under root with
// the given entries, plus the entry for .gitleaks.toml when root holds
// one. It does not commit them.
func writeCoverageRegisters(t *testing.T, root string, binaries []binaryFileEntry, skipped []skippedPathEntry) {
	t.Helper()
	if binaries == nil {
		binaries = []binaryFileEntry{}
	}
	if _, err := os.Stat(filepath.Join(root, secretScanConfig)); err == nil {
		skipped = append([]skippedPathEntry{configSkippedEntry}, skipped...)
	}
	if skipped == nil {
		skipped = []skippedPathEntry{}
	}
	bin, err := yaml.Marshal(scanCoverageRegister[binaryFileEntry]{Kind: "binary-files", Version: 1, Entries: binaries})
	if err != nil {
		t.Fatal(err)
	}
	skip, err := yaml.Marshal(scanCoverageRegister[skippedPathEntry]{Kind: "secret-scan-skipped-paths", Version: 1, Entries: skipped})
	if err != nil {
		t.Fatal(err)
	}
	writeScopeFile(t, filepath.Join(root, binaryFilesRegister), string(bin))
	writeScopeFile(t, filepath.Join(root, skippedPathsRegister), string(skip))
}

// coverageOf runs the coverage half over the committed tree of root.
func coverageOf(t *testing.T, root string) (string, error) {
	t.Helper()
	scan, err := readCommittedScan(root, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return checkSecretScanCoverage(scan.facts, scan.binReg, scan.skipReg)
}

// entryFor builds the binary-files.yaml entry that matches body.
func entryFor(rel, class, body string) binaryFileEntry {
	sum := sha256.Sum256([]byte(body))
	return binaryFileEntry{Path: rel, Class: class, Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:]), Reason: "fixture"}
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

// TestSecretScanFailsOnAFileUnderASkippedPathWithoutAnEntry pins decision
// 5: a committed text file whose path matches a default allowlist pattern
// fails until the skipped-paths register names it, and the report names
// the file and the pattern.
func TestSecretScanFailsOnAFileUnderASkippedPathWithoutAnEntry(t *testing.T) {
	root := newSecretScanFixtureRepo(t)
	commitFile(t, root, "web/package-lock.json", "{}\n")
	requireScanFails(t, root, "web/package-lock.json", "package-lock", skippedPathsRegister)
}

// TestSecretScanFailsOnAFileInASkippedDirectoryWithoutAnEntry pins that a
// directory counts: gitleaks does not enter a directory whose path
// matches, so a file under it needs an entry even when its own name
// matches no pattern, and the report names the directory.
func TestSecretScanFailsOnAFileInASkippedDirectoryWithoutAnEntry(t *testing.T) {
	root := newSecretScanFixtureRepo(t)
	writeScopeFile(t, filepath.Join(root, "tools/node_modules/pkg/index.js"), "module.exports = 1\n")
	commitFile(t, root, "data/blobs.bin/notes.txt", "notes\n")
	requireScanFails(t, root,
		"tools/node_modules/pkg/index.js", "its directory tools/node_modules",
		"data/blobs.bin/notes.txt", "its directory data/blobs.bin")
}

// TestSecretScanReadsFilesUnderSkippedPaths pins that a file under a
// skipped path is scanned once its entry exists: a secret in a lockfile
// or under node_modules fails, and the report names the repository path
// rather than the renamed one.
func TestSecretScanReadsFilesUnderSkippedPaths(t *testing.T) {
	requireGitleaks(t)
	root := newSecretScanFixtureRepo(t)
	writeScopeFile(t, filepath.Join(root, "web/package-lock.json"), "{\n"+plantedLine+"}\n")
	writeScopeFile(t, filepath.Join(root, "tools/node_modules/pkg/index.js"), plantedLine)
	writeCoverageRegisters(t, root, nil, []skippedPathEntry{
		{Path: "web/package-lock.json", Reason: "lockfile"},
		{Path: "tools/node_modules/pkg/index.js", Reason: "vendored"},
	})
	commitAll(t, root)
	requireCoveragePasses(t, root)
	requireScanFails(t, root, "web/package-lock.json:2", "tools/node_modules/pkg/index.js:1")
}

// TestSecretScanFailsOnAnUnlistedBinaryFile pins decision 7: a new
// committed file with a NUL byte in its first 8,000 bytes fails until
// binary-files.yaml names it, and the report prints the entry to add.
func TestSecretScanFailsOnAnUnlistedBinaryFile(t *testing.T) {
	root := newSecretScanFixtureRepo(t)
	commitFile(t, root, "fixtures/blob.dat", nulBody)
	want := entryFor("fixtures/blob.dat", classBinary, nulBody)
	requireScanFails(t, root, "fixtures/blob.dat: committed binary file", want.SHA256, binaryFilesRegister)
}

// TestSecretScanReadsOnlyTheFirst8000BytesForBinariness pins git's rule: a
// NUL byte past the first 8,000 bytes leaves the file text.
func TestSecretScanReadsOnlyTheFirst8000BytesForBinariness(t *testing.T) {
	root := newSecretScanFixtureRepo(t)
	commitFile(t, root, "fixtures/long.txt", strings.Repeat("a", binarySniffLen)+"\x00")
	requireCoveragePasses(t, root)
}

// TestSecretScanFailsOnAnUnlistedArchiveOrApplicationFile pins gitleaks's
// own rules: a name gitleaks identifies as an archive, including a
// substring match, and content whose first chunk has an `application` MIME
// type each need an entry.
func TestSecretScanFailsOnAnUnlistedArchiveOrApplicationFile(t *testing.T) {
	root := newSecretScanFixtureRepo(t)
	writeScopeFile(t, filepath.Join(root, "fixtures/bundle.zip"), "not really a zip\n")
	writeScopeFile(t, filepath.Join(root, "config/kafka.brokers.yaml"), "brokers: []\n")
	commitFile(t, root, "fixtures/report.txt", "%PDF-1.4\ntext\n")
	requireScanFails(t, root,
		"fixtures/bundle.zip: committed archive",
		"config/kafka.brokers.yaml: committed archive",
		"fixtures/report.txt: committed binary file")
}

// TestSecretScanTreatsAttributeBinaryAsBinary pins the attribute half of
// git's rule, read from the committed .gitattributes: a text file marked
// `binary` or `-diff` is binary, and an uncommitted .gitattributes has no
// effect.
func TestSecretScanTreatsAttributeBinaryAsBinary(t *testing.T) {
	root := newSecretScanFixtureRepo(t)
	writeScopeFile(t, filepath.Join(root, "fixtures/plain.dat"), "plain text\n")
	writeScopeFile(t, filepath.Join(root, "fixtures/plain.raw"), "plain text\n")
	commitAll(t, root)
	writeScopeFile(t, filepath.Join(root, ".gitattributes"), "*.dat binary\n*.raw -diff\n")
	requireCoveragePasses(t, root)
	commitAll(t, root)
	requireScanFails(t, root, "fixtures/plain.dat: committed binary file", "fixtures/plain.raw: committed binary file")
}

// TestSecretScanFailsOnAnUnlistedSymlink pins that a committed symbolic
// link needs an entry, because nothing reads its target text, and passes
// once binary-files.yaml names it with the hash of its target.
func TestSecretScanFailsOnAnUnlistedSymlink(t *testing.T) {
	root := newSecretScanFixtureRepo(t)
	if err := os.Symlink("../README.md", filepath.Join(root, "docs-link")); err != nil {
		t.Skipf("blocked: precondition not met: cannot create a symbolic link: %v", err)
	}
	commitAll(t, root)
	requireScanFails(t, root, "docs-link: committed symbolic link")
	writeCoverageRegisters(t, root, []binaryFileEntry{entryFor("docs-link", classSymlink, "../README.md")}, nil)
	commitAll(t, root)
	requireCoveragePasses(t, root)
}

// TestSecretScanAcceptsAListedBinaryFile pins the passing case: a binary
// file and an image under a skipped path, each named in binary-files.yaml
// with its size and hash, pass without an entry in the skipped-paths
// register.
func TestSecretScanAcceptsAListedBinaryFile(t *testing.T) {
	root := newSecretScanFixtureRepo(t)
	png := "\x89PNG\r\n\x1a\n\x00"
	writeScopeFile(t, filepath.Join(root, "fixtures/blob.dat"), nulBody)
	writeScopeFile(t, filepath.Join(root, "docs/logo.png"), png)
	writeCoverageRegisters(t, root, []binaryFileEntry{
		entryFor("fixtures/blob.dat", classBinary, nulBody),
		entryFor("docs/logo.png", classBinary, png),
	}, nil)
	commitAll(t, root)
	requireCoveragePasses(t, root)
}

// TestSecretScanFailsOnAChangedListedBinaryFile pins the hash: a listed
// binary file whose committed content changed fails until the entry is
// updated, and the report prints the new hash.
func TestSecretScanFailsOnAChangedListedBinaryFile(t *testing.T) {
	root := newSecretScanFixtureRepo(t)
	writeCoverageRegisters(t, root, []binaryFileEntry{entryFor("fixtures/blob.dat", classBinary, nulBody)}, nil)
	changed := "header\x00other payload\n"
	commitFile(t, root, "fixtures/blob.dat", changed)
	requireScanFails(t, root, "fixtures/blob.dat: content changed since review", entryFor("", "", changed).SHA256)
}

// TestSecretScanFailsOnAStaleEntry pins that both registers stay exact: an
// entry for a file the commit does not hold, for a file that is no longer
// binary, for a text file no pattern skips, and for a binary file named in
// both registers each fail.
func TestSecretScanFailsOnAStaleEntry(t *testing.T) {
	root := newSecretScanFixtureRepo(t)
	writeScopeFile(t, filepath.Join(root, "fixtures/now-text.dat"), "text now\n")
	writeScopeFile(t, filepath.Join(root, "assets/icon.png"), nulBody)
	writeCoverageRegisters(t, root,
		[]binaryFileEntry{
			entryFor("fixtures/gone.dat", classBinary, nulBody),
			entryFor("fixtures/now-text.dat", classBinary, "text now\n"),
			entryFor("assets/icon.png", classBinary, nulBody),
		},
		[]skippedPathEntry{
			{Path: "README.md", Reason: "not skipped"},
			{Path: "web/yarn.lock", Reason: "not committed"},
			{Path: "assets/icon.png", Reason: "binary"},
		})
	commitAll(t, root)
	requireScanFails(t, root,
		"fixtures/gone.dat: "+binaryFilesRegister+" names a file the committed tree does not hold",
		"fixtures/now-text.dat: "+binaryFilesRegister+" names a file that is no longer an archive, binary file, or symbolic link",
		"README.md: "+skippedPathsRegister+" names a file no default allowlist pattern skips",
		"web/yarn.lock: "+skippedPathsRegister+" names a file the committed tree does not hold",
		"assets/icon.png: named in both registers")
}

// TestSecretScanFailsOnABrokenRegister pins the fail-closed path: a
// missing register, an unknown field, a malformed hash, an entry with no
// reason, and a duplicate entry each fail rather than read as an empty
// list. Only the committed register counts.
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
			root := newSecretScanFixtureRepo(t)
			target := filepath.Join(root, tc.file)
			if tc.body == "" {
				gitIn(t, root, "rm", "-q", tc.file)
			} else {
				writeScopeFile(t, target, tc.body)
			}
			commitAll(t, root)
			if out, err := runSecretScan(root); err == nil {
				t.Fatalf("secret scan passed with a broken register:\n%s", out)
			}
		})
	}
}

// TestSecretScanCoveragePassesOnTheTree runs the coverage half over this
// checkout's HEAD. A failure names a committed archive, binary file,
// symbolic link, or file under a skipped path that lacks a matching
// reviewed entry: review the file and update
// tests/registers/binary-files.yaml or
// tests/registers/secret-scan-skipped-paths.yaml.
func TestSecretScanCoveragePassesOnTheTree(t *testing.T) {
	requireGit(t)
	if out, err := coverageOf(t, repoRoot()); err != nil {
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

// TestRenamedScanCandidatesAreReadByGitleaks pins the renaming of a file
// under a skipped path: each candidate matches no default pattern and is
// not an archive name; a file skipped by a directory keeps its own name,
// a file skipped by its own name keeps its extension, and a long path
// stays under the file name limit.
func TestRenamedScanCandidatesAreReadByGitleaks(t *testing.T) {
	for _, rel := range []string{"go.sum", ".gitleaks.toml", "web/node_modules/a/b.js", "Database.refactorlog", "vendor/ruby/x", "pnpm-lock.yaml", strings.Repeat("deep/", 60) + "go.mod"} {
		got, err := renamedScanCandidates(rel)
		if err != nil {
			t.Errorf("renamedScanCandidates(%q): %v", rel, err)
			continue
		}
		for _, c := range got {
			if _, _, skipped := skippedByDefaultAllowlist(c); skipped || gitleaksIdentifiesArchive(c) || len(path.Base(c)) > 255 {
				t.Errorf("renamedScanCandidates(%q) holds %q, which gitleaks would not read", rel, c)
			}
		}
	}
	first := map[string]string{
		"go.sum":                  renamedScanDir + "/go.scan.sum",
		".gitleaks.toml":          renamedScanDir + "/%2Egitleaks.scan.toml",
		"web/pnpm-lock.yaml":      renamedScanDir + "/web%2Fpnpm-lock.scan.yaml",
		"web/node_modules/a/b.js": renamedScanDir + "/web%2Fnode_modules%2Fa/b.js",
		"Notes.txt":               renamedScanDir + "/%2E/Notes.txt",
	}
	for rel, want := range first {
		if got, _ := renamedScanCandidates(rel); len(got) == 0 || got[0] != want {
			t.Errorf("renamedScanCandidates(%q) = %q, want %q first", rel, got, want)
		}
	}
}

// k8sSecretYAML is a Kubernetes Secret manifest the default
// kubernetes-secret-yaml rule reports in a file whose name ends in .yaml.
// The value is assembled at run time, so this file never holds it whole.
var k8sSecretYAML = "apiVersion: v1\nkind: Secret\nmetadata:\n  name: db\ndata:\n  password: " + "c2VjcmV0" + "dmFsdWUxMjM0NQ==\n"

// TestSecretScanAppliesPathScopedRulesToRenamedFiles pins that a renamed
// file keeps the name or extension a path-scoped default rule needs: a
// Kubernetes Secret in a .yaml file under node_modules and in a lockfile
// named .yaml are both reported by kubernetes-secret-yaml at their
// repository paths.
func TestSecretScanAppliesPathScopedRulesToRenamedFiles(t *testing.T) {
	requireGitleaks(t)
	root := newSecretScanFixtureRepo(t)
	writeScopeFile(t, filepath.Join(root, "deploy/node_modules/chart/secret.yaml"), k8sSecretYAML)
	writeScopeFile(t, filepath.Join(root, "web/pnpm-lock.yaml"), k8sSecretYAML)
	writeCoverageRegisters(t, root, nil, []skippedPathEntry{
		{Path: "deploy/node_modules/chart/secret.yaml", Reason: "vendored"},
		{Path: "web/pnpm-lock.yaml", Reason: "lockfile"},
	})
	commitAll(t, root)
	out, err := runSecretScan(root)
	if err == nil {
		t.Fatalf("secret scan passed:\n%s", out)
	}
	for _, want := range []string{"deploy/node_modules/chart/secret.yaml", "web/pnpm-lock.yaml"} {
		found := false
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, want+":") && strings.Contains(line, "kubernetes-secret-yaml") {
				found = true
			}
		}
		if !found {
			t.Errorf("no kubernetes-secret-yaml finding at %s:\n%s", want, out)
		}
	}
}

// TestSecretScanRefusesAPathOutsideTheTree pins the path check: a crafted
// tree entry named "..", or a directory named ".." holding a file, fails
// the check before anything is written, so it cannot place a file outside
// the scan tree.
func TestSecretScanRefusesAPathOutsideTheTree(t *testing.T) {
	for _, name := range []string{"..", "nested"} {
		t.Run(name, func(t *testing.T) {
			root := newSecretScanFixtureRepo(t)
			blob := gitOut(t, root, "", "hash-object", "-w", "--stdin")
			listing := gitOut(t, root, "", "ls-tree", "HEAD")
			entry := "100644 blob " + blob + "\t..\n"
			if name == "nested" {
				inner := gitOut(t, root, "100644 blob "+blob+"\tevil\n", "mktree")
				entry = "040000 tree " + inner + "\t..\n"
			}
			tree := gitOut(t, root, listing+"\n"+entry, "mktree")
			commit := gitOut(t, root, "", "commit-tree", tree, "-p", "HEAD", "-m", "crafted")
			gitIn(t, root, "update-ref", "HEAD", commit)
			out, err := runSecretScan(root)
			if err == nil || !strings.Contains(err.Error(), "not a plain relative path") {
				t.Fatalf("scan of a tree with a .. entry = %v, want a path refusal:\n%s", err, out)
			}
		})
	}
	for _, rel := range []string{"..", "../x", "a/../b", "./a", "a//b", "/abs", "a/"} {
		if committedPathIsLocal(rel) {
			t.Errorf("committedPathIsLocal(%q) = true", rel)
		}
	}
	if !committedPathIsLocal("a/b.txt") {
		t.Error("committedPathIsLocal(a/b.txt) = false")
	}
}

// gitOut runs git under root with stdin and returns its trimmed output.
func gitOut(t *testing.T, root, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=alice", "-c", "user.email=alice@acme.com"}, args...)...)
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// TestSecretScanAllowsOnlyTheListedElidedTokens pins the documentation
// entry of .gitleaks.toml: an elided example token it lists passes, and
// another value with "..." in it at the same path fails.
func TestSecretScanAllowsOnlyTheListedElidedTokens(t *testing.T) {
	requireGitleaks(t)
	root := newSecretScanFixtureRepo(t)
	// The key name and the values are assembled at run time, so this file,
	// which the scan of the tree reads, holds no line the rule matches.
	page := func(token string) string {
		return "```json\n{\n  \"upload" + "Token\": \"" + token + "\"\n}\n```\n"
	}
	commitFile(t, root, "docs/api/mcp.md", page("sess_"+"01J5K9....1712345678.a1b2c3..."))
	requireScanPasses(t, root)
	commitFile(t, root, "docs/api/mcp.md", page("sess_"+"q7ZkM2xWr4TnV6pB.1712345678.c9d8e7f6a5b4..."))
	requireScanFails(t, root, "docs/api/mcp.md:3", "generic-api-key")
}

// TestSecretScanClassifiesFilesAsGitleaksReadsThem runs the pinned
// gitleaks over sample files, each holding a planted key, and requires the
// check's rule for which files gitleaks reads to agree with which files
// gitleaks reports. The samples cover archive names matched by substring,
// content whose first bytes carry an `application` file signature, a
// signature in a later chunk before any newline, and a long line with
// none.
func TestSecretScanClassifiesFilesAsGitleaksReadsThem(t *testing.T) {
	requireGitleaks(t)
	samples := map[string]string{
		"notes.brand.md":  "# notes\n",
		"x.tar.md":        "# not a tar\n",
		"w.s2.yaml":       "a: 1\n",
		"magic-rtf.txt":   "{\\rtf1 text\n",
		"magic-ps.txt":    "%!PS-Adobe-3.0\n",
		"magic-pdf.txt":   "%PDF-1.4\n",
		"magic-mz.txt":    "MZ text\n",
		"ordinary.txt":    "ordinary text\n",
		"with-nul.txt":    "header\x00\n",
		"kafka.conf.yaml": "brokers: 1\n",
		// No newline in the first chunk or its read-ahead, so gitleaks
		// checks the next chunk's type, finds a PE header, and stops.
		"late-mz.txt": strings.Repeat("A", 125000) + "MZ ",
		// The same, after a newline, so gitleaks checks no later chunk.
		"late-mz-after-newline.txt": "x\n" + strings.Repeat("A", 125000) + "MZ ",
		// A long single-line JSON file with no file signature, read in
		// full, as changes/0152-user-git-identity/evidence/gh-history/out/mutations2.json is.
		"long-line.json": "{\"items\":[" + strings.Repeat("{\"name\":\"value\"},", 20000) + "{}]} ",
	}
	dir := t.TempDir()
	for name, head := range samples {
		writeScopeFile(t, filepath.Join(dir, name), head+plantedLine)
	}
	config := filepath.Join(t.TempDir(), "gitleaks.toml")
	writeScopeFile(t, config, "[extend]\nuseDefault = true\n")
	report := filepath.Join(t.TempDir(), "report.json")
	cmd := exec.Command("gitleaks", "dir", ".", "--config", config, "--no-banner", "--redact",
		"--exit-code", "0", "--report-format", "json", "--report-path", report)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gitleaks: %v\n%s", err, out)
	}
	body, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var findings []secretScanFinding
	if err := json.Unmarshal(body, &findings); err != nil {
		t.Fatal(err)
	}
	reported := map[string]bool{}
	for _, f := range findings {
		reported[path.Base(filepath.ToSlash(f.File))] = true
	}
	var names []string
	for name := range samples {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		why, reads := gitleaksReads(name, []byte(samples[name]+plantedLine))
		if reads != reported[name] {
			t.Errorf("%s: the check says gitleaks reads it = %v (%s), gitleaks reported a finding = %v", name, reads, why, reported[name])
		}
	}
	if !reported["ordinary.txt"] || reported["x.tar.md"] || reported["late-mz.txt"] || !reported["long-line.json"] {
		t.Errorf("the samples did not exercise both outcomes: reported %v", reported)
	}
}

// TestGitleaksDefaultSkippedPathsMatchThePinnedRelease ties the copied
// patterns to the release the gate image installs and the configuration
// requires. A failure means the pin moved: read the `[allowlist]` paths of
// config/gitleaks.toml at the new release's tag, update
// gitleaksDefaultSkippedPaths and gitleaksDefaultsVersion, move the
// github.com/mholt/archives and github.com/h2non/filetype requirements to
// the versions that release's go.mod pins, and re-run the coverage check
// over the tree.
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
	gomod, err := os.ReadFile(filepath.Join(repoRoot(), "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for mod, version := range gitleaksClassifierModules {
		if !regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(mod) + ` ` + regexp.QuoteMeta(version) + `(\s|$)`).Match(gomod) {
			t.Errorf("go.mod does not require %s %s, the version gitleaks %s pins", mod, version, gitleaksDefaultsVersion)
		}
	}
}
