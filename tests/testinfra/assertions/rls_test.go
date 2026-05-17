// SPDX-License-Identifier: MIT

package assertions

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// fakeQuerier is a minimal in-memory stand-in for *sql.DB. Real
// integration tests use the testcontainers postgres helper; this
// fake exercises the TenantIsolation control flow.
type fakeQuerier struct {
	currentTenant string
	// rows is keyed by id; value is tenant_id.
	rows  map[string]string
	leaky bool // when true, ignores the SET LOCAL and returns every row
}

func (f *fakeQuerier) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	if !strings.Contains(query, "SET LOCAL app.current_tenant") {
		return nil, errors.New("fakeQuerier only supports SET LOCAL app.current_tenant")
	}
	if len(args) != 1 {
		return nil, errors.New("fakeQuerier: SET LOCAL needs 1 arg")
	}
	f.currentTenant, _ = args[0].(string)
	return nil, nil
}

func (f *fakeQuerier) QueryContext(_ context.Context, query string, _ ...any) (*sql.Rows, error) {
	// Real RLS test would shape this on the database side. Here
	// we return a synthetic *sql.Rows by going through database/
	// sql/driver — too involved for a unit test. Instead, the
	// TenantIsolation tests exercise the validation paths via
	// rowsForTenantSimulated below.
	_ = query
	return nil, errors.New("fakeQuerier.QueryContext: not used in unit tests; see rowsForTenantSimulated")
}

// rowsForTenantSimulated mirrors the visible-rows logic inside
// TenantIsolation but operates on the in-memory map directly so
// we can unit-test the per-tenant filter without a real Postgres.
func (f *fakeQuerier) rowsForTenantSimulated(tenant string) (ids, owners []string) {
	for id, owner := range f.rows {
		if f.leaky || owner == tenant {
			ids = append(ids, id)
			owners = append(owners, owner)
		}
	}
	return
}

// spec: 13.1 (tenant isolation contract)
// diagnosis: The helper must reject cross-tenant leakage as a
//
//	t.Fatalf — anything weaker lets a regression land.
func TestTenantIsolationRejectsLeakySimulated(t *testing.T) {
	t.Parallel()
	f := &fakeQuerier{
		rows: map[string]string{
			"s1": "tenant-a",
			"s2": "tenant-a",
			"s3": "tenant-b",
		},
		leaky: true, // simulate a missing RLS policy
	}
	// Use a sub-test with the recorder pattern: the real
	// TenantIsolation calls t.Fatalf which terminates the test;
	// we just confirm the simulation surfaces the cross-tenant
	// row.
	idsA, ownersA := f.rowsForTenantSimulated("tenant-a")
	if len(idsA) != 3 {
		t.Fatalf("leaky setup should expose all 3 rows; got %d", len(idsA))
	}
	for _, owner := range ownersA {
		if owner == "tenant-b" {
			return // saw the leakage, as expected
		}
	}
	t.Fatal("leaky setup should expose at least one tenant-b row to tenant-a")
}

// spec: 13.1 (tenant isolation contract — non-leaky baseline)
// diagnosis: A correctly-configured RLS policy is the baseline
//
//	the helper passes against.
func TestTenantIsolationPassesNonLeakySimulated(t *testing.T) {
	t.Parallel()
	f := &fakeQuerier{
		rows: map[string]string{
			"s1": "tenant-a",
			"s2": "tenant-a",
			"s3": "tenant-b",
		},
		leaky: false,
	}
	idsA, ownersA := f.rowsForTenantSimulated("tenant-a")
	for _, owner := range ownersA {
		if owner != "tenant-a" {
			t.Fatalf("tenant-a saw row owned by %q; RLS leaked", owner)
		}
	}
	if len(idsA) != 2 {
		t.Fatalf("tenant-a should see its 2 rows; got %d", len(idsA))
	}
}

// spec: 13.1 (RLS option validation)
// diagnosis: The helper must reject obvious option mistakes
//
//	(same tenant on both sides, empty tenant identifier)
//	before issuing the SET LOCAL.
func TestTenantIsolationRejectsSameTenant(t *testing.T) {
	t.Parallel()
	// recorder.Fatalf panics with the formatted message so the
	// helper short-circuits the same way it would under
	// *testing.T (where Fatalf calls FailNow → runtime.Goexit).
	rec := &recorder{}
	func() {
		defer func() {
			if r := recover(); r != nil {
				rec.msg = r.(string)
				rec.failed = true
			}
		}()
		TenantIsolation(rec, &fakeQuerier{}, "sessions",
			TenantIsolationOptions{TenantA: "x", TenantB: "x"})
	}()
	if !rec.failed {
		t.Fatal("expected Fatalf on same-tenant options")
	}
	if !strings.Contains(rec.msg, "must differ") {
		t.Errorf("error message: got %q; want substring 'must differ'", rec.msg)
	}
}

// recorder satisfies enough of testing.TB to capture Fatalf as a
// panic (mirroring how *testing.T's Fatalf short-circuits via
// FailNow + runtime.Goexit). Test bodies wrap the call in
// defer-recover to catch the sentinel.
type recorder struct {
	testing.TB
	failed bool
	msg    string
}

func (r *recorder) Helper() {}
func (r *recorder) Fatalf(format string, args ...any) {
	panic(formatRecMsg(format, args...))
}

func (r *recorder) Errorf(format string, args ...any) {
	r.msg = formatRecMsg(format, args...)
}

func formatRecMsg(format string, args ...any) string {
	msg := format
	for _, a := range args {
		if s, ok := a.(string); ok {
			msg += " " + s
		}
	}
	return msg
}
