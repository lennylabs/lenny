// SPDX-License-Identifier: MIT

package tokenservice

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
)

// proxyPool wires a §4.9 proxy-mode pool with one healthy credential.
func proxyPool(t *testing.T, name string) (credassign.Pool, *credleasestore.Store, *GRPCServer) {
	t.Helper()
	leases := credleasestore.New()
	creds := credcache.New()
	svc := credassign.New(leases, creds)
	pool := credassign.Pool{
		Name:         name,
		Provider:     credential.ProviderAnthropicDirect,
		DeliveryMode: credential.DeliveryProxy,
		Strategy:     credential.StrategyLeastLoaded,
		ProxyURL:     "https://gateway-internal:8443/llm-proxy",
		ProxyDialect: "anthropic",
		Credentials: []credassign.PoolCredential{
			{ID: "key-1", APIKey: "sk-ant-real", Healthy: true},
		},
	}
	svc.RegisterPool(pool)
	return pool, leases, NewGRPCServer(svc, leases)
}

// spec: 4.3 / 4.9
// diagnosis: AssignCredentials returns the materialized §4.9 lease set
// keyed by provider id. The minted lease is recorded in the
// credential-lease store and resolvable by its bearer token.
func TestGRPCAssignCredentialsReturnsMaterializedLease(t *testing.T) {
	_, leases, srv := proxyPool(t, "claude-prod")
	resp, err := srv.AssignCredentials(context.Background(), &tokensv1.AssignCredentialsRequest{
		TenantId:     "acme",
		SessionId:    "s_1",
		PodSpiffeUri: "spiffe://lenny.test/agent/claude-prod/pod-1",
		PoolIds:      []string{"claude-prod"},
	})
	if err != nil {
		t.Fatalf("AssignCredentials: %v", err)
	}
	got, ok := resp.Leases["anthropic_direct"]
	if !ok {
		t.Fatalf("response has no anthropic_direct lease: %+v", resp.Leases)
	}
	if got.PoolId != "claude-prod" || got.CredentialId != "key-1" {
		t.Errorf("lease identity = %s/%s, want claude-prod/key-1", got.PoolId, got.CredentialId)
	}
	if got.DeliveryMode != string(credential.DeliveryProxy) || got.LeaseToken == "" {
		t.Errorf("proxy-mode response missing lease_token: %+v", got)
	}
	if _, found := leases.GetByID(got.LeaseId); !found {
		t.Errorf("lease %q not recorded in the lease store", got.LeaseId)
	}
	// spec: §4.9 line 1145 — the wire lease carries issued_at so the
	// gateway can record the lease's wall-clock duration. It must be set
	// and not after expires_at.
	if got.IssuedAt == nil || got.IssuedAt.AsTime().IsZero() {
		t.Errorf("lease has no issued_at: %+v", got)
	}
	if got.ExpiresAt != nil && got.IssuedAt.AsTime().After(got.ExpiresAt.AsTime()) {
		t.Errorf("issued_at %v is after expires_at %v", got.IssuedAt.AsTime(), got.ExpiresAt.AsTime())
	}
}

// spec: 4.3
// diagnosis: invalid requests are rejected with InvalidArgument.
func TestGRPCAssignCredentialsRejectsInvalid(t *testing.T) {
	_, _, srv := proxyPool(t, "claude-prod")
	cases := []struct {
		name string
		req  *tokensv1.AssignCredentialsRequest
	}{
		{"empty", nil},
		{"no tenant", &tokensv1.AssignCredentialsRequest{SessionId: "s", PoolIds: []string{"claude-prod"}}},
		{"no session", &tokensv1.AssignCredentialsRequest{TenantId: "acme", PoolIds: []string{"claude-prod"}}},
		{"no pools", &tokensv1.AssignCredentialsRequest{TenantId: "acme", SessionId: "s"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := srv.AssignCredentials(context.Background(), c.req)
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("err = %v, want InvalidArgument", err)
			}
		})
	}
}

// spec: 4.3
// diagnosis: a request that names an unknown pool is rejected with
// NotFound so the gateway can distinguish a config gap from an
// internal error.
func TestGRPCAssignCredentialsUnknownPool(t *testing.T) {
	_, _, srv := proxyPool(t, "claude-prod")
	_, err := srv.AssignCredentials(context.Background(), &tokensv1.AssignCredentialsRequest{
		TenantId:  "acme",
		SessionId: "s_1",
		PoolIds:   []string{"missing"},
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("err = %v, want NotFound", err)
	}
}

// spec: 4.9
// diagnosis: a pool with no healthy credentials maps to ResourceExhausted
// so the gateway can decide whether to retry or fall back.
func TestGRPCAssignCredentialsPoolExhausted(t *testing.T) {
	leases := credleasestore.New()
	creds := credcache.New()
	svc := credassign.New(leases, creds)
	svc.RegisterPool(credassign.Pool{
		Name:         "claude-prod",
		Provider:     credential.ProviderAnthropicDirect,
		DeliveryMode: credential.DeliveryProxy,
		Strategy:     credential.StrategyLeastLoaded,
		ProxyURL:     "https://gateway-internal:8443/llm-proxy",
		ProxyDialect: "anthropic",
		Credentials: []credassign.PoolCredential{
			{ID: "key-1", APIKey: "sk-ant-real", Healthy: false},
		},
	})
	srv := NewGRPCServer(svc, leases)
	_, err := srv.AssignCredentials(context.Background(), &tokensv1.AssignCredentialsRequest{
		TenantId:  "acme",
		SessionId: "s_1",
		PoolIds:   []string{"claude-prod"},
	})
	if status.Code(err) != codes.ResourceExhausted {
		t.Errorf("err = %v, want ResourceExhausted", err)
	}
}

// spec: 4.7
// diagnosis: RotateCredentials releases the previous lease and mints a
// fresh lease from the same pool. The new lease carries a distinct
// lease id and lease token but the same source identity.
func TestGRPCRotateCredentialsReissuesFromSamePool(t *testing.T) {
	_, leases, srv := proxyPool(t, "claude-prod")
	resp, err := srv.AssignCredentials(context.Background(), &tokensv1.AssignCredentialsRequest{
		TenantId:  "acme",
		SessionId: "s_1",
		PoolIds:   []string{"claude-prod"},
	})
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	old := resp.Leases["anthropic_direct"]
	rotated, err := srv.RotateCredentials(context.Background(), &tokensv1.RotateCredentialsRequest{
		TenantId: "acme",
		LeaseId:  old.LeaseId,
	})
	if err != nil {
		t.Fatalf("RotateCredentials: %v", err)
	}
	if rotated.Lease.LeaseId == old.LeaseId {
		t.Errorf("rotated lease reused id %q, want a new lease", old.LeaseId)
	}
	if rotated.Lease.PoolId != old.PoolId || rotated.Lease.CredentialId != old.CredentialId {
		t.Errorf("rotated source = %s/%s, want %s/%s", rotated.Lease.PoolId,
			rotated.Lease.CredentialId, old.PoolId, old.CredentialId)
	}
	if _, found := leases.GetByID(old.LeaseId); found {
		t.Errorf("previous lease %q was not released on rotate", old.LeaseId)
	}
	if _, found := leases.GetByID(rotated.Lease.LeaseId); !found {
		t.Errorf("rotated lease %q is not in the lease store", rotated.Lease.LeaseId)
	}
}

// spec: §4.9 line 1413 — an invalid rotation_trigger is rejected so a
// typo cannot silently disable the §4.7 ceiling discipline. F-13.3.10.
func TestGRPCRotateCredentialsRejectsInvalidTrigger_F13310(t *testing.T) {
	_, leases, srv := proxyPool(t, "claude-prod")
	resp, err := srv.AssignCredentials(context.Background(), &tokensv1.AssignCredentialsRequest{
		TenantId: "acme", SessionId: "s_1", PoolIds: []string{"claude-prod"},
	})
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	old := resp.Leases["anthropic_direct"]
	_, err = srv.RotateCredentials(context.Background(), &tokensv1.RotateCredentialsRequest{
		TenantId: "acme", LeaseId: old.LeaseId, RotationTrigger: "not_a_trigger",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("err = %v, want InvalidArgument for an unknown trigger", err)
	}
	// The lease must not have been released by a rejected rotation.
	if _, found := leases.GetByID(old.LeaseId); !found {
		t.Errorf("lease %q was released despite a rejected rotation", old.LeaseId)
	}
}

// spec: §4.9 line 1413 / §13.3 line 597 — a valid trigger is accepted and
// recorded as the revocation reason; an empty trigger defaults to a fault
// trigger (fail-closed) and still rotates. F-13.3.10.
func TestGRPCRotateCredentialsRecordsTrigger_F13310(t *testing.T) {
	for _, tc := range []struct {
		name       string
		trigger    string
		wantReason string
	}{
		{"proactive", "proactive_renewal", "rotation:proactive_renewal"},
		{"fault", "fault_auth_expired", "rotation:fault_auth_expired"},
		{"empty defaults to fault", "", "rotation:fault_provider_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, srv := proxyPool(t, "claude-prod")
			auditor := &recordingAuditor{}
			srv.SetAuditor(auditor)
			resp, err := srv.AssignCredentials(context.Background(), &tokensv1.AssignCredentialsRequest{
				TenantId: "acme", SessionId: "s_1", PoolIds: []string{"claude-prod"},
			})
			if err != nil {
				t.Fatalf("Assign: %v", err)
			}
			old := resp.Leases["anthropic_direct"]
			if _, err := srv.RotateCredentials(context.Background(), &tokensv1.RotateCredentialsRequest{
				TenantId: "acme", LeaseId: old.LeaseId, RotationTrigger: tc.trigger,
			}); err != nil {
				t.Fatalf("RotateCredentials: %v", err)
			}
			rows := auditor.snapshot()
			if len(rows) != 1 {
				t.Fatalf("audit rows = %d, want 1 token.revoked row", len(rows))
			}
			if !strings.Contains(string(rows[0].Payload), tc.wantReason) {
				t.Errorf("token.revoked payload = %s, want reason %q", rows[0].Payload, tc.wantReason)
			}
		})
	}
}

// spec: 4.3
// diagnosis: rotating an unknown lease is NotFound.
func TestGRPCRotateCredentialsUnknownLease(t *testing.T) {
	_, _, srv := proxyPool(t, "claude-prod")
	_, err := srv.RotateCredentials(context.Background(), &tokensv1.RotateCredentialsRequest{
		TenantId: "acme",
		LeaseId:  "cl-missing",
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("err = %v, want NotFound", err)
	}
}

// spec: 4.9
// diagnosis: RevokeCredentials releases the lease and the bearer token
// no longer resolves on the lease store.
func TestGRPCRevokeCredentials(t *testing.T) {
	_, leases, srv := proxyPool(t, "claude-prod")
	resp, err := srv.AssignCredentials(context.Background(), &tokensv1.AssignCredentialsRequest{
		TenantId:  "acme",
		SessionId: "s_1",
		PoolIds:   []string{"claude-prod"},
	})
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	got := resp.Leases["anthropic_direct"]
	if _, found := leases.GetByID(got.LeaseId); !found {
		t.Fatalf("lease missing before revoke")
	}
	if _, err := srv.RevokeCredentials(context.Background(), &tokensv1.RevokeCredentialsRequest{
		TenantId: "acme",
		LeaseId:  got.LeaseId,
		Reason:   "user_revoked",
	}); err != nil {
		t.Fatalf("RevokeCredentials: %v", err)
	}
	if _, found := leases.GetByID(got.LeaseId); found {
		t.Errorf("lease %q still in store after revoke", got.LeaseId)
	}
}

// spec: 4.3
// diagnosis: revoking an unknown lease is NotFound so a double-revoke
// (or a stale lease id) does not silently succeed.
func TestGRPCRevokeCredentialsUnknownLease(t *testing.T) {
	_, _, srv := proxyPool(t, "claude-prod")
	_, err := srv.RevokeCredentials(context.Background(), &tokensv1.RevokeCredentialsRequest{
		TenantId: "acme",
		LeaseId:  "cl-missing",
		Reason:   "test",
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("err = %v, want NotFound", err)
	}
}

// directPool wires a §4.9 direct-mode pool with one credential carrying
// the provided materializedConfig.
func directPool(t *testing.T, name string, p credential.Provider, mc credential.MaterializedConfig) (*credleasestore.Store, *GRPCServer) {
	t.Helper()
	leases := credleasestore.New()
	creds := credcache.New()
	svc := credassign.New(leases, creds)
	svc.RegisterPool(credassign.Pool{
		Name:         name,
		Provider:     p,
		DeliveryMode: credential.DeliveryDirect,
		Strategy:     credential.StrategyLeastLoaded,
		Credentials: []credassign.PoolCredential{
			{ID: "key-1", Healthy: true, Materialized: mc},
		},
	})
	return leases, NewGRPCServer(svc, leases)
}

// spec: §4.9 lines 1246-1298
// diagnosis: a direct-mode lease carries its per-provider
// materializedConfig on the wire (and no proxy lease token) so the
// gateway can forward it to the pod through the adapter credential file.
func TestGRPCAssignCredentialsDirectModeCarriesMaterializedConfig(t *testing.T) {
	_, srv := directPool(t, "bedrock-prod", credential.ProviderAWSBedrock, credential.MaterializedConfig{
		"accessKeyId": "AKIA", "secretAccessKey": "shh", "sessionToken": "tok",
		"region": "us-east-1", "expiresAt": "2099-01-01T00:00:00Z",
	})
	resp, err := srv.AssignCredentials(context.Background(), &tokensv1.AssignCredentialsRequest{
		TenantId:  "acme",
		SessionId: "s_1",
		PoolIds:   []string{"bedrock-prod"},
	})
	if err != nil {
		t.Fatalf("AssignCredentials: %v", err)
	}
	got, ok := resp.Leases["aws_bedrock"]
	if !ok {
		t.Fatalf("response has no aws_bedrock lease: %+v", resp.Leases)
	}
	if got.DeliveryMode != string(credential.DeliveryDirect) {
		t.Errorf("deliveryMode = %q, want direct", got.DeliveryMode)
	}
	if got.LeaseToken != "" {
		t.Errorf("direct-mode lease carries a proxy lease token: %q", got.LeaseToken)
	}
	if got.MaterializedConfig["accessKeyId"] != "AKIA" || got.MaterializedConfig["region"] != "us-east-1" {
		t.Errorf("materialized_config = %+v, want the STS bundle", got.MaterializedConfig)
	}
}

// spec: §4.9 line 1298
// diagnosis: a direct-mode pool whose materializedConfig is missing a
// Required:yes field fails issuance with CREDENTIAL_MATERIALIZATION_ERROR
// (category INTERNAL), which the Token Service surfaces as
// ResourceExhausted so the gateway maps it to CREDENTIAL_POOL_EXHAUSTED.
func TestGRPCAssignCredentialsMaterializationFailureMapsToResourceExhausted(t *testing.T) {
	_, srv := directPool(t, "bedrock-broken", credential.ProviderAWSBedrock, credential.MaterializedConfig{
		"region": "us-east-1", // missing the STS triple + expiresAt
	})
	_, err := srv.AssignCredentials(context.Background(), &tokensv1.AssignCredentialsRequest{
		TenantId:  "acme",
		SessionId: "s_1",
		PoolIds:   []string{"bedrock-broken"},
	})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("err = %v, want ResourceExhausted", err)
	}
	if !strings.Contains(err.Error(), credential.CodeCredentialMaterializationError) {
		t.Errorf("err = %v, want it to name %s", err, credential.CodeCredentialMaterializationError)
	}
}
