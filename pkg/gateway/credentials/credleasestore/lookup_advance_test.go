// SPDX-License-Identifier: MIT

package credleasestore_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
)

// TestMemoryLookupAndAdvance_spec_4_9 asserts that the in-memory store's
// Lookup reports an absent lease as not found with no error, and that Advance
// of a removed lease writes nothing while Advance of a present lease moves
// only its ExpiresAt and RenewBefore.
//
// spec: §4.9 (Proactive Lease Renewal)
func TestMemoryLookupAndAdvance_spec_4_9(t *testing.T) {
	s := credleasestore.New()
	if _, ok, err := s.Lookup("cl-absent"); ok || err != nil {
		t.Fatalf("Lookup(absent) = ok %v err %v", ok, err)
	}
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	l := credential.Lease{
		LeaseID: "cl-1", SessionID: "s1", Provider: credential.ProviderAnthropicDirect,
		Source: credential.SourcePool, PoolID: "p", CredentialID: "k", DeliveryMode: credential.DeliveryProxy,
		IssuedAt: now, ExpiresAt: now.Add(time.Hour), RenewBefore: now.Add(50 * time.Minute),
		CoordinationGeneration: 3,
		Proxy:                  &credential.ProxyConfig{ProxyURL: "https://p", ProxyDialect: "anthropic", LeaseToken: "lt-1"},
	}
	if err := s.Put(l); err != nil {
		t.Fatalf("Put: %v", err)
	}
	moved := l
	moved.ExpiresAt = now.Add(2 * time.Hour)
	moved.RenewBefore = now.Add(110 * time.Minute)
	moved.CoordinationGeneration = 99
	if ok, err := s.Advance(moved); !ok || err != nil {
		t.Fatalf("Advance(present) = %v %v", ok, err)
	}
	got, ok, err := s.Lookup("cl-1")
	if !ok || err != nil {
		t.Fatalf("Lookup = ok %v err %v", ok, err)
	}
	if !got.ExpiresAt.Equal(moved.ExpiresAt) || !got.RenewBefore.Equal(moved.RenewBefore) || got.CoordinationGeneration != 3 {
		t.Errorf("advanced lease = %+v, want only the expiry and renewal moved", got)
	}
	s.Remove("cl-1")
	if ok, err := s.Advance(moved); ok || err != nil {
		t.Errorf("Advance(removed) = %v %v, want false and nil", ok, err)
	}
	if s.Len() != 0 {
		t.Errorf("Len = %d, want 0: Advance inserts nothing", s.Len())
	}
}
