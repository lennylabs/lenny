// SPDX-License-Identifier: MIT

package runtimestore

import "fmt"

// CapabilityOverride is the §5.1 line 49 per-tenant capability
// customization: a tenant overlays a subset of a runtime's declared
// §5.1 capabilities block onto the platform default. Every field is
// optional. A nil field inherits the runtime's declared value; a non-nil
// field replaces it for the tenant. The overlay covers the five
// tenant-customizable axes the §5.1 cross-reference names: interaction,
// injection support, injection modes, preConnect, and the
// runtime-level sdkWarmBlockingPaths list.
//
// spec: §5.1 line 49 — F-5.1.20.
type CapabilityOverride struct {
	// Interaction overrides capabilities.interaction. nil inherits.
	Interaction *RuntimeInteraction `json:"interaction,omitempty"`

	// InjectionSupported overrides capabilities.injection.supported. nil
	// inherits.
	InjectionSupported *bool `json:"injectionSupported,omitempty"`

	// InjectionModes overrides capabilities.injection.modes. A nil pointer
	// inherits; a non-nil pointer (including a pointer to an empty slice)
	// replaces the declared modes, so a tenant can pin a runtime to a
	// single mode or strip every mode.
	InjectionModes *[]InjectionMode `json:"injectionModes,omitempty"`

	// PreConnect overrides capabilities.preConnect. nil inherits.
	PreConnect *bool `json:"preConnect,omitempty"`

	// SDKWarmBlockingPaths overrides the runtime-level sdkWarmBlockingPaths
	// list. A nil pointer inherits; a non-nil pointer replaces the list,
	// so a tenant can add tenant-specific demotion blockers (or clear
	// them).
	SDKWarmBlockingPaths *[]string `json:"sdkWarmBlockingPaths,omitempty"`
}

// IsZero reports whether the override sets no field, in which case the
// overlay is a no-op.
func (o CapabilityOverride) IsZero() bool {
	return o.Interaction == nil &&
		o.InjectionSupported == nil &&
		o.InjectionModes == nil &&
		o.PreConnect == nil &&
		o.SDKWarmBlockingPaths == nil
}

// Validate rejects an override that sets an invalid enum value. Unset
// fields are not checked. spec: §5.1 line 49.
func (o CapabilityOverride) Validate() error {
	if o.Interaction != nil && !o.Interaction.IsValid() {
		return fmt.Errorf("interaction %q is not a valid §5.1 interaction model", *o.Interaction)
	}
	if o.InjectionModes != nil {
		for _, m := range *o.InjectionModes {
			if !m.IsValid() {
				return fmt.Errorf("injectionModes entry %q is not a valid §5.1 injection mode", m)
			}
		}
	}
	return nil
}

// Clone returns a deep copy so a caller never shares the override's
// pointer or slice state with the store.
func (o CapabilityOverride) Clone() CapabilityOverride {
	cp := CapabilityOverride{}
	if o.Interaction != nil {
		v := *o.Interaction
		cp.Interaction = &v
	}
	if o.InjectionSupported != nil {
		v := *o.InjectionSupported
		cp.InjectionSupported = &v
	}
	if o.InjectionModes != nil {
		modes := append([]InjectionMode(nil), (*o.InjectionModes)...)
		cp.InjectionModes = &modes
	}
	if o.PreConnect != nil {
		v := *o.PreConnect
		cp.PreConnect = &v
	}
	if o.SDKWarmBlockingPaths != nil {
		paths := append([]string(nil), (*o.SDKWarmBlockingPaths)...)
		cp.SDKWarmBlockingPaths = &paths
	}
	return cp
}

// ApplyCapabilityOverride returns a copy of rt with o overlaid onto its
// §5.1 capabilities block and sdkWarmBlockingPaths. Unset override fields
// preserve rt's declared values; a set field replaces the platform
// default for the tenant. The receiver runtime is not mutated: the
// capabilities block is cloned before any field is written, and the
// blocking-paths slice is replaced by value, so the caller's runtime and
// any other tenant's overlay are unaffected.
//
// When the runtime declares no capabilities block but the override sets a
// capability field, a fresh block is created so the override takes
// effect. spec: §5.1 line 49 — F-5.1.20.
func ApplyCapabilityOverride(rt Runtime, o CapabilityOverride) Runtime {
	if o.IsZero() {
		return rt
	}
	needCaps := o.Interaction != nil || o.InjectionSupported != nil ||
		o.InjectionModes != nil || o.PreConnect != nil
	if needCaps {
		caps := rt.Capabilities.Clone()
		if caps == nil {
			caps = &RuntimeCapabilities{}
		}
		if o.Interaction != nil {
			caps.Interaction = *o.Interaction
		}
		if o.InjectionSupported != nil {
			caps.Injection.Supported = *o.InjectionSupported
		}
		if o.InjectionModes != nil {
			caps.Injection.Modes = append([]InjectionMode(nil), (*o.InjectionModes)...)
		}
		if o.PreConnect != nil {
			caps.PreConnect = *o.PreConnect
		}
		rt.Capabilities = caps
	}
	if o.SDKWarmBlockingPaths != nil {
		rt.SDKWarmBlockingPaths = append([]string(nil), (*o.SDKWarmBlockingPaths)...)
	}
	return rt
}
