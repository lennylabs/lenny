// SPDX-License-Identifier: MIT

package delegation_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/delegation/cycle"
	"github.com/lennylabs/lenny/pkg/delegation/lease"
	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

func seedParent(t *testing.T, store sessionstore.Store, id, parentID, runtime, pool string, prof isolation.Profile) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	row := sessionstore.Session{
		ID: id, TenantID: "acme", State: session.StateRunning,
		// §8.2 line 58: child-token exchange requires a parent user
		// identity; tests seed `user_alice` so Delegate does not
		// reject with ErrParentNoUser.
		UserID:     "user_alice",
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

// spec: §8.3 / §10.7 — a delegation child inherits the parent's
// experimentContext per the experiment's propagation mode.

func seedExperiment(t *testing.T, exps experimentstore.Store, id string, prop experiment.Propagation) {
	t.Helper()
	if err := exps.Create(context.Background(), experimentstore.Experiment{
		ID: id, TenantID: "acme", Status: experiment.StatusActive,
		BaseRuntime:   "claude",
		Variants:      []experimentstore.Variant{{ID: "treatment", Weight: 0.5}},
		TargetingMode: experiment.TargetingPercentage,
		Sticky:        experiment.StickySession,
		Propagation:   prop,
	}); err != nil {
		t.Fatalf("seed experiment %s: %v", id, err)
	}
}

func seedEnrolledParent(t *testing.T, store sessionstore.Store, expID string) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", State: session.StateRunning,
		// §8.2 line 58: Delegate requires the parent carry an
		// authenticated user identity.
		UserID:     "user_alice",
		RuntimeRef: "claude", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		ExperimentContext: &sessionstore.ExperimentContext{ExperimentID: expID, VariantID: "treatment"},
		CreatedAt:         now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed enrolled parent: %v", err)
	}
}

func delegateChild(t *testing.T, svc *delegation.Service) sessionstore.Session {
	t.Helper()
	res, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
	})
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	return res.Child
}

func TestDelegateInheritPropagatesParentContext(t *testing.T) {
	store := memstore.New()
	exps := experimentstore.NewMemory()
	seedExperiment(t, exps, "exp_1", experiment.PropagationInherit)
	seedEnrolledParent(t, store, "exp_1")
	svc := delegation.NewService(store, delegation.Options{
		IDFunc: func() string { return "sess_child" }, Experiments: exps,
	})
	child := delegateChild(t, svc)
	if child.ExperimentContext == nil {
		t.Fatal("child has no experimentContext under inherit")
	}
	if child.ExperimentContext.ExperimentID != "exp_1" ||
		child.ExperimentContext.VariantID != "treatment" || !child.ExperimentContext.Inherited {
		t.Errorf("inherit: child context = %+v, want exp_1/treatment inherited", child.ExperimentContext)
	}
}

func TestDelegateControlForcesControlVariant(t *testing.T) {
	store := memstore.New()
	exps := experimentstore.NewMemory()
	seedExperiment(t, exps, "exp_1", experiment.PropagationControl)
	seedEnrolledParent(t, store, "exp_1")
	svc := delegation.NewService(store, delegation.Options{
		IDFunc: func() string { return "sess_child" }, Experiments: exps,
	})
	child := delegateChild(t, svc)
	if child.ExperimentContext == nil {
		t.Fatal("child has no experimentContext under control")
	}
	if child.ExperimentContext.VariantID != experiment.ControlVariantID || !child.ExperimentContext.Inherited {
		t.Errorf("control: child context = %+v, want the control variant inherited", child.ExperimentContext)
	}
}

func TestDelegateIndependentLeavesChildUnenrolled(t *testing.T) {
	store := memstore.New()
	exps := experimentstore.NewMemory()
	seedExperiment(t, exps, "exp_1", experiment.PropagationIndependent)
	seedEnrolledParent(t, store, "exp_1")
	svc := delegation.NewService(store, delegation.Options{
		IDFunc: func() string { return "sess_child" }, Experiments: exps,
	})
	if child := delegateChild(t, svc); child.ExperimentContext != nil {
		t.Errorf("independent: child context = %+v, want nil — routed afresh", child.ExperimentContext)
	}
}

func TestDelegateUnenrolledParentYieldsNoContext(t *testing.T) {
	store := memstore.New()
	exps := experimentstore.NewMemory()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	svc := delegation.NewService(store, delegation.Options{
		IDFunc: func() string { return "sess_child" }, Experiments: exps,
	})
	if child := delegateChild(t, svc); child.ExperimentContext != nil {
		t.Errorf("child context = %+v, want nil — the parent is not enrolled", child.ExperimentContext)
	}
}

func TestDelegateNoExperimentStoreYieldsNoContext(t *testing.T) {
	store := memstore.New()
	seedEnrolledParent(t, store, "exp_1")
	svc := delegation.NewService(store, delegation.Options{
		IDFunc: func() string { return "sess_child" },
	})
	if child := delegateChild(t, svc); child.ExperimentContext != nil {
		t.Errorf("child context = %+v, want nil — no experiment store wired", child.ExperimentContext)
	}
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

// TestDelegateChildInheritsParentRootSessionID_spec_8_9_1010 pins
// §8.9 line 1010: every node in a delegation tree shares the same
// RootSessionID. The child created by Delegate carries the parent's
// RootSessionID rather than minting a fresh one. A grandchild
// delegated from the child continues to inherit the same tree root.
// F-8.9.8.
func TestDelegateChildInheritsParentRootSessionID_spec_8_9_1010(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_root_p", "", "claude", "pool-a", isolation.ProfileSandboxed)
	svc := newService(t, store, func() string { return "sess_kid" })
	res, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_root_p",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
	})
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if res.Child.RootSessionID != "sess_root_p" {
		t.Errorf("child.RootSessionID = %q, want sess_root_p (inherited from parent root)", res.Child.RootSessionID)
	}
	// Grandchild delegated from the child must carry the same root.
	if _, err := store.Update(context.Background(), "acme", "sess_kid", func(s *sessionstore.Session) error {
		s.State = session.StateRunning
		return nil
	}); err != nil {
		t.Fatalf("Update kid to running: %v", err)
	}
	svc2 := newService(t, store, func() string { return "sess_gc" })
	res2, err := svc2.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_kid",
		RuntimeRef:       "claude",
		PoolRef:          "pool-c",
		IsolationProfile: isolation.ProfileSandboxed,
	})
	if err != nil {
		t.Fatalf("Delegate grandchild: %v", err)
	}
	if res2.Child.RootSessionID != "sess_root_p" {
		t.Errorf("grandchild.RootSessionID = %q, want sess_root_p (deep tree root)", res2.Child.RootSessionID)
	}
}

// TestDelegateStampsDelegationDepth_spec_10_7_905 pins §10.7 lines
// 868/905: a delegated child's delegation_depth is parent.depth+1, fixed
// at admission. A root parent is depth 0, its child depth 1, and a
// grandchild depth 2. The eval endpoint copies this onto EvalResult so
// the Results API delegation_depth filter operates on truthful data.
// F-10.7.5.
func TestDelegateStampsDelegationDepth_spec_10_7_905(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_root_p", "", "claude", "pool-a", isolation.ProfileSandboxed)
	root, _ := store.Get(context.Background(), "acme", "sess_root_p")
	if root.DelegationDepth != 0 {
		t.Fatalf("root.DelegationDepth = %d, want 0", root.DelegationDepth)
	}
	svc := newService(t, store, func() string { return "sess_kid" })
	res, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_root_p",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
	})
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if res.Child.DelegationDepth != 1 {
		t.Errorf("child.DelegationDepth = %d, want 1", res.Child.DelegationDepth)
	}
	// The stamped depth must survive the round-trip through the store.
	persisted, _ := store.Get(context.Background(), "acme", "sess_kid")
	if persisted.DelegationDepth != 1 {
		t.Errorf("persisted child.DelegationDepth = %d, want 1", persisted.DelegationDepth)
	}
	if _, err := store.Update(context.Background(), "acme", "sess_kid", func(s *sessionstore.Session) error {
		s.State = session.StateRunning
		return nil
	}); err != nil {
		t.Fatalf("Update kid to running: %v", err)
	}
	svc2 := newService(t, store, func() string { return "sess_gc" })
	res2, err := svc2.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_kid",
		RuntimeRef:       "claude",
		PoolRef:          "pool-c",
		IsolationProfile: isolation.ProfileSandboxed,
	})
	if err != nil {
		t.Fatalf("Delegate grandchild: %v", err)
	}
	if res2.Child.DelegationDepth != 2 {
		t.Errorf("grandchild.DelegationDepth = %d, want 2", res2.Child.DelegationDepth)
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

// countingStore wraps a sessionstore.Store and counts Get calls so
// lineage-walk bounding tests can assert the store-lookup fan-out is
// bounded.
type countingStore struct {
	inner sessionstore.Store
	gets  int
}

func (c *countingStore) Create(ctx context.Context, s sessionstore.Session) error {
	return c.inner.Create(ctx, s)
}

func (c *countingStore) Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	c.gets++
	return c.inner.Get(ctx, tenantID, id)
}

func (c *countingStore) GetByID(ctx context.Context, id string) (sessionstore.Session, error) {
	return c.inner.GetByID(ctx, id)
}

func (c *countingStore) Update(ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	return c.inner.Update(ctx, tenantID, id, mutate)
}

func (c *countingStore) List(ctx context.Context, tenantID string, filter sessionstore.ListFilter) ([]sessionstore.Session, error) {
	return c.inner.List(ctx, tenantID, filter)
}

func (c *countingStore) ListByRoot(ctx context.Context, tenantID, rootSessionID string) ([]sessionstore.Session, error) {
	return c.inner.ListByRoot(ctx, tenantID, rootSessionID)
}

func (c *countingStore) Delete(ctx context.Context, tenantID, id string) error {
	return c.inner.Delete(ctx, tenantID, id)
}

func (c *countingStore) DeleteByUser(ctx context.Context, tenantID, userID string) (int, error) {
	return c.inner.DeleteByUser(ctx, tenantID, userID)
}

func (c *countingStore) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	return c.inner.DeleteByTenant(ctx, tenantID)
}

func (c *countingStore) GetActiveSlotsByPod(ctx context.Context, podID string) (int, error) {
	return c.inner.GetActiveSlotsByPod(ctx, podID)
}

func (c *countingStore) ReserveSlotUnderLock(ctx context.Context, podID string, maxConcurrent int32) (int32, bool, error) {
	return c.inner.ReserveSlotUnderLock(ctx, podID, maxConcurrent)
}

func (c *countingStore) PoolDrainStats(ctx context.Context, poolRef string) (int, time.Time, error) {
	return c.inner.PoolDrainStats(ctx, poolRef)
}

func (c *countingStore) CountActiveSessions(ctx context.Context, tenantID string) (int, error) {
	return c.inner.CountActiveSessions(ctx, tenantID)
}

func (c *countingStore) CountActiveSessionsByUser(ctx context.Context, tenantID, userID string) (int, error) {
	return c.inner.CountActiveSessionsByUser(ctx, tenantID, userID)
}

func (c *countingStore) CountActiveSessionsByRuntime(ctx context.Context, tenantID, runtimeRef string) (int, error) {
	return c.inner.CountActiveSessionsByRuntime(ctx, tenantID, runtimeRef)
}

func (c *countingStore) CountActiveSessionsGlobal(ctx context.Context) (int, error) {
	return c.inner.CountActiveSessionsGlobal(ctx)
}

func (c *countingStore) CountActiveSessionsInRecoveryGlobal(ctx context.Context) (int, error) {
	return c.inner.CountActiveSessionsInRecoveryGlobal(ctx)
}

func (c *countingStore) CountActiveDelegatedChildrenByUser(ctx context.Context, tenantID, userID string) (int, error) {
	return c.inner.CountActiveDelegatedChildrenByUser(ctx, tenantID, userID)
}

// spec: §8.2 line 57 — lineage walk uses ParentSessionID from parent
// up to root, defended against cycles by a visited set. F-8.2.16 —
// the walk is additionally bounded by the active maxDepth ceiling
// plus a safety margin so a pathological chain does not produce an
// unbounded store fan-out per delegate_task call.
func TestDelegateBoundsLineageWalkUnderDeepChain_spec_8_2_57(t *testing.T) {
	inner := memstore.New()
	store := &countingStore{inner: inner}
	// Seed a chain 50 deep. The default Helm maxDepth is 10, so the
	// walk must stop at 10 + safety margin (4) = 14 lookups regardless
	// of total chain length.
	const chainDepth = 50
	prev := ""
	for i := 0; i < chainDepth; i++ {
		id := fmt.Sprintf("sess_%03d", i)
		seedParent(t, inner, id, prev, "r0", "p0", isolation.ProfileSandboxed)
		prev = id
	}
	parentID := fmt.Sprintf("sess_%03d", chainDepth-1)
	svc := newService(t, store, func() string { return "sess_child" })

	getsBefore := store.gets
	_, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  parentID,
		RuntimeRef:       "r_new",
		PoolRef:          "p_new",
		IsolationProfile: isolation.ProfileSandboxed,
	})
	// Expect DepthExceededError because depth way exceeds 10.
	var de *lease.DepthExceededError
	if !errors.As(err, &de) {
		t.Fatalf("expected DepthExceededError, got %v", err)
	}
	used := store.gets - getsBefore
	// One Get for the parent lookup + (DefaultMaxDepth+safety-1) walk
	// hops at most. Concretely: 1 (parent) + 14 (walk) = 15 upper bound.
	if used > delegation.DefaultMaxDepth+5 {
		t.Errorf("buildLineage walk unbounded: store.Get called %d times for a %d-deep chain, want ≤ %d",
			used, chainDepth, delegation.DefaultMaxDepth+5)
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

func TestDelegatePropagatesTracingContext(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	parentCtx := map[string]string{"trace-id": "t-root", "span_id": "s-1"}
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "user_alice",
		State:      session.StateRunning,
		RuntimeRef: "claude", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		TracingContext: parentCtx,
		CreatedAt:      now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
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
	// §8.3: the child inherits the parent's tracingContext.
	if res.Child.TracingContext["trace-id"] != "t-root" || res.Child.TracingContext["span_id"] != "s-1" {
		t.Errorf("child tracingContext = %v, want the parent's entries", res.Child.TracingContext)
	}
	// The child's map is a copy — mutating it must not reach the parent.
	res.Child.TracingContext["trace-id"] = "mutated"
	if parentCtx["trace-id"] != "t-root" {
		t.Error("child tracingContext aliases the parent's map")
	}
}

// spec: §5.1 line 69 / §8.2 — the resolved target runtime's
// allowSelfRecursion flows into the cycle gate's LayerRuntime input. The
// rejection's EffectiveSettings reflects the runtime's declared value
// when a runtime registry is wired into the service.
func TestDelegateCycleReadsRuntimeAllowSelfRecursion(t *testing.T) {
	newSvc := func(rtAllow bool, withRegistry bool) *delegation.Service {
		store := memstore.New()
		seedParent(t, store, "sess_root", "", "claude", "pool-a", isolation.ProfileSandboxed)
		seedParent(t, store, "sess_parent", "sess_root", "gemini", "pool-b", isolation.ProfileSandboxed)
		opts := delegation.Options{
			Clock:  func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
			IDFunc: func() string { return "sess_child" },
		}
		if withRegistry {
			runtimes := runtimestore.NewMemory()
			if err := runtimes.Create(context.Background(), runtimestore.Runtime{
				Name: "claude", Image: "lenny/claude@sha256:abc", AllowSelfRecursion: rtAllow,
			}); err != nil {
				t.Fatalf("seed runtime: %v", err)
			}
			opts.Runtimes = runtimes
		}
		return delegation.NewService(store, opts)
	}

	selfRecursive := delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "claude",
		PoolRef:          "pool-a",
		IsolationProfile: isolation.ProfileSandboxed,
	}

	// Runtime declares allowSelfRecursion: true — the gate still rejects
	// (platform/policy layers remain false), but EffectiveSettings shows
	// the runtime layer was read from the registry as true.
	_, err := newSvc(true, true).Delegate(context.Background(), "acme", selfRecursive)
	var rej *cycle.Rejection
	if !errors.As(err, &rej) {
		t.Fatalf("got %v, want cycle.Rejection", err)
	}
	if !rej.EffectiveSettings.RuntimeAllowSelfRec {
		t.Errorf("RuntimeAllowSelfRec must reflect the registry value true, got %+v", rej.EffectiveSettings)
	}

	// Runtime declares false — the runtime layer is false.
	_, err = newSvc(false, true).Delegate(context.Background(), "acme", selfRecursive)
	if !errors.As(err, &rej) {
		t.Fatalf("got %v, want cycle.Rejection", err)
	}
	if rej.EffectiveSettings.RuntimeAllowSelfRec {
		t.Error("RuntimeAllowSelfRec must be false when the runtime declares false")
	}

	// No registry wired — the runtime layer falls back to the
	// conservative false default.
	_, err = newSvc(true, false).Delegate(context.Background(), "acme", selfRecursive)
	if !errors.As(err, &rej) {
		t.Fatalf("got %v, want cycle.Rejection", err)
	}
	if rej.EffectiveSettings.RuntimeAllowSelfRec {
		t.Error("RuntimeAllowSelfRec must default false with no runtime registry")
	}
}

// recorder captures the §8.2 metric calls so tests can assert emission
// without depending on a Prometheus registry. spec: §8.2 / §16.1.
type recorder struct {
	depths []depthObs
	blocks []blockObs
}

type depthObs struct {
	pool  string
	depth int
}

type blockObs struct {
	pool, tenantID, layer, mode string
}

func (r *recorder) ObserveDelegationDepth(pool string, depth int) {
	r.depths = append(r.depths, depthObs{pool, depth})
}

func (r *recorder) IncDelegationWouldHaveBlocked(pool, tenantID, layer, mode string) {
	r.blocks = append(r.blocks, blockObs{pool, tenantID, layer, mode})
}

// spec: §8.2 line 58 — the child-token exchange requires the parent's
// authenticated user JWT as `subject_token`. A userless parent must be
// rejected at admission with ErrParentNoUser; an empty req.UserID
// inherits-or-rejects but does NOT substitute the parent identity.
func TestDelegateRejectsUserlessParent(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent", TenantID: "acme",
		// UserID intentionally omitted.
		State:      session.StateRunning,
		RuntimeRef: "claude", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed userless parent: %v", err)
	}
	svc := newService(t, store, func() string { return "sess_child" })

	_, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
	})
	if !errors.Is(err, delegation.ErrParentNoUser) {
		t.Fatalf("Delegate userless parent: got %v, want ErrParentNoUser", err)
	}

	// A caller-supplied req.UserID must NOT bypass the gate — the
	// spec ties the child to the authenticated parent identity.
	_, err = svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
		UserID:           "spoofed_caller",
	})
	if !errors.Is(err, delegation.ErrParentNoUser) {
		t.Fatalf("spoofed req.UserID must not bypass ErrParentNoUser: got %v", err)
	}
}

// spec: §8.2 / §16.1 line 27 — Delegate observes the admitted child's
// depth onto lenny_delegation_depth at admission time.
func TestDelegateRecordsAdmittedDepth(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_root", "", "claude", "pool-a", isolation.ProfileSandboxed)
	seedParent(t, store, "sess_parent", "sess_root", "gemini", "pool-b", isolation.ProfileSandboxed)
	rec := &recorder{}
	svc := delegation.NewService(store, delegation.Options{
		Clock:   func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:  func() string { return "sess_child" },
		Metrics: rec,
	})
	res, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "research",
		PoolRef:          "pool-c",
		IsolationProfile: isolation.ProfileSandboxed,
	})
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if res.Depth != 2 {
		t.Fatalf("child depth = %d, want 2", res.Depth)
	}
	if len(rec.depths) != 1 || rec.depths[0] != (depthObs{pool: "pool-c", depth: 2}) {
		t.Errorf("depths = %+v, want [{pool-c 2}]", rec.depths)
	}
	if len(rec.blocks) != 0 {
		t.Errorf("non-self-recursive hop must not emit would-have-blocked, got %+v", rec.blocks)
	}
}

// spec: §8.2 line 70 — the gateway emits one
// lenny_delegation_would_have_blocked_total row per failing layer of
// the three-layer AND gate on a self-recursive rejection (enforce
// mode), so per-tenant rejection-attribution dashboards can read the
// per-layer breakdown. spec: §16.1 line 79.
func TestDelegateRecordsCycleRejectionAttribution(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_root", "", "claude", "pool-a", isolation.ProfileSandboxed)
	seedParent(t, store, "sess_parent", "sess_root", "gemini", "pool-b", isolation.ProfileSandboxed)
	rec := &recorder{}
	svc := delegation.NewService(store, delegation.Options{
		Clock:     func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:    func() string { return "sess_child" },
		Metrics:   rec,
		CycleMode: cycle.ModeEnforce,
	})
	// Delegating back to (claude, pool-a) creates a cycle. With no
	// runtime registry the runtime layer defaults false; platform and
	// policy layers default false. Expect three would-have-blocked
	// rows on the rejected hop, all stamped mode=enforce.
	_, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "claude",
		PoolRef:          "pool-a",
		IsolationProfile: isolation.ProfileSandboxed,
	})
	if err == nil {
		t.Fatal("Delegate must reject the self-recursive hop")
	}
	if len(rec.blocks) != 3 {
		t.Fatalf("would-have-blocked rows = %d, want 3 (platform/runtime/policy under enforce): %+v",
			len(rec.blocks), rec.blocks)
	}
	wantLayers := map[string]bool{"platform": false, "runtime": false, "policy": false}
	for _, b := range rec.blocks {
		if b.pool != "pool-a" || b.tenantID != "acme" || b.mode != "enforce" {
			t.Errorf("attribution row %+v: want pool=pool-a tenant=acme mode=enforce", b)
		}
		if _, ok := wantLayers[b.layer]; !ok {
			t.Errorf("unexpected layer %q in attribution row", b.layer)
			continue
		}
		wantLayers[b.layer] = true
	}
	for layer, seen := range wantLayers {
		if !seen {
			t.Errorf("missing attribution row for layer %q", layer)
		}
	}
	// A rejected hop must not emit a depth observation.
	if len(rec.depths) != 0 {
		t.Errorf("rejected hop emitted depth = %+v, want none", rec.depths)
	}
}

// spec: §8.2 line 70 — under mode=warn the delegation is admitted, but
// the same per-layer breakdown is recorded for the diagnostic rollout.
func TestDelegateRecordsCycleWarnModeAttribution(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_root", "", "claude", "pool-a", isolation.ProfileSandboxed)
	seedParent(t, store, "sess_parent", "sess_root", "gemini", "pool-b", isolation.ProfileSandboxed)
	rec := &recorder{}
	svc := delegation.NewService(store, delegation.Options{
		Clock:     func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:    func() string { return "sess_child" },
		Metrics:   rec,
		CycleMode: cycle.ModeWarn,
	})
	res, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "claude",
		PoolRef:          "pool-a",
		IsolationProfile: isolation.ProfileSandboxed,
	})
	if err != nil {
		t.Fatalf("warn-mode self-recursive hop must be admitted: %v", err)
	}
	if res.Child.ID != "sess_child" {
		t.Errorf("warn-mode admission missing child: %+v", res)
	}
	if len(rec.blocks) != 3 {
		t.Fatalf("warn-mode rows = %d, want 3: %+v", len(rec.blocks), rec.blocks)
	}
	for _, b := range rec.blocks {
		if b.mode != "warn" {
			t.Errorf("warn-mode attribution row %+v: mode must be warn", b)
		}
	}
	// Admitted under warn → depth observation is recorded.
	if len(rec.depths) != 1 || rec.depths[0] != (depthObs{pool: "pool-a", depth: 2}) {
		t.Errorf("warn-mode admission depths = %+v, want [{pool-a 2}]", rec.depths)
	}
}

// spec: §8.2 / §16.1 — under mode=permissive cycle detection is
// disabled and lenny_delegation_would_have_blocked_total is NOT
// emitted (the §16.1 catalog row marks this explicit).
func TestDelegatePermissiveModeSkipsAttribution(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_root", "", "claude", "pool-a", isolation.ProfileSandboxed)
	seedParent(t, store, "sess_parent", "sess_root", "gemini", "pool-b", isolation.ProfileSandboxed)
	rec := &recorder{}
	svc := delegation.NewService(store, delegation.Options{
		Clock:     func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:    func() string { return "sess_child" },
		Metrics:   rec,
		CycleMode: cycle.ModePermissive,
	})
	if _, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "claude",
		PoolRef:          "pool-a",
		IsolationProfile: isolation.ProfileSandboxed,
	}); err != nil {
		t.Fatalf("permissive-mode must admit: %v", err)
	}
	if len(rec.blocks) != 0 {
		t.Errorf("permissive-mode must not emit would-have-blocked, got %+v", rec.blocks)
	}
	// Depth is still observed on the admitted hop.
	if len(rec.depths) != 1 {
		t.Errorf("permissive-mode depths = %+v, want one observation", rec.depths)
	}
}

// fakeExperimentRouter is a delegation.ExperimentRouter test double:
// the routing closure is provided per-test so behaviour can be set.
type fakeExperimentRouter struct {
	calls int
	route func(row *sessionstore.Session) error
}

func (f *fakeExperimentRouter) ApplyExperimentRouting(_ context.Context, row *sessionstore.Session) error {
	f.calls++
	if f.route != nil {
		return f.route(row)
	}
	return nil
}

// spec: §8.2 line 90 / §10.7 — under `independent` propagation the
// gateway invokes the §10.7 ExperimentRouter on the child afresh; the
// child may newly enroll in a different experiment. F-8.2.10.
func TestDelegateIndependentRoutesChildAfresh_spec_8_2_F_8_2_10(t *testing.T) {
	store := memstore.New()
	exps := experimentstore.NewMemory()
	seedExperiment(t, exps, "exp_parent", experiment.PropagationIndependent)
	seedEnrolledParent(t, store, "exp_parent")
	router := &fakeExperimentRouter{route: func(row *sessionstore.Session) error {
		row.ExperimentContext = &sessionstore.ExperimentContext{
			ExperimentID: "exp_child", VariantID: "treatment",
		}
		return nil
	}}
	svc := delegation.NewService(store, delegation.Options{
		IDFunc:           func() string { return "sess_child" },
		Experiments:      exps,
		ExperimentRouter: router,
	})

	child := delegateChild(t, svc)
	if router.calls != 1 {
		t.Fatalf("router called %d times, want 1 under independent propagation", router.calls)
	}
	if child.ExperimentContext == nil ||
		child.ExperimentContext.ExperimentID != "exp_child" ||
		child.ExperimentContext.VariantID != "treatment" {
		t.Errorf("child context = %+v, want fresh routing onto exp_child/treatment", child.ExperimentContext)
	}
}

// spec: §8.2 line 90 / §10.7 — when the parent carries no experiment
// context, the child is still evaluated by the ExperimentRouter so it
// may pick up a newly matching experiment.
func TestDelegateUnenrolledParentRoutesChildAfresh_spec_8_2_F_8_2_10(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	router := &fakeExperimentRouter{route: func(row *sessionstore.Session) error {
		row.ExperimentContext = &sessionstore.ExperimentContext{
			ExperimentID: "exp_child", VariantID: "treatment",
		}
		return nil
	}}
	svc := delegation.NewService(store, delegation.Options{
		IDFunc:           func() string { return "sess_child" },
		ExperimentRouter: router,
	})
	child := delegateChild(t, svc)
	if router.calls != 1 {
		t.Fatalf("router called %d times, want 1 (parent unenrolled)", router.calls)
	}
	if child.ExperimentContext == nil ||
		child.ExperimentContext.ExperimentID != "exp_child" {
		t.Errorf("child context = %+v, want fresh routing", child.ExperimentContext)
	}
}

// spec: §8.2 line 90 / §10.7 — `inherit` and `control` propagation
// modes leave the child with the parent's context and skip the
// ExperimentRouter (the child must adopt the parent's variant
// verbatim, not be re-routed).
func TestDelegateInheritSkipsExperimentRouter_spec_8_2_F_8_2_10(t *testing.T) {
	for _, mode := range []experiment.Propagation{
		experiment.PropagationInherit,
		experiment.PropagationControl,
	} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			store := memstore.New()
			exps := experimentstore.NewMemory()
			seedExperiment(t, exps, "exp_p", mode)
			seedEnrolledParent(t, store, "exp_p")
			router := &fakeExperimentRouter{}
			svc := delegation.NewService(store, delegation.Options{
				IDFunc:           func() string { return "sess_child" },
				Experiments:      exps,
				ExperimentRouter: router,
			})
			child := delegateChild(t, svc)
			if router.calls != 0 {
				t.Errorf("router called %d times under %s, want 0", router.calls, mode)
			}
			if child.ExperimentContext == nil ||
				child.ExperimentContext.ExperimentID != "exp_p" {
				t.Errorf("child context = %+v, want propagated parent context", child.ExperimentContext)
			}
		})
	}
}

// spec: §8.2 line 90 — when the router fails closed (e.g. §10.7
// VARIANT_ISOLATION_UNAVAILABLE), the delegation must abort rather
// than create an unenrolled child.
func TestDelegateRouterFailureAbortsDelegation_spec_8_2_F_8_2_10(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	failure := errors.New("variant_isolation_unavailable")
	router := &fakeExperimentRouter{route: func(_ *sessionstore.Session) error { return failure }}
	svc := delegation.NewService(store, delegation.Options{
		IDFunc:           func() string { return "sess_child" },
		ExperimentRouter: router,
	})
	_, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
	})
	if !errors.Is(err, failure) {
		t.Fatalf("Delegate err = %v, want router failure to propagate", err)
	}
	if _, gerr := store.Get(context.Background(), "acme", "sess_child"); gerr == nil {
		t.Error("a router-aborted delegation must not persist the child")
	}
}

// spec: §8.2 line 50 — `lenny/delegate_task` rejects `type: mcp`
// targets with `target_not_an_agent`. The delegation Service is the
// defence-in-depth call site so non-MCP entry points (REST, future
// SDKs) cannot bypass the check. F-8.2.8 / F-8.5.4.
func TestDelegateRejectsTypeMCPTarget_spec_8_2_F_8_2_8(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "fs-mcp", Type: runtimestore.TypeMCP, Image: "lenny/fs-mcp@sha256:abc",
	}); err != nil {
		t.Fatalf("seed mcp runtime: %v", err)
	}
	svc := delegation.NewService(store, delegation.Options{
		IDFunc:   func() string { return "sess_child" },
		Runtimes: runtimes,
	})
	_, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "fs-mcp",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
	})
	if !errors.Is(err, delegation.ErrTargetNotAgent) {
		t.Fatalf("Delegate err = %v, want ErrTargetNotAgent", err)
	}
	if _, gerr := store.Get(context.Background(), "acme", "sess_child"); gerr == nil {
		t.Error("a type:mcp delegation must not create a child session")
	}
}

// An agent runtime resolves normally — the type check is targeted, not
// a catch-all.
func TestDelegateAdmitsTypeAgentTarget_spec_8_2_F_8_2_8(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "gemini", Type: runtimestore.TypeAgent, Image: "lenny/gemini@sha256:def",
	}); err != nil {
		t.Fatalf("seed agent runtime: %v", err)
	}
	svc := delegation.NewService(store, delegation.Options{
		IDFunc:   func() string { return "sess_child" },
		Runtimes: runtimes,
	})
	res, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
	})
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if res.Child.ID != "sess_child" {
		t.Errorf("child = %+v, want admitted", res.Child)
	}
}

// TestDelegatePlatformLayerWiredFromOptions verifies the §8.2 LayerPlatform
// input to the three-layer cycle gate flows from
// Options.PlatformAllowSelfRecursion. The platform layer is one of the
// three AND gates: a self-recursive hop is admitted only when every
// layer is true.
//
// spec: §8.2 line 73 (LayerPlatform); F-8.1.3 / F-8.2.3.
func TestDelegatePlatformLayerWiredFromOptions_spec_8_2_F_8_1_3(t *testing.T) {
	newSvc := func(platformAllow, rtAllow, polAllow bool) *delegation.Service {
		store := memstore.New()
		seedParent(t, store, "sess_root", "", "claude", "pool-a", isolation.ProfileSandboxed)
		seedParent(t, store, "sess_parent", "sess_root", "gemini", "pool-b", isolation.ProfileSandboxed)
		runtimes := runtimestore.NewMemory()
		if err := runtimes.Create(context.Background(), runtimestore.Runtime{
			Name:                "claude",
			Image:               "lenny/claude@sha256:abc",
			AllowSelfRecursion:  rtAllow,
			DelegationPolicyRef: "policy-x",
		}); err != nil {
			t.Fatalf("seed runtime: %v", err)
		}
		policies := delegationpolicystore.NewMemory()
		if err := policies.Create(context.Background(), delegationpolicystore.DelegationPolicy{
			TenantID: "acme", Name: "policy-x", AllowSelfRecursion: polAllow,
		}); err != nil {
			t.Fatalf("seed policy: %v", err)
		}
		return delegation.NewService(store, delegation.Options{
			Clock:                      func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
			IDFunc:                     func() string { return "sess_child" },
			Runtimes:                   runtimes,
			Policies:                   policies,
			PlatformAllowSelfRecursion: platformAllow,
		})
	}

	selfRec := delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "claude",
		PoolRef:          "pool-a",
		IsolationProfile: isolation.ProfileSandboxed,
	}

	// All three layers true — the hop is admitted under enforce.
	res, err := newSvc(true, true, true).Delegate(context.Background(), "acme", selfRec)
	if err != nil {
		t.Fatalf("all-true: got %v, want admitted", err)
	}
	if res.Child.ID == "" {
		t.Error("all-true: child must be created")
	}

	// Platform false — the hop is rejected with BlockedBy=platform.
	_, err = newSvc(false, true, true).Delegate(context.Background(), "acme", selfRec)
	var rej *cycle.Rejection
	if !errors.As(err, &rej) {
		t.Fatalf("platform-false: got %v, want cycle.Rejection", err)
	}
	if rej.EffectiveSettings.PlatformAllowSelfRec {
		t.Error("PlatformAllowSelfRec must be false when the option is false")
	}
	if rej.BlockedBy != cycle.LayerPlatform {
		t.Errorf("BlockedBy = %q, want %q (platform-first canonical order)", rej.BlockedBy, cycle.LayerPlatform)
	}
}

// TestDelegatePolicyLayerWiredFromStore verifies the §8.2 LayerPolicy
// input reads DelegationPolicy.AllowSelfRecursion when the resolved
// Runtime carries a DelegationPolicyRef. With platform+runtime both
// true, the policy layer alone decides admission.
//
// spec: §8.2 line 75 (LayerPolicy); F-8.1.3 / F-8.2.3.
func TestDelegatePolicyLayerWiredFromStore_spec_8_2_F_8_2_3(t *testing.T) {
	build := func(polRef string, polAllow bool, withPolicy bool) *delegation.Service {
		store := memstore.New()
		seedParent(t, store, "sess_root", "", "claude", "pool-a", isolation.ProfileSandboxed)
		seedParent(t, store, "sess_parent", "sess_root", "gemini", "pool-b", isolation.ProfileSandboxed)
		runtimes := runtimestore.NewMemory()
		if err := runtimes.Create(context.Background(), runtimestore.Runtime{
			Name:                "claude",
			Image:               "lenny/claude@sha256:abc",
			AllowSelfRecursion:  true,
			DelegationPolicyRef: polRef,
		}); err != nil {
			t.Fatalf("seed runtime: %v", err)
		}
		opts := delegation.Options{
			IDFunc:                     func() string { return "sess_child" },
			Runtimes:                   runtimes,
			PlatformAllowSelfRecursion: true,
		}
		if withPolicy {
			policies := delegationpolicystore.NewMemory()
			if err := policies.Create(context.Background(), delegationpolicystore.DelegationPolicy{
				TenantID: "acme", Name: "policy-x", AllowSelfRecursion: polAllow,
			}); err != nil {
				t.Fatalf("seed policy: %v", err)
			}
			opts.Policies = policies
		}
		return delegation.NewService(store, opts)
	}
	req := delegation.Request{
		ParentSessionID: "sess_parent", RuntimeRef: "claude", PoolRef: "pool-a",
		IsolationProfile: isolation.ProfileSandboxed,
	}

	t.Run("policy-true admits", func(t *testing.T) {
		if _, err := build("policy-x", true, true).Delegate(context.Background(), "acme", req); err != nil {
			t.Fatalf("policy-true: got %v, want admitted", err)
		}
	})
	t.Run("policy-false blocks at policy layer", func(t *testing.T) {
		_, err := build("policy-x", false, true).Delegate(context.Background(), "acme", req)
		var rej *cycle.Rejection
		if !errors.As(err, &rej) {
			t.Fatalf("policy-false: got %v, want cycle.Rejection", err)
		}
		if rej.EffectiveSettings.PolicyAllowSelfRec {
			t.Error("PolicyAllowSelfRec must be false when the policy declares false")
		}
		if rej.BlockedBy != cycle.LayerPolicy {
			t.Errorf("BlockedBy = %q, want %q", rej.BlockedBy, cycle.LayerPolicy)
		}
	})
	t.Run("missing policy ref leaves layer false", func(t *testing.T) {
		// Runtime carries no DelegationPolicyRef and the policy store is
		// nil — the policy layer remains at its conservative false default.
		_, err := build("", false, false).Delegate(context.Background(), "acme", req)
		var rej *cycle.Rejection
		if !errors.As(err, &rej) {
			t.Fatalf("missing-ref: got %v, want cycle.Rejection", err)
		}
		if rej.EffectiveSettings.PolicyAllowSelfRec {
			t.Error("PolicyAllowSelfRec must default false when no DelegationPolicyRef is set")
		}
	})
	t.Run("unresolvable policy leaves layer false", func(t *testing.T) {
		// Runtime references a policy that does not exist in the store.
		// The lookup fails and the layer remains false.
		_, err := build("missing-policy", true, true).Delegate(context.Background(), "acme", req)
		var rej *cycle.Rejection
		if !errors.As(err, &rej) {
			t.Fatalf("missing-policy: got %v, want cycle.Rejection", err)
		}
		if rej.EffectiveSettings.PolicyAllowSelfRec {
			t.Error("PolicyAllowSelfRec must default false when the policy ref is unresolvable")
		}
	})
}

// TestDelegateMaxDepthFallsThroughToHelmDefault verifies §8.2.bis line 89:
// a delegation that omits explicit MaxDepth still receives a bounded
// chain via the Helm fallback (DefaultMaxDepth). A child at depth equal
// to the fallback is rejected; a child below it is admitted.
//
// spec: §8.2.bis line 89; F-8.1.4 / F-8.2.6.
func TestDelegateMaxDepthFallsThroughToHelmDefault_spec_8_2_bis_F_8_1_4(t *testing.T) {
	mk := func(fallback int, lineageLen int) (*delegation.Service, string) {
		store := memstore.New()
		parent := ""
		for i := 0; i < lineageLen; i++ {
			id := "sess_" + string(rune('a'+i))
			seedParent(t, store, id, parent, "r"+string(rune('0'+i)), "p"+string(rune('0'+i)), isolation.ProfileSandboxed)
			parent = id
		}
		return delegation.NewService(store, delegation.Options{
			Clock:           func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
			IDFunc:          func() string { return "sess_child" },
			DefaultMaxDepth: fallback,
		}), parent
	}

	t.Run("caller omits MaxDepth — Helm fallback admits within budget", func(t *testing.T) {
		// 3-deep lineage (root → a → b), parent depth = 2; fallback=10
		// admits a child at depth 3.
		svc, parentID := mk(10, 3)
		res, err := svc.Delegate(context.Background(), "acme", delegation.Request{
			ParentSessionID:  parentID,
			RuntimeRef:       "r9",
			PoolRef:          "p9",
			IsolationProfile: isolation.ProfileSandboxed,
			// MaxDepth omitted — Helm fallback applies.
		})
		if err != nil {
			t.Fatalf("Delegate: %v", err)
		}
		if res.Depth != 3 {
			t.Errorf("Depth = %d, want 3", res.Depth)
		}
	})
	t.Run("caller omits MaxDepth — Helm fallback rejects at the cap", func(t *testing.T) {
		// Lineage depth equal to fallback (parent at depth fallback-1
		// = 2 with fallback=3) — a child would be depth 3, exceeding 3.
		// Actually depth-3 admission requires CheckDepth(parent=2,
		// resolvedMax=3) which succeeds, so build a 4-deep lineage:
		// parent depth = 3, fallback = 3 → child at depth 4 > 3 rejects.
		svc, parentID := mk(3, 4)
		_, err := svc.Delegate(context.Background(), "acme", delegation.Request{
			ParentSessionID:  parentID,
			RuntimeRef:       "r9",
			PoolRef:          "p9",
			IsolationProfile: isolation.ProfileSandboxed,
		})
		var de *lease.DepthExceededError
		if !errors.As(err, &de) {
			t.Fatalf("got %v, want DepthExceededError under Helm fallback", err)
		}
		if de.Max != 3 {
			t.Errorf("DepthExceededError.Max = %d, want 3", de.Max)
		}
	})
	t.Run("zero Options.DefaultMaxDepth resolves to compile-time DefaultMaxDepth (10)", func(t *testing.T) {
		// fallback=0 must round up to delegation.DefaultMaxDepth (10) so
		// no chain grows unbounded.
		svc, parentID := mk(0, 3)
		res, err := svc.Delegate(context.Background(), "acme", delegation.Request{
			ParentSessionID:  parentID,
			RuntimeRef:       "r9",
			PoolRef:          "p9",
			IsolationProfile: isolation.ProfileSandboxed,
		})
		if err != nil {
			t.Fatalf("Delegate: %v", err)
		}
		if res.Depth != 3 {
			t.Errorf("Depth = %d, want 3", res.Depth)
		}
		// And an 11-deep lineage (parent depth 10) hits the
		// DefaultMaxDepth=10 ceiling: child would be depth 11 > 10.
		// Use a fresh runtime/pool tuple for the child so cycle
		// detection (each ancestor uses r{i}/p{i}) does not fire and
		// the depth check is the only gate.
		deepSvc, deepParent := mk(0, 11)
		_, err = deepSvc.Delegate(context.Background(), "acme", delegation.Request{
			ParentSessionID:  deepParent,
			RuntimeRef:       "rZ",
			PoolRef:          "pZ",
			IsolationProfile: isolation.ProfileSandboxed,
		})
		var de *lease.DepthExceededError
		if !errors.As(err, &de) {
			t.Fatalf("got %v, want DepthExceededError at the DefaultMaxDepth ceiling", err)
		}
		if de.Max != delegation.DefaultMaxDepth {
			t.Errorf("DepthExceededError.Max = %d, want %d", de.Max, delegation.DefaultMaxDepth)
		}
	})
}

// TestDelegateExplicitMaxDepthBeatsHelmFallback verifies §8.2.bis
// precedence: the explicit client-supplied MaxDepth overrides the Helm
// fallback. A small explicit value can reject a chain the fallback
// would have admitted.
//
// spec: §8.2.bis lines 81-89; F-8.1.4 / F-8.2.6.
func TestDelegateExplicitMaxDepthBeatsHelmFallback_spec_8_2_bis(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_root", "", "r0", "p0", isolation.ProfileSandboxed)
	seedParent(t, store, "sess_a", "sess_root", "r1", "p1", isolation.ProfileSandboxed)
	seedParent(t, store, "sess_parent", "sess_a", "r2", "p2", isolation.ProfileSandboxed)
	svc := delegation.NewService(store, delegation.Options{
		IDFunc:          func() string { return "sess_child" },
		DefaultMaxDepth: 10,
	})
	// Parent depth = 2; explicit MaxDepth=2 rejects (child would be 3).
	_, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "r3",
		PoolRef:          "p3",
		IsolationProfile: isolation.ProfileSandboxed,
		MaxDepth:         2,
	})
	var de *lease.DepthExceededError
	if !errors.As(err, &de) {
		t.Fatalf("got %v, want DepthExceededError under explicit MaxDepth=2", err)
	}
	if de.Max != 2 {
		t.Errorf("DepthExceededError.Max = %d, want 2 (explicit beats fallback 10)", de.Max)
	}
}

// recordingAuditor captures every §11.7 delegation audit event the
// service emits.
type recordingAuditor struct {
	events []recordedEvent
}

type recordedEvent struct {
	eventType string
	detail    map[string]any
}

func (a *recordingAuditor) EmitDelegationEvent(_ context.Context, t string, d map[string]any) {
	a.events = append(a.events, recordedEvent{t, d})
}

// TestDelegateEmitsSpawnedAuditEvent_spec_11_7_F_8_5_8 verifies that
// a successful Delegate call records a `delegation.spawned` audit row
// carrying the §11.7 lineage tuple. spec: §11.7 line 62; F-8.5.8.
func TestDelegateEmitsSpawnedAuditEvent_spec_11_7_F_8_5_8(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	aud := &recordingAuditor{}
	svc := delegation.NewService(store, delegation.Options{
		IDFunc:  func() string { return "sess_child" },
		Auditor: aud,
		Clock:   func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	})
	if _, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileMicrovm,
	}); err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	var spawned *recordedEvent
	for i := range aud.events {
		if aud.events[i].eventType == "delegation.spawned" {
			spawned = &aud.events[i]
			break
		}
	}
	if spawned == nil {
		t.Fatalf("delegation.spawned not emitted: %+v", aud.events)
	}
	for _, k := range []string{
		"parent_session_id", "child_session_id", "delegation_depth",
		"runtime_ref", "pool_ref", "isolation_profile", "is_self_recursive",
	} {
		if _, ok := spawned.detail[k]; !ok {
			t.Errorf("delegation.spawned detail missing %q: %+v", k, spawned.detail)
		}
	}
	if spawned.detail["child_session_id"] != "sess_child" {
		t.Errorf("child_session_id = %v, want sess_child", spawned.detail["child_session_id"])
	}
	if spawned.detail["delegation_depth"] != 1 {
		t.Errorf("delegation_depth = %v, want 1", spawned.detail["delegation_depth"])
	}
	if spawned.detail["is_self_recursive"] != false {
		t.Errorf("is_self_recursive = %v, want false", spawned.detail["is_self_recursive"])
	}
}

// TestDelegateEmitsSelfRecursionAllowedAudit_spec_8_2_F_8_5_9 verifies
// that an admitted self-recursive hop under `enforce` mode emits the
// §8.2 `delegation.self_recursion_allowed` audit row. spec: §8.2 lines
// 70-79; §16.7 catalog; F-8.5.9.
func TestDelegateEmitsSelfRecursionAllowedAudit_spec_8_2_F_8_5_9(t *testing.T) {
	store := memstore.New()
	// Lineage so the target (claude/pool-a) re-appears.
	seedParent(t, store, "sess_root", "", "claude", "pool-a", isolation.ProfileSandboxed)
	seedParent(t, store, "sess_parent", "sess_root", "gemini", "pool-b", isolation.ProfileSandboxed)
	// Wire the runtime registry so the cycle gate reads the target
	// runtime's allowSelfRecursion.
	rt := runtimestore.NewMemory()
	_ = rt.Create(context.Background(), runtimestore.Runtime{
		Name: "claude", Type: runtimestore.TypeAgent, AllowSelfRecursion: true,
	})
	pol := delegationpolicystore.NewMemory()
	_ = pol.Create(context.Background(), delegationpolicystore.DelegationPolicy{
		TenantID: "acme", Name: "loose", AllowSelfRecursion: true,
	})
	if _, err := rt.Update(context.Background(), "claude", func(r *runtimestore.Runtime) error {
		r.DelegationPolicyRef = "loose"
		return nil
	}); err != nil {
		t.Fatalf("link runtime to policy: %v", err)
	}
	aud := &recordingAuditor{}
	svc := delegation.NewService(store, delegation.Options{
		IDFunc:                     func() string { return "sess_child" },
		Runtimes:                   rt,
		Policies:                   pol,
		PlatformAllowSelfRecursion: true,
		Auditor:                    aud,
		Clock:                      func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	})
	if _, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "claude",
		PoolRef:          "pool-a",
		IsolationProfile: isolation.ProfileSandboxed,
	}); err != nil {
		t.Fatalf("Delegate (self-recursion): %v", err)
	}
	var allowed *recordedEvent
	for i := range aud.events {
		if aud.events[i].eventType == "delegation.self_recursion_allowed" {
			allowed = &aud.events[i]
			break
		}
	}
	if allowed == nil {
		t.Fatalf("delegation.self_recursion_allowed not emitted: %+v", aud.events)
	}
	if allowed.detail["mode"] != "enforce" {
		t.Errorf("mode = %v, want enforce", allowed.detail["mode"])
	}
	if allowed.detail["platform_allow_self_rec"] != true {
		t.Errorf("platform_allow_self_rec = %v, want true", allowed.detail["platform_allow_self_rec"])
	}
}

// TestDelegateEmitsCycleWarningAudit_spec_8_2_F_8_5_9 verifies that a
// `would_have_blocked` outcome under `warn` mode emits the
// `delegation.cycle_warning` audit row, paired with
// `would_have_blocked_layers`. spec: §8.2 lines 70-79; §16.7 catalog;
// F-8.5.9.
func TestDelegateEmitsCycleWarningAudit_spec_8_2_F_8_5_9(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_root", "", "claude", "pool-a", isolation.ProfileSandboxed)
	seedParent(t, store, "sess_parent", "sess_root", "gemini", "pool-b", isolation.ProfileSandboxed)
	aud := &recordingAuditor{}
	svc := delegation.NewService(store, delegation.Options{
		IDFunc:    func() string { return "sess_child" },
		CycleMode: cycle.ModeWarn,
		Auditor:   aud,
		Clock:     func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	})
	if _, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "claude",
		PoolRef:          "pool-a",
		IsolationProfile: isolation.ProfileSandboxed,
	}); err != nil {
		t.Fatalf("Delegate (warn-mode would-have-blocked): %v", err)
	}
	var warning *recordedEvent
	for i := range aud.events {
		if aud.events[i].eventType == "delegation.cycle_warning" {
			warning = &aud.events[i]
			break
		}
	}
	if warning == nil {
		t.Fatalf("delegation.cycle_warning not emitted: %+v", aud.events)
	}
	if warning.detail["mode"] != "warn" {
		t.Errorf("mode = %v, want warn", warning.detail["mode"])
	}
	layers, _ := warning.detail["would_have_blocked_layers"].([]string)
	if len(layers) == 0 {
		t.Errorf("would_have_blocked_layers empty: %+v", warning.detail)
	}
}

// TestDelegateNoCycleAuditWithoutSelfRecursion_spec_F_8_5_9 verifies a
// non-self-recursive admission does NOT pollute the audit log with
// cycle-gate events (only delegation.spawned is emitted).
func TestDelegateNoCycleAuditWithoutSelfRecursion_spec_F_8_5_9(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	aud := &recordingAuditor{}
	svc := delegation.NewService(store, delegation.Options{
		IDFunc:  func() string { return "sess_child" },
		Auditor: aud,
		Clock:   func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	})
	if _, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
	}); err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	for _, e := range aud.events {
		if e.eventType == "delegation.self_recursion_allowed" || e.eventType == "delegation.cycle_warning" {
			t.Errorf("non-self-recursive admission must not emit %q: %+v", e.eventType, e.detail)
		}
	}
}

// TestDelegateRejectsDenyApprovalMode_spec_8_4_521 verifies §8.4 line
// 521: an `approvalMode: "deny"` lease short-circuits the delegation
// path before pod allocation and before the §4 PreDelegation
// interceptor. The parent lookup, child token mint, lineage walk, and
// store INSERT MUST NOT run; the service returns ErrDelegationDenied.
// F-8.4.1.
func TestDelegateRejectsDenyApprovalMode_spec_8_4_521(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	aud := &recordingAuditor{}
	svc := delegation.NewService(store, delegation.Options{
		IDFunc:  func() string { return "sess_child" },
		Auditor: aud,
		Clock:   func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	})
	_, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
		ApprovalMode:     lease.ApprovalModeDeny,
	})
	if !errors.Is(err, delegation.ErrDelegationDenied) {
		t.Fatalf("Delegate(approvalMode=deny) = %v, want ErrDelegationDenied", err)
	}
	// §8.4: a denied delegation must not commit a child row.
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err == nil {
		t.Errorf("ErrDelegationDenied must not commit a child row; sess_child exists")
	}
	// §8.4: no `delegation.spawned` audit row must fire for a denied
	// delegation; the spawn event is reserved for committed children.
	for _, e := range aud.events {
		if e.eventType == "delegation.spawned" {
			t.Errorf("denied delegation must not emit delegation.spawned: %+v", e.detail)
		}
	}
}

// TestDelegateAcceptsApprovalAliasedToPolicy_spec_8_4_520 verifies
// §8.4 line 520: `approvalMode: "approval"` is accepted at lease
// evaluation time and the gateway treats it identically to `policy`
// mode in v1. The child session MUST be created and the audit record
// MUST preserve `approval` so the v1 alias is observable. F-8.4.1,
// F-8.4.3.
func TestDelegateAcceptsApprovalAliasedToPolicy_spec_8_4_520(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	aud := &recordingAuditor{}
	svc := delegation.NewService(store, delegation.Options{
		IDFunc:  func() string { return "sess_child" },
		Auditor: aud,
		Clock:   func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	})
	res, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
		ApprovalMode:     lease.ApprovalModeApproval,
	})
	if err != nil {
		t.Fatalf("Delegate(approvalMode=approval): %v", err)
	}
	if res.Child.ID != "sess_child" {
		t.Errorf("child id = %q, want sess_child", res.Child.ID)
	}
	var spawned *recordedEvent
	for i := range aud.events {
		if aud.events[i].eventType == "delegation.spawned" {
			spawned = &aud.events[i]
			break
		}
	}
	if spawned == nil {
		t.Fatalf("delegation.spawned not emitted: %+v", aud.events)
	}
	// F-8.4.3: the declared mode survives onto the audit row so the
	// v1 `approval → policy` alias is auditable.
	if got := spawned.detail["approval_mode"]; got != string(lease.ApprovalModeApproval) {
		t.Errorf("audit approval_mode = %v, want %q", got, lease.ApprovalModeApproval)
	}
	if got := spawned.detail["effective_approval_mode"]; got != string(lease.ApprovalModePolicy) {
		t.Errorf("audit effective_approval_mode = %v, want %q", got, lease.ApprovalModePolicy)
	}
}

// TestDelegateRejectsUnknownApprovalMode_spec_8_4 verifies §8.4: a
// value outside the closed enum is rejected at the service boundary
// with *lease.InvalidApprovalModeError, before any side effects (no
// parent lookup, no store write, no audit emission). F-8.4.2.
func TestDelegateRejectsUnknownApprovalMode_spec_8_4(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	aud := &recordingAuditor{}
	svc := delegation.NewService(store, delegation.Options{
		IDFunc:  func() string { return "sess_child" },
		Auditor: aud,
		Clock:   func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	})
	_, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
		ApprovalMode:     "ALLOW",
	})
	var ime *lease.InvalidApprovalModeError
	if !errors.As(err, &ime) {
		t.Fatalf("Delegate(approvalMode=ALLOW) = %v, want *lease.InvalidApprovalModeError", err)
	}
	if ime.Value != "ALLOW" {
		t.Errorf("InvalidApprovalModeError.Value = %q, want ALLOW", ime.Value)
	}
	if len(aud.events) != 0 {
		t.Errorf("invalid approvalMode must not emit audit: %+v", aud.events)
	}
}

// TestDelegateSpawnedAuditRecordsDefaultApprovalMode_spec_8_4 verifies
// §8.4: a delegation that omits approvalMode is recorded with the
// spec-default ("policy") on the audit row so the audit shape is
// stable regardless of caller input. F-8.4.3.
func TestDelegateSpawnedAuditRecordsDefaultApprovalMode_spec_8_4(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	aud := &recordingAuditor{}
	svc := delegation.NewService(store, delegation.Options{
		IDFunc:  func() string { return "sess_child" },
		Auditor: aud,
		Clock:   func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	})
	if _, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
	}); err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	var spawned *recordedEvent
	for i := range aud.events {
		if aud.events[i].eventType == "delegation.spawned" {
			spawned = &aud.events[i]
			break
		}
	}
	if spawned == nil {
		t.Fatalf("delegation.spawned not emitted: %+v", aud.events)
	}
	if got := spawned.detail["approval_mode"]; got != string(lease.ApprovalModePolicy) {
		t.Errorf("audit approval_mode = %v, want %q", got, lease.ApprovalModePolicy)
	}
	if got := spawned.detail["effective_approval_mode"]; got != string(lease.ApprovalModePolicy) {
		t.Errorf("audit effective_approval_mode = %v, want %q", got, lease.ApprovalModePolicy)
	}
}

// spec: §8.3 line 181 (F-8.7.12 / F-13.5.7). The §8.3 cluster-scoped
// `gateway.interceptorWeakeningCooldownSeconds` window opens when an
// admin flips a DelegationPolicy's `scanExportedFiles` from true to
// false. Every `delegate_task` whose effective DelegationPolicy
// resolves to the affected policy MUST reject with the typed
// InterceptorWeakeningCooldownError carrying the policy name,
// transition timestamp, configured cooldown, and the remaining
// retry-after window so the §8.5 MCP shim can emit the canonical
// INTERCEPTOR_WEAKENING_COOLDOWN envelope (TRANSIENT, HTTP 503).
func newCooldownTestRig(t *testing.T, weakenedAt time.Time, cooldown time.Duration, fixedNow time.Time) (*delegation.Service, delegation.Request) {
	t.Helper()
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name:                "gemini",
		Image:               "lenny/gemini@sha256:abc",
		DelegationPolicyRef: "scan-policy",
	}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	policies := delegationpolicystore.NewMemory()
	if err := policies.Create(context.Background(), delegationpolicystore.DelegationPolicy{
		TenantID: "acme",
		Name:     "scan-policy",
		ContentPolicy: delegationpolicystore.ContentPolicy{
			ScanExportedFiles: false,
			InterceptorRef:    "guardrails",
		},
	}); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	if !weakenedAt.IsZero() {
		if _, err := policies.Update(context.Background(), "acme", "scan-policy",
			func(p *delegationpolicystore.DelegationPolicy) error {
				p.ScanExportedFilesWeakenedAt = weakenedAt
				return nil
			}); err != nil {
			t.Fatalf("stamp weakenedAt: %v", err)
		}
	}
	svc := delegation.NewService(store, delegation.Options{
		Runtimes:                     runtimes,
		Policies:                     policies,
		Clock:                        func() time.Time { return fixedNow },
		IDFunc:                       func() string { return "sess_child" },
		InterceptorWeakeningCooldown: cooldown,
	})
	return svc, delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
	}
}

func TestDelegateRejectsInsideInterceptorWeakeningCooldownWindow_spec_8_3_181(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Weakened 30 s ago — half the cooldown window remains.
	weakenedAt := now.Add(-30 * time.Second)
	svc, req := newCooldownTestRig(t, weakenedAt, 60*time.Second, now)

	_, err := svc.Delegate(context.Background(), "acme", req)
	var cdErr *delegation.InterceptorWeakeningCooldownError
	if !errors.As(err, &cdErr) {
		t.Fatalf("Delegate during cooldown = %v, want *InterceptorWeakeningCooldownError", err)
	}
	if cdErr.PolicyName != "scan-policy" {
		t.Errorf("PolicyName = %q, want scan-policy", cdErr.PolicyName)
	}
	if !cdErr.TransitionTs.Equal(weakenedAt) {
		t.Errorf("TransitionTs = %v, want %v", cdErr.TransitionTs, weakenedAt)
	}
	if cdErr.CooldownSeconds != 60 {
		t.Errorf("CooldownSeconds = %d, want 60", cdErr.CooldownSeconds)
	}
	if cdErr.RetryAfterSeconds != 30 {
		t.Errorf("RetryAfterSeconds = %d, want 30", cdErr.RetryAfterSeconds)
	}
}

func TestDelegateAdmitsAfterCooldownWindowExpires_spec_8_3_181(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Weakened 61 s ago — the 60 s window has expired.
	weakenedAt := now.Add(-61 * time.Second)
	svc, req := newCooldownTestRig(t, weakenedAt, 60*time.Second, now)

	if _, err := svc.Delegate(context.Background(), "acme", req); err != nil {
		t.Fatalf("Delegate after cooldown expiry: %v", err)
	}
}

func TestDelegateAdmitsWhenWeakenedAtZero_spec_8_3_181(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// A policy that never weakened carries WeakenedAt = zero; the
	// admission path must skip the cooldown check entirely so the
	// spec-default open posture is undisturbed.
	svc, req := newCooldownTestRig(t, time.Time{}, 60*time.Second, now)
	if _, err := svc.Delegate(context.Background(), "acme", req); err != nil {
		t.Fatalf("Delegate with no prior weakening: %v", err)
	}
}

func TestDelegateRespectsCooldownDisabled_spec_8_3_181(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	weakenedAt := now.Add(-5 * time.Second)
	// A negative Options.InterceptorWeakeningCooldown disables the
	// gate so tests that exercise unrelated admission paths can opt
	// out without re-rigging the policy registry. F-8.7.12.
	svc, req := newCooldownTestRig(t, weakenedAt, -1, now)
	if _, err := svc.Delegate(context.Background(), "acme", req); err != nil {
		t.Fatalf("Delegate with cooldown disabled: %v", err)
	}
}

func TestDelegateCooldownRoundsUpSubSecondTail_spec_8_3_181(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 500*int(time.Millisecond), time.UTC)
	// 59.5 s have elapsed — the spec-canonical retryAfter ceiling
	// surfaces 1 (round-up of the remaining 0.5 s).
	weakenedAt := now.Add(-59500 * time.Millisecond)
	svc, req := newCooldownTestRig(t, weakenedAt, 60*time.Second, now)
	_, err := svc.Delegate(context.Background(), "acme", req)
	var cdErr *delegation.InterceptorWeakeningCooldownError
	if !errors.As(err, &cdErr) {
		t.Fatalf("Delegate during sub-second tail = %v, want cooldown error", err)
	}
	if cdErr.RetryAfterSeconds != 1 {
		t.Errorf("RetryAfterSeconds = %d, want 1 (round-up)", cdErr.RetryAfterSeconds)
	}
}

func TestDelegateCooldownDefaultsTo60sWhenOptionZero_spec_8_3_181(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	weakenedAt := now.Add(-10 * time.Second)
	// Options.InterceptorWeakeningCooldown == 0 selects the
	// DefaultInterceptorWeakeningCooldown (60 s) so a delegation 10 s
	// into the window still rejects.
	svc, req := newCooldownTestRig(t, weakenedAt, 0, now)
	_, err := svc.Delegate(context.Background(), "acme", req)
	var cdErr *delegation.InterceptorWeakeningCooldownError
	if !errors.As(err, &cdErr) {
		t.Fatalf("Delegate during cooldown = %v, want cooldown error", err)
	}
	if cdErr.CooldownSeconds != int(delegation.DefaultInterceptorWeakeningCooldown/time.Second) {
		t.Errorf("CooldownSeconds = %d, want %d", cdErr.CooldownSeconds, int(delegation.DefaultInterceptorWeakeningCooldown/time.Second))
	}
}
