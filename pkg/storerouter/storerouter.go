// SPDX-License-Identifier: MIT

// Package storerouter is the §12.6 StoreRouter interface and its v1
// single-shard implementation. The router abstracts Postgres shard
// routing and per-concern Redis instance selection so call sites
// never hard-code a specific pool: a future multi-shard topology
// rotates only the StoreRouter implementation, not every billing,
// audit, or session-scoped read path.
//
// In v1 (`SingleShardRouter`) every accessor returns the same
// Postgres pool and the same Redis client. The §12.3 R-03 rule still
// applies — billing and audit writes MUST go through the router so a
// later split can land without sweeping the codebase.
//
// The session-routing prefix lives in the first 32 bits of the §12.6
// UUIDv8 session ID. SingleShardRouter ignores the prefix; a future
// router consistent-hashes it across the shard fleet. All sessions
// in a delegation tree share the same prefix and therefore co-locate
// on the same shard.
package storerouter

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/rediskeys"
)

// TenantID is the platform-issued tenant identifier.
type TenantID string

// SessionID is the §12.6 UUIDv8 session identifier.
type SessionID string

// ShardID names one Postgres shard returned by AllSessionShards or
// AllAuditShards. v1 routers expose a single shard identified as
// "default".
type ShardID string

// RedisConcern selects which Redis instance StoreRouter returns. In
// v1 every concern maps to the same Redis instance; the names are
// load-bearing only when an operator splits concerns onto separate
// instances at tier 3+.
type RedisConcern string

const (
	RedisConcernCoordination RedisConcern = "coordination" // session leases, generation counters
	RedisConcernQuota        RedisConcern = "quota"        // token/rate counters, sliding windows, billing stream
	RedisConcernCachePubSub  RedisConcern = "cache_pubsub" // routing cache, EventBus channels, semantic cache
	RedisConcernDelegation   RedisConcern = "delegation"   // budget keys ({root_session_id}:dlg:*)
	RedisConcernSessionData  RedisConcern = "session_data" // DLQ, durable inbox
)

// StoreType identifies a Postgres store category for ShardCount.
type StoreType string

const (
	StoreTypeSession StoreType = "session" // sessions, session_messages, session_tree_archive
	StoreTypeTenant  StoreType = "tenant"  // tenants, environments, runtime_definitions, credential_pools, delegation_policies
	StoreTypeBilling StoreType = "billing" // billing_events (R-03)
	StoreTypeAudit   StoreType = "audit"   // audit_log (R-03)
)

// ShardHandle is one Postgres shard plus its connection pool. The
// scatter-gather scan methods (AllSessionShards, AllAuditShards)
// return one per shard so callers can iterate without holding a
// router-level lock.
type ShardHandle struct {
	ID   ShardID
	Pool *pgxpool.Pool
}

// StoreRouter is the §12.6 interface that abstracts Postgres shard
// routing and per-concern Redis instance selection.
type StoreRouter interface {
	// TenantShard returns the pool for tenant-scoped metadata
	// tables. Always routes by tenant_id.
	TenantShard(ctx context.Context, tenantID TenantID) (*pgxpool.Pool, error)

	// SessionShard returns the pool for session-scoped tables. The
	// session routing prefix is extracted from the session ID; in
	// v1 the prefix is ignored and the sole pool is returned.
	SessionShard(ctx context.Context, sessionID SessionID) (*pgxpool.Pool, error)

	// BillingShard returns the pool for billing event writes (R-03).
	BillingShard(ctx context.Context, tenantID TenantID) (*pgxpool.Pool, error)

	// AuditShard returns the pool for audit log writes (R-03).
	AuditShard(ctx context.Context, tenantID TenantID) (*pgxpool.Pool, error)

	// RedisShard returns the Redis client for a given concern.
	RedisShard(ctx context.Context, tenantID TenantID, concern RedisConcern) (redis.UniversalClient, error)

	// PlatformRedis returns the Redis client for platform-scoped
	// keys (pod slot counters, circuit breakers). Not tenant-routed.
	PlatformRedis(ctx context.Context) (redis.UniversalClient, error)

	// AllSessionShards returns every session shard for scatter-gather
	// reads / GDPR erasure / tenant deletion.
	AllSessionShards(ctx context.Context) ([]ShardHandle, error)

	// PlatformPostgres returns the pool for platform-global tables
	// not owned by a tenant or session (ops-scoped tables).
	PlatformPostgres(ctx context.Context) (*pgxpool.Pool, error)

	// AllAuditShards returns every audit shard for scatter-gather
	// cross-tenant audit queries (§25.9).
	AllAuditShards(ctx context.Context) ([]ShardHandle, error)

	// ShardCount returns the number of shards for a given store type.
	ShardCount(storeType StoreType) int
}

// Errors returned by the router.
var (
	// ErrInvalidSessionID reports that a session id is empty or does
	// not parse as a routable identifier.
	ErrInvalidSessionID = errors.New("storerouter: session id is required")
	// ErrInvalidTenantID reports that a tenant id is empty.
	ErrInvalidTenantID = errors.New("storerouter: tenant id is required")
	// ErrUnknownRedisConcern reports that the supplied concern is
	// not one of the documented RedisConcern* constants.
	ErrUnknownRedisConcern = errors.New("storerouter: unknown redis concern")
	// ErrUnknownStoreType reports that the supplied store type is
	// not one of the documented StoreType* constants.
	ErrUnknownStoreType = errors.New("storerouter: unknown store type")
	// ErrRedisUnavailable reports that a Redis accessor was called on
	// a router constructed in Postgres-only mode (Config.Redis nil).
	// The §12.3 R-03 billing/audit write paths route only Postgres
	// shards, so a Postgres-only deployment can still satisfy R-03
	// without a Redis instance.
	ErrRedisUnavailable = errors.New("storerouter: router has no redis client (postgres-only mode)")
)

// SingleShardRouter is the v1 implementation: every Postgres
// accessor returns the same pool, every RedisConcern resolves to the
// same Redis client, and AllSessionShards / AllAuditShards return one
// ShardHandle each.
//
// The router enforces the §12.3 R-03 discipline at the type level: a
// caller that holds the SingleShardRouter has no way to pull the raw
// pool directly — the BillingShard and AuditShard methods are the
// only billing/audit accessors.
type SingleShardRouter struct {
	pg             *pgxpool.Pool
	billingPG      *pgxpool.Pool
	rdb            redis.UniversalClient
	redisByConcern map[RedisConcern]redis.UniversalClient
	platformRedis  redis.UniversalClient
	defaultID      ShardID
	platformID     ShardID
	scatterCfg     ScatterConfig
	scatterMetrics ScatterMetrics
}

// Config configures NewSingleShardRouter.
type Config struct {
	// Postgres is the single pool every accessor returns. Required.
	Postgres *pgxpool.Pool
	// BillingAuditPostgres is the optional dedicated pool for the
	// billing-event and audit-log write paths. When set (the §12.3
	// Tier-3 LENNY_PG_BILLING_AUDIT_DSN separate-instance posture),
	// BillingShard, AuditShard, and AllAuditShards resolve to this pool
	// while every other accessor stays on Postgres. When nil the
	// billing/audit paths share the Postgres pool, so a single
	// well-provisioned primary serves Tier-3 load unchanged.
	//
	// spec: §12.3 line 103 — "The gateway supports separate connection
	// strings for the billing/audit write path (LENNY_PG_BILLING_AUDIT_DSN).
	// When configured, billing and audit inserts are routed to this
	// instance while all other writes continue to the primary."
	BillingAuditPostgres *pgxpool.Pool
	// Redis is the single Redis client every concern resolves to.
	// Optional: when nil the router runs in Postgres-only mode and the
	// Redis accessors (RedisShard, PlatformRedis) return
	// ErrRedisUnavailable. The §12.3 R-03 billing/audit write paths
	// route only Postgres shards, so a Postgres-only deployment (and
	// the billing/audit store component tests) can wire the router
	// without a Redis instance.
	//
	// When RedisByConcern (below) is set, Redis is the fallback for any
	// concern the map omits.
	Redis redis.UniversalClient

	// RedisByConcern optionally maps a RedisConcern to its own Redis
	// instance, implementing the §12.4 "Logical separation of Redis
	// concerns" deployment-time split: an operator supplies a separate
	// connection string per store role (Coordination, Quota/Rate
	// Limiting, Cache/Pub-Sub, ...) and the router hands each concern its
	// dedicated client. A concern absent from the map falls back to
	// Redis. When the map is nil every concern resolves to Redis (the
	// single-instance Tier 1/2 topology). NewSingleShardRouter installs
	// the §12.4 line 195 tenant-key Guard hook on every distinct client.
	//
	// spec: §12.4 lines 237-245 — "separate connection strings per store
	// role — no code changes are required because each store role already
	// has its own interface".
	RedisByConcern map[RedisConcern]redis.UniversalClient

	// PlatformRedisClient optionally overrides the Redis instance
	// PlatformRedis returns for platform-scoped keys (pod slot counters,
	// circuit breakers). When nil PlatformRedis returns Redis. The §12.4
	// table places coordination-class platform keys on the Coordination
	// instance, so an operator that splits concerns typically points this
	// at the same client as RedisByConcern[RedisConcernCoordination].
	PlatformRedisClient redis.UniversalClient
	// DefaultShardID is the ShardID assigned to the single shard
	// returned by AllSessionShards and AllAuditShards. A zero value
	// defaults to "default".
	DefaultShardID ShardID
	// Scatter pins the §12.6 lines 556-558 scatter-gather execution
	// bounds the ScatterRead / ScatterWrite helpers observe. A zero
	// value resolves to DefaultScatterConfig. v1 is single-shard so the
	// bounds are trivially satisfied; they become load-bearing the first
	// time a multi-shard router is deployed.
	Scatter ScatterConfig
	// ScatterMetrics receives the §12.6 line 560 scatter-gather metrics.
	// nil disables emission. The gateway may also attach it after
	// construction via SetScatterMetrics (the production registerer is
	// built after the router).
	ScatterMetrics ScatterMetrics
}

// NewSingleShardRouter constructs a SingleShardRouter against the
// supplied pool and Redis client.
func NewSingleShardRouter(cfg Config) (*SingleShardRouter, error) {
	if cfg.Postgres == nil {
		return nil, fmt.Errorf("storerouter: Postgres pool is required")
	}
	id := cfg.DefaultShardID
	if id == "" {
		id = "default"
	}
	// spec §12.4 line 195: tenant-key isolation is enforced in the Redis
	// wrapper layer. Installing the Guard hook on each distinct client
	// makes every concern returned by RedisShard validate keys against the
	// per-request scope (rediskeys.WithScope) before the command is issued.
	// When concerns are split across separate instances each instance gets
	// its own Guard. A client shared across concerns (or shared with the
	// Redis fallback / PlatformRedisClient) is guarded once — AddHook
	// appends to the client's hook chain, so a double install would run the
	// validation twice. In Postgres-only mode (Redis nil and no per-concern
	// clients) there is nothing to guard.
	guarded := map[redis.UniversalClient]bool{}
	guard := func(c redis.UniversalClient) {
		if c == nil || guarded[c] {
			return
		}
		guarded[c] = true
		c.AddHook(rediskeys.NewGuard())
	}
	guard(cfg.Redis)
	for _, c := range cfg.RedisByConcern {
		guard(c)
	}
	guard(cfg.PlatformRedisClient)
	return &SingleShardRouter{
		pg:             cfg.Postgres,
		billingPG:      cfg.BillingAuditPostgres,
		rdb:            cfg.Redis,
		redisByConcern: cfg.RedisByConcern,
		platformRedis:  cfg.PlatformRedisClient,
		defaultID:      id,
		platformID:     id,
		scatterCfg:     cfg.Scatter.withDefaults(),
		scatterMetrics: cfg.ScatterMetrics,
	}, nil
}

// ScatterConfig returns the §12.6 lines 556-558 scatter-gather bounds the
// router was configured with. A scatter-gather caller passes it to
// ScatterRead / ScatterWrite. The zero Config value resolves to
// DefaultScatterConfig.
func (r *SingleShardRouter) ScatterConfig() ScatterConfig { return r.scatterCfg }

// ScatterMetrics returns the §12.6 line 560 scatter-gather metrics sink
// the router was configured with, or nil when none is wired.
func (r *SingleShardRouter) ScatterMetrics() ScatterMetrics { return r.scatterMetrics }

// SetScatterMetrics attaches the scatter-gather metrics sink after
// construction. The production registerer is built after the router, so
// the gateway wires the collector here once it exists.
func (r *SingleShardRouter) SetScatterMetrics(m ScatterMetrics) { r.scatterMetrics = m }

// billingAuditPool returns the dedicated billing/audit pool when the
// §12.3 LENNY_PG_BILLING_AUDIT_DSN separate instance is configured,
// otherwise the primary pool. Routing both the billing and audit write
// paths through this single resolver keeps the §12.3 R-03 discipline:
// the two append-only write sources move to the separate instance
// together, matching the §12.3 line 130 instance-separation step.
func (r *SingleShardRouter) billingAuditPool() *pgxpool.Pool {
	if r.billingPG != nil {
		return r.billingPG
	}
	return r.pg
}

var _ StoreRouter = (*SingleShardRouter)(nil)

// TenantShard returns the sole Postgres pool. An empty tenant id is
// rejected; callers MUST resolve a tenant before reaching the
// router.
func (r *SingleShardRouter) TenantShard(_ context.Context, tenantID TenantID) (*pgxpool.Pool, error) {
	if tenantID == "" {
		return nil, ErrInvalidTenantID
	}
	return r.pg, nil
}

// SessionShard returns the sole Postgres pool. v1 ignores the
// routing prefix; the SessionID is validated for non-emptiness so
// the contract surface is consistent with a future multi-shard
// implementation that extracts the prefix.
func (r *SingleShardRouter) SessionShard(_ context.Context, sessionID SessionID) (*pgxpool.Pool, error) {
	if sessionID == "" {
		return nil, ErrInvalidSessionID
	}
	return r.pg, nil
}

// BillingShard returns the billing/audit pool, routed by tenant_id.
// When LENNY_PG_BILLING_AUDIT_DSN is configured this is the separate
// §12.3 instance; otherwise it is the primary pool.
func (r *SingleShardRouter) BillingShard(_ context.Context, tenantID TenantID) (*pgxpool.Pool, error) {
	if tenantID == "" {
		return nil, ErrInvalidTenantID
	}
	return r.billingAuditPool(), nil
}

// AuditShard returns the billing/audit pool, routed by tenant_id. When
// LENNY_PG_BILLING_AUDIT_DSN is configured this is the separate §12.3
// instance; otherwise it is the primary pool.
func (r *SingleShardRouter) AuditShard(_ context.Context, tenantID TenantID) (*pgxpool.Pool, error) {
	if tenantID == "" {
		return nil, ErrInvalidTenantID
	}
	return r.billingAuditPool(), nil
}

// RedisShard returns the Redis client for a concern. When the router
// was built with a per-concern split (RedisByConcern) the concern's
// dedicated client is returned; otherwise every concern resolves to the
// single Redis client. The concern is validated against the documented
// set so an unknown concern at the call site fails fast.
func (r *SingleShardRouter) RedisShard(_ context.Context, tenantID TenantID, concern RedisConcern) (redis.UniversalClient, error) {
	if tenantID == "" {
		return nil, ErrInvalidTenantID
	}
	if !validConcern(concern) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownRedisConcern, concern)
	}
	if c, ok := r.redisByConcern[concern]; ok && c != nil {
		return c, nil
	}
	if r.rdb == nil {
		return nil, ErrRedisUnavailable
	}
	return r.rdb, nil
}

// PlatformRedis returns the platform-scoped Redis client
// (PlatformRedisClient when set, else the single Redis client), or
// ErrRedisUnavailable when the router runs in Postgres-only mode.
func (r *SingleShardRouter) PlatformRedis(_ context.Context) (redis.UniversalClient, error) {
	if r.platformRedis != nil {
		return r.platformRedis, nil
	}
	if r.rdb == nil {
		return nil, ErrRedisUnavailable
	}
	return r.rdb, nil
}

// AllSessionShards returns one ShardHandle wrapping the sole
// Postgres pool.
func (r *SingleShardRouter) AllSessionShards(_ context.Context) ([]ShardHandle, error) {
	return []ShardHandle{{ID: r.defaultID, Pool: r.pg}}, nil
}

// PlatformPostgres returns the sole Postgres pool. v1 routes
// platform-global tables to the same database; a multi-shard
// deployment can route them to a dedicated instance.
func (r *SingleShardRouter) PlatformPostgres(_ context.Context) (*pgxpool.Pool, error) {
	return r.pg, nil
}

// AllAuditShards returns one ShardHandle wrapping the audit pool. When
// LENNY_PG_BILLING_AUDIT_DSN is configured the §25.9 scatter-gather
// audit scans (OCSF translation, EventBus retranscribe) iterate the
// separate §12.3 instance, so a cross-tenant audit drain reaches the
// rows the write path landed there.
func (r *SingleShardRouter) AllAuditShards(_ context.Context) ([]ShardHandle, error) {
	return []ShardHandle{{ID: r.defaultID, Pool: r.billingAuditPool()}}, nil
}

// ShardCount returns 1 for every documented StoreType. An unknown
// store type returns 0 — callers MUST handle the zero case to avoid
// dividing by it.
func (r *SingleShardRouter) ShardCount(storeType StoreType) int {
	switch storeType {
	case StoreTypeSession, StoreTypeTenant, StoreTypeBilling, StoreTypeAudit:
		return 1
	}
	return 0
}

func validConcern(c RedisConcern) bool {
	switch c {
	case RedisConcernCoordination, RedisConcernQuota, RedisConcernCachePubSub,
		RedisConcernDelegation, RedisConcernSessionData:
		return true
	}
	return false
}
