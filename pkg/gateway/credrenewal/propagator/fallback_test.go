// SPDX-License-Identifier: MIT

package propagator

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/pubsub"
)

// fakeFallback is an in-test §4.9 Postgres LISTEN/NOTIFY substitute. It
// records what was published and lets the test deliver a payload to the
// subscribed handler.
type fakeFallback struct {
	mu        sync.Mutex
	published [][]byte
	handler   func([]byte)
	pubErr    error
}

func (f *fakeFallback) Publish(_ context.Context, _ string, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pubErr != nil {
		return f.pubErr
	}
	f.published = append(f.published, append([]byte(nil), payload...))
	return nil
}

func (f *fakeFallback) Subscribe(ctx context.Context, _ string, handler func([]byte)) {
	f.mu.Lock()
	f.handler = handler
	f.mu.Unlock()
	<-ctx.Done()
}

func (f *fakeFallback) deliver(payload []byte) {
	f.mu.Lock()
	h := f.handler
	f.mu.Unlock()
	if h != nil {
		h(payload)
	}
}

func (f *fakeFallback) publishCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.published)
}

func (f *fakeFallback) waitSubscribed(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		ok := f.handler != nil
		f.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("fallback Subscribe was never invoked by Run")
}

// spec: §4.9 line 1647 — with no Redis bus configured, a Revoke
// propagates over the Postgres LISTEN/NOTIFY fallback. F-13.3.8.
func TestRevokeUsesFallbackWhenNoRedisBus_F1338(t *testing.T) {
	fb := &fakeFallback{}
	p := New(denylist.New(), &fakeRevoker{}, nil, WithFallback(fb))

	p.Revoke(poolKey("pool-1", "cred-1"))

	if got := fb.publishCount(); got != 1 {
		t.Errorf("fallback publishes = %d, want 1 (no Redis bus → fallback carries the revocation)", got)
	}
}

// spec: §4.9 line 1647 — when the Redis publish fails (Redis down), the
// revocation falls through to the Postgres LISTEN/NOTIFY fallback.
// F-13.3.8.
func TestRevokeFallsBackWhenRedisPublishFails_F1338(t *testing.T) {
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	bus := pubsub.New(cl)
	// Close Redis so the publish errors.
	mr.Close()

	fb := &fakeFallback{}
	p := New(denylist.New(), &fakeRevoker{}, bus, WithFallback(fb))

	p.Revoke(poolKey("pool-1", "cred-1"))

	if got := fb.publishCount(); got != 1 {
		t.Errorf("fallback publishes = %d, want 1 (Redis down → fallback carries the revocation)", got)
	}
}

// When Redis is healthy the fallback is not used: it is a fallback, not a
// second always-on transport. F-13.3.8.
func TestRevokeSkipsFallbackWhenRedisHealthy_F1338(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	bus := pubsub.New(cl)

	fb := &fakeFallback{}
	p := New(denylist.New(), &fakeRevoker{}, bus, WithFallback(fb))

	p.Revoke(poolKey("pool-1", "cred-1"))

	if got := fb.publishCount(); got != 0 {
		t.Errorf("fallback publishes = %d, want 0 (Redis healthy → no fallback publish)", got)
	}
}

// spec: §4.9 line 1647 — Run subscribes on the Postgres fallback channel
// too, so a peer's revocation raised over Postgres converges on this
// replica even while Redis is down. F-13.3.8.
func TestRunSubscribesFallbackAndConverges_F1338(t *testing.T) {
	fb := &fakeFallback{}
	denyB := denylist.New()
	replicaB := New(denyB, &fakeRevoker{}, nil, WithFallback(fb))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		replicaB.Run(ctx)
		close(done)
	}()
	fb.waitSubscribed(t)

	key := poolKey("claude-prod", "key-2")
	fb.deliver(mustEncode(t, key))

	deadline := time.Now().Add(time.Second)
	for !denyB.Revoked(key) {
		if time.Now().After(deadline) {
			t.Fatal("replica B did not converge on the fallback-delivered revocation")
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
