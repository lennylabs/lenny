// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// preclaimFixture builds a Server wired with a tenant credentialPolicy,
// a runtime with supportedProviders, and a set of credential pools, so
// resolveCredentialPools exercises the §4.9 intersection + pre-claim
// path. providers is the runtime's supportedProviders.
func preclaimFixture(t *testing.T, policy credential.CredentialPolicy, providers []string, pools ...credentialpoolstore.CredentialPool) *Server {
	t.Helper()
	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme", CredentialPolicy: policy}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(ctx, runtimestore.Runtime{Name: "claude-code", SupportedProviders: providers}); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	credPools := credentialpoolstore.NewMemory()
	for _, p := range pools {
		if err := credPools.Create(ctx, p); err != nil {
			t.Fatalf("create pool %s: %v", p.Name, err)
		}
	}
	return &Server{
		tenants:    tenants,
		runtimes:   runtimes,
		credPools:  credPools,
		credRouter: credrouter.NewDefault(),
	}
}

func poolFixture(name, provider string, credStatuses ...credentialpoolstore.CredentialStatus) credentialpoolstore.CredentialPool {
	p := credentialpoolstore.CredentialPool{
		TenantID:              "acme",
		Name:                  name,
		Provider:              provider,
		MaxConcurrentSessions: 10,
	}
	for i, st := range credStatuses {
		p.Credentials = append(p.Credentials, credentialpoolstore.Credential{
			ID:        name + "-cred-" + string(rune('a'+i)),
			SecretRef: "secret-" + name,
			Status:    st,
		})
	}
	return p
}

func sessionRow() sessionstore.Session {
	return sessionstore.Session{ID: "s1", TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: "claude-code"}
}

// spec: §4.9 lines 1326 — the resolved provider→pool map is the
// intersection of supportedProviders and the policy's providerPools,
// each routed to its defaultPool.
func TestResolveCredentialPoolsPoolSource(t *testing.T) {
	policy := credential.CredentialPolicy{
		PreferredSource: credential.PreferredSourcePool,
		ProviderPools: map[string]credential.ProviderPool{
			"anthropic_direct": {DefaultPool: "claude-prod"},
			"aws_bedrock":      {DefaultPool: "bedrock-prod"},
		},
	}
	s := preclaimFixture(
		t, policy, []string{"anthropic_direct", "aws_bedrock"},
		poolFixture("claude-prod", "anthropic_direct", credentialpoolstore.CredentialActive),
		poolFixture("bedrock-prod", "aws_bedrock", credentialpoolstore.CredentialActive),
	)
	got, _, err := s.resolveCredentialPools(context.Background(), sessionRow())
	if err != nil {
		t.Fatalf("resolveCredentialPools: %v", err)
	}
	if got["anthropic_direct"] != "claude-prod" || got["aws_bedrock"] != "bedrock-prod" {
		t.Errorf("CredentialPools = %v, want both providers routed to their pools", got)
	}
}

// spec: §4.9 line 1326 — only providers in both the runtime's
// supportedProviders and the policy's providerPools are assigned.
func TestResolveCredentialPoolsIntersectionNarrows(t *testing.T) {
	policy := credential.CredentialPolicy{
		ProviderPools: map[string]credential.ProviderPool{
			"anthropic_direct": {DefaultPool: "claude-prod"},
			"vertex_ai":        {DefaultPool: "vertex-prod"}, // not supported by runtime
		},
	}
	s := preclaimFixture(
		t, policy, []string{"anthropic_direct"}, // runtime supports only one
		poolFixture("claude-prod", "anthropic_direct", credentialpoolstore.CredentialActive),
		poolFixture("vertex-prod", "vertex_ai", credentialpoolstore.CredentialActive),
	)
	got, _, err := s.resolveCredentialPools(context.Background(), sessionRow())
	if err != nil {
		t.Fatalf("resolveCredentialPools: %v", err)
	}
	if len(got) != 1 || got["anthropic_direct"] != "claude-prod" {
		t.Errorf("CredentialPools = %v, want only anthropic_direct→claude-prod", got)
	}
}

// spec: §4.9 lines 1314-1319 — the fallback chain skips a pool whose
// credentials are all revoked and routes to the next pool in order.
func TestResolveCredentialPoolsFallbackOrder(t *testing.T) {
	policy := credential.CredentialPolicy{
		ProviderPools: map[string]credential.ProviderPool{
			"anthropic_direct": {Fallback: credential.ProviderFallback{Order: []string{"primary", "backup"}}},
		},
	}
	s := preclaimFixture(
		t, policy, []string{"anthropic_direct"},
		poolFixture("primary", "anthropic_direct", credentialpoolstore.CredentialRevoked), // all revoked
		poolFixture("backup", "anthropic_direct", credentialpoolstore.CredentialActive),
	)
	got, _, err := s.resolveCredentialPools(context.Background(), sessionRow())
	if err != nil {
		t.Fatalf("resolveCredentialPools: %v", err)
	}
	if got["anthropic_direct"] != "backup" {
		t.Errorf("got pool %q, want backup (primary all-revoked)", got["anthropic_direct"])
	}
}

// spec: §4.9 line 1218 — every pool exhausted (all credentials revoked)
// rejects with ErrNoCredentialAvailable before a pod is claimed.
func TestResolveCredentialPoolsExhausted(t *testing.T) {
	policy := credential.CredentialPolicy{
		ProviderPools: map[string]credential.ProviderPool{
			"anthropic_direct": {DefaultPool: "primary"},
		},
	}
	s := preclaimFixture(
		t, policy, []string{"anthropic_direct"},
		poolFixture("primary", "anthropic_direct", credentialpoolstore.CredentialRevoked),
	)
	_, _, err := s.resolveCredentialPools(context.Background(), sessionRow())
	if !errors.Is(err, credrouter.ErrNoCredentialAvailable) {
		t.Errorf("got %v, want ErrNoCredentialAvailable", err)
	}
}

// spec: §4.9 lines 1364, 1370 — a user-only policy with no user-credential
// checker (v1: user-source delivery deferred) rejects with
// ErrUserCredentialNotFound.
func TestResolveCredentialPoolsUserOnlyMiss(t *testing.T) {
	policy := credential.CredentialPolicy{
		PreferredSource:        credential.PreferredSourceUser,
		UserCredentialsEnabled: true,
		ProviderPools: map[string]credential.ProviderPool{
			"anthropic_direct": {DefaultPool: "primary"},
		},
	}
	s := preclaimFixture(
		t, policy, []string{"anthropic_direct"},
		poolFixture("primary", "anthropic_direct", credentialpoolstore.CredentialActive),
	)
	_, _, err := s.resolveCredentialPools(context.Background(), sessionRow())
	if !errors.Is(err, credrouter.ErrUserCredentialNotFound) {
		t.Errorf("got %v, want ErrUserCredentialNotFound", err)
	}
}

// An unconfigured tenant policy assigns no upstream credentials — the
// pre-§4.9 behavior is preserved for deployments without pools.
func TestResolveCredentialPoolsUnconfiguredPolicy(t *testing.T) {
	s := preclaimFixture(t, credential.CredentialPolicy{}, []string{"anthropic_direct"})
	got, _, err := s.resolveCredentialPools(context.Background(), sessionRow())
	if err != nil {
		t.Fatalf("resolveCredentialPools: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("CredentialPools = %v, want empty for unconfigured policy", got)
	}
}

// With no registries wired the §4.9 layer is inert.
func TestResolveCredentialPoolsNoRegistries(t *testing.T) {
	s := &Server{credRouter: credrouter.NewDefault()}
	got, _, err := s.resolveCredentialPools(context.Background(), sessionRow())
	if err != nil || got != nil {
		t.Errorf("got (%v, %v), want (nil, nil) with no registries", got, err)
	}
}

// spec: §4.9 line 1218, §15.1 line 990 — the pre-claim exhaustion
// envelope is CREDENTIAL_POOL_EXHAUSTED, HTTP 503, category POLICY,
// non-retryable bit aside; the reason distinguishes pre-claim.
func TestWriteCredentialPoolExhaustedShape(t *testing.T) {
	s := &Server{}
	rr := httptest.NewRecorder()
	s.writePodClaimError(rr, credrouter.ErrNoCredentialAvailable, "SESSION_CREATION_FAILED", "claim failed")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	var env struct {
		Error struct {
			Code     string         `json:"code"`
			Category string         `json:"category"`
			Details  map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "CREDENTIAL_POOL_EXHAUSTED" {
		t.Errorf("code = %q, want CREDENTIAL_POOL_EXHAUSTED", env.Error.Code)
	}
	if env.Error.Category != "POLICY" {
		t.Errorf("category = %q, want POLICY", env.Error.Category)
	}
	if env.Error.Details["reason"] != "pre_claim" {
		t.Errorf("reason = %v, want pre_claim", env.Error.Details["reason"])
	}
}

// spec: §4.9 lines 1364, §15.1 line 993 — the user-only miss envelope is
// USER_CREDENTIAL_NOT_FOUND, HTTP 404, category PERMANENT.
func TestWriteUserCredentialNotFoundShape(t *testing.T) {
	s := &Server{}
	rr := httptest.NewRecorder()
	s.writePodClaimError(rr, credrouter.ErrUserCredentialNotFound, "SESSION_CREATION_FAILED", "claim failed")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	var env struct {
		Error struct {
			Code     string `json:"code"`
			Category string `json:"category"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "USER_CREDENTIAL_NOT_FOUND" {
		t.Errorf("code = %q, want USER_CREDENTIAL_NOT_FOUND", env.Error.Code)
	}
	if env.Error.Category != "PERMANENT" {
		t.Errorf("category = %q, want PERMANENT", env.Error.Category)
	}
}

// spec: §4.9 line 1220 — when the pre-claim check passed but the lease
// assignment raced and failed, the gateway emits the mismatch metric
// (labeled by the failing pool and provider) and returns
// CREDENTIAL_POOL_EXHAUSTED with reason assignment_race.
func TestWritePodClaimErrorAssignmentRaceEmitsMetric(t *testing.T) {
	var gotPool, gotProvider string
	s := &Server{preclaimMismatch: func(pool, provider string) { gotPool, gotProvider = pool, provider }}
	rr := httptest.NewRecorder()
	raceErr := &podsession.CredentialAssignmentError{
		Provider: "anthropic_direct",
		Pool:     "claude-prod",
		Err:      credential.ErrPoolExhausted,
	}
	s.writePodClaimError(rr, raceErr, "SESSION_CREATION_FAILED", "claim failed")
	if gotPool != "claude-prod" || gotProvider != "anthropic_direct" {
		t.Errorf("mismatch metric labels = (%q,%q), want (claude-prod, anthropic_direct)", gotPool, gotProvider)
	}
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &env)
	if env.Error.Code != "CREDENTIAL_POOL_EXHAUSTED" || env.Error.Details["reason"] != "assignment_race" {
		t.Errorf("got code=%q reason=%v, want CREDENTIAL_POOL_EXHAUSTED/assignment_race", env.Error.Code, env.Error.Details["reason"])
	}
}
