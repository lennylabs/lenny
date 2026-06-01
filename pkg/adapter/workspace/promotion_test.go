// SPDX-License-Identifier: MIT

package workspace_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// TestMaterializePromotesIntoRoot_spec_7_4_433 covers §7.4 line 433: the
// resolved tree is built in /workspace/staging and atomically promoted
// onto the workspace root, so every source's content lands under root and
// the sibling build tree is gone after a committed promotion. F-7.4.12,
// F-13.4.5.
func TestMaterializePromotesIntoRoot_spec_7_4_433(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	stageUpload(t, staging, "u1", []byte("up"))

	if _, err := workspace.Materialize(root, staging, []*adapterv1.WorkspaceSource{
		source("inlineFile", "a.txt", "hello", "644"),
		uploadSource("data/b.bin", "u1", ""),
	}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	if got, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(got) != "hello" {
		t.Errorf("a.txt = %q, want hello", got)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "data", "b.bin")); string(got) != "up" {
		t.Errorf("data/b.bin = %q, want up", got)
	}
	// The /workspace/staging build tree is renamed onto root by the
	// promotion, so it no longer exists as a sibling after a committed run.
	build := filepath.Join(filepath.Dir(root), "staging")
	if _, err := os.Stat(build); !os.IsNotExist(err) {
		t.Errorf("build staging tree survived the promotion: %v", err)
	}
}

// TestMaterializeRollsBackEntirePlanOnLaterSourceFailure_spec_7_4_460 is
// the core F-7.4.12 regression: before the staging→current promotion the
// adapter wrote each source straight into the workspace root, so a failure
// in a later source left earlier sources committed. With the build-then-
// promote pattern a failure in any source discards the whole build tree,
// so no partial plan reaches /workspace/current. spec: §7.4 line 460.
func TestMaterializeRollsBackEntirePlanOnLaterSourceFailure_spec_7_4_460(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir() // empty: the uploadFile ref below is never staged

	_, err := workspace.Materialize(root, staging, []*adapterv1.WorkspaceSource{
		source("inlineFile", "first.txt", "early", "644"),
		uploadSource("second.bin", "never_staged", ""),
	})
	if err == nil {
		t.Fatal("expected a failure on the unstaged upload source")
	}
	if _, statErr := os.Stat(filepath.Join(root, "first.txt")); statErr == nil {
		t.Error("an earlier source's file survived a later source's failure; whole-plan rollback is missing")
	}
}

// TestMaterializePreservesPriorRootOnFailure_spec_7_4_460 confirms a
// failed materialization leaves the pre-existing /workspace/current
// untouched: the build tree is discarded and root is never moved aside.
// spec: §7.4 line 460 ("returned to its pre-extraction state"). F-7.4.12.
func TestMaterializePreservesPriorRootOnFailure_spec_7_4_460(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "kept.txt"), []byte("untouched"), 0o644); err != nil {
		t.Fatalf("seed pre-existing file: %v", err)
	}
	staging := t.TempDir()

	_, err := workspace.Materialize(root, staging, []*adapterv1.WorkspaceSource{
		source("inlineFile", "new.txt", "x", "644"),
		uploadSource("f.bin", "missing", ""),
	})
	if err == nil {
		t.Fatal("expected a failure on the missing upload source")
	}
	if got, readErr := os.ReadFile(filepath.Join(root, "kept.txt")); readErr != nil || string(got) != "untouched" {
		t.Errorf("prior root content not preserved: got %q err %v", got, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, "new.txt")); statErr == nil {
		t.Error("partial new content from the failed plan survived in the workspace root")
	}
}

// TestMaterializeEmptyPlanLeavesEmptyRoot_spec_7_4_433 covers the
// degenerate plan: zero sources promote an empty build tree, leaving an
// empty but present workspace root (the §6.1 warm-time invariant). F-7.4.12.
func TestMaterializeEmptyPlanLeavesEmptyRoot_spec_7_4_433(t *testing.T) {
	root := t.TempDir()
	if _, err := workspace.Materialize(root, "", nil); err != nil {
		t.Fatalf("Materialize empty plan: %v", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		t.Fatalf("workspace root absent after empty-plan promotion: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("empty-plan root has %d entries, want 0", len(entries))
	}
}
