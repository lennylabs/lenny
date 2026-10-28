// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/trace"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
)

// heldLeaseReplica is the holder value the held-delivery lease tests run the
// fixture's server under.
const heldLeaseReplica = "rep-1.0011223344556677@127.0.0.1:50054"

// heldLeasePeer is the holder value of the replica a handoff moves the lease
// to.
const heldLeasePeer = "rep-2.8899aabbccddeeff@127.0.0.1:50055"

// withLeases wires the fixture's server as one replica of a multi-replica
// gateway: a lease store over leases, a pod binder, and heldLeaseReplica as
// its replica identity, so coordinatesEverySession is false.
func (f *heldFixture) withLeases(leases leasestore.LeaseStore) {
	f.srv.leaseStore = leases
	f.srv.podBinder = &podsession.Binder{}
	f.srv.replicaID = heldLeaseReplica
}

// holdTwo holds two DLQ entries, m1 before m2.
func (f *heldFixture) holdTwo(t *testing.T) {
	t.Helper()
	base := time.Now().Add(-time.Minute)
	f.holdDLQ(t, "m1", "", base, heldPayload(t, "m1", "", "a"), 0)
	f.holdDLQ(t, "m2", "", base.Add(time.Second), heldPayload(t, "m2", "", "b"), 0)
}

// spec: §7.2 (Message delivery routing — seven paths, path 6) Delivery of
// held messages, §29.3 (Interactive message send) Forwarding rule 5, §10.1.1
// (Stateless Replicas and Per-Session Coordination)
//
// TestHeldDeliveryStopsOnceAnotherReplicaCoordinates_spec_7_2 asserts that
// only the coordinating replica delivers held messages. A replica whose
// REG-COORDLEASE names it delivers every entry. A lease that is absent or
// names another holder delivers nothing, leaves every entry, and returns
// ErrHeldDeliveryPreempted. A lease handed to a peer after the first entry
// was delivered stops the loop before the second entry, which the pre-fix
// loop delivered as well, so the new coordinator's takeover delivery and
// this replica both delivered it. A lease read error does not stop the
// delivery.
func TestHeldDeliveryStopsOnceAnotherReplicaCoordinates_spec_7_2(t *testing.T) {
	ctx := context.Background()
	newLeases := func(t *testing.T) (*leasestore.Store, *miniredis.Miniredis) {
		t.Helper()
		mr := miniredis.RunT(t)
		rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rc.Close() })
		return leasestore.New(rc), mr
	}
	acquire := func(t *testing.T, leases *leasestore.Store, holder string) {
		t.Helper()
		if _, err := leases.Acquire(ctx, "acme", "s", holder, time.Minute); err != nil {
			t.Fatalf("acquire the lease for %s: %v", holder, err)
		}
	}
	for _, tc := range []struct {
		name   string
		holder string
		want   []string
		err    error
	}{
		{"the lease names this replica", heldLeaseReplica, []string{"m1", "m2"}, nil},
		{"the lease names another replica", heldLeasePeer, nil, ErrHeldDeliveryPreempted},
		{"no lease is held", "", nil, ErrHeldDeliveryPreempted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newHeldFixture(t, false)
			leases, _ := newLeases(t)
			f.withLeases(leases)
			if tc.holder != "" {
				acquire(t, leases, tc.holder)
			}
			f.holdTwo(t)
			err := f.srv.deliverBuffered(ctx, f.row(t))
			if !errors.Is(err, tc.err) {
				t.Fatalf("deliverBuffered = %v, want %v", err, tc.err)
			}
			if got := f.exec.ids(); len(got) != len(tc.want) {
				t.Fatalf("delivered %v, want %v", got, tc.want)
			}
			if n := f.remaining(t); n != 2-len(tc.want) {
				t.Errorf("held entries left = %d, want %d", n, 2-len(tc.want))
			}
		})
	}

	t.Run("the lease is handed off after the first entry", func(t *testing.T) {
		f := newHeldFixture(t, false)
		leases, mr := newLeases(t)
		f.withLeases(leases)
		acquire(t, leases, heldLeaseReplica)
		f.holdTwo(t)
		f.exec.onSend = func(context.Context, executor.Message) {
			// The handoff: the lease is gone and the peer acquires it
			// while this replica's first held delivery is in flight.
			mr.Del(leasestore.Key("acme", "s"))
			acquire(t, leases, heldLeasePeer)
		}
		err := f.srv.deliverBuffered(ctx, f.row(t))
		if !errors.Is(err, ErrHeldDeliveryPreempted) {
			t.Fatalf("deliverBuffered = %v, want ErrHeldDeliveryPreempted", err)
		}
		if got := f.exec.ids(); len(got) != 1 || got[0] != "m1" {
			t.Fatalf("delivered %v, want only [m1]", got)
		}
		if n := f.remaining(t); n != 1 {
			t.Errorf("held entries left = %d, want m2 left for the new coordinator", n)
		}
	})

	t.Run("a lease read error does not stop the delivery", func(t *testing.T) {
		f := newHeldFixture(t, false)
		leases, mr := newLeases(t)
		f.withLeases(leases)
		mr.Close()
		f.holdTwo(t)
		if err := f.srv.deliverBuffered(ctx, f.row(t)); err != nil {
			t.Fatalf("deliverBuffered = %v, want nil", err)
		}
		if got := f.exec.ids(); len(got) != 2 {
			t.Errorf("delivered %v, want [m1 m2]", got)
		}
	})
}

// spec: §29.3 (Interactive message send) Forwarding rules 5 and 6, §7.2
// (Message delivery routing — seven paths, path 6) Delivery of held messages
//
// TestPath2SendAfterHeldDeliveryPreemptionTakesTheNotDeliveredOutcome_spec_29_3
// asserts the answer to a path-2 message whose held delivery finds that
// another replica holds REG-COORDLEASE. Nothing of the message reaches the
// runtime, so the serving replica applies the not-delivered outcome of
// Forwarding rule 6 itself: a message with no inReplyTo is held in the
// session DLQ and answered 200 with a queued receipt, and a message carrying
// inReplyTo is refused 409 TARGET_NOT_READY and is not held. Before the fix
// both were answered 500 EXECUTOR_FAILURE.
func TestPath2SendAfterHeldDeliveryPreemptionTakesTheNotDeliveredOutcome_spec_29_3(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name      string
		inReplyTo string
		code      int
		bodyHas   string
		held      int
	}{
		{"a message with no inReplyTo is held and answered queued", "", http.StatusOK, `"status":"queued"`, 1},
		{"a message carrying inReplyTo is refused", "req-1", http.StatusConflict, "TARGET_NOT_READY", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newHeldFixture(t, false)
			mr := miniredis.RunT(t)
			rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = rc.Close() })
			leases := leasestore.New(rc)
			f.withLeases(leases)
			if _, err := leases.Acquire(ctx, "acme", "s", heldLeasePeer, time.Minute); err != nil {
				t.Fatalf("acquire the lease for the peer: %v", err)
			}
			req := MessageRequest{Messages: []MessagePayload{{
				ID: "m-new", Content: sessionrecord.MessageContentFromText("hello"), InReplyTo: tc.inReplyTo,
			}}}
			msgs := []executor.Message{projectMessage(req.Messages[0])}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/v1/sessions/s/messages", nil)
			outcome, ok := f.srv.deliverMessageBatch(w, r, trace.SpanFromContext(ctx), f.row(t), "acme", req, msgs, []int{0})
			if ok {
				f.srv.writeDeliveryReceipt(w, req, outcome)
			}
			if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.bodyHas) {
				t.Fatalf("answer = %d %s, want %d containing %s", w.Code, w.Body.String(), tc.code, tc.bodyHas)
			}
			if got := f.exec.ids(); len(got) != 0 {
				t.Errorf("delivered %v, want nothing sent by a replica that lost the lease", got)
			}
			if n := f.remaining(t); n != tc.held {
				t.Errorf("held entries = %d, want %d", n, tc.held)
			}
		})
	}
}
