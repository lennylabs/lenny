// SPDX-License-Identifier: MIT

package credassign_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
)

// spec: §4.7 / §4.9 — converting a minted proxy-mode credential lease
// into the adapterv1.CredentialLease wire form the gateway pushes to a
// pod via AssignCredentials.

func TestProtoLeaseEncodesProxyMaterializedConfig(t *testing.T) {
	now := time.Date(2026, 4, 7, 9, 0, 0, 0, time.UTC)
	lease, err := credential.MintLease(credential.MintRequest{
		SessionID:    "s_1",
		Provider:     credential.ProviderAnthropicDirect,
		Source:       credential.SourcePool,
		PoolID:       "claude-prod",
		CredentialID: "key-1",
		DeliveryMode: credential.DeliveryProxy,
		Now:          now,
		ProxyURL:     "https://gateway-internal:8443/llm-proxy",
		ProxyDialect: "anthropic",
	})
	if err != nil {
		t.Fatalf("MintLease: %v", err)
	}

	proto, err := credassign.ProtoLease(lease)
	if err != nil {
		t.Fatalf("ProtoLease: %v", err)
	}
	if proto.GetLeaseId() != lease.LeaseID {
		t.Errorf("proto leaseId = %q, want %q", proto.GetLeaseId(), lease.LeaseID)
	}
	if proto.GetProvider() != "anthropic_direct" {
		t.Errorf("proto provider = %q, want anthropic_direct", proto.GetProvider())
	}
	if proto.GetExpiresAtUnixMs() != lease.ExpiresAt.UnixMilli() {
		t.Errorf("proto expiresAtUnixMs = %d, want %d", proto.GetExpiresAtUnixMs(), lease.ExpiresAt.UnixMilli())
	}
	if proto.GetRenewBeforeUnixMs() != lease.RenewBefore.UnixMilli() {
		t.Errorf("proto renewBeforeUnixMs = %d, want %d", proto.GetRenewBeforeUnixMs(), lease.RenewBefore.UnixMilli())
	}

	// The payload is the §4.7 credential-file entry: deliveryMode plus the
	// §4.9 proxy materializedConfig.
	var payload struct {
		DeliveryMode       string `json:"deliveryMode"`
		MaterializedConfig struct {
			ProxyURL     string `json:"proxyUrl"`
			ProxyDialect string `json:"proxyDialect"`
			LeaseToken   string `json:"leaseToken"`
		} `json:"materializedConfig"`
	}
	if err := json.Unmarshal(proto.GetPayload(), &payload); err != nil {
		t.Fatalf("decode proto payload: %v", err)
	}
	if payload.DeliveryMode != "proxy" {
		t.Errorf("payload deliveryMode = %q, want proxy", payload.DeliveryMode)
	}
	if payload.MaterializedConfig.ProxyURL != "https://gateway-internal:8443/llm-proxy" ||
		payload.MaterializedConfig.ProxyDialect != "anthropic" {
		t.Errorf("payload materializedConfig = %+v, want the pool's proxy endpoint", payload.MaterializedConfig)
	}
	if payload.MaterializedConfig.LeaseToken != lease.Proxy.LeaseToken {
		t.Errorf("payload leaseToken = %q, want the minted token %q",
			payload.MaterializedConfig.LeaseToken, lease.Proxy.LeaseToken)
	}
}

func TestProtoLeasePayloadCarriesNoUpstreamSecret(t *testing.T) {
	// A proxy-mode lease's wire form must not leak the real upstream
	// credential into the pod (§4.7 item 4) — it carries only the proxy
	// endpoint and the opaque lease token.
	lease, err := credential.MintLease(credential.MintRequest{
		SessionID:    "s_1",
		Provider:     credential.ProviderAnthropicDirect,
		Source:       credential.SourcePool,
		PoolID:       "claude-prod",
		CredentialID: "key-1",
		DeliveryMode: credential.DeliveryProxy,
		ProxyURL:     "https://p/v1",
		ProxyDialect: "anthropic",
	})
	if err != nil {
		t.Fatalf("MintLease: %v", err)
	}
	proto, err := credassign.ProtoLease(lease)
	if err != nil {
		t.Fatalf("ProtoLease: %v", err)
	}
	if strings.Contains(string(proto.GetPayload()), "apiKey") {
		t.Errorf("proxy-mode payload contains an apiKey field: %s", proto.GetPayload())
	}
}

func TestProtoLeaseEncodesDirectMaterializedConfig(t *testing.T) {
	// spec: §4.9 lines 1246-1298 — a direct-mode lease's per-provider
	// materializedConfig is rendered into the credential-file payload the
	// adapter writes; the pod reads the real upstream credential there.
	now := time.Date(2026, 4, 7, 9, 0, 0, 0, time.UTC)
	lease, err := credential.MintLease(credential.MintRequest{
		SessionID:    "s_1",
		Provider:     credential.ProviderAWSBedrock,
		Source:       credential.SourcePool,
		PoolID:       "bedrock-prod",
		CredentialID: "key-1",
		DeliveryMode: credential.DeliveryDirect,
		Direct: credential.MaterializedConfig{
			"accessKeyId": "AKIA", "secretAccessKey": "shh", "sessionToken": "tok",
			"region": "us-east-1", "expiresAt": "2026-04-07T10:30:00Z",
		},
		Now: now,
	})
	if err != nil {
		t.Fatalf("MintLease: %v", err)
	}

	proto, err := credassign.ProtoLease(lease)
	if err != nil {
		t.Fatalf("ProtoLease: %v", err)
	}
	var payload struct {
		DeliveryMode       string            `json:"deliveryMode"`
		MaterializedConfig map[string]string `json:"materializedConfig"`
	}
	if err := json.Unmarshal(proto.GetPayload(), &payload); err != nil {
		t.Fatalf("decode proto payload: %v", err)
	}
	if payload.DeliveryMode != "direct" {
		t.Errorf("payload deliveryMode = %q, want direct", payload.DeliveryMode)
	}
	if payload.MaterializedConfig["accessKeyId"] != "AKIA" ||
		payload.MaterializedConfig["region"] != "us-east-1" {
		t.Errorf("payload materializedConfig = %+v, want the bedrock STS fields", payload.MaterializedConfig)
	}
}

func TestProtoLeaseRejectsDirectModeLeaseWithoutConfig(t *testing.T) {
	// A direct-mode lease reloaded from the durable store carries no
	// materializedConfig (the store never persists upstream secrets);
	// ProtoLease refuses to emit an incomplete credential file rather
	// than deliver an empty payload to the pod.
	lease := credential.Lease{
		LeaseID:      "cl-stripped",
		SessionID:    "s_1",
		Provider:     credential.ProviderAnthropicDirect,
		Source:       credential.SourcePool,
		PoolID:       "claude-prod",
		CredentialID: "key-1",
		DeliveryMode: credential.DeliveryDirect,
		IssuedAt:     time.Now(),
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	if _, err := credassign.ProtoLease(lease); err == nil {
		t.Error("ProtoLease converted a direct-mode lease with no materializedConfig, want an error")
	}
}

func TestAssignProtoMintsAndConverts(t *testing.T) {
	svc, leases, creds := newService(t)
	svc.RegisterPool(proxyPool("claude-prod", credential.StrategyLeastLoaded,
		healthyCred("key-1", "sk-ant-real")))

	proto, err := svc.AssignProto("claude-prod", "s_1", "spiffe://lenny.test/agent/claude-prod/pod-1", "")
	if err != nil {
		t.Fatalf("AssignProto: %v", err)
	}
	if proto.GetProvider() != "anthropic_direct" || proto.GetLeaseId() == "" {
		t.Errorf("proto lease = %+v, want a minted anthropic_direct lease", proto)
	}

	// AssignProto recorded the lease in the store and cached the upstream
	// credential, the same as Assign.
	if leases.Len() != 1 {
		t.Errorf("lease store holds %d leases, want 1 after AssignProto", leases.Len())
	}
	if got, ok := leases.GetByID(proto.GetLeaseId()); !ok || got.SpiffeURI != "spiffe://lenny.test/agent/claude-prod/pod-1" {
		t.Error("AssignProto did not record the minted lease with its SPIFFE identity")
	}

	// The minted lease's token resolves to the lease, and the cached
	// upstream credential is reachable through it — the §4.9 LLM proxy
	// hot path.
	stored, ok := leases.GetByID(proto.GetLeaseId())
	if !ok {
		t.Fatal("the minted lease is not in the store")
	}
	if key, ok := creds.UpstreamCredential(stored); !ok || key != "sk-ant-real" {
		t.Errorf("cached upstream credential = %q ok=%v, want sk-ant-real", key, ok)
	}
}

func TestAssignProtoPropagatesAssignError(t *testing.T) {
	svc, _, _ := newService(t)
	if _, err := svc.AssignProto("no-such-pool", "s_1", "", ""); err == nil {
		t.Error("AssignProto succeeded for an unknown pool, want an error")
	}
}
