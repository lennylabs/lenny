// SPDX-License-Identifier: MIT

package integrity

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// fakeRows feeds a fixed set of single-column string rows to the
// catalog-coverage query under test. The embedded pgx.Rows is nil; only
// the four methods TenantGuardCoverageGaps calls (Next/Scan/Close/Err)
// are overridden, so any other call would panic — which is the intent,
// since the function must not reach for them.
type fakeRows struct {
	pgx.Rows
	vals    []string
	idx     int
	scanErr error
	iterErr error
}

func (f *fakeRows) Next() bool {
	if f.idx >= len(f.vals) {
		return false
	}
	f.idx++
	return true
}

func (f *fakeRows) Scan(dest ...any) error {
	if f.scanErr != nil {
		return f.scanErr
	}
	*dest[0].(*string) = f.vals[f.idx-1]
	return nil
}

func (f *fakeRows) Close()     {}
func (f *fakeRows) Err() error { return f.iterErr }

type fakeQuerier struct {
	rows     *fakeRows
	queryErr error
	gotSQL   string
	calls    int
}

func (q *fakeQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	q.calls++
	q.gotSQL = sql
	if q.queryErr != nil {
		return nil, q.queryErr
	}
	return q.rows, nil
}

func (q *fakeQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	panic("QueryRow is not used by the pooler-defense checks")
}

// spec: §12.3 line 56 — the coverage check must surface every
// tenant-scoped table missing the lenny_tenant_guard trigger.
func TestTenantGuardCoverageGapsSpec123Line56ReturnsMissingTables(t *testing.T) {
	q := &fakeQuerier{rows: &fakeRows{vals: []string{"session_eviction_state", "users"}}}
	gaps, err := TenantGuardCoverageGaps(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gaps) != 2 || gaps[0] != "session_eviction_state" || gaps[1] != "users" {
		t.Fatalf("gaps = %v, want [session_eviction_state users]", gaps)
	}
	// The query must read the live catalog, not a hard-coded list, and
	// scope to RLS-enabled, tenant_id-bearing tables (so platform-global
	// tables are excluded) lacking the trigger.
	for _, frag := range []string{"pg_trigger", "lenny_tenant_guard", "relrowsecurity", "tenant_id"} {
		if !strings.Contains(q.gotSQL, frag) {
			t.Errorf("coverage query missing %q fragment:\n%s", frag, q.gotSQL)
		}
	}
}

// spec: §12.3 line 56 — an empty gap set means every tenant-scoped
// table is protected.
func TestTenantGuardCoverageGapsSpec123Line56EmptyWhenAllCovered(t *testing.T) {
	q := &fakeQuerier{rows: &fakeRows{vals: nil}}
	gaps, err := TenantGuardCoverageGaps(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gaps) != 0 {
		t.Fatalf("gaps = %v, want empty", gaps)
	}
}

func TestTenantGuardCoverageGapsQueryError(t *testing.T) {
	sentinel := errors.New("boom")
	q := &fakeQuerier{queryErr: sentinel}
	if _, err := TenantGuardCoverageGaps(context.Background(), q); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrapped %v", err, sentinel)
	}
}

func TestTenantGuardCoverageGapsScanError(t *testing.T) {
	sentinel := errors.New("scan-fail")
	q := &fakeQuerier{rows: &fakeRows{vals: []string{"x"}, scanErr: sentinel}}
	if _, err := TenantGuardCoverageGaps(context.Background(), q); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrapped %v", err, sentinel)
	}
}

func TestTenantGuardCoverageGapsIterError(t *testing.T) {
	sentinel := errors.New("iter-fail")
	q := &fakeQuerier{rows: &fakeRows{vals: []string{"x"}, iterErr: sentinel}}
	if _, err := TenantGuardCoverageGaps(context.Background(), q); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrapped %v", err, sentinel)
	}
}

// spec: §12.3 line 56 — outside external pooler mode the check is a
// no-op and must not even query the catalog (the in-cluster pooler
// enforces the connect_query sentinel).
func TestVerifyCloudManagedPoolerDefenseSpec123Line56NoopWhenNotExternal(t *testing.T) {
	for _, mode := range []string{"transactional", "", "pgbouncer"} {
		q := &fakeQuerier{rows: &fakeRows{vals: []string{"users"}}}
		if err := VerifyCloudManagedPoolerDefense(context.Background(), q, mode); err != nil {
			t.Fatalf("mode %q: unexpected error: %v", mode, err)
		}
		if q.calls != 0 {
			t.Fatalf("mode %q: querier called %d times, want 0", mode, q.calls)
		}
	}
}

// spec: §12.3 line 56 — under external mode a missing trigger makes the
// gateway refuse to start with the verbatim fatal message.
func TestVerifyCloudManagedPoolerDefenseSpec123Line56FatalWhenTriggerAbsent(t *testing.T) {
	q := &fakeQuerier{rows: &fakeRows{vals: []string{"sessions"}}}
	err := VerifyCloudManagedPoolerDefense(context.Background(), q, "external")
	if err == nil {
		t.Fatal("expected a fatal error when the trigger is absent under external mode")
	}
	if err.Error() != CloudManagedPoolerFatalMessage {
		t.Fatalf("error = %q, want the verbatim spec message %q", err.Error(), CloudManagedPoolerFatalMessage)
	}
	// Guard the spec-literal substrings so a future edit cannot quietly
	// drift the operator-facing remediation text.
	for _, frag := range []string{
		"LENNY_POOLER_MODE=external",
		"lenny_tenant_guard trigger is absent",
		"run schema migrations",
		"(Section 12.3)",
	} {
		if !strings.Contains(CloudManagedPoolerFatalMessage, frag) {
			t.Errorf("fatal message missing spec fragment %q", frag)
		}
	}
}

// spec: §12.3 line 56 — under external mode with full coverage the
// gateway starts.
func TestVerifyCloudManagedPoolerDefenseSpec123Line56PassWhenCovered(t *testing.T) {
	q := &fakeQuerier{rows: &fakeRows{vals: nil}}
	if err := VerifyCloudManagedPoolerDefense(context.Background(), q, "external"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifyCloudManagedPoolerDefensePropagatesQueryError(t *testing.T) {
	sentinel := errors.New("db-down")
	q := &fakeQuerier{queryErr: sentinel}
	if err := VerifyCloudManagedPoolerDefense(context.Background(), q, "external"); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrapped %v", err, sentinel)
	}
}
