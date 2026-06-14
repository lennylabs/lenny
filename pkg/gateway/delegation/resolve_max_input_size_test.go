// SPDX-License-Identifier: MIT

package delegation_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// spec: §8.3 lines 149-157 / §4.8 line 974 — Service.ResolveMaxInputSize
// returns the parent runtime's effective contentPolicy.maxInputSize so
// the §4.8 DelegationPolicyEvaluator measures TaskSpec.input against the
// per-policy ceiling rather than the cluster default alone. A parent
// whose runtime names no active policy (or whose policy leaves the cap at
// zero) resolves ok == false, leaving the evaluator on its default.
// F-13.5.1 / F-8.2.9.
func TestResolveMaxInputSize_spec_8_3_157(t *testing.T) {
	ctx := context.Background()

	seedParent := func(store sessionstore.Store, runtimeRef string) {
		t.Helper()
		if err := store.Create(ctx, sessionstore.Session{
			ID: "sess_parent", TenantID: "acme", UserID: "alice@acme.com",
			State: session.StateRunning, RuntimeRef: runtimeRef,
		}); err != nil {
			t.Fatalf("seed parent: %v", err)
		}
	}

	t.Run("policy cap resolves", func(t *testing.T) {
		store := memstore.New()
		seedParent(store, "claude")
		runtimes := runtimestore.NewMemory()
		if err := runtimes.Create(ctx, runtimestore.Runtime{
			Name: "claude", Type: runtimestore.TypeAgent, DelegationPolicyRef: "tight",
		}); err != nil {
			t.Fatalf("seed runtime: %v", err)
		}
		policies := delegationpolicystore.NewMemory()
		if err := policies.Create(ctx, delegationpolicystore.DelegationPolicy{
			TenantID: "acme", Name: "tight",
			ContentPolicy: delegationpolicystore.ContentPolicy{MaxInputSize: 4096},
		}); err != nil {
			t.Fatalf("seed policy: %v", err)
		}
		svc := delegation.NewService(store, delegation.Options{Runtimes: runtimes, Policies: policies})

		limit, ok := svc.ResolveMaxInputSize(ctx, "acme", "sess_parent")
		if !ok {
			t.Fatal("ok = false, want true for a runtime naming an active policy with a positive cap")
		}
		if limit != 4096 {
			t.Errorf("limit = %d, want 4096", limit)
		}
	})

	t.Run("runtime without policy resolves false", func(t *testing.T) {
		store := memstore.New()
		seedParent(store, "claude")
		runtimes := runtimestore.NewMemory()
		if err := runtimes.Create(ctx, runtimestore.Runtime{
			Name: "claude", Type: runtimestore.TypeAgent,
		}); err != nil {
			t.Fatalf("seed runtime: %v", err)
		}
		svc := delegation.NewService(store, delegation.Options{Runtimes: runtimes, Policies: delegationpolicystore.NewMemory()})

		if _, ok := svc.ResolveMaxInputSize(ctx, "acme", "sess_parent"); ok {
			t.Error("ok = true, want false when the runtime names no DelegationPolicy")
		}
	})

	t.Run("policy with zero cap resolves false", func(t *testing.T) {
		store := memstore.New()
		seedParent(store, "claude")
		runtimes := runtimestore.NewMemory()
		if err := runtimes.Create(ctx, runtimestore.Runtime{
			Name: "claude", Type: runtimestore.TypeAgent, DelegationPolicyRef: "loose",
		}); err != nil {
			t.Fatalf("seed runtime: %v", err)
		}
		policies := delegationpolicystore.NewMemory()
		if err := policies.Create(ctx, delegationpolicystore.DelegationPolicy{
			TenantID: "acme", Name: "loose",
			ContentPolicy: delegationpolicystore.ContentPolicy{MaxInputSize: 0},
		}); err != nil {
			t.Fatalf("seed policy: %v", err)
		}
		svc := delegation.NewService(store, delegation.Options{Runtimes: runtimes, Policies: policies})

		if _, ok := svc.ResolveMaxInputSize(ctx, "acme", "sess_parent"); ok {
			t.Error("ok = true, want false when the policy leaves maxInputSize at zero (evaluator default applies)")
		}
	})

	t.Run("nil registries resolve false", func(t *testing.T) {
		store := memstore.New()
		seedParent(store, "claude")
		svc := delegation.NewService(store, delegation.Options{})
		if _, ok := svc.ResolveMaxInputSize(ctx, "acme", "sess_parent"); ok {
			t.Error("ok = true, want false when no runtime/policy registry is wired")
		}
	})

	t.Run("missing parent resolves false", func(t *testing.T) {
		runtimes := runtimestore.NewMemory()
		svc := delegation.NewService(memstore.New(), delegation.Options{Runtimes: runtimes, Policies: delegationpolicystore.NewMemory()})
		if _, ok := svc.ResolveMaxInputSize(ctx, "acme", "does-not-exist"); ok {
			t.Error("ok = true, want false for an unknown parent session")
		}
	})
}
