// SPDX-License-Identifier: MIT

package delegation_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// TestDelegatedChildIdentityIsItsSessionRow_spec_8_2 pins that a delegated
// child's identity is its session row and that no token is minted for it:
// the committed row carries the parent's user and tenant, the parent and
// root session IDs, and a delegation depth one greater than the parent's,
// and the delegation.spawned audit record carries no child-token fields
// (child_token_jti, act_chain, or delegation_token_scope).
//
// spec: 8.2 (Delegation Mechanism: Child identity), 11.7 (Audit Logging)
func TestDelegatedChildIdentityIsItsSessionRow_spec_8_2(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	aud := &recordingAuditor{}
	svc := delegation.NewService(store, delegation.Options{
		IDFunc:  func() string { return "sess_child" },
		Auditor: aud,
		Clock:   func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
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

	child, err := store.Get(ctx, "acme", res.Child.ID)
	if err != nil {
		t.Fatalf("Get child: %v", err)
	}
	parent, err := store.Get(ctx, "acme", "sess_parent")
	if err != nil {
		t.Fatalf("Get parent: %v", err)
	}
	if child.UserID != parent.UserID || child.TenantID != "acme" {
		t.Errorf("child owner = (%q, %q), want the parent's (%q, acme)", child.UserID, child.TenantID, parent.UserID)
	}
	if child.ParentSessionID != "sess_parent" {
		t.Errorf("child parent_session_id = %q, want sess_parent", child.ParentSessionID)
	}
	if child.RootSessionID != "sess_parent" {
		t.Errorf("child root_session_id = %q, want sess_parent", child.RootSessionID)
	}
	if child.DelegationDepth != parent.DelegationDepth+1 {
		t.Errorf("child delegation_depth = %d, want parent + 1 = %d", child.DelegationDepth, parent.DelegationDepth+1)
	}

	var spawned map[string]any
	for _, e := range aud.events {
		if e.eventType == "delegation.spawned" {
			spawned = e.detail
		}
	}
	if spawned == nil {
		t.Fatalf("delegation.spawned not emitted: %+v", aud.events)
	}
	for _, k := range []string{"child_token_jti", "act_chain", "delegation_token_scope"} {
		if v, ok := spawned[k]; ok {
			t.Errorf("delegation.spawned carries child-token field %q = %v; no token is minted for a delegated child", k, v)
		}
	}
}
