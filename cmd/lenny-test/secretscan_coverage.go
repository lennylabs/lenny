// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/mholt/archives"
	"gopkg.in/yaml.v3"
)

// The coverage half of the secret scan holds the committed files whose
// content gitleaks does not read, and the files under the paths its
// default allowlist skips, to reviewed entries (change 0158, decisions 5
// and 7). It implements the merge gate rather than a specification
// behavior, so it carries no spec annotation.
//
// gitleaks v8.30.1 leaves a file unread when its name identifies an
// archive to github.com/mholt/archives, which matches substrings of the
// name such as ".br" or ".s2" (the scan passes no --max-archive-depth,
// because some upload fixtures are archive bombs), or stops reading it at
// a chunk whose type github.com/h2non/filetype gives as an `application`
// MIME type, which it checks while no newline has appeared
// (secretscan_chunks.go). The check calls the same two libraries at the
// versions gitleaks v8.30.1 pins, the same way, and adds git's own rules
// for a binary file: a NUL byte in the first 8,000 bytes, or the `binary`
// attribute set or the `diff` attribute unset. A symbolic link is not
// read either, because its content is a path. A text file that gitleaks
// would skip only because its name matches an archive's is renamed in the
// scan tree so gitleaks reads it, and needs no entry; a file whose content
// carries an archive magic number, or whose archive name goes with binary
// content, is an archive. Two registers under
// tests/registers, which the review rule of .claude/rules/git-workflow.md
// covers, hold the entries:
//
//   - binary-files.yaml names each committed archive, binary file, and
//     symbolic link with its size, SHA-256, and reason. One it does not
//     name fails, and so does a listed file whose content changed.
//   - secret-scan-skipped-paths.yaml names each committed text file under
//     a path the default allowlist skips. The scan renames such a file in
//     its scan tree so gitleaks reads it (secretscan_tree.go), and the
//     register records which files are scanned that way.

// The two registers, relative to the repository root.
const (
	binaryFilesRegister  = "tests/registers/binary-files.yaml"
	skippedPathsRegister = "tests/registers/secret-scan-skipped-paths.yaml"
)

// binarySniffLen is how much of a file git reads to decide binariness.
const binarySniffLen = 8000

// The classes of committed file the binary register holds.
const (
	classArchive = "archive"
	classBinary  = "binary"
	classSymlink = "symlink"
)

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

// trackedFileFacts is what the coverage check learns about one committed
// file.
type trackedFileFacts struct {
	class  string // classArchive, classBinary, classSymlink, or "" for text
	size   int64
	sha256 string
}

// gitleaksIdentifiesArchive reports whether gitleaks v8.30.1 treats a file
// with this path as an archive: archives.Identify on the name alone, with
// no stream, as gitleaks's sources/file.go calls it, returning a format.
func gitleaksIdentifiesArchive(name string) bool {
	format, _, err := archives.Identify(context.Background(), name, nil)
	return err == nil && format != nil
}

// gitleaksReads reports whether gitleaks v8.30.1 reads the content of a
// file it reaches at path rel, and when it does not, why.
func gitleaksReads(rel string, content []byte) (string, bool) {
	if gitleaksIdentifiesArchive(rel) {
		return "its name identifies an archive", false
	}
	if mime, skip := gitleaksMIMESkip(content); skip {
		return "its content has the MIME type " + mime, false
	}
	return "", true
}

// blobFacts classifies one committed file and records its size and
// SHA-256. A symbolic link's content is its target path. A file whose
// name alone makes gitleaks take it for an archive, such as a step digest
// named 0158.S2.md, is text when its content carries no archive magic
// number and is not binary: the scan tree renames it so gitleaks reads
// it (scanTree.place), and it needs no register entry. A file is an
// archive when its content carries an archive magic number, or when its
// name is an archive's and its content is binary.
func blobFacts(rel, mode string, content []byte, attrBinary bool) trackedFileFacts {
	sum := sha256.Sum256(content)
	f := trackedFileFacts{size: int64(len(content)), sha256: hex.EncodeToString(sum[:])}
	if mode == gitModeSymlink {
		f.class = classSymlink
		return f
	}
	head := content[:min(len(content), binarySniffLen)]
	_, mimeSkip := gitleaksMIMESkip(content)
	binary := attrBinary || bytes.IndexByte(head, 0) >= 0 || mimeSkip
	switch {
	case hasArchiveMagic(head):
		f.class = classArchive
	case binary && gitleaksIdentifiesArchive(rel):
		f.class = classArchive
	case binary:
		f.class = classBinary
	}
	return f
}

// textNamedLikeArchive reports whether gitleaks would skip a text file
// only because of its name, so the scan tree renames it.
func textNamedLikeArchive(rel string, f trackedFileFacts) bool {
	return f.class == "" && gitleaksIdentifiesArchive(rel)
}

// hasArchiveMagic reports whether head starts with the magic number of an
// archive or compression format github.com/mholt/archives reads: gzip,
// zip, bzip2, xz, zstd, 7z, RAR, LZ4, lzip, the snappy framing format, or
// tar (its "ustar" marker at offset 257). Brotli and zlib streams carry no
// reliable magic number; a renamed file of either kind is read by gitleaks
// as raw bytes.
func hasArchiveMagic(head []byte) bool {
	magics := [][]byte{
		{0x1f, 0x8b},
		[]byte("PK\x03\x04"),
		[]byte("PK\x05\x06"),
		[]byte("PK\x07\x08"),
		{0xfd, '7', 'z', 'X', 'Z', 0x00},
		{0x28, 0xb5, 0x2f, 0xfd},
		{'7', 'z', 0xbc, 0xaf, 0x27, 0x1c},
		[]byte("Rar!\x1a\x07"),
		{0x04, 0x22, 0x4d, 0x18},
		[]byte("LZIP"),
		{0xff, 0x06, 0x00, 0x00, 's', 'N', 'a', 'P', 'p', 'Y'},
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

// checkSecretScanCoverage holds the committed files gitleaks does not read
// to the two registers, whose committed content it is given (nil when the
// register is not committed). It returns a summary when every such file
// matches its entry and every entry names such a file, and otherwise the
// list of problems, each naming its path and remedy, with an error. A
// register that is missing or does not parse fails, so a lost register
// never passes as an empty one.
func checkSecretScanCoverage(facts map[string]trackedFileFacts, binaryRegister, skippedRegister []byte) (string, error) {
	binaries, err := loadBinaryFilesRegister(binaryRegister)
	if err != nil {
		return "", err
	}
	skipped, err := loadSkippedPathsRegister(skippedRegister)
	if err != nil {
		return "", err
	}
	var problems []string
	problems = append(problems, binaryFileProblems(facts, binaries)...)
	problems = append(problems, skippedPathProblems(facts, binaries, skipped)...)
	if len(problems) > 0 {
		sort.Strings(problems)
		return formatScanCoverageProblems(problems), fmt.Errorf("secret scan coverage: %d problem(s) with the committed files gitleaks does not read", len(problems))
	}
	return fmt.Sprintf("%d archives, binary files, and symbolic links match %s; %d files under skipped paths, scanned under another name, match %s", len(binaries), binaryFilesRegister, len(skipped), skippedPathsRegister), nil
}

// binaryFileProblems compares the committed archives, binary files, and
// symbolic links with binary-files.yaml.
func binaryFileProblems(facts map[string]trackedFileFacts, entries map[string]binaryFileEntry) []string {
	var problems []string
	for rel, f := range facts {
		if f.class == "" {
			continue
		}
		e, listed := entries[rel]
		switch {
		case !listed:
			problems = append(problems, fmt.Sprintf("%s: committed %s that %s does not name; review it and add {path: %s, class: %s, size: %d, sha256: %s, reason: ...}", rel, classNoun(f.class), binaryFilesRegister, rel, f.class, f.size, f.sha256))
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
			problems = append(problems, fmt.Sprintf("%s: %s names a file the committed tree does not hold; remove the entry", rel, binaryFilesRegister))
		case f.class == "":
			problems = append(problems, fmt.Sprintf("%s: %s names a file that is no longer an archive, binary file, or symbolic link, so gitleaks reads it; remove the entry", rel, binaryFilesRegister))
		}
	}
	return problems
}

// classNoun names a class in a problem message.
func classNoun(class string) string {
	switch class {
	case classArchive:
		return "archive"
	case classSymlink:
		return "symbolic link"
	default:
		return "binary file"
	}
}

// skippedPathProblems compares the committed text files under the paths
// the default allowlist skips with secret-scan-skipped-paths.yaml. An
// archive, binary file, or symbolic link under a skipped path is covered
// by its entry in binary-files.yaml, which binaryFileProblems checks.
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
			problems = append(problems, fmt.Sprintf("%s: %s matches the default allowlist pattern %s, so the scan reads this file under another name; review it and add {path: %s, reason: ...} to %s, or move it", rel, where, pattern, rel, skippedPathsRegister))
		}
	}
	for rel := range entries {
		f, tracked := facts[rel]
		_, _, skippedPath := skippedByDefaultAllowlist(rel)
		switch {
		case !tracked:
			problems = append(problems, fmt.Sprintf("%s: %s names a file the committed tree does not hold; remove the entry", rel, skippedPathsRegister))
		case !skippedPath:
			problems = append(problems, fmt.Sprintf("%s: %s names a file no default allowlist pattern skips; remove the entry", rel, skippedPathsRegister))
		case f.class != "":
			if _, inBinaries := binaries[rel]; inBinaries {
				problems = append(problems, fmt.Sprintf("%s: named in both registers; an archive, binary file, or symbolic link keeps its entry in %s alone, so remove it from %s", rel, binaryFilesRegister, skippedPathsRegister))
			}
		}
	}
	return problems
}

// formatScanCoverageProblems lists the problems with the reason they fail.
func formatScanCoverageProblems(problems []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d problem(s) with the committed files gitleaks does not read:\n", len(problems))
	for _, p := range problems {
		fmt.Fprintf(&b, "  %s\n", p)
	}
	b.WriteString("An archive, binary file, or symbolic link is reviewed for secrets by hand and named in " + binaryFilesRegister + "; a text file under a path the default allowlist skips is named in " + skippedPathsRegister + ". Both registers are reviewed before they merge.")
	return b.String()
}

// loadBinaryFilesRegister parses binary-files.yaml and checks each entry's
// fields, keyed by path.
func loadBinaryFilesRegister(body []byte) (map[string]binaryFileEntry, error) {
	reg, err := readScanCoverageRegister[binaryFileEntry](binaryFilesRegister, body, "binary-files")
	if err != nil {
		return nil, err
	}
	entries := map[string]binaryFileEntry{}
	for i, e := range reg.Entries {
		switch {
		case e.Path == "":
			return nil, fmt.Errorf("%s: entry %d has no path", binaryFilesRegister, i+1)
		case e.Class != classArchive && e.Class != classBinary && e.Class != classSymlink:
			return nil, fmt.Errorf("%s: %s: class %q is not %q, %q, or %q", binaryFilesRegister, e.Path, e.Class, classArchive, classBinary, classSymlink)
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

// loadSkippedPathsRegister parses secret-scan-skipped-paths.yaml and
// checks each entry's fields, keyed by path.
func loadSkippedPathsRegister(body []byte) (map[string]skippedPathEntry, error) {
	reg, err := readScanCoverageRegister[skippedPathEntry](skippedPathsRegister, body, "secret-scan-skipped-paths")
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

// readScanCoverageRegister parses a register's committed content,
// refusing an absent register, unknown fields, a kind other than wantKind,
// and a version other than 1.
func readScanCoverageRegister[E any](name string, body []byte, wantKind string) (scanCoverageRegister[E], error) {
	var reg scanCoverageRegister[E]
	if body == nil {
		return reg, fmt.Errorf("secret scan coverage register %s: not in the committed tree", name)
	}
	dec := yaml.NewDecoder(bytes.NewReader(body))
	dec.KnownFields(true)
	if err := dec.Decode(&reg); err != nil {
		return reg, fmt.Errorf("secret scan coverage register %s: %w", name, err)
	}
	if reg.Kind != wantKind || reg.Version != 1 {
		return reg, fmt.Errorf("secret scan coverage register %s: kind %q version %d, want kind %q version 1", name, reg.Kind, reg.Version, wantKind)
	}
	return reg, nil
}
