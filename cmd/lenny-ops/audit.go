// SPDX-License-Identifier: MIT

package main

import (
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/ops/opsaudit"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// buildStoreRouter constructs the §12.6 StoreRouter lenny-ops accesses
// platform Postgres and Redis through. v1 is the single-shard router:
// every accessor returns the sole pool / Redis client, but routing the
// §12.3 R-03 audit-write path through PlatformPostgres() / AuditShard()
// (rather than a raw *pgxpool.Pool) keeps the discipline that a future
// multi-shard split rotates only the router. Returns nil when no Postgres
// is configured (single-process dev), in which case the durable audit
// path degrades to log-only.
//
// spec: §25.4 lines 1490-1500 (lenny-ops uses StoreRouter:
// PlatformPostgres(), PlatformRedis(), AuditShard()). F-25.4.14.
func buildStoreRouter(pgPool *pgxpool.Pool, redisClient redis.UniversalClient) *storerouter.SingleShardRouter {
	if pgPool == nil {
		return nil
	}
	router, err := storerouter.NewSingleShardRouter(storerouter.Config{
		Postgres: pgPool,
		Redis:    redisClient,
	})
	if err != nil {
		log.Fatalf("lenny-ops: §12.6 store router: %v", err)
	}
	return router
}

// buildPlatformAuditRecorder builds the durable platform-audit recorder
// every lenny-ops audit sink funnels through. When a StoreRouter is wired
// it commits each ops_event.* audit event to the §11.7 audit_log hash
// chain under the platform tenant, routed through the §12.3 R-03
// AuditShard(); otherwise it degrades to log-only so single-process dev
// stays observable.
//
// spec: §11.7 line 435 (ops_event.* route to the platform tenant via
// PlatformPostgres()); §25.4 (lock / escalation / self-health audit
// events). F-25.4.22.
func buildPlatformAuditRecorder(router *storerouter.SingleShardRouter) *opsaudit.Recorder {
	if router == nil {
		log.Printf("lenny-ops: §11.7 platform audit: no durable store wired (no --postgres-dsn); audit events logged only")
		return opsaudit.New(nil)
	}
	log.Printf("lenny-ops: §11.7 platform audit: durable store wired (audit_log via StoreRouter.AuditShard)")
	return opsaudit.New(auditstore.New(router))
}
