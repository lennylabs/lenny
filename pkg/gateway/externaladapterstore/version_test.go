// SPDX-License-Identifier: MIT

package externaladapterstore

import (
	"context"
	"testing"
)

// TestExternalAdapterVersionRoundTrip_spec_15_1_1207 covers the §15.1 lines
// 1207-1211 optimistic-concurrency version: it starts at 1 on Create and
// increments on every successful Update (including a validate transition).
func TestExternalAdapterVersionRoundTrip_spec_15_1_1207(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	a := ExternalAdapter{
		Name:       "acme-a2a",
		BinaryPath: "/usr/local/bin/acme-a2a",
		Level:      "standard",
	}
	if err := m.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := m.Get(ctx, "acme-a2a")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Version != 1 {
		t.Fatalf("create version = %d, want 1", got.Version)
	}

	updated, err := m.Update(ctx, "acme-a2a", func(a *ExternalAdapter) error {
		a.DisplayName = "Acme"
		return nil
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Version != 2 {
		t.Errorf("update version = %d, want 2", updated.Version)
	}
}
