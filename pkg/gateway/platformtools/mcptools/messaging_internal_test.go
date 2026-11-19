// SPDX-License-Identifier: MIT

package mcptools

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

var msgClock = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// allowBoth runs the sender-keyed and target-keyed limits in the order the
// lenny/send_message handler runs them, and reports whether both allow.
func allowBoth(t *testing.T, lim *messagingLimiter, sender, target string) bool {
	t.Helper()
	ok, err := lim.allowSender(context.Background(), "acme", sender, msgClock)
	if err != nil {
		t.Fatalf("allowSender: %v", err)
	}
	if !ok {
		return false
	}
	ok, err = lim.allowTarget(context.Background(), "acme", target, msgClock)
	if err != nil {
		t.Fatalf("allowTarget: %v", err)
	}
	return ok
}

// TestMessagingLimiterWithinLimits — a send that stays under every §8.3
// limit is allowed by both the sender-keyed and the target-keyed limits.
// spec: §8.3 (Delegation Policy and Lease). F-7.2.6.
func TestMessagingLimiterWithinLimits(t *testing.T) {
	lim := newMessagingLimiter(nil, MessagingRateLimit{})
	if ok, err := lim.allowSender(context.Background(), "acme", "sess_a", msgClock); err != nil || !ok {
		t.Fatalf("allowSender = (%v,%v), want (true,nil)", ok, err)
	}
	if ok, err := lim.allowTarget(context.Background(), "acme", "sess_b", msgClock); err != nil || !ok {
		t.Fatalf("allowTarget = (%v,%v), want (true,nil)", ok, err)
	}
}

// TestMessagingLimiterOutboundExceeded — the §8.3 maxPerMinute per-sender
// burst is sender-keyed: it rejects the send that crosses the cap whatever
// the target. spec: §8.3 (Delegation Policy and Lease).
func TestMessagingLimiterOutboundExceeded(t *testing.T) {
	lim := newMessagingLimiter(nil, MessagingRateLimit{MaxPerMinute: 2, MaxPerSession: 100, MaxInboundPerMinute: 100})
	for i := 1; i <= 2; i++ {
		if !allowBoth(t, lim, "sess_a", "t"+string(rune('0'+i))) {
			t.Fatalf("send %d should be allowed", i)
		}
	}
	if ok, _ := lim.allowSender(context.Background(), "acme", "sess_a", msgClock); ok {
		t.Fatal("3rd outbound send should be rate-limited (maxPerMinute=2)")
	}
}

// TestMessagingLimiterInboundExceeded — the §8.3 maxInboundPerMinute cap
// is a target-keyed aggregate across senders: two distinct senders to one
// target trip the cap regardless of each sender's own outbound budget.
// spec: §8.3 (Delegation Policy and Lease) — the O(N²) storm brake. F-7.2.6.
func TestMessagingLimiterInboundExceeded(t *testing.T) {
	lim := newMessagingLimiter(nil, MessagingRateLimit{MaxPerMinute: 100, MaxPerSession: 100, MaxInboundPerMinute: 2})
	if !allowBoth(t, lim, "sender1", "target") {
		t.Fatal("inbound 1 should be allowed")
	}
	if !allowBoth(t, lim, "sender2", "target") {
		t.Fatal("inbound 2 should be allowed")
	}
	if allowBoth(t, lim, "sender3", "target") {
		t.Fatal("inbound 3 should be rate-limited (maxInboundPerMinute=2, aggregate across senders)")
	}
}

// TestMessagingLimiterLifetimeExceeded — the §8.3 maxPerSession lifetime
// cap is sender-keyed and rejects beyond its limit even when the
// per-minute window has budget. spec: §8.3 (Delegation Policy and Lease).
// F-7.2.6.
func TestMessagingLimiterLifetimeExceeded(t *testing.T) {
	lim := newMessagingLimiter(nil, MessagingRateLimit{MaxPerMinute: 100, MaxPerSession: 2, MaxInboundPerMinute: 100})
	for i := 1; i <= 2; i++ {
		if ok, _ := lim.allowSender(context.Background(), "acme", "sess_a", msgClock); !ok {
			t.Fatalf("lifetime send %d should be allowed", i)
		}
	}
	if ok, _ := lim.allowSender(context.Background(), "acme", "sess_a", msgClock); ok {
		t.Fatal("3rd send should hit the lifetime cap (maxPerSession=2)")
	}
}

// TestMessagingLimiterTargetCountsNoSenderBudget — the target-keyed limit
// counts only msg:in, so target-keyed calls never consume a sender's
// outbound or lifetime budget. spec: §8.3 (Delegation Policy and Lease),
// §29.3 (Interactive message send) Forwarding rule 4.
func TestMessagingLimiterTargetCountsNoSenderBudget(t *testing.T) {
	lim := newMessagingLimiter(nil, MessagingRateLimit{MaxPerMinute: 1, MaxPerSession: 1, MaxInboundPerMinute: 100})
	for i := 0; i < 5; i++ {
		if ok, _ := lim.allowTarget(context.Background(), "acme", "target", msgClock); !ok {
			t.Fatalf("target-keyed call %d should be allowed (maxInboundPerMinute=100)", i+1)
		}
	}
	if ok, _ := lim.allowSender(context.Background(), "acme", "sess_a", msgClock); !ok {
		t.Fatal("the first sender-keyed call should be allowed: target-keyed calls consumed no sender budget")
	}
}

// TestMessagingLimiterEmptySenderSkipsOutbound — with no resolved sender
// the sender-keyed method counts nothing and allows, and the target-keyed
// inbound brake still applies. spec: §8.3 (Delegation Policy and Lease).
// F-7.2.6.
func TestMessagingLimiterEmptySenderSkipsOutbound(t *testing.T) {
	lim := newMessagingLimiter(nil, MessagingRateLimit{MaxPerMinute: 1, MaxPerSession: 1, MaxInboundPerMinute: 2})
	// Two sends with empty sender exceed neither the (skipped) outbound
	// cap of 1 nor the inbound cap of 2.
	for i := 1; i <= 2; i++ {
		if !allowBoth(t, lim, "", "target") {
			t.Fatalf("empty-sender send %d should be allowed (inbound cap=2)", i)
		}
	}
	if allowBoth(t, lim, "", "target") {
		t.Fatal("empty-sender send 3 should hit the inbound cap")
	}
}

// TestWithinMessagingScope_spec_7_2_240 verifies the §7.2 reachability
// matrix: parent and child are always reachable; a sibling is reachable
// only under `siblings` scope; self is never reachable. F-7.2.6.
func TestWithinMessagingScope_spec_7_2_240(t *testing.T) {
	parent := sessionstore.Session{ID: "p"}
	childA := sessionstore.Session{ID: "a", ParentSessionID: "p"}
	childB := sessionstore.Session{ID: "b", ParentSessionID: "p"}
	other := sessionstore.Session{ID: "x", ParentSessionID: "q"}

	cases := []struct {
		name           string
		sender, target sessionstore.Session
		scope          session.MessagingScope
		want           bool
	}{
		{"parent to child (direct)", parent, childA, session.MessagingScopeDirect, true},
		{"child to parent (direct)", childA, parent, session.MessagingScopeDirect, true},
		{"sibling under direct is denied", childA, childB, session.MessagingScopeDirect, false},
		{"sibling under siblings is allowed", childA, childB, session.MessagingScopeSiblings, true},
		{"self is denied", childA, childA, session.MessagingScopeSiblings, false},
		{"unrelated is denied", childA, other, session.MessagingScopeSiblings, false},
		{"empty scope defaults to direct (sibling denied)", childA, childB, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := withinMessagingScope(tc.sender, tc.target, tc.scope); got != tc.want {
				t.Errorf("withinMessagingScope = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCrossTenantDenied_spec_7_2_268 verifies the §7.2 cross-tenant
// guard: a target in another tenant is denied; a same-tenant target and
// a target with no recorded tenant are not. F-7.2.6.
func TestCrossTenantDenied_spec_7_2_268(t *testing.T) {
	if !crossTenantDenied("acme", sessionstore.Session{TenantID: "globex"}) {
		t.Error("foreign-tenant target should be denied")
	}
	if crossTenantDenied("acme", sessionstore.Session{TenantID: "acme"}) {
		t.Error("same-tenant target should be allowed")
	}
	if crossTenantDenied("acme", sessionstore.Session{TenantID: ""}) {
		t.Error("target with no recorded tenant should not be a cross-tenant denial")
	}
}
