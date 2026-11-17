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

	"github.com/h2non/filetype"
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
// because some upload fixtures are archive bombs), or when the first chunk
// of its content has an `application` MIME type under
// github.com/h2non/filetype. The check calls the same two libraries at the
// versions gitleaks v8.30.1 pins, the same way, and adds git's own rules
// for a binary file: a NUL byte in the first 8,000 bytes, or the `binary`
// attribute set or the `diff` attribute unset. A symbolic link is not
// read either, because its content is a path. Two registers under
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
//
// gitleaks re-checks the MIME type of a later chunk while no newline has
// appeared since the start of the file; the check reads the first chunk
// alone, so a file whose first 100 kB hold no newline is a gap.

// The two registers, relative to the repository root.
const (
	binaryFilesRegister  = "tests/registers/binary-files.yaml"
	skippedPathsRegister = "tests/registers/secret-scan-skipped-paths.yaml"
)

// binarySniffLen is how much of a file git reads to decide binariness.
const binarySniffLen = 8000

// gitleaksChunkLen is the size of the first chunk gitleaks v8.30.1 reads
// from a file and passes to filetype.Match (defaultBufferSize in its
// sources/file.go).
const gitleaksChunkLen = 100 * 1000

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

// gitleaksMIMESkip reports whether gitleaks v8.30.1 skips content as
// binary: filetype.Match on its first chunk returns a MIME type whose type
// is `application`. It returns that MIME value.
func gitleaksMIMESkip(content []byte) (string, bool) {
	if len(content) == 0 {
		return "", false
	}
	head := content[:min(len(content), gitleaksChunkLen)]
	kind, err := filetype.Match(head)
	if err != nil || kind.MIME.Type != "application" {
		return "", false
	}
	return kind.MIME.Value, true
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
// SHA-256. A symbolic link's content is its target path.
func blobFacts(rel, mode string, content []byte, attrBinary bool) trackedFileFacts {
	sum := sha256.Sum256(content)
	f := trackedFileFacts{size: int64(len(content)), sha256: hex.EncodeToString(sum[:])}
	head := content[:min(len(content), binarySniffLen)]
	switch {
	case mode == gitModeSymlink:
		f.class = classSymlink
	case gitleaksIdentifiesArchive(rel):
		f.class = classArchive
	case attrBinary || bytes.IndexByte(head, 0) >= 0:
		f.class = classBinary
	default:
		if _, skip := gitleaksMIMESkip(content); skip {
			f.class = classBinary
		}
	}
	return f
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
