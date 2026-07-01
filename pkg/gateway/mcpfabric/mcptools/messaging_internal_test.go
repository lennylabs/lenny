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

// TestMessagingLimiterWithinLimits — a send that stays under every §8.3
// limit is allowed. spec: §8.3 lines 269-272. F-7.2.6.
func TestMessagingLimiterWithinLimits(t *testing.T) {
	lim := newMessagingLimiter(nil, MessagingRateLimit{})
	ok, err := lim.allow(context.Background(), "acme", "sess_a", "sess_b", msgClock)
	if err != nil || !ok {
		t.Fatalf("allow = (%v,%v), want (true,nil)", ok, err)
	}
}

// TestMessagingLimiterOutboundExceeded — the §8.3 maxPerMinute per-sender
// burst rejects the send that crosses the cap. spec: §8.3 line 270.
func TestMessagingLimiterOutboundExceeded(t *testing.T) {
	lim := newMessagingLimiter(nil, MessagingRateLimit{MaxPerMinute: 2, MaxPerSession: 100, MaxInboundPerMinute: 100})
	for i := 1; i <= 2; i++ {
		if ok, _ := lim.allow(context.Background(), "acme", "sess_a", "t"+string(rune('0'+i)), msgClock); !ok {
			t.Fatalf("send %d should be allowed", i)
		}
	}
	if ok, _ := lim.allow(context.Background(), "acme", "sess_a", "t9", msgClock); ok {
		t.Fatal("3rd outbound send should be rate-limited (maxPerMinute=2)")
	}
}

// TestMessagingLimiterInboundExceeded — the §8.3 maxInboundPerMinute cap
// is a per-target aggregate across senders: two distinct senders to one
// target trip the cap regardless of each sender's own outbound budget.
// spec: §8.3 line 309 (the O(N²) storm brake). F-7.2.6.
func TestMessagingLimiterInboundExceeded(t *testing.T) {
	lim := newMessagingLimiter(nil, MessagingRateLimit{MaxPerMinute: 100, MaxPerSession: 100, MaxInboundPerMinute: 2})
	if ok, _ := lim.allow(context.Background(), "acme", "sender1", "target", msgClock); !ok {
		t.Fatal("inbound 1 should be allowed")
	}
	if ok, _ := lim.allow(context.Background(), "acme", "sender2", "target", msgClock); !ok {
		t.Fatal("inbound 2 should be allowed")
	}
	if ok, _ := lim.allow(context.Background(), "acme", "sender3", "target", msgClock); ok {
		t.Fatal("inbound 3 should be rate-limited (maxInboundPerMinute=2, aggregate across senders)")
	}
}

// TestMessagingLimiterLifetimeExceeded — the §8.3 maxPerSession lifetime
// cap rejects beyond its limit even when the per-minute windows have
// budget. spec: §8.3 line 270 (`maxPerSession`). F-7.2.6.
func TestMessagingLimiterLifetimeExceeded(t *testing.T) {
	lim := newMessagingLimiter(nil, MessagingRateLimit{MaxPerMinute: 100, MaxPerSession: 2, MaxInboundPerMinute: 100})
	for i := 1; i <= 2; i++ {
		if ok, _ := lim.allow(context.Background(), "acme", "sess_a", "target", msgClock); !ok {
			t.Fatalf("lifetime send %d should be allowed", i)
		}
	}
	if ok, _ := lim.allow(context.Background(), "acme", "sess_a", "target", msgClock); ok {
		t.Fatal("3rd send should hit the lifetime cap (maxPerSession=2)")
	}
}

// TestMessagingLimiterEmptySenderSkipsOutbound — with no resolved sender
// the per-sender outbound/lifetime caps are skipped but the per-target
// inbound brake still applies. spec: §8.3 line 309. F-7.2.6.
func TestMessagingLimiterEmptySenderSkipsOutbound(t *testing.T) {
	lim := newMessagingLimiter(nil, MessagingRateLimit{MaxPerMinute: 1, MaxPerSession: 1, MaxInboundPerMinute: 2})
	// Two sends with empty sender exceed neither the (skipped) outbound
	// cap of 1 nor the inbound cap of 2.
	for i := 1; i <= 2; i++ {
		if ok, _ := lim.allow(context.Background(), "acme", "", "target", msgClock); !ok {
			t.Fatalf("empty-sender send %d should be allowed (inbound cap=2)", i)
		}
	}
	if ok, _ := lim.allow(context.Background(), "acme", "", "target", msgClock); ok {
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
