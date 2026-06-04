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
	"encoding/json"
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

	// WorkspaceTier is the §12.9 / §5.2 data-classification tier this
	// runtime processes (`T3` default, `T4` Restricted). A T4 runtime
	// cannot back a cross-tenant-reuse pool (§5.2 line 396). Empty is
	// treated as T3.
	WorkspaceTier WorkspaceTier

	// AllowedResourceClasses is the §5.1 set of resource classes the
	// runtime permits (for example small, medium, large). The §5.1 merge
	// table classifies it Prohibited on derived runtimes: a derived
	// runtime inherits its base's set and may not declare its own. A nil
	// or empty slice means the runtime declares no class set.
	AllowedResourceClasses []string

	// SupportedProviders is the §5.1 set of credential-provider
	// identifiers the runtime's SDK supports (for example
	// anthropic_direct, aws_bedrock). The §4.9 lease path matches a
	// session's requested provider against it. The §5.1 merge table
	// classifies it Override with the restrict-only rule that a derived
	// runtime may restrict but not expand beyond its base set.
	SupportedProviders []string

	// CredentialCapabilities is the §5.1 credentialCapabilities block:
	// the runtime's hot-rotation support and the §4.9 LLM-proxy dialects
	// its SDK speaks. It is required when a pool bound to the runtime
	// uses deliveryMode: proxy, and empty for runtimes that only support
	// direct mode. The §5.1 merge table classifies it Override: a
	// derived runtime's value replaces the base when set. It is nil when
	// the runtime declares no credentialCapabilities block.
	CredentialCapabilities *CredentialCapabilities

	// AllowSelfRecursion is the §5.1 line 69 runtime-layer opt-in for the
	// §8.2 cycle-detection three-layer AND gate (LayerRuntime). When
	// false this runtime rejects every self-recursive delegation hop. The
	// §5.1 merge table classifies it Override (restrict-only): a derived
	// runtime may set false when the base is true, but a derived value of
	// true is rejected when the base is false.
	AllowSelfRecursion bool

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

	// Limits is the §5.1 limits block: per-runtime session-age, upload-size,
	// and request-input-wait caps. The §5.1 merge table classifies it
	// Override. It is nil when the runtime declares no limits block.
	Limits *Limits

	// SetupCommandPolicy is the §5.1 setupCommandPolicy block: the
	// per-runtime command allowlist or shell mode the gateway enforces at
	// pod startup. The §5.1 merge table classifies it Override. It is nil
	// when the runtime declares no setupCommandPolicy block.
	SetupCommandPolicy *SetupCommandPolicy

	// ArchivePolicy is the §13.4 per-Runtime archive-extraction opt-in
	// block: the AllowSymlinks toggle the gateway delivers to the adapter
	// on FinalizeWorkspace so uploadArchive symlink entries are admitted
	// (and target-validated) only for runtimes that explicitly opt in. A
	// nil block means platform defaults (symlinks rejected). spec: §7.4
	// lines 458, 462; §13.4 lines 663-672 — F-7.4.4.
	ArchivePolicy *ArchivePolicy

	// DefaultPoolConfig is the §5.1 defaultPoolConfig block: the
	// runtime-declared default pool sizing the §5.2 pool resolver consults
	// before falling back to platform defaults. The §5.1 merge table
	// classifies it Override. It is nil when the runtime declares no
	// defaultPoolConfig block.
	DefaultPoolConfig *DefaultPoolConfig

	// WorkspaceDefaults is the §5.1 workspaceDefaults block: the default
	// files and setup commands the §14 workspace-plan path materializes
	// into every pod before client uploads. The §5.1 merge table
	// classifies it Append (base defaults → derived defaults → client
	// uploads). It is nil when the runtime declares no workspaceDefaults
	// block.
	WorkspaceDefaults *WorkspaceDefaults

	// RuntimeOptionsSchema is the §5.1 runtimeOptionsSchema: a JSON Schema
	// fragment the §14 path validates session-creation runtimeOptions
	// against. The §5.1 merge table classifies it Override with the
	// restrict-only rule that a derived schema may only reference property
	// names present in the base schema's properties map. It is nil when
	// the runtime declares no runtimeOptionsSchema.
	RuntimeOptionsSchema json.RawMessage

	// SharedAssets is the §5.1 sharedAssets list: read-only files the
	// §6.4 pod-init flow materializes into /workspace/shared/ for a
	// concurrent-execution-mode runtime. The §5.1 merge table classifies
	// it Append (conflicting destPath entries replaced by derived). A nil
	// or empty slice means the runtime declares no shared assets.
	SharedAssets []SharedAsset

	// Capabilities is the §5.1 capabilities block: the runtime's
	// interaction model and its mid-session injection support. It is
	// nil when the runtime declares no capabilities block.
	Capabilities *RuntimeCapabilities

	// SDKWarmBlockingPaths is the §5.1 top-level sdkWarmBlockingPaths
	// list: glob patterns matched against relative workspace paths. When
	// capabilities.preConnect is true and any uploaded file (including
	// workspaceDefaults files) matches a pattern, the §6.1 warm-pool
	// controller demotes the SDK-warm pod before use. The field is only
	// meaningful when capabilities.preConnect is true; ApplyDefaults seeds
	// it to ["CLAUDE.md", ".claude/*"] in that case when the runtime
	// declares none. Patterns follow Go path.Match extended with "**".
	SDKWarmBlockingPaths []string

	// MinPlatformVersion is the §5.1 minPlatformVersion: the lowest
	// Lenny gateway version the runtime supports. The gateway rejects
	// registration when its own version is below this. An empty value
	// declares no minimum.
	MinPlatformVersion string

	// TaskPolicy is the §5.1 taskPolicy block: the §5.2 task-mode
	// pod-reuse and workspace-cleanup policy. It is nil when the
	// runtime declares no task policy.
	TaskPolicy *TaskPolicy

	// BaseRuntime is the §5.1 baseRuntime reference. When set, this
	// runtime is a derived runtime: the gateway resolves its effective
	// definition by merging it onto the named base runtime. An empty
	// value marks a standalone runtime.
	BaseRuntime string

	// CreatedAt / UpdatedAt are the audit timestamps.
	CreatedAt time.Time
	UpdatedAt time.Time

	// DeletedAt is the §15.1 soft-delete timestamp. The minimal
	// implementation refuses to register pods against soft-deleted
	// runtimes but keeps the row for audit.
	DeletedAt time.Time

	// Version is the §15.1 ETag-based optimistic-concurrency counter: it
	// starts at 1 and increments on every successful write (Update and
	// SoftDelete). The quoted decimal version is the runtime's strong
	// entity tag, enforced on PUT via the If-Match precondition and
	// exposed on GET/list. spec: §15.1 lines 1207-1213.
	Version int64
}

// IsActive reports whether the runtime has not been soft-deleted.
func (r Runtime) IsActive() bool { return r.DeletedAt.IsZero() }

// IsDerived reports whether the runtime is a §5.1 derived runtime — one
// that references a base runtime and is resolved by the merge algorithm.
func (r Runtime) IsDerived() bool { return r.BaseRuntime != "" }

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

// CredentialCapabilities is the §5.1 credentialCapabilities block on a
// runtime. HotRotation reports whether the runtime's SDK honors a
// mid-session credential rotation without a restart. ProxyDialect lists
// the §4.9 LLM-proxy dialects the SDK speaks (openai, anthropic); a
// pool bound to the runtime that uses deliveryMode: proxy must declare a
// proxyDialect in this set.
type CredentialCapabilities struct {
	// HotRotation reports §5.1 mid-session credential hot-rotation
	// support.
	HotRotation bool `json:"hotRotation,omitempty"`

	// ProxyDialect lists the §4.9 LLM-proxy dialects the runtime's SDK
	// speaks. An empty slice declares direct-mode-only support.
	ProxyDialect []string `json:"proxyDialect,omitempty"`
}

// Clone returns a deep copy of the block so the store never shares the
// ProxyDialect slice with a caller. A nil receiver clones to nil.
func (c *CredentialCapabilities) Clone() *CredentialCapabilities {
	if c == nil {
		return nil
	}
	cp := *c
	cp.ProxyDialect = append([]string(nil), c.ProxyDialect...)
	return &cp
}

// AllowsProxyDialect reports whether the runtime declares dialect in its
// §5.1 credentialCapabilities.proxyDialect set. A nil receiver (no
// credentialCapabilities block) allows nothing.
func (c *CredentialCapabilities) AllowsProxyDialect(dialect string) bool {
	if c == nil {
		return false
	}
	for _, d := range c.ProxyDialect {
		if d == dialect {
			return true
		}
	}
	return false
}

// RuntimeCapabilities is the §5.1 capabilities block on a runtime.
type RuntimeCapabilities struct {
	// Interaction is the runtime's §5.1 interaction model.
	Interaction RuntimeInteraction `json:"interaction,omitempty"`

	// Injection is the runtime's §5.1 mid-session injection support.
	Injection InjectionCapability `json:"injection,omitempty"`

	// PreConnect is the §5.1 capabilities.preConnect flag. When true the
	// §6.1 warm-pool controller pre-connects the agent SDK process during
	// the warm phase so every pod in the pool is SDK-warm, and the
	// runtime's adapter must implement DemoteSDK. Default false. The
	// companion runtime-level SDKWarmBlockingPaths governs the demotion
	// decision (§5.1 lines 22-24).
	PreConnect bool `json:"preConnect,omitempty"`

	// MidSessionUpload is the §7.4 line 433 capabilities.midSessionUpload
	// flag. When true (and the deployer policy permits it) clients may
	// upload files into the workspace of an already-running session via
	// the mid-session upload surface; the adapter overlays the files onto
	// /workspace/current and signals the runtime once promotion completes.
	// Clients discover the flag through the GET /v1/runtimes capabilities
	// block. Default false. spec: §7.4 line 433 — F-7.4.6.
	MidSessionUpload bool `json:"midSessionUpload,omitempty"`
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
	// TimeoutSeconds is the aggregate cap on the setup phase in seconds.
	// Zero means the runtime declares no aggregate cap and the setup
	// phase waits indefinitely (§5.1 line 260). In the §5.1 derived
	// Maximum merge (line 195) zero is the largest possible bound: it
	// wins max() over any finite value, so a base's "no cap" floor cannot
	// be shortened by a derived runtime. The registration validator
	// enforces the line-195 note "neither can be zero if the other is
	// set" across a base and derived pair.
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

// Limits is the §5.1 limits block on a runtime. It carries the per-runtime
// session-age cap, upload-size cap, and the §11.3 inter-agent
// lenny/request_input wait timeout. A zero field declares no per-runtime
// cap for that dimension; the gateway falls back to its platform default.
type Limits struct {
	// MaxSessionAgeSeconds caps a session's lifetime in seconds. Zero
	// declares no per-runtime session-age cap.
	MaxSessionAgeSeconds int `json:"maxSessionAgeSeconds,omitempty"`

	// MaxUploadSizeBytes caps a single client upload in bytes. Zero
	// declares no per-runtime upload cap.
	MaxUploadSizeBytes int64 `json:"maxUploadSizeBytes,omitempty"`

	// MaxRequestInputWaitSeconds is the §11.3 inter-agent
	// lenny/request_input timeout in seconds. Zero selects the platform
	// default.
	MaxRequestInputWaitSeconds int `json:"maxRequestInputWaitSeconds,omitempty"`

	// MaxElicitationWaitSeconds is the §11.3 line 202 human-facing
	// lenny/request_elicitation wait timeout in seconds, configurable per
	// pool in the `limits:` block of the RuntimeDefinition (same placement
	// as maxRequestInputWaitSeconds). Zero selects the platform default
	// (600s). spec: §11.3 line 202.
	MaxElicitationWaitSeconds int `json:"maxElicitationWaitSeconds,omitempty"`

	// MaxElicitationsPerSession is the §11.3 line 203 per-session lifetime
	// elicitation budget, configurable per pool in the `limits:` block.
	// Zero selects the platform default (50). spec: §11.3 line 203.
	MaxElicitationsPerSession int `json:"maxElicitationsPerSession,omitempty"`
}

// Clone returns a copy of the limits block. A nil receiver clones to nil.
func (l *Limits) Clone() *Limits {
	if l == nil {
		return nil
	}
	cp := *l
	return &cp
}

// SetupCommandMode is the §5.1 / §7.5 setupCommandPolicy.mode enum: whether
// the runtime restricts setup commands to an explicit allowlist (deny-by-
// default) or to an explicit blocklist (allow-by-default). spec: §7.5 lines
// 483-486 — F-7.5.1.
type SetupCommandMode string

const (
	// SetupCommandModeAllowlist permits only commands whose §7.5 prefix
	// match is present in Allowlist. Everything else is rejected.
	SetupCommandModeAllowlist SetupCommandMode = "allowlist"
	// SetupCommandModeBlocklist rejects commands whose §7.5 prefix match
	// is present in Blocklist. Everything else is allowed.
	SetupCommandModeBlocklist SetupCommandMode = "blocklist"
)

// AllSetupCommandModes returns the closed enum.
func AllSetupCommandModes() []SetupCommandMode {
	return []SetupCommandMode{SetupCommandModeAllowlist, SetupCommandModeBlocklist}
}

// IsValid reports whether m is a known setup-command mode.
func (m SetupCommandMode) IsValid() bool {
	for _, v := range AllSetupCommandModes() {
		if m == v {
			return true
		}
	}
	return false
}

// SetupCommandPolicy is the §5.1 / §7.5 setupCommandPolicy block on a
// runtime: the command allow/block list, the shell-execution flag, and the
// per-session command cap the gateway enforces against the client-supplied
// workspace plan at create-time (§7.5 line 488). The §5.1 merge table
// classifies it Override. spec: §7.5 lines 481-490 — F-7.5.1.
type SetupCommandPolicy struct {
	// Mode selects allowlist (deny-by-default) or blocklist (allow-by-
	// default) prefix matching.
	Mode SetupCommandMode `json:"mode,omitempty"`

	// Shell selects shell-vs-argv execution: true wraps commands in
	// `/bin/sh -c`; false splits on whitespace and execs the argv directly,
	// neutering shell metacharacters per §7.5 line 490. F-7.5.2.
	Shell bool `json:"shell,omitempty"`

	// Allowlist is the set of permitted §7.5 command prefixes when Mode is
	// allowlist. A command matches when its raw text is the prefix or
	// begins with `prefix + space` so `curl` matches `curl -s http://...`
	// but not `curl-other`.
	Allowlist []string `json:"allowlist,omitempty"`

	// Blocklist is the set of rejected §7.5 command prefixes when Mode is
	// blocklist. Prefix-match semantics are identical to Allowlist.
	Blocklist []string `json:"blocklist,omitempty"`

	// MaxCommands caps the number of setup commands. Zero declares no cap.
	MaxCommands int `json:"maxCommands,omitempty"`
}

// Clone returns a deep copy of the policy so the store never shares the
// Allowlist or Blocklist slice with a caller. A nil receiver clones to nil.
func (p *SetupCommandPolicy) Clone() *SetupCommandPolicy {
	if p == nil {
		return nil
	}
	cp := *p
	cp.Allowlist = append([]string(nil), p.Allowlist...)
	cp.Blocklist = append([]string(nil), p.Blocklist...)
	return &cp
}

// PermitsCommand reports whether cmd is admitted by the policy under §7.5
// prefix-match semantics. A nil receiver (no policy declared) admits every
// command, preserving the pre-F-7.5.1 behaviour. An empty Mode also admits
// (the policy declares no mode, so neither list is consulted). When Mode is
// allowlist the empty Allowlist denies everything; when Mode is blocklist
// the empty Blocklist allows everything. spec: §7.5 line 488 — F-7.5.1.
func (p *SetupCommandPolicy) PermitsCommand(cmd string) bool {
	if p == nil || p.Mode == "" {
		return true
	}
	matched := false
	switch p.Mode {
	case SetupCommandModeAllowlist:
		for _, entry := range p.Allowlist {
			if hasCommandPrefix(cmd, entry) {
				matched = true
				break
			}
		}
		return matched
	case SetupCommandModeBlocklist:
		for _, entry := range p.Blocklist {
			if hasCommandPrefix(cmd, entry) {
				return false
			}
		}
		return true
	}
	return true
}

// hasCommandPrefix returns true when cmd equals prefix or begins with
// `prefix + " "`. A literal `curl` matches `curl -s http://...` but not
// `curl-other`. An empty prefix never matches.
func hasCommandPrefix(cmd, prefix string) bool {
	if prefix == "" {
		return false
	}
	if cmd == prefix {
		return true
	}
	if len(cmd) > len(prefix) && cmd[:len(prefix)] == prefix && cmd[len(prefix)] == ' ' {
		return true
	}
	return false
}

// ArchivePolicy is the §13.4 per-Runtime archive-extraction opt-in
// block. AllowSymlinks lifts the §7.4 line 458 default-deny on symlink
// entries; even when true, the adapter still resolves the target through
// pkg/upload.ValidateSymlinkTarget (target must canonicalize under the
// runtime's workspace root and must not traverse /proc, /sys, /dev, or
// /run/lenny). spec: §7.4 lines 458, 462; §13.4 — F-7.4.4.
type ArchivePolicy struct {
	AllowSymlinks bool `json:"allowSymlinks,omitempty"`
}

// Clone returns a deep copy of the policy. A nil receiver clones to nil.
func (p *ArchivePolicy) Clone() *ArchivePolicy {
	if p == nil {
		return nil
	}
	cp := *p
	return &cp
}

// DefaultPoolConfig is the §5.1 defaultPoolConfig block on a runtime: the
// default pool sizing the §5.2 pool resolver consults before falling back
// to platform defaults. The §5.1 merge table classifies it Override.
type DefaultPoolConfig struct {
	// WarmCount is the default number of warm pods to keep.
	WarmCount int `json:"warmCount,omitempty"`

	// ResourceClass is the default §5.1 resource class for the pool.
	ResourceClass string `json:"resourceClass,omitempty"`

	// EgressProfile is the default §13 egress profile for the pool.
	EgressProfile string `json:"egressProfile,omitempty"`
}

// Clone returns a copy of the config. A nil receiver clones to nil.
func (c *DefaultPoolConfig) Clone() *DefaultPoolConfig {
	if c == nil {
		return nil
	}
	cp := *c
	return &cp
}

// WorkspaceFile is one §5.1 workspaceDefaults.files entry: a default
// workspace file the §14 path materializes into /workspace/current
// before client uploads. Small files carry inline Content; large files
// carry a Ref (a lenny-blob:// reference materialized in its place).
type WorkspaceFile struct {
	// Path is the destination path, relative to /workspace/current.
	Path string `json:"path"`

	// Content is the inline file content. Empty when the file is
	// materialized from Ref.
	Content string `json:"content,omitempty"`

	// Ref names a large-file reference materialized in place of inline
	// Content. Empty when Content is inline.
	Ref string `json:"ref,omitempty"`
}

// WorkspaceSetupCommand is one §5.1 workspaceDefaults.setupCommands
// entry. Per §5.1 line 198 the per-command TimeoutSeconds is preserved
// from each source through the merge. The §5.1 YAML accepts a bare
// command string or a {cmd, timeoutSeconds} object; UnmarshalJSON
// admits both forms.
type WorkspaceSetupCommand struct {
	Cmd            string `json:"cmd"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
}

// UnmarshalJSON accepts the §5.1 bare-string form
// (`- pip install -r requirements.txt`) and the §14 object form
// (`{cmd: ..., timeoutSeconds: ...}`).
func (c *WorkspaceSetupCommand) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		c.Cmd = s
		c.TimeoutSeconds = 0
		return nil
	}
	type alias WorkspaceSetupCommand
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*c = WorkspaceSetupCommand(a)
	return nil
}

// WorkspaceDefaults is the §5.1 workspaceDefaults block: the default
// files and setup commands a runtime ships. The §5.1 merge table
// classifies it Append: derived files are appended to base files with a
// conflicting Path replaced by the derived entry, and derived setup
// commands are appended after base setup commands.
type WorkspaceDefaults struct {
	Files         []WorkspaceFile         `json:"files,omitempty"`
	SetupCommands []WorkspaceSetupCommand `json:"setupCommands,omitempty"`
}

// Clone returns a deep copy of the block so the store never shares the
// Files or SetupCommands slices with a caller. A nil receiver clones to
// nil.
func (w *WorkspaceDefaults) Clone() *WorkspaceDefaults {
	if w == nil {
		return nil
	}
	cp := &WorkspaceDefaults{}
	if w.Files != nil {
		cp.Files = append([]WorkspaceFile(nil), w.Files...)
	}
	if w.SetupCommands != nil {
		cp.SetupCommands = append([]WorkspaceSetupCommand(nil), w.SetupCommands...)
	}
	return cp
}

// SharedAssetType is the §5.1 sharedAssets[].type enum: an artifact
// reference or an inline file.
type SharedAssetType string

const (
	// SharedAssetArtifact materializes a blob reference (Ref) into
	// /workspace/shared/.
	SharedAssetArtifact SharedAssetType = "artifact"
	// SharedAssetInline materializes inline Content into /workspace/shared/.
	SharedAssetInline SharedAssetType = "inline"
)

// AllSharedAssetTypes returns the closed enum.
func AllSharedAssetTypes() []SharedAssetType {
	return []SharedAssetType{SharedAssetArtifact, SharedAssetInline}
}

// IsValid reports whether t is a known shared-asset type.
func (t SharedAssetType) IsValid() bool {
	for _, v := range AllSharedAssetTypes() {
		if t == v {
			return true
		}
	}
	return false
}

// SharedAsset is one §5.1 sharedAssets entry: a read-only file the §6.4
// pod-init flow materializes into /workspace/shared/ for a
// concurrent-execution-mode runtime.
type SharedAsset struct {
	// Type discriminates an artifact reference from an inline file.
	Type SharedAssetType `json:"type"`

	// Ref is the blob reference for an artifact asset.
	Ref string `json:"ref,omitempty"`

	// Path is the source path for an inline asset.
	Path string `json:"path,omitempty"`

	// Content is the inline content for an inline asset.
	Content string `json:"content,omitempty"`

	// DestPath is the destination under /workspace/shared/. The §5.1
	// merge keys on DestPath: a conflicting derived entry replaces the
	// base entry.
	DestPath string `json:"destPath"`
}

// MicrovmScrubMode is the §5.1 taskPolicy.microvmScrubMode enum: how a
// microvm-isolated task-mode pod is scrubbed between cross-tenant tasks.
type MicrovmScrubMode string

const (
	// MicrovmScrubRestart restarts the guest between tasks (default).
	MicrovmScrubRestart MicrovmScrubMode = "restart"
	// MicrovmScrubInPlace reuses the running guest, leaving documented
	// guest-kernel residual state across tenants.
	MicrovmScrubInPlace MicrovmScrubMode = "in-place"
)

// AllMicrovmScrubModes returns the closed enum.
func AllMicrovmScrubModes() []MicrovmScrubMode {
	return []MicrovmScrubMode{MicrovmScrubRestart, MicrovmScrubInPlace}
}

// IsValid reports whether m is a known scrub mode.
func (m MicrovmScrubMode) IsValid() bool {
	for _, v := range AllMicrovmScrubModes() {
		if m == v {
			return true
		}
	}
	return false
}

// CleanupFailureDisposition is the §5.1 taskPolicy.onCleanupFailure
// enum: the disposition when a task-mode pod's cleanup commands fail.
type CleanupFailureDisposition string

const (
	// CleanupFailureWarn returns the pod to the pool with a warning.
	CleanupFailureWarn CleanupFailureDisposition = "warn"
	// CleanupFailureFail retires the pod on a cleanup failure.
	CleanupFailureFail CleanupFailureDisposition = "fail"
)

// AllCleanupFailureDispositions returns the closed enum.
func AllCleanupFailureDispositions() []CleanupFailureDisposition {
	return []CleanupFailureDisposition{CleanupFailureWarn, CleanupFailureFail}
}

// IsValid reports whether d is a known cleanup-failure disposition.
func (d CleanupFailureDisposition) IsValid() bool {
	for _, v := range AllCleanupFailureDispositions() {
		if d == v {
			return true
		}
	}
	return false
}

// TaskPolicy is the §5.1 taskPolicy block: the §5.2 task-mode pod-reuse
// and workspace-cleanup policy for a runtime.
type TaskPolicy struct {
	// AcknowledgeBestEffortScrub is the §5.1 deployer acknowledgment
	// that workspace scrub is best-effort. Task mode requires it.
	AcknowledgeBestEffortScrub bool `json:"acknowledgeBestEffortScrub,omitempty"`

	// AllowCrossTenantReuse permits a task-mode pod to serve tasks from
	// more than one tenant. §5.1 only permits it with microvm isolation.
	AllowCrossTenantReuse bool `json:"allowCrossTenantReuse,omitempty"`

	// MicrovmScrubMode is the cross-task scrub mode for a microvm pod.
	MicrovmScrubMode MicrovmScrubMode `json:"microvmScrubMode,omitempty"`

	// AcknowledgeMicrovmResidualState is the §5.1 acknowledgment that
	// in-place microvm scrub leaves guest-kernel residual state.
	AcknowledgeMicrovmResidualState bool `json:"acknowledgeMicrovmResidualState,omitempty"`

	// CleanupCommands run between tasks to scrub the workspace.
	CleanupCommands []string `json:"cleanupCommands,omitempty"`

	// CleanupTimeoutSeconds bounds the cleanup-command phase.
	CleanupTimeoutSeconds int `json:"cleanupTimeoutSeconds,omitempty"`

	// OnCleanupFailure is the disposition when cleanup commands fail.
	OnCleanupFailure CleanupFailureDisposition `json:"onCleanupFailure,omitempty"`

	// MaxScrubFailures retires a pod after this many cumulative scrub
	// failures. §5.1 default is 3.
	MaxScrubFailures int `json:"maxScrubFailures,omitempty"`

	// MaxTasksPerPod retires a pod after this many tasks. §5.1 requires
	// it for task mode.
	MaxTasksPerPod int `json:"maxTasksPerPod,omitempty"`

	// MaxPodUptimeSeconds optionally retires a pod after this uptime.
	MaxPodUptimeSeconds int `json:"maxPodUptimeSeconds,omitempty"`

	// MaxTaskRetries is the §6.6 per-task crash-retry budget. A nil
	// value takes the §6.6 default of 1; an explicit 0 disables retries.
	MaxTaskRetries *int `json:"maxTaskRetries,omitempty"`
}

// Clone returns a deep copy of the policy so the store never shares the
// cleanup-command slice or the retry pointer with a caller. A nil
// receiver clones to nil.
func (p *TaskPolicy) Clone() *TaskPolicy {
	if p == nil {
		return nil
	}
	cp := *p
	cp.CleanupCommands = append([]string(nil), p.CleanupCommands...)
	if p.MaxTaskRetries != nil {
		n := *p.MaxTaskRetries
		cp.MaxTaskRetries = &n
	}
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

// WorkspaceTier is the §12.9 / §5.2 data-classification tier a runtime
// is configured for. Workspace data defaults to T3 (Confidential);
// runtimes that process Restricted data (PHI, credentials) are
// configured with T4. §5.2 line 396 forbids cross-tenant pod reuse on a
// pool whose runtime is T4 because T4 requires dedicated node pools for
// per-tenant key isolation (§6.4). The empty value is treated as T3.
type WorkspaceTier string

const (
	// WorkspaceTierT3 is the §12.9 default workspace classification
	// (Confidential).
	WorkspaceTierT3 WorkspaceTier = "T3"
	// WorkspaceTierT4 is the §12.9 Restricted classification. A T4
	// runtime cannot back a cross-tenant-reuse pool (§5.2 line 396).
	WorkspaceTierT4 WorkspaceTier = "T4"
)

// AllWorkspaceTiers returns the closed set of workspace tiers a runtime
// may declare. §12.9 line 1025 scopes workspace classification to T3
// (default) and T4; lower tiers describe non-workspace data.
func AllWorkspaceTiers() []WorkspaceTier {
	return []WorkspaceTier{WorkspaceTierT3, WorkspaceTierT4}
}

// IsValid reports whether t is a recognised runtime workspace tier. The
// empty value is not "valid" but callers treat it as the T3 default;
// admission accepts an empty tier and only a non-empty, unrecognised one
// is rejected.
func (t WorkspaceTier) IsValid() bool {
	for _, v := range AllWorkspaceTiers() {
		if t == v {
			return true
		}
	}
	return false
}

// IsT4 reports whether t is the §12.9 Restricted tier. The empty value
// (implicit T3 default) is not T4.
func (t WorkspaceTier) IsT4() bool { return t == WorkspaceTierT4 }

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
// isolation profile to the platform default. The isolation default
// honors the §5.3 line 677 dev-mode fallback: when devMode is true an
// unset profile defaults to `standard` (runc) rather than `sandboxed`.
// Integration level defaults to basic for agent runtimes only — §5.1
// specifies that integrationLevel is meaningful solely on type: agent
// runtimes, so an mcp runtime keeps an empty integration level.
//
// Registration handlers call this at the admin-API boundary; the
// stores persist whatever they are given.
func ApplyDefaults(r *Runtime, devMode bool) {
	if r.Type == "" {
		r.Type = TypeAgent
	}
	if r.ExecutionMode == "" {
		r.ExecutionMode = ExecutionModeSession
	}
	if r.IsolationProfile == "" {
		r.IsolationProfile = isolation.DefaultForMode(devMode)
	}
	if r.Type == TypeAgent && r.IntegrationLevel == "" {
		r.IntegrationLevel = IntegrationLevelBasic
	}
	if r.CapabilityInferenceMode == "" {
		r.CapabilityInferenceMode = capabilityinference.DefaultMode
	}
	// §5.1 line 24: sdkWarmBlockingPaths defaults to ["CLAUDE.md",
	// ".claude/*"] when capabilities.preConnect is true and the runtime
	// declares no list. The field is ignored when preConnect is false, so
	// no default is seeded there.
	if r.Capabilities != nil && r.Capabilities.PreConnect && len(r.SDKWarmBlockingPaths) == 0 {
		r.SDKWarmBlockingPaths = []string{"CLAUDE.md", ".claude/*"}
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
	r.AllowedResourceClasses = append([]string(nil), r.AllowedResourceClasses...)
	r.SupportedProviders = append([]string(nil), r.SupportedProviders...)
	r.SDKWarmBlockingPaths = append([]string(nil), r.SDKWarmBlockingPaths...)
	r.CredentialCapabilities = r.CredentialCapabilities.Clone()
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
	r.TaskPolicy = r.TaskPolicy.Clone()
	r.Limits = r.Limits.Clone()
	r.SetupCommandPolicy = r.SetupCommandPolicy.Clone()
	r.ArchivePolicy = r.ArchivePolicy.Clone()
	r.DefaultPoolConfig = r.DefaultPoolConfig.Clone()
	r.WorkspaceDefaults = r.WorkspaceDefaults.Clone()
	if r.RuntimeOptionsSchema != nil {
		r.RuntimeOptionsSchema = append(json.RawMessage(nil), r.RuntimeOptionsSchema...)
	}
	if r.SharedAssets != nil {
		r.SharedAssets = append([]SharedAsset(nil), r.SharedAssets...)
	}
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
	// spec: §15.1 line 1207 — a new resource is born at version 1.
	if r.Version == 0 {
		r.Version = 1
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
	// spec: §15.1 line 1207 — bump the entity-tag version on every write.
	row.Version++
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
	// spec: §15.1 line 1207 — a soft-delete is a write, so it bumps the tag.
	row.Version++
	m.runtimes[name] = row
	return nil
}
