// SPDX-License-Identifier: MIT

// Package derivelock implements the §7.1 derive rule 2 per-source-
// session advisory lock. The gateway acquires this lock before reading
// the source session's workspace snapshot reference so concurrent
// `POST /v1/sessions/{id}/derive` calls on the same source serialize
// across replicas, preventing torn reads of a snapshot reference that
// is being updated by a concurrent checkpoint.
//
// Spec semantics (§7.1 line 92):
//
//   - Key: derive_lock:{source_session_id}.
//   - TTL: 30 seconds. The lock auto-expires if the holder crashes
//     mid-derive so a downed replica cannot block subsequent derives
//     indefinitely.
//   - Wait budget: 5 seconds. A caller that fails to acquire within the
//     budget receives `429 DERIVE_LOCK_CONTENTION`.
//   - Release: the lock is released as soon as the gateway has read the
//     snapshot reference. Holding it across the full copy is not
//     required because checkpoint writes always produce new MinIO
//     objects (§7.1 line 92 "Why releasing the lock before the copy is
//     safe"), so the read reference resolves to an immutable object key.
//
// Two implementations:
//
//   - Redis-backed (production): SETNX with a 30s TTL, identified by a
//     per-acquire token so the release script can verify ownership
//     before deleting the key (defense against an expired holder racing
//     with the next acquirer).
//   - In-process (default fallback): a per-source-session sync.Mutex
//     map. The minimal gateway and single-replica deployments fall back
//     to this so the test suite and the dev-mode gateway need no Redis;
//     the in-memory store mutex described in §7.1 line 119 (derive.go)
//     stays the only serialization, which is correct on a single
//     replica and matches the documented v1 posture.
//
// spec: §7.1 line 92 — derive concurrent-serialization.
package derivelock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrContended reports that the caller failed to acquire the §7.1
// derive lock within the spec's 5-second budget. The session-server
// handler maps this to `429 DERIVE_LOCK_CONTENTION`.
//
// spec: §7.1 line 92.
var ErrContended = errors.New("derivelock: per-source-session lock is contended")

// DefaultTTL is the §7.1 line 92 lock TTL (30 seconds). A holder that
// crashes mid-derive releases its lock implicitly when this fires so a
// downed replica cannot block subsequent derives.
const DefaultTTL = 30 * time.Second

// DefaultWait is the §7.1 line 92 caller wait budget (5 seconds). A
// caller that does not acquire within this window receives ErrContended
// and the session-server returns 429 DERIVE_LOCK_CONTENTION.
const DefaultWait = 5 * time.Second

// defaultPollInterval is the spin-wait cadence while polling for an
// existing lock to lapse. Small relative to the wait budget so a
// waiter resumes promptly once the holder finishes.
const defaultPollInterval = 50 * time.Millisecond

// Releaser is the function returned by Acquire. Callers MUST invoke it
// (typically via defer) once the snapshot reference has been read so
// the lock does not have to wait out its TTL before the next derive
// can proceed. Release is idempotent: a second call is a no-op.
type Releaser func()

// Lock is the §7.1 derive advisory lock surface. Acquire blocks for up
// to the configured wait budget, returning ErrContended if no slot
// opens. ctx cancellation short-circuits the wait.
type Lock interface {
	// Acquire takes the per-source-session lock. ctx cancellation
	// returns the caller's context error. A non-nil release function is
	// returned exactly when err is nil.
	Acquire(ctx context.Context, sourceSessionID string) (Releaser, error)
}

// Memory is the in-process fallback Lock used when no Redis-backed
// implementation is wired. Concurrent derives on the same source
// session serialize via a per-source sync.Mutex; cross-replica races
// are not protected — single-replica deployments are correct, multi-
// replica deployments should wire the Redis implementation.
type Memory struct {
	wait time.Duration

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// NewMemory returns an in-process Lock. wait <= 0 selects DefaultWait;
// a non-positive wait would still serialize but would let an
// unbounded number of derives queue, which the spec budget forbids.
func NewMemory(wait time.Duration) *Memory {
	if wait <= 0 {
		wait = DefaultWait
	}
	return &Memory{wait: wait, locks: make(map[string]*sync.Mutex)}
}

// Acquire serializes derives on the same source session inside the
// running process. It honors the wait budget so a runaway derive
// cannot starve callers beyond the spec's 5-second window.
func (m *Memory) Acquire(ctx context.Context, sourceSessionID string) (Releaser, error) {
	lk := m.lockFor(sourceSessionID)

	// Fast path: try once without going through a goroutine. A
	// contended caller falls through to the deadlined goroutine
	// branch.
	if tryLock(lk) {
		return m.releaserFor(sourceSessionID, lk), nil
	}

	deadline := time.Now().Add(m.wait)
	for {
		if tryLock(lk) {
			return m.releaserFor(sourceSessionID, lk), nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, ErrContended
		}
		wait := defaultPollInterval
		if remaining < wait {
			wait = remaining
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

func (m *Memory) lockFor(sourceSessionID string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	lk, ok := m.locks[sourceSessionID]
	if !ok {
		lk = &sync.Mutex{}
		m.locks[sourceSessionID] = lk
	}
	return lk
}

func (m *Memory) releaserFor(sourceSessionID string, lk *sync.Mutex) Releaser {
	var once sync.Once
	return func() {
		once.Do(func() {
			lk.Unlock()
			// GC the entry once no waiters remain. A new acquirer
			// repopulates the map under m.mu, so this drop is safe
			// from a steady-state memory-growth perspective.
			m.mu.Lock()
			if cur, ok := m.locks[sourceSessionID]; ok && cur == lk && tryLock(cur) {
				delete(m.locks, sourceSessionID)
				cur.Unlock()
			}
			m.mu.Unlock()
		})
	}
}

// tryLock returns true when it acquired lk without blocking. Implements
// the Go 1.18+ idiom via the runtime-internal TryLock method.
func tryLock(lk *sync.Mutex) bool {
	return lk.TryLock()
}

// Redis is the §7.1 line 92 Redis-backed Lock. SETNX writes a per-
// acquire token under the spec key with a 30-second TTL; release runs
// a Lua compare-and-delete so an expired holder cannot accidentally
// release the next acquirer's lock.
type Redis struct {
	client *redis.Client
	ttl    time.Duration
	wait   time.Duration

	// keyPrefix overrides the spec's `derive_lock:` namespace. Tests
	// override to keep parallel runs isolated; production uses the
	// default.
	keyPrefix string
}

// RedisOption configures a Redis Lock.
type RedisOption func(*Redis)

// WithTTL overrides the §7.1 30-second TTL. A non-positive value keeps
// the default.
func WithTTL(d time.Duration) RedisOption {
	return func(r *Redis) {
		if d > 0 {
			r.ttl = d
		}
	}
}

// WithWait overrides the §7.1 5-second wait budget. A non-positive
// value keeps the default.
func WithWait(d time.Duration) RedisOption {
	return func(r *Redis) {
		if d > 0 {
			r.wait = d
		}
	}
}

// WithKeyPrefix overrides the `derive_lock:` namespace. Tests use this
// to keep parallel-run instances isolated within a single Redis
// database.
func WithKeyPrefix(p string) RedisOption {
	return func(r *Redis) {
		if p != "" {
			r.keyPrefix = p
		}
	}
}

// NewRedis constructs a Redis-backed Lock.
func NewRedis(client *redis.Client, opts ...RedisOption) *Redis {
	r := &Redis{
		client:    client,
		ttl:       DefaultTTL,
		wait:      DefaultWait,
		keyPrefix: "derive_lock:",
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// releaseScript compare-and-deletes the lock key only when the stored
// token matches. Prevents an expired holder from releasing a peer
// replica's freshly-acquired lock.
var releaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

// Acquire issues SET NX with the spec TTL. On miss it polls until the
// wait budget elapses; the spec's 5-second wait is deliberately
// generous so a slow snapshot read does not starve callers. ctx
// cancellation short-circuits the poll.
func (r *Redis) Acquire(ctx context.Context, sourceSessionID string) (Releaser, error) {
	key := r.keyPrefix + sourceSessionID
	token, err := mintToken()
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(r.wait)
	for {
		ok, err := r.client.SetNX(ctx, key, token, r.ttl).Result()
		if err != nil {
			return nil, err
		}
		if ok {
			return r.releaserFor(key, token), nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, ErrContended
		}
		wait := defaultPollInterval
		if remaining < wait {
			wait = remaining
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

func (r *Redis) releaserFor(key, token string) Releaser {
	var once sync.Once
	return func() {
		once.Do(func() {
			// Run the release script with a fresh context so a caller
			// whose original ctx cancelled after the snapshot read
			// still drops the lock promptly. The TTL is the backstop.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _ = releaseScript.Run(ctx, r.client, []string{key}, token).Result()
		})
	}
}

// mintToken returns a fresh random per-acquire token used to fence the
// release script.
func mintToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// noopReleaser is a Releaser that does nothing. Returned by callers
// that want to bypass the lock (e.g., a derive that explicitly opts
// out). Exported for tests; production paths always take a real lock.
var noopReleaser Releaser = func() {}

// NoLock returns a Lock implementation that admits every acquire. Used
// by tests and by callers that want to disable the §7.1 lock for
// throughput experiments. Production code MUST NOT use this.
func NoLock() Lock { return noLock{} }

type noLock struct{}

func (noLock) Acquire(_ context.Context, _ string) (Releaser, error) {
	return noopReleaser, nil
}
