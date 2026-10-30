// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
	"time"

	billingpg "github.com/lennylabs/lenny/pkg/gateway/billing/billingstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/quota/storagequota"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pubsub"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// buildStoreRouterAndSecurityBus constructs the §12.3 R-03 single-shard store
// router (wrapping the billing ledger), runs the §11.2 storage-quota
// rehydration from the §12.5 artifact catalog, and constructs the §4.9/§10.3
// security-cache Redis pub/sub bus, recording each on the accumulator. The
// router and the bus are built only on the Postgres / Redis paths
// respectively; an in-memory deployment leaves them nil.
//
// spec: §4.1 gateway subsystem seams; §12.3 store router; §11.2 quota
// rehydration; §4.9/§10.3 security pub/sub.
func (w *gatewayWiring) buildStoreRouterAndSecurityBus() {
	f := w.f
	scatterAggregateTimeoutSeconds := f.scatterAggregateTimeoutSeconds
	scatterMaxConcurrency := f.scatterMaxConcurrency
	scatterPerShardTimeoutSeconds := f.scatterPerShardTimeoutSeconds

	// The persistence, Redis, and quota surfaces were recorded on the
	// accumulator by the earlier build steps.
	pgPool := w.pgPool
	readPool := w.readPool
	billingAuditPool := w.billingAuditPool
	redisClient := w.redisClient
	concernRedis := w.concernRedis
	tenants := w.tenants
	blobsCataloged := w.blobsCataloged
	storageCounter := w.storageCounter
	billing := w.billing

	// §12.3 R-03: billing and audit writes route through the
	// StoreRouter so a future Tier-3 shard split is a router swap with no
	// billing/audit call-site changes. v1 wires the single-shard router;
	// it runs in Postgres-only mode when Redis is unconfigured because
	// the billing/audit paths route only Postgres shards. The router is
	// built only with a real Postgres pool; in-memory deployments keep
	// the billingstore.NewMemory ledger set above and leave storeRouter
	// nil (the audit chain below is likewise Postgres-only). The redis
	// client is passed through the UniversalClient interface only when it
	// is a real *redis.Client — a typed-nil would defeat the nil check in
	// NewSingleShardRouter, matching the securityBus guard below.
	// F-12.3.4 / F-12.6.1 / F-12.2.13 / F-12.6.2 / F-12.7.1.
	// §12.6 scatter-gather execution bounds, resolved from
	// the storeRouter.* Helm values. The single-shard v1 router satisfies
	// them trivially; the bounds and the §12.6 metrics are wired
	// now so a later multi-shard split needs no retrofit. F-12.6.18.
	scatterCfg := storerouter.ScatterConfig{
		MaxConcurrency:   *scatterMaxConcurrency,
		PerShardTimeout:  time.Duration(*scatterPerShardTimeoutSeconds) * time.Second,
		AggregateTimeout: time.Duration(*scatterAggregateTimeoutSeconds) * time.Second,
	}
	var scatterRouter *storerouter.SingleShardRouter
	var storeRouter storerouter.StoreRouter
	if pgPool != nil {
		// §12.4: the router resolves each RedisConcern to
		// its own client when an operator has split concerns onto separate
		// instances; concernRedis.ByConcern() is nil for the single Tier
		// 1/2 topology, so RedisShard falls back to the base client.
		// PlatformRedis (pod slot counters, circuit breakers) rides on the
		// Coordination instance per the §12.4 table.
		r, err := storerouter.NewSingleShardRouter(storerouter.Config{
			Postgres:             pgPool,
			ReadPostgres:         readPool,
			BillingAuditPostgres: billingAuditPool,
			Redis:                redisClient,
			RedisByConcern:       concernRedis.ByConcern(),
			PlatformRedisClient:  concernRedis.For(storerouter.RedisConcernCoordination),
			Scatter:              scatterCfg,
		})
		if err != nil {
			log.Fatalf("lenny-gateway: store router: %v", err)
		}
		storeRouter = r
		scatterRouter = r
		billing = billingpg.New(r)
	}

	// §11 storage-quota release. The cataloging decorator
	// decrements the per-tenant storage counter by a deleted artifact's
	// size after its catalog row commits with `deleted_at` set, so a
	// terminal session's GC-collected artifacts free their reserved
	// bytes and a tenant near its cap can upload again. The decorator is
	// built before storageCounter is resolved (the Redis-backed counter
	// is wired above), so the releaser is installed here through the
	// setter. F-11.2.9.
	if blobsCataloged != nil {
		blobsCataloged.SetQuotaReleaser(storageCounter)
		// §11.2 startup rehydration: reconstruct each tenant's storage
		// counter from the authoritative Postgres byte total (live artifact
		// bytes plus outstanding checkpoint reservations, through the
		// reservation-aware seam composed in buildRedisAndQuota) so the
		// rebuilt counter already holds every unreleased reservation the
		// guarded relative Adjust will later release (same recovery path as
		// the token quota counters). Runs whenever the Postgres artifact
		// catalog is wired, independent of Redis: a Postgres-catalog
		// deployment without Redis rebuilds its in-memory counter here so it
		// does not start at zero and under-count after a restart. A per-tenant
		// fault is logged and skipped so one tenant cannot block startup. Runs
		// before the HTTP listener accepts traffic, so no reservation races the
		// absolute Set.
		if w.storageLiveBytes != nil {
			rehydrateCtx, cancelRehydrate := context.WithTimeout(context.Background(), 30*time.Second)
			if ids, lerr := (tenantsLister{tenants}).ListTenants(rehydrateCtx); lerr != nil {
				log.Printf("lenny-gateway: §11 storage-quota rehydration: list tenants: %v", lerr)
			} else if rerr := storagequota.Rehydrate(rehydrateCtx, storageCounter, ids, w.storageLiveBytes); rerr != nil {
				log.Printf("lenny-gateway: §11 storage-quota rehydration: %v", rerr)
			} else if len(ids) > 0 {
				log.Printf("lenny-gateway: §11 storage-quota counters rehydrated from artifact_store for %d tenants", len(ids))
			}
			cancelRehydrate()
		}
	}

	// §4.9 / §10.3 / §13.3 security-cache pub/sub substrate. The
	// gateway's revocation cache and the two deny lists are per-replica
	// in-memory sets; the Bus fans a local mutation out to peer replicas
	// over Redis pub/sub so a revocation takes effect fleet-wide. With
	// no Redis the Bus stays nil, which the propagators treat as the
	// single-replica mode: every cache stays local and nothing is
	// published. redisClient is a redis.UniversalClient set only on the
	// Redis-configured path, so its nil is a genuine interface nil the
	// guard below detects; the per-concern client resolves through the
	// same nil base when no split is configured.
	var securityBus *pubsub.Bus
	if redisClient != nil {
		// §12.4 Cache/Pub-Sub concern: revocation fan-out is event pub/sub.
		securityBus = pubsub.New(concernRedis.For(storerouter.RedisConcernCachePubSub))
		log.Printf("lenny-gateway: security caches converge across replicas over Redis pub/sub")
	}

	// spec: §4.1 — record the §12.3 router and the §4.9 security bus on the
	// accumulator. billing is now the router-wrapped ledger the billing
	// pipeline step reads.
	w.storeRouter = storeRouter
	w.scatterRouter = scatterRouter
	w.billing = billing
	w.securityBus = securityBus
}
