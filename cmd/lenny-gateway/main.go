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
	"encoding/json"
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
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/integrity"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/miniostore"
	"github.com/lennylabs/lenny/pkg/circuitbreaker"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	billingpg "github.com/lennylabs/lenny/pkg/gateway/billingstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore/cachingstore"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore/redisstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	connectorpg "github.com/lennylabs/lenny/pkg/gateway/connectorstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/coordination"
	"github.com/lennylabs/lenny/pkg/gateway/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentialserver"
	"github.com/lennylabs/lenny/pkg/gateway/credentialstore"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/drainreadiness"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/erasurejob"
	"github.com/lennylabs/lenny/pkg/gateway/evalstore"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/gitref"
	"github.com/lennylabs/lenny/pkg/gateway/health"
	"github.com/lennylabs/lenny/pkg/gateway/health/backends"
	"github.com/lennylabs/lenny/pkg/gateway/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/gateway/leasestore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/memorystore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	cbmw "github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker"
	idemmw "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency"
	idempgstore "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency/pgstore"
	ratelimitmw "github.com/lennylabs/lenny/pkg/gateway/middleware/ratelimit"
	"github.com/lennylabs/lenny/pkg/gateway/openapi"
	"github.com/lennylabs/lenny/pkg/gateway/opsevents"
	"github.com/lennylabs/lenny/pkg/gateway/orphancleanup"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/ratelimit"
	ratelimitredis "github.com/lennylabs/lenny/pkg/gateway/ratelimit/redisstore"
	"github.com/lennylabs/lenny/pkg/gateway/recommendations"
	"github.com/lennylabs/lenny/pkg/gateway/retentiongc"
	"github.com/lennylabs/lenny/pkg/gateway/revocation"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	runtimepg "github.com/lennylabs/lenny/pkg/gateway/runtimestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/storagequota"
	storagequotaredis "github.com/lennylabs/lenny/pkg/gateway/storagequota/redisstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	tenantpg "github.com/lennylabs/lenny/pkg/gateway/tenantstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/transcriptstore"
	transcriptpg "github.com/lennylabs/lenny/pkg/gateway/transcriptstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/translator"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
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

// adapterGRPCPort is the TCP port a Sandbox pod's §4.7 adapter listens
// on. §13.2 fixes the gateway↔adapter link to TCP 50051.
const adapterGRPCPort = 50051

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
	rlGlobalPerMin := flag.Int("rate-limit-global-per-min", 0,
		"§11.1 global requests-per-minute admission limit. Zero disables the global rate limit.")
	rlPerUserPerMin := flag.Int("rate-limit-per-user-per-min", 0,
		"§11.1 per-user requests-per-minute admission limit. Zero disables the per-user rate limit.")
	agentNamespace := flag.String("agent-namespace", os.Getenv("LENNY_AGENT_NAMESPACE"),
		"Kubernetes namespace the §5 warm pools and Sandboxes live in. When set, the gateway places each started session on a warm pod via the §4.7 adapter instead of the in-process executor.")
	adapterTLSCert := flag.String("adapter-tls-cert", os.Getenv("LENNY_ADAPTER_TLS_CERT"),
		"path to the gateway's client certificate for the §4.7 mTLS link to pod adapters. Empty dials adapters in plaintext (local development only).")
	adapterTLSKey := flag.String("adapter-tls-key", os.Getenv("LENNY_ADAPTER_TLS_KEY"),
		"path to the private key for --adapter-tls-cert.")
	adapterCA := flag.String("adapter-ca", os.Getenv("LENNY_ADAPTER_CA"),
		"path to the CA bundle that verifies a pod adapter's server certificate on the §4.7 mTLS link.")
	llmProxyAddr := flag.String("llm-proxy-addr", os.Getenv("LENNY_LLM_PROXY_ADDR"),
		"§4.9 LLM reverse-proxy listen address (host:port, e.g. :8443). When set, the gateway serves the proxy for proxy-mode agent pods on this address. Empty disables the LLM proxy listener.")
	anthropicVersion := flag.String("anthropic-version", os.Getenv("LENNY_ANTHROPIC_VERSION"),
		"default anthropic-version header the §4.9 LLM proxy injects when a request omits it. Empty rejects a request that omits the header.")
	minioEndpoint := flag.String("minio-endpoint", os.Getenv("LENNY_MINIO_ENDPOINT"),
		"MinIO endpoint (host:port). When set, the §4.5 artifact store is the MinIO-backed blob store; the drain-readiness endpoint runs a real §12.5 bucket probe. When empty, an in-memory blob store is used.")
	minioAccessKey := flag.String("minio-access-key", os.Getenv("LENNY_MINIO_ACCESS_KEY"),
		"MinIO access key. Required when --minio-endpoint is set.")
	minioSecretKey := flag.String("minio-secret-key", os.Getenv("LENNY_MINIO_SECRET_KEY"),
		"MinIO secret key. Required when --minio-endpoint is set.")
	minioBucket := flag.String("minio-bucket", os.Getenv("LENNY_MINIO_BUCKET"),
		"MinIO bucket for §4.5 artifacts. Required when --minio-endpoint is set.")
	minioUseSSL := flag.Bool("minio-use-ssl", envFlag("LENNY_MINIO_USE_SSL"),
		"connect to MinIO over HTTPS. Override via LENNY_MINIO_USE_SSL.")
	checkpointInterval := flag.Duration("checkpoint-interval", 5*time.Minute,
		"§4.4 periodic-checkpoint cadence. The gateway snapshots every coordinated session's workspace on this interval; active only with --agent-namespace.")
	noEnvPolicy := flag.String("no-environment-policy", os.Getenv("LENNY_NO_ENVIRONMENT_POLICY"),
		"§10.6 platform-wide noEnvironmentPolicy (deny-all or allow-all). Required outside --dev-mode.")
	flag.Parse()

	// §10.6: the platform-wide noEnvironmentPolicy must be set
	// explicitly. Dev mode derives allow-all for local convenience;
	// outside dev mode an unset value is a fatal misconfiguration so a
	// chart with the default stripped fails closed at startup.
	resolvedNoEnvPolicy := *noEnvPolicy
	if resolvedNoEnvPolicy == "" && *devMode {
		resolvedNoEnvPolicy = tenantstore.NoEnvPolicyAllowAll
	}
	if resolvedNoEnvPolicy == "" {
		log.Fatalf("lenny-gateway: LENNY_CONFIG_MISSING config_key=noEnvironmentPolicy scope=platform: " +
			"set --no-environment-policy or LENNY_NO_ENVIRONMENT_POLICY to deny-all or allow-all (§10.6)")
	}
	if resolvedNoEnvPolicy != tenantstore.NoEnvPolicyDenyAll && resolvedNoEnvPolicy != tenantstore.NoEnvPolicyAllowAll {
		log.Fatalf("lenny-gateway: --no-environment-policy must be deny-all or allow-all, got %q", resolvedNoEnvPolicy)
	}

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
	// §4.5 artifact store: MinIO-backed when --minio-endpoint is set,
	// otherwise an in-memory store for the minimal gateway. blobProbe
	// is the §12.5 drain-readiness liveness probe — a real MinIO
	// bucket check with MinIO, an always-ready stub for the in-memory
	// store, which is process-local and cannot degrade.
	var blobs blobstore.Store = blobstore.NewMemoryStore(nil)
	var blobProbe drainreadiness.Prober = drainreadiness.ProberFunc(func(context.Context) error { return nil })
	if *minioEndpoint != "" {
		ms, err := miniostore.New(miniostore.Config{
			Endpoint:  *minioEndpoint,
			AccessKey: *minioAccessKey,
			SecretKey: *minioSecretKey,
			Bucket:    *minioBucket,
			UseSSL:    *minioUseSSL,
		})
		if err != nil {
			log.Fatalf("lenny-gateway: minio: %v", err)
		}
		blobs = ms
		blobProbe = ms
		log.Printf("lenny-gateway: §4.5 artifact store is MinIO at %s (bucket %q)", *minioEndpoint, *minioBucket)
	}
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
		rateLimiter    ratelimit.Counter    = ratelimit.NewMemory()
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
		// The §11.1 rate-limit counter is Redis-backed so requests-per-
		// minute limits hold across replicas.
		rateLimiter = ratelimitredis.New(redisClient)
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

	// §15.1 pod placement: with --agent-namespace the gateway claims a
	// §5 warm pod for each started session and dispatches its messages
	// to the pod's §4.7 adapter. The in-process and subprocess
	// executors stay available for local development.
	var (
		podBinder     *podsession.Binder
		podRegistry   *podsession.Registry
		checkpointSvc *checkpointer.Checkpointer
	)
	if *agentNamespace != "" {
		cfg, err := ctrl.GetConfig()
		if err != nil {
			log.Fatalf("lenny-gateway: resolve cluster config for --agent-namespace: %v", err)
		}
		scheme := k8sruntime.NewScheme()
		utilruntime.Must(clientgoscheme.AddToScheme(scheme))
		utilruntime.Must(lennyv1.AddToScheme(scheme))
		k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
		if err != nil {
			log.Fatalf("lenny-gateway: build cluster client: %v", err)
		}
		dialOpt, err := adapter.TLSClientOption(*adapterTLSCert, *adapterTLSKey, *adapterCA)
		if err != nil {
			log.Fatalf("lenny-gateway: adapter TLS: %v", err)
		}
		podRegistry = podsession.NewRegistry()
		podBinder = &podsession.Binder{
			Client:           k8sClient,
			Namespace:        *agentNamespace,
			AdapterPort:      adapterGRPCPort,
			AcceptedVersions: []string{adapter.ProtocolVersionV1},
			DialAdapter: func(addr string) (*adapterclient.Client, error) {
				return adapterclient.Dial(addr, dialOpt)
			},
		}
		exec = executor.NewPodExecutor(podRegistry, podBinder)
		checkpointSvc = &checkpointer.Checkpointer{
			Sessions: sessions,
			Registry: podRegistry,
			Interval: *checkpointInterval,
			OnError: func(sessionID string, err error) {
				log.Printf("lenny-gateway: checkpoint of session %s failed: %v", sessionID, err)
			},
		}
		log.Printf("lenny-gateway: placing sessions on warm pods in namespace %q", *agentNamespace)
	}

	// §7.1 seal-and-export uses the same checkpointer; an untyped-nil
	// Sealer keeps seal-and-export disabled without --agent-namespace.
	var sessionSealer sessionserver.Sealer
	if checkpointSvc != nil {
		sessionSealer = checkpointSvc
	}

	eventBus := events.NewBus(0)
	// One §8.10 tree archive shared by the sessionserver (which archives
	// children on terminal transitions) and the platform MCP tools.
	treeArchive := treearchive.NewMemory()
	// One §9.2 interaction store shared by the sessionserver (which
	// serves the respond/dismiss endpoints) and the platform MCP tools
	// (lenny/request_elicitation), so an elicitation a tool records is
	// resolvable through the REST surface.
	interactions := interactionstore.NewMemory()
	evals := evalstore.NewMemory(0, nil)
	experiments := experimentstore.NewMemory()
	memories := memorystore.NewInMemory(0, nil)

	// ----- §16.1 Prometheus metrics -----
	gwMetrics, err := gatewaymetrics.New()
	if err != nil {
		log.Fatalf("lenny-gateway: metrics: %v", err)
	}

	// The §11.7 per-tenant audit hash chain. With Postgres the chain is
	// durable (auditstore); otherwise it is in-memory and lost on
	// restart. Both the admin router and the §10.7 ExperimentRouter
	// rejection reporter commit events to it.
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

	// §12.8: re-surface any tenant that combines billingErasurePolicy
	// exempt with a regulated compliance profile so the retention
	// posture cannot silently persist across redeployments.
	if err := admin.EmitBillingErasureExemptRegulatedStartup(
		context.Background(), tenants, auditSink, nil); err != nil {
		log.Printf("lenny-gateway: WARNING: billing-erasure-exempt startup scan: %v", err)
	}

	// environments backs the §10.6 admin environment CRUD, the
	// transparent filtering on lenny/discover_agents, and the §9.1
	// GET /v1/runtimes discovery surface.
	environments := environmentstore.NewMemory()
	// §4 runtime tenant-access registry, shared by the admin
	// tenant-access endpoints and the §5.1 internal meta-fetch endpoint.
	tenantAccess := tenantaccessstore.NewMemory()

	// §25.3 operational-event emitter, shared by the gateway subsystems
	// that emit and the admin event-buffer query endpoint.
	opsEmitter := opsevents.NewEmitter(opsevents.NewEventBuffer(0), buildVersion)

	// §4.9 credential-pool registry, shared by the admin credential-pool
	// CRUD and the §14 gitClone auth host-to-pool binding check.
	credentialPools := credentialpoolstore.NewMemory()

	sessionSrv := sessionserver.New(sessions, sessionserver.Options{
		UploadTokenIssuer:          uploadIssuer,
		UploadTokenVerifier:        uploadVerifier,
		Blobs:                      blobs,
		Executor:                   exec,
		Transcripts:                transcripts,
		Events:                     eventBus,
		Interactions:               interactions,
		Evals:                      evals,
		Experiments:                experiments,
		Pools:                      pools,
		Runtimes:                   runtimes,
		Environments:               environments,
		TenantAccess:               tenantAccess,
		OpsEmitter:                 opsEmitter,
		RefResolver:                gitref.NewLsRemoteResolver(gitref.Options{}),
		CredentialPools:            credentialPools,
		DefaultNoEnvironmentPolicy: resolvedNoEnvPolicy,
		ExperimentRejections: experimentRejectionReporter{
			audit:   auditSink,
			metrics: gwMetrics,
			emitter: opsEmitter,
		},
		Usage:          usagestore.NewMemory(),
		Users:          users,
		Billing:        billing,
		Tenants:        tenants,
		StorageQuota:   storageCounter,
		PodBinder:      podBinder,
		PodRegistry:    podRegistry,
		AgentNamespace: *agentNamespace,
		Sealer:         sessionSealer,
		TreeArchive:    treeArchive,
	})

	// ----- OpenAI Chat + Open Responses translators -----
	openaiHandler := translator.NewOpenAIChatHandler(sessions, exec, translator.OpenAIChatOptions{})
	responsesHandler := translator.NewOpenResponsesHandler(sessions, exec, translator.OpenResponsesOptions{})

	// ----- §4.9 end-user credential registry -----
	credServer := credentialserver.New(credentialstore.NewMemory(nil))

	// ----- MCP adapter -----
	delegationSvc := delegation.NewService(sessions, delegation.Options{Experiments: experiments})
	mcpSrv := mcp.NewServer()
	mcptools.Register(mcpSrv, mcptools.Deps{
		Store:                      sessions,
		Executor:                   exec,
		Delegation:                 delegationSvc,
		Runtimes:                   runtimes,
		Environments:               environments,
		Tenants:                    tenants,
		Pools:                      pools,
		Audit:                      mcpDelegationAuditor{sink: auditSink},
		DefaultNoEnvironmentPolicy: resolvedNoEnvPolicy,
		Interceptors:               interceptor.NewChain(),
		Events:                     eventBus,
		InputWaits:                 inputwait.NewRegistry(),
		TreeArchive:                treeArchive,
		Interactions:               interactions,
		Memory:                     memories,
		ElicitationMetrics:         gwMetrics,
		TenantID:                   "default",
	})

	// §13.3 revocation cache: the auth middleware rejects a token
	// whose jti is in this set. It is rehydrated from the Postgres
	// issued-token index below.
	revCache := revocation.NewCache()

	// ----- Admin API -----
	adminRouter := admin.NewRouter(tenants, admin.Options{Audit: auditSink, Metrics: gwMetrics}).
		WithRuntimes(runtimes).
		WithUsers(users).
		WithPools(pools).
		WithBreakers(breakers).
		WithConnectors(connectors).
		WithDelegationPolicies(delegationpolicystore.NewMemory()).
		WithCredentialPools(credentialPools).
		WithCustomRoles(customrolestore.NewMemory()).
		WithTenantAccess(tenantAccess).
		WithSessions(sessions).
		WithInteractions(interactions).
		WithExperiments(experiments).
		WithEnvironments(environments).
		WithEvalResults(evals).
		WithRecommendations(recommendations.NewCapacityService(
			recommendations.NewWindowStore(7 * 24 * time.Hour)))
	adminRouter = adminRouter.
		WithEventBuffer(opsEmitter.Buffer()).
		WithEventEmitter(opsEmitter)
	adminRouter = wireAudit(adminRouter)
	// §12.8 GDPR erasure: build the DeleteByUser orchestrator over the
	// wired stores and expose it behind the admin erasure endpoints.
	// Session-scoped stores (transcripts, artifacts) are erased per
	// session before the session-keyed user-scoped stores.
	{
		sessionScoped := []erasure.SessionEraser{}
		if te, ok := transcripts.(sessionArtifactDeleter); ok {
			sessionScoped = append(sessionScoped,
				erasure.SessionEraser{Name: "transcripts", DeleteBySession: te.DeleteBySession})
		}
		if be, ok := blobs.(sessionArtifactDeleter); ok {
			sessionScoped = append(sessionScoped,
				erasure.SessionEraser{Name: "artifacts", DeleteBySession: be.DeleteBySession})
		}
		sessionScoped = append(sessionScoped,
			erasure.SessionEraser{Name: "eval_results", DeleteBySession: evals.DeleteBySession})
		erasureOrch := erasure.New(erasure.Config{
			Sessions: func(ctx context.Context, tenantID, userID string) ([]string, error) {
				rows, err := sessions.List(ctx, tenantID, sessionstore.ListFilter{})
				if err != nil {
					return nil, err
				}
				ids := make([]string, 0, len(rows))
				for _, s := range rows {
					if s.UserID == userID {
						ids = append(ids, s.ID)
					}
				}
				return ids, nil
			},
			SessionScoped: sessionScoped,
			UserScoped: []erasure.Eraser{
				{Name: "interactions", DeleteByUser: interactions.DeleteByUser},
				{Name: "memory", DeleteByUser: func(ctx context.Context, tenantID, userID string) (int, error) {
					// §9.4 MemoryStore.DeleteByUser returns only an error;
					// the orchestrator's adapter reports the count it
					// cannot supply as 0.
					return 0, memories.DeleteByUser(ctx, tenantID, userID)
				}},
				{Name: "sessions", DeleteByUser: sessions.DeleteByUser},
			},
		})
		erasureJobs := erasurejob.NewMemory()
		erasureRunner := erasurejob.NewRunner(erasureJobs, erasureOrch, nil)
		// §12.8: billing events are append-only, so the erasure job
		// pseudonymizes them rather than deleting them. The Postgres
		// billing store's pseudonymize path is deferred (it needs an
		// UPDATE under the lenny_erasure role), so this attaches the
		// BillingEraser only when the in-memory billing store is wired.
		if be, ok := billing.(erasurejob.BillingErasureStore); ok {
			erasureRunner = erasureRunner.WithBilling(erasurejob.NewBillingEraser(be, tenants))
		}
		adminRouter = adminRouter.WithErasure(erasureRunner, erasureJobs)
	}
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
	// §25.3: emit a health_status_changed operational event when the
	// aggregate health verdict transitions.
	healthAgg.OnTransition(func(prev, curr health.Status) {
		data, _ := json.Marshal(map[string]any{
			"oldStatus": string(prev), "newStatus": string(curr),
		})
		opsEmitter.Emit(opsevents.OperationalEvent{
			Source:          "/v1/admin/health",
			Type:            opsevents.EventHealthStatusChanged.CloudEventsType(),
			Severity:        "warning",
			DataContentType: "application/json",
			Data:            data,
		})
	})
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
	mux.Handle("GET /metrics", gwMetrics.Handler())

	// ----- Healthz (unauthenticated) -----
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// ----- §12.5 drain-readiness endpoint (unauthenticated) -----
	// The lenny-drain-readiness webhook probes this before admitting a
	// node-drain pod eviction. blobProbe runs a real MinIO bucket check
	// when the artifact store is MinIO-backed, and an always-ready stub
	// for the process-local in-memory store.
	mux.Handle("GET /internal/drain-readiness", &drainreadiness.Handler{Prober: blobProbe})

	// ----- Middleware stack -----
	var handler http.Handler = mux

	// The §11.2 per-tenant concurrent-session quota is enforced inside
	// the session-creation handlers (sessionserver.requireSessionQuota)
	// against each tenant's configured MaxConcurrentSessions.

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

	// §11.1 rate limiting next — runs just after auth so the per-user
	// scope sees the authenticated principal. Limits default to zero
	// (disabled); operators set them via the rate-limit flags.
	handler = ratelimitmw.Wrap(handler, ratelimitmw.Options{
		Counter:          rateLimiter,
		GlobalPerMinute:  *rlGlobalPerMin,
		PerUserPerMinute: *rlPerUserPerMin,
	})

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

	// ----- §4.9 LLM reverse proxy -----
	// With --llm-proxy-addr the gateway serves the §4.9 LLM proxy for
	// proxy-mode agent pods on a listener separate from the REST API.
	llmProxySrv := newLLMProxyServer(*llmProxyAddr, *anthropicVersion)

	// ----- §6.2 / §11.3 pre-running watchdog -----
	// Sweeps every 5 s; transitions stuck sessions to failed.
	// Tenants list is sourced from the in-memory store so newly
	// registered tenants are picked up on the next tick.
	wd := watchdog.New(sessions, tenantsLister{tenants}, watchdog.Config{}, nil).
		WithBilling(billing).
		WithTreeArchive(treeArchive)
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

	// ----- §8.10 orphan-cleanup job -----
	orphanSweeper := orphancleanup.New(sessions, tenantsLister{tenants}, orphancleanup.Options{
		Archive: treeArchive,
	})
	go orphanSweeper.Run(watchdogCtx, func(terminated int, err error) {
		if err != nil {
			log.Printf("lenny-gateway: orphan-cleanup sweep error: %v", err)
			return
		}
		if terminated > 0 {
			log.Printf("lenny-gateway: orphan-cleanup terminated %d sessions past the §8.10 cascade timeout",
				terminated)
		}
	})

	// ----- §7.1 artifact-retention GC -----
	// Collects the workspace snapshot, transcript, and blobs of every
	// terminal session past its retention TTL; a §12.8 legal hold
	// exempts the session.
	{
		var arts []retentiongc.Artifact
		if te, ok := transcripts.(sessionArtifactDeleter); ok {
			arts = append(arts, retentiongc.Artifact{Name: "transcripts", Delete: te.DeleteBySession})
		}
		if be, ok := blobs.(sessionArtifactDeleter); ok {
			arts = append(arts, retentiongc.Artifact{Name: "artifacts", Delete: be.DeleteBySession})
		}
		retGC := retentiongc.New(sessions, tenantsLister{tenants}, arts, retentiongc.Options{})
		go retGC.Run(watchdogCtx, func(collected int, err error) {
			if err != nil {
				log.Printf("lenny-gateway: retention-GC sweep error: %v", err)
				return
			}
			if collected > 0 {
				log.Printf("lenny-gateway: retention-GC collected artifacts for %d sessions past their §7.1 retention TTL",
					collected)
			}
		})
	}

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

	// ----- §4.4 periodic-checkpoint loop -----
	// Active only with --agent-namespace: snapshots every coordinated
	// session's workspace on the checkpoint cadence so the §7.1
	// WorkspaceSnapshot stays fresh against the §16.5 freshness SLO.
	// The same checkpointer backs the §7.1 seal-and-export on the
	// session-completion path.
	if checkpointSvc != nil {
		go checkpointSvc.Run(watchdogCtx)
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

	// ----- §16.1 metrics export -----
	// Refreshes the gauge metrics (storage quota, circuit breakers)
	// that the §16.5 alerts read.
	exportGaugeMetrics := func(ctx context.Context) {
		exportStorageQuotaMetrics(ctx, tenants, storageCounter, gwMetrics)
		exportCircuitBreakerMetrics(ctx, breakers, breakerCache, gwMetrics)
	}
	exportGaugeMetrics(context.Background())
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-watchdogCtx.Done():
				return
			case <-ticker.C:
				exportGaugeMetrics(watchdogCtx)
			}
		}
	}()

	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		log.Printf("lenny-gateway: listening on %s (dev_mode=%v multi_tenant=%v)",
			*addr, *devMode, *multiTenant)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("lenny-gateway: listen: %v", err)
		}
	}()
	if llmProxySrv != nil {
		go func() {
			log.Printf("lenny-gateway: §4.9 LLM proxy listening on %s", llmProxySrv.Addr)
			if err := llmProxySrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("lenny-gateway: llm proxy listen: %v", err)
			}
		}()
	}

	<-stopCh
	log.Printf("lenny-gateway: shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), *shutdownTimeout)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	if llmProxySrv != nil {
		_ = llmProxySrv.Shutdown(ctx)
	}
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

// newLLMProxyServer builds the §4.9 LLM reverse-proxy HTTP server,
// serving the Anthropic Messages endpoint at POST /llm-proxy/v1/messages.
// It returns nil when addr is empty, which disables the proxy listener.
// The credential-lease store, the credential cache, and the deny list
// start empty; the §4.9 credential-assignment path populates them, and
// a request that arrives before then is cleanly rejected.
func newLLMProxyServer(addr, anthropicVersion string) *http.Server {
	if addr == "" {
		return nil
	}
	proxyMux := http.NewServeMux()
	proxyMux.Handle("POST /llm-proxy/v1/messages", &llmproxy.Handler{
		Leases:      credleasestore.New(),
		Translator:  &llmproxy.AnthropicDirectTranslator{DefaultAnthropicVersion: anthropicVersion},
		Forwarder:   &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
		Credentials: credcache.New(),
		DenyList:    denylist.New(),
	})
	return &http.Server{
		Addr:              addr,
		Handler:           proxyMux,
		ReadHeaderTimeout: 10 * time.Second,
	}
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

// sessionArtifactDeleter is implemented by session-scoped stores that
// expose the per-session DeleteBySession adapter — the transcript and
// blob stores. It backs both the §12.8 erasure orchestrator and the
// §7.1 retention GC.
type sessionArtifactDeleter interface {
	DeleteBySession(ctx context.Context, tenantID, sessionID string) (int, error)
}

// experimentRejectionReporter bridges a §10.7 ExperimentRouter
// fail-closed rejection to the §11.7 audit chain, the §16.1 metrics
// registry, and the §25.3 operational-event buffer: it records the
// `experiment.isolation_mismatch` event on all three and increments
// `lenny_experiment_isolation_rejections_total`.
type experimentRejectionReporter struct {
	audit   admin.AuditSink
	metrics *gatewaymetrics.Metrics
	emitter *opsevents.Emitter
}

func (e experimentRejectionReporter) ReportExperimentIsolationRejection(ctx context.Context, ev sessionserver.ExperimentIsolationRejection) {
	if e.metrics != nil {
		e.metrics.RecordExperimentIsolationRejection(ev.TenantID, ev.ExperimentID, ev.VariantID)
	}
	detail := map[string]any{
		"tenant_id":            ev.TenantID,
		"user_id":              ev.UserID,
		"experiment_id":        ev.ExperimentID,
		"variant_id":           ev.VariantID,
		"sessionMinIsolation":  ev.SessionMinIsolation,
		"variantPoolIsolation": ev.VariantPoolIsolation,
	}
	if e.audit != nil {
		e.audit.EmitAdminEvent(ctx, admin.AuditEvent{
			Type:           "experiment.isolation_mismatch",
			ActorTenantID:  ev.TenantID,
			TargetResource: ev.ExperimentID,
			Detail:         detail,
		})
	}
	// §16.6: the rejection is also an operational event — surface it on
	// the §25.3 event buffer so ops agents observe it without log scraping.
	if e.emitter != nil {
		data, _ := json.Marshal(detail)
		e.emitter.Emit(opsevents.OperationalEvent{
			Source:          "/v1/sessions",
			Type:            opsevents.EventExperimentIsolationMismatch.CloudEventsType(),
			Severity:        "warning",
			DataContentType: "application/json",
			Data:            data,
		})
	}
}

// mcpDelegationAuditor adapts the gateway audit sink to the
// mcptools.DelegationAuditor interface, drawing the §11.7 actor fields
// from the request principal on the context.
type mcpDelegationAuditor struct {
	sink admin.AuditSink
}

func (a mcpDelegationAuditor) EmitDelegationEvent(ctx context.Context, eventType string, detail map[string]any) {
	if a.sink == nil {
		return
	}
	ev := admin.AuditEvent{Type: eventType, Detail: detail, At: time.Now().UTC()}
	if p, ok := authmw.FromContext(ctx); ok {
		ev.ActorSubject = p.Subject
		ev.ActorTenantID = p.TenantID
	}
	a.sink.EmitAdminEvent(ctx, ev)
}

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

// exportStorageQuotaMetrics refreshes the §16.1 per-tenant
// storage-quota gauges from the tenant registry and the storage
// counter. Only tenants with a configured quota are exported so the
// §16.5 StorageQuotaHigh alert does not divide by a zero limit.
func exportStorageQuotaMetrics(ctx context.Context, tenants tenantstore.Store, counter storagequota.Counter, m *gatewaymetrics.Metrics) {
	rows, err := tenants.List(ctx, tenantstore.ListFilter{})
	if err != nil {
		log.Printf("lenny-gateway: storage-quota metrics: listing tenants failed: %v", err)
		return
	}
	for _, t := range rows {
		if t.StorageQuotaBytes <= 0 {
			continue
		}
		used, err := counter.Used(ctx, t.ID)
		if err != nil {
			continue
		}
		m.SetStorageQuota(t.ID, used, t.StorageQuotaBytes)
	}
}

// exportCircuitBreakerMetrics refreshes the §16.1 circuit-breaker
// gauges: the per-breaker open state and the cache freshness. In
// in-memory mode there is no cache, so it reports the registry as
// always-current and initialized.
func exportCircuitBreakerMetrics(ctx context.Context, breakers breakerRegistry, cache *cachingstore.Store, m *gatewaymetrics.Metrics) {
	if rows, err := breakers.List(ctx); err == nil {
		for _, b := range rows {
			m.SetCircuitBreakerOpen(b.Name, b.State == circuitbreaker.StateOpen)
		}
	}
	if cache == nil {
		m.SetCircuitBreakerCache(0, true)
		return
	}
	last := cache.LastRefresh()
	if last.IsZero() {
		m.SetCircuitBreakerCache(0, false)
		return
	}
	m.SetCircuitBreakerCache(time.Since(last).Seconds(), true)
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
