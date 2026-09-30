// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/audit/integrity"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/blobstore/cataloging"
	"github.com/lennylabs/lenny/pkg/blobstore/miniostore"
	blobproviderflags "github.com/lennylabs/lenny/pkg/blobstore/providerflags"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointretention"
	checkpointretentionpg "github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointretention/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	partialmanifestpg "github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore"
	connectorpg "github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	tenantpg "github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	transcriptpg "github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	userpg "github.com/lennylabs/lenny/pkg/gateway/environment/userstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/drainreadiness"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	poolpg "github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimecapoverride"
	capoverridepg "github.com/lennylabs/lenny/pkg/gateway/runtime/runtimecapoverride/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	runtimepg "github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionlogstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/sqlitestore"
)

// buildStores constructs the §4.2/§4.4/§4.5 persistence surfaces, the §12.3
// store router, the §11.2.1 billing pipeline, the §7.1/§10.2/§10.3 signing
// and verification surfaces, the §17.4 executor, the §4.9 credential
// assignment service, the §15.1 pod-placement lifecycle, and the §7.2/§7.3
// session-messaging and per-session shared stores, recording each on the
// accumulator. It is the persistence-and-credential build step of the §4.1
// composition root, decomposed into per-concern sub-steps that read and
// record their cross-step state on the accumulator (proposal 0020 §4 Part A
// R1; the buildBillingPipeline / buildTokenSigningStores precedent).
//
// spec: §4.1 gateway subsystem seams; §4.2/§4.4/§4.5 stores; §12.3 store
// router; §4.9 credentials; §15.1 pod placement.
func (w *gatewayWiring) buildStores() {
	// checkpointRetention is the only persistence local without an
	// accumulator field: it is constructed by the §4.4/§12.5 retention step
	// and consumed only by the §15.1 pod-lifecycle checkpointer, so it is
	// threaded through an explicit return rather than onto the accumulator.
	checkpointRetention := w.buildPersistenceStores()
	w.buildRedisAndQuota()
	w.buildStoreRouterAndSecurityBus()
	// spec: §4.1 / §11.2.1 — build the billing failover pipeline. Records
	// the ledger, pipeline, fan-out emitter, and resolved audit-retention
	// schedule on the accumulator.
	w.buildBillingPipeline(w.billing, w.pgPool, w.redisClient, w.concernRedis, w.tenants, w.replica)
	// spec: §4.1 / §7.1 — build the uploadToken, KMS, and signing/verification
	// surfaces. Records them on the accumulator.
	w.buildTokenSigningStores(w.tenants)
	w.buildExecutorAndCredentials()
	w.buildPodLifecycle(checkpointRetention)
	w.buildSessionMessaging()
}

// buildPersistenceStores constructs the §4.2 session and metadata stores
// (Postgres, §17.4 embedded SQLite, or in-memory), the §12.3 read-replica /
// billing-audit / audit-sync pools, the §4.4 partial-manifest, session-log,
// and §12.5 checkpoint-retention stores, the §4.5/§17.9.3 artifact store and
// its §12.5 catalog, and the §5 pool store, recording each on the
// accumulator. It returns the §12.5 checkpoint-retention store, which is
// consumed only by the §15.1 pod-lifecycle checkpointer.
//
// spec: §4.1 gateway subsystem seams; §4.2/§4.4/§4.5 stores; §12.3 pools;
// §12.5 catalog.
func (w *gatewayWiring) buildPersistenceStores() checkpointretention.Store {
	f := w.f
	auditSyncWritePoolSize := f.auditSyncWritePoolSize
	billingAuditDSN := f.billingAuditDSN
	billingAuditDDLDSN := f.billingAuditDDLDSN
	primaryDDLDSN := f.primaryDDLDSN
	minioAccessKey := f.minioAccessKey
	minioBucket := f.minioBucket
	minioEndpoint := f.minioEndpoint
	minioSecretKey := f.minioSecretKey
	minioUseSSL := f.minioUseSSL
	objectStorageAccountURL := f.objectStorageAccountURL
	objectStorageBucket := f.objectStorageBucket
	objectStorageFilesystemRoot := f.objectStorageFilesystemRoot
	objectStorageProvider := f.objectStorageProvider
	objectStorageRegion := f.objectStorageRegion
	poolerMode := f.poolerMode
	postgresDSN := f.postgresDSN
	readDSN := f.readDSN
	sqlitePath := f.sqlitePath

	// ----- Stores -----
	// session, transcript, tenant, and runtime state is persisted to
	// Postgres when --postgres-dsn is set, and held in memory
	// otherwise. The remaining stores are in-memory pending their
	// Redis (circuit breakers, quota) or Postgres backings.
	var (
		sessions         sessionstore.Store
		tenants          tenantstore.Store
		runtimes         runtimestore.Store
		capOverrides     runtimecapoverride.Store
		transcripts      transcriptstore.Store
		users            userstore.Store
		connectors       connectorstore.Store
		billing          billingstore.Store
		pgPool           *pgxpool.Pool
		readPool         *pgxpool.Pool
		billingAuditPool *pgxpool.Pool
		auditSyncPool    *pgxpool.Pool
		// sqliteDB is the §17.4 Source-Mode embedded-SQLite
		// durability layer. Non-nil only when --sqlite-path is set and
		// --postgres-dsn is empty; the shutdown path stops the flush loop
		// (sqliteFlushCancel) and flushes+closes it. F-17.4.2.
		sqliteDB          *sqlitestore.DB
		sqliteFlushCancel context.CancelFunc
	)
	// spec: §12.3, §15.1 — the CREATE-privileged per-tenant DDL pools resolved
	// by resolveDDLPools below. Declared separately from the store block so the
	// resolveDDLPools call assigns both in one statement. F-11.2.10.
	var billingAuditDDLPool, primaryDDLPool *pgxpool.Pool
	if *postgresDSN != "" {
		pool, err := pgxpool.New(context.Background(), *postgresDSN)
		if err != nil {
			log.Fatalf("lenny-gateway: postgres: %v", err)
		}
		if err := verifyPostgresSchema(context.Background(), pool); err != nil {
			log.Fatalf("lenny-gateway: %v", err)
		}
		// spec: §12.3 — cloud-managed pooler defense. Under
		// LENNY_POOLER_MODE=external the managed proxy cannot run the
		// connect_query __unset__ sentinel, so the per-transaction
		// lenny_tenant_guard trigger is the load-bearing RLS defense.
		// Refuse to start when the trigger is absent from any
		// tenant-scoped table, independent of the §17.6 preflight Job so a
		// post-install migration rollback is also caught. The fatal fires
		// regardless of LENNY_ENV because external pooler mode is an
		// explicit production posture. F-12.3.1 / F-12.2.14 / F-17.9.2.
		if err := integrity.VerifyCloudManagedPoolerDefense(context.Background(), pool, *poolerMode); err != nil {
			log.Fatalf("lenny-gateway: %v", err)
		}
		pgPool = pool
		// spec: §12.3 — the optional read-replica reader endpoint.
		// When --postgres-read-dsn is set, the read-heavy query classes the
		// spec names route to this replica (the StoreRouter audit-read path
		// and the session-status / task-tree / usage-report read paths in
		// their stores); every write stays on the primary. The replica
		// serves the same migrations/ schema, so the same startup schema
		// verification applies. Empty keeps reads on the primary (readPool
		// stays nil and every read shares pgPool). F-12.3.16 / F-17.9.13.
		if *readDSN != "" {
			rpool, err := pgxpool.New(context.Background(), *readDSN)
			if err != nil {
				log.Fatalf("lenny-gateway: read-replica postgres: %v", err)
			}
			if err := verifyPostgresSchema(context.Background(), rpool); err != nil {
				log.Fatalf("lenny-gateway: read-replica postgres: %v", err)
			}
			readPool = rpool
			log.Printf("lenny-gateway: §12.3 routing read-heavy queries (session status, task tree, audit reads, usage reports) to the LENNY_PG_READ_DSN read replica")
		}
		// §12.3 — when the separate billing/audit instance is
		// configured, open and verify its pool here so the §12.3 R-03
		// StoreRouter below can route billing/audit writes to it. The
		// separate instance carries the migrations/ schema for the
		// append-only ledgers (billing_events, audit_log); the
		// cloud-managed-pooler defense is primary-only (the separate
		// instance is operator-provisioned for write isolation, not a
		// tenant-facing RLS surface), so only the schema check runs here.
		// F-12.3.5.
		if *billingAuditDSN != "" {
			bapool, err := pgxpool.New(context.Background(), *billingAuditDSN)
			if err != nil {
				log.Fatalf("lenny-gateway: billing/audit postgres: %v", err)
			}
			if err := verifyPostgresSchema(context.Background(), bapool); err != nil {
				log.Fatalf("lenny-gateway: billing/audit postgres: %v", err)
			}
			billingAuditPool = bapool
			log.Printf("lenny-gateway: §12.3 routing billing-event and audit-log writes to the separate LENNY_PG_BILLING_AUDIT_DSN instance")
		}
		// spec: §12.3, §15.1 — CREATE-privileged DDL pools for per-tenant
		// sequence provisioning. The per-tenant billing (billing_seq_<40hex>)
		// and audit (audit_seq_<40hex>) sequences are created at tenant-create
		// time through a role that holds CREATE ON SCHEMA public, distinct from
		// the lenny_app billing/audit pool the StoreRouter resolves for Append
		// (lenny_app holds no CREATE grant). The billing/audit DDL pool targets
		// the instance where billing_events and audit_log physically live (the
		// separate LENNY_PG_BILLING_AUDIT_DSN instance when configured, otherwise
		// the primary). The primary DDL pool targets the primary instance the
		// §13.3 issued-token write-before-issue path seals its per-tenant audit
		// row on; when the separate billing/audit instance is not configured the
		// primary and the billing/audit instance are one, so the primary DDL pool
		// falls back to the single billing/audit DDL pool. The admin Router
		// (adminrouter.go) threads both pools into the provisioning helper (S4).
		// F-11.2.10.
		var ddlErr error
		billingAuditDDLPool, primaryDDLPool, ddlErr = resolveDDLPools(
			context.Background(),
			ddlPoolDSNs{
				billingAudit:    *billingAuditDSN,
				billingAuditDDL: *billingAuditDDLDSN,
				primaryDDL:      *primaryDDLDSN,
			},
			pgxpool.New,
			verifyPostgresSchema,
		)
		if ddlErr != nil {
			log.Fatalf("lenny-gateway: §12.3 per-tenant DDL postgres: %v", ddlErr)
		}
		if billingAuditDDLPool != nil {
			log.Printf("lenny-gateway: §15.1 per-tenant billing/audit sequence provisioning uses the CREATE-privileged LENNY_PG_BILLING_AUDIT_DDL_DSN connection")
		}
		if primaryDDLPool != nil && primaryDDLPool != billingAuditDDLPool {
			log.Printf("lenny-gateway: §15.1 per-tenant primary audit sequence provisioning uses the CREATE-privileged LENNY_PG_PRIMARY_DDL_DSN connection")
		}
		// §12.3: a dedicated, small audit sync write pool so the
		// synchronous audit hash-chain writes do not consume the shared
		// request pool's connections. It targets the instance where the
		// audit ledger physically lives (the separate billing/audit
		// instance when configured, otherwise the primary), sized by
		// audit.syncWritePoolSize. A non-positive size keeps audit writes
		// on the router pool. F-12.3.14.
		if *auditSyncWritePoolSize > 0 {
			auditDSN := *postgresDSN
			if *billingAuditDSN != "" {
				auditDSN = *billingAuditDSN
			}
			syncCfg, err := pgxpool.ParseConfig(auditDSN)
			if err != nil {
				log.Fatalf("lenny-gateway: §12.3 audit sync write pool config: %v", err)
			}
			syncCfg.MaxConns = int32(*auditSyncWritePoolSize)
			sp, err := pgxpool.NewWithConfig(context.Background(), syncCfg)
			if err != nil {
				log.Fatalf("lenny-gateway: §12.3 audit sync write pool: %v", err)
			}
			auditSyncPool = sp
			log.Printf("lenny-gateway: §12.3 dedicated audit sync write pool active (max_conns=%d)", *auditSyncWritePoolSize)
		}
		// §11.7 startup integrity check: the append-only ledgers must
		// keep their grants, triggers, and erasure guard intact.
		// Production refuses to start on a violation; other
		// environments log a warning and continue. The check runs against
		// the instance where the ledgers physically live — the separate
		// billing/audit pool when configured, otherwise the primary.
		// F-12.3.5.
		ledgerPool := pool
		if billingAuditPool != nil {
			ledgerPool = billingAuditPool
		}
		if err := integrity.Verify(context.Background(), ledgerPool); err != nil {
			if os.Getenv("LENNY_ENV") == "production" {
				log.Fatalf("lenny-gateway: audit integrity check failed: %v", err)
			}
			log.Printf("lenny-gateway: WARNING: audit integrity check failed (non-production, continuing): %v", err)
		}
		sessions = sessionpg.New(pool, sessionpg.WithReadPool(readPool))
		tenants = tenantpg.New(pool)
		runtimes = runtimepg.New(pool)
		capOverrides = capoverridepg.New(pool)
		transcripts = transcriptpg.New(pool)
		users = userpg.New(pool)
		connectors = connectorpg.New(pool)
		// billing is constructed below via the §12.3 R-03 StoreRouter,
		// once the Redis client (if any) is resolved, so the ledger never
		// holds a raw pool. F-12.3.4 / F-12.6.1 / F-12.2.13 / F-12.7.1.
		log.Printf("lenny-gateway: persisting sessions, transcripts, tenants, runtimes, users, connectors, and billing events to Postgres")
	} else {
		sessMem := memstore.New()
		tenantMem := tenantstore.NewMemory()
		runtimeMem := runtimestore.NewMemory()
		capMem := runtimecapoverride.NewMemory()
		transcriptMem := transcriptstore.NewMemory()
		userMem := userstore.NewMemory()
		connectorMem := connectorstore.NewMemory()
		billingMem := billingstore.NewMemory()
		sessions = sessMem
		tenants = tenantMem
		runtimes = runtimeMem
		capOverrides = capMem
		transcripts = transcriptMem
		users = userMem
		connectors = connectorMem
		billing = billingMem
		// spec: §17.4 — Source Mode replaces Postgres with
		// embedded SQLite for session and metadata storage. With
		// --sqlite-path set, the in-memory stores above are loaded from
		// the SQLite file on startup, snapshotted to it every two seconds
		// while serving, and flushed once more on graceful shutdown, so a
		// `make run` developer keeps their tenants, runtimes, sessions,
		// and transcripts across a restart without a Postgres dependency.
		// The stores' query logic is the same in-memory implementation
		// the tier-3 contract suites exercise; SQLite is purely the
		// durable backing. F-17.4.2.
		if *sqlitePath != "" {
			db, err := sqlitestore.Open(*sqlitePath)
			if err != nil {
				log.Fatalf("lenny-gateway: §17.4 sqlite: %v", err)
			}
			sqliteDB = db
			sqliteDB.Register("sessions", sessMem)
			sqliteDB.Register("tenants", tenantMem)
			sqliteDB.Register("runtimes", runtimeMem)
			sqliteDB.Register("runtime_cap_overrides", capMem)
			sqliteDB.Register("transcripts", transcriptMem)
			sqliteDB.Register("users", userMem)
			sqliteDB.Register("connectors", connectorMem)
			sqliteDB.Register("billing_events", billingMem)
			if err := sqliteDB.Restore(context.Background()); err != nil {
				log.Fatalf("lenny-gateway: §17.4 sqlite restore: %v", err)
			}
			var flushCtx context.Context
			flushCtx, sqliteFlushCancel = context.WithCancel(context.Background())
			// Idempotent backstop release of the auto-flush context. The
			// graceful-shutdown path cancels sqliteFlushCancel explicitly
			// before sqliteDB.Close so the flush loop stops in the right
			// order; this deferred call guarantees the CancelFunc is released
			// on every runGateway exit path and is a no-op once the shutdown
			// path has already called it. F-17.4.2.
			sqliteDB.StartAutoFlush(flushCtx, 2*time.Second, func(err error) {
				log.Printf("lenny-gateway: §17.4 sqlite flush: %v", err)
			})
			log.Printf("lenny-gateway: §17.4 Source Mode embedded SQLite store at %s (sessions, tenants, runtimes, transcripts, users, connectors, billing persist across restart)", *sqlitePath)
		} else {
			log.Printf("lenny-gateway: session and metadata stores are in-memory (no --postgres-dsn or --sqlite-path)")
		}
	}
	// §4.4 / §10.1 checkpoint_manifest store: persists the
	// intent, per-chunk confirm, and terminal finalisation rows the §15.1
	// checkpoint driver writes as it produces a workspace checkpoint, plus
	// the partial recovery-aid row an eviction checkpoint leaves when it
	// exceeds the preStop tiered cap. The store is always initialized; the
	// pod-lifecycle checkpointer wires it as its writer, and the resume-side
	// cleanup path reads the same rows.
	var partialManifests partialmanifeststore.Store
	if pgPool != nil {
		partialManifests = partialmanifestpg.New(pgPool, nil)
	} else {
		partialManifests = partialmanifeststore.NewMemoryStore(nil)
	}
	// §4.4 session-log store: persists runtime stderr to MinIO
	// when a session reaches a terminal state. The store is best-effort;
	// the Noop implementation drops the bytes and is sufficient for
	// in-memory deployments. A MinIO endpoint configured below
	// upgrades the wiring to MinIOStore so production retains the
	// observability artifact. The MinIO uploader integration is
	// deferred to the §4.5 wiring follow-on; today the close-hook
	// fires with an empty body so the session-completion path
	// exercises the contract without writing any object.
	// spec: §4.4.
	var sessionLogs sessionlogstore.Store = sessionlogstore.Noop{}
	// §4.4 / §12.5 latest-2 retention catalog. The Postgres-
	// backed store records every successful checkpoint and runs the
	// rotation in the same transaction; the in-memory store backs the
	// dev-mode deployment so the checkpointer can call Insert + Rotate
	// without a live database.
	// spec: §4.4.
	var checkpointRetention checkpointretention.Store
	if pgPool != nil {
		checkpointRetention = checkpointretentionpg.New(pgPool, nil)
	} else {
		checkpointRetention = checkpointretention.NewMemoryStore(nil)
	}
	// §4.5 / §17.9.3 artifact store: the object-storage backend the
	// operator selected via objectStorage.provider (minio | s3 | gcs |
	// azure). Empty defaults to MinIO when --minio-endpoint is set,
	// otherwise the in-memory store for the minimal gateway. The §12.5
	// ll. 297-303 SSEKeyResolver is wired into every backend so a T4
	// tenant's Put is fail-closed under the per-tenant SSE-KMS alias
	// regardless of which provider serves the bucket. F-17.5.1 /
	// F-12.7.3.
	objectStore, err := blobproviderflags.Resolve(context.Background(), blobproviderflags.Options{
		Provider:        *objectStorageProvider,
		Bucket:          *objectStorageBucket,
		Region:          *objectStorageRegion,
		AzureAccountURL: *objectStorageAccountURL,
		FilesystemRoot:  *objectStorageFilesystemRoot,
		MinIOEndpoint:   *minioEndpoint,
		MinIOAccessKey:  *minioAccessKey,
		MinIOSecretKey:  *minioSecretKey,
		MinIOBucket:     *minioBucket,
		MinIOUseSSL:     *minioUseSSL,
		// §12.5 ll. 297-303 SSEKeyResolver: look up the writing
		// tenant's workspaceTier on every Put and hand the backend the
		// per-tenant SSE-KMS alias for T4 tenants.
		SSEKeyResolver: newSSEKeyResolver(tenants),
	})
	if err != nil {
		log.Fatalf("lenny-gateway: §17.9.3 object storage: %v", err)
	}
	// minioStore retains the concrete *miniostore.Store so the
	// MinIO-specific durable-catalog reader and the startup T4 KMS
	// probe wire against it below. It is nil for every non-MinIO
	// backend.
	var minioStore *miniostore.Store
	if ms, ok := objectStore.(*miniostore.Store); ok {
		minioStore = ms
	}
	var blobs blobstore.Store = objectStore
	// blobProbe is the §12.5 drain-readiness liveness probe — a real
	// bucket check when the backend implements drainreadiness.Prober
	// (MinIO), an always-ready stub otherwise (the in-memory store and
	// managed cloud object storage, which cannot degrade the way a
	// self-managed MinIO can).
	var blobProbe drainreadiness.Prober = drainreadiness.ProberFunc(func(context.Context) error { return nil })
	if p, ok := objectStore.(drainreadiness.Prober); ok {
		blobProbe = p
	}
	log.Printf("lenny-gateway: §4.5/§17.9.3 artifact store backend=%q (§12.5 SSEKeyResolver wired; T4 tenant-scoped SSE-KMS)",
		objectStoreBackendName(*objectStorageProvider, *minioEndpoint))

	// §13.2 capability model: a pod-based deployment mints presigned
	// checkpoint capabilities against the resolved backend, so that
	// backend MUST implement blobstore.Presigner. The check is on the
	// resolved store rather than on the configured provider name because
	// provider=minio with an empty --minio-endpoint falls back to the
	// in-memory store, which deliberately omits Presigner. Fail closed at
	// startup rather than at the first checkpoint attempt.
	//
	// spec: §13.2 (capability model).
	if *f.agentNamespace != "" {
		if _, ok := objectStore.(blobstore.Presigner); !ok {
			log.Fatalf("lenny-gateway: §13.2 pod-based sandboxes (--agent-namespace=%q) require a Presigner-capable artifact store, but the resolved backend %T does not implement it (an empty --minio-endpoint falls back to the in-memory store); configure a signing object-store backend",
				*f.agentNamespace, objectStore)
		}
	}

	// §12.5 / §17.9.7 — the fail-closed replacement for the SigV4
	// signature binding that the GCS V4 and Azure SAS checkpoint PUT paths
	// cannot carry per request. On a gcs or azure backend the presigned
	// capability signs no encryption header, so a workspaceTier T4 tenant's
	// per-tenant encryption rests on a backend default (a per-tenant GCS
	// bucket-default CMEK, or an Azure container default encryption scope with
	// DenyEncryptionScopeOverride). Refuse to boot when such a backend serves
	// any T4 tenant without that default declared, before the deployment
	// silently writes T4 checkpoints under the deployment-wide key. The
	// §17.6 preflight check reads the same objectStorage.{gcs,azure} keys.
	//
	// spec: §12.5; §17.9.7.
	if err := w.assertT4DefaultEncryption(context.Background(), tenants); err != nil {
		log.Fatalf("lenny-gateway: %v", err)
	}

	// §12.5 ll. 309-321 artifact_store catalog. The Postgres-backed
	// catalog is the surface the §12.5 GC sweep, the §11.2 size
	// accounting, the §12.8 erasure orchestrator, and the §12.5 legal-
	// hold checks all read against. A nil pgPool yields a noop wrapper
	// — the in-memory deployment runs without the catalog, which is
	// the dev-mode posture: production paths require Postgres.
	var artifactCatalog artifactcatalog.Store
	var blobsCataloged *cataloging.Store
	if pgPool != nil {
		artifactCatalog = artifactcatalog.New(pgPool, nil)
		blobsCataloged = cataloging.New(blobs, artifactCatalog, cataloging.Options{
			LogOnCatalogFailure: func(uri string, err error) {
				log.Printf("lenny-gateway: §12.5 artifact_store catalog insert failed for %s: %v", uri, err)
			},
		})
		blobs = blobsCataloged
		log.Printf("lenny-gateway: §12.5 artifact_store catalog wired (Postgres-backed)")
	}
	var pools poolstore.Store = poolstore.NewMemory()
	if pgPool != nil {
		pools = poolpg.New(pgPool)
	}

	// spec: §4.1 — record the §4.2/§4.4/§4.5 persistence surfaces on the
	// accumulator for the Redis, router, pod-lifecycle, and messaging steps.
	w.sessions = sessions
	w.tenants = tenants
	w.runtimes = runtimes
	w.capOverrides = capOverrides
	w.transcripts = transcripts
	w.users = users
	w.connectors = connectors
	w.billing = billing
	w.pgPool = pgPool
	w.readPool = readPool
	w.billingAuditPool = billingAuditPool
	w.billingAuditDDLPool = billingAuditDDLPool
	w.primaryDDLPool = primaryDDLPool
	w.auditSyncPool = auditSyncPool
	w.sqliteDB = sqliteDB
	w.sqliteFlushCancel = sqliteFlushCancel
	w.partialManifests = partialManifests
	w.sessionLogs = sessionLogs
	w.objectStore = objectStore
	w.minioStore = minioStore
	w.blobs = blobs
	w.blobProbe = blobProbe
	w.artifactCatalog = artifactCatalog
	w.blobsCataloged = blobsCataloged
	w.pools = pools
	return checkpointRetention
}

// objectStoreBackendName returns the human-readable backend name for
// the startup log line. An empty provider resolves to "minio" when a
// MinIO endpoint is configured and "memory" otherwise, matching the
// §17.9.3 default behaviour Resolve implements.
func objectStoreBackendName(provider, minioEndpoint string) string {
	if p := strings.ToLower(strings.TrimSpace(provider)); p != "" {
		return p
	}
	if minioEndpoint != "" {
		return blobproviderflags.ProviderMinIO
	}
	return blobproviderflags.ProviderMemory
}

// runServers registers the §25.13 in-process alert tracker, installs the
// signal handler, starts the §11.7 / §12.3 audit background loops and the
// HTTP, LLM-proxy, and GatewayControl listeners, then blocks on SIGTERM /
// SIGINT and runs the §17 graceful-shutdown drain. It is the tail of the
// gateway composition root: every component it serves is constructed by an
// earlier build step and threaded through the accumulator.
//
// spec: §17 — the run-and-shutdown loop; §25.13 in-process alert tracker.
