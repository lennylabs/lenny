// SPDX-License-Identifier: MIT

package memstore_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// parentTerminalStates is the terminal set written out independently of
// session.IsTerminal.
var parentTerminalStates = map[session.State]bool{
	session.StateCompleted: true, session.StateFailed: true,
	session.StateCancelled: true, session.StateExpired: true,
}

// seedParent stores a depth-zero parent row in state st and returns it.
func seedParent(t *testing.T, s *memstore.Store, st session.State) sessionstore.Session {
	t.Helper()
	p := liveRow(fenceOwner)
	p.State = st
	if err := s.Create(context.Background(), p); err != nil {
		t.Fatalf("seed parent in %s: %v", st, err)
	}
	return p
}

// delegatedChildOf is a depth-one child row of parentID.
func delegatedChildOf(parentID string) sessionstore.Session {
	c := liveRow(fenceOwner)
	c.ParentSessionID, c.RootSessionID, c.DelegationDepth = parentID, parentID, 1
	return c
}

// TestMemstoreCreateFenceParentStates_spec_12_8 walks every session state of
// the parent and asserts that a delegated child's Create and CreateReserved
// are refused with DELEGATION_PARENT_REVOKED naming the parent exactly when
// the parent is terminal, and leave no row. A parent in a live state other
// than running admits its child.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 8.2 (Parent freshness)
func TestMemstoreCreateFenceParentStates_spec_12_8(t *testing.T) {
	ctx := context.Background()
	for _, st := range session.AllStates() {
		for _, viaReservation := range []bool{false, true} {
			f := newFenceFixture(t)
			parent := seedParent(t, f.sessions, st)
			child := delegatedChildOf(parent.ID)
			var err error
			if viaReservation {
				if rerr := f.sessions.Reserve(ctx, fenceTenant, fenceOwner, child.ID, true); rerr != nil {
					t.Fatalf("%s: Reserve under the parent: %v, want admit (no parent rule)", st, rerr)
				}
				err = f.sessions.CreateReserved(ctx, child)
			} else {
				err = f.sessions.Create(ctx, child)
			}
			if !parentTerminalStates[st] {
				if err != nil {
					t.Fatalf("%s (reserved=%v): child Create = %v, want admit", st, viaReservation, err)
				}
				continue
			}
			ref := refusalOf(t, err, sessionstore.RefusalDelegationParentRevoked)
			if !reflect.DeepEqual(ref.Details, map[string]any{"parentSessionId": parent.ID}) {
				t.Fatalf("%s: details = %v, want parentSessionId", st, ref.Details)
			}
			requireNoRow(t, f.sessions, child.ID)
		}
	}
}

// TestMemstoreCreateFenceAbsentParentRefuses_spec_12_8 asserts that a
// delegated child whose parent has no row, or whose parent row belongs to
// another tenant, is refused with DELEGATION_PARENT_REVOKED, also on a store
// built without a marker reader.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 8.2 (Parent freshness)
func TestMemstoreCreateFenceAbsentParentRefuses_spec_12_8(t *testing.T) {
	ctx := context.Background()
	f := newFenceFixture(t)
	child := delegatedChildOf("00000000-0000-4000-a000-000000000001")
	requireRefusal(t, f.sessions.Create(ctx, child), sessionstore.RefusalDelegationParentRevoked)
	requireNoRow(t, f.sessions, child.ID)

	unfenced := memstore.New()
	other := liveRow(fenceOwner)
	other.TenantID = "globex"
	if err := unfenced.Create(ctx, other); err != nil {
		t.Fatalf("seed other-tenant parent: %v", err)
	}
	crossTenant := delegatedChildOf(other.ID)
	requireRefusal(t, unfenced.Create(ctx, crossTenant), sessionstore.RefusalDelegationParentRevoked)
	requireNoRow(t, unfenced, crossTenant.ID)
}

// TestMemstoreCreateFenceDelegatedChildWithoutParentIDRefuses_spec_12_8
// asserts that a row with delegation depth one and an empty parent session
// ID is refused with DELEGATION_PARENT_REVOKED by Create and CreateReserved,
// on a fenced store and on one without a marker reader, while its
// reservation is admitted. Such a row names no parent row, so the parent
// rule refuses it.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 8.2 (Parent freshness)
// diagnosis: a failure means the insert path reads the parent only when the
// parent ID is non-empty, so a delegated child with no parent ID escapes the
// parent rule.
func TestMemstoreCreateFenceDelegatedChildWithoutParentIDRefuses_spec_12_8(t *testing.T) {
	ctx := context.Background()
	orphan := func() sessionstore.Session {
		c := delegatedChildOf("")
		c.RootSessionID = ""
		return c
	}

	f := newFenceFixture(t)
	created := orphan()
	ref := refusalOf(t, f.sessions.Create(ctx, created), sessionstore.RefusalDelegationParentRevoked)
	if !reflect.DeepEqual(ref.Details, map[string]any{"parentSessionId": ""}) {
		t.Fatalf("details = %v, want an empty parentSessionId", ref.Details)
	}
	requireNoRow(t, f.sessions, created.ID)

	reserved := orphan()
	if err := f.sessions.Reserve(ctx, fenceTenant, fenceOwner, reserved.ID, true); err != nil {
		t.Fatalf("Reserve for a delegated child: %v, want admit (no parent rule)", err)
	}
	requireRefusal(t, f.sessions.CreateReserved(ctx, reserved), sessionstore.RefusalDelegationParentRevoked)
	requireNoRow(t, f.sessions, reserved.ID)

	unfenced := memstore.New()
	bare := orphan()
	requireRefusal(t, unfenced.Create(ctx, bare), sessionstore.RefusalDelegationParentRevoked)
	requireNoRow(t, unfenced, bare.ID)
}

// TestMemstoreCreateFenceParentRuleScope_spec_12_8 asserts the rows the
// parent rule leaves alone and its place in the refusal order: a depth-zero
// row naming a terminal source (derive, replay) is admitted, a reservation
// for a delegated child is admitted while the parent is terminal, and a
// tenant marker wins over a terminal parent.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 8.2 (Parent freshness)
func TestMemstoreCreateFenceParentRuleScope_spec_12_8(t *testing.T) {
	ctx := context.Background()
	f := newFenceFixture(t)
	parent := seedParent(t, f.sessions, session.StateCompleted)

	derived := liveRow(fenceOwner)
	derived.ParentSessionID = parent.ID
	if err := f.sessions.Create(ctx, derived); err != nil {
		t.Fatalf("depth-zero row under a terminal source: %v, want admit", err)
	}

	reservedChild := delegatedChildOf(parent.ID)
	if err := f.sessions.Reserve(ctx, fenceTenant, fenceOwner, reservedChild.ID, true); err != nil {
		t.Fatalf("Reserve while the parent is terminal: %v, want admit", err)
	}

	setTenant(func(tn *tenantstore.Tenant) { tn.Suspended = true })(t, f)
	requireRefusal(t, f.sessions.Create(ctx, delegatedChildOf(parent.ID)), sessionstore.RefusalTenantSuspended)
}

// TestMemstoreCreateFenceParentTerminalWriteSerializes_spec_12_8 asserts the
// in-memory store's serialization of a child insert with its parent's
// terminal Update: a child created before the Update is listed as the
// parent's child afterwards, and a child created after it is refused.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 8.10 (Delegation Tree Recovery)
func TestMemstoreCreateFenceParentTerminalWriteSerializes_spec_12_8(t *testing.T) {
	ctx := context.Background()
	f := newFenceFixture(t)
	parent := seedParent(t, f.sessions, session.StateRunning)
	before := delegatedChildOf(parent.ID)
	if err := f.sessions.Create(ctx, before); err != nil {
		t.Fatalf("child before the terminal write: %v", err)
	}
	if _, err := f.sessions.Update(ctx, fenceTenant, parent.ID, func(r *sessionstore.Session) error {
		r.State = session.StateFailed
		return nil
	}); err != nil {
		t.Fatalf("terminal write: %v", err)
	}
	byParent, err := sessionstore.ChildrenByParent(ctx, f.sessions, fenceTenant)
	if err != nil {
		t.Fatalf("ChildrenByParent: %v", err)
	}
	if kids := byParent[parent.ID]; len(kids) != 1 || kids[0].ID != before.ID {
		t.Fatalf("children of the ended parent = %v, want [%s]", kids, before.ID)
	}
	after := delegatedChildOf(parent.ID)
	requireRefusal(t, f.sessions.Create(ctx, after), sessionstore.RefusalDelegationParentRevoked)
	requireNoRow(t, f.sessions, after.ID)
}
