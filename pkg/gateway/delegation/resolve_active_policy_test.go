// SPDX-License-Identifier: MIT

package delegation_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// spec: §10.6 line 601 — defaultDelegationPolicy names the
// DelegationPolicy applied to sessions created in an environment. The
// gateway resolves the policy name through Service.ResolveActivePolicy,
// which returns an active policy or signals "no restriction" for an
// unresolved reference. F-10.6.7.

func TestResolveActivePolicy_spec_10_6_601(t *testing.T) {
	ctx := context.Background()
	policies := delegationpolicystore.NewMemory()
	if err := policies.Create(ctx, delegationpolicystore.DelegationPolicy{
		TenantID: "acme", Name: "scoped",
		Rules: []delegationpolicystore.Rule{{
			Target: delegationpolicystore.Target{MatchLabels: map[string]string{"access": "read"}},
			Allow:  true,
		}},
	}); err != nil {
		t.Fatalf("seed scoped: %v", err)
	}
	if err := policies.Create(ctx, delegationpolicystore.DelegationPolicy{
		TenantID: "acme", Name: "retired",
	}); err != nil {
		t.Fatalf("seed retired: %v", err)
	}
	if err := policies.SoftDelete(ctx, "acme", "retired", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("soft-delete retired: %v", err)
	}
	svc := delegation.NewService(memstore.New(), delegation.Options{Policies: policies})

	t.Run("active policy resolves", func(t *testing.T) {
		pol, ok, err := svc.ResolveActivePolicy(ctx, "acme", "scoped")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if !ok {
			t.Fatal("ok = false, want true for an active policy")
		}
		if pol.Name != "scoped" {
			t.Errorf("Name = %q, want scoped", pol.Name)
		}
	})

	t.Run("missing policy is not an error", func(t *testing.T) {
		_, ok, err := svc.ResolveActivePolicy(ctx, "acme", "does-not-exist")
		if err != nil {
			t.Fatalf("err = %v, want nil (a missing reference imposes no restriction)", err)
		}
		if ok {
			t.Error("ok = true, want false for a missing policy")
		}
	})

	t.Run("soft-deleted policy resolves false", func(t *testing.T) {
		_, ok, err := svc.ResolveActivePolicy(ctx, "acme", "retired")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if ok {
			t.Error("ok = true, want false for a soft-deleted policy")
		}
	})

	t.Run("empty name resolves false", func(t *testing.T) {
		_, ok, err := svc.ResolveActivePolicy(ctx, "acme", "")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if ok {
			t.Error("ok = true, want false for an empty name")
		}
	})

	t.Run("cross-tenant miss resolves false", func(t *testing.T) {
		_, ok, err := svc.ResolveActivePolicy(ctx, "globex", "scoped")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if ok {
			t.Error("ok = true, want false — scoped belongs to acme, not globex")
		}
	})
}

func TestResolveActivePolicyNilRegistry_spec_10_6_601(t *testing.T) {
	svc := delegation.NewService(memstore.New(), delegation.Options{}) // no Policies wired
	_, ok, err := svc.ResolveActivePolicy(context.Background(), "acme", "scoped")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if ok {
		t.Error("ok = true, want false when no policy registry is wired")
	}
}
