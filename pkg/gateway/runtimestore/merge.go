// SPDX-License-Identifier: MIT

package runtimestore

import (
	"context"
	"fmt"
)

// Resolve returns the effective runtime registered under name. For a
// standalone runtime the effective runtime is the runtime itself. For a
// §5.1 derived runtime it is Merge(base, derived) — the derived runtime
// resolved against its base. A derived runtime whose base is missing
// resolves to an error.
func Resolve(ctx context.Context, s Store, name string) (Runtime, error) {
	rt, err := s.Get(ctx, name)
	if err != nil {
		return Runtime{}, err
	}
	if !rt.IsDerived() {
		return rt, nil
	}
	base, err := s.Get(ctx, rt.BaseRuntime)
	if err != nil {
		return Runtime{}, fmt.Errorf("runtimestore: derived runtime %q base %q: %w", name, rt.BaseRuntime, err)
	}
	return Merge(base, rt), nil
}

// Merge resolves a §5.1 derived runtime against its base, returning the
// effective runtime the gateway uses for pod scheduling and session
// validation. It applies the §5.1 normative per-field merge rules:
//
//   - Inherited / Prohibited fields (image, type, executionMode,
//     isolationProfile, integrationLevel, capabilities,
//     allowedResourceClasses) are always taken from the base; a derived
//     runtime may not set them.
//   - Override fields (description, delegationPolicyRef, agentInterface,
//     taskPolicy, capabilityInferenceMode, toolCapabilityOverrides,
//     minPlatformVersion, supportedProviders, credentialCapabilities,
//     limits, setupCommandPolicy, defaultPoolConfig) take the derived
//     value when it is set and the base value otherwise.
//   - allowSelfRecursion is Override (restrict-only): the derived value
//     wins, and the restrict-only invariant is enforced at registration.
//   - publishedMetadata appends the derived entries onto the base list,
//     with a duplicate key won by the derived entry.
//   - labels overlay the derived keys onto the base map.
//   - setupPolicy.timeoutSeconds takes max(base, derived); onTimeout
//     takes the derived value when it is set.
//
// The result is a fully resolved standalone runtime: its BaseRuntime is
// empty and it shares no mutable state with the base or derived inputs.
func Merge(base, derived Runtime) Runtime {
	eff := cloneRuntime(derived)
	cb := cloneRuntime(base)
	eff.BaseRuntime = ""

	// Inherited / Prohibited — always from base; a derived runtime may
	// not declare these (the admin registration validator rejects a
	// derived payload that sets one). allowedResourceClasses is the §5.1
	// Prohibited row: derived inherits the base set.
	eff.Image = cb.Image
	eff.Type = cb.Type
	eff.ExecutionMode = cb.ExecutionMode
	eff.IsolationProfile = cb.IsolationProfile
	eff.IntegrationLevel = cb.IntegrationLevel
	eff.Capabilities = cb.Capabilities
	eff.AllowedResourceClasses = append([]string(nil), cb.AllowedResourceClasses...)

	// §5.1 allowSelfRecursion is Override (restrict-only). The derived
	// value wins; the restrict-only invariant (derived true rejected when
	// base false) is enforced at registration.
	eff.AllowSelfRecursion = derived.AllowSelfRecursion

	// Override — derived wins when set, base otherwise.
	if derived.Description == "" {
		eff.Description = cb.Description
	}
	if derived.DelegationPolicyRef == "" {
		eff.DelegationPolicyRef = cb.DelegationPolicyRef
	}
	if derived.AgentInterface == nil {
		eff.AgentInterface = cb.AgentInterface
	}
	if derived.TaskPolicy == nil {
		eff.TaskPolicy = cb.TaskPolicy
	}
	if derived.CapabilityInferenceMode == "" {
		eff.CapabilityInferenceMode = cb.CapabilityInferenceMode
	}
	if derived.ToolCapabilityOverrides == nil {
		eff.ToolCapabilityOverrides = cb.ToolCapabilityOverrides
	}
	if derived.MinPlatformVersion == "" {
		eff.MinPlatformVersion = cb.MinPlatformVersion
	}
	// §5.1 supportedProviders is Override: the derived set replaces the
	// base set when present, and the base set applies when the derived
	// runtime declares none. The restrict-only subset invariant (derived
	// must not expand beyond base) is enforced at registration.
	if len(derived.SupportedProviders) == 0 {
		eff.SupportedProviders = append([]string(nil), cb.SupportedProviders...)
	}
	// §5.1 credentialCapabilities is Override: the derived block replaces
	// the base block when the derived runtime declares one, and the base
	// block applies when the derived runtime omits it.
	if derived.CredentialCapabilities == nil {
		eff.CredentialCapabilities = cb.CredentialCapabilities.Clone()
	}
	// §5.1 limits, setupCommandPolicy, and defaultPoolConfig are Override:
	// the derived block wholly replaces the base block when the derived
	// runtime declares one, and the base block applies otherwise.
	if derived.Limits == nil {
		eff.Limits = cb.Limits.Clone()
	}
	if derived.SetupCommandPolicy == nil {
		eff.SetupCommandPolicy = cb.SetupCommandPolicy.Clone()
	}
	if derived.DefaultPoolConfig == nil {
		eff.DefaultPoolConfig = cb.DefaultPoolConfig.Clone()
	}

	// Collection merges.
	eff.Labels = mergeLabels(cb.Labels, eff.Labels)
	eff.PublishedMetadata = mergePublishedMetadata(cb.PublishedMetadata, eff.PublishedMetadata)
	eff.SetupPolicy = mergeSetupPolicy(cb.SetupPolicy, eff.SetupPolicy)

	return eff
}

// mergeLabels overlays the derived label keys onto the base label map,
// per the §5.1 "Merge" rule. A conflicting key is won by derived.
func mergeLabels(base, derived map[string]string) map[string]string {
	if len(base) == 0 && len(derived) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(derived))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range derived {
		out[k] = v
	}
	return out
}

// mergePublishedMetadata appends the derived publishedMetadata entries
// onto the base list, per the §5.1 "Append" rule. A derived entry whose
// key is already present in the base list replaces the base entry in
// place; a derived-only key is appended after the base entries.
func mergePublishedMetadata(base, derived []PublishedMetadataEntry) []PublishedMetadataEntry {
	if len(base) == 0 && len(derived) == 0 {
		return nil
	}
	derivedByKey := make(map[string]PublishedMetadataEntry, len(derived))
	for _, e := range derived {
		derivedByKey[e.Key] = e
	}
	out := make([]PublishedMetadataEntry, 0, len(base)+len(derived))
	seen := make(map[string]bool, len(base))
	for _, e := range base {
		if d, ok := derivedByKey[e.Key]; ok {
			out = append(out, d)
		} else {
			out = append(out, e)
		}
		seen[e.Key] = true
	}
	for _, e := range derived {
		if !seen[e.Key] {
			out = append(out, e)
		}
	}
	return out
}

// mergeSetupPolicy applies the §5.1 field-level setupPolicy merge:
// timeoutSeconds takes max(base, derived) and onTimeout takes the
// derived value when set. When only one side declares a policy that
// side is used unchanged.
func mergeSetupPolicy(base, derived *SetupPolicy) *SetupPolicy {
	if base == nil {
		return derived
	}
	if derived == nil {
		return base
	}
	out := &SetupPolicy{
		TimeoutSeconds: base.TimeoutSeconds,
		OnTimeout:      base.OnTimeout,
	}
	if derived.TimeoutSeconds > out.TimeoutSeconds {
		out.TimeoutSeconds = derived.TimeoutSeconds
	}
	if derived.OnTimeout != "" {
		out.OnTimeout = derived.OnTimeout
	}
	return out
}
