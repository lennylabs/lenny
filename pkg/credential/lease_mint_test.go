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
	if !lease.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Errorf("ExpiresAt = %v, want now+1h", lease.ExpiresAt)
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
		Now:           time.Now(),
	})
	if err != nil {
		t.Fatalf("MintLease: %v", err)
	}
	if lease.Proxy != nil {
		t.Errorf("direct-mode lease carries a proxy config: %+v", lease.Proxy)
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
		SessionID:    "s_1",
		Provider:     ProviderVertexAI,
		Source:       SourcePool,
		PoolID:       "p",
		CredentialID: "c",
		DeliveryMode: DeliveryDirect,
		PoolTTLSeconds: 99999, // far above the vertex_ai 1h ceiling
		Now:            now,
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
