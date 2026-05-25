// SPDX-License-Identifier: MIT

package credential_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
)

// spec: §4.9.2 — the credential audit event catalog enumerates the
// twelve event types. The typed surface must transcribe them exactly.
func TestCredentialAuditEventCatalog_F491(t *testing.T) {
	want := []string{
		"credential.registered",
		"credential.deleted",
		"credential.rotated",
		"credential.user_revoked",
		"credential.leased",
		"credential.revoked",
		"credential.re_enabled",
		"credential.renewed",
		"credential.rotation_ceiling_hit",
		"credential.fallback_exhausted",
		"credential.lease_spiffe_mismatch",
		"credential.proxy_mode_spiffe_binding_disabled",
	}
	got := credential.AllCredentialAuditEventTypes()
	if len(got) != len(want) {
		t.Fatalf("catalog size = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].String() != w {
			t.Errorf("catalog[%d] = %q, want %q", i, got[i].String(), w)
		}
	}
}

// IsCredentialAuditEvent is true for every catalog entry and false for a
// non-credential or misspelled event type.
func TestIsCredentialAuditEvent_F491(t *testing.T) {
	for _, et := range credential.AllCredentialAuditEventTypes() {
		if !credential.IsCredentialAuditEvent(et) {
			t.Errorf("IsCredentialAuditEvent(%q) = false, want true", et)
		}
	}
	for _, bad := range []credential.AuditEventType{
		"credential.lease_revoked", // pre-§4.9.2 OCSF misname
		"credential.pool_exhausted",
		"token.exchanged",
		"",
	} {
		if credential.IsCredentialAuditEvent(bad) {
			t.Errorf("IsCredentialAuditEvent(%q) = true, want false", bad)
		}
	}
}

// AllCredentialAuditEventTypes returns a fresh slice each call so a
// caller mutating the result does not corrupt the catalog.
func TestCredentialAuditCatalogIsCopied_F491(t *testing.T) {
	a := credential.AllCredentialAuditEventTypes()
	a[0] = "mutated"
	b := credential.AllCredentialAuditEventTypes()
	if b[0] == "mutated" {
		t.Fatal("AllCredentialAuditEventTypes returns a shared backing array")
	}
}
