// SPDX-License-Identifier: MIT

package orphansession_test

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/orphansession"
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

// spec: §10.1.4 (Coordinator-Loss Detection and Hold State), §10.1.1
// (Primary:)
//
// A failed listing of one tenant skips that tenant and a failed write of one
// session skips that session: the pass goes on to the next session and
// tenant, returns both failures joined, and treats a missing row as
// superseded. Before the change a listing failure ended the pass and a write
// failure was logged and dropped from the pass result.
func TestReconcilerContinuesPastListAndWriteFailures_spec_10_1_1(t *testing.T) {
	mem := memstore.New()
	for _, id := range []string{"a-bad", "b-gone", "c-good"} {
		mkSession(t, mem, "acme", id, session.StateRunning, "pod-"+id, "pool-a")
	}
	mkSession(t, mem, "globex", "g", session.StateRunning, "pod-g", "pool-a")
	rows := map[string]orphansession.MirrorPod{}
	for _, id := range []string{"a-bad", "b-gone", "c-good", "g"} {
		rows["pod-"+id] = orphansession.MirrorPod{PoolID: "pool-a", Phase: "terminated"}
	}
	store := &faultStore{Store: mem, listTenant: "globex", updateErr: map[string]error{
		"a-bad": errInjectedWrite, "b-gone": sessionstore.ErrNotFound,
	}}
	term := &fakeTerminal{}
	met := &fakeMetrics{}
	r := orphansession.New(store, fakeTenants{"globex", "acme"}, &fakeMirror{rows: rows, lag: map[string]float64{"pool-a": 1}},
		orphansession.Options{Terminal: term, Metrics: met})

	n, err := r.Tick(context.Background())
	if !errors.Is(err, errInjectedList) || !errors.Is(err, errInjectedWrite) || errors.Is(err, sessionstore.ErrNotFound) {
		t.Fatalf("Tick error = %v, want the listing and write failures joined and no ErrNotFound", err)
	}
	r.WaitDispatched()
	if n != 1 || met.reconciliations != 1 {
		t.Errorf("failed = %d, reconciliations = %d; want 1 each", n, met.reconciliations)
	}
	if got := getState(t, mem, "acme", "c-good").State; got != session.StateFailed {
		t.Errorf("c-good = %q, want failed in the same pass", got)
	}
	if got := getState(t, mem, "acme", "a-bad").State; got != session.StateRunning {
		t.Errorf("a-bad = %q, want running after its failed write", got)
	}
	term.mu.Lock()
	called := append([]string(nil), term.called...)
	term.mu.Unlock()
	if len(called) != 1 || called[0] != "c-good" {
		t.Errorf("terminal hooks = %v, want [c-good]", called)
	}

	delete(store.updateErr, "a-bad")
	store.listTenant = ""
	if n, err := r.Tick(context.Background()); err != nil || n != 2 {
		t.Fatalf("second Tick = %d, %v; want a-bad and globex's g failed with no error", n, err)
	}
	r.WaitDispatched()
}

type failingTenants struct{}

func (failingTenants) ListTenants(context.Context) ([]string, error) { return nil, errInjectedList }

// spec: §10.1.4 (Coordinator-Loss Detection and Hold State)
//
// A tenant listing failure ends the pass with that error.
func TestReconcilerTenantListingFailure_spec_10_1_4(t *testing.T) {
	r := orphansession.New(memstore.New(), failingTenants{}, &fakeMirror{}, orphansession.Options{})
	if _, err := r.Tick(context.Background()); !errors.Is(err, errInjectedList) {
		t.Fatalf("Tick error = %v, want the tenant listing failure", err)
	}
}
