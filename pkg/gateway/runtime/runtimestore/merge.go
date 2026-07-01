// SPDX-License-Identifier: MIT

package runtimestore

import (
	"context"
	"encoding/json"
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
//     allowedResourceClasses, workspaceTier, requireSoPeercred) are always
//     taken from the base; a derived runtime may not set them.
//   - Override fields (description, delegationPolicyRef, agentInterface,
//     sessionPolicy, capabilityInferenceMode, toolCapabilityOverrides,
//     minPlatformVersion, supportedProviders, credentialCapabilities,
//     limits, setupCommandPolicy, defaultPoolConfig) take the derived
//     value when it is set and the base value otherwise.
//   - allowSelfRecursion is Override (restrict-only): the derived value
//     wins, and the restrict-only invariant is enforced at registration.
//   - publishedMetadata appends the derived entries onto the base list,
//     with a duplicate key won by the derived entry.
//   - labels overlay the derived keys onto the base map.
//   - setupPolicy.timeoutSeconds takes max(base, derived), where zero
//     ("no cap") counts as the largest bound and wins; onTimeout takes the
//     derived value when it is set.
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
	// §12.9 / §5.2: workspaceTier is a property of the base image's data
	// classification, so a derived runtime inherits the base's tier.
	eff.WorkspaceTier = cb.WorkspaceTier
	// §4.7 / §5.1: requireSoPeercred is Inherited (derived may not set).
	// The SO_PEERCRED authentication-boundary posture is a property of the
	// base runtime's image and isolation environment, so a derived runtime
	// resolved against a nonce-only base inherits that posture; cb was
	// deep-copied by cloneRuntime, so the pointer is not shared with base.
	eff.RequireSoPeercred = cb.RequireSoPeercred
	eff.Capabilities = cb.Capabilities
	eff.AllowedResourceClasses = append([]string(nil), cb.AllowedResourceClasses...)
	// §5.1 lines 22-24: sdkWarmBlockingPaths is the companion of the
	// inherited capabilities.preConnect flag (SDK-warm is a property of
	// the base image's pre-connect behavior). It is taken from the base, so
	// a derived runtime inherits the base's demotion path set.
	eff.SDKWarmBlockingPaths = append([]string(nil), cb.SDKWarmBlockingPaths...)

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
	if derived.SessionPolicy == nil {
		eff.SessionPolicy = cb.SessionPolicy
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
	// §13.4 archivePolicy is Override: the derived runtime's opt-in block
	// wholly replaces the base block; the base applies otherwise. The
	// platform default (no symlinks) is encoded as a nil block. F-7.4.4.
	if derived.ArchivePolicy == nil {
		eff.ArchivePolicy = cb.ArchivePolicy.Clone()
	}
	if derived.DefaultPoolConfig == nil {
		eff.DefaultPoolConfig = cb.DefaultPoolConfig.Clone()
	}
	// §5.1 runtimeOptionsSchema is Override: the derived schema replaces
	// the base when set, and the base applies otherwise. The derived
	// schema's property-subset invariant is enforced at registration.
	if len(derived.RuntimeOptionsSchema) == 0 {
		eff.RuntimeOptionsSchema = append(json.RawMessage(nil), cb.RuntimeOptionsSchema...)
	}

	// Collection merges.
	eff.Labels = mergeLabels(cb.Labels, eff.Labels)
	eff.PublishedMetadata = mergePublishedMetadata(cb.PublishedMetadata, eff.PublishedMetadata)
	eff.SetupPolicy = mergeSetupPolicy(cb.SetupPolicy, eff.SetupPolicy)
	eff.WorkspaceDefaults = mergeWorkspaceDefaults(cb.WorkspaceDefaults, eff.WorkspaceDefaults)
	eff.SharedAssets = mergeSharedAssets(cb.SharedAssets, eff.SharedAssets)

	return eff
}

// mergeWorkspaceDefaults applies the §5.1 Append merge: derived files are
// appended onto the base file list with a conflicting Path replaced in
// place by the derived entry, and derived setup commands are appended
// after the base setup commands (order: base defaults → derived
// defaults). When only one side declares a block that side is used.
func mergeWorkspaceDefaults(base, derived *WorkspaceDefaults) *WorkspaceDefaults {
	if base == nil {
		return derived
	}
	if derived == nil {
		return base
	}
	out := &WorkspaceDefaults{
		Files:         mergeWorkspaceFiles(base.Files, derived.Files),
		SetupCommands: append(append([]WorkspaceSetupCommand(nil), base.SetupCommands...), derived.SetupCommands...),
	}
	return out
}

// mergeWorkspaceFiles appends the derived files onto the base list, with
// a derived entry whose Path matches a base entry replacing the base
// entry in place. A derived-only Path is appended after the base entries.
func mergeWorkspaceFiles(base, derived []WorkspaceFile) []WorkspaceFile {
	if len(base) == 0 && len(derived) == 0 {
		return nil
	}
	derivedByPath := make(map[string]WorkspaceFile, len(derived))
	for _, f := range derived {
		derivedByPath[f.Path] = f
	}
	out := make([]WorkspaceFile, 0, len(base)+len(derived))
	seen := make(map[string]bool, len(base))
	for _, f := range base {
		if d, ok := derivedByPath[f.Path]; ok {
			out = append(out, d)
		} else {
			out = append(out, f)
		}
		seen[f.Path] = true
	}
	for _, f := range derived {
		if !seen[f.Path] {
			out = append(out, f)
		}
	}
	return out
}

// mergeSharedAssets applies the §5.1 Append merge: derived assets are
// appended onto the base list with a conflicting DestPath replaced in
// place by the derived entry.
func mergeSharedAssets(base, derived []SharedAsset) []SharedAsset {
	if len(base) == 0 && len(derived) == 0 {
		return nil
	}
	derivedByDest := make(map[string]SharedAsset, len(derived))
	for _, a := range derived {
		derivedByDest[a.DestPath] = a
	}
	out := make([]SharedAsset, 0, len(base)+len(derived))
	seen := make(map[string]bool, len(base))
	for _, a := range base {
		if d, ok := derivedByDest[a.DestPath]; ok {
			out = append(out, d)
		} else {
			out = append(out, a)
		}
		seen[a.DestPath] = true
	}
	for _, a := range derived {
		if !seen[a.DestPath] {
			out = append(out, a)
		}
	}
	return out
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
	// §5.1 line 195: timeoutSeconds is Maximum. Zero means "no aggregate
	// cap" (waits indefinitely), which is the largest possible bound — it
	// wins the max over any finite value so a base's no-cap floor survives
	// a finite derived value. The registration validator rejects a mixed
	// pair where exactly one side is zero ("neither can be zero if the
	// other is set"); this branch is the deterministic fallback if a base
	// is later mutated into a mismatched state.
	switch {
	case base.TimeoutSeconds == 0 || derived.TimeoutSeconds == 0:
		out.TimeoutSeconds = 0
	case derived.TimeoutSeconds > base.TimeoutSeconds:
		out.TimeoutSeconds = derived.TimeoutSeconds
	}
	if derived.OnTimeout != "" {
		out.OnTimeout = derived.OnTimeout
	}
	return out
}
