// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
	"github.com/lennylabs/lenny/pkg/webhookdelivery"
)

// buildEventStreamAndWebhooks constructs the §25.5 operational-event stream
// and emitter, the webhook delivery worker, the subscription cache, and the
// cold-start availability signal. spec: §25.5.
func (w *opsWiring) buildEventStreamAndWebhooks() {
	// The §25.5 operational-event stream service and emitter. lenny-ops
	// emits the events it originates (ops_health_status_changed,
	// escalation_*, remediation_lock_*, drift_detected,
	// platform_upgrade_*, operation_progressed) into this service;
	// subsystems take it as the §4.0 EventEmitter dependency. When Redis
	// is wired every emit also writes to the platform-scoped
	// ops:events:stream alongside the gateway-emitted events; without
	// Redis the local opsstream.Service in-memory buffer is the only
	// delivery surface (per §25.5 cold-start). Built before the webhook
	// worker so OnSelfHealthChange can emit through it and the worker can
	// consume the same Redis stream.
	// §25.5 degradation matrix: when Redis backs the stream, a source-
	// health probe lets the read surface attach the degradation envelope
	// (Redis-down → gateway-buffer fall-back) and the dual-outage 503.
	// Without Redis there is no meaningful "outage" to signal, so the
	// probe is omitted and the read surface stays healthy. spec: §25.5
	// lines 2768-2780. F-25.5.14.
	streamOpts := opsstream.Options{ReplicaID: w.replicaID, OnGap: observeStreamGap}
	if w.redisClient != nil {
		w.srcHealth = newSourceHealthProbe()
		streamOpts.SourceHealth = w.srcHealth
		// The §25.5 read side consumes the same platform-scoped
		// ops:events:stream the fan-out emitter and webhook worker use, so
		// polling and SSE serve the merged cross-replica view (XRANGE
		// polling / backlog resume, XREAD BLOCK 0 live tail) whenever the
		// source-health probe reports Redis reachable.
		streamOpts.RedisClient = w.opsStreamRedis
		streamOpts.RedisStreamKey = eventbuffer.DefaultStreamKey
	}
	w.eventStream = opsstream.New(streamOpts)
	var opsEmitter events.EventEmitter = w.eventStream
	if w.redisClient != nil {
		fanOut := newRedisFanOutEmitter(w.redisClient, w.eventStream, w.replicaID, *w.f.eventsStreamMaxLen)
		// §25.5 best-effort recovery flush: on a Redis down-to-up edge the
		// source-health probe re-emits the lenny-ops events buffered locally
		// during the outage to the recovered stream, through the same
		// StreamEmitter the fan-out writes to (ReEmit skips the local
		// re-publish since the events are already in the ring).
		w.eventStream.SetRedisReEmitter(fanOut.ReEmit)
		opsEmitter = fanOut
		log.Printf("lenny-ops: §25.5 operational events streaming to Redis %s (maxlen=%d)", eventbuffer.DefaultStreamKey, *w.f.eventsStreamMaxLen)
	}
	w.opsEmitter = opsEmitter

	// The §25.5 webhook delivery worker. The §25.5 EventSource is the
	// Redis ops:events:stream consumer when Redis is wired (so the worker
	// fans out events emitted by every replica — gateway, controllers,
	// peer lenny-ops); without Redis it runs against an empty source and
	// delivers nothing, the correct cold-start behavior.
	var eventSource opsservice.EventSource = emptyEventSource{}
	if w.redisClient != nil {
		eventSource = opsservice.NewRedisEventSource(w.redisClient, eventbuffer.DefaultStreamKey)
		log.Printf("lenny-ops: §25.5 webhook worker consuming Redis stream %s", eventbuffer.DefaultStreamKey)
	}
	w.eventSource = eventSource

	// §25.5 line 2753: the cold-start health signal. When the subscription
	// cache cannot reach Postgres no webhook delivery occurs, so lenny-ops
	// emits ops_health_status_changed with subscriptionsUnavailable; a
	// later recovery emits the clear.
	onSubsAvailability := func(available bool) {
		w.subsUnavailMu.Lock()
		defer w.subsUnavailMu.Unlock()
		if available && !w.subsUnavailEmitted {
			return // healthy start or steady state — nothing to announce
		}
		w.subsUnavailEmitted = !available
		severity := "info"
		if !available {
			severity = "warning"
		}
		payload, _ := json.Marshal(map[string]any{
			"replicaId":                w.replicaID,
			"subscriptionsUnavailable": !available,
		})
		if err := w.opsEmitter.Emit(w.ctx, events.OperationalEvent{
			Type:            events.EventOpsHealthStatusChanged.CloudEventsType(),
			Subject:         "ops/" + w.replicaID,
			Severity:        severity,
			DataContentType: "application/json",
			Data:            payload,
		}); err != nil {
			log.Printf("lenny-ops: emit ops_health_status_changed (subscriptionsUnavailable=%t): %v", !available, err)
		}
	}

	// §25.5 line 2751: the invalidate-RPC token derives from the shared
	// HMAC key both replicas mount; an empty path (dev) disables the RPC.
	var webhookSharedKey []byte
	if *w.f.bearerTrustHMACKeyFile != "" {
		if b, rerr := os.ReadFile(*w.f.bearerTrustHMACKeyFile); rerr == nil {
			webhookSharedKey = b
		}
	}
	adminPort := 0
	if i := strings.LastIndex(*w.f.addr, ":"); i >= 0 {
		adminPort, _ = strconv.Atoi((*w.f.addr)[i+1:])
	}
	// §25.5 lines 2735-2745 ops.webhooks SSRF policy. The validator gates
	// subscription create/update and every delivery attempt. F-25.4.9.
	webhookSSRF := buildWebhookSSRF(*w.f.webhookAllowHTTP, *w.f.webhookBlockedCIDRs, *w.f.webhookDomainAllowlist)
	w.delivery = buildWebhookDelivery(w.ctx, webhookDeliveryDeps{
		Pool:         w.pgPool,
		Clientset:    w.clientset,
		Source:       w.eventSource,
		Emitter:      w.opsEmitter,
		SSRF:         webhookSSRF,
		Audit:        subscriptionAuditFunc(func(ev eventsubscription.AuditEvent) { log.Printf("lenny-ops: audit %s %v", ev.Type, ev.Details) }),
		SharedKey:    webhookSharedKey,
		Namespace:    envOr("POD_NAMESPACE", *w.f.leaderElectNS),
		ServiceName:  *w.f.opsServiceName,
		SelfIP:       os.Getenv("POD_IP"),
		AdminPort:    adminPort,
		TrackingMode: webhookdelivery.TrackingMode(*w.f.webhookTrackingMode),
		Retention: opsservice.RetentionPolicy{
			Retention:             time.Duration(*w.f.webhookRetentionDays) * 24 * time.Hour,
			FailuresOnlyRetention: time.Duration(*w.f.webhookFailuresRetentionDays) * 24 * time.Hour,
		},
		OnAvailabilityChange: onSubsAvailability,
	})
	w.webhook = w.delivery.Worker
	w.eventSubscriptions = w.delivery.Subscriptions
	w.subscriptionCache = w.delivery.Cache
}

// buildGatewayLink constructs the §25.4 gateway admin-API client and starts
// the §25.5 source-health refresh loop once both the Redis and gateway
// clients exist. spec: §25.4 F-25.4.8, §25.5 F-25.5.14.
func (w *opsWiring) buildGatewayLink() {
	// The §25.4 gateway admin-API client (F-25.4.8). It refreshes the
	// service-account OIDC token, routes per-replica fan-out through the
	// headless Service with circuit breakers, and emits the NET-070
	// handshake metric (F-25.4.19) on every request. The gateway-auth
	// self-health check is its in-process consumer; the per-replica
	// health/recommendation aggregation endpoints consume the same client
	// as they land (F-25.4.3).
	gwClient, err := buildGatewayClient(gatewayClientConfig{
		gatewayURL:          *w.f.gatewayURL,
		headlessService:     *w.f.gatewayHeadlessSvc,
		namespace:           envOr("POD_NAMESPACE", *w.f.leaderElectNS),
		internalTLS:         *w.f.gatewayInternalTLS,
		internalTLSPort:     *w.f.gatewayTLSPort,
		plaintextPort:       *w.f.gatewayPlaintextPort,
		fanOutTimeout:       *w.f.gatewayFanOutTimeout,
		breakerThreshold:    *w.f.gatewayBreakerThreshold,
		breakerResetAfter:   *w.f.gatewayBreakerResetAfter,
		saTokenPath:         *w.f.gatewaySATokenFile,
		refreshBeforeExpiry: *w.f.gatewayTokenRefreshBefore,
		minTokenTTL:         *w.f.gatewayTokenMinTTL,
		caBundlePath:        *w.f.gatewayCABundleFile,
	}, prometheus.DefaultRegisterer)
	if err != nil {
		log.Fatalf("lenny-ops: build gateway client: %v", err)
	}
	w.gwClient = gwClient

	// Inject the §25.5 Redis-down gateway-buffer fall-back source now that
	// the gateway admin-API client exists (it was nil at the opsstream.New
	// construction site). During a Redis outage the read surface fans GET
	// /v1/admin/events/buffer across every gateway replica over the
	// lenny-gateway-pods headless Service and merges the pages deduped by
	// eventKey. spec: §25.5 (Redis-down gateway-buffer fallback).
	// A nil client (single-process dev with no gateway configured) is left
	// uninjected: passing it would store a non-nil interface holding a nil
	// client, which the read surface would then select as a live fall-back
	// source and dereference.
	if w.eventStream != nil && w.gwClient != nil {
		w.eventStream.SetGatewayBufferSource(w.gwClient)
	}

	// Start the §25.5 source-health refresh now that both the Redis
	// client and the gateway client exist. The loop is bounded by ctx,
	// so it exits on shutdown. On a Redis down-to-up edge it drives the
	// §25.5 best-effort recovery flush. F-25.5.14.
	if w.srcHealth != nil {
		go w.srcHealth.run(w.ctx, 15*time.Second, w.redisClient, w.gwClient, redisEdgeCallbacks{
			onRedisDown:      w.openEventStreamOutageWindow,
			onRedisRecovered: w.recoverEventStreamToRedis,
		})
	}
}

// openEventStreamOutageWindow is the §25.5 callback the source-health probe
// invokes when it first observes Redis unreachable. It records where the local
// ring stood at the outage's start so the recovery flush re-emits the events
// buffered during the outage rather than the whole ring. spec: §25.5
// (best-effort recovery flush scoped to the outage window).
func (w *opsWiring) openEventStreamOutageWindow() {
	if w.eventStream == nil {
		return
	}
	w.eventStream.MarkRedisOutage()
}

// recoverEventStreamToRedis is the §25.5 best-effort recovery-flush callback
// the source-health probe invokes on a Redis down-to-up edge. It re-emits the
// lenny-ops-originated events buffered locally during the outage to the
// recovered ops:events:stream, deduped by eventKey, bounded so a slow flush
// does not stall the probe loop. A re-emit failure is logged and does not
// block serving. spec: §25.5 (best-effort recovery flush).
func (w *opsWiring) recoverEventStreamToRedis(ctx context.Context) {
	if w.eventStream == nil {
		return
	}
	fctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	flushed, err := w.eventStream.FlushBufferedToRedis(fctx)
	if err != nil {
		log.Printf("lenny-ops: §25.5 recovery flush re-emitted %d event(s) with error: %v", flushed, err)
		return
	}
	if flushed > 0 {
		log.Printf("lenny-ops: §25.5 recovery flush re-emitted %d locally-buffered event(s) to Redis %s", flushed, eventbuffer.DefaultStreamKey)
	}
}
