// SPDX-License-Identifier: MIT

package orphancleanup_test

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/orphancleanup"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

var (
	errInjectedList  = errors.New("injected list failure")
	errInjectedWrite = errors.New("injected write failure")
)

// faultStore fails List for one tenant and Update for chosen sessions, and
// lists rows in ID order.
type faultStore struct {
	sessionstore.Store
	listTenant string
	updateErr  map[string]error
}

func (s *faultStore) List(ctx context.Context, tenantID string, f sessionstore.ListFilter) ([]sessionstore.Session, error) {
	if tenantID == s.listTenant {
		return nil, errInjectedList
	}
	rows, err := s.Store.List(ctx, tenantID, f)
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, err
}

func (s *faultStore) Update(ctx context.Context, tenantID, id string, mut func(*sessionstore.Session) error) (sessionstore.Session, error) {
	if err := s.updateErr[id]; err != nil {
		return sessionstore.Session{}, err
	}
	return s.Store.Update(ctx, tenantID, id, mut)
}

// spec: §8.10 (Delegation Tree Recovery), §10.1.1 (Primary:)
//
// A failed listing of one tenant skips that tenant and a failed write of one
// orphan skips that orphan: the pass goes on, returns both failures joined,
// keeps the failed orphan in the active gauge, and treats a missing row as
// superseded. Before the change the first failure ended the pass.
func TestOrphanCleanupContinuesPastListAndWriteFailures_spec_10_1_1(t *testing.T) {
	mem := memstore.New()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seed(t, mem, "sess_root", "", session.StateCompleted, base)
	for _, id := range []string{"a_bad", "b_gone", "c_good"} {
		seed(t, mem, id, "sess_root", session.StateRunning, base)
	}
	store := &faultStore{Store: mem, listTenant: "globex", updateErr: map[string]error{
		"a_bad": errInjectedWrite, "b_gone": sessionstore.ErrNotFound,
	}}
	metrics := &fakeMetricsSink{perTenant: map[string]int{}}
	hook := &fakeTerminalHook{}
	sw := orphancleanup.New(store, orphancleanup.StaticTenants{"globex", "acme"},
		orphancleanup.Options{Terminal: hook, Metrics: metrics})

	n, err := sw.Tick(context.Background(), base.Add(2*time.Hour))
	if !errors.Is(err, errInjectedList) || !errors.Is(err, errInjectedWrite) || errors.Is(err, sessionstore.ErrNotFound) {
		t.Fatalf("Tick error = %v, want the listing and write failures joined and no ErrNotFound", err)
	}
	sw.WaitDispatched()
	if n != 1 {
		t.Errorf("terminated = %d, want 1 (the orphan after the failed one)", n)
	}
	if row, _ := mem.Get(context.Background(), "acme", "c_good"); row.State != session.StateExpired {
		t.Errorf("c_good = %q, want expired in the same pass", row.State)
	}
	if row, _ := mem.Get(context.Background(), "acme", "a_bad"); row.State != session.StateRunning {
		t.Errorf("a_bad = %q, want running after its failed write", row.State)
	}
	if got := metrics.perTenant["acme"]; got != 2 {
		t.Errorf("acme active orphans = %d, want 2 (the failed and the missing row)", got)
	}
	if metrics.terminated != 1 {
		t.Errorf("terminated counter = %d, want 1", metrics.terminated)
	}
	hook.mu.Lock()
	calls := len(hook.calls)
	hook.mu.Unlock()
	if calls != 1 {
		t.Errorf("terminal hooks = %d, want 1", calls)
	}

	delete(store.updateErr, "a_bad")
	if n, err := sw.Tick(context.Background(), base.Add(2*time.Hour+time.Second)); err == nil || n != 1 {
		t.Fatalf("second Tick = %d, %v; want a_bad ended and the globex failure still reported", n, err)
	}
}

type failingTenants struct{}

func (failingTenants) ListTenants(context.Context) ([]string, error) { return nil, errInjectedList }

// spec: §8.10 (Delegation Tree Recovery)
//
// A tenant listing failure ends the pass with that error.
func TestOrphanCleanupTenantListingFailure_spec_8_10(t *testing.T) {
	sw := orphancleanup.New(memstore.New(), failingTenants{}, orphancleanup.Options{})
	if _, err := sw.Tick(context.Background(), time.Now()); !errors.Is(err, errInjectedList) {
		t.Fatalf("Tick error = %v, want the tenant listing failure", err)
	}
}
