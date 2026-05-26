// SPDX-License-Identifier: MIT

package credassign_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credassign"
)

// spec: §4.9 lines 1246-1298 — direct-mode credential assignment: the
// Token Service materializes a per-provider credential bundle and the
// gateway delivers it to the pod through the adapter credential file.

// directPool returns a direct-mode pool for provider p.
func directPool(name string, p credential.Provider, creds ...credassign.PoolCredential) credassign.Pool {
	return credassign.Pool{
		Name:         name,
		Provider:     p,
		DeliveryMode: credential.DeliveryDirect,
		Strategy:     credential.StrategyLeastLoaded,
		Credentials:  creds,
	}
}

func TestAssignProtoDirectAnthropicPromotesAPIKey(t *testing.T) {
	// A single-secret anthropic_direct pool records only an APIKey; the
	// assigner promotes it to the {apiKey: …} materializedConfig the
	// runtime expects, with no separate Materialized bundle needed.
	svc, _, _ := newService(t)
	svc.RegisterPool(directPool("claude-direct", credential.ProviderAnthropicDirect,
		healthyCred("key-1", "sk-ant-real")))

	proto, err := svc.AssignProto("claude-direct", "s_1", "", "")
	if err != nil {
		t.Fatalf("AssignProto: %v", err)
	}
	var payload struct {
		DeliveryMode       string            `json:"deliveryMode"`
		MaterializedConfig map[string]string `json:"materializedConfig"`
	}
	if err := json.Unmarshal(proto.GetPayload(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.DeliveryMode != "direct" {
		t.Errorf("deliveryMode = %q, want direct", payload.DeliveryMode)
	}
	if payload.MaterializedConfig["apiKey"] != "sk-ant-real" {
		t.Errorf("materializedConfig = %+v, want the promoted apiKey", payload.MaterializedConfig)
	}
}

func TestAssignProtoDirectBedrockDeliversMaterializedBundle(t *testing.T) {
	svc, _, _ := newService(t)
	svc.RegisterPool(directPool("bedrock-prod", credential.ProviderAWSBedrock,
		credassign.PoolCredential{
			ID:      "key-1",
			Healthy: true,
			Materialized: credential.MaterializedConfig{
				"accessKeyId": "AKIA", "secretAccessKey": "shh", "sessionToken": "tok",
				"region": "us-east-1", "expiresAt": "2026-05-16T13:00:00Z",
			},
		}))

	proto, err := svc.AssignProto("bedrock-prod", "s_1", "", "")
	if err != nil {
		t.Fatalf("AssignProto: %v", err)
	}
	var payload struct {
		MaterializedConfig map[string]string `json:"materializedConfig"`
	}
	if err := json.Unmarshal(proto.GetPayload(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.MaterializedConfig["sessionToken"] != "tok" ||
		payload.MaterializedConfig["region"] != "us-east-1" {
		t.Errorf("materializedConfig = %+v, want the STS bundle", payload.MaterializedConfig)
	}
}

func TestAssignDirectIncompleteBundleFailsMaterialization(t *testing.T) {
	// spec: §4.9 line 1298 — an incomplete materializedConfig fails the
	// assign with a materialization error.
	svc, _, _ := newService(t)
	svc.RegisterPool(directPool("bedrock-broken", credential.ProviderAWSBedrock,
		credassign.PoolCredential{
			ID:           "key-1",
			Healthy:      true,
			Materialized: credential.MaterializedConfig{"region": "us-east-1"}, // missing STS triple + expiresAt
		}))

	_, err := svc.Assign("bedrock-broken", "s_1", "", "")
	if !errors.Is(err, credential.ErrCredentialMaterialization) {
		t.Fatalf("Assign error = %v, want a materialization error", err)
	}
}
