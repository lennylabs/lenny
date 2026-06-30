// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/experimentprovider"
	"github.com/lennylabs/lenny/pkg/gateway/experimentsticky"
	"github.com/lennylabs/lenny/pkg/gateway/extractionthreshold"
	"github.com/lennylabs/lenny/pkg/gateway/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/leasecontrol/denialpg"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionbudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioncallback"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionidle"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/slothealth"
	"github.com/lennylabs/lenny/pkg/gateway/subsystem"
	"github.com/lennylabs/lenny/pkg/kms/envelope"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// buildSessionDeps is the §4.1 composition-root build step (R1) that
// constructs the dependencies the §4.2 session server consumes, which the
// composition root previously assembled inline before the buildSessionServer
// call. It builds the §4.1 Upload Handler subsystem gate and its §16.1 metrics
// (threading both to the pod binder), the §8.5 request_input registry, the
// §10.7 sticky-assignment cache and OpenFeature provider cache, the §14
// completion-webhook validator/seal/dispatcher, the §11.2 mid-session budget
// enforcer and its terminator, the §8.6 lease-extension budget source with its
// session and child registrars, the §6.2 activity stamper, and the §5.2/§6.2
// slot-health tracker. It records each on the accumulator so buildSessionServer
// (and the MCP fabric, the admin router, and the background workers) read them
// back.
//
// spec: §4.1 gateway subsystem seams; §4.2 session manager; §14 completion
// webhook; §8.6 lease-extension budget.
func (w *gatewayWiring) buildSessionDeps() {
	f := w.f
	callbackURLAllowedDomains := f.callbackURLAllowedDomains
	grpcAddr := f.grpcAddr
	leaseDefaultBudget := f.leaseDefaultBudget
	leaseMaxBudget := f.leaseMaxBudget
	leaseDefaultApproval := f.leaseDefaultApproval
	leaseCoolOffSec := f.leaseCoolOffSec
	leaseRejectionCoolOffSec := f.leaseRejectionCoolOffSec
	leaseAutoMaxPerMin := f.leaseAutoMaxPerMin

	gwMetrics := w.gwMetrics

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

	w.uploadSubsystem = uploadSubsystem
	w.uploadMetrics = uploadMetrics
	w.inputWaits = inputWaits
	w.sessionStickyCache = sessionStickyCache
	w.adminStickyFlusher = adminStickyFlusher
	w.erasureSticky = erasureSticky
	w.experimentProviders = experimentProviders
	w.callbackValidator = callbackValidator
	w.callbackSeal = callbackSeal
	w.callbackDispatcher = callbackDispatcher
	w.budgetTerminator = budgetTerminator
	w.sessionBudgetEnforcer = sessionBudgetEnforcer
	w.leaseBudgets = leaseBudgets
	w.leaseExtDefaults = leaseExtDefaults
	w.sessionLeaseRegistrar = sessionLeaseRegistrar
	w.childLeaseRegistrar = childLeaseRegistrar
	w.activityStamper = activityStamper
	w.slotHealth = slotHealth
}
