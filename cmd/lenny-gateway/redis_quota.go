// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordination"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordlease"
	coordleasepg "github.com/lennylabs/lenny/pkg/gateway/coordination/coordlease/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationbudget"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/capacityplanning"
	"github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker/breakerstore"
	"github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker/breakerstore/cachingstore"
	"github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker/breakerstore/redisstore"
	"github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
	ratelimitredis "github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit/redisstore"
	"github.com/lennylabs/lenny/pkg/gateway/quota/storagequota"
	storagequotaredis "github.com/lennylabs/lenny/pkg/gateway/quota/storagequota/redisstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	leasepg "github.com/lennylabs/lenny/pkg/gateway/storage/leasestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/redistopology"
	"github.com/lennylabs/lenny/pkg/redisconn"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// buildRedisAndQuota constructs the §12.4 Redis client and per-concern split,
// the §11.6 circuit-breaker registry, the §10.1 coordination-lease sweeper
// and mirror, the §11.2 storage-quota counter and its §12.4 Postgres-fallback
// recovery reconciler, and the §11.1 Redis-backed rate-limit counter,
// recording each on the accumulator. Without Redis it records the in-memory
// breaker registry and leaves the quota and coordination surfaces on their
// in-memory defaults.
//
// spec: §4.1 gateway subsystem seams; §12.4 Redis topology; §11.6 breakers;
// §10.1 coordination; §11.1/§11.2 quota.
// outstandingReservationSource adapts the checkpoint manifest store's
// SumOutstandingReservations to a storagequota.LiveBytesSource so
// ReservationAwareLiveBytes can fold outstanding checkpoint reservations into
// the durable artifact_store byte sum. A nil store yields a nil source, which
// degrades the composed seam to the bare artifact_store sum. Taking the method
// value only when the store is non-nil avoids a method-value-on-nil panic.
//
// spec: §11.2 reservation-aware rebuild; §12.4.
func outstandingReservationSource(m partialmanifeststore.Store) storagequota.LiveBytesSource {
	if m == nil {
		return nil
	}
	return m.SumOutstandingReservations
}

func (w *gatewayWiring) buildRedisAndQuota() {
	f := w.f
	capacityTier := f.capacityTier
	coordInterval := f.coordInterval
	redisAllowInsecure := f.redisAllowInsecure
	redisCachePubSubURL := f.redisCachePubSubURL
	redisClusterAddrs := f.redisClusterAddrs
	redisCoordinationURL := f.redisCoordinationURL
	redisDelegationURL := f.redisDelegationURL
	redisPassword := f.redisPassword
	redisQuotaURL := f.redisQuotaURL
	redisSentinelAddrs := f.redisSentinelAddrs
	redisSentinelMaster := f.redisSentinelMaster
	redisSentinelPassword := f.redisSentinelPassword
	redisSessionDataURL := f.redisSessionDataURL
	redisTLS := f.redisTLS
	redisURL := f.redisURL
	singleTenantRedisTopology := f.singleTenantRedisTopology

	// pgPool, the session/tenant stores, and the artifact catalog were
	// constructed by buildPersistenceStores and recorded on the accumulator.
	pgPool := w.pgPool
	sessions := w.sessions
	tenants := w.tenants
	artifactCatalog := w.artifactCatalog
	partialManifests := w.partialManifests

	// spec: §11.2 / §12.4 — the reservation-aware storage
	// LiveBytesSource (SumLiveBytes + SumOutstandingReservations) is composed
	// once here, whenever the Postgres artifact catalog is wired, and shared by
	// the during-outage Failover enforcement read, the RecoveryReconciler
	// recovery-edge rebuild, and the startup Rehydrate, so a tenant's
	// outstanding checkpoint reservations count against its quota on every
	// rebuild the guarded relative Adjust runs after. Composition is independent
	// of Redis: a Postgres-catalog deployment without Redis still rebuilds its
	// in-memory counter (folding outstanding reservations) at startup, so the
	// counter does not start at zero and under-count after a restart.
	if artifactCatalog != nil {
		w.storageLiveBytes = storagequota.ReservationAwareLiveBytes(
			artifactCatalog.SumLiveBytes,
			outstandingReservationSource(partialManifests),
		)
	}

	// Circuit-breaker state goes to Redis when --redis-url is set, so
	// an operator-opened breaker survives a restart and stays
	// consistent across replicas (§12.4). The §10.1 session-
	// coordination lease sweeper runs against the same Redis.
	replica := resolveReplicaID()
	var (
		breakers       breakerRegistry
		breakerCache   *cachingstore.Store
		redisClient    redis.UniversalClient
		concernRedis   *redistopology.Clients
		coordinator    *coordination.Sweeper
		storageCounter storagequota.Counter = storagequota.NewMemory()
		rateLimiter    ratelimit.Counter    = ratelimit.NewMemory()
		// erasureLeaseStore captures the concrete §12.4 lease store for
		// the §12.8 step-1 LeaseStore erasure wiring (the store is built
		// inside the Redis-available branch below; nil without Redis).
		erasureLeaseStore leasestore.LeaseStore

		// coordMirror is the §10.1.8 coordination_lease barrier-target
		// mirror the Sweeper writes and the preStop barrier reads. Postgres-
		// backed when pgPool is wired; nil otherwise, in which case the
		// barrier falls back to the in-memory lease cache.
		coordMirror coordlease.Store

		// coordFencer drives the §10.1 / §4.2 CoordinatorFence after a
		// resume re-bind (announce coordination_generation; §11.3
		// retry/relinquish). Built inside the Redis-available branch below
		// (it needs the lease store for the relinquish path); nil interface
		// without Redis, which leaves the resume path unfenced.
		coordFencer sessionserver.CoordinationFencer

		// storageRecoveryReconciler drives the §12.4 write-back of
		// storage-quota counters to Redis on a Redis-recovery edge. Set only
		// when both Redis and the Postgres artifact catalog are wired.
		storageRecoveryReconciler *storagequota.RecoveryReconciler

		// delegationBudgetReconciler drives the §11.2 periodic Postgres
		// checkpoint of the §8.2 delegation tree budget counters and their
		// §11.2 two-source reconstruction on a Redis-recovery edge.
		// Set only when the delegation Redis counters, the Postgres pool,
		// and the SessionStore are all wired. F-11.2.5 / F-12.4.8.
		delegationBudgetReconciler *delegationbudget.Reconciler
	)
	if *redisURL != "" || *redisSentinelAddrs != "" || *redisClusterAddrs != "" {
		if *redisURL != "" && *redisSentinelAddrs != "" {
			log.Fatalf("lenny-gateway: --redis-url and --redis-sentinel-addrs are mutually exclusive")
		}
		var rcfg redisconn.Config
		switch {
		case *redisClusterAddrs != "":
			// §12.4: Cluster mode is the CLUSTER KEYSLOT-aware
			// topology and takes precedence over the direct/Sentinel fields.
			rcfg = redisconn.Config{
				ClusterAddrs:  splitAndTrim(*redisClusterAddrs),
				Password:      *redisPassword,
				TLS:           *redisTLS,
				AllowInsecure: *redisAllowInsecure,
			}
		case *redisURL != "":
			rcfg = redisconn.Config{URL: *redisURL, Password: *redisPassword, AllowInsecure: *redisAllowInsecure}
		default:
			rcfg = redisconn.Config{
				SentinelAddrs:    splitAndTrim(*redisSentinelAddrs),
				MasterName:       *redisSentinelMaster,
				Password:         *redisPassword,
				SentinelPassword: *redisSentinelPassword,
				TLS:              *redisTLS,
				AllowInsecure:    *redisAllowInsecure,
			}
		}
		client, err := redisconn.NewUniversalClient(rcfg)
		if err != nil {
			log.Fatalf("lenny-gateway: redis client: %v", err)
		}
		redisClient = client
		if err := redisconn.PingWithTimeout(redisClient, 5*time.Second); err != nil {
			log.Fatalf("lenny-gateway: redis: %v", err)
		}
		// §12.4: build the per-concern client split. A
		// concern with no dedicated URL falls back to the base client, so
		// the single Tier 1/2 topology resolves every concern to
		// redisClient unchanged. The auth/TLS template carries the base
		// password and allow-insecure posture to each per-concern URL.
		concernRedis, err = redistopology.Build(redisClient, map[storerouter.RedisConcern]string{
			storerouter.RedisConcernCoordination: *redisCoordinationURL,
			storerouter.RedisConcernQuota:        *redisQuotaURL,
			storerouter.RedisConcernCachePubSub:  *redisCachePubSubURL,
			storerouter.RedisConcernSessionData:  *redisSessionDataURL,
			storerouter.RedisConcernDelegation:   *redisDelegationURL,
		}, redisconn.Config{Password: *redisPassword, AllowInsecure: *redisAllowInsecure})
		if err != nil {
			log.Fatalf("lenny-gateway: redis concern split: %v", err)
		}
		// The §11.6 breaker registry lives in Redis (Cache/Pub-Sub
		// concern); the cachingstore keeps a local open-breaker snapshot
		// so the request-path check never round-trips to Redis and
		// survives a Redis outage.
		cacheClient := concernRedis.For(storerouter.RedisConcernCachePubSub)
		breakerCache = cachingstore.New(redisstore.New(cacheClient), cacheClient)
		breakers = breakerCache
		// §12.4 Coordination concern: session leases. The Redis-backed
		// store is the primary; with Postgres also wired the failover
		// wrapper routes lease operations to the §12.4 Postgres
		// advisory-lock fallback during a Redis outage, so coordination
		// degrades to higher latency rather than breaking lease
		// acquisition outright.
		var leaseStore leasestore.LeaseStore = leasestore.New(concernRedis.For(storerouter.RedisConcernCoordination))
		if pgPool != nil {
			leaseStore = leasestore.NewFailover(leaseStore, leasepg.New(pgPool), nil)
		}
		// §12.8 step 1: expose the lease store to the erasure orchestrator
		// so a user erasure releases the user's active coordination leases.
		erasureLeaseStore = leaseStore
		// spec: §4.6.1 (coordinating replica holds the lease), §10.1
		// (per-session coordination lease) — the same lease store backs the
		// at-bind Acquire in the sessionserver bind funnel, so the replica that
		// holds the pod binding is the lease holder from bind time. Recorded on
		// the accumulator for buildSessionServer to inject as
		// CoordinationLeaseStore.
		w.coordLeaseStore = leaseStore
		// §10.1.8: mirror held leases into Postgres so the preStop
		// barrier-target query observes coordinator handoffs that occurred
		// in the seconds before drain. Without Postgres the mirror is nil
		// and the barrier falls back to the in-memory lease cache.
		if pgPool != nil {
			coordMirror = coordleasepg.New(pgPool, nil)
		}
		// spec: §4.6.1 (coordinating replica holds the lease), §10.1
		// (per-session coordination lease; coordinator handoff re-adopts the
		// still-running pod) — co-locate the coordination lease with the pod
		// binding. The Sweeper renews the lease for the sessions this replica
		// binds (Bindings.Bound over podRegistry), evicts and releases a bound
		// session whose held gateway-to-pod channel has died (Bindings.ConnAlive
		// over the BindResult adapter channel, Bindings.EvictBinding over
		// podRegistry plus the executor's cached Attach stream), evicts without
		// any lease write a bound session a peer replica has taken over (the
		// lease is held elsewhere and coordination_generation has advanced past
		// the generation this replica last renewed at; spec: §10.1.1, §10.1.5),
		// and on the
		// crash-takeover edge re-adopts the still-running pod through a
		// fence-first re-adopt (Readopter over the Binder's ReadoptConnect entry
		// point, the reused coordfence Fencer, and podRegistry.Put). Both seams
		// read their collaborators (podRegistry, the executor, the Binder, the
		// coordFencer) lazily at sweep time because they are constructed in
		// later build steps; the Sweeper's Run loop starts only after the whole
		// composition root is wired.
		coordinator = coordination.NewSweeper(
			tenantsLister{tenants}, sessions, leaseStore,
			coordination.Options{
				ReplicaID: replica,
				Interval:  *coordInterval,
				Mirror:    coordMirror,
				Bindings:  coordinationBindings{w: w},
				Readopter: coordinationReadopter{w: w},
			},
		)
		// §12.4 Quota/Rate Limiting concern: the storage-quota counter
		// lives in Redis so the quota holds across replicas; its reserve
		// is Lua-atomic.
		storageRedis := storagequotaredis.New(concernRedis.For(storerouter.RedisConcernQuota))
		storageCounter = storageRedis
		// §12.4: when the durable artifact catalog is wired,
		// front the Redis counter with the Postgres-fallback failover so a
		// Redis outage degrades upload pre-checks to the authoritative
		// Postgres-derived total instead of breaking uploads, and a
		// simultaneous Postgres outage fails closed (ErrUnavailable → 503).
		// On Redis recovery the reconciler below writes the total back so the
		// Lua fast path resumes. Without a catalog (dev/in-memory) the bare
		// Redis store keeps the prior fail-on-Redis-error behavior.
		//
		// spec: §11.2 / §12.4 — the during-outage Failover
		// enforcement read and the recovery-edge rebuild share the single
		// reservation-aware LiveBytesSource composed above (whenever the
		// artifact catalog is wired), so a tenant's outstanding checkpoint
		// reservations count against its quota during a Redis outage and are
		// re-added on every rebuild the guarded relative Adjust runs after.
		if artifactCatalog != nil {
			liveBytes := w.storageLiveBytes
			storageCounter = storagequota.NewFailover(storageRedis, liveBytes, nil)
			storageRecoveryReconciler = &storagequota.RecoveryReconciler{
				Probe: func(ctx context.Context) bool {
					return redisconn.PingWithTimeout(redisClient, 2*time.Second) == nil
				},
				Primary: storageRedis,
				Tenants: (tenantsLister{tenants}).ListTenants,
				SizeOf:  liveBytes,
				Logf:    log.Printf,
			}
		}
		// §12.4 Quota/Rate Limiting concern: the §11.1 rate-limit counter
		// is Redis-backed so requests-per-minute limits hold across replicas.
		rateLimiter = ratelimitredis.New(concernRedis.For(storerouter.RedisConcernQuota))
		redisTopology := capacityplanning.RedisTopologyStandalone
		switch {
		case *redisClusterAddrs != "":
			redisTopology = capacityplanning.RedisTopologyCluster
			log.Printf("lenny-gateway: Redis via Cluster nodes=%d split=%t; coordination replica %s",
				len(splitAndTrim(*redisClusterAddrs)), concernRedis.Split(), replica)
		case *redisSentinelAddrs != "":
			redisTopology = capacityplanning.RedisTopologySentinel
			log.Printf("lenny-gateway: Redis via Sentinel master=%q sentinels=%d split=%t; coordination replica %s",
				*redisSentinelMaster, len(splitAndTrim(*redisSentinelAddrs)), concernRedis.Split(), replica)
		default:
			log.Printf("lenny-gateway: circuit-breaker state in Redis split=%t; coordination replica %s", concernRedis.Split(), replica)
		}
		// spec: §17.8.2 — a Tier 3 deployment on a single-tenant
		// Redis Sentinel topology gets the RedisClusterRecommended startup
		// warning unless capacityPlanning.singleTenantRedisTopology=sentinel
		// documents the operator's intent.
		if capacityplanning.ShouldWarnRedisClusterRecommended(*capacityTier, redisTopology, *singleTenantRedisTopology) {
			log.Printf("lenny-gateway: %s", capacityplanning.RedisClusterRecommendedWarning)
		}
	} else {
		breakers = breakerstore.NewMemory()
	}

	// spec: §4.1 — record the §12.4 Redis surfaces, the §11.6 breaker
	// registry, the §10.1 coordination sweeper/mirror, and the §11.1/§11.2
	// quota counters on the accumulator for the router, billing,
	// pod-lifecycle, and worker steps.
	w.replica = replica
	w.redisClient = redisClient
	w.concernRedis = concernRedis
	w.breakers = breakers
	w.breakerCache = breakerCache
	w.coordinator = coordinator
	w.coordMirror = coordMirror
	w.coordFencer = coordFencer
	w.erasureLeaseStore = erasureLeaseStore
	w.storageCounter = storageCounter
	w.storageRecoveryReconciler = storageRecoveryReconciler
	w.delegationBudgetReconciler = delegationBudgetReconciler
	w.rateLimiter = rateLimiter
}
