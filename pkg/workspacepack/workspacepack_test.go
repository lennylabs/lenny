// SPDX-License-Identifier: MIT

package workspacepack

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// archiveEntries extracts a packed archive into a name→content map, with
// directory entries recorded as a nil value under their trailing-slash
// name.
func archiveEntries(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		if hdr.Typeflag == tar.TypeDir {
			out[hdr.Name] = nil
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read entry %q: %v", hdr.Name, err)
		}
		out[hdr.Name] = b
	}
	return out
}

func sortedNames(m map[string][]byte) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", p, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %q: %v", p, err)
		}
	}
}

// TestPackIncludesRegularFiles_spec_26_2 confirms Pack archives the
// workspace's regular files with workspace-relative paths and verbatim
// content. spec: §26.2 lines 95-114.
func TestPackIncludesRegularFiles_spec_26_2(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"main.go":     "package main",
		"pkg/util.go": "package pkg",
		"README.md":   "# hi",
	})
	res, err := Pack(dir)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if res.Files != 3 {
		t.Errorf("Files = %d, want 3", res.Files)
	}
	entries := archiveEntries(t, res.Data)
	if string(entries["main.go"]) != "package main" {
		t.Errorf("main.go content = %q", entries["main.go"])
	}
	if string(entries["pkg/util.go"]) != "package pkg" {
		t.Errorf("pkg/util.go content = %q", entries["pkg/util.go"])
	}
}

// TestPackExcludesGitDir_spec_26_2 confirms the always-on .git exclusion
// keeps version-control metadata out of the uploadArchive (gitClone is the
// §26.2 path for history). spec: §26.2 line 119.
func TestPackExcludesGitDir_spec_26_2(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"app.py":             "print(1)",
		".git/config":        "[core]",
		".git/objects/ab/cd": "blob",
	})
	res, err := Pack(dir)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	for name := range archiveEntries(t, res.Data) {
		if strings.HasPrefix(name, ".git") {
			t.Errorf("archive leaked git metadata: %q", name)
		}
	}
	if res.Files != 1 {
		t.Errorf("Files = %d, want 1", res.Files)
	}
}

// TestPackHonorsLennyignore_spec_26_2 confirms .lennyignore patterns are
// applied and take precedence over .gitignore. spec: §26.2 line 114.
func TestPackHonorsLennyignore_spec_26_2(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"keep.txt":        "keep",
		"build/out.bin":   "bin",
		"logs/app.log":    "log",
		"nested/deep.log": "deep",
		".lennyignore":    "build/\n*.log\n",
		".gitignore":      "keep.txt\n", // must be ignored when .lennyignore present
	})
	res, err := Pack(dir)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if res.IgnoreFile != ".lennyignore" {
		t.Errorf("IgnoreFile = %q, want .lennyignore", res.IgnoreFile)
	}
	entries := archiveEntries(t, res.Data)
	names := sortedNames(entries)
	// keep.txt survives (.gitignore not consulted); build/ and *.log dropped.
	mustHave := map[string]bool{"keep.txt": true, ".lennyignore": true, ".gitignore": true}
	for _, n := range names {
		if n == "build/out.bin" || n == "logs/app.log" || n == "nested/deep.log" {
			t.Errorf("ignored entry present: %q", n)
		}
	}
	for n := range mustHave {
		if _, ok := entries[n]; !ok {
			t.Errorf("expected entry missing: %q (have %v)", n, names)
		}
	}
}

// TestPackFallsBackToGitignore_spec_26_2 confirms .gitignore is applied
// when no .lennyignore is present. spec: §26.2 line 114.
func TestPackFallsBackToGitignore_spec_26_2(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"src/main.c": "int main",
		"a.o":        "obj",
		".gitignore": "*.o\n",
	})
	res, err := Pack(dir)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if res.IgnoreFile != ".gitignore" {
		t.Errorf("IgnoreFile = %q, want .gitignore", res.IgnoreFile)
	}
	if _, ok := archiveEntries(t, res.Data)["a.o"]; ok {
		t.Error("*.o should be excluded by .gitignore")
	}
}

// TestPackNegationReincludes_spec_26_2 confirms a later `!` rule
// re-includes a file an earlier rule excluded. spec: §26.2 line 114.
func TestPackNegationReincludes_spec_26_2(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a.log":         "a",
		"important.log": "keep me",
		".lennyignore":  "*.log\n!important.log\n",
	})
	res, err := Pack(dir)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	entries := archiveEntries(t, res.Data)
	if _, ok := entries["a.log"]; ok {
		t.Error("a.log should be ignored")
	}
	if _, ok := entries["important.log"]; !ok {
		t.Error("important.log should be re-included by negation")
	}
}

// TestPackAnchoredPattern_spec_26_2 confirms a leading-slash pattern
// anchors to the workspace root and does not match the same name nested
// deeper. spec: §26.2 line 114.
func TestPackAnchoredPattern_spec_26_2(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"dist/bundle.js":     "root dist",
		"web/dist/bundle.js": "nested dist",
		".lennyignore":       "/dist/\n",
	})
	res, err := Pack(dir)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	entries := archiveEntries(t, res.Data)
	if _, ok := entries["dist/bundle.js"]; ok {
		t.Error("root dist/ should be excluded by anchored /dist/")
	}
	if _, ok := entries["web/dist/bundle.js"]; !ok {
		t.Error("nested web/dist/ should NOT match anchored /dist/")
	}
}

// TestPackSkipsSymlinks_spec_26_2 confirms non-regular entries are omitted
// rather than aborting the pack. spec: §26.2 line 114; §7.4 upload safety.
func TestPackSkipsSymlinks_spec_26_2(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"real.txt": "data"})
	if err := os.Symlink("real.txt", filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	res, err := Pack(dir)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	entries := archiveEntries(t, res.Data)
	if _, ok := entries["link.txt"]; ok {
		t.Error("symlink should be skipped")
	}
	if _, ok := entries["real.txt"]; !ok {
		t.Error("regular file should be present")
	}
}

// TestPackEmptyDirAndNoIgnore_spec_26_2 confirms directories survive and
// an absent ignore file leaves IgnoreFile empty. spec: §26.2 line 114.
func TestPackEmptyDirAndNoIgnore_spec_26_2(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"f.txt": "x"})
	if err := os.MkdirAll(filepath.Join(dir, "emptydir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	res, err := Pack(dir)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if res.IgnoreFile != "" {
		t.Errorf("IgnoreFile = %q, want empty", res.IgnoreFile)
	}
	if _, ok := archiveEntries(t, res.Data)["emptydir/"]; !ok {
		t.Error("empty directory should be present as a dir entry")
	}
}

// TestPackRejectsNonDirectory_spec_26_2 confirms Pack errors on a missing
// or non-directory path. spec: §26.2 line 114.
func TestPackRejectsNonDirectory_spec_26_2(t *testing.T) {
	if _, err := Pack(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Error("Pack of a missing path should error")
	}
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Pack(f); err == nil {
		t.Error("Pack of a file should error")
	}
}

// TestPackSanitizesHeaders_spec_26_2 confirms host uid/gid identity is
// stripped from archive headers. spec: §26.2 line 114.
func TestPackSanitizesHeaders_spec_26_2(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"f.txt": "x"})
	res, err := Pack(dir)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	gz, _ := gzip.NewReader(bytes.NewReader(res.Data))
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		if hdr.Uid != 0 || hdr.Gid != 0 || hdr.Uname != "" || hdr.Gname != "" {
			t.Errorf("header %q leaks identity: uid=%d gid=%d uname=%q gname=%q",
				hdr.Name, hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname)
		}
	}
}
