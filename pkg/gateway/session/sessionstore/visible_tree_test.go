// SPDX-License-Identifier: MIT

package sessionstore_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// tree fixture: root → mid → leaf, plus a sibling of mid under root.
// All rows share root_session_id == "root". spec: §8.9 line 1010.
func visTree() (root, mid, leaf, sib sessionstore.Session, all []sessionstore.Session) {
	root = sessionstore.Session{ID: "root", RootSessionID: "root"}
	mid = sessionstore.Session{ID: "mid", ParentSessionID: "root", RootSessionID: "root"}
	leaf = sessionstore.Session{ID: "leaf", ParentSessionID: "mid", RootSessionID: "root"}
	sib = sessionstore.Session{ID: "sib", ParentSessionID: "root", RootSessionID: "root"}
	all = []sessionstore.Session{root, mid, leaf, sib}
	return
}

// TestVisibleTreeFullRootsAtApex_spec_8_5_540 — full visibility roots the
// response at the tree apex regardless of which node is the caller, and
// imposes no node restriction (nil allowed). F-8.5.2 / F-8.9.2.
func TestVisibleTreeFullRootsAtApex_spec_8_5_540(t *testing.T) {
	root, mid, _, _, all := visTree()
	got, allowed := sessionstore.VisibleTree(mid, all, session.VisibilityFull)
	if got.ID != root.ID {
		t.Errorf("full from mid: root = %q, want %q (apex)", got.ID, root.ID)
	}
	if allowed != nil {
		t.Errorf("full must impose no restriction (nil allowed), got %v", allowed)
	}
}

// TestVisibleTreeFullEmptyVisibilityDefaults_spec_8_5_540 — an empty
// stored visibility resolves to full (apex-rooted).
func TestVisibleTreeFullEmptyVisibilityDefaults_spec_8_5_540(t *testing.T) {
	root, mid, _, _, all := visTree()
	got, allowed := sessionstore.VisibleTree(mid, all, "")
	if got.ID != root.ID || allowed != nil {
		t.Errorf("empty visibility: root=%q allowed=%v, want apex/nil", got.ID, allowed)
	}
}

// TestVisibleTreeParentAndSelf_spec_8_5_540 — parent-and-self roots at the
// caller's parent and admits exactly {parent, self}. F-8.5.2 / F-8.9.2.
func TestVisibleTreeParentAndSelf_spec_8_5_540(t *testing.T) {
	root, mid, _, _, all := visTree()
	got, allowed := sessionstore.VisibleTree(mid, all, session.VisibilityParentAndSelf)
	if got.ID != root.ID {
		t.Errorf("parent-and-self: root = %q, want %q (parent)", got.ID, root.ID)
	}
	if len(allowed) != 2 || !allowed["root"] || !allowed["mid"] {
		t.Errorf("parent-and-self allowed = %v, want {root, mid}", allowed)
	}
	if allowed["leaf"] || allowed["sib"] {
		t.Errorf("parent-and-self must exclude leaf and sibling: %v", allowed)
	}
}

// TestVisibleTreeParentAndSelfAtRoot_spec_8_3_315 — at the tree root
// (no parent) parent-and-self degenerates to self-only. spec: §8.3 line 315.
func TestVisibleTreeParentAndSelfAtRoot_spec_8_3_315(t *testing.T) {
	root, _, _, _, all := visTree()
	got, allowed := sessionstore.VisibleTree(root, all, session.VisibilityParentAndSelf)
	if got.ID != root.ID || len(allowed) != 1 || !allowed["root"] {
		t.Errorf("parent-and-self at root: root=%q allowed=%v, want self-only", got.ID, allowed)
	}
}

// TestVisibleTreeParentAndSelfMissingParent_spec_8_5_540 — when the parent
// row is absent from the set, the caller falls back to self-only so it
// never observes more than its own node.
func TestVisibleTreeParentAndSelfMissingParent_spec_8_5_540(t *testing.T) {
	orphan := sessionstore.Session{ID: "orphan", ParentSessionID: "gone", RootSessionID: "root"}
	all := []sessionstore.Session{orphan}
	got, allowed := sessionstore.VisibleTree(orphan, all, session.VisibilityParentAndSelf)
	if got.ID != "orphan" || len(allowed) != 1 || !allowed["orphan"] {
		t.Errorf("missing parent: root=%q allowed=%v, want self-only fallback", got.ID, allowed)
	}
}

// TestVisibleTreeSelfOnly_spec_8_5_540 — self-only roots at the caller and
// admits only the caller. F-8.5.2 / F-8.9.2.
func TestVisibleTreeSelfOnly_spec_8_5_540(t *testing.T) {
	_, mid, _, _, all := visTree()
	got, allowed := sessionstore.VisibleTree(mid, all, session.VisibilitySelfOnly)
	if got.ID != "mid" || len(allowed) != 1 || !allowed["mid"] {
		t.Errorf("self-only: root=%q allowed=%v, want {mid}", got.ID, allowed)
	}
}

// TestVisibleTreeFullApexMissing_spec_8_5_540 — when the apex row is not
// present (legacy row pointing at a GC'd root), full falls back to rooting
// at the caller without restriction.
func TestVisibleTreeFullApexMissing_spec_8_5_540(t *testing.T) {
	mid := sessionstore.Session{ID: "mid", ParentSessionID: "root", RootSessionID: "root"}
	all := []sessionstore.Session{mid} // apex "root" absent
	got, allowed := sessionstore.VisibleTree(mid, all, session.VisibilityFull)
	if got.ID != "mid" || allowed != nil {
		t.Errorf("full apex-missing: root=%q allowed=%v, want caller/nil", got.ID, allowed)
	}
}
