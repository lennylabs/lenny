// SPDX-License-Identifier: MIT

package delegation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/delegation/cycle"
	"github.com/lennylabs/lenny/pkg/delegation/lease"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

func seedParent(t *testing.T, store sessionstore.Store, id, parentID, runtime, pool string, prof isolation.Profile) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	row := sessionstore.Session{
		ID: id, TenantID: "acme", State: session.StateRunning,
		RuntimeRef: runtime, PoolRef: pool, IsolationProfile: prof,
		ParentSessionID: parentID,
		CreatedAt:       now, UpdatedAt: now,
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func newService(t *testing.T, store sessionstore.Store, idFn func() string) *delegation.Service {
	t.Helper()
	return delegation.NewService(store, delegation.Options{
		Clock:  func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc: idFn,
	})
}

func TestDelegateHappyPath(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	svc := newService(t, store, func() string { return "sess_child" })

	res, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
	})
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if res.Child.ID != "sess_child" || res.Child.ParentSessionID != "sess_parent" {
		t.Errorf("child: %+v", res.Child)
	}
	if res.Depth != 1 {
		t.Errorf("depth: got %d, want 1", res.Depth)
	}
	// The child must be persisted.
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err != nil {
		t.Errorf("child not stored: %v", err)
	}
}

func TestDelegateRejectsNonRunningParent(t *testing.T) {
	store := memstore.New()
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", State: session.StateCompleted,
		CreatedAt: now, UpdatedAt: now,
	})
	svc := newService(t, store, nil)
	_, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID: "sess_parent", RuntimeRef: "x",
	})
	if !errors.Is(err, delegation.ErrParentNotRunning) {
		t.Errorf("got %v, want ErrParentNotRunning", err)
	}
}

func TestDelegateRejectsMissingParent(t *testing.T) {
	store := memstore.New()
	svc := newService(t, store, nil)
	_, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID: "sess_missing", RuntimeRef: "x",
	})
	if !errors.Is(err, delegation.ErrParentNotFound) {
		t.Errorf("got %v, want ErrParentNotFound", err)
	}
}

func TestDelegateRejectsIsolationDowngrade(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileMicrovm)
	svc := newService(t, store, func() string { return "sess_child" })

	_, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed, // weaker than microvm
	})
	var ive *delegation.IsolationViolationError
	if !errors.As(err, &ive) {
		t.Fatalf("got %v, want IsolationViolationError", err)
	}
	if ive.ParentProfile != isolation.ProfileMicrovm || ive.ChildProfile != isolation.ProfileSandboxed {
		t.Errorf("violation profiles: %+v", ive)
	}
}

func TestDelegateAdmitsStricterIsolation(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	svc := newService(t, store, func() string { return "sess_child" })
	_, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileMicrovm, // stricter — OK
	})
	if err != nil {
		t.Errorf("stricter isolation should be admitted: %v", err)
	}
}

func TestDelegateDetectsCycle(t *testing.T) {
	store := memstore.New()
	// Lineage: root(claude/pool-a) -> parent(gemini/pool-b).
	seedParent(t, store, "sess_root", "", "claude", "pool-a", isolation.ProfileSandboxed)
	seedParent(t, store, "sess_parent", "sess_root", "gemini", "pool-b", isolation.ProfileSandboxed)
	svc := newService(t, store, func() string { return "sess_child" })

	// Delegating back to (claude, pool-a) re-enters the lineage → cycle.
	_, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "claude",
		PoolRef:          "pool-a",
		IsolationProfile: isolation.ProfileSandboxed,
	})
	var rej *cycle.Rejection
	if !errors.As(err, &rej) {
		t.Fatalf("got %v, want cycle.Rejection", err)
	}
}

func TestDelegatePoolDifferentiatedIsNotCycle(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_root", "", "claude", "pool-a", isolation.ProfileSandboxed)
	seedParent(t, store, "sess_parent", "sess_root", "gemini", "pool-b", isolation.ProfileSandboxed)
	svc := newService(t, store, func() string { return "sess_child" })

	// Same runtime (claude) but a different pool — §8.2 says NOT a cycle.
	_, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "claude",
		PoolRef:          "pool-c",
		IsolationProfile: isolation.ProfileSandboxed,
	})
	if err != nil {
		t.Errorf("pool-differentiated delegation should be admitted: %v", err)
	}
}

func TestDelegateDepthLimit(t *testing.T) {
	store := memstore.New()
	// Build a 3-deep lineage: root -> a -> parent (depth 2).
	seedParent(t, store, "sess_root", "", "r0", "p0", isolation.ProfileSandboxed)
	seedParent(t, store, "sess_a", "sess_root", "r1", "p1", isolation.ProfileSandboxed)
	seedParent(t, store, "sess_parent", "sess_a", "r2", "p2", isolation.ProfileSandboxed)
	svc := newService(t, store, func() string { return "sess_child" })

	// Parent depth is 2; child would be depth 3. MaxDepth=2 rejects.
	_, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "r3",
		PoolRef:          "p3",
		IsolationProfile: isolation.ProfileSandboxed,
		MaxDepth:         2,
	})
	var de *lease.DepthExceededError
	if !errors.As(err, &de) {
		t.Fatalf("got %v, want DepthExceededError", err)
	}
}

func TestDelegateInheritsParentIsolationWhenUnset(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileMicrovm)
	svc := newService(t, store, func() string { return "sess_child" })
	res, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID: "sess_parent",
		RuntimeRef:      "gemini",
		PoolRef:         "pool-b",
		// IsolationProfile omitted — inherits microvm.
	})
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if res.Child.IsolationProfile != isolation.ProfileMicrovm {
		t.Errorf("child isolation: got %q, want microvm", res.Child.IsolationProfile)
	}
}
