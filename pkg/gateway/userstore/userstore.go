// SPDX-License-Identifier: MIT

// Package userstore is the per-tenant user registry. It backs the
// §11.4 user-invalidation surface and the §15.1 admin user CRUD
// endpoints.
//
// Users are tenant-scoped per §10.2: every Get / Update / List /
// Delete call carries the tenant_id and the store asserts the row's
// tenant_id matches before returning. Cross-tenant reads return
// ErrNotFound — the store never leaks the existence of a user in
// another tenant.
package userstore

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
)

// User captures the §11.4 / §15.1 user registry row.
type User struct {
	// Subject is the JWT `sub` claim. Acts as the registry key.
	Subject string

	// TenantID is the tenant the user belongs to. Cross-tenant reads
	// return ErrNotFound per §10.2.
	TenantID string

	// Email is the human-readable identifier for audit + admin UI.
	Email string

	// DisplayName is the human-readable label.
	DisplayName string

	// Roles is the §10.2 RBAC role set assigned to the user. Empty
	// slice means the default `user` role at runtime.
	Roles []auth.Role

	// Disabled, when true, indicates the user is in the §11.4
	// `soft_disable` state — sessions are not terminated but new
	// session creation is blocked.
	Disabled bool

	// CreatedAt is when the row was committed.
	CreatedAt time.Time

	// UpdatedAt is the last admin mutation.
	UpdatedAt time.Time

	// DeletedAt is the §11.4 `hard_disable` / soft-delete tombstone.
	DeletedAt time.Time

	// ProcessingRestricted is the §12.8 / GDPR Article 18
	// restriction-of-processing flag. It is set when a GDPR erasure job
	// is initiated for the user and cleared when that job completes.
	// While set, new session creation is rejected with
	// ERASURE_IN_PROGRESS. A failed erasure job leaves the flag set.
	ProcessingRestricted bool

	// ErasureJobID is the id of the erasure job that raised
	// ProcessingRestricted, surfaced in the ERASURE_IN_PROGRESS error so
	// the caller can poll the job. It is empty when the user is not
	// restricted.
	ErasureJobID string
}

// IsActive reports whether the user is neither disabled nor
// soft-deleted.
func (u User) IsActive() bool { return !u.Disabled && u.DeletedAt.IsZero() }

// Store is the §11.4 / §15.1 user registry contract.
type Store interface {
	Create(ctx context.Context, u User) error
	Get(ctx context.Context, tenantID, subject string) (User, error)
	Update(ctx context.Context, tenantID, subject string, mutate func(*User) error) (User, error)
	List(ctx context.Context, tenantID string, filter ListFilter) ([]User, error)
	SoftDelete(ctx context.Context, tenantID, subject string, at time.Time) error
}

// ListFilter narrows List results.
type ListFilter struct {
	IncludeDeleted  bool
	IncludeDisabled bool
}

// Sentinel errors.
var (
	ErrNotFound      = errors.New("userstore: user not found")
	ErrAlreadyExists = errors.New("userstore: user already exists")
)

// subjectPattern is the §10.2 JWT-subject pattern — printable ASCII
// up to 256 chars (no whitespace, no control).
var subjectPattern = regexp.MustCompile(`^[!-~]{1,256}$`)

// ValidateSubject reports whether subject satisfies the §10.2 JWT
// subject format.
func ValidateSubject(subject string) error {
	if subject == "" {
		return errors.New("userstore: subject is required")
	}
	if !subjectPattern.MatchString(subject) {
		return errors.New("userstore: subject must be printable ASCII (1-256 chars)")
	}
	return nil
}

// Memory is the in-memory Store implementation.
type Memory struct {
	mu    sync.RWMutex
	users map[string]User // key: tenantID + "/" + subject
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory { return &Memory{users: map[string]User{}} }

func key(tenantID, subject string) string { return tenantID + "/" + subject }

// Create implements Store.
func (m *Memory) Create(_ context.Context, u User) error {
	if err := auth.ValidateTenantID(u.TenantID); err != nil {
		return err
	}
	if err := ValidateSubject(u.Subject); err != nil {
		return err
	}
	for _, r := range u.Roles {
		if !r.IsValid() {
			return errors.New("userstore: role " + string(r) + " is not a recognised §10.2 RBAC role")
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(u.TenantID, u.Subject)
	if _, exists := m.users[k]; exists {
		return ErrAlreadyExists
	}
	now := time.Now().UTC()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = now
	}
	if u.UpdatedAt.IsZero() {
		u.UpdatedAt = u.CreatedAt
	}
	m.users[k] = u
	return nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, tenantID, subject string) (User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	row, ok := m.users[key(tenantID, subject)]
	if !ok || row.TenantID != tenantID {
		return User{}, ErrNotFound
	}
	return row, nil
}

// Update implements Store.
func (m *Memory) Update(_ context.Context, tenantID, subject string, mutate func(*User) error) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(tenantID, subject)
	row, ok := m.users[k]
	if !ok || row.TenantID != tenantID {
		return User{}, ErrNotFound
	}
	prev := row.UpdatedAt
	if err := mutate(&row); err != nil {
		return User{}, err
	}
	for _, r := range row.Roles {
		if !r.IsValid() {
			return User{}, errors.New("userstore: role " + string(r) + " is not a recognised §10.2 RBAC role")
		}
	}
	now := time.Now().UTC()
	if !now.After(prev) {
		now = prev.Add(time.Nanosecond)
	}
	row.UpdatedAt = now
	m.users[k] = row
	return row, nil
}

// List implements Store.
func (m *Memory) List(_ context.Context, tenantID string, filter ListFilter) ([]User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]User, 0)
	for _, row := range m.users {
		if row.TenantID != tenantID {
			continue
		}
		if !filter.IncludeDeleted && !row.DeletedAt.IsZero() {
			continue
		}
		if !filter.IncludeDisabled && row.Disabled {
			continue
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Subject < out[j].Subject })
	return out, nil
}

// SoftDelete implements Store.
func (m *Memory) SoftDelete(_ context.Context, tenantID, subject string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(tenantID, subject)
	row, ok := m.users[k]
	if !ok || row.TenantID != tenantID {
		return ErrNotFound
	}
	if !row.DeletedAt.IsZero() {
		return nil
	}
	row.DeletedAt = at
	row.UpdatedAt = at
	m.users[k] = row
	return nil
}
