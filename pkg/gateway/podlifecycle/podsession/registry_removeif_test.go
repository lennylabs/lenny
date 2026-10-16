// SPDX-License-Identifier: MIT

package podsession_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
)

// TestRegistryRemoveIfComparesTheBinding_spec_10_1_5 sweeps RemoveIf over the
// registry states an evictor can meet: the binding it read is still
// published, a newer binding replaced it, it was already removed, and the
// session was never bound. Only the first removes, and only the session named.
//
// spec: 10.1.5 (Stale Replica Behavior)
func TestRegistryRemoveIfComparesTheBinding_spec_10_1_5(t *testing.T) {
	read := &podsession.BindResult{SessionID: "s1"}
	newer := &podsession.BindResult{SessionID: "s1"}
	other := &podsession.BindResult{SessionID: "s2"}
	cases := []struct {
		name      string
		published *podsession.BindResult
		want      bool
		wantLeft  *podsession.BindResult
	}{
		{"still_published", read, true, nil},
		{"replaced", newer, false, newer},
		{"already_removed", nil, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := podsession.NewRegistry()
			r.Put(other)
			if tc.published != nil {
				r.Put(tc.published)
			}
			if got := r.RemoveIf("s1", read); got != tc.want {
				t.Fatalf("RemoveIf = %v, want %v", got, tc.want)
			}
			left, ok := r.Get("s1")
			if (tc.wantLeft == nil) == ok || (ok && left != tc.wantLeft) {
				t.Errorf("binding left = %p (present %v), want %p", left, ok, tc.wantLeft)
			}
			if got, ok := r.Get("s2"); !ok || got != other {
				t.Error("RemoveIf touched another session's binding")
			}
		})
	}
}

// TestRegistryRemoveIfHasOneWinner_spec_10_1_5 races RemoveIf calls for one
// binding under -race and requires exactly one to report the removal, which
// makes that caller the binding's single closer.
//
// spec: 10.1.5 (Stale Replica Behavior)
func TestRegistryRemoveIfHasOneWinner_spec_10_1_5(t *testing.T) {
	for round := range 50 {
		r := podsession.NewRegistry()
		b := &podsession.BindResult{SessionID: "s1"}
		r.Put(b)
		var wins atomic.Int32
		var wg sync.WaitGroup
		start := make(chan struct{})
		for range 16 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if r.RemoveIf("s1", b) {
					wins.Add(1)
				}
			}()
		}
		close(start)
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatalf("round %d: RemoveIf winners = %d, want 1", round, wins.Load())
		}
	}
}

// TestBindResultCloseAdapter_spec_10_1_1 pins that CloseAdapter is a no-op on
// a nil binding and on a binding without an adapter, and closes a binding's
// connection otherwise, after which the channel no longer reports alive.
//
// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination)
func TestBindResultCloseAdapter_spec_10_1_1(t *testing.T) {
	var nilBind *podsession.BindResult
	if err := nilBind.CloseAdapter(); err != nil {
		t.Errorf("nil binding CloseAdapter = %v, want nil", err)
	}
	if err := (&podsession.BindResult{}).CloseAdapter(); err != nil {
		t.Errorf("adapterless CloseAdapter = %v, want nil", err)
	}
	cl, err := adapterclient.Dial("127.0.0.1:1", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	b := &podsession.BindResult{Adapter: cl}
	if err := b.CloseAdapter(); err != nil {
		t.Fatalf("CloseAdapter = %v", err)
	}
	if cl.Alive() {
		t.Error("channel alive after CloseAdapter")
	}
	if err := b.CloseAdapter(); err == nil {
		t.Error("a second CloseAdapter succeeded; the first did not close the channel")
	}
}
