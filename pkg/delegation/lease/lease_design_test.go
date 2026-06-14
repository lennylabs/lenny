// SPDX-License-Identifier: MIT

package lease_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// spec: §4.2 line 161 design clarification — "delegation lease ==
// child session row + Redis budget keys". The v1 invariant is that
// a delegation lease has no dedicated row: every field of the lease
// is captured on the child session row.
//
// This test pins the invariant: it seeds a parent + child session
// and asserts that every documented lease field has a corresponding
// home on the child row.
//
//   - granted_at        ==> sessions.created_at on the child
//   - expiry            ==> sessions.resume_eligible_until on child
//   - parent reference  ==> sessions.parent_session_id on child
//   - policy reference  ==> sessions.policy_enforcement_state on
//     child (gateway writes the resolved
//     §8.3 effective policy here)
//
// The runtime budget (LeaseSlice arithmetic) is held in Redis under
// {root_session_id}:dlg:* and is not part of the session row; that
// path is exercised by the in-package ValidateChildSlice tests.
func TestDelegationLeaseFieldsCapturedByChildSessionRow(t *testing.T) {
	store := memstore.New()
	ctx := context.Background()

	parentCreated := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(ctx, sessionstore.Session{
		ID:        "sess_parent",
		TenantID:  "acme",
		State:     session.StateRunning,
		CreatedAt: parentCreated,
		UpdatedAt: parentCreated,
	}); err != nil {
		t.Fatalf("seed parent: %v", err)
	}

	childCreated := parentCreated.Add(time.Second)
	leaseExpiry := childCreated.Add(2 * time.Hour)
	policy := []byte(`{"policy":"acme-default","ceiling":{"maxDepth":3}}`)
	if err := store.Create(ctx, sessionstore.Session{
		ID:                     "sess_child",
		TenantID:               "acme",
		ParentSessionID:        "sess_parent",
		State:                  session.StateRunning,
		CreatedAt:              childCreated,
		UpdatedAt:              childCreated,
		ResumeEligibleUntil:    leaseExpiry,
		PolicyEnforcementState: policy,
	}); err != nil {
		t.Fatalf("seed child: %v", err)
	}

	row, err := store.Get(ctx, "acme", "sess_child")
	if err != nil {
		t.Fatalf("read child: %v", err)
	}

	// granted_at: the child row's CreatedAt records the moment the
	// gateway issued the lease.
	if !row.CreatedAt.Equal(childCreated) {
		t.Errorf("CreatedAt = %v, want %v (§4.2 line 161 granted_at)",
			row.CreatedAt, childCreated)
	}
	// expiry: the §4.2 line 159 resume window doubles as the lease
	// lifetime cap.
	if !row.ResumeEligibleUntil.Equal(leaseExpiry) {
		t.Errorf("ResumeEligibleUntil = %v, want %v (§4.2 line 161 expiry)",
			row.ResumeEligibleUntil, leaseExpiry)
	}
	// parent reference: the §4.2 line 157 lineage pointer.
	if row.ParentSessionID != "sess_parent" {
		t.Errorf("ParentSessionID = %q, want sess_parent (§4.2 line 161 parent ref)",
			row.ParentSessionID)
	}
	// policy reference: the gateway records the resolved §8.3
	// effective policy on the child for replay and audit.
	if string(row.PolicyEnforcementState) != string(policy) {
		t.Errorf("PolicyEnforcementState = %s, want %s (§4.2 line 161 policy ref)",
			row.PolicyEnforcementState, policy)
	}
}
