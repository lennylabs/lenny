// SPDX-License-Identifier: MIT

// Command lenny-gateway is the minimal Lenny gateway binary. It
// serves:
//
//   - §15.1 REST session endpoints (POST/GET/list/derive/upload/...).
//   - §15.1 admin endpoints (tenant + runtime CRUD) gated on
//     platform-admin.
//   - §15.1 GET /v1/blobs/{ref} blob dereference.
//
// The handler stack wraps every request with:
//
//   - §10.2 auth middleware — Bearer JWT or dev-mode header
//     fallback, configurable via LENNY_DEV_MODE.
//   - §11.6 circuit-breaker admission middleware.
//   - §11.5 idempotency replay cache middleware.
//
// Backed by in-memory stores. The tier-3 contract suites and the
// tier-4 integration tests drive the same binary; production swaps
// the in-memory backends for Postgres / Redis / Kubernetes wiring
// behind the same interfaces.
//
// Usage:
//
//	lenny-gateway --addr :8080
//
// The binary exits 0 on graceful SIGTERM, non-zero on bind failure.
package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/integrity"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	billingpg "github.com/lennylabs/lenny/pkg/gateway/billingstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore/cachingstore"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore/redisstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	connectorpg "github.com/lennylabs/lenny/pkg/gateway/connectorstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/coordination"
	"github.com/lennylabs/lenny/pkg/gateway/credentialserver"
	"github.com/lennylabs/lenny/pkg/gateway/credentialstore"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/health"
	"github.com/lennylabs/lenny/pkg/gateway/health/backends"
	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/gateway/leasestore"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	cbmw "github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker"
	idemmw "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency"
	idempgstore "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency/pgstore"
	quotamw "github.com/lennylabs/lenny/pkg/gateway/middleware/quota"
	"github.com/lennylabs/lenny/pkg/gateway/openapi"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/revocation"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	runtimepg "github.com/lennylabs/lenny/pkg/gateway/runtimestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/storagequota"
	storagequotaredis "github.com/lennylabs/lenny/pkg/gateway/storagequota/redisstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	tenantpg "github.com/lennylabs/lenny/pkg/gateway/tenantstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/transcriptstore"
	transcriptpg "github.com/lennylabs/lenny/pkg/gateway/transcriptstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/translator"
	"github.com/lennylabs/lenny/pkg/gateway/usagestore"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
	userpg "github.com/lennylabs/lenny/pkg/gateway/userstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/watchdog"
	"github.com/lennylabs/lenny/pkg/idempotency"
	"github.com/lennylabs/lenny/pkg/tokenservice"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// Build metadata, overridable at link time via -ldflags
// "-X main.buildVersion=... -X main.buildCommit=... -X main.buildDate=...".
var (
	buildVersion = "dev"
	buildCommit  = "unknown"
	buildDate    = "unknown"
)

func main() {
	addr := flag.String("addr", ":8080", "address to bind (host:port)")
	multiTenant := flag.Bool("multi-tenant", false, "enable §10.2 multi-tenant claim extraction")
	devMode := flag.Bool("dev-mode", envFlag("LENNY_DEV_MODE"),
		"enable dev-mode auth shortcuts (X-Lenny-Roles dev-header). Override via LENNY_DEV_MODE.")
	runtimeBin := flag.String("runtime-bin", "",
		"path to a Basic-level runtime binary. When set, the gateway dispatches messages to a child process speaking the §15.4.1 adapter protocol instead of the in-process echo executor.")
	postgresDSN := flag.String("postgres-dsn", os.Getenv("LENNY_POSTGRES_DSN"),
		"Postgres connection string. When set, sessions, transcripts, tenants, and runtimes are persisted to Postgres (the migrations/ schema must already be applied). When empty, in-memory stores are used.")
	redisURL := flag.String("redis-url", os.Getenv("LENNY_REDIS_URL"),
		"Redis URL (redis://host:port/db). When set, circuit-breaker state is held in Redis so operator safety blocks survive a restart and stay consistent across replicas. When empty, an in-memory breaker store is used.")
	coordInterval := flag.Duration("coordination-interval", 15*time.Second,
		"§10.1 session-coordination lease sweep interval. Each sweep renews this replica's lease on every non-terminal session. Only active when --redis-url is set.")
	shutdownTimeout := flag.Duration("shutdown-timeout", 5*time.Second, "graceful shutdown timeout")
	flag.Parse()

	// ----- Stores -----
	// session, transcript, tenant, and runtime state is persisted to
	// Postgres when --postgres-dsn is set, and held in memory
	// otherwise. The remaining stores are in-memory pending their
	// Redis (circuit breakers, quota) or Postgres backings.
	var (
		sessions    sessionstore.Store
		tenants     tenantstore.Store
		runtimes    runtimestore.Store
		transcripts transcriptstore.Store
		users       userstore.Store
		connectors  connectorstore.Store
		billing     billingstore.Store
		pgPool      *pgxpool.Pool
	)
	if *postgresDSN != "" {
		pool, err := pgxpool.New(context.Background(), *postgresDSN)
		if err != nil {
			log.Fatalf("lenny-gateway: postgres: %v", err)
		}
		if err := verifyPostgresSchema(context.Background(), pool); err != nil {
			log.Fatalf("lenny-gateway: %v", err)
		}
		// §11.7 startup integrity check: the append-only ledgers must
		// keep their grants, triggers, and erasure guard intact.
		// Production refuses to start on a violation; other
		// environments log a warning and continue.
		if err := integrity.Verify(context.Background(), pool); err != nil {
			if os.Getenv("LENNY_ENV") == "production" {
				log.Fatalf("lenny-gateway: audit integrity check failed: %v", err)
			}
			log.Printf("lenny-gateway: WARNING: audit integrity check failed (non-production, continuing): %v", err)
		}
		pgPool = pool
		sessions = sessionpg.New(pool)
		tenants = tenantpg.New(pool)
		runtimes = runtimepg.New(pool)
		transcripts = transcriptpg.New(pool)
		users = userpg.New(pool)
		connectors = connectorpg.New(pool)
		billing = billingpg.New(pool)
		log.Printf("lenny-gateway: persisting sessions, transcripts, tenants, runtimes, users, connectors, and billing events to Postgres")
	} else {
		sessions = memstore.New()
		tenants = tenantstore.NewMemory()
		runtimes = runtimestore.NewMemory()
		transcripts = transcriptstore.NewMemory()
		users = userstore.NewMemory()
		connectors = connectorstore.NewMemory()
		billing = billingstore.NewMemory()
	}
	blobs := blobstore.NewMemoryStore(nil)
	pools := poolstore.NewMemory()

	// Circuit-breaker state goes to Redis when --redis-url is set, so
	// an operator-opened breaker survives a restart and stays
	// consistent across replicas (§12.4). The §10.1 session-
	// coordination lease sweeper runs against the same Redis.
	replica := resolveReplicaID()
	var (
		breakers       breakerRegistry
		breakerCache   *cachingstore.Store
		redisClient    *redis.Client
		coordinator    *coordination.Sweeper
		storageCounter storagequota.Counter = storagequota.NewMemory()
	)
	if *redisURL != "" {
		opts, err := redis.ParseURL(*redisURL)
		if err != nil {
			log.Fatalf("lenny-gateway: redis url: %v", err)
		}
		redisClient = redis.NewClient(opts)
		if err := redisClient.Ping(context.Background()).Err(); err != nil {
			log.Fatalf("lenny-gateway: redis: %v", err)
		}
		// The §11.6 breaker registry lives in Redis; the cachingstore
		// keeps a local open-breaker snapshot so the request-path check
		// never round-trips to Redis and survives a Redis outage.
		breakerCache = cachingstore.New(redisstore.New(redisClient), redisClient)
		breakers = breakerCache
		coordinator = coordination.NewSweeper(
			tenantsLister{tenants}, sessions, leasestore.New(redisClient),
			coordination.Options{ReplicaID: replica, Interval: *coordInterval})
		// The §11.2 storage-quota counter lives in Redis so the quota
		// holds across replicas; its reserve is Lua-atomic.
		storageCounter = storagequotaredis.New(redisClient)
		log.Printf("lenny-gateway: circuit-breaker state in Redis; coordination replica %s", replica)
	} else {
		breakers = breakerstore.NewMemory()
	}

	// ----- §7.1 uploadToken KeyRing -----
	// One ephemeral signing key per process. Production deployers
	// rotate this key on the §7.1 24-hour schedule via the KMS-backed
	// implementation; the minimal gateway uses a random key per boot.
	var seed [32]byte
	if _, err := rand.Read(seed[:]); err != nil {
		log.Fatalf("lenny-gateway: rand: %v", err)
	}
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "boot", Secret: seed[:]})
	uploadIssuer := uploadtoken.NewIssuer(ring, nil)
	uploadTracker := uploadtoken.NewMemoryTracker()
	uploadVerifier := uploadtoken.NewVerifier(ring, uploadTracker, nil)

	// ----- §13.3 Token Service -----
	// Ephemeral JWT HMAC signer per process. Production wires the
	// KMS-backed signer with the §12a envelope. The token-service
	// handler mounted below serves POST /v1/oauth/token (RFC 8693).
	var jwtSeed [32]byte
	if _, err := rand.Read(jwtSeed[:]); err != nil {
		log.Fatalf("lenny-gateway: rand: %v", err)
	}
	jwtSigner := jwt.NewHMACSigner("boot", jwtSeed[:])
	// With Postgres the §13.3 write-before-issue record is durable in
	// the issued_tokens table; otherwise the Token Service keeps only
	// its in-memory jti set.
	var issuedTokens tokenservice.IssuedTokenStore
	if pgPool != nil {
		issuedTokens = issuedtokenstore.New(pgPool)
	}
	tokSvc := tokenservice.NewServer(tokenservice.Options{
		Signer: jwtSigner,
		Issuer: "https://lenny.dev.local/token",
		PerDialectCap: map[string]time.Duration{
			"lenny-gateway": 24 * time.Hour,
			"lenny-ops":     1 * time.Hour,
			"llm-proxy":     1 * time.Hour,
		},
		IssuedTokens: issuedTokens,
	})

	// ----- Session API + Executor -----
	// Default: the in-process echo executor. With --runtime-bin, the
	// gateway dispatches to a child process speaking the §15.4.1
	// adapter protocol — the `make run` developer loop.
	var exec executor.Executor = executor.NewEchoExecutor()
	if *runtimeBin != "" {
		exec = executor.NewSubprocessExecutor(executor.SubprocessOptions{BinPath: *runtimeBin})
		log.Printf("lenny-gateway: dispatching sessions to runtime binary %s", *runtimeBin)
	}
	eventBus := events.NewBus(0)
	sessionSrv := sessionserver.New(sessions, sessionserver.Options{
		UploadTokenIssuer:   uploadIssuer,
		UploadTokenVerifier: uploadVerifier,
		Blobs:               blobs,
		Executor:            exec,
		Transcripts:         transcripts,
		Events:              eventBus,
		Interactions:        interactionstore.NewMemory(),
		Usage:               usagestore.NewMemory(),
		Users:               users,
		Billing:             billing,
		Tenants:             tenants,
		StorageQuota:        storageCounter,
	})

	// ----- OpenAI Chat + Open Responses translators -----
	openaiHandler := translator.NewOpenAIChatHandler(sessions, exec, translator.OpenAIChatOptions{})
	responsesHandler := translator.NewOpenResponsesHandler(sessions, exec, translator.OpenResponsesOptions{})

	// ----- §4.9 end-user credential registry -----
	credServer := credentialserver.New(credentialstore.NewMemory(nil))

	// ----- MCP adapter -----
	delegationSvc := delegation.NewService(sessions, delegation.Options{})
	mcpSrv := mcp.NewServer()
	mcptools.Register(mcpSrv, mcptools.Deps{
		Store:      sessions,
		Executor:   exec,
		Delegation: delegationSvc,
		TenantID:   "default",
	})

	// §13.3 revocation cache: the auth middleware rejects a token
	// whose jti is in this set. It is rehydrated from the Postgres
	// issued-token index below.
	revCache := revocation.NewCache()

	// ----- Admin API -----
	// Every admin mutation is committed to a §11.7 per-tenant audit
	// hash chain. With Postgres the chain is durable (auditstore);
	// otherwise it is in-memory and lost on restart.
	var (
		auditSink admin.AuditSink
		wireAudit func(*admin.Router) *admin.Router
	)
	if pgPool != nil {
		pgAudit := auditstore.New(pgPool)
		auditSink = admin.NewAuditLogSink(pgAudit, nil)
		wireAudit = func(rt *admin.Router) *admin.Router { return rt.WithAuditLog(pgAudit) }
	} else {
		auditChains := audit.NewChainSet()
		auditSink = admin.NewChainAuditSink(auditChains, nil)
		wireAudit = func(rt *admin.Router) *admin.Router { return rt.WithAuditChains(auditChains) }
	}
	adminRouter := admin.NewRouter(tenants, admin.Options{Audit: auditSink}).
		WithRuntimes(runtimes).
		WithUsers(users).
		WithPools(pools).
		WithBreakers(breakers).
		WithConnectors(connectors)
	adminRouter = wireAudit(adminRouter)
	if pgPool != nil {
		// §13.3 operator-initiated token revocation, durable in the
		// issued-token index and reflected in the revocation cache.
		adminRouter = adminRouter.WithIssuedTokens(issuedtokenstore.New(pgPool), revCache)
	}
	adminRouter = adminRouter.WithPlatformInfo(
		admin.PlatformInfo{Version: buildVersion, GitCommit: buildCommit, BuildDate: buildDate},
		map[string]string{
			"gateway.addr":        *addr,
			"gateway.multiTenant": boolStr(*multiTenant),
			"gateway.devMode":     boolStr(*devMode),
			"gateway.runtimeBin":  *runtimeBin,
			"gateway.postgres":    boolStr(*postgresDSN != ""),
			"gateway.redis":       boolStr(*redisURL != ""),
			"gateway.replicaId":   replica,
		},
	)

	// ----- Compose the mux -----
	mux := http.NewServeMux()
	mux.Handle("/v1/sessions", sessionSrv.Handler())
	mux.Handle("/v1/sessions/", sessionSrv.Handler())
	mux.Handle("/v1/blobs/", sessionSrv.Handler())
	mux.Handle("/v1/admin/", adminRouter.Handler())

	// §25.3 Platform Health API. Registered at the specific
	// /v1/admin/health* paths so Go's ServeMux routes them to the
	// health handler ahead of the /v1/admin/ admin catch-all.
	healthAgg := health.NewAggregator()
	healthAgg.Register(staticHealthy("gateway"))
	healthAgg.Register(staticHealthy("sessionstore"))
	healthAgg.Register(staticHealthy("blobstore"))
	healthAgg.Register(staticHealthy("executor"))
	// When a backing service is wired, the §25.3 health API reports
	// its real reachability instead of a static verdict.
	if pgPool != nil {
		healthAgg.Register(backends.Postgres(pgPool, "postgres"))
	}
	if redisClient != nil {
		healthAgg.Register(backends.Redis(redisClient, "redis"))
	}
	if breakerCache != nil {
		healthAgg.Register(backends.CircuitBreakerCache(breakerCache, "circuit-breaker-cache"))
	}
	healthHandler := health.Handler(healthAgg)
	mux.Handle("/v1/admin/health", healthHandler)
	mux.Handle("/v1/admin/health/", healthHandler)
	mux.Handle("/openapi.yaml", openapi.Handler())
	mux.Handle("/v1/openapi.json", openapi.Handler())
	mux.Handle("/v1/oauth/", tokSvc.Handler())
	mux.Handle("/v1/chat/completions", openaiHandler.Handler())
	mux.Handle("/v1/responses", responsesHandler.Handler())
	mux.Handle("/v1/responses/", responsesHandler.Handler())
	mux.Handle("/mcp", mcpSrv.Handler())
	mux.Handle("/v1/credentials", credServer.Handler())
	mux.Handle("/v1/credentials/", credServer.Handler())

	// ----- §16.1 Prometheus metrics -----
	gwMetrics, err := gatewaymetrics.New()
	if err != nil {
		log.Fatalf("lenny-gateway: metrics: %v", err)
	}
	mux.Handle("GET /metrics", gwMetrics.Handler())

	// ----- Healthz (unauthenticated) -----
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// ----- Middleware stack -----
	var handler http.Handler = mux

	// §5.75 QuotaEvaluator innermost — counts the tenant's active
	// sessions in the store and rejects session creation over the
	// ceiling. The minimal gateway uses a generous per-tenant limit
	// (1000 active sessions); production resolves per-tenant limits
	// from the tenant policy.
	quotaCounter := quotamw.StoreActiveCounter{
		List: func(ctx context.Context, tenantID string) ([]session.State, error) {
			rows, err := sessions.List(ctx, tenantID, sessionstore.ListFilter{})
			if err != nil {
				return nil, err
			}
			states := make([]session.State, 0, len(rows))
			for _, row := range rows {
				states = append(states, row.State)
			}
			return states, nil
		},
	}
	handler = quotamw.Wrap(handler, quotamw.Options{
		Counter: quotaCounter,
		Limits:  quotamw.StaticLimit(1000),
	})

	// Idempotency next (after auth + circuit; needs the
	// authenticated tenant on the request to scope keys correctly).
	// The §11.5 key cache is durable under --postgres-dsn so an
	// idempotent retry replays across gateway replicas and restarts.
	var idemStore idemmw.Store = idemmw.NewMemoryStore()
	if pgPool != nil {
		idemStore = idempgstore.New(pgPool)
	}
	handler = idemmw.Wrap(handler, idemStore, idemmw.Options{})

	// Circuit breaker next: rejects requests when any open breaker
	// matches. The shared breakerstore.Memory satisfies cbmw.Registry
	// so the admin /v1/admin/circuit-breakers endpoints share state
	// with the request-path middleware.
	handler = cbmw.Wrap(handler, breakers, cbmw.Options{})

	// Auth next-to-outermost. AllowDevRoles is only honoured when the
	// dev flag is set (LENNY_DEV_MODE=true or --dev-mode); production
	// deployments leave it off so X-Lenny-Roles cannot self-grant
	// platform-admin.
	//
	// The §10.2 Bearer path is verified with the same HMAC key the
	// in-process Token Service signs with, so a token minted by
	// POST /v1/oauth/token round-trips through the gateway. Production
	// swaps in the OIDC JWKS verifier.
	authOpts := authmw.Options{
		MultiTenant:     *multiTenant,
		AllowDevHeaders: true,
		AllowDevRoles:   *devMode,
		Verifier:        jwtSigner,
		Revocations:     revCache,
	}
	if !*multiTenant {
		// Even in single-tenant mode, dev-header callers carry the
		// tenant header. Flip to multi-tenant with a permissive
		// registry so the header round-trips.
		authOpts.MultiTenant = true
	}
	authOpts.Registry = permissiveRegistry{}
	handler = authmw.Wrap(handler, authOpts)

	// §16.1 request metrics, outermost wrap so every request — including
	// auth rejections — is counted. The route label collapses
	// high-cardinality path segments to a stable template.
	handler = gwMetrics.Middleware(handler, routeTemplate)

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// ----- §6.2 / §11.3 pre-running watchdog -----
	// Sweeps every 5 s; transitions stuck sessions to failed.
	// Tenants list is sourced from the in-memory store so newly
	// registered tenants are picked up on the next tick.
	wd := watchdog.New(sessions, tenantsLister{tenants}, watchdog.Config{}, nil).
		WithBilling(billing)
	watchdogCtx, watchdogCancel := context.WithCancel(context.Background())
	defer watchdogCancel()
	go wd.Run(watchdogCtx, func(res watchdog.Result, err error) {
		if err != nil {
			log.Printf("lenny-gateway: watchdog sweep error: %v", err)
			return
		}
		if res.ForcedFailures > 0 {
			log.Printf("lenny-gateway: watchdog forced %d sessions to failed: %v",
				res.ForcedFailures, res.PerReason)
		}
		if res.Expirations > 0 {
			log.Printf("lenny-gateway: watchdog expired %d sessions past their §11.3 deadline",
				res.Expirations)
		}
	})

	// ----- §10.1 session-coordination lease sweeper -----
	// Active only with Redis: it renews this replica's lease on every
	// non-terminal session so a crashed replica's sessions free up.
	if coordinator != nil {
		go coordinator.Run(watchdogCtx)
	}

	// ----- §11.6 circuit-breaker cache refresh -----
	// Active only with Redis: keeps the local open-breaker snapshot
	// current via pub/sub and a periodic refresh.
	if breakerCache != nil {
		go breakerCache.Run(watchdogCtx)
	}

	// ----- §13.3 revocation-cache rehydration -----
	// Loads revoked-token jtis from the issued-token index so a
	// revocation survives a restart and propagates across replicas.
	if pgPool != nil {
		issued := issuedtokenstore.New(pgPool)
		lister := tenantsLister{tenants}
		if err := revCache.Rehydrate(context.Background(), lister, issued); err != nil {
			log.Printf("lenny-gateway: initial revocation rehydration failed: %v", err)
		}
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-watchdogCtx.Done():
					return
				case <-ticker.C:
					if err := revCache.Rehydrate(context.Background(), lister, issued); err != nil && watchdogCtx.Err() == nil {
						log.Printf("lenny-gateway: revocation rehydration failed: %v", err)
					}
				}
			}
		}()
	}

	// ----- §11.5 idempotency-key TTL garbage collection -----
	// Reclaims idempotency_keys rows past the 24-hour retention window
	// so the durable key cache stays bounded.
	if pgPool != nil {
		idemGC := idempgstore.New(pgPool)
		lister := tenantsLister{tenants}
		sweepIdempotencyKeys(context.Background(), idemGC, lister)
		go func() {
			ticker := time.NewTicker(time.Hour)
			defer ticker.Stop()
			for {
				select {
				case <-watchdogCtx.Done():
					return
				case <-ticker.C:
					sweepIdempotencyKeys(watchdogCtx, idemGC, lister)
				}
			}
		}()
	}

	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		log.Printf("lenny-gateway: listening on %s (dev_mode=%v multi_tenant=%v)",
			*addr, *devMode, *multiTenant)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("lenny-gateway: listen: %v", err)
		}
	}()

	<-stopCh
	log.Printf("lenny-gateway: shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), *shutdownTimeout)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	if pgPool != nil {
		pgPool.Close()
	}
	if redisClient != nil {
		_ = redisClient.Close()
	}
}

// breakerRegistry is the breaker-store surface the gateway wires: the
// breakerstore.Store admin operations plus the cbmw.Registry snapshot
// the circuit-breaker middleware reads. Both the in-memory and the
// Redis-backed breaker stores satisfy it.
type breakerRegistry interface {
	breakerstore.Store
	cbmw.Registry
}

// verifyPostgresSchema fails fast when the gateway is pointed at a
// database that has not had the migrations/ schema applied. It probes
// for the sessions table; the fuller §11.7 startup grant-verification
// check lands with the audit pipeline.
func verifyPostgresSchema(ctx context.Context, pool *pgxpool.Pool) error {
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		 WHERE table_schema = 'public' AND table_name = 'sessions')`).Scan(&exists)
	if err != nil {
		return fmt.Errorf("postgres: schema probe failed: %w", err)
	}
	if !exists {
		return fmt.Errorf("postgres: schema not migrated (the sessions table is absent); apply migrations/ before starting the gateway")
	}
	return nil
}

// resolveReplicaID returns this gateway replica's §10.1 coordination
// identity: the LENNY_REPLICA_ID override, or the hostname plus a
// random suffix so two replicas sharing a host still differ.
func resolveReplicaID() string {
	if id := os.Getenv("LENNY_REPLICA_ID"); id != "" {
		return id
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "gateway"
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%x", host, b)
}

// permissiveRegistry accepts every tenant. The minimal gateway uses
// this so dev-header transports can name an arbitrary tenant during
// integration tests without operator pre-provisioning. Production
// swaps in a Postgres-backed Registry (e.g., the in-memory
// tenantstore.Memory which also satisfies auth.TenantRegistry).
type permissiveRegistry struct{}

func (permissiveRegistry) IsRegistered(string) (bool, error) { return true, nil }

// tenantsLister adapts a tenantstore.Store into a
// watchdog.TenantLister so the watchdog sweeps every registered
// tenant. In single-tenant deployments it also returns "default" so
// dev-mode sessions are bounded.
type tenantsLister struct {
	store tenantstore.Store
}

func (t tenantsLister) ListTenants(ctx context.Context) ([]string, error) {
	rows, err := t.store.List(ctx, tenantstore.ListFilter{})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows)+1)
	out = append(out, "default")
	for _, row := range rows {
		out = append(out, row.ID)
	}
	return out, nil
}

// sweepIdempotencyKeys runs one §11.5 TTL garbage-collection pass,
// deleting idempotency_keys rows older than the 24-hour retention
// window. The sweep is per-tenant because the lenny_tenant_guard
// trigger fires for every DELETE.
func sweepIdempotencyKeys(ctx context.Context, gc *idempgstore.Store, lister tenantsLister) {
	tenants, err := lister.ListTenants(ctx)
	if err != nil {
		log.Printf("lenny-gateway: idempotency GC: listing tenants failed: %v", err)
		return
	}
	cutoff := time.Now().Add(-idempotency.TTL)
	for _, tenant := range tenants {
		if _, err := gc.DeleteExpired(ctx, tenant, cutoff); err != nil && ctx.Err() == nil {
			log.Printf("lenny-gateway: idempotency GC: tenant %q sweep failed: %v", tenant, err)
		}
	}
}

// staticHealthy returns a §25.3 health Checker that always reports
// the named component healthy. The minimal gateway uses these
// because every subsystem is an in-process in-memory store with no
// failure mode; production swaps in checkers that probe Postgres /
// Redis / MinIO connectivity.
func staticHealthy(name string) health.Checker {
	return health.CheckerFunc{
		ComponentName: name,
		Fn: func(context.Context) health.Component {
			return health.Component{Name: name, Status: health.StatusHealthy}
		},
	}
}

// routeTemplate collapses a request path to a stable §16.1.1
// low-cardinality route label so the request metric does not
// explode into one series per session id / blob ref.
func routeTemplate(r *http.Request) string {
	p := r.URL.Path
	switch {
	case p == "/healthz", p == "/metrics", p == "/v1/sessions",
		p == "/v1/sessions/start", p == "/v1/chat/completions",
		p == "/v1/responses", p == "/mcp", p == "/openapi.yaml",
		p == "/v1/openapi.json":
		return p
	case strings.HasPrefix(p, "/v1/sessions/"):
		return "/v1/sessions/{id}/*"
	case strings.HasPrefix(p, "/v1/blobs/"):
		return "/v1/blobs/{ref}"
	case strings.HasPrefix(p, "/v1/responses/"):
		return "/v1/responses/{id}"
	case strings.HasPrefix(p, "/v1/admin/"):
		return "/v1/admin/*"
	case strings.HasPrefix(p, "/v1/oauth/"):
		return "/v1/oauth/*"
	default:
		return "other"
	}
}

// boolStr renders a bool as the lowercase string the §25.3
// platform-config endpoint surfaces.
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// envFlag returns true when the env var name is set to a truthy
// value (1, true, yes — case-insensitive). Used to default the
// --dev-mode flag from LENNY_DEV_MODE.
func envFlag(name string) bool {
	v := os.Getenv(name)
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
