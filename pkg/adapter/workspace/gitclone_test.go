// SPDX-License-Identifier: MIT

package workspace_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

func gitCloneSource(path, url, sha string) *adapterv1.WorkspaceSource {
	return &adapterv1.WorkspaceSource{
		Type: "gitClone", Path: path, Url: url, ResolvedCommitSha: sha,
	}
}

// repoArchive builds the gzip-tar the gateway delivers for a gitClone
// source: a snapshot of the cloned repository tree.
func repoArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	repo := t.TempDir()
	for name, content := range files {
		full := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("repo dir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("repo file: %v", err)
		}
	}
	var buf bytes.Buffer
	if _, err := workspace.Archive(repo, &buf); err != nil {
		t.Fatalf("archive repo: %v", err)
	}
	return buf.Bytes()
}

func TestMaterializeGitClone(t *testing.T) {
	src := gitCloneSource("vendor/lib", "https://example.com/acme/lib.git",
		"0123456789abcdef0123456789abcdef01234567")
	archive := repoArchive(t, map[string]string{
		"README.md":   "# project",
		"src/main.go": "package main",
	})

	root := t.TempDir()
	staging := t.TempDir()
	stageUpload(t, staging, workspace.GitCloneStagingRef(src), archive)

	if _, err := workspace.Materialize(root, staging, []*adapterv1.WorkspaceSource{src}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "vendor", "lib", "src", "main.go"))
	if err != nil {
		t.Fatalf("read cloned file: %v", err)
	}
	if string(got) != "package main" {
		t.Errorf("cloned file = %q, want %q", got, "package main")
	}
	if _, err := os.Stat(filepath.Join(root, "vendor", "lib", "README.md")); err != nil {
		t.Errorf("gitClone did not materialize the repository root file: %v", err)
	}
}

func TestMaterializeGitCloneWithoutStagingDir(t *testing.T) {
	src := gitCloneSource(".", "https://example.com/acme/lib.git",
		"0123456789abcdef0123456789abcdef01234567")
	if _, err := workspace.Materialize(t.TempDir(), "", []*adapterv1.WorkspaceSource{src}); err == nil {
		t.Fatal("Materialize should fail for a gitClone source with no staging directory")
	}
}

func TestMaterializeGitCloneMissingArchive(t *testing.T) {
	// The gateway did not stage the repository archive.
	src := gitCloneSource(".", "https://example.com/acme/lib.git",
		"0123456789abcdef0123456789abcdef01234567")
	if _, err := workspace.Materialize(t.TempDir(), t.TempDir(),
		[]*adapterv1.WorkspaceSource{src}); err == nil {
		t.Fatal("Materialize should fail when the gitClone archive is absent from staging")
	}
}

func TestGitCloneStagingRef(t *testing.T) {
	a := workspace.GitCloneStagingRef(gitCloneSource(".", "https://h/r.git", "aaaa"))
	again := workspace.GitCloneStagingRef(gitCloneSource("other", "https://h/r.git", "aaaa"))
	diffSHA := workspace.GitCloneStagingRef(gitCloneSource(".", "https://h/r.git", "bbbb"))
	diffURL := workspace.GitCloneStagingRef(gitCloneSource(".", "https://h/other.git", "aaaa"))
	if a != again {
		t.Error("GitCloneStagingRef should not depend on the destination path")
	}
	if a == diffSHA || a == diffURL {
		t.Error("GitCloneStagingRef should differ when the URL or commit SHA differs")
	}
}
