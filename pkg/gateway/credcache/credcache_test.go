// SPDX-License-Identifier: MIT

package credcache_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy"
)

// spec: §4.9 — the in-memory upstream-credential cache the LLM proxy
// reads on every upstream call.

// Cache satisfies the §4.9 LLM proxy's CredentialResolver.
var _ llmproxy.CredentialResolver = (*credcache.Cache)(nil)

// poolLease returns a pool-backed lease for the given pool credential.
func poolLease(poolID, credID string) credential.Lease {
	return credential.Lease{
		LeaseID:      "cl_1",
		SessionID:    "s_1",
		Provider:     credential.ProviderAnthropicDirect,
		Source:       credential.SourcePool,
		PoolID:       poolID,
		CredentialID: credID,
		DeliveryMode: credential.DeliveryProxy,
		ExpiresAt:    time.Now().Add(time.Hour),
	}
}

// userLease returns a user-backed lease for the given user credential.
func userLease(tenantID, credRef string) credential.Lease {
	return credential.Lease{
		LeaseID:       "cl_2",
		SessionID:     "s_2",
		Provider:      credential.ProviderAnthropicDirect,
		Source:        credential.SourceUser,
		TenantID:      tenantID,
		CredentialRef: credRef,
		DeliveryMode:  credential.DeliveryProxy,
		ExpiresAt:     time.Now().Add(time.Hour),
	}
}

func TestPutAndResolvePoolCredential(t *testing.T) {
	c := credcache.New()
	lease := poolLease("claude-prod", "key-1")
	c.Put(lease.CredentialKey(), "sk-ant-real")

	got, ok := c.UpstreamCredential(lease)
	if !ok {
		t.Fatal("UpstreamCredential did not resolve a cached pool credential")
	}
	if got != "sk-ant-real" {
		t.Errorf("credential = %q, want sk-ant-real", got)
	}
}

func TestPutAndResolveUserCredential(t *testing.T) {
	c := credcache.New()
	lease := userLease("acme", "cred-xyz")
	c.Put(lease.CredentialKey(), "sk-user-real")

	got, ok := c.UpstreamCredential(lease)
	if !ok || got != "sk-user-real" {
		t.Errorf("UpstreamCredential = %q ok=%v, want sk-user-real", got, ok)
	}
}

func TestResolveMiss(t *testing.T) {
	c := credcache.New()
	if _, ok := c.UpstreamCredential(poolLease("claude-prod", "key-1")); ok {
		t.Error("UpstreamCredential resolved a credential the cache never held")
	}
}

func TestRotationOverwritesInPlace(t *testing.T) {
	c := credcache.New()
	lease := poolLease("claude-prod", "key-1")
	c.Put(lease.CredentialKey(), "sk-ant-old")
	c.Put(lease.CredentialKey(), "sk-ant-rotated")

	got, ok := c.UpstreamCredential(lease)
	if !ok || got != "sk-ant-rotated" {
		t.Errorf("after rotation credential = %q ok=%v, want sk-ant-rotated", got, ok)
	}
	if c.Len() != 1 {
		t.Errorf("cache holds %d credentials after a rotation, want 1", c.Len())
	}
}

func TestDistinctCredentialsAreIndependent(t *testing.T) {
	c := credcache.New()
	c.Put(poolLease("claude-prod", "key-1").CredentialKey(), "sk-one")
	c.Put(poolLease("claude-prod", "key-2").CredentialKey(), "sk-two")
	c.Put(userLease("acme", "cred-1").CredentialKey(), "sk-three")

	if got, _ := c.UpstreamCredential(poolLease("claude-prod", "key-1")); got != "sk-one" {
		t.Errorf("key-1 = %q, want sk-one", got)
	}
	if got, _ := c.UpstreamCredential(poolLease("claude-prod", "key-2")); got != "sk-two" {
		t.Errorf("key-2 = %q, want sk-two", got)
	}
	if got, _ := c.UpstreamCredential(userLease("acme", "cred-1")); got != "sk-three" {
		t.Errorf("user cred-1 = %q, want sk-three", got)
	}
	if c.Len() != 3 {
		t.Errorf("cache holds %d credentials, want 3", c.Len())
	}
}

func TestPoolAndUserKeyspacesDoNotCollide(t *testing.T) {
	// A pool credential and a user credential that share string values
	// must not alias: Source discriminates the keyspaces.
	c := credcache.New()
	c.Put(poolLease("shared", "shared").CredentialKey(), "sk-pool")
	c.Put(userLease("shared", "shared").CredentialKey(), "sk-user")

	if got, _ := c.UpstreamCredential(poolLease("shared", "shared")); got != "sk-pool" {
		t.Errorf("pool lookup = %q, want sk-pool", got)
	}
	if got, _ := c.UpstreamCredential(userLease("shared", "shared")); got != "sk-user" {
		t.Errorf("user lookup = %q, want sk-user", got)
	}
}

func TestRemove(t *testing.T) {
	c := credcache.New()
	lease := poolLease("claude-prod", "key-1")
	c.Put(lease.CredentialKey(), "sk-ant-real")
	c.Remove(lease.CredentialKey())

	if _, ok := c.UpstreamCredential(lease); ok {
		t.Error("UpstreamCredential resolved a removed credential")
	}
	if c.Len() != 0 {
		t.Errorf("cache holds %d credentials after Remove, want 0", c.Len())
	}
}

func TestRemoveMissingIsNoOp(t *testing.T) {
	c := credcache.New()
	c.Remove(poolLease("absent", "absent").CredentialKey()) // must not panic
	if c.Len() != 0 {
		t.Errorf("cache holds %d credentials, want 0", c.Len())
	}
}
