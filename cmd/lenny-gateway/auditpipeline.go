// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	"github.com/lennylabs/lenny/pkg/audit/siem"
	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/auditretention"
	"github.com/lennylabs/lenny/pkg/gateway/auditscope"
	"github.com/lennylabs/lenny/pkg/gateway/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/auditstore/auditbatch"
	"github.com/lennylabs/lenny/pkg/gateway/health"
	"github.com/lennylabs/lenny/pkg/gateway/health/backends"
	"github.com/lennylabs/lenny/pkg/gateway/jwtaudit"
	"github.com/lennylabs/lenny/pkg/gateway/policy"
	"github.com/lennylabs/lenny/pkg/gateway/storage/eventbus"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// buildAuditPipeline is the §4.1 composition-root build step (R1) for the
// §11.7 audit pipeline. It constructs the per-tenant audit hash chain (durable
// auditstore over Postgres, or an in-memory ChainSet for the minimal gateway),
// the §11.7 write-scope validator, the admin and policy-rejection audit sinks,
// the admin-router audit-wiring closure, the §16.4 retention pruner, and the
// §12.6 EventBus retranscribe worker; wires the §10.3 JWT-signing-key rotation
// observer onto the chain; and constructs the §11.7 OCSF translator, the §12.3
// SIEM outbox forwarder, and the §11.7 SIEM health checker. The durable Store
// is hoisted so the §25.5 ops-stream escalation emitter (built once Redis is
// resolved) can attach via SetOpsStreamEmitter. The step records the audit
// sink, the audit-wiring closure, the appender, the validator, the batch
// buffer, the pruner, the retranscriber, the durable Store, the OCSF
// translator and SIEM outbox, and the SIEM health checker on the accumulator
// so the session server, the MCP fabric, the admin router, and the HTTP
// surface read them back.
//
// spec: §4.1 gateway subsystem seams; §11.7 audit chain / OCSF / SIEM; §12.3
// SIEM outbox; §12.6 EventBus retranscribe.
func (w *gatewayWiring) buildAuditPipeline() {
	f := w.f
	auditLockAcquireTimeoutMs := f.auditLockAcquireTimeoutMs
	auditLockMaxRetries := f.auditLockMaxRetries
	auditLockRetryBaseMs := f.auditLockRetryBaseMs
	scatterMaxConcurrency := f.scatterMaxConcurrency
	scatterPerShardTimeoutSeconds := f.scatterPerShardTimeoutSeconds
	scatterAggregateTimeoutSeconds := f.scatterAggregateTimeoutSeconds
	auditBatchingEnabled := f.auditBatchingEnabled
	auditFlushIntervalMs := f.auditFlushIntervalMs
	auditFlushBatchSize := f.auditFlushBatchSize
	auditScatterCacheEnabled := f.auditScatterCacheEnabled
	gdprRetentionDays := f.gdprRetentionDays
	auditSIEMEndpoint := f.auditSIEMEndpoint
	auditRetentionPruneIntervalSeconds := f.auditRetentionPruneIntervalSeconds
	eventBusDuplicateInjectionFactor := f.eventBusDuplicateInjectionFactor
	eventBusRetryIntervalSeconds := f.eventBusRetryIntervalSeconds
	eventBusMaxRetryAttempts := f.eventBusMaxRetryAttempts
	auditOCSFRetryIntervalSeconds := f.auditOCSFRetryIntervalSeconds
	auditOCSFMaxAttempts := f.auditOCSFMaxAttempts
	auditOCSFBatchSize := f.auditOCSFBatchSize
	auditSIEMMaxDeliveryLagSeconds := f.auditSIEMMaxDeliveryLagSeconds
	auditSIEMSecret := f.auditSIEMSecret
	auditSIEMFailureThresholdPercent := f.auditSIEMFailureThresholdPercent
	auditSIEMPollIntervalSeconds := f.auditSIEMPollIntervalSeconds

	gwMetrics := w.gwMetrics

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

	w.auditSink = auditSink
	w.wireAudit = wireAudit
	w.auditAppender = auditAppender
	w.auditValidator = auditValidator
	w.auditBatchBuffer = auditBatchBuffer
	w.auditPruner = auditPruner
	w.eventBusRetranscriber = eventBusRetranscriber
	w.auditOpsStore = auditOpsStore
	w.ocsfTranslator = ocsfTranslator
	w.ocsfOutbox = ocsfOutbox
	w.siemHealthChecker = siemHealthChecker
}
