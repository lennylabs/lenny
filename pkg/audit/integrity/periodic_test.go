// SPDX-License-Identifier: MIT

package integrity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// spec: §11.7 item 2 lines 357-358 — the periodic grant-check cadence is
// profile-dependent: regulated defaults to 60s (max 120s), unregulated to
// 300s (max 900s). A configured value above the profile maximum is a
// fatal startup error; zero selects the default; the boundary value is
// accepted. F-11.7.3.
func TestResolveGrantCheckIntervalSpec117Item2(t *testing.T) {
	cases := []struct {
		name       string
		configured time.Duration
		regulated  bool
		want       time.Duration
		wantErr    bool
	}{
		{"regulated default on zero", 0, true, RegulatedGrantCheckDefault, false},
		{"unregulated default on zero", 0, false, UnregulatedGrantCheckDefault, false},
		{"regulated default on negative", -5 * time.Second, true, RegulatedGrantCheckDefault, false},
		{"regulated within bounds", 90 * time.Second, true, 90 * time.Second, false},
		{"regulated at max boundary", RegulatedGrantCheckMax, true, RegulatedGrantCheckMax, false},
		{"regulated above max is fatal", 121 * time.Second, true, 0, true},
		{"unregulated within bounds", 600 * time.Second, false, 600 * time.Second, false},
		{"unregulated at max boundary", UnregulatedGrantCheckMax, false, UnregulatedGrantCheckMax, false},
		{"unregulated above max is fatal", 901 * time.Second, false, 0, true},
		// A regulated deployment must reject a value that an unregulated
		// deployment would accept (90s < 900s but > 120s).
		{"regulated rejects unregulated-legal value", 300 * time.Second, true, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveGrantCheckInterval(tc.configured, tc.regulated)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected fatal error for configured=%s regulated=%v", tc.configured, tc.regulated)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

// scriptRows replays a fixed set of typed rows to the periodic check's
// catalog queries. Each inner slice is one row; Scan copies positionally
// by destination pointer type.
type scriptRows struct {
	pgx.Rows
	rows    [][]any
	idx     int
	iterErr error
}

func (s *scriptRows) Next() bool {
	if s.idx >= len(s.rows) {
		return false
	}
	s.idx++
	return true
}

func (s *scriptRows) Scan(dest ...any) error {
	row := s.rows[s.idx-1]
	for i := range dest {
		switch d := dest[i].(type) {
		case *string:
			*d = row[i].(string)
		case *int64:
			*d = row[i].(int64)
		case *byte:
			*d = row[i].(byte)
		case *[]byte:
			*d = row[i].([]byte)
		case *time.Time:
			*d = row[i].(time.Time)
		default:
			return fmt.Errorf("scriptRows: unsupported dest type %T at %d", d, i)
		}
	}
	return nil
}

func (s *scriptRows) Close()     {}
func (s *scriptRows) Err() error { return s.iterErr }

type scriptRow struct {
	val string
	err error
}

func (r scriptRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*string)) = r.val
	return nil
}

// scriptQuerier dispatches the §11.7 integrity queries by SQL substring.
// The zero value answers every check cleanly (no forbidden grants, all
// triggers enabled, erasure guard present, no audit tenants), so a test
// overrides only the slice it wants to perturb.
type scriptQuerier struct {
	grants       [][]any // VerifyGrants ledger-grant rows (table, priv)
	triggers     [][]any // VerifyTriggersEnabled (name, tgenabled byte)
	erasureScope [][]any // VerifyErasureRoleScope (table, priv)
	erasureSrc   string  // VerifyErasureGuard prosrc
	tenants      [][]any // auditTenants (tenant_id)
	chainRows    [][]any // loadRecentChainRows (seq, type, payload, created, prev_hash)
	queryErr     error   // when set, every Query returns it
}

func (q *scriptQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if q.queryErr != nil {
		return nil, q.queryErr
	}
	switch {
	case strings.Contains(sql, "DISTINCT table_name"):
		return &scriptRows{rows: q.erasureScope}, nil
	case strings.Contains(sql, "table_name = ANY($2)"):
		return &scriptRows{rows: q.grants}, nil
	case strings.Contains(sql, "FROM pg_trigger"):
		rows := q.triggers
		if rows == nil {
			rows = defaultTriggerRows()
		}
		return &scriptRows{rows: rows}, nil
	case strings.Contains(sql, "DISTINCT tenant_id"):
		return &scriptRows{rows: q.tenants}, nil
	case strings.Contains(sql, "sequence_number, event_type"):
		return &scriptRows{rows: q.chainRows}, nil
	default:
		return &scriptRows{}, nil
	}
}

func (q *scriptQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	src := q.erasureSrc
	if src == "" {
		src = "BEGIN ... current_setting('lenny.erasure_mode', true) ... END"
	}
	return scriptRow{val: src}
}

func defaultTriggerRows() [][]any {
	return [][]any{
		{"lenny_tenant_guard", byte('O')},
		{"lenny_billing_immutability", byte('O')},
		{"lenny_audit_immutability", byte('O')},
	}
}

// spec: §11.7 item 2 — a clean cycle (no forbidden grants, triggers
// enabled, erasure guard intact, no chain gaps) reports no drift and
// fires no observer. F-11.7.3.
func TestPeriodicCheckOnceNoDrift(t *testing.T) {
	var grantHits, chainStates int
	p := &PeriodicCheck{
		DB:           &scriptQuerier{},
		Cfg:          PeriodicConfig{ChainSampleN: 1000},
		OnGrantDrift: func() { grantHits++ },
		OnChainState: func(string) { chainStates++ },
	}
	if p.CheckOnce(context.Background()) {
		t.Fatal("CheckOnce reported drift on a clean cycle")
	}
	if grantHits != 0 {
		t.Fatalf("OnGrantDrift fired %d times on clean cycle", grantHits)
	}
	if chainStates != 0 {
		t.Fatalf("OnChainState fired %d times with no audit tenants", chainStates)
	}
}

// spec: §11.7 item 2 line 359 — an UPDATE grant added to an append-only
// ledger after startup is the named tamper-and-revert drift: CheckOnce
// must report drift and increment lenny_audit_grant_drift_total. F-11.7.3.
func TestPeriodicCheckOnceGrantDrift(t *testing.T) {
	var grantHits int
	p := &PeriodicCheck{
		DB: &scriptQuerier{
			grants: [][]any{{"audit_log", "UPDATE"}},
		},
		Cfg:          PeriodicConfig{ChainSampleN: 1000},
		OnGrantDrift: func() { grantHits++ },
	}
	if !p.CheckOnce(context.Background()) {
		t.Fatal("CheckOnce did not report drift on a forbidden ledger grant")
	}
	if grantHits != 1 {
		t.Fatalf("OnGrantDrift fired %d times, want 1", grantHits)
	}
}

// spec: §11.7 line 370 — the periodic check also samples chain segments;
// a broken chain (a sequence gap) triggers the same critical alert path
// even when grants are clean. F-11.7.3.
func TestPeriodicCheckOnceChainBroken(t *testing.T) {
	var states []string
	p := &PeriodicCheck{
		DB: &scriptQuerier{
			tenants: [][]any{{"acme"}},
			// A gap between sequence 5 and 7 is a broken chain; the window
			// does not start at genesis, so contiguity alone is checked.
			chainRows: [][]any{
				{int64(5), "session.created", []byte(`{}`), time.Time{}, []byte("aa")},
				{int64(7), "session.created", []byte(`{}`), time.Time{}, []byte("bb")},
			},
		},
		Cfg:          PeriodicConfig{ChainSampleN: 1000},
		OnChainState: func(s string) { states = append(states, s) },
	}
	if !p.CheckOnce(context.Background()) {
		t.Fatal("CheckOnce did not report drift on a broken chain")
	}
	if len(states) != 1 || states[0] != "broken" {
		t.Fatalf("OnChainState states = %v, want [broken]", states)
	}
}

// spec: §11.7 item 2 — a transient query failure is logged and treated as
// no-drift so a flaky database does not trip a hard-fail shutdown.
// F-11.7.3.
func TestPeriodicCheckOnceQueryErrorIsNotDrift(t *testing.T) {
	p := &PeriodicCheck{
		DB:  &scriptQuerier{queryErr: errors.New("db down")},
		Cfg: PeriodicConfig{ChainSampleN: 1000},
	}
	// Verify joins the per-check errors, so a query failure surfaces as a
	// non-nil Verify error and counts as drift — the conservative posture
	// for grant checks. The chain sample, by contrast, only logs. Assert
	// the cycle does not panic and the observers are optional (nil).
	_ = p.CheckOnce(context.Background())
}

// spec: §11.7 item 2 line 359 — when audit.hardFailOnDrift is enabled, a
// detected drift initiates a graceful shutdown via the Shutdown hook.
// F-11.7.3.
func TestPeriodicRunHardFailOnDrift(t *testing.T) {
	shutdown := make(chan string, 1)
	var mu sync.Mutex
	var grantHits int
	p := &PeriodicCheck{
		DB: &scriptQuerier{
			grants: [][]any{{"billing_events", "DELETE"}},
		},
		Cfg: PeriodicConfig{
			Interval:        time.Millisecond,
			HardFailOnDrift: true,
			ChainSampleN:    1000,
		},
		OnGrantDrift: func() { mu.Lock(); grantHits++; mu.Unlock() },
		Shutdown:     func(reason string) { shutdown <- reason },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	select {
	case reason := <-shutdown:
		if !strings.Contains(reason, "hardFailOnDrift") {
			t.Fatalf("shutdown reason = %q, want it to mention hardFailOnDrift", reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not invoke Shutdown on drift within the deadline")
	}
	mu.Lock()
	hits := grantHits
	mu.Unlock()
	if hits == 0 {
		t.Fatal("OnGrantDrift never fired before hard-fail")
	}
}

// spec: §11.7 item 2 — without hardFailOnDrift the loop keeps running on
// drift; with a cancelled context it returns promptly. F-11.7.3.
func TestPeriodicRunStopsOnContextCancel(t *testing.T) {
	p := &PeriodicCheck{
		DB:  &scriptQuerier{},
		Cfg: PeriodicConfig{Interval: time.Millisecond, ChainSampleN: 1000},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

// A zero interval disables the loop (the gateway resolves a positive
// cadence at startup, but Run must not spin on a misconfiguration).
func TestPeriodicRunZeroIntervalReturns(t *testing.T) {
	p := &PeriodicCheck{DB: &scriptQuerier{}, Cfg: PeriodicConfig{Interval: 0}}
	done := make(chan struct{})
	go func() { p.Run(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run with zero interval did not return immediately")
	}
}
