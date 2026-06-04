// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/agentcard"
	"github.com/lennylabs/lenny/pkg/gateway/capabilityinference"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/semver"
	"github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// applyGeneratedCard regenerates the §5.1 A2A agent card from the
// runtime's agentInterface and writes it into PublishedMetadata under
// the agent-card key, replacing any existing entry with that key. It is
// a no-op when the runtime carries no agentInterface, so a hand-crafted
// agent-card entry on a runtime without an agentInterface is left
// untouched per §5.1.
func (r *Router) applyGeneratedCard(rt *runtimestore.Runtime, at time.Time) {
	if rt.AgentInterface == nil {
		return
	}
	version := r.platformInfo.Version
	if version == "" {
		version = "dev"
	}
	entry := agentcard.Entry(rt.Name, *rt.AgentInterface, at, version)
	out := make([]runtimestore.PublishedMetadataEntry, 0, len(rt.PublishedMetadata)+1)
	for _, e := range rt.PublishedMetadata {
		if e.Key != agentcard.Key {
			out = append(out, e)
		}
	}
	rt.PublishedMetadata = append(out, entry)
}

// RuntimePayload is the §15.1 admin-runtime request/response body.
type RuntimePayload struct {
	Name                    string                                      `json:"name"`
	Type                    string                                      `json:"type,omitempty"`
	Image                   string                                      `json:"image,omitempty"`
	ExecutionMode           string                                      `json:"executionMode,omitempty"`
	IsolationProfile        string                                      `json:"isolationProfile,omitempty"`
	IntegrationLevel        string                                      `json:"integrationLevel,omitempty"`
	WorkspaceTier           string                                      `json:"workspaceTier,omitempty"`
	AllowedResourceClasses  []string                                    `json:"allowedResourceClasses,omitempty"`
	SupportedProviders      []string                                    `json:"supportedProviders,omitempty"`
	CredentialCapabilities  *runtimestore.CredentialCapabilities        `json:"credentialCapabilities,omitempty"`
	AllowSelfRecursion      bool                                        `json:"allowSelfRecursion,omitempty"`
	Description             string                                      `json:"description,omitempty"`
	DelegationPolicyRef     string                                      `json:"delegationPolicyRef,omitempty"`
	Labels                  map[string]string                           `json:"labels,omitempty"`
	AgentInterface          *runtimestore.AgentInterface                `json:"agentInterface,omitempty"`
	PublishedMetadata       []runtimestore.PublishedMetadataEntry       `json:"publishedMetadata,omitempty"`
	CapabilityInferenceMode string                                      `json:"capabilityInferenceMode,omitempty"`
	ToolCapabilityOverrides map[string][]capabilityinference.Capability `json:"toolCapabilityOverrides,omitempty"`
	SetupPolicy             *runtimestore.SetupPolicy                   `json:"setupPolicy,omitempty"`
	Limits                  *runtimestore.Limits                        `json:"limits,omitempty"`
	SetupCommandPolicy      *runtimestore.SetupCommandPolicy            `json:"setupCommandPolicy,omitempty"`
	DefaultPoolConfig       *runtimestore.DefaultPoolConfig             `json:"defaultPoolConfig,omitempty"`
	WorkspaceDefaults       *runtimestore.WorkspaceDefaults             `json:"workspaceDefaults,omitempty"`
	RuntimeOptionsSchema    json.RawMessage                             `json:"runtimeOptionsSchema,omitempty"`
	SharedAssets            []runtimestore.SharedAsset                  `json:"sharedAssets,omitempty"`
	Capabilities            *runtimestore.RuntimeCapabilities           `json:"capabilities,omitempty"`
	SDKWarmBlockingPaths    []string                                    `json:"sdkWarmBlockingPaths,omitempty"`
	MinPlatformVersion      string                                      `json:"minPlatformVersion,omitempty"`
	TaskPolicy              *runtimestore.TaskPolicy                    `json:"taskPolicy,omitempty"`
	BaseRuntime             string                                      `json:"baseRuntime,omitempty"`
	CreatedAt               string                                      `json:"createdAt,omitempty"`
	UpdatedAt               string                                      `json:"updatedAt,omitempty"`
	DeletedAt               string                                      `json:"deletedAt,omitempty"`
}

// runtimeFromPayload builds a runtimestore.Runtime from a §15.1
// RuntimePayload. The POST /v1/admin/runtimes handler and the
// POST /v1/admin/bootstrap upsert share it so a runtime created
// through either path carries the same field set.
func runtimeFromPayload(p RuntimePayload, createdAt time.Time) runtimestore.Runtime {
	return runtimestore.Runtime{
		Name:                    p.Name,
		Type:                    runtimestore.RuntimeType(p.Type),
		Image:                   p.Image,
		ExecutionMode:           runtimestore.ExecutionMode(p.ExecutionMode),
		IsolationProfile:        isolation.Profile(p.IsolationProfile),
		IntegrationLevel:        runtimestore.IntegrationLevel(p.IntegrationLevel),
		WorkspaceTier:           runtimestore.WorkspaceTier(p.WorkspaceTier),
		AllowedResourceClasses:  p.AllowedResourceClasses,
		SupportedProviders:      p.SupportedProviders,
		CredentialCapabilities:  p.CredentialCapabilities,
		AllowSelfRecursion:      p.AllowSelfRecursion,
		Description:             p.Description,
		DelegationPolicyRef:     p.DelegationPolicyRef,
		Labels:                  p.Labels,
		AgentInterface:          p.AgentInterface,
		PublishedMetadata:       p.PublishedMetadata,
		CapabilityInferenceMode: capabilityinference.Mode(p.CapabilityInferenceMode),
		ToolCapabilityOverrides: p.ToolCapabilityOverrides,
		SetupPolicy:             p.SetupPolicy,
		Limits:                  p.Limits,
		SetupCommandPolicy:      p.SetupCommandPolicy,
		DefaultPoolConfig:       p.DefaultPoolConfig,
		WorkspaceDefaults:       p.WorkspaceDefaults,
		RuntimeOptionsSchema:    p.RuntimeOptionsSchema,
		SharedAssets:            p.SharedAssets,
		Capabilities:            p.Capabilities,
		SDKWarmBlockingPaths:    p.SDKWarmBlockingPaths,
		MinPlatformVersion:      p.MinPlatformVersion,
		TaskPolicy:              p.TaskPolicy,
		BaseRuntime:             p.BaseRuntime,
		CreatedAt:               createdAt,
	}
}

// UpdateRuntimeRequest is the §15.1 PUT body. Optional pointer
// fields signal "leave unchanged when omitted". AgentInterface is a
// raw message so the three states omitted, JSON null, and an object
// stay distinct: omitted leaves the descriptor unchanged, null clears
// it, and an object replaces it.
type UpdateRuntimeRequest struct {
	Image                   *string                                      `json:"image,omitempty"`
	ExecutionMode           *string                                      `json:"executionMode,omitempty"`
	IsolationProfile        *string                                      `json:"isolationProfile,omitempty"`
	IntegrationLevel        *string                                      `json:"integrationLevel,omitempty"`
	WorkspaceTier           *string                                      `json:"workspaceTier,omitempty"`
	AllowedResourceClasses  *[]string                                    `json:"allowedResourceClasses,omitempty"`
	SupportedProviders      *[]string                                    `json:"supportedProviders,omitempty"`
	AllowSelfRecursion      *bool                                        `json:"allowSelfRecursion,omitempty"`
	Description             *string                                      `json:"description,omitempty"`
	DelegationPolicyRef     *string                                      `json:"delegationPolicyRef,omitempty"`
	Labels                  *map[string]string                           `json:"labels,omitempty"`
	AgentInterface          json.RawMessage                              `json:"agentInterface,omitempty"`
	PublishedMetadata       *[]runtimestore.PublishedMetadataEntry       `json:"publishedMetadata,omitempty"`
	CapabilityInferenceMode *string                                      `json:"capabilityInferenceMode,omitempty"`
	ToolCapabilityOverrides *map[string][]capabilityinference.Capability `json:"toolCapabilityOverrides,omitempty"`
	SetupPolicy             *runtimestore.SetupPolicy                    `json:"setupPolicy,omitempty"`
	Limits                  *runtimestore.Limits                         `json:"limits,omitempty"`
	SetupCommandPolicy      *runtimestore.SetupCommandPolicy             `json:"setupCommandPolicy,omitempty"`
	DefaultPoolConfig       *runtimestore.DefaultPoolConfig              `json:"defaultPoolConfig,omitempty"`
	WorkspaceDefaults       *runtimestore.WorkspaceDefaults              `json:"workspaceDefaults,omitempty"`
	RuntimeOptionsSchema    json.RawMessage                              `json:"runtimeOptionsSchema,omitempty"`
	SharedAssets            *[]runtimestore.SharedAsset                  `json:"sharedAssets,omitempty"`
	Capabilities            *runtimestore.RuntimeCapabilities            `json:"capabilities,omitempty"`
	SDKWarmBlockingPaths    *[]string                                    `json:"sdkWarmBlockingPaths,omitempty"`
	CredentialCapabilities  *runtimestore.CredentialCapabilities         `json:"credentialCapabilities,omitempty"`
	MinPlatformVersion      *string                                      `json:"minPlatformVersion,omitempty"`
	TaskPolicy              *runtimestore.TaskPolicy                     `json:"taskPolicy,omitempty"`
	BaseRuntime             *string                                      `json:"baseRuntime,omitempty"`
}

// validateDerivedRuntime applies the §5.1 derived-runtime registration
// rules to a create payload. A derived runtime (one with baseRuntime
// set) may not declare the inherited or prohibited fields — image,
// type, executionMode, isolationProfile, integrationLevel, and
// capabilities are always taken from the base — and its baseRuntime
// must reference an existing standalone runtime. The §5.1 merge
// algorithm is single-level, so a base that is itself derived is
// rejected. A standalone payload passes unconditionally.
func (r *Router) validateDerivedRuntime(ctx context.Context, p RuntimePayload) error {
	if p.BaseRuntime == "" {
		return nil
	}
	switch {
	case p.Image != "":
		return errors.New("image is prohibited on derived runtimes")
	case p.Type != "":
		return errors.New("type is prohibited on derived runtimes")
	case p.ExecutionMode != "":
		return errors.New("executionMode is prohibited on derived runtimes")
	case p.IsolationProfile != "":
		return errors.New("isolationProfile is prohibited on derived runtimes")
	case p.IntegrationLevel != "":
		return errors.New("integrationLevel is prohibited on derived runtimes")
	case p.WorkspaceTier != "":
		return errors.New("workspaceTier is prohibited on derived runtimes")
	case p.Capabilities != nil:
		return errors.New("capabilities is prohibited on derived runtimes")
	case len(p.AllowedResourceClasses) > 0:
		// §5.1 merge table: allowedResourceClasses is Prohibited on
		// derived runtimes — the derived runtime inherits the base set.
		return errors.New("allowedResourceClasses is prohibited on derived runtimes")
	}
	base, err := r.runtimes.Get(ctx, p.BaseRuntime)
	if err != nil {
		return errors.New("baseRuntime does not reference an existing runtime")
	}
	if base.IsDerived() {
		return errors.New("baseRuntime must reference a standalone runtime, not another derived runtime")
	}
	// §5.1 supportedProviders is Override (restrict-only): a derived
	// runtime may restrict but not expand beyond its base set.
	if extra := providersNotInBase(p.SupportedProviders, base.SupportedProviders); extra != "" {
		return fmt.Errorf("supportedProviders cannot expand beyond base: %q is not in the base runtime's set", extra)
	}
	// §5.1 allowSelfRecursion is Override (restrict-only): a derived
	// value of true is rejected when the base is false (security
	// boundary — a derived runtime may only narrow recursion).
	if p.AllowSelfRecursion && !base.AllowSelfRecursion {
		return errors.New("allowSelfRecursion cannot widen base value")
	}
	// §5.1 runtimeOptionsSchema is Override (restrict-only): a derived
	// schema may only reference property names present in the base
	// schema's properties map.
	if forbidden, err := forbiddenDerivedSchemaProperty(p.RuntimeOptionsSchema, base.RuntimeOptionsSchema); err != nil {
		return err
	} else if forbidden != "" {
		return fmt.Errorf("runtimeOptionsSchema declares forbidden property %q", forbidden)
	}
	// §5.1 line 195 note: in the timeoutSeconds Maximum merge, neither
	// side can be zero (no aggregate cap) while the other sets a finite
	// value — max(no-cap, finite) is ambiguous.
	if setupTimeoutPairingConflict(base.SetupPolicy, p.SetupPolicy) {
		return errSetupTimeoutPairing
	}
	return nil
}

// errSetupTimeoutPairing reports the §5.1 line-195 note violation: a base
// and derived setupPolicy where exactly one declares a finite
// timeoutSeconds while the other is zero (no aggregate cap).
var errSetupTimeoutPairing = errors.New(
	"setupPolicy.timeoutSeconds cannot be zero on one runtime when the other sets a value")

// setupTimeoutPairingConflict reports the §5.1 line-195 "neither can be
// zero if the other is set" violation across a base and derived
// setupPolicy pair. It applies only when both runtimes declare a
// setupPolicy: a conflict is exactly one side carrying a zero (no-cap)
// timeoutSeconds while the other carries a finite value. When either side
// declares no setupPolicy at all the merge takes the single declared
// policy unchanged and the note does not apply.
func setupTimeoutPairingConflict(base, derived *runtimestore.SetupPolicy) bool {
	if base == nil || derived == nil {
		return false
	}
	return (base.TimeoutSeconds == 0) != (derived.TimeoutSeconds == 0)
}

// providersNotInBase returns the first entry of derived that is not in
// base, or "" when derived is a subset of base. An empty base set means
// the base supports no providers, so any derived entry is out of range.
func providersNotInBase(derived, base []string) string {
	if len(derived) == 0 {
		return ""
	}
	allowed := make(map[string]bool, len(base))
	for _, p := range base {
		allowed[p] = true
	}
	for _, p := range derived {
		if !allowed[p] {
			return p
		}
	}
	return ""
}

// validateDerivedRuntimeUpdate applies the §5.1 derived-runtime rules
// to a PUT against an existing runtime. A runtime's baseRuntime is
// fixed at registration, so a PUT may not change it. A PUT against an
// already-derived runtime may not set the inherited or prohibited
// fields (image, executionMode, isolationProfile, integrationLevel,
// capabilities) — they are always taken from the base.
func validateDerivedRuntimeUpdate(current, base runtimestore.Runtime, body UpdateRuntimeRequest) error {
	if body.BaseRuntime != nil && *body.BaseRuntime != current.BaseRuntime {
		return errors.New("baseRuntime cannot be changed after registration")
	}
	if !current.IsDerived() {
		return nil
	}
	switch {
	case body.Image != nil && *body.Image != "":
		return errors.New("image is prohibited on derived runtimes")
	case body.ExecutionMode != nil && *body.ExecutionMode != "":
		return errors.New("executionMode is prohibited on derived runtimes")
	case body.IsolationProfile != nil && *body.IsolationProfile != "":
		return errors.New("isolationProfile is prohibited on derived runtimes")
	case body.IntegrationLevel != nil && *body.IntegrationLevel != "":
		return errors.New("integrationLevel is prohibited on derived runtimes")
	case body.WorkspaceTier != nil && *body.WorkspaceTier != "":
		return errors.New("workspaceTier is prohibited on derived runtimes")
	case body.Capabilities != nil:
		return errors.New("capabilities is prohibited on derived runtimes")
	case body.AllowedResourceClasses != nil && len(*body.AllowedResourceClasses) > 0:
		// §5.1 merge table: allowedResourceClasses is Prohibited on derived.
		return errors.New("allowedResourceClasses is prohibited on derived runtimes")
	}
	// §5.1 supportedProviders restrict-only and allowSelfRecursion
	// restrict-only invariants are evaluated against the resolved base set.
	if body.SupportedProviders != nil {
		if extra := providersNotInBase(*body.SupportedProviders, base.SupportedProviders); extra != "" {
			return fmt.Errorf("supportedProviders cannot expand beyond base: %q is not in the base runtime's set", extra)
		}
	}
	if body.AllowSelfRecursion != nil && *body.AllowSelfRecursion && !base.AllowSelfRecursion {
		return errors.New("allowSelfRecursion cannot widen base value")
	}
	// §5.1 runtimeOptionsSchema restrict-only: a derived schema may only
	// reference property names present in the base schema.
	if len(body.RuntimeOptionsSchema) > 0 {
		if forbidden, err := forbiddenDerivedSchemaProperty(body.RuntimeOptionsSchema, base.RuntimeOptionsSchema); err != nil {
			return err
		} else if forbidden != "" {
			return fmt.Errorf("runtimeOptionsSchema declares forbidden property %q", forbidden)
		}
	}
	// §5.1 line 195 note: a PUT that sets the derived runtime's
	// setupPolicy must not produce a zero/finite mismatch against the
	// base's setupPolicy. An omitted setupPolicy leaves the prior
	// (already-validated) pairing in place.
	if body.SetupPolicy != nil && setupTimeoutPairingConflict(base.SetupPolicy, body.SetupPolicy) {
		return errSetupTimeoutPairing
	}
	return nil
}

// proposedBaseRuntime applies the fields a PUT may change on a base
// runtime that a derived runtime's validity depends on, returning the
// would-be base. It covers the restrict-only / merge-input fields
// (supportedProviders, allowSelfRecursion, runtimeOptionsSchema,
// setupPolicy) that §5.1 impact validation evaluates; image and name are
// immutable and the remaining fields do not constrain derived runtimes.
func proposedBaseRuntime(current runtimestore.Runtime, body UpdateRuntimeRequest) runtimestore.Runtime {
	p := current
	if body.SupportedProviders != nil {
		p.SupportedProviders = *body.SupportedProviders
	}
	if body.AllowSelfRecursion != nil {
		p.AllowSelfRecursion = *body.AllowSelfRecursion
	}
	if len(body.RuntimeOptionsSchema) > 0 {
		p.RuntimeOptionsSchema = body.RuntimeOptionsSchema
	}
	if body.SetupPolicy != nil {
		p.SetupPolicy = body.SetupPolicy
	}
	return p
}

// derivedConflictsWithBase reports whether derived d would become invalid
// against base — the §5.1 derived-runtime invariants evaluated at
// registration, recomputed here for impact validation: supportedProviders
// must stay a subset of the base set, allowSelfRecursion may not widen
// beyond the base, runtimeOptionsSchema may only reference base property
// names, and the setupPolicy.timeoutSeconds pairing may not become a
// zero/finite mismatch.
func derivedConflictsWithBase(d, base runtimestore.Runtime) bool {
	if providersNotInBase(d.SupportedProviders, base.SupportedProviders) != "" {
		return true
	}
	if d.AllowSelfRecursion && !base.AllowSelfRecursion {
		return true
	}
	if forbidden, err := forbiddenDerivedSchemaProperty(d.RuntimeOptionsSchema, base.RuntimeOptionsSchema); err != nil || forbidden != "" {
		return true
	}
	if setupTimeoutPairingConflict(base.SetupPolicy, d.SetupPolicy) {
		return true
	}
	return false
}

// derivedRuntimesInvalidatedBy returns the names of active derived
// runtimes that reference baseName and would be invalidated by replacing
// their base with newBase. §5.1 line 174 requires the gateway to reject a
// base-runtime mutation that would invalidate an existing derived runtime
// and to name the affected runtimes.
func (r *Router) derivedRuntimesInvalidatedBy(ctx context.Context, baseName string, newBase runtimestore.Runtime) ([]string, error) {
	rows, err := r.runtimes.List(ctx, runtimestore.ListFilter{})
	if err != nil {
		return nil, err
	}
	var affected []string
	for _, d := range rows {
		if d.BaseRuntime != baseName || !d.IsActive() {
			continue
		}
		if derivedConflictsWithBase(d, newBase) {
			affected = append(affected, d.Name)
		}
	}
	return affected, nil
}

// validateTaskPolicy checks a §5.1 taskPolicy: known scrub-mode and
// cleanup-failure enums and non-negative numeric fields. The §5.1
// cross-field rules — allowCrossTenantReuse requires microvm isolation
// and in-place scrub requires the residual-state acknowledgment — are
// enforced by the pool controller against the resolved pool.
func validateTaskPolicy(p *runtimestore.TaskPolicy) error {
	if p == nil {
		return nil
	}
	if p.MicrovmScrubMode != "" && !p.MicrovmScrubMode.IsValid() {
		return errors.New("taskPolicy.microvmScrubMode must be restart or in-place")
	}
	if p.OnCleanupFailure != "" && !p.OnCleanupFailure.IsValid() {
		return errors.New("taskPolicy.onCleanupFailure must be warn or fail")
	}
	if p.CleanupTimeoutSeconds < 0 || p.MaxScrubFailures < 0 ||
		p.MaxTasksPerPod < 0 || p.MaxPodUptimeSeconds < 0 {
		return errors.New("taskPolicy numeric fields must not be negative")
	}
	if p.MaxTaskRetries != nil && *p.MaxTaskRetries < 0 {
		return errors.New("taskPolicy.maxTaskRetries must not be negative")
	}
	return nil
}

// validateMinPlatformVersion checks a §5.1 minPlatformVersion: when set
// it must be a parseable version, and the running gateway version must
// not be below it. The gateway-version floor check is skipped when the
// gateway's own version is not a parseable release (a dev build), since
// no meaningful comparison is possible.
func (r *Router) validateMinPlatformVersion(minVersion string) error {
	if minVersion == "" {
		return nil
	}
	if _, ok := semver.Parse(minVersion); !ok {
		return errors.New("minPlatformVersion is not a valid version")
	}
	gw := r.platformInfo.Version
	if _, ok := semver.Parse(gw); ok && semver.Compare(gw, minVersion) < 0 {
		return errors.New("runtime minPlatformVersion is newer than this gateway's platform version")
	}
	return nil
}

// validateSetupPolicy checks a §5.1 setupPolicy: a non-negative
// timeout and an onTimeout value within the fail / warn enum.
//
// When maxFinalizingTimeoutSeconds > 0 (the gateway-side outer bound
// from §11.3 line 219), the validator also enforces the §6.2 line 260
// invariant `maxFinalizingTimeoutSeconds ≥ setupTimeoutSeconds`. A
// runtime whose `setupPolicy.timeoutSeconds` exceeds the gateway cap
// is rejected at admin time per §6.2 line 260 ("the gateway rejects
// configurations that violate this constraint"), since the
// finalizing-state watchdog would otherwise terminate the session
// before the setup phase could complete.
//
// spec: §5.1 setupPolicy; §6.2 line 260; §11.3 line 219.
func validateSetupPolicy(p *runtimestore.SetupPolicy, maxFinalizingTimeoutSeconds int) error {
	if p == nil {
		return nil
	}
	if p.TimeoutSeconds < 0 {
		return errors.New("setupPolicy.timeoutSeconds must not be negative")
	}
	if p.OnTimeout != "" && !p.OnTimeout.IsValid() {
		return errors.New("setupPolicy.onTimeout must be fail or warn")
	}
	if maxFinalizingTimeoutSeconds > 0 && p.TimeoutSeconds > maxFinalizingTimeoutSeconds {
		return fmt.Errorf(
			"setupPolicy.timeoutSeconds (%d) exceeds gateway.maxFinalizingTimeoutSeconds (%d); §6.2 requires the gateway outer bound to be ≥ the runtime inner bound",
			p.TimeoutSeconds, maxFinalizingTimeoutSeconds)
	}
	return nil
}

// validateLimits checks a §5.1 limits block: non-negative session-age,
// upload-size, request-input-wait, elicitation-wait, and
// elicitations-per-session caps. spec: §11.3 lines 198-204.
func validateLimits(l *runtimestore.Limits) error {
	if l == nil {
		return nil
	}
	if l.MaxSessionAgeSeconds < 0 || l.MaxUploadSizeBytes < 0 || l.MaxRequestInputWaitSeconds < 0 ||
		l.MaxElicitationWaitSeconds < 0 || l.MaxElicitationsPerSession < 0 {
		return errors.New("limits fields must not be negative")
	}
	return nil
}

// validateSetupCommandPolicy checks a §5.1 setupCommandPolicy block: a
// known mode and a non-negative maxCommands.
func validateSetupCommandPolicy(p *runtimestore.SetupCommandPolicy) error {
	if p == nil {
		return nil
	}
	if p.Mode != "" && !p.Mode.IsValid() {
		// spec: §7.5 lines 483-486 — F-7.5.1.
		return errors.New("setupCommandPolicy.mode must be allowlist or blocklist")
	}
	if p.MaxCommands < 0 {
		return errors.New("setupCommandPolicy.maxCommands must not be negative")
	}
	return nil
}

// validateDefaultPoolConfig checks a §5.1 defaultPoolConfig block: a
// non-negative warmCount.
func validateDefaultPoolConfig(c *runtimestore.DefaultPoolConfig) error {
	if c == nil {
		return nil
	}
	if c.WarmCount < 0 {
		return errors.New("defaultPoolConfig.warmCount must not be negative")
	}
	return nil
}

// validateWorkspaceDefaults checks a §5.1 workspaceDefaults block: each
// file entry declares a destination Path, and each setup command is
// non-empty with a non-negative timeout.
func validateWorkspaceDefaults(w *runtimestore.WorkspaceDefaults) error {
	if w == nil {
		return nil
	}
	for _, f := range w.Files {
		if f.Path == "" {
			return errors.New("workspaceDefaults.files entries must declare a path")
		}
	}
	for _, c := range w.SetupCommands {
		if c.Cmd == "" {
			return errors.New("workspaceDefaults.setupCommands entries must declare a command")
		}
		if c.TimeoutSeconds < 0 {
			return errors.New("workspaceDefaults.setupCommands timeoutSeconds must not be negative")
		}
	}
	return nil
}

// validateSharedAssets checks a §5.1 sharedAssets list: each entry
// declares a known type and a destPath, an artifact asset carries a ref,
// and an inline asset carries a path.
func validateSharedAssets(assets []runtimestore.SharedAsset) error {
	for _, a := range assets {
		if !a.Type.IsValid() {
			return errors.New("sharedAssets.type must be artifact or inline")
		}
		if a.DestPath == "" {
			return errors.New("sharedAssets entries must declare a destPath")
		}
		switch a.Type {
		case runtimestore.SharedAssetArtifact:
			if a.Ref == "" {
				return errors.New("sharedAssets artifact entries must declare a ref")
			}
		case runtimestore.SharedAssetInline:
			if a.Path == "" {
				return errors.New("sharedAssets inline entries must declare a path")
			}
		}
	}
	return nil
}

// validateRuntimeOptionsSchema checks a §5.1 runtimeOptionsSchema: when
// present it must be a JSON object (a JSON Schema fragment). An empty
// value declares no schema.
func validateRuntimeOptionsSchema(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if _, err := schemaProperties(raw); err != nil {
		return err
	}
	return nil
}

// schemaProperties parses a §5.1 runtimeOptionsSchema and returns the set
// of property names declared in its top-level `properties` object. It
// errors when the schema is not a JSON object or its `properties` member
// is present but is not an object.
func schemaProperties(raw json.RawMessage) (map[string]bool, error) {
	var doc struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, errors.New("runtimeOptionsSchema must be a JSON object")
	}
	out := make(map[string]bool, len(doc.Properties))
	for name := range doc.Properties {
		out[name] = true
	}
	return out, nil
}

// forbiddenDerivedSchemaProperty returns the first property name declared
// in the derived runtimeOptionsSchema that is absent from the base
// schema's properties map, or "" when the derived schema is a subset.
// Per §5.1 a derived schema may only reference base property names. An
// empty base schema means the base declares no properties, so any
// derived property is forbidden.
func forbiddenDerivedSchemaProperty(derived, base json.RawMessage) (string, error) {
	if len(derived) == 0 {
		return "", nil
	}
	derivedProps, err := schemaProperties(derived)
	if err != nil {
		return "", err
	}
	baseProps, err := schemaProperties(base)
	if err != nil {
		return "", err
	}
	for name := range derivedProps {
		if !baseProps[name] {
			return name, nil
		}
	}
	return "", nil
}

// validateCapabilities checks a §5.1 capabilities block: a known
// interaction model, known injection modes, and the §5.1 coherence
// rule that a multi_turn runtime must support injection.
func validateCapabilities(c *runtimestore.RuntimeCapabilities) error {
	if c == nil {
		return nil
	}
	if c.Interaction != "" && !c.Interaction.IsValid() {
		return errors.New("capabilities.interaction must be one_shot or multi_turn")
	}
	for _, m := range c.Injection.Modes {
		if !m.IsValid() {
			return errors.New("capabilities.injection.modes contains an unrecognised mode")
		}
	}
	if c.Interaction == runtimestore.InteractionMultiTurn && !c.Injection.Supported {
		return errors.New("capabilities.interaction multi_turn requires injection.supported true")
	}
	return nil
}

// errAgentInterfaceOnMCP is returned from the update mutate closure when
// a PUT attaches an agentInterface to a type:mcp runtime (§5.1).
var errAgentInterfaceOnMCP = errors.New("agentInterface is not valid on a type:mcp runtime")

func fromRuntime(r runtimestore.Runtime) RuntimePayload {
	out := RuntimePayload{
		Name:                    r.Name,
		Type:                    string(r.Type),
		Image:                   r.Image,
		ExecutionMode:           string(r.ExecutionMode),
		IsolationProfile:        string(r.IsolationProfile),
		IntegrationLevel:        string(r.IntegrationLevel),
		WorkspaceTier:           string(r.WorkspaceTier),
		AllowedResourceClasses:  r.AllowedResourceClasses,
		SupportedProviders:      r.SupportedProviders,
		CredentialCapabilities:  r.CredentialCapabilities,
		AllowSelfRecursion:      r.AllowSelfRecursion,
		Description:             r.Description,
		DelegationPolicyRef:     r.DelegationPolicyRef,
		Labels:                  r.Labels,
		AgentInterface:          r.AgentInterface,
		PublishedMetadata:       r.PublishedMetadata,
		CapabilityInferenceMode: string(r.CapabilityInferenceMode),
		ToolCapabilityOverrides: r.ToolCapabilityOverrides,
		SetupPolicy:             r.SetupPolicy,
		Limits:                  r.Limits,
		SetupCommandPolicy:      r.SetupCommandPolicy,
		DefaultPoolConfig:       r.DefaultPoolConfig,
		WorkspaceDefaults:       r.WorkspaceDefaults,
		RuntimeOptionsSchema:    r.RuntimeOptionsSchema,
		SharedAssets:            r.SharedAssets,
		Capabilities:            r.Capabilities,
		SDKWarmBlockingPaths:    r.SDKWarmBlockingPaths,
		MinPlatformVersion:      r.MinPlatformVersion,
		TaskPolicy:              r.TaskPolicy,
		BaseRuntime:             r.BaseRuntime,
		CreatedAt:               rfc3339Nano(r.CreatedAt),
		UpdatedAt:               rfc3339Nano(r.UpdatedAt),
	}
	if !r.DeletedAt.IsZero() {
		out.DeletedAt = rfc3339Nano(r.DeletedAt)
	}
	return out
}

// WithRuntimes wires the §15.1 runtime CRUD handlers onto the Router.
// Call before Handler() so the mux picks them up.
func (r *Router) WithRuntimes(s runtimestore.Store) *Router {
	r.runtimes = s
	return r
}

// validatePayloadEnums applies the closed-enum checks the store does
// not — runtimes.Memory accepts arbitrary string values for the enum
// fields so the admin handler is the authoritative validator.
func (p RuntimePayload) validatePayloadEnums() error {
	if p.Type != "" && !runtimestore.RuntimeType(p.Type).IsValid() {
		return errors.New("type is not a recognised runtime type")
	}
	if p.ExecutionMode != "" && !runtimestore.ExecutionMode(p.ExecutionMode).IsValid() {
		return errors.New("executionMode is not a recognised mode")
	}
	if p.IsolationProfile != "" && !isolation.IsValid(isolation.Profile(p.IsolationProfile)) {
		return errors.New("isolationProfile is not a recognised §5.3 profile")
	}
	if p.IntegrationLevel != "" && !runtimestore.IntegrationLevel(p.IntegrationLevel).IsValid() {
		return errors.New("integrationLevel is not a recognised level")
	}
	if p.WorkspaceTier != "" && !runtimestore.WorkspaceTier(p.WorkspaceTier).IsValid() {
		return errors.New("workspaceTier is not a recognised §12.9 tier (T3, T4)")
	}
	if p.CapabilityInferenceMode != "" && !capabilityinference.Mode(p.CapabilityInferenceMode).IsValid() {
		return errors.New("capabilityInferenceMode must be strict or permissive")
	}
	if p.Image != "" {
		// §5.1 / §13.1: digest-pinned references only. Accept the
		// common forms `image@sha256:...` and `image:tag@sha256:...`.
		if !strings.Contains(p.Image, "@sha256:") {
			return errors.New("image must be digest-pinned (contain @sha256:...)")
		}
	}
	return nil
}

// validateIntegrationLevelOnType enforces §5.1 line 36: integrationLevel
// is only meaningful on type:agent runtimes and must not be set on
// type:mcp. An empty Type defaults to agent (ApplyDefaults), so an empty
// Type with a declared integrationLevel is allowed.
func (p RuntimePayload) validateIntegrationLevelOnType() error {
	if p.IntegrationLevel != "" && runtimestore.RuntimeType(p.Type) == runtimestore.TypeMCP {
		return errors.New("integrationLevel is only valid on type: agent runtimes")
	}
	return nil
}

func (r *Router) handleCreateRuntime(w http.ResponseWriter, req *http.Request) {
	var body RuntimePayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	if err := runtimestore.ValidateName(body.Name); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(),
			map[string]any{"field": "name"})
		return
	}
	if err := body.validatePayloadEnums(); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	if err := runtimestore.ValidatePublishedMetadata(body.PublishedMetadata); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	if err := capabilityinference.ValidateOverrides(body.ToolCapabilityOverrides); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	if err := validateSetupPolicy(body.SetupPolicy, r.maxFinalizingTimeoutSeconds); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	if err := validateLimits(body.Limits); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	if err := validateSetupCommandPolicy(body.SetupCommandPolicy); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	if err := validateDefaultPoolConfig(body.DefaultPoolConfig); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	if err := validateWorkspaceDefaults(body.WorkspaceDefaults); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	if err := validateSharedAssets(body.SharedAssets); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	if err := validateRuntimeOptionsSchema(body.RuntimeOptionsSchema); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	// §5.1 line 36: integrationLevel is only valid on type:agent runtimes.
	if err := body.validateIntegrationLevelOnType(); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_RUNTIME", err.Error(), nil)
		return
	}
	if err := validateCapabilities(body.Capabilities); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	if err := r.validateMinPlatformVersion(body.MinPlatformVersion); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	if err := validateTaskPolicy(body.TaskPolicy); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	if err := r.validateDerivedRuntime(req.Context(), body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_DERIVED_RUNTIME", err.Error(), nil)
		return
	}

	rt := runtimeFromPayload(body, r.clock())
	runtimestore.ApplyDefaults(&rt, r.devMode)
	rt.UpdatedAt = rt.CreatedAt
	// §5.1: type:mcp runtimes do not carry an agentInterface.
	if rt.Type == runtimestore.TypeMCP && rt.AgentInterface != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"agentInterface is not valid on a type:mcp runtime", nil)
		return
	}
	// §5.1: a runtime with an agentInterface gets a write-time
	// auto-generated A2A agent card stored as a publishedMetadata entry.
	r.applyGeneratedCard(&rt, rt.CreatedAt)
	// spec: §15.1 line 1140 — ?dryRun=true validates without persisting or auditing.
	if req.URL.Query().Get("dryRun") == "true" {
		writeDryRun(w, http.StatusCreated, fromRuntime(rt))
		return
	}
	if err := r.runtimes.Create(req.Context(), rt); err != nil {
		if errors.Is(err, runtimestore.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "RESOURCE_CONFLICT",
				"runtime with this name already exists",
				map[string]any{"name": body.Name})
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	stored, _ := r.runtimes.Get(req.Context(), body.Name)
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "admin.runtime.created", body.Name, map[string]any{
		"type":             string(stored.Type),
		"executionMode":    string(stored.ExecutionMode),
		"isolationProfile": string(stored.IsolationProfile),
		"integrationLevel": string(stored.IntegrationLevel),
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(fromRuntime(stored))
}

func (r *Router) handleListRuntimes(w http.ResponseWriter, req *http.Request) {
	filter := runtimestore.ListFilter{
		IncludeDeleted: req.URL.Query().Get("includeDeleted") == "true",
		Type:           runtimestore.RuntimeType(req.URL.Query().Get("type")),
	}
	rows, err := r.runtimes.List(req.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// §4: a tenant-admin sees only runtimes granted to their tenant
	// through the runtime_tenant_access table; platform-admin is
	// unfiltered.
	if tenantID, filtered := tenantScopeFilter(req); filtered {
		allowed := r.accessibleSet(req.Context(), tenantaccessstore.KindRuntime, tenantID)
		kept := rows[:0]
		for _, rt := range rows {
			if allowed[rt.Name] {
				kept = append(kept, rt)
			}
		}
		rows = kept
	}
	out := make([]RuntimePayload, 0, len(rows))
	for _, rt := range rows {
		out = append(out, fromRuntime(rt))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"runtimes": out})
}

func (r *Router) handleGetRuntime(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	row, err := r.runtimes.Get(req.Context(), name)
	if err != nil {
		if errors.Is(err, runtimestore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "runtime not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// §4: a tenant-admin may read a runtime only when their tenant
	// holds an access grant for it.
	if tenantID, filtered := tenantScopeFilter(req); filtered {
		if !r.accessibleSet(req.Context(), tenantaccessstore.KindRuntime, tenantID)[row.Name] {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "runtime not found", nil)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromRuntime(row))
}

// applyRuntimeUpdate merges a §15.1 UpdateRuntimeRequest onto a runtime
// in place, applying the §5.1 field-merge rules and regenerating the
// auto-generated A2A agent card. It is the single merge implementation
// shared by the real store Update closure and the dry-run preview, so
// the preview reflects exactly what a persisted update would produce.
// agentInterfaceSet / newAgentInterface carry the parsed agentInterface
// tri-state resolved by the handler before the store transaction opens.
func (r *Router) applyRuntimeUpdate(rt *runtimestore.Runtime, body UpdateRuntimeRequest, agentInterfaceSet bool, newAgentInterface *runtimestore.AgentInterface) error {
	if body.Image != nil {
		rt.Image = *body.Image
	}
	if body.ExecutionMode != nil {
		rt.ExecutionMode = runtimestore.ExecutionMode(*body.ExecutionMode)
	}
	if body.IsolationProfile != nil {
		rt.IsolationProfile = isolation.Profile(*body.IsolationProfile)
	}
	if body.IntegrationLevel != nil {
		rt.IntegrationLevel = runtimestore.IntegrationLevel(*body.IntegrationLevel)
	}
	if body.WorkspaceTier != nil {
		rt.WorkspaceTier = runtimestore.WorkspaceTier(*body.WorkspaceTier)
	}
	if body.AllowedResourceClasses != nil {
		rt.AllowedResourceClasses = *body.AllowedResourceClasses
	}
	if body.SupportedProviders != nil {
		rt.SupportedProviders = *body.SupportedProviders
	}
	if body.CredentialCapabilities != nil {
		rt.CredentialCapabilities = body.CredentialCapabilities
	}
	if body.AllowSelfRecursion != nil {
		rt.AllowSelfRecursion = *body.AllowSelfRecursion
	}
	if body.Description != nil {
		rt.Description = *body.Description
	}
	if body.Labels != nil {
		rt.Labels = *body.Labels
	}
	if body.DelegationPolicyRef != nil {
		rt.DelegationPolicyRef = *body.DelegationPolicyRef
	}
	if agentInterfaceSet {
		// §5.1: type:mcp runtimes do not carry an agentInterface.
		if rt.Type == runtimestore.TypeMCP && newAgentInterface != nil {
			return errAgentInterfaceOnMCP
		}
		rt.AgentInterface = newAgentInterface
	}
	if body.PublishedMetadata != nil {
		rt.PublishedMetadata = *body.PublishedMetadata
	}
	if body.CapabilityInferenceMode != nil {
		rt.CapabilityInferenceMode = capabilityinference.Mode(*body.CapabilityInferenceMode)
	}
	if body.ToolCapabilityOverrides != nil {
		rt.ToolCapabilityOverrides = *body.ToolCapabilityOverrides
	}
	if body.SetupPolicy != nil {
		rt.SetupPolicy = body.SetupPolicy
	}
	if body.Limits != nil {
		rt.Limits = body.Limits
	}
	if body.SetupCommandPolicy != nil {
		rt.SetupCommandPolicy = body.SetupCommandPolicy
	}
	if body.DefaultPoolConfig != nil {
		rt.DefaultPoolConfig = body.DefaultPoolConfig
	}
	if body.WorkspaceDefaults != nil {
		rt.WorkspaceDefaults = body.WorkspaceDefaults
	}
	if body.SharedAssets != nil {
		rt.SharedAssets = *body.SharedAssets
	}
	if len(body.RuntimeOptionsSchema) > 0 {
		rt.RuntimeOptionsSchema = body.RuntimeOptionsSchema
	}
	if body.Capabilities != nil {
		rt.Capabilities = body.Capabilities
	}
	if body.SDKWarmBlockingPaths != nil {
		rt.SDKWarmBlockingPaths = *body.SDKWarmBlockingPaths
	}
	if body.MinPlatformVersion != nil {
		rt.MinPlatformVersion = *body.MinPlatformVersion
	}
	if body.TaskPolicy != nil {
		rt.TaskPolicy = body.TaskPolicy
	}
	if body.BaseRuntime != nil {
		rt.BaseRuntime = *body.BaseRuntime
	}
	// §5.1: regenerate the auto-generated A2A agent card at write
	// time whenever the runtime carries an agentInterface.
	r.applyGeneratedCard(rt, r.clock())
	return nil
}

func (r *Router) handleUpdateRuntime(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	var body UpdateRuntimeRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	// Validate enums when present.
	if body.ExecutionMode != nil && *body.ExecutionMode != "" && !runtimestore.ExecutionMode(*body.ExecutionMode).IsValid() {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"executionMode is not a recognised mode", nil)
		return
	}
	if body.IsolationProfile != nil && *body.IsolationProfile != "" && !isolation.IsValid(isolation.Profile(*body.IsolationProfile)) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"isolationProfile is not a recognised §5.3 profile", nil)
		return
	}
	if body.CapabilityInferenceMode != nil && *body.CapabilityInferenceMode != "" &&
		!capabilityinference.Mode(*body.CapabilityInferenceMode).IsValid() {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"capabilityInferenceMode must be strict or permissive", nil)
		return
	}
	if body.IntegrationLevel != nil && *body.IntegrationLevel != "" && !runtimestore.IntegrationLevel(*body.IntegrationLevel).IsValid() {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"integrationLevel is not a recognised level", nil)
		return
	}
	if body.WorkspaceTier != nil && *body.WorkspaceTier != "" && !runtimestore.WorkspaceTier(*body.WorkspaceTier).IsValid() {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"workspaceTier is not a recognised §12.9 tier (T3, T4)", nil)
		return
	}
	if body.Image != nil && *body.Image != "" && !strings.Contains(*body.Image, "@sha256:") {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"image must be digest-pinned (contain @sha256:...)", nil)
		return
	}
	// agentInterface: an omitted key leaves the descriptor unchanged; a
	// present key (JSON null or an object) is parsed here so malformed
	// JSON fails as 400 before the store transaction opens.
	agentInterfaceSet := len(body.AgentInterface) > 0
	var newAgentInterface *runtimestore.AgentInterface
	if agentInterfaceSet {
		if err := json.Unmarshal(body.AgentInterface, &newAgentInterface); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"agentInterface is not valid JSON", nil)
			return
		}
	}
	if body.PublishedMetadata != nil {
		if err := runtimestore.ValidatePublishedMetadata(*body.PublishedMetadata); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
			return
		}
	}
	if body.ToolCapabilityOverrides != nil {
		if err := capabilityinference.ValidateOverrides(*body.ToolCapabilityOverrides); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
			return
		}
	}
	if body.SetupPolicy != nil {
		if err := validateSetupPolicy(body.SetupPolicy, r.maxFinalizingTimeoutSeconds); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
			return
		}
	}
	if body.Limits != nil {
		if err := validateLimits(body.Limits); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
			return
		}
	}
	if body.SetupCommandPolicy != nil {
		if err := validateSetupCommandPolicy(body.SetupCommandPolicy); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
			return
		}
	}
	if body.DefaultPoolConfig != nil {
		if err := validateDefaultPoolConfig(body.DefaultPoolConfig); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
			return
		}
	}
	if body.WorkspaceDefaults != nil {
		if err := validateWorkspaceDefaults(body.WorkspaceDefaults); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
			return
		}
	}
	if body.SharedAssets != nil {
		if err := validateSharedAssets(*body.SharedAssets); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
			return
		}
	}
	if len(body.RuntimeOptionsSchema) > 0 {
		if err := validateRuntimeOptionsSchema(body.RuntimeOptionsSchema); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
			return
		}
	}
	if body.Capabilities != nil {
		if err := validateCapabilities(body.Capabilities); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
			return
		}
	}
	if body.MinPlatformVersion != nil {
		if err := r.validateMinPlatformVersion(*body.MinPlatformVersion); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
			return
		}
	}
	if body.TaskPolicy != nil {
		if err := validateTaskPolicy(body.TaskPolicy); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
			return
		}
	}
	if current, err := r.runtimes.Get(req.Context(), name); err == nil {
		// §5.1 line 36: integrationLevel is only valid on type:agent. Type
		// is immutable via PUT, so the current row's type is authoritative.
		if body.IntegrationLevel != nil && *body.IntegrationLevel != "" && current.Type == runtimestore.TypeMCP {
			writeError(w, http.StatusBadRequest, "INVALID_RUNTIME",
				"integrationLevel is only valid on type: agent runtimes", nil)
			return
		}
		// §5.1 line 174: a base runtime's image is immutable via the admin
		// API — the §10.5 RuntimeUpgrade orchestration is the only
		// legitimate writer. (A derived runtime's image is rejected
		// separately by validateDerivedRuntimeUpdate as prohibited.)
		if !current.IsDerived() && body.Image != nil && *body.Image != current.Image {
			writeError(w, http.StatusBadRequest, "IMAGE_IMMUTABLE",
				"base runtime image is immutable; use RuntimeUpgrade to change runtime image", nil)
			return
		}
		// §5.1 restrict-only invariants on supportedProviders /
		// allowSelfRecursion are evaluated against the resolved base.
		var base runtimestore.Runtime
		if current.IsDerived() {
			base, _ = r.runtimes.Get(req.Context(), current.BaseRuntime)
		}
		if derr := validateDerivedRuntimeUpdate(current, base, body); derr != nil {
			writeError(w, http.StatusBadRequest, "INVALID_DERIVED_RUNTIME", derr.Error(), nil)
			return
		}
		// §5.1 line 174: a base runtime is mutable with impact validation —
		// a change that would invalidate an existing derived runtime is
		// rejected with the list of affected runtimes.
		if !current.IsDerived() {
			affected, ierr := r.derivedRuntimesInvalidatedBy(req.Context(), name,
				proposedBaseRuntime(current, body))
			if ierr != nil {
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", ierr.Error(), nil)
				return
			}
			if len(affected) > 0 {
				writeError(w, http.StatusBadRequest, "BASE_RUNTIME_MUTATION_INVALIDATES_DERIVED",
					"change would invalidate derived runtimes",
					map[string]any{"affectedRuntimes": affected})
				return
			}
		}
	}
	// spec: §15.1 line 1140 — ?dryRun=true validates without persisting or auditing.
	// The PUT resolves the current runtime first so the preview reflects
	// applying the body onto the stored record; a missing runtime 404s
	// ahead of the dry-run branch.
	if req.URL.Query().Get("dryRun") == "true" {
		current, gerr := r.runtimes.Get(req.Context(), name)
		if gerr != nil {
			if errors.Is(gerr, runtimestore.ErrNotFound) {
				writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "runtime not found", nil)
				return
			}
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", gerr.Error(), nil)
			return
		}
		preview := current
		if err := r.applyRuntimeUpdate(&preview, body, agentInterfaceSet, newAgentInterface); err != nil {
			// The only error the merge returns is errAgentInterfaceOnMCP
			// (§5.1: type:mcp runtimes carry no agentInterface), a 400.
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
			return
		}
		writeDryRun(w, http.StatusOK, fromRuntime(preview))
		return
	}
	updated, err := r.runtimes.Update(req.Context(), name, func(rt *runtimestore.Runtime) error {
		return r.applyRuntimeUpdate(rt, body, agentInterfaceSet, newAgentInterface)
	})
	if err != nil {
		if errors.Is(err, runtimestore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "runtime not found", nil)
			return
		}
		if errors.Is(err, errAgentInterfaceOnMCP) {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "admin.runtime.updated", name, map[string]any{
		"changedFields": changedRuntimeFields(body),
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromRuntime(updated))
}

func (r *Router) handleDeleteRuntime(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	if err := r.runtimes.SoftDelete(req.Context(), name, r.clock()); err != nil {
		if errors.Is(err, runtimestore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "runtime not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "admin.runtime.soft_deleted", name, nil)
	w.WriteHeader(http.StatusNoContent)
}

func changedRuntimeFields(b UpdateRuntimeRequest) []string {
	var out []string
	if b.Image != nil {
		out = append(out, "image")
	}
	if b.ExecutionMode != nil {
		out = append(out, "executionMode")
	}
	if b.IsolationProfile != nil {
		out = append(out, "isolationProfile")
	}
	if b.IntegrationLevel != nil {
		out = append(out, "integrationLevel")
	}
	if b.WorkspaceTier != nil {
		out = append(out, "workspaceTier")
	}
	if b.AllowedResourceClasses != nil {
		out = append(out, "allowedResourceClasses")
	}
	if b.SupportedProviders != nil {
		out = append(out, "supportedProviders")
	}
	if b.CredentialCapabilities != nil {
		out = append(out, "credentialCapabilities")
	}
	if b.AllowSelfRecursion != nil {
		out = append(out, "allowSelfRecursion")
	}
	if b.Description != nil {
		out = append(out, "description")
	}
	if len(b.AgentInterface) > 0 {
		out = append(out, "agentInterface")
	}
	if b.PublishedMetadata != nil {
		out = append(out, "publishedMetadata")
	}
	if b.CapabilityInferenceMode != nil {
		out = append(out, "capabilityInferenceMode")
	}
	if b.ToolCapabilityOverrides != nil {
		out = append(out, "toolCapabilityOverrides")
	}
	if b.SetupPolicy != nil {
		out = append(out, "setupPolicy")
	}
	if b.Limits != nil {
		out = append(out, "limits")
	}
	if b.SetupCommandPolicy != nil {
		out = append(out, "setupCommandPolicy")
	}
	if b.DefaultPoolConfig != nil {
		out = append(out, "defaultPoolConfig")
	}
	if b.WorkspaceDefaults != nil {
		out = append(out, "workspaceDefaults")
	}
	if b.SharedAssets != nil {
		out = append(out, "sharedAssets")
	}
	if len(b.RuntimeOptionsSchema) > 0 {
		out = append(out, "runtimeOptionsSchema")
	}
	if b.Capabilities != nil {
		out = append(out, "capabilities")
	}
	if b.MinPlatformVersion != nil {
		out = append(out, "minPlatformVersion")
	}
	if b.TaskPolicy != nil {
		out = append(out, "taskPolicy")
	}
	if b.BaseRuntime != nil {
		out = append(out, "baseRuntime")
	}
	return out
}

// RegenerateCardsRequest is the §5.1 POST /v1/admin/runtimes/regenerate-cards
// body. Every field is optional.
type RegenerateCardsRequest struct {
	// GeneratorVersionBefore regenerates only runtimes whose stored card
	// has a generatorVersion strictly older than this value. An empty
	// value regenerates every runtime that carries an agentInterface.
	GeneratorVersionBefore string `json:"generatorVersionBefore,omitempty"`

	// DryRun reports the affected runtimes without writing.
	DryRun bool `json:"dryRun,omitempty"`
}

// RegenerateCardsResponse is the §5.1 regenerate-cards result.
type RegenerateCardsResponse struct {
	Regenerated []string `json:"regenerated"`
	Skipped     []string `json:"skipped"`
	Errors      []string `json:"errors"`
}

// storedGeneratorVersion reads the generatorVersion envelope field from
// a runtime's stored agent-card entry. It returns "" when the runtime
// has no agent-card entry or the entry is not a parseable card.
func storedGeneratorVersion(entries []runtimestore.PublishedMetadataEntry) string {
	for _, e := range entries {
		if e.Key != agentcard.Key {
			continue
		}
		var card struct {
			GeneratorVersion string `json:"generatorVersion"`
		}
		_ = json.Unmarshal([]byte(e.Content), &card)
		return card.GeneratorVersion
	}
	return ""
}

// handleRegenerateCards implements POST /v1/admin/runtimes/regenerate-cards
// — the §5.1 bulk A2A agent-card regeneration. Each runtime carrying an
// agentInterface whose stored card is older than generatorVersionBefore
// has its agent-card publishedMetadata entry regenerated. A runtime with
// no agentInterface is skipped, so a hand-crafted agent-card entry is
// left untouched; a runtime whose card already satisfies the version
// threshold is skipped. dryRun reports the affected runtimes without
// writing.
func (r *Router) handleRegenerateCards(w http.ResponseWriter, req *http.Request) {
	var body RegenerateCardsRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	rows, err := r.runtimes.List(req.Context(), runtimestore.ListFilter{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	resp := RegenerateCardsResponse{Regenerated: []string{}, Skipped: []string{}, Errors: []string{}}
	for _, rt := range rows {
		if rt.AgentInterface == nil {
			// §5.1: a hand-crafted agent-card entry is left untouched.
			resp.Skipped = append(resp.Skipped, rt.Name)
			continue
		}
		if !agentcard.NeedsRegen(storedGeneratorVersion(rt.PublishedMetadata), body.GeneratorVersionBefore) {
			resp.Skipped = append(resp.Skipped, rt.Name)
			continue
		}
		if body.DryRun {
			resp.Regenerated = append(resp.Regenerated, rt.Name)
			continue
		}
		if _, uerr := r.runtimes.Update(req.Context(), rt.Name, func(u *runtimestore.Runtime) error {
			r.applyGeneratedCard(u, r.clock())
			return nil
		}); uerr != nil {
			resp.Errors = append(resp.Errors, rt.Name)
			continue
		}
		resp.Regenerated = append(resp.Regenerated, rt.Name)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
