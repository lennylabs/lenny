// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a coverage for concurrent first-time tenant suspends. Two suspend
// requests for the same active tenant each pass the handler's pre-read
// before either commits, so both see the tenant unsuspended. Newness is
// decided under the tenant row lock, so exactly one of them writes the
// marker and audits already_suspended=false, the other audits a repeat,
// and both answer 200 with the committed marker.
package tier7a_load_local_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// heldPreReadTenants holds every Get until `parties` callers have
// arrived, so each concurrent suspend's pre-read completes before any
// of them reaches the locked update. It counts the updates that wrote.
type heldPreReadTenants struct {
	*tenantstore.Memory
	arrive  sync.WaitGroup
	mu      sync.Mutex
	writes  int
	release chan struct{}
}

func newHeldPreReadTenants(parties int) *heldPreReadTenants {
	h := &heldPreReadTenants{Memory: tenantstore.NewMemory(), release: make(chan struct{})}
	h.arrive.Add(parties)
	go func() {
		h.arrive.Wait()
		close(h.release)
	}()
	return h
}

func (h *heldPreReadTenants) Get(ctx context.Context, id string) (tenantstore.Tenant, error) {
	row, err := h.Memory.Get(ctx, id)
	h.arrive.Done()
	select {
	case <-h.release:
	case <-time.After(10 * time.Second):
	}
	return row, err
}

func (h *heldPreReadTenants) Update(ctx context.Context, id string, mutate func(*tenantstore.Tenant) error) (tenantstore.Tenant, error) {
	row, err := h.Memory.Update(ctx, id, mutate)
	if err == nil {
		h.mu.Lock()
		h.writes++
		h.mu.Unlock()
	}
	return row, err
}

// suspendAuditSink records audit events under a lock.
type suspendAuditSink struct {
	mu     sync.Mutex
	events []admin.AuditEvent
}

func (s *suspendAuditSink) EmitAdminEvent(_ context.Context, e admin.AuditEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

// emptySessions answers every drain listing with an empty scope, so each
// suspend runs both drain listings under the race detector.
type emptySessions struct {
	sessionstore.Store
}

func (emptySessions) List(context.Context, string, sessionstore.ListFilter) ([]sessionstore.Session, error) {
	return nil, nil
}

// noopTerminator satisfies the session-admin seam; the empty scope never
// reaches ForceTerminate.
type noopTerminator struct{}

func (noopTerminator) GetByID(context.Context, string) (sessionstore.Session, error) {
	return sessionstore.Session{}, sessionstore.ErrNotFound
}

func (noopTerminator) ForceTerminate(_ context.Context, tenantID, id string) (sessionstore.Session, session.State, bool, error) {
	return sessionstore.Session{ID: id, TenantID: tenantID, State: session.StateFailed}, session.StateRunning, true, nil
}

// spec: §15.1 (REST API), §12.8 (Lifecycle fence and drain), §11.8
// diagnosis: a failure means suspend decides newness from its unlocked
// pre-read, so concurrent first-time suspends write the marker twice,
// audit more than one first suspension, or answer a body taken from the
// stale pre-read instead of the committed row.
func TestTenantSuspendConcurrentFirstSuspendsDecideNewnessUnderLock_spec_15_1(t *testing.T) {
	const rounds = 50
	for round := 0; round < rounds; round++ {
		tenants := newHeldPreReadTenants(2)
		if err := tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme", DisplayName: "acme"}); err != nil {
			t.Fatalf("seed tenant: %v", err)
		}
		sink := &suspendAuditSink{}
		h := admin.NewRouter(tenants, admin.Options{
			Clock: func() time.Time { return time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) },
			Audit: sink,
		}).WithSessions(emptySessions{}).WithSessionAdmin(noopTerminator{}).Handler()

		var wg sync.WaitGroup
		recs := make([]*httptest.ResponseRecorder, 2)
		for i := range recs {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				req := httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/suspend", nil)
				req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
					TenantID: "platform", Subject: "alice@acme.com",
					Roles: []pkgauth.Role{pkgauth.RolePlatformAdmin},
				}))
				recs[i] = httptest.NewRecorder()
				h.ServeHTTP(recs[i], req)
			}(i)
		}
		wg.Wait()

		if tenants.writes != 1 {
			t.Fatalf("round %d: marker writes = %d, want 1", round, tenants.writes)
		}
		if len(sink.events) != 2 {
			t.Fatalf("round %d: audit rows = %d, want 2", round, len(sink.events))
		}
		first := 0
		for _, e := range sink.events {
			if e.Type != "tenant.suspended" {
				t.Fatalf("round %d: audit type %q, want tenant.suspended", round, e.Type)
			}
			if e.Detail["already_suspended"] == false {
				first++
			}
		}
		if first != 1 {
			t.Fatalf("round %d: rows with already_suspended=false = %d, want 1", round, first)
		}
		for i, rr := range recs {
			if rr.Code != http.StatusOK {
				t.Fatalf("round %d request %d: status %d, body %s", round, i, rr.Code, rr.Body.String())
			}
			var out struct {
				Suspended bool `json:"suspended"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
				t.Fatalf("round %d request %d: decode: %v", round, i, err)
			}
			if !out.Suspended {
				t.Fatalf("round %d request %d: body reports suspended=false: %s", round, i, rr.Body.String())
			}
		}
	}
}
