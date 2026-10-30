// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// pagingLister records the pages it serves from the memstore listing.
type pagingLister struct {
	store *memstore.Store
	mu    sync.Mutex
	pages []string
	err   map[string]error
}

func (l *pagingLister) ListPodlessSuspended(ctx context.Context, tenant, after string, limit int) ([]sessionstore.Session, error) {
	l.mu.Lock()
	l.pages = append(l.pages, tenant+"/"+after)
	err := l.err[tenant]
	l.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return l.store.ListPodlessSuspended(ctx, tenant, after, limit)
}

// TestBacklogRecheckerPass_spec_7_2 pins the leader's re-check: a podless
// suspended row moves to resume_pending when it holds an inbox entry (Idle)
// or a DLQ entry accepted after SuspendedAt, other rows stay, a tenant's
// list error does not stop the next tenant, and each tenant is paged by id.
//
// spec: 7.2 (Message delivery routing — seven paths, path 6), 12.5 (Artifact
// Store)
func TestBacklogRecheckerPass_spec_7_2(t *testing.T) {
	r := newSuspensionRig(t, true)
	store := r.store.(*memstore.Store)
	since := r.now.Add(-time.Hour)
	seed := func(tenant, id, reason string) {
		if err := store.Create(context.Background(), sessionstore.Session{
			ID: id, TenantID: tenant, State: session.StateSuspended, SuspendedAt: since,
			SuspendedReason: reason, CreatedAt: since, UpdatedAt: since,
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	seed("acme", "a-idle-inbox", sessionstore.SuspendedReasonIdle)
	seed("acme", "b-intr-late-dlq", sessionstore.SuspendedReasonInterruptAcknowledged)
	seed("acme", "c-intr-inbox-only", sessionstore.SuspendedReasonInterruptAcknowledged)
	seed("acme", "d-idle-empty", sessionstore.SuspendedReasonIdle)
	seed("globex", "e-idle-dlq", sessionstore.SuspendedReasonIdle)
	ctx := context.Background()
	msg := func(id string) sessioninbox.Message {
		return sessioninbox.Message{MessageID: id, Payload: []byte(`{}`), EnqueuedAt: r.now}
	}
	if _, err := r.coord.EnqueueInbox(ctx, "acme", "a-idle-inbox", msg("m1")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.coord.EnqueueDLQ(ctx, "acme", "b-intr-late-dlq", msg("m2"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := r.coord.EnqueueInbox(ctx, "acme", "c-intr-inbox-only", msg("m3")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.coord.EnqueueDLQ(ctx, "globex", "e-idle-dlq", msg("m4"), 0); err != nil {
		t.Fatal(err)
	}
	lister := &pagingLister{store: store}
	rc := &BacklogRechecker{
		Server:   r.srv,
		Lister:   lister,
		Tenants:  func(context.Context) ([]string, error) { return []string{"acme", "globex"}, nil },
		PageSize: 2,
	}
	if n := rc.Pass(ctx); n != 3 {
		t.Fatalf("Pass resumed %d, want 3", n)
	}
	for id, want := range map[string]session.State{
		"a-idle-inbox": session.StateResumePending, "b-intr-late-dlq": session.StateResumePending,
		"c-intr-inbox-only": session.StateSuspended, "d-idle-empty": session.StateSuspended,
	} {
		if got, _ := store.Get(ctx, "acme", id); got.State != want {
			t.Errorf("%s = %q, want %q", id, got.State, want)
		}
	}
	if got, _ := store.Get(ctx, "globex", "e-idle-dlq"); got.State != session.StateResumePending {
		t.Errorf("globex row = %q, want resume_pending", got.State)
	}
	sort.Strings(lister.pages)
	if len(lister.pages) < 3 {
		t.Errorf("pages = %v, want acme paged by id", lister.pages)
	}

	lister.err = map[string]error{"acme": errors.New("list failed")}
	seed("globex", "f-idle-dlq", sessionstore.SuspendedReasonIdle)
	if _, err := r.coord.EnqueueDLQ(ctx, "globex", "f-idle-dlq", msg("m5"), 0); err != nil {
		t.Fatal(err)
	}
	if n := rc.Pass(ctx); n != 1 {
		t.Fatalf("Pass after an acme list error resumed %d, want 1 (globex continues)", n)
	}
}

// recordingRearmer records RearmTree calls.
type recordingRearmer struct {
	mu    sync.Mutex
	calls [][]string
	err   error
}

func (r *recordingRearmer) RearmTree(_ context.Context, root string, parents []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string{root}, parents...))
	return r.err
}

// TestRearmBudgetKeys_spec_8_3 pins the re-arm: it names the tree root and
// each distinct delegating parent once, makes no call for a tree with no
// delegation, and leaves the transition in place on a rearmer error.
//
// spec: 8.3 (Delegation Policy and Lease)
func TestRearmBudgetKeys_spec_8_3(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	for _, row := range []sessionstore.Session{
		{ID: "root", TenantID: "acme", State: session.StateSuspended, RootSessionID: "root"},
		{ID: "c1", TenantID: "acme", State: session.StateRunning, RootSessionID: "root", ParentSessionID: "root"},
		{ID: "c2", TenantID: "acme", State: session.StateRunning, RootSessionID: "root", ParentSessionID: "root"},
		{ID: "g1", TenantID: "acme", State: session.StateRunning, RootSessionID: "root", ParentSessionID: "c1"},
		{ID: "solo", TenantID: "acme", State: session.StateSuspended},
	} {
		row.CreatedAt, row.UpdatedAt = now, now
		if err := store.Create(context.Background(), row); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	rearm := &recordingRearmer{}
	s := New(store, Options{DevTenantHeader: true, BudgetKeyRearmer: rearm})
	defer s.Close()
	g1, _ := store.Get(context.Background(), "acme", "g1")
	s.rearmBudgetKeys(context.Background(), g1)
	if len(rearm.calls) != 1 {
		t.Fatalf("calls = %v, want one", rearm.calls)
	}
	got := rearm.calls[0]
	if got[0] != "root" || len(got) != 3 {
		t.Fatalf("call = %v, want root with parents root and c1", got)
	}
	solo, _ := store.Get(context.Background(), "acme", "solo")
	s.rearmBudgetKeys(context.Background(), solo)
	if len(rearm.calls) != 1 {
		t.Fatalf("calls = %v, want no call for a tree with no delegation", rearm.calls)
	}
	rearm.err = errors.New("redis down")
	s.rearmBudgetKeys(context.Background(), g1)
	if len(rearm.calls) != 2 {
		t.Fatalf("calls = %v, want the call made despite the error", rearm.calls)
	}
}
