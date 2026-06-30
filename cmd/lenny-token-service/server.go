// SPDX-License-Identifier: MIT

package main

import (
	"log"
	"os"
	"time"

	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/driftmonitor"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	"github.com/lennylabs/lenny/pkg/redisconn"
	"github.com/lennylabs/lenny/pkg/tokenservice"
	tokencache "github.com/lennylabs/lenny/pkg/tokenservice/cache"
)

// buildDriftMonitor constructs the §13.3 line 595 NTP drift self-monitor and
// starts its sampling loop. The source returns the clockinject-injected offset
// for v1 (zero in production unless an operator wires a real adjtimex/chrony
// probe). The exchange path consults driftMonitor.Degraded() and returns
// 503 token_validation_unavailable when |drift| >= 5s.
//
// spec: §13.3 line 595 / F-13.3.5.
func (w *tokenServiceWiring) buildDriftMonitor() {
	w.driftMonitor = driftmonitor.New(func() time.Duration {
		off, _ := clockinject.Offset()
		return off
	}, w.metricsEmitter)
	go w.driftMonitor.Start(ctx, 30*time.Second)
}

// buildServer constructs the §13.3 token-exchange Server from the signer,
// issuer, per-dialect caps, write-before-issue stores, metrics, rate limits,
// and the drift-degraded gate.
//
// spec: §13.3.
func (w *tokenServiceWiring) buildServer() {
	w.srv = tokenservice.NewServer(tokenservice.Options{
		Signer: w.signer,
		Issuer: *w.f.issuer,
		PerDialectCap: map[string]time.Duration{
			"lenny-gateway": 24 * time.Hour,
			"lenny-ops":     1 * time.Hour,
			"llm-proxy":     1 * time.Hour,
		},
		IssuedTokens: w.issuedTokens,
		Auditor:      w.auditor,
		Metrics:      w.metricsEmitter,
		RateLimit: tokenservice.RateLimitOptions{
			CallerPerSecond: *w.f.rlCallerPerSec,
			CallerPerMinute: *w.f.rlCallerPerMin,
			TenantPerSecond: *w.f.rlTenantPerSec,
			SampleWindow:    *w.f.rlSampleWindow,
		},
		DriftDegraded: w.driftMonitor.Degraded,
	})
}

// buildEventEmitterAndCache wires the §4.0 EventEmitter and, when --redis-url
// is set, the §25.5 Redis event stream and the §4.3 Redis-backed encrypted
// access-token cache. The §4.0 spec requires every process hosting subsystems
// that may emit §16.6 operational events to construct an EventEmitter so a
// future emit site does not have to re-thread the dependency through the
// binary. The buffer is constructed unconditionally so a Redis outage degrades
// to local-only delivery rather than dropping events. The Redis client close
// is recorded on the accumulator so runTokenService can defer it.
//
// spec: §4.0 line 13, §25.5, §4.3 line 201.
func (w *tokenServiceWiring) buildEventEmitterAndCache() {
	w.replicaID = os.Getenv("HOSTNAME")
	if w.replicaID == "" {
		w.replicaID = "token-service"
	}
	opsEventBuffer := eventbuffer.NewEventBuffer(0)
	w.opsEmitter = eventbuffer.NewEmitter(opsEventBuffer, w.replicaID)
	// §4.3 line 201 Redis-backed encrypted access-token cache. Wired
	// only when --redis-url is set; the cache short-circuits Postgres
	// revocation lookups on the validation hot path. With no Redis,
	// the validator falls back to the authoritative Postgres lookup.
	if *w.f.redisURL != "" {
		redisClient, err := redisconn.NewClient(redisconn.Config{URL: *w.f.redisURL, Password: *w.f.redisPassword})
		if err != nil {
			fatalf("redis client: %v", err)
		}
		w.redisCleanup = func() { _ = redisClient.Close() }
		w.opsEmitter = eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
			Client:    redisClient,
			Buffer:    opsEventBuffer,
			Source:    "//lenny.dev/token-service/" + w.replicaID,
			ReplicaID: w.replicaID,
		})
		log.Printf("lenny-token-service: §25.5 operational events streaming to Redis %s", eventbuffer.DefaultStreamKey)
		accessCache, err := tokencache.New(redisClient, w.kmsProvider)
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
		w.replicaID, *w.f.redisURL != "")
	_ = w.opsEmitter
}
