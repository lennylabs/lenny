// SPDX-License-Identifier: MIT

package pgtenant

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// spec: §4.2 line 163 / §12.3 line 53 — tenant ID format.
// The InTx / InAllTenants helpers now interpolate the value into a
// SET LOCAL statement (SET LOCAL has no parameter binding), so the
// regex is the load-bearing guard against SQL injection.
func TestTenantIDPatternAccepts(t *testing.T) {
	t.Parallel()
	valid := []string{
		"acme",
		"default",
		"tenant-a",
		"tenant_b",
		"a",
		strings.Repeat("a", 128),
		"ABC123_-",
	}
	for _, id := range valid {
		if !tenantIDPattern.MatchString(id) {
			t.Errorf("tenantIDPattern rejected %q, want accept", id)
		}
	}
}

// spec: §4.2 line 163 / §12.3 line 53 — adversarial tenant IDs.
// Every classic SQL-injection vector must be rejected client-side
// before any SET LOCAL statement runs.
func TestTenantIDPatternRejectsInjection(t *testing.T) {
	t.Parallel()
	hostile := []string{
		"",
		" ",
		"acme; DROP TABLE sessions; --",
		"acme' OR '1'='1",
		`acme"; SELECT * FROM tenants; --`,
		"acme\nDROP TABLE",
		"a b",
		"acme/admin",
		"alice'; --",     // example from the §4.2.16 closure brief
		strings.Repeat("a", 129),
		"αβγ",
	}
	for _, id := range hostile {
		if tenantIDPattern.MatchString(id) {
			t.Errorf("tenantIDPattern accepted %q, want reject", id)
		}
	}
}

// spec: §4.2 line 163 — the sentinel values __unset__ and __all__
// satisfy the regex format (only [A-Za-z0-9_-]) but InTx rejects
// them anyway because the helper is for concrete tenant IDs. The
// __all__ sentinel must reach the DB only via InAllTenants, which
// pairs it with the lenny.allow_all_sentinel opt-in.
func TestInTxRejectsSentinelTenantIDs(t *testing.T) {
	t.Parallel()
	for _, id := range []string{AllTenantsSentinel, "__unset__"} {
		err := InTx(t.Context(), nil, id, func(_ pgx.Tx) error {
			t.Fatalf("fn must not be called for sentinel %q", id)
			return nil
		})
		if !errors.Is(err, ErrInvalidTenantID) {
			t.Errorf("InTx(%q) = %v, want ErrInvalidTenantID", id, err)
		}
	}
}

// spec: §4.2 line 163 — quoteTenantID renders the validated tenant
// ID as a SQL string literal. The regex already rejects single
// quotes; this verifies the double-escape is in place as
// defense-in-depth.
func TestQuoteTenantID(t *testing.T) {
	t.Parallel()
	got := quoteTenantID("acme")
	if got != "'acme'" {
		t.Errorf("quoteTenantID(acme) = %q, want 'acme'", got)
	}
	// The regex rejects single quotes, but the helper still doubles
	// them defensively.
	got = quoteTenantID("a'b")
	if got != "'a''b'" {
		t.Errorf("quoteTenantID(a'b) = %q, want 'a''b'", got)
	}
}

// spec: §4.2 line 163 — adversarial tenant IDs must fail closed
// with ErrInvalidTenantID before any pool interaction. The helper
// returns the sentinel even with a nil pool to confirm the regex
// short-circuits before reaching Begin().
func TestInTxRejectsInvalidTenantIDBeforeBegin(t *testing.T) {
	t.Parallel()
	hostile := []string{
		"",
		"acme'; DROP TABLE sessions; --",
		"acme' OR '1'='1",
		"alice'; --",
		strings.Repeat("x", 200),
	}
	for _, id := range hostile {
		err := InTx(t.Context(), nil, id, func(_ pgx.Tx) error {
			t.Fatalf("fn must not be called for invalid tenant %q", id)
			return nil
		})
		if !errors.Is(err, ErrInvalidTenantID) {
			t.Errorf("InTx(%q) = %v, want ErrInvalidTenantID", id, err)
		}
	}
}

// spec: §12.6 — NullTime maps a zero time.Time to nil so a nullable
// TIMESTAMPTZ column distinguishes "unset" from a real instant.
func TestNullTimeRoundtrip(t *testing.T) {
	t.Parallel()
	if NullTime(time.Time{}) != nil {
		t.Errorf("NullTime(zero) = non-nil, want nil")
	}
	now := time.Now()
	got := NullTime(now)
	if got == nil || !got.Equal(now) {
		t.Errorf("NullTime(now) = %v, want %v", got, now)
	}
}

// spec: §12.6 — NullString maps an empty string to nil.
func TestNullStringRoundtrip(t *testing.T) {
	t.Parallel()
	if NullString("") != nil {
		t.Errorf("NullString(empty) = non-nil, want nil")
	}
	got := NullString("hi")
	if got == nil || *got != "hi" {
		t.Errorf("NullString(hi) = %v, want hi", got)
	}
}

// spec: §4.2 line 156 — MonotonicNext returns prev+1µs when now has
// not advanced past prev, so updated_at strictly advances even when
// two writes land within the same microsecond.
func TestMonotonicNext(t *testing.T) {
	t.Parallel()
	prev := time.Date(2026, 1, 1, 0, 0, 0, 123456000, time.UTC)
	earlier := prev.Add(-time.Second)
	got := MonotonicNext(prev, earlier)
	if !got.After(prev) {
		t.Errorf("MonotonicNext past-now = %v, want strictly > prev", got)
	}
	if got.Sub(prev) != time.Microsecond {
		t.Errorf("MonotonicNext past-now delta = %v, want 1µs", got.Sub(prev))
	}
	later := prev.Add(2 * time.Second)
	got = MonotonicNext(prev, later)
	if !got.Equal(later.UTC().Truncate(time.Microsecond)) {
		t.Errorf("MonotonicNext future-now = %v, want %v", got, later.UTC().Truncate(time.Microsecond))
	}
}
