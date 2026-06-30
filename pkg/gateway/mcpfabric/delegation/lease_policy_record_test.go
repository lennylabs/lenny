// SPDX-License-Identifier: MIT

package delegation_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// spec: §8.10 lines 1044-1049 — the gateway persists the lease-scoped
// policy reference (delegationPolicyRef, effective maxDelegationPolicy,
// contentPolicy interceptorRef, min isolation profile) at original
// `delegate_task` approval time so tree recovery resumes a node against
// the persisted lease record rather than re-evaluating the live policy
// state. F-8.10.5.

func seedTargetRuntimeWithPolicy(t *testing.T, runtimeRef, policyRef, interceptorRef string) (runtimestore.Store, delegationpolicystore.Store) {
	t.Helper()
	ctx := context.Background()
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(ctx, runtimestore.Runtime{
		Name: runtimeRef, Type: runtimestore.TypeAgent, DelegationPolicyRef: policyRef,
	}); err != nil {
		t.Fatalf("seed runtime %s: %v", runtimeRef, err)
	}
	policies := delegationpolicystore.NewMemory()
	if policyRef != "" {
		if err := policies.Create(ctx, delegationpolicystore.DelegationPolicy{
			TenantID: "acme", Name: policyRef,
			ContentPolicy: delegationpolicystore.ContentPolicy{InterceptorRef: interceptorRef},
		}); err != nil {
			t.Fatalf("seed policy %s: %v", policyRef, err)
		}
	}
	return runtimes, policies
}

func TestDelegatePersistsLeasePolicyRecord_spec_8_10_1044(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	runtimes, policies := seedTargetRuntimeWithPolicy(t, "gemini", "tight", "scrub-pii")
	svc := delegation.NewService(store, delegation.Options{
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Runtimes: runtimes,
		Policies: policies,
		IDFunc:   func() string { return "sess_child" },
	})

	res, err := svc.Delegate(ctx, "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileMicrovm,
	})
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}

	// The returned child must carry the captured lease-policy record.
	dl := res.Child.DelegationLease
	if dl == nil {
		t.Fatal("child DelegationLease is nil; want a persisted §8.10 lease-policy record")
	}
	if dl.DelegationPolicyRef != "tight" {
		t.Errorf("DelegationPolicyRef = %q, want tight", dl.DelegationPolicyRef)
	}
	if dl.MaxDelegationPolicy != "tight" {
		t.Errorf("MaxDelegationPolicy = %q, want tight (resolved policy name)", dl.MaxDelegationPolicy)
	}
	if dl.ContentPolicyRef != "scrub-pii" {
		t.Errorf("ContentPolicyRef = %q, want scrub-pii", dl.ContentPolicyRef)
	}
	// The min isolation profile rides the session's IsolationProfile
	// column, not the lease record.
	if res.Child.IsolationProfile != isolation.ProfileMicrovm {
		t.Errorf("IsolationProfile = %q, want %q", res.Child.IsolationProfile, isolation.ProfileMicrovm)
	}
	// v1 does not snapshot pool labels (snapshotPolicyAtLease default
	// false), so the snapshotted set stays empty.
	if len(dl.SnapshottedPoolIDs) != 0 {
		t.Errorf("SnapshottedPoolIDs = %v, want empty under the v1 default", dl.SnapshottedPoolIDs)
	}

	// The record must be durable: a recovery driver reads it from the
	// SessionStore row, not from the live runtime registry. Re-read the
	// persisted row and assert the same record survives the write.
	persisted, err := store.Get(ctx, "acme", res.Child.ID)
	if err != nil {
		t.Fatalf("Get persisted child: %v", err)
	}
	if persisted.DelegationLease == nil || persisted.DelegationLease.DelegationPolicyRef != "tight" ||
		persisted.DelegationLease.MaxDelegationPolicy != "tight" ||
		persisted.DelegationLease.ContentPolicyRef != "scrub-pii" {
		t.Errorf("persisted lease record = %+v, want the captured policy refs", persisted.DelegationLease)
	}
}

// spec: §8.10 lines 1044-1049 — a node with no resource lease slice but
// a resolved policy reference still persists a lease record so recovery
// has the policy fields. The record must not be dropped to NULL.
func TestDelegateLeaseRecordPersistsWithoutResourceSlice_spec_8_10_1044(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	runtimes, policies := seedTargetRuntimeWithPolicy(t, "gemini", "tight", "")
	svc := delegation.NewService(store, delegation.Options{
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Runtimes: runtimes,
		Policies: policies,
		IDFunc:   func() string { return "sess_child" },
	})

	res, err := svc.Delegate(ctx, "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
		// No LeaseSlice: the resource axes are all zero.
	})
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	dl := res.Child.DelegationLease
	if dl == nil {
		t.Fatal("DelegationLease is nil; a resolved policy ref must keep the record non-nil")
	}
	if dl.MaxTokenBudget != 0 || dl.MaxChildrenTotal != 0 {
		t.Errorf("resource axes = %+v, want all zero", dl)
	}
	if dl.DelegationPolicyRef != "tight" || dl.MaxDelegationPolicy != "tight" {
		t.Errorf("policy record = %+v, want delegationPolicyRef/maxDelegationPolicy tight", dl)
	}
}

// spec: §8.10 lines 1044-1049 / §8.2 lines 38-48 — a target runtime that
// names no DelegationPolicy and a delegation with no resource slice
// leaves the lease record nil; there is nothing policy-scoped to persist
// beyond the session's own IsolationProfile column. F-8.10.5 must not
// regress the §8.2 nil-lease invariant for plain delegations.
func TestDelegateNoPolicyLeavesLeaseNil_spec_8_10_1044(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	runtimes, policies := seedTargetRuntimeWithPolicy(t, "gemini", "", "")
	svc := delegation.NewService(store, delegation.Options{
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Runtimes: runtimes,
		Policies: policies,
		IDFunc:   func() string { return "sess_child" },
	})

	res, err := svc.Delegate(ctx, "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileMicrovm,
	})
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if res.Child.DelegationLease != nil {
		t.Errorf("DelegationLease = %+v, want nil when the runtime names no policy and no slice was declared", res.Child.DelegationLease)
	}
	// The min isolation profile is still recoverable from the session row.
	if res.Child.IsolationProfile != isolation.ProfileMicrovm {
		t.Errorf("IsolationProfile = %q, want %q", res.Child.IsolationProfile, isolation.ProfileMicrovm)
	}

	// Sanity: a standalone (non-delegated) session row carries no lease
	// record at all — the policy capture is delegation-only.
	parent, _ := store.Get(ctx, "acme", "sess_parent")
	if !session.IsTerminal(parent.State) && parent.DelegationLease != nil {
		t.Errorf("parent DelegationLease = %+v, want nil for a non-delegated session", parent.DelegationLease)
	}
}
