//go:build component

// SPDX-License-Identifier: MIT

package securitycaches_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/credential/revocation"
	revocationprop "github.com/lennylabs/lenny/pkg/credential/revocation/propagator"
	"github.com/lennylabs/lenny/pkg/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/storage/pubsub"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/tcpfault"
)

// revokedIndex stands in for the issued_tokens index: the revoking
// replica records each revoked jti here before it publishes, as the
// durable revoke commits before the cache push.
type revokedIndex struct {
	mu   sync.Mutex
	jtis []string
}

func (r *revokedIndex) add(jti string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jtis = append(r.jtis, jti)
}

func (r *revokedIndex) ListRevoked(context.Context, string) ([]issuedtokenstore.IssuedToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]issuedtokenstore.IssuedToken, 0, len(r.jtis))
	for _, jti := range r.jtis {
		out = append(out, issuedtokenstore.IssuedToken{JTI: jti})
	}
	return out, nil
}

// acmeOnly lists the single tenant the index holds.
type acmeOnly struct{}

func (acmeOnly) ListTenants(context.Context) ([]string, error) { return []string{"acme"}, nil }

// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
// diagnosis: replica B, whose Redis connection was cut while replica A
// revoked a token, did not apply the revocation within 15 s of the
// connection coming back, although its periodic rehydration is an hour
// away. pkg/storage/pubsub must report the re-established subscription
// (OnSubscribed), pkg/credential/revocation/propagator must pass it to the
// rehydration callback, and revocation.Cache.RunRehydration must reread
// the index when the trigger fires, no later than the reconnect spacing
// after its previous requested read. A failure before the cut means the
// two replicas never shared the channel.
func TestTokenRevocationMissedDuringARedisLinkLossIsAppliedOnReconnect(t *testing.T) {
	t.Parallel()
	rd := containers.StartRedis(t, containers.RedisOptions{})
	linkB := tcpfault.Start(t, rd.Addr)
	clientB := redis.NewClient(&redis.Options{Addr: linkB.Addr()})
	t.Cleanup(func() { _ = clientB.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	index := &revokedIndex{}
	propA := revocationprop.New(revocation.NewCache(), pubsub.New(rd.Client))
	cacheB := revocation.NewCache()
	trigger := revocation.NewTrigger()
	go revocationprop.New(cacheB, pubsub.New(clientB)).Run(ctx, trigger.Fire)
	go cacheB.RunRehydration(ctx, time.Hour, revocation.DefaultReconnectSpacing, trigger, acmeOnly{}, index, func(err error) {
		t.Logf("replica B rehydration failed: %v", err)
	})

	waitFor(t, 10*time.Second, "replica B to apply a revocation over a live subscription", func() bool {
		if cacheB.IsRevoked("jti-probe") {
			return true
		}
		propA.Revoke("jti-probe")
		return false
	})

	linkB.Cut()
	index.add("jti-missed")
	propA.Revoke("jti-missed")
	// Hold the link down long enough for go-redis to observe the broken
	// connection and the message to be lost.
	time.Sleep(500 * time.Millisecond)
	if cacheB.IsRevoked("jti-missed") {
		t.Fatal("replica B applied jti-missed while its Redis link was cut")
	}
	linkB.Restore(t)
	waitFor(t, 15*time.Second, "replica B to apply jti-missed after its subscription reconnected", func() bool {
		return cacheB.IsRevoked("jti-missed")
	})
}

// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
// diagnosis: replica B, which started while its Redis link was cut and
// rehydrated before replica A revoked a token, did not apply the
// revocation within 15 s of the link coming back, although its periodic
// rehydration is an hour away. pkg/storage/pubsub must call OnSubscribed
// on the first subscription confirmation as well as on later ones, and
// revocation.Cache.RunRehydration must start the first requested read
// without waiting for the reconnect spacing.
func TestTokenRevocationMissedBeforeTheFirstSubscriptionIsAppliedWhenItIsConfirmed(t *testing.T) {
	t.Parallel()
	rd := containers.StartRedis(t, containers.RedisOptions{})
	linkB := tcpfault.Start(t, rd.Addr)
	linkB.Cut()
	clientB := redis.NewClient(&redis.Options{Addr: linkB.Addr()})
	t.Cleanup(func() { _ = clientB.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	index := &revokedIndex{}
	cacheB := revocation.NewCache()
	if err := cacheB.Rehydrate(ctx, acmeOnly{}, index); err != nil {
		t.Fatalf("replica B startup rehydration: %v", err)
	}
	trigger := revocation.NewTrigger()
	go revocationprop.New(cacheB, pubsub.New(clientB)).Run(ctx, trigger.Fire)
	go cacheB.RunRehydration(ctx, time.Hour, revocation.DefaultReconnectSpacing, trigger, acmeOnly{}, index, func(err error) {
		t.Logf("replica B rehydration failed: %v", err)
	})

	index.add("jti-missed")
	revocationprop.New(revocation.NewCache(), pubsub.New(rd.Client)).Revoke("jti-missed")
	time.Sleep(500 * time.Millisecond)
	if cacheB.IsRevoked("jti-missed") {
		t.Fatal("replica B applied jti-missed while its Redis link was cut")
	}
	linkB.Restore(t)
	start := time.Now()
	waitFor(t, 15*time.Second, "replica B to apply jti-missed on its first subscription confirmation", func() bool {
		return cacheB.IsRevoked("jti-missed")
	})
	t.Logf("replica B applied jti-missed %s after its Redis link came up", time.Since(start).Round(time.Millisecond))
}
