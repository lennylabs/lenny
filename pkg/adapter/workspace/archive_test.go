// SPDX-License-Identifier: MIT

package workspace_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
)

// spec: §4.4 / §7.1 — Archive snapshots a workspace directory into the
// gzip-tar a checkpoint or seal-and-export stores.

// archiveEntry is one decoded tar entry.
type archiveEntry struct {
	typeflag byte
	linkname string
	content  string
}

// readArchive decodes a gzip-tar into a name-keyed entry map.
func readArchive(t *testing.T, data []byte) map[string]archiveEntry {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	out := map[string]archiveEntry{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read entry %q: %v", hdr.Name, err)
		}
		out[hdr.Name] = archiveEntry{
			typeflag: hdr.Typeflag,
			linkname: hdr.Linkname,
			content:  string(body),
		}
	}
	return out
}

func TestArchiveCapturesFilesAndDirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "b.txt"), []byte("world"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}

	var buf bytes.Buffer
	n, err := workspace.Archive(root, &buf)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if n <= 0 || n != int64(buf.Len()) {
		t.Errorf("byte count = %d, want the compressed length %d", n, buf.Len())
	}

	entries := readArchive(t, buf.Bytes())
	if e, ok := entries["a.txt"]; !ok || e.content != "hello" {
		t.Errorf("a.txt entry = %+v, want content hello", e)
	}
	if e, ok := entries["sub/b.txt"]; !ok || e.content != "world" {
		t.Errorf("sub/b.txt entry = %+v, want content world", e)
	}
	if e, ok := entries["sub/"]; !ok || e.typeflag != tar.TypeDir {
		t.Errorf("sub/ entry = %+v, want a directory entry", e)
	}
}

func TestArchiveRecordsSymlinkWithoutFollowingIt(t *testing.T) {
	root := t.TempDir()
	// A symlink pointing outside the workspace must be archived as a
	// symlink entry — never as the target's content.
	if err := os.Symlink("/etc/hostname", filepath.Join(root, "escape")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	var buf bytes.Buffer
	if _, err := workspace.Archive(root, &buf); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	entries := readArchive(t, buf.Bytes())
	e, ok := entries["escape"]
	if !ok {
		t.Fatal("the symlink was dropped from the archive")
	}
	if e.typeflag != tar.TypeSymlink {
		t.Errorf("typeflag = %d, want TypeSymlink — Archive must not follow the link", e.typeflag)
	}
	if e.linkname != "/etc/hostname" {
		t.Errorf("linkname = %q, want the verbatim target", e.linkname)
	}
	if e.content != "" {
		t.Error("the symlink entry carries content — Archive followed the link out of the workspace")
	}
}

func TestArchiveEmptyWorkspace(t *testing.T) {
	var buf bytes.Buffer
	n, err := workspace.Archive(t.TempDir(), &buf)
	if err != nil {
		t.Fatalf("Archive of an empty workspace: %v", err)
	}
	if n <= 0 {
		t.Errorf("byte count = %d, want a non-empty (gzip+tar framing) archive", n)
	}
	if len(readArchive(t, buf.Bytes())) != 0 {
		t.Error("an empty workspace produced tar entries")
	}
}

func TestArchiveMissingRootErrors(t *testing.T) {
	var buf bytes.Buffer
	if _, err := workspace.Archive(filepath.Join(t.TempDir(), "no-such-dir"), &buf); err == nil {
		t.Error("Archive of a missing root returned no error")
	}
}
