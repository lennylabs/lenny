// SPDX-License-Identifier: MIT

package credentialpoolstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore"
)

// TestCredentialPoolVersionRoundTrip_spec_15_1_1207 covers the §15.1 lines
// 1207-1213 optimistic-concurrency version: it starts at 1 on Create and
// increments on every successful Update and SoftDelete.
func TestCredentialPoolVersionRoundTrip_spec_15_1_1207(t *testing.T) {
	ctx := context.Background()
	m := credentialpoolstore.NewMemory()
	p := credentialpoolstore.CredentialPool{
		TenantID:           "acme",
		Name:               "claude",
		Provider:           "anthropic_direct",
		AssignmentStrategy: "least-loaded",
		Credentials: []credentialpoolstore.Credential{
			{ID: "key-1", SecretRef: "lenny-system/anthropic-key-1"},
		},
	}
	if err := m.Create(ctx, p); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := m.Get(ctx, "acme", "claude")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Version != 1 {
		t.Fatalf("create version = %d, want 1", got.Version)
	}

	updated, err := m.Update(ctx, "acme", "claude", func(p *credentialpoolstore.CredentialPool) error {
		p.MaxConcurrentSessions = 20
		return nil
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Version != 2 {
		t.Errorf("update version = %d, want 2", updated.Version)
	}

	if err := m.SoftDelete(ctx, "acme", "claude", time.Now()); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	got, _ = m.Get(ctx, "acme", "claude")
	if got.Version != 3 {
		t.Errorf("soft-delete version = %d, want 3", got.Version)
	}
}
