// SPDX-License-Identifier: MIT

package pubsub

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// waitUntil polls cond every 10 ms until it holds or timeout elapses.
func waitUntil(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", timeout, desc)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// awaitDelivery publishes payload on channel until the subscriber has
// received it, because Redis pub/sub drops a message published before the
// subscription is confirmed.
func awaitDelivery(t *testing.T, bus *Bus, channel, payload string, received func(string) bool) {
	t.Helper()
	waitUntil(t, 10*time.Second, "delivery of "+payload, func() bool {
		if received(payload) {
			return true
		}
		_ = bus.Publish(context.Background(), channel, []byte(payload))
		return false
	})
}

// payloadSet records the payloads a handler received.
type payloadSet struct {
	ch   chan string
	seen map[string]bool
}

func newPayloadSet() *payloadSet {
	return &payloadSet{ch: make(chan string, 1024), seen: map[string]bool{}}
}

func (p *payloadSet) handler(payload []byte) { p.ch <- string(payload) }

// has drains what the handler delivered and reports whether payload is
// among the payloads received so far. Only the test goroutine calls it.
func (p *payloadSet) has(payload string) bool {
	for {
		select {
		case v := <-p.ch:
			p.seen[v] = true
		default:
			return p.seen[payload]
		}
	}
}

// startBus returns a Bus over a fresh miniredis and the miniredis itself,
// so a test can drop and re-establish every connection.
func startBus(t *testing.T) (*Bus, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return New(client), mr
}

// restart drops every connection to mr and listens again on its address.
func restart(t *testing.T, mr *miniredis.Miniredis) {
	t.Helper()
	mr.Close()
	if err := mr.Restart(); err != nil {
		t.Fatalf("restart miniredis: %v", err)
	}
}

// TestSubscribeCallsOnSubscribedOnEveryConfirmation pins that the
// OnSubscribed callback runs once on the first subscription confirmation
// and once more on each confirmation go-redis receives after it
// re-establishes a dropped connection, and that messages keep reaching
// the handler after each reconnect. A replica's revocation cache
// rehydrates on that callback, so a callback that does not fire leaves a
// revocation published while no subscription was confirmed unapplied
// until the periodic rehydration.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
func TestSubscribeCallsOnSubscribedOnEveryConfirmation(t *testing.T) {
	bus, mr := startBus(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var confirmations atomic.Int32
	got := newPayloadSet()
	done := make(chan struct{})
	go func() {
		bus.Subscribe(ctx, "ch", got.handler, OnSubscribed(func() { confirmations.Add(1) }))
		close(done)
	}()

	// The confirmation precedes every message on the subscription, and the
	// callback runs on the loop goroutine before the handler sees one.
	awaitDelivery(t, bus, "ch", "before", got.has)
	if n := confirmations.Load(); n != 1 {
		t.Fatalf("OnSubscribed ran %d times on the first subscription, want 1", n)
	}

	for i, payload := range []string{"after-first", "after-second"} {
		restart(t, mr)
		want := int32(i + 2)
		waitUntil(t, 10*time.Second, "the OnSubscribed callback after a restart", func() bool {
			return confirmations.Load() >= want
		})
		awaitDelivery(t, bus, "ch", payload, got.has)
		if n := confirmations.Load(); n != want {
			t.Errorf("OnSubscribed ran %d times after %d reconnects, want %d", n, i+1, want)
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Subscribe did not return after the context was cancelled")
	}
}

// TestSubscribeCallsOnSubscribedWhenTheFirstConfirmationComesLate pins
// that a subscription started while Redis is unreachable calls the
// callback when Redis first confirms it, so a replica that starts during a
// Redis outage rereads its store as soon as the subscription is up rather
// than at its next periodic read.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
func TestSubscribeCallsOnSubscribedWhenTheFirstConfirmationComesLate(t *testing.T) {
	bus, mr := startBus(t)
	mr.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var confirmations atomic.Int32
	got := newPayloadSet()
	go bus.Subscribe(ctx, "ch", got.handler, OnSubscribed(func() { confirmations.Add(1) }))

	time.Sleep(300 * time.Millisecond)
	if n := confirmations.Load(); n != 0 {
		t.Fatalf("OnSubscribed ran %d times while Redis was unreachable, want 0", n)
	}
	if err := mr.Restart(); err != nil {
		t.Fatalf("restart miniredis: %v", err)
	}
	waitUntil(t, 10*time.Second, "the OnSubscribed callback once Redis is reachable", func() bool {
		return confirmations.Load() >= 1
	})
	awaitDelivery(t, bus, "ch", "first", got.has)
	if n := confirmations.Load(); n != 1 {
		t.Errorf("OnSubscribed ran %d times for one late confirmation, want 1", n)
	}
}

// TestSubscribeWithoutOnSubscribedSurvivesAReconnect pins that a caller
// that passes no OnSubscribed keeps receiving messages after the
// subscription is re-established, so the lenny-ops revocation set and the
// deny-list propagators, which pass none, behave as before.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
func TestSubscribeWithoutOnSubscribedSurvivesAReconnect(t *testing.T) {
	bus, mr := startBus(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	got := newPayloadSet()
	go bus.Subscribe(ctx, "ch", got.handler)
	awaitDelivery(t, bus, "ch", "before", got.has)

	restart(t, mr)
	awaitDelivery(t, bus, "ch", "after", got.has)
}

// TestNilBusSubscribeNeverCallsOnSubscribed pins that the no-Redis mode
// blocks until cancel without calling the callback, because a replica
// without Redis has no subscription to confirm.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
func TestNilBusSubscribeNeverCallsOnSubscribed(t *testing.T) {
	var b *Bus
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	called := false
	b.Subscribe(ctx, "ch", func([]byte) {}, OnSubscribed(func() { called = true }))
	if called {
		t.Error("a nil Bus called the OnSubscribed callback, want no call")
	}
}

// TestIsConfirmationAcceptsOnlyASubscribeToTheChannel pins that only a
// SUBSCRIBE confirmation for the subscribed channel calls the
// OnSubscribed callback, so an unsubscribe notice or a confirmation for another
// channel never triggers a reread. It verifies a helper and cites no
// requirement.
func TestIsConfirmationAcceptsOnlyASubscribeToTheChannel(t *testing.T) {
	cases := []struct {
		name string
		sub  redis.Subscription
		want bool
	}{
		{"subscribe to the channel", redis.Subscription{Kind: "subscribe", Channel: "ch", Count: 1}, true},
		{"unsubscribe from the channel", redis.Subscription{Kind: "unsubscribe", Channel: "ch"}, false},
		{"subscribe to another channel", redis.Subscription{Kind: "subscribe", Channel: "other", Count: 1}, false},
		{"pattern subscribe", redis.Subscription{Kind: "psubscribe", Channel: "ch", Count: 1}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isConfirmation(&tc.sub, "ch"); got != tc.want {
				t.Errorf("isConfirmation(%+v) = %v, want %v", tc.sub, got, tc.want)
			}
		})
	}
}
