// SPDX-License-Identifier: MIT

package runtimestore_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/capabilityinference"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

func TestMergeInheritsSecurityFieldsFromBase(t *testing.T) {
	// §5.1: image, type, executionMode, isolationProfile,
	// integrationLevel, and capabilities are always taken from the base.
	base := runtimestore.Runtime{
		Name: "base", Type: runtimestore.TypeAgent,
		Image:            "lenny/base@sha256:abc",
		ExecutionMode:    runtimestore.ExecutionModeSession,
		IsolationProfile: isolation.ProfileMicrovm,
		IntegrationLevel: runtimestore.IntegrationLevelFull,
		Capabilities: &runtimestore.RuntimeCapabilities{
			Interaction: runtimestore.InteractionMultiTurn,
			Injection:   runtimestore.InjectionCapability{Supported: true},
		},
	}
	derived := runtimestore.Runtime{
		Name: "derived", BaseRuntime: "base",
		// A derived runtime that tries to set inherited fields: the
		// merge discards them in favour of the base values.
		Image:            "lenny/EVIL@sha256:def",
		IsolationProfile: isolation.ProfileStandard,
	}
	eff := runtimestore.Merge(base, derived)

	if eff.Name != "derived" {
		t.Errorf("effective name: %q, want derived", eff.Name)
	}
	if eff.Image != "lenny/base@sha256:abc" {
		t.Errorf("image must be inherited from base: %q", eff.Image)
	}
	if eff.IsolationProfile != isolation.ProfileMicrovm {
		t.Errorf("isolationProfile must be inherited from base: %q", eff.IsolationProfile)
	}
	if eff.IntegrationLevel != runtimestore.IntegrationLevelFull {
		t.Errorf("integrationLevel must be inherited: %q", eff.IntegrationLevel)
	}
	if eff.Capabilities == nil || eff.Capabilities.Interaction != runtimestore.InteractionMultiTurn {
		t.Errorf("capabilities must be inherited: %+v", eff.Capabilities)
	}
	if eff.BaseRuntime != "" {
		t.Errorf("the effective runtime must not itself be derived: BaseRuntime=%q", eff.BaseRuntime)
	}
}

func TestMergeOverrideFields(t *testing.T) {
	base := runtimestore.Runtime{
		Name: "base", Type: runtimestore.TypeAgent,
		Description:             "base description",
		DelegationPolicyRef:     "base-policy",
		CapabilityInferenceMode: capabilityinference.ModeStrict,
		MinPlatformVersion:      "1.0.0",
		AgentInterface:          &runtimestore.AgentInterface{Description: "base iface"},
	}
	// derived overrides description and capabilityInferenceMode, leaves
	// delegationPolicyRef / minPlatformVersion / agentInterface unset.
	derived := runtimestore.Runtime{
		Name: "derived", BaseRuntime: "base",
		Description:             "derived description",
		CapabilityInferenceMode: capabilityinference.ModePermissive,
	}
	eff := runtimestore.Merge(base, derived)

	if eff.Description != "derived description" {
		t.Errorf("description override: %q", eff.Description)
	}
	if eff.CapabilityInferenceMode != capabilityinference.ModePermissive {
		t.Errorf("capabilityInferenceMode override: %q", eff.CapabilityInferenceMode)
	}
	if eff.DelegationPolicyRef != "base-policy" {
		t.Errorf("delegationPolicyRef must fall back to base: %q", eff.DelegationPolicyRef)
	}
	if eff.MinPlatformVersion != "1.0.0" {
		t.Errorf("minPlatformVersion must fall back to base: %q", eff.MinPlatformVersion)
	}
	if eff.AgentInterface == nil || eff.AgentInterface.Description != "base iface" {
		t.Errorf("agentInterface must fall back to base: %+v", eff.AgentInterface)
	}
}

func TestMergeLabels(t *testing.T) {
	// §5.1: labels overlay — derived keys win on collision.
	base := runtimestore.Runtime{
		Name: "base", Labels: map[string]string{"team": "platform", "tier": "gold"},
	}
	derived := runtimestore.Runtime{
		Name: "derived", BaseRuntime: "base",
		Labels: map[string]string{"team": "research", "env": "prod"},
	}
	eff := runtimestore.Merge(base, derived)
	if eff.Labels["team"] != "research" || eff.Labels["tier"] != "gold" || eff.Labels["env"] != "prod" {
		t.Errorf("merged labels: %+v", eff.Labels)
	}
}

func TestMergePublishedMetadataAppendsAndReplaces(t *testing.T) {
	// §5.1: publishedMetadata append — a derived entry replaces a base
	// entry with the same key; a derived-only key is appended.
	base := runtimestore.Runtime{
		Name: "base",
		PublishedMetadata: []runtimestore.PublishedMetadataEntry{
			{Key: "agent-card", Content: "base-card", Visibility: runtimestore.VisibilityPublic},
			{Key: "spec", Content: "base-spec", Visibility: runtimestore.VisibilityInternal},
		},
	}
	derived := runtimestore.Runtime{
		Name: "derived", BaseRuntime: "base",
		PublishedMetadata: []runtimestore.PublishedMetadataEntry{
			{Key: "agent-card", Content: "derived-card", Visibility: runtimestore.VisibilityPublic},
			{Key: "cost", Content: "derived-cost", Visibility: runtimestore.VisibilityTenant},
		},
	}
	eff := runtimestore.Merge(base, derived)
	if len(eff.PublishedMetadata) != 3 {
		t.Fatalf("merged publishedMetadata: %d entries, want 3 (%+v)", len(eff.PublishedMetadata), eff.PublishedMetadata)
	}
	byKey := map[string]string{}
	for _, e := range eff.PublishedMetadata {
		byKey[e.Key] = e.Content
	}
	if byKey["agent-card"] != "derived-card" {
		t.Errorf("derived entry must replace the base agent-card: %q", byKey["agent-card"])
	}
	if byKey["spec"] != "base-spec" || byKey["cost"] != "derived-cost" {
		t.Errorf("merged publishedMetadata content: %+v", byKey)
	}
}

func TestMergeSetupPolicyTakesMaxTimeout(t *testing.T) {
	// §5.1: setupPolicy.timeoutSeconds takes max(base, derived);
	// onTimeout takes the derived value when set.
	base := runtimestore.Runtime{
		Name:        "base",
		SetupPolicy: &runtimestore.SetupPolicy{TimeoutSeconds: 300, OnTimeout: runtimestore.SetupTimeoutFail},
	}
	derived := runtimestore.Runtime{
		Name: "derived", BaseRuntime: "base",
		SetupPolicy: &runtimestore.SetupPolicy{TimeoutSeconds: 120, OnTimeout: runtimestore.SetupTimeoutWarn},
	}
	eff := runtimestore.Merge(base, derived)
	if eff.SetupPolicy == nil || eff.SetupPolicy.TimeoutSeconds != 300 {
		t.Errorf("setupPolicy.timeoutSeconds must be max(300,120)=300: %+v", eff.SetupPolicy)
	}
	if eff.SetupPolicy.OnTimeout != runtimestore.SetupTimeoutWarn {
		t.Errorf("setupPolicy.onTimeout must be the derived value: %q", eff.SetupPolicy.OnTimeout)
	}
}

func TestMergeDoesNotAliasInputs(t *testing.T) {
	// Merge must return a runtime that shares no mutable state with the
	// base or derived inputs.
	base := runtimestore.Runtime{
		Name: "base", Labels: map[string]string{"team": "platform"},
		PublishedMetadata: []runtimestore.PublishedMetadataEntry{{Key: "k", Content: "base"}},
	}
	derived := runtimestore.Runtime{Name: "derived", BaseRuntime: "base"}
	eff := runtimestore.Merge(base, derived)

	eff.Labels["team"] = "tampered"
	eff.PublishedMetadata[0].Content = "tampered"
	if base.Labels["team"] != "platform" {
		t.Error("Merge result aliases the base label map")
	}
	if base.PublishedMetadata[0].Content != "base" {
		t.Error("Merge result aliases the base publishedMetadata slice")
	}
}
