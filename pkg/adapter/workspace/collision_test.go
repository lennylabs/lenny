// SPDX-License-Identifier: MIT

package workspace_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

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

// TestMaterializeCollisionInlineOverArchive is the spec's named example:
// an uploadArchive extracts foo/bar.txt, then an inlineFile writes
// foo/bar.txt. The collision only manifests at materialization time
// because the archive entry path is unknown until extraction.
// spec: §14 line 338. F-14.1.9.
func TestMaterializeCollisionInlineOverArchive_spec_14_338(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	stageUpload(t, staging, "arch", buildTar(t, false, map[string]string{
		"foo/bar.txt":  "from archive",
		"foo/keep.txt": "untouched",
	}))
	warnings, err := workspace.Materialize(root, staging, []*adapterv1.WorkspaceSource{
		{Type: "uploadArchive", UploadRef: "arch", Format: "tar"},
		source("inlineFile", "foo/bar.txt", "from inline", "644"),
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	cs := collisionWarnings(warnings)
	if len(cs) != 1 {
		t.Fatalf("collision warnings = %d, want 1 (%+v)", len(cs), warnings)
	}
	if cs[0].Path != "foo/bar.txt" || cs[0].WinningSourceIndex != 1 || cs[0].LosingSourceIndex != 0 {
		t.Errorf("collision = %+v, want path=foo/bar.txt winning=1 losing=0", cs[0])
	}
	got, _ := os.ReadFile(filepath.Join(root, "foo", "bar.txt"))
	if string(got) != "from inline" {
		t.Errorf("on-disk content = %q, want last-writer %q", got, "from inline")
	}
	// The non-colliding archive entry is left untouched.
	if got, _ := os.ReadFile(filepath.Join(root, "foo", "keep.txt")); string(got) != "untouched" {
		t.Errorf("non-colliding entry content = %q, want %q", got, "untouched")
	}
}

// TestMaterializeCollisionArchiveOverInline reverses the order: the
// archive is the later (winning) source. The losing index must point at
// the earlier inlineFile. spec: §14 line 338. F-14.1.9.
func TestMaterializeCollisionArchiveOverInline_spec_14_338(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	stageUpload(t, staging, "arch", buildTar(t, true, map[string]string{
		"data/x.bin": "archive bytes",
	}))
	warnings, err := workspace.Materialize(root, staging, []*adapterv1.WorkspaceSource{
		source("inlineFile", "data/x.bin", "inline bytes", "644"),
		{Type: "uploadArchive", UploadRef: "arch", Format: "tar.gz"},
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
	stageUpload(t, staging, "arch", buildTar(t, false, map[string]string{
		"src/main.go": "package main",
	}))
	warnings, err := workspace.Materialize(root, staging, []*adapterv1.WorkspaceSource{
		source("inlineFile", "README.md", "readme", "644"),
		{Type: "uploadArchive", UploadRef: "arch", Format: "tar"},
		source("inlineFile", "docs/guide.md", "guide", "644"),
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if cs := collisionWarnings(warnings); len(cs) != 0 {
		t.Errorf("collision warnings = %d, want 0 (%+v)", len(cs), cs)
	}
}

// TestMaterializeCollisionAcrossArchives covers two archives whose entry
// sets overlap; the second archive's entry wins. spec: §14 line 338.
// F-14.1.9.
func TestMaterializeCollisionAcrossArchives_spec_14_338(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	stageUpload(t, staging, "a", buildTar(t, false, map[string]string{"shared.txt": "from a", "only-a.txt": "a"}))
	stageUpload(t, staging, "b", buildTar(t, false, map[string]string{"shared.txt": "from b", "only-b.txt": "b"}))
	warnings, err := workspace.Materialize(root, staging, []*adapterv1.WorkspaceSource{
		{Type: "uploadArchive", UploadRef: "a", Format: "tar"},
		{Type: "uploadArchive", UploadRef: "b", Format: "tar"},
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

// TestMaterializeNoSelfCollisionWithinArchive confirms that the
// cross-source collision detector does not fire for a single source. The
// §14 line 338 rule is scoped to the `sources` array. F-14.1.9.
func TestMaterializeNoSelfCollisionWithinArchive_spec_14_338(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	stageUpload(t, staging, "arch", buildTar(t, false, map[string]string{
		"a.txt": "1",
		"b.txt": "2",
	}))
	warnings, err := workspace.Materialize(root, staging, []*adapterv1.WorkspaceSource{
		{Type: "uploadArchive", UploadRef: "arch", Format: "tar"},
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if cs := collisionWarnings(warnings); len(cs) != 0 {
		t.Errorf("collision warnings = %d, want 0 (%+v)", len(cs), cs)
	}
}
