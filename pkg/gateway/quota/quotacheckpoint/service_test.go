// SPDX-License-Identifier: MIT

package quotacheckpoint_test

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/quota/quotacheckpoint"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotastore"
	"github.com/lennylabs/lenny/pkg/quota"
)

// fixedNow is the injectable clock used across the suite so the window
// labels the Service computes are deterministic.
var fixedNow = time.Date(2026, 6, 2, 14, 30, 0, 0, time.UTC)

func clock() time.Time { return fixedNow }

// counterKey identifies a window in the fake Redis counter: subject "" is
// the per-tenant rollup.
type counterKey struct {
	tenant, subject string
	period          quota.ResetPeriod
}

// fakeCounter is an in-memory WindowReader + CounterRestorer; reads and
// restores share state so a reconcile's MAX rule and InMemoryValue are
// observable.
type fakeCounter struct {
	m map[counterKey]int64
}

func newFakeCounter() *fakeCounter { return &fakeCounter{m: map[counterKey]int64{}} }

func (f *fakeCounter) Usage(_ context.Context, tenant, user string, period quota.ResetPeriod, _ time.Time) (int64, error) {
	return f.m[counterKey{tenant, user, period}], nil
}

func (f *fakeCounter) TenantRollupUsage(_ context.Context, tenant string, period quota.ResetPeriod, _ time.Time) (int64, error) {
	return f.m[counterKey{tenant, "", period}], nil
}

func (f *fakeCounter) RestoreUserWindow(_ context.Context, tenant, user string, period quota.ResetPeriod, _ time.Time, v int64) (int64, error) {
	k := counterKey{tenant, user, period}
	if v > f.m[k] {
		f.m[k] = v
	}
	return f.m[k], nil
}

func (f *fakeCounter) RestoreTenantRollupWindow(_ context.Context, tenant string, period quota.ResetPeriod, _ time.Time, v int64) (int64, error) {
	k := counterKey{tenant, "", period}
	if v > f.m[k] {
		f.m[k] = v
	}
	return f.m[k], nil
}

// fakeStore is an in-memory quotacheckpoint.Store keyed by the table PK.
type fakeStore struct {
	rows map[string]quotacheckpoint.Row
}

func newFakeStore() *fakeStore { return &fakeStore{rows: map[string]quotacheckpoint.Row{}} }

func pk(r quotacheckpoint.Row) string {
	return r.TenantID + "|" + r.Scope + "|" + r.SubjectID + "|" + r.Period + "|" + r.WindowLabel
}

func (s *fakeStore) Write(_ context.Context, rows []quotacheckpoint.Row) error {
	for _, r := range rows {
		r.CheckpointAt = fixedNow
		s.rows[pk(r)] = r
	}
	return nil
}

func (s *fakeStore) ListActive(_ context.Context) ([]quotacheckpoint.Row, error) {
	return s.all(), nil
}

func (s *fakeStore) ListByTenant(_ context.Context, tenantID string) ([]quotacheckpoint.Row, error) {
	var out []quotacheckpoint.Row
	for _, r := range s.all() {
		if r.TenantID == tenantID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *fakeStore) DeleteByUser(_ context.Context, tenantID, userID string) (int, error) {
	n := 0
	for k, r := range s.rows {
		if r.TenantID == tenantID && r.Scope == quotacheckpoint.ScopeUser && r.SubjectID == userID {
			delete(s.rows, k)
			n++
		}
	}
	return n, nil
}

func (s *fakeStore) DeleteByTenant(_ context.Context, tenantID string) (int, error) {
	n := 0
	for k, r := range s.rows {
		if r.TenantID == tenantID {
			delete(s.rows, k)
			n++
		}
	}
	return n, nil
}

func (s *fakeStore) all() []quotacheckpoint.Row {
	out := make([]quotacheckpoint.Row, 0, len(s.rows))
	for _, r := range s.rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return pk(out[i]) < pk(out[j]) })
	return out
}

// fakeSubjects returns a fixed subject set.
type fakeSubjects []quotacheckpoint.Subject

func (f fakeSubjects) ListActiveSubjects(context.Context) ([]quotacheckpoint.Subject, error) {
	return []quotacheckpoint.Subject(f), nil
}

// fakePeriods maps tenants to reset periods (default hourly).
type fakePeriods map[string]quota.ResetPeriod

func (f fakePeriods) ResolvePeriod(_ context.Context, tenantID string) (quota.ResetPeriod, error) {
	if p, ok := f[tenantID]; ok {
		return p, nil
	}
	return quota.ResetHourly, nil
}

// fakeMetrics counts reconcile outcomes.
type fakeMetrics struct{ counts map[string]int }

func (m *fakeMetrics) IncQuotaCheckpointReconcile(outcome string) { m.counts[outcome]++ }

func label(t *testing.T, period quota.ResetPeriod) string {
	t.Helper()
	l, err := quotastore.WindowLabel(period, fixedNow)
	if err != nil {
		t.Fatalf("WindowLabel: %v", err)
	}
	return l
}

// spec: §11.2 line 44 — Checkpoint persists each active (tenant, user)
// window total and a single deduped per-tenant rollup row; a zero window
// and a rolling-period tenant contribute no rows.
func TestCheckpoint_spec_11_2_line44(t *testing.T) {
	t.Parallel()
	cnt := newFakeCounter()
	cnt.m[counterKey{"acme", "alice@acme.com", quota.ResetHourly}] = 100
	cnt.m[counterKey{"acme", "bob@acme.com", quota.ResetHourly}] = 200
	cnt.m[counterKey{"acme", "", quota.ResetHourly}] = 300
	cnt.m[counterKey{"globex", "carol@globex.com", quota.ResetDaily}] = 0 // zero → skip user row
	cnt.m[counterKey{"globex", "", quota.ResetDaily}] = 50
	cnt.m[counterKey{"initech", "dave@initech.com", quota.ResetRolling}] = 999 // rolling → skip

	store := newFakeStore()
	svc := &quotacheckpoint.Service{
		Store: store,
		Subjects: fakeSubjects{
			{TenantID: "acme", UserID: "alice@acme.com"},
			{TenantID: "acme", UserID: "bob@acme.com"},
			{TenantID: "globex", UserID: "carol@globex.com"},
			{TenantID: "initech", UserID: "dave@initech.com"},
		},
		Periods:  fakePeriods{"globex": quota.ResetDaily, "initech": quota.ResetRolling},
		Reader:   cnt,
		Restorer: cnt,
		Now:      clock,
	}
	svc.Checkpoint(context.Background())

	want := map[string]int64{
		"acme|user|alice@acme.com|hourly|" + label(t, quota.ResetHourly): 100,
		"acme|user|bob@acme.com|hourly|" + label(t, quota.ResetHourly):   200,
		"acme|tenant||hourly|" + label(t, quota.ResetHourly):             300,
		"globex|tenant||daily|" + label(t, quota.ResetDaily):             50,
	}
	got := store.all()
	if len(got) != len(want) {
		t.Fatalf("checkpoint wrote %d rows, want %d: %+v", len(got), len(want), got)
	}
	for _, r := range got {
		w, ok := want[pk(r)]
		if !ok {
			t.Errorf("unexpected row %q", pk(r))
			continue
		}
		if r.TokenTotal != w {
			t.Errorf("row %q total = %d, want %d", pk(r), r.TokenTotal, w)
		}
	}
}

// spec: §11.2 line 44 — CheckpointSubject writes the final reconciliation
// for one (tenant, user) on session completion.
func TestCheckpointSubject_spec_11_2_line44(t *testing.T) {
	t.Parallel()
	cnt := newFakeCounter()
	cnt.m[counterKey{"acme", "alice@acme.com", quota.ResetHourly}] = 777
	cnt.m[counterKey{"acme", "", quota.ResetHourly}] = 1500
	store := newFakeStore()
	svc := &quotacheckpoint.Service{
		Store: store, Periods: fakePeriods{}, Reader: cnt, Now: clock,
	}
	if err := svc.CheckpointSubject(context.Background(), "acme", "alice@acme.com"); err != nil {
		t.Fatalf("CheckpointSubject: %v", err)
	}
	rows := store.all()
	if len(rows) != 2 {
		t.Fatalf("final checkpoint wrote %d rows, want 2 (user + tenant): %+v", len(rows), rows)
	}
}

// spec: §11.2 line 48 / §24.6 line 99 — Reconcile applies the MAX rule to
// every still-current checkpoint, lifting a counter Redis lost while
// leaving a higher live value intact, and records per-counter MAX inputs.
func TestReconcileMaxRule_spec_11_2_line48(t *testing.T) {
	t.Parallel()
	hourly := label(t, quota.ResetHourly)
	store := newFakeStore()
	_ = store.Write(context.Background(), []quotacheckpoint.Row{
		{TenantID: "acme", Scope: quotacheckpoint.ScopeUser, SubjectID: "alice@acme.com", Period: "hourly", WindowLabel: hourly, TokenTotal: 500},
		{TenantID: "acme", Scope: quotacheckpoint.ScopeTenant, Period: "hourly", WindowLabel: hourly, TokenTotal: 900},
		// Stale window: a label that has already rolled over.
		{TenantID: "acme", Scope: quotacheckpoint.ScopeUser, SubjectID: "bob@acme.com", Period: "hourly", WindowLabel: "hourly-2020010100", TokenTotal: 42},
	})
	cnt := newFakeCounter()
	cnt.m[counterKey{"acme", "alice@acme.com", quota.ResetHourly}] = 100 // Redis lost most of it
	cnt.m[counterKey{"acme", "", quota.ResetHourly}] = 1000              // live already higher than checkpoint
	metrics := &fakeMetrics{counts: map[string]int{}}
	svc := &quotacheckpoint.Service{Store: store, Reader: cnt, Restorer: cnt, Metrics: metrics, Now: clock}

	res, err := svc.Reconcile(context.Background(), quotacheckpoint.ReconcileScope{AllTenants: true})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.CountersWritten != 2 || res.TenantsReconciled != 1 {
		t.Fatalf("result = %d counters / %d tenants, want 2 / 1", res.CountersWritten, res.TenantsReconciled)
	}
	if got := cnt.m[counterKey{"acme", "alice@acme.com", quota.ResetHourly}]; got != 500 {
		t.Errorf("user counter after restore = %d, want 500 (lifted to checkpoint)", got)
	}
	if got := cnt.m[counterKey{"acme", "", quota.ResetHourly}]; got != 1000 {
		t.Errorf("tenant counter after restore = %d, want 1000 (live preserved)", got)
	}
	if metrics.counts[quotacheckpoint.OutcomeRestored] != 2 || metrics.counts[quotacheckpoint.OutcomeSkipped] != 1 {
		t.Errorf("metrics = %+v, want restored=2 skipped=1", metrics.counts)
	}
	// Confirm the per-counter MAX inputs are reported.
	var sawUser bool
	for _, c := range res.Counters {
		if c.Scope == quotacheckpoint.ScopeUser && c.SubjectID == "alice@acme.com" {
			sawUser = true
			if c.CheckpointValue != 500 || c.InMemoryValue != 100 || c.WrittenValue != 500 {
				t.Errorf("user MAX inputs = checkpoint %d / live %d / written %d, want 500/100/500",
					c.CheckpointValue, c.InMemoryValue, c.WrittenValue)
			}
		}
	}
	if !sawUser {
		t.Error("reconcile result missing the per-user counter detail")
	}
}

// spec: §24.6 line 99 — a per-tenant reconcile naming an unknown tenant
// returns ErrTenantNotFound; a known tenant scopes the pass to its rows.
func TestReconcilePerTenant_spec_24_6(t *testing.T) {
	t.Parallel()
	hourly := label(t, quota.ResetHourly)
	store := newFakeStore()
	_ = store.Write(context.Background(), []quotacheckpoint.Row{
		{TenantID: "acme", Scope: quotacheckpoint.ScopeUser, SubjectID: "alice@acme.com", Period: "hourly", WindowLabel: hourly, TokenTotal: 10},
		{TenantID: "globex", Scope: quotacheckpoint.ScopeUser, SubjectID: "carol@globex.com", Period: "hourly", WindowLabel: hourly, TokenTotal: 20},
	})
	cnt := newFakeCounter()
	svc := &quotacheckpoint.Service{
		Store: store, Reader: cnt, Restorer: cnt, Now: clock,
		Tenants: quotacheckpoint.TenantExistsFunc(func(_ context.Context, id string) (bool, error) {
			return id == "acme", nil
		}),
	}
	if _, err := svc.Reconcile(context.Background(), quotacheckpoint.ReconcileScope{TenantID: "ghost"}); !errors.Is(err, quotacheckpoint.ErrTenantNotFound) {
		t.Fatalf("Reconcile(unknown tenant) err = %v, want ErrTenantNotFound", err)
	}
	res, err := svc.Reconcile(context.Background(), quotacheckpoint.ReconcileScope{TenantID: "acme"})
	if err != nil {
		t.Fatalf("Reconcile(acme): %v", err)
	}
	if res.CountersWritten != 1 || res.TenantsReconciled != 1 {
		t.Errorf("per-tenant result = %d counters / %d tenants, want 1 / 1", res.CountersWritten, res.TenantsReconciled)
	}
}

// A nil *Service is safe (typed-nil passed as the interface in dev mode).
func TestNilServiceSafe(t *testing.T) {
	t.Parallel()
	var svc *quotacheckpoint.Service
	svc.Checkpoint(context.Background())
	if err := svc.CheckpointSubject(context.Background(), "acme", "alice@acme.com"); err != nil {
		t.Errorf("nil CheckpointSubject err = %v, want nil", err)
	}
	if _, err := svc.Reconcile(context.Background(), quotacheckpoint.ReconcileScope{AllTenants: true}); err != nil {
		t.Errorf("nil Reconcile err = %v, want nil", err)
	}
}
