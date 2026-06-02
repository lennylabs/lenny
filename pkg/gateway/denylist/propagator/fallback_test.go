// SPDX-License-Identifier: MIT

package propagator

import (
	"context"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/denylist"
)

// fakeFallback is an in-test §4.9 Postgres LISTEN/NOTIFY substitute.
type fakeFallback struct {
	mu        sync.Mutex
	published [][]byte
}

func (f *fakeFallback) Publish(_ context.Context, _ string, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, append([]byte(nil), payload...))
	return nil
}

func (f *fakeFallback) Subscribe(ctx context.Context, _ string, _ func([]byte)) { <-ctx.Done() }

func (f *fakeFallback) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.published)
}

// spec: §4.9 line 1647 — with no Redis bus, a Revoke propagates over the
// Postgres LISTEN/NOTIFY fallback. F-13.3.8.
func TestRevokeUsesFallbackWhenNoRedisBus_F1338(t *testing.T) {
	fb := &fakeFallback{}
	p := New(denylist.New(), nil, WithFallback(fb))

	p.Revoke(poolKey("pool-1", "cred-1"))

	if got := fb.count(); got != 1 {
		t.Errorf("fallback publishes = %d, want 1", got)
	}
}
