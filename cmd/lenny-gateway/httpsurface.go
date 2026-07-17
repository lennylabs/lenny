// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/pkg/alerting/evaluator"
	"github.com/lennylabs/lenny/pkg/auth/introspection"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/driftmonitor"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/barrier"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialserver"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/revocation"
	revocationprop "github.com/lennylabs/lenny/pkg/gateway/credentials/revocation/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/openapi"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcpruntimes"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	cbmw "github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker"
	correlationmw "github.com/lennylabs/lenny/pkg/gateway/middleware/correlation"
	deprecationmw "github.com/lennylabs/lenny/pkg/gateway/middleware/deprecation"
	environmentmw "github.com/lennylabs/lenny/pkg/gateway/middleware/environment"
	idemmw "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency"
	idempgstore "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency/pgstore"
	ratelimitmw "github.com/lennylabs/lenny/pkg/gateway/middleware/ratelimit"
	recovermw "github.com/lennylabs/lenny/pkg/gateway/middleware/recover"
	"github.com/lennylabs/lenny/pkg/gateway/operability/health"
	"github.com/lennylabs/lenny/pkg/gateway/operability/health/backends"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/drainreadiness"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/prestop"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	gwadapter "github.com/lennylabs/lenny/pkg/gateway/runtime/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterregistry"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioncheckpointmeta"
	sessioncheckpointmetapg "github.com/lennylabs/lenny/pkg/gateway/session/sessioncheckpointmeta/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/storage/dualstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/failopen"
	"github.com/lennylabs/lenny/pkg/gateway/upgrade/runtimeupgradeguard"
	"github.com/lennylabs/lenny/pkg/gateway/upgrade/runtimeupgradestore"
	"github.com/lennylabs/lenny/pkg/storerouter"
	"github.com/lennylabs/lenny/pkg/tokensvcproxy"
)

func (w *gatewayWiring) buildHTTPSurface(
	gwMetrics *gatewaymetrics.Metrics,
	sessionSrv *sessionserver.Server,
	openaiHandler *translator.OpenAIChatHandler,
	responsesHandler *translator.OpenResponsesHandler,
	credServer *credentialserver.Server,
	mcpSrv *mcp.Server,
	policyChain *interceptor.Chain,
	auditSink admin.AuditSink,
	auditAppender policy.AuditAppender,
	opsEmitter events.EventEmitter,
	environments environmentstore.Store,
	driftMonitor *driftmonitor.Monitor,
	dsMonitor *dualstore.Monitor,
	failOpenReplicas *failopen.ReplicaCount,
	revCache *revocation.Cache,
	revProp *revocationprop.Propagator,
	ruStore runtimeupgradestore.Store,
	siemHealthChecker health.Checker,
	resolvedNoEnvPolicy string,
) {
	f := w.f
	addr := f.addr
	multiTenant := f.multiTenant
	tenantIDClaim := f.tenantIDClaim
	devMode := f.devMode
	jwksPublish := f.jwksPublish
	deprecatedAPIVersionsCSV := f.deprecatedAPIVersionsCSV
	adapterTLSCert := f.adapterTLSCert
	barrierAckTimeoutSeconds := f.barrierAckTimeoutSeconds
	idempotencyMaxBodyBytes := f.idempotencyMaxBodyBytes
	failOpenStatePath := f.failOpenStatePath
	tokenServiceHTTPURL := f.tokenServiceHTTPURL
	rlGlobalPerMin := f.rlGlobalPerMin
	rlPerUserPerMin := f.rlPerUserPerMin
	rlPerTenantPerMin := f.rlPerTenantPerMin
	rlFailOpenMaxSeconds := f.rlFailOpenMaxSeconds
	quotaUserFailOpenFraction := f.quotaUserFailOpenFraction
	quotaPerReplicaHardCap := f.quotaPerReplicaHardCap
	quotaFailOpenCumulativeMaxSeconds := f.quotaFailOpenCumulativeMaxSeconds
	playgroundEnabled := f.playgroundEnabled
	playgroundAuthMode := f.playgroundAuthMode
	playgroundDevTenantID := f.playgroundDevTenantID
	playgroundAllowedRuntimes := f.playgroundAllowedRuntimes
	playgroundSessionLabels := f.playgroundSessionLabels
	playgroundMaxSessionMinutes := f.playgroundMaxSessionMinutes
	playgroundMaxIdleTimeSeconds := f.playgroundMaxIdleTimeSeconds
	playgroundBearerTTL := f.playgroundBearerTTL
	playgroundGatewayHost := f.playgroundGatewayHost
	playgroundOIDCSessionTTL := f.playgroundOIDCSessionTTL
	mux := http.NewServeMux()
	mux.Handle("/v1/sessions", sessionSrv.Handler())
	mux.Handle("/v1/sessions/", sessionSrv.Handler())
	mux.Handle("/v1/blobs/", sessionSrv.Handler())
	// §9.1 / §15.1 runtime-discovery and model-list surfaces. The
	// sessionserver mux serves GET /v1/runtimes, /v1/runtimes/{name}/
	// meta/{key}, /v1/models, and the §5.1 internal meta-fetch path;
	// each is identity-filtered by §10.6 environment access.
	mux.Handle("/v1/runtimes", sessionSrv.Handler())
	mux.Handle("/v1/runtimes/", sessionSrv.Handler())
	mux.Handle("/v1/models", sessionSrv.Handler())
	mux.Handle("/internal/runtimes/", sessionSrv.Handler())
	mux.Handle("/v1/admin/", w.adminRouter.Handler())

	// §25.3 Platform Health API. Registered at the specific
	// /v1/admin/health* paths so Go's ServeMux routes them to the
	// health handler ahead of the /v1/admin/ admin catch-all.
	healthAgg := health.NewAggregator()
	// §25.3 lines 538-542: per-component probe-latency histogram and
	// status gauge, registered on the gateway's Prometheus registry.
	healthMetrics, err := health.NewMetrics(gwMetrics.Registerer())
	if err != nil {
		log.Fatalf("lenny-gateway: §25.3 health metrics: %v", err)
	}
	healthAgg.SetMetrics(healthMetrics)
	healthAgg.Register(staticHealthy("gateway"))
	healthAgg.Register(staticHealthy("sessionstore"))
	healthAgg.Register(staticHealthy("executor"))
	// spec: §25.3 line 441 / lines 527-528 — the MinIO/ArtifactStore probe
	// runs the §12.5 HeadBucket-equivalent liveness check (blobProbe is the
	// real bucket probe for self-managed MinIO and an always-ready stub for
	// in-memory / managed cloud object storage). The component name matches
	// the §25.3 Degradation section ("objectStore.status reports
	// unhealthy"), replacing the prior static stub.
	healthAgg.Register(backends.ObjectStore(w.blobProbe.Probe, "objectStore"))
	// spec: §25.3 line 441 — registered-connectors dependency probe. A
	// single List against the connector registry confirms the backing
	// store answers; the platform tenant scope is a reachability ping (the
	// query hits the same store regardless of tenant and an empty result
	// is healthy).
	healthAgg.Register(backends.Connectors(func(ctx context.Context) error {
		_, err := w.connectors.List(ctx, "platform", connectorstore.ListFilter{})
		return err
	}, "connectors"))
	// spec: §25.3 line 441 — Kubernetes API server (/healthz) dependency
	// probe, registered only on an agent-namespace deployment where the
	// cluster transport exists.
	if w.kubeHealthzProbe != nil {
		healthAgg.Register(backends.APIServer(w.kubeHealthzProbe, "kubernetes-api"))
	}
	// spec: §25.3 line 441 — cert-manager certificate-status probe over the
	// gateway's mounted mesh certificate. Registered only when a cert path
	// is configured (mTLS deployments); reports degraded inside the §16.5
	// CertExpiryImminent window and unhealthy once expired or unreadable.
	if *adapterTLSCert != "" {
		healthAgg.Register(backends.CertManager(backends.FileCertReader(*adapterTLSCert), "cert-manager"))
	}
	// When a backing service is wired, the §25.3 health API reports
	// its real reachability instead of a static verdict.
	//
	// readinessDeps lists the hard backend dependencies whose
	// unreachability the §10.4 /readyz probe gates on. Only Postgres
	// (the externalized session-truth store, §10.4 mechanism 1) is
	// included: Redis loss has the §12.4 advisory-lock lease fallback,
	// and the SIEM-delivery checker is §11.7-non-gating, so neither
	// should pull a replica out of the Service on its own. F-10.4.6.
	var readinessDeps []string
	if w.pgPool != nil {
		healthAgg.Register(backends.Postgres(w.pgPool, "postgres"))
		readinessDeps = append(readinessDeps, "postgres")
	}
	if w.redisClient != nil {
		healthAgg.Register(backends.Redis(w.redisClient, "redis"))
	}
	if w.breakerCache != nil {
		healthAgg.Register(backends.CircuitBreakerCache(w.breakerCache, "circuit-breaker-cache"))
	}
	// spec: §11.7 item 4 line 372 — the SIEM delivery health check feeds
	// the §25.3 health verdict. Registered only when a SIEM endpoint is
	// configured; a degraded verdict reports the external audit copy is
	// lagging while the durable Postgres chain stays intact. The gateway
	// deliberately does not gate /healthz or /readyz on this component so a
	// shared-SIEM outage cannot pull every replica out of the Service.
	// F-11.7.16.
	if siemHealthChecker != nil {
		healthAgg.Register(siemHealthChecker)
	}
	// §25.3: emit a health_status_changed operational event when the
	// aggregate health verdict transitions.
	healthAgg.OnTransition(func(prev, curr health.Status) {
		data, _ := json.Marshal(map[string]any{
			"oldStatus": string(prev), "newStatus": string(curr),
		})
		_ = opsEmitter.Emit(context.Background(), events.OperationalEvent{
			Source:          "/v1/admin/health",
			Type:            events.EventHealthStatusChanged.CloudEventsType(),
			Severity:        "warning",
			DataContentType: "application/json",
			Data:            data,
		})
	})
	// alertEvalPtr late-binds the §25.13 in-process alert tracker into the
	// pool health resolver below; it is stored once the tracker is
	// constructed further down in this function.
	var alertEvalPtr atomic.Pointer[evaluator.Evaluator]
	// spec: §25.17 line 5254 — the watchdog's recovery-verification call
	// `GET /v1/admin/health/{pool}` resolves a warm-pool name to its pool
	// health view (status + activeAlerts) when no health subsystem of that
	// name is registered. The §16.5 warm-pool alerts come from this
	// replica's in-process alert tracker, late-bound via alertEvalPtr
	// because the tracker is constructed further below; until it is set
	// (or when §25.13 in-process tracking is disabled) the firing set is
	// empty and the pool reports healthy from the pool store alone.
	poolHealthResolver := health.FuncPoolHealthResolver{
		Pool: func(ctx context.Context, name string) (string, time.Time, bool) {
			p, err := w.pools.Get(ctx, name)
			if err != nil {
				return "", time.Time{}, false
			}
			last := p.UpdatedAt
			if p.IsDraining() && p.DrainingSince.After(last) {
				last = p.DrainingSince
			}
			return p.Phase(), last, true
		},
		Firing: func() []string {
			e := alertEvalPtr.Load()
			if e == nil {
				return nil
			}
			return e.Firing()
		},
	}
	// spec: §25.3 lines 443-451 — derive each dependency/subsystem
	// component's /v1/admin/health verdict from the §16.5 alert catalogue:
	// a firing critical alert mapped to the component reports unhealthy, a
	// warning reports degraded. The firing set comes from this replica's
	// in-process tracker (late-bound via alertEvalPtr); the alert→component
	// mapping lives in pkg/alerting/rules so it shares the rule catalogue.
	healthAgg.SetAlertSource(alertHealthSource{eval: &alertEvalPtr})
	// spec: §25.13 — publish the late-bound alert-tracker pointer onto the
	// accumulator so the run loop (runServers) can Store the constructed
	// evaluator into it after the signal handler is installed.
	w.alertEvalPtr = &alertEvalPtr
	healthHandler := health.Handler(healthAgg, poolHealthResolver)
	mux.Handle("/v1/admin/health", healthHandler)
	mux.Handle("/v1/admin/health/", healthHandler)
	// spec: §15.1 line 589 — served `info.version` must reflect the
	// gateway's release version, not the embedded default.
	openapiHandler := openapi.HandlerWithVersion(buildVersion)
	mux.Handle("/openapi.yaml", openapiHandler)
	// spec: §15.1 line 589 — `/openapi.json` is the canonical
	// gateway-side JSON mount. spec: §15.1 (OpenAPI generation and
	// discovery) — `/v1/openapi.json` and `/v1/openapi.yaml` are the
	// admin-API discovery paths the §25.12 schema-discovery block and
	// the §25.4 `/me` `links.openApi` hop resolve against; both forms
	// are mounted so the YAML path is not a 404 against the gateway.
	// F-15.1.17 / F-COV-1.
	mux.Handle("/openapi.json", openapiHandler)
	mux.Handle("/v1/openapi.json", openapiHandler)
	mux.Handle("/v1/openapi.yaml", openapiHandler)
	// §4.3 line 193 canonical token endpoint. The gateway reverse-
	// proxies /v1/oauth/* to lenny-token-service so the Token Service
	// is the actual minter for every Lenny bearer token. When the
	// flag is empty (dev path without a Token Service binary), the
	// /v1/oauth surface is unmounted; callers receive 404 from the
	// gateway. spec: F-4.3.12.
	if *tokenServiceHTTPURL != "" {
		proxy, err := tokensvcproxy.New(*tokenServiceHTTPURL)
		if err != nil {
			log.Fatalf("lenny-gateway: §4.3 token-service reverse proxy: %v", err)
		}
		mux.Handle("/v1/oauth/", proxy.Handler())
		log.Printf("lenny-gateway: §4.3 /v1/oauth/* reverse-proxied to %s", *tokenServiceHTTPURL)
	}

	// ----- §10.3 JWK Set publication -----
	// The JWKS endpoint advertises the kid/alg of every key the
	// §10.3 RotatingVerifier holds (the current key plus every
	// retained previous key during the §10.3 24h overlap window).
	// The endpoint is unauthenticated by design; the JWK Set carries
	// only public metadata, never private key material. Suppress with
	// --jwks-publish=false.
	if *jwksPublish {
		jwksHandler := jwt.NewJWKSHandler(w.rotatingVerifier)
		mux.Handle("/.well-known/jwks.json", jwksHandler)
		// spec: §10.2 line 195 / F-10.2.14. The v1 signer is HMAC; the
		// published JWKS entries carry `kty: oct` with no `k` field, so
		// the document advertises kid/alg only. Log a notice when the
		// endpoint is mounted on top of an HMAC-only key set so
		// operators understand that JWKS verification of the actual
		// signature is not possible against the published document
		// (the secret must be obtained out of band).
		log.Printf("lenny-gateway: §10.3 JWKS published at /.well-known/jwks.json (current kid %s)",
			w.rotatingVerifier.CurrentKeyID())
		if !jwksAdvertisesAsymmetric(jwksHandler.Document()) {
			log.Printf("lenny-gateway: §10.2 line 195 / F-10.2.14: published JWKS contains only `kty: oct` entries (HMAC). The document advertises kid/alg only; verifiers cannot validate signatures against it.")
		}
	}
	// §15.0 ExternalAdapterRegistry. Each built-in adapter registers
	// through the registry and the registry mounts its HTTPHandler on
	// the shared mux. The §15.0 admin-API runtime-registration path
	// uses the same registry so a third-party adapter takes effect
	// without a gateway restart.
	adapterReg := adapterregistry.New()
	if err := adapterReg.Register(adapterregistry.NewSimpleAdapter(
		"openai-completions",
		openaiHandler.Handler(),
		gwadapter.Capabilities{PathPrefix: "/v1/chat/completions", Protocol: "openai-completions"},
	)); err != nil {
		log.Fatalf("lenny-gateway: §15.0 adapter registry: %v", err)
	}
	if err := adapterReg.Register(adapterregistry.NewSimpleAdapter(
		"open-responses",
		responsesHandler.Handler(),
		gwadapter.Capabilities{PathPrefix: "/v1/responses", Protocol: "open-responses"},
	)); err != nil {
		log.Fatalf("lenny-gateway: §15.0 adapter registry: %v", err)
	}
	// §15.2 line 1335: the MCP adapter is the platform's primary streaming
	// surface, so it overrides the BaseAdapter no-op OutboundCapabilities()
	// with the mandatory six-kind push declaration (MCPAdapter, not a plain
	// SimpleAdapter). F-15.2.8.
	if err := adapterReg.Register(adapterregistry.NewMCPAdapter(mcpSrv.Handler())); err != nil {
		log.Fatalf("lenny-gateway: §15.0 adapter registry: %v", err)
	}
	adapterReg.Mount(mux)
	// §4.1 long-lived interactive streams: the MCP WebSocket transport
	// is mounted alongside the POST /mcp single-shot handler so the
	// playground UI (pkg/gateway/playground/assets.go WSPath
	// /mcp/v1/ws) and any other MCP-over-WebSocket client land on the
	// same dispatch logic. The Streamable HTTP path remains on /mcp.
	mux.Handle("/mcp/v1/ws", mcpSrv.WebSocketHandler())
	// spec: §10.6 line 557 — the explicit-environment MCP surface. A
	// client opts into a named environment scope by speaking MCP to
	// /mcp/environments/{name}; the dispatch is identical to POST /mcp
	// with the environment attached to the request context so
	// session-creation and discovery default to that scope. F-10.6.11.
	mux.Handle("POST /mcp/environments/{name}", mcpSrv.EnvironmentHandler())
	// §4.1 dedicated MCP endpoints for type:mcp runtimes. The
	// dispatcher is nil in v1: every request that passes runtime
	// type validation surfaces RUNTIME_UNAVAILABLE per §15.2.1
	// while preserving the spec-required 404 / 400 error patterns
	// for unknown and non-mcp runtimes.
	// spec: §10.6 line 607 — the per-runtime MCP dispatcher gates a
	// tools/call scoped to an environment (`?environment=<name>`) by that
	// environment's mcpRuntimeFilters capability filter. F-10.6.2.
	mux.Handle("POST /mcp/runtimes/{name}", mcpruntimes.New(w.runtimes, nil).WithEnvironments(environments))
	mux.Handle("/v1/credentials", credServer.Handler())
	mux.Handle("/v1/credentials/", credServer.Handler())

	// ----- §27 web playground -----
	// The playground is feature-flag gated (§27.2). When
	// --playground-enabled is set the gateway serves the embedded SPA
	// bundle at /playground and the §27.3.1 auth gatekeepers; when
	// unset the /playground/* and /v1/playground/token routes are not
	// mounted at all and return 404. The playground is a client of the
	// public MCP and REST surface for session, chat, and discovery
	// traffic (§27.5); only the auth-gatekeeper endpoints are
	// playground-specific.
	//
	// §27.6 line 204 — the playground handler's authoritative per-request
	// revocation check is hoisted here so the auth middleware (built
	// below) can consult it for every origin=playground bearer. It stays
	// nil when the playground is disabled, leaving the auth hot path
	// unchanged. F-27.6.3 / F-27.3.1.
	var playgroundRevocations authmw.PlaygroundRevocationChecker
	if *playgroundEnabled {
		pgCfg := playground.Config{
			Enabled:            true,
			AuthMode:           playground.AuthMode(*playgroundAuthMode),
			DevTenantID:        *playgroundDevTenantID,
			AllowedRuntimes:    splitCSV(*playgroundAllowedRuntimes),
			MaxSessionMinutes:  *playgroundMaxSessionMinutes,
			MaxIdleTimeSeconds: *playgroundMaxIdleTimeSeconds,
			OIDCSessionTTL:     time.Duration(*playgroundOIDCSessionTTL) * time.Second,
			BearerTTL:          time.Duration(*playgroundBearerTTL) * time.Second,
			MultiTenant:        *multiTenant,
			GatewayHost:        *playgroundGatewayHost,
			SessionLabels:      parseKeyValueCSV(*playgroundSessionLabels),
		}
		// §27.3 dev mode is permitted only under global devMode; the
		// chart rejects authMode=dev otherwise, and this is the
		// backstop for a deployment that bypassed Helm-validate.
		if pgCfg.AuthMode == playground.AuthModeDev && !*devMode {
			log.Fatalf("lenny-gateway: LENNY_PLAYGROUND_DEV_MODE_FORBIDDEN: playground.authMode=dev requires global.devMode=true (§27.3)")
		}
		// §27.2 layer-3 startup gate: a malformed playground config is
		// fatal (the schema and preflight layers are the primary
		// defenses; this is defense-in-depth).
		if err := pgCfg.Validate(); err != nil {
			log.Fatalf("lenny-gateway: %v", err)
		}
		// §27.8 playground metrics register against the same private
		// registry the gateway's /metrics scrape target serves. Created
		// before the session store so the Redis-backed store can record
		// §27.8 propagation samples from its subscribe loop. F-27.6.6.
		pgMetrics, err := playground.NewMetrics(gwMetrics.Registerer())
		if err != nil {
			log.Fatalf("lenny-gateway: §27.8 playground metrics: %v", err)
		}
		// §27.3.1 session record + revocation backing store: Redis when
		// --redis-url is set so a logout on one replica revokes the
		// bearer fleet-wide, in-process otherwise (single-replica).
		var pgSessions playground.SessionStore
		if w.redisClient != nil {
			redisSessions := playground.NewRedisSessionStore(w.concernRedis.For(storerouter.RedisConcernCachePubSub)).WithMetrics(pgMetrics)
			pgSessions = redisSessions
			// §27.3.1 / §27.6 line 204 pub/sub: a single PSUBSCRIBE on
			// t:*:pg:revocations subscribes this replica to every tenant's
			// revocation channel — including tenants provisioned after
			// gateway start — so the auth hot path can short-circuit the
			// Redis GET on a cache hit and the §27.8 propagation histogram
			// captures cross-replica visibility for all tenants. F-27.6.6,
			// F-27.6.7.
			go redisSessions.SubscribeAllRevocations(context.Background())
		} else {
			pgSessions = playground.NewMemorySessionStore()
			// spec: §27.6 line 204 — Logout endpoints MUST NOT return
			// 200 to the browser until the revocation writes have
			// committed to Redis. The in-memory store has no cross-
			// replica fan-out and loses every revocation marker on
			// restart, so a bearer minted before restart and not yet
			// expired becomes honourable again. Surface the durability
			// gap at startup so a single-replica dev/embedded operator
			// knows the §27.6 commit-before-200 guarantee is unbacked.
			// F-27.6.9.
			log.Printf("lenny-gateway: §27.6 playground SessionStore is in-memory (no --redis-url): bearer revocations are not durable across gateway restarts and do not propagate across replicas; production deployments MUST set --redis-url")
		}
		pg := playground.New(pgCfg, playground.Options{
			// spec: §10.2 line 225 — Sign goes through the breaker so
			// KMS outages convert to retryable KMS_SIGNING_UNAVAILABLE;
			// Verify uses the inner signer (verification is local memory
			// and never reaches KMS). F-10.2.6.
			Signer:   w.jwtSigner,
			Verifier: w.kmsBackedSigner,
			Tenants:  playgroundTenantRegistry{store: w.tenants},
			Sessions: pgSessions,
			Metrics:  pgMetrics,
		}).WithAuditEmitter(playgroundAuditEmitter{sink: auditSink})
		// spec: §27.6 lines 200-201 — wire the playground idle/duration caps
		// into the session-creation path so an origin=playground session is
		// bounded by min(runtime, playground) on both axes, and record the
		// §27.8 lenny_playground_sessions_created_total counter once the claim
		// is read. pg.Config() is the §27.2-normalized config (defaults
		// applied), so its EffectiveIdleSeconds / EffectiveSessionMinutes carry
		// the resolved caps. F-27.3.3 / F-27.6.1 / F-27.6.2 / F-27.6.11.
		sessionSrv.SetPlaygroundCaps(pg.Config(), pgMetrics.SessionCreated)
		// §27.6 line 204 — expose the per-request revocation check to the
		// auth middleware so an origin=playground bearer is rejected on
		// every replica once its session is revoked. F-27.6.3 / F-27.3.1.
		playgroundRevocations = pg
		// §27.5.4 — wire the same revocation check into the MCP WebSocket
		// transport so an origin=playground bearer revoked mid-stream
		// (logout / idle / admin / user.invalidated) closes the in-flight
		// connection with WebSocket code 4401. The principal is read from
		// the auth-middleware context the upgrade request already carries;
		// non-playground bearers (no origin claim) are not watched.
		// F-27.5.4.
		mcpSrv.SetWebSocketAuth(func(r *http.Request) (mcp.WSPrincipal, bool) {
			p, ok := authmw.FromContext(r.Context())
			if !ok {
				return mcp.WSPrincipal{}, false
			}
			return mcp.WSPrincipal{Tenant: p.TenantID, JTI: p.JTI, Origin: p.Origin}, true
		}, pg, 0)
		// §11.4 / §27.6 line 204 — drive the §11.4 user-invalidation
		// fan-out into the playground revocation primitive so an OIDC
		// principal invalidation revokes the user's playground sessions.
		// adminRouter is a pointer the mounted handlers read at request
		// time, so wiring it here (after the admin mux is built) takes
		// effect on the next invalidate call. F-27.6.4 / F-27.3.2.
		w.adminRouter = w.adminRouter.WithPlaygroundRevocation(pg)
		mux.Handle("/playground", pg.PlaygroundRoutes())
		mux.Handle("/playground/", pg.PlaygroundRoutes())
		mux.Handle("/v1/playground/token", pg.TokenRoutes())
		// spec: §27.3.1 line 94 / §27.6 line 201/204 — drive the
		// playground-auth-record idle-timeout sweep so an abandoned OIDC
		// cookie-to-bearer session is reclaimed through the same revocation
		// primitive (DEL record + SET pg:revoked + PUBLISH) as logout and
		// user.invalidation, with reason idle_timeout. This is the missing
		// caller for the RevokeIdleTimeout reason; the agent-session idle
		// reaper (§6.2 watchdog) operates over a different store and does not
		// reclaim these records. F-27.3.7.
		go pg.RunIdleSweeper(context.Background(), 0)
		log.Printf("lenny-gateway: §27 web playground served at /playground (authMode=%s)", pgCfg.AuthMode)
	}

	// ----- §16.1 Prometheus metrics -----
	mux.Handle("GET /metrics", gwMetrics.Handler())

	// ----- Healthz (unauthenticated) -----
	// spec: §13.3 line 595 — when this replica's NTP drift exceeds the
	// 5s ceiling, /healthz reports degraded (503). Kubernetes responds
	// by removing the pod from the Service endpoints, so traffic stops
	// reaching a replica whose clock cannot be trusted for `exp`
	// validation. F-13.3.5.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if driftMonitor.Degraded() {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("clock_drift_exceeded\n"))
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	// ----- §12.5 drain-readiness endpoint (unauthenticated) -----
	// The lenny-drain-readiness webhook probes this before admitting a
	// node-drain pod eviction. blobProbe runs a real MinIO bucket check
	// when the artifact store is MinIO-backed, and an always-ready stub
	// for the process-local in-memory store.
	mux.Handle("GET /internal/drain-readiness", &drainreadiness.Handler{Prober: w.blobProbe})

	// ----- §10.5 runtime-upgrade-active endpoint (unauthenticated) -----
	// The lenny-sandboxtemplate-deletion-guard webhook probes this before
	// admitting a SandboxTemplate DELETE, so the old template cannot be
	// deleted while a RuntimeUpgrade referencing its pool is still active
	// (§10.5 line 508). The same record's schemaGated flag gates a Phase 3
	// migration for the pool (§10.5 line 502). Same internal-port
	// NetworkPolicy scope and unauthenticated posture as drain-readiness.
	mux.Handle("GET /internal/runtime-upgrade/active", &runtimeupgradeguard.Handler{Store: ruStore})

	// ----- §12.5 line 291 node.drain.forced audit endpoint (unauthenticated) -----
	// The webhook POSTs here on a drain-force override admission so the
	// §16.7 audit event lands in the per-tenant §11.7 hash chain. The
	// endpoint sits on the gateway's internal port (same NetworkPolicy
	// scope as drain-readiness) and is unauthenticated at the HTTP layer
	// because the webhook's egress NetworkPolicy is the only path that
	// can reach it.
	if auditAppender != nil {
		mux.Handle("POST /internal/audit/node-drain-forced", &drainreadiness.ForcedDrainHandler{
			Appender:       auditAppender,
			Metrics:        gwMetrics,
			PlatformTenant: "platform",
		})
	}

	// ----- §4.4 / §10.1 preStop drain hook (unauthenticated) -----
	// Kubernetes invokes this via lifecycle.preStop.httpGet when a
	// gateway pod is scheduled for termination. The hook reads the
	// terminationGracePeriodSeconds budget, selects the §10.1 tiered
	// checkpoint cap per coordinated session, and triggers a
	// synchronous eviction checkpoint per session. Each cap selection
	// bumps lenny_prestop_cap_selection_total labeled by source so
	// the §16.5 PreStopCapFallbackRateHigh alert can fire.
	//
	// spec: §4.4 line 234, 263 / §10.1 preStop staged drain.
	var prestopCheckpointer prestop.CheckpointTrigger
	if w.checkpointSvc != nil {
		prestopCheckpointer = w.checkpointSvc
	}
	// §10.1 lines 163-181 — the preStop CheckpointBarrier coordinator. At
	// Stage 1 it sends the barrier to every session this replica
	// coordinates (the barrier-target set from the coordination_lease
	// mirror, falling back to the in-memory registry), quiescing in-flight
	// tool calls and flushing a best-effort checkpoint under the §11.3
	// line 210 checkpointBarrierAckTimeoutSeconds budget. The barrier
	// reaches each pod through the live adapter connection the registry
	// already holds for a coordinated session. F-10.1.19 / F-11.3.15.
	var barrierDispatch prestop.BarrierDispatcher
	if w.podRegistry != nil {
		var checkpointMeta sessioncheckpointmeta.Store
		if w.pgPool != nil {
			checkpointMeta = sessioncheckpointmetapg.New(w.pgPool, nil)
		} else {
			checkpointMeta = sessioncheckpointmeta.NewMemoryStore(nil)
		}
		barrierLister := &barrier.MirrorTargetLister{
			ReplicaID: w.replica,
			Mirror:    w.coordMirror,
			Fallback: func() []barrier.Target {
				bindings := w.podRegistry.Snapshot()
				out := make([]barrier.Target, 0, len(bindings))
				for _, b := range bindings {
					gen := int64(0)
					if row, err := w.sessions.Get(context.Background(), b.TenantID, b.SessionID); err == nil {
						gen = row.CoordinationGeneration
					}
					out = append(out, barrier.Target{
						TenantID:               b.TenantID,
						SessionID:              b.SessionID,
						CoordinationGeneration: gen,
					})
				}
				return out
			},
		}
		barrierDisp := &barrier.PodDispatcher{
			Conn: func(sessionID string) (*adapterclient.Client, bool) {
				b, ok := w.podRegistry.Get(sessionID)
				if !ok || b.Adapter == nil {
					return nil, false
				}
				return b.Adapter, true
			},
		}
		// §10.1 line 169 — hand the barrier the in-process checkpointer so
		// it drives the gateway-side Checkpoint stream (with
		// checkpoint.TriggerEviction) against each quiesced pod concurrently
		// with the CheckpointBarrier RPC. A nil checkpointSvc (no store
		// wired) leaves the barrier firing without a gateway-driven
		// checkpoint.
		var barrierCheckpointer barrier.Checkpointer
		if w.checkpointSvc != nil {
			barrierCheckpointer = w.checkpointSvc
		}
		coord := barrier.New(barrierLister, barrierDisp, checkpointMeta, gwMetrics, barrierCheckpointer)
		barrierDispatch = barrierCoordinatorDispatch{coord}
	}
	prestopHook := &prestop.Hook{
		Sessions: &prestop.RegistryEnumerator{
			Registry:    w.podRegistry,
			Sessions:    w.sessions,
			DefaultPool: "default",
		},
		Checkpoint:        prestop.CheckpointFnFor(prestopCheckpointer),
		Metrics:           gwMetrics,
		ServiceInstanceID: w.replica,
		GracePeriod:       parseTerminationGrace(),
		Barrier:           barrierDispatch,
		BarrierAckTimeout: time.Duration(*barrierAckTimeoutSeconds) * time.Second,
		Logf:              func(format string, args ...any) { log.Printf(format, args...) },
	}
	mux.Handle("POST /internal/prestop", prestopHook)
	mux.Handle("GET /internal/prestop", prestopHook)

	// ----- Readiness (unauthenticated) -----
	// spec: §10.1 — the preStop staged drain's Stage 1 is a readiness
	// flip: once the preStop hook fires, /readyz reports 503 so the
	// Endpoints controller removes this pod from the Service and the
	// load balancer stops routing new requests *before* the
	// eviction-checkpoint drain begins. Liveness stays on /healthz so a
	// draining pod is not also killed by the kubelet mid-drain. The
	// readiness probe also fails on NTP drift so a clock-untrustworthy
	// replica is removed from the endpoints (the §13.3.5 behaviour that
	// previously rode on /healthz serving double duty). F-10.1.6.
	//
	// spec: §10.4 line 386 — readiness additionally reflects the hard
	// backend dependency (Postgres session truth) so a replica whose
	// store connection is broken is removed from traffic rather than
	// serving until the process crashes. The dual-store-both-down case
	// is exempted so the replica stays ready to answer the §10.1 503
	// PLATFORM_DEGRADED. F-10.4.6.
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		dualStoreDown := dsMonitor != nil && dsMonitor.Unavailable()
		probe := func(ctx context.Context) health.Status {
			return healthAgg.HardDependencyStatus(ctx, readinessDeps...)
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		res := readinessVerdict(ctx, prestopHook.Draining(), driftMonitor.Degraded(), dualStoreDown, probe)
		w.WriteHeader(res.Code)
		if res.Body != "" {
			_, _ = w.Write([]byte(res.Body))
		}
	})

	// ----- Middleware stack -----
	var handler http.Handler = mux

	// The §11.2 per-tenant concurrent-session quota is enforced inside
	// the session-creation handlers (sessionserver.requireSessionQuota)
	// against each tenant's configured MaxConcurrentSessions.

	// Idempotency next (after auth + circuit; needs the
	// authenticated tenant on the request to scope keys correctly).
	// The §11.5 key cache is durable under --postgres-dsn so an
	// idempotent retry replays across gateway replicas and restarts.
	// AllowedPaths restricts the cache to the §11.5-listed critical
	// operations so a stray Idempotency-Key on an unrelated route
	// cannot trap a non-mutating response for 24 hours. spec: §11.5
	// line 268; F-11.5.7.
	var idemStore idemmw.Store = idemmw.NewMemoryStore()
	if w.pgPool != nil {
		idemStore = idempgstore.New(w.pgPool)
	}
	// spec: §11.5 line 277 — when an MCP client supplies idempotencyKey
	// inside a tool's arguments (lenny/create_session,
	// lenny/delegate_task), the MCP server runs the call through the
	// same §11.5 Store with per-tool key namespacing so retries collapse
	// to one execution. The Tools allow-list keeps the field opt-in per
	// tool; the TenantFromRequest resolver mirrors the REST middleware's
	// auth-set header. spec: F-11.5.1, F-11.5.6.
	mcpSrv.SetIdempotency(mcp.IdempotencyConfig{
		Store: idemStore,
		TenantFromRequest: func(r *http.Request) string {
			return r.Header.Get("X-Lenny-Tenant-ID")
		},
		Tools: map[string]bool{
			"lenny/create_session": true,
			"lenny/delegate_task":  true,
		},
		Now: clockinject.Now,
	})
	handler = idemmw.Wrap(handler, idemStore, idemmw.Options{
		Metrics: gwMetrics,
		// spec: §11.5 line 277 — the middleware buffers the request body
		// to hash and replay it. The default 8 MiB cap covers the
		// critical-operation payloads and is operator-tunable via the
		// flag for deployments that carry larger bodies (e.g. resume
		// with a verbose TaskResult). F-11.5.8.
		MaxBodyBytes: *idempotencyMaxBodyBytes,
		// spec: §11.5 line 268 — the six "critical operations"
		// (CreateSession, FinalizeWorkspace, StartSession, SpawnChild,
		// Approve/DenyDelegation, Resume) bound the §11.5 cache. The
		// middleware also defaults to POST-only (Options.AllowedMethods),
		// so a misplaced Idempotency-Key on a GET or unrelated POST
		// passes through without being trapped for 24 hours. F-11.5.7.
		AllowedPaths: []string{
			"/v1/sessions",                     // CreateSession
			"/v1/sessions/start",               // create-and-start
			"/v1/environments/{name}/sessions", // environment-scoped CreateSession
			"/v1/sessions/{id}/finalize",       // FinalizeWorkspace
			"/v1/sessions/{id}/start",          // StartSession
			"/v1/sessions/{id}/resume",         // Resume
			"/v1/sessions/{id}/derive",         // fork (Resume-adjacent)
			"/v1/sessions/{id}/tool-use/...",   // Approve/DenyDelegation tree
			"/mcp",                             // SpawnChild via JSON-RPC POST
		},
	})

	// Circuit breaker next: rejects requests when any open breaker
	// matches. The shared breakerstore.Memory satisfies cbmw.Registry
	// so the admin /v1/admin/circuit-breakers endpoints share state
	// with the request-path middleware.
	// spec: §11.6 line 327 / §16.7 — on a breaker match the gate emits
	// the `admission.circuit_breaker_rejected` audit row carrying the
	// authenticated caller identity (resolved by the auth middleware,
	// which wraps outside this gate), sampled per-(tenant, circuit,
	// caller) over a 10s window per replica.
	cbAudit := cbmw.NewAuditReporter(auditAppender, gwMetrics, w.replica, nil)
	cbOpts := cbmw.Options{
		Audit: cbAudit,
		Snapshot: func(r *http.Request) cbmw.RejectionSnapshot {
			snap := cbmw.RejectionSnapshot{}
			if p, ok := authmw.FromContext(r.Context()); ok {
				snap.CallerSub = p.Subject
				snap.CallerTenantID = p.TenantID
				snap.SessionID = p.SessionID
			}
			return snap
		},
	}
	// spec: §16.7 line 679 / §11.6 — when the breaker registry is the
	// Redis-polling cache, surface its age so the gate emits the sampled
	// `admission.circuit_breaker_cache_stale` audit event (and the
	// stale-serve counter) for any decision served against a cache that
	// has not refreshed within the 5-second budget. The in-memory store
	// (no Redis) never goes stale, so CacheAge stays nil there.
	if w.breakerCache != nil {
		bc := w.breakerCache
		cbOpts.CacheAge = func() time.Duration { return time.Since(bc.LastRefresh()) }
	}
	handler = cbmw.Wrap(handler, w.breakers, cbOpts)

	// spec: §12.4 lines 220-224 — the per-replica fail-open controller. The
	// per-user fraction is validated config-time (a value outside (0, 1.0]
	// fails the process fast per §12.4 line 222); the cumulative timer, the
	// per-user / per-tenant emergency backstop, and the cached replica count
	// are assembled here and consulted by the ratelimit middleware while
	// failing open on a Redis outage. F-12.4.9 / F-11.2.6.
	if err := failopen.ValidateUserFraction(*quotaUserFailOpenFraction); err != nil {
		log.Fatalf("lenny-gateway: %v", err)
	}
	gwMetrics.SetQuotaUserFailopenFraction(*quotaUserFailOpenFraction)
	if failopen.UserFractionWeakened(*quotaUserFailOpenFraction) {
		// spec: §12.4 line 222 — QuotaFailOpenUserFractionInoperative: a
		// fraction >= 0.5 substantially weakens the monopolization control.
		log.Printf("lenny-gateway: WARNING QuotaFailOpenUserFractionInoperative — quotaUserFailOpenFraction=%v >= 0.5 weakens the per-user fail-open cap; acknowledge the posture in the deployment answer file",
			*quotaUserFailOpenFraction)
	}
	failOpenController := buildFailOpenController(failOpenWiring{
		metrics:              gwMetrics,
		replicas:             failOpenReplicas,
		appender:             auditAppender,
		statePath:            *failOpenStatePath,
		cumulativeMaxSeconds: *quotaFailOpenCumulativeMaxSeconds,
		perReplicaHardCap:    *quotaPerReplicaHardCap,
		userFraction:         *quotaUserFailOpenFraction,
		serviceInstanceID:    w.replica,
	})

	// §11.1 rate limiting next — runs just after auth so the per-user
	// scope sees the authenticated principal. Limits default to zero
	// (disabled); operators set them via the rate-limit flags. Metrics
	// wires the §11.1 line 7 rejection counter and the §16.5
	// RateLimitDegraded fail-open gauge.
	handler = ratelimitmw.Wrap(handler, ratelimitmw.Options{
		Counter:          w.rateLimiter,
		GlobalPerMinute:  *rlGlobalPerMin,
		PerUserPerMinute: *rlPerUserPerMin,
		// spec: §13.3 line 607 / §11.1 — per-tenant fair-share brake.
		// F-11.1.8.
		PerTenantPerMinute: *rlPerTenantPerMin,
		Metrics:            gwMetrics,
		// spec: §11.3 line 222 / §12.4 line 220 — operator-tunable cap on
		// the fail-open episode. F-11.3.22.
		FailOpenMax: time.Duration(*rlFailOpenMaxSeconds) * time.Second,
		// spec: §12.4 lines 220-224 — per-replica fail-open emergency
		// ceilings + cumulative timer. F-12.4.9 / F-11.2.6.
		FailOpen: failOpenController,
	})

	// §10.6 transparent-filtering environment resolver — runs
	// immediately after auth so the caller's identity and OIDC groups
	// are resolved. It lists the caller tenant's environments, resolves
	// the noEnvironmentPolicy (per-tenant value over the platform-wide
	// resolvedNoEnvPolicy), and attaches a Resolution to the request
	// context. The §9.1 runtime-discovery surfaces and the
	// session-creation path read that Resolution and filter through it,
	// so a caller sees only the runtimes its environments authorize
	// without naming an environment. The resolver is a no-op when the
	// environment or tenant registry is not wired.
	handler = environmentmw.Wrap(handler, environmentmw.Options{
		Environments:               environments,
		Tenants:                    w.tenants,
		DefaultNoEnvironmentPolicy: resolvedNoEnvPolicy,
	})

	// Auth next-to-outermost. AllowDevRoles is only honoured when the
	// dev flag is set (LENNY_DEV_MODE=true or --dev-mode); production
	// deployments leave it off so X-Lenny-Roles cannot self-grant
	// platform-admin.
	//
	// The §10.2 Bearer path is verified with the bearer verifier built
	// above: the in-process Token Service signer, so a token minted by
	// POST /v1/oauth/token round-trips through the gateway, plus the
	// embedded OIDC provider's key when --bearer-trust-hmac-key-file is
	// set under §17.4 Embedded Mode. Production swaps in the OIDC JWKS
	// verifier.
	authOpts := authmw.Options{
		MultiTenant: *multiTenant,
		// spec: §10.2 line 212 — operator-configurable OIDC tenant claim
		// name (`auth.tenantIdClaim` Helm value). F-10.2.9.
		TenantClaimName: *tenantIDClaim,
		AllowDevHeaders: true,
		AllowDevRoles:   *devMode,
		Verifier:        w.bearerVerifier,
		Revocations:     revProp,
		// §27.6 line 204 / §27.3.1 lines 95-97 — authoritative per-request
		// revocation check for origin=playground bearers; nil (and thus a
		// no-op) when the playground is disabled. F-27.6.3 / F-27.3.1.
		PlaygroundRevocations: playgroundRevocations,
		// §4.2 line 185: every tenant-claim rejection writes an
		// auth_failure audit row alongside the INFO log line.
		AuthFailureSink: authFailureAuditAdapter{sink: auditSink},
		// §4.8 line 1046: run the PreAuth chain (AuthEvaluator) after the
		// principal resolves and before the request reaches the handler.
		Interceptors: policyChain,
		// spec: §10.2 line 294 — platform-managed user→role mapping
		// overrides OIDC-derived roles. The userstore-backed adapter
		// returns the user's stored Roles when the row exists; missing
		// rows fall through to the JWT claim. F-10.2.3.
		PlatformRoles: userstorePlatformRoles{store: w.users},
		// spec: §10.6 line 661 — real-time group check. The introspection
		// verifier reads each tenant's identityProvider record; a tenant
		// that leaves introspectionEnabled off pays nothing beyond a cached
		// config read and keeps its JWT groups. F-10.6.8.
		GroupIntrospector: introspection.New(tenantIntrospectionConfig{store: w.tenants}),
	}
	// §13.3 line 601 — fail-closed token validation. Only when Postgres
	// backs the revocation rehydration (below) is the staleness gate
	// meaningful: a replica that cannot reach Postgres for longer than the
	// freshness window refuses to validate (503 token_validation_unavailable)
	// rather than honor a possibly-revoked token from its stale cache. The
	// in-memory dev path leaves it nil so a no-Postgres deployment does not
	// fail closed. F-13.3.4.
	if w.pgPool != nil {
		authOpts.RevocationFreshness = revCache
	}
	if !*multiTenant {
		// Even in single-tenant mode, dev-header callers carry the
		// tenant header. Flip to multi-tenant with a permissive
		// registry so the header round-trips.
		authOpts.MultiTenant = true
		authOpts.Registry = permissiveRegistry{}
	} else {
		// spec: §10.2 lines 219-221 — in multi-tenant mode, an
		// unregistered `tenant_id` claim is a hard 403 TENANT_NOT_FOUND.
		// The bearer chain consults the real tenantstore so unprovisioned
		// callers cannot ride a signature-valid token into the platform.
		// F-10.2.1.
		authOpts.Registry = bearerTenantRegistry{store: w.tenants}
	}
	handler = authmw.Wrap(handler, authOpts)

	// §16.1 request metrics, wrapped before recovery so panics still
	// surface in the request_total / request_duration histograms with
	// the resulting 500 status. The route label collapses
	// high-cardinality path segments to a stable template.
	handler = gwMetrics.Middleware(handler, routeTemplate)

	// spec: §15.5 item 1 + docs/api/index.md line 124 — stamp the
	// `X-Lenny-Deprecated-Version` response header onto every response
	// served under a deprecated URL version prefix. The wrapper is a
	// no-op when --deprecated-api-versions is empty, which is the v1
	// default. Inserted ahead of the recovery wrapper so the header
	// also rides the 500 surface emitted when an inner handler
	// panics. F-15.5.11.
	handler = deprecationmw.Wrap(handler, splitCSV(*deprecatedAPIVersionsCSV)...)

	// spec: §10.4 line 377 — handler-goroutine panic must surface as
	// an explicit 500 response and a structured log line rather than
	// the net/http default of silent recover + truncated response. The
	// recovery wrapper is the outermost middleware so it catches a
	// panic from any inner handler (auth, rate limit, business
	// handlers, SSE writers). F-10.4.9.
	handler = recovermw.Middleware(handler)

	// spec: §16.4 lines 371-372 — outermost middleware. It reads the inbound
	// correlation headers (X-Lenny-Operation-ID, X-Lenny-Agent-Name,
	// traceparent, X-Lenny-Session-ID, X-Lenny-Tenant-ID) and attaches them
	// to the request context so the §16.4 logging handler projects them onto
	// every downstream log line, then emits one structured request-completion
	// line per request carrying those fields. Probe endpoints are skipped so
	// liveness/readiness/metrics scrapes do not flood the access log.
	// F-16.4.2, F-16.4.3.
	handler = correlationmw.Wrap(handler, correlationmw.Options{
		SkipPaths: map[string]bool{
			"/healthz":                  true,
			"/readyz":                   true,
			"/metrics":                  true,
			"/internal/drain-readiness": true,
		},
	})

	// spec: §27.3.1 line 142 — outermost wrapper. The §27.5 MCP WebSocket
	// bearer carrier promotes `Sec-WebSocket-Protocol: lenny.bearer.<token>`
	// (the browser fallback for upgrades that cannot set an Authorization
	// header) to a standard `Authorization: Bearer` header and strips the
	// credential entry from the sub-protocol header. It runs ahead of the
	// correlation/access-log and auth middleware so the bearer is never
	// logged or emitted in audit traces and is validated on the standard
	// bearer path. It is a no-op for every request without a
	// `lenny.bearer.` carrier. F-27.3.4 / F-27.5.2.
	handler = mcp.WebSocketBearerCarrier(handler)

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	// spec: §4.1 — record the composed REST mux and its wrapped HTTP server
	// on the accumulator. The §13.4 stateless-routing wiring mutates w.mux
	// in place later; the run loop serves w.httpSrv.
	w.mux = mux
	w.httpSrv = httpSrv
}

// startLeaderElectedSweeps launches the §12.5 gateway-singleton sweeps
// (artifact GC, audit-retention pruner, event-store partition maintenance,
// EventBus retranscribe, tombstone hard-prune, legal-hold reconciler, and
// the T4 KMS probe) under a single leader-elected lenny-gateway-leader
// Lease so exactly one replica is the GC writer at a time. It is an
// extracted per-group step of the §4.1 background-worker stage.
//
// spec: §4.1 gateway background subsystems; §12.5 gateway-leader election.
