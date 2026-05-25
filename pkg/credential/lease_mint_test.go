// SPDX-License-Identifier: MIT

package credential

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// spec: §4.9 — the credential-lease minter: synthetic TTL resolution,
// the provider TTL ceilings, and lease/token generation.

func TestProviderMaxTTLSeconds(t *testing.T) {
	for _, tc := range []struct {
		provider Provider
		want     int
	}{
		{ProviderAnthropicDirect, 0},
		{ProviderAWSBedrock, 43200},
		{ProviderAzureOpenAI, 86400},
		{ProviderVertexAI, 3600},
		{ProviderGitHub, 3600},
		{ProviderVaultTransit, 0},
	} {
		if got := ProviderMaxTTLSeconds(tc.provider); got != tc.want {
			t.Errorf("ProviderMaxTTLSeconds(%q) = %d, want %d", tc.provider, got, tc.want)
		}
	}
}

func TestResolveLeaseTTL(t *testing.T) {
	for _, tc := range []struct {
		name        string
		poolSeconds int
		provider    Provider
		want        time.Duration
	}{
		{"default when unset", 0, ProviderAnthropicDirect, time.Hour},
		{"pool override honored", 1800, ProviderAnthropicDirect, 30 * time.Minute},
		{"override below ceiling kept", 1800, ProviderVertexAI, 30 * time.Minute},
		{"override above ceiling capped", 99999, ProviderVertexAI, time.Hour},
		{"no ceiling keeps large override", 99999, ProviderAnthropicDirect, 99999 * time.Second},
		{"bedrock capped at 12h", 99999, ProviderAWSBedrock, 12 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveLeaseTTL(tc.poolSeconds, tc.provider); got != tc.want {
				t.Errorf("ResolveLeaseTTL(%d, %q) = %v, want %v", tc.poolSeconds, tc.provider, got, tc.want)
			}
		})
	}
}

func TestMintLeaseProxyLease(t *testing.T) {
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	lease, err := MintLease(MintRequest{
		SessionID:    "s_1",
		Provider:     ProviderAnthropicDirect,
		Source:       SourcePool,
		PoolID:       "claude-prod",
		CredentialID: "key-1",
		DeliveryMode: DeliveryProxy,
		Now:          now,
		ProxyURL:     "https://gateway-internal:8443/llm-proxy",
		ProxyDialect: "anthropic",
	})
	if err != nil {
		t.Fatalf("MintLease: %v", err)
	}
	if err := lease.Validate(); err != nil {
		t.Errorf("minted lease fails Validate: %v", err)
	}
	// spec: §4.9 line 1145 — expiresAt = issuedAt + min(leaseTTLSeconds, providerMaxTTL).
	if !lease.IssuedAt.Equal(now) {
		t.Errorf("IssuedAt = %v, want %v", lease.IssuedAt, now)
	}
	if !lease.ExpiresAt.Equal(lease.IssuedAt.Add(time.Hour)) {
		t.Errorf("ExpiresAt = %v, want issuedAt+1h", lease.ExpiresAt)
	}
	if !lease.RenewBefore.Equal(lease.ExpiresAt.Add(-300 * time.Second)) {
		t.Errorf("RenewBefore = %v, want expiresAt-300s", lease.RenewBefore)
	}
	if lease.Proxy == nil || lease.Proxy.LeaseToken == "" {
		t.Fatal("proxy-mode lease has no lease token")
	}
	if !strings.HasPrefix(lease.Proxy.LeaseToken, "lt-") {
		t.Errorf("lease token %q lacks the lt- prefix", lease.Proxy.LeaseToken)
	}
	if !strings.HasPrefix(lease.LeaseID, "cl-") {
		t.Errorf("lease ID %q lacks the cl- prefix", lease.LeaseID)
	}
}

func TestMintLeaseDirectLeaseHasNoProxyConfig(t *testing.T) {
	lease, err := MintLease(MintRequest{
		SessionID:     "s_1",
		Provider:      ProviderAnthropicDirect,
		Source:        SourceUser,
		TenantID:      "acme",
		CredentialRef: "cred-1",
		DeliveryMode:  DeliveryDirect,
		Direct:        MaterializedConfig{"apiKey": "sk-ant-x"},
		Now:           time.Now(),
	})
	if err != nil {
		t.Fatalf("MintLease: %v", err)
	}
	if lease.Proxy != nil {
		t.Errorf("direct-mode lease carries a proxy config: %+v", lease.Proxy)
	}
	if lease.Direct["apiKey"] != "sk-ant-x" {
		t.Errorf("direct-mode lease dropped its materializedConfig: %+v", lease.Direct)
	}
}

func TestMintLeaseGeneratesUniqueIdentifiers(t *testing.T) {
	req := MintRequest{
		SessionID: "s_1", Provider: ProviderAnthropicDirect, Source: SourcePool,
		PoolID: "p", CredentialID: "c", DeliveryMode: DeliveryProxy,
		ProxyURL: "https://p/llm-proxy", ProxyDialect: "anthropic",
	}
	a, err := MintLease(req)
	if err != nil {
		t.Fatalf("MintLease a: %v", err)
	}
	b, err := MintLease(req)
	if err != nil {
		t.Fatalf("MintLease b: %v", err)
	}
	if a.LeaseID == b.LeaseID {
		t.Error("two mints produced the same lease ID")
	}
	if a.Proxy.LeaseToken == b.Proxy.LeaseToken {
		t.Error("two mints produced the same lease token")
	}
}

func TestMintLeaseRejectsInvalidRequest(t *testing.T) {
	// A proxy-mode request with no proxy URL yields an invalid lease.
	_, err := MintLease(MintRequest{
		SessionID:    "s_1",
		Provider:     ProviderAnthropicDirect,
		Source:       SourcePool,
		PoolID:       "p",
		CredentialID: "c",
		DeliveryMode: DeliveryProxy,
		ProxyDialect: "anthropic",
	})
	if err == nil {
		t.Fatal("MintLease accepted a proxy request with no proxy URL")
	}
	var le *LeaseError
	if !errors.As(err, &le) {
		t.Errorf("error %v is not a *LeaseError", err)
	}
}

func TestMintLeaseCapsTTLAtProviderCeiling(t *testing.T) {
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	lease, err := MintLease(MintRequest{
		SessionID:      "s_1",
		Provider:       ProviderVertexAI,
		Source:         SourcePool,
		PoolID:         "p",
		CredentialID:   "c",
		DeliveryMode:   DeliveryDirect,
		PoolTTLSeconds: 99999, // far above the vertex_ai 1h ceiling
		// The materialized token expiry equals the capped lease expiry,
		// so the direct-expiry clamp does not fire and the ceiling stands.
		Direct: MaterializedConfig{
			"accessToken": "ya29.x", "projectId": "acme-proj", "region": "us-central1",
			"expiresAt": "2026-05-16T13:00:00Z",
		},
		Now: now,
	})
	if err != nil {
		t.Fatalf("MintLease: %v", err)
	}
	if !lease.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Errorf("ExpiresAt = %v, want the vertex_ai ceiling now+1h", lease.ExpiresAt)
	}
}

func TestMintLeaseHonorsRenewBeforeOverride(t *testing.T) {
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	lease, err := MintLease(MintRequest{
		SessionID:          "s_1",
		Provider:           ProviderAnthropicDirect,
		Source:             SourcePool,
		PoolID:             "p",
		CredentialID:       "c",
		DeliveryMode:       DeliveryDirect,
		Direct:             MaterializedConfig{"apiKey": "sk-ant-x"},
		RenewBeforeSeconds: 600,
		Now:                now,
	})
	if err != nil {
		t.Fatalf("MintLease: %v", err)
	}
	if !lease.RenewBefore.Equal(lease.ExpiresAt.Add(-600 * time.Second)) {
		t.Errorf("RenewBefore = %v, want expiresAt-600s", lease.RenewBefore)
	}
}

func TestMintLeaseDirectModeRequiresMaterializedConfig(t *testing.T) {
	// spec: §4.9 line 1298 — a direct-mode mint with a missing required
	// materializedConfig field fails with a *MaterializationError, not a
	// structural *LeaseError.
	_, err := MintLease(MintRequest{
		SessionID:    "s_1",
		Provider:     ProviderAWSBedrock,
		Source:       SourcePool,
		PoolID:       "p",
		CredentialID: "c",
		DeliveryMode: DeliveryDirect,
		Direct:       MaterializedConfig{"region": "us-east-1"}, // missing the STS triple + expiresAt
	})
	var me *MaterializationError
	if !errors.As(err, &me) {
		t.Fatalf("error %v is not a *MaterializationError", err)
	}
	if me.Provider != ProviderAWSBedrock {
		t.Errorf("MaterializationError.Provider = %q, want aws_bedrock", me.Provider)
	}
}

func TestMintLeaseClampsExpiryToVaultTokenExpiry(t *testing.T) {
	// spec: §4.9 line 1154 — when materializing a vault_transit lease the
	// Token Service sets expiresAt = min(issuedAt + leaseTTLSeconds,
	// vaultTokenExpiryTime). A Vault token expiring before the TTL window
	// clamps the lease and renewBefore is recomputed from the clamp.
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	vaultExpiry := now.Add(20 * time.Minute) // shorter than the 1h default TTL
	lease, err := MintLease(MintRequest{
		SessionID:          "s_1",
		Provider:           ProviderVaultTransit,
		Source:             SourcePool,
		PoolID:             "p",
		CredentialID:       "c",
		DeliveryMode:       DeliveryDirect,
		RenewBeforeSeconds: 300,
		Direct: MaterializedConfig{
			"vaultToken": "s.x", "vaultAddr": "https://vault:8200",
			"transitPath": "transit/", "keyName": "k",
			"expiresAt": vaultExpiry.Format(time.RFC3339),
		},
		Now: now,
	})
	if err != nil {
		t.Fatalf("MintLease: %v", err)
	}
	if !lease.ExpiresAt.Equal(vaultExpiry) {
		t.Errorf("ExpiresAt = %v, want the clamped vault expiry %v", lease.ExpiresAt, vaultExpiry)
	}
	if !lease.RenewBefore.Equal(vaultExpiry.Add(-300 * time.Second)) {
		t.Errorf("RenewBefore = %v, want vaultExpiry-300s", lease.RenewBefore)
	}
}

func TestMintLeaseDoesNotClampWhenVaultExpiryExceedsTTL(t *testing.T) {
	// A Vault token outliving the TTL window leaves the TTL-derived
	// expiry in place: the lease never outlives min(TTL, token), and here
	// the TTL is the binding limit.
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	lease, err := MintLease(MintRequest{
		SessionID:      "s_1",
		Provider:       ProviderVaultTransit,
		Source:         SourcePool,
		PoolID:         "p",
		CredentialID:   "c",
		DeliveryMode:   DeliveryDirect,
		PoolTTLSeconds: 1800, // 30m TTL window
		Direct: MaterializedConfig{
			"vaultToken": "s.x", "vaultAddr": "https://vault:8200",
			"transitPath": "transit/", "keyName": "k",
			"expiresAt": now.Add(2 * time.Hour).Format(time.RFC3339),
		},
		Now: now,
	})
	if err != nil {
		t.Fatalf("MintLease: %v", err)
	}
	if !lease.ExpiresAt.Equal(now.Add(30 * time.Minute)) {
		t.Errorf("ExpiresAt = %v, want the TTL window now+30m", lease.ExpiresAt)
	}
}
