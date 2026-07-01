// SPDX-License-Identifier: MIT

// Package environmentstore is the §10.6 Environment registry: the
// per-tenant Environment resource behind the /v1/admin/environments
// admin API.
//
// An Environment is a named, RBAC-governed project context that
// groups runtimes and connectors for a team. The pkg/environment
// package supplies the §10.6 primitives this resource composes: the
// Role enum and the tag-based Selector evaluator.
package environmentstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
)

// Identity names a §10.6 environment member: an OIDC group or other
// identity principal.
type Identity struct {
	// Type is the §10.6 identity type, e.g. "oidc-group".
	Type string
	// Value is the identity itself, e.g. the OIDC group name.
	Value string
}

// Member binds an identity to a §10.6 role within the environment.
type Member struct {
	Identity Identity
	Role     environment.Role
}

// MCPRuntimeFilter is a §10.6 capability-based tool filter applied to
// the `type: mcp` runtimes the RuntimeSelector admits.
type MCPRuntimeFilter struct {
	RuntimeSelector     environment.Selector
	AllowedCapabilities []string
	DeniedCapabilities  []string
}

// ConnectorSelector is the §10.6 connectorSelector: a tag-based
// connector selection plus the capability allow/deny lists that decide
// what the selected connectors may do. The Selector chooses which
// connectors are in scope; AllowedCapabilities and DeniedCapabilities
// decide what those connectors may do, parallel to MCPRuntimeFilter for
// `type: mcp` runtimes. spec: §10.6 lines 595-599. F-10.6.3.
type ConnectorSelector struct {
	Selector            environment.Selector
	AllowedCapabilities []string
	DeniedCapabilities  []string
}

// CrossEnvRule is one §10.6 bilateral cross-environment-delegation
// declaration. For an outbound rule Environment is the target; for an
// inbound rule it is the permitted source ("*" matches any source).
type CrossEnvRule struct {
	Environment string
	Runtimes    environment.Selector
}

// Environment is the §10.6 Environment resource.
type Environment struct {
	// Name is the §10.6 identifier and the per-tenant key.
	Name string
	// TenantID scopes the environment.
	TenantID string
	// Description is the human-facing label.
	Description string
	// WorkspaceTier is the §12.9 line 1033 environment-level
	// data-classification override. Empty inherits the tenant's tier.
	// A non-empty value must be a §12.9 tenant-settable tier (T3 or T4)
	// and may only tighten the tenant's tier, never loosen it; the
	// stricter-only relation is enforced at the admin handler, which has
	// the parent tenant in scope. Validate rejects only an out-of-enum
	// value here. spec: §12.9 line 1033.
	WorkspaceTier string
	// Members is the §10.6 RBAC member list.
	Members []Member
	// RuntimeSelector is the §10.6 tag-based runtime selection.
	RuntimeSelector environment.Selector
	// MCPRuntimeFilters are the §10.6 capability filters for mcp runtimes.
	MCPRuntimeFilters []MCPRuntimeFilter
	// ConnectorSelector is the §10.6 internal connector selection plus
	// the capability allow/deny lists that govern what the selected
	// connectors may do.
	ConnectorSelector ConnectorSelector
	// DefaultDelegationPolicy names the DelegationPolicy applied to
	// sessions created in this environment.
	DefaultDelegationPolicy string
	// CrossEnvOutbound and CrossEnvInbound are the §10.6 bilateral
	// cross-environment-delegation declarations.
	CrossEnvOutbound []CrossEnvRule
	CrossEnvInbound  []CrossEnvRule
	// CreatedAt and UpdatedAt are audit timestamps.
	CreatedAt time.Time
	UpdatedAt time.Time

	// Version is the §15.1 ETag-based optimistic-concurrency counter: it
	// starts at 1 and increments on every successful admin Update. The
	// quoted decimal version is the resource's strong entity tag, enforced
	// on PUT via the If-Match precondition and exposed on GET/list.
	// spec: §15.1 lines 1207-1213.
	Version int64
}

// MaxDescriptionLen bounds the §10.6 environment Description so the
// admin API does not accept an unbounded human-facing label. 1 KiB is
// the same ceiling adopted for adjacent §10.6 free-text fields and
// fits the §10.6 example (`Security engineering workspace`) two orders
// of magnitude over. F-10.6.12.
const MaxDescriptionLen = 1024

// Validate reports the §10.6 admission errors for an Environment.
func (e Environment) Validate() error {
	var v []string
	if e.Name == "" {
		v = append(v, "name is required")
	}
	if e.TenantID == "" {
		v = append(v, "tenantId is required")
	}
	// spec: §10.6 lines 562-565 — description is a human-facing label;
	// bound it so a misuse cannot deposit an unbounded blob into the
	// registry. F-10.6.12.
	if n := len(e.Description); n > MaxDescriptionLen {
		v = append(v, fmt.Sprintf("description: %d bytes exceeds the §10.6 cap of %d", n, MaxDescriptionLen))
	}
	// spec: §12.9 line 1033 — an environment-level workspaceTier override
	// must be a tenant-settable §12.9 tier (T3 or T4); empty inherits the
	// tenant tier. The stricter-only relation against the tenant tier is
	// enforced at the admin handler, which resolves the parent tenant.
	if !tenantstore.ValidWorkspaceTier(e.WorkspaceTier) {
		v = append(v, fmt.Sprintf("workspaceTier %q is not a valid §12.9 tier (T3 or T4)", e.WorkspaceTier))
	}
	seen := map[string]bool{}
	for i, m := range e.Members {
		if m.Identity.Value == "" {
			v = append(v, fmt.Sprintf("members[%d].identity.value is required", i))
		}
		if !m.Role.IsValid() {
			v = append(v, fmt.Sprintf("members[%d].role %q is not a valid §10.6 role", i, m.Role))
		}
		k := m.Identity.Type + "/" + m.Identity.Value
		if m.Identity.Value != "" && seen[k] {
			v = append(v, fmt.Sprintf("members[%d] duplicates identity %q", i, m.Identity.Value))
		}
		seen[k] = true
	}
	if err := e.RuntimeSelector.Validate(); err != nil {
		v = append(v, "runtimeSelector: "+err.Error())
	}
	if err := e.ConnectorSelector.Selector.Validate(); err != nil {
		v = append(v, "connectorSelector: "+err.Error())
	}
	for _, ce := range validateCapabilityLists(e.ConnectorSelector.AllowedCapabilities, e.ConnectorSelector.DeniedCapabilities) {
		v = append(v, "connectorSelector."+ce)
	}
	for i, f := range e.MCPRuntimeFilters {
		if err := f.RuntimeSelector.Validate(); err != nil {
			v = append(v, fmt.Sprintf("mcpRuntimeFilters[%d].runtimeSelector: %v", i, err))
		}
		for _, ce := range validateCapabilityLists(f.AllowedCapabilities, f.DeniedCapabilities) {
			v = append(v, fmt.Sprintf("mcpRuntimeFilters[%d].%s", i, ce))
		}
	}
	for i, r := range e.CrossEnvOutbound {
		// spec: §10.6 lines 613-625 — the outbound declaration names a
		// literal target environment. The "*" wildcard is described
		// only for inbound rules; admitting it outbound would create a
		// silent no-op (the envaccess outboundPermits matcher compares
		// rule.Environment to a literal target environment name and
		// would never hit). Reject it at admission so admins reading
		// the inbound wildcard example and generalising to outbound
		// fail loudly rather than write a rule that never matches.
		// F-10.6.13.
		if r.Environment == "*" {
			v = append(v, fmt.Sprintf("crossEnvironmentDelegation.outbound[%d].targetEnvironment: %q wildcard is not supported (use literal target environment names)", i, r.Environment))
		}
		if strings.TrimSpace(r.Environment) == "" {
			v = append(v, fmt.Sprintf("crossEnvironmentDelegation.outbound[%d].targetEnvironment is required", i))
		}
		if err := r.Runtimes.Validate(); err != nil {
			v = append(v, fmt.Sprintf("crossEnvironmentDelegation.outbound[%d].runtimes: %v", i, err))
		}
	}
	for i, r := range e.CrossEnvInbound {
		// spec: §10.6 line 619 — inbound rules accept "*" wildcard
		// for sourceEnvironment. Any other empty value is a
		// malformed rule. F-10.6.13.
		if strings.TrimSpace(r.Environment) == "" {
			v = append(v, fmt.Sprintf("crossEnvironmentDelegation.inbound[%d].sourceEnvironment is required (use %q for any source)", i, "*"))
		}
		if err := r.Runtimes.Validate(); err != nil {
			v = append(v, fmt.Sprintf("crossEnvironmentDelegation.inbound[%d].runtimes: %v", i, err))
		}
	}
	if len(v) == 0 {
		return nil
	}
	return fmt.Errorf("environmentstore: %s: %s", e.Name, strings.Join(v, "; "))
}

// validateCapabilityLists reports the §10.6 admission errors for a
// connectorSelector or mcpRuntimeFilter capability allow/deny pair: a
// capability name must be non-empty, must not repeat within its list,
// and must not appear in both the allowed and denied lists. Names are
// validated loosely — the §10.6 line 665 capability taxonomy is
// extensible per tenant (platform defaults plus tenant-custom) and the
// §10.6 connectorSelector example itself names `search`, which is not a
// §5.3 tool capability — so membership in the closed §5.3 enum is not
// required. spec: §10.6 lines 595-599, line 665. F-10.6.3.
func validateCapabilityLists(allowed, denied []string) []string {
	var errs []string
	seenAllowed := map[string]bool{}
	for i, c := range allowed {
		if strings.TrimSpace(c) == "" {
			errs = append(errs, fmt.Sprintf("allowedCapabilities[%d] must not be empty", i))
			continue
		}
		if seenAllowed[c] {
			errs = append(errs, fmt.Sprintf("allowedCapabilities[%d] duplicates %q", i, c))
		}
		seenAllowed[c] = true
	}
	seenDenied := map[string]bool{}
	for i, c := range denied {
		if strings.TrimSpace(c) == "" {
			errs = append(errs, fmt.Sprintf("deniedCapabilities[%d] must not be empty", i))
			continue
		}
		if seenDenied[c] {
			errs = append(errs, fmt.Sprintf("deniedCapabilities[%d] duplicates %q", i, c))
		}
		seenDenied[c] = true
		if seenAllowed[c] {
			errs = append(errs, fmt.Sprintf("deniedCapabilities[%d] %q also appears in allowedCapabilities", i, c))
		}
	}
	return errs
}

// PermitCapabilities reports whether a tool whose inferred §5.1
// capability set is toolCaps is permitted by an allowed/denied
// capability filter. The semantics follow the §10.6 example
// (allowedCapabilities: [read, execute], deniedCapabilities: [write,
// delete, admin]):
//
//   - A tool is denied when any of its capabilities appears in denied.
//   - When allowed is non-empty, every capability of the tool must
//     appear in allowed; a tool carrying a capability outside the
//     allow-list is denied.
//   - An empty allowed list imposes no allow-list restriction.
//
// A tool with no inferred capabilities is permitted (there is nothing
// to deny and the allow-list is vacuously satisfied); callers that want
// an unknown tool to fail closed substitute the §5.1 conservative admin
// default before calling. blockedBy names the first capability that
// triggered the denial, for the rejection message; it is empty when
// permitted. spec: §10.6 lines 588-607.
func PermitCapabilities(toolCaps, allowed, denied []string) (permitted bool, blockedBy string) {
	deny := map[string]bool{}
	for _, c := range denied {
		deny[c] = true
	}
	for _, c := range toolCaps {
		if deny[c] {
			return false, c
		}
	}
	if len(allowed) == 0 {
		return true, ""
	}
	allow := map[string]bool{}
	for _, c := range allowed {
		allow[c] = true
	}
	for _, c := range toolCaps {
		if !allow[c] {
			return false, c
		}
	}
	return true, ""
}

// PermitTool reports whether a tool with the inferred capability set
// toolCaps is permitted by this connectorSelector's capability filter.
// spec: §10.6 lines 595-599.
func (cs ConnectorSelector) PermitTool(toolCaps []string) (bool, string) {
	return PermitCapabilities(toolCaps, cs.AllowedCapabilities, cs.DeniedCapabilities)
}

// Admits reports whether the connector identified by id with the given
// labels is in scope of this connectorSelector's tag selector. The
// capability filter governs only connectors the selector admits.
func (cs ConnectorSelector) Admits(id string, labels map[string]string) bool {
	return cs.Selector.Matches(environment.Candidate{Name: id, Labels: labels})
}

// PermitTool reports whether a tool with the inferred capability set
// toolCaps is permitted by this mcpRuntimeFilter's capability filter.
// spec: §10.6 lines 588-593, line 607.
func (f MCPRuntimeFilter) PermitTool(toolCaps []string) (bool, string) {
	return PermitCapabilities(toolCaps, f.AllowedCapabilities, f.DeniedCapabilities)
}

// Admits reports whether the runtime identified by name/typ with the
// given labels is in scope of this filter's runtimeSelector.
func (f MCPRuntimeFilter) Admits(name, typ string, labels map[string]string) bool {
	return f.RuntimeSelector.Matches(environment.Candidate{Name: name, Type: typ, Labels: labels})
}

// MCPRuntimeFilterFor returns the first §10.6 mcpRuntimeFilter whose
// runtimeSelector admits the runtime identified by name/typ/labels, and
// whether such a filter exists. A runtime that no filter admits has no
// capability restriction from this environment. spec: §10.6 line 607.
func (e Environment) MCPRuntimeFilterFor(name, typ string, labels map[string]string) (MCPRuntimeFilter, bool) {
	for _, f := range e.MCPRuntimeFilters {
		if f.Admits(name, typ, labels) {
			return f, true
		}
	}
	return MCPRuntimeFilter{}, false
}

// Sentinel errors.
var (
	// ErrNotFound — no environment with the requested (tenant, name).
	ErrNotFound = errors.New("environmentstore: environment not found")
	// ErrAlreadyExists — a (tenant, name) environment already exists.
	ErrAlreadyExists = errors.New("environmentstore: environment already exists")
)

// Store is the §10.6 Environment registry contract. Every method is
// goroutine-safe and tenant-scoped.
type Store interface {
	// Create persists a fresh environment. Returns ErrAlreadyExists
	// when the (tenant, name) pair is taken, or a validation error.
	Create(ctx context.Context, e Environment) error
	// Get returns the environment keyed by (tenantID, name). Returns
	// ErrNotFound when no matching row exists.
	Get(ctx context.Context, tenantID, name string) (Environment, error)
	// Update applies mutate, re-validates, and advances UpdatedAt.
	// Returns ErrNotFound when the row is missing.
	Update(ctx context.Context, tenantID, name string, mutate func(*Environment) error) (Environment, error)
	// List returns the tenant's environments, name-ascending.
	List(ctx context.Context, tenantID string) ([]Environment, error)
	// Delete removes the environment. Returns ErrNotFound when missing.
	Delete(ctx context.Context, tenantID, name string) error
}

// Memory is the in-memory Store implementation.
type Memory struct {
	mu           sync.RWMutex
	environments map[string]Environment // keyed tenantID + "/" + name
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory { return &Memory{environments: map[string]Environment{}} }

func key(tenantID, name string) string { return tenantID + "/" + name }

// Create implements Store.
func (m *Memory) Create(_ context.Context, e Environment) error {
	if err := e.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(e.TenantID, e.Name)
	if _, ok := m.environments[k]; ok {
		return ErrAlreadyExists
	}
	now := time.Now().UTC()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now
	}
	if e.UpdatedAt.IsZero() {
		e.UpdatedAt = e.CreatedAt
	}
	// spec: §15.1 line 1207 — a new resource is born at version 1.
	if e.Version == 0 {
		e.Version = 1
	}
	m.environments[k] = clone(e)
	return nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, tenantID, name string) (Environment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.environments[key(tenantID, name)]
	if !ok {
		return Environment{}, ErrNotFound
	}
	return clone(e), nil
}

// Update implements Store.
func (m *Memory) Update(_ context.Context, tenantID, name string, mutate func(*Environment) error) (Environment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(tenantID, name)
	e, ok := m.environments[k]
	if !ok {
		return Environment{}, ErrNotFound
	}
	e = clone(e)
	if err := mutate(&e); err != nil {
		return Environment{}, err
	}
	if err := e.Validate(); err != nil {
		return Environment{}, err
	}
	e.UpdatedAt = time.Now().UTC()
	// spec: §15.1 line 1207 — bump the entity-tag version on every write.
	e.Version++
	m.environments[k] = clone(e)
	return clone(e), nil
}

// List implements Store.
func (m *Memory) List(_ context.Context, tenantID string) ([]Environment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Environment
	for _, e := range m.environments {
		if e.TenantID == tenantID {
			out = append(out, clone(e))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Delete implements Store.
func (m *Memory) Delete(_ context.Context, tenantID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(tenantID, name)
	if _, ok := m.environments[k]; !ok {
		return ErrNotFound
	}
	delete(m.environments, k)
	return nil
}

// DeleteByUser implements the §12.1 mandatory-erasure interface.
// Environments are tenant-scoped and do not record user-owned rows;
// DeleteByUser is therefore a no-op that returns 0 erased rows.
func (m *Memory) DeleteByUser(_ context.Context, _, _ string) (int, error) {
	return 0, nil
}

// DeleteByTenant implements the §12.1 mandatory-erasure interface.
// Removes every environment belonging to tenantID.
func (m *Memory) DeleteByTenant(_ context.Context, tenantID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := tenantID + "/"
	n := 0
	for k := range m.environments {
		if strings.HasPrefix(k, prefix) {
			delete(m.environments, k)
			n++
		}
	}
	return n, nil
}

// clone deep-copies an Environment so a stored value and a returned
// copy never share the nested member, selector, or cross-environment
// slices and maps.
func clone(e Environment) Environment {
	b, err := json.Marshal(e)
	if err != nil {
		return e
	}
	var c Environment
	if err := json.Unmarshal(b, &c); err != nil {
		return e
	}
	return c
}
