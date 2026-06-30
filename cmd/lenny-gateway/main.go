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
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/agentpodstate"
	"github.com/lennylabs/lenny/pkg/alerting/evaluator"
	"github.com/lennylabs/lenny/pkg/alerting/rules"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/integrity"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	"github.com/lennylabs/lenny/pkg/audit/siem"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/introspection"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	blobproviderflags "github.com/lennylabs/lenny/pkg/blobstore/providerflags"
	"github.com/lennylabs/lenny/pkg/circuitbreaker"
	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/connectoroauth"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/delegation/recovery"
	"github.com/lennylabs/lenny/pkg/driftmonitor"
	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/auditretention"
	"github.com/lennylabs/lenny/pkg/gateway/auditscope"
	"github.com/lennylabs/lenny/pkg/gateway/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/auditstore/auditbatch"
	"github.com/lennylabs/lenny/pkg/gateway/barrier"
	"github.com/lennylabs/lenny/pkg/gateway/billingcheckpoint"
	"github.com/lennylabs/lenny/pkg/gateway/billingfanout"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore/cachingstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectorcredstore"
	connectorcredpg "github.com/lennylabs/lenny/pkg/gateway/connectorcredstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectorsecret"
	"github.com/lennylabs/lenny/pkg/gateway/coordfence"
	"github.com/lennylabs/lenny/pkg/gateway/createdsweeper"
	"github.com/lennylabs/lenny/pkg/gateway/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore"
	credentialpoolpg "github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentialserver"
	"github.com/lennylabs/lenny/pkg/gateway/credentialstore"
	credentialpg "github.com/lennylabs/lenny/pkg/gateway/credentialstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/credfallback"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/credrenewal"
	credrenewalprop "github.com/lennylabs/lenny/pkg/gateway/credrenewal/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/customrolestore"
	customrolepg "github.com/lennylabs/lenny/pkg/gateway/customrolestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/derivelock"
	"github.com/lennylabs/lenny/pkg/gateway/devmode"
	"github.com/lennylabs/lenny/pkg/gateway/dualstore"
	"github.com/lennylabs/lenny/pkg/gateway/elicitationfloor"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	environmentpg "github.com/lennylabs/lenny/pkg/gateway/environmentstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/eventbus"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/gateway/experimentprovider"
	"github.com/lennylabs/lenny/pkg/gateway/experimentsticky"
	"github.com/lennylabs/lenny/pkg/gateway/extractionthreshold"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/health"
	"github.com/lennylabs/lenny/pkg/gateway/health/backends"
	"github.com/lennylabs/lenny/pkg/gateway/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/jwtaudit"
	"github.com/lennylabs/lenny/pkg/gateway/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/leasecontrol/denialpg"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/memorystore"
	memorypg "github.com/lennylabs/lenny/pkg/gateway/memorystore/pgstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	idempgstore "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/orphansession"
	"github.com/lennylabs/lenny/pkg/gateway/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/pgnotify"
	"github.com/lennylabs/lenny/pkg/gateway/playground"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/policy"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/prestop"
	"github.com/lennylabs/lenny/pkg/gateway/quotacheckpoint"
	quotacheckpointpg "github.com/lennylabs/lenny/pkg/gateway/quotacheckpoint/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/ratelimit"
	"github.com/lennylabs/lenny/pkg/gateway/recycle"
	"github.com/lennylabs/lenny/pkg/gateway/resultrollup"
	revocationprop "github.com/lennylabs/lenny/pkg/gateway/revocation/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionbudget"
	"github.com/lennylabs/lenny/pkg/gateway/sessioncallback"
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionidle"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionusage"
	sessionusagepg "github.com/lennylabs/lenny/pkg/gateway/sessionusage/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/slothealth"
	"github.com/lennylabs/lenny/pkg/gateway/storagequota"
	"github.com/lennylabs/lenny/pkg/gateway/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore"
	tenantaccesspg "github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/tlsprobe"
	"github.com/lennylabs/lenny/pkg/gateway/translator"
	"github.com/lennylabs/lenny/pkg/gateway/usagestore"
	usagepg "github.com/lennylabs/lenny/pkg/gateway/usagestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/usercreds"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/vcscred"
	"github.com/lennylabs/lenny/pkg/idempotency"
	"github.com/lennylabs/lenny/pkg/kms/envelope"
	"github.com/lennylabs/lenny/pkg/kms/rekey"
	"github.com/lennylabs/lenny/pkg/mtls/certreload"
	mtlsdenylist "github.com/lennylabs/lenny/pkg/mtls/denylist"
	mtlsdenylistprop "github.com/lennylabs/lenny/pkg/mtls/denylist/propagator"
	"github.com/lennylabs/lenny/pkg/mtls/interceptordial"
	"github.com/lennylabs/lenny/pkg/mtls/spiffe"
	"github.com/lennylabs/lenny/pkg/observability/logging"
	"github.com/lennylabs/lenny/pkg/observability/slo"
	"github.com/lennylabs/lenny/pkg/observability/tracing"
	"github.com/lennylabs/lenny/pkg/podlifecycle"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	interceptorv1 "github.com/lennylabs/lenny/pkg/proto/interceptor/v1"
	"github.com/lennylabs/lenny/pkg/quota"
	"github.com/lennylabs/lenny/pkg/redisconn"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/storerouter"
	"github.com/lennylabs/lenny/pkg/tenantkms"
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
	// spec: §16.4 lines 370-372 — install the structured JSON logger as the
	// process-wide slog default and route the stdlib log package through it,
	// so every gateway log line carries ts (RFC 3339 UTC), level, msg, and
	// component=gateway. F-16.4.1.
	logging.Setup(os.Stderr, "gateway")

	// spec: §4.1 gateway subsystem seams — parse the composition-root
	// inputs once, finalize the §4 / §17.5 KMS provider selection, then
	// hand off to runGateway, which wires and starts every subsystem.
	f := parseFlags()
	if err := f.kmsFinalize(); err != nil {
		log.Fatalf("lenny-gateway: %v", err)
	}
	runGateway(f)
}

// runGateway wires and starts every gateway subsystem from the parsed
// flags, then blocks on the §17 run-and-shutdown loop. It is the gateway
// composition root: a flat sequence of construct-and-wire steps grouped
// by subsystem, terminating in the signal-driven graceful shutdown.
//
// spec: §4.1 — the gateway is one component internally partitioned into
// subsystem boundaries (Go interfaces within a single binary); this
// function constructs each in dependency order.
func runGateway(f *gatewayFlags) {
	w := &gatewayWiring{f: f}
	// Re-bind the parsed flags to their original local names so the
	// subsystem-wiring blocks below read them unchanged. Each name aliases
	// the same flag pointer parseFlags produced.
	oidcIssuerURL := f.oidcIssuerURL
	oidcClientID := f.oidcClientID
	devMode := f.devMode
	tlsTerminatedUpstream := f.tlsTerminatedUpstream
	sloValidated := f.sloValidated
	scatterMaxConcurrency := f.scatterMaxConcurrency
	scatterPerShardTimeoutSeconds := f.scatterPerShardTimeoutSeconds
	scatterAggregateTimeoutSeconds := f.scatterAggregateTimeoutSeconds
	startupProbeRedisAddr := f.startupProbeRedisAddr
	startupProbePgBouncerAddr := f.startupProbePgBouncerAddr
	startupProbeCA := f.startupProbeCA
	startupProbeCert := f.startupProbeCert
	startupProbeKey := f.startupProbeKey
	dualStoreMaxSeconds := f.dualStoreMaxSeconds
	claimHoldTTLSeconds := f.claimHoldTTLSeconds
	auditLockAcquireTimeoutMs := f.auditLockAcquireTimeoutMs
	auditLockMaxRetries := f.auditLockMaxRetries
	auditLockRetryBaseMs := f.auditLockRetryBaseMs
	externalInterceptorTLSCert := f.externalInterceptorTLSCert
	externalInterceptorTLSKey := f.externalInterceptorTLSKey
	externalInterceptorCA := f.externalInterceptorCA
	guardrailsClassifier := f.guardrailsClassifier
	opsNonceCheckpointPath := f.opsNonceCheckpointPath
	gatewayQueueDepthThreshold := f.gatewayQueueDepthThreshold
	gatewayLatencyThresholdSeconds := f.gatewayLatencyThresholdSeconds
	credentialPoolLowThreshold := f.credentialPoolLowThreshold
	sloBurnRateFastMultiplier := f.sloBurnRateFastMultiplier
	sloBurnRateSlowMultiplier := f.sloBurnRateSlowMultiplier
	postgresWriteCeilingIops := f.postgresWriteCeilingIops
	auditStartupChainCheckEntries := f.auditStartupChainCheckEntries
	auditScatterCacheEnabled := f.auditScatterCacheEnabled
	auditSIEMEndpoint := f.auditSIEMEndpoint
	auditSIEMSecret := f.auditSIEMSecret
	auditSIEMFailureThresholdPercent := f.auditSIEMFailureThresholdPercent
	auditSIEMMaxDeliveryLagSeconds := f.auditSIEMMaxDeliveryLagSeconds
	auditSIEMPollIntervalSeconds := f.auditSIEMPollIntervalSeconds
	auditOCSFRetryIntervalSeconds := f.auditOCSFRetryIntervalSeconds
	auditOCSFMaxAttempts := f.auditOCSFMaxAttempts
	auditOCSFBatchSize := f.auditOCSFBatchSize
	auditBatchingEnabled := f.auditBatchingEnabled
	auditFlushIntervalMs := f.auditFlushIntervalMs
	auditFlushBatchSize := f.auditFlushBatchSize
	callbackURLAllowedDomains := f.callbackURLAllowedDomains
	elicitationFloor := f.elicitationFloor
	grpcAddr := f.grpcAddr
	leaseAutoMaxPerMin := f.leaseAutoMaxPerMin
	leaseDefaultBudget := f.leaseDefaultBudget
	leaseMaxBudget := f.leaseMaxBudget
	leaseDefaultApproval := f.leaseDefaultApproval
	leaseCoolOffSec := f.leaseCoolOffSec
	leaseRejectionCoolOffSec := f.leaseRejectionCoolOffSec
	spiffeTrustDomain := f.spiffeTrustDomain
	interceptorNamespaces := f.interceptorNamespaces
	llmProxyPublicURL := f.llmProxyPublicURL
	maxSessionAgeSeconds := f.maxSessionAgeSeconds
	delegationUsageQuiescenceTimeoutSeconds := f.delegationUsageQuiescenceTimeoutSeconds
	delegationMaxLevelRecoverySeconds := f.delegationMaxLevelRecoverySeconds
	delegationMaxTreeRecoverySeconds := f.delegationMaxTreeRecoverySeconds
	delegationCascadeTimeoutSeconds := f.delegationCascadeTimeoutSeconds
	delegationMaxOrphanTasksPerTenant := f.delegationMaxOrphanTasksPerTenant
	credentialsExpiryWarningLeadSeconds := f.credentialsExpiryWarningLeadSeconds
	noEnvPolicy := f.noEnvPolicy
	connectorOAuthCallbackURL := f.connectorOAuthCallbackURL
	connectorOAuthCA := f.connectorOAuthCA
	connectorOAuthClientSecretKey := f.connectorOAuthClientSecretKey
	billingCorrectionRateThreshold := f.billingCorrectionRateThreshold
	gdprRetentionDays := f.gdprRetentionDays
	auditRetentionPruneIntervalSeconds := f.auditRetentionPruneIntervalSeconds
	eventBusRetryIntervalSeconds := f.eventBusRetryIntervalSeconds
	eventBusMaxRetryAttempts := f.eventBusMaxRetryAttempts
	eventBusDuplicateInjectionFactor := f.eventBusDuplicateInjectionFactor
	eventBusDropAlertThreshold := f.eventBusDropAlertThreshold
	maxSessionsPerReplica := f.maxSessionsPerReplica
	minReplicas := f.minReplicas
	streamCeiling := f.streamCeiling
	sessionEventReplayBufferDepth := f.sessionEventReplayBufferDepth
	poolerMode := f.poolerMode
	externalInterceptors := f.externalInterceptors

	// spec: §17.2 line 86 / §9.2 line 64 — the platform-wide elicitation
	// content-integrity floor is seeded from the
	// --elicitation-content-integrity-floor flag and then kept live by the
	// phase-stamp ConfigMap reconcile started below (when a cluster client
	// exists). Every floor read (the per-request effective-mode resolver,
	// the admin below-floor guard, and the §16.5 weakened-mode gauge) goes
	// through this provider so a `helm upgrade` floor change takes effect
	// without a gateway restart. F-17.2.9.
	elicitationFloorProvider := elicitationfloor.NewProvider(*elicitationFloor)

	// spec: §17.4 line 268 — dev-mode hard startup assertion. The
	// gateway's own listener is plain HTTP; production terminates TLS at
	// the ingress (the §17 line 7 Deployment+Service+Ingress topology)
	// and acknowledges that posture with --tls-terminated-upstream. With
	// neither dev mode nor that acknowledgment the gateway refuses to
	// start so a misconfigured staging or production deployment cannot
	// silently run without encryption. F-17.4.5.
	if err := devmode.ResolveStartupGate(*devMode, *tlsTerminatedUpstream); err != nil {
		log.Fatalf("lenny-gateway: LENNY_TLS_REQUIRED: %v", err)
	}

	// spec: §5.3 line 677 — in dev mode the default isolation profile
	// falls back to runc. Log the mandated warning once at startup so an
	// accidental production dev-mode install is visible in the logs.
	if *devMode {
		log.Printf("lenny-gateway: %s", isolation.DevModeIsolationWarning)
		// spec: §17.4 line 269 — when dev mode relaxes TLS, log the
		// warning at startup and re-broadcast it every minute while the
		// process runs. F-17.4.6.
		devmode.StartWarnTicker(context.Background(), devmode.WarnInterval, func(msg string) {
			log.Printf("lenny-gateway: %s", msg)
		})
	}

	// spec: §16.3 line 359 — install the process-wide OpenTelemetry
	// TracerProvider and W3C trace-context propagator so the §16.3 span
	// catalog has a real exporter behind it instead of the global no-op
	// provider. The gateway emits 100% (head) and an OTLP/HTTP exporter
	// ships spans to OTEL_EXPORTER_OTLP_ENDPOINT; with no endpoint (or in
	// dev mode) a stdout exporter covers `make run`. F-16.3.2 / F-16.3.8.
	traceShutdown, err := tracing.InitProvider(context.Background(), tracing.ProviderConfig{
		ServiceName:  "lenny-gateway",
		OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		DevMode:      *devMode,
	})
	if err != nil {
		log.Fatalf("lenny-gateway: tracing init: %v", err)
	}

	// spec: §16.5 lines 609, 623 — surface the provisional-SLO startup
	// warning unless the Phase 14.5 benchmark gate has set slo.validated,
	// so a deployment running the unvalidated defaults cannot silently
	// treat them as customer SLA commitments.
	if msg, emit := slo.StartupWarning(*sloValidated); emit {
		log.Printf("lenny-gateway: %s", msg)
	}

	// §12.8 clock-injection harness. Read LENNY_CLOCK_OFFSET_SECONDS
	// once at startup so the gateway and every clock-using subsystem
	// pick up a chaos-test offset. A non-integer value fails loudly
	// here rather than silently behaving as production. Production
	// installs run with the variable unset; the §17.6 preflight Job
	// asserts that separately via AssertProductionDefault.
	if err := clockinject.FromEnv(); err != nil {
		log.Fatalf("lenny-gateway: %v", err)
	}

	// §10.6: the platform-wide noEnvironmentPolicy must be set
	// explicitly. Dev mode derives allow-all for local convenience;
	// outside dev mode an unset value is a fatal misconfiguration so a
	// chart with the default stripped fails closed at startup. See
	// resolveNoEnvironmentPolicy for the pure-function form the
	// §11.1 TestGatewayConfigValidation regression test exercises.
	resolvedNoEnvPolicy, err := resolveNoEnvironmentPolicy(*noEnvPolicy, *devMode)
	if err != nil {
		log.Fatalf("lenny-gateway: %v", err)
	}

	// spec: §10.3 lines 361-371 — startup configuration validation. Each
	// missing required platform key emits a structured LENNY_CONFIG_MISSING
	// log entry (config_key/scope/remediation fields) and the gateway
	// refuses to become ready by exiting non-zero so Kubernetes surfaces
	// CrashLoopBackOff rather than the replica silently serving with
	// undefined semantics. noEnvironmentPolicy is gated just above;
	// playground.devTenantId by playground.Config.Validate; this covers the
	// OIDC and session-duration keys. The OIDC keys are exempt in dev mode
	// (§10.3 line 373 / §17.4). F-10.3.14.
	if missing := validatePlatformConfig(*devMode, *oidcIssuerURL, *oidcClientID, *maxSessionAgeSeconds); len(missing) > 0 {
		for _, m := range missing {
			slog.Error("LENNY_CONFIG_MISSING", "config_key", m.configKey, "scope", m.scope, "remediation", m.remediation)
		}
		log.Fatalf("lenny-gateway: %d required platform configuration key(s) missing or invalid (§10.3); see the LENNY_CONFIG_MISSING entries above", len(missing))
	}

	// spec: §10.3 line 359 — gateway startup TLS probe. Before the replica
	// is marked ready, verify a TLS handshake to Redis and PgBouncer
	// succeeds and that a plaintext connection is refused, so a
	// misconfigured backend (wrong port, missing cert) fails startup
	// rather than degrading silently at runtime. Dev mode is exempt (the
	// line 373 dev-mode symmetry / §17.4) and an unset endpoint is skipped.
	// F-10.3.15.
	if !*devMode && (*startupProbeRedisAddr != "" || *startupProbePgBouncerAddr != "") {
		probeTLS, perr := buildStartupProbeTLSConfig(*startupProbeCA, *startupProbeCert, *startupProbeKey)
		if perr != nil {
			log.Fatalf("lenny-gateway: §10.3 startup TLS probe configuration: %v", perr)
		}
		if err := tlsprobe.Probe(
			context.Background(), tlsprobe.Config{TLSConfig: probeTLS},
			tlsprobe.Target{Backend: tlsprobe.BackendRedis, Addr: *startupProbeRedisAddr},
			tlsprobe.Target{Backend: tlsprobe.BackendPgBouncer, Addr: *startupProbePgBouncerAddr},
		); err != nil {
			log.Fatalf("lenny-gateway: §10.3 startup TLS probe failed: %v", err)
		}
		log.Printf("lenny-gateway: §10.3 startup TLS probe passed (redis=%q pgbouncer=%q)", *startupProbeRedisAddr, *startupProbePgBouncerAddr)
	}

	// spec: §4.2 line 165 — LENNY_POOLER_MODE must be one of the two
	// documented values. The trigger-level guard runs regardless of
	// this check; failing at startup keeps a misconfigured deploy
	// from running silently with the wrong assumed posture.
	switch *poolerMode {
	case "transactional", "external":
		log.Printf("lenny-gateway: pooler-mode=%s (§4.2 line 165)", *poolerMode)
	default:
		log.Fatalf("lenny-gateway: --pooler-mode must be `transactional` or `external`, got %q (§4.2 line 165)", *poolerMode)
	}

	// spec: §11.3 line 224 — surface the effective
	// `delegation.usageQuiescenceTimeoutSeconds` at startup so operators
	// see the value the §8.10 tree-recovery orchestrator will consume.
	// The recovery package exposes the Config field; the gateway-side
	// orchestrator threads `delegationQuiescenceCfg` to keep one source
	// of truth. F-11.3.19.
	//
	// spec: §8.10 lines 1022-1023 / 1042 — extend the same Config to
	// carry `maxLevelRecoverySeconds` / `maxTreeRecoverySeconds` so the
	// recovery package consumes the live operator overrides. F-8.10.6.
	delegationQuiescenceCfg := recovery.Config{
		LevelTimeout:           time.Duration(*delegationMaxLevelRecoverySeconds) * time.Second,
		TreeTimeout:            time.Duration(*delegationMaxTreeRecoverySeconds) * time.Second,
		UsageQuiescenceTimeout: time.Duration(*delegationUsageQuiescenceTimeoutSeconds) * time.Second,
	}
	log.Printf("lenny-gateway: delegation.usageQuiescenceTimeoutSeconds=%ds (§11.3 line 224)",
		int(delegationQuiescenceCfg.QuiescenceDeadline(time.Time{}).Sub(time.Time{}).Seconds()))
	log.Printf("lenny-gateway: delegation.maxLevelRecoverySeconds=%ds delegation.maxTreeRecoverySeconds=%ds (§8.10 lines 1022-1023)",
		*delegationMaxLevelRecoverySeconds, *delegationMaxTreeRecoverySeconds)
	log.Printf("lenny-gateway: delegation.cascadeTimeoutSeconds=%ds delegation.maxOrphanTasksPerTenant=%d (§8.10 lines 1078, 1103)",
		*delegationCascadeTimeoutSeconds, *delegationMaxOrphanTasksPerTenant)
	_ = delegationQuiescenceCfg

	// §4.6.1: a high reserved-hold TTL holds a recycled pod out of its pinned
	// tenant's claimable idle inventory (a reserved pod is counted as occupied
	// for inventory and scaling, §4.6.2) and delays retirement-limit
	// evaluation at the next disposition. Warn at startup when the operator
	// sets it above the advisory ceiling so the inventory effect is visible in
	// the logs; the value is honored regardless.
	if *claimHoldTTLSeconds > recycle.HighClaimHoldTTLWarnSeconds {
		log.Printf("lenny-gateway: WARNING: claimHoldTTLSeconds=%ds exceeds the advisory ceiling of %ds (§4.6.1); a long reserved hold depresses apparent idle inventory and delays retirement-limit evaluation",
			*claimHoldTTLSeconds, recycle.HighClaimHoldTTLWarnSeconds)
	}

	// spec: §4.1 — build the §4.2/§4.4/§4.5 persistence and §4.3/§10.2/§10.3
	// credential surfaces, then relocate the §17.4 SQLite flush-loop cancel
	// and the §4.3 token-service connection close to the composition root so
	// they run at process shutdown. The original defers lived inside the
	// stores block; guarding on the accumulator field preserves their
	// register-only-when-set semantics.
	w.buildStores()
	if w.sqliteFlushCancel != nil {
		defer w.sqliteFlushCancel()
	}
	defer func() {
		if w.tokenServiceConn != nil {
			_ = w.tokenServiceConn.Close()
		}
	}()
	gwMetrics, err := gatewaymetrics.New()
	if err != nil {
		log.Fatalf("lenny-gateway: metrics: %v", err)
	}
	// spec: §10.1 lines 33-37 / §11.3 line 209 — the gateway-side
	// CoordinatorFence driver. On a resume re-bind the sessionserver
	// announces the session's coordination_generation to the pod; a
	// generation-stale rejection drives the retry/relinquish policy,
	// releasing the coordination lease when the coordinator gives up.
	// Wired only when the lease store exists (it needs Release for the
	// relinquish path); the metrics are now built so the counters move.
	if w.erasureLeaseStore != nil {
		w.coordFencer = coordfence.New(
			sessionGenerationReader{store: w.sessions},
			w.erasureLeaseStore,
			w.replica,
			gwMetrics,
			coordfence.Options{Logf: log.Printf},
		)
	}
	// spec: §10.2 line 225 — back-fill the JWTSigner breaker observer
	// with the freshly-built metrics so signing failures and circuit
	// transitions land on `lenny_gateway_kms_signing_errors_total` and
	// `lenny_gateway_kms_signing_circuit_state`. F-10.2.6.
	w.kmsBreakerObs.SetMetrics(gwMetrics)
	// spec: §12.6 line 560 — register the scatter-gather duration histogram
	// and shard-count gauge and attach them to the store router so the §16
	// ScatterGatherSlowQuery alert has a series. The router is built before
	// the metrics registerer, so the collector is wired here. F-12.6.18.
	if w.scatterRouter != nil {
		scatterMetrics, err := storerouter.NewScatterMetrics(gwMetrics.Registerer())
		if err != nil {
			log.Fatalf("lenny-gateway: scatter-gather metrics: %v", err)
		}
		w.scatterRouter.SetScatterMetrics(scatterMetrics)
	}
	// spec: §13.3 line 595 — NTP drift self-monitor. The source returns
	// the clockinject-injected offset for v1 (zero in production unless
	// an operator wires a real adjtimex/chrony probe). /healthz and any
	// downstream consumer (currently the embedded TokenService path,
	// which lives in lenny-token-service) consult driftMonitor.Degraded.
	// F-13.3.5.
	driftMonitor := driftmonitor.New(func() time.Duration {
		off, _ := clockinject.Offset()
		return off
	}, gwMetrics)
	// spec: §10.1 — dual-store degraded-mode monitor. It is active only
	// when both coordination stores are wired; an in-memory / single-store
	// dev posture has no "both down" condition to detect. The monitor
	// probes Postgres and Redis on a short cadence and, on detecting both
	// unreachable, pins lenny_dual_store_unavailable=1, broadcasts a
	// PLATFORM_DEGRADED SSE event to every active client stream, and gates
	// session.create with 503 + Retry-After: 10 (via DualStore on the
	// session-server Options). The per-replica dualStoreUnavailableMaxSeconds
	// countdown is anchored at detection. F-10.1.3.
	var dsMonitor *dualstore.Monitor
	if w.pgPool != nil && w.redisClient != nil {
		pgPoolRef := w.pgPool
		redisRef := w.redisClient
		dsMonitor = &dualstore.Monitor{
			PostgresProbe: func(ctx context.Context) bool {
				pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
				defer cancel()
				return pgPoolRef.Ping(pctx) == nil
			},
			RedisProbe: func(context.Context) bool {
				return redisconn.PingWithTimeout(redisRef, 2*time.Second) == nil
			},
			Gauge:          gwMetrics,
			Streams:        w.eventBus,
			MaxUnavailable: time.Duration(*dualStoreMaxSeconds) * time.Second,
			Logf:           func(format string, args ...any) { log.Printf(format, args...) },
		}
	}
	// §4.6.1: record fallback-claim skips on the gateway metrics registry.
	// Wired after gatewaymetrics.New() because the binder is constructed
	// earlier in the agent-namespace block.
	if w.podBinder != nil {
		w.podBinder.FallbackSkipped = gwMetrics.IncPodClaimFallbackSkipped
		// §5.2 line 519: record concurrent-mode slot-contention conflicts
		// on lenny_slot_assignment_conflict_total so operators can detect
		// pool under-sizing.
		w.podBinder.SlotConflict = gwMetrics.IncSlotAssignmentConflict
		// §5.2 line 12: record concurrent-workspace slot bind failures on
		// lenny_slot_failure_total (error_type, pool, k8s_pod_name).
		w.podBinder.SlotFailure = gwMetrics.IncSlotFailure
		// §5.2 line 521: record post-recovery slot-counter rehydration
		// events on lenny_slot_rehydration_total (pod, pool).
		w.podBinder.Rehydration = gwMetrics.IncSlotRehydration
		// §6.3 line 352 / §16.1 line 122: emit lenny_warmpool_claims_total
		// on each idle→claimed transition so deployers can read the
		// denominator of the SDK-warm demotion-rate ratio.
		w.podBinder.ClaimAccepted = gwMetrics.IncWarmpoolClaim
		// §6.1 line 34 / §6.3 line 352 / §16.1 line 121: emit
		// lenny_warmpool_sdk_demotions_total (the demotion-rate numerator)
		// and lenny_warmpool_sdk_demotion_duration_seconds (the SDK
		// teardown penalty) on each SDK-warm demotion.
		w.podBinder.SDKDemotion = gwMetrics.RecordSDKDemotion
	}
	// §16.1 lines 51, 53, 55: emit credential-lease assignment, lease
	// duration, and pool-utilization telemetry from the in-process
	// assignment service. The Token Service client path emits its own
	// §16.1 metrics on its registry.
	if w.inProcessAssign != nil {
		w.inProcessAssign.SetMetrics(gwMetrics)
	}
	// spec: §9.4 line 200 / §16.1 lines 151-154 — wire the MemoryStore
	// Observer once gatewaymetrics is ready. The §16.1 `backend` label
	// is the bound implementation tag (`postgres` for the pgvector
	// backend, `memory` for the in-process test backend). F-9.4.1 /
	// F-9.4.6.
	if w.memories != nil {
		obs := memoryStoreObserver{metrics: gwMetrics, backend: w.memoryBackendLabel}
		switch s := w.memories.(type) {
		case *memorystore.InMemory:
			s.SetObserver(obs)
		case *memorypg.Store:
			s.SetObserver(obs)
		}
	}
	// spec: §12.8 lines 743-758 — MemoryStore erasure preflight (stub
	// detection, defense-in-depth layer 2). Before serving traffic, seed a
	// probe memory under the reserved (__preflight__, __preflight_user__)
	// scope, erase it, and assert it does not survive. A backend whose
	// DeleteByUser / DeleteByTenant satisfies the interface but silently
	// no-ops makes the gateway refuse to start, so a GDPR erasure can never
	// report success while memories persist. Skipped when memory.enabled is
	// false (no store wired). F-9.4.3 / F-12.1.4 / F-12.2.10 / F-12.8.9.
	if w.memories != nil {
		preflightCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		preflightErr := memorystore.ValidateMemoryStoreErasure(preflightCtx, w.memories)
		cancel()
		if preflightErr != nil {
			log.Fatalf("FATAL: MemoryStore preflight failed — configured backend (%s) does not honor DeleteByUser; GDPR erasure would silently succeed while leaving memories in place (Section 12.8): %v", w.memoryBackendLabel, preflightErr)
		}
	}
	// §4.1 / §16.1: emit the per-replica capacity ceiling as a startup-set
	// gauge so the §16.5 GatewaySessionBudgetNearExhaustion alert can
	// divide active sessions by it. The spec requires both delivery_mode
	// values be reported per replica; until a separate
	// maxSessionsPerReplicaProxyMode setting exists, both labels carry
	// the same configured value so capacity-planning dashboards have a
	// non-NaN value for either mode.
	if *maxSessionsPerReplica <= 0 {
		log.Fatalf("lenny-gateway: --max-sessions-per-replica must be > 0 (got %d) (§4.1)", *maxSessionsPerReplica)
	}
	gwMetrics.SetMaxSessionsPerReplica("direct", *maxSessionsPerReplica)
	gwMetrics.SetMaxSessionsPerReplica("proxy", *maxSessionsPerReplica)

	// spec: §8.10 line 1103 + §16.5 OrphanTasksPerTenantHigh — publish the
	// configured maxOrphanTasksPerTenant cap so the alert's
	// `scalar(lenny_max_orphan_tasks_per_tenant)` denominator resolves to
	// the live ceiling. The Helm-driven flag is the source of truth; the
	// sessionserver constructor also receives the same value via the
	// Options.MaxOrphanTasksPerTenant field so the detach-cascade fallback
	// path and the alert evaluate against one shared cap. F-8.10.10 /
	// F-8.10.13.
	gwMetrics.SetMaxOrphanTasksPerTenant(*delegationMaxOrphanTasksPerTenant)

	// §4.4 line 254 — late-binding the checkpointer's duration
	// histogram emitter to the freshly-constructed gateway metrics.
	// The checkpointer is constructed before gwMetrics so the Sealer
	// can flow into the session-server, so the Metrics field is wired
	// here once the registry is live.
	if w.checkpointSvc != nil {
		w.checkpointSvc.Metrics = gwMetrics
	}

	// §12.5 ll. 282/303 — wire the artifact store's fail-closed T4
	// KMS-unavailable callback and its retry-exhausted upload-error
	// callback to the gateway metrics emitter, regardless of which
	// §17.9.3 backend (MinIO, S3, GCS, Azure) serves the bucket. Every
	// ErrClassificationControlViolation the store raises bumps
	// `lenny_checkpoint_storage_failure_total{reason="kms_unavailable"}`
	// so the CheckpointStorageUnavailable alert fires under the
	// outage; every retry-exhausted upload bumps
	// `lenny_artifact_upload_error_total` so the §16.5 MinIOUnavailable
	// alert fires from one source of truth. The handler also logs the
	// KMS rejection at INFO so operators see the tenant id without
	// spelunking through the bucket-side access logs. F-17.5.1.
	if sink, ok := w.objectStore.(artifactMetricsSink); ok {
		sink.SetOnKMSUnavailable(func(tenantID string) {
			gwMetrics.IncCheckpointKMSUnavailable()
			log.Printf("lenny-gateway: §12.5 ll. 303 CLASSIFICATION_CONTROL_VIOLATION: tenant=%s KMS key unavailable", tenantID)
		})
		sink.SetOnArtifactUploadError(func(tenantID, errorType string) {
			gwMetrics.IncArtifactUploadError(tenantID, errorType)
		})
	}
	// §12.9 line 1048 — the in-memory / filesystem backends reject a T4
	// tenant's write (they cannot envelope-encrypt at rest). Wire the
	// rejection to the tier_store_mismatch reason of the same
	// checkpoint-storage-failure counter so the misconfiguration is
	// visible to operators.
	if sink, ok := w.objectStore.(tierMismatchSink); ok {
		sink.SetOnTierStoreMismatch(func(tenantID string) {
			gwMetrics.IncCheckpointTierStoreMismatch()
			log.Printf("lenny-gateway: §12.9 line 1048 CLASSIFICATION_CONTROL_VIOLATION: tenant=%s workspaceTier requires envelope encryption but the artifact store is not configured for it (tier_store_mismatch)", tenantID)
		})
	}

	// §12.8 line 735 / §12.5 ll. 297 — the durable artifact_store
	// catalog reader and the startup T4 KMS probe are MinIO-specific
	// (the in-memory and cloud backends do not expose SetCatalog), so
	// they stay gated on the concrete MinIO store.
	if w.minioStore != nil {
		// §12.8 line 735 — wire the durable artifact_store catalog as
		// the legal-hold source of truth on DeleteBySession. The
		// in-memory legalHolds sync.Map remains a v1 fallback for the
		// catalog-less dev gateway; production reads the durable row.
		if w.artifactCatalog != nil {
			w.minioStore.SetCatalog(w.artifactCatalog)
			log.Printf("lenny-gateway: §12.8 line 735 durable legal-hold reader wired into MinIO blob store")
		}

		// §12.5 ll. 297 startup KMS probe: when at least one T4 tenant
		// is configured, probe a sample alias so a chronic
		// misconfiguration surfaces in startup logs. The gateway does
		// NOT fail startup — production may bring the gateway up before
		// every KMS alias is provisioned; the warning is the operator
		// signal.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			rows, err := w.tenants.List(ctx, tenantstore.ListFilter{})
			if err != nil {
				log.Printf("lenny-gateway: §12.5 startup T4 KMS probe: list tenants: %v", err)
				return
			}
			for _, t := range rows {
				if t.WorkspaceTier != tenantkms.WorkspaceTierT4 {
					continue
				}
				alias := tenantkms.AliasFor(t.ID)
				if _, perr := w.kmsProvider.CurrentKEKVersion(ctx, alias); perr != nil {
					log.Printf("lenny-gateway: §12.5 startup T4 KMS probe WARN: tenant=%s alias=%s unreachable: %v",
						t.ID, alias, perr)
				} else {
					log.Printf("lenny-gateway: §12.5 startup T4 KMS probe OK: tenant=%s alias=%s",
						t.ID, alias)
				}
				return // probe a single sample tenant only
			}
		}()
	}

	// §4.1 / §16.5: emit the configuration scalars the §16.5
	// GatewayNoHealthyReplicas and GatewayActiveStreamsHigh alert
	// expressions read via scalar(...). Each gauge is emitted per
	// replica; for replicaCount the spec recording rule sum() over
	// the fleet yields the fleet-wide ready-replica numerator.
	if *minReplicas <= 0 {
		log.Fatalf("lenny-gateway: --min-replicas must be > 0 (got %d) (§4.1 / §16.5)", *minReplicas)
	}
	if *streamCeiling <= 0 {
		log.Fatalf("lenny-gateway: --stream-ceiling must be > 0 (got %d) (§4.1 / §16.5)", *streamCeiling)
	}
	// spec: §10.4 line 389 — accepted range 64..4096 events.
	if *sessionEventReplayBufferDepth < 64 || *sessionEventReplayBufferDepth > 4096 {
		log.Fatalf("lenny-gateway: --session-event-replay-buffer-depth must be in [64, 4096] (got %d) (§10.4 line 389)", *sessionEventReplayBufferDepth)
	}
	gwMetrics.SetMinReplicas(*minReplicas)
	gwMetrics.SetStreamCeiling(*streamCeiling)
	gwMetrics.SetReplicaCount(1)
	// §16.4 / §16.5: emit the audit alert-support scalars so the
	// AuditSIEMNotConfigured and AuditRetentionLow expressions resolve.
	// siemConfigured is the suppression term (1 when audit.siem.endpoint
	// is set), retentionDays is the resolved §16.4 window, and
	// envProduction gates both alerts to production. F-16.4.9; F-16.4.10.
	gwMetrics.SetAuditSIEMConfigured(*auditSIEMEndpoint != "")
	gwMetrics.SetAuditRetentionDays(w.effectiveAuditRetentionDays)
	gwMetrics.SetEnvProduction(os.Getenv("LENNY_ENV") == "production")
	// spec: §12.3 line 99 — in production with T2 audit batching enabled
	// but no SIEM endpoint, there is no external durable copy to recover
	// buffered T2 events from on a crash. Warn at startup and emit the
	// AuditBatchingNoSIEM counter. F-12.3.15.
	if auditBatchingNoSIEM(os.Getenv("LENNY_ENV"), *auditBatchingEnabled, *auditSIEMEndpoint != "") {
		log.Printf("lenny-gateway: WARNING: Audit batching is enabled for T2 events but no SIEM is configured — buffered T2 audit events will be lost on gateway crash")
		gwMetrics.IncAuditBatchingNoSIEM()
	}
	// spec: §11.2.1 line 187 — emit the configured BillingCorrectionRateHigh
	// threshold as a startup-set scalar gauge so the alert expression in
	// pkg/alerting/rules can read it via scalar(lenny_billing_correction_rate_threshold).
	if *billingCorrectionRateThreshold < 0 || *billingCorrectionRateThreshold > 1 {
		log.Fatalf("lenny-gateway: --billing-correction-rate-threshold must be in [0, 1] (got %v) (§11.2.1 / §16.5)", *billingCorrectionRateThreshold)
	}
	gwMetrics.SetBillingCorrectionRateThreshold(*billingCorrectionRateThreshold)

	// spec: §12.6 line 683 / §16.5 — publish the operator-configured
	// EventBusPublishDropped per-minute threshold so the bundled alert's
	// scalar(lenny_event_bus_drop_alert_threshold) resolves to the
	// eventBus.dropAlertThreshold Helm value rather than a literal. A
	// non-positive value would make the alert fire on any drop, so it is
	// clamped to the spec default. F-12.6.23.
	if *eventBusDropAlertThreshold <= 0 {
		log.Fatalf("lenny-gateway: --eventbus-drop-alert-threshold must be a positive per-minute rate (got %d) (§12.6 line 683)", *eventBusDropAlertThreshold)
	}
	gwMetrics.SetEventBusDropAlertThreshold(float64(*eventBusDropAlertThreshold))

	// §12.3 line 76 — wire the billing_flush_pressure callback now that
	// the metric registry exists (the billing Pipeline was constructed
	// earlier). F-12.3.13.
	w.billingPipeline.SetFlushPressureHook(gwMetrics.IncBillingFlushPressure)
	// §12.3 line 123 — emit the configured Postgres sustained write-IOPS
	// ceiling so the §16.5 PostgresWriteSaturation alert resolves
	// scalar(lenny_postgres_write_ceiling_iops). F-12.3.8.
	if *postgresWriteCeilingIops <= 0 {
		log.Fatalf("lenny-gateway: --postgres-write-ceiling-iops must be > 0 (got %v) (§12.3 line 123)", *postgresWriteCeilingIops)
	}
	gwMetrics.SetPostgresWriteCeilingIops(*postgresWriteCeilingIops)
	// §12.3 line 101 — startup chain-continuity check. After Postgres is
	// reachable the gateway re-verifies the most recent
	// audit.startupChainCheckEntries rows of each tenant's hash chain,
	// emits lenny_audit_chain_integrity_total per tenant, and logs a WARN
	// per detected gap. It never refuses to start — a gap is a compliance
	// signal. The check reads audit_log from the instance it lives on —
	// the separate §12.3 billing/audit pool when configured. F-12.3.9 /
	// F-12.3.5.
	if w.pgPool != nil {
		chainPool := w.pgPool
		if w.billingAuditPool != nil {
			chainPool = w.billingAuditPool
		}
		runStartupChainContinuityCheck(context.Background(), chainPool, *auditStartupChainCheckEntries, gwMetrics)
	}

	// spec: §25.13 line 4737 / §16.5 — emit the configured Tier preset
	// for the §25.13 tier-dependent thresholds (gateway queue depth,
	// gateway p95 latency, credential pool utilisation). The
	// corresponding alert expressions read each value via scalar(...),
	// so a tier preset tightening the threshold flows through to the
	// bundled manifest without re-rendering the rule body. F-25.13.2.
	if *gatewayQueueDepthThreshold < 0 {
		log.Fatalf("lenny-gateway: --gateway-queue-depth-threshold must be >= 0 (got %v) (§25.13 line 4737)", *gatewayQueueDepthThreshold)
	}
	gwMetrics.SetGatewayQueueDepthThreshold(*gatewayQueueDepthThreshold)
	if *gatewayLatencyThresholdSeconds < 0 {
		log.Fatalf("lenny-gateway: --gateway-latency-threshold-seconds must be >= 0 (got %v) (§25.13 line 4737)", *gatewayLatencyThresholdSeconds)
	}
	gwMetrics.SetGatewayLatencyThresholdSeconds(*gatewayLatencyThresholdSeconds)
	if *credentialPoolLowThreshold < 0 || *credentialPoolLowThreshold > 1 {
		log.Fatalf("lenny-gateway: --credential-pool-low-threshold must be in [0, 1] (got %v) (§25.13 line 4737)", *credentialPoolLowThreshold)
	}
	gwMetrics.SetCredentialPoolLowThreshold(*credentialPoolLowThreshold)
	// §16.5 line 640: mirror the operator-configured burn-rate window
	// multipliers onto the lenny_slo_burn_rate_{fast,slow}_multiplier
	// gauges every burn-rate alert reads via scalar(...). Both must be
	// positive — a non-positive multiplier would make every burn-rate
	// alert fire continuously (ratio > 0 always exceeds it). F-16.5.3.
	if *sloBurnRateFastMultiplier <= 0 || *sloBurnRateSlowMultiplier <= 0 {
		log.Fatalf("lenny-gateway: --slo-burn-rate-fast-multiplier and --slo-burn-rate-slow-multiplier must be > 0 (got %v / %v) (§16.5 line 640)", *sloBurnRateFastMultiplier, *sloBurnRateSlowMultiplier)
	}
	gwMetrics.SetSLOBurnRateMultipliers(*sloBurnRateFastMultiplier, *sloBurnRateSlowMultiplier)

	// §4.1 extractionThresholds: read the configured per-subsystem
	// thresholds from LENNY_EXTRACTION_THRESHOLD_* env vars (rendered
	// by charts/lenny/templates/gateway-deployment.yaml from
	// gateway.extractionThresholds Helm values) and emit each one
	// on the lenny_gateway_extraction_threshold gauge so the values
	// used for an extraction decision are auditable against /metrics.
	extractionthreshold.FromEnv().Emit(gwMetrics)

	// §4.1 per-subsystem metric family. Register the
	// lenny_gateway_subsystem_{request_duration_seconds, queue_depth,
	// circuit_state, errors_total} vectors against the gateway's
	// shared private registry so a Subsystem with its DoObserved path
	// wired surfaces samples on /metrics. The §4.1 alerts in §16.5
	// (GatewaySubsystemCircuitOpen, GatewayQueueDepthHigh,
	// GatewayLatencyHigh) read these vectors via the `subsystem` label.
	subsystemMetrics, err := subsystem.NewMetrics(gwMetrics.Registerer())
	if err != nil {
		log.Fatalf("lenny-gateway: subsystem metrics: %v", err)
	}

	// The §11.7 per-tenant audit hash chain. With Postgres the chain is
	// durable (auditstore); otherwise it is in-memory and lost on
	// restart. Both the admin router and the §10.7 ExperimentRouter
	// rejection reporter commit events to it.
	var (
		auditSink             admin.AuditSink
		wireAudit             func(*admin.Router) *admin.Router
		auditAppender         policy.AuditAppender
		auditValidator        *auditscope.Validator
		ocsfTranslationStore  ocsf.TranslationStore
		siemDeliveryStore     siem.DeliveryStore
		auditBatchBuffer      *auditbatch.Buffer
		auditPruner           *auditretention.Pruner
		eventBusRetranscriber *eventbus.Retranscriber
		// auditOpsStore is the durable audit Store, hoisted so the §25.5
		// operational-event emitter (built further down, once Redis is
		// resolved) can be wired into the §16.7 ops-stream escalation path
		// via SetOpsStreamEmitter. F-25.5.18.
		auditOpsStore *auditstore.Store
	)
	if w.pgPool != nil {
		// spec: §11.7 item 3 line 368 — bound the per-tenant audit
		// advisory-lock acquisition with the operator-tunable
		// statement_timeout + jittered retry budget, and emit the
		// lenny_audit_lock_acquire_seconds / _concurrency_timeout_total
		// series the AuditLockContention alert reads.
		auditLockMetrics, err := auditstore.NewLockMetrics(gwMetrics.Registerer())
		if err != nil {
			log.Fatalf("lenny-gateway: audit lock metrics: %v", err)
		}
		// §12.3 R-03: the audit chain routes through the same StoreRouter
		// built above (non-nil whenever pgPool is). F-12.3.4 / F-12.6.1.
		pgAudit := auditstore.New(w.storeRouter,
			auditstore.WithLockConfig(auditstore.LockConfig{
				AcquireTimeoutMs: *auditLockAcquireTimeoutMs,
				MaxRetries:       *auditLockMaxRetries,
				RetryBaseMs:      *auditLockRetryBaseMs,
			}),
			auditstore.WithLockMetrics(auditLockMetrics),
			// §12.3 line 79: route synchronous audit writes onto the
			// dedicated sync write pool when one was opened. F-12.3.14.
			auditstore.WithSyncWritePool(w.auditSyncPool),
			// spec: §11.7 lines 430-435 (CMP-058) — route a platform-tenant
			// audit write that references a non-platform target_tenant_id to
			// the target tenant's regional platform-Postgres, failing closed
			// with PLATFORM_AUDIT_REGION_UNRESOLVABLE when the region cannot
			// be resolved. The storage.regions.<region>.postgresEndpoint map
			// is empty in the single-region default (Config.PlatformRegions),
			// so a target tenant with a dataResidencyRegion set but no
			// configured regional endpoint fails closed as the spec requires.
			// F-11.7.9.
			auditstore.WithPlatformAuditResidency(
				jwtaudit.PlatformTenantID,
				w.storeRouter,
				tenantResidencyLookup{tenants: w.tenants},
				gwMetrics,
			),
			// spec: §25.9 line 3710 — bound the cross-tenant audit
			// scatter-gather fan-out by the shared storeRouter scatter
			// config (max concurrency, per-shard + aggregate timeout). v1 is
			// single-shard so the bounds are inert until a multi-shard router
			// is deployed. F-25.9.11.
			auditstore.WithScatterConfig(storerouter.ScatterConfig{
				MaxConcurrency:   *scatterMaxConcurrency,
				PerShardTimeout:  time.Duration(*scatterPerShardTimeoutSeconds) * time.Second,
				AggregateTimeout: time.Duration(*scatterAggregateTimeoutSeconds) * time.Second,
			}))
		// §12.3 line 81: opt-in T2 audit-event batching. When enabled, the
		// non-PII cross_tenant_read worker receipts are buffered and
		// flushed in batches through the dedicated sync write pool instead
		// of one synchronous write each; T3/T4 PII audit events stay
		// synchronous. F-12.3.14.
		if *auditBatchingEnabled {
			auditBatchBuffer = auditbatch.New(pgAudit.AppendBatch, auditbatch.Config{
				FlushInterval: time.Duration(*auditFlushIntervalMs) * time.Millisecond,
				BatchSize:     *auditFlushBatchSize,
			}, nil)
			pgAudit.SetBatchBuffer(auditBatchBuffer)
		}
		// spec: §11.7 line 428 — guard the caller-driven audit-write
		// boundaries (the admin sink and the §4.8 policy-rejection sink)
		// with the write-time tenant-scope validator so a forged-tenant
		// row cannot be injected. Reads stay on the raw chain.
		auditValidator = auditscope.New(pgAudit, nil)
		auditSink = admin.NewAuditLogSink(auditValidator, nil)
		// spec: §25.9 lines 3668, 3709 — the Postgres-backed chain serves
		// the platform-admin cross-tenant scatter-gather and its 5-minute
		// result cache (opt-out via --audit-scatter-gather-cache-enabled).
		// The in-memory dev chain (below) has no scatter reader, so its
		// platform-admin no-tenantId query stays single-tenant. F-25.9.11.
		auditScatterCache := admin.NewMemScatterGatherCache(nil)
		wireAudit = func(rt *admin.Router) *admin.Router {
			return rt.WithAuditLog(pgAudit).
				WithAuditScatter(pgAudit).
				WithScatterGatherCache(auditScatterCache, *auditScatterCacheEnabled)
		}
		// The §11.7 `interceptor.rejected` policy-rejection rows share
		// the durable Postgres-backed per-tenant hash chain.
		auditAppender = pgAudit
		// Hoist the Store so the §25.5 operational-event escalation
		// emitter can be wired once Redis is resolved. Every escalating
		// §16.7 audit event funnels through Store.Append (the admin sink,
		// the policy-rejection sink, and the §25.9 audit-maintenance API
		// all reach this chain), so a single hook covers them. F-25.5.18.
		auditOpsStore = pgAudit
		// The auditstore drives the §11.7 OCSF translation state machine
		// (ocsf_translation_state). Hoisted so the OCSF translator wired
		// below reads pending rows from the durable chain. F-11.7.1.
		ocsfTranslationStore = pgAudit
		// The same durable chain backs the §12.3 SIEM outbox forwarder:
		// it tails committed audit_log rows past the per-tenant delivery
		// high-water mark in siem_delivery_state and checkpoints each
		// SIEM-acknowledged row. F-12.3.6.
		siemDeliveryStore = pgAudit
		// §16.4 lines 378-382 audit-retention pruner: a leader-elected
		// sweep deletes audit rows past audit.retentionDays, holding
		// gdpr.* erasure receipts under the longer audit.gdprRetentionDays
		// floor and any SIEM-undelivered row behind the delivery guard.
		// The forced-drop override records audit.partition_drop_forced on
		// the platform chain through pgAudit.Append. F-11.7.17.
		auditPruner = auditretention.New(
			pgAudit,
			auditPruneTenants{w.tenants},
			func(ctx context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) error {
				_, err := pgAudit.Append(ctx, tenantID, eventType, payload, at)
				return err
			},
			auditretention.Options{
				RetentionDays:     w.effectiveAuditRetentionDays,
				GDPRRetentionDays: *gdprRetentionDays,
				SIEMConfigured:    *auditSIEMEndpoint != "",
				Interval:          time.Duration(*auditRetentionPruneIntervalSeconds) * time.Second,
				Clock:             clockinject.Now,
				// spec: §16.4 line 378 — surface the
				// lenny_audit_partition_drop_blocked gauge so the §16.5
				// AuditPartitionDropBlocked alert evaluates when the SIEM
				// delivery guard holds a partition past its retention TTL.
				Metrics: auditRetentionMetrics{gwMetrics},
			},
		)
		// spec: §12.6 lines 685-689 — the EventBus retranscribe worker, the
		// durable correctness layer that re-publishes every audit row whose
		// first EventBus publish failed (eventbus_publish_state IN
		// ('failed','retry_pending')) even when the in-memory replay buffer
		// was lost. It is constructed only when both a durable audit chain
		// (pgPool) and a real pub/sub substrate (securityBus / Redis) exist:
		// with no Redis there is no EventBus to re-publish to. The worker
		// drives the §12.6 RedisEventBus as its publisher, reads the failed
		// rows from the auditstore RetranscribeStore, and sweeps every
		// eventBus.retryInterval. F-12.6.22 / F-12.6.23.
		if w.securityBus != nil {
			eventBusMetrics, err := eventbus.NewPromMetrics(gwMetrics.Registerer())
			if err != nil {
				log.Fatalf("lenny-gateway: §12.6 EventBus metrics: %v", err)
			}
			eventBusPublisher := eventbus.NewRedisEventBus(
				w.securityBus, eventBusMetrics,
				eventbus.WithDuplicateInjectionFactor(*eventBusDuplicateInjectionFactor),
			)
			eventBusRetranscriber = eventbus.NewRetranscriber(
				pgAudit, eventBusPublisher,
				eventbus.RetranscribeConfig{
					RetryInterval:    time.Duration(*eventBusRetryIntervalSeconds) * time.Second,
					MaxRetryAttempts: *eventBusMaxRetryAttempts,
				},
				eventBusMetrics,
			)
		}
	} else {
		auditChains := audit.NewChainSet()
		auditValidator = auditscope.New(auditscope.NewChainSetChain(auditChains, nil), nil)
		auditSink = admin.NewAuditLogSink(auditValidator, nil)
		wireAudit = func(rt *admin.Router) *admin.Router { return rt.WithAuditChains(auditChains) }
		// In-memory chain — lost on restart, used by the minimal gateway.
		auditAppender = policy.NewChainSetAppender(auditChains, nil)
	}

	// §10.3 JWT signing-key rotation audit. Each Rotate or
	// RetireExpired call against the rotatingVerifier emits one
	// `platform.jwt_signing_key_rotated` audit row through this
	// observer; the observer shares the per-tenant chain backend
	// chosen above and writes to the platform tenant.
	w.rotatingVerifier.SetObserver(jwtaudit.NewObserver(auditAppender))

	// spec: §11.7 item 4 + Wire Format — wire the OCSF translator and
	// SIEM forwarder into the gateway binary. The translator drains the
	// auditstore's ocsf_translation_state rows, serializes each to the
	// canonical OCSF v1.1.0 wire form, and multicasts to its sink; the
	// SIEM forwarder is that sink (it implements ocsf.Sink). When a SIEM
	// endpoint is configured the gateway validates connectivity at startup
	// and refuses to start until a test event is acknowledged; at runtime
	// the §25.3 health API reports the `siem` component degraded once the
	// delivery failure rate crosses the threshold. With no SIEM the
	// translator still advances the per-row state machine so audit rows do
	// not pin in `pending`. F-11.7.1 / F-11.7.11 / F-11.7.16.
	var (
		ocsfTranslator    *ocsf.Translator
		ocsfOutbox        *siem.Outbox
		siemHealthChecker health.Checker
	)
	ocsfCfg := ocsf.TranslationConfig{
		RetryInterval: time.Duration(*auditOCSFRetryIntervalSeconds) * time.Second,
		MaxAttempts:   *auditOCSFMaxAttempts,
		BatchSize:     *auditOCSFBatchSize,
	}
	// §12.3 line 97: emit the configured SIEM delivery-lag threshold so
	// AuditSIEMDeliveryLag compares lenny_audit_siem_delivery_lag_seconds
	// against an operator-tunable scalar rather than a literal. F-12.3.17.
	gwMetrics.SetSIEMMaxDeliveryLagSeconds(float64(*auditSIEMMaxDeliveryLagSeconds))
	if *auditSIEMEndpoint != "" {
		siemMetrics := siem.NewCountingMetrics()
		forwarder := siem.NewForwarder(
			siem.NewHTTPSink(siem.HTTPSinkOptions{
				Endpoint: *auditSIEMEndpoint,
				Secret:   *auditSIEMSecret,
			}),
			siem.ForwarderConfig{},
			siemMetrics,
		)
		validateCtx, cancelValidate := context.WithTimeout(context.Background(), 10*time.Second)
		if err := forwarder.ValidateConnectivity(validateCtx); err != nil {
			cancelValidate()
			log.Fatalf("lenny-gateway: §11.7 SIEM startup connectivity validation failed (the gateway refuses to start until the SIEM endpoint acknowledges a test event): %v", err)
		}
		cancelValidate()
		siemHealthChecker = backends.SIEM(forwarder, siemMetrics, *auditSIEMFailureThresholdPercent, "siem")
		if siemDeliveryStore != nil {
			// §12.3 line 97: durable Postgres chain → the SIEM egress is
			// the outbox / CDC forwarder. It tails committed audit_log
			// rows past the siem_delivery_state high-water mark and
			// advances the mark only after the SIEM acknowledges each
			// record, so a crash after a Postgres commit but before SIEM
			// delivery replays the row instead of losing it. The OCSF
			// translator no longer pushes to the SIEM (sink = nil) — it
			// only advances ocsf_translation_state — so the two paths do
			// not double-deliver. F-12.3.6.
			ocsfOutbox = siem.NewOutbox(siemDeliveryStore, forwarder,
				siem.OutboxConfig{PollInterval: time.Duration(*auditSIEMPollIntervalSeconds) * time.Second},
				gwMetrics)
			log.Printf("lenny-gateway: §12.3 SIEM outbox forwarder validated connectivity to %s; tailing committed audit rows (poll %ds)", *auditSIEMEndpoint, *auditSIEMPollIntervalSeconds)
			if ocsfTranslationStore != nil {
				ocsfTranslator = ocsf.NewTranslator(ocsfTranslationStore, nil, ocsfCfg, ocsfMetricsAdapter{metrics: gwMetrics})
			}
		} else if ocsfTranslationStore != nil {
			// No durable chain (in-memory, minimal gateway): there is no
			// audit_log table to tail, so fall back to the push-based
			// translator → SIEM forwarder path. F-11.7.1.
			log.Printf("lenny-gateway: §11.7 SIEM forwarder validated connectivity to %s; OCSF audit egress active (push mode, no durable chain)", *auditSIEMEndpoint)
			ocsfTranslator = ocsf.NewTranslator(ocsfTranslationStore, forwarder, ocsfCfg, ocsfMetricsAdapter{metrics: gwMetrics})
		}
	} else if ocsfTranslationStore != nil {
		ocsfTranslator = ocsf.NewTranslator(ocsfTranslationStore, nil, ocsfCfg, ocsfMetricsAdapter{metrics: gwMetrics})
	}

	// §12.8: re-surface any tenant that combines billingErasurePolicy
	// exempt with a regulated compliance profile so the retention
	// posture cannot silently persist across redeployments.
	if err := admin.EmitBillingErasureExemptRegulatedStartup(
		context.Background(), w.tenants, auditSink, nil,
	); err != nil {
		log.Printf("lenny-gateway: WARNING: billing-erasure-exempt startup scan: %v", err)
	}

	// environments backs the §10.6 admin environment CRUD, the
	// transparent filtering on lenny/discover_agents, and the §9.1
	// GET /v1/runtimes discovery surface.
	var environments environmentstore.Store = environmentstore.NewMemory()
	if w.pgPool != nil {
		environments = environmentpg.New(w.pgPool)
	}
	// §4 runtime tenant-access registry, shared by the admin
	// tenant-access endpoints and the §5.1 internal meta-fetch endpoint.
	var tenantAccess tenantaccessstore.Store = tenantaccessstore.NewMemory()
	if w.pgPool != nil {
		tenantAccess = tenantaccesspg.New(w.pgPool)
	}

	// §25.3 / §25.5 operational-event emitter, shared by the gateway
	// subsystems that emit and the admin event-buffer query endpoint.
	// Always keep a local buffer — the §25.3 buffer endpoint reads it
	// and the §25.5 fall-back path serves the same buffer when Redis is
	// unreachable. When Redis is wired, every emit also lands on the
	// §25.5 platform-scoped stream ops:events:stream so lenny-ops and
	// the controllers share the same logical event source.
	// §25.3 lines 705-710 / 766-772: the operational-event emission and
	// buffer metrics, registered on the gateway's Prometheus registry.
	opsEventsMetrics, err := events.NewMetrics(gwMetrics.Registerer())
	if err != nil {
		log.Fatalf("lenny-gateway: §25.3 ops-events metrics: %v", err)
	}
	// §25.3 line 748: the optional on-disk nonce checkpoint so the
	// eventKey stays unique across a restart with a pinned replica id.
	var nonceCheckpoint *events.NonceCheckpoint
	if *opsNonceCheckpointPath != "" {
		nonceCheckpoint = &events.NonceCheckpoint{Path: *opsNonceCheckpointPath}
	}
	opsEmitErrLogger := func(emitErr error) {
		log.Printf("lenny-gateway: §25.3 ops-event emit: %v", emitErr)
	}
	opsEventBuffer := events.NewEventBuffer(0, events.WithBufferMetrics(opsEventsMetrics))
	emitterOpts := []events.EmitterOption{
		events.WithMetrics(opsEventsMetrics),
		events.WithEmitErrorLogger(opsEmitErrLogger),
	}
	if nonceCheckpoint != nil {
		emitterOpts = append(emitterOpts, events.WithNonceCheckpoint(*nonceCheckpoint))
	}
	var opsEmitter events.EventEmitter = events.NewEmitter(opsEventBuffer, w.replica, emitterOpts...)
	if w.redisClient != nil {
		opsEmitter = events.NewStreamEmitter(events.StreamEmitterOptions{
			// §12.4 Cache/Pub-Sub concern: ops event stream fan-out.
			Client:          w.concernRedis.For(storerouter.RedisConcernCachePubSub),
			Buffer:          opsEventBuffer,
			Source:          "//lenny.dev/gateway/" + w.replica,
			ReplicaID:       w.replica,
			Metrics:         opsEventsMetrics,
			NonceCheckpoint: nonceCheckpoint,
			OnError:         opsEmitErrLogger,
		})
		log.Printf("lenny-gateway: §25.5 operational events streaming to Redis %s", events.DefaultStreamKey)
	}
	// Wire the §16.7 / §25.5 operational-event escalation path: the
	// durable audit Store routes the §16.7 ops-stream subset of audit
	// events onto the operational event stream as audit-bearing
	// CloudEvents (datacontenttype application/ocsf+json). F-25.5.18.
	if auditOpsStore != nil {
		auditOpsStore.SetOpsStreamEmitter(opsEmitter, w.replica)
	}

	// §4.9 credential-pool registry, shared by the admin credential-pool
	// CRUD and the §14 gitClone auth host-to-pool binding check.
	var credentialPools credentialpoolstore.Store = credentialpoolstore.NewMemory()
	if w.pgPool != nil {
		credentialPools = credentialpoolpg.New(w.pgPool)
	}

	// §14 line 95: the VCS credential resolver materializes a gitClone
	// source's short-lived token on the gateway (binding the URL host to
	// one of the tenant's VCS credential pools, then reading the token
	// from the credential's Kubernetes Secret) so the ref-pinning
	// ls-remote and the clone authenticate without the pod ever seeing
	// the raw credential. It needs a cluster client to read Secrets; in a
	// dev/in-memory posture (no cluster client) it stays nil and an
	// authenticated gitClone fails with a clear "no resolver wired"
	// error. The Secret key defaults to `token`, the §4.9 github
	// materializedConfig field.
	var vcsCreds vcscred.Resolver
	if w.clusterClient != nil {
		vcsCreds = &vcscred.StoreResolver{
			Pools:   credentialPools,
			Secrets: connectorsecret.NewKubeResolver(w.clusterClient, "token"),
		}
		if w.podBinder != nil {
			w.podBinder.VCSCreds = vcsCreds
		}
	}

	// §10.2 tenant custom-role registry, shared by the admin custom-role
	// CRUD and the §10.2 session-endpoint authorization gate (so a
	// custom role granting manage_own_sessions / read_own_sessions is
	// honored on the session endpoints as well as the admin surface).
	var customRoles customrolestore.Store = customrolestore.NewMemory()
	if w.pgPool != nil {
		customRoles = customrolepg.New(w.pgPool)
	}

	var usage usagestore.Store = usagestore.NewMemory()
	if w.pgPool != nil {
		usage = usagepg.New(w.pgPool, usagepg.WithReadPool(w.readPool))
	}

	// spec: §8.8 lines 897-917 — the per-session token accumulator the
	// §4.9 proxy folds proxy-extracted counts into; the §8.8 TaskResult
	// usage / treeUsage rollups read it at settle time.
	var sessionUsage sessionusage.Store = sessionusage.NewMemory()
	if w.pgPool != nil {
		sessionUsage = sessionusagepg.New(w.pgPool, sessionusagepg.WithReadPool(w.readPool))
	}
	// spec: §8.8 lines 897-917 — the shared usage Builder both the
	// sessionserver materialization path and the MCP lenny/await_children
	// path use to stamp usage / treeUsage on every TaskResult.
	taskUsageBuilder := resultrollup.New(w.sessions, sessionUsage, w.treeArchive, clockinject.Now)

	// spec: §4.8 / §11.2 — build the §4.8 policy interceptor chain (the
	// built-in Auth, DelegationPolicy, Retry, and Quota evaluators) and the
	// §11.2 / §12.4 quota surfaces. The step records the chain, the §8.3
	// maxInputSize resolver holder, the policy audit sink, and the quota
	// counter, tenant-limits resolver, budget tracker, fail-open accumulator,
	// and replica-count source on the accumulator; re-alias them so the
	// build steps below read them unchanged.
	w.buildPolicyChain(auditAppender, auditValidator)
	policyChain := w.policyChain
	maxInputResolver := w.maxInputResolver
	policyAuditSink := w.policyAuditSink
	quotaCounter := w.quotaCounter
	tenantLimits := w.tenantLimits
	quotaFailOpenAccum := w.quotaFailOpenAccum
	failOpenReplicas := w.failOpenReplicas

	// ----- §11.2 token-usage Postgres checkpoint + §24.6 reconcile -----
	// The Service persists each active (tenant, user) window total and the
	// per-tenant rollup to the token_usage_checkpoint table on the
	// quotaSyncIntervalSeconds cadence (§11.2 line 44), writes a final
	// checkpoint on session completion, and restores counters to
	// MAX(redis_current, postgres_checkpoint) on Redis recovery (§11.2 line
	// 48) and on the operator-driven §24.6 reconcile. Active only when the
	// Redis quota counter, the Postgres pool, and the SessionStore are all
	// wired; otherwise the §24.6 endpoint stays the 503 stub and the final
	// write is a no-op. F-11.2.4 / F-24.6.1 / F-24.6.2 / F-24.6.3.
	var quotaCheckpointSvc *quotacheckpoint.Service
	if quotaCounter != nil && w.pgPool != nil && w.sessions != nil {
		limitLookup := tenantLimits
		quotaCheckpointSvc = &quotacheckpoint.Service{
			Store:    quotacheckpointpg.New(w.pgPool),
			Subjects: quotacheckpoint.SessionSubjectLister{Sessions: w.sessions, Tenants: (tenantsLister{w.tenants}).ListTenants},
			Periods: quotacheckpoint.PeriodResolverFunc(func(ctx context.Context, tenantID string) (quota.ResetPeriod, error) {
				lim, err := limitLookup.LookupLimits(ctx, tenantID)
				if err != nil {
					return "", err
				}
				return lim.Period, nil
			}),
			Reader:   quotaCounter,
			Restorer: quotaCounter,
			// §12.4 source (2): fold the in-memory fail-open accumulator into
			// the MAX rule on the Redis-recovery edge. F-12.4.20.
			FailOpen: quotaFailOpenAccum,
			Tenants: quotacheckpoint.TenantExistsFunc(func(ctx context.Context, tenantID string) (bool, error) {
				if _, err := w.tenants.Get(ctx, tenantID); err != nil {
					if errors.Is(err, tenantstore.ErrNotFound) {
						return false, nil
					}
					return false, err
				}
				return true, nil
			}),
			Metrics: gwMetrics,
			Now:     clockinject.Now,
			Logf:    log.Printf,
		}
	}

	// §10.3 NET-063: the shared interceptor peer-validation context for
	// every gateway→interceptor dial below. The deny list is the same
	// per-replica set the propagator drives (F-10.3.7); the observer is
	// the §16.1 handshake histogram. F-10.3.3.
	mtlsDeny := mtlsdenylist.New()
	interceptorID := interceptorIdentity{
		trustDomain: *spiffeTrustDomain,
		namespaces:  splitAndTrim(*interceptorNamespaces),
		denyList:    mtlsDeny,
		observe:     gwMetrics.ObserveInterceptorMTLSHandshake,
	}

	// §4.8 line 1019: register each deployer-supplied external
	// interceptor. The gateway dials the service's endpoint, builds the
	// generated RequestInterceptor client, and registers an External on
	// the named phase. Registration applies the reserved-priority
	// ceiling and the PreAuth restriction, so a misconfigured priority or
	// phase fails fast at startup rather than silently bypassing policy.
	for _, raw := range externalInterceptors {
		spec, err := interceptor.ParseExternalSpec(raw)
		if err != nil {
			log.Fatalf("lenny-gateway: --external-interceptor: %v", err)
		}
		conn, err := dialInterceptor(spec.Endpoint, *externalInterceptorTLSCert, *externalInterceptorTLSKey, *externalInterceptorCA, interceptorID)
		if err != nil {
			log.Fatalf("lenny-gateway: dial external interceptor %q at %s: %v", spec.Name, spec.Endpoint, err)
		}
		if _, err := policyChain.RegisterExternal(spec.Phase, interceptor.ExternalConfig{
			Name:       spec.Name,
			Endpoint:   spec.Endpoint,
			Priority:   spec.Priority,
			FailPolicy: spec.FailPolicy,
			Timeout:    spec.Timeout,
			Client:     interceptorv1.NewRequestInterceptorClient(conn),
		}); err != nil {
			log.Fatalf("lenny-gateway: register external interceptor %q on %s: %v", spec.Name, spec.Phase, err)
		}
		log.Printf("lenny-gateway: §4.8 external interceptor %q registered on %s (endpoint %s, priority %d)",
			spec.Name, spec.Phase, spec.Endpoint, spec.Priority)
	}

	// §4.8 line 1070: the GuardrailsInterceptor is the built-in hook for
	// a deployer-wired external content classifier, disabled by default.
	// When --guardrails-classifier is set the gateway dials the
	// classifier, wraps it at the fixed priority 400 across the guardrails
	// phases, and registers it; the spec's phase/priority fields are
	// ignored because §4.8 fixes them for this built-in.
	if *guardrailsClassifier != "" {
		spec, err := interceptor.ParseExternalSpec(*guardrailsClassifier + ",phase=" + string(interceptor.PhasePreLLMRequest))
		if err != nil {
			log.Fatalf("lenny-gateway: --guardrails-classifier: %v", err)
		}
		conn, err := dialInterceptor(spec.Endpoint, *externalInterceptorTLSCert, *externalInterceptorTLSKey, *externalInterceptorCA, interceptorID)
		if err != nil {
			log.Fatalf("lenny-gateway: dial guardrails classifier %q at %s: %v", spec.Name, spec.Endpoint, err)
		}
		classifier, err := interceptor.NewExternal(interceptor.ExternalConfig{
			Name:       spec.Name,
			Endpoint:   spec.Endpoint,
			Priority:   policy.GuardrailsInterceptorPriority,
			FailPolicy: spec.FailPolicy,
			Timeout:    spec.Timeout,
			Client:     interceptorv1.NewRequestInterceptorClient(conn),
		})
		if err != nil {
			log.Fatalf("lenny-gateway: build guardrails classifier %q: %v", spec.Name, err)
		}
		if err := policy.RegisterGuardrails(policyChain, policy.NewGuardrailsInterceptor(classifier)); err != nil {
			log.Fatalf("lenny-gateway: register GuardrailsInterceptor: %v", err)
		}
		log.Printf("lenny-gateway: §4.8 GuardrailsInterceptor enabled (classifier %q at %s, priority %d)",
			spec.Name, spec.Endpoint, policy.GuardrailsInterceptorPriority)
	}

	// §4.1 Upload Handler subsystem boundary. The Subsystem gates the
	// POST /v1/sessions/{id}/upload handler through a per-replica
	// breaker and concurrency semaphore so a saturated upload queue
	// cannot consume goroutines the Stream Proxy or MCP Fabric need.
	// The configured maxConcurrent matches the §4.1 extraction-
	// threshold default (uploadHandler.activeConcurrent: 200); the
	// breaker's FailureThreshold uses the package default until an
	// operator-tunable knob lands.
	uploadSubsystem := &subsystem.Subsystem{
		Name:    "upload_handler",
		Breaker: &subsystem.Breaker{},
		Limiter: &subsystem.Limiter{MaxConcurrent: int(extractionthreshold.FromEnv().UploadHandlerActiveConcurrent)},
	}
	// §16.1: the upload-handler-specific byte-count and queue-depth
	// metrics (lenny_upload_bytes_total, lenny_upload_queue_depth) that
	// the unified per-subsystem family does not carry under their
	// catalogued names. F-13.4.12.
	uploadMetrics, err := sessionserver.NewUploadMetrics(gwMetrics.Registerer())
	if err != nil {
		log.Fatalf("lenny-gateway: upload metrics: %v", err)
	}
	// §7.4 line 448 / §13.4 line 652 — archive extraction runs inside the
	// gateway's §4.1 Upload Handler subsystem (never the pod). Share the
	// same subsystem gate that bounds the upload HTTP path so a hostile
	// archive's decompression cannot starve session attachment, and feed
	// the §16.1 lenny_upload_extraction_aborted_total counter from the
	// binder's extraction abort path. F-7.4.1, F-13.4.1, F-7.4.11.
	if w.podBinder != nil {
		w.podBinder.UploadGate = uploadSubsystem
		w.podBinder.ExtractionAbort = uploadMetrics.AddExtractionAbort
	}

	// §8.5 lenny/request_input pending-call registry. Shared across the
	// sessionserver REST surface and the MCP tools so a REST
	// POST /v1/sessions/{id}/messages with `inReplyTo` resolves the
	// blocked tool call the MCP `lenny/request_input` registered.
	// spec: §7.2 line 317 (path 1); F-7.2.14.
	inputWaits := inputwait.NewRegistry()

	// spec: §10.7 lines 831, 1096 / §12.4 (`t:{tenant}:exp:{exp}:sticky:*`) —
	// the `sticky: user` variant-assignment cache. Backed by the cache/pubsub
	// Redis concern; nil without Redis, in which case the ExperimentRouter
	// re-evaluates every experiment fresh (the §12.4 fail-open path) and the
	// PATCH flush is a no-op. F-12.4.7 / F-10.7.6.
	var (
		sessionStickyCache sessionserver.StickyCache
		adminStickyFlusher admin.StickyFlusher
		// erasureSticky captures the concrete sticky cache for the §12.8
		// step-4 experiment-sticky-assignment erasure wiring; nil without
		// Redis (the cache itself is absent in that posture).
		erasureSticky *experimentsticky.RedisCache
	)
	if w.redisClient != nil {
		stickyCache := experimentsticky.NewRedis(
			w.concernRedis.For(storerouter.RedisConcernCachePubSub),
			experimentsticky.WithInvalidationRecorder(gwMetrics),
		)
		// Assign to the interface variables only when constructed so the
		// nil-Redis posture leaves a genuine nil interface (not a typed-nil
		// *RedisCache the consumers would call methods on).
		sessionStickyCache = stickyCache
		adminStickyFlusher = stickyCache
		erasureSticky = stickyCache
	}

	// §10.7 lines 779-782: the built-in OpenFeature SDK providers
	// (launchdarkly, statsig, unleash) linked into the gateway binary. The
	// cache constructs one vendor OpenFeature client per distinct tenant
	// targeting config and reuses it across sessions; OFREP-targeted
	// experiments do not touch it. F-10.7.3.
	experimentProviders := experimentprovider.NewCache()

	// spec: §14 lines 108-150 — the session-completion webhook subsystem.
	// The SSRF validator enforces the §14 callbackUrl rules at admission;
	// the seal/open closures KMS-envelope-encrypt the callbackSecret under
	// the same per-tenant KEK alias ("tenant:{id}") as credential pool
	// secrets; the dispatcher delivers from an isolated worker pool with
	// the §14 retry budget and clears the sealed secret when a delivery
	// settles. F-14.1.11 / F-15.1.11.
	callbackValidator := sessioncallback.NewValidator(splitCSV(*callbackURLAllowedDomains), nil)
	callbackSeal := func(ctx context.Context, tenantID string, plaintext []byte) ([]byte, error) {
		c, err := envelope.New(w.kmsProvider, "tenant:"+tenantID)
		if err != nil {
			return nil, err
		}
		sealed, err := c.Seal(ctx, plaintext)
		if err != nil {
			return nil, err
		}
		return envelope.Encode(sealed)
	}
	callbackOpener := func(ctx context.Context, tenantID string, sealed []byte) ([]byte, error) {
		c, err := envelope.New(w.kmsProvider, "tenant:"+tenantID)
		if err != nil {
			return nil, err
		}
		s, err := envelope.Decode(sealed)
		if err != nil {
			return nil, err
		}
		return c.Open(ctx, s)
	}
	callbackFinalize := func(ctx context.Context, tenantID, sessionID string, undelivered *sessioncallback.DeliveryRecord) error {
		_, err := w.sessions.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
			// spec: §14 line 139 — clear the sealed secret once the
			// session is terminal and the delivery has settled.
			row.CallbackSecret = nil
			if undelivered != nil {
				row.WebhookEvents = append(row.WebhookEvents, sessionstore.WebhookEventRecord{
					EventID:     undelivered.EventID,
					EventType:   undelivered.EventType,
					CallbackURL: undelivered.CallbackURL,
					Body:        undelivered.Body,
					Attempts:    undelivered.Attempts,
					LastError:   undelivered.LastError,
					LastStatus:  undelivered.LastStatus,
					FailedAt:    undelivered.FailedAt,
				})
			}
			return nil
		})
		return err
	}
	callbackDispatcher := sessioncallback.NewDispatcher(sessioncallback.Config{
		GatewayID: w.replica,
		Opener:    callbackOpener,
		Finalizer: callbackFinalize,
	})

	// spec: §11.2 line 44 — the mid-session token-budget enforcer. The
	// §4.9 LLM-proxy recorder feeds it each session's cumulative
	// proxy-recorded tokens; on exhaustion the terminator transitions the
	// session to `expired` (§7.1 line 175) and the pre-flight gate rejects
	// further proxied requests with BUDGET_EXHAUSTED (§8.10 line 1108). The
	// terminator's terminal hook is set after the session server exists
	// (the same deferred wiring sessionAdminAdapter uses).
	budgetTerminator := &budgetSessionTerminator{store: w.sessions}
	sessionBudgetEnforcer := sessionbudget.New(budgetTerminator,
		func(tenantID, _ string, _, _ int64) { gwMetrics.IncSessionBudgetExceeded(tenantID) })

	// §8.6 GatewayControl lease-extension budget state. Created here, when
	// the GatewayControl listener is enabled via --grpc-addr, so the same
	// per-tree denial flags are shared between the ExtendLease handler and
	// the §15.1 line 868 admin extension-denial clear endpoint — the admin
	// handler must mutate the very state the handler reads. The session
	// server registers each root tree (RegisterTree) and the delegation
	// Service registers each child (AddSession/SetParentLease), so a later
	// ExtendLease resolves the tree instead of failing ErrSessionNotFound.
	// F-8.6.8 / F-15.3.5.
	var leaseBudgets *leasecontrol.MemoryBudgetSource
	if *grpcAddr != "" {
		leaseBudgets = leasecontrol.NewMemoryBudgetSource()
		// §8.6 lines 730-733 durability: when Postgres is configured the
		// extension-denied flag, cool-off expiry, and grant counters are
		// persisted to delegation_tree_budget through the denialpg store,
		// so a coordinator handoff or gateway restart cannot bypass a
		// user's rejection. Without Postgres (the dev/Embedded path) the
		// denial stays in-memory. F-8.6.5.
		if w.pgPool != nil {
			leaseBudgets = leaseBudgets.WithDenialStore(denialpg.New(w.pgPool))
		}
	}
	// §8.6 lines 660-678: resolve the deployment-level lease-extension
	// defaults the root tree's budget ceiling is registered with. nil
	// leaseBudgets (no GatewayControl listener) leaves leaseRegistrar
	// unset, so RegisterTree is never called. F-15.3.5.
	leaseExtDefaults := sessionserver.LeaseExtensionDefaults{
		DeploymentBudget:    *leaseDefaultBudget,
		DeploymentMaxBudget: *leaseMaxBudget,
		ApprovalMode:        leasecontrol.ApprovalMode(*leaseDefaultApproval),
		SuccessCoolOff:      time.Duration(*leaseCoolOffSec) * time.Second,
		RejectionCoolOff:    time.Duration(*leaseRejectionCoolOffSec) * time.Second,
		AutoMaxPerMinute:    *leaseAutoMaxPerMin,
	}
	var sessionLeaseRegistrar sessionserver.LeaseTreeRegistrar
	var childLeaseRegistrar delegation.LeaseChildRegistrar
	if leaseBudgets != nil {
		sessionLeaseRegistrar = leaseBudgets
		childLeaseRegistrar = leaseBudgets
	}

	// spec: §6.2 lines 273-300 / §11.3 line 199 — the activity stamper
	// records qualifying agent activity (agent_output / tool_use events,
	// await_children polls, proxied LLM responses) onto each session's
	// last_agent_activity_at so the §11.3 idle watchdog (sweepIdle) does
	// not reap an actively-working session. Coalesces durable writes to
	// ≤1/s per session. F-11.3.7.
	activityStamper := sessionidle.NewStamper(w.sessions, clockinject.Now)

	// spec: §5.2 (combined failed+leaked unhealthy threshold), §6.2
	// (leaked-slot semantics) — a single per-pod fail/leak rolling-window
	// tracker shared by the slot-bind-failure path (the sessionserver slot
	// retry policy) and the §4.7 scrub-report drain ledger (adapter-reported
	// slot-scrub leaks). Both feed one window so a pod crossing
	// ceil(maxConcurrentSessions/2) on the combined count drains regardless of
	// which path observed the degradation; instantiating two disjoint trackers
	// would let the counts never combine.
	slotHealth := slothealth.New(slothealth.WithClock(clockinject.Now))

	// spec: §4.2 / §15.1 — build the §4.2 session server (the §4.1 Stream
	// Proxy and Upload Handler realized behind the sessionserver interfaces)
	// with its full §11.1/§11.2/§7.x/§14/§16.1 Options set. The composition
	// root threads the returned server to the MCP fabric, the admin router,
	// the HTTP surface, and the watchdog.
	sessionSrv := w.buildSessionServer(
		gwMetrics, activityStamper, sessionBudgetEnforcer, dsMonitor,
		environments, tenantAccess, opsEmitter, credentialPools, vcsCreds,
		customRoles, resolvedNoEnvPolicy, auditSink, sessionStickyCache,
		experimentProviders, usage, taskUsageBuilder, sessionLeaseRegistrar,
		leaseExtDefaults, quotaCheckpointSvc, policyChain, policyAuditSink,
		auditAppender, inputWaits, uploadSubsystem, uploadMetrics, slotHealth,
		callbackValidator, callbackSeal, callbackDispatcher,
	)
	// spec: §11.2 line 44 — the budget terminator runs the same terminal
	// pipeline a watchdog or operator force-terminate runs, so an
	// over-budget session releases its pod and emits its terminal audit /
	// billing / SSE signals exactly once.
	budgetTerminator.onTerminal = sessionSrv.OnSessionTerminal

	// ----- OpenAI Chat + Open Responses translators -----
	openaiHandler := translator.NewOpenAIChatHandler(w.sessions, w.exec, translator.OpenAIChatOptions{Clock: clockinject.Now})
	responsesHandler := translator.NewOpenResponsesHandler(w.sessions, w.exec, translator.OpenResponsesOptions{Clock: clockinject.Now})

	// ----- §4.9 end-user credential registry -----
	// The Postgres-backed store envelope-encrypts the §12.9 T4 secret
	// column under per-tenant KMS KEKs; the in-memory store keeps the
	// secret process-local and never persists it.
	var credentials credentialstore.Store = credentialstore.NewMemory(nil)
	// §4.9.1 re-encryption job: the envelope-backed stores re-key their
	// rows under the current KEK version after a rotation. Only the
	// Postgres stores have a KEK to rotate; the in-memory stores hold
	// plaintext.
	var credentialRekeyers []rekey.TenantRekeyer
	if w.pgPool != nil {
		pgCreds, perr := credentialpg.New(w.pgPool, w.kmsProvider)
		if perr != nil {
			log.Fatalf("lenny-gateway: credential store: %v", perr)
		}
		credentials = pgCreds
		credentialRekeyers = append(credentialRekeyers, pgCreds)
	}
	// ----- §4.9 Pre-Authorized Credential Flow (user-source delivery) -----
	// The user-source materializer resolves a user-registered credential
	// into a proxy-mode lease at session creation and serves the §4.9 LLM
	// proxy from it, sharing the lease store (llmLeases) and upstream-
	// credential cache (credCache) the pool path uses. User credentials are
	// delivered in proxy mode so the secret never reaches the pod and
	// rotation/revocation are gateway-local. It is wired only when the
	// public proxy URL is configured; otherwise Available reports every
	// provider unavailable and sessions fall through to pool.
	// spec: §4.9 lines 1340-1381.
	var userCredMaterializer *usercreds.Materializer
	if userLeaseStore, ok := w.llmLeases.(usercreds.LeaseStore); ok {
		userCredMaterializer = usercreds.New(usercreds.Config{
			Store:    credentials,
			Leases:   userLeaseStore,
			Creds:    w.credCache,
			ProxyURL: *llmProxyPublicURL,
		})
	}
	credServer := credentialserver.New(credentials).
		WithAudit(credentialAuditor{sink: auditSink})
	if userCredMaterializer != nil {
		// spec: §4.9 lines 1350-1351 — the PUT (rotate) and revoke endpoints
		// reach the active user leases through the materializer.
		credServer = credServer.WithLeasePropagator(userCredMaterializer)
		// spec: §4.9 lines 1347-1351 — the session-creation router resolves a
		// user source only when the materializer reports the credential
		// available and deliverable.
		sessionSrv.SetUserCredChecker(userCredMaterializer.Available)
		if w.podBinder != nil {
			// spec: §4.9 lines 1246-1262 — the §4.7 binder materializes each
			// user-source provider into a proxy-mode lease pushed to the pod.
			w.podBinder.UserCredentials = userCredMaterializer
		}
	}

	// ----- §9.3 connector OAuth 2.1 authorization-code flow -----
	// The connector-credential store holds the access/refresh tokens a
	// completed connector OAuth flow produces, keyed by the
	// (tenant, connector, user) triple. The in-memory store keeps the
	// tokens process-local; a Postgres-backed store envelope-encrypts
	// them under the same per-tenant KMS KEKs the credential store
	// uses. The flow is wired only when --connector-oauth-callback-url
	// is set: the OAuth provider needs an absolute redirect URI.
	// §4.3 line 200 / §13.3 connector OAuth tokens are T4 Restricted
	// and must be envelope-encrypted at rest. The Postgres-backed store
	// envelope-encrypts both access and refresh tokens under the
	// per-tenant KMS KEK; the in-memory store is for tests and the
	// minimal gateway. The §4.3 long-term trust-boundary tightening —
	// routing connector credential reads/writes through a Token Service
	// RPC so the gateway holds no KMS decrypt grant — is deferred (see
	// F-4.3.1 resolution note); today the gateway holds the same KMS
	// access for connector creds that it already holds for §4.9
	// user-credential rows.
	var connectorCreds connectorcredstore.Store = connectorcredstore.NewMemory(nil)
	if w.pgPool != nil {
		pgConnectorCreds, err := connectorcredpg.New(w.pgPool, w.kmsProvider, nil)
		if err != nil {
			log.Fatalf("lenny-gateway: connector-credential store: %v", err)
		}
		connectorCreds = pgConnectorCreds
		credentialRekeyers = append(credentialRekeyers, pgConnectorCreds)
		log.Printf("lenny-gateway: §4.3 connector credentials backed by Postgres (envelope-encrypted)")
	}
	// §4.9.1 KMS-key-rotation re-encryption job over every envelope-backed
	// credential store. Wired to the admin router below; absent in the
	// in-memory dev posture (no store to re-key).
	var credentialRekeyJob *rekey.Job
	if len(credentialRekeyers) > 0 {
		credentialRekeyJob = rekey.NewJob(credentialRekeyers...)
	}
	var connectorOAuth *admin.ConnectorOAuth
	var connectorStateStore *connectoroauth.MemoryStateStore
	if *connectorOAuthCallbackURL != "" {
		var stateSeed [32]byte
		if _, err := rand.Read(stateSeed[:]); err != nil {
			log.Fatalf("lenny-gateway: connector OAuth state signing key: %v", err)
		}
		stateSigner, err := connectoroauth.NewStateSigner(connectoroauth.SigningKey{
			KeyID: "boot", Secret: stateSeed[:],
		})
		if err != nil {
			log.Fatalf("lenny-gateway: connector OAuth state signer: %v", err)
		}
		// spec: §9.3 line 157 — pending state binds to (session, connector)
		// with TTL=10min. Production binds it to Redis so the flow survives
		// a gateway restart and a callback resolves on any replica
		// (F-9.3.5); the in-memory store is the single-process fallback and
		// alone needs the periodic Sweep scheduled below in the watchdog
		// group (Redis relies on native key expiry). F-9.3.16.
		var connectorStateBacking connectoroauth.StateStore
		if w.redisClient != nil {
			connectorStateBacking = connectoroauth.NewRedisStateStore(w.concernRedis.For(storerouter.RedisConcernCachePubSub))
			log.Printf("lenny-gateway: §9.3 connector OAuth state backed by Redis (TTL=10m, cross-replica)")
		} else {
			connectorStateStore = connectoroauth.NewMemoryStateStore()
			connectorStateBacking = connectorStateStore
		}
		connectorOAuth = &admin.ConnectorOAuth{
			StateSigner: stateSigner,
			StateStore:  connectorStateBacking,
			Credentials: connectorCreds,
			CallbackURL: *connectorOAuthCallbackURL,
		}
		// spec: §9.3 line 129 — resolve a confidential connector's client
		// secret from its auth.clientSecretRef Kubernetes Secret at
		// token-exchange time. Wired whenever the gateway holds a cluster
		// client (the production --agent-namespace path); without it a
		// confidential-client callback returns a clear "no client-secret
		// resolver is wired" error instead of failing on exchange. F-9.3.4.
		if w.clusterClient != nil {
			connectorOAuth.ClientSecrets = connectorsecret.NewKubeResolver(w.clusterClient, *connectorOAuthClientSecretKey)
		}
		// §9.3: when the provider's token endpoint is behind a private
		// CA, --connector-oauth-ca supplies the bundle that verifies it.
		if *connectorOAuthCA != "" {
			caPEM, err := os.ReadFile(*connectorOAuthCA)
			if err != nil {
				log.Fatalf("lenny-gateway: connector OAuth CA bundle: %v", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(caPEM) {
				log.Fatalf("lenny-gateway: connector OAuth CA bundle %s contains no PEM certificates", *connectorOAuthCA)
			}
			connectorOAuth.HTTPClient = &http.Client{
				Timeout: 15 * time.Second,
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
				},
			}
		}
		log.Printf("lenny-gateway: §9.3 connector OAuth 2.1 flow enabled, callback %s", *connectorOAuthCallbackURL)
	}

	// spec: §9.1 / §8.2 — build the §9.1 MCP fabric: the delegation-policy,
	// external-interceptor, and deployment-config stores, the §8.2
	// delegation service, the §9.1 MCP server with every tool family
	// registered, and the §15.2 SSE attach channel. The step records the
	// delegation service, the MCP server, and the three admin-mutable stores
	// on the accumulator; re-alias them to the original local names so the
	// admin-router, HTTP-surface, and control-server build steps below read
	// them unchanged.
	w.buildMCPSurface(gwMetrics, sessionSrv, policyChain, auditSink, auditAppender,
		policyAuditSink, childLeaseRegistrar, maxInputResolver, environments,
		resolvedNoEnvPolicy, inputWaits, activityStamper, taskUsageBuilder,
		vcsCreds, elicitationFloorProvider)
	delegationPolicies := w.delegationPolicies
	interceptors := w.interceptors
	deploymentConfig := w.deploymentConfig
	delegationSvc := w.delegationSvc
	mcpSrv := w.mcpSrv
	revCache := w.revCache

	// §13.3 revocation cache: the auth middleware rejects a token
	// whose jti is in this set. It is rehydrated from the Postgres
	// issued-token index below. The propagator wraps the cache with
	// Redis pub/sub fan-out so a revocation on any replica reaches every
	// replica within pub/sub latency; with no Redis the propagator is a
	// local-only pass-through. revCache stays the read primitive the
	// auth middleware and the rehydration loop use directly. revCache is
	// constructed above (shared with the §8.2 child-token minter).
	revProp := revocationprop.New(revCache, w.securityBus, revocationprop.WithErrorHandler(func(err error) {
		log.Printf("lenny-gateway: token-revocation pub/sub publish failed: %v", err)
	}))

	// §10.3 mTLS certificate deny list: the per-replica SPIFFE-URI deny
	// set checked on every mTLS handshake (declared earlier so the
	// §10.3 NET-063 interceptor dials can consult it). Its propagator
	// carries an Add or Remove across replicas over Redis pub/sub. The
	// deny list is a single-replica primitive; the propagator owns the
	// fan-out the package doc defers to a wrapping controller.
	mtlsDenyProp := mtlsdenylistprop.New(mtlsDeny, w.securityBus, mtlsdenylistprop.WithErrorHandler(func(err error) {
		log.Printf("lenny-gateway: mTLS deny-list pub/sub publish failed: %v", err)
	}))

	// §4.9 credential deny list: the per-replica set of revoked
	// credential identities the LLM proxy checks before every upstream
	// call. The §4.9 LLM proxy below reads it directly on the hot path;
	// the credential-lease revocation propagator built next wraps it
	// with cross-replica Redis pub/sub fan-out, so the admin router's
	// §11.4 full_revoke fan-out and the emergency-revocation path revoke
	// a credential onto every replica's deny list.
	credDeny := denylist.New()

	// ----- §4.9 Proactive Lease Renewal worker -----
	// The credrenewal.Worker tracks each active credential lease by its
	// renewBefore deadline and issues a replacement before the original
	// expires, so a long-lived session never sees its LLM credential
	// lapse. credRenewal binds the worker to the credential-assignment
	// service that mints the replacement and to the warm-pod registry it
	// pushes the rotated credential to via the §4.7 RotateCredentials
	// RPC. credRenewal is nil when no credential pools are wired; a nil
	// receiver leaves every renewal hook a no-op.
	credRenewal := newCredRenewalWiring(w.credAssign, w.podRegistry, opsEmitter)
	// credRenewalProp carries a §4.9 credential-lease revocation across
	// replicas: a Revoke updates the local deny list, drops the renewal
	// worker's tracked leases bound to the credential, and fans out over
	// the same Redis pub/sub channel the §4.9 credential-deny-list
	// propagator uses. The §11.4 full_revoke fan-out and the emergency-
	// revocation path route through it so a revoked credential lease
	// stops reaching the provider on every replica, and no replica
	// proactively renews a credential that is no longer trustworthy.
	// §4.9 line 1647: the credential deny-list revocation propagates via
	// Redis pub/sub with Postgres LISTEN/NOTIFY as fallback. The Postgres
	// half is wired only when Postgres is configured (the option is
	// omitted otherwise so a no-Postgres dev gateway keeps a true-nil
	// fallback); it carries a revocation when Redis is down or disabled
	// and feeds the LISTEN subscribe loop so a peer's revocation still
	// converges. F-13.3.8.
	credDenyPropOpts := []credrenewalprop.Option{
		credrenewalprop.WithErrorHandler(func(err error) {
			log.Printf("lenny-gateway: credential-lease revocation pub/sub publish failed: %v", err)
		}),
	}
	if w.pgPool != nil {
		credDenyPropOpts = append(credDenyPropOpts, credrenewalprop.WithFallback(pgnotify.New(w.pgPool)))
	}
	// §4.9 line 1649 emergency-revocation step 5: when the gateway mints
	// leases in-process, wire the direct-mode rotate as a revoke hook on
	// the credential-lease propagator. A revoked pool credential then
	// proactively rotates every direct-delivery pod off the materialized
	// key on whichever replica holds the binding, minting the replacement
	// from a different credential in the same pool. The deny list already
	// terminates proxy-mode access fleet-wide; this adds the direct-mode
	// proactive push the deny list cannot deliver. The token-service
	// minting path (--token-service-grpc-addr) carries no per-credential
	// revocation surface yet, so the rotate is wired only for the
	// in-process path; the deny-list termination still applies in both.
	if w.inProcessAssign != nil {
		if ls, ok := w.llmLeases.(poolLeaseStore); ok {
			directRotator := &directModeRevocationRotator{
				leases:      ls,
				markRevoked: w.inProcessAssign.RevokeCredential,
				rotate:      proxyFallbackRotator{assign: w.credAssign, registry: w.podRegistry}.Rotate,
			}
			credDenyPropOpts = append(credDenyPropOpts,
				credrenewalprop.WithRevokeHook(directRotator.onRevoke))
		}
	}
	var credRenewalWorker *credrenewal.Worker
	credRenewalProp := credrenewalprop.New(credDeny, nil, w.securityBus, credDenyPropOpts...)
	if credRenewal != nil {
		// spec: §11.3 line 215 — credentials.expiryWarningLeadSeconds.
		// 0 disables warnings; -1 keeps the package default; any other
		// non-negative value is the explicit operator override.
		expiryWarningLead := time.Duration(*credentialsExpiryWarningLeadSeconds) * time.Second
		credRenewalWorker = credrenewal.New(credRenewal, credrenewal.Options{
			// §4.9: a proactive renewal that rotates a lease onto a fresh
			// credential pushes it to the lease's pod via RotateCredentials.
			OnRenewed: credRenewal.onRenewed,
			// §4.9: a lease whose renewal cannot proceed falls through to
			// fault rotation. The worker drops it; onExhausted clears its
			// pool binding.
			OnExhausted: credRenewal.onExhausted,
			Clock:       clockinject.Now,
			// spec: §11.3 line 215 — operator-tunable expiry-warning lead.
			// F-11.3.20.
			ExpiryWarningLead: expiryWarningLead,
			OnExpiryWarning:   logCredentialExpiryWarning,
		})
		log.Printf("lenny-gateway: §11.3 line 215 credentials.expiryWarningLeadSeconds=%ds", int(expiryWarningLead/time.Second))
		// Every §4.9 credential lease the assignment service mints — at
		// session start and at fault rotation — is tracked by the renewal
		// worker so its renewBefore deadline drives a proactive renewal.
		w.credAssign.OnAssigned(func(a credassign.LeaseAssignment) {
			credRenewal.track(credRenewalWorker, a.PoolName, string(a.Lease.Provider), a.Lease)
		})
		// Rebuild the propagator over the live worker so a peer replica's
		// credential-lease revocation also drops this replica's tracked
		// leases for the credential, not just its deny-list entry.
		credRenewalProp = credrenewalprop.New(credDeny, credRenewalWorker, w.securityBus, credDenyPropOpts...)
	}
	// spec: §4.9 lines 1640-1652 — wire the user-credential revocation onto
	// the cross-replica deny-list propagator so a POST /v1/credentials/{ref}
	// /revoke adds the user-shaped deny-list entry on every replica. Set
	// after the propagator's final form (it is rebuilt above over the live
	// renewal worker).
	if userCredMaterializer != nil {
		userCredMaterializer.SetRevoker(credRenewalProp)
	}

	// spec: §4.1 / §15.1 — build the admin REST subsystem. It records the
	// router on w.adminRouter and returns the sibling-block locals the §8.6
	// control server (the §9.1 connector tool bridge) and the §4.9 LLM proxy
	// (the §12.8 erasure cache) and the mux (the §10.5 runtime-upgrade store)
	// still consume.
	connectorAuthorizer, connectorInvoker, ruStore, erasureSemanticCache := w.buildAdminRouter(
		gwMetrics, delegationSvc, environments, connectorCreds, connectorOAuth,
		credentialRekeyJob, policyChain, auditSink, auditAppender,
		wireAudit, adminStickyFlusher, erasureSticky, deploymentConfig,
		credentialPools, customRoles, delegationPolicies, interceptors,
		leaseBudgets, opsEmitter, opsEventBuffer, sessionSrv, tenantAccess,
		auditOpsStore, auditPruner, auditValidator, credRenewalProp,
		elicitationFloorProvider, quotaCheckpointSvc, quotaCounter,
		quotaFailOpenAccum, revProp,
	)

	// ----- Compose the mux -----
	// spec: §4.1 / §15.1 — compose the REST mux and HTTP server. Records
	// w.mux and w.httpSrv on the accumulator.
	w.buildHTTPSurface(
		gwMetrics, sessionSrv, openaiHandler, responsesHandler, credServer,
		mcpSrv, policyChain, auditSink, auditAppender, opsEmitter, environments,
		driftMonitor, dsMonitor, failOpenReplicas, revCache, revProp, ruStore,
		siemHealthChecker, resolvedNoEnvPolicy,
	)

	// spec: §4.9 — build the LLM Proxy subsystem (a named §4.1 extraction
	// target) and record its server on the accumulator for the run loop.
	llmProxySrv := w.buildLLMProxy(policyChain,
		sessionBudgetEnforcer, activityStamper, auditSink, erasureSemanticCache,
		usage, quotaCounter, tenantLimits)

	// spec: §8.6 / §6.2 — build the §8.6 GatewayControl gRPC server (the
	// adapter→gateway control surface, the §9.1/§9.3 tool bridges, and the
	// §4.7 scrub-report service) and the §6.2 / §11.3 session watchdog. The
	// step records the server, its listener, the watchdog, and the watchdog
	// context (and its cancel) on the accumulator.
	w.buildControlServer(gwMetrics, mcpSrv, auditAppender, slotHealth, mtlsDeny,
		connectorAuthorizer, connectorInvoker, leaseBudgets, sessionSrv)
	// Cancel the §6.2 watchdog context at process shutdown rather than when
	// buildControlServer returns.
	defer w.watchdogCancel()
	// The §3.2 reserved-hold coordinator and §3.4 recycle-boundary
	// coordinator are stopped on shutdown so the in-process timers and
	// re-warm polls do not run against a draining client. The original
	// inline control-server block registered these Stop defers only inside
	// the scrub-report branch; re-evaluate the same predicate here so the
	// process-lifetime defers fire exactly when that branch did.
	if w.scrubReportServiceWired() {
		if w.holdCoordinator != nil {
			defer w.holdCoordinator.Stop()
		}
		if w.recycleBoundary != nil {
			defer w.recycleBoundary.Stop()
		}
	}

	// spec: §4.1 — thread the constructed stores and subsystems onto the
	// accumulator, then launch the background-worker step. Each field is
	// the local an earlier build block produced; startBackgroundWorkers
	// reads them to drive the periodic sweepers and propagators.
	w.auditAppender = auditAppender
	w.auditPruner = auditPruner
	w.credDeny = credDeny
	w.credRenewalProp = credRenewalProp
	w.driftMonitor = driftMonitor
	w.elicitationFloorProvider = elicitationFloorProvider
	w.eventBusRetranscriber = eventBusRetranscriber
	w.inputWaits = inputWaits
	w.mtlsDenyProp = mtlsDenyProp
	w.revProp = revProp
	w.sessionSrv = sessionSrv
	w.subsystemMetrics = subsystemMetrics
	w.uploadSubsystem = uploadSubsystem
	w.connectorStateStore = connectorStateStore
	w.dsMonitor = dsMonitor
	w.quotaCheckpointSvc = quotaCheckpointSvc
	w.sessionUsage = sessionUsage
	w.credRenewalWorker = credRenewalWorker
	w.credentialPools = credentialPools
	w.startBackgroundWorkers()

	// spec: §4.1 — thread the constructed subsystems and run-loop inputs
	// onto the composition-root accumulator, then hand off to runServers,
	// which installs the §25.13 alert tracker, the signal handler, and the
	// §17 run-and-shutdown loop. Each field is the local the build steps
	// above produced.
	w.llmProxySrv = llmProxySrv
	w.traceShutdown = traceShutdown
	w.experimentProviders = experimentProviders
	w.gwMetrics = gwMetrics
	w.opsEmitter = opsEmitter
	w.ocsfTranslator = ocsfTranslator
	w.ocsfOutbox = ocsfOutbox
	w.auditBatchBuffer = auditBatchBuffer
	w.runServers()
}

// startBackgroundWorkers launches the §4.1 gateway's periodic
// sweepers, samplers, reconcilers, leader-election loops, and
// security-cache propagator subscribers under the watchdog context.
// Every component it drives is constructed by an earlier build step and
// threaded through the accumulator; the step launches goroutines and
// returns, so the run loop (runServers) can install the signal handler
// and the listeners. It produces no value the later steps consume.
//
// spec: §4.1 — gateway background subsystems; §10.1 / §11.2 / §12.5 sweeps.
func (l sessionUserLookup) UserID(ctx context.Context, tenantID, sessionID string) (string, bool) {
	sess, err := l.sessions.Get(ctx, tenantID, sessionID)
	if err != nil || sess.UserID == "" {
		return "", false
	}
	return sess.UserID, true
}

// sessionGenerationReader adapts the session store to
// coordfence.GenerationReader so the §10.1 CoordinatorFence driver reads
// (and re-reads, after a stale rejection) the session's authoritative
// §4.2 coordination_generation.
type sessionGenerationReader struct{ store sessionstore.Store }

func (r sessionGenerationReader) CoordinationGeneration(ctx context.Context, tenantID, sessionID string) (int64, error) {
	row, err := r.store.Get(ctx, tenantID, sessionID)
	if err != nil {
		return 0, err
	}
	return row.CoordinationGeneration, nil
}

// lastSeqStore adapts the §4.2 session store to the §7.3 line 397
// sessions.last_seq durability hooks on the session event bus. It
// satisfies both sessionevents.LastSeqPersister (advance on every
// publish) and sessionevents.LastSeqLoader (seed the in-memory
// counter on first publish for the session). Both methods are
// best-effort — a Postgres outage degrades to the local counter
// without dropping events. F-7.3.3.
type lastSeqStore struct{ sessions sessionstore.Store }

// LoadLastSeq returns the persisted §7.3 line 397 sessions.last_seq
// counter so the Bus seeds its local counter on the first publish for
// the session (the coordinator-handoff "primed from Postgres at
// handoff step 0" contract). A missing row reads as zero so a fresh
// session starts at 1.
func (l lastSeqStore) LoadLastSeq(ctx context.Context, tenantID, sessionID string) (int64, error) {
	sess, err := l.sessions.Get(ctx, tenantID, sessionID)
	if errors.Is(err, sessionstore.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return sess.LastSeq, nil
}

// AdvanceLastSeq persists the new per-session SeqNum to Postgres. The
// store's Update mutate-callback applies the new value and the
// pgstore's GREATEST floor in updateSQL keeps the persisted value
// monotonic against late writers from sibling replicas.
func (l lastSeqStore) AdvanceLastSeq(ctx context.Context, tenantID, sessionID string, seq int64) error {
	_, err := l.sessions.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		if seq > row.LastSeq {
			row.LastSeq = seq
		}
		return nil
	})
	if errors.Is(err, sessionstore.ErrNotFound) {
		return nil
	}
	return err
}

// sessionRetryLookup adapts the §4.2 session store to the §4.8
// RetryPolicyEvaluator's RetryStateLookup: a missing session reads as
// not-found (ok == false, the request is admitted), and any other store
// fault surfaces as an error so the fail-closed evaluator rejects.
// maxInputSizeResolverHolder lets the §4.8 DelegationPolicyEvaluator be
// registered into the policy chain before delegationSvc is constructed:
// the inner resolver is filled in once the service exists. Until then
// (and whenever inner is nil) it reports "no policy", so the evaluator
// falls back to the operator-configured default maxInputSize. The holder
// is read on the request path after wiring completes, so the deferred
// assignment is safe. spec: §4.8 line 974; §8.3 line 157. F-13.5.1 / F-8.2.9.
type maxInputSizeResolverHolder struct {
	inner policy.MaxInputSizeResolver
}

func (h *maxInputSizeResolverHolder) ResolveMaxInputSize(ctx context.Context, tenantID, parentSessionID string) (int, bool) {
	if h.inner == nil {
		return 0, false
	}
	return h.inner.ResolveMaxInputSize(ctx, tenantID, parentSessionID)
}

type sessionRetryLookup struct{ sessions sessionstore.Store }

func (l sessionRetryLookup) LookupRetryState(ctx context.Context, tenantID, sessionID string) (policy.RetryState, bool, error) {
	sess, err := l.sessions.Get(ctx, tenantID, sessionID)
	if errors.Is(err, sessionstore.ErrNotFound) {
		return policy.RetryState{}, false, nil
	}
	if err != nil {
		return policy.RetryState{}, false, err
	}
	return policy.RetryState{RetryCount: sess.RetryCount}, true, nil
}

// llmFallbackWiring bundles the §4.9 Fallback Flow dependencies the LLM
// proxy handler drives on an upstream credential fault. A zero value (or
// nil controller) leaves the proxy on its pre-fallback behavior.
type llmFallbackWiring struct {
	controller *credfallback.Controller
	rotator    llmproxy.FallbackRotator
	audit      llmproxy.FallbackAuditSink
	metrics    llmproxy.FallbackMetrics
}

func newLLMProxyServer(addr string, translators llmproxy.TranslatorRegistry, leases credleasestore.LeaseStore, creds *credcache.Cache, denyList *denylist.DenyList, chain *interceptor.Chain, cache llmproxy.ProxyCache, gwMetrics *gatewaymetrics.Metrics, usage llmproxy.UsageRecorder, budgetGate llmproxy.BudgetGate, fallback llmFallbackWiring) *http.Server {
	if addr == "" {
		return nil
	}
	proxyMux := http.NewServeMux()
	proxyMux.Handle("POST /llm-proxy/v1/messages", &llmproxy.Handler{
		Leases:       leases,
		Translators:  translators,
		Forwarder:    &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
		Credentials:  creds,
		DenyList:     denyList,
		Interceptors: chain,
		Cache:        cache,
		// spec: §4.9 line 1468 — proxy-extracted counts feed the §15.1 /
		// §11.2 usage record. A nil Usage discards the counts.
		Usage: usage,
		// spec: §11.2 line 44 / §8.10 line 1108 — reject a proxied request
		// for a session that has already exhausted its token budget.
		BudgetGate: budgetGate,
		// §16.1 lines 97, 99, 100: active connections, translation
		// duration, and translation errors on the gateway registry.
		Metrics: gwMetrics,
		// spec: §4.9 lines 1383-1411 — the credentialPolicy Fallback Flow.
		Fallback:        fallback.controller,
		FallbackRotator: fallback.rotator,
		FallbackAudit:   fallback.audit,
		FallbackMetrics: fallback.metrics,
	})
	return &http.Server{
		Addr:              addr,
		Handler:           proxyMux,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

// llmTranslatorConfig carries the §4.9 per-provider translator config
// the gateway reads from flags. anthropic_direct and openai_direct
// register unconditionally with their defaults; the provider-config
// dependent translators register only when their required fields are
// set.
type llmTranslatorConfig struct {
	anthropicVersion string
	openaiBaseURL    string
	openaiOrg        string
	bedrockRegion    string
	vertexRegion     string
	vertexProject    string
	azureEndpoint    string
	azureAPIVersion  string
}

// buildLLMTranslatorRegistry assembles the §4.9 provider→translator
// registry the proxy dispatches on. spec: §4.9 lines 1525-1526.
func buildLLMTranslatorRegistry(c llmTranslatorConfig) llmproxy.TranslatorRegistry {
	translators := []llmproxy.Translator{
		&llmproxy.AnthropicDirectTranslator{DefaultAnthropicVersion: c.anthropicVersion},
		&llmproxy.OpenAIDirectTranslator{BaseURL: c.openaiBaseURL, Organization: c.openaiOrg},
	}
	if c.bedrockRegion != "" {
		translators = append(translators, &llmproxy.AWSBedrockTranslator{Region: c.bedrockRegion})
	}
	if c.vertexRegion != "" && c.vertexProject != "" {
		translators = append(translators, &llmproxy.VertexAITranslator{Region: c.vertexRegion, Project: c.vertexProject})
	}
	if c.azureEndpoint != "" && c.azureAPIVersion != "" {
		translators = append(translators, &llmproxy.AzureOpenAITranslator{Endpoint: c.azureEndpoint, APIVersion: c.azureAPIVersion})
	}
	return llmproxy.NewTranslatorRegistry(translators...)
}

// newGatewayControlServer builds the §8.6 GatewayControl gRPC server
// and binds its listener. It returns (nil, nil, nil) when addr is
// empty, which disables the GatewayControl listener. A non-empty addr
// that cannot be bound returns the error so the gateway fails fast.
//
// The server hosts the §8.6 ExtendLease RPC. Its budget state is the
// caller-supplied MemoryBudgetSource (shared with the §15.1 admin
// extension-denial clear endpoint so both mutate one set of per-tree
// denial flags), which doubles as the TenantResolver; a nil budgets
// argument falls back to a fresh source. The §8.6 durability
// requirement — persisting the extension-denied flag and cool-off
// expiry to the delegation_tree_budget Postgres table so a coordinator
// handoff cannot bypass a user rejection — is met by swapping in a
// Postgres-backed leasecontrol.BudgetSource with the Wave 1
// store-persistence work; leasecontrol.Service depends only on the
// interface.
//
// tlsCert/tlsKey/clientCA carry the §4.7 mesh credentials (the gateway's
// own --adapter-tls-* material). When clientCA is set the listener
// requires and verifies the pod adapter's client certificate, and the
// RequireVerifiedPeerInterceptor fails any call lacking a verified
// chain; all three empty selects the local-development plaintext path.
// F-8.6.4 / F-15.3.1.
//
// metrics may be nil for the no-metrics test path; in production the
// gatewaymetrics.Metrics implements leasecontrol.MetricEmitter so
// every ExtendLease decision drives the §16 line 66
// `lenny_delegation_lease_extension_total` counter. F-8.6.13.
//
// trustDomain and denyList wire the §10.3 NET-060 inbound peer
// validation: when both clientCA and trustDomain are set, the listener
// installs a SPIFFE VerifyPeerCertificate callback that validates each
// inbound pod certificate's `spiffe://<trust-domain>/agent/{pool}/{pod}`
// URI SAN at handshake (spec line 321) and rejects a certificate on the
// §10.3 revocation deny list (spec line 352). A rejection aborts the
// handshake with no gRPC frame and emits the spec's `pod_identity_mismatch`
// log. trustDomain empty leaves CA-only verification in place (the
// local-development path). F-10.3.1 / F-10.3.7 / F-10.3.13.
func newGatewayControlServer(addr string, budgets *leasecontrol.MemoryBudgetSource, metrics leasecontrol.MetricEmitter, auditor leasecontrol.Auditor, elicitor leasecontrol.Elicitor, autoCounter ratelimit.Counter, defaultAutoMaxPerMin int, platformTools leasecontrol.PlatformToolService, connectorTools leasecontrol.ConnectorToolService, treeGranter leasecontrol.TreeBudgetGranter, scrubReports leasecontrol.ScrubReportService, replicaID, tlsCert, tlsKey, clientCA, trustDomain, saTokenAudience string, saTokenVerifier leasecontrol.TokenVerifier, denyList spiffe.DenyChecker) (*grpc.Server, net.Listener, error) {
	if addr == "" {
		return nil, nil, nil
	}
	if budgets == nil {
		budgets = leasecontrol.NewMemoryBudgetSource()
	}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets:           budgets,
		Tenants:           budgets,
		Metrics:           metrics,
		Auditing:          auditor,
		ServiceInstanceID: replicaID,
		Clock:             clockinject.Now,
		// §8.6 line 714 — wire the elicitation path so elicitation-mode
		// trees solicit the user's consent instead of auto-granting.
		// F-8.6.2.
		Elicitor: elicitor,
		// §8.6 line 712 — the auto-mode rate-limit counter (reuses the
		// §11.1 request-rate counter, Redis-backed when configured) and
		// the deployment-default cap. F-8.6.7.
		AutoExtensionCounter:    autoCounter,
		DefaultAutoMaxPerMinute: defaultAutoMaxPerMin,
		// §9.1 lines 14-31 — forward a type:agent runtime's intra-pod
		// platform tool calls (lenny/delegate_task, ...) to the gateway
		// platform tool surface. F-9.1.1.
		PlatformTools: platformTools,
		// §9.3 lines 142-164 — forward a type:agent runtime's intra-pod
		// per-connector tool calls (against @lenny-connector-<id> sockets)
		// to the gateway connector-invocation surface. F-9.1.2.
		ConnectorTools: connectorTools,
		// §8.6 line 643 — propagate a granted token-budget extension onto
		// the §8.2 per-tree delegation budget counter so admission observes
		// the raised pool. F-8.6.3.
		TreeBudget: treeGranter,
		// §4.7 — the adapter's per-slot and whole-pod scrub reports drive the
		// recycle-counter writes, the unhealthy-threshold drain ledger, and the
		// §3.4 / §6.39 recycle disposition. Nil leaves ReportSessionScrub and
		// ReportPodScrub returning Unimplemented (the §8.6-only deployment).
		ScrubReports: scrubReports,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("build GatewayControl service: %w", err)
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("bind GatewayControl listener on %s: %w", addr, err)
	}
	// spec: §4.7 line 616 / §15.3 — the adapter↔gateway channel is mTLS.
	// The pod adapter is the client of this listener, so the gateway
	// presents its mesh server cert (--adapter-tls-cert/key, its §4.7
	// identity) and requires + verifies the adapter's client cert against
	// the mesh CA (--adapter-ca). The same adapter.TLSServerOption helper
	// the pod-facing Adapter service uses builds the credentials, so both
	// directions of the channel share one mTLS configuration. When no
	// cert material is configured the option is nil and the listener
	// serves plaintext — the documented local-development path only.
	// F-8.6.4 / F-15.3.1.
	// spec: §10.3 line 321 (NET-060) — the gateway validates the pod's
	// SPIFFE URI on every inbound handshake. The verifier runs as a
	// VerifyPeerCertificate callback on top of CA chain verification, so
	// possession of a cluster-CA cert is necessary but never sufficient
	// (spec line 324). It also consults the §10.3 revocation deny list
	// (spec line 352) so a cert revoked between rotations is rejected at
	// handshake. Only installed when client-cert verification is active
	// (clientCA set) and a trust domain is configured; otherwise the
	// local-development plaintext/CA-only path is preserved.
	var tlsMods []adapter.TLSConfigMod
	if clientCA != "" && trustDomain != "" {
		verifier := spiffe.AgentPeerVerifier{
			TrustDomain: trustDomain,
			DenyList:    denyList,
			OnMismatch: func(reason spiffe.MismatchReason, uri string, mErr error) {
				slog.Warn("pod_identity_mismatch",
					"net_rule", "NET-060",
					"reason", string(reason),
					"spiffe_uri", uri,
					"error", mErr.Error())
			},
		}
		tlsMods = append(tlsMods, func(c *tls.Config) {
			c.VerifyPeerCertificate = verifier.VerifyPeerCertificate
		})
	}
	tlsOpt, err := adapter.TLSServerOption(tlsCert, tlsKey, clientCA, tlsMods...)
	if err != nil {
		return nil, nil, fmt.Errorf("§8.6 GatewayControl mTLS credentials: %w", err)
	}
	var opts []grpc.ServerOption
	if tlsOpt != nil {
		opts = append(opts, tlsOpt)
	}
	// The interceptor fails closed when client-cert verification is
	// active (clientCA set): every ExtendLease call must arrive over a
	// verified mTLS chain, since the handler trusts the session_id in the
	// request body and has no other proof of the caller's identity.
	// F-8.6.4 / F-15.3.1.
	//
	// spec: §10.2 line 227 / §10.3 line 334 — the gateway validates the
	// projected SA token on every pod→gateway request: its signature and
	// expiry via a Kubernetes TokenReview (when saTokenVerifier is wired)
	// and its deployment-specific audience claim, the SA-token layer of
	// the §10.3 defense-in-depth chain. The interceptor is a no-op when no
	// audience is configured (the local-development path), so it composes
	// with the mTLS gate above without disturbing dev runs. When an
	// audience is set but no verifier is available it degrades to the
	// audience-only decode. F-10.3.20 / F-10.2.10.
	opts = append(opts, grpc.ChainUnaryInterceptor(
		leasecontrol.RequireVerifiedPeerInterceptor(clientCA != ""),
		leasecontrol.RequireSATokenInterceptor(saTokenAudience, saTokenVerifier),
	))
	// spec: §16.3 line 328 ("Pod → Gateway (delegation tool calls carry
	// parent trace context)") — extract the inbound traceparent from gRPC
	// metadata so the gateway's GatewayControl spans continue the pod's
	// trace. F-16.3.3.
	opts = append(opts, grpc.StatsHandler(otelgrpc.NewServerHandler()))
	gs := grpc.NewServer(opts...)
	adapterv1.RegisterGatewayControlServer(gs, svc)
	return gs, lis, nil
}

// newScrubReportService builds the §4.7 ScrubReporter that backs the
// ReportSessionScrub and ReportPodScrub RPCs. It wires the five concrete
// recycle seams (pkg/gateway/recycle) onto the gateway's dependencies: the
// agent_pod_state recycle counters, the unhealthy-threshold drain ledger
// over the shared slothealth tracker (the same tracker the sessionserver
// slot-bind-failure path feeds, so adapter-reported leaks and slot-bind
// failures accumulate in one §5.2 rolling window), the §6.39 host-node
// schedulability pod inspector, the §3.4 claim disposition driver, and the
// §16.1 retirement metrics. The drain ledger resolves each leaked pod's pool
// maxConcurrentSessions through the pool store, so a single-session
// recycling pod drains on the first leak while a recycling concurrent-session
// pool (the §5.2 "Concurrent" preset, maxConcurrentSessions: N with
// recycle.enabled) drains only at ceil(N/2) failed-or-leaked slots.
//
// spec: §4.7 (ReportSessionScrub/ReportPodScrub), §3.4 (recycle
// disposition), §5.2 (scrub model, combined failed+leaked threshold), §6.39
// (host-node schedulability retire), §16.1 (recycle metrics).
func newScrubReportService(cl client.Client, counters recycle.CounterStore, pools poolstore.Store, runtimes runtimestore.Store, metrics recycle.RetirementMetricsSink, slotHealth *slothealth.Tracker, agentNamespace string, holdTTL time.Duration, holds recycle.HoldRegistrar, boundary *recycle.RecycleBoundaryCoordinator, now func() time.Time) (leasecontrol.ScrubReportService, error) {
	ledger, err := recycle.NewDrainLedger(recycle.DrainLedgerOptions{
		Tracker:   slotHealth,
		Client:    cl,
		Namespace: agentNamespace,
		Pools:     pools,
		Now:       now,
	})
	if err != nil {
		return nil, fmt.Errorf("build drain ledger: %w", err)
	}
	inspector, err := recycle.NewPodInspector(recycle.PodInspectorOptions{
		Client:    cl,
		Namespace: agentNamespace,
		Pools:     pools,
		Runtimes:  runtimes,
		Now:       now,
	})
	if err != nil {
		return nil, fmt.Errorf("build pod inspector: %w", err)
	}
	driverOpts := recycle.ClaimDispositionDriverOptions{
		Client:    cl,
		Namespace: agentNamespace,
		HoldTTL:   holdTTL,
		Now:       now,
		Holds:     holds,
	}
	// §3.4: the disposition driver signals the recycle-boundary coordinator on
	// every resolved ReportPodScrub so it cancels the missing-report timeout
	// and, on a preConnect recycle, drives recycling → reserved once the SDK
	// re-warm completes. Set only when the coordinator exists so a typed-nil
	// pointer is not wrapped into a non-nil interface (single-process dev leaves
	// the timeout to fire and the re-warm completion to the orphan GC).
	if boundary != nil {
		driverOpts.Boundary = boundary
	}
	driver, err := recycle.NewClaimDispositionDriver(driverOpts)
	if err != nil {
		return nil, fmt.Errorf("build claim disposition driver: %w", err)
	}
	reporter, err := leasecontrol.NewScrubReporter(leasecontrol.ScrubReporterOptions{
		Counters:  recycle.NewRecycleCounterStore(counters),
		Ledger:    ledger,
		Inspector: inspector,
		Driver:    driver,
		Metrics:   recycle.NewRetirementMetrics(metrics),
	})
	if err != nil {
		return nil, fmt.Errorf("build scrub reporter: %w", err)
	}
	return reporter, nil
}

// leaseExtensionAuditAdapter implements leasecontrol.Auditor, turning
// each ExtensionAudit record into a §11.7 hash-chained audit row
// keyed on the request tenant. The event type is the spec-listed
// `delegation.lease_extended`; the payload carries every §8.6 line
// 743 field so a forensic reconstruction can identify the requesting
// session, the approval mode and approver, the per-batch grouping,
// the issuing replica, and the client originator. F-8.6.10.
// spec: §8.6 line 743
type leaseExtensionAuditAdapter struct {
	appender policy.AuditAppender
}

func (a leaseExtensionAuditAdapter) RecordExtension(ctx context.Context, e leasecontrol.ExtensionAudit) {
	if a.appender == nil {
		return
	}
	payload := map[string]any{
		"session_id":      e.RequestSessionID,
		"root_session_id": e.RootSessionID,
		// §8.6 line 643 requested/granted amounts across every extendable
		// dimension, not just tokens. F-8.6.1.
		"requested_tokens":            e.Requested.Tokens,
		"granted_tokens":              e.Granted.Tokens,
		"requested_seconds":           e.Requested.Seconds,
		"granted_seconds":             e.Granted.Seconds,
		"requested_children":          e.Requested.Children,
		"granted_children":            e.Granted.Children,
		"requested_parallel_children": e.Requested.ParallelChildren,
		"granted_parallel_children":   e.Granted.ParallelChildren,
		"requested_tree_size":         e.Requested.TreeSize,
		"granted_tree_size":           e.Granted.TreeSize,
		"requested_file_export_files": e.Requested.FileExportFiles,
		"granted_file_export_files":   e.Granted.FileExportFiles,
		"requested_file_export_bytes": e.Requested.FileExportBytes,
		"granted_file_export_bytes":   e.Granted.FileExportBytes,
		"effective_max":               e.EffectiveMax,
		"outcome":                     string(e.Outcome),
		"approval_mode":               string(e.ApprovalMode),
		"approver":                    e.Approver,
		"batch_id":                    e.BatchID,
		"service_instance_id":         e.ServiceInstanceID,
		"client_ip":                   e.ClientIP,
		"new_limits": map[string]any{
			"token_budget":      e.NewLimits.Tokens,
			"max_age_seconds":   e.NewLimits.Seconds,
			"children":          e.NewLimits.Children,
			"parallel_children": e.NewLimits.ParallelChildren,
			"tree_size":         e.NewLimits.TreeSize,
			"file_export_files": e.NewLimits.FileExportFiles,
			"file_export_bytes": e.NewLimits.FileExportBytes,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = a.appender.Append(ctx, e.TenantID, "delegation.lease_extended", json.RawMessage(data), clockinject.Now().UTC())
}

// RecordAutoRateLimitExceeded emits the §8.6 line 712
// `delegation.lease_extension_auto_rate_limit_exceeded` audit row when an
// auto-mode extension request trips the tree's maxAutoExtensionsPerMinute
// and the gateway falls back to elicitation for the remainder of the
// window. F-8.6.7.
// spec: §8.6 line 712
func (a leaseExtensionAuditAdapter) RecordAutoRateLimitExceeded(ctx context.Context, e leasecontrol.AutoRateLimitAudit) {
	if a.appender == nil {
		return
	}
	payload := map[string]any{
		"session_id":          e.RequestSessionID,
		"root_session_id":     e.RootSessionID,
		"max_per_minute":      e.MaxPerMinute,
		"service_instance_id": e.ServiceInstanceID,
		"client_ip":           e.ClientIP,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = a.appender.Append(ctx, e.TenantID, "delegation.lease_extension_auto_rate_limit_exceeded", json.RawMessage(data), clockinject.Now().UTC())
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

// platformConfigMissing is one §10.3 line 361 LENNY_CONFIG_MISSING
// violation: a required platform configuration key that is absent or
// invalid. The fields mirror the structured-log fields §10.3 line 371
// mandates (config_key, scope, remediation).
type platformConfigMissing struct {
	configKey   string
	scope       string
	remediation string
}

// validatePlatformConfig returns the §10.3 line 361 required-key
// violations for the platform keys gated at this point in gateway
// startup: the OIDC issuer URL and client ID (both exempt in dev mode
// per the line 373 dev-mode symmetry and §17.4), and the
// defaultMaxSessionDuration (always required to be a positive duration).
// The remaining required keys fail closed elsewhere so each key is
// gated before the replica is marked ready: noEnvironmentPolicy by
// resolveNoEnvironmentPolicy, playground.devTenantId by
// playground.Config.Validate. Extracted from main() so the
// TestGatewayConfigValidation regression test can cover the §10.3
// contract without booting a gateway. spec: §10.3 lines 361-373;
// §17.4 dev mode.
func validatePlatformConfig(devMode bool, oidcIssuerURL, oidcClientID string, defaultMaxSessionSeconds int) []platformConfigMissing {
	var missing []platformConfigMissing
	if !devMode {
		switch issuer := strings.TrimSpace(oidcIssuerURL); {
		case issuer == "":
			missing = append(missing, platformConfigMissing{
				configKey:   "auth.oidc.issuerUrl",
				scope:       "platform",
				remediation: "set auth.oidc.issuerUrl (Helm) / --oidc-issuer-url / LENNY_OIDC_ISSUER_URL to the OIDC issuer URL, or run with LENNY_DEV_MODE=true",
			})
		case !isAbsoluteURL(issuer):
			missing = append(missing, platformConfigMissing{
				configKey:   "auth.oidc.issuerUrl",
				scope:       "platform",
				remediation: "auth.oidc.issuerUrl must be an absolute URL (scheme://host); fix --oidc-issuer-url / LENNY_OIDC_ISSUER_URL",
			})
		}
		if strings.TrimSpace(oidcClientID) == "" {
			missing = append(missing, platformConfigMissing{
				configKey:   "auth.oidc.clientId",
				scope:       "platform",
				remediation: "set auth.oidc.clientId (Helm) / --oidc-client-id / LENNY_OIDC_CLIENT_ID, or run with LENNY_DEV_MODE=true",
			})
		}
	}
	if defaultMaxSessionSeconds <= 0 {
		missing = append(missing, platformConfigMissing{
			configKey:   "defaultMaxSessionDuration",
			scope:       "platform",
			remediation: "set gateway.maxSessionAgeSeconds (Helm) / --max-session-age-seconds / LENNY_MAX_SESSION_AGE_SECONDS to a positive number of seconds",
		})
	}
	return missing
}

// isAbsoluteURL reports whether s parses as an absolute URL with a
// scheme and host — the §10.3 line 365 "Non-empty URL" acceptance
// criterion for auth.oidc.issuerUrl.
func isAbsoluteURL(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	return err == nil && u.IsAbs() && u.Host != ""
}

// buildStartupProbeTLSConfig assembles the §10.3 line 359 startup TLS
// probe's client config from the optional CA bundle and client
// certificate. An empty CA uses the system trust store; an empty
// cert/key presents no client certificate. spec: §10.3 line 359.
func buildStartupProbeTLSConfig(caFile, certFile, keyFile string) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read --startup-tls-probe-ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("--startup-tls-probe-ca %s contains no PEM certificates", caFile)
		}
		cfg.RootCAs = pool
	}
	if certFile != "" || keyFile != "" {
		crt, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("load --startup-tls-probe-cert/--startup-tls-probe-key: %w", err)
		}
		cfg.Certificates = []tls.Certificate{crt}
	}
	return cfg, nil
}

// resolveNoEnvironmentPolicy returns the resolved §10.6 / §11.1
// platform-wide noEnvironmentPolicy or a fatal-startup error. An
// empty value outside dev mode returns the
// "LENNY_CONFIG_MISSING config_key=noEnvironmentPolicy scope=platform"
// error §10.3's configuration validation table mandates. Dev mode
// derives allow-all for local convenience. Any value other than
// deny-all / allow-all returns a typed validation error. Extracted
// from main() so the §11.1 TestGatewayConfigValidation test can
// regression-cover the §10.3 contract. spec: §10.6 line 646;
// §11.1 line 13; §10.3 configuration validation table.
func resolveNoEnvironmentPolicy(value string, devMode bool) (string, error) {
	resolved := value
	if resolved == "" && devMode {
		resolved = tenantstore.NoEnvPolicyAllowAll
	}
	if resolved == "" {
		return "", fmt.Errorf("LENNY_CONFIG_MISSING config_key=noEnvironmentPolicy scope=platform: " +
			"set --no-environment-policy or LENNY_NO_ENVIRONMENT_POLICY to deny-all or allow-all (§10.6)")
	}
	if resolved != tenantstore.NoEnvPolicyDenyAll && resolved != tenantstore.NoEnvPolicyAllowAll {
		return "", fmt.Errorf("--no-environment-policy must be deny-all or allow-all, got %q", resolved)
	}
	return resolved, nil
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
// this in single-tenant mode (where the §10.2 dev-header transport
// flips to MultiTenant=true to round-trip the tenant header).
// Multi-tenant production deployments use bearerTenantRegistry
// instead, which consults the real tenantstore.
type permissiveRegistry struct{}

func (permissiveRegistry) IsRegistered(string) (bool, error) { return true, nil }

// kmsBreakerObserver routes the §10.2 line 225 JWTSigner breaker
// transitions and signing failures onto gatewaymetrics so the §16.5
// KMSSigningUnavailable alert reads them. The metrics pointer is wired
// in after gatewaymetrics.New() returns; pre-wire calls are no-ops.
// spec: §10.2 line 225. F-10.2.6.
type kmsBreakerObserver struct {
	mu sync.Mutex
	m  *gatewaymetrics.Metrics
}

func (o *kmsBreakerObserver) SetMetrics(m *gatewaymetrics.Metrics) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.m = m
}

func (o *kmsBreakerObserver) metrics() *gatewaymetrics.Metrics {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.m
}

// hardPrunePartialManifests runs the §12.5 ll. 341 tombstone hard-prune
// pass over the partial-checkpoint manifest table: it physically removes
// every row whose soft-delete tombstone predates cutoff
// (now - gc.tombstoneRetentionSeconds). The pass is the sibling of the
// artifact_store hard-prune and runs on the same GC cycle so partial
// manifests follow the identical post-soft-delete lifecycle. Per-row
// HardDelete failures are logged and skipped — the next cycle retries
// them; a list failure returns the error with no rows pruned. Returns
// the number of rows physically removed.
//
// spec: §12.5 ll. 316, 341 — partial-manifest rows are swept by the same
// hard-prune pass on the same deleted_at retention predicate.
func hardPrunePartialManifests(ctx context.Context, store partialmanifeststore.Store, cutoff time.Time) (int, error) {
	expired, err := store.ListSoftDeletedBefore(ctx, cutoff)
	if err != nil {
		return 0, err
	}
	pruned := 0
	for _, r := range expired {
		if derr := store.HardDelete(ctx, r.TenantID, r.SessionID, r.Generation); derr != nil {
			log.Printf("lenny-gateway: §12.5 partial-manifest hard-prune row %s/%s gen=%d: %v",
				r.TenantID, r.SessionID, r.Generation, derr)
			continue
		}
		pruned++
	}
	return pruned, nil
}

// runStartupChainContinuityCheck implements the §12.3 line 101 startup
// chain-continuity check: it re-verifies the most recent lastN audit
// rows of every tenant's hash chain, increments
// lenny_audit_chain_integrity_total per tenant by §11.7 state, and logs
// the spec WARN message for each detected gap. A broken chain fires the
// §16.5 AuditChainGap alert through the metric; the gateway does not
// refuse to start. spec: §12.3 line 101. F-12.3.9.
func runStartupChainContinuityCheck(ctx context.Context, db integrity.Querier, lastN int, m *gatewaymetrics.Metrics) {
	results, err := integrity.CheckChainContinuityRecent(ctx, db, lastN)
	if err != nil {
		log.Printf("lenny-gateway: WARNING: §12.3 startup audit chain-continuity check could not run: %v", err)
		return
	}
	for _, r := range results {
		m.IncAuditChainIntegrity(string(r.Result.Integrity))
		if !r.Broken() {
			continue
		}
		if r.GapHighSeq() > 0 {
			log.Printf("Audit chain gap detected for tenant %s: gap between sequence %d and %d (~%s to %s). This indicates T2 audit events were lost from the in-memory batch buffer during a previous gateway crash. T3/T4 events are synchronous and will not appear in chain gaps.",
				r.TenantID, r.GapLowSeq(), r.GapHighSeq(),
				r.GapStart().Format(time.RFC3339), r.GapEnd().Format(time.RFC3339))
			continue
		}
		log.Printf("lenny-gateway: WARNING: §12.3 audit chain broken for tenant %s at sequence %d: %s",
			r.TenantID, r.Result.BreakSeq, r.Result.Detail)
	}
}

func (o *kmsBreakerObserver) OnSigningFailure() {
	if m := o.metrics(); m != nil {
		m.RecordKMSSigningError("inner")
	}
}

func (o *kmsBreakerObserver) OnRejected() {
	if m := o.metrics(); m != nil {
		m.RecordKMSSigningError("rejected")
	}
}

func (o *kmsBreakerObserver) OnCircuitOpen() {
	if m := o.metrics(); m != nil {
		m.SetKMSSigningCircuitState(2)
	}
}

func (o *kmsBreakerObserver) OnCircuitClosed() {
	if m := o.metrics(); m != nil {
		m.SetKMSSigningCircuitState(0)
	}
}

// memoryStoreObserver adapts the gatewaymetrics emitters into the §9.4
// MemoryStore Observer contract so the in-memory and Postgres
// backends route their per-operation metrics through one bound
// `backend` label. spec: §9.4 line 200 / §16.1 line 151–154. F-9.4.1.
type memoryStoreObserver struct {
	metrics *gatewaymetrics.Metrics
	backend string
}

func (o memoryStoreObserver) ObserveOperation(op string, seconds float64) {
	o.metrics.ObserveMemoryStoreOperation(op, o.backend, seconds)
}

func (o memoryStoreObserver) IncError(op, errorType string) {
	o.metrics.IncMemoryStoreError(op, o.backend, errorType)
}

func (o memoryStoreObserver) SetRecordCount(tenantID string, count int) {
	o.metrics.SetMemoryStoreRecordCount(tenantID, count)
}

func (o memoryStoreObserver) IncUserOverThreshold(tenantID string) {
	o.metrics.IncMemoryStoreUserOverThreshold(tenantID, o.backend)
}

// ocsfMetricsAdapter bridges the §11.7 OCSF translator's metric surface
// onto the gateway's Prometheus registry: a per-row translation failure
// advances lenny_audit_ocsf_translation_failed_total labeled by event
// type and ocsf.ErrorClass. Success and dead-letter counts stay on the
// translator's in-memory CountingMetrics (no dedicated Prometheus series
// exists for them in the §16.1 catalog). F-11.7.1 / F-11.7.15.
type ocsfMetricsAdapter struct{ metrics *gatewaymetrics.Metrics }

func (a ocsfMetricsAdapter) TranslationFailed(eventType string, class ocsf.ErrorClass) {
	a.metrics.IncAuditOCSFTranslationFailed(eventType, string(class))
}

func (a ocsfMetricsAdapter) TranslationSucceeded(string) {}

func (a ocsfMetricsAdapter) DeadLettered(string) {}

// userstorePlatformRoles adapts a userstore.Store into the §10.2 line
// 294 platform-managed role resolver consulted by the auth middleware.
// When a row carries a platform-managed assignment (RoleAssigned) — even
// one whose Roles slice is empty — its Roles fully replace the OIDC
// claim, so tenant-admins can downgrade a user with an over-broad OIDC
// claim by recording an explicit (possibly empty) assignment. A row with
// no assignment (the state left by `DELETE /v1/admin/tenants/{id}/users/
// {userId}/role`) or a missing row leaves the JWT claim authoritative.
// spec: §10.2 line 294, §15.1 line 828. F-10.2.3, F-15.1.3.
type userstorePlatformRoles struct {
	store userstore.Store
}

func (r userstorePlatformRoles) ResolveRoles(ctx context.Context, tenantID, subject string) ([]auth.Role, bool, error) {
	if r.store == nil {
		return nil, false, nil
	}
	row, err := r.store.Get(ctx, tenantID, subject)
	if errors.Is(err, userstore.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return append([]auth.Role(nil), row.Roles...), row.RoleAssigned, nil
}

// tenantIntrospectionConfig resolves the §10.6 line 661 real-time
// group-check configuration from a tenant's stored identityProvider
// record, satisfying introspection.ConfigSource. A tenant that has not
// set introspectionEnabled yields a disabled Config, so the auth
// middleware keeps the JWT groups claim for it. F-10.6.8.
type tenantIntrospectionConfig struct {
	store tenantstore.Store
}

func (s tenantIntrospectionConfig) IntrospectionConfig(ctx context.Context, tenantID string) (introspection.Config, error) {
	if s.store == nil {
		return introspection.Config{}, nil
	}
	row, err := s.store.Get(ctx, tenantID)
	if errors.Is(err, tenantstore.ErrNotFound) {
		return introspection.Config{}, nil
	}
	if err != nil {
		return introspection.Config{}, err
	}
	ip := row.RBACConfig.IdentityProvider
	return introspection.Config{
		Enabled:      ip.IntrospectionEnabled,
		Endpoint:     ip.IntrospectionEndpoint,
		ClientID:     ip.IntrospectionClientID,
		ClientSecret: ip.IntrospectionClientSecret,
		CacheTTL:     time.Duration(ip.IntrospectionCacheTTLSeconds) * time.Second,
	}, nil
}

// bearerTenantRegistry is the §10.2 line 219 multi-tenant bearer-chain
// adapter. It consults the wired tenantstore so a Bearer JWT whose
// `tenant_id` claim names a tenant that is not provisioned (or is
// soft-deleted) is rejected with TENANT_NOT_FOUND. The built-in
// `default` tenant is admitted unconditionally so the Embedded-Mode
// quickstart (which seeds the default row via the bootstrap Job) works
// even before the row is persisted; once the row exists, the active
// flag (IsActive) governs.
// spec: §10.2 lines 219-221. F-10.2.1.
type bearerTenantRegistry struct {
	store tenantstore.Store
}

func (r bearerTenantRegistry) IsRegistered(tenantID string) (bool, error) {
	if tenantID == auth.DefaultTenantID {
		return true, nil
	}
	row, err := r.store.Get(context.Background(), tenantID)
	if errors.Is(err, tenantstore.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return row.IsActive(), nil
}

// sessionArtifactDeleter is implemented by session-scoped stores that
// expose the per-session DeleteBySession adapter — the transcript and
// blob stores. It backs both the §12.8 erasure orchestrator and the
// §7.1 retention GC.
type sessionArtifactDeleter interface {
	DeleteBySession(ctx context.Context, tenantID, sessionID string) (int, error)
}

// artifactMetricsSink is implemented by every §17.9.3 artifact-store
// backend that surfaces the §12.5 ll. 282/303 metric callbacks
// (MinIO, S3, GCS, Azure). The gateway type-asserts the resolved
// blobstore.Store onto it so the fail-closed KMS-unavailable and
// retry-exhausted upload-error counters are wired no matter which
// provider serves the bucket. spec: §12.5 ll. 282, 303; F-17.5.1.
type artifactMetricsSink interface {
	SetOnArtifactUploadError(func(tenantID, errorType string))
	SetOnKMSUnavailable(func(tenantID string))
}

// tierMismatchSink is implemented by the non-envelope-capable artifact
// stores (the in-memory and §17.4 local-filesystem backends) that reject
// a T4 tenant's write under the §12.9 line 1048 storage-boundary tier
// check. The cloud backends do not implement it: they enforce the T4
// contract through their own SSE-KMS resolver and surface
// kms_unavailable instead.
//
// spec: §12.9 line 1048.
type tierMismatchSink interface {
	SetOnTierStoreMismatch(func(tenantID string))
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

// newSSEKeyResolver builds the §12.5 ll. 297-303 SSEKeyResolver the
// MinIO blob store calls on every Put. The closure:
//
//   - Returns (tenantkms.AliasFor(tenantID), true, nil) for a T4
//     tenant: MinIO MUST wrap under the per-tenant alias so the §12.5
//     cryptographic-erasure property holds.
//   - Returns ("", false, nil) for a non-T4 tenant: fall through to
//     the bucket-default SSE-S3 / SSE-KMS key.
//   - Returns ("", true, err) for a T4 tenant whose registry row is
//     unreachable: the blobstore maps it onto
//     CLASSIFICATION_CONTROL_VIOLATION and fires the KMS-unavailable
//     callback. Returning requireKey=true on a lookup failure is the
//     fail-closed posture: we cannot infer the tier from a missing
//     row, and a requireKey=false return would silently downgrade an
//     unknown tenant to the bucket-default key.
//
// spec: §12.5 ll. 297-303 — T4 SSE-KMS resolution and fail-closed
// rejection.
func newSSEKeyResolver(tenants tenantstore.Store) func(string) (string, bool, error) {
	return func(tenantID string) (string, bool, error) {
		row, err := tenants.Get(context.Background(), tenantID)
		if err != nil {
			return "", true, fmt.Errorf("lookup tenant %s: %w", tenantID, err)
		}
		if row.WorkspaceTier == tenantkms.WorkspaceTierT4 {
			return tenantkms.AliasFor(tenantID), true, nil
		}
		return "", false, nil
	}
}

// authFailureAuditAdapter bridges the §10.2 auth middleware to the
// §11.7 audit chain so every §4.2 line 185 tenant-claim rejection
// (TENANT_CLAIM_MISSING / TENANT_NOT_FOUND / TENANT_CLAIM_INVALID_FORMAT)
// produces an `auth_failure` audit row. Rejections that infer a
// tenant id from the JWT claim or dev header land on that inferred
// tenant's chain; the TENANT_CLAIM_MISSING case (no claim presented)
// falls back to the platform chain.
type authFailureAuditAdapter struct {
	sink admin.AuditSink
}

func (a authFailureAuditAdapter) EmitAuthFailure(ctx context.Context, ev authmw.AuthFailureEvent) {
	if a.sink == nil {
		return
	}
	actorTenant := ev.TenantID
	if actorTenant == "" {
		// §4.2: when no tenant could be inferred, land the row on the
		// platform chain (admin.NewChainAuditSink defaults the empty
		// ActorTenantID to "platform").
		actorTenant = ""
	}
	a.sink.EmitAdminEvent(ctx, admin.AuditEvent{
		Type:          authmw.AuthFailureEventType,
		ActorSubject:  ev.UserID,
		ActorTenantID: actorTenant,
		Detail: map[string]any{
			"reason":    ev.Reason,
			"tenant_id": ev.TenantID,
			"user_id":   ev.UserID,
			"jti":       ev.JTI,
		},
		At: ev.At,
	})
}

// experimentRejectionReporter bridges a §10.7 ExperimentRouter
// fail-closed rejection to the §11.7 audit chain, the §16.1 metrics
// registry, and the §25.3 operational-event buffer: it records the
// `experiment.isolation_mismatch` event on all three and increments
// `lenny_experiment_isolation_rejections_total`.
type experimentRejectionReporter struct {
	audit   admin.AuditSink
	metrics *gatewaymetrics.Metrics
	emitter events.EventEmitter
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
		_ = e.emitter.Emit(ctx, events.OperationalEvent{
			Source:          "/v1/sessions",
			Type:            events.EventExperimentIsolationMismatch.CloudEventsType(),
			Severity:        "warning",
			DataContentType: "application/json",
			Data:            data,
		})
	}
}

// ObserveTargetingDuration records the §16.1 line 156
// lenny_experiment_targeting_duration_seconds histogram.
func (e experimentRejectionReporter) ObserveTargetingDuration(_ context.Context, provider string, seconds float64) {
	if e.metrics != nil {
		e.metrics.ObserveExperimentTargetingDuration(provider, seconds)
	}
}

// RecordTargetingError increments the §16.1 line 157
// lenny_experiment_targeting_error_total counter.
func (e experimentRejectionReporter) RecordTargetingError(_ context.Context, provider, errorType string) {
	if e.metrics != nil {
		e.metrics.RecordExperimentTargetingError(provider, errorType)
	}
}

// mcpDelegationAuditor adapts the gateway audit sink to the
// mcptools.DelegationAuditor interface, drawing the §11.7 actor fields
// from the request principal on the context. It also tees the §11.2.1
// billing-stream events (delegation.spawned, delegation.isolation_violation)
// into the per-tenant billing ledger so cost-attribution consumers see
// them alongside the audit chain. spec: §11.2.1. F-11.2.1.
type mcpDelegationAuditor struct {
	sink    admin.AuditSink
	billing *billingfanout.Emitter
}

func (a mcpDelegationAuditor) EmitDelegationEvent(ctx context.Context, eventType string, detail map[string]any) {
	tenantID, subject := "", ""
	if p, ok := authmw.FromContext(ctx); ok {
		tenantID, subject = p.TenantID, p.Subject
	}
	if a.sink != nil {
		ev := admin.AuditEvent{Type: eventType, Detail: detail, At: clockinject.Now().UTC()}
		ev.ActorSubject = subject
		ev.ActorTenantID = tenantID
		a.sink.EmitAdminEvent(ctx, ev)
	}
	// spec: §11.2.1 — tee the cost-attribution / compliance subset into the
	// billing stream. The tenant is the delegating caller's (the parent
	// session's) tenant; the user is the parent session owner.
	switch billingstore.EventType(eventType) {
	case billingstore.EventDelegationSpawned:
		if ev, ok := billingfanout.DelegationSpawned(tenantID, subject, detail); ok {
			a.billing.Emit(ctx, ev)
		}
	case billingstore.EventDelegationIsolationViolation:
		if ev, ok := billingfanout.DelegationIsolationViolation(tenantID, subject, detail); ok {
			a.billing.Emit(ctx, ev)
		}
	}
}

// mcpVCSLeaseAuditor writes the §4.9.2 `credential.leased` audit row each
// time lenny/vcs_token mints a VCS token for a pod's git-credential
// helper, binding the lease to the originating session id per the §26.2
// audit-traceability requirement. It appends directly to the §11.7
// per-tenant hash chain (the §4.9.2 event-type catalog is distinct from
// the admin-audit catalog the EmitAdminEvent path validates against).
// The token is never recorded. spec: §26.2 line 119; §4.9.2. F-26.2.5.
type mcpVCSLeaseAuditor struct {
	appender policy.AuditAppender
	// billing tees the §11.2.1 credential.leased event into the per-tenant
	// billing stream alongside the §4.9.2 audit row. Nil disables the tee.
	// F-11.2.1.
	billing *billingfanout.Emitter
}

func (a mcpVCSLeaseAuditor) RecordVCSLease(ctx context.Context, lease mcptools.VCSLeaseRecord) {
	// spec: §11.2.1 — the credential lease is a billing-stream
	// cost-attribution event bound to the leasing session. A VCS token mint
	// is not pool-backed, so credential_pool_id is empty; the provider is
	// the credential attribution and the access mode is the delivery mode.
	a.billing.Emit(ctx, billingfanout.CredentialLeased(
		lease.TenantID, lease.SessionID, "", lease.Provider, lease.Mode,
	))
	if a.appender == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"session_id": lease.SessionID,
		"provider":   lease.Provider,
		"host":       lease.Host,
		"mode":       lease.Mode,
		"scope":      fmt.Sprintf("vcs.%s.%s", lease.Provider, lease.Mode),
	})
	if err != nil {
		return
	}
	_, _ = a.appender.Append(ctx, lease.TenantID, string(credential.AuditCredentialLeased),
		json.RawMessage(payload), clockinject.Now().UTC())
}

// deriveDowngradeBillingAuditor implements
// sessionserver.DeriveAuditSink. The §7.1 derive rule 5
// derive.isolation_downgrade event is enumerated in the §11.2.1 billing
// event set but not in the §16.7 audit catalog, so it is emitted to the
// per-tenant billing stream (an append-only record matching the audit
// log integrity model) rather than the §11.7 hash chain — the same
// closed-catalog discipline as F-9.2.11. spec: §11.2.1; §7.1 rule 5.
// F-11.2.1.
type deriveDowngradeBillingAuditor struct {
	billing *billingfanout.Emitter
}

func (a deriveDowngradeBillingAuditor) EmitDeriveIsolationDowngrade(ctx context.Context, ev sessionserver.DeriveIsolationDowngradeEvent) {
	a.billing.Emit(ctx, billingfanout.DeriveIsolationDowngrade(
		ev.TenantID, ev.SourceSessionID, string(ev.SourceIsolationProfile),
		ev.TargetPool, string(ev.TargetIsolationProfile), ev.AuthorizingUserSubject, ev.TicketID,
	))
}

// sessionLifecycleAuditor adapts the gateway audit appender to the
// sessionserver.LifecycleAuditSink interface. It writes the §7.1 /
// §16.6 session lifecycle events (session.created and the terminal
// session.{completed,failed,cancelled,expired}) to the §11.7
// hash-chained audit log under the session's own tenant partition. The
// tenant is taken from the session-derived event, satisfying the §11.7
// line 428 write-time tenant-validation rule. The OCSF mapping maps
// these event types to API Activity (6003).
type sessionLifecycleAuditor struct {
	appender policy.AuditAppender
}

func (a sessionLifecycleAuditor) EmitSessionLifecycle(ctx context.Context, ev sessionserver.SessionLifecycleEvent) {
	if a.appender == nil {
		return
	}
	payload := map[string]any{
		"session_id": ev.SessionID,
		"user_sub":   ev.UserID,
		"runtime":    ev.RuntimeRef,
		"state":      ev.State,
	}
	if ev.FailureClass != "" {
		payload["failure_class"] = ev.FailureClass
	}
	if ev.Detail != "" {
		// spec: §7.1 line 112 — workspaceSealFailed records the last MinIO
		// export error in the detail field.
		payload["detail"] = ev.Detail
	}
	if ev.Outcome != "" {
		// spec: §13.4; §11.7 — the §16.6 session.upload boundary records
		// accepted/rejected so the SIEM stream carries the upload-rejection
		// class; the rejected row pairs the outcome with a sub-code reason.
		// F-13.4.8.
		payload["outcome"] = ev.Outcome
	}
	if ev.Reason != "" {
		payload["reason"] = ev.Reason
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	at := ev.At
	if at.IsZero() {
		at = clockinject.Now().UTC()
	}
	_, _ = a.appender.Append(ctx, ev.TenantID, ev.EventType, json.RawMessage(data), at)
}

// interactionResolutionAuditor adapts the gateway audit appender to
// the sessionserver.InteractionAuditSink interface. It writes the
// §7.2 / §11.7 / §16.7 tool-use approve/deny and elicitation
// respond/dismiss events to the hash-chained audit log under the
// session's own tenant partition. The tenant is taken from the
// session-derived event, satisfying the §11.7 line 428 write-time
// tenant-validation rule. The OCSF mapping maps these event types to
// API Activity (6003). spec: §7.2 lines 124-127. F-7.2.8.
type interactionResolutionAuditor struct {
	appender policy.AuditAppender
}

func (a interactionResolutionAuditor) EmitInteractionResolution(ctx context.Context, ev sessionserver.InteractionResolutionEvent) {
	if a.appender == nil {
		return
	}
	payload := map[string]any{
		"session_id":     ev.SessionID,
		"user_sub":       ev.UserID,
		"interaction_id": ev.InteractionID,
		"phase":          ev.Phase,
	}
	if ev.Reason != "" {
		// §15.1 deny body — the optional dismissal reason recorded so
		// the post-incident reconstruction can show why a tool call was
		// denied.
		payload["reason"] = ev.Reason
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	at := ev.At
	if at.IsZero() {
		at = clockinject.Now().UTC()
	}
	_, _ = a.appender.Append(ctx, ev.TenantID, ev.EventType, json.RawMessage(data), at)
}

// auditRetentionMetrics adapts the gateway metrics object to the
// auditretention.MetricsSink. Only the §16.1-cataloged
// lenny_audit_partition_drop_blocked gauge is exported through
// Prometheus; the per-sweep rows-pruned and run-outcome counts are not
// §16.1 series and are surfaced through the pruner's onTick log line, so
// those two sink methods are deliberate no-ops. spec: §16.4 line 378.
type auditRetentionMetrics struct{ m *gatewaymetrics.Metrics }

func (auditRetentionMetrics) AddAuditRowsPruned(int)      {}
func (auditRetentionMetrics) IncAuditRetentionRun(string) {}

func (a auditRetentionMetrics) SetAuditPartitionDropBlocked(partition string, blocked bool) {
	a.m.SetAuditPartitionDropBlocked(partition, blocked)
}

// treeCycleEmitter increments the
// `lenny_delegation_tree_cycle_detected_total` counter when a §8.9
// tree walker hits a cycle in the §8.2 ParentSessionID lineage. Each
// tree-walker surface (REST `/v1/sessions/{id}/tree`, MCP
// `lenny/get_task_tree`) wraps this emitter in a per-package adapter
// that matches the package's TreeCycleObserver interface; both
// adapters fan into the same metric so the corruption surfaces
// regardless of which transport walked the tree. The audit-row half
// of the §8.9 finding (a `delegation.tree_cycle_detected` row) is
// not yet emitted: §16.7 is a closed catalog of spec-listed events
// and the new event type requires a spec change. spec: §8.9 line
// 1003; F-8.9.10 (metric half closed, audit half deferred to a
// future spec addition).
type treeCycleEmitter struct {
	metrics *gatewaymetrics.Metrics
}

func (e treeCycleEmitter) emit(_ context.Context, tenantID, _, _, source string) {
	if e.metrics != nil {
		e.metrics.IncDelegationTreeCycleDetected(tenantID, source)
	}
	// The cycle-detected metric is the operator-visible signal in v1.
	// A `delegation.tree_cycle_detected` audit row is the cleaner long-
	// run answer but lands with the §16.7 catalog extension.
}

// sessionserverTreeCycleObserver adapts treeCycleEmitter to
// sessionserver.TreeCycleObserver for the REST /v1/sessions/{id}/tree
// walker. spec: §8.9 line 1003; F-8.9.10.
type sessionserverTreeCycleObserver struct {
	emitter treeCycleEmitter
}

func (o sessionserverTreeCycleObserver) OnTreeCycle(ctx context.Context, ev sessionserver.TreeCycleEvent) {
	o.emitter.emit(ctx, ev.TenantID, ev.RootSessionID, ev.CycleNodeID, ev.Source)
}

// mcpToolsTreeCycleObserver adapts treeCycleEmitter to
// mcptools.TreeCycleObserver for the lenny/get_task_tree platform-
// tool walker. spec: §8.9 line 1003; F-8.9.10.
type mcpToolsTreeCycleObserver struct {
	emitter treeCycleEmitter
}

func (o mcpToolsTreeCycleObserver) OnTreeCycle(ctx context.Context, ev mcptools.TreeCycleEvent) {
	o.emitter.emit(ctx, ev.TenantID, ev.RootSessionID, ev.CycleNodeID, ev.Source)
}

// credentialAuditor adapts the gateway audit sink to the
// credentialserver.AuditSink interface, drawing the §11.7 actor fields
// from the request principal and the §4.9.2 credential_ref from the
// event detail so the audit query can target the affected credential.
type credentialAuditor struct {
	sink admin.AuditSink
}

func (a credentialAuditor) EmitCredentialEvent(ctx context.Context, eventType string, detail map[string]any) {
	if a.sink == nil {
		return
	}
	ev := admin.AuditEvent{Type: eventType, Detail: detail, At: clockinject.Now().UTC()}
	if p, ok := authmw.FromContext(ctx); ok {
		ev.ActorSubject = p.Subject
		ev.ActorTenantID = p.TenantID
	}
	if ref, ok := detail["credential_ref"].(string); ok {
		ev.TargetResource = ref
	}
	a.sink.EmitAdminEvent(ctx, ev)
}

// tenantsLister adapts a tenantstore.Store into a
// watchdog.TenantLister so the watchdog sweeps every registered
// tenant. In single-tenant deployments it also returns "default" so
// dev-mode sessions are bounded.
// agentPodStateMirror adapts the §4.6.1 agent_pod_state store to the
// §10.1 orphan-session reconciler's MirrorReader, mapping the store's
// PodState onto the reconciler's narrow MirrorPod view. spec: §10.1
// line 51. F-10.1.5.
type agentPodStateMirror struct {
	store agentpodstate.Store
}

func (a agentPodStateMirror) GetByPodID(ctx context.Context, podID string) (orphansession.MirrorPod, bool, error) {
	p, found, err := a.store.GetByPodID(ctx, podID)
	if err != nil || !found {
		return orphansession.MirrorPod{}, found, err
	}
	return orphansession.MirrorPod{PoolID: p.PoolID, Phase: p.State}, true, nil
}

func (a agentPodStateMirror) MirrorLagSeconds(ctx context.Context, poolID string) (float64, error) {
	return a.store.MirrorLagSeconds(ctx, poolID)
}

// sandboxPhaseReader is the §10.1 line 51 direct-Kubernetes fallback the
// orphan-session reconciler consults when the agent_pod_state mirror is
// stale or missing. It reads the authoritative Sandbox phase through the
// §4.6.1 PodLifecycleManager.GetPodStatus surface; a deleted Sandbox
// (ErrPodNotFound) reports found=false, itself a terminal signal.
// spec: §10.1 line 51. F-10.1.5.
type sandboxPhaseReader struct {
	mgr podlifecycle.PodLifecycleManager
	ns  string
}

func (r sandboxPhaseReader) PodPhase(ctx context.Context, sessionID, podID, poolID string) (string, bool, error) {
	st, err := r.mgr.GetPodStatus(ctx, podlifecycle.PodHandle{
		SandboxName: podID,
		Namespace:   r.ns,
		SessionID:   sessionID,
		PoolName:    poolID,
	})
	if errors.Is(err, podlifecycle.ErrPodNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(st.Phase), true, nil
}

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

// createdSweeperReclaim adapts the podsession Binder's claimless reclaim into
// the createdsweeper.Reclaimer the §7.1 created-expiry sweep invokes before it
// drops an abandoned `created`-state row. It closes over the binder (which
// carries the kube Client, Namespace, and CredentialAssigner) so the sweep
// releases the pod claimed at /create and revokes any assigned lease through
// the same ReclaimClaimed call /terminate uses. ReclaimClaimed releases by pod
// name, so the poolRef the Reclaimer carries is dropped here. Returns nil when
// the gateway runs without a pod binder (in-memory mode), leaving the sweep to
// drop the row without a pod release.
//
// spec: §15.1 line 630 (created TTL-expiry releases the pod claim and revokes
// the lease); proposal §4.5.
func createdSweeperReclaim(binder *podsession.Binder) createdsweeper.Reclaimer {
	if binder == nil {
		return nil
	}
	return func(ctx context.Context, podName, _ /* poolRef */, sessionID string) error {
		return binder.ReclaimClaimed(ctx, podName, sessionID)
	}
}

// billingSessionLister enumerates the active (non-terminal) sessions the
// §11.2.1 token_usage.checkpoint producer snapshots, walking every
// registered tenant's session rows. It mirrors
// quotacheckpoint.SessionSubjectLister but returns the per-session tuple
// (a billing checkpoint is per session, not per (tenant, user) subject).
// F-11.2.1.
type billingSessionLister struct {
	sessions sessionstore.Store
	tenants  func(ctx context.Context) ([]string, error)
}

func (l billingSessionLister) ListActiveSessions(ctx context.Context) ([]billingcheckpoint.Session, error) {
	ids, err := l.tenants(ctx)
	if err != nil {
		return nil, err
	}
	var out []billingcheckpoint.Session
	for _, tenantID := range ids {
		rows, err := l.sessions.List(ctx, tenantID, sessionstore.ListFilter{})
		if err != nil {
			return nil, err
		}
		for _, s := range rows {
			if session.IsTerminal(s.State) {
				continue
			}
			out = append(out, billingcheckpoint.Session{TenantID: tenantID, SessionID: s.ID, UserID: s.UserID})
		}
	}
	return out, nil
}

// auditPruneTenants enumerates the audit chains the §16.4 retention
// sweep covers: every registered tenant plus the "platform"
// pseudo-tenant, which carries platform-admin audit rows (e.g.
// compliance.profile_decommissioned) that are not keyed to a registered
// tenant row but still age past the retention window. F-11.7.17.
type auditPruneTenants struct {
	store tenantstore.Store
}

func (a auditPruneTenants) ListTenants(ctx context.Context) ([]string, error) {
	rows, err := a.store.List(ctx, tenantstore.ListFilter{})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows)+1)
	out = append(out, "platform")
	for _, row := range rows {
		out = append(out, row.ID)
	}
	return out, nil
}

// t4TenantSource adapts a tenantstore.Store into a
// tenantkms.TenantSource so the §12.5 line 307 continuous probe
// enumerates exactly the active tenants at workspaceTier T4 — the only
// tenants holding a tenant-scoped KMS key. Soft-deleted tenants are
// dropped (their key is destroyed in §12.8 Phase 4a, so probing it is
// pointless and would flatline the gauge for a tenant that is gone).
type t4TenantSource struct {
	store tenantstore.Store
}

func (t t4TenantSource) T4Tenants(ctx context.Context) ([]string, error) {
	rows, err := t.store.List(ctx, tenantstore.ListFilter{})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.IsActive() && row.WorkspaceTier == tenantkms.WorkspaceTierT4 {
			out = append(out, row.ID)
		}
	}
	return out, nil
}

// activeComplianceProfiles returns the distinct, non-empty
// complianceProfile values across the registered tenants. It backs the
// §11.2.1 billing.retentionDays compliance-floor preflight: a profile
// active on any tenant raises the deployment's retention floor.
// spec: §11.2.1 line 151. F-11.2.15.
func activeComplianceProfiles(ctx context.Context, store tenantstore.Store) ([]string, error) {
	rows, err := store.List(ctx, tenantstore.ListFilter{})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var profiles []string
	for _, row := range rows {
		if row.ComplianceProfile == "" || seen[row.ComplianceProfile] {
			continue
		}
		seen[row.ComplianceProfile] = true
		profiles = append(profiles, row.ComplianceProfile)
	}
	return profiles, nil
}

// playgroundTenantRegistry adapts a tenantstore.Store into the
// playground.TenantRegistry the §27.2 layer-4 Ready-gate consults. It
// reports a tenant as registered when the store returns a row that is
// not soft-deleted; the built-in "default" tenant is always
// registered so a dev-mode playground against the Embedded-Mode
// default tenant resolves without a Postgres row.
type playgroundTenantRegistry struct {
	store tenantstore.Store
}

func (r playgroundTenantRegistry) IsRegistered(tenantID string) (bool, error) {
	if tenantID == "default" {
		return true, nil
	}
	row, err := r.store.Get(context.Background(), tenantID)
	if errors.Is(err, tenantstore.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return row.IsActive(), nil
}

// playgroundAuditEmitter bridges the playground's §27.3.1 / §10.2
// audit events into the durable §11.7 audit chain. spec: §27.3.1 step
// 6 (line 156) — playground.bearer_minted and playground.bearer_revoked
// "share the taxonomy and redaction rules of other auth events in
// §11.7", so they are committed to the principal's per-tenant hash
// chain, not just logged. It keeps the lightweight log line so a mint
// and revoke remain observable in the gateway log stream, and falls
// back to log-only when no durable sink is wired. F-27.3.5.
type playgroundAuditEmitter struct {
	sink admin.AuditSink
}

func (e playgroundAuditEmitter) EmitPlaygroundEvent(ctx context.Context, ev playground.AuditEvent) {
	log.Printf("lenny-gateway: §27 audit %s tenant=%s user=%s jti=%s", ev.Type, ev.TenantID, ev.UserID, ev.BearerJTI)
	if e.sink == nil {
		return
	}
	detail := map[string]any{
		"session_cookie_id": ev.SessionCookieID,
		"bearer_jti":        ev.BearerJTI,
		"origin":            ev.Origin,
	}
	if ev.BearerTTLSeconds > 0 {
		detail["bearer_ttl_seconds"] = ev.BearerTTLSeconds
	}
	for k, v := range ev.Labels {
		detail["label_"+k] = v
	}
	// The event lands on the principal's tenant chain (§11.7 is
	// tenant-scoped); ActorSubject is the playground user.
	e.sink.EmitAdminEvent(ctx, admin.AuditEvent{
		Type:           ev.Type,
		ActorSubject:   ev.UserID,
		ActorTenantID:  ev.TenantID,
		TargetResource: ev.SessionCookieID,
		Detail:         detail,
		At:             ev.At,
	})
}

// EmitMintRejected routes the §10.2 line 243
// playground.bearer_mint_rejected event to the durable §11.7 sink and
// logs it alongside the metric increment. A rejection that fires before
// tenant extraction carries an empty tenant; the sink commits it to the
// platform chain. F-27.3.5.
func (e playgroundAuditEmitter) EmitMintRejected(ctx context.Context, ev playground.MintRejectedEvent) {
	log.Printf("lenny-gateway: §10.2 audit playground.bearer_mint_rejected tenant=%s subject_jti=%s subject_typ=%s invariant=%s ingress=%s",
		ev.TenantID, ev.SubjectJTI, ev.SubjectTyp, ev.InvariantViolated, ev.IngressPath)
	if e.sink == nil {
		return
	}
	e.sink.EmitAdminEvent(ctx, admin.AuditEvent{
		Type:          "playground.bearer_mint_rejected",
		ActorTenantID: ev.TenantID,
		Detail: map[string]any{
			"subject_jti":        ev.SubjectJTI,
			"subject_typ":        ev.SubjectTyp,
			"invariant_violated": ev.InvariantViolated,
			"ingress_path":       ev.IngressPath,
		},
		At: ev.At,
	})
}

// splitCSV splits a comma-separated flag value into a trimmed,
// non-empty slice. An empty input yields a nil slice.
// embeddedOIDCAudience is the only audience the §17.4 embedded OIDC
// provider issues. It mirrors pkg/embedded/oidc.Audience; the gateway
// keeps the literal local so the production binary does not link the
// embedded dev-only provider. spec: §17.4 line 182.
const embeddedOIDCAudience = "dev.local"

// embeddedHMACVerifier wraps the trusted embedded OIDC HMAC verifier so
// the gateway refuses any token whose aud claim is not the embedded
// provider's audience, even when the signature is valid. §17.4 line 182
// requires the gateway to reject foreign-audience tokens; the embedded
// provider's own Verify enforces this, but the gateway trusts the key
// directly and must apply the same check on its side. F-17.4.16.
func embeddedHMACVerifier(trusted jwt.Verifier) jwt.Verifier {
	return jwt.NewClaimChecker(trusted, jwt.ExpectedClaims{
		Audiences: []string{embeddedOIDCAudience},
	})
}

func splitCSV(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseKeyValueCSV splits a comma-separated key=value flag value into
// a map. Trimmed empty entries and entries without `=` are skipped.
// An empty input yields a nil map. The §27.2 line 41
// playground.sessionLabels flag uses this encoding so a Helm value
// like `{origin: playground, env: stage}` renders to
// `--playground-session-labels=origin=playground,env=stage`.
func parseKeyValueCSV(raw string) map[string]string {
	var out map[string]string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "" {
			continue
		}
		if out == nil {
			out = make(map[string]string)
		}
		out[k] = v
	}
	return out
}

// jwksAdvertisesAsymmetric reports whether doc contains at least one
// asymmetric (`kty: RSA` or `kty: EC`) entry. The HMAC-only case
// produces only `kty: oct` entries with no `k` field, so the document
// advertises kid/alg metadata that a verifier cannot use to validate a
// signature. F-10.2.14 keys the §10.3 publication notice on this check
// so an operator who opts into --jwks-publish on top of the v1 HMAC
// signer is told that the JWKS document is metadata-only.
// spec: §10.2 line 195. F-10.2.14.
func jwksAdvertisesAsymmetric(doc jwt.JWKSet) bool {
	for _, k := range doc.Keys {
		if k.Kty != "" && k.Kty != "oct" {
			return true
		}
	}
	return false
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
	cutoff := clockinject.Now().Add(-idempotency.TTL)
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

// exportElicitationIntegrityWeakened refreshes the §16.5 line 460
// ElicitationContentIntegrityWeakened standing-alert gauge: the count
// of active tenants whose §9.2 effective elicitation content-integrity
// mode (max(platformFloor, tenantStored)) is weaker than enforce. The
// gauge keeps the standing warning alert firing while any tenant runs a
// reduced-integrity posture and resolves it to zero once every active
// tenant resolves to enforce. List with the zero filter already drops
// soft-deleted rows, so the count reflects active tenants only. Errors
// are logged but never bubble — the exporter is a best-effort signal
// and must not interrupt the gauge-refresh loop.
//
// spec: §16.5 line 460 (standing-alert numerator)
// spec: §9.2 lines 60, 64 (effective-mode resolution + defaults)
func exportElicitationIntegrityWeakened(ctx context.Context, tenants tenantstore.Store, floor string, m *gatewaymetrics.Metrics) {
	rows, err := tenants.List(ctx, tenantstore.ListFilter{})
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("lenny-gateway: elicitation-integrity weakened gauge: listing tenants failed: %v", err)
		}
		return
	}
	var weakened int
	for _, t := range rows {
		eff := elicitation.ResolveEffectiveWithDefaults(floor, t.ElicitationContentIntegrity)
		if !eff.AtLeast(elicitation.ModeEnforce) {
			weakened++
		}
	}
	m.SetElicitationIntegrityWeakened(weakened)
}

// tenantListerForHPA is the narrow interface exportHPAGauges
// requires. Both tenantsLister (production) and the test fake
// staticTenantLister satisfy it.
type tenantListerForHPA interface {
	ListTenants(ctx context.Context) ([]string, error)
}

// exportHPAGauges refreshes the §4.1 / §16.1 horizontal-scaling
// gauges: the primary scale-out trigger (request queue depth, the
// in-flight HTTP request count on this replica), the secondary HPA
// metric (active streaming connections), and the capacity-ceiling
// numerator (non-terminal sessions tracked across tenants). Each
// gauge is set unconditionally on every poll so a transient store
// failure does not strand the gauge at a stale value — the next poll
// retries. Errors are logged but never bubble: the exporter is a
// best-effort signal and must not interrupt the watchdog loop.
//
// spec: §4.1 SCL-026 (HPA metric roles)
// spec: §16.1 (gauge metric definitions)
// spec: §16.5 GatewaySessionBudgetNearExhaustion (denominator gauge)
func exportHPAGauges(ctx context.Context, sessions sessionstore.Store, lister tenantListerForHPA, bus *sessionevents.Bus, m *gatewaymetrics.Metrics) {
	// Request queue depth — the §4.1 SCL-026 primary HPA scale-out
	// trigger. The metric is the count of HTTP requests the metrics
	// Middleware is currently servicing on this replica.
	m.SetRequestQueueDepth(m.InflightRequests())

	// Active streams — the §4.1 SCL-026 secondary HPA metric. Counts
	// in-flight SSE subscribers on this replica's sessionevents bus.
	if bus != nil {
		m.SetActiveStreams(bus.ActiveSubscribers())
		// spec: §10.4 line 389 / §16 catalog — sample the worst
		// per-session SSE replay buffer utilization so the
		// lenny_event_bus_replay_buffer_utilization gauge tracks the
		// pressure on the §10.4 reconnect-window assumption. F-10.4.11.
		m.SetReplayBufferUtilization(bus.MaxReplayBufferUtilization())
	}

	// Active sessions — the §16.5 GatewaySessionBudgetNearExhaustion
	// alert numerator. Walks every tenant and counts non-terminal
	// sessions. Production scale will replace the per-tenant list
	// with a SessionStore.Count primitive; the per-tenant walk is
	// adequate for current tier sizes.
	tenants, err := lister.ListTenants(ctx)
	if err != nil {
		log.Printf("lenny-gateway: HPA gauge export: listing tenants failed: %v", err)
		return
	}
	var active int
	for _, tenant := range tenants {
		rows, err := sessions.List(ctx, tenant, sessionstore.ListFilter{})
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("lenny-gateway: HPA gauge export: tenant %q list failed: %v", tenant, err)
			}
			continue
		}
		for _, row := range rows {
			if !session.IsTerminal(row.State) {
				active++
			}
		}
	}
	m.SetActiveSessions(active)
}

// exportSessionAvailabilityRatio refreshes the §16.5 Session availability
// SLI: lenny_session_unavailability_ratio is the fraction of active
// sessions currently in a retry/recovery state (resume_pending, resuming,
// awaiting_client_action), the inverse of "uptime of sessions not in
// retry/recovery state". The SessionAvailabilityBurnRate alert reads it.
// The ratio is 0 when there are no active sessions (an idle gateway is
// fully available). F-16.5.3.
func exportSessionAvailabilityRatio(ctx context.Context, sessions sessionstore.Store, m *gatewaymetrics.Metrics) {
	active, err := sessions.CountActiveSessionsGlobal(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("lenny-gateway: session-availability gauge export: active count failed: %v", err)
		}
		return
	}
	if active == 0 {
		m.SetSessionUnavailabilityRatio(0)
		return
	}
	recovery, err := sessions.CountActiveSessionsInRecoveryGlobal(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("lenny-gateway: session-availability gauge export: recovery count failed: %v", err)
		}
		return
	}
	m.SetSessionUnavailabilityRatio(float64(recovery) / float64(active))
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

// alertHealthSource implements health.AlertStatusSource over this
// replica's in-process §25.13 alert tracker. For a component it returns
// the worst severity among firing §16.5 alerts mapped to it: any firing
// critical alert reports unhealthy, otherwise a firing warning reports
// degraded. ok is false when no firing alert maps to the component, in
// which case the dependency probe's verdict stands.
// spec: §25.3 lines 443-451.
type alertHealthSource struct {
	eval *atomic.Pointer[evaluator.Evaluator]
}

func (s alertHealthSource) ComponentStatus(component string) (health.Status, []string, bool) {
	e := s.eval.Load()
	if e == nil {
		return "", nil, false
	}
	var firing []string
	hasCritical := false
	for _, al := range e.FiringAlerts() {
		comp, ok := rules.HealthComponentFor(al.Rule.Name)
		if !ok || comp != component {
			continue
		}
		firing = append(firing, al.Rule.Name)
		if al.Rule.Severity == rules.SeverityCritical {
			hasCritical = true
		}
	}
	if len(firing) == 0 {
		return "", nil, false
	}
	if hasCritical {
		return health.StatusUnhealthy, firing, true
	}
	return health.StatusDegraded, firing, true
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
		p == "/openapi.json", p == "/v1/openapi.json":
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

// envFlagDefault returns true / false from the env var name, or def
// when the var is unset. Used for flags that default on (e.g., the
// §10.3 --jwks-publish endpoint) where envFlag's always-false-default
// semantics do not match the spec posture.
func envFlagDefault(name string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

// envFloat returns the env var name parsed as a float64, or def when
// the var is unset or does not parse. Used to default the
// --billing-dual-control-threshold flag from the environment.
func envFloat(name string, def float64) float64 {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

// envOr returns the env var name, or def when the var is unset or
// empty. Used to default the §27.2 playground string flags.
func envOr(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

// envInt returns the env var name parsed as an int, or def when the
// var is unset or does not parse. Used to default the §27.2
// playground integer flags.
func envInt(name string, def int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// envDuration mirrors envInt for time.Duration-valued flags. Accepts
// any value time.ParseDuration parses ("60s", "5m", "1h"); returns def
// on missing or unparseable env vars.
func envDuration(name string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

// envBool returns the env var parsed as a bool, or def when the var is
// unset or does not parse. Accepts the strconv.ParseBool truth values
// ("1", "true", "TRUE", "0", "false", ...).
func envBool(name string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// auditBatchingNoSIEM reports the §12.3 line 99 AuditBatchingNoSIEM
// condition: production mode has T2 audit batching enabled but no SIEM
// endpoint, so buffered T2 audit events would be lost on a crash with
// no external durable copy to recover from. F-12.3.15.
func auditBatchingNoSIEM(env string, batchingEnabled, siemConfigured bool) bool {
	return env == "production" && batchingEnabled && !siemConfigured
}

// envInt64 mirrors envInt for int64-valued flags (idempotency body
// cap, size limits). Returns def on missing or unparseable env vars.
func envInt64(name string, def int64) int64 {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// barrierCoordinatorDispatch adapts a *barrier.Coordinator to the
// prestop.BarrierDispatcher interface: the staged drain only needs the
// pass to fire under its ACK budget, so the DispatchSummary is discarded
// and any enumeration error is surfaced for the hook to log.
// spec: §10.1 lines 163-181.
type barrierCoordinatorDispatch struct{ c *barrier.Coordinator }

func (d barrierCoordinatorDispatch) Dispatch(ctx context.Context) error {
	_, err := d.c.Dispatch(ctx)
	return err
}

// parseTerminationGrace returns the §4.4 line 263 termination grace
// period the preStop hook uses to bound the staged drain. It reads
// LENNY_TERMINATION_GRACE_SECONDS first; the chart-default 240s
// applies when the env is unset or invalid.
//
// spec: §17.8.2 — terminationGracePeriodSeconds: 240 default.
func parseTerminationGrace() time.Duration {
	seconds := envInt("LENNY_TERMINATION_GRACE_SECONDS", prestop.DefaultTerminationGraceSeconds)
	if seconds <= 0 {
		seconds = prestop.DefaultTerminationGraceSeconds
	}
	return time.Duration(seconds) * time.Second
}

// parseWindowOverrides parses the §25.3 recommendations window-override
// flag (comma-separated category=duration pairs, e.g.
// "warm_pool_sizing=12h,credential_pool_sizing=72h") into the map the
// recommendations.Config expects. Malformed pairs and unparseable
// durations are skipped so one bad entry does not drop the rest.
// spec: §25.3 line 596. F-25.3.12.
func parseWindowOverrides(raw string) map[string]time.Duration {
	out := map[string]time.Duration{}
	for _, pair := range splitAndTrim(raw) {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		d, err := time.ParseDuration(strings.TrimSpace(v))
		if err != nil || d <= 0 {
			continue
		}
		out[strings.TrimSpace(k)] = d
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// splitAndTrim splits a comma-separated string and drops empty entries
// after trimming whitespace. Used to parse the --redis-sentinel-addrs
// list.
func splitAndTrim(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// defaultDeriveLock picks the §7.1 line 92 derive-lock implementation.
// Redis-backed serialization is mandatory across replicas; the in-
// process Memory fallback is correct for the minimal-gateway and
// single-replica deployments (the in-memory store mutex inside
// derive.go is the only other serialization path in v1, and it
// serializes by accident — not by spec). F-7.1.12.
func defaultDeriveLock(client redis.UniversalClient) derivelock.Lock {
	if client != nil {
		return derivelock.NewRedis(client)
	}
	return derivelock.NewMemory(derivelock.DefaultWait)
}

// dialTokenService dials lenny-token-service for the §4.3 credential
// materialization path. mTLS is required in production deployments —
// the gateway has a distinct client identity per replica per §4.3 —
// and certPath / keyPath / caPath name the project's mTLS material.
// With every TLS flag empty the dial falls through to plaintext for
// dev mode, which is the path the gateway-side bufconn tests exercise.
func dialTokenService(addr, certPath, keyPath, caPath string) (*grpc.ClientConn, error) {
	if addr == "" {
		return nil, fmt.Errorf("token service address is empty")
	}
	var transport grpc.DialOption
	switch {
	case certPath == "" && keyPath == "" && caPath == "":
		transport = grpc.WithTransportCredentials(insecure.NewCredentials())
	case certPath == "" || keyPath == "" || caPath == "":
		return nil, fmt.Errorf("token service mTLS requires --token-service-tls-cert, --token-service-tls-key, and --token-service-ca to all be set")
	default:
		// spec: §10.3 line 338 — present the gateway leaf via a
		// filesystem-watching GetClientCertificate callback so a
		// cert-manager renewal is picked up on the next dial without a
		// gateway restart.
		reloader, err := certreload.New(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("load token-service client cert: %w", err)
		}
		caPEM, err := os.ReadFile(caPath)
		if err != nil {
			return nil, fmt.Errorf("read token-service CA bundle: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("token-service CA bundle %q parsed no certificates", caPath)
		}
		transport = grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			GetClientCertificate: reloader.GetClientCertificate,
			RootCAs:              pool,
			MinVersion:           tls.VersionTLS13,
		}))
	}
	return grpc.NewClient(addr, transport)
}

// interceptorIdentity carries the §10.3 NET-063 peer-validation inputs a
// dialInterceptor call needs: the SPIFFE trust domain, the
// interceptor-namespace allowlist, the shared revocation deny list, and
// the §16.1 handshake-metric observer. The zero value disables SPIFFE
// validation (trust domain empty), leaving the existing CA-only dial.
type interceptorIdentity struct {
	trustDomain string
	namespaces  []string
	denyList    spiffe.DenyChecker
	observe     interceptordial.Observer
}

// dialInterceptor dials a §4.8 external RequestInterceptor service. mTLS
// is used when cert/key/ca are all set; with all three empty the dial
// falls through to plaintext for dev mode. The §13.2 NET-058
// NetworkPolicy that scopes egress to the interceptor namespace is
// templated by the Helm chart; this dial assumes that egress is
// permitted.
//
// For an in-cluster interceptor (a .svc endpoint host) with a configured
// SPIFFE trust domain, the dial pins tls.Config.ServerName to the
// endpoint host (DNS-SAN validation, spec §10.3 line 328) and installs a
// spiffe.InterceptorPeerVerifier that validates the SPIFFE-URI SAN
// against the trust domain and namespace allowlist and rejects revoked
// certificates (NET-063). Every mTLS handshake outcome is timed into the
// §16.1 lenny_interceptor_mtls_handshake_duration_seconds histogram.
func dialInterceptor(addr, certPath, keyPath, caPath string, id interceptorIdentity) (*grpc.ClientConn, error) {
	if addr == "" {
		return nil, fmt.Errorf("interceptor endpoint is empty")
	}
	var transport grpc.DialOption
	switch {
	case certPath == "" && keyPath == "" && caPath == "":
		transport = grpc.WithTransportCredentials(insecure.NewCredentials())
	case certPath == "" || keyPath == "" || caPath == "":
		return nil, fmt.Errorf("external interceptor mTLS requires --external-interceptor-tls-cert, --external-interceptor-tls-key, and --external-interceptor-ca to all be set")
	default:
		// spec: §10.3 line 338 — present the gateway leaf via a
		// filesystem-watching GetClientCertificate callback so a
		// cert-manager renewal is picked up on the next dial without a
		// gateway restart.
		reloader, err := certreload.New(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("load external-interceptor client cert: %w", err)
		}
		caPEM, err := os.ReadFile(caPath)
		if err != nil {
			return nil, fmt.Errorf("read external-interceptor CA bundle: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("external-interceptor CA bundle %q parsed no certificates", caPath)
		}
		host := addr
		if h, _, splitErr := net.SplitHostPort(addr); splitErr == nil {
			host = h
		}
		// spec: §10.3 line 328 (NET-063) — only an in-cluster
		// interceptor presents a SPIFFE identity; an external endpoint
		// (public FQDN or raw IP) is out of NET-063 scope (spec line 322)
		// and keeps CA + DNS-SAN validation only.
		var verifier *spiffe.InterceptorPeerVerifier
		if id.trustDomain != "" && interceptordial.InCluster(host) {
			verifier = &spiffe.InterceptorPeerVerifier{
				TrustDomain: id.trustDomain,
				Namespaces:  id.namespaces,
				DenyList:    id.denyList,
				OnMismatch: func(reason spiffe.MismatchReason, uri string, err error) {
					log.Printf("lenny-gateway: §10.3 NET-063 interceptor_identity_mismatch endpoint=%s reason=%s uri=%q: %v", addr, reason, uri, err)
				},
			}
		}
		transport = grpc.WithTransportCredentials(interceptordial.Credentials(interceptordial.Options{
			Reloader:   reloader,
			RootCAs:    pool,
			ServerName: host,
			Verifier:   verifier,
			Observe:    id.observe,
		}))
	}
	return grpc.NewClient(addr, transport)
}
