// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the §11.4 full_revoke binding-release request
// the handling gateway replica publishes on Redis pub/sub and each peer
// replica applies to the bindings it holds. The publisher and the
// subscriber are two real propagators over one Redis, so the JSON payload is
// exercised on both sides of the wire.
package fullrevoke_release_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/bindrelease"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pubsub"
)

// peerReleaser records the requests a peer replica's subscriber applies
// through TerminateLocal.
type peerReleaser struct {
	mu   sync.Mutex
	reqs []bindrelease.Request
}

func (p *peerReleaser) TerminateLocal(_ context.Context, req bindrelease.Request) bindrelease.Result {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reqs = append(p.reqs, req)
	return bindrelease.Result{PodsTerminated: len(req.SessionIDs)}
}

func (p *peerReleaser) applied() []bindrelease.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]bindrelease.Request(nil), p.reqs...)
}

// rawCapture subscribes to the request channel and keeps the raw payloads.
type rawCapture struct {
	mu       sync.Mutex
	payloads [][]byte
}

func (r *rawCapture) add(b []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.payloads = append(r.payloads, append([]byte(nil), b...))
}

func (r *rawCapture) first() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.payloads) == 0 {
		return nil
	}
	return r.payloads[0]
}

// spec: 11.4 (User Invalidation)
// diagnosis: the cross-replica full_revoke release request changed on the
// wire. A payload carrying a reason field means the retired revoke reason
// is still published, which a peer has no use for because its release
// sends its own Shutdown. A peer that never applies the request, or applies
// it with the wrong tenant, user, origin, or session set, leaves the
// bindings that peer holds to its coordination Sweeper and breaks the §11.4
// propagation the response's note promises within seconds.
func TestFullRevokeReleaseRequestWireContract_spec_11_4(t *testing.T) {
	mr := miniredis.RunT(t)
	newClient := func() *redis.Client {
		c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	capture := &rawCapture{}
	go pubsub.New(newClient()).Subscribe(ctx, bindrelease.Channel, capture.add)

	peer := &peerReleaser{}
	go bindrelease.New(peer, pubsub.New(newClient()), "replica-B").Run(ctx)

	handling := bindrelease.New(&peerReleaser{}, pubsub.New(newClient()), "replica-A")
	req := bindrelease.Request{TenantID: "acme", UserID: "alice@acme.com", SessionIDs: []string{"run_a", "run_b"}}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && (len(peer.applied()) == 0 || capture.first() == nil) {
		handling.Publish(ctx, req)
		time.Sleep(20 * time.Millisecond)
	}

	raw := capture.first()
	if raw == nil {
		t.Fatal("no request reached the channel")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode the published payload: %v", err)
	}
	for _, k := range []string{"tenantId", "userId", "origin", "sessionIds"} {
		if _, ok := fields[k]; !ok {
			t.Errorf("payload %s lacks %q", raw, k)
		}
	}
	if _, ok := fields["reason"]; ok {
		t.Errorf("payload %s carries a reason field", raw)
	}
	if len(fields) != 4 {
		t.Errorf("payload %s carries %d fields, want 4", raw, len(fields))
	}

	got := peer.applied()
	if len(got) == 0 {
		t.Fatal("the peer never applied the request through TerminateLocal")
	}
	last := got[len(got)-1]
	if last.TenantID != "acme" || last.UserID != "alice@acme.com" || last.Origin != "replica-A" ||
		len(last.SessionIDs) != 2 || last.SessionIDs[0] != "run_a" || last.SessionIDs[1] != "run_b" {
		t.Errorf("peer applied %+v, want acme/alice from replica-A over [run_a run_b]", last)
	}
}
