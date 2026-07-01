//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §11.2 storage-quota counter, exercising the
// Redis-backed pkg/gateway/storagequota/redisstore against a real
// Redis container. Covers the reserve/reject path, release-and-clamp,
// and the atomicity guarantee under concurrent reservations.
package storagequota_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/quota/storagequota"
	"github.com/lennylabs/lenny/pkg/gateway/quota/storagequota/redisstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

func tenantID(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return "t-" + hex.EncodeToString(b[:])
}

// spec: 11.2
// diagnosis: the Redis-backed storage-quota counter in
// pkg/gateway/storagequota/redisstore did not behave as specified.
// Reserve must accumulate and report prior usage, an over-quota
// reserve must be rejected and leave the counter intact, Adjust must
// release and clamp at zero, and concurrent reservations must never
// exceed the quota.
func TestStorageQuotaCounterContract(t *testing.T) {
	t.Parallel()
	rd := containers.StartRedis(t, containers.RedisOptions{})
	counter := redisstore.New(rd.Client)
	ctx := context.Background()

	t.Run("reserve accumulates and reports prior usage", func(t *testing.T) {
		tn := tenantID(t)
		if prior, err := counter.Reserve(ctx, tn, 300, 1000); err != nil || prior != 0 {
			t.Fatalf("first reserve: prior=%d err=%v, want 0/nil", prior, err)
		}
		prior, err := counter.Reserve(ctx, tn, 200, 1000)
		if err != nil || prior != 300 {
			t.Fatalf("second reserve: prior=%d err=%v, want 300/nil", prior, err)
		}
		if used, _ := counter.Used(ctx, tn); used != 500 {
			t.Errorf("Used: got %d, want 500", used)
		}
	})

	t.Run("reserve rejects over quota and leaves the counter intact", func(t *testing.T) {
		tn := tenantID(t)
		if _, err := counter.Reserve(ctx, tn, 700, 1000); err != nil {
			t.Fatalf("Reserve: %v", err)
		}
		if _, err := counter.Reserve(ctx, tn, 400, 1000); !errors.Is(err, storagequota.ErrQuotaExceeded) {
			t.Fatalf("over-quota reserve: got %v, want ErrQuotaExceeded", err)
		}
		if used, _ := counter.Used(ctx, tn); used != 700 {
			t.Errorf("a rejected reserve must not change the counter: got %d, want 700", used)
		}
	})

	t.Run("adjust releases and clamps at zero", func(t *testing.T) {
		tn := tenantID(t)
		if _, err := counter.Reserve(ctx, tn, 600, 1000); err != nil {
			t.Fatalf("Reserve: %v", err)
		}
		if err := counter.Adjust(ctx, tn, -200); err != nil {
			t.Fatalf("Adjust: %v", err)
		}
		if used, _ := counter.Used(ctx, tn); used != 400 {
			t.Errorf("Used after release: got %d, want 400", used)
		}
		if err := counter.Adjust(ctx, tn, -9999); err != nil {
			t.Fatalf("Adjust: %v", err)
		}
		if used, _ := counter.Used(ctx, tn); used != 0 {
			t.Errorf("Used after clamping release: got %d, want 0", used)
		}
	})

	t.Run("concurrent reservations never exceed the quota", func(t *testing.T) {
		tn := tenantID(t)
		// 20 reservations of 100 bytes each against a 1000-byte quota:
		// the atomic Lua reserve must admit exactly 10.
		const reservers, each, limit = 20, 100, 1000
		var admitted int64
		var wg sync.WaitGroup
		for i := 0; i < reservers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := counter.Reserve(ctx, tn, each, limit); err == nil {
					atomic.AddInt64(&admitted, 1)
				}
			}()
		}
		wg.Wait()
		if admitted != limit/each {
			t.Errorf("admitted %d reservations, want %d", admitted, limit/each)
		}
		used, _ := counter.Used(ctx, tn)
		if used != admitted*each {
			t.Errorf("Used %d does not match admitted %d", used, admitted*each)
		}
		if used > limit {
			t.Errorf("counter %d exceeded the quota %d — reservation is not atomic", used, limit)
		}
	})
}
