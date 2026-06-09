// SPDX-License-Identifier: MIT

package workspace_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
)

// spec: §7.3 lines 408-409 — ArchiveTree/ExtractTree bundle the session
// workspace and the /sessions session-file tmpfs into one checkpoint and
// replay each to its own root on resume. F-7.3.14.

// writeDir materializes files into a fresh temp dir and returns it.
func writeDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestArchiveTreeRoundTripsWorkspaceAndSessions_spec_7_3_14(t *testing.T) {
	ws := writeDir(t, map[string]string{"a.txt": "alpha", "sub/b.txt": "bravo"})
	sessions := writeDir(t, map[string]string{".session.json": "state", "log/conv.txt": "hello"})

	var buf bytes.Buffer
	n, err := workspace.ArchiveTree([]workspace.NamedRoot{
		{Prefix: workspace.WorkspacePrefix, Root: ws},
		{Prefix: workspace.SessionsPrefix, Root: sessions},
	}, &buf)
	if err != nil {
		t.Fatalf("ArchiveTree: %v", err)
	}
	if n != int64(buf.Len()) {
		t.Errorf("reported %d compressed bytes, archive is %d", n, buf.Len())
	}

	wsOut := t.TempDir()
	sessOut := t.TempDir()
	written, err := workspace.ExtractTree([]workspace.NamedRoot{
		{Prefix: workspace.WorkspacePrefix, Root: wsOut},
		{Prefix: workspace.SessionsPrefix, Root: sessOut},
	}, bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("ExtractTree: %v", err)
	}
	wantBytes := int64(len("alpha") + len("bravo") + len("state") + len("hello"))
	if written != wantBytes {
		t.Errorf("written = %d, want %d", written, wantBytes)
	}
	assertFile(t, filepath.Join(wsOut, "a.txt"), "alpha")
	assertFile(t, filepath.Join(wsOut, "sub/b.txt"), "bravo")
	assertFile(t, filepath.Join(sessOut, ".session.json"), "state")
	assertFile(t, filepath.Join(sessOut, "log/conv.txt"), "hello")
	// The workspace tree must not leak into the sessions root or vice versa.
	if _, err := os.Stat(filepath.Join(wsOut, ".session.json")); !os.IsNotExist(err) {
		t.Errorf("session file leaked into the workspace root")
	}
}

// An empty or absent sessions root contributes no entries; extraction
// leaves the sessions destination empty. F-7.3.14.
func TestArchiveTreeSkipsEmptyAndAbsentRoots_spec_7_3_14(t *testing.T) {
	ws := writeDir(t, map[string]string{"only.txt": "ws"})
	absent := filepath.Join(t.TempDir(), "does-not-exist")

	var buf bytes.Buffer
	for _, sessRoot := range []string{"", absent} {
		buf.Reset()
		if _, err := workspace.ArchiveTree([]workspace.NamedRoot{
			{Prefix: workspace.WorkspacePrefix, Root: ws},
			{Prefix: workspace.SessionsPrefix, Root: sessRoot},
		}, &buf); err != nil {
			t.Fatalf("ArchiveTree (sessRoot=%q): %v", sessRoot, err)
		}
		wsOut := t.TempDir()
		sessOut := t.TempDir()
		if _, err := workspace.ExtractTree([]workspace.NamedRoot{
			{Prefix: workspace.WorkspacePrefix, Root: wsOut},
			{Prefix: workspace.SessionsPrefix, Root: sessOut},
		}, bytes.NewReader(buf.Bytes())); err != nil {
			t.Fatalf("ExtractTree: %v", err)
		}
		assertFile(t, filepath.Join(wsOut, "only.txt"), "ws")
		if entries, _ := os.ReadDir(sessOut); len(entries) != 0 {
			t.Errorf("sessRoot=%q: sessions dest has %d entries, want 0", sessRoot, len(entries))
		}
	}
}

// An entry under a prefix the extractor does not know is skipped, so a
// newer bundle that carries an extra tree restores forward-compatibly.
// F-7.3.14.
func TestExtractTreeSkipsUnknownPrefix_spec_7_3_14(t *testing.T) {
	ws := writeDir(t, map[string]string{"keep.txt": "ws"})
	extra := writeDir(t, map[string]string{"ignored.txt": "future"})

	var buf bytes.Buffer
	if _, err := workspace.ArchiveTree([]workspace.NamedRoot{
		{Prefix: workspace.WorkspacePrefix, Root: ws},
		{Prefix: "futuretree", Root: extra},
	}, &buf); err != nil {
		t.Fatalf("ArchiveTree: %v", err)
	}

	wsOut := t.TempDir()
	// Extractor knows only the workspace prefix.
	written, err := workspace.ExtractTree([]workspace.NamedRoot{
		{Prefix: workspace.WorkspacePrefix, Root: wsOut},
	}, bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("ExtractTree: %v", err)
	}
	if written != int64(len("ws")) {
		t.Errorf("written = %d, want %d (unknown prefix skipped)", written, len("ws"))
	}
	assertFile(t, filepath.Join(wsOut, "keep.txt"), "ws")
}

func TestArchiveTreeRejectsBadPrefix_spec_7_3_14(t *testing.T) {
	ws := writeDir(t, map[string]string{"a.txt": "x"})
	var buf bytes.Buffer
	for _, bad := range []string{"", "a/b"} {
		if _, err := workspace.ArchiveTree([]workspace.NamedRoot{
			{Prefix: bad, Root: ws},
		}, &buf); err == nil {
			t.Errorf("ArchiveTree prefix %q: want error, got nil", bad)
		}
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Errorf("%s = %q, want %q", path, got, want)
	}
}
