// SPDX-License-Identifier: MIT

// Package experimentstore is the §10.7 experiment registry: the
// per-tenant ExperimentDefinition resource behind the
// /v1/admin/experiments admin API.
//
// An Experiment is the full §10.7 resource, including each variant's
// runtime and pool mapping; experiment.Definition is the validated
// subset the experiment package checks at admission.
package experimentstore

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/experiment"
)

// Variant is one entry in the §10.7 experiment variants list.
type Variant struct {
	// ID is the variant identifier; cannot equal
	// experiment.ControlVariantID.
	ID string
	// Runtime is the runtime version this variant routes to.
	Runtime string
	// Pool is the warm pool this variant's sessions claim.
	Pool string
	// Weight is the traffic fraction routed to this variant, in (0, 1).
	Weight float64
	// InitialMinWarm is the §10.7 bootstrap-mode static minWarm floor.
	InitialMinWarm int
}

// Experiment is the §10.7 ExperimentDefinition resource.
type Experiment struct {
	// ID is the §10.7 experiment identifier and the per-tenant key.
	ID string
	// TenantID scopes the experiment; §10.7 experiments are per-tenant.
	TenantID string
	// Status is the §10.7 lifecycle state.
	Status experiment.Status
	// BaseRuntime is the control-group runtime.
	BaseRuntime string
	// Variants is the §10.7 variant list.
	Variants []Variant
	// TargetingMode, Sticky, and Propagation are the §10.7 targeting
	// and child-propagation policy.
	TargetingMode experiment.TargetingMode
	Sticky        experiment.Sticky
	Propagation   experiment.Propagation
	// CreatedAt and UpdatedAt are audit timestamps.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Definition projects the Experiment onto the experiment.Definition
// subset the experiment package validates and routes over.
func (e Experiment) Definition() experiment.Definition {
	vs := make([]experiment.Variant, len(e.Variants))
	for i, v := range e.Variants {
		vs[i] = experiment.Variant{ID: v.ID, Weight: v.Weight}
	}
	return experiment.Definition{
		ID:            e.ID,
		Status:        e.Status,
		BaseRuntime:   e.BaseRuntime,
		Variants:      vs,
		TargetingMode: e.TargetingMode,
		Sticky:        e.Sticky,
		Propagation:   e.Propagation,
	}
}

// Validate runs the §10.7 admission validation: variant weights, the
// reserved "control" identifier, and the enum fields. Cross-resource
// checks (variant runtime/pool existence, isolation monotonicity) are
// not covered here.
func (e Experiment) Validate() error { return e.Definition().Validate() }

// Sentinel errors.
var (
	// ErrNotFound — no experiment with the requested (tenant, id).
	ErrNotFound = errors.New("experimentstore: experiment not found")
	// ErrAlreadyExists — a (tenant, id) experiment already exists.
	ErrAlreadyExists = errors.New("experimentstore: experiment already exists")
)

// Store is the §10.7 experiment registry contract. Every method is
// goroutine-safe and tenant-scoped.
type Store interface {
	// Create persists a fresh experiment. Returns ErrAlreadyExists when
	// the (tenant, id) pair is taken, or a validation error.
	Create(ctx context.Context, e Experiment) error
	// Get returns the experiment keyed by (tenantID, id). Returns
	// ErrNotFound when no matching row exists.
	Get(ctx context.Context, tenantID, id string) (Experiment, error)
	// Update applies mutate to the experiment, re-validates the result,
	// and advances UpdatedAt. Returns ErrNotFound when the row is
	// missing.
	Update(ctx context.Context, tenantID, id string, mutate func(*Experiment) error) (Experiment, error)
	// List returns the tenant's experiments, id-ascending.
	List(ctx context.Context, tenantID string) ([]Experiment, error)
	// Delete removes the experiment. Returns ErrNotFound when missing.
	Delete(ctx context.Context, tenantID, id string) error
}

// Memory is the in-memory Store implementation.
type Memory struct {
	mu          sync.RWMutex
	experiments map[string]Experiment // keyed tenantID + "/" + id
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory { return &Memory{experiments: map[string]Experiment{}} }

func key(tenantID, id string) string { return tenantID + "/" + id }

// Create implements Store.
func (m *Memory) Create(_ context.Context, e Experiment) error {
	if err := e.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(e.TenantID, e.ID)
	if _, ok := m.experiments[k]; ok {
		return ErrAlreadyExists
	}
	now := time.Now().UTC()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now
	}
	if e.UpdatedAt.IsZero() {
		e.UpdatedAt = e.CreatedAt
	}
	m.experiments[k] = cloneExperiment(e)
	return nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, tenantID, id string) (Experiment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.experiments[key(tenantID, id)]
	if !ok {
		return Experiment{}, ErrNotFound
	}
	return cloneExperiment(e), nil
}

// Update implements Store.
func (m *Memory) Update(_ context.Context, tenantID, id string, mutate func(*Experiment) error) (Experiment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(tenantID, id)
	e, ok := m.experiments[k]
	if !ok {
		return Experiment{}, ErrNotFound
	}
	e = cloneExperiment(e)
	if err := mutate(&e); err != nil {
		return Experiment{}, err
	}
	if err := e.Validate(); err != nil {
		return Experiment{}, err
	}
	e.UpdatedAt = time.Now().UTC()
	m.experiments[k] = cloneExperiment(e)
	return cloneExperiment(e), nil
}

// List implements Store.
func (m *Memory) List(_ context.Context, tenantID string) ([]Experiment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Experiment
	for _, e := range m.experiments {
		if e.TenantID == tenantID {
			out = append(out, cloneExperiment(e))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Delete implements Store.
func (m *Memory) Delete(_ context.Context, tenantID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(tenantID, id)
	if _, ok := m.experiments[k]; !ok {
		return ErrNotFound
	}
	delete(m.experiments, k)
	return nil
}

// DeleteByUser implements the §12.2.1 mandatory-erasure interface.
// Experiment definitions are tenant-scoped and not user-owned;
// DeleteByUser is a no-op that returns 0 erased rows.
func (m *Memory) DeleteByUser(_ context.Context, _, _ string) (int, error) {
	return 0, nil
}

// DeleteByTenant implements the §12.2.1 mandatory-erasure interface.
// Removes every experiment definition belonging to tenantID.
func (m *Memory) DeleteByTenant(_ context.Context, tenantID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := tenantID + "/"
	n := 0
	for k := range m.experiments {
		if strings.HasPrefix(k, prefix) {
			delete(m.experiments, k)
			n++
		}
	}
	return n, nil
}

// cloneExperiment deep-copies the Variants slice so a stored
// experiment and a returned copy never share mutable state.
func cloneExperiment(e Experiment) Experiment {
	if e.Variants != nil {
		vs := make([]Variant, len(e.Variants))
		copy(vs, e.Variants)
		e.Variants = vs
	}
	return e
}
