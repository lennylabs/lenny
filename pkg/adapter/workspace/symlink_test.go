// SPDX-License-Identifier: MIT

package workspace_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// The gateway rewrites a symlink-bearing archive entry into a `symlink`
// source (§7.4 line 448 — the pod never decompresses). These tests cover
// the adapter's materialization of that source, and the fail-closed
// rejection of the uploadArchive / gitClone sources the gateway is
// supposed to have already extracted. F-7.4.1, F-13.4.1.

// TestMaterializeSymlinkSource_spec_7_4_458 materializes a gateway-
// produced symlink source under a runtime that opted in to symlinks and
// confirms the link is created pointing at its in-workspace target.
func TestMaterializeSymlinkSource_spec_7_4_458(t *testing.T) {
	root := t.TempDir()
	_, err := workspace.MaterializeWithPolicy(root, "", []*adapterv1.WorkspaceSource{
		source("inlineFile", "target.txt", "payload", "644"),
		{Type: "symlink", Path: "link.txt", LinkTarget: "target.txt"},
	}, workspace.ArchivePolicy{AllowSymlinks: true, WorkspaceRoot: root})
	if err != nil {
		t.Fatalf("MaterializeWithPolicy: %v", err)
	}
	info, err := os.Lstat(filepath.Join(root, "link.txt"))
	if err != nil {
		t.Fatalf("lstat link: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link.txt is not a symlink (mode %v)", info.Mode())
	}
	tgt, err := os.Readlink(filepath.Join(root, "link.txt"))
	if err != nil || tgt != "target.txt" {
		t.Fatalf("readlink = %q, %v; want target.txt", tgt, err)
	}
	// The link resolves to the target's content.
	got, err := os.ReadFile(filepath.Join(root, "link.txt"))
	if err != nil || string(got) != "payload" {
		t.Fatalf("resolved content = %q, %v; want payload", got, err)
	}
}

// A symlink source under a runtime that did NOT opt in fails closed: the
// gateway should never have produced it. spec: §7.4 line 458 — F-7.4.1.
func TestMaterializeSymlinkSourceRejectedWithoutOptIn_spec_7_4_458(t *testing.T) {
	root := t.TempDir()
	_, err := workspace.MaterializeWithPolicy(root, "", []*adapterv1.WorkspaceSource{
		{Type: "symlink", Path: "link.txt", LinkTarget: "target.txt"},
	}, workspace.ArchivePolicy{AllowSymlinks: false, WorkspaceRoot: root})
	if err == nil {
		t.Fatal("symlink source without allowSymlinks succeeded, want failure")
	}
	if !strings.Contains(err.Error(), "allowSymlinks") {
		t.Errorf("error = %v, want an allowSymlinks rejection", err)
	}
}

// A symlink whose target escapes the workspace root is rolled back by the
// §7.4 post-promotion re-validation. spec: §13.4 line 665 — F-7.4.1.
func TestMaterializeSymlinkEscapeRolledBack_spec_13_4_665(t *testing.T) {
	root := t.TempDir()
	_, err := workspace.MaterializeWithPolicy(root, "", []*adapterv1.WorkspaceSource{
		{Type: "symlink", Path: "link.txt", LinkTarget: "../../etc/passwd"},
	}, workspace.ArchivePolicy{AllowSymlinks: true, WorkspaceRoot: root})
	if err == nil {
		t.Fatal("escaping symlink succeeded, want post-promotion rollback failure")
	}
	if _, statErr := os.Lstat(filepath.Join(root, "link.txt")); statErr == nil {
		t.Error("escaping symlink survived on disk; promotion was not rolled back")
	}
}

// spec: §7.4 line 448; §13.4 line 652 — the adapter must never decompress
// an archive. A uploadArchive source reaching the adapter (a trust-
// boundary violation) fails closed. F-7.4.1, F-13.4.1.
func TestMaterializeUploadArchiveSourceRejected_spec_7_4_448(t *testing.T) {
	root := t.TempDir()
	_, err := workspace.Materialize(root, t.TempDir(), []*adapterv1.WorkspaceSource{
		{Type: "uploadArchive", Path: "x", UploadRef: "ref", Format: "tar"},
	})
	if err == nil {
		t.Fatal("uploadArchive at the adapter succeeded, want fail-closed rejection")
	}
	if !strings.Contains(err.Error(), "gateway") {
		t.Errorf("error = %v, want a gateway-extraction rejection", err)
	}
}

// spec: §7.4 line 448; §14 line 95 — gitClone is cloned and extracted by
// the gateway; a gitClone source reaching the adapter fails closed.
// F-7.4.1.
func TestMaterializeGitCloneSourceRejected_spec_7_4_448(t *testing.T) {
	root := t.TempDir()
	_, err := workspace.Materialize(root, t.TempDir(), []*adapterv1.WorkspaceSource{
		{Type: "gitClone", Path: "checkout", Url: "https://example.com/r.git"},
	})
	if err == nil {
		t.Fatal("gitClone at the adapter succeeded, want fail-closed rejection")
	}
	if !strings.Contains(err.Error(), "gateway") {
		t.Errorf("error = %v, want a gateway-extraction rejection", err)
	}
}
