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
	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
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
		UserID:          "user_alice",
		RuntimeRef:      runtime, PoolRef: pool, IsolationProfile: prof,
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
		CreatedAt:  now, UpdatedAt: now,
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
