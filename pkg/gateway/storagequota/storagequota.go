// SPDX-License-Identifier: MIT

// Package storagequota is the §11.2 per-tenant storage byte counter.
// It backs the gateway's pre-upload quota reservation: before any
// workspace upload or checkpoint write, the gateway reserves the
// declared incoming byte count against the tenant's storageQuotaBytes
// limit, rejecting the request with STORAGE_QUOTA_EXCEEDED when the
// projected usage would exceed it.
//
// The in-memory Counter here backs tests and the minimal gateway;
// storagequota/redisstore is the cross-replica Redis implementation
// whose reservation is atomic via a Lua script.
package storagequota

import (
	"context"
	"errors"
	"sync"
)

// ErrQuotaExceeded — the reservation would push the tenant's
// reserved-plus-committed bytes past its storage quota.
var ErrQuotaExceeded = errors.New("storagequota: tenant storage quota exceeded")

// Counter is the §11.2 per-tenant storage byte counter.
type Counter interface {
	// Reserve atomically reserves incoming bytes against the tenant's
	// quota. It returns the tenant's usage as it stood before the
	// reservation so the caller can cap the inbound stream at the
	// headroom (limit minus priorUsed). ErrQuotaExceeded is returned,
	// and nothing reserved, when priorUsed plus incoming exceeds limit.
	Reserve(ctx context.Context, tenantID string, incoming, limit int64) (priorUsed int64, err error)

	// Adjust shifts the tenant's counter by delta, never below zero. A
	// negative delta releases a reservation (a failed upload) or
	// reconciles an upload that wrote fewer bytes than were declared.
	Adjust(ctx context.Context, tenantID string, delta int64) error

	// Used returns the tenant's current reserved-plus-committed bytes.
	Used(ctx context.Context, tenantID string) (int64, error)
}

// Memory is the in-memory Counter. The byte total per tenant is held
// in a map guarded by a mutex.
type Memory struct {
	mu    sync.Mutex
	bytes map[string]int64
}

// NewMemory returns an empty in-memory storage counter.
func NewMemory() *Memory {
	return &Memory{bytes: map[string]int64{}}
}

// Reserve implements Counter.
func (m *Memory) Reserve(_ context.Context, tenantID string, incoming, limit int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prior := m.bytes[tenantID]
	if prior+incoming > limit {
		return prior, ErrQuotaExceeded
	}
	m.bytes[tenantID] = prior + incoming
	return prior, nil
}

// Adjust implements Counter.
func (m *Memory) Adjust(_ context.Context, tenantID string, delta int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	next := m.bytes[tenantID] + delta
	if next < 0 {
		next = 0
	}
	m.bytes[tenantID] = next
	return nil
}

// Used implements Counter.
func (m *Memory) Used(_ context.Context, tenantID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bytes[tenantID], nil
}

var _ Counter = (*Memory)(nil)
