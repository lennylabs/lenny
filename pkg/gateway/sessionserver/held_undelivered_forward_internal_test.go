// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// spec: §29.3 (Interactive message send) Forwarding rule 6, §7.2 (Message
// delivery routing — seven paths, path 6)
//
// TestHoldUndeliveredForwardHoldsWithTheRecoveringTTL_spec_29_3 asserts that
// a message whose forward to the coordinating replica was not delivered is
// held in the session DLQ with the expiry of the §7.2 dead-letter table's
// recovering-state row (the entry's score is EnqueuedAt plus
// recoveringDLQTTL for the row), answers HoldQueued for the live session,
// and that a server without messaging refuses the hold with
// ErrMessagingUnavailable and holds nothing.
func TestHoldUndeliveredForwardHoldsWithTheRecoveringTTL_spec_29_3(t *testing.T) {
	ctx := context.Background()

	t.Run("the message is held with the recovering-state TTL", func(t *testing.T) {
		f := newHeldFixture(t, false)
		row := f.row(t)
		at := time.Now().Add(-time.Second).Truncate(time.Millisecond)
		outcome, _, err := f.srv.HoldUndeliveredForward(ctx, row, sessioninbox.Message{
			MessageID: "m-fwd", Payload: heldPayload(t, "m-fwd", "", "hello"), EnqueuedAt: at,
		})
		if err != nil {
			t.Fatalf("HoldUndeliveredForward: %v", err)
		}
		if outcome != HoldQueued {
			t.Errorf("outcome = %v, want HoldQueued", outcome)
		}
		entries, err := f.rc.ZRangeWithScores(ctx, sessioninbox.DLQKey("acme", "s"), 0, -1).Result()
		if err != nil {
			t.Fatalf("read the DLQ: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("DLQ entries = %d, want 1", len(entries))
		}
		want := at.Add(f.srv.recoveringDLQTTL(row)).UnixMilli()
		if got := int64(entries[0].Score); got != want {
			t.Errorf("DLQ expiry = %d, want EnqueuedAt + the recovering-state TTL = %d", got, want)
		}
	})

	t.Run("a server without messaging refuses the hold", func(t *testing.T) {
		f := newHeldFixture(t, false)
		f.srv.messaging = nil
		_, _, err := f.srv.HoldUndeliveredForward(ctx, sessionstore.Session{UserID: testOwner, ID: "s", TenantID: "acme"},
			sessioninbox.Message{MessageID: "m-fwd"})
		if !errors.Is(err, sessioninbox.ErrMessagingUnavailable) {
			t.Fatalf("HoldUndeliveredForward = %v, want ErrMessagingUnavailable", err)
		}
		if n := f.remaining(t); n != 0 {
			t.Errorf("held entries = %d, want 0", n)
		}
	})
}
