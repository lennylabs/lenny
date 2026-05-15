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
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentialserver"
	"github.com/lennylabs/lenny/pkg/gateway/credentialstore"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/health"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/openapi"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	cbmw "github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker"
	idemmw "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency"
	quotamw "github.com/lennylabs/lenny/pkg/gateway/middleware/quota"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/translator"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/watchdog"
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
	shutdownTimeout := flag.Duration("shutdown-timeout", 5*time.Second, "graceful shutdown timeout")
	flag.Parse()

	// ----- Stores -----
	sessions := memstore.New()
	blobs := blobstore.NewMemoryStore(nil)
	tenants := tenantstore.NewMemory()
	runtimes := runtimestore.NewMemory()
	users := userstore.NewMemory()
	pools := poolstore.NewMemory()
	breakers := breakerstore.NewMemory()
	connectors := connectorstore.NewMemory()

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
	tokSvc := tokenservice.NewServer(tokenservice.Options{
		Signer: jwtSigner,
		Issuer: "https://lenny.dev.local/token",
		PerDialectCap: map[string]time.Duration{
			"lenny-gateway": 24 * time.Hour,
			"lenny-ops":     1 * time.Hour,
			"llm-proxy":     1 * time.Hour,
		},
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
		Transcripts:         transcriptstore.NewMemory(),
		Events:              eventBus,
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

	// ----- Admin API -----
	// Every admin mutation is committed to a §11.7 per-tenant audit
	// hash chain via the ChainAuditSink.
	auditChains := audit.NewChainSet()
	adminRouter := admin.NewRouter(tenants, admin.Options{
		Audit: admin.NewChainAuditSink(auditChains, nil),
	}).
		WithRuntimes(runtimes).
		WithUsers(users).
		WithPools(pools).
		WithBreakers(breakers).
		WithConnectors(connectors).
		WithAuditChains(auditChains).
		WithPlatformInfo(
			admin.PlatformInfo{Version: buildVersion, GitCommit: buildCommit, BuildDate: buildDate},
			map[string]string{
				"gateway.addr":        *addr,
				"gateway.multiTenant": boolStr(*multiTenant),
				"gateway.devMode":     boolStr(*devMode),
				"gateway.runtimeBin":  *runtimeBin,
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
	idemStore := idemmw.NewMemoryStore()
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
	wd := watchdog.New(sessions, tenantsLister{tenants}, watchdog.Config{}, nil)
	watchdogCtx, watchdogCancel := context.WithCancel(context.Background())
	defer watchdogCancel()
	go wd.Run(watchdogCtx, func(res watchdog.Result, err error) {
		if err != nil {
			log.Printf("lenny-gateway: watchdog sweep error: %v", err)
		} else if res.ForcedFailures > 0 {
			log.Printf("lenny-gateway: watchdog forced %d sessions to failed: %v",
				res.ForcedFailures, res.PerReason)
		}
	})

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
}

// permissiveRegistry accepts every tenant. The minimal gateway uses
// this so dev-header transports can name an arbitrary tenant during
// integration tests without operator pre-provisioning. Production
// swaps in a Postgres-backed Registry (e.g., the in-memory
// tenantstore.Memory which also satisfies auth.TenantRegistry).
type permissiveRegistry struct{}

func (permissiveRegistry) IsRegistered(string) (bool, error) { return true, nil }

// tenantsLister adapts a tenantstore.Memory into a
// watchdog.TenantLister so the watchdog sweeps every registered
// tenant. In single-tenant deployments it also returns "default" so
// dev-mode sessions are bounded.
type tenantsLister struct {
	store *tenantstore.Memory
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
