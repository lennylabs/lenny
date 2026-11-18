// SPDX-License-Identifier: MIT

package sessionevents_test

import (
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/streamproxy/sessionevents"
)

// TestBusServesTheSubscriptionThroughTheSeam checks the subscription a
// caller reaches through the seam interface: the retained events after the
// cursor arrive in the backlog in order, a later event arrives on the live
// channel, Close closes the channel and is safe to repeat, and a tenant
// other than the session's owner is refused with no subscription.
//
// spec: GWSEAM-Subscribe, GWSEAM-Publish
func TestBusServesTheSubscriptionThroughTheSeam(t *testing.T) {
	var events seam.Events = sessionevents.NewBus(0)
	for _, typ := range []string{"a", "b", "c"} {
		events.Publish("acme", "sess_1", typ, `{}`, ts())
	}

	sub, err := events.Subscribe("acme", "sess_1", 1, 4)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if len(sub.Backlog) != 2 || sub.Backlog[0].Type != "b" || sub.Backlog[1].Type != "c" || sub.Backlog[0].Seq != 2 {
		t.Fatalf("backlog = %+v, want b (seq 2) then c", sub.Backlog)
	}
	live := events.Publish("acme", "sess_1", "d", `{"n":4}`, ts())
	select {
	case ev := <-sub.Events:
		if ev != live {
			t.Errorf("live event = %+v, want %+v", ev, live)
		}
	default:
		t.Fatal("the live event did not arrive on the subscription's channel")
	}
	sub.Close()
	sub.Close()
	if _, open := <-sub.Events; open {
		t.Error("the channel is open after Close")
	}

	refused, err := events.Subscribe("globex", "sess_1", 0, 4)
	if !errors.Is(err, sessionevents.ErrTenantMismatch) {
		t.Fatalf("foreign tenant: err = %v, want ErrTenantMismatch", err)
	}
	if refused.Events != nil || refused.Close != nil || refused.Backlog != nil {
		t.Errorf("foreign tenant got subscription %+v, want none", refused)
	}
}

// TestBusStreamStatsCountsLiveSubscriptionsAndTheFullestBuffer checks that
// the stream stats count only open subscriptions and report the highest
// replay-buffer utilization across sessions.
//
// spec: GWSEAM-StreamStats
func TestBusStreamStatsCountsLiveSubscriptionsAndTheFullestBuffer(t *testing.T) {
	bus := sessionevents.NewBus(4)
	if got := bus.StreamStats(); got != (seam.StreamStats{}) {
		t.Fatalf("empty bus stats = %+v, want zero", got)
	}
	for i := 0; i < 3; i++ {
		bus.Publish("", "sess_busy", "e", `{}`, ts())
	}
	bus.Publish("", "sess_quiet", "e", `{}`, ts())
	a, _ := bus.Subscribe("", "sess_busy", 0, 4)
	b, _ := bus.Subscribe("", "sess_quiet", 0, 4)
	b.Close()
	defer a.Close()
	got := bus.StreamStats()
	if got.ActiveSubscribers != 1 || got.MaxReplayBufferUtilization != 0.75 {
		t.Errorf("stats = %+v, want 1 subscriber and utilization 0.75", got)
	}
}
