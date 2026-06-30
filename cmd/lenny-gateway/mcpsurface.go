// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/revocation"
	"github.com/lennylabs/lenny/pkg/gateway/delegationtree/deadlock"
	"github.com/lennylabs/lenny/pkg/gateway/delegationtree/resultrollup"
	"github.com/lennylabs/lenny/pkg/gateway/environment/deploymentconfigstore"
	deploymentconfigpg "github.com/lennylabs/lenny/pkg/gateway/environment/deploymentconfigstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation/childtoken"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation/export"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation/exportwire"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
	delegationpolicypg "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/elicitationfloor"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor/interceptorstore"
	interceptorpg "github.com/lennylabs/lenny/pkg/gateway/policy/interceptor/interceptorstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/provisioning/vcscred"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionidle"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// buildMCPSurface is the §4.1 composition-root build step (R1) for the §9.1
// MCP fabric. It constructs the §8.3 delegation-policy, §4.8/SEC-013
// external-interceptor, and §16.7 deployment-config stores, the §8.2
// delegation service (with its §8.7 export materializer, §8.3 export-scan
// resolver, and §8.2 child-token minter), the §9.1 MCP server, registers
// every MCP tool family on it (lenny/create_session, the §15.2 client-facing
// lifecycle tools, §8.2 delegate_task, §7.2 send_message, §8.8 await_children,
// §26.2 vcs_token, and the rest), and wires the §15.2 Streamable HTTP SSE
// attach channel. It records the delegation service, the MCP server, and the
// three admin-mutable stores on the accumulator so the admin router, the HTTP
// surface, and the control server read them back.
//
// spec: §4.1 gateway subsystem seams; §9.1 MCP fabric; §8.2 / §8.3 delegation.
func (w *gatewayWiring) buildMCPSurface(
	gwMetrics *gatewaymetrics.Metrics,
	sessionSrv *sessionserver.Server,
	policyChain *interceptor.Chain,
	auditSink admin.AuditSink,
	auditAppender policy.AuditAppender,
	policyAuditSink *policy.AuditSink,
	childLeaseRegistrar delegation.LeaseChildRegistrar,
	maxInputResolver *maxInputSizeResolverHolder,
	environments environmentstore.Store,
	resolvedNoEnvPolicy string,
	inputWaits *inputwait.Registry,
	activityStamper *sessionidle.Stamper,
	taskUsageBuilder *resultrollup.Builder,
	vcsCreds vcscred.Resolver,
	elicitationFloorProvider *elicitationfloor.Provider,
) {
	f := w.f
	delegationMaxActiveChildrenPerUser := f.delegationMaxActiveChildrenPerUser
	gatewayAllowSelfRecursion := f.gatewayAllowSelfRecursion
	delegationDefaultMaxDepth := f.delegationDefaultMaxDepth
	interceptorWeakeningCooldownSeconds := f.interceptorWeakeningCooldownSeconds
	devMode := f.devMode
	maxDeadlockWaitSeconds := f.maxDeadlockWaitSeconds
	messagingDefaultScope := f.messagingDefaultScope
	messagingMaxScope := f.messagingMaxScope
	messagingMaxPerMinute := f.messagingMaxPerMinute
	messagingMaxPerSession := f.messagingMaxPerSession
	messagingMaxInboundPerMinute := f.messagingMaxInboundPerMinute

	// ----- MCP adapter -----
	// spec: §8.3 — the DelegationPolicy registry feeds both the
	// admin CRUD surface (below) and the delegation admission gate so
	// the §8.2 LayerPolicy AllowSelfRecursion input and the §8.2.bis
	// policy ceiling can both consult the same store. Construct it
	// here so delegationSvc can read it; the admin router below shares
	// the same handle.
	var delegationPolicies delegationpolicystore.Store = delegationpolicystore.NewMemory()
	if w.pgPool != nil {
		delegationPolicies = delegationpolicypg.New(w.pgPool)
	}
	// §4.8 / §8.3 SEC-013 external-interceptor registry: the admin-mutable
	// source of truth for the fail-policy weakening cooldown the
	// delegation service enforces at delegate_task / send_message time.
	// F-4.8.17.
	var interceptors interceptorstore.Store = interceptorstore.NewMemory()
	if w.pgPool != nil {
		interceptors = interceptorpg.New(w.pgPool)
	}
	interceptorCooldownResolver := interceptorstore.NewCooldownResolver(interceptors)
	// §16.7 deployment-transition audit baseline: the durable last-applied
	// Helm deployment-scope config the post-upgrade reconciliation endpoint
	// diffs each render against to emit the gateway.*/platform.*/deployment.*
	// transition audit events. Postgres-backed so the baseline survives a
	// gateway restart (the in-memory fallback re-emits a first-install event
	// set after a restart, acceptable for the no-Postgres minimal gateway).
	// F-8.2.5, F-9.2.10, F-17.2.8.
	var deploymentConfig deploymentconfigstore.Store = deploymentconfigstore.NewMemory()
	if w.pgPool != nil {
		deploymentConfig = deploymentconfigpg.New(w.pgPool)
	}
	// §8.7 / §8.2 steps 3, 4: the file-export materializer pulls declared
	// fileExport sets from the running parent pod (over the pod-session
	// registry's adapter client), validates + persists them to the §4.5
	// blob store, and stamps the §14 child WorkspacePlan. It is wired only
	// when the pod registry exists; a delegation that declares fileExport
	// without it fails closed with EXPORT_NOT_CONFIGURED. F-8.7.1.
	var exportMaterializer delegation.ExportMaterializer
	if w.podRegistry != nil {
		exportMaterializer = export.NewMaterializer(
			exportwire.NewPodExporter(w.podRegistry),
			exportwire.NewBlobSink(w.blobs, 0),
			mcpDelegationAuditor{sink: auditSink, billing: w.billingEmitter},
		)
	}
	// §8.3 lines 160-181 / §13.5 mitigation 4: the per-file export-scan
	// resolver routes each exported file through the DelegationPolicy's
	// contentPolicy.interceptorRef at PreExportMaterialization. The
	// resolver looks the ref up among the PreDelegation interceptors
	// registered on policyChain (§4.8 line 1038: the same named interceptor
	// in force on the parent's policy) and stamps an observer that emits
	// the §11.7 delegation.export_file_scan_rejected / export_scan_failed_open
	// audit events and the §16.1 lenny_export_file_scans_total /
	// _duration_seconds metrics. A scanExportedFiles: true policy whose
	// interceptorRef names no registered interceptor still fails closed
	// with EXPORT_FILE_SCAN_UNAVAILABLE. F-13.5.5.
	exportScanResolver := delegation.NewChainExportScanResolver(
		policyChain,
		policy.NewExportScanObserver(auditAppender, gwMetrics, nil).WithBilling(w.billingEmitter),
	)
	// §13.3 revocation cache: the auth middleware rejects a token whose
	// jti is in this set, and the §8.2 line 61 child-token exchange reads
	// the parent (actor) token's jti against it inside the minting step.
	// Constructed here so both the delegation child-token minter and the
	// revocation propagator (below) share the one cache instance. F-8.1.2.
	revCache := revocation.NewCache()
	// §8.2 line 59 / §13.3: the in-process child-token minter the
	// delegation service runs after admission. It narrows scope, builds
	// the act chain, fixes delegation_depth at parent + 1, caps exp, and
	// fails closed with DELEGATION_PARENT_REVOKED when the parent jti is
	// revoked. F-8.1.2 / F-8.2.7.
	childTokenMinter := childtoken.NewMinter(childtoken.Options{
		Revocations: revCache,
		Clock:       clockinject.Now,
	})
	delegationSvc := delegation.NewService(w.sessions, delegation.Options{
		Experiments:             w.experiments,
		Runtimes:                w.runtimes,
		Policies:                delegationPolicies,
		Clock:                   clockinject.Now,
		ExportMaterializer:      exportMaterializer,
		ExportScanChainResolver: exportScanResolver,
		TreeBudgetReserver:      w.treeBudgetReserver,
		ChildTokenMinter:        childTokenMinter,
		// §8.6 line 648: register each admitted child with the lease-
		// extension budget source, capped at the parent's own lease, so a
		// later adapter ExtendLease from the child resolves its tree. F-15.3.5.
		LeaseRegistrar: childLeaseRegistrar,
		// §11.1 line 9 — per-user active-delegated-children admission cap.
		// Zero leaves the scope unlimited. F-11.1.4.
		MaxActiveChildrenPerUser: *delegationMaxActiveChildrenPerUser,
		// §8.2 LayerPlatform — Helm value gateway.allowSelfRecursion.
		PlatformAllowSelfRecursion: *gatewayAllowSelfRecursion,
		// §8.2.bis line 89 — Helm value gateway.delegation.defaultMaxDepth.
		DefaultMaxDepth: *delegationDefaultMaxDepth,
		// §8.3 line 181 — Helm value gateway.interceptorWeakeningCooldownSeconds.
		// F-8.7.12 / F-13.5.7.
		InterceptorWeakeningCooldown: time.Duration(*interceptorWeakeningCooldownSeconds) * time.Second,
		// §4.8 line 1034 / §8.3 SEC-013 — reject delegate_task whose
		// effective interceptorRef names an interceptor inside the
		// fail-closed → fail-open weakening cooldown. F-4.8.17.
		InterceptorCooldown: interceptorCooldownResolver,
		// §8.2 / §16.1: the delegation service emits
		// `lenny_delegation_depth` and
		// `lenny_delegation_would_have_blocked_total` through the
		// gateway metrics registry.
		Metrics: gwMetrics,
		// spec: §11.7 line 62 / §16.7 — wire the §11.7 audit sink so
		// the service emits `delegation.spawned`,
		// `delegation.self_recursion_allowed`, and `delegation.cycle_warning`.
		// F-8.5.8 / F-8.5.9.
		Auditor: mcpDelegationAuditor{sink: auditSink, billing: w.billingEmitter},
		// spec: §8.2 line 90 / §10.7 — `independent` propagation
		// routes the child afresh through the same ExperimentRouter
		// the top-level session-creation path uses. Wired as a
		// pointer to *sessionserver.Server, which implements
		// delegation.ExperimentRouter via ApplyExperimentRouting.
		ExperimentRouter: sessionSrv,
	})
	// §8.3 line 157 / §4.8 line 974: now that delegationSvc exists, fill
	// in the holder so the DelegationPolicyEvaluator measures TaskSpec.input
	// against the parent runtime's effective contentPolicy.maxInputSize
	// rather than the cluster default alone. F-13.5.1 / F-8.2.9.
	maxInputResolver.inner = delegationSvc
	mcpSrv := mcp.NewServer()
	// spec: §8.8 lines 981-997 — the subtree deadlock detector. The await
	// tracker records which session awaits which children (registered by
	// lenny/await_children for the duration of each poll); the manager
	// holds the active deadlocked subtrees so the await poll can surface a
	// deadlock_detected partial. The periodic sweep + DEADLOCK_TIMEOUT
	// application is wired under watchdogCtx below. F-8.8.6.
	deadlockTracker := deadlock.NewAwaitTracker()
	var deadlockManager *deadlock.Manager
	if *maxDeadlockWaitSeconds > 0 {
		deadlockManager = deadlock.NewManager(time.Duration(*maxDeadlockWaitSeconds)*time.Second, gwMetrics)
	}
	mcptools.Register(mcpSrv, mcptools.Deps{
		Store: w.sessions,
		// spec: §15.2.1 rule 1 line 1380 — route lenny/create_session
		// through the gateway's shared §15.1 session-creation service so
		// the MCP surface runs the same admission gates and returns the
		// same envelope the REST POST /v1/sessions handler does. F-15.2.4.
		SessionCreator: sessionSrv,
		// spec: §15.2 lines 1284-1306 — route the overlapping client-facing
		// lifecycle/read tools through the same shared service layer so the
		// MCP surface runs the identical REST routes and validation. F-15.2.3.
		SessionService:             sessionSrv,
		Executor:                   w.exec,
		DeadlockTracker:            deadlockTracker,
		Deadlocks:                  deadlockManager,
		DevMode:                    *devMode,
		Delegation:                 delegationSvc,
		Users:                      w.users,
		Runtimes:                   w.runtimes,
		CapabilityOverrides:        w.capOverrides,
		Environments:               environments,
		Tenants:                    w.tenants,
		Pools:                      w.pools,
		Audit:                      mcpDelegationAuditor{sink: auditSink, billing: w.billingEmitter},
		DefaultNoEnvironmentPolicy: resolvedNoEnvPolicy,
		Interceptors:               policyChain,
		// spec: §8.3 lines 157-188 / §4.8 lines 1036, 1040 / §13.5
		// mitigations 2-3 — lenny/delegate_task and lenny/send_message
		// resolve the effective contentPolicy.interceptorRef and run only
		// that named external scanner (and enforce the message-side
		// maxInputSize) rather than every registered external interceptor.
		// F-8.2.9 / F-13.5.2.
		ContentPolicies: delegationSvc,
		// §4.8 line 1034 / §8.3 SEC-013 — gate lenny/send_message on the
		// interceptor fail-policy weakening cooldown, mirroring the
		// delegate_task gate inside the delegation Service. F-4.8.17.
		CooldownChecker: delegationSvc,
		PolicyAudit:     policyAuditSink,
		Events:          w.eventBus,
		InputWaits:      inputWaits,
		// spec: §6.2 line 276 — keep a parent blocked in await_children
		// non-idle so the §11.3 watchdog does not reap it while it waits
		// on slow children. F-11.3.7.
		ActivityStamper:    activityStamper,
		TreeArchive:        w.treeArchive,
		TaskUsage:          taskUsageBuilder,
		Interactions:       w.interactions,
		Memory:             w.memories,
		ElicitationMetrics: gwMetrics,
		// spec: §16.1 lines 60–63; §16.5 line 458. F-9.2.14 — the
		// §9.2 dispatcher emits admit/terminal lifecycle samples that
		// drive the ElicitationBacklogHigh alert and the operator
		// roundtrip / timeout / suppressed dashboards.
		ElicitationLifecycleMetrics: gwMetrics,
		// spec: §16.1 line 64; §9.2 line 60 — the §9.2 chain walker
		// reports a content-tamper detection through this recorder, which
		// increments lenny_elicitation_content_tamper_detected_total
		// {origin_pod, tampering_pod, enforcement_mode}. Without it the
		// dispatcher's tamper branch is a no-op and the §16.5
		// ElicitationContentTamperDetected alert can never fire. F-9.2.4.
		ElicitationTamperMetrics: gwMetrics,
		// spec: §9.2 lines 58–64 — resolve the per-tenant effective
		// content-integrity enforcement mode (max of the platform floor
		// and the tenant stored mode) on the elicitation dispatch path so
		// an operator's enforce / detect-only / off setting takes effect:
		// off skips the integrity check, detect-only records a divergence
		// but forwards as received, enforce drops the divergent forward.
		// A lookup error fails safe to the enforce default. F-9.2.2.
		ElicitationModeResolver: func(ctx context.Context, tenantID string) elicitation.EnforcementMode {
			stored := ""
			if t, err := w.tenants.Get(ctx, tenantID); err == nil {
				stored = t.ElicitationContentIntegrity
			}
			return elicitation.ResolveEffectiveWithDefaults(elicitationFloorProvider.Floor(), stored)
		},
		// spec: §9.2 / §16.1 / §15.2 line 1335 — Deps.TenantID is the
		// fallback for transports without an authenticated principal
		// (tests, the dev-headers path). Every production handler
		// re-resolves the per-request tenant from the auth middleware's
		// principal via callerTenantID, so a multi-tenant deployment
		// scopes session creation, the elicitation budget, the §9.2
		// chain lookup, the §16.7 audit emission, and the §16.1 tamper
		// metric to the right tenant. F-9.2.13 / F-15.2.15.
		TenantID: "default",
		// spec: §7.2 lines 236-272; §8.3 lines 269-272 — deployment
		// messagingScope (default + ceiling) and the per-session
		// send_message rate limits. The same cross-replica rate counter
		// the §11.1 admission limits use backs the per-minute messaging
		// windows. F-7.2.6.
		MessagingDefaultScope: session.MessagingScope(*messagingDefaultScope),
		MessagingMaxScope:     session.MessagingScope(*messagingMaxScope),
		MessagingRateLimit: mcptools.MessagingRateLimit{
			MaxPerMinute:        *messagingMaxPerMinute,
			MaxPerSession:       *messagingMaxPerSession,
			MaxInboundPerMinute: *messagingMaxInboundPerMinute,
		},
		MessagingRateCounter: w.rateLimiter,
		// §7.2 paths 3/5/6/7 — the same inbox + DLQ coordinator the REST
		// handler uses, so lenny/send_message buffers a message to a
		// non-running target instead of forcing it onto the executor.
		// F-7.2.5.
		Messaging: w.messagingCoord,
		Clock:     clockinject.Now,
		// §8.9 line 1003 / §11.7 / §16.1 — same tree-walker cycle
		// observer the REST /tree handler uses, so the audit row +
		// counter fire regardless of which surface walked the tree.
		// F-8.9.10.
		TreeCycleObserver: mcpToolsTreeCycleObserver{emitter: treeCycleEmitter{metrics: gwMetrics}},
		// spec: §26.2 line 119; §4.9 — the in-pod git-credential helper
		// (git-credential-lenny) reaches lenny/vcs_token over the §9.1
		// platform MCP socket to mint a short-lived VCS token from the
		// session tenant's credential pool. Reuses the same
		// vcscred.Resolver the §14 gateway-side gitClone path uses. The
		// §4.9.2 credential.leased audit row binds each minted token to
		// the originating session id. F-26.2.5.
		VCSCreds:        vcsCreds,
		VCSLeaseAuditor: mcpVCSLeaseAuditor{appender: auditAppender, billing: w.billingEmitter},
	})

	// spec: §15.2 lines 1331-1333 — wire the Streamable HTTP SSE channel
	// into the MCP transport so an attach_session tools/call sent with
	// Accept: text/event-stream is upgraded to the per-session event
	// stream (sourced from the same §15.1 event bus the REST
	// GET /v1/sessions/{id}/events path tails). Tenant is the
	// authenticated principal's so the bus enforces the §7.2 binding; the
	// Authorize gate runs the §4.2 session-store Get before any SSE byte
	// is written so a missing or foreign session surfaces as a normal
	// JSON-RPC RESOURCE_NOT_FOUND rather than a half-open stream.
	// F-15.2.2, F-9.1.7.
	mcpSrv.SetAttach(mcp.AttachConfig{
		Events: w.eventBus,
		TenantFromRequest: func(r *http.Request) string {
			if p, ok := authmw.FromContext(r.Context()); ok && p.TenantID != "" {
				return p.TenantID
			}
			return ""
		},
		Authorize: func(ctx context.Context, tenantID, sessionID string) error {
			if tenantID == "" {
				return mcp.NewToolError("UNAUTHORIZED",
					"attach_session: tenant could not be resolved; auth chain must precede MCP dispatch", nil)
			}
			if _, err := w.sessions.Get(ctx, tenantID, sessionID); err != nil {
				if errors.Is(err, sessionstore.ErrNotFound) {
					return mcp.NewToolError("RESOURCE_NOT_FOUND", "session not found", nil)
				}
				return mcp.NewToolError("INTERNAL_ERROR", err.Error(), nil)
			}
			return nil
		},
		Now: clockinject.Now,
	})

	w.delegationPolicies = delegationPolicies
	w.interceptors = interceptors
	w.deploymentConfig = deploymentConfig
	w.delegationSvc = delegationSvc
	w.mcpSrv = mcpSrv
	w.revCache = revCache
	w.deadlockTracker = deadlockTracker
	w.deadlockManager = deadlockManager
}
