// SPDX-License-Identifier: MIT

package propagator

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/credential/revocation"
	"github.com/lennylabs/lenny/pkg/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/storage/pubsub"
)

// durableIndex stands in for the issued_tokens index: the revoking
// replica writes each revoked jti here before it publishes, as the
// durable revoke does before the cache push.
type durableIndex struct {
	mu   sync.Mutex
	jtis []string
}

func (d *durableIndex) add(jti string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.jtis = append(d.jtis, jti)
}

func (d *durableIndex) ListRevoked(context.Context, string) ([]issuedtokenstore.IssuedToken, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]issuedtokenstore.IssuedToken, 0, len(d.jtis))
	for _, jti := range d.jtis {
		out = append(out, issuedtokenstore.IssuedToken{JTI: jti})
	}
	return out, nil
}

// oneTenant lists the single tenant the durable index holds.
type oneTenant struct{}

func (oneTenant) ListTenants(context.Context) ([]string, error) { return []string{"acme"}, nil }

// eventually polls cond every 10 ms until it holds or timeout elapses.
func eventually(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", timeout, desc)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// awaitLive revokes probe through from until to applies it, so the
// subscription of to is confirmed before the test goes on.
func awaitLive(t *testing.T, from *Propagator, to *revocation.Cache, probe string) {
	t.Helper()
	eventually(t, 10*time.Second, "the subscription to apply "+probe, func() bool {
		if to.IsRevoked(probe) {
			return true
		}
		from.Revoke(probe)
		return false
	})
}

// TestRunCallsRehydrateWhenTheSubscriptionReconnects pins that Run hands
// its rehydration callback to the subscription as the reconnect callback:
// the first subscription does not call it, and each re-established
// subscription calls it once.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
func TestRunCallsRehydrateWhenTheSubscriptionReconnects(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	bus := pubsub.New(client)

	local := revocation.NewCache()
	var rehydrations atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go New(local, bus).Run(ctx, func() { rehydrations.Add(1) })

	awaitLive(t, New(revocation.NewCache(), bus), local, "jti-probe")
	if n := rehydrations.Load(); n != 0 {
		t.Fatalf("rehydrate ran %d times on the first subscription, want 0", n)
	}
	mr.Close()
	if err := mr.Restart(); err != nil {
		t.Fatalf("restart miniredis: %v", err)
	}
	eventually(t, 10*time.Second, "rehydrate after the reconnect", func() bool {
		return rehydrations.Load() == 1
	})
}

// TestReplicaAppliesARevocationMissedDuringTheOutageOnReconnect pins the
// path end to end: a revocation that replica A records and publishes
// while Redis is down never reaches replica B over pub/sub, and B applies
// it when its subscription comes back, through the rehydration the
// reconnect triggers, long before its hour-long periodic tick.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
func TestReplicaAppliesARevocationMissedDuringTheOutageOnReconnect(t *testing.T) {
	mr := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	clientB := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = clientA.Close(); _ = clientB.Close() })

	index := &durableIndex{}
	replicaA := New(revocation.NewCache(), pubsub.New(clientA))
	cacheB := revocation.NewCache()
	trigger := revocation.NewTrigger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go New(cacheB, pubsub.New(clientB)).Run(ctx, trigger.Fire)
	go cacheB.RunRehydration(ctx, time.Hour, trigger, oneTenant{}, index, nil)

	awaitLive(t, replicaA, cacheB, "jti-probe")

	mr.Close()
	index.add("jti-missed")
	replicaA.Revoke("jti-missed")
	if cacheB.IsRevoked("jti-missed") {
		t.Fatal("replica B applied jti-missed while Redis was down")
	}
	if err := mr.Restart(); err != nil {
		t.Fatalf("restart miniredis: %v", err)
	}
	eventually(t, 10*time.Second, "replica B to apply jti-missed after the reconnect", func() bool {
		return cacheB.IsRevoked("jti-missed")
	})
}
