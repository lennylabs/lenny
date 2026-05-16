// SPDX-License-Identifier: MIT

// Package credfallback implements the §4.9 credentialPolicy fallback
// chain: the ordered list of credential pools the gateway tries for a
// provider, with per-pool cooldown after a fault.
//
// A tenant's credentialPolicy names, per provider, a `fallback.order`
// of credential pools. The gateway selects the highest-priority pool
// that is not cooling down; when a pool faults (rate limited, auth
// expired, provider unavailable) it is placed on cooldown for
// `cooldownOnRateLimit` and the gateway moves to the next pool. The
// chain is the pure selection-and-cooldown state; the credential
// assignment path drives it.
package credfallback

import "time"

// DefaultCooldown is the §4.9 credentialPolicy `cooldownOnRateLimit`
// default applied when New receives a non-positive cooldown.
const DefaultCooldown = 60 * time.Second

// Chain is a §4.9 credentialPolicy fallback chain for one provider: an
// ordered list of credential pools with per-pool cooldown state. It is
// not goroutine-safe; the caller serializes access.
type Chain struct {
	order    []string
	cooldown time.Duration
	until    map[string]time.Time
}

// New returns a fallback Chain over the ordered pool list, primary
// first. A non-positive cooldown selects DefaultCooldown.
func New(order []string, cooldown time.Duration) *Chain {
	if cooldown <= 0 {
		cooldown = DefaultCooldown
	}
	return &Chain{
		order:    append([]string(nil), order...),
		cooldown: cooldown,
		until:    map[string]time.Time{},
	}
}

// Order returns the chain's pools in priority order.
func (c *Chain) Order() []string {
	return append([]string(nil), c.order...)
}

// CoolingDown reports whether pool is on cooldown at now.
func (c *Chain) CoolingDown(pool string, now time.Time) bool {
	until, ok := c.until[pool]
	return ok && now.Before(until)
}

// Select returns the highest-priority pool in the chain that is not on
// cooldown at now. ok is false when the chain is empty or every pool
// is cooling down — the §4.9 CREDENTIAL_POOL_EXHAUSTED condition.
func (c *Chain) Select(now time.Time) (pool string, ok bool) {
	for _, p := range c.order {
		if !c.CoolingDown(p, now) {
			return p, true
		}
	}
	return "", false
}

// Available returns the chain's pools not on cooldown at now, in
// priority order.
func (c *Chain) Available(now time.Time) []string {
	out := make([]string, 0, len(c.order))
	for _, p := range c.order {
		if !c.CoolingDown(p, now) {
			out = append(out, p)
		}
	}
	return out
}

// Fault places pool on cooldown until now plus the chain's cooldown
// duration — the §4.9 cooldownOnRateLimit applied after a fault
// rotation. A repeated fault while a pool is still cooling extends the
// cooldown from now.
func (c *Chain) Fault(pool string, now time.Time) {
	c.until[pool] = now.Add(c.cooldown)
}
