// SPDX-License-Identifier: MIT

package externaladapterstore

import (
	"context"
	"errors"
	"testing"
)

func sampleAdapter() ExternalAdapter {
	return ExternalAdapter{
		Name:        "acme-a2a",
		DisplayName: "Acme A2A",
		Protocol:    "a2a",
		PathPrefix:  "/a2a",
		BinaryPath:  "/usr/local/bin/acme-a2a",
		Level:       "standard",
	}
}

// spec: §15 line 1414 — a newly registered adapter starts in
// pending_validation regardless of any status supplied.
func TestCreateForcesPendingValidation(t *testing.T) {
	m := NewMemory()
	a := sampleAdapter()
	a.Status = StatusActive // attempt to bypass the gate
	if err := m.Create(context.Background(), a); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := m.Get(context.Background(), a.Name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusPendingValidation {
		t.Fatalf("status = %q, want pending_validation", got.Status)
	}
	if got.CreatedAt.IsZero() || !got.UpdatedAt.Equal(got.CreatedAt) {
		t.Fatalf("timestamps not stamped: created=%v updated=%v", got.CreatedAt, got.UpdatedAt)
	}
}

func TestCreateDuplicateRejected(t *testing.T) {
	m := NewMemory()
	a := sampleAdapter()
	if err := m.Create(context.Background(), a); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.Create(context.Background(), a); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("dup Create err = %v, want ErrAlreadyExists", err)
	}
}

// spec: §15.1 — the registry key and §15.4 adapter-under-test fields are
// admission-validated at Create.
func TestCreateValidationErrors(t *testing.T) {
	cases := map[string]func(*ExternalAdapter){
		"empty-name":   func(a *ExternalAdapter) { a.Name = "" },
		"bad-name":     func(a *ExternalAdapter) { a.Name = "Bad Name!" },
		"empty-binary": func(a *ExternalAdapter) { a.BinaryPath = "" },
		"empty-level":  func(a *ExternalAdapter) { a.Level = "" },
		"bad-level":    func(a *ExternalAdapter) { a.Level = "expert" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := NewMemory()
			a := sampleAdapter()
			mutate(&a)
			if err := m.Create(context.Background(), a); err == nil {
				t.Fatalf("Create(%s) = nil, want validation error", name)
			}
		})
	}
}

func TestGetNotFound(t *testing.T) {
	m := NewMemory()
	if _, err := m.Get(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get err = %v, want ErrNotFound", err)
	}
}

func TestListSorted(t *testing.T) {
	m := NewMemory()
	for _, n := range []string{"zeta", "alpha", "mu"} {
		a := sampleAdapter()
		a.Name = n
		if err := m.Create(context.Background(), a); err != nil {
			t.Fatalf("Create %s: %v", n, err)
		}
	}
	rows, err := m.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 3 || rows[0].Name != "alpha" || rows[1].Name != "mu" || rows[2].Name != "zeta" {
		t.Fatalf("List not sorted: %+v", rows)
	}
}

func TestUpdateMutatesAndStamps(t *testing.T) {
	m := NewMemory()
	a := sampleAdapter()
	if err := m.Create(context.Background(), a); err != nil {
		t.Fatalf("Create: %v", err)
	}
	updated, err := m.Update(context.Background(), a.Name, func(e *ExternalAdapter) error {
		e.Status = StatusActive
		e.DisplayName = "Renamed"
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Status != StatusActive || updated.DisplayName != "Renamed" {
		t.Fatalf("update not applied: %+v", updated)
	}
	if !updated.UpdatedAt.After(updated.CreatedAt) && !updated.UpdatedAt.Equal(updated.CreatedAt) {
		t.Fatalf("UpdatedAt not stamped")
	}
}

func TestUpdateNotFound(t *testing.T) {
	m := NewMemory()
	_, err := m.Update(context.Background(), "missing", func(*ExternalAdapter) error { return nil })
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update err = %v, want ErrNotFound", err)
	}
}

func TestDelete(t *testing.T) {
	m := NewMemory()
	a := sampleAdapter()
	if err := m.Create(context.Background(), a); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.Delete(context.Background(), a.Name); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := m.Get(context.Background(), a.Name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete = %v, want ErrNotFound", err)
	}
	if err := m.Delete(context.Background(), a.Name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double Delete = %v, want ErrNotFound", err)
	}
}

func TestStatusIsValid(t *testing.T) {
	for _, s := range []Status{StatusPendingValidation, StatusActive, StatusValidationFailed} {
		if !s.IsValid() {
			t.Errorf("%q should be valid", s)
		}
	}
	if Status("bogus").IsValid() {
		t.Error("bogus status should be invalid")
	}
}
