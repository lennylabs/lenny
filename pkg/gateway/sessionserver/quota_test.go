// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §11.2 per-tenant concurrent-session quota.

// quotaServer builds a session server for tenant acme with the given
// concurrent-session limit, pre-seeded with one session per state in
// seed.
func quotaServer(t *testing.T, limit int, seed []session.State) *sessionserver.Server {
	t.Helper()
	ctx := context.Background()
	store := memstore.New()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(ctx, tenantstore.Tenant{
		ID: "acme", MaxConcurrentSessions: limit,
	}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	now := time.Now().UTC()
	for i, st := range seed {
		if err := store.Create(ctx, sessionstore.Session{
			ID: fmt.Sprintf("seed-%d", i), TenantID: "acme", State: st,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("seed session: %v", err)
		}
	}
	return sessionserver.New(store, sessionserver.Options{Tenants: tenants})
}

func TestCreateAllowedUnderConcurrentSessionQuota(t *testing.T) {
	srv := quotaServer(t, 3, []session.State{session.StateRunning})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("under quota: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
}

func TestCreateRejectedAtConcurrentSessionQuota(t *testing.T) {
	srv := quotaServer(t, 2, []session.State{session.StateRunning, session.StateCreated})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("at quota: status %d, want 429; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "QUOTA_EXCEEDED") {
		t.Errorf("rejection should carry QUOTA_EXCEEDED: %s", rr.Body.String())
	}
}

func TestCreateConcurrentSessionQuotaZeroIsUnlimited(t *testing.T) {
	srv := quotaServer(t, 0, []session.State{
		session.StateRunning, session.StateRunning, session.StateRunning,
	})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("zero limit (unlimited): status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
}

func TestCreateConcurrentSessionQuotaIgnoresTerminalSessions(t *testing.T) {
	// Two terminal sessions, limit of 1: a terminal session holds no
	// quota, so the create is admitted.
	srv := quotaServer(t, 1, []session.State{session.StateCompleted, session.StateFailed})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("terminal sessions must not count: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
}

func TestCreateConcurrentSessionQuotaUnknownTenantSkipped(t *testing.T) {
	// The tenant registry has no acme row, so no limit applies.
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{Tenants: tenantstore.NewMemory()})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("unknown tenant: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
}
