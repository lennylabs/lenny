// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// The secret scan reads the committed tree at HEAD rather than the working
// tree or the index, so a skip-worktree or assume-unchanged entry, a path
// that collides with another by case, or an index path missing from disk
// cannot make the scanned content differ from what is committed. Both
// halves of the check read the same blobs: the coverage half classifies
// them and the gitleaks half scans the copies this file writes. It
// implements the merge gate rather than a specification behavior, so it
// carries no spec annotation.

// The git file modes of a committed tree entry the check distinguishes.
const (
	gitModeSymlink   = "120000"
	gitModeSubmodule = "160000"
)

// renamedScanDir is the directory of the scan tree that holds the files
// placed under a name other than their repository path: a file under a
// path the default gitleaks allowlist skips, so that gitleaks reads it; a
// root .gitleaksignore, so that gitleaks does not apply it; and a file
// whose path collides by case with another on a case-insensitive file
// system. A committed path under this directory fails the check, so the
// two never meet.
const renamedScanDir = ".lenny-secretscan-renamed"

// maxRenamedNameLen bounds the length of a flattened name, below the
// 255-byte file name limit of the common file systems. A longer name is
// replaced by the SHA-256 of the path.
const maxRenamedNameLen = 240

// treeBlob is one entry of the committed tree at HEAD.
type treeBlob struct {
	path string
	mode string
	oid  string
	size int64
}

// committedTree lists every entry of the tree at HEAD under root,
// recursively, with its mode, object ID, and size.
func committedTree(root string) ([]treeBlob, error) {
	cmd := exec.Command("git", "ls-tree", "-r", "-z", "-l", "--full-tree", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list the committed tree at HEAD: %w", err)
	}
	var blobs []treeBlob
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		meta, rel, ok := strings.Cut(string(rec), "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 4 {
			return nil, fmt.Errorf("list the committed tree at HEAD: unexpected entry %q", rec)
		}
		b := treeBlob{path: rel, mode: fields[0], oid: fields[2]}
		if fields[3] != "-" {
			if b.size, err = strconv.ParseInt(fields[3], 10, 64); err != nil {
				return nil, fmt.Errorf("list the committed tree at HEAD: size of %s: %w", rel, err)
			}
		}
		blobs = append(blobs, b)
	}
	return blobs, nil
}

// readBlobs streams the content of each listed blob from one
// `git cat-file --batch` process and passes it to fn, in order. A
// submodule entry, which names a commit of another repository, is passed
// over.
func readBlobs(root string, blobs []treeBlob, fn func(treeBlob, []byte) error) error {
	cmd := exec.Command("git", "cat-file", "--batch")
	cmd.Dir = root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("read committed blobs: %w", err)
	}
	var wanted []treeBlob
	for _, b := range blobs {
		if b.mode != gitModeSubmodule {
			wanted = append(wanted, b)
		}
	}
	go func() {
		w := bufio.NewWriter(stdin)
		for _, b := range wanted {
			fmt.Fprintln(w, b.oid)
		}
		w.Flush()
		stdin.Close()
	}()
	r := bufio.NewReaderSize(stdout, 1<<16)
	var readErr error
	for _, b := range wanted {
		content, err := readOneBlob(r, b)
		if err != nil {
			readErr = err
			break
		}
		if err := fn(b, content); err != nil {
			readErr = err
			break
		}
	}
	if readErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return readErr
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("read committed blobs: %w", err)
	}
	return nil
}

// readOneBlob reads one `git cat-file --batch` record: a header line
// "<oid> <type> <size>", the content, and a newline.
func readOneBlob(r *bufio.Reader, b treeBlob) ([]byte, error) {
	header, err := r.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read committed blob %s: %w", b.path, err)
	}
	fields := strings.Fields(header)
	if len(fields) != 3 || fields[0] != b.oid || fields[1] != "blob" {
		return nil, fmt.Errorf("read committed blob %s: unexpected header %q", b.path, strings.TrimSpace(header))
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("read committed blob %s: %w", b.path, err)
	}
	content := make([]byte, size)
	if _, err := io.ReadFull(r, content); err != nil {
		return nil, fmt.Errorf("read committed blob %s: %w", b.path, err)
	}
	if nl, err := r.ReadByte(); err != nil || nl != '\n' {
		return nil, fmt.Errorf("read committed blob %s: missing record terminator", b.path)
	}
	return content, nil
}

// attributeBinaryFiles returns the committed files git's attributes mark
// as binary: the `binary` macro set, or the `diff` attribute unset, which
// is how git diff decides to print "Binary files differ" without reading
// the file. The attributes are read from the .gitattributes files of the
// tree at HEAD, through a temporary index that holds that tree, because
// `git check-attr --source` needs git 2.40 and the gate image ships an
// older git.
func attributeBinaryFiles(root, work string, files []string) (map[string]bool, error) {
	env := append(os.Environ(), "GIT_INDEX_FILE="+filepath.Join(work, "attr-index"))
	readTree := exec.Command("git", "read-tree", "HEAD")
	readTree.Dir = root
	readTree.Env = env
	if out, err := readTree.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("secret scan coverage: git read-tree HEAD: %w: %s", err, out)
	}
	cmd := exec.Command("git", "check-attr", "--cached", "-z", "--stdin", "binary", "diff")
	cmd.Dir = root
	cmd.Env = env
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

// scanTree is the temporary tree gitleaks scans, with the repository path
// of each file placed under another name.
type scanTree struct {
	dir     string
	renamed map[string]string // scan-tree path -> repository path
	folded  map[string]bool   // lower-cased scan-tree paths placed so far
}

func newScanTree(dir string) *scanTree {
	return &scanTree{dir: dir, renamed: map[string]string{}, folded: map[string]bool{}}
}

// place writes one committed regular file into the scan tree. A file
// gitleaks would skip by its path, a root .gitleaksignore, and a file
// whose path collides by case with one already placed go under
// renamedScanDir with a flattened name gitleaks reads; every other file
// keeps its repository path, so the path rules of the configuration match
// it.
func (t *scanTree) place(rel string, content []byte) error {
	target := rel
	if _, _, skipped := skippedByDefaultAllowlist(rel); skipped || rel == ".gitleaksignore" || t.folded[strings.ToLower(rel)] {
		var err error
		if target, err = renamedScanPath(rel); err != nil {
			return err
		}
		t.renamed[target] = rel
	}
	t.folded[strings.ToLower(target)] = true
	dst := filepath.Join(t.dir, filepath.FromSlash(target))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("secret scan: place %s: %w", rel, err)
	}
	if err := os.WriteFile(dst, content, 0o600); err != nil {
		return fmt.Errorf("secret scan: place %s: %w", rel, err)
	}
	return nil
}

// repositoryPath maps a path in a gitleaks report back to the repository
// path of the file it scanned.
func (t *scanTree) repositoryPath(reported string) string {
	reported = strings.TrimPrefix(filepath.ToSlash(reported), "./")
	if rel, ok := t.renamed[reported]; ok {
		return rel
	}
	return reported
}

// renamedScanPath returns the scan-tree path of a file placed under
// renamedScanDir: the repository path with "%", "/", and "." escaped as
// "%25", "%2F", and "%2E", then ".scan", so the name holds one dot and no
// directory. It refuses a name that a default allowlist path pattern
// still matches or that gitleaks would identify as an archive, so a
// renamed file is always one gitleaks reads by path and by name.
func renamedScanPath(rel string) (string, error) {
	name := strings.NewReplacer("%", "%25", "/", "%2F", ".", "%2E").Replace(rel) + ".scan"
	if len(name) > maxRenamedNameLen {
		sum := sha256.Sum256([]byte(rel))
		name = "sha256-" + hex.EncodeToString(sum[:]) + ".scan"
	}
	target := path.Join(renamedScanDir, name)
	if _, pattern, skipped := skippedByDefaultAllowlist(target); skipped {
		return "", fmt.Errorf("secret scan: the renamed path %s of %s matches the default allowlist pattern %s", target, rel, pattern)
	}
	if gitleaksIdentifiesArchive(target) {
		return "", fmt.Errorf("secret scan: gitleaks would identify the renamed path %s of %s as an archive", target, rel)
	}
	return target, nil
}
