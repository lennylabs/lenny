// SPDX-License-Identifier: MIT

// Package runtimecapoverride is the §5.1 line 49 per-tenant runtime
// capability customization store and the tenant-aware runtime resolver
// that overlays a tenant's override onto the platform-default runtime.
//
// A row is keyed by (tenant_id, runtime_name): a tenant customizes a
// subset of a platform-global runtime's §5.1 capabilities block for its
// own sessions without affecting the runtime for other tenants. The
// resolver (ResolveForTenant) is the tenant-aware companion to
// runtimestore.Resolve: it resolves the effective runtime (merging a
// derived runtime against its base) and then applies the tenant's
// override on top.
//
// spec: §5.1 line 49 — F-5.1.20.
package runtimecapoverride

import (
	"context"
	"sync"

	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// Store persists per-tenant runtime capability overrides keyed by
// (tenantID, runtimeName).
type Store interface {
	// Get returns the override for (tenantID, runtime). The bool is false
	// when no override is recorded.
	Get(ctx context.Context, tenantID, runtime string) (runtimestore.CapabilityOverride, bool, error)

	// Put upserts the override for (tenantID, runtime).
	Put(ctx context.Context, tenantID, runtime string, o runtimestore.CapabilityOverride) error

	// Delete removes the override for (tenantID, runtime). Deleting a
	// missing override is not an error.
	Delete(ctx context.Context, tenantID, runtime string) error

	// List returns every override scoped to tenantID, keyed by runtime
	// name.
	List(ctx context.Context, tenantID string) (map[string]runtimestore.CapabilityOverride, error)
}

// Memory is the in-memory Store. Overrides are held in a per-tenant map
// guarded by a mutex; every read and write deep-copies so the store
// never shares pointer or slice state with a caller.
type Memory struct {
	mu sync.RWMutex
	// byTenant maps tenantID -> runtimeName -> override.
	byTenant map[string]map[string]runtimestore.CapabilityOverride
}

// NewMemory returns an empty in-memory Store.
func NewMemory() *Memory {
	return &Memory{byTenant: map[string]map[string]runtimestore.CapabilityOverride{}}
}

var _ Store = (*Memory)(nil)

// Get returns the override for (tenantID, runtime).
func (m *Memory) Get(_ context.Context, tenantID, runtime string) (runtimestore.CapabilityOverride, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	o, ok := m.byTenant[tenantID][runtime]
	if !ok {
		return runtimestore.CapabilityOverride{}, false, nil
	}
	return o.Clone(), true, nil
}

// Put upserts the override for (tenantID, runtime).
func (m *Memory) Put(_ context.Context, tenantID, runtime string, o runtimestore.CapabilityOverride) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.byTenant[tenantID] == nil {
		m.byTenant[tenantID] = map[string]runtimestore.CapabilityOverride{}
	}
	m.byTenant[tenantID][runtime] = o.Clone()
	return nil
}

// Delete removes the override for (tenantID, runtime).
func (m *Memory) Delete(_ context.Context, tenantID, runtime string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.byTenant[tenantID], runtime)
	if len(m.byTenant[tenantID]) == 0 {
		delete(m.byTenant, tenantID)
	}
	return nil
}

// List returns every override scoped to tenantID.
func (m *Memory) List(_ context.Context, tenantID string) (map[string]runtimestore.CapabilityOverride, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]runtimestore.CapabilityOverride, len(m.byTenant[tenantID]))
	for name, o := range m.byTenant[tenantID] {
		out[name] = o.Clone()
	}
	return out, nil
}

// ResolveForTenant resolves the effective runtime registered under name
// (runtimestore.Resolve semantics) and overlays the tenant's §5.1
// capability override on top. When the override store is nil or the
// tenant has no override for the runtime, the resolved runtime is
// returned unchanged.
//
// The overlay is best-effort: an override-store read error is swallowed
// and the un-overlaid runtime is returned, matching the degrade-safe
// posture of the §5.1 capability consumers (a registry or override-store
// blip never blocks a session operation). The runtime-registry error
// from Resolve is propagated as before.
//
// spec: §5.1 line 49 — F-5.1.20.
func ResolveForTenant(ctx context.Context, runtimes runtimestore.Store, overrides Store, tenantID, name string) (runtimestore.Runtime, error) {
	rt, err := runtimestore.Resolve(ctx, runtimes, name)
	if err != nil {
		return runtimestore.Runtime{}, err
	}
	if overrides == nil || tenantID == "" {
		return rt, nil
	}
	o, ok, gerr := overrides.Get(ctx, tenantID, name)
	if gerr != nil || !ok {
		return rt, nil
	}
	return runtimestore.ApplyCapabilityOverride(rt, o), nil
}
