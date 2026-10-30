// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local coverage for the §8.3 delegation budget sentinel. A
// randomized interleaving of first-reservation (marked) reserves, unmarked
// reserves, returns, and sentinel deletions runs against the reserve and
// return scripts without advancing any TTL. No unmarked script run may create
// or increment a counter while the tree-wide sentinel is absent.
//
// spec: §8.3 (Defense-in-depth TTL), §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup)
package tier7a_load_local_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treebudget"
)

const sentinelRoot = "root-sentinel"

func sentinelReservation(parent string, tokens int64, create bool) treebudget.Reservation {
	return treebudget.Reservation{
		RootSessionID: sentinelRoot, ParentSessionID: parent,
		TreeSizeDelta: 1, TreeMemoryDelta: treebudget.PerNodeMemoryBytes,
		ParallelChildrenDelta: 1, ChildrenTotalDelta: 1,
		TokenDelta: tokens, CreateTree: create,
	}
}

// keyValues snapshots every key and its value.
func keyValues(mr *miniredis.Miniredis) map[string]string {
	out := map[string]string{}
	for _, k := range mr.Keys() {
		v, _ := mr.Get(k)
		out[k] = v
	}
	return out
}

// spec: §8.3 (Defense-in-depth TTL), §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup)
// diagnosis: an unmarked reserve or a return changed a key while the tokens
// sentinel was absent. The scripts must return BUDGET_KEYS_EXPIRED without
// writing; a write here means an expired tree's counters restart silently,
// or a return recreates partial keys. A marked reserve that failed to admit
// means the first-reservation create does not recreate the sentinel.
func TestBudgetSentinelRandomizedInterleaving_spec_8_3(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	r := treebudget.New(cl, time.Hour)
	ctx := context.Background()
	parents := []string{"p1", "p2", "p3"}

	for step := 0; step < 2000; step++ {
		parent := parents[rng.Intn(len(parents))]
		tokens := int64(rng.Intn(3)) * 50
		absent := !mr.Exists("{" + sentinelRoot + "}:dlg:tokens")
		before := keyValues(mr)
		var err error
		op := rng.Intn(5)
		switch op {
		case 0:
			_, err = r.Reserve(ctx, sentinelReservation(parent, tokens, true))
			if err != nil {
				t.Fatalf("seed %d step %d: marked reserve: %v", seed, step, err)
			}
			continue
		case 1, 2:
			_, err = r.Reserve(ctx, sentinelReservation(parent, tokens, false))
		case 3:
			err = r.Return(ctx, sentinelReservation(parent, tokens, false))
		case 4:
			mr.Del("{" + sentinelRoot + "}:dlg:tokens")
			continue
		}
		if !absent {
			if err != nil {
				t.Fatalf("seed %d step %d: op %d on a live tree: %v", seed, step, op, err)
			}
			continue
		}
		if !errors.Is(err, treebudget.ErrBudgetKeysExpired) {
			t.Fatalf("seed %d step %d: op %d with the sentinel absent err = %v, want ErrBudgetKeysExpired", seed, step, op, err)
		}
		if after := keyValues(mr); !reflect.DeepEqual(after, before) {
			t.Fatalf("seed %d step %d: op %d wrote with the sentinel absent: %v -> %v", seed, step, op, before, after)
		}
	}

	// Concurrent unmarked runs against an absent sentinel write nothing.
	mr.FlushAll()
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			res := sentinelReservation(fmt.Sprintf("p%d", g%3), 50, false)
			for i := 0; i < 50; i++ {
				if i%2 == 0 {
					_, _ = r.Reserve(ctx, res)
				} else {
					_ = r.Return(ctx, res)
				}
			}
		}(g)
	}
	wg.Wait()
	if keys := mr.Keys(); len(keys) != 0 {
		t.Fatalf("concurrent unmarked runs wrote %v with the sentinel absent", keys)
	}
}
