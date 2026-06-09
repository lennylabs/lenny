// SPDX-License-Identifier: MIT

// Package quotafailopen holds the §12.4 / §11.2 in-memory per-(tenant,
// user) token counter that the gateway keeps so the Redis-recovery MAX
// rule has a real source (2).
//
// spec: §12.4 ("Quota counter reconciliation after fail-open" — source (2)
// is "the in-memory counter accumulated during the fail-open window on the
// recovering replica"); §11.2 lines 44, 48. The §11.2 line 48 / §24.6
// reconcile restores each still-current window to
// MAX(redis_current, postgres_checkpoint, in_memory_counter). The Redis
// counter and the Postgres checkpoint are the other two sources; this
// package is the third.
//
// The accumulator folds every proxy-extracted token delta into a cumulative
// per-fixed-window counter, so on a Redis-recovery edge the MAX rule can
// restore usage that a Redis write dropped while the shared counter was
// unreachable. A rolling-window period is skipped (no single restorable
// window), matching quotacheckpoint's deliberate rolling-window skip.
package quotafailopen

import (
	"context"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/quotastore"
	"github.com/lennylabs/lenny/pkg/quota"
)

// Accumulator is a thread-safe in-memory cumulative token counter keyed by
// (tenant, user, reset period). Each entry tracks one fixed window and
// resets when the window rolls, so the map is bounded by the number of
// active (tenant, user, period) tuples within the current window.
type Accumulator struct {
	mu      sync.Mutex
	entries map[string]*entry
}

// entry is one subject's cumulative token count for a single fixed window.
type entry struct {
	period      quota.ResetPeriod
	windowLabel string
	tokens      int64
}

// New returns an empty Accumulator.
func New() *Accumulator {
	return &Accumulator{entries: make(map[string]*entry)}
}

// key builds the per-(tenant, user, period) map key. The empty user id
// addresses the per-tenant rollup window (the scope the §11.2 reconcile
// names alongside the per-user window).
func key(tenantID, userID string, period quota.ResetPeriod) string {
	return tenantID + "\x00" + userID + "\x00" + string(period)
}

// Record folds tokens consumed by one proxied LLM response into both the
// per-user window and the per-tenant rollup window for the period
// containing at. A non-positive token count, or a rolling-period record
// (no single restorable window), is a no-op — the recovery reconcile skips
// those scopes as well. The counter is cumulative within a fixed window
// and resets when the window rolls.
//
// spec: §12.4 source (2); §11.2 lines 44, 48.
func (a *Accumulator) Record(tenantID, userID string, period quota.ResetPeriod, at time.Time, tokens int64) {
	if a == nil || tokens <= 0 {
		return
	}
	label, err := quotastore.WindowLabel(period, at)
	if err != nil {
		// Rolling period: the checkpoint/reconcile path skips it too.
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if userID != "" {
		a.addLocked(key(tenantID, userID, period), period, label, tokens)
	}
	a.addLocked(key(tenantID, "", period), period, label, tokens)
}

// addLocked adds tokens to the entry for k, rolling the entry to a fresh
// zeroed counter when the window label has changed.
func (a *Accumulator) addLocked(k string, period quota.ResetPeriod, label string, tokens int64) {
	e := a.entries[k]
	if e == nil || e.windowLabel != label {
		e = &entry{period: period, windowLabel: label}
		a.entries[k] = e
	}
	e.tokens += tokens
}

// UserWindow returns the accumulated token total for the per-user window of
// period containing at, or 0 when the window has rolled or no record
// exists. It is the §11.2 line 48 MAX-rule source (2) for the per-user
// scope.
func (a *Accumulator) UserWindow(tenantID, userID string, period quota.ResetPeriod, at time.Time) int64 {
	return a.read(key(tenantID, userID, period), period, at)
}

// TenantRollup returns the accumulated token total for the per-tenant
// rollup window of period containing at, or 0 when the window has rolled or
// no record exists. It is the §11.2 line 48 MAX-rule source (2) for the
// per-tenant rollup scope.
func (a *Accumulator) TenantRollup(tenantID string, period quota.ResetPeriod, at time.Time) int64 {
	return a.read(key(tenantID, "", period), period, at)
}

func (a *Accumulator) read(k string, period quota.ResetPeriod, at time.Time) int64 {
	if a == nil {
		return 0
	}
	label, err := quotastore.WindowLabel(period, at)
	if err != nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	e := a.entries[k]
	if e == nil || e.windowLabel != label {
		return 0
	}
	return e.tokens
}

// Sample is one accumulated window the recovery reconcile can restore. An
// empty UserID addresses the per-tenant rollup window.
type Sample struct {
	TenantID    string
	UserID      string
	Period      quota.ResetPeriod
	WindowLabel string
	Tokens      int64
}

// Snapshot returns every entry whose window is still current as of now, so
// the §11.2 line 48 recovery reconcile can also restore fail-open-only
// windows — windows that opened entirely during the outage and therefore
// have no Postgres checkpoint row to drive the row-based reconcile. Entries
// whose window has already rolled are skipped.
func (a *Accumulator) Snapshot(now time.Time) []Sample {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []Sample
	for k, e := range a.entries {
		label, err := quotastore.WindowLabel(e.period, now)
		if err != nil || e.windowLabel != label || e.tokens <= 0 {
			continue
		}
		out = append(out, Sample{
			TenantID:    tenantOf(k),
			UserID:      userOf(k),
			Period:      e.period,
			WindowLabel: e.windowLabel,
			Tokens:      e.tokens,
		})
	}
	return out
}

// tenantOf / userOf decode the map key built by key(). The key is
// tenant\x00user\x00period; tenant ids and user ids never contain \x00.
func tenantOf(k string) string {
	if i := indexNul(k); i >= 0 {
		return k[:i]
	}
	return k
}

func userOf(k string) string {
	i := indexNul(k)
	if i < 0 {
		return ""
	}
	rest := k[i+1:]
	if j := indexNul(rest); j >= 0 {
		return rest[:j]
	}
	return ""
}

func indexNul(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return i
		}
	}
	return -1
}

// Sweep drops every entry whose window has rolled relative to now, bounding
// the map to the active window. A rolling-period entry (which Record never
// creates) is also dropped defensively. It returns the number of entries
// reclaimed. The gateway calls it on a low cadence.
func (a *Accumulator) Sweep(now time.Time) int {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	removed := 0
	for k, e := range a.entries {
		label, err := quotastore.WindowLabel(e.period, now)
		if err != nil || e.windowLabel != label {
			delete(a.entries, k)
			removed++
		}
	}
	return removed
}

// DeleteByUser drops every accumulated window for (tenantID, userID). It is
// the §12.8 step-6 GDPR erasure of the in-memory fail-open budget source:
// the §11.2 line-48 MAX rule folds this accumulator in (source (2)), so a
// reconcile that ran after a user's Redis counter and Postgres checkpoint
// were already erased would otherwise re-seed the erased user's usage back
// into Postgres — the same post-recovery resurrection the §12.8 step-5
// billing-buffer purge prevents. The per-tenant rollup window (userID="")
// is preserved; only the named user's per-user windows are removed. An
// empty scope is rejected so erasure is never treated as a wildcard
// (§12.8 line 753). spec: §12.1 line 5; §12.8 step 6.
func (a *Accumulator) DeleteByUser(_ context.Context, tenantID, userID string) (int, error) {
	if a == nil {
		return 0, nil
	}
	if tenantID == "" || userID == "" {
		return 0, quotastore.ErrEmptyScope
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	removed := 0
	for k := range a.entries {
		if tenantOf(k) == tenantID && userOf(k) == userID {
			delete(a.entries, k)
			removed++
		}
	}
	return removed, nil
}

// DeleteByTenant drops every accumulated window for tenantID, including the
// per-tenant rollup. It is the §12.8 Phase-4 tenant-deletion erasure of the
// in-memory fail-open budget source. An empty tenant id is rejected (never
// a wildcard). spec: §12.1 line 5; §12.8 Phase 4.
func (a *Accumulator) DeleteByTenant(_ context.Context, tenantID string) (int, error) {
	if a == nil {
		return 0, nil
	}
	if tenantID == "" {
		return 0, quotastore.ErrEmptyScope
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	removed := 0
	for k := range a.entries {
		if tenantOf(k) == tenantID {
			delete(a.entries, k)
			removed++
		}
	}
	return removed, nil
}

// Len reports the number of live entries. It exists for observability and
// tests.
func (a *Accumulator) Len() int {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.entries)
}
