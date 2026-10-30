// SPDX-License-Identifier: MIT

package backup

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// spec: §25.11 backup IDs are opaque but unique. The randomID fallback
// (taken only when crypto/rand fails) must produce a unique value for
// every call even within the same nanosecond — the previous hex-encoded
// RFC3339Nano fallback could collide on two near-simultaneous backups.
func TestRandomIDFallbackIsCollisionFree_spec_25_11(t *testing.T) {
	// Reset the package-local counter so the assertion is independent of
	// any earlier-test-induced state.
	atomic.StoreUint64(&randomIDFallbackCounter, 0)

	const n = 1000
	const sameNanos int64 = 1_700_000_000_000_000_000

	seen := make(map[string]struct{}, n)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := randomIDFallback("bkp", sameNanos)
			mu.Lock()
			seen[id] = struct{}{}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if got, want := len(seen), n; got != want {
		t.Fatalf("randomIDFallback collided: %d unique ids at the same nanosecond, want %d", got, want)
	}
}

// spec: §25.11 — happy-path randomID still emits a stable prefix and
// 16 hex characters of crypto-grade entropy.
func TestRandomIDHappyPathShape_spec_25_11(t *testing.T) {
	id := randomID("bkp")
	if !strings.HasPrefix(id, "bkp-") {
		t.Errorf("randomID id %q missing bkp- prefix", id)
	}
	rest := strings.TrimPrefix(id, "bkp-")
	if len(rest) != 16 {
		t.Errorf("randomID body %q is %d bytes, want 16", rest, len(rest))
	}
}
