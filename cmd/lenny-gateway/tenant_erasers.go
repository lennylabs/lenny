// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/audit/jwtaudit"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/semanticcache"
	delegationbudgetpg "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationbudget/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotafailopen"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotastore"
	sessioncheckpointmetapg "github.com/lennylabs/lenny/pkg/gateway/session/sessioncheckpointmeta/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
)

// tenantEraseFunc is one Phase 4 entry's DeleteByTenant: it erases the
// tenant's data in one store and returns the count removed.
type tenantEraseFunc func(ctx context.Context, tenantID string) (int, error)

// buildTenantErasers assembles the §12.8 tenant-deletion Phase 4 eraser
// sequence in the dependency order the specification fixes, and refuses to
// start when the sequence is out of order or leaves a ranked store neither
// wired nor exempt. Every ranked store is listed by name: a store whose
// backend is absent in this posture is listed with a nil function, which the
// eraser skips and the receipt omits.
//
// The order matters because Phase 4 stops at the first store that fails and a
// later pass re-runs the whole sequence. Every session-keyed entry lists the
// tenant's sessions from the primary and ranks before SessionStore, so a
// re-run after a partial failure still finds the session identifiers its
// entries need.
//
// spec: §12.8 (Compliance Interfaces: Tenant deletion lifecycle).
func (w *gatewayWiring) buildTenantErasers(
	quotaCounter *quotastore.Counter,
	quotaFailOpenAccum *quotafailopen.Accumulator,
	credentialPools credentialpoolstore.Store,
	semanticCache *semanticcache.InMemory,
) []namedTenantEraser {
	erasers := w.tenantErasersBeforeSessions(quotaCounter, quotaFailOpenAccum, semanticCache)
	// SessionStore is the FK parent of every session-keyed store above.
	erasers = append(erasers, namedTenantEraser{"sessions", w.sessions.DeleteByTenant})
	erasers = append(erasers, w.tenantErasersAfterSessions(credentialPools)...)
	if err := erasure.ValidateTenantOrder(tenantEraserNames(erasers)); err != nil {
		log.Fatalf("lenny-gateway: §12.8 tenant-deletion eraser order: %v", err)
	}
	return erasers
}

// tenantErasersBeforeSessions returns the Phase 4 erasers that run before
// SessionStore, in §12.8 rank order.
func (w *gatewayWiring) tenantErasersBeforeSessions(
	quotaCounter *quotastore.Counter,
	quotaFailOpenAccum *quotafailopen.Accumulator,
	semanticCache *semanticcache.InMemory,
) []namedTenantEraser {
	erasers := w.cacheTenantErasers(semanticCache)
	// §12.8 Phase 4 quota erasure spans the whole §12.2 QuotaStore role:
	// Redis counter, Postgres token_usage_checkpoint, and the in-memory
	// fail-open accumulator.
	var quota tenantEraseFunc
	if quotaEraser := buildQuotaEraser(quotaCounter, w.pgPool, quotaFailOpenAccum); quotaEraser != nil {
		quota = quotaEraser.DeleteByTenant
	}
	erasers = append(erasers, namedTenantEraser{"quota", quota})
	// The tenant prefix delete removes every object under the tenant's
	// prefix, including objects that have no session row. Phase 3.5 has
	// already copied held evidence to the escrow bucket.
	var artifacts tenantEraseFunc
	if pd, ok := w.blobs.(blobstore.TenantPrefixDeleter); ok {
		artifacts = pd.DeleteByTenant
	}
	erasers = append(erasers, namedTenantEraser{"artifacts", artifacts})
	erasers = append(erasers, w.checkpointTenantErasers()...)
	erasers = append(erasers, w.sessionDataTenantErasers()...)
	// §12.8 Phase 4 session message queues: each session's DLQ and durable
	// inbox keys, named by session identifiers that must be listed before
	// the SessionStore entry deletes them.
	var queues tenantEraseFunc
	if w.messagingCoord != nil {
		queues = sessionKeyedTenantEraser(w.sessions, func(ctx context.Context, s sessionstore.Session) (int, error) {
			return w.messagingCoord.EraseSession(ctx, s.TenantID, s.ID)
		})
	}
	erasers = append(erasers, namedTenantEraser{"session_message_queues", queues})
	erasers = append(erasers, w.ledgerTenantErasers()...)
	return append(erasers, w.delegationBudgetTenantEraser())
}

// cacheTenantErasers returns the Phase 4 entries for the coordination
// leases, their Postgres mirror, and the Redis-resident caches that run first
// in the §12.8 order.
func (w *gatewayWiring) cacheTenantErasers(semanticCache *semanticcache.InMemory) []namedTenantEraser {
	var leases, mirror, semantic, sticky, stream tenantEraseFunc
	if w.erasureLeaseStore != nil {
		leases = w.erasureLeaseStore.DeleteByTenant
	}
	// The §10.1.8 Postgres mirror of the coordination leases.
	if w.coordMirror != nil {
		mirror = countless(w.coordMirror.DeleteByTenant)
	}
	if semanticCache != nil {
		semantic = countless(semanticCache.DeleteByTenant)
	}
	if w.erasureSticky != nil {
		sticky = w.erasureSticky.DeleteByTenant
	}
	if w.billingStream != nil {
		stream = unlessBillingExempt(w.tenants, w.billingStream.PurgeTenant)
	}
	return []namedTenantEraser{
		{"leases", leases},
		{"coordination_mirror", mirror},
		{"semantic_cache", semantic},
		{"experiment_sticky", sticky},
		{"billing_buffer", stream},
	}
}

// checkpointTenantErasers returns the erasers for the checkpoint stores
// of the ArtifactStore family: the checkpoint manifests, the retention
// catalog, and the per-session checkpoint metadata. The metadata store is
// stateless over Postgres, so it is constructed here; without Postgres
// its in-memory instance lives with the preStop barrier and no eraser
// reaches it.
func (w *gatewayWiring) checkpointTenantErasers() []namedTenantEraser {
	var manifests, retention, meta tenantEraseFunc
	if w.partialManifests != nil {
		manifests = countless(w.partialManifests.DeleteByTenant)
	}
	if w.checkpointRetention != nil {
		retention = countless(w.checkpointRetention.DeleteByTenant)
	}
	if w.pgPool != nil {
		meta = countless(sessioncheckpointmetapg.New(w.pgPool, nil).DeleteByTenant)
	}
	return []namedTenantEraser{
		{"checkpoint_manifests", manifests},
		{"checkpoint_retention", retention},
		{"checkpoint_meta", meta},
	}
}

// sessionDataTenantErasers returns the Phase 4 entries for the session
// transcripts, interaction records, and memories.
func (w *gatewayWiring) sessionDataTenantErasers() []namedTenantEraser {
	var transcripts, interactions, memories tenantEraseFunc
	if w.transcripts != nil {
		transcripts = w.transcripts.DeleteByTenant
	}
	if w.interactions != nil {
		interactions = w.interactions.DeleteByTenant
	}
	if w.memories != nil {
		memories = countless(w.memories.DeleteByTenant)
	}
	return []namedTenantEraser{
		{"transcripts", transcripts},
		{"interactions", interactions},
		{"memory", memories},
	}
}

// ledgerTenantErasers returns the Phase 4 entries from the delegation tree
// archive through the eval results: the archive, the audit chain, and the
// durable billing events.
func (w *gatewayWiring) ledgerTenantErasers() []namedTenantEraser {
	var archive, audit, billing, evals tenantEraseFunc
	// session_tree_archive.root_session_id references sessions(id) with
	// no ON DELETE action, so the archive must be empty before the
	// SessionStore step.
	if w.treeArchive != nil {
		archive = w.treeArchive.DeleteByTenant
	}
	if w.auditOpsStore != nil {
		audit = auditTenantEraser(w.auditOpsStore)
	}
	if w.billingPipeline != nil {
		billing = unlessBillingExempt(w.tenants, w.billingPipeline.DeleteByTenant)
	}
	if w.evals != nil {
		evals = w.evals.DeleteByTenant
	}
	return []namedTenantEraser{
		{"session_tree_archive", archive},
		{"audit", audit},
		{"billing", billing},
		{"eval_results", evals},
	}
}

// delegationBudgetTenantEraser returns the delegation budget entry: the
// tree-wide Redis keys of each of the tenant's root sessions, then the
// tenant's Postgres budget checkpoint rows. It is nil without the tree
// budget.
func (w *gatewayWiring) delegationBudgetTenantEraser() namedTenantEraser {
	if w.treeBudgetConcrete == nil {
		return namedTenantEraser{"delegation_budget", nil}
	}
	var checkpoints tenantDeleter
	if w.pgPool != nil {
		checkpoints = delegationbudgetpg.New(w.pgPool)
	}
	return namedTenantEraser{"delegation_budget", delegationBudgetEraser(w.sessions, w.treeBudgetConcrete, checkpoints)}
}

// tenantErasersAfterSessions returns the Phase 4 erasers that run after
// SessionStore, in §12.8 rank order: the tenant's tokens and credentials,
// its access grants and roles, its definitions, and last its configuration
// record.
func (w *gatewayWiring) tenantErasersAfterSessions(credentialPools credentialpoolstore.Store) []namedTenantEraser {
	var issued, pool tenantEraseFunc
	if w.pgPool != nil {
		issued = issuedtokenstore.New(w.pgPool).DeleteByTenant
	}
	if credentialPools != nil {
		pool = credentialPools.DeleteByTenant
	}
	return []namedTenantEraser{
		// TokenStore: connector refresh tokens, then registered user
		// credentials (§4.9 places them in TokenStore).
		{"tokens", sumTenantErasers(tenantDeleterFunc(w.connectorCreds), tenantDeleterFunc(w.credentials))},
		{"issued_tokens", issued},
		{"credential_pool", pool},
		{"runtime_tenant_access", tenantAccessEraser(w.tenantAccess, tenantaccessstore.KindRuntime)},
		{"pool_tenant_access", tenantAccessEraser(w.tenantAccess, tenantaccessstore.KindPool)},
		{"user_roles", tenantDeleterFunc(w.users)},
		{"custom_roles", tenantDeleterFunc(w.customRoles)},
		{"tenant_definitions", sumTenantErasers(
			tenantDeleterFunc(w.connectors), environmentTenantEraser(w.environments), tenantDeleterFunc(w.experiments),
		)},
		{"tenant_config", tenantConfigEraser(w.tenants)},
	}
}

// tenantDeleter is a store with a counting DeleteByTenant.
type tenantDeleter interface {
	DeleteByTenant(ctx context.Context, tenantID string) (int, error)
}

// tenantDeleterFunc returns d's DeleteByTenant, or nil when d is nil, so an
// absent store becomes a nil-function entry.
func tenantDeleterFunc(d tenantDeleter) tenantEraseFunc {
	if d == nil {
		return nil
	}
	return d.DeleteByTenant
}

// sumTenantErasers runs each non-nil eraser in order, stops at the first
// error, and returns the summed count. It returns nil when every eraser is
// nil, so a composite of absent stores is itself a nil-function entry.
func sumTenantErasers(fns ...tenantEraseFunc) tenantEraseFunc {
	var live []tenantEraseFunc
	for _, fn := range fns {
		if fn != nil {
			live = append(live, fn)
		}
	}
	if len(live) == 0 {
		return nil
	}
	return func(ctx context.Context, tenantID string) (int, error) {
		total := 0
		for _, fn := range live {
			n, err := fn(ctx, tenantID)
			total += n
			if err != nil {
				return total, err
			}
		}
		return total, nil
	}
}

// forEachTenantSession lists the tenant's sessions from the primary and calls
// fn for each, returning the summed count and the first error. The primary
// read keeps a session the creation fence admitted from being missed on a
// lagging replica.
//
// spec: §12.8 (Compliance Interfaces: Tenant deletion lifecycle, Lifecycle
// fence and drain).
func forEachTenantSession(
	ctx context.Context,
	sessions sessionstore.Store,
	tenantID string,
	fn func(ctx context.Context, s sessionstore.Session) (int, error),
) (int, error) {
	rows, err := sessions.List(ctx, tenantID, sessionstore.ListFilter{Primary: true})
	if err != nil {
		return 0, fmt.Errorf("list tenant %s sessions: %w", tenantID, err)
	}
	total := 0
	for _, s := range rows {
		n, err := fn(ctx, s)
		total += n
		if err != nil {
			return total, fmt.Errorf("session %s: %w", s.ID, err)
		}
	}
	return total, nil
}

// sessionKeyedTenantEraser adapts a per-session erase into a Phase 4 entry
// over forEachTenantSession.
func sessionKeyedTenantEraser(
	sessions sessionstore.Store,
	fn func(ctx context.Context, s sessionstore.Session) (int, error),
) tenantEraseFunc {
	return func(ctx context.Context, tenantID string) (int, error) {
		return forEachTenantSession(ctx, sessions, tenantID, fn)
	}
}

// rootPurger purges the tree-wide delegation budget keys of one root session.
// *treebudget.Reserver satisfies it.
type rootPurger interface {
	PurgeRoot(ctx context.Context, rootSessionID string) (int, error)
}

// delegationBudgetEraser purges the budget keys of each root session of the
// tenant, then deletes the tenant's budget checkpoint rows when checkpoints
// is set. A session is a root when its RootSessionID is empty or names
// itself; the keys of a delegation tree hang off its root only.
//
// spec: §12.8 (Compliance Interfaces: Tenant deletion lifecycle, delegation
// budget keys).
func delegationBudgetEraser(sessions sessionstore.Store, budgets rootPurger, checkpoints tenantDeleter) tenantEraseFunc {
	return func(ctx context.Context, tenantID string) (int, error) {
		n, err := forEachTenantSession(ctx, sessions, tenantID, func(ctx context.Context, s sessionstore.Session) (int, error) {
			if s.RootSessionID != "" && s.RootSessionID != s.ID {
				return 0, nil
			}
			return budgets.PurgeRoot(ctx, s.ID)
		})
		if err != nil || checkpoints == nil {
			return n, err
		}
		m, err := checkpoints.DeleteByTenant(ctx, tenantID)
		return n + m, err
	}
}

// auditTenantEraser deletes the tenant's non-gdpr.* audit rows. The platform
// tenant's rows are the platform audit chain rather than a tenant's data, so
// the entry erases nothing for that ID.
//
// spec: §12.8 (Compliance Interfaces: Tenant deletion lifecycle), §11.7.
func auditTenantEraser(audit tenantDeleter) tenantEraseFunc {
	return func(ctx context.Context, tenantID string) (int, error) {
		if tenantID == jwtaudit.PlatformTenantID {
			return 0, nil
		}
		return audit.DeleteByTenant(ctx, tenantID)
	}
}

// unlessBillingExempt wraps a billing eraser so it erases nothing for a
// tenant whose billingErasurePolicy is exempt: such a tenant keeps its
// billing events until billing.retentionDays ages them out. A tenant-row read
// error is returned without touching the store.
//
// spec: §12.8 (Compliance Interfaces: Tenant deletion lifecycle,
// tenant-controlled billing erasure).
func unlessBillingExempt(tenants tenantstore.Store, fn tenantEraseFunc) tenantEraseFunc {
	return func(ctx context.Context, tenantID string) (int, error) {
		t, err := tenants.Get(ctx, tenantID)
		if err != nil {
			return 0, fmt.Errorf("read tenant %s billing erasure policy: %w", tenantID, err)
		}
		if t.BillingErasurePolicy == tenantstore.BillingErasureExempt {
			return 0, nil
		}
		return fn(ctx, tenantID)
	}
}

// tenantAccessEraser revokes every grant of the given resource kind held by
// the tenant. A grant already gone counts as revoked, so a re-run is
// idempotent. It is nil when the store is absent.
func tenantAccessEraser(access tenantaccessstore.Store, kind tenantaccessstore.ResourceKind) tenantEraseFunc {
	if access == nil {
		return nil
	}
	return func(ctx context.Context, tenantID string) (int, error) {
		resources, err := access.ListForTenant(ctx, kind, tenantID)
		if err != nil {
			return 0, fmt.Errorf("list %s grants: %w", kind, err)
		}
		n := 0
		for _, r := range resources {
			err := access.Revoke(ctx, kind, r, tenantID)
			if errors.Is(err, tenantaccessstore.ErrNotFound) {
				continue
			}
			if err != nil {
				return n, fmt.Errorf("revoke %s grant %s: %w", kind, r, err)
			}
			n++
		}
		return n, nil
	}
}

// environmentTenantEraser deletes each of the tenant's environments. An
// environment already gone counts as deleted, so a re-run is idempotent. It
// is nil when the store is absent.
func environmentTenantEraser(envs environmentstore.Store) tenantEraseFunc {
	if envs == nil {
		return nil
	}
	return func(ctx context.Context, tenantID string) (int, error) {
		rows, err := envs.List(ctx, tenantID)
		if err != nil {
			return 0, fmt.Errorf("list environments: %w", err)
		}
		n := 0
		for _, e := range rows {
			err := envs.Delete(ctx, tenantID, e.Name)
			if errors.Is(err, environmentstore.ErrNotFound) {
				continue
			}
			if err != nil {
				return n, fmt.Errorf("delete environment %s: %w", e.Name, err)
			}
			n++
		}
		return n, nil
	}
}

// tenantConfigEraser nulls the tenant configuration record through
// tenants.Update, keeping only the fields keptTenantConfig names. Nulling
// ErasureSalt deletes the sealed salt ciphertext. A missing row has nothing
// to null.
//
// spec: §12.8 (Compliance Interfaces: Tenant deletion lifecycle, Tenant
// record tombstone).
func tenantConfigEraser(tenants tenantstore.Store) tenantEraseFunc {
	return func(ctx context.Context, tenantID string) (int, error) {
		_, err := tenants.Update(ctx, tenantID, func(t *tenantstore.Tenant) error {
			*t = keptTenantConfig(*t)
			return nil
		})
		if errors.Is(err, tenantstore.ErrNotFound) {
			return 0, nil
		}
		if err != nil {
			return 0, fmt.Errorf("null tenant %s configuration: %w", tenantID, err)
		}
		return 1, nil
	}
}

// keptTenantConfig returns the tombstone of t: every field zero except those
// a resumed or later phase, or a reader of the tombstone, still reads. The
// lifecycle state and timestamps drive the reconciler and the 410 response;
// WorkspaceTier and DataResidencyRegion are read by the later phases;
// BillingErasurePolicy must survive because a Phase 4 re-run reads it in the
// billing entries, and an empty policy reads as pseudonymize; the
// force-delete override fields record who overrode a hold.
//
// spec: §12.8 (Compliance Interfaces: Tenant deletion lifecycle, Tenant
// record tombstone).
func keptTenantConfig(t tenantstore.Tenant) tenantstore.Tenant {
	return tenantstore.Tenant{
		ID:                       t.ID,
		State:                    t.State,
		DeletedAt:                t.DeletedAt,
		WorkspaceTier:            t.WorkspaceTier,
		DataResidencyRegion:      t.DataResidencyRegion,
		BillingErasurePolicy:     t.BillingErasurePolicy,
		ForceDeleteHoldOverride:  t.ForceDeleteHoldOverride,
		ForceDeleteJustification: t.ForceDeleteJustification,
		ForceDeleteBy:            t.ForceDeleteBy,
		ForceDeleteAt:            t.ForceDeleteAt,
		CreatedAt:                t.CreatedAt,
		UpdatedAt:                t.UpdatedAt,
		Version:                  t.Version,
	}
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
