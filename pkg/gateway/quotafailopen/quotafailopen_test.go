// SPDX-License-Identifier: MIT

package quotafailopen

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/quota"
)

var fixed = time.Date(2026, 6, 9, 10, 30, 0, 0, time.UTC)

// spec: §12.4 source (2); §11.2 line 48 — Record folds proxy-extracted
// tokens into a cumulative per-(tenant, user) window counter and the per-
// tenant rollup counter; reads return the accumulated value for the current
// window.
func TestRecordAccumulatesUserAndTenantRollup_spec_12_4(t *testing.T) {
	a := New()
	a.Record("acme", "alice", quota.ResetHourly, fixed, 100)
	a.Record("acme", "alice", quota.ResetHourly, fixed.Add(time.Minute), 50)
	a.Record("acme", "bob", quota.ResetHourly, fixed, 30)

	if got := a.UserWindow("acme", "alice", quota.ResetHourly, fixed); got != 150 {
		t.Errorf("UserWindow(alice) = %d, want 150", got)
	}
	if got := a.UserWindow("acme", "bob", quota.ResetHourly, fixed); got != 30 {
		t.Errorf("UserWindow(bob) = %d, want 30", got)
	}
	// Tenant rollup is the sum of both users' contributions.
	if got := a.TenantRollup("acme", quota.ResetHourly, fixed); got != 180 {
		t.Errorf("TenantRollup(acme) = %d, want 180", got)
	}
}

// spec: §11.2 line 48 — a counter resets when its fixed window rolls; reads
// for a window other than the recorded one return 0.
func TestWindowRollResetsCounter_spec_11_2(t *testing.T) {
	a := New()
	a.Record("acme", "alice", quota.ResetHourly, fixed, 100)
	nextHour := fixed.Add(time.Hour)
	// Read for the next hour: window rolled, so 0.
	if got := a.UserWindow("acme", "alice", quota.ResetHourly, nextHour); got != 0 {
		t.Errorf("UserWindow next hour = %d, want 0", got)
	}
	// A record in the next hour starts a fresh counter.
	a.Record("acme", "alice", quota.ResetHourly, nextHour, 20)
	if got := a.UserWindow("acme", "alice", quota.ResetHourly, nextHour); got != 20 {
		t.Errorf("UserWindow after roll = %d, want 20", got)
	}
	// The original window's read is now 0 (the entry rolled).
	if got := a.UserWindow("acme", "alice", quota.ResetHourly, fixed); got != 0 {
		t.Errorf("UserWindow original window after roll = %d, want 0", got)
	}
}

// spec: §12.4 source (2) — the rolling period has no single restorable
// window, so Record and reads skip it (matching quotacheckpoint).
func TestRollingPeriodSkipped_spec_12_4(t *testing.T) {
	a := New()
	a.Record("acme", "alice", quota.ResetRolling, fixed, 100)
	if got := a.UserWindow("acme", "alice", quota.ResetRolling, fixed); got != 0 {
		t.Errorf("UserWindow(rolling) = %d, want 0", got)
	}
	if got := a.TenantRollup("acme", quota.ResetRolling, fixed); got != 0 {
		t.Errorf("TenantRollup(rolling) = %d, want 0", got)
	}
	if got := a.Len(); got != 0 {
		t.Errorf("Len after rolling record = %d, want 0", got)
	}
}

// Non-positive token deltas are no-ops.
func TestNonPositiveTokensIgnored(t *testing.T) {
	a := New()
	a.Record("acme", "alice", quota.ResetHourly, fixed, 0)
	a.Record("acme", "alice", quota.ResetHourly, fixed, -5)
	if got := a.UserWindow("acme", "alice", quota.ResetHourly, fixed); got != 0 {
		t.Errorf("UserWindow after non-positive records = %d, want 0", got)
	}
}

// spec: §11.2 line 48 — Snapshot lists every still-current window so the
// recovery reconcile can restore fail-open-only windows. It includes the
// per-user windows and the per-tenant rollup.
func TestSnapshotListsCurrentWindows_spec_11_2(t *testing.T) {
	a := New()
	a.Record("acme", "alice", quota.ResetHourly, fixed, 100)
	a.Record("globex", "carol", quota.ResetDaily, fixed, 40)

	got := map[string]int64{}
	for _, s := range a.Snapshot(fixed) {
		key := s.TenantID + "/" + s.UserID + "/" + string(s.Period)
		got[key] = s.Tokens
	}
	want := map[string]int64{
		"acme/alice/hourly":  100,
		"acme//hourly":       100, // tenant rollup
		"globex/carol/daily": 40,
		"globex//daily":      40, // tenant rollup
	}
	if len(got) != len(want) {
		t.Fatalf("Snapshot returned %d samples, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("Snapshot[%s] = %d, want %d", k, got[k], v)
		}
	}
}

// Snapshot omits windows that have already rolled relative to the read time.
func TestSnapshotOmitsRolledWindows(t *testing.T) {
	a := New()
	a.Record("acme", "alice", quota.ResetHourly, fixed, 100)
	if s := a.Snapshot(fixed.Add(time.Hour)); len(s) != 0 {
		t.Errorf("Snapshot for rolled window = %v, want empty", s)
	}
}

// spec: §12.4 source (2) — Sweep reclaims rolled-window entries; current
// entries survive.
func TestSweepReclaimsRolledEntries_spec_12_4(t *testing.T) {
	a := New()
	a.Record("acme", "alice", quota.ResetHourly, fixed, 100) // alice + rollup = 2 entries
	a.Record("acme", "bob", quota.ResetHourly, fixed.Add(time.Hour), 50)
	// Sweep at the second hour: alice's window rolled (entry + its rollup
	// share the same window, but bob recorded in the second hour so the
	// rollup is at the second-hour label). Sweep at second hour drops the
	// first-hour alice user entry.
	before := a.Len()
	removed := a.Sweep(fixed.Add(time.Hour))
	if removed == 0 {
		t.Errorf("Sweep removed 0 entries, want >0 (had %d entries)", before)
	}
	// alice's first-hour window is gone.
	if got := a.UserWindow("acme", "alice", quota.ResetHourly, fixed); got != 0 {
		t.Errorf("UserWindow(alice) after sweep = %d, want 0", got)
	}
	// bob's second-hour window survives.
	if got := a.UserWindow("acme", "bob", quota.ResetHourly, fixed.Add(time.Hour)); got != 50 {
		t.Errorf("UserWindow(bob) after sweep = %d, want 50", got)
	}
}

// A nil accumulator is safe to use everywhere (the recorder may not wire one).
func TestNilAccumulatorSafe(t *testing.T) {
	var a *Accumulator
	a.Record("acme", "alice", quota.ResetHourly, fixed, 100) // no panic
	if got := a.UserWindow("acme", "alice", quota.ResetHourly, fixed); got != 0 {
		t.Errorf("nil UserWindow = %d, want 0", got)
	}
	if got := a.TenantRollup("acme", quota.ResetHourly, fixed); got != 0 {
		t.Errorf("nil TenantRollup = %d, want 0", got)
	}
	if got := a.Snapshot(fixed); got != nil {
		t.Errorf("nil Snapshot = %v, want nil", got)
	}
	if got := a.Sweep(fixed); got != 0 {
		t.Errorf("nil Sweep = %d, want 0", got)
	}
	if got := a.Len(); got != 0 {
		t.Errorf("nil Len = %d, want 0", got)
	}
}

// The map key decode round-trips tenant and user ids (including the empty
// rollup user) so Snapshot reports the right subjects.
func TestKeyDecodeRoundTrip(t *testing.T) {
	cases := []struct{ tenant, user string }{
		{"acme", "alice"},
		{"acme", ""},
		{"t-with-dashes", "u_underscore"},
	}
	for _, c := range cases {
		k := key(c.tenant, c.user, quota.ResetHourly)
		if got := tenantOf(k); got != c.tenant {
			t.Errorf("tenantOf(%q) = %q, want %q", k, got, c.tenant)
		}
		if got := userOf(k); got != c.user {
			t.Errorf("userOf(%q) = %q, want %q", k, got, c.user)
		}
	}
}
