// SPDX-License-Identifier: MIT

package propagator

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/revocation"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pubsub"
)

// TestPublishReachesPeerPropagator_spec_13_3 pins that Publish sends the
// message a peer Propagator.Run applies, so a process that revokes a
// token without a local cache (the Token Service on a rotation) reaches
// every replica's revocation cache, and that a nil bus publishes nothing
// without error.
//
// spec: 13.3 (Credential Flow) "Token rotation and revocation"
func TestPublishReachesPeerPropagator_spec_13_3(t *testing.T) {
	if err := Publish(context.Background(), nil, "jti-nil-bus"); err != nil {
		t.Fatalf("Publish on a nil bus = %v, want nil", err)
	}

	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	bus := pubsub.New(client)

	peer := revocation.NewCache()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go New(peer, bus).Run(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for !peer.IsRevoked("jti-rotated") {
		if time.Now().After(deadline) {
			t.Fatal("the peer cache did not apply the published jti within 5 s")
		}
		// Publish repeatedly until the subscription is live: Redis
		// pub/sub drops a message sent before the subscriber attaches.
		if err := Publish(ctx, bus, "jti-rotated"); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
