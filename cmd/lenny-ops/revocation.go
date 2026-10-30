// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"slices"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/revocation"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/revocation/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	tenantpg "github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore/pgstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pubsub"
	"github.com/lennylabs/lenny/pkg/gateway/storage/redistopology"
	"github.com/lennylabs/lenny/pkg/redisconn"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// revocationReadInterval is the period of the rule-1 Postgres read. The
// spec fixes it at 30 seconds, and each read is also bounded by it so a
// hung query cannot hold the loop past the next scheduled read.
//
// spec: §25.4 (The lenny-ops Service: Authentication, revocation set rule 1).
const revocationReadInterval = 30 * time.Second

// opsRevocation is the lenny-ops revocation set for Lenny-issued tokens.
// It wraps the gateway's revocation.Cache, which holds the revoked jtis,
// and adds the first-read gate.
//
// The gate is keyed on whether a read has succeeded since the process
// started, never on Cache.Stale: Cache.Stale turns true again after 90 s
// of failed reads, which would refuse every Lenny-issued token during a
// Postgres outage. §25.4 rule 5 instead keeps the set in force and accepts
// a token not in it.
//
// spec: §25.4 (The lenny-ops Service: Authentication, revocation set).
type opsRevocation struct {
	cache *revocation.Cache
	// loaded is set by the first successful rule-1 read and never cleared.
	loaded atomic.Bool
}

// newOpsRevocation returns an empty revocation set whose gate is closed.
func newOpsRevocation(opts ...revocation.Option) *opsRevocation {
	return &opsRevocation{cache: revocation.NewCache(opts...)}
}

// Stale reports whether no rule-1 read has succeeded yet. It implements
// authmw.RevocationFreshness, so authmw refuses every in-scope token with
// the response it writes for a stale revocation set (§25.4 rule 4), before
// the revocation and playground checks (rules 3 and 7).
func (r *opsRevocation) Stale() bool { return !r.loaded.Load() }

// IsRevoked reports whether jti is in the set. It implements
// authmw.RevocationChecker (§25.4 rule 3).
func (r *opsRevocation) IsRevoked(jti string) bool { return r.cache.IsRevoked(jti) }

// apply wires the set into opts: rule 3 through Revocations, rule 4
// through RevocationFreshness, and rule 7 through PlaygroundRevocations.
func (r *opsRevocation) apply(opts *authmw.Options) {
	opts.Revocations = r
	opts.RevocationFreshness = r
	opts.PlaygroundRevocations = playgroundRefusal{}
}

// read runs one rule-1 read. A successful read replaces the set with the
// rows it returned and the jtis rule 2 added after it began, and opens
// the gate. A failed read leaves the set and the gate unchanged.
func (r *opsRevocation) read(ctx context.Context, tenants revocation.TenantLister, src revocation.Source) error {
	readCtx, cancel := context.WithTimeout(ctx, revocationReadInterval)
	defer cancel()
	if err := r.cache.Rehydrate(readCtx, tenants, src); err != nil {
		return fmt.Errorf("revocation set read: %w", err)
	}
	r.loaded.Store(true)
	return nil
}

// runReads runs the rule-1 read at once and again on each value from
// ticks, until ctx is cancelled. Each outcome goes to observe; a failed
// read, including the first, never stops the loop.
func (r *opsRevocation) runReads(ctx context.Context, tenants revocation.TenantLister, src revocation.Source, ticks <-chan time.Time, observe func(error)) {
	observe(r.read(ctx, tenants, src))
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			observe(r.read(ctx, tenants, src))
		}
	}
}

// runEvents adds the jti of each token.revoked event on bus to the set
// until ctx is cancelled (§25.4 rule 2). The events go through
// Cache.Revoke, so a read in flight keeps them at its swap.
func (r *opsRevocation) runEvents(ctx context.Context, bus *pubsub.Bus) {
	propagator.New(r.cache, bus).Run(ctx)
}

// logRevocationRead logs a failed rule-1 read through slog at error
// level, so a query filtering on the record's level sees it. The set stays
// as it was, and before the first success every Lenny-issued token is
// refused, so the record is how an operator sees a set left behind. The
// read loop keeps running after it.
//
// spec: §25.4 (The lenny-ops Service: Authentication, revocation set rule 1).
func logRevocationRead(err error) {
	if err != nil {
		slog.Error("lenny-ops: §25.4 revocation set read failed; the revocation set keeps its previous contents", "err", err)
	}
}

// playgroundRefusal refuses every playground bearer (§25.4 rule 7). A
// gateway replica checks a playground bearer's revocation in the §27.3.1
// playground revocation key, which lenny-ops does not read, so it refuses
// the bearer with the response authmw gives a revoked one rather than
// reading Redis on the request path.
type playgroundRefusal struct{}

// IsBearerRevoked reports every playground bearer revoked.
func (playgroundRefusal) IsBearerRevoked(context.Context, string, string) (bool, error) {
	return true, nil
}

// revocationTenants lists every tenant whose revoked rows a starting
// gateway replica loads: tombstoned tenants included, because a deleted
// tenant's kept rows are still in force, and the built-in default tenant
// whether or not it has a row.
//
// spec: §13.3 (Credential Flow: Authoritative durability for revocation).
type revocationTenants struct {
	store tenantRows
}

// tenantRows is the slice of tenantstore.Store the lister reads.
type tenantRows interface {
	List(ctx context.Context, filter tenantstore.ListFilter) ([]tenantstore.Tenant, error)
}

// ListTenants returns the IDs of every tenant row and the default tenant.
func (t revocationTenants) ListTenants(ctx context.Context) ([]string, error) {
	rows, err := t.store.List(ctx, tenantstore.ListFilter{IncludeDeleted: true})
	if err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}
	ids := make([]string, 0, len(rows)+1)
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	if !slices.Contains(ids, auth.DefaultTenantID) {
		ids = append(ids, auth.DefaultTenantID)
	}
	return ids, nil
}

// startRevocationSet starts the rule-1 read loop when Postgres is wired
// and the rule-2 subscription when Redis is wired. Without Postgres no
// read runs, so the gate stays closed and every Lenny-issued token gets
// the rule-4 refusal.
//
// spec: §25.4 (The lenny-ops Service: Authentication, revocation set);
// §12.4 (the Cache/Pub-Sub concern carries token.revoked).
func (w *opsWiring) startRevocationSet(rev *opsRevocation) {
	if w.pgPool != nil {
		tenants := revocationTenants{store: tenantpg.New(w.pgPool)}
		src := issuedtokenstore.New(w.pgPool)
		ticker := time.NewTicker(revocationReadInterval)
		go func() {
			defer ticker.Stop()
			rev.runReads(w.ctx, tenants, src, ticker.C, logRevocationRead)
		}()
	} else {
		log.Printf("lenny-ops: §25.4 no --postgres-dsn; the revocation set is never read, so every Lenny-issued token is refused")
	}
	if w.redisClient == nil {
		return
	}
	clients, err := redistopology.Build(w.redisClient,
		map[storerouter.RedisConcern]string{storerouter.RedisConcernCachePubSub: *w.f.redisCachePubSubURL},
		redisconn.Config{Password: *w.f.redisPassword, AllowInsecure: *w.f.redisAllowInsecure})
	if err != nil {
		log.Fatalf("lenny-ops: §12.4 cache/pub-sub redis: %v", err)
	}
	bus := pubsub.New(clients.For(storerouter.RedisConcernCachePubSub))
	go func() {
		defer func() { _ = clients.Close() }()
		rev.runEvents(w.ctx, bus)
	}()
}
