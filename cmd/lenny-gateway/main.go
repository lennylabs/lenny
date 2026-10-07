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
	"log"
	"os"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/observability/logging"
)

// Build metadata, overridable at link time via -ldflags
// "-X main.buildVersion=... -X main.buildCommit=... -X main.buildDate=...".
var (
	buildVersion = "dev"
	buildCommit  = "unknown"
	buildDate    = "unknown"
)

func main() {
	// spec: §16.4 — install the structured JSON logger as the
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
	// spec: §11.2 — the budget terminator runs the same terminal
	// pipeline a watchdog or operator force-terminate runs, so an
	// over-budget session releases its pod and emits its terminal audit /
	// billing / SSE signals exactly once.
	w.budgetTerminator.onTerminal = sessionSrv.OnSessionTerminal
	// spec: §28.5.1 (CH-ATTACH Degradation.) — the pod executor's reader
	// reports a held Attach stream's failure through the §7.3 classifier as
	// runtime_crash, after the session server confirms this replica still
	// coordinates the session. The handler is set before any Send runs.
	if pe, ok := w.exec.(*executor.PodExecutor); ok {
		pe.SetStreamFailureHandler(sessionSrv.ReportAttachStreamFailure)
	}

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
	// The reserved-hold coordinator and recycle-boundary
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
