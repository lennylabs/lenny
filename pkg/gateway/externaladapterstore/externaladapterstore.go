// SPDX-License-Identifier: MIT

// Package externaladapterstore is the §15.1 / §15.4 external-protocol
// adapter registry. It backs the admin CRUD endpoints at
// `/v1/admin/external-adapters` (spec: §15.1 lines 850-855) and the
// §24.8 `validate` registration gate.
//
// Per §15 line 10 the ExternalAdapterRegistry routes client-facing
// traffic to simultaneously-active protocol adapters (MCP, OpenAI
// Completions, Open Responses, and third-party adapters) by path
// prefix. Per §15 line 1414 a newly registered adapter is created in
// `pending_validation` status and does not receive traffic until
// `POST /v1/admin/external-adapters/{name}/validate` runs the
// conformance suite and transitions it to `active`. Adapters in
// `pending_validation` or `validation_failed` are excluded from all
// traffic routing — this store's Status field is the machine-enforceable
// gate the registry consults.
//
// External adapters are platform-global like §5.1 runtimes: no
// tenant_id, no RLS. The records are keyed by name.
package externaladapterstore

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"sync"
	"time"
)

// Status is the §15 line 1414 adapter lifecycle state.
type Status string

const (
	// StatusPendingValidation is the state a newly registered adapter
	// starts in. It receives no traffic until validated.
	StatusPendingValidation Status = "pending_validation"
	// StatusActive is reached after the validate suite passes; the
	// adapter is eligible for traffic routing.
	StatusActive Status = "active"
	// StatusValidationFailed is reached when the validate suite finds
	// one or more conformance violations. The adapter receives no
	// traffic; the per-test failure details are recorded on the record.
	StatusValidationFailed Status = "validation_failed"
)

// IsValid reports whether s is one of the three documented statuses.
func (s Status) IsValid() bool {
	return s == StatusPendingValidation || s == StatusActive || s == StatusValidationFailed
}

// ExternalAdapter is one registered external protocol adapter record.
type ExternalAdapter struct {
	// Name is the §15.1 registry key, unique platform-wide.
	Name string

	// DisplayName is the human-facing label.
	DisplayName string

	// Protocol names the wire protocol the adapter speaks, e.g.
	// `mcp`, `a2a`, `openai-completions`, `open-responses`. Advisory
	// metadata; the registry routes by PathPrefix.
	Protocol string

	// PathPrefix is the §15 line 10 routing prefix this adapter claims
	// (e.g. `/a2a`). Active adapters route traffic arriving under this
	// prefix.
	PathPrefix string

	// BinaryPath is the adapter implementation the §24.8 conformance
	// suite drives over the §15.4 stdin/stdout JSONL protocol. The
	// validate handler runs the suite against this binary.
	BinaryPath string

	// Level is the §15.4.3 integration level the adapter declares
	// (`basic`, `standard`, or `full`). The validate suite runs the
	// matching battery.
	Level string

	// Status is the §15 line 1414 gate state. Always set by the store
	// (Create forces pending_validation); never accepted from the wire.
	Status Status

	// LastValidation records the result of the most recent validate
	// run, including per-test failure details when Status is
	// validation_failed. Nil until the first validate.
	LastValidation *ValidationReport

	CreatedAt time.Time
	UpdatedAt time.Time
}

// ValidationReport is the per-test outcome of a validate run, persisted
// on the adapter record and returned on the validate wire response.
// spec: §24.8 line 113 ("validation report cites the specific schema
// assertion that failed"); §15 line 1414 ("per-test failure details").
type ValidationReport struct {
	Level       string
	Total       int
	Passed      int
	Failed      int
	Failures    []ValidationFailure
	ValidatedAt time.Time
}

// ValidationFailure is one failed conformance check.
type ValidationFailure struct {
	Name   string
	Spec   string
	Detail string
}

// Store is the external-adapter registry contract.
type Store interface {
	Create(ctx context.Context, a ExternalAdapter) error
	Get(ctx context.Context, name string) (ExternalAdapter, error)
	List(ctx context.Context) ([]ExternalAdapter, error)
	Update(ctx context.Context, name string, mutate func(*ExternalAdapter) error) (ExternalAdapter, error)
	Delete(ctx context.Context, name string) error
}

// Sentinel errors.
var (
	ErrNotFound      = errors.New("externaladapterstore: external adapter not found")
	ErrAlreadyExists = errors.New("externaladapterstore: external adapter already exists")
)

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

// ValidateName reports whether name satisfies the registry-key format.
func ValidateName(name string) error {
	if name == "" {
		return errors.New("externaladapterstore: name is required")
	}
	if !namePattern.MatchString(name) {
		return errors.New(`externaladapterstore: name must match ^[a-z0-9][a-z0-9_-]{0,127}$`)
	}
	return nil
}

// Validate runs the cross-field admission rules on a record. Used at
// the admin Create / Update boundary.
func Validate(a ExternalAdapter) error {
	if err := ValidateName(a.Name); err != nil {
		return err
	}
	if a.BinaryPath == "" {
		return errors.New("externaladapterstore: binaryPath is required (the §15.4 adapter under test)")
	}
	switch a.Level {
	case "basic", "standard", "full":
	case "":
		return errors.New("externaladapterstore: level is required (basic|standard|full)")
	default:
		return errors.New(`externaladapterstore: level must be one of basic|standard|full`)
	}
	return nil
}

// Memory is an in-memory Store. Production deployments back the registry
// with Postgres (the documented seam, mirroring runtimestore/pgstore);
// the in-memory implementation is the Embedded-Mode and unit-test store.
type Memory struct {
	mu   sync.RWMutex
	rows map[string]ExternalAdapter
}

// NewMemory returns an empty in-memory Store.
func NewMemory() *Memory {
	return &Memory{rows: map[string]ExternalAdapter{}}
}

// Create inserts a new adapter. The status is always forced to
// pending_validation regardless of the supplied value (§15 line 1414).
func (m *Memory) Create(_ context.Context, a ExternalAdapter) error {
	if err := Validate(a); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rows[a.Name]; ok {
		return ErrAlreadyExists
	}
	a.Status = StatusPendingValidation
	a.LastValidation = nil
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	a.UpdatedAt = a.CreatedAt
	m.rows[a.Name] = a
	return nil
}

// Get returns the adapter by name.
func (m *Memory) Get(_ context.Context, name string) (ExternalAdapter, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.rows[name]
	if !ok {
		return ExternalAdapter{}, ErrNotFound
	}
	return a, nil
}

// List returns all adapters sorted by name.
func (m *Memory) List(_ context.Context) ([]ExternalAdapter, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ExternalAdapter, 0, len(m.rows))
	for _, a := range m.rows {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Update applies mutate to a copy of the stored record and persists the
// result. The Name and CreatedAt fields are preserved; UpdatedAt is
// stamped. The mutator runs under the store lock.
func (m *Memory) Update(_ context.Context, name string, mutate func(*ExternalAdapter) error) (ExternalAdapter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.rows[name]
	if !ok {
		return ExternalAdapter{}, ErrNotFound
	}
	if err := mutate(&a); err != nil {
		return ExternalAdapter{}, err
	}
	a.Name = name
	a.UpdatedAt = time.Now().UTC()
	m.rows[name] = a
	return a, nil
}

// Delete removes the adapter by name.
func (m *Memory) Delete(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rows[name]; !ok {
		return ErrNotFound
	}
	delete(m.rows, name)
	return nil
}

var _ Store = (*Memory)(nil)
