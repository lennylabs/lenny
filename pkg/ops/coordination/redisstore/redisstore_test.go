// SPDX-License-Identifier: MIT

package redisstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/coordination/redisstore"
	"github.com/lennylabs/lenny/pkg/redisconn/clustertest"
	"github.com/lennylabs/lenny/pkg/redisconn/slotguard"
)

// The Tier 2 key layout the store writes (§25.4 Storage Tiers).
const (
	scopeKeyPrefix = "ops:{lock}:scope:"
	idKeyPrefix    = "ops:{lock}:id:"
	epochKey       = "ops:{lock}:epoch"
)

// fixture is a store under test plus the handles a test needs to observe
// and perturb it: the store's own guarded client (where test hooks go), an
// unhooked raw client on the server that owns the lock slot, a second store
// over that raw client for injected operations, and the servers for clock
// control.
type fixture struct {
	store      *redisstore.Store
	client     redis.UniversalClient
	raw        *redis.Client
	side       *redisstore.Store
	lockServer *miniredis.Miniredis
	servers    []*miniredis.Miniredis
}

// newStore builds the store over a single miniredis whose client carries
// slotguard.Hook, so every script and multi-key command the store issues
// must stay in one hash slot, as on a Redis Cluster.
func newStore(t *testing.T) *fixture {
	t.Helper()
	mr := miniredis.RunT(t)
	cl := slotguard.Install(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	t.Cleanup(func() { _ = cl.Close() })
	return finishFixture(t, cl, mr, []*miniredis.Miniredis{mr})
}

// newClusterStore builds the store over an in-process three-master Redis
// Cluster (clustertest installs slotguard.Hook on its client).
func newClusterStore(t *testing.T) *fixture {
	t.Helper()
	c := clustertest.New(t, 3)
	return finishFixture(t, c.Client, c.ServerFor(epochKey), c.Servers)
}

func finishFixture(t *testing.T, cl redis.UniversalClient, lockServer *miniredis.Miniredis, servers []*miniredis.Miniredis) *fixture {
	t.Helper()
	raw := redis.NewClient(&redis.Options{Addr: lockServer.Addr()})
	t.Cleanup(func() { _ = raw.Close() })
	return &fixture{
		store:      redisstore.New(cl),
		client:     cl,
		raw:        raw,
		side:       redisstore.New(raw),
		lockServer: lockServer,
		servers:    servers,
	}
}

// setTime pins the TIME reply of every server, since a keyless TIME may be
// answered by any primary.
func (f *fixture) setTime(at time.Time) {
	for _, s := range f.servers {
		s.SetTime(at)
	}
}

// backends are the two topologies every operation test runs over.
var backends = []struct {
	name  string
	build func(*testing.T) *fixture
}{
	{"miniredis", newStore},
	{"cluster", newClusterStore},
}

func forEachBackend(t *testing.T, fn func(t *testing.T, f *fixture)) {
	t.Helper()
	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) { fn(t, b.build(t)) })
	}
}

// hookFunc is a go-redis ProcessHook body with access to next.
type hookFunc func(ctx context.Context, cmd redis.Cmder, next redis.ProcessHook) error

type processHook struct{ fn hookFunc }

func (h processHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h processHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error { return h.fn(ctx, cmd, next) }
}

func (h processHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (f *fixture) addHook(fn hookFunc) { f.client.AddHook(processHook{fn: fn}) }

func isScript(cmd redis.Cmder) bool {
	n := cmd.Name()
	return n == "evalsha" || n == "eval"
}

// scriptArg returns ARGV[i] (1-based) of an EVAL or EVALSHA command.
func scriptArg(cmd redis.Cmder, i int) string {
	args := cmd.Args()
	n, _ := args[2].(int)
	switch v := args[2+n+i].(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprint(v)
	}
}

// injectBeforeScripts runs inject immediately before the first script
// command of the next operation (every one when every is set).
func (f *fixture) injectBeforeScripts(every bool, inject func(cmd redis.Cmder)) {
	var fired atomic.Bool
	f.addHook(func(ctx context.Context, cmd redis.Cmder, next redis.ProcessHook) error {
		if isScript(cmd) && (every || fired.CompareAndSwap(false, true)) {
			inject(cmd)
		}
		return next(ctx, cmd)
	})
}

// replayFirstWrite makes the client send the operation's first write
// command (the first script command that does not fail with NOSCRIPT, or
// an INCR) twice and return the second reply, as go-redis does when it
// loses a reply. between runs between the two sends.
func (f *fixture) replayFirstWrite(between func(cmd redis.Cmder)) {
	var done atomic.Bool
	f.addHook(func(ctx context.Context, cmd redis.Cmder, next redis.ProcessHook) error {
		if done.Load() || (!isScript(cmd) && cmd.Name() != "incr") {
			return next(ctx, cmd)
		}
		if err := next(ctx, cmd); err != nil && strings.HasPrefix(err.Error(), "NOSCRIPT") {
			return err
		}
		done.Store(true)
		if between != nil {
			between(cmd)
		}
		cmd.SetErr(nil)
		return next(ctx, cmd)
	})
}

// failFirst replaces the first command named name with a connection error.
func (f *fixture) failFirst(name string) {
	var fired atomic.Bool
	f.addHook(func(ctx context.Context, cmd redis.Cmder, next redis.ProcessHook) error {
		if cmd.Name() == name && fired.CompareAndSwap(false, true) {
			err := &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}
			cmd.SetErr(err)
			return err
		}
		return next(ctx, cmd)
	})
}

func acquire(t *testing.T, s *redisstore.Store, scope, by string, ttl int) *coordination.Lock {
	t.Helper()
	lock, err := s.Acquire(context.Background(), coordination.LockRequest{
		Scope: scope, Operation: "scale", AcquiredBy: by, TTLSeconds: ttl,
	}, 0)
	if err != nil {
		t.Fatalf("acquire %s: %v", scope, err)
	}
	return lock
}

func steal(t *testing.T, s *redisstore.Store, id, by string, ttl int) *coordination.Lock {
	t.Helper()
	lock, err := s.Steal(context.Background(), id, coordination.StealRequest{Confirm: true, AcquiredBy: by, TTLSeconds: ttl})
	if err != nil {
		t.Fatalf("steal %s for %s: %v", id, by, err)
	}
	return lock
}

// canon renders a lock as Go's JSON, so two locks compare field by field.
func canon(t testing.TB, l *coordination.Lock) string {
	t.Helper()
	b, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// stored decodes the record at the scope key through the raw client.
func (f *fixture) stored(t *testing.T, scope string) *coordination.Lock {
	t.Helper()
	raw, err := f.raw.HGet(context.Background(), scopeKeyPrefix+scope, "data").Result()
	if errors.Is(err, redis.Nil) {
		return nil
	}
	if err != nil {
		t.Fatalf("hget %s: %v", scope, err)
	}
	var l coordination.Lock
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		t.Fatalf("decode stored %s: %v (%s)", scope, err, raw)
	}
	return &l
}

func (f *fixture) keyCount(t *testing.T, keys ...string) int64 {
	t.Helper()
	n, err := f.raw.Exists(context.Background(), keys...).Result()
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	return n
}

func wantCode(t *testing.T, what string, err error, code string) {
	t.Helper()
	if coordination.CodeOf(err) != code {
		t.Fatalf("%s err = %v, want %s", what, err, code)
	}
	if errors.Is(err, coordination.ErrStoreUnavailable) {
		t.Fatalf("%s err = %v maps to ErrStoreUnavailable", what, err)
	}
}

// spec: 25.4 (Storage Tiers, Error Codes) — the Tier 2 acquire conflicts on
// an existing scope key with REMEDIATION_LOCK_CONFLICT.
func TestRedisAcquireAndConflict_spec_25_4(t *testing.T) {
	forEachBackend(t, func(t *testing.T, f *fixture) {
		s := f.store
		if s.Tier() != coordination.StoreRedis {
			t.Fatalf("tier = %q, want redis", s.Tier())
		}
		lock := acquire(t, s, "pool:p", "alice", 300)
		if lock.LockStore != coordination.StoreRedis {
			t.Errorf("lockStore = %q, want redis", lock.LockStore)
		}
		_, err := s.Acquire(context.Background(), coordination.LockRequest{Scope: "pool:p", AcquiredBy: "bob"}, 0)
		wantCode(t, "second acquire", err, coordination.ErrCodeConflict)
	})
}

// spec: 25.4 (Storage Tiers) — Get by id and List by scope.
func TestRedisGetAndList_spec_25_4(t *testing.T) {
	forEachBackend(t, func(t *testing.T, f *fixture) {
		s := f.store
		a := acquire(t, s, "pool:b", "alice", 300)
		acquire(t, s, "pool:a", "bob", 300)

		got, err := s.Get(context.Background(), a.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Scope != "pool:b" || got.AcquiredBy != "alice" {
			t.Errorf("get = %+v, want pool:b/alice", got)
		}
		_, err = s.Get(context.Background(), "lock-missing")
		wantCode(t, "get missing", err, coordination.ErrCodeNotFound)
		locks, err := s.List(context.Background())
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(locks) != 2 || locks[0].Scope != "pool:a" || locks[1].Scope != "pool:b" {
			t.Errorf("list = %+v, want pool:a,pool:b ordered", locks)
		}
	})
}

// spec: 25.4 (Storage Tiers) — identity-based Release and Extend: the
// caller must equal acquiredBy.
func TestRedisReleaseExtendIdentity_spec_25_4(t *testing.T) {
	forEachBackend(t, func(t *testing.T, f *fixture) {
		s := f.store
		ctx := context.Background()
		lock := acquire(t, s, "pool:p", "alice", 300)

		wantCode(t, "release by non-owner", s.Release(ctx, lock.ID, "bob"), coordination.ErrCodeNotOwned)
		_, err := s.Extend(ctx, lock.ID, 600, "bob")
		wantCode(t, "extend by non-owner", err, coordination.ErrCodeNotOwned)
		ext, err := s.Extend(ctx, lock.ID, 600, "alice")
		if err != nil {
			t.Fatalf("extend: %v", err)
		}
		if ext.Revision != 1 {
			t.Errorf("revision after extend = %d, want 1", ext.Revision)
		}
		if err := s.Release(ctx, lock.ID, "alice"); err != nil {
			t.Fatalf("release by owner: %v", err)
		}
		_, err = s.Get(ctx, lock.ID)
		wantCode(t, "get after release", err, coordination.ErrCodeNotFound)
		if n := f.keyCount(t, scopeKeyPrefix+"pool:p", idKeyPrefix+lock.ID); n != 0 {
			t.Errorf("keys left after release = %d, want 0", n)
		}
	})
}

// spec: 25.4 (Storage Tiers, Tier Transitions) — RemoveByID drops a losing
// split-brain Redis lock regardless of holder; a missing id is a no-op.
func TestRedisRemoveByID_spec_25_4(t *testing.T) {
	forEachBackend(t, func(t *testing.T, f *fixture) {
		s := f.store
		ctx := context.Background()
		lock := acquire(t, s, "pool:p", "bob", 300)

		if err := s.RemoveByID(ctx, "lock-unknown"); err != nil {
			t.Fatalf("remove unknown id: %v", err)
		}
		if _, err := s.Get(ctx, lock.ID); err != nil {
			t.Fatalf("live lock disturbed by unrelated remove: %v", err)
		}
		if err := s.RemoveByID(ctx, lock.ID); err != nil {
			t.Fatalf("remove by id: %v", err)
		}
		_, err := s.Get(ctx, lock.ID)
		wantCode(t, "get after remove", err, coordination.ErrCodeNotFound)
		if again := acquire(t, s, "pool:p", "carol", 300); again.AcquiredBy != "carol" {
			t.Errorf("re-acquire after remove = %+v, want carol", again)
		}
	})
}

// spec: 25.4 (Storage Tiers) — Steal records the prior holder and bumps the
// revision.
func TestRedisSteal_spec_25_4(t *testing.T) {
	forEachBackend(t, func(t *testing.T, f *fixture) {
		lock := acquire(t, f.store, "pool:p", "alice", 300)
		stolen := steal(t, f.store, lock.ID, "bob", 300)
		if stolen.AcquiredBy != "bob" || stolen.StolenFrom != "alice" {
			t.Errorf("steal = %+v, want acquiredBy=bob stolenFrom=alice", stolen)
		}
		if stolen.Revision != 1 {
			t.Errorf("revision = %d, want 1", stolen.Revision)
		}
		if got := f.stored(t, "pool:p"); canon(t, got) != canon(t, stolen) {
			t.Errorf("stored = %s, want %s", canon(t, got), canon(t, stolen))
		}
	})
}

// spec: 25.4 (Tier Transitions) — the outage epoch counter: increment
// advances it, SetEpoch applies the MAX (never backward), and a Tier 2
// acquire stamps the current value.
func TestRedisEpochAndStamp_spec_25_4(t *testing.T) {
	forEachBackend(t, func(t *testing.T, f *fixture) {
		s := f.store
		ctx := context.Background()
		if e, _ := s.Epoch(ctx); e != 0 {
			t.Fatalf("initial epoch = %d, want 0", e)
		}
		if l := acquire(t, s, "pool:z", "alice", 300); l.Epoch != 0 {
			t.Errorf("epoch with absent epoch key = %d, want 0", l.Epoch)
		}
		if e, err := s.IncrementEpoch(ctx); err != nil || e != 1 {
			t.Fatalf("increment = %d (%v), want 1", e, err)
		}
		if err := s.SetEpoch(ctx, 5); err != nil {
			t.Fatalf("set epoch 5: %v", err)
		}
		if err := s.SetEpoch(ctx, 3); err != nil { // lower: ignored
			t.Fatalf("set epoch 3: %v", err)
		}
		if e, _ := s.Epoch(ctx); e != 5 {
			t.Errorf("epoch = %d, want 5 (MAX, never backward)", e)
		}
		lock := acquire(t, s, "pool:p", "alice", 300)
		if lock.Epoch != 5 {
			t.Errorf("acquired lock epoch = %d, want 5 (stamped from ops:{lock}:epoch)", lock.Epoch)
		}
		if got := f.stored(t, "pool:p"); got.Epoch != 5 {
			t.Errorf("stored lock epoch = %d, want 5", got.Epoch)
		}
	})
}

// spec: 25.4 (Storage Tiers) — the key-level PTTL expires the lock
// natively: after the TTL elapses the scope is free again.
func TestRedisTTLExpiry_spec_25_4(t *testing.T) {
	forEachBackend(t, func(t *testing.T, f *fixture) {
		acquire(t, f.store, "pool:p", "alice", 5)
		f.lockServer.FastForward(6 * time.Second)
		if locks, err := f.store.List(context.Background()); err != nil || len(locks) != 0 {
			t.Fatalf("list after expiry = %v (%v), want empty", locks, err)
		}
		if _, err := f.store.Acquire(context.Background(), coordination.LockRequest{Scope: "pool:p", AcquiredBy: "bob"}, 0); err != nil {
			t.Fatalf("re-acquire after expiry: %v", err)
		}
	})
}

// spec: 25.4 (Tier Transitions) — ActiveLocks is the reconciliation
// snapshot source.
func TestRedisActiveLocks_spec_25_4(t *testing.T) {
	forEachBackend(t, func(t *testing.T, f *fixture) {
		acquire(t, f.store, "pool:p", "alice", 300)
		locks, err := f.store.ActiveLocks(context.Background())
		if err != nil || len(locks) != 1 {
			t.Fatalf("active locks = %v (%v), want 1", locks, err)
		}
	})
}

// TestRedisServerTime_spec_25_4 asserts the exported ServerTime reads the
// Redis TIME clock the Postgres-Redis clock-skew sampler compares against
// Postgres now(). It is the same source acquiredAt/expiresAt are authored
// from, so the monitored skew is the skew the lease path is exposed to.
//
// spec: 25.4 (Clock Source).
func TestRedisServerTime_spec_25_4(t *testing.T) {
	forEachBackend(t, func(t *testing.T, f *fixture) {
		want := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
		f.setTime(want)
		got, err := f.store.ServerTime(context.Background())
		if err != nil {
			t.Fatalf("ServerTime: %v", err)
		}
		// miniredis TIME has second granularity in this mode; assert the
		// read reflects the injected clock to the second.
		if got.Unix() != want.Unix() {
			t.Errorf("ServerTime = %v (unix %d), want %v (unix %d)", got, got.Unix(), want, want.Unix())
		}
		if got.Location() != time.UTC {
			t.Errorf("ServerTime location = %v, want UTC", got.Location())
		}
	})
}

// TestRedisStoreClientInstallsSlotGuard_spec_12_4 is the negative control
// for the slot guard: an EVAL through the client newStore builds, over a
// lock key and an untagged key, fails with slotguard.ErrCrossSlot, which
// proves the guard is wired on this package's client.
//
// spec: 25.4 (Storage Tiers), 12.4 (Redis HA and Failure Modes).
func TestRedisStoreClientInstallsSlotGuard_spec_12_4(t *testing.T) {
	f := newStore(t)
	err := f.client.Eval(context.Background(), "return 1", []string{scopeKeyPrefix + "x", "untagged"}).Err()
	if !errors.Is(err, slotguard.ErrCrossSlot) {
		t.Fatalf("cross-slot eval err = %v, want slotguard.ErrCrossSlot", err)
	}
}

// TestRedisLockKeysShareOneSlot_spec_25_4 asserts the scope key, the ID
// key, and the epoch key hash to one Redis Cluster slot for any scope,
// including scopes that imitate the epoch key, an ID key, or carry their
// own braces, and that a scope named "epoch" cannot overwrite the epoch key.
//
// spec: 25.4 (Storage Tiers), 12.4 (Redis HA and Failure Modes).
func TestRedisLockKeysShareOneSlot_spec_25_4(t *testing.T) {
	want := slotguard.Slot(epochKey)
	for _, scope := range []string{"epoch", "id:x", "pool:{evil}", "pool:p", ""} {
		for _, k := range []string{scopeKeyPrefix + scope, idKeyPrefix + "lock-" + scope} {
			if got := slotguard.Slot(k); got != want {
				t.Errorf("slot(%q) = %d, want %d (the epoch key's slot)", k, got, want)
			}
		}
	}
	forEachBackend(t, func(t *testing.T, f *fixture) {
		ctx := context.Background()
		if e, err := f.store.IncrementEpoch(ctx); err != nil || e != 1 {
			t.Fatalf("increment = %d (%v), want 1", e, err)
		}
		l := acquire(t, f.store, "epoch", "alice", 300)
		acquire(t, f.store, "id:"+l.ID, "bob", 300)
		acquire(t, f.store, "pool:{evil}", "carol", 300)
		if l.Epoch != 1 {
			t.Errorf("stamped epoch = %d, want 1", l.Epoch)
		}
		typ, err := f.raw.Type(ctx, epochKey).Result()
		if err != nil || typ != "string" {
			t.Fatalf("epoch key type = %q (%v), want string", typ, err)
		}
		if e, err := f.store.IncrementEpoch(ctx); err != nil || e != 2 {
			t.Fatalf("increment after acquiring scope epoch = %d (%v), want 2", e, err)
		}
		if got, err := f.store.Get(ctx, l.ID); err != nil || got.AcquiredBy != "alice" {
			t.Fatalf("get scope epoch lock = %+v (%v), want alice", got, err)
		}
		for _, srv := range f.servers {
			if srv == f.lockServer {
				continue
			}
			if keys := srv.Keys(); len(keys) != 0 {
				t.Errorf("server %s holds %v, want every lock key on the lock slot's server", srv.Addr(), keys)
			}
		}
	})
}

// TestRedisListClusterCompleteness_spec_25_4 asserts that on a Redis
// Cluster every lock acquired through the store sits in one slot and that
// List returns all of them ordered by scope.
// TestRedisScanRoutesByNode_spec_12_4 pins that the scan itself runs on
// every node, which keeps the listing complete while the slot migrates.
//
// spec: 25.4 (Storage Tiers), 12.4 (Redis HA and Failure Modes).
func TestRedisListClusterCompleteness_spec_25_4(t *testing.T) {
	f := newClusterStore(t)
	ctx := context.Background()
	const n = 20
	for i := 0; i < n; i++ {
		acquire(t, f.store, fmt.Sprintf("pool:p%02d", i), "alice", 300)
	}
	if got := len(f.lockServer.Keys()); got != 2*n {
		t.Fatalf("lock slot server holds %d keys, want %d (scope and ID key per lock)", got, 2*n)
	}
	locks, err := f.store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(locks) != n {
		t.Fatalf("list = %d locks, want %d", len(locks), n)
	}
	for i, l := range locks {
		if want := fmt.Sprintf("pool:p%02d", i); l.Scope != want {
			t.Fatalf("list[%d].scope = %q, want %q (ordered by scope)", i, l.Scope, want)
		}
	}
}

// TestRedisAcquireStampsEpochInsideScript_spec_25_4 asserts the acquire
// reads the epoch inside its script: an INCR of the epoch key that lands
// after the TIME read but before the script is the epoch the returned and
// the stored lock carry.
//
// spec: 25.4 (Storage Tiers, Tier Transitions).
func TestRedisAcquireStampsEpochInsideScript_spec_25_4(t *testing.T) {
	forEachBackend(t, func(t *testing.T, f *fixture) {
		ctx := context.Background()
		if err := f.store.SetEpoch(ctx, 4); err != nil {
			t.Fatalf("set epoch: %v", err)
		}
		f.injectBeforeScripts(false, func(redis.Cmder) {
			if err := f.raw.Incr(ctx, epochKey).Err(); err != nil {
				t.Errorf("inject incr: %v", err)
			}
		})
		lock := acquire(t, f.store, "pool:p", "alice", 300)
		if lock.Epoch != 5 {
			t.Errorf("returned epoch = %d, want 5 (the value the script read)", lock.Epoch)
		}
		if got := f.stored(t, "pool:p"); got.Epoch != 5 {
			t.Errorf("stored epoch = %d, want 5", got.Epoch)
		}
	})
}

// TestRedisStaleExtendLosesToSteal_spec_25_4 asserts an Extend that read
// the lock before a steal reports LOCK_NOT_OWNED and leaves the stealer's
// record in place.
//
// spec: 25.4 (Storage Tiers, Error Codes).
func TestRedisStaleExtendLosesToSteal_spec_25_4(t *testing.T) {
	forEachBackend(t, func(t *testing.T, f *fixture) {
		lock := acquire(t, f.store, "pool:p", "alice", 300)
		var stolen *coordination.Lock
		f.injectBeforeScripts(false, func(redis.Cmder) { stolen = steal(t, f.side, lock.ID, "bob", 300) })
		_, err := f.store.Extend(context.Background(), lock.ID, 600, "alice")
		wantCode(t, "stale extend", err, coordination.ErrCodeNotOwned)
		got := f.stored(t, "pool:p")
		if got.AcquiredBy != "bob" || got.StolenFrom != "alice" || canon(t, got) != canon(t, stolen) {
			t.Errorf("stored = %s, want bob's steal %s", canon(t, got), canon(t, stolen))
		}
	})
}

// TestRedisStaleReleaseLosesToSteal_spec_25_4 asserts a Release that read
// the lock before a steal reports LOCK_NOT_OWNED and the stealer's lock and
// ID key survive.
//
// spec: 25.4 (Storage Tiers, Error Codes).
func TestRedisStaleReleaseLosesToSteal_spec_25_4(t *testing.T) {
	forEachBackend(t, func(t *testing.T, f *fixture) {
		lock := acquire(t, f.store, "pool:p", "alice", 300)
		var stolen *coordination.Lock
		f.injectBeforeScripts(false, func(redis.Cmder) { stolen = steal(t, f.side, lock.ID, "bob", 300) })
		err := f.store.Release(context.Background(), lock.ID, "alice")
		wantCode(t, "stale release", err, coordination.ErrCodeNotOwned)
		if got := f.stored(t, "pool:p"); got == nil || canon(t, got) != canon(t, stolen) {
			t.Fatalf("stored = %+v, want bob's steal", got)
		}
		if n := f.keyCount(t, idKeyPrefix+lock.ID); n != 1 {
			t.Errorf("ID key count = %d, want 1", n)
		}
	})
}

// TestRedisExtendOfVanishedLockNotFound_spec_25_4 asserts an Extend whose
// lock is released between its read and its compare-and-set reports
// REMEDIATION_LOCK_NOT_FOUND.
//
// spec: 25.4 (Storage Tiers, Error Codes).
func TestRedisExtendOfVanishedLockNotFound_spec_25_4(t *testing.T) {
	forEachBackend(t, func(t *testing.T, f *fixture) {
		lock := acquire(t, f.store, "pool:p", "alice", 300)
		f.injectBeforeScripts(false, func(redis.Cmder) {
			if err := f.side.Release(context.Background(), lock.ID, "alice"); err != nil {
				t.Errorf("inject release: %v", err)
			}
		})
		_, err := f.store.Extend(context.Background(), lock.ID, 600, "alice")
		wantCode(t, "extend of vanished lock", err, coordination.ErrCodeNotFound)
	})
}

// TestRedisCASRetryThenConflict_spec_25_4 asserts a failed comparison
// retries the operation, and that three failed comparisons end in
// REMEDIATION_LOCK_CONFLICT with nothing written, never
// ErrStoreUnavailable.
//
// spec: 25.4 (Storage Tiers, Error Codes).
func TestRedisCASRetryThenConflict_spec_25_4(t *testing.T) {
	frozen := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	forEachBackend(t, func(t *testing.T, f *fixture) {
		ctx := context.Background()
		f.setTime(frozen)
		lock := acquire(t, f.store, "pool:p", "alice", 300)

		t.Run("one change retries and succeeds", func(t *testing.T) {
			f.injectBeforeScripts(false, func(redis.Cmder) {
				if _, err := f.side.Extend(ctx, lock.ID, 900, "alice"); err != nil {
					t.Errorf("inject extend: %v", err)
				}
			})
			ext, err := f.store.Extend(ctx, lock.ID, 300, "alice")
			if err != nil {
				t.Fatalf("extend after one concurrent change: %v", err)
			}
			if ext.Revision != 2 {
				t.Errorf("revision = %d, want 2 (concurrent change plus this extend)", ext.Revision)
			}
			if got := f.stored(t, "pool:p"); canon(t, got) != canon(t, ext) {
				t.Errorf("stored = %s, want %s", canon(t, got), canon(t, ext))
			}
		})

		t.Run("a change before every attempt ends in conflict", func(t *testing.T) {
			var last *coordination.Lock
			ttl := 600
			f.injectBeforeScripts(true, func(redis.Cmder) {
				ttl += 60 // a fresh record on every injection
				l, err := f.side.Extend(ctx, lock.ID, ttl, "alice")
				if err != nil {
					t.Errorf("inject extend: %v", err)
					return
				}
				last = l
			})
			_, err := f.store.Extend(ctx, lock.ID, 300, "alice")
			wantCode(t, "extend under constant change", err, coordination.ErrCodeConflict)
			if got := f.stored(t, "pool:p"); last == nil || canon(t, got) != canon(t, last) {
				t.Errorf("stored = %s, want the concurrent writer's last record", canon(t, got))
			}
		})
	})
}

// TestRedisStaleStealRestealsCurrentHolder_spec_25_4 asserts a Steal that
// loses its comparison to another steal re-reads and takes the lock from
// the current holder.
//
// spec: 25.4 (Storage Tiers).
func TestRedisStaleStealRestealsCurrentHolder_spec_25_4(t *testing.T) {
	forEachBackend(t, func(t *testing.T, f *fixture) {
		lock := acquire(t, f.store, "pool:p", "alice", 300)
		f.injectBeforeScripts(false, func(redis.Cmder) { steal(t, f.side, lock.ID, "carol", 300) })
		got := steal(t, f.store, lock.ID, "bob", 300)
		if got.AcquiredBy != "bob" || got.StolenFrom != "carol" || got.Revision != lock.Revision+2 {
			t.Errorf("steal = %+v, want bob from carol at revision %d", got, lock.Revision+2)
		}
		if st := f.stored(t, "pool:p"); canon(t, st) != canon(t, got) {
			t.Errorf("stored = %s, want %s", canon(t, st), canon(t, got))
		}
	})
}

// opCtxKey carries the operation kind and caller to the checker's hook.
type opCtxKey struct{}

type opInfo struct{ kind, caller string }

// lockHistory records every lock record observed for one lock ID by
// revision, failing when two different records share a revision.
type lockHistory struct {
	t    *testing.T
	mu   sync.Mutex
	recs map[string]map[uint64]string
	last map[string]uint64
}

func (h *lockHistory) record(l *coordination.Lock) {
	c := canon(h.t, l)
	h.mu.Lock()
	defer h.mu.Unlock()
	byRev := h.recs[l.ID]
	if byRev == nil {
		byRev = map[uint64]string{}
		h.recs[l.ID] = byRev
	}
	if prev, ok := byRev[l.Revision]; ok && prev != c {
		h.t.Errorf("lock %s revision %d has two records:\n  %s\n  %s", l.ID, l.Revision, prev, c)
	}
	byRev[l.Revision] = c
}

// observe records a reader's view and fails when the revision a reader
// sees for one lock ID decreases.
func (h *lockHistory) observe(l *coordination.Lock) {
	h.record(l)
	h.mu.Lock()
	defer h.mu.Unlock()
	if prev, ok := h.last[l.ID]; ok && l.Revision < prev {
		h.t.Errorf("lock %s revision went from %d to %d", l.ID, prev, l.Revision)
	}
	h.last[l.ID] = l.Revision
}

func (h *lockHistory) at(id string, rev uint64) (coordination.Lock, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.recs[id][rev]
	if !ok {
		return coordination.Lock{}, false
	}
	var l coordination.Lock
	_ = json.Unmarshal([]byte(c), &l)
	return l, true
}

// TestRedisLockCASRandomizedInvariants_spec_25_4 drives concurrent
// Acquire, Extend, Release, and Steal calls with random holders over a few
// scopes while a reader polls the stored records, and checks: every
// compare-and-set an Extend or Release applies compared against a record
// whose acquiredBy was the caller; no two records share a lock ID and
// revision (a stale Extend never overwrites a stealer's record); a reader
// never sees a lock's revision decrease; every write's predecessor at the
// prior revision was held by the writer (or, for a steal, by stolenFrom).
// Steals are exempt from the holder check (§25.4 Stealing).
//
// spec: 25.4 (Storage Tiers).
func TestRedisLockCASRandomizedInvariants_spec_25_4(t *testing.T) {
	forEachBackend(t, func(t *testing.T, f *fixture) {
		seed := time.Now().UnixNano()
		t.Logf("seed %d", seed)
		h := &lockHistory{t: t, recs: map[string]map[uint64]string{}, last: map[string]uint64{}}
		f.addHook(func(ctx context.Context, cmd redis.Cmder, next redis.ProcessHook) error {
			op, _ := ctx.Value(opCtxKey{}).(opInfo)
			if !isScript(cmd) || (op.kind != "extend" && op.kind != "release") {
				return next(ctx, cmd)
			}
			expected := scriptArg(cmd, 1)
			err := next(ctx, cmd)
			if c, ok := cmd.(*redis.Cmd); ok && err == nil {
				if v, _ := c.Int64(); v == 1 {
					var l coordination.Lock
					if jerr := json.Unmarshal([]byte(expected), &l); jerr != nil || l.AcquiredBy != op.caller {
						t.Errorf("%s by %s applied against a record held by %q", op.kind, op.caller, l.AcquiredBy)
					}
				}
			}
			return err
		})

		var idsMu sync.Mutex
		var ids []string
		pick := func(r *rand.Rand) string {
			idsMu.Lock()
			defer idsMu.Unlock()
			if len(ids) == 0 {
				return "lock-none"
			}
			return ids[r.Intn(len(ids))]
		}
		holders := []string{"alice", "bob", "carol"}
		scopes := []string{"pool:a", "pool:b", "pool:c"}
		allowed := map[string]bool{
			coordination.ErrCodeConflict: true, coordination.ErrCodeNotFound: true, coordination.ErrCodeNotOwned: true,
		}
		check := func(what string, err error) bool {
			if err == nil {
				return true
			}
			if !allowed[coordination.CodeOf(err)] {
				t.Errorf("%s: unexpected err %v", what, err)
			}
			return false
		}

		stop := make(chan struct{})
		var readerDone sync.WaitGroup
		readerDone.Add(1)
		go func() {
			defer readerDone.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				locks, err := f.store.List(context.Background())
				if err != nil {
					t.Errorf("reader list: %v", err)
					return
				}
				for i := range locks {
					h.observe(&locks[i])
				}
			}
		}()

		const workers, ops = 4, 150
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(r *rand.Rand) {
				defer wg.Done()
				for i := 0; i < ops; i++ {
					by := holders[r.Intn(len(holders))]
					ctx := func(kind string) context.Context {
						return context.WithValue(context.Background(), opCtxKey{}, opInfo{kind: kind, caller: by})
					}
					switch r.Intn(4) {
					case 0:
						l, err := f.store.Acquire(ctx("acquire"), coordination.LockRequest{
							Scope: scopes[r.Intn(len(scopes))], Operation: "scale", AcquiredBy: by, TTLSeconds: 300,
						}, 0)
						if check("acquire", err) {
							h.record(l)
							idsMu.Lock()
							ids = append(ids, l.ID)
							idsMu.Unlock()
						}
					case 1:
						l, err := f.store.Extend(ctx("extend"), pick(r), 300+r.Intn(3), by)
						if check("extend", err) {
							h.record(l)
							if prev, ok := h.at(l.ID, l.Revision-1); ok && prev.AcquiredBy != by {
								t.Errorf("extend by %s succeeded over revision %d held by %s", by, l.Revision-1, prev.AcquiredBy)
							}
						}
					case 2:
						check("release", f.store.Release(ctx("release"), pick(r), by))
					case 3:
						l, err := f.store.Steal(ctx("steal"), pick(r), coordination.StealRequest{Confirm: true, AcquiredBy: by, TTLSeconds: 300})
						if check("steal", err) {
							h.record(l)
							if l.Revision == 0 {
								t.Errorf("steal returned revision 0")
							}
							if prev, ok := h.at(l.ID, l.Revision-1); ok && prev.AcquiredBy != l.StolenFrom {
								t.Errorf("steal recorded stolenFrom %s over revision %d held by %s", l.StolenFrom, l.Revision-1, prev.AcquiredBy)
							}
						}
					}
				}
			}(rand.New(rand.NewSource(seed + int64(w))))
		}
		wg.Wait()
		close(stop)
		readerDone.Wait()
	})
}

// TestRedisLockKeyTTLFollowsNormalizedTTL_spec_25_4 asserts that after
// Acquire, Extend, and Steal the scope key and the ID key each expire after
// the normalized TTL the operation requested, measured from the write.
//
// spec: 25.4 (Storage Tiers, Clock Source).
func TestRedisLockKeyTTLFollowsNormalizedTTL_spec_25_4(t *testing.T) {
	forEachBackend(t, func(t *testing.T, f *fixture) {
		ctx := context.Background()
		wantPTTL := func(what string, id string, ttlSeconds int) {
			t.Helper()
			want := time.Duration(coordination.NormalizeTTL(ttlSeconds)) * time.Second
			for _, k := range []string{scopeKeyPrefix + "pool:p", idKeyPrefix + id} {
				got, err := f.raw.PTTL(ctx, k).Result()
				if err != nil || got != want {
					t.Errorf("%s: PTTL(%s) = %v (%v), want %v", what, k, got, err, want)
				}
			}
		}
		lock := acquire(t, f.store, "pool:p", "alice", 300)
		wantPTTL("acquire", lock.ID, 300)

		f.lockServer.FastForward(150 * time.Second)
		if _, err := f.store.Extend(ctx, lock.ID, 0, "alice"); err != nil {
			t.Fatalf("extend: %v", err)
		}
		wantPTTL("extend with ttlSeconds 0", lock.ID, 0)

		f.lockServer.FastForward(time.Duration(coordination.NormalizeTTL(0)) * time.Second / 2)
		steal(t, f.store, lock.ID, "bob", 1<<30)
		wantPTTL("steal above the ceiling", lock.ID, 1<<30)
	})
}

// TestRedisOutageMapsToStoreUnavailable_spec_25_4 asserts every store call
// that reaches Redis maps an unreachable Redis to ErrStoreUnavailable, so
// the tiered service falls back.
//
// spec: 25.4 (Storage Tiers, Tier Transitions).
func TestRedisOutageMapsToStoreUnavailable_spec_25_4(t *testing.T) {
	f := newStore(t)
	ctx := context.Background()
	lock := acquire(t, f.store, "pool:p", "alice", 300)
	f.lockServer.Close()
	calls := map[string]func() error{
		"Acquire": func() error {
			_, err := f.store.Acquire(ctx, coordination.LockRequest{Scope: "pool:q", AcquiredBy: "bob"}, 0)
			return err
		},
		"Get":         func() error { _, err := f.store.Get(ctx, lock.ID); return err },
		"List":        func() error { _, err := f.store.List(ctx); return err },
		"ActiveLocks": func() error { _, err := f.store.ActiveLocks(ctx); return err },
		"Extend":      func() error { _, err := f.store.Extend(ctx, lock.ID, 300, "alice"); return err },
		"Release":     func() error { return f.store.Release(ctx, lock.ID, "alice") },
		"Steal": func() error {
			_, err := f.store.Steal(ctx, lock.ID, coordination.StealRequest{Confirm: true, AcquiredBy: "bob"})
			return err
		},
		"RemoveByID":     func() error { return f.store.RemoveByID(ctx, lock.ID) },
		"Epoch":          func() error { _, err := f.store.Epoch(ctx); return err },
		"IncrementEpoch": func() error { _, err := f.store.IncrementEpoch(ctx); return err },
		"SetEpoch":       func() error { return f.store.SetEpoch(ctx, 9) },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, coordination.ErrStoreUnavailable) {
			t.Errorf("%s during outage = %v, want ErrStoreUnavailable", name, err)
		}
	}
}

// TestRedisTransportErrorInMutationIsStoreUnavailable_spec_25_4 asserts a
// connection error at any Redis call of Acquire, Extend, Steal, or Release
// (the ID-key read, the record read, the TIME read, or the script) maps to
// ErrStoreUnavailable.
//
// spec: 25.4 (Storage Tiers, Tier Transitions).
func TestRedisTransportErrorInMutationIsStoreUnavailable_spec_25_4(t *testing.T) {
	ops := map[string]func(s *redisstore.Store, id string) error{
		"Extend": func(s *redisstore.Store, id string) error {
			_, err := s.Extend(context.Background(), id, 300, "alice")
			return err
		},
		"Steal": func(s *redisstore.Store, id string) error {
			_, err := s.Steal(context.Background(), id, coordination.StealRequest{Confirm: true, AcquiredBy: "bob"})
			return err
		},
		"Release": func(s *redisstore.Store, id string) error {
			return s.Release(context.Background(), id, "alice")
		},
		"Acquire": func(s *redisstore.Store, _ string) error {
			_, err := s.Acquire(context.Background(), coordination.LockRequest{Scope: "pool:q", AcquiredBy: "bob"}, 0)
			return err
		},
	}
	for name, op := range ops {
		failing := []string{"get", "hget", "time", "evalsha"}
		if name == "Acquire" {
			failing = []string{"time", "evalsha"}
		}
		for _, cmd := range failing {
			t.Run(name+"/"+cmd, func(t *testing.T) {
				forEachBackend(t, func(t *testing.T, f *fixture) {
					lock := acquire(t, f.store, "pool:p", "alice", 300)
					f.failFirst(cmd)
					if err := op(f.store, lock.ID); !errors.Is(err, coordination.ErrStoreUnavailable) {
						t.Fatalf("err = %v, want ErrStoreUnavailable", err)
					}
				})
			})
		}
	}
}

// TestRedisCjsonRoundTripPreservesLock_spec_25_4 asserts the record the
// acquire script re-encodes with cjson decodes to every field Go wrote,
// with the stamped epoch, and that Extend and Steal keep the record intact
// after that round trip (an empty operationId, a set stolenFrom).
//
// spec: 25.4 (Storage Tiers).
func TestRedisCjsonRoundTripPreservesLock_spec_25_4(t *testing.T) {
	forEachBackend(t, func(t *testing.T, f *fixture) {
		ctx := context.Background()
		f.setTime(time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))
		if err := f.store.SetEpoch(ctx, 7); err != nil {
			t.Fatalf("set epoch: %v", err)
		}
		lock := acquire(t, f.store, "pool:p", "alice", 300)
		if lock.Epoch != 7 || lock.OperationID != "" {
			t.Fatalf("acquired = %+v, want epoch 7 and no operationId", lock)
		}
		check := func(what string, want *coordination.Lock) {
			t.Helper()
			if got := f.stored(t, "pool:p"); canon(t, got) != canon(t, want) {
				t.Errorf("%s: stored = %s, want %s", what, canon(t, got), canon(t, want))
			}
			got, err := f.store.Get(ctx, want.ID)
			if err != nil || canon(t, got) != canon(t, want) {
				t.Errorf("%s: get = %+v (%v), want %s", what, got, err, canon(t, want))
			}
		}
		check("acquire", lock)
		ext, err := f.store.Extend(ctx, lock.ID, 600, "alice")
		if err != nil {
			t.Fatalf("extend after cjson round trip: %v", err)
		}
		check("extend", ext)
		st := steal(t, f.store, lock.ID, "bob", 300)
		if st.StolenFrom != "alice" || st.Epoch != 7 {
			t.Errorf("steal = %+v, want stolenFrom alice, epoch 7", st)
		}
		check("steal", st)
	})
}

// TestRedisStaleIDKeyIsNotFound_spec_25_4 asserts that an ID key naming a
// scope another lock has since acquired resolves to no lock: Get, Extend,
// Steal, and Release of the old ID report REMEDIATION_LOCK_NOT_FOUND and
// the new lock is untouched.
//
// spec: 25.4 (Storage Tiers, Error Codes).
func TestRedisStaleIDKeyIsNotFound_spec_25_4(t *testing.T) {
	forEachBackend(t, func(t *testing.T, f *fixture) {
		ctx := context.Background()
		old := acquire(t, f.store, "s", "alice", 300)
		if err := f.store.Release(ctx, old.ID, "alice"); err != nil {
			t.Fatalf("release: %v", err)
		}
		fresh := acquire(t, f.store, "s", "bob", 300)
		if err := f.raw.Set(ctx, idKeyPrefix+old.ID, "s", 0).Err(); err != nil {
			t.Fatalf("seed stale ID key: %v", err)
		}
		before, _ := f.raw.HGet(ctx, scopeKeyPrefix+"s", "data").Result()

		_, err := f.store.Get(ctx, old.ID)
		wantCode(t, "get old id", err, coordination.ErrCodeNotFound)
		_, err = f.store.Extend(ctx, old.ID, 300, "bob")
		wantCode(t, "extend old id", err, coordination.ErrCodeNotFound)
		_, err = f.store.Steal(ctx, old.ID, coordination.StealRequest{Confirm: true, AcquiredBy: "carol"})
		wantCode(t, "steal old id", err, coordination.ErrCodeNotFound)
		wantCode(t, "release old id", f.store.Release(ctx, old.ID, "bob"), coordination.ErrCodeNotFound)

		if after, _ := f.raw.HGet(ctx, scopeKeyPrefix+"s", "data").Result(); after != before {
			t.Errorf("new lock record changed:\n  before %s\n  after  %s", before, after)
		}
		if scope, err := f.raw.Get(ctx, idKeyPrefix+fresh.ID).Result(); err != nil || scope != "s" {
			t.Errorf("new ID key = %q (%v), want s", scope, err)
		}
	})
}

// TestRedisReplayedWritesAreIdempotent_spec_25_4 asserts every Tier 2 write
// tolerates the client re-sending it after a lost reply, one case per
// clause of the §25.4 repeat rule.
//
// spec: 25.4 (Storage Tiers, Tier Transitions).
func TestRedisReplayedWritesAreIdempotent_spec_25_4(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func(t *testing.T, f *fixture){
		"acquire returns its own lock": func(t *testing.T, f *fixture) {
			if err := f.store.SetEpoch(ctx, 3); err != nil {
				t.Fatal(err)
			}
			f.replayFirstWrite(nil)
			lock := acquire(t, f.store, "pool:p", "alice", 300)
			if lock.Epoch != 3 {
				t.Errorf("epoch = %d, want 3", lock.Epoch)
			}
			if got := f.stored(t, "pool:p"); canon(t, got) != canon(t, lock) {
				t.Errorf("stored = %s, want %s", canon(t, got), canon(t, lock))
			}
		},
		"acquire after an intervening steal conflicts": func(t *testing.T, f *fixture) {
			var stolen *coordination.Lock
			f.replayFirstWrite(func(cmd redis.Cmder) { stolen = steal(t, f.side, scriptArg(cmd, 1), "bob", 300) })
			_, err := f.store.Acquire(ctx, coordination.LockRequest{Scope: "pool:p", AcquiredBy: "alice"}, 0)
			wantCode(t, "replayed acquire", err, coordination.ErrCodeConflict)
			if got := f.stored(t, "pool:p"); stolen == nil || canon(t, got) != canon(t, stolen) {
				t.Errorf("stored = %+v, want bob's steal unchanged", got)
			}
		},
		"extend": func(t *testing.T, f *fixture) {
			lock := acquire(t, f.store, "pool:p", "alice", 300)
			f.replayFirstWrite(nil)
			ext, err := f.store.Extend(ctx, lock.ID, 600, "alice")
			if err != nil || ext.Revision != lock.Revision+1 {
				t.Fatalf("replayed extend = %+v (%v), want revision %d", ext, err, lock.Revision+1)
			}
			if got := f.stored(t, "pool:p"); canon(t, got) != canon(t, ext) {
				t.Errorf("stored = %s, want %s", canon(t, got), canon(t, ext))
			}
		},
		"steal": func(t *testing.T, f *fixture) {
			lock := acquire(t, f.store, "pool:p", "alice", 300)
			f.replayFirstWrite(nil)
			st := steal(t, f.store, lock.ID, "bob", 300)
			if st.StolenFrom != "alice" || st.Revision != lock.Revision+1 {
				t.Fatalf("replayed steal = %+v, want stolenFrom alice at revision %d", st, lock.Revision+1)
			}
			if got := f.stored(t, "pool:p"); canon(t, got) != canon(t, st) {
				t.Errorf("stored = %s, want %s", canon(t, got), canon(t, st))
			}
		},
		"release finds the lock absent": func(t *testing.T, f *fixture) {
			lock := acquire(t, f.store, "pool:p", "alice", 300)
			f.replayFirstWrite(nil)
			wantCode(t, "replayed release", f.store.Release(ctx, lock.ID, "alice"), coordination.ErrCodeNotFound)
			if n := f.keyCount(t, scopeKeyPrefix+"pool:p", idKeyPrefix+lock.ID); n != 0 {
				t.Errorf("keys left = %d, want 0", n)
			}
		},
		"reconciliation removal deletes nothing more": func(t *testing.T, f *fixture) {
			lock := acquire(t, f.store, "pool:p", "alice", 300)
			f.replayFirstWrite(nil)
			if err := f.store.RemoveByID(ctx, lock.ID); err != nil {
				t.Fatalf("replayed remove: %v", err)
			}
			if n := f.keyCount(t, scopeKeyPrefix+"pool:p", idKeyPrefix+lock.ID); n != 0 {
				t.Errorf("keys left = %d, want 0", n)
			}
		},
		"epoch increment advances twice": func(t *testing.T, f *fixture) {
			if err := f.store.SetEpoch(ctx, 4); err != nil {
				t.Fatal(err)
			}
			f.replayFirstWrite(nil)
			e, err := f.store.IncrementEpoch(ctx)
			if err != nil || e != 6 {
				t.Fatalf("replayed increment = %d (%v), want 6", e, err)
			}
			if got, _ := f.store.Epoch(ctx); got != 6 {
				t.Errorf("stored epoch = %d, want 6", got)
			}
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) { forEachBackend(t, run) })
	}
}
