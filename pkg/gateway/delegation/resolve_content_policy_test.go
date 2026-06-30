// SPDX-License-Identifier: MIT

package delegation_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §8.3 lines 149-188 / §4.8 lines 1036, 1040 / §13.5 mitigations
// 2-3 — Service.ResolveContentPolicy returns the session runtime's
// effective contentPolicy maxInputSize and interceptorRef so the MCP
// delegate_task / send_message handlers run only the policy-named
// external content scanner at PreDelegation / PreMessageDelivery and
// enforce the message-side byte cap. F-8.2.9 / F-13.5.2.
func TestResolveContentPolicy_spec_8_3_157(t *testing.T) {
	ctx := context.Background()

	seed := func(store sessionstore.Store, runtimeRef string) {
		t.Helper()
		if err := store.Create(ctx, sessionstore.Session{
			ID: "sess", TenantID: "acme", UserID: "alice@acme.com",
			State: session.StateRunning, RuntimeRef: runtimeRef,
		}); err != nil {
			t.Fatalf("seed session: %v", err)
		}
	}

	t.Run("returns ref and cap", func(t *testing.T) {
		store := memstore.New()
		seed(store, "claude")
		runtimes := runtimestore.NewMemory()
		if err := runtimes.Create(ctx, runtimestore.Runtime{
			Name: "claude", Type: runtimestore.TypeAgent, DelegationPolicyRef: "scan",
		}); err != nil {
			t.Fatalf("seed runtime: %v", err)
		}
		policies := delegationpolicystore.NewMemory()
		if err := policies.Create(ctx, delegationpolicystore.DelegationPolicy{
			TenantID: "acme", Name: "scan",
			ContentPolicy: delegationpolicystore.ContentPolicy{MaxInputSize: 4096, InterceptorRef: "pii-scanner"},
		}); err != nil {
			t.Fatalf("seed policy: %v", err)
		}
		svc := delegation.NewService(store, delegation.Options{Runtimes: runtimes, Policies: policies})

		max, ref, ok := svc.ResolveContentPolicy(ctx, "acme", "sess")
		if !ok {
			t.Fatal("ok = false, want true")
		}
		if max != 4096 || ref != "pii-scanner" {
			t.Errorf("got (max=%d ref=%q), want (4096 pii-scanner)", max, ref)
		}
	})

	t.Run("null interceptorRef resolves to empty ref", func(t *testing.T) {
		store := memstore.New()
		seed(store, "claude")
		runtimes := runtimestore.NewMemory()
		if err := runtimes.Create(ctx, runtimestore.Runtime{
			Name: "claude", Type: runtimestore.TypeAgent, DelegationPolicyRef: "noscan",
		}); err != nil {
			t.Fatalf("seed runtime: %v", err)
		}
		policies := delegationpolicystore.NewMemory()
		if err := policies.Create(ctx, delegationpolicystore.DelegationPolicy{
			TenantID: "acme", Name: "noscan",
			ContentPolicy: delegationpolicystore.ContentPolicy{MaxInputSize: 2048},
		}); err != nil {
			t.Fatalf("seed policy: %v", err)
		}
		svc := delegation.NewService(store, delegation.Options{Runtimes: runtimes, Policies: policies})

		max, ref, ok := svc.ResolveContentPolicy(ctx, "acme", "sess")
		if !ok || ref != "" || max != 2048 {
			t.Errorf("got (max=%d ref=%q ok=%v), want (2048 \"\" true)", max, ref, ok)
		}
	})

	t.Run("runtime without policy resolves false", func(t *testing.T) {
		store := memstore.New()
		seed(store, "claude")
		runtimes := runtimestore.NewMemory()
		if err := runtimes.Create(ctx, runtimestore.Runtime{Name: "claude", Type: runtimestore.TypeAgent}); err != nil {
			t.Fatalf("seed runtime: %v", err)
		}
		svc := delegation.NewService(store, delegation.Options{Runtimes: runtimes, Policies: delegationpolicystore.NewMemory()})
		if _, _, ok := svc.ResolveContentPolicy(ctx, "acme", "sess"); ok {
			t.Error("ok = true, want false when the runtime names no DelegationPolicy")
		}
	})

	t.Run("nil registries and missing session resolve false", func(t *testing.T) {
		store := memstore.New()
		seed(store, "claude")
		if _, _, ok := delegation.NewService(store, delegation.Options{}).ResolveContentPolicy(ctx, "acme", "sess"); ok {
			t.Error("ok = true, want false when no registry is wired")
		}
		runtimes := runtimestore.NewMemory()
		svc := delegation.NewService(memstore.New(), delegation.Options{Runtimes: runtimes, Policies: delegationpolicystore.NewMemory()})
		if _, _, ok := svc.ResolveContentPolicy(ctx, "acme", "missing"); ok {
			t.Error("ok = true, want false for an unknown session")
		}
	})

	// ResolveMaxInputSize delegates to ResolveContentPolicy; confirm the
	// zero-cap "use the default" contract still holds after the refactor.
	t.Run("ResolveMaxInputSize still false on zero cap", func(t *testing.T) {
		store := memstore.New()
		seed(store, "claude")
		runtimes := runtimestore.NewMemory()
		if err := runtimes.Create(ctx, runtimestore.Runtime{
			Name: "claude", Type: runtimestore.TypeAgent, DelegationPolicyRef: "z",
		}); err != nil {
			t.Fatalf("seed runtime: %v", err)
		}
		policies := delegationpolicystore.NewMemory()
		if err := policies.Create(ctx, delegationpolicystore.DelegationPolicy{
			TenantID: "acme", Name: "z",
			ContentPolicy: delegationpolicystore.ContentPolicy{MaxInputSize: 0, InterceptorRef: "s"},
		}); err != nil {
			t.Fatalf("seed policy: %v", err)
		}
		svc := delegation.NewService(store, delegation.Options{Runtimes: runtimes, Policies: policies})
		if _, ok := svc.ResolveMaxInputSize(ctx, "acme", "sess"); ok {
			t.Error("ResolveMaxInputSize ok = true, want false on zero cap")
		}
		// But the ref is still resolvable for the scanner-selection path.
		if _, ref, ok := svc.ResolveContentPolicy(ctx, "acme", "sess"); !ok || ref != "s" {
			t.Errorf("ResolveContentPolicy ref = %q ok=%v, want s true", ref, ok)
		}
	})
}
