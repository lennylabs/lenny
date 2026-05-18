//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §4.9 / §10.3 / §13.3 security-cache pub/sub
// substrate, exercising pkg/gateway/pubsub and the three propagators
// against a real Redis container. The gateway runs as multiple
// replicas; each security cache is a per-replica in-memory set that
// must converge across replicas over Redis pub/sub. Each subtest
// stands up two propagator instances over one Redis (two stand-in
// replicas), mutates one, and asserts the other's local cache observes
// the change.
package securitycaches_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	credentialdenylist "github.com/lennylabs/lenny/pkg/gateway/denylist"
	credentialdenylistprop "github.com/lennylabs/lenny/pkg/gateway/denylist/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/pubsub"
	"github.com/lennylabs/lenny/pkg/gateway/revocation"
	revocationprop "github.com/lennylabs/lenny/pkg/gateway/revocation/propagator"
	mtlsdenylist "github.com/lennylabs/lenny/pkg/mtls/denylist"
	mtlsdenylistprop "github.com/lennylabs/lenny/pkg/mtls/denylist/propagator"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// waitFor polls cond until it holds or the timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for: %s", timeout, desc)
}

// spec: 13.3
// diagnosis: the §13.3 token-revocation cache in
// pkg/gateway/revocation is a per-replica in-memory jti set and
// Revoke is local-only. A token revoked on one gateway replica must
// reach every replica's cache; pkg/gateway/revocation/propagator must
// fan a Revoke out over Redis pub/sub and apply a peer's revocation
// onto the local cache.
func TestTokenRevocationPropagation(t *testing.T) {
	t.Parallel()
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// Two revocation propagators over the same Redis stand in for two
	// gateway replicas: a revocation on one must reach the other.
	cacheA := revocation.NewCache()
	cacheB := revocation.NewCache()
	busA := pubsub.New(rd.Client)
	busB := pubsub.New(rd.Client)
	propA := revocationprop.New(cacheA, busA)
	propB := revocationprop.New(cacheB, busB)
	go propA.Run(ctx)
	go propB.Run(ctx)
	// Let both subscribe loops attach before publishing; a pub/sub
	// message published before SUBSCRIBE completes is dropped.
	time.Sleep(100 * time.Millisecond)

	t.Run("a revocation on replica A reaches replica B", func(t *testing.T) {
		propA.Revoke("jti-a-to-b")
		waitFor(t, 5*time.Second, "replica B to see the revoked jti", func() bool {
			return cacheB.IsRevoked("jti-a-to-b")
		})
	})

	t.Run("a revocation on replica B reaches replica A", func(t *testing.T) {
		propB.Revoke("jti-b-to-a")
		waitFor(t, 5*time.Second, "replica A to see the revoked jti", func() bool {
			return cacheA.IsRevoked("jti-b-to-a")
		})
	})
}

// spec: 10.3
// diagnosis: the §10.3 mTLS certificate deny list in
// pkg/mtls/denylist is a per-replica SPIFFE-URI set; its package doc
// defers cross-replica fan-out to a wrapping controller. A certificate
// revoked on one replica must be rejected on every replica.
// pkg/mtls/denylist/propagator must publish an Add or Remove over
// Redis pub/sub and replay a peer's mutation onto the local deny list.
func TestMTLSDenylistPropagation(t *testing.T) {
	t.Parallel()
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	denyA := mtlsdenylist.New()
	denyB := mtlsdenylist.New()
	propA := mtlsdenylistprop.New(denyA, pubsub.New(rd.Client))
	propB := mtlsdenylistprop.New(denyB, pubsub.New(rd.Client))
	go propA.Run(ctx)
	go propB.Run(ctx)
	time.Sleep(100 * time.Millisecond)

	expiry := time.Now().Add(time.Hour)

	t.Run("an add on replica A reaches replica B", func(t *testing.T) {
		uri := "spiffe://acme.com/agent/pod-add"
		propA.Add(uri, expiry)
		waitFor(t, 5*time.Second, "replica B to deny the revoked certificate", func() bool {
			return denyB.Contains(uri)
		})
	})

	t.Run("a remove on replica A reaches replica B", func(t *testing.T) {
		uri := "spiffe://acme.com/agent/pod-remove"
		propA.Add(uri, expiry)
		waitFor(t, 5*time.Second, "replica B to deny the certificate", func() bool {
			return denyB.Contains(uri)
		})
		propA.Remove(uri)
		waitFor(t, 5*time.Second, "replica B to drop the rescinded certificate", func() bool {
			return !denyB.Contains(uri)
		})
	})
}

// spec: 4.9
// diagnosis: the §4.9 credential deny list in pkg/gateway/denylist is
// a per-replica set of revoked credential identities the LLM reverse
// proxy checks before every upstream call; Revoke is local-only. A
// credential revoked on one replica must be rejected on every replica.
// pkg/gateway/denylist/propagator must fan a Revoke out over Redis
// pub/sub and apply a peer's revocation onto the local deny list.
func TestCredentialDenylistPropagation(t *testing.T) {
	t.Parallel()
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	denyA := credentialdenylist.New()
	denyB := credentialdenylist.New()
	propA := credentialdenylistprop.New(denyA, pubsub.New(rd.Client))
	propB := credentialdenylistprop.New(denyB, pubsub.New(rd.Client))
	go propA.Run(ctx)
	go propB.Run(ctx)
	time.Sleep(100 * time.Millisecond)

	t.Run("a pool-backed revocation on replica A reaches replica B", func(t *testing.T) {
		key := credential.CredentialKey{
			Source: credential.SourcePool, PoolID: "pool-1", CredentialID: "cred-1",
		}
		propA.Revoke(key)
		waitFor(t, 5*time.Second, "replica B to reject the revoked credential", func() bool {
			return denyB.Revoked(key)
		})
	})

	t.Run("a user-backed revocation on replica B reaches replica A", func(t *testing.T) {
		key := credential.CredentialKey{
			Source: credential.SourceUser, TenantID: "acme", CredentialRef: "anthropic-key",
		}
		propB.Revoke(key)
		waitFor(t, 5*time.Second, "replica A to reject the revoked credential", func() bool {
			return denyA.Revoked(key)
		})
	})
}
