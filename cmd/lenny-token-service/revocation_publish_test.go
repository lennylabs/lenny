// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	revocationprop "github.com/lennylabs/lenny/pkg/gateway/credentials/revocation/propagator"
	"github.com/lennylabs/lenny/tests/testinfra/tlsredis"
)

// startupWithRedis runs the Token Service build steps in runTokenService
// order over the local KMS provider and the in-memory stores, with
// --redis-url at redisURL (empty for none).
func startupWithRedis(t *testing.T, redisURL string) *tokenServiceWiring {
	t.Helper()
	f := signingWiringFlags(filepath.Join(t.TempDir(), "master.key"))
	empty := ""
	f.redisURL = &redisURL
	f.redisPassword = &empty
	w := &tokenServiceWiring{f: f}
	w.buildMetrics()
	w.buildKMSAndSigner()
	w.buildStores()
	w.buildEventEmitterAndCache()
	if w.redisCleanup != nil {
		t.Cleanup(w.redisCleanup)
	}
	w.buildDriftMonitor()
	w.buildServer()
	return w
}

// TestTokenServiceRevocationPublisher_spec_13_3 is the F-13.3.19
// regression at the wiring level. With --redis-url set, startup builds a
// revocation bus before the Server, and the publisher it hands the
// Server sends a revoked jti on the channel every gateway replica's
// revocation propagator and the lenny-ops revocation set subscribe to.
// Without Redis there is no bus and no publisher, so the Server records
// postgres_only rather than an eventbus delivery a no-op publish did not
// make.
//
// spec: 13.3 (Credential Flow) "Token rotation and revocation", 16.7 (token.revoked propagation_mode)
func TestTokenServiceRevocationPublisher_spec_13_3(t *testing.T) {
	if p := revocationPublisher(nil); p != nil {
		t.Fatal("revocationPublisher(nil) is non-nil, so a Redis-less Token Service would record eventbus")
	}
	if w := startupWithRedis(t, ""); w.revocationBus != nil || w.srv == nil {
		t.Fatalf("without --redis-url: bus %v server %v, want no bus and a server", w.revocationBus, w.srv)
	}

	rd := tlsredis.Start(t, "redis-secret")
	w := startupWithRedis(t, rd.URL)
	if w.revocationBus == nil || w.srv == nil {
		t.Fatalf("with --redis-url: bus %v server %v, want both", w.revocationBus, w.srv)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sub := rd.Client.Subscribe(ctx, revocationprop.Channel)
	t.Cleanup(func() { _ = sub.Close() })
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	publish := revocationPublisher(w.revocationBus)
	if publish == nil {
		t.Fatal("no publisher over the Redis bus")
	}
	if err := publish(ctx, "acme", "jti-rotated"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case msg := <-sub.Channel():
		if msg.Payload != "jti-rotated" {
			t.Fatalf("published %q, want jti-rotated", msg.Payload)
		}
	case <-ctx.Done():
		t.Fatal("the publisher sent nothing on the revocation channel")
	}
}
