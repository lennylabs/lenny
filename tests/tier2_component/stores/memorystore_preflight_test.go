//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §12.8 MemoryStore erasure preflight and the §9.4
// ValidateMemoryStoreIsolation contract helper against the Postgres-backed
// pkg/gateway/memorystore/pgstore with the production migrations applied.
// The migration that seeds the reserved __preflight__ tenant (FK parent
// for the synthetic agent_memory probe row) is part of the applied set, so
// the preflight write/erase/requery cycle runs end to end against a real
// database exactly as it does at gateway startup.
package stores_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/memorystore"
	"github.com/lennylabs/lenny/pkg/gateway/memorystore/memorystoretest"
	memorypg "github.com/lennylabs/lenny/pkg/gateway/memorystore/pgstore"
)

// spec: §12.8 lines 743-758 — the startup erasure preflight passes against
// the default Postgres backend: the seeded probe row under the reserved
// __preflight__ tenant is written and then fully erased by both
// DeleteByUser and DeleteByTenant.
// diagnosis: a failure means the startup erasure preflight does not pass
// against Postgres, so the memory store could ship without a working
// DeleteByUser/DeleteByTenant erasure path.
func TestMemoryStoreErasurePreflightPostgres(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := memorypg.New(pg.Pool)
	if err := memorystore.ValidateMemoryStoreErasure(context.Background(), store); err != nil {
		t.Fatalf("ValidateMemoryStoreErasure against Postgres: %v", err)
	}
	// Idempotent: a repeat run after a clean pass still succeeds.
	if err := memorystore.ValidateMemoryStoreErasure(context.Background(), store); err != nil {
		t.Fatalf("repeat ValidateMemoryStoreErasure: %v", err)
	}
}

// spec: §9.4 lines 200, 204 — the published contract helper passes against
// the default Postgres backend, covering tenant isolation, empty-scope
// rejection, the six-label instrumentation contract, and the §12.8 erasure
// stub-detection.
// diagnosis: a failure means the published memory-store contract helper
// fails against Postgres, indicating a break in tenant isolation,
// empty-scope rejection, instrumentation, or erasure stub-detection.
func TestMemoryStoreContractHelperPostgres(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := memorypg.New(pg.Pool)
	memorystoretest.ValidateMemoryStoreIsolation(t, store)
}
