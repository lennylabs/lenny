// SPDX-License-Identifier: MIT

package credassign_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
)

// spec: §12.6 lines 631-634 — the v1 StaticPoolGenerator wraps the §4.9
// Assigner: GenerateCredential mints a lease from the named pool and
// returns it as a *CredentialLease.
func TestStaticPoolGenerator_GenerateCredential_spec_12_6_631(t *testing.T) {
	svc, _, _ := newService(t)
	svc.RegisterPool(proxyPool("claude-prod", credential.StrategyLeastLoaded,
		healthyCred("key-1", "sk-ant-real")))

	gen := credassign.NewStaticPoolGenerator(svc)
	lease, err := gen.GenerateCredential(context.Background(), credassign.CredentialRequest{
		TenantID:  "acme",
		Provider:  credential.ProviderAnthropicDirect,
		PoolID:    "claude-prod",
		SessionID: "s_1",
		SpiffeURI: "spiffe://lenny.test/agent/claude-prod/pod-1",
	})
	if err != nil {
		t.Fatalf("GenerateCredential: %v", err)
	}
	if lease == nil {
		t.Fatal("GenerateCredential returned a nil lease")
	}
	if lease.PoolID != "claude-prod" || lease.CredentialID != "key-1" {
		t.Errorf("lease identity = %s/%s, want claude-prod/key-1", lease.PoolID, lease.CredentialID)
	}
	if lease.SessionID != "s_1" {
		t.Errorf("lease SessionID = %q, want s_1", lease.SessionID)
	}
	if lease.TenantID != "acme" {
		t.Errorf("lease TenantID = %q, want acme", lease.TenantID)
	}
}

// An unknown pool surfaces the §4.9 ErrPoolNotFound through the generator.
func TestStaticPoolGenerator_UnknownPool(t *testing.T) {
	svc, _, _ := newService(t)
	gen := credassign.NewStaticPoolGenerator(svc)
	_, err := gen.GenerateCredential(context.Background(), credassign.CredentialRequest{
		PoolID:    "missing",
		SessionID: "s_1",
	})
	if !errors.Is(err, credassign.ErrPoolNotFound) {
		t.Fatalf("err = %v, want ErrPoolNotFound", err)
	}
}

// A cancelled context is honored before the pool is touched.
func TestStaticPoolGenerator_ContextCancelled(t *testing.T) {
	svc, _, _ := newService(t)
	svc.RegisterPool(proxyPool("claude-prod", credential.StrategyLeastLoaded,
		healthyCred("key-1", "sk-ant-real")))
	gen := credassign.NewStaticPoolGenerator(svc)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := gen.GenerateCredential(ctx, credassign.CredentialRequest{
		PoolID:    "claude-prod",
		SessionID: "s_1",
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
