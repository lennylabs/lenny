// SPDX-License-Identifier: MIT

// Package interceptorstore is the §4.8 external-interceptor registry:
// the persistent, admin-mutable, cross-replica source of truth for the
// deployer-supplied RequestInterceptor configurations that the §8.3
// rule-5 SEC-013 fail-policy weakening cooldown is keyed on.
//
// An interceptor row carries the §4.8 registration fields (endpoint,
// priority, failPolicy, timeout, phases) plus two server-minted,
// admin-API-immutable fields recording the most recent
// `fail-closed → fail-open` transition: FailOpenTransitionAt (the
// server-minted transition timestamp) and CooldownSecondsAtTransition
// (the cluster-scoped cooldown duration that was in force at that
// transition). The admin write path never sets these from a request
// body — they are minted by the gateway at the instant a weakening
// transition is persisted (§8.3 SEC-013), which is why they live in a
// field set the wire payload does not expose as writable.
//
// The registry is platform-scoped: external interceptors are cluster
// infrastructure registered by deployers under the platform-admin role,
// not tenant resources. A §8.3 DelegationPolicy references an
// interceptor by name through `contentPolicy.interceptorRef`; the
// delegation service reads this registry per `delegate_task` /
// `lenny/send_message` to apply the weakening cooldown (§8.3 rules 1-2:
// the registry is the single source of truth, read per invocation, never
// snapshotted into a lease).
//
// spec: §4.8 lines 1034-1040; §8.3 lines 205-224 (SEC-013).
package interceptorstore

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
)

// Interceptor is one §4.8 registered external interceptor.
type Interceptor struct {
	// Name identifies the interceptor; it is referenced by a
	// DelegationPolicy's `contentPolicy.interceptorRef`. Unique across
	// the platform.
	Name string

	// Endpoint is the gRPC address (host:port) of the interceptor
	// service per the §4.8 registration table (line 1019).
	Endpoint string

	// Priority orders execution within a phase; lower runs first. An
	// external interceptor must register above
	// interceptor.ReservedPriorityCeiling (§4.8 line 1020).
	Priority int32

	// FailPolicy governs the chain's response to a transport error or
	// timeout: fail-closed (default) or fail-open.
	FailPolicy interceptor.FailPolicy

	// TimeoutMs is the per-call deadline in milliseconds. Zero selects
	// the §4.8 per-phase default at invocation time.
	TimeoutMs int

	// Phases is the §4.8 phase set this interceptor registers for. No
	// phase may be PhasePreAuth (§4.8 line 1023).
	Phases []interceptor.Phase

	// FailOpenTransitionAt is the §8.3 SEC-013 server-minted timestamp
	// of the most recent `fail-closed → fail-open` transition. Zero
	// means no active weakening cooldown (the interceptor never
	// weakened, or a later `fail-open → fail-closed` strengthen cleared
	// it). It is admin-API-immutable: the admin write path mints it and
	// never reads it from the request body.
	FailOpenTransitionAt time.Time

	// CooldownSecondsAtTransition is the cluster-scoped
	// `gateway.interceptorWeakeningCooldownSeconds` value that was in
	// force at FailOpenTransitionAt. The §8.3 meta-cooldown rule
	// evaluates a pending cooldown against this recorded value rather
	// than the live cluster config, so reducing the cluster value never
	// shortens an already-active cooldown.
	CooldownSecondsAtTransition int

	// CreatedAt / UpdatedAt are the audit timestamps.
	CreatedAt time.Time
	UpdatedAt time.Time

	// Version is the §15.1 optimistic-concurrency counter: it starts at
	// 1 and increments by one on every successful Update.
	Version int64
}

// Store is the §4.8 interceptor registry persistence interface. The
// Memory implementation backs tests and the minimal gateway; pgstore
// backs the production deployment.
type Store interface {
	Create(ctx context.Context, ic Interceptor) error
	Get(ctx context.Context, name string) (Interceptor, error)
	// Update applies mutate to the stored row in place and persists the
	// result with an incremented Version. mutate may not change Name.
	Update(ctx context.Context, name string, mutate func(*Interceptor) error) (Interceptor, error)
	List(ctx context.Context) ([]Interceptor, error)
	Delete(ctx context.Context, name string) error
}

// Sentinel errors.
var (
	// ErrNotFound — the interceptor name is not in the registry.
	ErrNotFound = errors.New("interceptorstore: interceptor not found")
	// ErrAlreadyExists — an interceptor with this name is already
	// registered.
	ErrAlreadyExists = errors.New("interceptorstore: interceptor already exists")
)

// namePattern follows the §4.8 interceptor-name shape — the same
// identifier pattern runtimes, pools, and delegation policies use.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

// ValidateName reports whether name satisfies the §4.8 pattern.
func ValidateName(name string) error {
	if name == "" {
		return errors.New("interceptorstore: name is required")
	}
	if !namePattern.MatchString(name) {
		return errors.New(`interceptorstore: name must match ^[a-z0-9][a-z0-9_-]{0,127}$`)
	}
	return nil
}

// Validate reports the §4.8 structural invariants of an interceptor
// registration: a valid name and endpoint, a priority above the
// reserved ceiling, a known fail policy, and a non-empty phase set in
// which no phase is PhasePreAuth. The priority and phase checks reuse
// the interceptor.Chain sentinels so the registry rejects exactly what
// the in-process Register would reject (interceptor.ErrInvalidPriority,
// interceptor.ErrInvalidPhase), letting the admin layer map them to
// INVALID_INTERCEPTOR_PRIORITY / INVALID_INTERCEPTOR_PHASE.
func Validate(ic Interceptor) error {
	if err := ValidateName(ic.Name); err != nil {
		return err
	}
	if ic.Endpoint == "" {
		return errors.New("interceptorstore: endpoint is required")
	}
	switch ic.FailPolicy {
	case interceptor.FailClosed, interceptor.FailOpen:
	default:
		return fmt.Errorf("interceptorstore: failPolicy must be %q or %q", interceptor.FailClosed, interceptor.FailOpen)
	}
	if ic.TimeoutMs < 0 {
		return errors.New("interceptorstore: timeoutMs must be >= 0")
	}
	// §4.8 line 1020 — external interceptors must register above the
	// reserved ceiling.
	if ic.Priority <= interceptor.ReservedPriorityCeiling {
		return fmt.Errorf("%w: %q has priority %d", interceptor.ErrInvalidPriority, ic.Name, ic.Priority)
	}
	if len(ic.Phases) == 0 {
		return errors.New("interceptorstore: at least one phase is required")
	}
	for _, p := range ic.Phases {
		if !p.IsValid() {
			return fmt.Errorf("interceptorstore: unknown phase %q", p)
		}
		// §4.8 line 1023 — the PreAuth phase is built-in only.
		if p == interceptor.PhasePreAuth {
			return fmt.Errorf("%w: %q", interceptor.ErrInvalidPhase, ic.Name)
		}
	}
	return nil
}

// ApplyDefaults fills the §4.8 registration defaults: an empty
// FailPolicy becomes FailClosed (so a misbehaving interceptor cannot
// silently bypass policy) and a zero Priority becomes the §4.8 default
// external priority.
func ApplyDefaults(ic *Interceptor) {
	if ic.FailPolicy == "" {
		ic.FailPolicy = interceptor.FailClosed
	}
	if ic.Priority == 0 {
		ic.Priority = interceptor.DefaultExternalPriority
	}
}

// Memory is the in-memory Store backing tests and the minimal gateway.
type Memory struct {
	mu  sync.RWMutex
	ics map[string]Interceptor
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory { return &Memory{ics: map[string]Interceptor{}} }

// Create implements Store.
func (m *Memory) Create(_ context.Context, ic Interceptor) error {
	if err := Validate(ic); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.ics[ic.Name]; exists {
		return ErrAlreadyExists
	}
	now := time.Now().UTC()
	if ic.CreatedAt.IsZero() {
		ic.CreatedAt = now
	}
	if ic.UpdatedAt.IsZero() {
		ic.UpdatedAt = ic.CreatedAt
	}
	if ic.Version == 0 {
		ic.Version = 1
	}
	m.ics[ic.Name] = cloneInterceptor(ic)
	return nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, name string) (Interceptor, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ic, ok := m.ics[name]
	if !ok {
		return Interceptor{}, ErrNotFound
	}
	return cloneInterceptor(ic), nil
}

// Update implements Store. The mutation runs against a copy; on success
// the Version increments by one. mutate may not change the Name.
func (m *Memory) Update(_ context.Context, name string, mutate func(*Interceptor) error) (Interceptor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.ics[name]
	if !ok {
		return Interceptor{}, ErrNotFound
	}
	next := cloneInterceptor(cur)
	if err := mutate(&next); err != nil {
		return Interceptor{}, err
	}
	next.Name = name
	if err := Validate(next); err != nil {
		return Interceptor{}, err
	}
	next.Version = cur.Version + 1
	next.CreatedAt = cur.CreatedAt
	m.ics[name] = cloneInterceptor(next)
	return cloneInterceptor(next), nil
}

// List implements Store, returning rows sorted by name.
func (m *Memory) List(_ context.Context) ([]Interceptor, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Interceptor, 0, len(m.ics))
	for _, ic := range m.ics {
		out = append(out, cloneInterceptor(ic))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Delete implements Store. The §8.3 rule-6 deletion guard (rejecting a
// referenced interceptor) is enforced at the admin layer, which owns
// the DelegationPolicy registry needed to count dependents.
func (m *Memory) Delete(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.ics[name]; !ok {
		return ErrNotFound
	}
	delete(m.ics, name)
	return nil
}

func cloneInterceptor(ic Interceptor) Interceptor {
	if ic.Phases != nil {
		phases := make([]interceptor.Phase, len(ic.Phases))
		copy(phases, ic.Phases)
		ic.Phases = phases
	}
	return ic
}

// CooldownResolver adapts a Store to the §8.3 fail-policy weakening
// cooldown lookup the delegation service performs at `delegate_task` /
// `lenny/send_message` admission. Given an interceptor name it returns
// the active fail-open weakening cooldown: the server-minted transition
// timestamp and the cooldown seconds that were in force at that
// transition (the §8.3 meta-cooldown rule pins the duration to the
// transition, not the live cluster config). ok is false when the
// interceptor is unknown or not currently weakened.
type CooldownResolver struct{ store Store }

// NewCooldownResolver wraps a Store as a CooldownResolver.
func NewCooldownResolver(s Store) CooldownResolver { return CooldownResolver{store: s} }

// FailOpenCooldown returns the named interceptor's active fail-open
// weakening cooldown. A read error, an unknown interceptor, or a row
// with no recorded transition resolves to (zero, 0, false), which the
// delegation service treats as "no cooldown in force".
func (r CooldownResolver) FailOpenCooldown(ctx context.Context, name string) (time.Time, int, bool) {
	if r.store == nil || name == "" {
		return time.Time{}, 0, false
	}
	ic, err := r.store.Get(ctx, name)
	if err != nil {
		return time.Time{}, 0, false
	}
	if ic.FailOpenTransitionAt.IsZero() || ic.CooldownSecondsAtTransition <= 0 {
		return time.Time{}, 0, false
	}
	return ic.FailOpenTransitionAt, ic.CooldownSecondsAtTransition, true
}
