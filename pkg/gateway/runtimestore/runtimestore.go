// SPDX-License-Identifier: MIT

// Package runtimestore is the §5.1 Runtime registry. It backs the
// platform-global `Runtime` records the gateway uses to resolve
// `runtimeRef` on session creation, and the §15.1 admin runtime CRUD
// endpoints.
//
// Per §5.1 / §10.6 runtimes are platform-global: no tenant_id, no
// RLS. The §15.1 admin handlers enforce per-tenant visibility via
// the `runtime_tenant_access` join table (out of scope for v1
// in-memory; production wires Postgres-backed access).
package runtimestore

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/capabilityinference"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// Runtime captures the §5.1 Runtime CRD shape. Only the v1 essential
// fields are modelled; extension fields (capabilities, providers,
// setup policy) are admitted but not strictly validated by this
// store — the admin handler runs the cross-field checks.
type Runtime struct {
	// Name is the §5.1 registry key. Required.
	Name string

	// Type discriminates between agent runtimes (the v1 default) and
	// `mcp` runtimes that speak MCP directly per §12b.
	Type RuntimeType

	// Image is the §5.1 image reference. v1 requires digest-pinned
	// references; the admin handler enforces the digest check.
	Image string

	// ExecutionMode is the §5.2 mode: `session`, `task`, or
	// `concurrent`.
	ExecutionMode ExecutionMode

	// IsolationProfile is the §5.3 profile this runtime defaults to.
	IsolationProfile isolation.Profile

	// IntegrationLevel is the §15.4.3 conformance level: `basic`,
	// `standard`, or `full`.
	IntegrationLevel IntegrationLevel

	// Description is an admin-facing description.
	Description string

	// DelegationPolicyRef names the §8.3 DelegationPolicy that scopes
	// delegations originating from this runtime. Empty when the
	// runtime has no runtime-level delegation policy.
	DelegationPolicyRef string

	// Labels is the §5.1 label set. §5.1 requires labels from v1 as the
	// primary mechanism for environment runtimeSelector matching
	// (§10.6). A nil or empty map means the runtime carries no labels.
	Labels map[string]string

	// AgentInterface is the optional §5.1 agentInterface descriptor. It
	// is nil for type:mcp runtimes and for type:agent runtimes that omit
	// the block.
	AgentInterface *AgentInterface

	// PublishedMetadata is the §5.1 publishedMetadata list — the
	// runtime's named, opaque metadata entries. A nil or empty slice
	// means the runtime publishes no metadata.
	PublishedMetadata []PublishedMetadataEntry

	// CapabilityInferenceMode is the §5.1 capabilityInferenceMode: it
	// sets the default §5.3 capability for an unannotated tool at
	// connector or type:mcp runtime registration. ApplyDefaults fills
	// the §5.1 default (strict) when it is empty.
	CapabilityInferenceMode capabilityinference.Mode

	// ToolCapabilityOverrides is the §5.1 toolCapabilityOverrides map:
	// an explicit §5.3 capability set per tool name that overrides MCP
	// annotation inference. A nil or empty map means every tool is
	// inferred.
	ToolCapabilityOverrides map[string][]capabilityinference.Capability

	// SetupPolicy is the §5.1 setupPolicy: the aggregate cap and
	// timeout disposition for the runtime's pod setup phase. It is nil
	// when the runtime declares no setup policy.
	SetupPolicy *SetupPolicy

	// Capabilities is the §5.1 capabilities block: the runtime's
	// interaction model and its mid-session injection support. It is
	// nil when the runtime declares no capabilities block.
	Capabilities *RuntimeCapabilities

	// MinPlatformVersion is the §5.1 minPlatformVersion: the lowest
	// Lenny gateway version the runtime supports. The gateway rejects
	// registration when its own version is below this. An empty value
	// declares no minimum.
	MinPlatformVersion string

	// CreatedAt / UpdatedAt are the audit timestamps.
	CreatedAt time.Time
	UpdatedAt time.Time

	// DeletedAt is the §15.1 soft-delete timestamp. The minimal
	// implementation refuses to register pods against soft-deleted
	// runtimes but keeps the row for audit.
	DeletedAt time.Time
}

// IsActive reports whether the runtime has not been soft-deleted.
func (r Runtime) IsActive() bool { return r.DeletedAt.IsZero() }

// InjectionSupported reports whether the runtime accepts §7.2
// mid-session message injection. Per §5.1 the default is false: a
// runtime must declare capabilities.injection.supported: true to
// accept injection.
func (r Runtime) InjectionSupported() bool {
	return r.Capabilities != nil && r.Capabilities.Injection.Supported
}

// RuntimeInteraction is the §5.1 capabilities.interaction enum: whether
// a runtime handles a single message or a multi-turn exchange.
type RuntimeInteraction string

const (
	// InteractionOneShot consumes one message and produces one response.
	InteractionOneShot RuntimeInteraction = "one_shot"
	// InteractionMultiTurn accepts repeated message delivery over a task.
	InteractionMultiTurn RuntimeInteraction = "multi_turn"
)

// AllRuntimeInteractions returns the closed enum.
func AllRuntimeInteractions() []RuntimeInteraction {
	return []RuntimeInteraction{InteractionOneShot, InteractionMultiTurn}
}

// IsValid reports whether i is a known interaction model.
func (i RuntimeInteraction) IsValid() bool {
	for _, v := range AllRuntimeInteractions() {
		if i == v {
			return true
		}
	}
	return false
}

// InjectionMode is one §5.1 capabilities.injection.modes entry.
type InjectionMode string

const (
	// InjectionImmediate interrupts a suspended session to deliver.
	InjectionImmediate InjectionMode = "immediate"
	// InjectionQueued buffers the message for the next runtime turn.
	InjectionQueued InjectionMode = "queued"
)

// AllInjectionModes returns the closed enum.
func AllInjectionModes() []InjectionMode {
	return []InjectionMode{InjectionImmediate, InjectionQueued}
}

// IsValid reports whether m is a known injection mode.
func (m InjectionMode) IsValid() bool {
	for _, v := range AllInjectionModes() {
		if m == v {
			return true
		}
	}
	return false
}

// InjectionCapability is the §5.1 capabilities.injection block: whether
// the runtime accepts mid-session message delivery and in which modes.
// Per §5.1 Supported defaults to false.
type InjectionCapability struct {
	Supported bool            `json:"supported"`
	Modes     []InjectionMode `json:"modes,omitempty"`
}

// RuntimeCapabilities is the §5.1 capabilities block on a runtime.
type RuntimeCapabilities struct {
	// Interaction is the runtime's §5.1 interaction model.
	Interaction RuntimeInteraction `json:"interaction,omitempty"`

	// Injection is the runtime's §5.1 mid-session injection support.
	Injection InjectionCapability `json:"injection,omitempty"`
}

// Clone returns a deep copy of the capabilities so the store never
// shares the Modes slice with a caller. A nil receiver clones to nil.
func (c *RuntimeCapabilities) Clone() *RuntimeCapabilities {
	if c == nil {
		return nil
	}
	cp := *c
	cp.Injection.Modes = append([]InjectionMode(nil), c.Injection.Modes...)
	return &cp
}

// AgentInterface is the optional §5.1 agentInterface descriptor declared
// on a type:agent runtime. It serves runtime discovery, A2A agent-card
// auto-generation, and adapter manifest summaries. type:mcp runtimes do
// not carry an agentInterface. The field names and JSON tags mirror the
// §5.1 YAML shape, which §15 names the normative contract for adapters
// that auto-generate native discovery formats.
type AgentInterface struct {
	// Description is the human-readable summary of the runtime's role.
	Description string `json:"description,omitempty"`

	// InputModes enumerates the media types the runtime accepts.
	InputModes []AgentInterfaceMode `json:"inputModes,omitempty"`

	// OutputModes enumerates the media types the runtime emits.
	OutputModes []AgentInterfaceMode `json:"outputModes,omitempty"`

	// SupportsWorkspaceFiles signals that the runtime honors workspace
	// files in TaskSpec, distinguishing a Lenny-internal runtime from an
	// external agent.
	SupportsWorkspaceFiles bool `json:"supportsWorkspaceFiles,omitempty"`

	// Skills enumerates the discrete capabilities the runtime advertises.
	Skills []AgentInterfaceSkill `json:"skills,omitempty"`

	// Examples provides worked usage samples.
	Examples []AgentInterfaceExample `json:"examples,omitempty"`
}

// AgentInterfaceMode is one entry in AgentInterface.InputModes or
// AgentInterface.OutputModes.
type AgentInterfaceMode struct {
	// Type is the IANA media type, for example "text/plain".
	Type string `json:"type"`

	// Role is an optional tag such as "primary". It is omitted when unset.
	Role string `json:"role,omitempty"`
}

// AgentInterfaceSkill is one entry in AgentInterface.Skills.
type AgentInterfaceSkill struct {
	// ID is the stable skill identifier, for example "review".
	ID string `json:"id"`

	// Name is the human-readable skill name.
	Name string `json:"name,omitempty"`

	// Description elaborates on what the skill does.
	Description string `json:"description,omitempty"`
}

// AgentInterfaceExample is one entry in AgentInterface.Examples.
type AgentInterfaceExample struct {
	// Description explains what the example demonstrates.
	Description string `json:"description,omitempty"`

	// Input is the prompt or input text the example submits.
	Input string `json:"input,omitempty"`
}

// Clone returns a deep copy of the descriptor so the store never shares
// mutable slice state with a caller. A nil receiver clones to nil.
func (a *AgentInterface) Clone() *AgentInterface {
	if a == nil {
		return nil
	}
	cp := *a
	cp.InputModes = append([]AgentInterfaceMode(nil), a.InputModes...)
	cp.OutputModes = append([]AgentInterfaceMode(nil), a.OutputModes...)
	cp.Skills = append([]AgentInterfaceSkill(nil), a.Skills...)
	cp.Examples = append([]AgentInterfaceExample(nil), a.Examples...)
	return &cp
}

// MetadataVisibility is the §5.1 publishedMetadata visibility class. It
// governs which meta-fetch endpoint serves an entry and how the gateway
// filters it: a public entry needs no auth, an internal entry needs a
// valid session JWT, and a tenant entry is additionally scoped to the
// JWT's tenant.
type MetadataVisibility string

const (
	VisibilityInternal MetadataVisibility = "internal"
	VisibilityTenant   MetadataVisibility = "tenant"
	VisibilityPublic   MetadataVisibility = "public"
)

// AllMetadataVisibilities returns the closed enum.
func AllMetadataVisibilities() []MetadataVisibility {
	return []MetadataVisibility{VisibilityInternal, VisibilityTenant, VisibilityPublic}
}

// IsValid reports whether v is a known visibility class.
func (v MetadataVisibility) IsValid() bool {
	for _, known := range AllMetadataVisibilities() {
		if v == known {
			return true
		}
	}
	return false
}

// PublishedMetadataEntry is one §5.1 publishedMetadata entry on a
// runtime: a named, opaque value carrying a content type and a
// visibility class. §5.1 makes the gateway treat Content as opaque
// pass-through — it is stored and served without parsing or validation.
type PublishedMetadataEntry struct {
	// Key is the registration key, unique within a runtime's
	// publishedMetadata list (for example "agent-card").
	Key string `json:"key"`

	// ContentType is the IANA media type of Content (for example
	// "application/json").
	ContentType string `json:"contentType"`

	// Visibility is the entry's §5.1 visibility class.
	Visibility MetadataVisibility `json:"visibility"`

	// Content is the opaque entry value. The gateway never parses it.
	Content string `json:"content,omitempty"`
}

// PublishedMetadataRef is the §15 discovery-surface view of a
// publishedMetadata entry: the key, content type, and visibility class
// without the entry content. Discovery responses carry refs rather than
// content because the content (agent cards, OpenAPI specs) may be large
// and is fetched separately through the meta-fetch endpoint.
type PublishedMetadataRef struct {
	Key         string             `json:"key"`
	ContentType string             `json:"contentType"`
	Visibility  MetadataVisibility `json:"visibility"`
}

// Ref returns the discovery-surface ref for the entry, dropping Content.
func (e PublishedMetadataEntry) Ref() PublishedMetadataRef {
	return PublishedMetadataRef{Key: e.Key, ContentType: e.ContentType, Visibility: e.Visibility}
}

// PublicMetadataRefs returns the discovery-surface refs for the public
// entries in a publishedMetadata list. §15 discovery carries refs, not
// content; this is the unauthenticated subset, so internal and tenant
// entries are visibility-filtered out.
func PublicMetadataRefs(entries []PublishedMetadataEntry) []PublishedMetadataRef {
	var refs []PublishedMetadataRef
	for _, e := range entries {
		if e.Visibility == VisibilityPublic {
			refs = append(refs, e.Ref())
		}
	}
	return refs
}

// ValidatePublishedMetadata checks a §5.1 publishedMetadata list: every
// entry needs a non-empty key and a valid visibility class, and keys
// are unique within the list.
func ValidatePublishedMetadata(entries []PublishedMetadataEntry) error {
	seen := make(map[string]bool, len(entries))
	for i, e := range entries {
		if e.Key == "" {
			return fmt.Errorf("publishedMetadata[%d]: key must not be empty", i)
		}
		if seen[e.Key] {
			return fmt.Errorf("publishedMetadata: duplicate key %q", e.Key)
		}
		seen[e.Key] = true
		if !e.Visibility.IsValid() {
			return fmt.Errorf("publishedMetadata[%q]: %q is not a recognised visibility class", e.Key, e.Visibility)
		}
	}
	return nil
}

// SetupTimeoutDisposition is the §5.1 setupPolicy.onTimeout enum: the
// disposition when a runtime's pod setup phase exceeds its cap.
type SetupTimeoutDisposition string

const (
	// SetupTimeoutFail aborts pod startup when the setup cap is hit.
	SetupTimeoutFail SetupTimeoutDisposition = "fail"
	// SetupTimeoutWarn continues pod startup, logging a warning.
	SetupTimeoutWarn SetupTimeoutDisposition = "warn"
)

// AllSetupTimeoutDispositions returns the closed enum.
func AllSetupTimeoutDispositions() []SetupTimeoutDisposition {
	return []SetupTimeoutDisposition{SetupTimeoutFail, SetupTimeoutWarn}
}

// IsValid reports whether d is a known timeout disposition.
func (d SetupTimeoutDisposition) IsValid() bool {
	for _, v := range AllSetupTimeoutDispositions() {
		if d == v {
			return true
		}
	}
	return false
}

// SetupPolicy is the §5.1 setupPolicy block on a runtime: the aggregate
// cap on the pod setup phase and the disposition when the cap is hit.
type SetupPolicy struct {
	// TimeoutSeconds is the aggregate cap on the setup phase in
	// seconds. Zero means the runtime declares no aggregate cap.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`

	// OnTimeout is the disposition when the cap is exceeded. An empty
	// value is treated as the conservative "fail" default.
	OnTimeout SetupTimeoutDisposition `json:"onTimeout,omitempty"`
}

// Clone returns a copy of the policy so the store never shares the
// pointed-to struct with a caller. A nil receiver clones to nil.
func (p *SetupPolicy) Clone() *SetupPolicy {
	if p == nil {
		return nil
	}
	cp := *p
	return &cp
}

// RuntimeType is the §5.1 type discriminator.
type RuntimeType string

const (
	TypeAgent RuntimeType = "agent"
	TypeMCP   RuntimeType = "mcp"
)

// AllRuntimeTypes returns the closed enum.
func AllRuntimeTypes() []RuntimeType { return []RuntimeType{TypeAgent, TypeMCP} }

// IsValid reports whether t is a known runtime type.
func (t RuntimeType) IsValid() bool {
	for _, v := range AllRuntimeTypes() {
		if t == v {
			return true
		}
	}
	return false
}

// ExecutionMode is the §5.2 enum.
type ExecutionMode string

const (
	ExecutionModeSession    ExecutionMode = "session"
	ExecutionModeTask       ExecutionMode = "task"
	ExecutionModeConcurrent ExecutionMode = "concurrent"
)

// AllExecutionModes returns the closed enum.
func AllExecutionModes() []ExecutionMode {
	return []ExecutionMode{ExecutionModeSession, ExecutionModeTask, ExecutionModeConcurrent}
}

// IsValid reports whether m is a known execution mode.
func (m ExecutionMode) IsValid() bool {
	for _, v := range AllExecutionModes() {
		if m == v {
			return true
		}
	}
	return false
}

// IntegrationLevel is the §15.4.3 conformance enum.
type IntegrationLevel string

const (
	IntegrationLevelBasic    IntegrationLevel = "basic"
	IntegrationLevelStandard IntegrationLevel = "standard"
	IntegrationLevelFull     IntegrationLevel = "full"
)

// AllIntegrationLevels returns the closed enum.
func AllIntegrationLevels() []IntegrationLevel {
	return []IntegrationLevel{IntegrationLevelBasic, IntegrationLevelStandard, IntegrationLevelFull}
}

// IsValid reports whether l is a known integration level.
func (l IntegrationLevel) IsValid() bool {
	for _, v := range AllIntegrationLevels() {
		if l == v {
			return true
		}
	}
	return false
}

// Store is the §5.1 Runtime registry contract.
type Store interface {
	Create(ctx context.Context, r Runtime) error
	Get(ctx context.Context, name string) (Runtime, error)
	Update(ctx context.Context, name string, mutate func(*Runtime) error) (Runtime, error)
	List(ctx context.Context, filter ListFilter) ([]Runtime, error)
	SoftDelete(ctx context.Context, name string, at time.Time) error
}

// ListFilter narrows List results.
type ListFilter struct {
	// IncludeDeleted, when true, returns soft-deleted rows.
	IncludeDeleted bool

	// Type filters by RuntimeType. Empty returns every type.
	Type RuntimeType
}

// Sentinel errors.
var (
	ErrNotFound      = errors.New("runtimestore: runtime not found")
	ErrAlreadyExists = errors.New("runtimestore: runtime already exists")
)

// namePattern is the §5.1 registry-name format: DNS-label-like with
// dashes, underscores, and lowercase alphanumerics. Bounded at 128
// to match the §10.2 tenant-id format.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

// ValidateName reports whether name satisfies the §5.1 pattern.
func ValidateName(name string) error {
	if name == "" {
		return errors.New("runtimestore: name is required")
	}
	if !namePattern.MatchString(name) {
		return errors.New(`runtimestore: name must match ^[a-z0-9][a-z0-9_-]{0,127}$`)
	}
	return nil
}

// ApplyDefaults fills in the §5.1 default values for unset Runtime
// fields: type defaults to agent, execution mode to session, and
// isolation profile to the platform default. Integration level
// defaults to basic for agent runtimes only — §5.1 specifies that
// integrationLevel is meaningful solely on type: agent runtimes, so
// an mcp runtime keeps an empty integration level.
//
// Registration handlers call this at the admin-API boundary; the
// stores persist whatever they are given.
func ApplyDefaults(r *Runtime) {
	if r.Type == "" {
		r.Type = TypeAgent
	}
	if r.ExecutionMode == "" {
		r.ExecutionMode = ExecutionModeSession
	}
	if r.IsolationProfile == "" {
		r.IsolationProfile = isolation.Default()
	}
	if r.Type == TypeAgent && r.IntegrationLevel == "" {
		r.IntegrationLevel = IntegrationLevelBasic
	}
	if r.CapabilityInferenceMode == "" {
		r.CapabilityInferenceMode = capabilityinference.DefaultMode
	}
}

// cloneRuntime returns a deep copy of r. The Labels map, the
// AgentInterface descriptor, and the PublishedMetadata list are copied
// so the store never shares mutable state with a caller.
func cloneRuntime(r Runtime) Runtime {
	if r.Labels != nil {
		labels := make(map[string]string, len(r.Labels))
		for k, v := range r.Labels {
			labels[k] = v
		}
		r.Labels = labels
	}
	r.AgentInterface = r.AgentInterface.Clone()
	if r.PublishedMetadata != nil {
		r.PublishedMetadata = append([]PublishedMetadataEntry(nil), r.PublishedMetadata...)
	}
	if r.ToolCapabilityOverrides != nil {
		m := make(map[string][]capabilityinference.Capability, len(r.ToolCapabilityOverrides))
		for k, v := range r.ToolCapabilityOverrides {
			m[k] = append([]capabilityinference.Capability(nil), v...)
		}
		r.ToolCapabilityOverrides = m
	}
	r.SetupPolicy = r.SetupPolicy.Clone()
	r.Capabilities = r.Capabilities.Clone()
	return r
}

// Memory is the in-memory Store implementation.
type Memory struct {
	mu       sync.RWMutex
	runtimes map[string]Runtime
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory { return &Memory{runtimes: map[string]Runtime{}} }

// Create implements Store.
func (m *Memory) Create(_ context.Context, r Runtime) error {
	if err := ValidateName(r.Name); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.runtimes[r.Name]; exists {
		return ErrAlreadyExists
	}
	now := time.Now().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = r.CreatedAt
	}
	m.runtimes[r.Name] = cloneRuntime(r)
	return nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, name string) (Runtime, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	row, ok := m.runtimes[name]
	if !ok {
		return Runtime{}, ErrNotFound
	}
	return cloneRuntime(row), nil
}

// Update implements Store.
func (m *Memory) Update(_ context.Context, name string, mutate func(*Runtime) error) (Runtime, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.runtimes[name]
	if !ok {
		return Runtime{}, ErrNotFound
	}
	row := cloneRuntime(stored)
	prev := row.UpdatedAt
	if err := mutate(&row); err != nil {
		return Runtime{}, err
	}
	now := time.Now().UTC()
	if !now.After(prev) {
		now = prev.Add(time.Nanosecond)
	}
	row.UpdatedAt = now
	m.runtimes[name] = cloneRuntime(row)
	return cloneRuntime(row), nil
}

// List implements Store.
func (m *Memory) List(_ context.Context, filter ListFilter) ([]Runtime, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Runtime, 0, len(m.runtimes))
	for _, row := range m.runtimes {
		if !filter.IncludeDeleted && !row.IsActive() {
			continue
		}
		if filter.Type != "" && row.Type != filter.Type {
			continue
		}
		out = append(out, cloneRuntime(row))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SoftDelete implements Store.
func (m *Memory) SoftDelete(_ context.Context, name string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.runtimes[name]
	if !ok {
		return ErrNotFound
	}
	if !row.DeletedAt.IsZero() {
		return nil // idempotent
	}
	row.DeletedAt = at
	row.UpdatedAt = at
	m.runtimes[name] = row
	return nil
}
