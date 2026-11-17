// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// The coverage half of the secret scan names every tracked file that
// gitleaks does not read and holds each one to a reviewed entry, so no
// tracked file escapes the scan unseen (change 0158, decisions 5 and 7).
// It implements the merge gate rather than a specification behavior, so
// it carries no spec annotation.
//
// gitleaks leaves three kinds of file unread: archives, which it opens
// only when --max-archive-depth is passed, and the check does not pass it
// because the upload fixtures include archive bombs; files it detects as
// binary; and files under the paths its default global allowlist skips
// (secretscan_skipped.go). Two registers under tests/registers, which the
// review rule of .claude/rules/git-workflow.md covers, hold the entries:
//
//   - binary-files.yaml names each tracked archive and binary file with
//     its size, SHA-256, and reason. A tracked archive or binary file it
//     does not name fails, and so does a listed file whose content changed.
//   - secret-scan-skipped-paths.yaml names each tracked text file under a
//     skipped path with its reason. A binary file under a skipped path
//     belongs in binary-files.yaml alone, so each file has one entry.
//
// Binariness follows git: a file is binary when its first 8,000 bytes hold
// a NUL byte, or when git check-attr reports the `binary` attribute set or
// the `diff` attribute unset. An attribute never makes a file text. A file
// is an archive when its name ends in a known archive extension or its
// first bytes carry an archive's magic number.

// The two registers, relative to the repository root.
const (
	binaryFilesRegister  = "tests/registers/binary-files.yaml"
	skippedPathsRegister = "tests/registers/secret-scan-skipped-paths.yaml"
)

// binarySniffLen is how much of a file git reads to decide binariness.
const binarySniffLen = 8000

// The classes of file gitleaks does not read by content.
const (
	classArchive = "archive"
	classBinary  = "binary"
)

// archiveExtension matches the file names of the archive and compression
// formats in common use, case-insensitively.
var archiveExtension = regexp.MustCompile(`(?i)\.(?:zip|jar|war|ear|aar|apk|ipa|tar|tgz|taz|gz|tbz2?|bz2|txz|xz|tzst|zst|lz|lz4|lzma|tlz|z|7z|rar|cab|cpio|ar|deb|rpm|whl|egg|nupkg|gem|iso|dmg|xpi|crx|vsix|br|sz)$`)

// binaryFileEntry is one entry of binary-files.yaml.
type binaryFileEntry struct {
	Path   string `yaml:"path"`
	Class  string `yaml:"class"`
	Size   int64  `yaml:"size"`
	SHA256 string `yaml:"sha256"`
	Reason string `yaml:"reason"`
}

// skippedPathEntry is one entry of secret-scan-skipped-paths.yaml.
type skippedPathEntry struct {
	Path   string `yaml:"path"`
	Reason string `yaml:"reason"`
}

// scanCoverageRegister is the document layout both registers share.
type scanCoverageRegister[E any] struct {
	Kind    string `yaml:"kind"`
	Version int    `yaml:"version"`
	Entries []E    `yaml:"entries"`
}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// trackedFileFacts is what the coverage check learns about one tracked
// regular file.
type trackedFileFacts struct {
	class  string // classArchive, classBinary, or "" for a text file
	size   int64
	sha256 string
}

// checkSecretScanCoverage holds the tracked files gitleaks does not read
// to the two registers under root. It returns a summary when every such
// file matches its entry and every entry names such a file, and otherwise
// the list of problems, each naming its path and remedy, with an error. A
// register that is missing or does not parse fails, so a lost register
// never passes as an empty one.
func checkSecretScanCoverage(root string, files []string) (string, error) {
	binaries, err := loadBinaryFilesRegister(filepath.Join(root, binaryFilesRegister))
	if err != nil {
		return "", err
	}
	skipped, err := loadSkippedPathsRegister(filepath.Join(root, skippedPathsRegister))
	if err != nil {
		return "", err
	}
	facts, err := classifyTrackedFiles(root, files)
	if err != nil {
		return "", err
	}
	var problems []string
	problems = append(problems, binaryFileProblems(facts, binaries)...)
	problems = append(problems, skippedPathProblems(facts, binaries, skipped)...)
	if len(problems) > 0 {
		sort.Strings(problems)
		return formatScanCoverageProblems(problems), fmt.Errorf("secret scan coverage: %d tracked file(s) gitleaks does not read lack a matching reviewed entry", len(problems))
	}
	return fmt.Sprintf("%d archive and binary files match %s; %d files under skipped paths match %s", len(binaries), binaryFilesRegister, len(skipped), skippedPathsRegister), nil
}

// binaryFileProblems compares the tracked archives and binary files with
// binary-files.yaml.
func binaryFileProblems(facts map[string]trackedFileFacts, entries map[string]binaryFileEntry) []string {
	var problems []string
	for rel, f := range facts {
		if f.class == "" {
			continue
		}
		e, listed := entries[rel]
		switch {
		case !listed:
			problems = append(problems, fmt.Sprintf("%s: tracked %s file that %s does not name; review it and add {path: %s, class: %s, size: %d, sha256: %s, reason: ...}", rel, f.class, binaryFilesRegister, rel, f.class, f.size, f.sha256))
		case e.SHA256 != f.sha256 || e.Size != f.size:
			problems = append(problems, fmt.Sprintf("%s: content changed since review (listed size %d, sha256 %s; now size %d, sha256 %s); review the new content and update the entry in %s", rel, e.Size, e.SHA256, f.size, f.sha256, binaryFilesRegister))
		case e.Class != f.class:
			problems = append(problems, fmt.Sprintf("%s: listed as class %s, detected as %s; correct the entry in %s", rel, e.Class, f.class, binaryFilesRegister))
		}
	}
	for rel := range entries {
		f, tracked := facts[rel]
		switch {
		case !tracked:
			problems = append(problems, fmt.Sprintf("%s: %s names a file the tree does not track; remove the entry", rel, binaryFilesRegister))
		case f.class == "":
			problems = append(problems, fmt.Sprintf("%s: %s names a file that is no longer an archive or binary file, so gitleaks reads it; remove the entry", rel, binaryFilesRegister))
		}
	}
	return problems
}

// skippedPathProblems compares the tracked text files under the paths the
// default allowlist skips with secret-scan-skipped-paths.yaml. An archive
// or binary file under a skipped path is covered by its entry in
// binary-files.yaml, which binaryFileProblems checks.
func skippedPathProblems(facts map[string]trackedFileFacts, binaries map[string]binaryFileEntry, entries map[string]skippedPathEntry) []string {
	var problems []string
	for rel, f := range facts {
		matched, pattern, skippedPath := skippedByDefaultAllowlist(rel)
		if !skippedPath || f.class != "" {
			continue
		}
		if _, listed := entries[rel]; !listed {
			where := "its path"
			if matched != rel {
				where = "its directory " + matched
			}
			problems = append(problems, fmt.Sprintf("%s: gitleaks skips this tracked file because %s matches the default allowlist pattern %s; move it, or review it and add {path: %s, reason: ...} to %s", rel, where, pattern, rel, skippedPathsRegister))
		}
	}
	for rel := range entries {
		f, tracked := facts[rel]
		_, _, skippedPath := skippedByDefaultAllowlist(rel)
		switch {
		case !tracked:
			problems = append(problems, fmt.Sprintf("%s: %s names a file the tree does not track; remove the entry", rel, skippedPathsRegister))
		case !skippedPath:
			problems = append(problems, fmt.Sprintf("%s: %s names a file no default allowlist pattern skips, so gitleaks reads it; remove the entry", rel, skippedPathsRegister))
		case f.class != "":
			if _, inBinaries := binaries[rel]; inBinaries {
				problems = append(problems, fmt.Sprintf("%s: named in both registers; an archive or binary file keeps its entry in %s alone, so remove it from %s", rel, binaryFilesRegister, skippedPathsRegister))
			}
		}
	}
	return problems
}

// formatScanCoverageProblems lists the problems with the reason they fail.
func formatScanCoverageProblems(problems []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "gitleaks does not read %d tracked file(s) that lack a matching reviewed entry:\n", len(problems))
	for _, p := range problems {
		fmt.Fprintf(&b, "  %s\n", p)
	}
	b.WriteString("A file gitleaks does not read is reviewed for secrets by hand and named in " + binaryFilesRegister + " (archives and binary files) or " + skippedPathsRegister + " (text files under a path the default allowlist skips), with the reason; both are reviewed before they merge.")
	return b.String()
}

// classifyTrackedFiles reads each tracked regular file under root and
// returns its class, size, and SHA-256, keyed by its root-relative path.
// It skips the same paths copyTrackedFiles does: a path the working tree
// no longer holds, a symbolic link, and a submodule.
func classifyTrackedFiles(root string, files []string) (map[string]trackedFileFacts, error) {
	attrBinary, err := attributeBinaryFiles(root, files)
	if err != nil {
		return nil, err
	}
	facts := make(map[string]trackedFileFacts, len(files))
	for _, rel := range files {
		src := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(src)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("secret scan coverage: %w", err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		f, err := readFileFacts(src)
		if err != nil {
			return nil, fmt.Errorf("secret scan coverage: read %s: %w", rel, err)
		}
		if archiveExtension.MatchString(rel) || f.class == classArchive {
			f.class = classArchive
		} else if attrBinary[rel] {
			f.class = classBinary
		}
		facts[rel] = f
	}
	return facts, nil
}

// readFileFacts hashes the file at src and classifies it by its first
// bytes: an archive by its magic number, otherwise binary when the first
// binarySniffLen bytes hold a NUL byte, otherwise text.
func readFileFacts(src string) (trackedFileFacts, error) {
	in, err := os.Open(src)
	if err != nil {
		return trackedFileFacts{}, err
	}
	defer in.Close()
	h := sha256.New()
	head := make([]byte, binarySniffLen)
	n, err := io.ReadFull(in, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return trackedFileFacts{}, err
	}
	head = head[:n]
	h.Write(head)
	rest, err := io.Copy(h, in)
	if err != nil {
		return trackedFileFacts{}, err
	}
	f := trackedFileFacts{size: int64(n) + rest, sha256: hex.EncodeToString(h.Sum(nil))}
	switch {
	case hasArchiveMagic(head):
		f.class = classArchive
	case bytes.IndexByte(head, 0) >= 0:
		f.class = classBinary
	}
	return f, nil
}

// hasArchiveMagic reports whether head starts with the magic number of a
// compressed or archive format gitleaks would identify as an archive:
// gzip, zip, bzip2, xz, zstd, 7z, or tar (its "ustar" marker at offset
// 257).
func hasArchiveMagic(head []byte) bool {
	magics := [][]byte{
		{0x1f, 0x8b},
		[]byte("PK\x03\x04"),
		[]byte("PK\x05\x06"),
		{0xfd, '7', 'z', 'X', 'Z', 0x00},
		{0x28, 0xb5, 0x2f, 0xfd},
		{'7', 'z', 0xbc, 0xaf, 0x27, 0x1c},
	}
	for _, m := range magics {
		if bytes.HasPrefix(head, m) {
			return true
		}
	}
	if len(head) >= 10 && bytes.HasPrefix(head, []byte("BZh")) && head[3] >= '1' && head[3] <= '9' && bytes.Equal(head[4:10], []byte{0x31, 0x41, 0x59, 0x26, 0x53, 0x59}) {
		return true
	}
	return len(head) >= 262 && bytes.Equal(head[257:262], []byte("ustar"))
}

// attributeBinaryFiles returns the files git's attributes mark as binary:
// the `binary` macro set, or the `diff` attribute unset, which is how git
// diff decides to print "Binary files differ" without reading the file.
func attributeBinaryFiles(root string, files []string) (map[string]bool, error) {
	cmd := exec.Command("git", "check-attr", "-z", "--stdin", "binary", "diff")
	cmd.Dir = root
	var in bytes.Buffer
	for _, f := range files {
		in.WriteString(f)
		in.WriteByte(0)
	}
	cmd.Stdin = &in
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("secret scan coverage: git check-attr: %w", err)
	}
	fields := bytes.Split(out, []byte{0})
	binary := map[string]bool{}
	for i := 0; i+2 < len(fields); i += 3 {
		rel, attr, value := string(fields[i]), string(fields[i+1]), string(fields[i+2])
		if (attr == "binary" && value == "set") || (attr == "diff" && value == "unset") {
			binary[rel] = true
		}
	}
	return binary, nil
}

// loadBinaryFilesRegister reads binary-files.yaml and checks each entry's
// fields, keyed by path.
func loadBinaryFilesRegister(file string) (map[string]binaryFileEntry, error) {
	reg, err := readScanCoverageRegister[binaryFileEntry](file, "binary-files")
	if err != nil {
		return nil, err
	}
	entries := map[string]binaryFileEntry{}
	for i, e := range reg.Entries {
		switch {
		case e.Path == "":
			return nil, fmt.Errorf("%s: entry %d has no path", binaryFilesRegister, i+1)
		case e.Class != classArchive && e.Class != classBinary:
			return nil, fmt.Errorf("%s: %s: class %q is neither %q nor %q", binaryFilesRegister, e.Path, e.Class, classArchive, classBinary)
		case !sha256Hex.MatchString(e.SHA256):
			return nil, fmt.Errorf("%s: %s: sha256 is not 64 lowercase hexadecimal digits", binaryFilesRegister, e.Path)
		case e.Size < 0:
			return nil, fmt.Errorf("%s: %s: negative size", binaryFilesRegister, e.Path)
		case strings.TrimSpace(e.Reason) == "":
			return nil, fmt.Errorf("%s: %s: no reason", binaryFilesRegister, e.Path)
		}
		if _, dup := entries[e.Path]; dup {
			return nil, fmt.Errorf("%s: %s is listed twice", binaryFilesRegister, e.Path)
		}
		entries[e.Path] = e
	}
	return entries, nil
}

// loadSkippedPathsRegister reads secret-scan-skipped-paths.yaml and checks
// each entry's fields, keyed by path.
func loadSkippedPathsRegister(file string) (map[string]skippedPathEntry, error) {
	reg, err := readScanCoverageRegister[skippedPathEntry](file, "secret-scan-skipped-paths")
	if err != nil {
		return nil, err
	}
	entries := map[string]skippedPathEntry{}
	for i, e := range reg.Entries {
		switch {
		case e.Path == "":
			return nil, fmt.Errorf("%s: entry %d has no path", skippedPathsRegister, i+1)
		case strings.TrimSpace(e.Reason) == "":
			return nil, fmt.Errorf("%s: %s: no reason", skippedPathsRegister, e.Path)
		}
		if _, dup := entries[e.Path]; dup {
			return nil, fmt.Errorf("%s: %s is listed twice", skippedPathsRegister, e.Path)
		}
		entries[e.Path] = e
	}
	return entries, nil
}

// readScanCoverageRegister parses a register, refusing unknown fields, a
// kind other than wantKind, and a version other than 1.
func readScanCoverageRegister[E any](file, wantKind string) (scanCoverageRegister[E], error) {
	var reg scanCoverageRegister[E]
	body, err := os.ReadFile(file)
	if err != nil {
		return reg, fmt.Errorf("secret scan coverage register: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(body))
	dec.KnownFields(true)
	if err := dec.Decode(&reg); err != nil {
		return reg, fmt.Errorf("secret scan coverage register %s: %w", file, err)
	}
	if reg.Kind != wantKind || reg.Version != 1 {
		return reg, fmt.Errorf("secret scan coverage register %s: kind %q version %d, want kind %q version 1", file, reg.Kind, reg.Version, wantKind)
	}
	return reg, nil
}
