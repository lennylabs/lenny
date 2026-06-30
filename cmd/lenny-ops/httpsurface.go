// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/lennylabs/lenny/pkg/alerting/evaluator"
	"github.com/lennylabs/lenny/pkg/alerting/rules"
	"github.com/lennylabs/lenny/pkg/audit/pgaudit"
	opsmetrics "github.com/lennylabs/lenny/pkg/ops/metrics"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// buildHTTPSurface constructs the §25.13 in-process alert evaluator, the
// §25.4 OIDC authentication gate, the §25.4 HTTP surface (opsserver.New),
// and the wrapping http.Server. spec: §25.4, §25.13, §25.16.
func (w *opsWiring) buildHTTPSurface() {
	// §4.0 / §25.13: the in-process alert tracker. lenny-ops has no
	// PromQL backend wired in this commit, so the evaluator runs with
	// NoopExprEvaluator and no rule fires — the §25.5 alert events come
	// from Prometheus's `/api/v1/alerts` aggregation rather than this
	// evaluator until a real backend lands. The wiring is unconditional
	// so a future commit that supplies a real ExprEvaluator only swaps
	// the backend.
	//
	// spec: §25.16 line 5124. When --prometheus-url is set, the operator
	// has supplied a BYO Prometheus HTTP API endpoint; the §25.13
	// ExprEvaluator and the §25.4 cross-replica health aggregator should
	// route through it (the HTTP client is built here so the future
	// backend swap is a single-line change). When empty, lenny-ops
	// degrades to the per-replica fan-out fallback the §25.16 Minimal
	// block accepts. F-25.16.4.
	// §25.4 lines 1914-1916 — register lenny_prometheus_query_duration_seconds
	// {kind} on the default registry so the §16.9 /metrics surface exposes it.
	// The histogram receives observations once a PromQL query path consumes a
	// PrometheusClient built with this adapter (below); until then the
	// pre-stamped kind series read 0. F-25.4.18.
	queryMetrics, err := opsmetrics.NewPromQueryMetrics(prometheus.DefaultRegisterer)
	if err != nil {
		log.Fatalf("lenny-ops: register §25.4 prometheus query-duration metric: %v", err)
	}
	if *w.f.prometheusURL != "" {
		log.Printf("lenny-ops: §25.16 BYO Prometheus configured at %s", *w.f.prometheusURL)
		// Build the §25.4 Prometheus client with the query-duration adapter
		// so each query the cross-replica health/recommendation aggregator
		// runs is timed. The aggregator consumer rides on the alert-evaluator
		// backend swap; the client + metric wiring is in place for it.
		if _, perr := opsmetrics.NewPrometheusClient(opsmetrics.PrometheusConfig{
			BaseURL: *w.f.prometheusURL,
			Metrics: queryMetrics,
		}); perr != nil {
			log.Printf("lenny-ops: §25.4 prometheus client config rejected: %v", perr)
		}
	} else {
		log.Printf("lenny-ops: §25.16 BYO Prometheus not configured; cross-replica health degrades to per-replica fan-out")
	}
	alertEvaluator := evaluator.NewWithEmitter(
		rules.Catalog(),
		evaluator.NoopExprEvaluator{},
		evaluator.EventEmitOptions{
			Emitter: w.opsEmitter,
			Source:  "//lenny.dev/ops/" + w.replicaID,
		},
	)
	go alertEvaluator.Run(w.ctx)

	// §25.4 lines 1562-1564: build the OIDC authentication + role gate.
	// The verify key is the shared HMAC signing key (the gateway Token
	// Service / §17.4 embedded OIDC key). When no key is configured the
	// surface is unauthenticated, which is rejected in production: serving
	// the platform-admin remediation-lock / backup / drift surface without
	// authentication is the §25.4 security regression the gate closes.
	authCfg, err := buildAuthConfig(*w.f.bearerTrustHMACKeyFile, *w.f.oidcIssuerURL, *w.f.authMultiTenant,
		*w.f.production, *w.f.rateLimitRPS, *w.f.rateLimitBurst)
	if err != nil {
		log.Fatalf("lenny-ops: %v", err)
	}

	// The §25.4 HTTP surface. Every replica serves it, leader or not. It
	// hosts the §25.6 diagnostics, the §25.7 runbook index, the §25.4
	// self-health, remediation-lock, and escalation endpoints, the
	// §25.10 drift endpoints, the §25.11 backup endpoints, and the
	// §25.12 MCP management server.
	// §25.4 lines 2528-2534: the pod-log proxy reads container logs via the
	// Kubernetes API so agents do not need kubectl access. Wired only when a
	// cluster connection is available; otherwise the endpoint reports the
	// proxy unavailable.
	var podLogs opsserver.PodLogReader
	if w.clientset != nil {
		podLogs = k8sPodLogReader{pods: w.clientset.CoreV1()}
	}

	// §25.4 GET /v1/admin/me platform context + capabilities snapshot. The
	// capabilities reflect the actual wired state; opsReplicas reads the
	// live Endpoints count. F-25.4.2.
	meConfig := &opsserver.MeConfig{
		InstallationID:           *w.f.installationID,
		Version:                  buildVersion,
		Tier:                     *w.f.platformTier,
		Namespace:                envOr("POD_NAMESPACE", *w.f.leaderElectNS),
		OpsServiceURL:            *w.f.opsServiceURL,
		GatewayURL:               *w.f.gatewayURL,
		Issuer:                   *w.f.oidcIssuerURL,
		TokenRefreshBeforeExpiry: w.f.gatewayTokenRefreshBefore.String(),
		Capabilities: opsserver.Capabilities{
			PrometheusAvailable:     *w.f.prometheusURL != "",
			OpsReplicas:             1,
			MtlsInternal:            *w.f.gatewayInternalTLS,
			LockMemoryTier:          *w.f.locksMemoryTier,
			TenantFiltering:         *w.f.authMultiTenant,
			HeadlessServiceFallback: *w.f.gatewayHeadlessSvc != "",
		},
		LiveOpsReplicas: func() int {
			if w.replicaCounter != nil {
				return w.replicaCounter.ReplicaCount()
			}
			return 1
		},
	}

	// §25.4 audit recorder for identity.discovered (line 1641) +
	// operations.inventory_queried (line 1779). Each emits a durable §11.7
	// platform-audit row through auditRecorder (logged only in the degraded
	// no-Postgres mode). F-25.4.22.
	opsAudit := opsserver.LogAuditRecorder{Sink: func(event string, fields map[string]any) {
		w.auditRecorder.Record(event, fields, time.Time{})
	}}
	w.opsHandler = opsserver.New(opsserver.Options{
		Probes:             w.probes,
		Runbooks:           w.runbookSource,
		SelfHealth:         w.svc.Monitor(),
		Leader:             w.svc,
		Backups:            w.backupSvc,
		Diagnostics:        w.diagnosticSvc,
		Doctor:             w.doctorSvc,
		Drift:              w.driftSvc,
		Locks:              w.lockSvc,
		LockCoordination:   w.lockCoordination,
		Escalations:        w.escalationSvc,
		EventStream:        w.eventStream,
		EventSubscriptions: w.eventSubscriptions,
		// §25.5 line 2751: the subscription_cache_invalidate peer RPC.
		CacheInvalidator:     w.subscriptionCache,
		CacheInvalidateToken: w.delivery.InvalidateToken,
		ReleaseChannel:       w.releaseChannelPub,
		Upgrade:              w.upgradeSvc,
		UpgradeChecker:       w.upgradeChecker,
		UpgradePreflighter:   w.upgradePreflighter,
		VersionAggregator:    w.versionAggregator,
		PlatformConfig:       w.platformConfigSvc,
		Registry:             w.registrySvc,
		BuildVersion:         buildVersion,
		PodLogs:              podLogs,
		Production:           *w.f.production,
		DiagnosticsAudit:     buildDiagnosticsAudit(*w.f.diagnosticsAuditRate, w.auditRecorder),
		Auth:                 authCfg,
		Idempotency:          w.idemStore,
		// §25.4 ops.idempotency.{keyTTLSeconds,longRunningKeyTTLSeconds}: a
		// zero value keeps the opsidem built-in (24h/7d). F-25.4.9.
		IdempotencyStandardTTL:    time.Duration(*w.f.idempotencyKeyTTL) * time.Second,
		IdempotencyLongRunningTTL: time.Duration(*w.f.idempotencyLongRunningTTL) * time.Second,
		Inventory:                 w.inventory,
		Me:                        meConfig,
		Audit:                     opsAudit,
		// §16.8 / §16.9: expose every instrument registered on the process
		// default registry (self-health, backup, drift, rate-limit,
		// diagnostics) on the mandatory lenny-ops scrape target.
		Metrics: promhttp.Handler(),
	})
	w.srv = &http.Server{
		Addr:              *w.f.addr,
		Handler:           w.opsHandler,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

// runServer starts the §25.4 background loops (diagnostics-audit sweep, the
// §25.5/§25.4 gauge refresh, and the remediation-lock reap), the §4.4
// pgaudit shipper, and the leader-election goroutine, then serves the §25.4
// HTTP surface until shutdown and drains the background loops. It blocks
// until the server stops and the loops return. spec: §25.4, §25.5, §4.4.
func (w *opsWiring) runServer() {
	// spec: §25.9 lines 3699-3700 — flush closed diagnostics-audit
	// coalescing windows on a periodic tick so a window emits even during
	// an idle period, and drain every open window on shutdown.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-w.ctx.Done():
				w.opsHandler.FlushDiagnosticsAudit()
				return
			case t := <-ticker.C:
				w.opsHandler.SweepDiagnosticsAudit(t)
			}
		}
	}()

	// spec: §25.5 lines 2787, 2789 — refresh the event-stream gauges
	// (lenny_ops_events_stream_length from the Redis XLEN of
	// ops:events:stream, lenny_ops_events_sse_active_connections from the
	// live SSE subscriber count) on every replica so the §16.9 scrape
	// reflects current depth and connection count.
	// §25.4 lines 2491-2497 — refresh the self-health source gauges
	// (postgres pool-active connections, redis consumer lag, webhook
	// backlog) on the same cadence so the §16.9 scrape exposes the raw
	// inputs behind the lenny_ops_self_health_status{check} statuses.
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		sample := func() {
			sampleEventStreamGauges(w.ctx, w.redisClient, w.eventStream)
			sampleSelfHealthSourceGauges(w.pgPool, nil, w.webhook.Backlog)
		}
		sample()
		for {
			select {
			case <-w.ctx.Done():
				return
			case <-ticker.C:
				sample()
			}
		}
	}()

	// spec: §25.4 line 2193 — the remediation-lock reap. Expired locks are
	// cleaned up lazily on acquire and by this 60s periodic sweep on every
	// replica: the in-memory tier is replica-local so each replica reaps its
	// own, and the durable-tier DELETE is idempotent across replicas. The
	// sweep also emits the remediation.lock_expired audit event for each
	// in-memory lock removed.
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-w.ctx.Done():
				return
			case <-ticker.C:
				if n := w.lockSvc.Reap(w.ctx); n > 0 {
					log.Printf("lenny-ops: remediation-lock reap removed %d expired lock(s)", n)
				}
			}
		}
	}()

	// §4.4 line 232: when --pgaudit-log-file is set, start the pgaudit
	// shipper. The shipper tails the file, parses each AUDIT line,
	// translates to OCSF, and delivers to the NoOp sink (deployers
	// override the sink by editing pkg/audit/pgaudit/main wiring to
	// point at a real downstream). The metrics surface bumps the
	// catalog-declared lenny_pgaudit_grant_events_total counter so the
	// §16.5 PgAuditSinkDeliveryFailed alert has a signal to fire on.
	if *w.f.pgauditLogFile != "" {
		w.pgauditShipper = pgaudit.New(pgaudit.Config{
			LogFile:  *w.f.pgauditLogFile,
			TenantID: *w.f.pgauditTenantID,
			Sink:     pgaudit.NoOpSink(),
			// Metrics could be wired via a Prometheus registerer once
			// lenny-ops exposes its own /metrics; for now the shipper
			// runs without per-class metric emission. The dedicated
			// PromMetrics adapter (pkg/audit/pgaudit/prommetrics.go)
			// is the integration seam.
		})
		if err := w.pgauditShipper.Start(w.ctx); err != nil {
			log.Printf("lenny-ops: pgaudit shipper start failed (continuing without it): %v", err)
			w.pgauditShipper = nil
		} else {
			log.Printf("lenny-ops: §4.4 pgaudit shipper tailing %s (tenant=%s)",
				*w.f.pgauditLogFile, *w.f.pgauditTenantID)
		}
	}

	// The background loops run on their own goroutine; the leader-only
	// loops start when this replica wins the lease.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.svc.Run(w.ctx)
	}()

	// On shutdown signal, stop the HTTP server within the grace window.
	go func() {
		<-w.ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), *w.f.shutdownTimeout)
		defer cancel()
		_ = w.srv.Shutdown(shutdownCtx)
	}()

	log.Printf("lenny-ops: replica %s serving the operability API on %s (loops: %v)",
		w.replicaID, *w.f.addr, w.svc.LoopNames())
	if err := w.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("lenny-ops: serve: %v", err)
	}

	// The HTTP server has stopped; wait for the background loops to
	// drain (StopLeaderLoops blocks until the singleton loops return).
	w.stop()
	if w.pgauditShipper != nil {
		w.pgauditShipper.Stop()
	}
	wg.Wait()
	log.Printf("lenny-ops: replica %s stopped", w.replicaID)
}
