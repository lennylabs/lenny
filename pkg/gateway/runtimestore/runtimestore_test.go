// SPDX-License-Identifier: MIT

package runtimestore_test

import (
	"context"
	"errors"
	"testing"
	"time"

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
