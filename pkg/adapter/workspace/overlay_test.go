// SPDX-License-Identifier: MIT

package workspace_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// TestMaterializeOverlayPreservesExistingFiles is the core §7.4 line 433
// mid-session invariant: an overlay adds the named files without wiping the
// files the running agent already created in /workspace/current. F-7.4.6.
func TestMaterializeOverlayPreservesExistingFiles_spec_7_4_433(t *testing.T) {
	root := t.TempDir()
	// The agent's existing workspace: a file and a nested file.
	if err := os.WriteFile(filepath.Join(root, "existing.txt"), []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := workspace.MaterializeOverlayWithPolicy(root, "", []*adapterv1.WorkspaceSource{
		source("inlineFile", "notes/added.md", "added", "644"),
	}, workspace.ArchivePolicy{})
	if err != nil {
		t.Fatalf("MaterializeOverlayWithPolicy: %v", err)
	}

	// The new file landed.
	if got, _ := os.ReadFile(filepath.Join(root, "notes", "added.md")); string(got) != "added" {
		t.Errorf("overlay file = %q, want %q", got, "added")
	}
	// The agent's pre-existing files survived.
	if got, _ := os.ReadFile(filepath.Join(root, "existing.txt")); string(got) != "keep me" {
		t.Errorf("existing file clobbered: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "src", "main.go")); string(got) != "package main" {
		t.Errorf("existing nested file clobbered: %q", got)
	}
}

// TestMaterializeOverlayReplacesCollidingFile asserts an overlay file at a
// path that already exists atomically replaces it (last-writer-wins), the
// per-file move semantics §7.4 line 433 prescribes. F-7.4.6.
func TestMaterializeOverlayReplacesCollidingFile_spec_7_4_433(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.MaterializeOverlayWithPolicy(root, "", []*adapterv1.WorkspaceSource{
		source("inlineFile", "config.json", "new", "644"),
	}, workspace.ArchivePolicy{}); err != nil {
		t.Fatalf("overlay: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("colliding file = %q, want overwrite %q", got, "new")
	}
	if info, _ := os.Stat(filepath.Join(root, "config.json")); info.Mode().Perm() != 0o644 {
		t.Errorf("overlaid mode = %o, want 644", info.Mode().Perm())
	}
}

// TestMaterializeOverlayUploadFile drives the production mid-session path:
// staged upload content overlaid onto the workspace. F-7.4.6.
func TestMaterializeOverlayUploadFile_spec_7_4_433(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	stageUpload(t, staging, "midupload-0", []byte("uploaded bytes"))
	if err := os.WriteFile(filepath.Join(root, "pre.txt"), []byte("pre"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := workspace.MaterializeOverlayWithPolicy(root, staging, []*adapterv1.WorkspaceSource{
		{Type: "uploadFile", Path: "data/in.bin", UploadRef: "midupload-0", Mode: "644"},
	}, workspace.ArchivePolicy{})
	if err != nil {
		t.Fatalf("overlay uploadFile: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "data", "in.bin")); string(got) != "uploaded bytes" {
		t.Errorf("uploaded file = %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "pre.txt")); err != nil {
		t.Errorf("pre-existing file removed by overlay: %v", err)
	}
}

// TestMaterializeOverlayRejectsTraversalLeavesWorkspaceUntouched asserts a
// containment-violating source aborts before any entry is moved, so the
// live workspace is untouched (§7.4 line 460 atomic-cleanup intent applied
// to the overlay path). F-7.4.6.
func TestMaterializeOverlayRejectsTraversalLeavesWorkspaceUntouched_spec_7_4(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := workspace.MaterializeOverlayWithPolicy(root, "", []*adapterv1.WorkspaceSource{
		source("inlineFile", "ok.txt", "ok", "644"),
		source("inlineFile", "../escape.txt", "bad", "644"),
	}, workspace.ArchivePolicy{})
	if err == nil {
		t.Fatal("overlay with a traversal path = nil, want error")
	}
	// The pre-existing file survives and the partial build was discarded:
	// neither the escaping file nor the first source's file leaked in.
	if got, _ := os.ReadFile(filepath.Join(root, "keep.txt")); string(got) != "keep" {
		t.Errorf("pre-existing file disturbed by aborted overlay: %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "ok.txt")); !os.IsNotExist(err) {
		t.Errorf("aborted overlay leaked the first source's file")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape.txt")); !os.IsNotExist(err) {
		t.Errorf("aborted overlay wrote outside the workspace root")
	}
}

// TestMaterializeOverlayCreatesMkdir confirms a mkdir source materializes an
// empty directory under the existing root without disturbing siblings.
// F-7.4.6.
func TestMaterializeOverlayCreatesMkdir_spec_7_4_433(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.MaterializeOverlayWithPolicy(root, "", []*adapterv1.WorkspaceSource{
		source("mkdir", "logs", "", "755"),
	}, workspace.ArchivePolicy{}); err != nil {
		t.Fatalf("overlay mkdir: %v", err)
	}
	info, err := os.Stat(filepath.Join(root, "logs"))
	if err != nil || !info.IsDir() {
		t.Fatalf("overlay did not create the directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); err != nil {
		t.Errorf("overlay mkdir disturbed a sibling file: %v", err)
	}
}
