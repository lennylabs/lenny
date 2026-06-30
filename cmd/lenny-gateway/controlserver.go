// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"time"

	agentpodstatepg "github.com/lennylabs/lenny/pkg/agentpodstate/pgstore"
	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorauthz"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorinvoke"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectortools"
	"github.com/lennylabs/lenny/pkg/gateway/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/slothealth"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionage"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionidle"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	mtlsdenylist "github.com/lennylabs/lenny/pkg/mtls/denylist"
)

// buildControlServer is the §4.1 composition-root build step (R1) for the
// §8.6 GatewayControl gRPC server and the §6.2 / §11.3 pre-running
// watchdog. It constructs the adapter→gateway control surface (the
// ExtendLease RPC, the §9.1 platform-tool and §9.3 connector-tool bridges,
// the §4.7 scrub-report service) and the session-state watchdog, and
// records the gRPC server, its listener, the watchdog, and the watchdog
// context (plus its cancel) on the accumulator for the run loop.
//
// The process-lifetime defers the original inline block registered (the
// watchdog-context cancel and the §3.2/§3.4 hold and recycle coordinator
// Stops) are relocated to the composition root (runGateway) so they fire at
// process shutdown rather than when this build step returns; see the
// scrub-report-branch predicate runGateway re-evaluates to defer the
// coordinator Stops exactly when the original branch did.
//
// spec: §4.1 gateway subsystem seams; §8.6 GatewayControl; §6.2 / §11.3
// session watchdog.
func (w *gatewayWiring) buildControlServer(
	gwMetrics *gatewaymetrics.Metrics,
	mcpSrv *mcp.Server,
	auditAppender policy.AuditAppender,
	slotHealth *slothealth.Tracker,
	mtlsDeny *mtlsdenylist.DenyList,
	connectorAuthorizer *connectorauthz.Authorizer,
	connectorInvoker *connectorinvoke.Invoker,
	leaseBudgets *leasecontrol.MemoryBudgetSource,
	sessionSrv *sessionserver.Server,
) {
	f := w.f
	grpcAddr := f.grpcAddr
	claimHoldTTLSeconds := f.claimHoldTTLSeconds
	agentNamespace := f.agentNamespace
	leaseAutoMaxPerMin := f.leaseAutoMaxPerMin
	adapterTLSCert := f.adapterTLSCert
	adapterTLSKey := f.adapterTLSKey
	adapterCA := f.adapterCA
	spiffeTrustDomain := f.spiffeTrustDomain
	saTokenAudience := f.saTokenAudience
	maxCreatedStateTimeoutSeconds := f.maxCreatedStateTimeoutSeconds
	maxFinalizingTimeoutSeconds := f.maxFinalizingTimeoutSeconds
	maxReadyStateTimeoutSeconds := f.maxReadyStateTimeoutSeconds
	maxStartingStateTimeoutSeconds := f.maxStartingStateTimeoutSeconds
	maxSessionAgeSeconds := f.maxSessionAgeSeconds
	maxAwaitingClientActionSeconds := f.maxAwaitingClientActionSeconds
	maxSuspendedPodHoldSeconds := f.maxSuspendedPodHoldSeconds
	maxResumePendingSeconds := f.maxResumePendingSeconds
	maxResumingSeconds := f.maxResumingSeconds
	retryMaxRetries := f.retryMaxRetries
	maxIdleTimeSeconds := f.maxIdleTimeSeconds
	sessionExpiryWarningSeconds := f.sessionExpiryWarningSeconds

	// ----- §8.6 GatewayControl gRPC server -----
	// With --grpc-addr the gateway serves the adapter→gateway control
	// surface — the inverse direction of the pod-facing Adapter service.
	// It currently hosts the §8.6 ExtendLease RPC: a pod's adapter calls
	// it when its LLM proxy rejects a request for budget exhaustion, and
	// the gateway computes the lease-extension grant. gwMetrics satisfies
	// leasecontrol.MetricEmitter so every grant drives the §16
	// lenny_delegation_lease_extension_total counter. F-8.6.13.
	// spec: §8.6 line 743 / §11.7 — the leasecontrol auditor adapts the
	// gateway audit appender so every ExtendLease decision (granted,
	// capped, denied) lands as a `delegation.lease_extended` row on the
	// hash-chained §11.7 audit log. The recorder pulls the requesting
	// session's tenant and the live actor sub from the §10.6
	// principal-bound context to satisfy §11.7 line 428 actor-tenant
	// validation. F-8.6.10.
	leaseExtensionAuditor := leaseExtensionAuditAdapter{appender: auditAppender}
	// §8.6 line 718 — the production Elicitor presents the generic budget
	// elicitation on the requesting session's client stream over the §9.2
	// interaction store and blocks for the user's decision. Built only when
	// the GatewayControl listener is enabled. F-8.6.2.
	var leaseElicit leasecontrol.Elicitor
	if *grpcAddr != "" {
		leaseElicit = &leaseElicitor{
			sessions:     w.sessions,
			interactions: w.interactions,
			publish:      func(s, et, d string, n time.Time) { w.eventBus.Publish(s, et, d, n) },
			clock:        clockinject.Now,
			idgen:        func() string { var b [16]byte; _, _ = rand.Read(b[:]); return fmt.Sprintf("lease-elicit-%x", b[:]) },
		}
	}
	// §9.1 lines 14-31 — the bridge forwards a type:agent runtime's
	// intra-pod platform tool calls (over GatewayControl) to the same
	// platform tool surface the gateway-edge /mcp endpoint serves,
	// scoped to the calling session. F-9.1.1.
	platformToolBridge := platformtools.New(mcpSrv, w.sessions)
	// §9.3 lines 142-164 — the connector bridge resolves a session's
	// policy-permitted connectors and forwards a type:agent runtime's
	// intra-pod per-connector tool calls (over GatewayControl) to the
	// connectorinvoke surface, which dials the external endpoint with the
	// gateway-held credential. F-9.1.2.
	connectorToolBridge := connectortools.New(w.sessions, w.connectors, connectorAuthorizer, connectorInvoker)
	// §8.6 line 643 — bridge a GRANTED token-budget extension onto the §8.2
	// per-tree delegation budget counter so the next delegate_task admission
	// is gated against the raised pool (F-8.6.3). Only the concrete
	// *treebudget.Reserver satisfies the granter; pass a nil interface (not a
	// typed-nil pointer) on the Postgres-only/no-Redis path so the Service's
	// nil-granter branch is taken.
	var leaseTreeGranter leasecontrol.TreeBudgetGranter
	if w.treeBudgetConcrete != nil {
		leaseTreeGranter = w.treeBudgetConcrete
	}
	// §4.7 — the adapter's per-slot and whole-pod scrub reports reach the
	// gateway recycle-counter writes, the unhealthy-threshold drain ledger,
	// and the §3.4 / §6.39 recycle disposition through the ScrubReporter. It
	// needs the cluster client (Pods get, SandboxClaim status patch) and the
	// Postgres agent_pod_state mirror (the recycle counters), so it is wired
	// only when both --agent-namespace (clusterClient) and the Postgres pool
	// are configured; otherwise both scrub-report RPCs return Unimplemented
	// (the §8.6-only GatewayControl deployment). F-4.7.
	var scrubReports leasecontrol.ScrubReportService
	holdTTL := time.Duration(*claimHoldTTLSeconds) * time.Second
	if w.scrubReportServiceWired() {
		// The §3.2 reserved-hold coordinator and §3.4 recycle-boundary
		// coordinator are stopped on shutdown so the in-process timers and
		// re-warm polls do not run against a draining client; those Stops are
		// deferred by the composition root (runGateway) so they fire at
		// process shutdown rather than when this build step returns.
		var err error
		scrubReports, err = newScrubReportService(w.clusterClient, agentpodstatepg.New(w.pgPool), w.pools, w.runtimes, gwMetrics, slotHealth, *agentNamespace, holdTTL, w.holdCoordinator, w.recycleBoundary, clockinject.Now)
		if err != nil {
			log.Fatalf("lenny-gateway: §4.7 scrub-report service: %v", err)
		}
	}
	gatewayCtrlSrv, gatewayCtrlLis, err := newGatewayControlServer(*grpcAddr, leaseBudgets, gwMetrics, leaseExtensionAuditor, leaseElicit, w.rateLimiter, *leaseAutoMaxPerMin, platformToolBridge, connectorToolBridge, leaseTreeGranter, scrubReports, w.replica, *adapterTLSCert, *adapterTLSKey, *adapterCA, *spiffeTrustDomain, *saTokenAudience, w.saTokenVerifier, mtlsDeny)
	if err != nil {
		log.Fatalf("lenny-gateway: §8.6 GatewayControl listen: %v", err)
	}

	// ----- §6.2 / §11.3 pre-running watchdog -----
	// Sweeps every 5 s; transitions stuck sessions to failed.
	// Tenants list is sourced from the in-memory store so newly
	// registered tenants are picked up on the next tick.
	// §5.2 line 519 / §6.2: a session forced terminal by background sweep
	// must run the full gateway-side terminal pipeline — workspace seal,
	// executor release (concurrent-mode slot release + pod drain), audit,
	// SSE, billing, archive — so the watchdog-driven path emits the same
	// signals exactly once as the REST-driven terminal path. Closes
	// F-5.2.26.
	// F-7.4.7: thread the configured maxCreatedStateTimeoutSeconds into
	// the watchdog so its `created`-state budget matches the
	// uploadToken TTL and the createdsweeper deadline.
	// spec: §7.1 line 58.
	// F-6.2.14: thread the §6.2 line 249 resuming watchdog and §6.2 line
	// 292 resume_pending wall-clock cap into the gateway watchdog. The
	// resume_pending cap defaults to maxResumeWindowSeconds (which the
	// per-session retryPolicy.maxResumeWindowSeconds tightens); resuming
	// is the §6.2 fixed 300s budget. MaxRetries falls through to the
	// same deployer flag the §4.8 RetryPolicyEvaluator uses so the
	// resuming → resume_pending retry counts against the same budget.
	// spec: §11.3 line 219-221 — every operator-tunable watchdog budget
	// flows through `Config`. The flag surface above defaults each to the
	// §11.3 spec value, so `Config{}` is now constructed with the
	// effective value (after env/flag resolution) rather than the
	// zero-value the watchdog used to backfill silently. F-11.3.11.
	wd := watchdog.New(w.sessions, tenantsLister{w.tenants}, watchdog.Config{
		MaxCreatedSeconds:              *maxCreatedStateTimeoutSeconds,
		MaxFinalizingSeconds:           *maxFinalizingTimeoutSeconds,
		MaxReadySeconds:                *maxReadyStateTimeoutSeconds,
		MaxStartingSeconds:             *maxStartingStateTimeoutSeconds,
		MaxSessionAgeSeconds:           *maxSessionAgeSeconds,
		MaxAwaitingClientActionSeconds: *maxAwaitingClientActionSeconds,
		MaxSuspendedPodHoldSeconds:     *maxSuspendedPodHoldSeconds,
		MaxResumePendingSeconds:        *maxResumePendingSeconds,
		MaxResumingSeconds:             *maxResumingSeconds,
		MaxRetries:                     *retryMaxRetries,
		MaxIdleSeconds:                 *maxIdleTimeSeconds,
		ExpiryWarningSeconds:           *sessionExpiryWarningSeconds,
	}, nil).
		WithBilling(w.billing).
		WithTreeArchive(w.treeArchive).
		WithTerminalHook(sessionSrv).
		// spec: §11.3 line 198 — the maxSessionAge sweep honours a
		// deployer's per-runtime `limits.maxSessionAgeSeconds` / per-pool
		// `maxSessionAgeSeconds` (most-restrictive-wins below the platform
		// default) instead of expiring every session at the single baked
		// default. F-11.3.3.
		WithSessionAgeResolver(sessionage.New(w.runtimes, w.pools)).
		// spec: §11.3 line 199 / §6.2 (maxClientIdleSeconds clock) — the
		// idle sweep expires a clock-running session idle longer than its
		// effective `maxClientIdleSeconds`, honouring the per-runtime /
		// per-pool `sessionPolicy.maxClientIdleSeconds` (default: the pool's
		// effective `maxSessionAgeSeconds`) and the §27.6 playground idle
		// override. The clock runs in `running`, `input_required`, and
		// `awaiting_client_action`; its pause table lives in the watchdog
		// sweep, so no separate pause predicate is wired. F-11.3.7.
		WithIdleResolver(sessionidle.NewResolver(w.runtimes, w.pools))
	// spec: §7.2 lines 294, 341 — wire the DLQ TTL sweeper only when the
	// messaging coordinator exists (Redis present). Passing a nil
	// *Coordinator would create a typed-nil interface that defeats the
	// watchdog's nil guard and force wasted List calls every tick.
	if w.messagingCoord != nil {
		wd = wd.WithMessaging(w.messagingCoord)
	}
	watchdogCtx, watchdogCancel := context.WithCancel(context.Background())

	w.gatewayCtrlSrv = gatewayCtrlSrv
	w.gatewayCtrlLis = gatewayCtrlLis
	w.wd = wd
	w.watchdogCtx = watchdogCtx
	w.watchdogCancel = watchdogCancel
}

// scrubReportServiceWired reports whether the §4.7 scrub-report service is
// wired, which is the predicate the original inline control-server block
// used to enter the scrub-report branch and to register the §3.2 / §3.4
// coordinator Stop defers. The composition root re-evaluates it to defer
// those Stops exactly when the original branch did.
//
// spec: §4.7 scrub-report service wiring predicate.
func (w *gatewayWiring) scrubReportServiceWired() bool {
	f := w.f
	return *f.grpcAddr != "" && w.clusterClient != nil && w.pgPool != nil && *f.agentNamespace != ""
}
