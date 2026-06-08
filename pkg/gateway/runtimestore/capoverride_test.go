// SPDX-License-Identifier: MIT

package runtimestore_test

import (
	"reflect"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

func ptr[T any](v T) *T { return &v }

// spec: §5.1 line 49 — an unset override field inherits the runtime's
// declared capability; a set field replaces it for the tenant.
func TestApplyCapabilityOverride_spec_5_1_49(t *testing.T) {
	base := runtimestore.Runtime{
		Name: "claude-code",
		Capabilities: &runtimestore.RuntimeCapabilities{
			Interaction: runtimestore.InteractionMultiTurn,
			Injection: runtimestore.InjectionCapability{
				Supported: true,
				Modes:     []runtimestore.InjectionMode{runtimestore.InjectionImmediate, runtimestore.InjectionQueued},
			},
			PreConnect: true,
		},
		SDKWarmBlockingPaths: []string{"CLAUDE.md", ".claude/*"},
	}

	t.Run("zero override is a no-op", func(t *testing.T) {
		got := runtimestore.ApplyCapabilityOverride(base, runtimestore.CapabilityOverride{})
		if !reflect.DeepEqual(got.Capabilities, base.Capabilities) {
			t.Errorf("capabilities changed by zero override: %+v", got.Capabilities)
		}
	})

	t.Run("tenant opts out of multi_turn", func(t *testing.T) {
		got := runtimestore.ApplyCapabilityOverride(base, runtimestore.CapabilityOverride{
			Interaction: ptr(runtimestore.InteractionOneShot),
		})
		if got.Capabilities.Interaction != runtimestore.InteractionOneShot {
			t.Errorf("Interaction: got %q", got.Capabilities.Interaction)
		}
		// Untouched axes inherit.
		if !got.Capabilities.Injection.Supported || !got.Capabilities.PreConnect {
			t.Errorf("untouched axes not inherited: %+v", got.Capabilities)
		}
		// Base is unchanged.
		if base.Capabilities.Interaction != runtimestore.InteractionMultiTurn {
			t.Errorf("base mutated: %q", base.Capabilities.Interaction)
		}
	})

	t.Run("tenant restricts injection.modes to a single mode", func(t *testing.T) {
		got := runtimestore.ApplyCapabilityOverride(base, runtimestore.CapabilityOverride{
			InjectionModes: ptr([]runtimestore.InjectionMode{runtimestore.InjectionQueued}),
		})
		want := []runtimestore.InjectionMode{runtimestore.InjectionQueued}
		if !reflect.DeepEqual(got.Capabilities.Injection.Modes, want) {
			t.Errorf("Modes: got %v want %v", got.Capabilities.Injection.Modes, want)
		}
		// Base slice is untouched.
		if len(base.Capabilities.Injection.Modes) != 2 {
			t.Errorf("base modes mutated: %v", base.Capabilities.Injection.Modes)
		}
	})

	t.Run("tenant disables injection support", func(t *testing.T) {
		got := runtimestore.ApplyCapabilityOverride(base, runtimestore.CapabilityOverride{
			InjectionSupported: ptr(false),
		})
		if got.InjectionSupported() {
			t.Errorf("injection still supported after override")
		}
	})

	t.Run("tenant disables preConnect", func(t *testing.T) {
		got := runtimestore.ApplyCapabilityOverride(base, runtimestore.CapabilityOverride{
			PreConnect: ptr(false),
		})
		if got.Capabilities.PreConnect {
			t.Errorf("preConnect still set after override")
		}
	})

	t.Run("tenant adds an sdkWarmBlockingPath", func(t *testing.T) {
		got := runtimestore.ApplyCapabilityOverride(base, runtimestore.CapabilityOverride{
			SDKWarmBlockingPaths: ptr([]string{"CLAUDE.md", ".claude/*", "tenant-secret.txt"}),
		})
		if len(got.SDKWarmBlockingPaths) != 3 || got.SDKWarmBlockingPaths[2] != "tenant-secret.txt" {
			t.Errorf("SDKWarmBlockingPaths: got %v", got.SDKWarmBlockingPaths)
		}
		// Base slice is untouched.
		if len(base.SDKWarmBlockingPaths) != 2 {
			t.Errorf("base paths mutated: %v", base.SDKWarmBlockingPaths)
		}
	})
}

// spec: §5.1 line 49 — an override that sets a capability on a runtime
// that declares no capabilities block creates a fresh block.
func TestApplyCapabilityOverride_NilCapabilities_spec_5_1_49(t *testing.T) {
	rt := runtimestore.Runtime{Name: "mcp-only"}
	got := runtimestore.ApplyCapabilityOverride(rt, runtimestore.CapabilityOverride{
		InjectionSupported: ptr(true),
	})
	if got.Capabilities == nil || !got.Capabilities.Injection.Supported {
		t.Fatalf("expected a fresh capabilities block with injection enabled, got %+v", got.Capabilities)
	}
	// The original runtime is untouched.
	if rt.Capabilities != nil {
		t.Errorf("source runtime gained a capabilities block")
	}
}

func TestCapabilityOverride_Validate_spec_5_1_49(t *testing.T) {
	bad := runtimestore.InteractionOneShot
	if err := (runtimestore.CapabilityOverride{Interaction: &bad}).Validate(); err != nil {
		t.Errorf("valid interaction rejected: %v", err)
	}
	invalid := runtimestore.RuntimeInteraction("streaming")
	if err := (runtimestore.CapabilityOverride{Interaction: &invalid}).Validate(); err == nil {
		t.Error("expected invalid interaction to be rejected")
	}
	if err := (runtimestore.CapabilityOverride{
		InjectionModes: ptr([]runtimestore.InjectionMode{"telepathy"}),
	}).Validate(); err == nil {
		t.Error("expected invalid injection mode to be rejected")
	}
}

func TestCapabilityOverride_CloneIsolation(t *testing.T) {
	o := runtimestore.CapabilityOverride{
		InjectionModes:       ptr([]runtimestore.InjectionMode{runtimestore.InjectionImmediate}),
		SDKWarmBlockingPaths: ptr([]string{"a"}),
	}
	cp := o.Clone()
	(*o.InjectionModes)[0] = runtimestore.InjectionQueued
	(*o.SDKWarmBlockingPaths)[0] = "b"
	if (*cp.InjectionModes)[0] != runtimestore.InjectionImmediate {
		t.Errorf("clone shares injection modes slice")
	}
	if (*cp.SDKWarmBlockingPaths)[0] != "a" {
		t.Errorf("clone shares blocking paths slice")
	}
}

func TestCapabilityOverride_IsZero(t *testing.T) {
	if !(runtimestore.CapabilityOverride{}).IsZero() {
		t.Error("empty override should be zero")
	}
	if (runtimestore.CapabilityOverride{PreConnect: ptr(false)}).IsZero() {
		t.Error("override with a set field is not zero")
	}
}
