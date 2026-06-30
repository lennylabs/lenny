// SPDX-License-Identifier: MIT

package auditstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
)

// spec: §12.1 line 5 — the EventStore (audit) role interface exposes
// the mandatory erasure pair, enforced at compile time.
func TestStoreSatisfiesEventStore_spec_12_1(t *testing.T) {
	t.Parallel()
	var _ auditstore.EventStore = (*auditstore.Store)(nil)
}

// spec: §12.1 line 5 / §12.8 line 775 — user erasure retains the audit
// ledger (gdpr.* receipts are exempt, dead-lettered rows are redacted
// not deleted, and audit_log has no user_id column), so DeleteByUser is
// a no-op returning (0, nil) and never touches the pool.
func TestDeleteByUserRetainsAudit_spec_12_8_line775(t *testing.T) {
	t.Parallel()
	s := auditstore.New(nil) // no DB needed: DeleteByUser is a no-op
	n, err := s.DeleteByUser(context.Background(), "acme", "alice@acme.com")
	if err != nil || n != 0 {
		t.Fatalf("DeleteByUser = (%d, %v), want (0, nil)", n, err)
	}
}

// spec: §12.8 line 753 — empty arguments must never be treated as
// "delete everything"; the erasure primitives reject them before any
// database access.
func TestErasureRejectsEmptyScope_spec_12_8_line753(t *testing.T) {
	t.Parallel()
	s := auditstore.New(nil)
	ctx := context.Background()
	if _, err := s.DeleteByUser(ctx, "", "alice@acme.com"); !errors.Is(err, auditstore.ErrEmptyScope) {
		t.Errorf("DeleteByUser(emptyTenant) err = %v, want ErrEmptyScope", err)
	}
	if _, err := s.DeleteByUser(ctx, "acme", ""); !errors.Is(err, auditstore.ErrEmptyScope) {
		t.Errorf("DeleteByUser(emptyUser) err = %v, want ErrEmptyScope", err)
	}
	// DeleteByTenant("") returns ErrEmptyScope before dereferencing the
	// nil pool, so a mis-scoped teardown cannot wipe the ledger.
	if _, err := s.DeleteByTenant(ctx, ""); !errors.Is(err, auditstore.ErrEmptyScope) {
		t.Errorf("DeleteByTenant(empty) err = %v, want ErrEmptyScope", err)
	}
}
