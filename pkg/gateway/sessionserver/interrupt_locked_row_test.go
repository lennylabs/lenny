// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// staleReadStore answers Get with the row a lagging read pool still holds:
// state running on pod-1. Update runs against the stored row.
type staleReadStore struct {
	sessionstore.Store
}

func (s staleReadStore) Get(ctx context.Context, tenant, id string) (sessionstore.Session, error) {
	row, err := s.Store.Get(ctx, tenant, id)
	if err != nil {
		return row, err
	}
	row.State = session.StateRunning
	row.PodAssignment = "pod-1"
	return row, nil
}

// countingRearmer counts RearmTree calls.
type countingRearmer struct {
	mu    sync.Mutex
	calls int
}

func (c *countingRearmer) RearmTree(context.Context, string, []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return nil
}

// TestInterruptRevalidatesOnTheLockedRow_spec_7_2 pins that the interrupt's
// precondition holds on the locked row: a row the read pool shows running but
// that is resume_pending, resuming, or a podless Idle suspension answers 409
// INVALID_STATE_TRANSITION, is left unchanged, and re-arms no budget key,
// while a running row is written suspended and re-arms once.
//
// spec: 7.2 (Interactive Session Model), 8.3 (Delegation Policy and Lease)
func TestInterruptRevalidatesOnTheLockedRow_spec_7_2(t *testing.T) {
	for _, tc := range []struct {
		name       string
		state      session.State
		reason     string
		wantStatus int
	}{
		{"resume_pending", session.StateResumePending, "", http.StatusConflict},
		{"resuming", session.StateResuming, "", http.StatusConflict},
		{"idle_suspended", session.StateSuspended, sessionstore.SuspendedReasonIdle, http.StatusConflict},
		{"running", session.StateRunning, "", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := memstore.New()
			now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
			// A child row names the session as its parent, so a re-arm has a
			// parent to name.
			for _, row := range []sessionstore.Session{
				{ID: "s1", TenantID: "acme", State: tc.state, SuspendedReason: tc.reason, CreatedAt: now, UpdatedAt: now},
				{ID: "c1", TenantID: "acme", State: session.StateRunning, ParentSessionID: "s1", RootSessionID: "s1", CreatedAt: now, UpdatedAt: now},
			} {
				if err := base.Create(context.Background(), row); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}
			rearm := &countingRearmer{}
			srv := sessionserver.New(staleReadStore{base}, sessionserver.Options{DevTenantHeader: true, BudgetKeyRearmer: rearm})
			req := httptest.NewRequest(http.MethodPost, "/v1/sessions/s1/interrupt", nil)
			req.Header.Set("X-Lenny-Tenant-ID", "acme")
			rr := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rr, req)
			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rr.Code, tc.wantStatus, rr.Body.String())
			}
			got, _ := base.Get(context.Background(), "acme", "s1")
			if tc.wantStatus == http.StatusConflict {
				if got.State != tc.state || rearm.calls != 0 {
					t.Fatalf("row %q re-arms %d, want %q unchanged and no re-arm", got.State, rearm.calls, tc.state)
				}
				return
			}
			if got.State != session.StateSuspended || rearm.calls != 1 {
				t.Fatalf("row %q re-arms %d, want suspended and one re-arm", got.State, rearm.calls)
			}
		})
	}
}
