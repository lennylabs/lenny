// SPDX-License-Identifier: MIT

package workspace_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
)

// spec: §4.4 / §7.1 / §14 — Extract restores a workspace from a
// gzip-tar, contained within the workspace root.

// tarEntry describes one archive entry for buildArchive.
type tarEntry struct {
	name     string
	typeflag byte
	body     string // file content, or symlink target for TypeSymlink
}

// buildArchive assembles a gzip-tar from entries.
func buildArchive(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Typeflag: e.typeflag, Mode: 0o644}
		switch e.typeflag {
		case tar.TypeDir:
			hdr.Mode = 0o755
		case tar.TypeSymlink:
			hdr.Linkname = e.body
		case tar.TypeReg:
			hdr.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %q: %v", e.name, err)
		}
		if e.typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatalf("write body %q: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func TestExtractRoundTripsAnArchive(t *testing.T) {
	// Build a source workspace, Archive it, Extract it elsewhere, and
	// confirm the tree — including a within-root symlink — survives.
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("world"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}
	if err := os.Symlink("sub/b.txt", filepath.Join(src, "link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	var archived bytes.Buffer
	if _, err := workspace.Archive(src, &archived); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	dst := t.TempDir()
	n, err := workspace.Extract(dst, &archived)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if n != 10 {
		t.Errorf("Extract reported %d uncompressed bytes, want 10 (hello + world)", n)
	}

	if got, _ := os.ReadFile(filepath.Join(dst, "a.txt")); string(got) != "hello" {
		t.Errorf("a.txt = %q, want hello", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dst, "sub", "b.txt")); string(got) != "world" {
		t.Errorf("sub/b.txt = %q, want world", got)
	}
	target, err := os.Readlink(filepath.Join(dst, "link"))
	if err != nil {
		t.Fatalf("the symlink was not restored: %v", err)
	}
	if target != "sub/b.txt" {
		t.Errorf("restored symlink target = %q, want sub/b.txt", target)
	}
}

func TestExtractRejectsPathTraversal(t *testing.T) {
	archive := buildArchive(t, tarEntry{name: "../escape.txt", typeflag: tar.TypeReg, body: "pwned"})
	dst := t.TempDir()
	if _, err := workspace.Extract(dst, bytes.NewReader(archive)); err == nil {
		t.Fatal("Extract accepted an archive entry that escapes the workspace root")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dst), "escape.txt")); err == nil {
		t.Error("the traversal entry was written outside the workspace root")
	}
}

func TestExtractRejectsAbsoluteSymlinkTarget(t *testing.T) {
	archive := buildArchive(t, tarEntry{name: "escape", typeflag: tar.TypeSymlink, body: "/etc/passwd"})
	if _, err := workspace.Extract(t.TempDir(), bytes.NewReader(archive)); err == nil {
		t.Error("Extract restored a symlink pointing outside the workspace root")
	}
}

func TestExtractRejectsEscapingRelativeSymlink(t *testing.T) {
	archive := buildArchive(t, tarEntry{name: "escape", typeflag: tar.TypeSymlink, body: "../../../etc"})
	if _, err := workspace.Extract(t.TempDir(), bytes.NewReader(archive)); err == nil {
		t.Error("Extract restored a relative symlink that escapes the workspace root")
	}
}

// TestExtractRejectsSymlinkToForbiddenMount covers F-13.4.4: §7.4 line
// 458 / §13.4 line 665 — even for snapshot restore, a symlink whose
// target is /proc, /sys, /dev, or /run/lenny is rejected.
func TestExtractRejectsSymlinkToForbiddenMount(t *testing.T) {
	for _, target := range []string{"/proc/self/environ", "/sys/kernel", "/dev/sda1", "/run/lenny/credentials"} {
		t.Run(target, func(t *testing.T) {
			archive := buildArchive(t, tarEntry{name: "link", typeflag: tar.TypeSymlink, body: target})
			if _, err := workspace.Extract(t.TempDir(), bytes.NewReader(archive)); err == nil {
				t.Errorf("Extract restored a symlink to %q (forbidden by §13.4 line 665)", target)
			}
		})
	}
}

func TestExtractRejectsUnsupportedEntryType(t *testing.T) {
	archive := buildArchive(t, tarEntry{name: "dev", typeflag: tar.TypeFifo})
	if _, err := workspace.Extract(t.TempDir(), bytes.NewReader(archive)); err == nil {
		t.Error("Extract accepted an unsupported (fifo) archive entry")
	}
}

func TestExtractRejectsMalformedArchive(t *testing.T) {
	if _, err := workspace.Extract(t.TempDir(), bytes.NewReader([]byte("not a gzip archive"))); err == nil {
		t.Error("Extract accepted a non-gzip archive")
	}
}
