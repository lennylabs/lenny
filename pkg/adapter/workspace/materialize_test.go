// SPDX-License-Identifier: MIT

package workspace_test

import (
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
	_, err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
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
	if _, err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
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
	if _, err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
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
	if _, err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
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
	if _, err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
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
	_, err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
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
	_, err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
		source("inlineFile", "/etc/passwd", "x", "644"),
	})
	if err == nil {
		t.Fatal("Materialize should reject an absolute source path")
	}
}

func TestMaterializeRejectsEmptyPath(t *testing.T) {
	root := t.TempDir()
	if _, err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
		source("inlineFile", "", "x", "644"),
	}); err == nil {
		t.Fatal("Materialize should reject an empty source path")
	}
}

func TestMaterializeRejectsSetuidMode(t *testing.T) {
	root := t.TempDir()
	if _, err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
		source("inlineFile", "a.sh", "x", "4755"),
	}); err == nil {
		t.Fatal("Materialize should reject a setuid mode")
	}
}

func TestMaterializeRejectsInvalidMode(t *testing.T) {
	root := t.TempDir()
	if _, err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
		source("inlineFile", "a.txt", "x", "not-octal"),
	}); err == nil {
		t.Fatal("Materialize should reject a non-octal mode string")
	}
}

// spec: §14 line 334 — an unknown source.type is skipped with a
// workspace_plan_unknown_source_type warning, not rejected. A newer
// gateway can inject a source type this adapter predates during a
// rolling upgrade; aborting the whole materialization would crash the
// session setup instead of gracefully degrading. F-14.1.2.
func TestMaterializeSkipsUnknownSourceType_spec_14_334(t *testing.T) {
	root := t.TempDir()
	warnings, err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
		source("teleport", "x", "", ""),
	})
	if err != nil {
		t.Fatalf("Materialize must skip an unknown source type, got error: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings: got %d, want 1: %+v", len(warnings), warnings)
	}
	w := warnings[0]
	if w.Code != "workspace_plan_unknown_source_type" {
		t.Errorf("warning code: got %q, want workspace_plan_unknown_source_type", w.Code)
	}
	if w.UnknownType != "teleport" {
		t.Errorf("warning unknownType: got %q, want teleport", w.UnknownType)
	}
	if w.SourceIndex != 0 {
		t.Errorf("warning sourceIndex: got %d, want 0", w.SourceIndex)
	}
}

// spec: §14 line 334 — a known source preceding an unknown one is still
// materialized; only the unknown entry is skipped. This guards against a
// regression where an unknown type short-circuits the source loop.
// F-14.1.2.
func TestMaterializeSkipsUnknownButKeepsKnown_spec_14_334(t *testing.T) {
	root := t.TempDir()
	warnings, err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
		source("inlineFile", "keep.txt", "hello", "0644"),
		source("teleport", "x", "", ""),
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(warnings) != 1 || warnings[0].UnknownType != "teleport" {
		t.Fatalf("warnings: got %+v, want one teleport skip", warnings)
	}
	if warnings[0].SourceIndex != 1 {
		t.Errorf("warning sourceIndex: got %d, want 1", warnings[0].SourceIndex)
	}
	got, readErr := os.ReadFile(filepath.Join(root, "keep.txt"))
	if readErr != nil {
		t.Fatalf("known inlineFile was not materialized: %v", readErr)
	}
	if string(got) != "hello" {
		t.Errorf("keep.txt content: got %q, want hello", got)
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
	if _, err := workspace.Materialize(root, staging, []*adapterv1.WorkspaceSource{
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
	if _, err := workspace.Materialize(root, staging, []*adapterv1.WorkspaceSource{
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
	if _, err := workspace.Materialize(root, staging, []*adapterv1.WorkspaceSource{
		uploadSource("f.txt", "never_staged", ""),
	}); err == nil {
		t.Fatal("Materialize should fail when the staged upload is absent")
	}
}

func TestMaterializeUploadFileWithoutStagingDir(t *testing.T) {
	root := t.TempDir()
	if _, err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
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
