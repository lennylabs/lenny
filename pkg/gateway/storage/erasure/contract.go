// SPDX-License-Identifier: MIT

package erasure

import "context"

// StoreEraser is the §12.1 canonical mandatory-erasure contract that
// every store role interface MUST expose: DeleteByUser(ctx, tenantID,
// userID) and DeleteByTenant(ctx, tenantID), each returning a single
// error. This is the exact signature §12.1 line 5 and §9.4 pin for the
// pluggable roles (MemoryStore, SemanticCache) — the roles a deployer
// may swap for a custom backend, where the compile-time guarantee
// matters most. Those store interfaces are asserted against
// erasure.StoreEraser, so a substitute backend that omits either method
// does not compile into the gateway binary.
//
// spec: §12.1 line 5 ("Every store role interface ... MUST expose the
// erasure primitives DeleteByUser(ctx, tenantID, userID) error and
// DeleteByTenant(ctx, tenantID) error ... enforced at compile time by Go
// interface satisfaction").
type StoreEraser interface {
	DeleteByUser(ctx context.Context, tenantID, userID string) error
	DeleteByTenant(ctx context.Context, tenantID string) error
}

// CountingEraser is the platform-internal superset of StoreEraser: the
// Postgres-backed first-party stores (SessionStore, EventStore audit,
// BillingStore, QuotaStore, LeaseStore, InteractionStore, EvalResultStore)
// additionally return the deleted-row count so the §12.8 erasure receipt
// can report a per-store tally. The count is a superset of the §12.1
// contract, not a divergence from it: the mandatory primitives are still
// present with the mandated argument lists; the extra return value is
// surplus information the orchestrator records. DeleteByUserFunc /
// DeleteBySessionFunc adapt this signature at the orchestrator boundary,
// and the pluggable StoreEraser stores (which return only error) are
// adapted to a 0 count at the wiring site.
//
// EvictionStateStore is the one first-party store that satisfies neither
// interface directly: its DeleteByUser carries a trailing []string of the
// user's session IDs because eviction-state rows are session-keyed, and
// §12.8 step 9 erases them "for the user's sessions". That signature is
// compile-checked against evictionstatestore.Store and is a justified,
// documented exception rather than an unchecked divergence.
//
// spec: §12.8 ("DeleteByUser ... erasure receipt"); §12.1 line 5.
type CountingEraser interface {
	DeleteByUser(ctx context.Context, tenantID, userID string) (int, error)
	DeleteByTenant(ctx context.Context, tenantID string) (int, error)
}

// FromCounting builds a user-scoped Eraser from a typed CountingEraser
// store. Wiring a store through this constructor makes the §12.1
// interface-satisfaction check land at the orchestrator call site itself:
// a store that drops DeleteByUser stops satisfying CountingEraser and the
// gateway binary fails to compile, rather than being silently registered
// as a no-op function pointer.
//
// spec: §12.1 line 5 (compile-time erasure-method enforcement).
func FromCounting(name string, e CountingEraser) Eraser {
	return Eraser{Name: name, DeleteByUser: e.DeleteByUser}
}

// FromStore builds a user-scoped Eraser from a typed StoreEraser — the
// §12.1 / §9.4 pluggable roles (MemoryStore, SemanticCache) a deployer may
// replace with a custom backend. The error-only contract signature is
// adapted to the orchestrator's (count, error) adapter with a 0 count,
// since these roles report no deleted-row tally. As with FromCounting, a
// substitute backend that omits a method fails to satisfy StoreEraser at
// this call site.
//
// spec: §12.1 line 5; §9.4.
func FromStore(name string, e StoreEraser) Eraser {
	return Eraser{Name: name, DeleteByUser: func(ctx context.Context, tenantID, userID string) (int, error) {
		return 0, e.DeleteByUser(ctx, tenantID, userID)
	}}
}
