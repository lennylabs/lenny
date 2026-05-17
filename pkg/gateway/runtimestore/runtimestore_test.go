// SPDX-License-Identifier: MIT

package runtimestore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/capabilityinference"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// spec: §5.1 Runtime CRD registry.

func TestCreateAndGet(t *testing.T) {
	s := runtimestore.NewMemory()
	in := runtimestore.Runtime{
		Name:             "claude-code",
		Type:             runtimestore.TypeAgent,
		Image:            "ghcr.io/anthropic/claude-code@sha256:abc",
		ExecutionMode:    runtimestore.ExecutionModeSession,
		IsolationProfile: isolation.ProfileSandboxed,
		IntegrationLevel: runtimestore.IntegrationLevelFull,
	}
	if err := s.Create(context.Background(), in); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Get(context.Background(), "claude-code")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != in.Name || got.IsolationProfile != in.IsolationProfile {
		t.Errorf("Get: got %+v", got)
	}
}

func TestRuntimeLabelsRoundTripAndIsolation(t *testing.T) {
	// §5.1: runtimes carry a label set. The store must deep-copy it so
	// it never shares the map with a caller.
	s := runtimestore.NewMemory()
	labels := map[string]string{"team": "security", "approved": "true"}
	if err := s.Create(context.Background(), runtimestore.Runtime{
		Name: "scanner", Type: runtimestore.TypeAgent, Labels: labels,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	labels["team"] = "tampered" // mutate the caller's map after Create

	got, err := s.Get(context.Background(), "scanner")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Labels["team"] != "security" || got.Labels["approved"] != "true" {
		t.Errorf("labels not stored or not isolated from caller mutation: %+v", got.Labels)
	}
	got.Labels["team"] = "tampered-again" // mutate the returned map
	again, _ := s.Get(context.Background(), "scanner")
	if again.Labels["team"] != "security" {
		t.Errorf("the returned map aliases the stored map: %+v", again.Labels)
	}
}

func TestRuntimeLabelsUpdateReplaces(t *testing.T) {
	s := runtimestore.NewMemory()
	_ = s.Create(context.Background(), runtimestore.Runtime{
		Name: "scanner", Labels: map[string]string{"env": "staging"},
	})
	updated, err := s.Update(context.Background(), "scanner", func(rt *runtimestore.Runtime) error {
		rt.Labels = map[string]string{"env": "prod", "tier": "gold"}
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Labels["env"] != "prod" || updated.Labels["tier"] != "gold" {
		t.Errorf("Update did not replace labels: %+v", updated.Labels)
	}
}

func TestRuntimeAgentInterfaceRoundTripAndIsolation(t *testing.T) {
	// §5.1: a type:agent runtime carries an optional agentInterface
	// descriptor. The store must deep-copy its slices so it never shares
	// mutable state with a caller.
	s := runtimestore.NewMemory()
	iface := &runtimestore.AgentInterface{
		Description:            "Analyzes codebases and produces refactoring plans",
		InputModes:             []runtimestore.AgentInterfaceMode{{Type: "text/plain"}},
		OutputModes:            []runtimestore.AgentInterfaceMode{{Type: "text/plain", Role: "primary"}},
		SupportsWorkspaceFiles: true,
		Skills:                 []runtimestore.AgentInterfaceSkill{{ID: "review", Name: "Code Review"}},
		Examples:               []runtimestore.AgentInterfaceExample{{Description: "Review auth", Input: "Review the auth module"}},
	}
	if err := s.Create(context.Background(), runtimestore.Runtime{
		Name: "refactorer", Type: runtimestore.TypeAgent, AgentInterface: iface,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	iface.Skills[0].Name = "tampered" // mutate the caller's slice after Create

	got, err := s.Get(context.Background(), "refactorer")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AgentInterface == nil {
		t.Fatalf("agentInterface not stored")
	}
	if got.AgentInterface.Description != iface.Description || !got.AgentInterface.SupportsWorkspaceFiles {
		t.Errorf("agentInterface scalar fields lost: %+v", got.AgentInterface)
	}
	if len(got.AgentInterface.Skills) != 1 || got.AgentInterface.Skills[0].Name != "Code Review" {
		t.Errorf("agentInterface skills not isolated from caller mutation: %+v", got.AgentInterface.Skills)
	}
	got.AgentInterface.Skills[0].Name = "tampered-again" // mutate the returned slice
	again, _ := s.Get(context.Background(), "refactorer")
	if again.AgentInterface.Skills[0].Name != "Code Review" {
		t.Errorf("the returned slice aliases the stored slice: %+v", again.AgentInterface.Skills)
	}
}

func TestRuntimeAgentInterfaceNilByDefault(t *testing.T) {
	// A runtime registered without an agentInterface block reports nil,
	// matching the type:mcp and type:agent-without-block cases in §5.1.
	s := runtimestore.NewMemory()
	if err := s.Create(context.Background(), runtimestore.Runtime{
		Name: "plain", Type: runtimestore.TypeAgent,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, _ := s.Get(context.Background(), "plain")
	if got.AgentInterface != nil {
		t.Errorf("agentInterface must be nil when omitted: %+v", got.AgentInterface)
	}
}

func TestRuntimePublishedMetadataRoundTripAndIsolation(t *testing.T) {
	// §5.1: a runtime carries a publishedMetadata list. The store must
	// deep-copy the slice so it never shares it with a caller.
	s := runtimestore.NewMemory()
	entries := []runtimestore.PublishedMetadataEntry{
		{Key: "agent-card", ContentType: "application/json", Visibility: runtimestore.VisibilityPublic, Content: `{"name":"x"}`},
		{Key: "cost-manifest", ContentType: "application/json", Visibility: runtimestore.VisibilityTenant},
	}
	if err := s.Create(context.Background(), runtimestore.Runtime{
		Name: "carded", Type: runtimestore.TypeAgent, PublishedMetadata: entries,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	entries[0].Content = "tampered" // mutate the caller's slice after Create

	got, err := s.Get(context.Background(), "carded")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.PublishedMetadata) != 2 || got.PublishedMetadata[0].Content != `{"name":"x"}` {
		t.Errorf("publishedMetadata not stored or not isolated from caller mutation: %+v", got.PublishedMetadata)
	}
	got.PublishedMetadata[0].Content = "tampered-again" // mutate the returned slice
	again, _ := s.Get(context.Background(), "carded")
	if again.PublishedMetadata[0].Content != `{"name":"x"}` {
		t.Errorf("the returned slice aliases the stored slice: %+v", again.PublishedMetadata)
	}
}

func TestValidatePublishedMetadata(t *testing.T) {
	ok := []runtimestore.PublishedMetadataEntry{
		{Key: "agent-card", Visibility: runtimestore.VisibilityPublic},
		{Key: "spec", Visibility: runtimestore.VisibilityInternal},
	}
	if err := runtimestore.ValidatePublishedMetadata(ok); err != nil {
		t.Errorf("valid list rejected: %v", err)
	}
	cases := map[string][]runtimestore.PublishedMetadataEntry{
		"empty key": {{Key: "", Visibility: runtimestore.VisibilityPublic}},
		"duplicate key": {
			{Key: "dup", Visibility: runtimestore.VisibilityPublic},
			{Key: "dup", Visibility: runtimestore.VisibilityTenant},
		},
		"invalid visibility": {{Key: "k", Visibility: "world"}},
	}
	for name, entries := range cases {
		if err := runtimestore.ValidatePublishedMetadata(entries); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}

func TestMetadataVisibilityIsValid(t *testing.T) {
	for _, v := range runtimestore.AllMetadataVisibilities() {
		if !v.IsValid() {
			t.Errorf("%q from the closed enum reports invalid", v)
		}
	}
	if runtimestore.MetadataVisibility("world").IsValid() {
		t.Error("an unknown visibility class must report invalid")
	}
}

func TestRuntimeCapabilityInferenceModeDefault(t *testing.T) {
	// §5.1: capabilityInferenceMode defaults to strict.
	rt := runtimestore.Runtime{Name: "rt", Type: runtimestore.TypeAgent}
	runtimestore.ApplyDefaults(&rt)
	if rt.CapabilityInferenceMode != capabilityinference.ModeStrict {
		t.Errorf("default capabilityInferenceMode = %q, want strict", rt.CapabilityInferenceMode)
	}
	// An explicit mode survives ApplyDefaults.
	rt2 := runtimestore.Runtime{Name: "rt2", CapabilityInferenceMode: capabilityinference.ModePermissive}
	runtimestore.ApplyDefaults(&rt2)
	if rt2.CapabilityInferenceMode != capabilityinference.ModePermissive {
		t.Errorf("explicit capabilityInferenceMode overwritten: %q", rt2.CapabilityInferenceMode)
	}
}

func TestRuntimeToolCapabilityOverridesRoundTripAndIsolation(t *testing.T) {
	// §5.1: a runtime carries a toolCapabilityOverrides map. The store
	// must deep-copy it so it never shares mutable state with a caller.
	s := runtimestore.NewMemory()
	overrides := map[string][]capabilityinference.Capability{
		"deploy": {capabilityinference.CapExecute, capabilityinference.CapAdmin},
	}
	if err := s.Create(context.Background(), runtimestore.Runtime{
		Name: "rt", Type: runtimestore.TypeAgent, ToolCapabilityOverrides: overrides,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	overrides["deploy"][0] = capabilityinference.CapRead // mutate caller's slice

	got, err := s.Get(context.Background(), "rt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.ToolCapabilityOverrides["deploy"]) != 2 ||
		got.ToolCapabilityOverrides["deploy"][0] != capabilityinference.CapExecute {
		t.Errorf("toolCapabilityOverrides not stored or not isolated: %+v", got.ToolCapabilityOverrides)
	}
	got.ToolCapabilityOverrides["deploy"][1] = capabilityinference.CapRead // mutate returned slice
	again, _ := s.Get(context.Background(), "rt")
	if again.ToolCapabilityOverrides["deploy"][1] != capabilityinference.CapAdmin {
		t.Errorf("the returned map aliases the stored map: %+v", again.ToolCapabilityOverrides)
	}
}

func TestRuntimeCapabilityInferenceModeRoundTrip(t *testing.T) {
	s := runtimestore.NewMemory()
	_ = s.Create(context.Background(), runtimestore.Runtime{
		Name: "rt", Type: runtimestore.TypeAgent,
		CapabilityInferenceMode: capabilityinference.ModePermissive,
	})
	got, err := s.Get(context.Background(), "rt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CapabilityInferenceMode != capabilityinference.ModePermissive {
		t.Errorf("capabilityInferenceMode not stored: %q", got.CapabilityInferenceMode)
	}
}

func TestRuntimeSetupPolicyRoundTripAndIsolation(t *testing.T) {
	// §5.1: a runtime carries an optional setupPolicy. The store must
	// copy the pointed-to struct so it never shares state with a caller.
	s := runtimestore.NewMemory()
	policy := &runtimestore.SetupPolicy{TimeoutSeconds: 300, OnTimeout: runtimestore.SetupTimeoutFail}
	if err := s.Create(context.Background(), runtimestore.Runtime{
		Name: "rt", Type: runtimestore.TypeAgent, SetupPolicy: policy,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	policy.TimeoutSeconds = 9999 // mutate the caller's struct after Create

	got, err := s.Get(context.Background(), "rt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SetupPolicy == nil || got.SetupPolicy.TimeoutSeconds != 300 ||
		got.SetupPolicy.OnTimeout != runtimestore.SetupTimeoutFail {
		t.Errorf("setupPolicy not stored or not isolated: %+v", got.SetupPolicy)
	}
	got.SetupPolicy.TimeoutSeconds = 1 // mutate the returned struct
	again, _ := s.Get(context.Background(), "rt")
	if again.SetupPolicy.TimeoutSeconds != 300 {
		t.Errorf("the returned pointer aliases the stored struct: %+v", again.SetupPolicy)
	}
}

func TestSetupTimeoutDispositionIsValid(t *testing.T) {
	for _, d := range runtimestore.AllSetupTimeoutDispositions() {
		if !d.IsValid() {
			t.Errorf("%q from the closed enum reports invalid", d)
		}
	}
	if runtimestore.SetupTimeoutDisposition("abort").IsValid() {
		t.Error("an unknown disposition must report invalid")
	}
}

func TestRuntimeCapabilitiesRoundTripAndIsolation(t *testing.T) {
	// §5.1: a runtime carries an optional capabilities block. The store
	// must deep-copy the injection modes slice.
	s := runtimestore.NewMemory()
	caps := &runtimestore.RuntimeCapabilities{
		Interaction: runtimestore.InteractionMultiTurn,
		Injection: runtimestore.InjectionCapability{
			Supported: true,
			Modes:     []runtimestore.InjectionMode{runtimestore.InjectionImmediate, runtimestore.InjectionQueued},
		},
	}
	if err := s.Create(context.Background(), runtimestore.Runtime{
		Name: "rt", Type: runtimestore.TypeAgent, Capabilities: caps,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	caps.Injection.Modes[0] = runtimestore.InjectionQueued // mutate caller's slice

	got, err := s.Get(context.Background(), "rt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Capabilities == nil || got.Capabilities.Interaction != runtimestore.InteractionMultiTurn ||
		!got.Capabilities.Injection.Supported || len(got.Capabilities.Injection.Modes) != 2 ||
		got.Capabilities.Injection.Modes[0] != runtimestore.InjectionImmediate {
		t.Errorf("capabilities not stored or not isolated: %+v", got.Capabilities)
	}
}

func TestRuntimeInjectionSupportedDefaultsFalse(t *testing.T) {
	// §5.1: injection support defaults to false — a runtime with no
	// capabilities block does not accept injection.
	if (runtimestore.Runtime{Name: "bare"}).InjectionSupported() {
		t.Error("a runtime with no capabilities block must report InjectionSupported false")
	}
	withCaps := runtimestore.Runtime{
		Name:         "rt",
		Capabilities: &runtimestore.RuntimeCapabilities{Injection: runtimestore.InjectionCapability{Supported: true}},
	}
	if !withCaps.InjectionSupported() {
		t.Error("a runtime declaring injection.supported true must report InjectionSupported true")
	}
}

func TestRuntimeInteractionAndInjectionModeIsValid(t *testing.T) {
	for _, i := range runtimestore.AllRuntimeInteractions() {
		if !i.IsValid() {
			t.Errorf("interaction %q from the closed enum reports invalid", i)
		}
	}
	if runtimestore.RuntimeInteraction("batch").IsValid() {
		t.Error("an unknown interaction must report invalid")
	}
	for _, m := range runtimestore.AllInjectionModes() {
		if !m.IsValid() {
			t.Errorf("injection mode %q from the closed enum reports invalid", m)
		}
	}
	if runtimestore.InjectionMode("deferred").IsValid() {
		t.Error("an unknown injection mode must report invalid")
	}
}

func TestRuntimeMinPlatformVersionRoundTrip(t *testing.T) {
	// §5.1: a runtime carries an optional minPlatformVersion.
	s := runtimestore.NewMemory()
	if err := s.Create(context.Background(), runtimestore.Runtime{
		Name: "rt", Type: runtimestore.TypeAgent, MinPlatformVersion: "1.4.0",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Get(context.Background(), "rt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.MinPlatformVersion != "1.4.0" {
		t.Errorf("minPlatformVersion: got %q, want 1.4.0", got.MinPlatformVersion)
	}
}

func TestRuntimeTaskPolicyRoundTripAndIsolation(t *testing.T) {
	// §5.1: a runtime carries an optional taskPolicy. The store must
	// deep-copy the cleanup-command slice and the retry pointer.
	s := runtimestore.NewMemory()
	retries := 3
	policy := &runtimestore.TaskPolicy{
		AcknowledgeBestEffortScrub: true,
		MicrovmScrubMode:           runtimestore.MicrovmScrubRestart,
		CleanupCommands:            []string{"rm -rf /tmp/x"},
		CleanupTimeoutSeconds:      30,
		OnCleanupFailure:           runtimestore.CleanupFailureWarn,
		MaxScrubFailures:           3,
		MaxTasksPerPod:             50,
		MaxTaskRetries:             &retries,
	}
	if err := s.Create(context.Background(), runtimestore.Runtime{
		Name: "rt", Type: runtimestore.TypeAgent, TaskPolicy: policy,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	policy.CleanupCommands[0] = "tampered" // mutate caller's slice
	*policy.MaxTaskRetries = 99            // mutate caller's pointee

	got, err := s.Get(context.Background(), "rt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.TaskPolicy == nil || !got.TaskPolicy.AcknowledgeBestEffortScrub ||
		got.TaskPolicy.MaxTasksPerPod != 50 || got.TaskPolicy.CleanupCommands[0] != "rm -rf /tmp/x" {
		t.Errorf("taskPolicy not stored or not isolated: %+v", got.TaskPolicy)
	}
	if got.TaskPolicy.MaxTaskRetries == nil || *got.TaskPolicy.MaxTaskRetries != 3 {
		t.Errorf("maxTaskRetries not isolated from caller mutation: %+v", got.TaskPolicy.MaxTaskRetries)
	}
}

func TestMicrovmScrubModeAndCleanupDispositionIsValid(t *testing.T) {
	for _, m := range runtimestore.AllMicrovmScrubModes() {
		if !m.IsValid() {
			t.Errorf("scrub mode %q from the closed enum reports invalid", m)
		}
	}
	if runtimestore.MicrovmScrubMode("wipe").IsValid() {
		t.Error("an unknown scrub mode must report invalid")
	}
	for _, d := range runtimestore.AllCleanupFailureDispositions() {
		if !d.IsValid() {
			t.Errorf("cleanup disposition %q from the closed enum reports invalid", d)
		}
	}
	if runtimestore.CleanupFailureDisposition("retry").IsValid() {
		t.Error("an unknown cleanup disposition must report invalid")
	}
}

func TestCreateRejectsInvalidName(t *testing.T) {
	s := runtimestore.NewMemory()
	for _, name := range []string{
		"", "With-Caps", "-leading-dash", "with space", "with/slash",
	} {
		if err := s.Create(context.Background(), runtimestore.Runtime{Name: name}); err == nil {
			t.Errorf("Create(%q) should fail", name)
		}
	}
}

func TestCreateAcceptsValidNames(t *testing.T) {
	s := runtimestore.NewMemory()
	for i, name := range []string{
		"a", "claude-code", "gemini_cli", "echo", "type_mcp",
	} {
		r := runtimestore.Runtime{Name: name, Type: runtimestore.TypeAgent}
		if err := s.Create(context.Background(), r); err != nil {
			t.Errorf("Create #%d (%q): %v", i, name, err)
		}
	}
}

func TestCreateRejectsDuplicate(t *testing.T) {
	s := runtimestore.NewMemory()
	_ = s.Create(context.Background(), runtimestore.Runtime{Name: "echo"})
	if err := s.Create(context.Background(), runtimestore.Runtime{Name: "echo"}); !errors.Is(err, runtimestore.ErrAlreadyExists) {
		t.Errorf("dupe: got %v", err)
	}
}

func TestGetMissing(t *testing.T) {
	s := runtimestore.NewMemory()
	if _, err := s.Get(context.Background(), "missing"); !errors.Is(err, runtimestore.ErrNotFound) {
		t.Errorf("Get missing: got %v", err)
	}
}

func TestUpdateAdvancesTimestamp(t *testing.T) {
	s := runtimestore.NewMemory()
	_ = s.Create(context.Background(), runtimestore.Runtime{Name: "echo"})
	row, _ := s.Get(context.Background(), "echo")
	prev := row.UpdatedAt
	updated, err := s.Update(context.Background(), "echo", func(r *runtimestore.Runtime) error {
		r.Description = "echo reference runtime"
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Description != "echo reference runtime" {
		t.Errorf("Description not updated: %+v", updated)
	}
	if !updated.UpdatedAt.After(prev) {
		t.Errorf("UpdatedAt did not advance: prev=%v, got=%v", prev, updated.UpdatedAt)
	}
}

func TestListSortsByName(t *testing.T) {
	s := runtimestore.NewMemory()
	for _, name := range []string{"b", "a", "c"} {
		_ = s.Create(context.Background(), runtimestore.Runtime{Name: name})
	}
	rows, _ := s.List(context.Background(), runtimestore.ListFilter{})
	if len(rows) != 3 || rows[0].Name != "a" || rows[2].Name != "c" {
		t.Errorf("List order: got %+v", rows)
	}
}

func TestListFilterByType(t *testing.T) {
	s := runtimestore.NewMemory()
	_ = s.Create(context.Background(), runtimestore.Runtime{Name: "agent1", Type: runtimestore.TypeAgent})
	_ = s.Create(context.Background(), runtimestore.Runtime{Name: "mcp1", Type: runtimestore.TypeMCP})

	rows, _ := s.List(context.Background(), runtimestore.ListFilter{Type: runtimestore.TypeMCP})
	if len(rows) != 1 || rows[0].Name != "mcp1" {
		t.Errorf("List by type: got %+v", rows)
	}
}

func TestSoftDeleteExcludesByDefault(t *testing.T) {
	s := runtimestore.NewMemory()
	_ = s.Create(context.Background(), runtimestore.Runtime{Name: "echo"})
	_ = s.SoftDelete(context.Background(), "echo", time.Now())

	rows, _ := s.List(context.Background(), runtimestore.ListFilter{})
	if len(rows) != 0 {
		t.Errorf("default List should exclude deleted: got %+v", rows)
	}
	all, _ := s.List(context.Background(), runtimestore.ListFilter{IncludeDeleted: true})
	if len(all) != 1 {
		t.Errorf("IncludeDeleted list: got %d rows", len(all))
	}
}

func TestSoftDeleteIdempotent(t *testing.T) {
	s := runtimestore.NewMemory()
	_ = s.Create(context.Background(), runtimestore.Runtime{Name: "echo"})
	first := time.Now()
	if err := s.SoftDelete(context.Background(), "echo", first); err != nil {
		t.Fatalf("SoftDelete 1: %v", err)
	}
	if err := s.SoftDelete(context.Background(), "echo", first.Add(time.Hour)); err != nil {
		t.Errorf("SoftDelete 2: %v", err)
	}
	row, _ := s.Get(context.Background(), "echo")
	if !row.DeletedAt.Equal(first) {
		t.Errorf("DeletedAt overwritten: got %v, want %v", row.DeletedAt, first)
	}
}

func TestEnumsAreClosed(t *testing.T) {
	if !runtimestore.TypeAgent.IsValid() || !runtimestore.TypeMCP.IsValid() {
		t.Error("known types should be valid")
	}
	if runtimestore.RuntimeType("foo").IsValid() {
		t.Error("unknown type should be invalid")
	}
	if !runtimestore.ExecutionModeSession.IsValid() || !runtimestore.ExecutionModeTask.IsValid() || !runtimestore.ExecutionModeConcurrent.IsValid() {
		t.Error("known execution modes should be valid")
	}
	if runtimestore.ExecutionMode("foo").IsValid() {
		t.Error("unknown mode should be invalid")
	}
	if !runtimestore.IntegrationLevelBasic.IsValid() || !runtimestore.IntegrationLevelStandard.IsValid() || !runtimestore.IntegrationLevelFull.IsValid() {
		t.Error("known integration levels should be valid")
	}
	if runtimestore.IntegrationLevel("foo").IsValid() {
		t.Error("unknown integration level should be invalid")
	}
}

func TestValidateName(t *testing.T) {
	for _, name := range []string{"a", "echo", "claude-code", "gemini_cli"} {
		if err := runtimestore.ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q): %v", name, err)
		}
	}
	for _, name := range []string{"", "With-Caps", "-leading"} {
		if err := runtimestore.ValidateName(name); err == nil {
			t.Errorf("ValidateName(%q) should fail", name)
		}
	}
}
