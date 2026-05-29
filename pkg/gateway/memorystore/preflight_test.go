// SPDX-License-Identifier: MIT

package memorystore_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/memorystore"
)

// fakeStore embeds a working InMemory backend and selectively breaks one
// method so the §12.8 erasure preflight (memorystore.ValidateMemoryStoreErasure)
// can be exercised against the no-op / error failure modes it must catch.
type fakeStore struct {
	*memorystore.InMemory
	writeErr             error
	dropWrites           bool
	noopDeleteUser       bool
	noopDeleteTenant     bool
	failSecondDeleteUser bool
	deleteUserCalls      int
}

func newFakeStore() *fakeStore { return &fakeStore{InMemory: memorystore.NewInMemory(0, nil)} }

func (f *fakeStore) Write(ctx context.Context, scope memorystore.MemoryScope, mems []memorystore.Memory) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	if f.dropWrites {
		// A signature-satisfying no-op: returns nil without persisting.
		return nil
	}
	return f.InMemory.Write(ctx, scope, mems)
}

func (f *fakeStore) DeleteByUser(ctx context.Context, tenantID, userID string) error {
	f.deleteUserCalls++
	if f.failSecondDeleteUser && f.deleteUserCalls >= 2 {
		return errors.New("transient backend error on repeat delete")
	}
	if f.noopDeleteUser {
		return nil
	}
	return f.InMemory.DeleteByUser(ctx, tenantID, userID)
}

func (f *fakeStore) DeleteByTenant(ctx context.Context, tenantID string) error {
	if f.noopDeleteTenant {
		return nil
	}
	return f.InMemory.DeleteByTenant(ctx, tenantID)
}

// spec: §12.8 lines 743-758 — the startup/per-job preflight passes when
// the backend honors both erasure primitives.
func TestValidateMemoryStoreErasure_InMemoryPasses(t *testing.T) {
	if err := memorystore.ValidateMemoryStoreErasure(context.Background(), memorystore.NewInMemory(0, nil)); err != nil {
		t.Fatalf("ValidateMemoryStoreErasure on the default backend = %v, want nil", err)
	}
}

// spec: §12.8 line 746 — a backend whose DeleteByUser satisfies the
// signature but silently no-ops must be caught (the seeded row survives).
func TestValidateMemoryStoreErasure_DetectsNoOpDeleteByUser(t *testing.T) {
	f := newFakeStore()
	f.noopDeleteUser = true
	err := memorystore.ValidateMemoryStoreErasure(context.Background(), f)
	if err == nil {
		t.Fatal("ValidateMemoryStoreErasure = nil, want an error for a no-op DeleteByUser")
	}
	if !strings.Contains(err.Error(), "DeleteByUser is a silent no-op") {
		t.Errorf("error = %q, want it to name the no-op DeleteByUser", err)
	}
}

// spec: §12.8 line 746 — "The preflight also covers DeleteByTenant using
// the same seeded row."
func TestValidateMemoryStoreErasure_DetectsNoOpDeleteByTenant(t *testing.T) {
	f := newFakeStore()
	f.noopDeleteTenant = true
	err := memorystore.ValidateMemoryStoreErasure(context.Background(), f)
	if err == nil {
		t.Fatal("ValidateMemoryStoreErasure = nil, want an error for a no-op DeleteByTenant")
	}
	if !strings.Contains(err.Error(), "DeleteByTenant is a silent no-op") {
		t.Errorf("error = %q, want it to name the no-op DeleteByTenant", err)
	}
}

// A Write that silently drops the probe row must not let the delete check
// pass vacuously — the seed-persistence guard catches it.
func TestValidateMemoryStoreErasure_DetectsSilentWriteDrop(t *testing.T) {
	f := newFakeStore()
	f.dropWrites = true
	err := memorystore.ValidateMemoryStoreErasure(context.Background(), f)
	if err == nil || !strings.Contains(err.Error(), "did not persist the probe row") {
		t.Fatalf("error = %v, want it to flag the probe row not persisting", err)
	}
}

// A backend error during the seed Write propagates rather than being
// swallowed — startup must fail closed.
func TestValidateMemoryStoreErasure_WriteErrorPropagates(t *testing.T) {
	f := newFakeStore()
	f.writeErr = errors.New("vector index unavailable")
	err := memorystore.ValidateMemoryStoreErasure(context.Background(), f)
	if err == nil || !strings.Contains(err.Error(), "vector index unavailable") {
		t.Fatalf("error = %v, want the underlying Write error", err)
	}
}

// spec: §9.4 — "Implementations MUST be idempotent: repeated invocation
// for the same (tenantID, userID) after successful completion returns nil."
func TestValidateMemoryStoreErasure_DetectsNonIdempotentDeleteByUser(t *testing.T) {
	f := newFakeStore()
	f.failSecondDeleteUser = true
	err := memorystore.ValidateMemoryStoreErasure(context.Background(), f)
	if err == nil || !strings.Contains(err.Error(), "not idempotent") {
		t.Fatalf("error = %v, want the idempotency violation", err)
	}
}

func TestValidateMemoryStoreErasure_NilStore(t *testing.T) {
	if err := memorystore.ValidateMemoryStoreErasure(context.Background(), nil); err == nil {
		t.Fatal("ValidateMemoryStoreErasure(nil) = nil, want an error")
	}
}

// The reserved preflight scope ids are stable: the migration that seeds
// the reserved tenant and the gateway startup wiring both depend on them.
func TestPreflightScopeConstants(t *testing.T) {
	if memorystore.PreflightTenantID != "__preflight__" {
		t.Errorf("PreflightTenantID = %q, want __preflight__ (§12.8 line 746)", memorystore.PreflightTenantID)
	}
	if memorystore.PreflightUserID != "__preflight_user__" {
		t.Errorf("PreflightUserID = %q, want __preflight_user__ (§12.8 line 746)", memorystore.PreflightUserID)
	}
}
