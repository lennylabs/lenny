// SPDX-License-Identifier: MIT

package credential

import (
	"errors"
	"testing"
	"time"
)

// spec: §4.9 lines 1246-1298 — the per-provider direct-mode
// materializedConfig schema and its Required:yes field validation.

func TestValidateMaterializedConfigBuiltinHappyPaths(t *testing.T) {
	cases := map[Provider]MaterializedConfig{
		ProviderAnthropicDirect: {"apiKey": "sk-ant-x"},
		ProviderAWSBedrock: {
			"accessKeyId": "AKIA", "secretAccessKey": "shh", "sessionToken": "tok",
			"region": "us-east-1", "expiresAt": "2026-05-16T13:00:00Z",
		},
		ProviderVertexAI: {
			"accessToken": "ya29.x", "projectId": "acme", "region": "us-central1",
			"expiresAt": "2026-05-16T13:00:00Z",
		},
		ProviderGitHub:       {"token": "ghs_x", "expiresAt": "2026-05-16T13:00:00Z"},
		ProviderVaultTransit: {"vaultToken": "s.x", "vaultAddr": "https://vault:8200", "transitPath": "transit/", "keyName": "k", "expiresAt": "2026-05-16T13:00:00Z"},
	}
	for p, cfg := range cases {
		if err := ValidateMaterializedConfig(p, cfg); err != nil {
			t.Errorf("provider %q: ValidateMaterializedConfig rejected a complete config: %v", p, err)
		}
	}
}

func TestValidateMaterializedConfigReportsMissingFields(t *testing.T) {
	// AWS Bedrock with only the region present must report the four
	// remaining Required:yes fields, in spec order.
	err := ValidateMaterializedConfig(ProviderAWSBedrock, MaterializedConfig{"region": "us-east-1"})
	var me *MaterializationError
	if !errors.As(err, &me) {
		t.Fatalf("error %v is not a *MaterializationError", err)
	}
	if !errors.Is(err, ErrCredentialMaterialization) {
		t.Error("errors.Is against the materialization sentinel did not match")
	}
	want := []string{"accessKeyId", "secretAccessKey", "sessionToken", "expiresAt"}
	if len(me.Missing) != len(want) {
		t.Fatalf("missing = %v, want %v", me.Missing, want)
	}
	for i, f := range want {
		if me.Missing[i] != f {
			t.Errorf("missing[%d] = %q, want %q", i, me.Missing[i], f)
		}
	}
}

func TestValidateMaterializedConfigTreatsEmptyValueAsMissing(t *testing.T) {
	// A present-but-empty field is treated as absent: an empty apiKey is
	// not a usable credential.
	err := ValidateMaterializedConfig(ProviderAnthropicDirect, MaterializedConfig{"apiKey": ""})
	if err == nil {
		t.Fatal("ValidateMaterializedConfig accepted an empty apiKey")
	}
}

func TestValidateMaterializedConfigRejectsUnparseableExpiry(t *testing.T) {
	err := ValidateMaterializedConfig(ProviderGitHub, MaterializedConfig{
		"token": "ghs_x", "expiresAt": "not-a-timestamp",
	})
	var me *MaterializationError
	if !errors.As(err, &me) || me.Reason == "" {
		t.Fatalf("expected a MaterializationError with a reason, got %v", err)
	}
}

func TestValidateMaterializedConfigAzureAPIKeyPool(t *testing.T) {
	// API-key pool: apiKey + endpoint + deploymentName, no expiresAt.
	cfg := MaterializedConfig{"apiKey": "az-key", "endpoint": "https://r.openai.azure.com", "deploymentName": "gpt"}
	if err := ValidateMaterializedConfig(ProviderAzureOpenAI, cfg); err != nil {
		t.Errorf("API-key Azure pool rejected: %v", err)
	}
}

func TestValidateMaterializedConfigAzureADPool(t *testing.T) {
	// Azure AD pool: accessToken + endpoint + deploymentName + expiresAt.
	cfg := MaterializedConfig{
		"accessToken": "aad-tok", "endpoint": "https://r.openai.azure.com",
		"deploymentName": "gpt", "expiresAt": "2026-05-16T13:00:00Z",
	}
	if err := ValidateMaterializedConfig(ProviderAzureOpenAI, cfg); err != nil {
		t.Errorf("Azure AD pool rejected: %v", err)
	}
	// Azure AD pool without expiresAt fails.
	delete(cfg, "expiresAt")
	if err := ValidateMaterializedConfig(ProviderAzureOpenAI, cfg); err == nil {
		t.Error("Azure AD pool with no expiresAt was accepted")
	}
}

func TestValidateMaterializedConfigAzureRejectsBothAndNeitherKeyModes(t *testing.T) {
	base := MaterializedConfig{"endpoint": "https://r.openai.azure.com", "deploymentName": "gpt"}
	if err := ValidateMaterializedConfig(ProviderAzureOpenAI, base); err == nil {
		t.Error("Azure pool with neither apiKey nor accessToken was accepted")
	}
	both := MaterializedConfig{
		"endpoint": "https://r.openai.azure.com", "deploymentName": "gpt",
		"apiKey": "k", "accessToken": "t", "expiresAt": "2026-05-16T13:00:00Z",
	}
	if err := ValidateMaterializedConfig(ProviderAzureOpenAI, both); err == nil {
		t.Error("Azure pool with both apiKey and accessToken was accepted")
	}
}

func TestValidateMaterializedConfigCustomProviderBypasses(t *testing.T) {
	// spec: §4.9 line 1298 — custom providers bypass built-in field
	// validation. An unknown provider with an empty config is accepted.
	if err := ValidateMaterializedConfig(Provider("my_custom_provider"), nil); err != nil {
		t.Errorf("custom provider validation did not bypass: %v", err)
	}
}

func TestMaterializedExpiry(t *testing.T) {
	got, ok := MaterializedExpiry(MaterializedConfig{"expiresAt": "2026-05-16T13:00:00Z"})
	if !ok || !got.Equal(time.Date(2026, 5, 16, 13, 0, 0, 0, time.UTC)) {
		t.Errorf("MaterializedExpiry = %v ok=%v, want 2026-05-16T13:00:00Z", got, ok)
	}
	if _, ok := MaterializedExpiry(MaterializedConfig{}); ok {
		t.Error("MaterializedExpiry reported ok for a config with no expiresAt")
	}
	if _, ok := MaterializedExpiry(MaterializedConfig{"expiresAt": "bad"}); ok {
		t.Error("MaterializedExpiry reported ok for an unparseable expiresAt")
	}
}
