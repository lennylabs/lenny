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
	"github.com/lennylabs/lenny/pkg/audit/integrity"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/introspection"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	blobproviderflags "github.com/lennylabs/lenny/pkg/blobstore/providerflags"
	"github.com/lennylabs/lenny/pkg/circuitbreaker"
	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingcheckpoint"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingfanout"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/barrier"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credfallback"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker/breakerstore/cachingstore"
	idempgstore "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/operability/health"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/prestop"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
	"github.com/lennylabs/lenny/pkg/gateway/quota/storagequota"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/slothealth"
	"github.com/lennylabs/lenny/pkg/gateway/session/createdsweeper"
	"github.com/lennylabs/lenny/pkg/gateway/session/orphansession"
	"github.com/lennylabs/lenny/pkg/gateway/session/recycle"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/storage/derivelock"
	"github.com/lennylabs/lenny/pkg/idempotency"
	"github.com/lennylabs/lenny/pkg/mtls/certreload"
	"github.com/lennylabs/lenny/pkg/mtls/interceptordial"
	"github.com/lennylabs/lenny/pkg/mtls/spiffe"
	"github.com/lennylabs/lenny/pkg/observability/logging"
	"github.com/lennylabs/lenny/pkg/podlifecycle"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
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
// composition root: a flat ordered sequence of per-subsystem build-step
// calls (each defined in a subsystem-named sibling file and documented
// there), terminating in the signal-driven graceful shutdown. No subsystem
// is constructed inline here; every build step records its outputs on the
// gatewayWiring accumulator, and this root re-aliases a recorded output to a
// local name only where it is read more than once, passing single-use outputs
// directly from the accumulator at the call site.
//
// This is the ordered call sequence proposal 0020 §4 Part A R1 specifies. It
// remains over the advisory long-funcs line threshold (the dispatcher proposal
// 0020 §6/§7 "Realistic per-function targets" explicitly accepts): its
// residual length is the cross-step value threading and the multi-argument
// build-step calls, not un-extracted construction. Its statement count is a
// fraction of the former monolith's, and the gatewayWiring accumulator carries
// the threading rather than 20-to-30-value constructor returns (see wiring.go).
//
// spec: §4.1 — the gateway is one component internally partitioned into
// subsystem boundaries (Go interfaces within a single binary); this
// function constructs each in dependency order.
func runGateway(f *gatewayFlags) {
	w := &gatewayWiring{f: f}

	// Each step below constructs one subsystem, records its outputs on the
	// accumulator (see wiring.go and wiring_fields.go), and is documented in
	// its own sibling file. The composition root re-aliases the recorded
	// outputs to local names where a later step takes them as an explicit
	// argument. spec: §4.1.

	// §10.3/§17.4/§16.3/§16.5 startup gates and process-wide providers.
	w.buildStartupGates()
	elicitationFloorProvider := w.elicitationFloorProvider
	resolvedNoEnvPolicy := w.resolvedNoEnvPolicy

	// §4.2/§4.4/§4.5 persistence and §4.3/§10.2/§10.3 credential surfaces. The
	// §4.3 token-service connection close is relocated here from the stores
	// block so it runs at process shutdown; the §17.4 SQLite flush-loop cancel
	// stays a synchronous call in runServers (gated on sqliteDB, before
	// sqliteDB.Close), matching the original timing exactly. F-17.4.2.
	w.buildStores()
	defer func() {
		if w.tokenServiceConn != nil {
			_ = w.tokenServiceConn.Close()
		}
	}()

	// §16.1 metric registry plus the back-fill onto the stores built before it
	// existed, and the §10.1/§13.3/§4.1 monitors.
	w.buildMetricsBackfill()
	gwMetrics := w.gwMetrics
	dsMonitor := w.dsMonitor

	// §11.7 audit pipeline (hash chain, sinks, §16.4 pruner, §12.6 EventBus
	// retranscriber, §11.7 OCSF / §12.3 SIEM forwarders).
	w.buildAuditPipeline()
	auditSink := w.auditSink
	auditAppender := w.auditAppender
	auditValidator := w.auditValidator

	// §12.8: re-surface any tenant that combines billingErasurePolicy
	// exempt with a regulated compliance profile so the retention
	// posture cannot silently persist across redeployments.
	if err := admin.EmitBillingErasureExemptRegulatedStartup(
		context.Background(), w.tenants, auditSink, nil,
	); err != nil {
		log.Printf("lenny-gateway: WARNING: billing-erasure-exempt startup scan: %v", err)
	}

	// §10.6/§4.9/§10.2/§8.8 auxiliary registries, the §25.3/§25.5 ops-event
	// emitter, the §14 VCS resolver, and the §8.8 usage Builder.
	w.buildAuxStores()
	environments := w.environments
	tenantAccess := w.tenantAccess
	opsEmitter := w.opsEmitter
	credentialPools := w.credentialPools
	vcsCreds := w.vcsCreds
	customRoles := w.customRoles
	usage := w.usage
	taskUsageBuilder := w.taskUsageBuilder

	// §4.8 policy interceptor chain and the §11.2/§12.4 quota surfaces.
	w.buildPolicyChain(auditAppender, auditValidator)
	policyChain := w.policyChain
	policyAuditSink := w.policyAuditSink
	quotaCounter := w.quotaCounter

	// §11.2 token-usage checkpoint / §24.6 reconcile, then the §4.8 external
	// interceptor and guardrails registration (recording the §10.3 mTLS deny
	// list for the control server).
	w.buildQuotaCheckpoint()
	quotaCheckpointSvc := w.quotaCheckpointSvc
	w.buildInterceptorRegistration()

	// §4.2 session-server dependencies (the §4.1 Upload Handler gate, the §8.5
	// request_input registry, the §10.7 sticky/provider caches, the §14
	// completion webhook, the §11.2 budget enforcer, the §8.6 lease budget and
	// registrars, the §6.2 activity stamper, and the §5.2/§6.2 slot health).
	w.buildSessionDeps()
	inputWaits := w.inputWaits
	sessionBudgetEnforcer := w.sessionBudgetEnforcer
	leaseBudgets := w.leaseBudgets
	activityStamper := w.activityStamper
	slotHealth := w.slotHealth

	// §4.2 session server (the §4.1 Stream Proxy and Upload Handler behind the
	// sessionserver interfaces); the returned server threads to the MCP
	// fabric, the admin router, the HTTP surface, and the watchdog.
	sessionSrv := w.buildSessionServer(
		gwMetrics, activityStamper, sessionBudgetEnforcer, dsMonitor,
		environments, tenantAccess, opsEmitter, credentialPools, vcsCreds,
		customRoles, resolvedNoEnvPolicy, auditSink, w.sessionStickyCache,
		w.experimentProviders, usage, taskUsageBuilder, w.sessionLeaseRegistrar,
		w.leaseExtDefaults, quotaCheckpointSvc, policyChain, policyAuditSink,
		auditAppender, inputWaits, w.uploadSubsystem, w.uploadMetrics, slotHealth,
		w.callbackValidator, w.callbackSeal, w.callbackDispatcher,
	)
	// spec: §11.2 line 44 — the budget terminator runs the same terminal
	// pipeline a watchdog or operator force-terminate runs, so an
	// over-budget session releases its pod and emits its terminal audit /
	// billing / SSE signals exactly once.
	w.budgetTerminator.onTerminal = sessionSrv.OnSessionTerminal

	// §4.9 end-user credential surface (translators, credential store/server,
	// the pre-authorized user-source materializer, the §4.9.1 KMS-rotation
	// job) and the §9.3 connector OAuth flow.
	w.buildCredentialSurface(sessionSrv)

	// §9.1 MCP fabric (the delegation-policy/external-interceptor/
	// deployment-config stores, the §8.2 delegation service, the §9.1 MCP
	// server with every tool family, and the §15.2 SSE attach channel).
	w.buildMCPSurface(gwMetrics, sessionSrv, policyChain, auditSink, auditAppender,
		policyAuditSink, w.childLeaseRegistrar, w.maxInputResolver, environments,
		resolvedNoEnvPolicy, inputWaits, activityStamper, taskUsageBuilder,
		vcsCreds, elicitationFloorProvider)
	mcpSrv := w.mcpSrv

	// §13.3 / §10.3 / §4.9 cross-replica revocation propagators and the §4.9
	// proactive lease-renewal worker.
	w.buildRevocationWiring()
	revProp := w.revProp

	// §15.1 admin REST subsystem. It records the router on w.adminRouter and
	// returns the locals the control server, the LLM proxy, and the mux
	// (the §10.5 runtime-upgrade store) still consume.
	connectorAuthorizer, connectorInvoker, ruStore, erasureSemanticCache := w.buildAdminRouter(
		gwMetrics, w.delegationSvc, environments, w.connectorCreds, w.connectorOAuth,
		w.credentialRekeyJob, policyChain, auditSink, auditAppender,
		w.wireAudit, w.adminStickyFlusher, w.erasureSticky, w.deploymentConfig,
		credentialPools, customRoles, w.delegationPolicies, w.interceptors,
		leaseBudgets, opsEmitter, w.opsEventBuffer, sessionSrv, tenantAccess,
		w.auditOpsStore, w.auditPruner, auditValidator, w.credRenewalProp,
		elicitationFloorProvider, quotaCheckpointSvc, quotaCounter,
		w.quotaFailOpenAccum, revProp,
	)

	// §15.1 REST mux and HTTP server. Records w.mux and w.httpSrv.
	w.buildHTTPSurface(
		gwMetrics, sessionSrv, w.openaiHandler, w.responsesHandler, w.credServer,
		mcpSrv, policyChain, auditSink, auditAppender, opsEmitter, environments,
		w.driftMonitor, dsMonitor, w.failOpenReplicas, w.revCache, revProp, ruStore,
		w.siemHealthChecker, resolvedNoEnvPolicy,
	)

	// §4.9 LLM Proxy subsystem (a named §4.1 extraction target).
	llmProxySrv := w.buildLLMProxy(policyChain,
		sessionBudgetEnforcer, activityStamper, auditSink, erasureSemanticCache,
		usage, quotaCounter, w.tenantLimits)

	// §8.6 GatewayControl gRPC server (the adapter→gateway control surface,
	// the §9.1/§9.3 tool bridges, the §4.7 scrub-report service) and the §6.2 /
	// §11.3 session watchdog.
	w.buildControlServer(gwMetrics, mcpSrv, auditAppender, slotHealth, w.mtlsDeny,
		connectorAuthorizer, connectorInvoker, leaseBudgets, sessionSrv)
	// The §3.2 reserved-hold coordinator and §3.4 recycle-boundary
	// coordinator are stopped on shutdown so the in-process timers and
	// re-warm polls do not run against a draining client. The original
	// inline control-server block registered these Stop defers only inside
	// the scrub-report branch, before the §6.2 watchdog-context cancel; these
	// two defers are registered ahead of defer w.watchdogCancel() below to
	// preserve that original LIFO teardown order (watchdogCancel runs first,
	// then recycleBoundary.Stop, then holdCoordinator.Stop). Re-evaluate the
	// same predicate the original scrub-report branch used so the
	// process-lifetime defers fire under the same condition.
	if w.scrubReportServiceWired() {
		if w.holdCoordinator != nil {
			defer w.holdCoordinator.Stop()
		}
		if w.recycleBoundary != nil {
			defer w.recycleBoundary.Stop()
		}
	}
	// Cancel the §6.2 watchdog context at process shutdown rather than when
	// buildControlServer returns. Registered after the coordinator Stops so
	// the LIFO shutdown cancels the watchdog context first, matching the
	// original ordering.
	defer w.watchdogCancel()

	// §4.1 — record the built session server, then launch the §4.1
	// background-worker step (it reads w.sessionSrv and the recorded
	// propagators to drive the periodic sweepers).
	w.sessionSrv = sessionSrv
	w.startBackgroundWorkers()

	// §17 — record the LLM proxy server, then hand off to runServers (the
	// §25.13 alert tracker, the signal handler, and the run-and-shutdown loop).
	w.llmProxySrv = llmProxySrv
	w.runServers()
}

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
// and binds its listener. It returns (nil, nil, nil, nil) when addr is
// empty, which disables the GatewayControl listener. A non-empty addr
// that cannot be bound returns the error so the gateway fails fast.
//
// The server hosts the surviving §9.1 platform-tool, §9.3
// connector-tool, and §4.7 scrub-report RPCs; the §8.6 lease-extension
// dispatch runs in-process (leasecontrol.ExtendForBudget) rather than as a
// wire RPC here. It returns the constructed leasecontrol.Service so the
// composition root wires the in-process §8.6 budget-exhaustion trigger onto
// the proxy's sessionbudget enforcer. Its budget state is the
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
// every extension decision drives the §16 line 66
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
func newGatewayControlServer(addr string, budgets *leasecontrol.MemoryBudgetSource, metrics leasecontrol.MetricEmitter, auditor leasecontrol.Auditor, elicitor leasecontrol.Elicitor, autoCounter ratelimit.Counter, defaultAutoMaxPerMin int, platformTools leasecontrol.PlatformToolService, connectorTools leasecontrol.ConnectorToolService, treeGranter leasecontrol.TreeBudgetGranter, scrubReports leasecontrol.ScrubReportService, replicaID, tlsCert, tlsKey, clientCA, trustDomain, saTokenAudience string, saTokenVerifier leasecontrol.TokenVerifier, denyList spiffe.DenyChecker) (*grpc.Server, net.Listener, *leasecontrol.Service, error) {
	if addr == "" {
		return nil, nil, nil, nil
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
		return nil, nil, nil, fmt.Errorf("build GatewayControl service: %w", err)
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("bind GatewayControl listener on %s: %w", addr, err)
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
		return nil, nil, nil, fmt.Errorf("§8.6 GatewayControl mTLS credentials: %w", err)
	}
	var opts []grpc.ServerOption
	if tlsOpt != nil {
		opts = append(opts, tlsOpt)
	}
	// The interceptor fails closed when client-cert verification is
	// active (clientCA set): every surviving GatewayControl call
	// (platform-tool, connector-tool, scrub-report) must arrive over a
	// verified mTLS chain, since the handlers trust the session_id in the
	// request body and have no other proof of the caller's identity.
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
	// Return svc so the composition root wires the §8.6 in-process
	// budget-exhaustion trigger: the proxy's sessionbudget enforcer calls
	// svc.ExtendForBudget as its extension seam, and svc.SetReclaimer receives
	// the §4.9 usage recorder so the per-tree episode fan-out (and the in-path
	// Granted path) can raise or terminate a session that detached at the
	// in-path deadline while keeping the raise alive across the next
	// Enforcer.Record (the recorder accumulates the granted delta). spec: §8.6
	// line 629; proposal 0023 S3/S4/S6.
	return gs, lis, svc, nil
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
	sessionRetirer, err := recycle.NewSessionCountRetirer(recycle.SessionCountRetirerOptions{
		Client:    cl,
		Namespace: agentNamespace,
		Pools:     pools,
		Runtimes:  runtimes,
		Metrics:   metrics,
		Now:       now,
	})
	if err != nil {
		return nil, fmt.Errorf("build session-count retirer: %w", err)
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
		Counters:       recycle.NewRecycleCounterStore(counters),
		Ledger:         ledger,
		SessionRetirer: sessionRetirer,
		Inspector:      inspector,
		Driver:         driver,
		Metrics:        recycle.NewRetirementMetrics(metrics),
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
// the spec WARN message for each broken chain. Under the §11.7 nextval
// allocator a benign transaction-rollback gap keeps its prev_hash link
// intact and verifyChainWindow reports it detected-but-not-broken, so
// only a non-linking prev_hash across the gap (a committed audit row
// tampered with or removed) reaches the boundary-populated WARN branch.
// A broken chain fires the §16.5 AuditChainGap alert through the metric;
// the gateway does not refuse to start. spec: §12.3 line 101, §11.7.
// F-12.3.9. F-11.2.10.
func runStartupChainContinuityCheck(ctx context.Context, db, ctrlDB integrity.Querier, lastN int, m *gatewaymetrics.Metrics) {
	// db is the ledger instance holding audit_log (the separate §12.3
	// billing/audit Postgres when configured, otherwise the primary);
	// ctrlDB is the control-plane pool where the tenants.state deletion
	// skip-set is authoritative, so the retained gdpr.*-only remnant of a
	// tenant in state='deleting' or state='deleted' does not raise a false
	// §16.5 AuditChainGap alert. In the co-located topology the call site
	// passes the same pool for both. spec: §12.3 line 103, §12.8.
	results, err := integrity.CheckChainContinuityRecent(ctx, db, ctrlDB, lastN)
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
			// After the §11.7 nextval switch the audit sequence_number is
			// allocated by nextval, so a benign transaction-rollback gap has
			// an intact prev_hash link and verifyChainWindow classifies it
			// detected-but-not-broken (no boundaries populated). The only
			// boundary-populated ChainBroken (GapHighSeq() > 0) is a
			// non-linking prev_hash across the gap — a committed audit row
			// tampered with or removed — never buffered-T2 loss, which is the
			// accepted unsignaled tradeoff of the opt-in audit.batchingEnabled
			// path and carries no chain-level signal. The message matches the
			// §12.3 line 101 WARN string verbatim; the surrounding §12.3 prose
			// directs the operator to reconcile against the independent SIEM
			// copy and document the break in their compliance records.
			// spec: §12.3 line 101, §11.7. F-11.2.10.
			log.Printf("Audit chain broken for tenant %s: prev_hash does not link across sequence %d to %d (~%s to %s). This indicates a committed audit row was tampered with or removed. T3/T4 events are synchronous and will not appear in chain gaps.",
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
