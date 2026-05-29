// SPDX-License-Identifier: MIT

package issuedtokenstore

import (
	"context"
	"testing"
)

// var _ Eraser = (*Store)(nil) in the package proves the §12.1
// compile-time contract; this test documents that the assertion is the
// load-bearing guarantee and exercises the guard clauses that run
// before any database access.

// spec: §12.1 line 5 — DeleteByUser rejects empty scope ids so a
// malformed erasure cannot widen to every row. The guard clause runs
// before pgtenant.InTx, so the nil pool is never dereferenced.
func TestDeleteByUserEmptyScopeRejected_spec_12_1(t *testing.T) {
	s := New(nil)
	if _, err := s.DeleteByUser(context.Background(), "", "alice@acme.com"); err == nil {
		t.Fatal("DeleteByUser with empty tenant id should error")
	}
	if _, err := s.DeleteByUser(context.Background(), "acme", ""); err == nil {
		t.Fatal("DeleteByUser with empty user id should error")
	}
}

// spec: §12.1 line 5 — DeleteByTenant rejects the empty tenant id so an
// unscoped tenant deletion never runs.
func TestDeleteByTenantEmptyRejected_spec_12_1(t *testing.T) {
	s := New(nil)
	if _, err := s.DeleteByTenant(context.Background(), ""); err == nil {
		t.Fatal("DeleteByTenant with empty tenant id should error")
	}
}

// spec: §12.1 line 5 — the TokenIssuanceStore satisfies the Eraser
// contract; this makes the compile-time assertion explicit in a test so
// the intent survives a refactor.
func TestStoreSatisfiesEraser_spec_12_1(t *testing.T) {
	var _ Eraser = New(nil)
}
