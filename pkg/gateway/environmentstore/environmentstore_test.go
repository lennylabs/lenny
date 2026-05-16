// SPDX-License-Identifier: MIT

package environmentstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
)

// spec: §10.6 Environment registry.

func validEnvironment(tenant, name string) environmentstore.Environment {
	return environmentstore.Environment{
		Name:        name,
		TenantID:    tenant,
		Description: "security engineering workspace",
		Members: []environmentstore.Member{
			{
				Identity: environmentstore.Identity{Type: "oidc-group", Value: "security-engineers"},
				Role:     environment.RoleCreator,
			},
		},
		RuntimeSelector: environment.Selector{MatchLabels: map[string]string{"team": "security"}},
	}
}

func TestCreateAndGet(t *testing.T) {
	m := environmentstore.NewMemory()
	if err := m.Create(context.Background(), validEnvironment("acme", "security-team")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := m.Get(context.Background(), "acme", "security-team")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Description != "security engineering workspace" || len(got.Members) != 1 {
		t.Errorf("Get = %+v, want the stored environment", got)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Error("Create must stamp CreatedAt and UpdatedAt")
	}
}

func TestCreateRejectsInvalidEnvironment(t *testing.T) {
	m := environmentstore.NewMemory()
	bad := validEnvironment("acme", "bad")
	bad.Members[0].Role = "superuser" // not a §10.6 role
	if err := m.Create(context.Background(), bad); err == nil {
		t.Error("Create accepted a member with an invalid role")
	}
	noName := validEnvironment("acme", "")
	if err := m.Create(context.Background(), noName); err == nil {
		t.Error("Create accepted an environment with no name")
	}
}

func TestCreateRejectsDuplicate(t *testing.T) {
	m := environmentstore.NewMemory()
	if err := m.Create(context.Background(), validEnvironment("acme", "dup")); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	if err := m.Create(context.Background(), validEnvironment("acme", "dup")); !errors.Is(err, environmentstore.ErrAlreadyExists) {
		t.Errorf("duplicate Create: got %v, want ErrAlreadyExists", err)
	}
}

func TestGetUnknownReturnsNotFound(t *testing.T) {
	m := environmentstore.NewMemory()
	if _, err := m.Get(context.Background(), "acme", "absent"); !errors.Is(err, environmentstore.ErrNotFound) {
		t.Errorf("Get unknown: got %v, want ErrNotFound", err)
	}
}

func TestGetIsTenantScoped(t *testing.T) {
	m := environmentstore.NewMemory()
	_ = m.Create(context.Background(), validEnvironment("acme", "shared-name"))
	if _, err := m.Get(context.Background(), "globex", "shared-name"); !errors.Is(err, environmentstore.ErrNotFound) {
		t.Error("an environment must not be visible to another tenant under the same name")
	}
}

func TestUpdateMutates(t *testing.T) {
	m := environmentstore.NewMemory()
	_ = m.Create(context.Background(), validEnvironment("acme", "env-u"))
	updated, err := m.Update(context.Background(), "acme", "env-u", func(e *environmentstore.Environment) error {
		e.Description = "renamed"
		e.DefaultDelegationPolicy = "security-policy"
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Description != "renamed" || updated.DefaultDelegationPolicy != "security-policy" {
		t.Errorf("Update returned %+v, want the mutation applied", updated)
	}
	got, _ := m.Get(context.Background(), "acme", "env-u")
	if got.Description != "renamed" {
		t.Error("the mutation was not persisted")
	}
}

func TestUpdateRejectsInvalidMutation(t *testing.T) {
	m := environmentstore.NewMemory()
	_ = m.Create(context.Background(), validEnvironment("acme", "env-iv"))
	_, err := m.Update(context.Background(), "acme", "env-iv", func(e *environmentstore.Environment) error {
		e.Members[0].Role = "superuser"
		return nil
	})
	if err == nil {
		t.Error("Update accepted a mutation to an invalid role")
	}
	got, _ := m.Get(context.Background(), "acme", "env-iv")
	if got.Members[0].Role != environment.RoleCreator {
		t.Errorf("a rejected Update mutated stored state: role = %q", got.Members[0].Role)
	}
}

func TestUpdateUnknownReturnsNotFound(t *testing.T) {
	m := environmentstore.NewMemory()
	_, err := m.Update(context.Background(), "acme", "absent", func(*environmentstore.Environment) error { return nil })
	if !errors.Is(err, environmentstore.ErrNotFound) {
		t.Errorf("Update unknown: got %v, want ErrNotFound", err)
	}
}

func TestListIsTenantScopedAndSorted(t *testing.T) {
	m := environmentstore.NewMemory()
	_ = m.Create(context.Background(), validEnvironment("acme", "env-b"))
	_ = m.Create(context.Background(), validEnvironment("acme", "env-a"))
	_ = m.Create(context.Background(), validEnvironment("globex", "env-z"))

	got, err := m.List(context.Background(), "acme")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 || got[0].Name != "env-a" || got[1].Name != "env-b" {
		t.Errorf("List = %v, want [env-a env-b] name-ascending, acme-scoped", names(got))
	}
}

func TestDelete(t *testing.T) {
	m := environmentstore.NewMemory()
	_ = m.Create(context.Background(), validEnvironment("acme", "env-d"))
	if err := m.Delete(context.Background(), "acme", "env-d"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := m.Get(context.Background(), "acme", "env-d"); !errors.Is(err, environmentstore.ErrNotFound) {
		t.Error("a deleted environment is still retrievable")
	}
	if err := m.Delete(context.Background(), "acme", "env-d"); !errors.Is(err, environmentstore.ErrNotFound) {
		t.Errorf("Delete of an absent environment: got %v, want ErrNotFound", err)
	}
}

func TestCloneIsolatesNestedState(t *testing.T) {
	m := environmentstore.NewMemory()
	_ = m.Create(context.Background(), validEnvironment("acme", "env-iso"))
	got, _ := m.Get(context.Background(), "acme", "env-iso")
	// Mutate the returned copy's nested member and selector map.
	got.Members[0].Role = environment.RoleAdmin
	got.RuntimeSelector.MatchLabels["team"] = "tampered"
	reread, _ := m.Get(context.Background(), "acme", "env-iso")
	if reread.Members[0].Role != environment.RoleCreator {
		t.Error("mutating a returned member reached into stored state")
	}
	if reread.RuntimeSelector.MatchLabels["team"] != "security" {
		t.Error("mutating a returned selector map reached into stored state")
	}
}

func names(es []environmentstore.Environment) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Name
	}
	return out
}
