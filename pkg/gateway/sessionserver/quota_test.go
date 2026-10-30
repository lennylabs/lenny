// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
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
	return sessionserver.New(store, sessionserver.Options{DevTenantHeader: true, Tenants: tenants})
}

func TestCreateAllowedUnderConcurrentSessionQuota(t *testing.T) {
	srv := quotaServer(t, 3, []session.State{session.StateRunning})
	rr := createRequest(t, asOwner(srv.Handler()), sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("under quota: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
}

func TestCreateRejectedAtConcurrentSessionQuota(t *testing.T) {
	srv := quotaServer(t, 2, []session.State{session.StateRunning, session.StateCreated})
	rr := createRequest(t, asOwner(srv.Handler()), sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
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
	rr := createRequest(t, asOwner(srv.Handler()), sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("zero limit (unlimited): status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
}

func TestCreateConcurrentSessionQuotaIgnoresTerminalSessions(t *testing.T) {
	// Two terminal sessions, limit of 1: a terminal session holds no
	// quota, so the create is admitted.
	srv := quotaServer(t, 1, []session.State{session.StateCompleted, session.StateFailed})
	rr := createRequest(t, asOwner(srv.Handler()), sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("terminal sessions must not count: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
}

func TestCreateConcurrentSessionQuotaUnknownTenantSkipped(t *testing.T) {
	// The tenant registry has no acme row, so no limit applies.
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true, Tenants: tenantstore.NewMemory()})
	rr := createRequest(t, asOwner(srv.Handler()), sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("unknown tenant: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
}

// quotaRefusalBody decodes a 429 envelope's message and details.
func quotaRefusalBody(t *testing.T, body []byte) (string, string, map[string]any) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return env.Error.Code, env.Error.Message, env.Error.Details
}

// TestCreateAdmittedWhileCapIsFullOfSuspendedSessions_spec_11_1 pins that a
// suspended session, with or without its pod, holds no concurrent-session
// slot: a create is admitted while the tenant quota and the per-user,
// per-runtime, and global limits would be full if suspended rows counted.
//
// spec: §11.1 (Concurrent-session count)
func TestCreateAdmittedWhileCapIsFullOfSuspendedSessions_spec_11_1(t *testing.T) {
	srv := quotaServer(t, 2, []session.State{session.StateSuspended, session.StateSuspended})
	if rr := createRequest(t, asOwner(srv.Handler()), sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"}); rr.Code != http.StatusCreated {
		t.Fatalf("tenant quota full of suspended sessions: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	seed := runningRows("acme", "alice", "claude-code", 2)
	seed[0].State, seed[0].PodAssignment = session.StateSuspended, "pod-a"
	seed[1].State = session.StateSuspended
	scoped := concurrencyServer(t, sessionserver.Options{
		DevTenantHeader:              true,
		MaxConcurrentSessionsPerUser: 2, MaxConcurrentSessionsPerRuntime: 2, MaxConcurrentSessionsGlobal: 2,
	}, seed)
	rr := createRequestAs(t, asOwner(scoped.Handler()), sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"},
		authmw.Principal{Subject: "alice", TenantID: "acme"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("scoped limits full of suspended sessions: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
}

// TestCreateRefusalBodyIsTheSharedCapRefusal_spec_11_1 pins that the
// creation refusal of each scope carries the message and details of the
// shared cap refusal, the body a refused resume carries.
//
// spec: §11.1 (Concurrent-session count)
func TestCreateRefusalBodyIsTheSharedCapRefusal_spec_11_1(t *testing.T) {
	check := func(t *testing.T, rr *httptest.ResponseRecorder, want *sessionserver.CapRefusal) {
		t.Helper()
		if rr.Code != http.StatusTooManyRequests {
			t.Fatalf("status %d, want 429; body %s", rr.Code, rr.Body.String())
		}
		code, msg, details := quotaRefusalBody(t, rr.Body.Bytes())
		if code != "QUOTA_EXCEEDED" || msg != want.Message() {
			t.Errorf("envelope %s %q, want QUOTA_EXCEEDED %q", code, msg, want.Message())
		}
		wantDetails := want.Details()
		if len(details) != len(wantDetails) {
			t.Errorf("details = %v, want %v", details, wantDetails)
		}
		for k, v := range wantDetails {
			got := details[k]
			if n, ok := got.(float64); ok {
				got = int(n)
			}
			if got != v {
				t.Errorf("details[%s] = %v, want %v", k, details[k], v)
			}
		}
	}
	t.Run("tenant", func(t *testing.T) {
		srv := quotaServer(t, 1, []session.State{session.StateRunning})
		check(t, createRequest(t, asOwner(srv.Handler()), sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"}),
			&sessionserver.CapRefusal{Scope: sessionserver.CapScopeTenant, Limit: 1, Active: 1})
	})
	for _, tc := range []struct {
		scope string
		opts  sessionserver.Options
	}{
		{sessionserver.CapScopeUser, sessionserver.Options{DevTenantHeader: true, MaxConcurrentSessionsPerUser: 1}},
		{sessionserver.CapScopeRuntime, sessionserver.Options{DevTenantHeader: true, MaxConcurrentSessionsPerRuntime: 1}},
		{sessionserver.CapScopeGlobal, sessionserver.Options{DevTenantHeader: true, MaxConcurrentSessionsGlobal: 1}},
	} {
		t.Run(tc.scope, func(t *testing.T) {
			srv := concurrencyServer(t, tc.opts, runningRows("acme", "alice", "claude-code", 1))
			rr := createRequestAs(t, asOwner(srv.Handler()), sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"},
				authmw.Principal{Subject: "alice", TenantID: "acme"})
			check(t, rr, &sessionserver.CapRefusal{Scope: tc.scope, Limit: 1, Active: 1})
		})
	}
}
