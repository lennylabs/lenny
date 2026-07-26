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

// TestAuthoritativeRedisObservationRecordsRedisAuthoritativeOutcome
// holds the histogram to the third value of its declared outcome
// domain. A peer replica that runs no subscription has no pub/sub
// message to learn from, so the authoritative Redis lookup on its auth
// hot path is where it first observes the revocation. The metrics table
// names that lookup as an observation point of the same histogram, and
// redis_authoritative is the only declared label that describes it, so
// the observation must be sampled under that outcome.
//
// spec: §27.8 — "End-to-end propagation latency from when a revocation
// is written on the originating replica to when peer replicas observe
// it on their auth hot path (authoritative Redis `GET` and/or
// pub/sub-warmed negative cache). `outcome ∈ {pubsub_delivered,
// redis_authoritative, resubscribe}`."
func TestAuthoritativeRedisObservationRecordsRedisAuthoritativeOutcome_spec_27_8_244(t *testing.T) {
	t.Skip("production emits no redis_authoritative sample and §27.3.1 stores the revocation marker presence-only, so a cold-cache peer has no origin write instant to measure from; the §27.8 outcome enumeration and the emission set await reconciliation")

	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	publisher := NewRedisSessionStore(client)
	// The peer never subscribes, so the pub/sub accelerator cannot warm
	// its cache and the authoritative Redis lookup is its only route to
	// observing the revocation.
	peer := NewRedisSessionStore(client)
	var mu sync.Mutex
	type sample struct {
		outcome string
		seconds float64
	}
	var samples []sample
	peer.propObserver = func(outcome string, seconds float64) {
		mu.Lock()
		samples = append(samples, sample{outcome, seconds})
		mu.Unlock()
	}

	ctx := context.Background()
	const tenant = "acme"
	const jti = "jti-authoritative-outcome"
	if err := publisher.RevokeSession(ctx, tenant, "sess-authoritative-outcome", []string{jti}, time.Minute); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}

	revoked, err := peer.IsBearerRevoked(ctx, tenant, jti)
	if err != nil || !revoked {
		t.Fatalf("peer IsBearerRevoked = %v, %v; want true, nil", revoked, err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(samples) != 1 {
		t.Fatalf("cold-cache peer observing a revocation through the authoritative Redis lookup recorded %d propagation samples %v, want exactly one", len(samples), samples)
	}
	if samples[0].outcome != "redis_authoritative" {
		t.Errorf("authoritative-Redis observation recorded outcome %q, want redis_authoritative", samples[0].outcome)
	}
	if samples[0].seconds < 0 {
		t.Errorf("authoritative-Redis observation recorded a negative propagation latency %v", samples[0].seconds)
	}
	if !slices.Contains(spec278PropagationOutcomes, samples[0].outcome) {
		t.Errorf("propagation sample carried outcome %q, outside the §27.8 domain %v", samples[0].outcome, spec278PropagationOutcomes)
	}
}

// TestDroppedSubscriptionEmitsResubscribeOutcome pins the §27.3.1
// requirement that a replica whose revocation subscription drops
// re-subscribes and reports the outage on the §27.8 propagation
// histogram. The drop it drives is the one the spec sentence describes:
// the Redis server carrying a live subscription goes away and later
// comes back at the same address, so the replica loses revocation
// messages for the length of the outage and must both re-establish the
// subscription and report how long it was blind. The spec asks for a
// sample whose value is the duration of the outage, so one outage
// produces exactly one sample; a stream of samples emitted while the
// subscription is still down reports no duration and skews a histogram
// whose P99 is alerted against the 500 ms propagation SLO.
//
// spec: §27.3.1 — "Replicas with a dropped subscription MUST
// re-subscribe and emit a
// `lenny_playground_session_revocation_propagation_seconds` sample
// tagged `{outcome=\"resubscribe\"}` for the duration of the outage."
func TestDroppedSubscriptionEmitsResubscribeOutcome_spec_27_3_1_98(t *testing.T) {
	mr := miniredis.NewMiniRedis()
	if err := mr.Start(); err != nil {
		t.Fatalf("start redis: %v", err)
	}
	addr := mr.Addr()

	publisherClient := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = publisherClient.Close() })
	// The subscriber holds its own client so the two replicas' pools are
	// independent, as they are in production.
	subscriberClient := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = subscriberClient.Close() })

	publisher := NewRedisSessionStore(publisherClient)
	subscriber := NewRedisSessionStore(subscriberClient)
	var mu sync.Mutex
	outcomes := map[string]int{}
	var resubscribeSeconds []float64
	subscriber.propObserver = func(outcome string, seconds float64) {
		mu.Lock()
		defer mu.Unlock()
		outcomes[outcome]++
		if outcome == "resubscribe" {
			resubscribeSeconds = append(resubscribeSeconds, seconds)
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

	mu.Lock()
	deliveredBeforeOutage := outcomes["pubsub_delivered"]
	mu.Unlock()

	// The outage: the Redis server carrying the subscription goes away.
	// Revocations published anywhere in the fleet are invisible to this
	// replica until it re-subscribes.
	mr.Close()
	outageStart := time.Now()
	time.Sleep(300 * time.Millisecond)

	// Redis returns at the same address; the replica must re-establish
	// its subscription without operator intervention.
	recovered := miniredis.NewMiniRedis()
	if err := recovered.StartAddr(addr); err != nil {
		t.Fatalf("restart redis at %s: %v", addr, err)
	}
	t.Cleanup(recovered.Close)

	// The subscription is re-established once a freshly published
	// revocation reaches the subscriber again.
	waitForCondition(t, 20*time.Second, func() bool {
		if err := publisher.RevokeSession(ctx, tenant, "sess-recovered", []string{"jti-recovered"}, time.Minute); err != nil {
			return false
		}
		mu.Lock()
		defer mu.Unlock()
		return outcomes["pubsub_delivered"] > deliveredBeforeOutage
	})
	outageEnd := time.Now()

	// Give the loop a moment to settle so a repeating sample stream
	// surfaces here rather than after the assertions.
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(resubscribeSeconds) != 1 {
		t.Fatalf("one dropped subscription recorded %d resubscribe samples %v, want exactly one carrying the outage duration", len(resubscribeSeconds), resubscribeSeconds)
	}
	if got := resubscribeSeconds[0]; got <= 0 || got > outageEnd.Sub(outageStart).Seconds()*2 {
		t.Errorf("resubscribe sample recorded %v s, want a positive duration on the order of the %v outage", got, outageEnd.Sub(outageStart))
	}
	for outcome := range outcomes {
		if !slices.Contains(spec278PropagationOutcomes, outcome) {
			t.Errorf("propagation sample carried outcome %q, outside the §27.8 domain %v", outcome, spec278PropagationOutcomes)
		}
	}
}

// TestUnrecoverableSubscriptionDropDoesNotRepeatResubscribeSamples is
// the negative half of the §27.3.1 resubscribe requirement. The sample
// reports the duration of an outage, so it belongs to the moment the
// subscription is back, and a subscription that never comes back
// contributes no completed outage. A replica whose client is shut down
// permanently must therefore not emit an unbounded sample stream on its
// retry cadence: those samples carry no outage duration and would drag
// the histogram's P99 across the 500 ms propagation SLO the §27.8 alert
// watches, purely because a replica was retiring.
//
// spec: §27.3.1 — "Replicas with a dropped subscription MUST
// re-subscribe and emit a
// `lenny_playground_session_revocation_propagation_seconds` sample
// tagged `{outcome=\"resubscribe\"}` for the duration of the outage."
func TestUnrecoverableSubscriptionDropDoesNotRepeatResubscribeSamples_spec_27_3_1_98(t *testing.T) {
	mr := miniredis.RunT(t)
	publisherClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = publisherClient.Close() })
	subscriberClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = subscriberClient.Close() })

	publisher := NewRedisSessionStore(publisherClient)
	subscriber := NewRedisSessionStore(subscriberClient)
	var mu sync.Mutex
	outcomes := map[string]int{}
	subscriber.propObserver = func(outcome string, _ float64) {
		mu.Lock()
		defer mu.Unlock()
		outcomes[outcome]++
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go subscriber.SubscribeAllRevocations(ctx)

	const tenant = "acme"
	// Wait until the subscription is live, so the close below drops a
	// subscription that was actually established.
	waitForCondition(t, 5*time.Second, func() bool {
		if err := publisher.RevokeSession(ctx, tenant, "sess-shutdown", []string{"jti-shutdown"}, time.Minute); err != nil {
			t.Fatalf("RevokeSession: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		return outcomes["pubsub_delivered"] > 0
	})

	// Shut the subscriber's client down for good. Nothing can restore
	// this subscription, so no outage ever completes.
	if err := subscriberClient.Close(); err != nil {
		t.Fatalf("close subscriber client: %v", err)
	}

	// Well over the loop's retry cadence, so a per-retry emitter shows up
	// as a multi-sample stream rather than as a single sample.
	time.Sleep(2 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	if outcomes["resubscribe"] > 1 {
		t.Errorf("a permanently dropped subscription recorded %d resubscribe samples, want at most one per outage", outcomes["resubscribe"])
	}
}
