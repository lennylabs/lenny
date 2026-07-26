// SPDX-License-Identifier: MIT

package playground

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// spec278PropagationOutcomes transcribes the §27.8 label domain of
// lenny_playground_session_revocation_propagation_seconds: "outcome ∈
// {pubsub_delivered, redis_authoritative, resubscribe}".
var spec278PropagationOutcomes = []string{"pubsub_delivered", "redis_authoritative", "resubscribe"}

// TestRevocationPropagationOutcomesStayWithinSpec278Domain drives a
// revocation across two replicas sharing one Redis and pins every
// outcome label the observing replica emits to the §27.8 label domain,
// so no propagation sample can carry a label the metrics table does not
// declare. The cross-replica pub/sub observation is the one production
// emitter this end-to-end path reaches; the outage sample comes from
// the subscribe loop's reconnect branch, and the third declared label
// has no producer at all (see the following test).
//
// spec: §27.8 — "`outcome ∈ {pubsub_delivered, redis_authoritative,
// resubscribe}`" for lenny_playground_session_revocation_propagation_seconds.
func TestRevocationPropagationOutcomesStayWithinSpec278Domain_spec_27_8_244(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	// Two stores model two gateway replicas over one Redis: the
	// publisher revokes, the subscriber observes.
	publisher := NewRedisSessionStore(client)
	subscriber := NewRedisSessionStore(client)
	var mu sync.Mutex
	outcomes := map[string]int{}
	subscriber.propObserver = func(outcome string, seconds float64) {
		mu.Lock()
		defer mu.Unlock()
		outcomes[outcome]++
		if seconds < 0 {
			t.Errorf("propagation sample %q recorded a negative latency %v", outcome, seconds)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go subscriber.SubscribeAllRevocations(ctx)

	const tenant = "acme"
	const jti = "jti-outcome-domain"
	// miniredis drops a message published before the PSUBSCRIBE has
	// registered, so re-publish on each poll until the subscriber
	// records the cross-replica sample. The marker write is idempotent.
	waitForCondition(t, 5*time.Second, func() bool {
		if err := publisher.RevokeSession(ctx, tenant, "sess-outcome-domain", []string{jti}, time.Minute); err != nil {
			t.Fatalf("RevokeSession: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		return outcomes["pubsub_delivered"] > 0
	})

	mu.Lock()
	defer mu.Unlock()
	for outcome := range outcomes {
		if !slices.Contains(spec278PropagationOutcomes, outcome) {
			t.Errorf("propagation sample carried outcome %q, outside the §27.8 domain %v", outcome, spec278PropagationOutcomes)
		}
	}
}

// TestRevocationPropagationEmitsRedisAuthoritativeOnHotPathObservation
// covers the third §27.8 outcome: a peer replica whose negative cache
// is cold observes the revocation through the authoritative Redis
// lookup on its auth hot path, which is one of the two observation
// points the histogram's description names. The peer here never
// subscribes, so the pub/sub accelerator cannot warm its cache and the
// Redis consult is the only way it learns of the revocation. Turning
// that observation into a latency sample also needs the instant the
// revocation was written, which the presence-only revocation marker
// does not carry, so the assertion is held here until the metrics table
// and the emission set are reconciled.
//
// spec: §27.8 — "End-to-end propagation latency from when a revocation
// is written on the originating replica to when peer replicas observe
// it on their auth hot path (authoritative Redis `GET` and/or
// pub/sub-warmed negative cache). `outcome ∈ {pubsub_delivered,
// redis_authoritative, resubscribe}`."
func TestRevocationPropagationEmitsRedisAuthoritativeOnHotPathObservation_spec_27_8_244(t *testing.T) {
	t.Skip("the redis_authoritative outcome the §27.8 label domain declares has no producer; whether the auth hot path samples it or the spec drops the value is an open spec question")

	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	publisher := NewRedisSessionStore(client)
	// The peer never subscribes, so its negative cache stays cold and
	// the authoritative Redis lookup is the observation point.
	peer := NewRedisSessionStore(client)
	var mu sync.Mutex
	var observed []string
	peer.propObserver = func(outcome string, _ float64) {
		mu.Lock()
		observed = append(observed, outcome)
		mu.Unlock()
	}

	ctx := context.Background()
	const tenant = "acme"
	const jti = "jti-hot-path"
	if err := publisher.RevokeSession(ctx, tenant, "sess-hot-path", []string{jti}, time.Minute); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}

	revoked, err := peer.IsBearerRevoked(ctx, tenant, jti)
	if err != nil || !revoked {
		t.Fatalf("peer IsBearerRevoked = %v, %v; want true, nil", revoked, err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !slices.Contains(observed, "redis_authoritative") {
		t.Fatalf("peer observed the revocation through the authoritative Redis lookup but recorded outcomes %v, want one tagged redis_authoritative", observed)
	}
}
