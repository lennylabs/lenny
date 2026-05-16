// SPDX-License-Identifier: MIT

// Package delegationpolicystore is the §8.3 DelegationPolicy registry.
// It backs the §15.1 `/v1/admin/delegation-policies` admin CRUD and
// the delegation-time policy resolution that replaces the legacy
// `allowedRuntimes` / `allowedConnectors` / `allowedPools` runtime
// fields with named, tag-matched allow/deny rule sets.
//
// Per §8.3 a DelegationPolicy is platform-global (no tenant_id): it is
// referenced by runtimes and delegation leases across tenants. This
// store is the source of truth for the policy record itself; the
// tag-matching evaluation that consumes it lives in the delegation
// service.
package delegationpolicystore

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"sync"
	"time"
)

// Target is a §8.3 tag-based match target for a delegation rule. A
// target matches a resource when the resource carries every label in
// MatchLabels, its identifier is listed in IDs (when IDs is set), and
// its type is listed in Types (when Types is set).
type Target struct {
	// MatchLabels selects resources carrying all of these labels.
	MatchLabels map[string]string

	// IDs selects resources by explicit identifier. Empty imposes no
	// id constraint.
	IDs []string

	// Types narrows the match to these resource types (for example
	// `agent` or `connector`). Empty imposes no type constraint.
	Types []string
}

// Rule is one §8.3 allow/deny rule. A delegation is permitted when a
// rule whose Target matches the delegation's resource sets Allow.
type Rule struct {
	Target Target
	Allow  bool
}

// ContentPolicy is the §8.3 delegation content-scanning policy. It is
// enforced by the gateway on every `delegate_task` call.
type ContentPolicy struct {
	// MaxInputSize is the hard byte cap on `TaskSpec.input` per
	// delegation. Zero selects the §8.3 default (128 KiB).
	MaxInputSize int

	// InterceptorRef optionally names a RequestInterceptor the gateway
	// invokes at the §4.8 PreDelegation phase. Empty means no
	// interceptor.
	InterceptorRef string

	// ScanExportedFiles routes each exported file through
	// InterceptorRef at the PreExportMaterialization phase. It
	// requires InterceptorRef to be set (§8.3 rule 1).
	ScanExportedFiles bool

	// MaxExportedFileSize is the per-file byte ceiling applied when
	// ScanExportedFiles is set. Zero selects the §8.3 default
	// (10 MiB).
	MaxExportedFileSize int64
}

// DelegationPolicy is the §8.3 first-class delegation-policy resource:
// a named, tag-matched allow/deny rule set plus a content policy and
// the `allowSelfRecursion` cycle-detection opt-in.
type DelegationPolicy struct {
	// Name is the §8.3 policy identifier, referenced by
	// `delegationPolicyRef` and `maxDelegationPolicy`.
	Name string

	// Rules is the ordered tag-based allow/deny rule set.
	Rules []Rule

	// ContentPolicy is the §8.3 per-delegation content policy.
	ContentPolicy ContentPolicy

	// AllowSelfRecursion is layer 3 of the §8.2 cycle-detection AND
	// gate. It is monotonic across delegation hops: a child lease may
	// narrow it (true to false) but never widen it.
	AllowSelfRecursion bool

	// CreatedAt / UpdatedAt / DeletedAt are the audit timestamps.
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt time.Time
}

// IsActive reports whether the policy has not been soft-deleted.
func (p DelegationPolicy) IsActive() bool { return p.DeletedAt.IsZero() }

// Store is the §8.3 DelegationPolicy registry contract.
type Store interface {
	Create(ctx context.Context, p DelegationPolicy) error
	Get(ctx context.Context, name string) (DelegationPolicy, error)
	Update(ctx context.Context, name string, mutate func(*DelegationPolicy) error) (DelegationPolicy, error)
	List(ctx context.Context, filter ListFilter) ([]DelegationPolicy, error)
	SoftDelete(ctx context.Context, name string, at time.Time) error
}

// ListFilter narrows the List result.
type ListFilter struct {
	// IncludeDeleted, when true, returns soft-deleted policies too.
	IncludeDeleted bool
}

// Sentinel errors.
var (
	// ErrNotFound — the policy name is not in the registry.
	ErrNotFound = errors.New("delegationpolicystore: policy not found")

	// ErrAlreadyExists — a policy with this name is already
	// registered.
	ErrAlreadyExists = errors.New("delegationpolicystore: policy already exists")

	// ErrScanRequiresInterceptor — the §8.3 rule-1 invariant:
	// contentPolicy.scanExportedFiles requires a non-empty
	// interceptorRef. The admin layer maps this to the
	// EXPORT_SCAN_REQUIRES_INTERCEPTOR error code.
	ErrScanRequiresInterceptor = errors.New(
		"delegationpolicystore: contentPolicy.scanExportedFiles requires contentPolicy.interceptorRef")
)

// namePattern follows the §8.3 policy-name shape — the same identifier
// pattern used by runtimes and pools.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

// ValidateName reports whether name satisfies the §8.3 pattern.
func ValidateName(name string) error {
	if name == "" {
		return errors.New("delegationpolicystore: name is required")
	}
	if !namePattern.MatchString(name) {
		return errors.New(`delegationpolicystore: name must match ^[a-z0-9][a-z0-9_-]{0,127}$`)
	}
	return nil
}

// Validate reports the §8.3 structural invariants of a policy: a valid
// name, non-negative content-policy sizes, and the scanExportedFiles
// interceptor dependency. Every Store implementation runs it before
// committing a policy.
func Validate(p DelegationPolicy) error {
	if err := ValidateName(p.Name); err != nil {
		return err
	}
	if p.ContentPolicy.MaxInputSize < 0 {
		return errors.New("delegationpolicystore: contentPolicy.maxInputSize must be >= 0")
	}
	if p.ContentPolicy.MaxExportedFileSize < 0 {
		return errors.New("delegationpolicystore: contentPolicy.maxExportedFileSize must be >= 0")
	}
	if p.ContentPolicy.ScanExportedFiles && p.ContentPolicy.InterceptorRef == "" {
		return ErrScanRequiresInterceptor
	}
	return nil
}

// Memory is the in-memory Store implementation backing tests and the
// minimal gateway.
type Memory struct {
	mu       sync.RWMutex
	policies map[string]DelegationPolicy
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory { return &Memory{policies: map[string]DelegationPolicy{}} }

// Create implements Store.
func (m *Memory) Create(_ context.Context, p DelegationPolicy) error {
	if err := Validate(p); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.policies[p.Name]; exists {
		return ErrAlreadyExists
	}
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = p.CreatedAt
	}
	m.policies[p.Name] = clonePolicy(p)
	return nil
}

// Get implements Store. Soft-deleted policies are returned; callers
// consult DelegationPolicy.IsActive to filter.
func (m *Memory) Get(_ context.Context, name string) (DelegationPolicy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	row, ok := m.policies[name]
	if !ok {
		return DelegationPolicy{}, ErrNotFound
	}
	return clonePolicy(row), nil
}

// Update implements Store.
func (m *Memory) Update(_ context.Context, name string, mutate func(*DelegationPolicy) error) (DelegationPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.policies[name]
	if !ok {
		return DelegationPolicy{}, ErrNotFound
	}
	row = clonePolicy(row)
	prev := row.UpdatedAt
	if err := mutate(&row); err != nil {
		return DelegationPolicy{}, err
	}
	if err := Validate(row); err != nil {
		return DelegationPolicy{}, err
	}
	now := time.Now().UTC()
	if !now.After(prev) {
		now = prev.Add(time.Nanosecond)
	}
	row.UpdatedAt = now
	m.policies[name] = clonePolicy(row)
	return clonePolicy(row), nil
}

// List implements Store, returning policies in name order.
func (m *Memory) List(_ context.Context, filter ListFilter) ([]DelegationPolicy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]DelegationPolicy, 0, len(m.policies))
	for _, row := range m.policies {
		if !filter.IncludeDeleted && !row.IsActive() {
			continue
		}
		out = append(out, clonePolicy(row))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SoftDelete implements Store. A second SoftDelete of the same policy
// is a no-op.
func (m *Memory) SoftDelete(_ context.Context, name string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.policies[name]
	if !ok {
		return ErrNotFound
	}
	if !row.DeletedAt.IsZero() {
		return nil
	}
	row.DeletedAt = at
	row.UpdatedAt = at
	m.policies[name] = row
	return nil
}

// clonePolicy deep-copies the slices and map so a stored policy and a
// returned copy never share mutable state.
func clonePolicy(p DelegationPolicy) DelegationPolicy {
	if p.Rules != nil {
		rules := make([]Rule, len(p.Rules))
		for i, r := range p.Rules {
			r.Target.IDs = append([]string(nil), r.Target.IDs...)
			r.Target.Types = append([]string(nil), r.Target.Types...)
			if r.Target.MatchLabels != nil {
				labels := make(map[string]string, len(r.Target.MatchLabels))
				for k, v := range r.Target.MatchLabels {
					labels[k] = v
				}
				r.Target.MatchLabels = labels
			}
			rules[i] = r
		}
		p.Rules = rules
	}
	return p
}

var _ Store = (*Memory)(nil)
