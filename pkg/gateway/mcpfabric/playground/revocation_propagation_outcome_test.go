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
// the subscribe loop's reconnect branch, which the resubscribe test
// below drives.
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

// TestAuthoritativeRedisCheckSamplesPropagationAtMostOncePerRevocation
// drives the other observation point the histogram's description names:
// a peer replica whose negative cache is cold learns of the revocation
// through the authoritative Redis lookup on its auth hot path. That
// lookup runs on every playground-origin request, while the histogram
// measures the latency of one propagation event, so repeated requests
// for an already-revoked bearer must not accumulate one sample each.
// The peer here never subscribes, so the pub/sub accelerator cannot
// warm its cache and the Redis consult is the only way it learns of the
// revocation.
//
// spec: §27.8 — "End-to-end propagation latency from when a revocation
// is written on the originating replica to when peer replicas observe
// it on their auth hot path (authoritative Redis `GET` and/or
// pub/sub-warmed negative cache). `outcome ∈ {pubsub_delivered,
// redis_authoritative, resubscribe}`"; §27.3.1 — "The authoritative
// per-request revocation check runs on every playground-origin request".
func TestAuthoritativeRedisCheckSamplesPropagationAtMostOncePerRevocation_spec_27_8_244(t *testing.T) {
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

	// Ten requests stand in for a session that keeps calling after its
	// bearer was revoked: each one must fail closed, and the propagation
	// histogram must not grow with the request count.
	const requests = 10
	for i := range requests {
		revoked, err := peer.IsBearerRevoked(ctx, tenant, jti)
		if err != nil || !revoked {
			t.Fatalf("peer IsBearerRevoked call %d = %v, %v; want true, nil", i, revoked, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(observed) > 1 {
		t.Errorf("%d requests against a revoked bearer recorded %d propagation samples %v, want at most one per revocation", requests, len(observed), observed)
	}
	for _, outcome := range observed {
		if !slices.Contains(spec278PropagationOutcomes, outcome) {
			t.Errorf("propagation sample carried outcome %q, outside the §27.8 domain %v", outcome, spec278PropagationOutcomes)
		}
	}
}

// TestDroppedSubscriptionEmitsResubscribeOutcome pins the §27.3.1
// requirement that a replica whose revocation subscription drops
// re-subscribes and reports the outage on the §27.8 propagation
// histogram. Closing the subscriber's client closes the pub/sub message
// channel underneath the consume loop, which is how a dropped
// subscription surfaces to it; the loop then backs off, re-subscribes,
// and records the gap.
//
// spec: §27.3.1 — "Replicas with a dropped subscription MUST
// re-subscribe and emit a
// `lenny_playground_session_revocation_propagation_seconds` sample
// tagged `{outcome=\"resubscribe\"}` for the duration of the outage."
func TestDroppedSubscriptionEmitsResubscribeOutcome_spec_27_3_1_98(t *testing.T) {
	mr := miniredis.RunT(t)
	publisherClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = publisherClient.Close() })
	// The subscriber holds its own client so closing it drops only the
	// subscription, leaving the publisher able to keep writing.
	subscriberClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = subscriberClient.Close() })

	publisher := NewRedisSessionStore(publisherClient)
	subscriber := NewRedisSessionStore(subscriberClient)
	var mu sync.Mutex
	outcomes := map[string]int{}
	var resubscribeSeconds float64
	subscriber.propObserver = func(outcome string, seconds float64) {
		mu.Lock()
		defer mu.Unlock()
		outcomes[outcome]++
		if outcome == "resubscribe" {
			resubscribeSeconds = seconds
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go subscriber.SubscribeAllRevocations(ctx)

	const tenant = "acme"
	// Wait until the subscription is live: miniredis drops a message
	// published before the PSUBSCRIBE registers, so re-publish on each
	// poll until the cross-replica sample lands. The write is idempotent.
	waitForCondition(t, 5*time.Second, func() bool {
		if err := publisher.RevokeSession(ctx, tenant, "sess-drop", []string{"jti-drop"}, time.Minute); err != nil {
			t.Fatalf("RevokeSession: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		return outcomes["pubsub_delivered"] > 0
	})

	// Drop the subscription.
	if err := subscriberClient.Close(); err != nil {
		t.Fatalf("close subscriber client: %v", err)
	}

	waitForCondition(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return outcomes["resubscribe"] > 0
	})

	mu.Lock()
	defer mu.Unlock()
	if resubscribeSeconds < 0 {
		t.Errorf("resubscribe sample recorded a negative outage duration %v", resubscribeSeconds)
	}
	for outcome := range outcomes {
		if !slices.Contains(spec278PropagationOutcomes, outcome) {
			t.Errorf("propagation sample carried outcome %q, outside the §27.8 domain %v", outcome, spec278PropagationOutcomes)
		}
	}
}
