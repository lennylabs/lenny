// SPDX-License-Identifier: MIT

package quotafailopen

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/quotastore"
	"github.com/lennylabs/lenny/pkg/quota"
)

// spec: §12.8 step 6; §12.1 line 5 — DeleteByUser removes the named user's
// accumulated windows while leaving other users and the per-tenant rollup
// intact, so a post-recovery reconcile cannot resurrect the erased user.
func TestDeleteByUser_RemovesOnlyTargetUser_spec_12_8_step6(t *testing.T) {
	a := New()
	a.Record("acme", "alice", quota.ResetHourly, fixed, 100)
	a.Record("acme", "alice", quota.ResetDaily, fixed, 40)
	a.Record("acme", "bob", quota.ResetHourly, fixed, 30)

	// alice holds two per-user windows (hourly + daily). The two Records
	// also advanced the per-tenant rollup for each period.
	n, err := a.DeleteByUser(context.Background(), "acme", "alice")
	if err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if n != 2 {
		t.Errorf("deleted = %d, want 2 (alice hourly + daily)", n)
	}
	if got := a.UserWindow("acme", "alice", quota.ResetHourly, fixed); got != 0 {
		t.Errorf("alice hourly after erase = %d, want 0", got)
	}
	if got := a.UserWindow("acme", "alice", quota.ResetDaily, fixed); got != 0 {
		t.Errorf("alice daily after erase = %d, want 0", got)
	}
	// bob is untouched.
	if got := a.UserWindow("acme", "bob", quota.ResetHourly, fixed); got != 30 {
		t.Errorf("bob hourly after alice erase = %d, want 30", got)
	}
	// The per-tenant rollup survives a single user's erasure.
	if got := a.TenantRollup("acme", quota.ResetHourly, fixed); got != 130 {
		t.Errorf("tenant rollup after alice erase = %d, want 130", got)
	}
}

// spec: §12.8 line 753 — an empty scope is never a wildcard.
func TestDeleteByUser_EmptyScopeRejected_spec_12_8(t *testing.T) {
	a := New()
	a.Record("acme", "alice", quota.ResetHourly, fixed, 100)
	for _, tc := range []struct{ tenant, user string }{
		{"", "alice"},
		{"acme", ""},
		{"", ""},
	} {
		if _, err := a.DeleteByUser(context.Background(), tc.tenant, tc.user); !errors.Is(err, quotastore.ErrEmptyScope) {
			t.Errorf("DeleteByUser(%q,%q) err = %v, want ErrEmptyScope", tc.tenant, tc.user, err)
		}
	}
	if a.Len() == 0 {
		t.Errorf("entries should be untouched after rejected erasure")
	}
}

// spec: §12.8 Phase 4 — DeleteByTenant removes every window for the tenant
// (including the rollup) and leaves other tenants intact.
func TestDeleteByTenant_RemovesWholeTenant_spec_12_8_phase4(t *testing.T) {
	a := New()
	a.Record("acme", "alice", quota.ResetHourly, fixed, 100)
	a.Record("acme", "bob", quota.ResetHourly, fixed, 30)
	a.Record("globex", "carol", quota.ResetHourly, fixed, 70)

	n, err := a.DeleteByTenant(context.Background(), "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	// acme: alice window + bob window + tenant rollup = 3 entries.
	if n != 3 {
		t.Errorf("deleted = %d, want 3", n)
	}
	if got := a.TenantRollup("acme", quota.ResetHourly, fixed); got != 0 {
		t.Errorf("acme rollup after tenant erase = %d, want 0", got)
	}
	if got := a.UserWindow("globex", "carol", quota.ResetHourly, fixed); got != 70 {
		t.Errorf("globex untouched = %d, want 70", got)
	}
}

func TestDeleteByTenant_EmptyRejected_spec_12_8(t *testing.T) {
	a := New()
	if _, err := a.DeleteByTenant(context.Background(), ""); !errors.Is(err, quotastore.ErrEmptyScope) {
		t.Errorf("DeleteByTenant(\"\") err = %v, want ErrEmptyScope", err)
	}
}

// A nil accumulator (no fail-open backend wired) erases to (0, nil).
func TestDeleteNilAccumulator(t *testing.T) {
	var a *Accumulator
	if n, err := a.DeleteByUser(context.Background(), "acme", "alice"); n != 0 || err != nil {
		t.Errorf("nil DeleteByUser = (%d,%v), want (0,nil)", n, err)
	}
	if n, err := a.DeleteByTenant(context.Background(), "acme"); n != 0 || err != nil {
		t.Errorf("nil DeleteByTenant = (%d,%v), want (0,nil)", n, err)
	}
}
