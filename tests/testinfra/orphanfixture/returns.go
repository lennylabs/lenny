// SPDX-License-Identifier: MIT

package orphanfixture

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/redisconn/slotguard"
)

// Returner is the §8.3 budget-return seam the session server calls;
// *treebudget.Reserver implements it.
type Returner interface {
	Return(ctx context.Context, r treebudget.Reservation) error
}

// ReturnCounter wraps a Returner and records each Return by its TokenDelta
// and outcome. With AssignSlices giving each member a distinct slice and no
// recorded usage, a return's TokenDelta equals its member's slice, so the
// count per TokenDelta is the count of returns that member issued.
//
// spec: §8.3 (Return on completion)
type ReturnCounter struct {
	inner Returner

	mu          sync.Mutex
	byToken     map[int64]int
	total       int
	keysExpired int
}

// NewReturnCounter wraps inner.
func NewReturnCounter(inner Returner) *ReturnCounter {
	return &ReturnCounter{inner: inner, byToken: map[int64]int{}}
}

// Return implements Returner.
func (c *ReturnCounter) Return(ctx context.Context, r treebudget.Reservation) error {
	err := c.inner.Return(ctx, r)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byToken[r.TokenDelta]++
	c.total++
	if errors.Is(err, treebudget.ErrBudgetKeysExpired) {
		c.keysExpired++
	}
	return err
}

// Calls returns the returns recorded with TokenDelta token.
func (c *ReturnCounter) Calls(token int64) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.byToken[token]
}

// Total returns every recorded return.
func (c *ReturnCounter) Total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

// KeysExpired returns the recorded returns that met an absent sentinel.
func (c *ReturnCounter) KeysExpired() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.keysExpired
}

// AssignSlices gives each delegated node a distinct granted MaxTokenBudget,
// base plus its position, and returns the slice by node id.
func AssignSlices(t testing.TB, store sessionstore.Store, tenant string, nodes []Node, base int64) map[string]int64 {
	t.Helper()
	slices := map[string]int64{}
	for i, n := range nodes {
		if n.Depth == 0 {
			continue
		}
		slice := base + int64(i)
		if _, err := store.Update(context.Background(), tenant, n.ID, func(r *sessionstore.Session) error {
			r.DelegationLease = &sessionstore.DelegationLease{MaxTokenBudget: slice}
			return nil
		}); err != nil {
			t.Fatalf("orphanfixture: assign slice to %s: %v", n.ID, err)
		}
		slices[n.ID] = slice
	}
	return slices
}

// FiredTimer is a usage quiescence timer seam whose wait has always already
// elapsed, so a test's budget returns run without real delay.
func FiredTimer(time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- time.Time{}
	return ch
}

// NewMiniReserver returns a treebudget.Reserver running the production
// reserve and return scripts on a miniredis server, with a one-hour key TTL.
func NewMiniReserver(t testing.TB) (*treebudget.Reserver, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	cl := slotguard.Install(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	t.Cleanup(func() { _ = cl.Close() })
	return treebudget.New(cl, time.Hour), mr
}

// ReserveMembers reserves each delegated node's budget under the tree root:
// one tree_size unit, the per-node tree_memory share, its parent's
// parallel_children slot and children_total unit, and its slice of tokens.
// The first reservation creates the tree.
func ReserveMembers(t testing.TB, r *treebudget.Reserver, nodes []Node, slices map[string]int64) {
	t.Helper()
	root := ""
	first := true
	const unbounded = int64(1) << 40
	for _, n := range nodes {
		if n.Depth == 0 {
			root = n.ID
			continue
		}
		if _, err := r.Reserve(context.Background(), treebudget.Reservation{
			RootSessionID: root, ParentSessionID: n.Parent,
			TreeSizeCap: unbounded, TreeSizeDelta: 1,
			TreeMemoryCap: unbounded, TreeMemoryDelta: treebudget.PerNodeMemoryBytes,
			ParallelChildrenCap: unbounded, ParallelChildrenDelta: 1,
			ChildrenTotalCap: unbounded, ChildrenTotalDelta: 1,
			TokenCap: unbounded, TokenDelta: slices[n.ID],
			CreateTree: first,
		}); err != nil {
			t.Fatalf("orphanfixture: reserve %s: %v", n.ID, err)
		}
		first = false
	}
}

// BudgetKeys returns every {root}:dlg:* key on mr with its value.
func BudgetKeys(mr *miniredis.Miniredis, root string) map[string]string {
	prefix := "{" + root + "}:dlg:"
	out := map[string]string{}
	for _, k := range mr.Keys() {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		v, _ := mr.Get(k)
		out[k] = v
	}
	return out
}

// KeyLoss names how much of a tree's budget key set a round removes before
// its detections run.
type KeyLoss int

const (
	// KeysPresent removes nothing.
	KeysPresent KeyLoss = iota
	// SentinelLost removes only the tree-wide sentinel {root}:dlg:tokens.
	SentinelLost
	// AllKeysLost removes every {root}:dlg:* key, as TTL expiry does.
	AllKeysLost
)

// LoseKeys removes the keys loss names.
func LoseKeys(mr *miniredis.Miniredis, root string, loss KeyLoss) {
	switch loss {
	case SentinelLost:
		mr.Del("{" + root + "}:dlg:tokens")
	case AllKeysLost:
		for k := range BudgetKeys(mr, root) {
			mr.Del(k)
		}
	}
}

// CheckReturns asserts the budget returns of a tree whose every delegated
// member ended through a post-terminal hook: each member issued exactly one
// return, carrying its slice; with the keys present the tree-wide counters
// end at zero; with the sentinel absent every return met
// ErrBudgetKeysExpired and the key set after the cleanup equals before, so no
// return created or modified any key.
//
// spec: §8.3 (Return on completion), §8.3 (BUDGET_KEYS_EXPIRED detection and
// tree cleanup)
func CheckReturns(t testing.TB, label string, mr *miniredis.Miniredis, root string, slices map[string]int64,
	returns *ReturnCounter, loss KeyLoss, before map[string]string,
) {
	t.Helper()
	for id, slice := range slices {
		if n := returns.Calls(slice); n != 1 {
			t.Errorf("%s: member %s issued %d budget returns, want 1", label, id, n)
		}
	}
	if n := returns.Total(); n != len(slices) {
		t.Errorf("%s: budget returns = %d, want %d", label, n, len(slices))
	}
	after := BudgetKeys(mr, root)
	if loss == KeysPresent {
		if n := returns.KeysExpired(); n != 0 {
			t.Errorf("%s: %d returns met an absent sentinel with the keys present", label, n)
		}
		for _, axis := range []string{"tree_size", "tree_memory", "tokens"} {
			if v := after["{"+root+"}:dlg:"+axis]; v != "0" {
				t.Errorf("%s: %s = %q after every member returned, want 0", label, axis, v)
			}
		}
		return
	}
	if n := returns.KeysExpired(); n != returns.Total() {
		t.Errorf("%s: %d of %d returns met ErrBudgetKeysExpired, want all", label, n, returns.Total())
	}
	if len(after) != len(before) {
		t.Errorf("%s: budget keys after the cleanup = %v, want the pre-cleanup set %v", label, after, before)
	}
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s: key %s = %q after the cleanup, want %q", label, k, after[k], v)
		}
	}
	if _, ok := after["{"+root+"}:dlg:tokens"]; ok {
		t.Errorf("%s: a return recreated the sentinel", label)
	}
}
