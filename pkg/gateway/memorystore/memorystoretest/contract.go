// SPDX-License-Identifier: MIT

// Package memorystoretest provides the §9.4 contract-validation helper a
// deployer runs against a custom MemoryStore backend (Mem0, Zep, or
// another vector database) before wiring it into production. It is
// published as a normal package — not a `_test.go` file — so deployer
// CI can import it and run it against the real backend.
//
// ValidateMemoryStoreIsolation exercises the §9.4 tenant-isolation and
// empty-scope-rejection contract, the §9.4 instrumentation contract (all
// six operation labels observed) for backends that expose the Observer
// seam, and the §12.8 erasure stub-detection contract by delegating to
// memorystore.ValidateMemoryStoreErasure.
package memorystoretest

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/memorystore"
)

// observerSetter is the optional Observer seam both default backends
// expose. When the store under test implements it, the helper installs a
// recording observer to assert the §9.4 instrumentation contract.
type observerSetter interface {
	SetObserver(memorystore.Observer)
}

// recordingObserver counts the operation labels passed to
// ObserveOperation so the helper can assert that all six §9.4 / §16.1
// operation labels were exercised.
type recordingObserver struct {
	mu  sync.Mutex
	ops map[string]int
}

func (r *recordingObserver) ObserveOperation(operation string, _ float64) {
	r.mu.Lock()
	r.ops[operation]++
	r.mu.Unlock()
}

func (r *recordingObserver) IncError(string, string)     {}
func (r *recordingObserver) SetRecordCount(string, int)  {}
func (r *recordingObserver) IncUserOverThreshold(string) {}

func (r *recordingObserver) seen(operation string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ops[operation]
}

// Reserved contract-test scope. The ids are namespaced so the helper can
// run against a shared backend without colliding with real data. The
// Postgres default backend satisfies the agent_memory → tenants(id)
// foreign key via the reserved tenants seeded by migrations; deployers
// running this against a custom backend pass tenant ids their backend
// accepts (the helper does not assume any external schema).
const (
	contractTenantA = memorystore.PreflightTenantID
	contractTenantB = "__preflight_b__"
	contractUser    = memorystore.PreflightUserID
)

// ValidateMemoryStoreIsolation is the §9.4 line 200 contract helper. It
// fails the test when the store violates tenant isolation, silently
// defaults an empty scope, fails the §12.8 erasure stub-detection, or
// (for a backend exposing the Observer seam) omits an operation label
// from its instrumentation.
//
// spec: §9.4 lines 196, 200, 204; §12.8 lines 743-758.
func ValidateMemoryStoreIsolation(t *testing.T, store memorystore.Store) {
	t.Helper()
	ctx := context.Background()

	var rec *recordingObserver
	if s, ok := store.(observerSetter); ok {
		rec = &recordingObserver{ops: map[string]int{}}
		s.SetObserver(rec)
		defer s.SetObserver(nil)
	}

	cleanup := func() {
		_ = store.DeleteByTenant(ctx, contractTenantA)
		_ = store.DeleteByTenant(ctx, contractTenantB)
	}
	cleanup()
	defer cleanup()

	scopeA := memorystore.MemoryScope{TenantID: contractTenantA, UserID: contractUser}
	scopeB := memorystore.MemoryScope{TenantID: contractTenantB, UserID: contractUser}

	// §9.4: a Write under tenant A followed by a Query under tenant B
	// returns zero results — cross-tenant reads are impossible.
	if err := store.Write(ctx, scopeA, []memorystore.Memory{{Content: "tenant A private memory"}}); err != nil {
		t.Fatalf("Write(tenantA): %v", err)
	}
	if got, err := store.Query(ctx, scopeB, "", 0); err != nil {
		t.Fatalf("Query(tenantB): %v", err)
	} else if len(got) != 0 {
		t.Fatalf("cross-tenant Query(tenantB) returned %d rows, want 0 — §9.4 tenant isolation violated", len(got))
	}
	// List is scoped to the tenant identically to Query.
	if got, err := store.List(ctx, scopeB, memorystore.MemoryFilter{}); err != nil {
		t.Fatalf("List(tenantB): %v", err)
	} else if len(got) != 0 {
		t.Fatalf("cross-tenant List(tenantB) returned %d rows, want 0 — §9.4 tenant isolation violated", len(got))
	}
	// Tenant A still observes its own memory.
	own, err := store.Query(ctx, scopeA, "", 0)
	if err != nil {
		t.Fatalf("Query(tenantA): %v", err)
	}
	if len(own) != 1 {
		t.Fatalf("Query(tenantA) returned %d rows, want 1 (the row just written)", len(own))
	}
	// Exercise the per-record Delete label so the instrumentation
	// assertion below covers all six operation labels.
	if err := store.Delete(ctx, scopeA, []string{own[0].ID}); err != nil {
		t.Fatalf("Delete(tenantA): %v", err)
	}

	assertRejectsEmptyTenant(t, ctx, store)
	assertRejectsEmptyUser(t, ctx, store)

	// §12.8: the erasure stub-detection contract.
	if err := memorystore.ValidateMemoryStoreErasure(ctx, store); err != nil {
		t.Fatalf("ValidateMemoryStoreErasure: %v", err)
	}

	// §9.4 instrumentation contract: when the backend exposes the
	// Observer seam, each of the six operation labels must have produced
	// at least one observation during this exercise.
	if rec != nil {
		for _, op := range []string{
			memorystore.OpWrite, memorystore.OpQuery, memorystore.OpDelete,
			memorystore.OpList, memorystore.OpDeleteByUser, memorystore.OpDeleteByTenant,
		} {
			if rec.seen(op) == 0 {
				t.Errorf("no lenny_memory_store_operation_duration_seconds observation under operation=%q during the contract exercise (§9.4 line 200)", op)
			}
		}
	}
}

// assertRejectsEmptyTenant verifies every scoped method rejects an empty
// TenantID with ErrEmptyTenant rather than silently defaulting it.
func assertRejectsEmptyTenant(t *testing.T, ctx context.Context, store memorystore.Store) {
	t.Helper()
	emptyTenant := memorystore.MemoryScope{TenantID: "", UserID: contractUser}
	if err := store.Write(ctx, emptyTenant, []memorystore.Memory{{Content: "x"}}); !errors.Is(err, memorystore.ErrEmptyTenant) {
		t.Errorf("Write(empty tenant) error = %v, want ErrEmptyTenant", err)
	}
	if _, err := store.Query(ctx, emptyTenant, "", 0); !errors.Is(err, memorystore.ErrEmptyTenant) {
		t.Errorf("Query(empty tenant) error = %v, want ErrEmptyTenant", err)
	}
	if _, err := store.List(ctx, emptyTenant, memorystore.MemoryFilter{}); !errors.Is(err, memorystore.ErrEmptyTenant) {
		t.Errorf("List(empty tenant) error = %v, want ErrEmptyTenant", err)
	}
	if err := store.Delete(ctx, emptyTenant, []string{"id"}); !errors.Is(err, memorystore.ErrEmptyTenant) {
		t.Errorf("Delete(empty tenant) error = %v, want ErrEmptyTenant", err)
	}
	if err := store.DeleteByUser(ctx, "", contractUser); !errors.Is(err, memorystore.ErrEmptyTenant) {
		t.Errorf("DeleteByUser(empty tenant) error = %v, want ErrEmptyTenant", err)
	}
	if err := store.DeleteByTenant(ctx, ""); !errors.Is(err, memorystore.ErrEmptyTenant) {
		t.Errorf("DeleteByTenant(empty tenant) error = %v, want ErrEmptyTenant", err)
	}
}

// assertRejectsEmptyUser verifies the user-scoped methods reject an empty
// UserID with ErrEmptyUser.
func assertRejectsEmptyUser(t *testing.T, ctx context.Context, store memorystore.Store) {
	t.Helper()
	emptyUser := memorystore.MemoryScope{TenantID: contractTenantA, UserID: ""}
	if err := store.Write(ctx, emptyUser, []memorystore.Memory{{Content: "x"}}); !errors.Is(err, memorystore.ErrEmptyUser) {
		t.Errorf("Write(empty user) error = %v, want ErrEmptyUser", err)
	}
	if _, err := store.Query(ctx, emptyUser, "", 0); !errors.Is(err, memorystore.ErrEmptyUser) {
		t.Errorf("Query(empty user) error = %v, want ErrEmptyUser", err)
	}
	if _, err := store.List(ctx, emptyUser, memorystore.MemoryFilter{}); !errors.Is(err, memorystore.ErrEmptyUser) {
		t.Errorf("List(empty user) error = %v, want ErrEmptyUser", err)
	}
	if err := store.DeleteByUser(ctx, contractTenantA, ""); !errors.Is(err, memorystore.ErrEmptyUser) {
		t.Errorf("DeleteByUser(empty user) error = %v, want ErrEmptyUser", err)
	}
}
