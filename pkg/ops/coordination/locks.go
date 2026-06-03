// SPDX-License-Identifier: MIT

// Package coordination implements the §25.4 remediation-lock store: the
// named-lock registry that prevents conflicting concurrent remediations
// from multiple agents. A remediation lock grants exclusive access to a
// platform resource scope ("pool:default-gvisor", "restore:platform")
// for a TTL.
//
// §25.4 specifies a tiered store (Postgres → Redis → in-memory) so that
// coordination survives storage outages. The v1 store implemented here
// is the in-memory Tier 3 store: a compare-and-set registry keyed by
// scope, with server-authoritative timestamps, lazy TTL expiry, the
// monotonic epoch, the steal revision counter, and the §25.4 identity
// rule on Release, Extend, and Steal. The Postgres and Redis tiers
// reuse this service's contract.
//
// Scope-based authorization (which caller roles may touch which scopes)
// is the separate §25.4 control in pkg/remediationlock; the HTTP
// handler applies it before calling this store.
package coordination

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"
)

// LockStore identifies which §25.4 storage tier holds a lock. The v1
// service runs the in-memory tier; the value stays on the Lock struct
// as a resource attribute (§25.2 distinguishes it from the response
// degradation envelope).
const (
	// StorePostgres is the Tier 1 store: strict exclusivity across all
	// replicas via the ops_remediation_locks table.
	StorePostgres = "postgres"
	// StoreRedis is the Tier 2 store: strict exclusivity across replicas
	// via a Redis keyspace, used while Postgres is unreachable.
	StoreRedis = "redis"
	// StoreMemory is the Tier 3 store: a per-replica in-memory registry,
	// used while both Postgres and Redis are unreachable.
	StoreMemory = "memory"
)

// §25.4 canonical remediation-lock error codes. The HTTP layer maps
// each to its documented status code and §25.2 category.
const (
	// ErrCodeConflict is REMEDIATION_LOCK_CONFLICT: a non-expired lock
	// already holds the scope.
	ErrCodeConflict = "REMEDIATION_LOCK_CONFLICT"
	// ErrCodeNotFound is REMEDIATION_LOCK_NOT_FOUND: the lock id is
	// unknown or the lock has expired or been released.
	ErrCodeNotFound = "REMEDIATION_LOCK_NOT_FOUND"
	// ErrCodeNotOwned is LOCK_NOT_OWNED: the caller is not the lock's
	// acquiredBy and so may not release or extend it.
	ErrCodeNotOwned = "LOCK_NOT_OWNED"
	// ErrCodeNoCoordination is REMEDIATION_LOCK_NO_COORDINATION: both
	// Postgres and Redis are unreachable and Tier 3 is not permitted.
	ErrCodeNoCoordination = "REMEDIATION_LOCK_NO_COORDINATION"
)

// Error is a §25.4 remediation-lock failure carrying the canonical
// error code so the HTTP handler maps it to the documented status code
// and category without re-classifying the failure.
type Error struct {
	// Code is one of the §25.4 ErrCode* constants.
	Code string
	// Message is the human-readable detail.
	Message string
	// SplitBrain is set on a REMEDIATION_LOCK_CONFLICT raised by the
	// §25.4 deterministic split-brain resolution rule.
	SplitBrain bool
}

// Error implements error.
func (e *Error) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

// CodeOf returns the §25.4 canonical error code carried by err, or the
// empty string when err is not a coordination *Error.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// IsSplitBrain reports whether err is a §25.4 split-brain conflict.
func IsSplitBrain(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.SplitBrain
}

// Lock is the §25.4 remediation-lock record. The JSON tags match the
// §25.4 Lock struct verbatim so the HTTP handler marshals it directly.
type Lock struct {
	ID          string    `json:"id"`
	Scope       string    `json:"scope"`
	Operation   string    `json:"operation"`
	AcquiredBy  string    `json:"acquiredBy"`
	OperationID string    `json:"operationId,omitempty"`
	AcquiredAt  time.Time `json:"acquiredAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
	LockStore   string    `json:"lockStore"`
	Epoch       uint64    `json:"epoch"`
	Revision    uint64    `json:"revision"`
	StolenFrom  string    `json:"stolenFrom,omitempty"`
}

// LockRequest is the §25.4 acquire-a-lock request body.
type LockRequest struct {
	Scope       string `json:"scope"`
	Operation   string `json:"operation"`
	TTLSeconds  int    `json:"ttlSeconds"`
	OperationID string `json:"operationId,omitempty"`
	// AcquiredBy is the caller identity; the HTTP layer fills it from the
	// authenticated principal, not from the request body.
	AcquiredBy string `json:"-"`
}

// StealRequest is the §25.4 steal-a-lock request body.
type StealRequest struct {
	Confirm    bool   `json:"confirm"`
	Reason     string `json:"reason"`
	TTLSeconds int    `json:"ttlSeconds"`
	// AcquiredBy is the caller identity; the HTTP layer fills it.
	AcquiredBy string `json:"-"`
}

// RemediationLockService is the §25.4 lock-service contract. The
// in-memory MemStore implements it; the Postgres and Redis tiers
// implement the same interface.
type RemediationLockService interface {
	Acquire(ctx context.Context, req LockRequest) (*Lock, error)
	List(ctx context.Context) ([]Lock, error)
	Get(ctx context.Context, lockID string) (*Lock, error)
	Extend(ctx context.Context, lockID string, ttlSeconds int) (*Lock, error)
	Release(ctx context.Context, lockID string) error
	Steal(ctx context.Context, lockID string, req StealRequest) (*Lock, error)
}

// defaultTTLSeconds is the §25.4 lock TTL applied when a request omits
// ttlSeconds; maxTTLSeconds is the documented ceiling (ops.locks.-
// maxTTLSeconds).
const (
	defaultTTLSeconds = 300
	maxTTLSeconds     = 3600
)

// MemStore is the §25.4 Tier 3 in-memory remediation-lock store. It is
// a compare-and-set registry keyed by scope: Acquire fails closed when
// a non-expired lock already holds the scope, with no check-then-write
// window. TTL expiry is lazy — a request observing an expired lock
// treats the scope as free. The store is safe for concurrent use.
type MemStore struct {
	mu      sync.Mutex
	byScope map[string]*Lock // scope → current lock
	byID    map[string]*Lock // lock id → current lock
	epoch   uint64
	now     func() time.Time
}

// NewMemStore returns an empty in-memory remediation-lock store.
func NewMemStore() *MemStore {
	return &MemStore{
		byScope: make(map[string]*Lock),
		byID:    make(map[string]*Lock),
		now:     func() time.Time { return time.Now().UTC() },
	}
}

// SetClock overrides the store's clock; tests use it for deterministic
// TTL expiry.
func (s *MemStore) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// NormalizeTTL clamps a requested TTL to the §25.4 default and ceiling.
// The durable tier stores (pgstore, redisstore) reuse it so every tier
// applies the identical ttlSeconds policy.
func NormalizeTTL(ttlSeconds int) int { return normalizeTTL(ttlSeconds) }

// NewLockID returns a fresh "lock-" + hex remediation-lock id. The
// durable tier stores reuse it so ids share one format across tiers.
func NewLockID() string { return newLockID() }

// normalizeTTL clamps a requested TTL to the §25.4 default and ceiling.
func normalizeTTL(ttlSeconds int) int {
	switch {
	case ttlSeconds <= 0:
		return defaultTTLSeconds
	case ttlSeconds > maxTTLSeconds:
		return maxTTLSeconds
	default:
		return ttlSeconds
	}
}

// expired reports whether the lock has passed its expiry as of now.
func expired(l *Lock, now time.Time) bool {
	return !now.Before(l.ExpiresAt)
}

// reapLocked drops every expired lock. The caller holds s.mu. §25.4
// expired locks are cleaned up lazily on acquire and by a periodic
// goroutine; this is the lazy path.
func (s *MemStore) reapLocked(now time.Time) {
	for scope, l := range s.byScope {
		if expired(l, now) {
			delete(s.byScope, scope)
			delete(s.byID, l.ID)
		}
	}
}

// Acquire creates a §25.4 remediation lock on req.Scope. It fails with
// REMEDIATION_LOCK_CONFLICT when a non-expired lock already holds the
// scope. The caller becomes acquiredBy; acquiredAt and expiresAt are
// server-authoritative (the server computes expiresAt = now +
// ttlSeconds, so the caller passes no expiry).
func (s *MemStore) Acquire(_ context.Context, req LockRequest) (*Lock, error) {
	if req.Scope == "" {
		return nil, &Error{Code: ErrCodeConflict, Message: "scope is required"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.reapLocked(now)
	if existing, held := s.byScope[req.Scope]; held {
		return nil, &Error{
			Code: ErrCodeConflict,
			Message: "scope " + req.Scope + " is held by " + existing.AcquiredBy +
				" until " + existing.ExpiresAt.Format(time.RFC3339),
		}
	}
	ttl := normalizeTTL(req.TTLSeconds)
	lock := &Lock{
		ID:          newLockID(),
		Scope:       req.Scope,
		Operation:   req.Operation,
		AcquiredBy:  req.AcquiredBy,
		OperationID: req.OperationID,
		AcquiredAt:  now,
		ExpiresAt:   now.Add(time.Duration(ttl) * time.Second),
		LockStore:   StoreMemory,
		Epoch:       s.epoch,
		Revision:    0,
	}
	s.byScope[req.Scope] = lock
	s.byID[lock.ID] = lock
	return cloneLock(lock), nil
}

// List returns every §25.4 active (non-expired) lock, ordered by scope
// for a deterministic response.
func (s *MemStore) List(_ context.Context) ([]Lock, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.reapLocked(now)
	out := make([]Lock, 0, len(s.byScope))
	for _, l := range s.byScope {
		out = append(out, *cloneLock(l))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Scope < out[j].Scope })
	return out, nil
}

// Get returns a single §25.4 lock's current state. It returns
// REMEDIATION_LOCK_NOT_FOUND when the lock id is unknown or the lock
// has expired or been released — never silent success — so an agent
// re-validating ownership before a remediation step discovers a lost
// lock.
func (s *MemStore) Get(_ context.Context, lockID string) (*Lock, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.reapLocked(now)
	l, ok := s.byID[lockID]
	if !ok {
		return nil, &Error{Code: ErrCodeNotFound, Message: "no lock " + lockID}
	}
	return cloneLock(l), nil
}

// Extend renews a §25.4 lock's TTL to now + ttlSeconds. §25.4
// authorization is identity-based: the caller MUST be the current
// acquiredBy. The HTTP layer passes the caller identity through the
// context; ExtendAs is the identity-aware form.
func (s *MemStore) Extend(ctx context.Context, lockID string, ttlSeconds int) (*Lock, error) {
	return s.ExtendAs(ctx, lockID, ttlSeconds, callerFrom(ctx))
}

// ExtendAs renews a lock's TTL on behalf of caller. It returns
// LOCK_NOT_OWNED when caller is not the current acquiredBy and
// REMEDIATION_LOCK_NOT_FOUND when the lock has expired or is unknown.
// Extension increments the revision per §25.4.
func (s *MemStore) ExtendAs(_ context.Context, lockID string, ttlSeconds int, caller string) (*Lock, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.reapLocked(now)
	l, ok := s.byID[lockID]
	if !ok {
		return nil, &Error{Code: ErrCodeNotFound, Message: "no lock " + lockID}
	}
	if l.AcquiredBy != caller {
		return nil, &Error{Code: ErrCodeNotOwned, Message: "lock " + lockID + " is not owned by the caller"}
	}
	ttl := normalizeTTL(ttlSeconds)
	l.ExpiresAt = now.Add(time.Duration(ttl) * time.Second)
	l.Revision++
	return cloneLock(l), nil
}

// Release frees a §25.4 lock. §25.4 authorization is identity-based:
// the caller MUST be the current acquiredBy, otherwise LOCK_NOT_OWNED —
// releasing another holder's lock requires Steal (audited). The HTTP
// layer passes the caller identity through the context.
func (s *MemStore) Release(ctx context.Context, lockID string) error {
	return s.ReleaseAs(ctx, lockID, callerFrom(ctx))
}

// ReleaseAs frees a lock on behalf of caller. It returns
// REMEDIATION_LOCK_NOT_FOUND when the lock is unknown or expired and
// LOCK_NOT_OWNED when caller is not the current acquiredBy.
func (s *MemStore) ReleaseAs(_ context.Context, lockID, caller string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.reapLocked(now)
	l, ok := s.byID[lockID]
	if !ok {
		return &Error{Code: ErrCodeNotFound, Message: "no lock " + lockID}
	}
	if l.AcquiredBy != caller {
		return &Error{Code: ErrCodeNotOwned, Message: "lock " + lockID + " is not owned by the caller"}
	}
	delete(s.byScope, l.Scope)
	delete(s.byID, l.ID)
	return nil
}

// Steal takes over an existing §25.4 lock: the caller becomes the new
// acquiredBy, the prior holder is recorded in stolenFrom, and the
// revision increments. §25.4 makes steal the explicit, audited
// alternative to silently ignoring a lock; the HTTP layer requires
// confirm:true before calling Steal. The caller identity comes from the
// context or req.AcquiredBy.
func (s *MemStore) Steal(ctx context.Context, lockID string, req StealRequest) (*Lock, error) {
	caller := req.AcquiredBy
	if caller == "" {
		caller = callerFrom(ctx)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.reapLocked(now)
	l, ok := s.byID[lockID]
	if !ok {
		return nil, &Error{Code: ErrCodeNotFound, Message: "no lock " + lockID}
	}
	ttl := normalizeTTL(req.TTLSeconds)
	l.StolenFrom = l.AcquiredBy
	l.AcquiredBy = caller
	l.OperationID = ""
	l.AcquiredAt = now
	l.ExpiresAt = now.Add(time.Duration(ttl) * time.Second)
	l.Revision++
	return cloneLock(l), nil
}

// IncrementEpoch advances the §25.4 outage epoch and returns the new
// value. §25.4 stamps the epoch onto Tier 2 acquisitions so that a
// post-recovery reconciliation can detect split-brain; the in-memory
// store exposes the counter for the tier-transition machinery.
func (s *MemStore) IncrementEpoch() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.epoch++
	return s.epoch
}

// Epoch returns the store's current §25.4 outage epoch.
func (s *MemStore) Epoch() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.epoch
}

// SetEpoch raises the store's §25.4 outage epoch to v when v exceeds the
// current value. The tiered Service syncs the Tier-3 store's epoch to the
// current outage epoch before a fall-through acquire so an in-memory lock
// carries the epoch under which it was granted. The epoch never moves
// backward (§25.4 line 2230 MAX reconciliation).
func (s *MemStore) SetEpoch(v uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v > s.epoch {
		s.epoch = v
	}
}

// Reap drops every expired lock. §25.4 has a periodic goroutine (every
// 60s) clean up expired rows; this is that sweep for the in-memory
// store. It returns the number of locks reaped.
func (s *MemStore) Reap() int {
	return len(s.ReapExpired())
}

// ReapExpired drops every expired lock and returns the removed records so
// the tiered Service can emit a remediation.lock_expired audit event for
// each. §25.4 line 2338 names lock_expired among the audited lifecycle
// transitions.
func (s *MemStore) ReapExpired() []Lock {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var expiredLocks []Lock
	for scope, l := range s.byScope {
		if expired(l, now) {
			expiredLocks = append(expiredLocks, *cloneLock(l))
			delete(s.byScope, scope)
			delete(s.byID, l.ID)
		}
	}
	return expiredLocks
}

// callerKey is the context key carrying the §25.4 caller identity into
// the identity-based Release and Extend paths.
type callerKey struct{}

// WithCaller returns a context carrying the §25.4 caller identity. The
// HTTP layer sets it from the authenticated principal so Release and
// Extend can enforce the identity-based rule.
func WithCaller(ctx context.Context, caller string) context.Context {
	return context.WithValue(ctx, callerKey{}, caller)
}

// callerFrom returns the §25.4 caller identity carried by ctx, or the
// empty string when none was set (in which case the identity check
// fails closed against any non-empty acquiredBy).
func callerFrom(ctx context.Context) string {
	v, _ := ctx.Value(callerKey{}).(string)
	return v
}

// cloneLock returns a copy of l so a caller cannot mutate the stored
// record through the returned pointer.
func cloneLock(l *Lock) *Lock {
	cp := *l
	return &cp
}

// newLockID returns a random "lock-" + hex remediation-lock id.
func newLockID() string {
	var b [10]byte
	_, _ = rand.Read(b[:])
	return "lock-" + hex.EncodeToString(b[:])
}
