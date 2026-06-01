// SPDX-License-Identifier: MIT

// Command lenny-token-service is the §13.3 Token Service. It serves
// two surfaces:
//
//   - HTTP `POST /v1/oauth/token` (RFC 8693 token exchange) for the
//     external dialect-issuing path. The gateway reverse-proxies
//     `/v1/oauth/*` here so the Token Service is the actual minter
//     for every Lenny bearer token.
//   - gRPC `lenny.tokenservice.v1.TokenService` for the §4.3 / §12.2.4
//     credential-assignment trust boundary the gateway calls over mTLS
//     to materialize, rotate, and revoke credential leases.
//
// Both surfaces sign with the §4 KMS-envelope-backed signer: the HMAC-
// SHA256 signing key is sealed under a KMS key-encryption-key rather
// than being a plaintext per-process secret. The KMS provider is
// pluggable: --kms-provider selects local | aws | gcp | azure; local
// is the no-cloud development KEK and is rejected when
// --environment=prod.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/lennylabs/lenny/pkg/audit"
	pkgaudit "github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/driftmonitor"
	"github.com/lennylabs/lenny/pkg/gateway/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/gateway/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/gateway/policy"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/providerflags"
	"github.com/lennylabs/lenny/pkg/observability/logging"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
	"github.com/lennylabs/lenny/pkg/redisconn"
	"github.com/lennylabs/lenny/pkg/spiffeid"
	"github.com/lennylabs/lenny/pkg/storerouter"
	"github.com/lennylabs/lenny/pkg/tokenservice"
	tokencache "github.com/lennylabs/lenny/pkg/tokenservice/cache"
	"github.com/lennylabs/lenny/pkg/tokenservice/promemit"
	"github.com/lennylabs/lenny/pkg/tokenservice/secretprobe"
)

func main() {
	// spec: §16.4 lines 370-372 — structured JSON logs from the token
	// service; routes the stdlib log package through the §16.4 handler
	// (component=token-service). F-16.4.1.
	logging.Setup(os.Stderr, "token-service")

	addr := flag.String("addr", ":8081", "address to bind for the HTTP token-exchange surface (host:port)")
	grpcAddr := flag.String("grpc-addr", "",
		"address to bind for the §4.3 gRPC TokenService surface (host:port). Empty disables the gRPC listener.")
	metricsAddr := flag.String("metrics-addr", "",
		"address to bind for the §16.1 Prometheus metrics surface (host:port). Empty disables the metrics listener. The chart renders :9090 to match tokenService.metricsPort.")
	issuer := flag.String("issuer", "https://lenny.dev.local/token",
		"iss claim stamped on issued tokens. Override via LENNY_TOKEN_ISSUER.")
	if envIssuer := os.Getenv("LENNY_TOKEN_ISSUER"); envIssuer != "" {
		*issuer = envIssuer
	}
	postgresDSN := flag.String("postgres-dsn", os.Getenv("LENNY_POSTGRES_DSN"),
		"Postgres connection string. When set, the §13.3 write-before-issue path persists issued-token metadata to the issued_tokens table and writes `token.exchanged` audit rows to audit_log under the §11.7 per-tenant advisory lock. When empty, the Token Service runs without durable issued-token or audit state (dev mode).")
	redisURL := flag.String("redis-url", os.Getenv("LENNY_REDIS_URL"),
		"Redis connection URL for the §25.5 operational event stream. When set, the Token "+
			"Service's EventEmitter writes to ops:events:stream alongside the gateway and the "+
			"controllers; when empty, events stay in the local in-memory buffer. Override via "+
			"LENNY_REDIS_URL.")
	redisPassword := flag.String("redis-password", os.Getenv("LENNY_REDIS_PASSWORD"),
		"Redis AUTH password. Override via LENNY_REDIS_PASSWORD.")
	// §13.3 line 607 rate limits on POST /v1/oauth/token. The
	// defaults transcribe the §13.3 line 607 normative limits.
	rlCallerPerSec := flag.Int("oauth-rate-limit-caller-per-second", envInt("LENNY_OAUTH_RL_CALLER_PER_SECOND", 10),
		"§13.3 line 607 per-(tenant_id, sub) per-second cap on /v1/oauth/token. Zero disables the tier.")
	rlCallerPerMin := flag.Int("oauth-rate-limit-caller-per-minute", envInt("LENNY_OAUTH_RL_CALLER_PER_MINUTE", 300),
		"§13.3 line 607 per-(tenant_id, sub) per-minute cap on /v1/oauth/token. Zero disables the tier.")
	rlTenantPerSec := flag.Int("oauth-rate-limit-tenant-per-second", envInt("LENNY_OAUTH_RL_TENANT_PER_SECOND", 100),
		"§13.3 line 607 per-tenant per-second cap on /v1/oauth/token. Zero disables the tier.")
	rlSampleWindow := flag.Duration("oauth-rate-limit-sample-window", envDuration("LENNY_OAUTH_RL_SAMPLE_WINDOW", 10*time.Second),
		"§13.3 line 611 rolling-window for sampled audit emission on rate-limited requests (one audit row per (tenant_id, sub, limit_tier) per window per replica).")
	// §4.3 / §10.3 mTLS material. The Token Service requires the
	// gateway to present a client certificate signed by the same CA
	// the chart's mtls-pki.yaml mints. spec: §4.3 line 195 "Gateway
	// replicas call the Token Service over mTLS"; §13.2 line 217
	// Token Service ingress row.
	tlsCert := flag.String("tls-cert", os.Getenv("LENNY_TOKEN_SERVICE_TLS_CERT"),
		"path to the Token Service's server certificate for the §4.3 / §10.3 mTLS gRPC listener. Empty serves the gRPC surface in plaintext (dev mode only).")
	tlsKey := flag.String("tls-key", os.Getenv("LENNY_TOKEN_SERVICE_TLS_KEY"),
		"path to the private key for --tls-cert.")
	tlsCA := flag.String("tls-ca", os.Getenv("LENNY_TOKEN_SERVICE_CA"),
		"path to the CA bundle that verifies gateway client certificates on the §4.3 / §10.3 mTLS gRPC link. Required when --tls-cert is set; the Token Service uses tls.RequireAndVerifyClientCert.")
	// §4.9 line 1212 admin-time RBAC live-probe namespace. The probe's
	// SelfSubjectAccessReview and Secret get target this namespace, where
	// credentialPool secretRef Secrets are mounted. Defaults to the
	// Token Service's own namespace (POD_NAMESPACE, set by the downward
	// API), falling back to lenny-system.
	secretNamespace := flag.String("secret-namespace", envOr("POD_NAMESPACE", "lenny-system"),
		"namespace the §4.9 RBAC live-probe checks for credentialPool secretRef Secrets.")
	// §4 / §17.5 KMS provider selector. The cloud adapters
	// (pkg/kms/{aws,gcp,azure}) reach the binary through these flags;
	// the chart renders tokenService.kms.* into them.
	kmsOpts, kmsFinalize := providerflags.Bind(flag.CommandLine, os.Getenv, providerflags.Options{
		Provider: providerflags.ProviderLocal,
	})
	flag.Parse()
	if err := kmsFinalize(); err != nil {
		log.Fatalf("lenny-token-service: %v", err)
	}

	// §4 / §17.5 KMS provider. The in-process kms.Local provider is
	// the no-cloud development KEK; cloud deployments swap in an
	// AWS/GCP/Azure provider behind the same kms.Provider interface
	// by setting --kms-provider. spec: F-4.3.11 / F-17.5.2.
	ctx := context.Background()
	kmsProvider, err := providerflags.Resolve(ctx, *kmsOpts)
	if err != nil {
		log.Fatalf("lenny-token-service: kms provider: %v", err)
	}
	log.Printf("lenny-token-service: §4 KMS provider = %s (environment=%s)",
		kmsOpts.Provider, kmsOpts.Environment)
	kmsBackedSigner, err := jwt.NewKMSSigner(ctx, kmsProvider, jwt.TokenServiceKEKAlias, "dev-1")
	if err != nil {
		log.Fatalf("lenny-token-service: kms-backed signer: %v", err)
	}
	// spec: §10.2 line 225 — wrap the KMS-backed signer in the
	// JWTSigner circuit breaker so KMS outages convert to
	// KMS_SIGNING_UNAVAILABLE rather than hanging the request path.
	// F-10.2.6.
	signer := &jwt.BreakerSigner{Inner: kmsBackedSigner}

	// §16.1 Prometheus metric vectors. The §16.5
	// TokenServiceUnavailable alert reads
	// lenny_token_service_circuit_state from the gateway-side breaker
	// (set in cmd/lenny-gateway/main.go); the metrics emitted here are
	// the §16.1 request_duration, errors, secret_reloads, and the
	// §13.3 rate-limit counters.
	metricsEmitter, err := promemit.New()
	if err != nil {
		log.Fatalf("lenny-token-service: metrics emitter: %v", err)
	}

	// §13.3 line 589 durable write-before-issue state. With Postgres
	// wired, every minted token is recorded in issued_tokens and
	// every accepted/rejected exchange writes a token.exchanged audit
	// row in the same transaction under the §11.7 advisory lock. With
	// no Postgres, the Token Service runs without durable issued-
	// token or audit state.
	var (
		pgPool       *pgxpool.Pool
		issuedTokens tokenservice.IssuedTokenStore
		auditor      tokenservice.Auditor
	)
	if *postgresDSN != "" {
		pool, err := pgxpool.New(ctx, *postgresDSN)
		if err != nil {
			log.Fatalf("lenny-token-service: postgres: %v", err)
		}
		pgPool = pool
		defer pool.Close()
		// Postgres-backed issuedtokenstore.Store also satisfies
		// IssuedTokenAuditStore, so the handler uses the combined
		// write-before-issue tx. The in-memory auditor is wired
		// alongside to cover the rate-limit and revocation paths.
		issuedTokens = issuedtokenstore.New(pool)
		// §12.3 R-03: the audit chain routes its writes through the
		// StoreRouter rather than holding a raw pool. The Token Service
		// audit path is Postgres-only (it never touches Redis), so the
		// single-shard router is built in Postgres-only mode. F-12.3.4.
		auditRouter, err := storerouter.NewSingleShardRouter(storerouter.Config{Postgres: pool})
		if err != nil {
			log.Fatalf("lenny-token-service: store router: %v", err)
		}
		auditor = auditstore.New(auditRouter)
	} else {
		// Dev path: keep an in-memory chain so the rate-limit /
		// revocation audit emits still produce rows (lost on restart).
		// The /v1/oauth/token success path falls through to
		// IssuedTokenStore.Record + Auditor.Append (no Postgres tx).
		chains := audit.NewChainSet()
		auditor = policy.NewChainSetAppender(chains, nil)
	}
	_ = pgPool

	// §13.3 line 595: NTP drift self-monitor. The source returns the
	// clockinject-injected offset for v1 (zero in production unless an
	// operator wires a real adjtimex/chrony probe). The exchange path
	// consults driftMonitor.Degraded() and returns
	// 503 token_validation_unavailable when |drift| >= 5s. F-13.3.5.
	driftMonitor := driftmonitor.New(func() time.Duration {
		off, _ := clockinject.Offset()
		return off
	}, metricsEmitter)
	go driftMonitor.Start(ctx, 30*time.Second)

	srv := tokenservice.NewServer(tokenservice.Options{
		Signer: signer,
		Issuer: *issuer,
		PerDialectCap: map[string]time.Duration{
			"lenny-gateway": 24 * time.Hour,
			"lenny-ops":     1 * time.Hour,
			"llm-proxy":     1 * time.Hour,
		},
		IssuedTokens: issuedTokens,
		Auditor:      auditor,
		Metrics:      metricsEmitter,
		RateLimit: tokenservice.RateLimitOptions{
			CallerPerSecond: *rlCallerPerSec,
			CallerPerMinute: *rlCallerPerMin,
			TenantPerSecond: *rlTenantPerSec,
			SampleWindow:    *rlSampleWindow,
		},
		DriftDegraded: driftMonitor.Degraded,
	})

	// §4.0 EventEmitter wiring. The §4.0 spec requires every process
	// hosting subsystems that may emit §16.6 operational events to
	// construct an EventEmitter so a future emit site does not have to
	// re-thread the dependency through the binary. The Token Service
	// signs and mints credential tokens (§4.3) and rotates leases for
	// §4.9; once the rotation events are wired they will land on this
	// emitter without further main.go changes. With --redis-url the
	// emitter streams to the §25.5 platform-scoped Redis stream alongside
	// the gateway and the controllers; without Redis the emitter writes
	// only to the process-local in-memory buffer (the §25.5 per-replica
	// fall-back). The buffer is constructed unconditionally so a Redis
	// outage degrades to local-only delivery rather than dropping events.
	replicaID := os.Getenv("HOSTNAME")
	if replicaID == "" {
		replicaID = "token-service"
	}
	opsEventBuffer := events.NewEventBuffer(0)
	var opsEmitter events.EventEmitter = events.NewEmitter(opsEventBuffer, replicaID)
	// §4.3 line 201 Redis-backed encrypted access-token cache. Wired
	// only when --redis-url is set; the cache short-circuits Postgres
	// revocation lookups on the validation hot path. With no Redis,
	// the validator falls back to the authoritative Postgres lookup.
	var accessCache *tokencache.Cache
	if *redisURL != "" {
		redisClient, err := redisconn.NewClient(redisconn.Config{URL: *redisURL, Password: *redisPassword})
		if err != nil {
			log.Fatalf("lenny-token-service: redis client: %v", err)
		}
		defer func() { _ = redisClient.Close() }()
		opsEmitter = events.NewStreamEmitter(events.StreamEmitterOptions{
			Client:    redisClient,
			Buffer:    opsEventBuffer,
			Source:    "//lenny.dev/token-service/" + replicaID,
			ReplicaID: replicaID,
		})
		log.Printf("lenny-token-service: §25.5 operational events streaming to Redis %s", events.DefaultStreamKey)
		accessCache, err = tokencache.New(redisClient, kmsProvider)
		if err != nil {
			log.Fatalf("lenny-token-service: access-token cache: %v", err)
		}
		log.Printf("lenny-token-service: §4.3 access-token cache wired (envelope-encrypted, KEK alias %s)", tokencache.CacheKEKAlias)
	}
	_ = accessCache
	// Keep opsEmitter live so the linker retains the wiring even before a
	// subsystem in this binary takes it as a constructor dependency. A
	// future credential-rotation event emit will replace this no-op log.
	log.Printf("lenny-token-service: §4.0 EventEmitter ready (replica=%s, redis=%t)",
		replicaID, *redisURL != "")
	_ = opsEmitter

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// §16.1 metrics endpoint. The chart's
	// allow-token-service-metrics-scrape NetworkPolicy admits the
	// monitoring namespace on tokenService.metricsPort; the binary
	// binds the metrics listener on --metrics-addr.
	var metricsSrv *http.Server
	if *metricsAddr != "" {
		metricsMux := http.NewServeMux()
		metricsMux.Handle("/metrics", metricsEmitter.Handler())
		metricsSrv = &http.Server{
			Addr:              *metricsAddr,
			Handler:           metricsMux,
			ReadHeaderTimeout: 10 * time.Second,
		}
	}

	// §4.3 / §12.2.4 gRPC TokenService surface. The credential-
	// assignment service is the same in-process credassign.Service
	// pool-selection + lease-minting logic the gateway runs today;
	// the binary makes it reachable over gRPC so the gateway can
	// switch its call site from the in-process MintLease to a gRPC
	// client without re-implementing the lease semantics. Pool
	// registration runs through the same RegisterPool entry point;
	// no pools are registered at startup so AssignCredentials fails
	// fast until an operator configures pools. The in-memory lease
	// store and credential cache are appropriate for the development
	// path; a production deployment swaps in Postgres-backed
	// `credleasestore/pgstore` and a shared credential cache.
	leases := credleasestore.New()
	cache := credcache.New()
	assignSvc := credassign.New(leases, cache)
	tsGRPC := tokenservice.NewGRPCServer(assignSvc, leases)
	tsGRPC.SetAuditor(auditor)
	tsGRPC.SetMetrics(metricsEmitter)
	// §4.9 line 1212 admin-time RBAC live-probe. Wire the
	// Kubernetes-backed prober when the Token Service runs in-cluster.
	// Out of cluster (local dev) the prober is left unset; the
	// ProbeSecretAccess RPC then returns codes.Unavailable so the gateway
	// admin handler maps it to 503 CREDENTIAL_PROBE_UNAVAILABLE and never
	// fails open.
	if clientset, kerr := inClusterClientset(); kerr != nil {
		log.Printf("lenny-token-service: §4.9 RBAC live-probe disabled (no in-cluster Kubernetes client: %v)", kerr)
	} else {
		tsGRPC.SetSecretAccessProber(secretprobe.New(clientset, *secretNamespace))
		log.Printf("lenny-token-service: §4.9 RBAC live-probe active in namespace %q", *secretNamespace)
	}
	// Sanity reference to pkgaudit so the import is used; the
	// IssuedTokenAuditStore implementation in
	// pkg/gateway/issuedtokenstore returns audit.Row values that the
	// token service handler reads through this type.
	_ = pkgaudit.Row{}

	var grpcSrv *grpc.Server
	if *grpcAddr != "" {
		// §4.3 / §10.3 mTLS server credentials. The Token Service
		// must verify the gateway's client certificate; the §4.3
		// trust boundary rests on the Token Service authenticating
		// the caller. When the TLS flags are unset the gRPC surface
		// runs in plaintext, which is the dev-mode path only.
		// spec: §4.3 line 195
		creds, err := tokenServiceCreds(*tlsCert, *tlsKey, *tlsCA)
		if err != nil {
			log.Fatalf("lenny-token-service: gRPC mTLS: %v", err)
		}
		var opts []grpc.ServerOption
		if creds != nil {
			opts = append(opts, grpc.Creds(creds))
			log.Printf("lenny-token-service: §4.3 gRPC mTLS active; client certs verified against --tls-ca")
		} else {
			log.Printf("lenny-token-service: §4.3 gRPC running plaintext (dev mode: --tls-cert/--tls-key/--tls-ca unset)")
		}
		// §4.3 line 205 per-replica identity. When the gateway client
		// cert carries a SPIFFE URI SAN (chart value
		// gateway.spiffe.enabled wires cert-manager-csi-driver-spiffe
		// per-replica identities into the gateway pods), extract the
		// SPIFFE ID and log it for audit. The unary interceptor
		// surfaces the value through the context so handler-level
		// audit emits can include it.
		opts = append(opts, grpc.UnaryInterceptor(spiffeAuditInterceptor()))
		grpcSrv = grpc.NewServer(opts...)
		tokensv1.RegisterTokenServiceServer(grpcSrv, tsGRPC)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		log.Printf("lenny-token-service: HTTP token-exchange listening on %s", *addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen http: %v", err)
		}
	}()
	if metricsSrv != nil {
		go func() {
			log.Printf("lenny-token-service: §16.1 metrics listening on %s", *metricsAddr)
			if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("listen metrics: %v", err)
			}
		}()
	}
	if grpcSrv != nil {
		lis, err := net.Listen("tcp", *grpcAddr)
		if err != nil {
			log.Fatalf("listen grpc %s: %v", *grpcAddr, err)
		}
		go func() {
			log.Printf("lenny-token-service: gRPC TokenService listening on %s", *grpcAddr)
			if err := grpcSrv.Serve(lis); err != nil {
				log.Fatalf("serve grpc: %v", err)
			}
		}()
	}
	<-stop
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	if metricsSrv != nil {
		_ = metricsSrv.Shutdown(shutdownCtx)
	}
	if grpcSrv != nil {
		grpcSrv.GracefulStop()
	}
}

// envInt resolves an int from the named env var, falling back to def
// when unset or unparseable.
// envOr resolves a string from the named env var, falling back to def
// when unset.
func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// inClusterClientset builds a Kubernetes clientset from the in-cluster
// ServiceAccount config for the §4.9 RBAC live-probe. It returns an
// error when the Token Service is not running inside a cluster (local
// dev), in which case the probe is disabled.
func inClusterClientset() (kubernetes.Interface, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(cfg)
}

func envInt(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	var out int
	_, err := fmt.Sscan(v, &out)
	if err != nil {
		return def
	}
	return out
}

// envDuration resolves a time.Duration from the named env var,
// falling back to def when unset or unparseable.
func envDuration(name string, def time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

// spiffeAuditInterceptor returns a unary server interceptor that
// extracts the SPIFFE-ID URI SAN from the gateway client's mTLS
// certificate and logs it on every Token Service gRPC call. When the
// caller presents a non-SPIFFE certificate (the shared per-Service
// cert path), the interceptor logs once at INFO and proceeds. When
// the caller presents a SPIFFE URI SAN (the per-replica path enabled
// by gateway.spiffe.enabled), the URI lands in the log line and
// future audit emits can read it from the context.
//
// spec: §4.3 line 205 — "Each gateway replica has a distinct mTLS
// identity so compromise of one is attributable and revocable
// independently."
func spiffeAuditInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		pr, ok := peer.FromContext(ctx)
		if !ok || pr == nil {
			return handler(ctx, req)
		}
		tlsInfo, ok := pr.AuthInfo.(credentials.TLSInfo)
		if !ok || len(tlsInfo.State.PeerCertificates) == 0 {
			// Plaintext dev path or no client cert: nothing to extract.
			return handler(ctx, req)
		}
		cert := tlsInfo.State.PeerCertificates[0]
		id, err := spiffeid.FromCert(cert)
		if err != nil {
			// Non-SPIFFE cert: shared per-Service path. Log once per
			// session so an operator can correlate the audit trail
			// with the chart configuration (spiffe.enabled=false).
			log.Printf("lenny-token-service: §4.3 gRPC %s: peer cert has no SPIFFE URI SAN (per-Service identity path)", info.FullMethod)
			return handler(ctx, req)
		}
		log.Printf("lenny-token-service: §4.3 gRPC %s: peer SPIFFE ID = %s (per-replica identity)", info.FullMethod, id)
		return handler(WithSPIFFEID(ctx, id), req)
	}
}

// spiffeIDCtxKey carries a parsed §4.3 SPIFFE ID through the request
// context so audit emits and future per-replica policy checks read a
// single canonical value.
type spiffeIDCtxKey struct{}

// WithSPIFFEID returns a copy of ctx carrying id. Exposed so future
// in-handler audit emits can record the caller's SPIFFE identity
// without re-parsing the certificate.
// spec: §4.3 line 205.
func WithSPIFFEID(ctx context.Context, id spiffeid.ID) context.Context {
	return context.WithValue(ctx, spiffeIDCtxKey{}, id)
}

// SPIFFEIDFromContext returns the SPIFFE ID the interceptor attached
// to ctx, or the zero value and false when none is set.
// spec: §4.3 line 205.
func SPIFFEIDFromContext(ctx context.Context) (spiffeid.ID, bool) {
	id, ok := ctx.Value(spiffeIDCtxKey{}).(spiffeid.ID)
	return id, ok
}

// tokenServiceCreds builds the §4.3 / §10.3 mTLS server credentials.
// All three of certPath, keyPath, and caPath empty selects plaintext
// (dev mode); any one set requires all three and returns mTLS server
// credentials that require and verify the gateway's client cert.
// spec: §4.3 line 195 "Gateway replicas call the Token Service over mTLS"
func tokenServiceCreds(certPath, keyPath, caPath string) (credentials.TransportCredentials, error) {
	switch {
	case certPath == "" && keyPath == "" && caPath == "":
		return nil, nil
	case certPath == "" || keyPath == "" || caPath == "":
		return nil, fmt.Errorf("token service mTLS requires --tls-cert, --tls-key, and --tls-ca to all be set")
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("load token-service server cert: %w", err)
	}
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read token-service client CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("token-service client CA bundle parsed no certificates")
	}
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
	}), nil
}

// kms.Provider is implicitly referenced through the providerflags
// package; keep the import live for code-search clarity.
var _ kms.Provider = (*kms.Local)(nil)

// Avoid unused-import error for strings (we may use it in future log
// formatting).
var _ = strings.TrimSpace
