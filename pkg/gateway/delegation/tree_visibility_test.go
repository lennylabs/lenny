// SPDX-License-Identifier: MIT

package delegation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// seedVisParent seeds a running parent with an explicit treeVisibility so
// the §8.3 inheritance / monotonicity tests can vary the parent side.
func seedVisParent(t *testing.T, store sessionstore.Store, vis session.TreeVisibility) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", State: session.StateRunning,
		UserID:     "user_alice",
		RuntimeRef: "claude", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		TreeVisibility: vis,
		CreatedAt:      now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
}

func delegateVis(svc *delegation.Service, vis session.TreeVisibility) (sessionstore.Session, error) {
	res, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
		TreeVisibility:   vis,
	})
	return res.Child, err
}

// TestDelegateTreeVisibilityInheritsWhenAbsent_spec_8_3_315 — a child that
// declares no treeVisibility inherits the parent's effective value.
func TestDelegateTreeVisibilityInheritsWhenAbsent_spec_8_3_315(t *testing.T) {
	store := memstore.New()
	seedVisParent(t, store, session.VisibilityParentAndSelf)
	svc := newService(t, store, func() string { return "child" })

	child, err := delegateVis(svc, "")
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if child.TreeVisibility != session.VisibilityParentAndSelf {
		t.Errorf("inherited treeVisibility = %q, want parent-and-self (§8.3 line 315)", child.TreeVisibility)
	}
}

// TestDelegateTreeVisibilityRootDefaultsToFull_spec_8_5_540 — a child of a
// parent with no declared visibility (empty → full) that also omits the
// field is stamped full.
func TestDelegateTreeVisibilityRootDefaultsToFull_spec_8_5_540(t *testing.T) {
	store := memstore.New()
	seedVisParent(t, store, "")
	svc := newService(t, store, func() string { return "child" })

	child, err := delegateVis(svc, "")
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if child.TreeVisibility != session.VisibilityFull {
		t.Errorf("default treeVisibility = %q, want full (§8.5 line 540)", child.TreeVisibility)
	}
}

// TestDelegateTreeVisibilityNarrowingAccepted_spec_8_3_316 — a full parent
// may issue a self-only child lease (narrowing is permitted).
func TestDelegateTreeVisibilityNarrowingAccepted_spec_8_3_316(t *testing.T) {
	store := memstore.New()
	seedVisParent(t, store, session.VisibilityFull)
	svc := newService(t, store, func() string { return "child" })

	child, err := delegateVis(svc, session.VisibilitySelfOnly)
	if err != nil {
		t.Fatalf("Delegate (narrow): %v", err)
	}
	if child.TreeVisibility != session.VisibilitySelfOnly {
		t.Errorf("narrowed treeVisibility = %q, want self-only", child.TreeVisibility)
	}
}

// TestDelegateTreeVisibilityWeakeningRejected_spec_8_3_317 — a
// parent-and-self parent may not issue a full child lease; the gateway
// rejects with the typed *TreeVisibilityWeakeningError carrying both sides.
func TestDelegateTreeVisibilityWeakeningRejected_spec_8_3_317(t *testing.T) {
	store := memstore.New()
	seedVisParent(t, store, session.VisibilityParentAndSelf)
	svc := newService(t, store, func() string { return "child" })

	_, err := delegateVis(svc, session.VisibilityFull)
	var tvw *delegation.TreeVisibilityWeakeningError
	if !errors.As(err, &tvw) {
		t.Fatalf("widening must return *TreeVisibilityWeakeningError, got %v", err)
	}
	if tvw.ParentVisibility != session.VisibilityParentAndSelf || tvw.ChildVisibility != session.VisibilityFull {
		t.Errorf("weakening error sides = (%q, %q), want (parent-and-self, full)", tvw.ParentVisibility, tvw.ChildVisibility)
	}
	// The rejection must happen before the child row is created.
	if _, gerr := store.Get(context.Background(), "acme", "child"); gerr == nil {
		t.Errorf("weakening must reject before creating the child row")
	}
}

// TestDelegateTreeVisibilitySelfOnlyParentRejectsAnyWidening_spec_8_3_317 —
// a self-only parent may issue only a self-only child lease.
func TestDelegateTreeVisibilitySelfOnlyParentRejectsAnyWidening_spec_8_3_317(t *testing.T) {
	store := memstore.New()
	seedVisParent(t, store, session.VisibilitySelfOnly)
	svc := newService(t, store, func() string { return "child" })

	if _, err := delegateVis(svc, session.VisibilityParentAndSelf); err == nil {
		t.Errorf("self-only parent must reject a parent-and-self child")
	}
	if child, err := delegateVis(svc, session.VisibilitySelfOnly); err != nil || child.TreeVisibility != session.VisibilitySelfOnly {
		t.Errorf("self-only parent must accept a self-only child: child=%+v err=%v", child, err)
	}
}

// TestDelegateTreeVisibilityInvalidRejected_spec_8_5_540 — an unrecognised
// explicit treeVisibility is rejected (not as a weakening error).
func TestDelegateTreeVisibilityInvalidRejected_spec_8_5_540(t *testing.T) {
	store := memstore.New()
	seedVisParent(t, store, session.VisibilityFull)
	svc := newService(t, store, func() string { return "child" })

	_, err := delegateVis(svc, "everything")
	if err == nil {
		t.Fatalf("invalid treeVisibility must be rejected")
	}
	var tvw *delegation.TreeVisibilityWeakeningError
	if errors.As(err, &tvw) {
		t.Errorf("invalid value must not surface as a weakening error: %v", err)
	}
}

// TestDelegateMessagingScopeSiblingsRequiresFull_spec_8_3_324 — a resolved
// effective messagingScope of `siblings` is incompatible with a non-full
// treeVisibility and is rejected with *TreeVisibilityMessagingScopeError.
func TestDelegateMessagingScopeSiblingsRequiresFull_spec_8_3_324(t *testing.T) {
	store := memstore.New()
	seedVisParent(t, store, session.VisibilityFull)
	svc := newService(t, store, func() string { return "child" })

	_, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:         "sess_parent",
		RuntimeRef:              "gemini",
		PoolRef:                 "pool-b",
		IsolationProfile:        isolation.ProfileSandboxed,
		TreeVisibility:          session.VisibilitySelfOnly,
		EffectiveMessagingScope: session.MessagingScopeSiblings,
	})
	var tvm *delegation.TreeVisibilityMessagingScopeError
	if !errors.As(err, &tvm) {
		t.Fatalf("siblings + self-only must return *TreeVisibilityMessagingScopeError, got %v", err)
	}
	if tvm.EffectiveMessagingScope != session.MessagingScopeSiblings || tvm.EffectiveTreeVisibility != session.VisibilitySelfOnly {
		t.Errorf("scope error = (%q, %q), want (siblings, self-only)", tvm.EffectiveMessagingScope, tvm.EffectiveTreeVisibility)
	}
}

// TestDelegateMessagingScopeSiblingsWithFullAccepted_spec_8_3_324 —
// siblings scope is compatible with full visibility.
func TestDelegateMessagingScopeSiblingsWithFullAccepted_spec_8_3_324(t *testing.T) {
	store := memstore.New()
	seedVisParent(t, store, session.VisibilityFull)
	svc := newService(t, store, func() string { return "child" })

	child, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:         "sess_parent",
		RuntimeRef:              "gemini",
		PoolRef:                 "pool-b",
		IsolationProfile:        isolation.ProfileSandboxed,
		EffectiveMessagingScope: session.MessagingScopeSiblings,
	})
	if err != nil {
		t.Fatalf("siblings + full must be accepted: %v", err)
	}
	if child.Child.TreeVisibility != session.VisibilityFull {
		t.Errorf("child treeVisibility = %q, want full", child.Child.TreeVisibility)
	}
}

// TestDelegateMessagingScopeDirectAllowsNarrowVisibility_spec_8_3_324 — the
// default `direct` scope imposes no treeVisibility requirement, so a
// self-only child is admitted.
func TestDelegateMessagingScopeDirectAllowsNarrowVisibility_spec_8_3_324(t *testing.T) {
	store := memstore.New()
	seedVisParent(t, store, session.VisibilityFull)
	svc := newService(t, store, func() string { return "child" })

	// EffectiveMessagingScope left empty (resolves to direct).
	child, err := delegateVis(svc, session.VisibilitySelfOnly)
	if err != nil {
		t.Fatalf("direct scope + self-only must be accepted: %v", err)
	}
	if child.TreeVisibility != session.VisibilitySelfOnly {
		t.Errorf("child treeVisibility = %q, want self-only", child.TreeVisibility)
	}
}
