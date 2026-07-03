// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"google.golang.org/grpc"

	"github.com/lennylabs/lenny/pkg/alerting/alertingmetrics"
	"github.com/lennylabs/lenny/pkg/alerting/evaluator"
	"github.com/lennylabs/lenny/pkg/alerting/inproceval"
	"github.com/lennylabs/lenny/pkg/alerting/rules"
	"github.com/lennylabs/lenny/pkg/audit/integrity"
	cbmw "github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker"
	"github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker/breakerstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

func (w *gatewayWiring) runServers() {
	f := w.f
	addr := f.addr
	devMode := f.devMode
	multiTenant := f.multiTenant
	auditStartupChainCheckEntries := f.auditStartupChainCheckEntries
	auditSIEMEndpoint := f.auditSIEMEndpoint
	auditSIEMMaxDeliveryLagSeconds := f.auditSIEMMaxDeliveryLagSeconds
	auditSIEMPollIntervalSeconds := f.auditSIEMPollIntervalSeconds
	auditOCSFRetryIntervalSeconds := f.auditOCSFRetryIntervalSeconds
	auditOCSFMaxAttempts := f.auditOCSFMaxAttempts
	auditOCSFBatchSize := f.auditOCSFBatchSize
	auditFlushIntervalMs := f.auditFlushIntervalMs
	auditFlushBatchSize := f.auditFlushBatchSize
	shutdownTimeout := f.shutdownTimeout
	healthTrackerUseCompiledRules := f.healthTrackerUseCompiledRules
	alertingBundleFormats := f.alertingBundleFormats
	alertingOverrideCount := f.alertingOverrideCount
	auditHardFailOnDrift := f.auditHardFailOnDrift

	// §16.1 line 713 / §25.13 lines 4833–4835 — register the bundled-
	// alerting observability surface so an operator's chart inputs and
	// per-rule in-process eval latency become visible on /metrics.
	// F-25.13.3.
	alertingMx, err := alertingmetrics.New(nil)
	if err != nil {
		log.Fatalf("lenny-gateway: §25.13 alerting metrics: %v", err)
	}
	{
		var formats []string
		for _, f := range strings.Split(*alertingBundleFormats, ",") {
			if t := strings.TrimSpace(f); t != "" {
				formats = append(formats, t)
			}
		}
		alertingMx.SetBundledFormats(formats...)
	}
	if *alertingOverrideCount < 0 {
		log.Fatalf("lenny-gateway: --alerting-override-count must be >= 0 (got %d) (§25.13 line 4834)", *alertingOverrideCount)
	}
	alertingMx.SetOverrideCount(*alertingOverrideCount)

	// §4.0 / §25.13: the per-replica in-process alert tracker drives the
	// §16.5 catalog through inactive → pending → firing and emits
	// alert_fired / alert_resolved through the shared EventEmitter. The
	// expression backend is inproceval, which evaluates the instant-vector
	// subset of the §16.5 catalog against this replica's own metric
	// registry (gwMetrics.Gatherer) — the per-replica fallback the spec
	// mandates when Prometheus is unreachable. Expressions needing a
	// time-series history (rate/increase/histogram_quantile) or label-set
	// joins resolve to ErrUnsupportedExpr, which the evaluator treats as
	// "preserve state", so those alerts stay with Prometheus and never
	// fire spuriously from the fallback.
	//
	// spec: §25.13 line 4676 — "The in-process alert state tracker
	// (Section 25.3, Health API) evaluates these expressions against the
	// in-process metric registry. This is the per-replica fallback used
	// when Prometheus is unreachable." F-25.13.6.
	//
	// spec: §25.13 line 4798 — operators can suppress the in-process
	// tracker entirely via `gateway.healthTracker.useCompiledRules:
	// false`. In that posture the per-replica health view falls back to
	// dependency probes and circuit breaker state only. F-25.13.4.
	if *healthTrackerUseCompiledRules {
		alertEvaluator := evaluator.NewWithEmitter(
			rules.Catalog(),
			inproceval.New(w.gwMetrics.Gatherer()),
			evaluator.EventEmitOptions{
				Emitter:            w.opsEmitter,
				Source:             "//lenny.dev/gateway/" + w.replica,
				OnRuleEvalDuration: alertingMx.ObserveRuleEvalDuration,
			},
		)
		// spec: §25.17 line 5254 — expose the firing-alert set to the
		// pool health resolver so GET /v1/admin/health/{pool} can report
		// whether a warm-pool alert has resolved.
		w.alertEvalPtr.Store(alertEvaluator)
		go alertEvaluator.Run(w.watchdogCtx)
	} else {
		log.Printf("lenny-gateway: §25.13 in-process alert tracker disabled (gateway.healthTracker.useCompiledRules=false); /v1/admin/health falls back to dependency probes + circuit breaker state only")
	}

	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGTERM, syscall.SIGINT)

	// spec: §11.7 item 2 lines 356-359 — periodic background integrity
	// check. After the signal handler is installed, run the grant /
	// trigger / erasure-guard re-verification and recent-chain sample on
	// the resolved cadence. A detected drift logs a critical line and
	// increments lenny_audit_grant_drift_total; with audit.hardFailOnDrift
	// it self-signals SIGTERM so the existing graceful-shutdown path
	// drains in-flight work. Postgres-only: the in-memory chain has no
	// grant surface to drift. F-11.7.3.
	if w.pgPool != nil {
		periodic := &integrity.PeriodicCheck{
			DB: w.pgPool,
			Cfg: integrity.PeriodicConfig{
				Interval:        w.resolvedGrantCheckInterval,
				HardFailOnDrift: *auditHardFailOnDrift,
				ChainSampleN:    *auditStartupChainCheckEntries,
			},
			OnGrantDrift:              w.gwMetrics.IncAuditGrantDrift,
			OnChainState:              w.gwMetrics.IncAuditChainIntegrity,
			OnRedactionReceiptMissing: w.gwMetrics.AddAuditRedactionReceiptMissing,
			Logf:                      func(format string, args ...any) { log.Printf("lenny-gateway: "+format, args...) },
			Shutdown: func(string) {
				if proc, perr := os.FindProcess(os.Getpid()); perr == nil {
					_ = proc.Signal(syscall.SIGTERM)
				}
			},
		}
		log.Printf("lenny-gateway: §11.7 item 2 periodic audit integrity check active (interval=%s regulated=%v hardFailOnDrift=%v)",
			w.resolvedGrantCheckInterval, w.grantCheckRegulated, *auditHardFailOnDrift)
		go periodic.Run(w.watchdogCtx)
	}

	// spec: §11.7 Wire Format — start the OCSF translator's background
	// retry loop. It drains pending audit rows into OCSF records and
	// multicasts them to the SIEM forwarder (when configured), advancing
	// each row's ocsf_translation_state on the resolved cadence.
	// F-11.7.1 / F-11.7.11.
	if w.ocsfTranslator != nil {
		log.Printf("lenny-gateway: §11.7 OCSF translator active (siem=%v retryInterval=%ds maxAttempts=%d batchSize=%d)",
			*auditSIEMEndpoint != "", *auditOCSFRetryIntervalSeconds, *auditOCSFMaxAttempts, *auditOCSFBatchSize)
		go w.ocsfTranslator.Run(w.watchdogCtx)
	}

	// spec: §12.3 line 97 — start the SIEM outbox forwarder's background
	// loop. It tails committed audit_log rows, delivers each to the SIEM
	// after Postgres commits it, checkpoints the per-tenant delivery
	// high-water mark in siem_delivery_state, and emits
	// lenny_audit_siem_delivery_lag_seconds. F-12.3.6 / F-12.3.17.
	if w.ocsfOutbox != nil {
		log.Printf("lenny-gateway: §12.3 SIEM outbox forwarder active (pollInterval=%ds maxDeliveryLag=%ds)",
			*auditSIEMPollIntervalSeconds, *auditSIEMMaxDeliveryLagSeconds)
		go w.ocsfOutbox.Run(w.watchdogCtx)
	}

	// spec: §12.3 line 81 — drive the opt-in T2 audit batch buffer's
	// flush loop. It flushes buffered non-PII T2 audit events every
	// flushIntervalMs or when the buffer reaches flushBatchSize, and
	// flushes the remainder on shutdown. F-12.3.14.
	if w.auditBatchBuffer != nil {
		log.Printf("lenny-gateway: §12.3 T2 audit batching enabled (flushInterval=%dms flushBatchSize=%d)",
			*auditFlushIntervalMs, *auditFlushBatchSize)
		go w.auditBatchBuffer.Run(w.watchdogCtx)
	}

	go func() {
		log.Printf("lenny-gateway: listening on %s (dev_mode=%v multi_tenant=%v)",
			*addr, *devMode, *multiTenant)
		if err := w.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("lenny-gateway: listen: %v", err)
		}
	}()
	if w.llmProxySrv != nil {
		go func() {
			log.Printf("lenny-gateway: §4.9 LLM proxy listening on %s", w.llmProxySrv.Addr)
			if err := w.llmProxySrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("lenny-gateway: llm proxy listen: %v", err)
			}
		}()
	}
	if w.gatewayCtrlSrv != nil {
		go func() {
			log.Printf("lenny-gateway: §8.6 GatewayControl gRPC listening on %s", w.gatewayCtrlLis.Addr())
			if err := w.gatewayCtrlSrv.Serve(w.gatewayCtrlLis); err != nil && err != grpc.ErrServerStopped {
				log.Fatalf("lenny-gateway: GatewayControl listen: %v", err)
			}
		}()
	}

	<-stopCh
	log.Printf("lenny-gateway: shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), *shutdownTimeout)
	defer cancel()
	// Flush buffered spans before the process exits (§16.3 batch processor).
	_ = w.traceShutdown(ctx)
	_ = w.httpSrv.Shutdown(ctx)
	if w.llmProxySrv != nil {
		_ = w.llmProxySrv.Shutdown(ctx)
	}
	if w.gatewayCtrlSrv != nil {
		w.gatewayCtrlSrv.GracefulStop()
	}
	if w.pgPool != nil {
		w.pgPool.Close()
	}
	// spec: §12.3, §15.1 — release the CREATE-privileged DDL pools opened for
	// per-tenant sequence provisioning. primaryDDLPool aliases billingAuditDDLPool
	// in the single-instance topology, so closeDDLPools closes each distinct
	// pool once. F-11.2.10.
	closeDDLPools(w.billingAuditDDLPool, w.primaryDDLPool)
	// spec: §17.4 line 199 — stop the Source-Mode SQLite flush loop and
	// snapshot the session/metadata stores one final time so a clean
	// shutdown (the documented Ctrl-C flow) loses no writes, then close
	// the database. F-17.4.2.
	if w.sqliteDB != nil {
		if w.sqliteFlushCancel != nil {
			w.sqliteFlushCancel()
		}
		if err := w.sqliteDB.Close(ctx); err != nil {
			log.Printf("lenny-gateway: §17.4 sqlite close: %v", err)
		}
	}
	if w.redisClient != nil {
		_ = w.redisClient.Close()
	}
	// §12.4: close the per-concern split clients (no-op when no split is
	// configured; the base client closed above is left untouched).
	_ = w.concernRedis.Close()
	// §10.7: release the vendor OpenFeature SDK background connections.
	w.experimentProviders.Close()
}

// breakerRegistry is the breaker-store surface the gateway wires: the
// breakerstore.Store admin operations plus the cbmw.Registry snapshot
// the circuit-breaker middleware reads. Both the in-memory and the
// Redis-backed breaker stores satisfy it.
type breakerRegistry interface {
	breakerstore.Store
	cbmw.Registry
}

// newLLMProxyServer builds the §4.9 LLM reverse-proxy HTTP server,
// serving the Anthropic Messages endpoint at POST /llm-proxy/v1/messages.
// It returns nil when addr is empty, which disables the proxy listener.
// The credential-lease store and the credential cache start empty; the
// §4.9 credential-assignment path populates them, and a request that
// arrives before then is cleanly rejected. creds is the §4.9
// upstream-credential cache the binder's credential-assignment path
// populates, so the proxy resolves a lease's upstream credential from
// the same instance the assignment wrote it to. denyList is the
// per-replica credential deny list, owned by a propagator the caller
// drives so revocations converge across replicas.
// sessionUserLookup adapts the session store to
// proxycache.SessionUserLookup so the §4.9 per-user semantic-cache scope
// resolves a session's owning user. A store miss, or a session with no
// recorded user, leaves the request uncached rather than keyed without a
// user id.
type sessionUserLookup struct{ sessions sessionstore.Store }
