// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/driftmonitor"
	revocationprop "github.com/lennylabs/lenny/pkg/gateway/credentials/revocation/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pubsub"
	"github.com/lennylabs/lenny/pkg/tokenservice"
	tokencache "github.com/lennylabs/lenny/pkg/tokenservice/cache"
)

// buildDriftMonitor constructs the §13.3 NTP drift self-monitor and
// starts its sampling loop. The source returns the clockinject-injected offset
// for v1 (zero in production unless an operator wires a real adjtimex/chrony
// probe). The exchange path consults driftMonitor.Degraded() and returns
// 503 token_validation_unavailable when |drift| >= 5s.
//
// spec: §13.3 / F-13.3.5.
func (w *tokenServiceWiring) buildDriftMonitor() {
	w.driftMonitor = driftmonitor.New(func() time.Duration {
		off, _ := clockinject.Offset()
		return off
	}, w.metricsEmitter)
	go w.driftMonitor.Start(ctx, 30*time.Second)
}

// revocationPublisher returns the tokenservice RevocationPropagator that
// publishes a revoked jti on bus, the channel every gateway replica's
// revocation propagator and the lenny-ops revocation set (§25.4 rule 2)
// apply, so a peer refuses a rotated or revoked token within pub/sub
// latency rather than at its next 30-second read. A nil bus returns a
// nil propagator: the Server then records propagation_mode postgres_only
// rather than claiming an eventbus delivery that a no-op publish did not
// make.
//
// spec: §13.3 (Credential Flow) "Token rotation and revocation";
// §16.7 (token.revoked propagation_mode).
func revocationPublisher(bus *pubsub.Bus) func(ctx context.Context, tenantID, jti string) error {
	if bus == nil {
		return nil
	}
	return func(ctx context.Context, _, jti string) error {
		return revocationprop.Publish(ctx, bus, jti)
	}
}

// buildServer constructs the §13.3 token-exchange Server from the signer,
// the §10.2 key set as its verifier, the caller verifier that also admits
// the upstream IdP's ID token, the issuer, per-dialect caps, the
// §10.2 capability token lifetime,
// write-before-issue stores, metrics, rate limits, the drift-degraded
// gate, and the revocation publisher.
//
// spec: §13.3; §10.2 (Authentication) "Verifier key set and rotation".
func (w *tokenServiceWiring) buildServer() {
	callerVerifier, err := buildCallerVerifier(w.f, w.keySet)
	if err != nil {
		fatalf("%v", err)
	}
	srv, err := tokenservice.NewServer(tokenservice.Options{
		Signer:         w.signer,
		Verifier:       w.keySet,
		CallerVerifier: callerVerifier,
		Issuer:         *w.f.issuer,
		PerDialectCap: map[string]time.Duration{
			"lenny-gateway": 24 * time.Hour,
			"lenny-ops":     1 * time.Hour,
			"llm-proxy":     1 * time.Hour,
		},
		// spec: §10.2 (Authentication) "JWT claims" expiry — the
		// default lifetime of a token requested without expires_in.
		CapabilityTokenTTL: time.Duration(*w.f.capabilityTokenTTL) * time.Second,
		IssuedTokens:       w.issuedTokens,
		Auditor:            w.auditor,
		Metrics:            w.metricsEmitter,
		RateLimit: tokenservice.RateLimitOptions{
			CallerPerSecond: *w.f.rlCallerPerSec,
			CallerPerMinute: *w.f.rlCallerPerMin,
			TenantPerSecond: *w.f.rlTenantPerSec,
			SampleWindow:    *w.f.rlSampleWindow,
		},
		DriftDegraded: w.driftMonitor.Degraded,
		// spec: §13.3 — a committed revocation is published on the
		// revocation bus every gateway replica and lenny-ops subscribe
		// to. Nil without Redis, which records postgres_only.
		RevocationPropagator: revocationPublisher(w.revocationBus),
	})
	if err != nil {
		fatalf("token-exchange server: %v", err)
	}
	w.srv = srv
}

// buildEventEmitterAndCache wires the §4.0 EventEmitter and, when a Redis
// topology flag is set, the §25.5 Redis event stream, the §4.3
// Redis-backed encrypted access-token cache, and the §13.3 revocation bus.
// The §4.0 spec requires every process hosting subsystems that may emit
// §16.6 operational events to construct an EventEmitter so a future emit
// site does not have to re-thread the dependency through the binary. The
// buffer is constructed unconditionally so a Redis outage degrades to
// local-only delivery rather than dropping events. The event stream and
// the cache use the base client; the revocation bus uses the §12.4
// Cache/Pub-Sub concern client, the one the gateway replicas and lenny-ops
// subscribe on. The Redis client close is recorded on the accumulator so
// runTokenService can defer it.
//
// spec: §4.0, §25.5, §4.3, §13.3, §12.4.
func (w *tokenServiceWiring) buildEventEmitterAndCache() {
	w.replicaID = os.Getenv("HOSTNAME")
	if w.replicaID == "" {
		w.replicaID = "token-service"
	}
	opsEventBuffer := eventbuffer.NewEventBuffer(0)
	w.opsEmitter = eventbuffer.NewEmitter(opsEventBuffer, w.replicaID)
	rd, err := newTokenServiceRedis(w.f)
	if err != nil {
		fatalf("%v", err)
	}
	if rd != nil {
		w.redisCleanup = rd.Close
		w.revocationBus = pubsub.New(rd.cachePubSub())
		w.opsEmitter = eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
			Client:    rd.base,
			Buffer:    opsEventBuffer,
			Source:    "//lenny.dev/token-service/" + w.replicaID,
			ReplicaID: w.replicaID,
		})
		log.Printf("lenny-token-service: §25.5 operational events streaming to Redis %s", eventbuffer.DefaultStreamKey)
		// §4.3 Redis-backed encrypted access-token cache; the cache
		// short-circuits Postgres revocation lookups on the validation hot
		// path. With no Redis, the validator falls back to the
		// authoritative Postgres lookup.
		accessCache, err := tokencache.New(rd.base, w.kmsProvider)
		if err != nil {
			fatalf("access-token cache: %v", err)
		}
		w.accessCache = accessCache
		log.Printf("lenny-token-service: §4.3 access-token cache wired (envelope-encrypted, KEK alias %s)", tokencache.CacheKEKAlias)
	}
	_ = w.accessCache
	// Keep opsEmitter live so the linker retains the wiring even before a
	// subsystem in this binary takes it as a constructor dependency. A
	// future credential-rotation event emit will replace this no-op log.
	log.Printf("lenny-token-service: §4.0 EventEmitter ready (replica=%s, redis=%t)",
		w.replicaID, w.redisCleanup != nil)
	_ = w.opsEmitter
}
