// SPDX-License-Identifier: MIT

package credrouter

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
)

func assignable(id string) PoolDescriptor {
	return PoolDescriptor{PoolID: id, Healthy: true, HasCapacity: true}
}

// spec: §4.9 line 1218 — a pool is assignable only when healthy, with
// capacity, and not cooling down.
func TestPoolDescriptorAssignable(t *testing.T) {
	if !assignable("p").Assignable() {
		t.Error("healthy+capacity pool should be assignable")
	}
	cases := []PoolDescriptor{
		{PoolID: "p", Healthy: false, HasCapacity: true},
		{PoolID: "p", Healthy: true, HasCapacity: false},
		{PoolID: "p", Healthy: true, HasCapacity: true, CoolingDown: true},
	}
	for i, d := range cases {
		if d.Assignable() {
			t.Errorf("case %d: %+v should not be assignable", i, d)
		}
	}
}

// spec: §4.9 lines 1328-1336 — pool source resolves to the first
// assignable pool in fallback order.
func TestResolvePoolSource(t *testing.T) {
	r := NewDefault()
	out, err := r.Resolve(context.Background(), Input{
		Provider:        "anthropic_direct",
		PreferredSource: credential.PreferredSourcePool,
		AllowedPools:    []PoolDescriptor{assignable("primary"), assignable("backup")},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if out.Source != credential.SourcePool || out.PoolID != "primary" {
		t.Errorf("got %+v, want pool/primary", out)
	}
}

// spec: §4.9 lines 1314-1319 — fallback chain skips a cooling/exhausted
// primary and selects the next assignable pool in order.
func TestResolvePoolFallbackOrder(t *testing.T) {
	r := NewDefault()
	out, err := r.Resolve(context.Background(), Input{
		Provider:        "anthropic_direct",
		PreferredSource: credential.PreferredSourcePool,
		AllowedPools: []PoolDescriptor{
			{PoolID: "primary", Healthy: true, HasCapacity: true, CoolingDown: true},
			{PoolID: "exhausted", Healthy: true, HasCapacity: false},
			assignable("backup"),
		},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if out.PoolID != "backup" {
		t.Errorf("got pool %q, want backup (primary cooling, second exhausted)", out.PoolID)
	}
}

// spec: §4.9 line 1218 — no assignable pool returns
// ErrNoCredentialAvailable (CREDENTIAL_POOL_EXHAUSTED).
func TestResolvePoolExhausted(t *testing.T) {
	r := NewDefault()
	_, err := r.Resolve(context.Background(), Input{
		Provider:        "anthropic_direct",
		PreferredSource: credential.PreferredSourcePool,
		AllowedPools:    []PoolDescriptor{{PoolID: "primary", Healthy: true, HasCapacity: false}},
	})
	if !errors.Is(err, ErrNoCredentialAvailable) {
		t.Errorf("got %v, want ErrNoCredentialAvailable", err)
	}
}

// spec: §4.9 line 1336 — user-only mode resolves to the user source
// when a user credential is available.
func TestResolveUserSource(t *testing.T) {
	r := NewDefault()
	out, err := r.Resolve(context.Background(), Input{
		Provider:                "anthropic_direct",
		PreferredSource:         credential.PreferredSourceUser,
		UserCredentialAvailable: true,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if out.Source != credential.SourceUser || out.PoolID != "" {
		t.Errorf("got %+v, want user source with empty poolID", out)
	}
}

// spec: §4.9 lines 1364, 1370 — user-only mode with no user credential
// is terminal: ErrUserCredentialNotFound.
func TestResolveUserOnlyMissTerminal(t *testing.T) {
	r := NewDefault()
	_, err := r.Resolve(context.Background(), Input{
		Provider:                "anthropic_direct",
		PreferredSource:         credential.PreferredSourceUser,
		UserCredentialAvailable: false,
		AllowedPools:            []PoolDescriptor{assignable("p")}, // present but ignored in user-only mode
	})
	if !errors.Is(err, ErrUserCredentialNotFound) {
		t.Errorf("got %v, want ErrUserCredentialNotFound", err)
	}
}

// spec: §4.9 line 1362 — prefer-user-then-pool falls through to pool
// when no user credential is registered.
func TestResolvePreferUserThenPoolFallsThrough(t *testing.T) {
	r := NewDefault()
	out, err := r.Resolve(context.Background(), Input{
		Provider:                "anthropic_direct",
		PreferredSource:         credential.PreferUserThenPool,
		UserCredentialAvailable: false,
		AllowedPools:            []PoolDescriptor{assignable("pool1")},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if out.Source != credential.SourcePool || out.PoolID != "pool1" {
		t.Errorf("got %+v, want pool/pool1 fallthrough", out)
	}
}

// spec: §4.9 line 1336 — prefer-user-then-pool prefers the user source
// when it is available, even with pools present.
func TestResolvePreferUserThenPoolPrefersUser(t *testing.T) {
	r := NewDefault()
	out, err := r.Resolve(context.Background(), Input{
		PreferredSource:         credential.PreferUserThenPool,
		UserCredentialAvailable: true,
		AllowedPools:            []PoolDescriptor{assignable("pool1")},
	})
	if err != nil || out.Source != credential.SourceUser {
		t.Errorf("got %+v err=%v, want user source", out, err)
	}
}

// spec: §4.9 line 1336 — prefer-pool-then-user tries the pool first and
// only falls through to the user source when no pool is assignable.
func TestResolvePreferPoolThenUser(t *testing.T) {
	r := NewDefault()
	// Pool assignable → pool wins.
	out, _ := r.Resolve(context.Background(), Input{
		PreferredSource:         credential.PreferPoolThenUser,
		UserCredentialAvailable: true,
		AllowedPools:            []PoolDescriptor{assignable("pool1")},
	})
	if out.Source != credential.SourcePool {
		t.Errorf("pool assignable: got %+v, want pool", out)
	}
	// No assignable pool, user available → user fallthrough.
	out, err := r.Resolve(context.Background(), Input{
		PreferredSource:         credential.PreferPoolThenUser,
		UserCredentialAvailable: true,
		AllowedPools:            []PoolDescriptor{{PoolID: "p", Healthy: true, HasCapacity: false}},
	})
	if err != nil || out.Source != credential.SourceUser {
		t.Errorf("pool exhausted: got %+v err=%v, want user", out, err)
	}
}

// spec: §4.9 line 1326 — the intersection is providers in both
// supportedProviders and the policy's providerPools keys, sorted.
func TestIntersection(t *testing.T) {
	policy := credential.CredentialPolicy{ProviderPools: map[string]credential.ProviderPool{
		"anthropic_direct": {DefaultPool: "a"},
		"aws_bedrock":      {DefaultPool: "b"},
		"vertex_ai":        {DefaultPool: "v"},
	}}
	got := Intersection([]string{"vertex_ai", "anthropic_direct", "github"}, policy)
	want := []string{"anthropic_direct", "vertex_ai"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Intersection = %v, want %v", got, want)
	}
	// Empty intersection when no provider overlaps.
	if got := Intersection([]string{"github"}, policy); len(got) != 0 {
		t.Errorf("non-overlapping intersection = %v, want empty", got)
	}
	// Empty policy yields an empty intersection.
	if got := Intersection([]string{"anthropic_direct"}, credential.CredentialPolicy{}); len(got) != 0 {
		t.Errorf("empty-policy intersection = %v, want empty", got)
	}
}
