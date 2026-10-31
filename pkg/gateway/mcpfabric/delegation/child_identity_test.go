// SPDX-License-Identifier: MIT

package delegation_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
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

// TestDelegationRequestCarriesNoOwner_spec_8_2 pins that a delegation
// request has no field naming the child's owner, so no caller of the
// delegation service can choose it: the child takes the parent's owner
// from the parent's session row. The check fails if an owner field returns
// to delegation.Request under any of the names it carried.
//
// spec: §8.2 (Child identity); §10.2 (Session ownership) rule 2
func TestDelegationRequestCarriesNoOwner_spec_8_2(t *testing.T) {
	typ := reflect.TypeOf(delegation.Request{})
	for _, name := range []string{"UserID", "UserId", "OwnerID", "Owner", "User"} {
		if _, ok := typ.FieldByName(name); ok {
			t.Errorf("delegation.Request has field %s; the child's owner must come from the parent row", name)
		}
	}
	for i := range typ.NumField() {
		if tag := typ.Field(i).Tag.Get("json"); strings.HasPrefix(tag, "userId") {
			t.Errorf("delegation.Request field %s serializes as %q", typ.Field(i).Name, tag)
		}
	}
}

// TestDelegatedChildTakesParentOwnerNotCaller_spec_8_2 pins that a child
// delegated under a principal other than the parent's owner still stores
// the parent's owner.
//
// spec: §8.2 (Child identity); §10.2 (Session ownership) rule 2
func TestDelegatedChildTakesParentOwnerNotCaller_spec_8_2(t *testing.T) {
	store := memstore.New()
	seedParent(t, store, "sess_parent", "", "claude", "pool-a", isolation.ProfileSandboxed)
	svc := delegation.NewService(store, delegation.Options{IDFunc: func() string { return "sess_child" }})
	ctx := authmw.WithPrincipal(context.Background(), authmw.Principal{Subject: "bob@acme.com", TenantID: "acme"})
	res, err := svc.Delegate(ctx, "acme", delegation.Request{ParentSessionID: "sess_parent", RuntimeRef: "gemini"})
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	child, err := store.Get(context.Background(), "acme", res.Child.ID)
	if err != nil {
		t.Fatalf("Get child: %v", err)
	}
	if child.UserID != "user_alice" {
		t.Errorf("child owner = %q, want the parent's owner user_alice", child.UserID)
	}
}
