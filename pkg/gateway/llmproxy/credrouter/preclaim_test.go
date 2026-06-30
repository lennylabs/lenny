// SPDX-License-Identifier: MIT

package credrouter

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
)

// spec: §4.9 lines 1216-1218 — the pre-claim check passes when at least
// one provider in the intersection has an assignable credential.
func TestPreClaimAvailableOneProvider(t *testing.T) {
	res, err := PreClaim(context.Background(), NewDefault(), PreClaimInput{
		PreferredSource: credential.PreferredSourcePool,
		Providers: []ProviderInput{
			{Provider: "anthropic_direct", AllowedPools: []PoolDescriptor{{PoolID: "p", Healthy: true, HasCapacity: false}}}, // exhausted
			{Provider: "aws_bedrock", AllowedPools: []PoolDescriptor{assignable("bedrock")}},                                 // assignable
		},
	})
	if err != nil {
		t.Fatalf("PreClaim: %v", err)
	}
	if !res.Available() {
		t.Error("expected Available")
	}
	if res.PoolAssignments["aws_bedrock"] != "bedrock" {
		t.Errorf("PoolAssignments = %v, want aws_bedrock→bedrock", res.PoolAssignments)
	}
	if _, ok := res.PoolAssignments["anthropic_direct"]; ok {
		t.Error("exhausted provider should not be in PoolAssignments")
	}
}

// spec: §4.9 line 1326 — an empty intersection rejects with
// CREDENTIAL_POOL_EXHAUSTED.
func TestPreClaimEmptyIntersection(t *testing.T) {
	res, err := PreClaim(context.Background(), NewDefault(), PreClaimInput{
		PreferredSource: credential.PreferredSourcePool,
	})
	if !errors.Is(err, ErrNoCredentialAvailable) {
		t.Errorf("got %v, want ErrNoCredentialAvailable", err)
	}
	if res.Available() {
		t.Error("empty intersection must not be Available")
	}
}

// spec: §4.9 line 1218 — every provider exhausted rejects with
// CREDENTIAL_POOL_EXHAUSTED.
func TestPreClaimAllExhausted(t *testing.T) {
	_, err := PreClaim(context.Background(), NewDefault(), PreClaimInput{
		PreferredSource: credential.PreferredSourcePool,
		Providers: []ProviderInput{
			{Provider: "anthropic_direct", AllowedPools: []PoolDescriptor{{PoolID: "p", Healthy: true, HasCapacity: false}}},
			{Provider: "aws_bedrock", AllowedPools: nil},
		},
	})
	if !errors.Is(err, ErrNoCredentialAvailable) {
		t.Errorf("got %v, want ErrNoCredentialAvailable", err)
	}
}

// spec: §4.9 lines 1364, 1370 — when a user-only policy resolves no
// credential for any provider, the failure is USER_CREDENTIAL_NOT_FOUND
// rather than pool exhaustion.
func TestPreClaimUserOnlyMiss(t *testing.T) {
	_, err := PreClaim(context.Background(), NewDefault(), PreClaimInput{
		PreferredSource: credential.PreferredSourceUser,
		Providers: []ProviderInput{
			{Provider: "anthropic_direct", UserCredentialAvailable: false},
		},
	})
	if !errors.Is(err, ErrUserCredentialNotFound) {
		t.Errorf("got %v, want ErrUserCredentialNotFound", err)
	}
}

// spec: §4.9 line 1326 — a user-source resolution counts as available
// even when no pool is assignable.
func TestPreClaimUserResolved(t *testing.T) {
	res, err := PreClaim(context.Background(), NewDefault(), PreClaimInput{
		PreferredSource: credential.PreferredSourceUser,
		Providers: []ProviderInput{
			{Provider: "anthropic_direct", UserCredentialAvailable: true},
		},
	})
	if err != nil {
		t.Fatalf("PreClaim: %v", err)
	}
	if !res.Available() || len(res.UserProviders) != 1 || res.UserProviders[0] != "anthropic_direct" {
		t.Errorf("expected user-resolved availability, got %+v", res)
	}
}

// A mixed failure where one provider is a user-only miss and another is
// pool-exhausted resolves to USER_CREDENTIAL_NOT_FOUND only when every
// provider failed and a user miss occurred. With a user-only policy
// both providers miss on the user source, so the result is the user
// error.
func TestPreClaimMixedFailuresUserOnly(t *testing.T) {
	_, err := PreClaim(context.Background(), NewDefault(), PreClaimInput{
		PreferredSource: credential.PreferredSourceUser,
		Providers: []ProviderInput{
			{Provider: "anthropic_direct", UserCredentialAvailable: false},
			{Provider: "aws_bedrock", UserCredentialAvailable: false, AllowedPools: []PoolDescriptor{assignable("ignored")}},
		},
	})
	if !errors.Is(err, ErrUserCredentialNotFound) {
		t.Errorf("got %v, want ErrUserCredentialNotFound", err)
	}
}
