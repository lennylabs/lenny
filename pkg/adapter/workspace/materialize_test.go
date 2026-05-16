// SPDX-License-Identifier: MIT

package workspace_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

func source(typ, path, content, mode string) *adapterv1.WorkspaceSource {
	return &adapterv1.WorkspaceSource{Type: typ, Path: path, Content: content, Mode: mode}
}

func TestMaterializeWritesInlineFile(t *testing.T) {
	root := t.TempDir()
	err := workspace.Materialize(root, []*adapterv1.WorkspaceSource{
		source("inlineFile", "CLAUDE.md", "# project notes", "640"),
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read materialized file: %v", err)
	}
	if string(got) != "# project notes" {
		t.Errorf("file content = %q, want %q", got, "# project notes")
	}
	info, err := os.Stat(filepath.Join(root, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("file mode = %o, want 640", info.Mode().Perm())
	}
}

func TestMaterializeInlineFileDefaultsMode(t *testing.T) {
	root := t.TempDir()
	if err := workspace.Materialize(root, []*adapterv1.WorkspaceSource{
		source("inlineFile", "a.txt", "x", ""),
	}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	info, _ := os.Stat(filepath.Join(root, "a.txt"))
	if info.Mode().Perm() != 0o644 {
		t.Errorf("default file mode = %o, want 644", info.Mode().Perm())
	}
}

func TestMaterializeCreatesNestedParents(t *testing.T) {
	root := t.TempDir()
	if err := workspace.Materialize(root, []*adapterv1.WorkspaceSource{
		source("inlineFile", "src/pkg/main.go", "package main", "644"),
	}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "src", "pkg", "main.go")); err != nil {
		t.Errorf("nested file was not materialized: %v", err)
	}
}

func TestMaterializeMakesDirectory(t *testing.T) {
	root := t.TempDir()
	if err := workspace.Materialize(root, []*adapterv1.WorkspaceSource{
		source("mkdir", "build", "", "750"),
	}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	info, err := os.Stat(filepath.Join(root, "build"))
	if err != nil {
		t.Fatalf("stat directory: %v", err)
	}
	if !info.IsDir() {
		t.Error("mkdir source did not produce a directory")
	}
	if info.Mode().Perm() != 0o750 {
		t.Errorf("directory mode = %o, want 750", info.Mode().Perm())
	}
}

func TestMaterializeAppliesSourcesInOrder(t *testing.T) {
	root := t.TempDir()
	if err := workspace.Materialize(root, []*adapterv1.WorkspaceSource{
		source("mkdir", "docs", "", "755"),
		source("inlineFile", "docs/readme.md", "hi", "644"),
	}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "readme.md")); err != nil {
		t.Errorf("ordered sources did not materialize: %v", err)
	}
}

func TestMaterializeRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	err := workspace.Materialize(root, []*adapterv1.WorkspaceSource{
		source("inlineFile", "../escape.txt", "x", "644"),
	})
	if err == nil {
		t.Fatal("Materialize should reject a path that escapes the workspace root")
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(root), "escape.txt")); statErr == nil {
		t.Error("a traversal path wrote a file outside the workspace root")
	}
}

func TestMaterializeRejectsAbsolutePath(t *testing.T) {
	root := t.TempDir()
	err := workspace.Materialize(root, []*adapterv1.WorkspaceSource{
		source("inlineFile", "/etc/passwd", "x", "644"),
	})
	if err == nil {
		t.Fatal("Materialize should reject an absolute source path")
	}
}

func TestMaterializeRejectsEmptyPath(t *testing.T) {
	root := t.TempDir()
	if err := workspace.Materialize(root, []*adapterv1.WorkspaceSource{
		source("inlineFile", "", "x", "644"),
	}); err == nil {
		t.Fatal("Materialize should reject an empty source path")
	}
}

func TestMaterializeRejectsSetuidMode(t *testing.T) {
	root := t.TempDir()
	if err := workspace.Materialize(root, []*adapterv1.WorkspaceSource{
		source("inlineFile", "a.sh", "x", "4755"),
	}); err == nil {
		t.Fatal("Materialize should reject a setuid mode")
	}
}

func TestMaterializeRejectsInvalidMode(t *testing.T) {
	root := t.TempDir()
	if err := workspace.Materialize(root, []*adapterv1.WorkspaceSource{
		source("inlineFile", "a.txt", "x", "not-octal"),
	}); err == nil {
		t.Fatal("Materialize should reject a non-octal mode string")
	}
}

func TestMaterializeRejectsUnsupportedSourceTypes(t *testing.T) {
	root := t.TempDir()
	for _, typ := range []string{"uploadFile", "uploadArchive", "gitClone"} {
		err := workspace.Materialize(root, []*adapterv1.WorkspaceSource{
			source(typ, "x", "", ""),
		})
		if !errors.Is(err, workspace.ErrSourceUnsupported) {
			t.Errorf("source type %q: error = %v, want ErrSourceUnsupported", typ, err)
		}
	}
}

func TestMaterializeRejectsUnknownSourceType(t *testing.T) {
	root := t.TempDir()
	err := workspace.Materialize(root, []*adapterv1.WorkspaceSource{
		source("teleport", "x", "", ""),
	})
	if !errors.Is(err, workspace.ErrUnknownSourceType) {
		t.Errorf("error = %v, want ErrUnknownSourceType", err)
	}
}
