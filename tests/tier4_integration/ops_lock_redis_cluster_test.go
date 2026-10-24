// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 test: the lenny-ops Tier 2 remediation-lock store against a real
// three-master Redis Cluster. miniredis and the in-process cluster fake
// cannot prove that a real cluster accepts the store's scripts: only a real
// cluster rejects a script whose keys span hash slots with CROSSSLOT, and
// only real Redis runs the cjson re-encode the acquire script performs.
package tier4_integration_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/coordination/redisstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// crossSlotRecorder is a go-redis hook that records every reply error
// naming CROSSSLOT. The store maps a Redis error to ErrStoreUnavailable, so
// the hook keeps the cluster's own rejection visible to the assertions.
type crossSlotRecorder struct {
	mu   sync.Mutex
	errs []string
}

func (r *crossSlotRecorder) record(err error) {
	if err != nil && strings.Contains(err.Error(), "CROSSSLOT") {
		r.mu.Lock()
		r.errs = append(r.errs, err.Error())
		r.mu.Unlock()
	}
}

func (r *crossSlotRecorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.errs...)
}

func (r *crossSlotRecorder) DialHook(next redis.DialHook) redis.DialHook { return next }

func (r *crossSlotRecorder) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		r.record(cmd.Err())
		return err
	}
}

func (r *crossSlotRecorder) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		err := next(ctx, cmds)
		for _, c := range cmds {
			r.record(c.Err())
		}
		return err
	}
}

// mustLock returns a function that fails the test when a store call that
// should succeed returned an error, and otherwise returns the lock. It names
// ErrStoreUnavailable separately, because a script the cluster rejects
// surfaces as that error.
func mustLock(t *testing.T, op string) func(*coordination.Lock, error) *coordination.Lock {
	return func(lock *coordination.Lock, err error) *coordination.Lock {
		t.Helper()
		if errors.Is(err, coordination.ErrStoreUnavailable) {
			t.Fatalf("%s: store unavailable on a live cluster (a script rejected by the cluster maps here): %v", op, err)
		}
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		return lock
	}
}

// wantCode fails the test when err does not carry the §25.4 error code.
func wantCode(t *testing.T, op string, err error, code string) {
	t.Helper()
	if got := coordination.CodeOf(err); got != code {
		t.Fatalf("%s: err = %v (code %q), want %s", op, err, got, code)
	}
}

// assertSameLock fails the test when the stored record read back through Get
// differs from the lock the store returned. Times compare with Equal, since
// the stored RFC3339 string carries no monotonic reading.
func assertSameLock(t *testing.T, op string, got, want *coordination.Lock) {
	t.Helper()
	if !got.AcquiredAt.Equal(want.AcquiredAt) || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("%s: stored times %v/%v, want %v/%v", op, got.AcquiredAt, got.ExpiresAt, want.AcquiredAt, want.ExpiresAt)
	}
	g, w := *got, *want
	g.AcquiredAt, g.ExpiresAt = w.AcquiredAt, w.ExpiresAt
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("%s: stored lock after the cjson round trip = %+v, want %+v", op, g, w)
	}
}

// diagnosis: a store unavailable error or a recorded CROSSSLOT reply means a
// remediation-lock script or command names keys outside the ops:{lock}: hash
// tag, so the cluster rejects it and every Tier 2 lock operation fails
// during a Postgres outage; lock keys on more than one master mean the tag
// is missing from a key; a stored lock that differs from the returned lock
// means the acquire script's cjson re-encode dropped or changed a field; a
// stamped epoch other than the incremented value means the acquire script
// no longer reads ops:{lock}:epoch atomically; an epoch that drops after
// SetEpoch means the MAX rule is broken.
//
// spec: §25.4 (Storage Tiers, Tier Transitions, Error Codes), §12.4 (Redis
// HA and Failure Modes).
func TestRedisClusterOpsLockStore_spec_25_4(t *testing.T) {
	rc := containers.StartRedisCluster(t)
	rec := &crossSlotRecorder{}
	rc.Client.AddHook(rec)
	store := redisstore.New(rc.Client)
	ctx := context.Background()

	// Acquire, then a conflicting acquire on the same scope.
	alpha := mustLock(t, "acquire pool:alpha")(store.Acquire(ctx, coordination.LockRequest{
		Scope: "pool:alpha", Operation: "scale", AcquiredBy: "alice", OperationID: "op-1", TTLSeconds: 300,
	}, 0))
	got := mustLock(t, "get pool:alpha")(store.Get(ctx, alpha.ID))
	assertSameLock(t, "acquire", got, alpha)
	_, err := store.Acquire(ctx, coordination.LockRequest{Scope: "pool:alpha", Operation: "scale", AcquiredBy: "bob", TTLSeconds: 300}, 0)
	wantCode(t, "conflicting acquire", err, coordination.ErrCodeConflict)

	// Extend by the holder; a non-holder Extend is refused.
	ext := mustLock(t, "extend")(store.Extend(ctx, alpha.ID, 600, "alice"))
	if ext.Revision != alpha.Revision+1 {
		t.Fatalf("extend revision = %d, want %d", ext.Revision, alpha.Revision+1)
	}
	_, err = store.Extend(ctx, alpha.ID, 600, "bob")
	wantCode(t, "non-holder extend", err, coordination.ErrCodeNotOwned)
	got = mustLock(t, "get after extend")(store.Get(ctx, alpha.ID))
	assertSameLock(t, "extend", got, ext)

	// Steal by bob, then the former holder's Release is refused and bob's
	// Release frees the lock.
	stolen := mustLock(t, "steal")(store.Steal(ctx, alpha.ID, coordination.StealRequest{
		Confirm: true, Reason: "holder unresponsive", TTLSeconds: 300, AcquiredBy: "bob",
	}))
	if stolen.AcquiredBy != "bob" || stolen.StolenFrom != "alice" || stolen.Revision != ext.Revision+1 {
		t.Fatalf("steal = holder %q stolenFrom %q revision %d, want bob/alice/%d",
			stolen.AcquiredBy, stolen.StolenFrom, stolen.Revision, ext.Revision+1)
	}
	got = mustLock(t, "get after steal")(store.Get(ctx, alpha.ID))
	assertSameLock(t, "steal", got, stolen)
	wantCode(t, "former holder release", store.Release(ctx, alpha.ID, "alice"), coordination.ErrCodeNotOwned)
	if err := store.Release(ctx, alpha.ID, "bob"); err != nil {
		t.Fatalf("holder release: %v", err)
	}
	_, err = store.Get(ctx, alpha.ID)
	wantCode(t, "get after release", err, coordination.ErrCodeNotFound)

	// RemoveByID drops a lock regardless of holder, and both of its keys go.
	bravo := mustLock(t, "acquire pool:bravo")(store.Acquire(ctx, coordination.LockRequest{
		Scope: "pool:bravo", Operation: "drain", AcquiredBy: "carol", TTLSeconds: 300,
	}, 0))
	if err := store.RemoveByID(ctx, bravo.ID); err != nil {
		t.Fatalf("RemoveByID: %v", err)
	}
	_, err = store.Get(ctx, bravo.ID)
	wantCode(t, "get after RemoveByID", err, coordination.ErrCodeNotFound)
	if _, total := clusterKeysOn(t, rc.Client, "ops:*"); total != 0 {
		t.Fatalf("%d ops keys left after release and RemoveByID, want 0", total)
	}

	// IncrementEpoch, then Acquire: the stamp equals the new epoch.
	epoch, err := store.IncrementEpoch(ctx)
	if err != nil || epoch != 1 {
		t.Fatalf("IncrementEpoch = %d, %v; want 1, nil", epoch, err)
	}
	stamped := mustLock(t, "acquire after IncrementEpoch")(store.Acquire(ctx, coordination.LockRequest{
		Scope: "pool:charlie", Operation: "scale", AcquiredBy: "dave", TTLSeconds: 300,
	}, 0))
	if stamped.Epoch != epoch {
		t.Fatalf("acquired lock epoch = %d, want %d", stamped.Epoch, epoch)
	}
	got = mustLock(t, "get stamped")(store.Get(ctx, stamped.ID))
	assertSameLock(t, "stamped acquire", got, stamped)

	// List: scopes that collide with the key namespaces or carry braces
	// still list completely and in scope order.
	scopes := []string{"pool:charlie", "epoch", "id:x", "pool:{evil}", "cluster"}
	for _, s := range scopes[1:] {
		mustLock(t, "acquire "+s)(store.Acquire(ctx, coordination.LockRequest{
			Scope: s, Operation: "scale", AcquiredBy: "erin", TTLSeconds: 300,
		}, 0))
	}
	locks, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var listed []string
	for _, l := range locks {
		listed = append(listed, l.Scope)
	}
	want := []string{"cluster", "epoch", "id:x", "pool:charlie", "pool:{evil}"}
	if !reflect.DeepEqual(listed, want) {
		t.Fatalf("List scopes = %v, want %v", listed, want)
	}

	// Every lock key, ID key, and the epoch key sit on one master.
	masters, total := clusterKeysOn(t, rc.Client, "ops:*")
	if masters != 1 || total != 2*len(scopes)+1 {
		t.Fatalf("ops keys = %d on %d masters, want %d on 1", total, masters, 2*len(scopes)+1)
	}
	if v, err := store.Epoch(ctx); err != nil || v != epoch {
		t.Fatalf("Epoch after acquiring scope epoch = %d, %v; want %d, nil", v, err, epoch)
	}

	// SetEpoch keeps the maximum: a lower value leaves the epoch, a higher
	// one replaces it, and the next acquire stamps the higher value.
	if err := store.SetEpoch(ctx, 0); err != nil {
		t.Fatalf("SetEpoch lower: %v", err)
	}
	if v, err := store.Epoch(ctx); err != nil || v != epoch {
		t.Fatalf("Epoch after lower SetEpoch = %d, %v; want %d, nil", v, err, epoch)
	}
	if err := store.SetEpoch(ctx, 7); err != nil {
		t.Fatalf("SetEpoch higher: %v", err)
	}
	if v, err := store.Epoch(ctx); err != nil || v != 7 {
		t.Fatalf("Epoch after higher SetEpoch = %d, %v; want 7, nil", v, err)
	}
	delta := mustLock(t, "acquire after SetEpoch")(store.Acquire(ctx, coordination.LockRequest{
		Scope: "pool:delta", Operation: "scale", AcquiredBy: "frank", TTLSeconds: 300,
	}, 0))
	if delta.Epoch != 7 {
		t.Fatalf("acquired lock epoch after SetEpoch = %d, want 7", delta.Epoch)
	}

	if errs := rec.seen(); len(errs) > 0 {
		t.Fatalf("cluster returned CROSSSLOT %d times: %v", len(errs), errs)
	}
}
