// SPDX-License-Identifier: MIT

// Package sessionage resolves the §5.1 per-runtime / §5.2 per-pool
// maxSessionAge cap for a session row. The §11.3 watchdog uses it so a
// deployer who tunes `maxSessionAge` per runtime (the §11.3 line 242
// guidance) or per pool sees that value enforced, instead of every session
// expiring at the single platform default.
//
// The resolver reads the session's RuntimeRef and PoolRef and returns the
// most-restrictive positive cap declared across the two configuration
// surfaces:
//
//   - the §5.1 RuntimeDefinition `limits.maxSessionAgeSeconds`
//     (runtimestore.Limits), resolved through the derived-runtime merge so
//     a derived runtime's Override value applies, and
//   - the §5.2 pool `maxSessionAgeSeconds` (poolstore.Pool).
//
// A return of 0 means neither surface declares a cap; the watchdog then
// falls back to the platform default. A store lookup error is treated as
// "no per-config cap" so a transient store blip never lengthens or shortens
// a session's deadline unexpectedly — the platform default still bounds it.
//
// spec: §11.3 line 198 — `maxSessionAge` is a deployer cap tuned per
// runtime; §5.1 limits; §5.2 pool. F-11.3.3.
package sessionage

import (
	"context"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// Resolver satisfies watchdog.SessionAgeResolver. Either store may be nil;
// the resolver then consults only the non-nil surface.
type Resolver struct {
	runtimes runtimestore.Store
	pools    poolstore.Store
}

// New returns a Resolver backed by the runtime and pool stores. A nil store
// disables the corresponding lookup.
func New(runtimes runtimestore.Store, pools poolstore.Store) *Resolver {
	return &Resolver{runtimes: runtimes, pools: pools}
}

// EffectiveMaxSessionAgeSeconds returns the most-restrictive per-runtime /
// per-pool maxSessionAge cap (in seconds) declared for sess, or 0 when
// neither surface sets one. spec: §11.3 line 198. F-11.3.3.
func (r *Resolver) EffectiveMaxSessionAgeSeconds(ctx context.Context, sess sessionstore.Session) int {
	cap := 0
	if r.runtimes != nil && sess.RuntimeRef != "" {
		if rt, err := runtimestore.Resolve(ctx, r.runtimes, sess.RuntimeRef); err == nil &&
			rt.Limits != nil && rt.Limits.MaxSessionAgeSeconds > 0 {
			cap = rt.Limits.MaxSessionAgeSeconds
		}
	}
	if r.pools != nil && sess.PoolRef != "" {
		if p, err := r.pools.Get(ctx, sess.PoolRef); err == nil && p.MaxSessionAgeSeconds > 0 {
			cap = minPositive(cap, p.MaxSessionAgeSeconds)
		}
	}
	// spec: §14 line 154 — a §14 per-session maxSessionAge override (also the
	// carrier for the §27.6 playground duration cap the create path stamps,
	// F-27.6.2) tightens the runtime/pool cap. Admission already bounds it by
	// the runtime's limits.maxSessionAge, so it only ever narrows; minPositive
	// ignores the unset (zero) case so a session that requested no override is
	// unaffected.
	if sess.Timeouts != nil && sess.Timeouts.MaxSessionAgeSeconds > 0 {
		cap = minPositive(cap, int(sess.Timeouts.MaxSessionAgeSeconds))
	}
	return cap
}

// minPositive returns the smaller of two caps, ignoring a zero (unset) cap
// on either side. minPositive(0, n) == n and minPositive(n, 0) == n so an
// unset surface never tightens or loosens the other's value.
func minPositive(a, b int) int {
	switch {
	case a <= 0:
		return b
	case b <= 0:
		return a
	case a < b:
		return a
	default:
		return b
	}
}
