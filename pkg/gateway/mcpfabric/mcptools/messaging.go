// SPDX-License-Identifier: MIT

package mcptools

import (
	"context"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// §8.3 lines 269-272 messagingRateLimit defaults. Applied per-field when
// the corresponding MessagingRateLimit field is zero. The values are
// deployment-configurable; per-lease overrides land with the §8.2
// delegation-lease persistence (the lease carries messagingRateLimit and
// child leases inherit the parent's limits or stricter).
const (
	defaultMessagingMaxPerMinute        = 30
	defaultMessagingMaxPerSession       = 200
	defaultMessagingMaxInboundPerMinute = 60
)

// MessagingRateLimit carries the §8.3 lines 269-272 per-session
// lenny/send_message rate limits. A zero field selects the spec default.
// spec: §7.2 line 270; §8.3 lines 269-272, 309. F-7.2.6.
type MessagingRateLimit struct {
	// MaxPerMinute is the per-sender outbound sliding-window burst limit
	// (default 30).
	MaxPerMinute int
	// MaxPerSession is the per-sender lifetime outbound cap (default
	// 200).
	MaxPerSession int
	// MaxInboundPerMinute is the per-target inbound aggregate
	// sliding-window limit, counting messages arriving from every sender
	// in the delegation tree (default 60). It is the §8.3 line 309 brake
	// on the O(N²) sibling-messaging storm: regardless of how many
	// senders contribute, the target accepts at most this many per
	// window.
	MaxInboundPerMinute int
}

func (l MessagingRateLimit) withDefaults() MessagingRateLimit {
	if l.MaxPerMinute <= 0 {
		l.MaxPerMinute = defaultMessagingMaxPerMinute
	}
	if l.MaxPerSession <= 0 {
		l.MaxPerSession = defaultMessagingMaxPerSession
	}
	if l.MaxInboundPerMinute <= 0 {
		l.MaxInboundPerMinute = defaultMessagingMaxInboundPerMinute
	}
	return l
}

// messagingLimiter enforces the §7.2 / §8.3 lenny/send_message rate
// limits. The per-minute outbound and inbound caps share a fixed-window
// ratelimit.Counter under distinct key prefixes; the lifetime
// per-session cap uses an in-process monotonic counter. The lifetime
// counter is best-effort in v1 — it resets on gateway restart, so the
// per-session cap is enforced within a process lifetime; durable
// per-session counters land with the §7.2 inbox persistence (F-7.2.4).
// spec: §7.2 line 270; §8.3 lines 269-272. F-7.2.6.
type messagingLimiter struct {
	counter  ratelimit.Counter
	limits   MessagingRateLimit
	mu       sync.Mutex
	lifetime map[string]int
}

func newMessagingLimiter(counter ratelimit.Counter, limits MessagingRateLimit) *messagingLimiter {
	if counter == nil {
		counter = ratelimit.NewMemory()
	}
	return &messagingLimiter{
		counter:  counter,
		limits:   limits.withDefaults(),
		lifetime: map[string]int{},
	}
}

// allow records one send attempt from sender to target and reports
// whether it stays within every §8.3 rate limit. It evaluates the
// per-sender outbound burst, then the per-target inbound aggregate, then
// the per-sender lifetime cap; the first limit a send exceeds rejects it
// with ok=false. The two per-minute checks use the fixed-window
// Incr-then-compare contract the §11.1 middleware uses (a rejected send
// still consumes its window, so a sender cannot evade the cap by
// retrying within the same minute). The irreversible lifetime counter is
// incremented last so a per-minute rejection does not consume lifetime
// budget. An empty sender (no principal and no fromSessionId) skips the
// per-sender limits but is still subject to the per-target inbound brake.
func (m *messagingLimiter) allow(ctx context.Context, tenant, sender, target string, now time.Time) (bool, error) {
	if sender != "" {
		n, err := m.counter.Incr(ctx, "msg:out:"+tenant+":"+sender, now)
		if err != nil {
			return false, err
		}
		if n > m.limits.MaxPerMinute {
			return false, nil
		}
	}
	n, err := m.counter.Incr(ctx, "msg:in:"+tenant+":"+target, now)
	if err != nil {
		return false, err
	}
	if n > m.limits.MaxInboundPerMinute {
		return false, nil
	}
	if sender != "" && !m.allowLifetime(tenant+":"+sender) {
		return false, nil
	}
	return true, nil
}

// allowLifetime increments the monotonic lifetime counter for key and
// reports whether the count stays within MaxPerSession.
func (m *messagingLimiter) allowLifetime(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lifetime[key]++
	return m.lifetime[key] <= m.limits.MaxPerSession
}

// withinMessagingScope reports whether target is reachable from sender
// under the effective §7.2 messagingScope. A direct parent or direct
// child is always reachable (both `direct` and `siblings` scope). A
// sibling (a session sharing the same non-empty parent) is reachable
// only under `siblings` scope; under the default `direct` scope a
// session cannot message its siblings. Self-messaging is always
// rejected. The check is constant-time — every admissible relation is a
// one-hop ParentSessionID comparison, no tree walk. spec: §7.2 line 240
// (`direct` / `siblings` scope), §7.2 line 373 (parent communication
// asymmetry). F-7.2.6, F-7.2.22.
func withinMessagingScope(sender, target sessionstore.Session, scope session.MessagingScope) bool {
	if sender.ID == target.ID {
		return false
	}
	// Direct child of the sender.
	if target.ParentSessionID == sender.ID {
		return true
	}
	// Direct parent of the sender.
	if sender.ParentSessionID == target.ID && target.ID != "" {
		return true
	}
	// Sibling — allowed only under `siblings` scope.
	if scope.OrDefault() == session.MessagingScopeSiblings &&
		sender.ParentSessionID != "" && sender.ParentSessionID == target.ParentSessionID {
		return true
	}
	return false
}

// crossTenantDenied reports whether target belongs to a tenant other
// than callerTenant — the §7.2 line 268 cross-tenant message guard,
// which the spec requires to run before scope evaluation and rate
// limiting. The session store is tenant-scoped (its Get rejects foreign
// rows as ErrNotFound and never leaks them), so in the per-tenant MCP
// adapter a foreign-tenant target is already unreachable; this explicit
// comparison is the normative validation the spec mandates and the guard
// any multi-tenant transport that surfaces cross-tenant rows relies on.
// An empty target tenant (a row that predates tenant stamping) is not
// treated as a cross-tenant denial. spec: §7.2 line 268. F-7.2.6.
func crossTenantDenied(callerTenant string, target sessionstore.Session) bool {
	return target.TenantID != "" && target.TenantID != callerTenant
}
