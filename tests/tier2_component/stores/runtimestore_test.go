//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §5.1 runtime registry, exercising the
// Postgres-backed pkg/gateway/runtimestore/pgstore against a real
// container. Covers CRUD, name validation, the soft-delete lifecycle,
// and the IncludeDeleted / Type list filters.
package stores_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/capabilityinference"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	runtimepg "github.com/lennylabs/lenny/pkg/gateway/runtimestore/pgstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

func runtimeName(t *testing.T) string {
	t.Helper()
	return "rt-" + newUUID(t)[:8]
}

func sampleRuntime(name string) runtimestore.Runtime {
	return runtimestore.Runtime{
		Name:             name,
		Type:             runtimestore.TypeAgent,
		Image:            "ghcr.io/acme/echo@sha256:" + strings.Repeat("a", 64),
		ExecutionMode:    runtimestore.ExecutionModeSession,
		IsolationProfile: isolation.ProfileStandard,
		IntegrationLevel: runtimestore.IntegrationLevelBasic,
		Description:      "echo reference runtime",
	}
}

// spec: 5.1
// diagnosis: the Postgres-backed runtime registry in
// pkg/gateway/runtimestore/pgstore did not behave as specified. Create
// and Get must round-trip a runtime including its jsonb-encoded
// descriptor blocks, name validation must reject duplicates and
// invalid names, Update must re-apply and advance updated_at, and the
// soft-delete lifecycle must honor the Type and IncludeDeleted list
// filters.
func TestRuntimeStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := runtimepg.New(pg.Pool)
	ctx := context.Background()

	t.Run("create and get round-trip", func(t *testing.T) {
		want := sampleRuntime(runtimeName(t))
		if err := store.Create(ctx, want); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, want.Name)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Type != want.Type || got.Image != want.Image ||
			got.ExecutionMode != want.ExecutionMode || got.IsolationProfile != want.IsolationProfile ||
			got.IntegrationLevel != want.IntegrationLevel || got.Description != want.Description {
			t.Errorf("field mismatch:\n got %+v\nwant %+v", got, want)
		}
		if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
			t.Error("Create did not default the audit timestamps")
		}
		if !got.IsActive() {
			t.Error("freshly created runtime reports inactive")
		}
	})

	t.Run("agentInterface round-trips through jsonb", func(t *testing.T) {
		r := sampleRuntime(runtimeName(t))
		r.AgentInterface = &runtimestore.AgentInterface{
			Description:            "Analyzes codebases",
			InputModes:             []runtimestore.AgentInterfaceMode{{Type: "text/plain"}},
			OutputModes:            []runtimestore.AgentInterfaceMode{{Type: "text/plain", Role: "primary"}},
			SupportsWorkspaceFiles: true,
			Skills:                 []runtimestore.AgentInterfaceSkill{{ID: "review", Name: "Code Review"}},
			Examples:               []runtimestore.AgentInterfaceExample{{Description: "review", Input: "review it"}},
		}
		if err := store.Create(ctx, r); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, r.Name)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.AgentInterface == nil {
			t.Fatalf("agentInterface lost through the jsonb round-trip")
		}
		if got.AgentInterface.Description != r.AgentInterface.Description ||
			!got.AgentInterface.SupportsWorkspaceFiles ||
			len(got.AgentInterface.Skills) != 1 || got.AgentInterface.Skills[0].ID != "review" ||
			len(got.AgentInterface.InputModes) != 1 || len(got.AgentInterface.Examples) != 1 {
			t.Errorf("agentInterface round-trip mismatch: %+v", got.AgentInterface)
		}

		// A runtime registered without the block reports nil (SQL NULL).
		plain := sampleRuntime(runtimeName(t))
		if err := store.Create(ctx, plain); err != nil {
			t.Fatalf("Create plain: %v", err)
		}
		gotPlain, _ := store.Get(ctx, plain.Name)
		if gotPlain.AgentInterface != nil {
			t.Errorf("agentInterface must be nil when omitted: %+v", gotPlain.AgentInterface)
		}

		// Update can clear the descriptor back to SQL NULL.
		cleared, err := store.Update(ctx, r.Name, func(rt *runtimestore.Runtime) error {
			rt.AgentInterface = nil
			return nil
		})
		if err != nil {
			t.Fatalf("Update clear: %v", err)
		}
		if cleared.AgentInterface != nil {
			t.Errorf("Update did not clear agentInterface: %+v", cleared.AgentInterface)
		}
	})

	t.Run("publishedMetadata round-trips through jsonb", func(t *testing.T) {
		r := sampleRuntime(runtimeName(t))
		r.PublishedMetadata = []runtimestore.PublishedMetadataEntry{
			{Key: "agent-card", ContentType: "application/json", Visibility: runtimestore.VisibilityPublic, Content: `{"name":"x"}`},
			{Key: "cost-manifest", ContentType: "application/json", Visibility: runtimestore.VisibilityTenant},
		}
		if err := store.Create(ctx, r); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, r.Name)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if len(got.PublishedMetadata) != 2 || got.PublishedMetadata[0].Key != "agent-card" ||
			got.PublishedMetadata[0].Content != `{"name":"x"}` ||
			got.PublishedMetadata[1].Visibility != runtimestore.VisibilityTenant {
			t.Errorf("publishedMetadata round-trip mismatch: %+v", got.PublishedMetadata)
		}

		// A runtime registered without entries reports nil (SQL NULL).
		plain := sampleRuntime(runtimeName(t))
		if err := store.Create(ctx, plain); err != nil {
			t.Fatalf("Create plain: %v", err)
		}
		gotPlain, _ := store.Get(ctx, plain.Name)
		if gotPlain.PublishedMetadata != nil {
			t.Errorf("publishedMetadata must be nil when omitted: %+v", gotPlain.PublishedMetadata)
		}

		// Update can clear the list back to SQL NULL.
		cleared, err := store.Update(ctx, r.Name, func(rt *runtimestore.Runtime) error {
			rt.PublishedMetadata = nil
			return nil
		})
		if err != nil {
			t.Fatalf("Update clear: %v", err)
		}
		if cleared.PublishedMetadata != nil {
			t.Errorf("Update did not clear publishedMetadata: %+v", cleared.PublishedMetadata)
		}
	})

	t.Run("capabilityInferenceMode round-trips", func(t *testing.T) {
		r := sampleRuntime(runtimeName(t))
		r.CapabilityInferenceMode = capabilityinference.ModePermissive
		if err := store.Create(ctx, r); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, r.Name)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.CapabilityInferenceMode != capabilityinference.ModePermissive {
			t.Errorf("capabilityInferenceMode: got %q, want permissive", got.CapabilityInferenceMode)
		}
	})

	t.Run("toolCapabilityOverrides round-trips through jsonb", func(t *testing.T) {
		r := sampleRuntime(runtimeName(t))
		r.ToolCapabilityOverrides = map[string][]capabilityinference.Capability{
			"deploy": {capabilityinference.CapExecute, capabilityinference.CapAdmin},
		}
		if err := store.Create(ctx, r); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, r.Name)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if len(got.ToolCapabilityOverrides["deploy"]) != 2 ||
			got.ToolCapabilityOverrides["deploy"][0] != capabilityinference.CapExecute {
			t.Errorf("toolCapabilityOverrides round-trip mismatch: %+v", got.ToolCapabilityOverrides)
		}

		// A runtime registered with no overrides reports nil (SQL NULL).
		plain := sampleRuntime(runtimeName(t))
		if err := store.Create(ctx, plain); err != nil {
			t.Fatalf("Create plain: %v", err)
		}
		gotPlain, _ := store.Get(ctx, plain.Name)
		if gotPlain.ToolCapabilityOverrides != nil {
			t.Errorf("toolCapabilityOverrides must be nil when omitted: %+v", gotPlain.ToolCapabilityOverrides)
		}
	})

	t.Run("setupPolicy round-trips through jsonb", func(t *testing.T) {
		r := sampleRuntime(runtimeName(t))
		r.SetupPolicy = &runtimestore.SetupPolicy{
			TimeoutSeconds: 420, OnTimeout: runtimestore.SetupTimeoutWarn,
		}
		if err := store.Create(ctx, r); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, r.Name)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.SetupPolicy == nil || got.SetupPolicy.TimeoutSeconds != 420 ||
			got.SetupPolicy.OnTimeout != runtimestore.SetupTimeoutWarn {
			t.Errorf("setupPolicy round-trip mismatch: %+v", got.SetupPolicy)
		}

		// A runtime with no setupPolicy reports nil (SQL NULL).
		plain := sampleRuntime(runtimeName(t))
		if err := store.Create(ctx, plain); err != nil {
			t.Fatalf("Create plain: %v", err)
		}
		gotPlain, _ := store.Get(ctx, plain.Name)
		if gotPlain.SetupPolicy != nil {
			t.Errorf("setupPolicy must be nil when omitted: %+v", gotPlain.SetupPolicy)
		}
	})

	t.Run("capabilities round-trips through jsonb", func(t *testing.T) {
		r := sampleRuntime(runtimeName(t))
		r.Capabilities = &runtimestore.RuntimeCapabilities{
			Interaction: runtimestore.InteractionMultiTurn,
			Injection: runtimestore.InjectionCapability{
				Supported: true,
				Modes:     []runtimestore.InjectionMode{runtimestore.InjectionImmediate},
			},
		}
		if err := store.Create(ctx, r); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, r.Name)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Capabilities == nil || got.Capabilities.Interaction != runtimestore.InteractionMultiTurn ||
			!got.Capabilities.Injection.Supported || len(got.Capabilities.Injection.Modes) != 1 {
			t.Errorf("capabilities round-trip mismatch: %+v", got.Capabilities)
		}

		// A runtime with no capabilities block reports nil (SQL NULL).
		plain := sampleRuntime(runtimeName(t))
		if err := store.Create(ctx, plain); err != nil {
			t.Fatalf("Create plain: %v", err)
		}
		gotPlain, _ := store.Get(ctx, plain.Name)
		if gotPlain.Capabilities != nil {
			t.Errorf("capabilities must be nil when omitted: %+v", gotPlain.Capabilities)
		}
	})

	t.Run("minPlatformVersion round-trips", func(t *testing.T) {
		r := sampleRuntime(runtimeName(t))
		r.MinPlatformVersion = "1.4.0"
		if err := store.Create(ctx, r); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, r.Name)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.MinPlatformVersion != "1.4.0" {
			t.Errorf("minPlatformVersion: got %q, want 1.4.0", got.MinPlatformVersion)
		}
	})

	t.Run("sessionPolicy round-trips through jsonb", func(t *testing.T) {
		r := sampleRuntime(runtimeName(t))
		retries := 2
		r.SessionPolicy = &runtimestore.SessionPolicy{
			MaxSessionRetries: &retries,
			Recycle: &runtimestore.RecyclePolicy{
				Enabled:                    true,
				AcknowledgeBestEffortScrub: true,
				ScrubProfile:               runtimestore.MicrovmScrubRestart,
				OnScrubFailure:             runtimestore.CleanupFailureWarn,
				MaxSessionsPerPod:          50,
			},
			CleanupCommands: []string{"rm -rf /tmp/x"},
		}
		if err := store.Create(ctx, r); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, r.Name)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.SessionPolicy == nil || got.SessionPolicy.Recycle == nil ||
			!got.SessionPolicy.Recycle.AcknowledgeBestEffortScrub ||
			got.SessionPolicy.Recycle.MaxSessionsPerPod != 50 ||
			len(got.SessionPolicy.CleanupCommands) != 1 ||
			got.SessionPolicy.MaxSessionRetries == nil || *got.SessionPolicy.MaxSessionRetries != 2 {
			t.Errorf("sessionPolicy round-trip mismatch: %+v", got.SessionPolicy)
		}

		// A runtime with no sessionPolicy reports nil (SQL NULL).
		plain := sampleRuntime(runtimeName(t))
		if err := store.Create(ctx, plain); err != nil {
			t.Fatalf("Create plain: %v", err)
		}
		gotPlain, _ := store.Get(ctx, plain.Name)
		if gotPlain.SessionPolicy != nil {
			t.Errorf("sessionPolicy must be nil when omitted: %+v", gotPlain.SessionPolicy)
		}
	})

	t.Run("baseRuntime round-trips", func(t *testing.T) {
		r := sampleRuntime(runtimeName(t))
		r.BaseRuntime = "langgraph-runtime"
		if err := store.Create(ctx, r); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, r.Name)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.BaseRuntime != "langgraph-runtime" || !got.IsDerived() {
			t.Errorf("baseRuntime: got %q (IsDerived=%v), want langgraph-runtime", got.BaseRuntime, got.IsDerived())
		}
	})

	t.Run("duplicate and invalid names are rejected", func(t *testing.T) {
		r := sampleRuntime(runtimeName(t))
		if err := store.Create(ctx, r); err != nil {
			t.Fatalf("first Create: %v", err)
		}
		if err := store.Create(ctx, r); !errors.Is(err, runtimestore.ErrAlreadyExists) {
			t.Errorf("duplicate Create: got %v, want ErrAlreadyExists", err)
		}
		bad := sampleRuntime("Invalid Name")
		if err := store.Create(ctx, bad); err == nil {
			t.Error("Create with an invalid runtime name should be rejected")
		}
	})

	t.Run("get missing returns ErrNotFound", func(t *testing.T) {
		if _, err := store.Get(ctx, runtimeName(t)); !errors.Is(err, runtimestore.ErrNotFound) {
			t.Errorf("Get missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("update mutates and advances updated_at", func(t *testing.T) {
		r := sampleRuntime(runtimeName(t))
		if err := store.Create(ctx, r); err != nil {
			t.Fatalf("Create: %v", err)
		}
		before, _ := store.Get(ctx, r.Name)
		updated, err := store.Update(ctx, r.Name, func(rt *runtimestore.Runtime) error {
			rt.Image = "ghcr.io/acme/echo@sha256:" + strings.Repeat("b", 64)
			rt.IntegrationLevel = runtimestore.IntegrationLevelFull
			return nil
		})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if updated.IntegrationLevel != runtimestore.IntegrationLevelFull {
			t.Errorf("Update result not applied: %+v", updated)
		}
		if !updated.UpdatedAt.After(before.UpdatedAt) {
			t.Errorf("UpdatedAt did not advance: before=%v after=%v", before.UpdatedAt, updated.UpdatedAt)
		}
		if _, err := store.Update(ctx, runtimeName(t), func(*runtimestore.Runtime) error {
			return nil
		}); !errors.Is(err, runtimestore.ErrNotFound) {
			t.Errorf("Update missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("list applies type, delete, and ordering", func(t *testing.T) {
		marker := newUUID(t)[:8]
		names := []string{marker + "-a", marker + "-b", marker + "-c"}
		for i, name := range names {
			r := sampleRuntime(name)
			if i == 2 {
				r.Type = runtimestore.TypeMCP
			}
			if err := store.Create(ctx, r); err != nil {
				t.Fatalf("Create %s: %v", name, err)
			}
		}
		marked := func(filter runtimestore.ListFilter) []runtimestore.Runtime {
			t.Helper()
			all, err := store.List(ctx, filter)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			var out []runtimestore.Runtime
			for _, rt := range all {
				if strings.HasPrefix(rt.Name, marker) {
					out = append(out, rt)
				}
			}
			return out
		}
		all := marked(runtimestore.ListFilter{})
		if len(all) != 3 {
			t.Fatalf("List: %d marked runtimes, want 3", len(all))
		}
		if all[0].Name != names[0] || all[2].Name != names[2] {
			t.Errorf("List not name-ascending: %s..%s", all[0].Name, all[2].Name)
		}
		if mcp := marked(runtimestore.ListFilter{Type: runtimestore.TypeMCP}); len(mcp) != 1 {
			t.Errorf("List Type=mcp: %d marked, want 1", len(mcp))
		}

		if err := store.SoftDelete(ctx, names[0], time.Now().UTC()); err != nil {
			t.Fatalf("SoftDelete: %v", err)
		}
		if err := store.SoftDelete(ctx, names[0], time.Now().UTC()); err != nil {
			t.Errorf("idempotent SoftDelete: %v", err)
		}
		if err := store.SoftDelete(ctx, runtimeName(t), time.Now().UTC()); !errors.Is(err, runtimestore.ErrNotFound) {
			t.Errorf("SoftDelete missing: got %v, want ErrNotFound", err)
		}
		if n := len(marked(runtimestore.ListFilter{})); n != 2 {
			t.Errorf("List default after delete: %d marked, want 2", n)
		}
		if n := len(marked(runtimestore.ListFilter{IncludeDeleted: true})); n != 3 {
			t.Errorf("List IncludeDeleted after delete: %d marked, want 3", n)
		}
		deleted, err := store.Get(ctx, names[0])
		if err != nil || deleted.IsActive() {
			t.Errorf("Get soft-deleted: %+v err=%v; want inactive", deleted, err)
		}
	})
}
