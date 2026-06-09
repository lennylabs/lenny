// SPDX-License-Identifier: MIT

package workspace_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// The §14 line 338 last-writer-wins path-collision warning is raised by
// the adapter's materializer across the sources it receives. Archive
// entries reach the adapter as the uploadFile/mkdir/symlink sources the
// gateway rewrote them into (§7.4 line 448 — the pod no longer
// decompresses), so these tests exercise collisions across inlineFile,
// uploadFile, and mkdir sources. Intra-archive dedup is the gateway
// extractor's concern (pkg/upload/archive). F-14.1.9, F-7.4.1.

// collisionWarnings filters a warning slice to the §14 line 338
// path-collision advisories.
func collisionWarnings(ws []workspace.Warning) []workspace.Warning {
	var out []workspace.Warning
	for _, w := range ws {
		if w.Code == "workspace_plan_path_collision" {
			out = append(out, w)
		}
	}
	return out
}

// uploadFileSource builds a uploadFile source whose content is staged
// under ref, mirroring the gateway's archive-entry rewrite.
func uploadFileSource(path, ref, mode string) *adapterv1.WorkspaceSource {
	return &adapterv1.WorkspaceSource{Type: "uploadFile", Path: path, UploadRef: ref, Mode: mode}
}

// TestMaterializeCollisionInlineOverInline asserts that two sources
// resolving to the same workspace path raise a path-collision warning
// with the later source winning. spec: §14 line 338. F-14.1.9.
func TestMaterializeCollisionInlineOverInline_spec_14_338(t *testing.T) {
	root := t.TempDir()
	warnings, err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
		source("inlineFile", "config/app.yaml", "first", "644"),
		source("inlineFile", "config/app.yaml", "second", "644"),
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	cs := collisionWarnings(warnings)
	if len(cs) != 1 {
		t.Fatalf("collision warnings = %d, want 1 (%+v)", len(cs), warnings)
	}
	w := cs[0]
	if w.Path != "config/app.yaml" {
		t.Errorf("collision path = %q, want config/app.yaml", w.Path)
	}
	if w.WinningSourceIndex != 1 || w.LosingSourceIndex != 0 {
		t.Errorf("winning/losing = %d/%d, want 1/0", w.WinningSourceIndex, w.LosingSourceIndex)
	}
	if w.SourceIndex != w.WinningSourceIndex {
		t.Errorf("SourceIndex = %d, want = WinningSourceIndex %d", w.SourceIndex, w.WinningSourceIndex)
	}
	// spec: §14 line 338 — last-writer-wins: the second source's content
	// survives on disk.
	got, _ := os.ReadFile(filepath.Join(root, "config", "app.yaml"))
	if string(got) != "second" {
		t.Errorf("on-disk content = %q, want last-writer %q", got, "second")
	}
}

// TestMaterializeCollisionInlineOverUploadFile is the spec's named
// example expressed at the adapter's input: a gateway-extracted archive
// entry (now a uploadFile) writes foo/bar.txt, then an inlineFile writes
// foo/bar.txt. The later inlineFile wins. spec: §14 line 338. F-14.1.9.
func TestMaterializeCollisionInlineOverUploadFile_spec_14_338(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	stageUpload(t, staging, "bar", []byte("from archive"))
	stageUpload(t, staging, "keep", []byte("untouched"))
	warnings, err := workspace.Materialize(root, staging, []*adapterv1.WorkspaceSource{
		uploadFileSource("foo/bar.txt", "bar", "644"),
		uploadFileSource("foo/keep.txt", "keep", "644"),
		source("inlineFile", "foo/bar.txt", "from inline", "644"),
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	cs := collisionWarnings(warnings)
	if len(cs) != 1 {
		t.Fatalf("collision warnings = %d, want 1 (%+v)", len(cs), warnings)
	}
	if cs[0].Path != "foo/bar.txt" || cs[0].WinningSourceIndex != 2 || cs[0].LosingSourceIndex != 0 {
		t.Errorf("collision = %+v, want path=foo/bar.txt winning=2 losing=0", cs[0])
	}
	got, _ := os.ReadFile(filepath.Join(root, "foo", "bar.txt"))
	if string(got) != "from inline" {
		t.Errorf("on-disk content = %q, want last-writer %q", got, "from inline")
	}
	if got, _ := os.ReadFile(filepath.Join(root, "foo", "keep.txt")); string(got) != "untouched" {
		t.Errorf("non-colliding entry content = %q, want %q", got, "untouched")
	}
}

// TestMaterializeCollisionUploadFileOverInline reverses the order: the
// later uploadFile (a gateway-extracted archive entry) wins, and the
// losing index points at the earlier inlineFile. spec: §14 line 338.
// F-14.1.9.
func TestMaterializeCollisionUploadFileOverInline_spec_14_338(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	stageUpload(t, staging, "x", []byte("archive bytes"))
	warnings, err := workspace.Materialize(root, staging, []*adapterv1.WorkspaceSource{
		source("inlineFile", "data/x.bin", "inline bytes", "644"),
		uploadFileSource("data/x.bin", "x", "644"),
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	cs := collisionWarnings(warnings)
	if len(cs) != 1 {
		t.Fatalf("collision warnings = %d, want 1 (%+v)", len(cs), warnings)
	}
	if cs[0].WinningSourceIndex != 1 || cs[0].LosingSourceIndex != 0 {
		t.Errorf("winning/losing = %d/%d, want 1/0", cs[0].WinningSourceIndex, cs[0].LosingSourceIndex)
	}
	got, _ := os.ReadFile(filepath.Join(root, "data", "x.bin"))
	if string(got) != "archive bytes" {
		t.Errorf("on-disk content = %q, want last-writer %q", got, "archive bytes")
	}
}

// TestMaterializeNoCollisionDistinctPaths confirms non-overlapping
// sources raise no collision warning. spec: §14 line 338. F-14.1.9.
func TestMaterializeNoCollisionDistinctPaths_spec_14_338(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	stageUpload(t, staging, "main", []byte("package main"))
	warnings, err := workspace.Materialize(root, staging, []*adapterv1.WorkspaceSource{
		source("inlineFile", "README.md", "readme", "644"),
		uploadFileSource("src/main.go", "main", "644"),
		source("inlineFile", "docs/guide.md", "guide", "644"),
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if cs := collisionWarnings(warnings); len(cs) != 0 {
		t.Errorf("collision warnings = %d, want 0 (%+v)", len(cs), cs)
	}
}

// TestMaterializeCollisionAcrossUploadFiles covers two gateway-extracted
// archives whose entry sets overlap: each entry arrives as a uploadFile,
// and the later one wins. spec: §14 line 338. F-14.1.9.
func TestMaterializeCollisionAcrossUploadFiles_spec_14_338(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	stageUpload(t, staging, "a-shared", []byte("from a"))
	stageUpload(t, staging, "b-shared", []byte("from b"))
	warnings, err := workspace.Materialize(root, staging, []*adapterv1.WorkspaceSource{
		uploadFileSource("shared.txt", "a-shared", "644"),
		uploadFileSource("shared.txt", "b-shared", "644"),
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	cs := collisionWarnings(warnings)
	if len(cs) != 1 {
		t.Fatalf("collision warnings = %d, want 1 (%+v)", len(cs), warnings)
	}
	if cs[0].Path != "shared.txt" || cs[0].WinningSourceIndex != 1 || cs[0].LosingSourceIndex != 0 {
		t.Errorf("collision = %+v, want path=shared.txt winning=1 losing=0", cs[0])
	}
	if got, _ := os.ReadFile(filepath.Join(root, "shared.txt")); string(got) != "from b" {
		t.Errorf("on-disk content = %q, want last-writer %q", got, "from b")
	}
}

// TestMaterializeNoCollisionSharedDirectory confirms that two sources
// creating files under a common directory (the directory itself is
// shared, the files differ) do not raise a collision: directory merging
// is normal, not an overwrite. spec: §14 line 338. F-14.1.9.
func TestMaterializeNoCollisionSharedDirectory_spec_14_338(t *testing.T) {
	root := t.TempDir()
	warnings, err := workspace.Materialize(root, "", []*adapterv1.WorkspaceSource{
		source("mkdir", "shared", "", "755"),
		source("inlineFile", "shared/a.txt", "a", "644"),
		source("inlineFile", "shared/b.txt", "b", "644"),
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if cs := collisionWarnings(warnings); len(cs) != 0 {
		t.Errorf("collision warnings = %d, want 0 (%+v)", len(cs), cs)
	}
}
