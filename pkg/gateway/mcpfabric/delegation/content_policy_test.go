// SPDX-License-Identifier: MIT

package delegation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// spec: §8.3 lines 157-188 — the gateway enforces contentPolicy
// inheritance and four-axis monotonicity on every delegate_task: the
// child's resolved policy (from the target runtime's DelegationPolicy) may
// only tighten the parent's effective policy, and the resolved effective
// policy is stamped on the child lease so the next hop inherits the
// transitively-narrowest cap. F-13.5.10.

// seedRuntimePolicy registers a runtime that names a DelegationPolicy and
// the policy itself in the shared stores so both the parent's and the
// target's policies resolve from one Service.
func seedRuntimePolicy(t *testing.T, rts runtimestore.Store, pols delegationpolicystore.Store, runtimeRef, policyRef string, cp delegationpolicystore.ContentPolicy) {
	t.Helper()
	ctx := context.Background()
	if err := rts.Create(ctx, runtimestore.Runtime{Name: runtimeRef, Type: runtimestore.TypeAgent, DelegationPolicyRef: policyRef}); err != nil {
		t.Fatalf("seed runtime %s: %v", runtimeRef, err)
	}
	if policyRef != "" {
		if err := pols.Create(ctx, delegationpolicystore.DelegationPolicy{TenantID: "acme", Name: policyRef, ContentPolicy: cp}); err != nil {
			t.Fatalf("seed policy %s: %v", policyRef, err)
		}
	}
}

func contentPolicyService(t *testing.T, store sessionstore.Store, rts runtimestore.Store, pols delegationpolicystore.Store) *delegation.Service {
	t.Helper()
	return delegation.NewService(store, delegation.Options{
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Runtimes: rts,
		Policies: pols,
		IDFunc:   func() string { return "sess_child" },
	})
}

// A child whose target policy tightens both the byte cap and adds an
// interceptor over a default parent is admitted and the tightened
// effective policy is stamped on the child lease for the next hop.
func TestDelegateStampsTightenedContentPolicy_spec_8_3_157(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	rts, pols := runtimestore.NewMemory(), delegationpolicystore.NewMemory()
	// Parent runtime carries no policy → parent effective is the platform
	// default (128 KiB, no interceptor, no scan, 10 MiB).
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	seedRuntimePolicy(t, rts, pols, "gemini", "child-pol",
		delegationpolicystore.ContentPolicy{MaxInputSize: 4096, InterceptorRef: "scrub", ScanExportedFiles: true, MaxExportedFileSize: 2048})
	svc := contentPolicyService(t, store, rts, pols)

	res, err := svc.Delegate(ctx, "acme", delegation.Request{
		ParentSessionID: "sess_parent", RuntimeRef: "gemini", PoolRef: "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
	})
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	dl := res.Child.DelegationLease
	if dl == nil {
		t.Fatal("child DelegationLease is nil; want the stamped effective contentPolicy")
	}
	if dl.ContentPolicyRef != "scrub" {
		t.Errorf("ContentPolicyRef = %q, want scrub", dl.ContentPolicyRef)
	}
	if dl.ContentMaxInputSize != 4096 {
		t.Errorf("ContentMaxInputSize = %d, want 4096", dl.ContentMaxInputSize)
	}
	if !dl.ContentScanExportedFiles {
		t.Error("ContentScanExportedFiles = false, want true")
	}
	if dl.ContentMaxExportedFileSize != 2048 {
		t.Errorf("ContentMaxExportedFileSize = %d, want 2048", dl.ContentMaxExportedFileSize)
	}
	// The record must be durable for the next hop / recovery.
	persisted, err := store.Get(ctx, "acme", res.Child.ID)
	if err != nil {
		t.Fatalf("Get persisted child: %v", err)
	}
	if persisted.DelegationLease == nil || persisted.DelegationLease.ContentMaxInputSize != 4096 {
		t.Errorf("persisted lease = %+v, want the stamped effective contentPolicy", persisted.DelegationLease)
	}
}

// A default-only effective policy must not bloat the child lease: the
// content fields stay zero so the read path resolves them to the default.
func TestDelegateDoesNotStampDefaultContentPolicy_spec_8_3_157(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	rts, pols := runtimestore.NewMemory(), delegationpolicystore.NewMemory()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	// Target runtime with no policy at all → no tightening anywhere.
	if err := rts.Create(ctx, runtimestore.Runtime{Name: "gemini", Type: runtimestore.TypeAgent}); err != nil {
		t.Fatalf("seed gemini: %v", err)
	}
	svc := contentPolicyService(t, store, rts, pols)

	res, err := svc.Delegate(ctx, "acme", delegation.Request{
		ParentSessionID: "sess_parent", RuntimeRef: "gemini", PoolRef: "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
	})
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if dl := res.Child.DelegationLease; dl != nil &&
		(dl.ContentMaxInputSize != 0 || dl.ContentScanExportedFiles || dl.ContentMaxExportedFileSize != 0 || dl.ContentPolicyRef != "") {
		t.Errorf("default-only effective policy stamped content fields: %+v", dl)
	}
}

// The parent's tightened effective policy (resolved from the parent's own
// runtime policy) blocks a child whose target policy widens the byte cap.
func TestDelegateRejectsContentPolicyWeakening_spec_8_3_157(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	rts, pols := runtimestore.NewMemory(), delegationpolicystore.NewMemory()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	// Parent runtime tightens maxInputSize to 4 KiB.
	seedRuntimePolicy(t, rts, pols, "claude", "parent-pol",
		delegationpolicystore.ContentPolicy{MaxInputSize: 4096})
	// Target runtime declares a looser 64 KiB cap.
	seedRuntimePolicy(t, rts, pols, "gemini", "child-pol",
		delegationpolicystore.ContentPolicy{MaxInputSize: 65536})
	svc := contentPolicyService(t, store, rts, pols)

	_, err := svc.Delegate(ctx, "acme", delegation.Request{
		ParentSessionID: "sess_parent", RuntimeRef: "gemini", PoolRef: "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
	})
	var w *delegation.ContentPolicyWeakeningError
	if !errors.As(err, &w) {
		t.Fatalf("want *ContentPolicyWeakeningError, got %v", err)
	}
	if w.Axis != "maxInputSize" {
		t.Errorf("axis = %q, want maxInputSize", w.Axis)
	}
	// No child row may be created on a rejection.
	if _, err := store.Get(ctx, "acme", "sess_child"); !errors.Is(err, sessionstore.ErrNotFound) {
		t.Errorf("a rejected delegation must not create a child row; Get err = %v", err)
	}
}

// A child substituting a different non-null interceptor is rejected
// unconditionally (§8.3 line 188).
func TestDelegateRejectsInterceptorSubstitution_spec_8_3_188(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	rts, pols := runtimestore.NewMemory(), delegationpolicystore.NewMemory()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	seedRuntimePolicy(t, rts, pols, "claude", "parent-pol",
		delegationpolicystore.ContentPolicy{InterceptorRef: "scrub"})
	seedRuntimePolicy(t, rts, pols, "gemini", "child-pol",
		delegationpolicystore.ContentPolicy{InterceptorRef: "redactor"})
	svc := contentPolicyService(t, store, rts, pols)

	_, err := svc.Delegate(ctx, "acme", delegation.Request{
		ParentSessionID: "sess_parent", RuntimeRef: "gemini", PoolRef: "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
	})
	var s *delegation.ContentPolicyInterceptorSubstitutionError
	if !errors.As(err, &s) {
		t.Fatalf("want *ContentPolicyInterceptorSubstitutionError, got %v", err)
	}
	if s.ParentRef != "scrub" || s.ChildRef != "redactor" {
		t.Errorf("refs = (%q,%q), want (scrub,redactor)", s.ParentRef, s.ChildRef)
	}
}

// The parent's effective policy comes from its stamped lease when it was
// itself delegated under this feature, so the narrowest cap propagates
// transitively across hops. A grandchild widening the stamped 4 KiB cap is
// rejected even though the grandchild's own runtime policy is the only one
// the gateway resolves at that hop.
func TestDelegateInheritsStampedParentContentPolicy_spec_8_3_240(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	rts, pols := runtimestore.NewMemory(), delegationpolicystore.NewMemory()
	// Parent (middle node) already carries a stamped 4 KiB effective cap.
	parent := sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", State: session.StateRunning, UserID: "user_alice",
		RuntimeRef: "claude", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		DelegationLease: &sessionstore.DelegationLease{ContentMaxInputSize: 4096},
	}
	if err := store.Create(ctx, parent); err != nil {
		t.Fatalf("seed stamped parent: %v", err)
	}
	// The grandchild's target runtime declares a looser 64 KiB cap.
	seedRuntimePolicy(t, rts, pols, "gemini", "child-pol",
		delegationpolicystore.ContentPolicy{MaxInputSize: 65536})
	svc := contentPolicyService(t, store, rts, pols)

	_, err := svc.Delegate(ctx, "acme", delegation.Request{
		ParentSessionID: "sess_parent", RuntimeRef: "gemini", PoolRef: "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
	})
	var w *delegation.ContentPolicyWeakeningError
	if !errors.As(err, &w) {
		t.Fatalf("want *ContentPolicyWeakeningError from the stamped 4 KiB cap, got %v", err)
	}
	if w.ParentValue != "4096" {
		t.Errorf("parentValue = %q, want 4096 (the stamped effective)", w.ParentValue)
	}
}
