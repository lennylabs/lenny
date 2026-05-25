// SPDX-License-Identifier: MIT

package credential

import (
	"errors"
	"testing"
	"time"
)

// spec: §4.9 — the CredentialLease data model, structural validation,
// the source-aware credential key, and the LLM-proxy request checks.

// validProxyLease returns a structurally valid pool-backed proxy lease.
func validProxyLease() Lease {
	return Lease{
		LeaseID:      "cl_1",
		SessionID:    "s_1",
		Provider:     ProviderAnthropicDirect,
		Source:       SourcePool,
		PoolID:       "claude-prod",
		CredentialID: "key-1",
		DeliveryMode: DeliveryProxy,
		IssuedAt:     time.Now(),
		ExpiresAt:    time.Now().Add(time.Hour),
		Proxy: &ProxyConfig{
			ProxyURL:     "https://gateway-internal:8443/llm-proxy",
			ProxyDialect: "anthropic",
			LeaseToken:   "lt-abc",
		},
	}
}

func TestDeliveryModeIsValid(t *testing.T) {
	for _, m := range []DeliveryMode{DeliveryProxy, DeliveryDirect} {
		if !m.IsValid() {
			t.Errorf("DeliveryMode %q reported invalid", m)
		}
	}
	for _, m := range []DeliveryMode{"", "Proxy", "passthrough"} {
		if DeliveryMode(m).IsValid() {
			t.Errorf("DeliveryMode %q reported valid", m)
		}
	}
}

func TestLeaseValidateAcceptsValidLeases(t *testing.T) {
	if err := validProxyLease().Validate(); err != nil {
		t.Errorf("valid pool-backed proxy lease rejected: %v", err)
	}
	userDirect := Lease{
		LeaseID:       "cl_2",
		SessionID:     "s_2",
		Provider:      ProviderAnthropicDirect,
		Source:        SourceUser,
		TenantID:      "acme",
		CredentialRef: "cred-xyz",
		DeliveryMode:  DeliveryDirect,
		IssuedAt:      time.Now(),
		ExpiresAt:     time.Now().Add(time.Hour),
	}
	if err := userDirect.Validate(); err != nil {
		t.Errorf("valid user-backed direct lease rejected: %v", err)
	}
}

func TestLeaseValidateRejectsMissingCoreFields(t *testing.T) {
	var l Lease
	err := l.Validate()
	if err == nil {
		t.Fatal("empty lease passed Validate")
	}
	var le *LeaseError
	if !errors.As(err, &le) {
		t.Fatalf("error %v is not a *LeaseError", err)
	}
	if len(le.Violations) == 0 {
		t.Error("LeaseError carries no violations")
	}
}

func TestLeaseValidateRejectsSourceFieldMismatch(t *testing.T) {
	poolNoIDs := validProxyLease()
	poolNoIDs.PoolID = ""
	poolNoIDs.CredentialID = ""
	if poolNoIDs.Validate() == nil {
		t.Error("pool-backed lease without poolId/credentialId passed Validate")
	}

	userNoRefs := validProxyLease()
	userNoRefs.Source = SourceUser
	userNoRefs.PoolID = ""
	userNoRefs.CredentialID = ""
	if userNoRefs.Validate() == nil {
		t.Error("user-backed lease without tenantId/credentialRef passed Validate")
	}
}

func TestLeaseValidateRejectsBadProxyConfig(t *testing.T) {
	noProxy := validProxyLease()
	noProxy.Proxy = nil
	if noProxy.Validate() == nil {
		t.Error("proxy-mode lease with no materializedConfig passed Validate")
	}

	partial := validProxyLease()
	partial.Proxy = &ProxyConfig{ProxyURL: "https://p", ProxyDialect: "anthropic"} // no leaseToken
	if partial.Validate() == nil {
		t.Error("proxy-mode lease with an incomplete materializedConfig passed Validate")
	}
}

func TestLeaseValidateRejectsZeroExpiry(t *testing.T) {
	l := validProxyLease()
	l.ExpiresAt = time.Time{}
	if l.Validate() == nil {
		t.Error("lease with a zero expiresAt passed Validate")
	}
}

// spec: §4.9 line 1145 — a lease records issuedAt so expiresAt and the
// wall-clock duration are auditable.
func TestLeaseValidateRejectsZeroIssuedAt(t *testing.T) {
	l := validProxyLease()
	l.IssuedAt = time.Time{}
	err := l.Validate()
	if err == nil {
		t.Fatal("lease with a zero issuedAt passed Validate")
	}
	var le *LeaseError
	if !errors.As(err, &le) {
		t.Fatalf("error %v is not a *LeaseError", err)
	}
	found := false
	for _, v := range le.Violations {
		if v == "issuedAt is required" {
			found = true
		}
	}
	if !found {
		t.Errorf("violations %v do not name issuedAt", le.Violations)
	}
}

// spec: §4.9 line 1145 — Duration measures the lease lifetime from
// issuedAt, backing the §16.1 lenny_credential_lease_duration_seconds
// observation.
func TestLeaseDuration(t *testing.T) {
	issued := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	l := validProxyLease()
	l.IssuedAt = issued
	if got := l.Duration(issued.Add(90 * time.Second)); got != 90*time.Second {
		t.Errorf("Duration = %v, want 90s", got)
	}
	// A now before issuedAt yields a zero duration rather than negative.
	if got := l.Duration(issued.Add(-time.Second)); got != 0 {
		t.Errorf("Duration before issuedAt = %v, want 0", got)
	}
	// A zero issuedAt (a pre-issuedAt lease) yields zero.
	l.IssuedAt = time.Time{}
	if got := l.Duration(issued); got != 0 {
		t.Errorf("Duration with zero issuedAt = %v, want 0", got)
	}
}

func TestLeaseExpired(t *testing.T) {
	base := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	l := validProxyLease()
	l.ExpiresAt = base
	if l.Expired(base.Add(-time.Second)) {
		t.Error("lease reported expired one second before expiresAt")
	}
	if !l.Expired(base) {
		t.Error("lease not reported expired at expiresAt")
	}
	if !l.Expired(base.Add(time.Second)) {
		t.Error("lease not reported expired one second after expiresAt")
	}
}

func TestLeaseCredentialKey(t *testing.T) {
	pool := validProxyLease()
	pk := pool.CredentialKey()
	if pk.Source != SourcePool || pk.PoolID != "claude-prod" || pk.CredentialID != "key-1" {
		t.Errorf("pool credential key = %+v, want the pool-shaped key", pk)
	}
	if pk.TenantID != "" || pk.CredentialRef != "" {
		t.Errorf("pool credential key leaked user fields: %+v", pk)
	}

	user := validProxyLease()
	user.Source = SourceUser
	user.TenantID = "acme"
	user.CredentialRef = "cred-xyz"
	uk := user.CredentialKey()
	if uk.Source != SourceUser || uk.TenantID != "acme" || uk.CredentialRef != "cred-xyz" {
		t.Errorf("user credential key = %+v, want the user-shaped key", uk)
	}
	if uk.PoolID != "" || uk.CredentialID != "" {
		t.Errorf("user credential key leaked pool fields: %+v", uk)
	}
}

func TestValidateProxyRequestAdmitsAHealthyRequest(t *testing.T) {
	l := validProxyLease()
	l.SpiffeURI = "spiffe://lenny.test/agent/claude-prod/pod-1"
	got := l.ValidateProxyRequest(ProxyRequestCheck{
		Now:           time.Now(),
		PeerSPIFFEURI: "spiffe://lenny.test/agent/claude-prod/pod-1",
		Revoked:       false,
	})
	if got != RejectNone {
		t.Errorf("healthy request rejected: %q", got)
	}
}

func TestValidateProxyRequestRejectsExpiredLease(t *testing.T) {
	l := validProxyLease()
	l.ExpiresAt = time.Now().Add(-time.Minute)
	if got := l.ValidateProxyRequest(ProxyRequestCheck{Now: time.Now()}); got != RejectExpired {
		t.Errorf("rejection = %q, want expired", got)
	}
}

func TestValidateProxyRequestRejectsRevokedCredential(t *testing.T) {
	l := validProxyLease()
	got := l.ValidateProxyRequest(ProxyRequestCheck{Now: time.Now(), Revoked: true})
	if got != RejectRevoked {
		t.Errorf("rejection = %q, want revoked", got)
	}
}

func TestValidateProxyRequestRejectsSpiffeMismatch(t *testing.T) {
	l := validProxyLease()
	l.SpiffeURI = "spiffe://lenny.test/agent/claude-prod/pod-1"
	got := l.ValidateProxyRequest(ProxyRequestCheck{
		Now:           time.Now(),
		PeerSPIFFEURI: "spiffe://lenny.test/agent/other-pool/pod-9",
	})
	if got != RejectSpiffeMismatch {
		t.Errorf("rejection = %q, want spiffe_mismatch", got)
	}
}

func TestValidateProxyRequestSkipsSpiffeCheckWhenUnbound(t *testing.T) {
	l := validProxyLease() // SpiffeURI empty: SPIFFE-binding disabled
	got := l.ValidateProxyRequest(ProxyRequestCheck{
		Now:           time.Now(),
		PeerSPIFFEURI: "spiffe://lenny.test/agent/anything/pod-1",
	})
	if got != RejectNone {
		t.Errorf("rejection = %q, want none — an unbound lease skips the SPIFFE check", got)
	}
}

func TestValidateProxyRequestExpiryBeatsOtherChecks(t *testing.T) {
	// An expired, revoked, SPIFFE-mismatched lease reports the expiry
	// first: ValidateProxyRequest returns the first failed check.
	l := validProxyLease()
	l.ExpiresAt = time.Now().Add(-time.Minute)
	l.SpiffeURI = "spiffe://lenny.test/agent/claude-prod/pod-1"
	got := l.ValidateProxyRequest(ProxyRequestCheck{
		Now:           time.Now(),
		PeerSPIFFEURI: "spiffe://lenny.test/agent/other/pod-9",
		Revoked:       true,
	})
	if got != RejectExpired {
		t.Errorf("rejection = %q, want expired to take precedence", got)
	}
}
