// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	apisession "github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionmem "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// callLog is one ordered record of the calls the Phase 3 revoker makes
// across its collaborators.
type callLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *callLog) add(c string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, c)
}

func (l *callLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.calls...)
}

// loggingTenants records each Update and fails it while err is set.
type loggingTenants struct {
	tenantstore.Store
	log *callLog
	err error
}

func (t *loggingTenants) Update(ctx context.Context, id string, mutate func(*tenantstore.Tenant) error) (tenantstore.Tenant, error) {
	t.log.add("tenants.Update")
	if t.err != nil {
		return tenantstore.Tenant{}, t.err
	}
	return t.Store.Update(ctx, id, mutate)
}

// loggingSessions records each List with its filter and fails it while err
// is set.
type loggingSessions struct {
	inner   tenantSessionLister
	log     *callLog
	err     error
	filters []sessionstore.ListFilter
}

func (s *loggingSessions) List(ctx context.Context, tenantID string, f sessionstore.ListFilter) ([]sessionstore.Session, error) {
	s.log.add("sessions.List")
	s.filters = append(s.filters, f)
	if s.err != nil {
		return nil, s.err
	}
	return s.inner.List(ctx, tenantID, f)
}

// recordingLeaseRevoker records each lease-step call.
type recordingLeaseRevoker struct {
	log   *callLog
	calls []leaseStepCall
}

type leaseStepCall struct {
	tenantID, userID string
	sessionIDs       []string
}

func (r *recordingLeaseRevoker) RevokeUserLeases(tenantID, userID string, sessionIDs []string) int {
	if r.log != nil {
		r.log.add("leases.RevokeUserLeases")
	}
	r.calls = append(r.calls, leaseStepCall{tenantID, userID, slices.Clone(sessionIDs)})
	return len(sessionIDs)
}

// tokenDeleter is one TokenStore DeleteByTenant stub.
type tokenDeleter struct {
	name string
	log  *callLog
	err  error
}

func (d *tokenDeleter) DeleteByTenant(_ context.Context, tenantID string) (int, error) {
	d.log.add(d.name + ".DeleteByTenant:" + tenantID)
	return 1, d.err
}

// revokerFixture is a Phase 3 revoker over an in-memory tenant row in
// disabling and three sessions of acme, one per lifecycle state class, and
// one session of globex.
type revokerFixture struct {
	rev        tenantCredentialRevoker
	log        *callLog
	tenants    *loggingTenants
	sessions   *loggingSessions
	leases     *recordingLeaseRevoker
	connectors *tokenDeleter
	creds      *tokenDeleter
	now        *time.Time
}

func newRevokerFixture(t *testing.T) *revokerFixture {
	t.Helper()
	ctx := context.Background()
	log := &callLog{}
	mem := tenantstore.NewMemory()
	for _, id := range []string{"acme", "globex"} {
		if err := mem.Create(ctx, tenantstore.Tenant{ID: id, State: tenantstore.TenantStateDisabling}); err != nil {
			t.Fatalf("Create tenant %s: %v", id, err)
		}
	}
	store := sessionmem.New()
	for _, s := range []sessionstore.Session{
		{ID: "s-running", TenantID: "acme", UserID: "alice", State: apisession.StateRunning},
		{ID: "s-completed", TenantID: "acme", UserID: "alice", State: apisession.StateCompleted},
		{ID: "s-failed", TenantID: "acme", UserID: "bob", State: apisession.StateFailed},
		{ID: "s-globex", TenantID: "globex", UserID: "carol", State: apisession.StateRunning},
	} {
		if err := store.Create(ctx, s); err != nil {
			t.Fatalf("Create session %s: %v", s.ID, err)
		}
	}
	now := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	f := &revokerFixture{
		log:        log,
		tenants:    &loggingTenants{Store: mem, log: log},
		sessions:   &loggingSessions{inner: store, log: log},
		leases:     &recordingLeaseRevoker{log: log},
		connectors: &tokenDeleter{name: "connectorcreds", log: log},
		creds:      &tokenDeleter{name: "credentials", log: log},
		now:        &now,
	}
	erasers := []namedTenantEraser{
		{"issued_tokens", nil},
		{"tokens", sumTenantErasers(f.connectors.DeleteByTenant, f.creds.DeleteByTenant)},
	}
	f.rev = tenantCredentialRevoker{
		tenants:  f.tenants,
		sessions: f.sessions,
		leases:   f.leases,
		tokens:   tenantEraserFunc(erasers, "tokens"),
		clock:    func() time.Time { return *f.now },
	}
	return f
}

func (f *revokerFixture) state(t *testing.T, id string) tenantstore.Tenant {
	t.Helper()
	row, err := f.tenants.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get tenant %s: %v", id, err)
	}
	return row
}

// The Phase 3 revoker commits deleting before any other call, lists the
// tenant's sessions from the primary, returns the leases of every listed
// session with terminal ones included, and then deletes the tenant's
// TokenStore rows, connector tokens before registered credentials. Before
// the fix the gateway wired no Phase 3 revoker, so none of these ran.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle Phase 3), 11.4 (User Invalidation)
func TestTenantPhase3RevokerCommitsDeletingThenReturnsLeasesThenDeletesTokens_spec_12_8(t *testing.T) {
	f := newRevokerFixture(t)
	if err := f.rev.RevokeTenantCredentials(context.Background(), "acme"); err != nil {
		t.Fatalf("RevokeTenantCredentials: %v", err)
	}
	want := []string{
		"tenants.Update", "sessions.List", "leases.RevokeUserLeases",
		"connectorcreds.DeleteByTenant:acme", "credentials.DeleteByTenant:acme",
	}
	if got := f.log.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("call order = %v, want %v", got, want)
	}
	if row := f.state(t, "acme"); row.State != tenantstore.TenantStateDeleting {
		t.Errorf("acme state = %q, want deleting", row.State)
	}
	if row := f.state(t, "globex"); row.State != tenantstore.TenantStateDisabling {
		t.Errorf("globex state = %q, want disabling (untouched)", row.State)
	}
	if len(f.sessions.filters) != 1 || !f.sessions.filters[0].Primary {
		t.Errorf("session list filters = %+v, want one primary listing", f.sessions.filters)
	}
	if len(f.leases.calls) != 1 {
		t.Fatalf("lease-step calls = %d, want 1", len(f.leases.calls))
	}
	c := f.leases.calls[0]
	ids := slices.Sorted(slices.Values(c.sessionIDs))
	if c.tenantID != "acme" || c.userID != "" || !slices.Equal(ids, []string{"s-completed", "s-failed", "s-running"}) {
		t.Errorf("lease step = %+v, want acme, no user, and every acme session including the terminal ones", c)
	}
}

// Each step's error is returned and stops the steps after it. A failed
// commit of deleting makes no listing, no lease-step call, and no
// TokenStore delete, so no Phase 3 action runs while the tenant's bearers
// are still admitted.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle Phase 3), 12.8 (Compliance Interfaces: Idempotency and resumption)
func TestTenantPhase3RevokerReturnsTheFirstErrorAndStops_spec_12_8(t *testing.T) {
	boom := errors.New("primary unreachable")
	cases := []struct {
		name     string
		arm      func(*revokerFixture)
		wantErr  string
		wantLog  []string
		deleting bool
	}{
		{
			name: "commit fails", arm: func(f *revokerFixture) { f.tenants.err = boom },
			wantErr: "commit deleting", wantLog: []string{"tenants.Update"},
		},
		{
			name: "listing fails", arm: func(f *revokerFixture) { f.sessions.err = boom },
			wantErr: "list sessions", wantLog: []string{"tenants.Update", "sessions.List"}, deleting: true,
		},
		{
			name: "connector token delete fails", arm: func(f *revokerFixture) { f.connectors.err = boom },
			wantErr:  "delete tokens",
			wantLog:  []string{"tenants.Update", "sessions.List", "leases.RevokeUserLeases", "connectorcreds.DeleteByTenant:acme"},
			deleting: true,
		},
		{
			name: "registered credential delete fails", arm: func(f *revokerFixture) { f.creds.err = boom },
			wantErr: "delete tokens",
			wantLog: []string{
				"tenants.Update", "sessions.List", "leases.RevokeUserLeases",
				"connectorcreds.DeleteByTenant:acme", "credentials.DeleteByTenant:acme",
			},
			deleting: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRevokerFixture(t)
			tc.arm(f)
			err := f.rev.RevokeTenantCredentials(context.Background(), "acme")
			if !errors.Is(err, boom) || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q wrapping the cause", err, tc.wantErr)
			}
			if got := f.log.snapshot(); !slices.Equal(got, tc.wantLog) {
				t.Errorf("calls = %v, want %v", got, tc.wantLog)
			}
			if got := f.state(t, "acme").State == tenantstore.TenantStateDeleting; got != tc.deleting {
				t.Errorf("acme deleting = %v, want %v", got, tc.deleting)
			}
		})
	}
}

// A second run, as the next pass makes after a pause or a takeover, keeps
// the committed state, returns the leases again,
// and deletes the TokenStore rows again, all without error. A tombstoned
// row is never moved back to deleting.
//
// spec: 12.8 (Compliance Interfaces: Idempotency and resumption), 12.8 (Compliance Interfaces: Tenant deletion lifecycle Phase 3)
func TestTenantPhase3RevokerIsIdempotent_spec_12_8(t *testing.T) {
	f := newRevokerFixture(t)
	ctx := context.Background()
	if err := f.rev.RevokeTenantCredentials(ctx, "acme"); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := f.rev.RevokeTenantCredentials(ctx, "acme"); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if row := f.state(t, "acme"); row.State != tenantstore.TenantStateDeleting {
		t.Errorf("after a second run acme state = %q, want deleting", row.State)
	}
	if len(f.leases.calls) != 2 {
		t.Errorf("lease-step calls = %d, want 2", len(f.leases.calls))
	}
	if n := slices.Index(f.log.snapshot(), "credentials.DeleteByTenant:acme"); n < 0 ||
		!slices.Contains(f.log.snapshot()[n+1:], "credentials.DeleteByTenant:acme") {
		t.Errorf("calls = %v, want the TokenStore deletes on both runs", f.log.snapshot())
	}

	if _, err := f.tenants.Store.Update(ctx, "acme", func(t *tenantstore.Tenant) error {
		t.State = tenantstore.TenantStateDeleted
		return nil
	}); err != nil {
		t.Fatalf("tombstone: %v", err)
	}
	if err := f.rev.RevokeTenantCredentials(ctx, "acme"); err != nil {
		t.Fatalf("run on a tombstone: %v", err)
	}
	if row := f.state(t, "acme"); row.State != tenantstore.TenantStateDeleted {
		t.Errorf("tombstone state = %q, want deleted", row.State)
	}
}

// A tenant with no sessions makes no lease-step call, and a posture with no
// TokenStore backend lists the tokens entry with a nil function, which the
// revoker skips.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle Phase 3)
func TestTenantPhase3RevokerWithNoSessionsAndNoTokenBackend_spec_12_8(t *testing.T) {
	f := newRevokerFixture(t)
	f.rev.tokens = tenantEraserFunc([]namedTenantEraser{{"tokens", sumTenantErasers(nil, nil)}}, "tokens")
	if f.rev.tokens != nil {
		t.Fatal("an absent TokenStore backend must yield a nil tokens function")
	}
	if err := f.rev.RevokeTenantCredentials(context.Background(), "globex"); err != nil {
		t.Fatalf("RevokeTenantCredentials: %v", err)
	}
	if err := f.rev.RevokeTenantCredentials(context.Background(), "initech-missing"); err == nil {
		t.Error("a tenant with no row must fail the commit")
	}
	f2 := newRevokerFixture(t)
	f2.sessions.inner = sessionmem.New()
	if err := f2.rev.RevokeTenantCredentials(context.Background(), "acme"); err != nil {
		t.Fatalf("RevokeTenantCredentials with no sessions: %v", err)
	}
	if len(f2.leases.calls) != 0 {
		t.Errorf("lease-step calls = %d, want 0 for a tenant with no sessions", len(f2.leases.calls))
	}
	if got := tenantEraserFunc(nil, "tokens"); got != nil {
		t.Error("a missing entry must yield a nil function")
	}
}

// TestTenantDeletionPhase3RevokerIsWired_spec_12_8 pins the gateway's Phase
// 3 wiring: the reconciler's Revoker is the tenant credential revoker over
// the tenant store, the session store, the §11.4 lease step, and the tokens
// entry of the Phase 4 eraser list. The revoker is unit-tested over fakes,
// so a production wiring that drops any of them is caught only here.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle Phase 3)
func TestTenantDeletionPhase3RevokerIsWired_spec_12_8(t *testing.T) {
	assertWired(t, []struct{ file, want, seam string }{
		{"adminrouter.go", "Revoker: tenantCredentialRevoker{ tenants: w.tenants, sessions: w.sessions, leases: leaseRevoker, tokens: tenantEraserFunc(tenantErasers, \"tokens\"), clock: clockinject.Now, },", "the Phase 3 revoker"},
	})
}
