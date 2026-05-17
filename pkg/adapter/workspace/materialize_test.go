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
	err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
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
	if err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
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
	if err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
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
	if err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
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
	if err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
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
	err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
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
	err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
		source("inlineFile", "/etc/passwd", "x", "644"),
	})
	if err == nil {
		t.Fatal("Materialize should reject an absolute source path")
	}
}

func TestMaterializeRejectsEmptyPath(t *testing.T) {
	root := t.TempDir()
	if err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
		source("inlineFile", "", "x", "644"),
	}); err == nil {
		t.Fatal("Materialize should reject an empty source path")
	}
}

func TestMaterializeRejectsSetuidMode(t *testing.T) {
	root := t.TempDir()
	if err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
		source("inlineFile", "a.sh", "x", "4755"),
	}); err == nil {
		t.Fatal("Materialize should reject a setuid mode")
	}
}

func TestMaterializeRejectsInvalidMode(t *testing.T) {
	root := t.TempDir()
	if err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
		source("inlineFile", "a.txt", "x", "not-octal"),
	}); err == nil {
		t.Fatal("Materialize should reject a non-octal mode string")
	}
}

func TestMaterializeRejectsUnsupportedSourceTypes(t *testing.T) {
	root := t.TempDir()
	for _, typ := range []string{"gitClone"} {
		err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
			source(typ, "x", "", ""),
		})
		if !errors.Is(err, workspace.ErrSourceUnsupported) {
			t.Errorf("source type %q: error = %v, want ErrSourceUnsupported", typ, err)
		}
	}
}

func TestMaterializeRejectsUnknownSourceType(t *testing.T) {
	root := t.TempDir()
	err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
		source("teleport", "x", "", ""),
	})
	if !errors.Is(err, workspace.ErrUnknownSourceType) {
		t.Errorf("error = %v, want ErrUnknownSourceType", err)
	}
}

func uploadSource(path, uploadRef, mode string) *adapterv1.WorkspaceSource {
	return &adapterv1.WorkspaceSource{
		Type: "uploadFile", Path: path, UploadRef: uploadRef, Mode: mode,
	}
}

// stageUpload writes content to the staging path StagingPath resolves
// the ref to, mirroring what the PrepareWorkspace RPC does on the pod.
func stageUpload(t *testing.T, stagingDir, ref string, content []byte) {
	t.Helper()
	p, err := workspace.StagingPath(stagingDir, ref)
	if err != nil {
		t.Fatalf("StagingPath(%q): %v", ref, err)
	}
	if err := os.WriteFile(p, content, 0o600); err != nil {
		t.Fatalf("stage upload %q: %v", ref, err)
	}
}

func TestMaterializeUploadFile(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	stageUpload(t, staging, "upload_abc", []byte("staged bytes"))
	if err := workspace.Materialize(root, staging, []*adapterv1.WorkspaceSource{
		uploadSource("data/input.bin", "upload_abc", ""),
	}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "data", "input.bin"))
	if err != nil {
		t.Fatalf("read materialized upload: %v", err)
	}
	if string(got) != "staged bytes" {
		t.Errorf("uploadFile content = %q, want %q", got, "staged bytes")
	}
	info, _ := os.Stat(filepath.Join(root, "data", "input.bin"))
	if info.Mode().Perm() != 0o644 {
		t.Errorf("uploadFile default mode = %o, want 644", info.Mode().Perm())
	}
}

func TestMaterializeUploadFileHonorsMode(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	stageUpload(t, staging, "u1", []byte("x"))
	if err := workspace.Materialize(root, staging, []*adapterv1.WorkspaceSource{
		uploadSource("run.sh", "u1", "750"),
	}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	info, _ := os.Stat(filepath.Join(root, "run.sh"))
	if info.Mode().Perm() != 0o750 {
		t.Errorf("uploadFile mode = %o, want 750", info.Mode().Perm())
	}
}

func TestMaterializeUploadFileMissingStagedContent(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir() // empty — nothing staged
	if err := workspace.Materialize(root, staging, []*adapterv1.WorkspaceSource{
		uploadSource("f.txt", "never_staged", ""),
	}); err == nil {
		t.Fatal("Materialize should fail when the staged upload is absent")
	}
}

func TestMaterializeUploadFileWithoutStagingDir(t *testing.T) {
	root := t.TempDir()
	if err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
		uploadSource("f.txt", "u1", ""),
	}); err == nil {
		t.Fatal("Materialize should fail for an uploadFile source with no staging directory")
	}
}

func TestStagingPath(t *testing.T) {
	if _, err := workspace.StagingPath("/staging", ""); err == nil {
		t.Error("StagingPath with an empty ref = nil, want an error")
	}
	// Any ref, including a lenny-blob:// URI or a traversal attempt,
	// hashes to a fixed-charset name strictly inside the staging dir.
	for _, ref := range []string{
		"upload_abc123",
		"lenny-blob://acme/sess-1/part-9?ttl=600",
		"../../etc/passwd",
		"a/b/c",
	} {
		got, err := workspace.StagingPath("/staging", ref)
		if err != nil {
			t.Fatalf("StagingPath(%q): %v", ref, err)
		}
		if filepath.Dir(got) != "/staging" {
			t.Errorf("StagingPath(%q) = %q, want a child of /staging", ref, got)
		}
	}
	// The mapping is deterministic and collision-free for distinct refs.
	a, _ := workspace.StagingPath("/staging", "ref-one")
	again, _ := workspace.StagingPath("/staging", "ref-one")
	b, _ := workspace.StagingPath("/staging", "ref-two")
	if a != again {
		t.Errorf("StagingPath is not deterministic: %q vs %q", a, again)
	}
	if a == b {
		t.Error("StagingPath mapped distinct refs to the same path")
	}
}
