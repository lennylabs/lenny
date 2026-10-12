// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotafailopen"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotastore"
	sessioncheckpointmetapg "github.com/lennylabs/lenny/pkg/gateway/session/sessioncheckpointmeta/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
)

// buildTenantErasers assembles the §12.8 tenant-deletion Phase 4 eraser
// sequence over the stores this gateway wires, in the dependency order
// the specification fixes, and refuses to start when the sequence breaks
// that order. Each store is included only when its backend is present,
// so a no-Redis or no-Postgres posture erases the subset it holds.
//
// The order matters because Phase 4 stops at the first store that fails.
// session_tree_archive holds a foreign key into sessions with no ON
// DELETE action, so before it was wired here every tenant with a settled
// delegation tree failed Phase 4 at the SessionStore step and never
// reached its receipt. The checkpoint stores and the session transcripts
// carry no such key, but their rows are tenant data in the ArtifactStore
// family the specification places at step 7, so they are erased here
// rather than left behind the tombstone.
//
// spec: §12.8 (Compliance Interfaces, tenant deletion Phase 4).
func (w *gatewayWiring) buildTenantErasers(
	quotaCounter *quotastore.Counter,
	quotaFailOpenAccum *quotafailopen.Accumulator,
	credentialPools credentialpoolstore.Store,
) []namedTenantEraser {
	erasers := w.tenantErasersBeforeSessions(quotaCounter, quotaFailOpenAccum)
	// SessionStore is the FK parent of every session-keyed store above.
	erasers = append(erasers, namedTenantEraser{"sessions", w.sessions.DeleteByTenant})
	if w.pgPool != nil {
		erasers = append(erasers,
			namedTenantEraser{"tokens", issuedtokenstore.New(w.pgPool).DeleteByTenant})
	}
	if credentialPools != nil {
		erasers = append(erasers,
			namedTenantEraser{"credential_pool", credentialPools.DeleteByTenant})
	}
	if err := erasure.ValidateTenantOrder(tenantEraserNames(erasers)); err != nil {
		log.Fatalf("lenny-gateway: §12.8 tenant-deletion eraser order: %v", err)
	}
	return erasers
}

// tenantErasersBeforeSessions returns the Phase 4 erasers that run
// before SessionStore, in §12.8 rank order.
func (w *gatewayWiring) tenantErasersBeforeSessions(
	quotaCounter *quotastore.Counter,
	quotaFailOpenAccum *quotafailopen.Accumulator,
) []namedTenantEraser {
	var erasers []namedTenantEraser
	if w.erasureLeaseStore != nil {
		erasers = append(erasers, namedTenantEraser{"leases", w.erasureLeaseStore.DeleteByTenant})
	}
	// §12.8 Phase 4 quota erasure spans the whole §12.2 QuotaStore role:
	// Redis counter, Postgres token_usage_checkpoint, and the in-memory
	// fail-open accumulator.
	if quotaEraser := buildQuotaEraser(quotaCounter, w.pgPool, quotaFailOpenAccum); quotaEraser != nil {
		erasers = append(erasers, namedTenantEraser{"quota", quotaEraser.DeleteByTenant})
	}
	erasers = append(erasers, w.checkpointTenantErasers()...)
	if w.transcripts != nil {
		erasers = append(erasers, namedTenantEraser{"transcripts", w.transcripts.DeleteByTenant})
	}
	if w.interactions != nil {
		erasers = append(erasers, namedTenantEraser{"interactions", w.interactions.DeleteByTenant})
	}
	if w.memories != nil {
		erasers = append(erasers, namedTenantEraser{"memory", countless(w.memories.DeleteByTenant)})
	}
	// session_tree_archive.root_session_id references sessions(id) with
	// no ON DELETE action, so the archive must be empty before the
	// SessionStore step.
	if w.treeArchive != nil {
		erasers = append(erasers, namedTenantEraser{"session_tree_archive", w.treeArchive.DeleteByTenant})
	}
	if w.evals != nil {
		erasers = append(erasers, namedTenantEraser{"eval_results", w.evals.DeleteByTenant})
	}
	return erasers
}

// checkpointTenantErasers returns the erasers for the checkpoint stores
// of the ArtifactStore family: the checkpoint manifests, the retention
// catalog, and the per-session checkpoint metadata. The metadata store is
// stateless over Postgres, so it is constructed here; without Postgres
// its in-memory instance lives with the preStop barrier and holds no
// durable tenant data.
func (w *gatewayWiring) checkpointTenantErasers() []namedTenantEraser {
	var erasers []namedTenantEraser
	if w.partialManifests != nil {
		erasers = append(erasers, namedTenantEraser{"checkpoint_manifests", countless(w.partialManifests.DeleteByTenant)})
	}
	if w.checkpointRetention != nil {
		erasers = append(erasers, namedTenantEraser{"checkpoint_retention", countless(w.checkpointRetention.DeleteByTenant)})
	}
	if w.pgPool != nil {
		erasers = append(erasers, namedTenantEraser{"checkpoint_meta", countless(sessioncheckpointmetapg.New(w.pgPool, nil).DeleteByTenant)})
	}
	return erasers
}

// countless adapts an error-only DeleteByTenant to the counting form the
// Phase 4 receipt tallies. The store reports no count, so it records 0.
func countless(fn func(ctx context.Context, tenantID string) error) func(ctx context.Context, tenantID string) (int, error) {
	return func(ctx context.Context, tenantID string) (int, error) {
		return 0, fn(ctx, tenantID)
	}
}

// tenantEraserNames returns the store names of erasers in order.
func tenantEraserNames(erasers []namedTenantEraser) []string {
	names := make([]string, len(erasers))
	for i, e := range erasers {
		names[i] = e.name
	}
	return names
}
