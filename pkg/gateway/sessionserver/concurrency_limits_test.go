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
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// spec: §11.1 line 8 — global, per-user, and per-runtime
// concurrent-session admission caps. F-11.1.3.

// concurrencyServer builds a session server pre-seeded with the given
// rows and the supplied §11.1 concurrent-session caps.
func concurrencyServer(t *testing.T, opts sessionserver.Options, seed []sessionstore.Session) *sessionserver.Server {
	t.Helper()
	ctx := context.Background()
	store := memstore.New()
	now := time.Now().UTC()
	for _, s := range seed {
		if s.State == "" {
			s.State = session.StateRunning
		}
		s.CreatedAt, s.UpdatedAt = now, now
		if err := store.Create(ctx, s); err != nil {
			t.Fatalf("seed %s: %v", s.ID, err)
		}
	}
	return sessionserver.New(store, opts)
}

func runningRows(tenant, user, runtime string, n int) []sessionstore.Session {
	rows := make([]sessionstore.Session, n)
	for i := range rows {
		rows[i] = sessionstore.Session{
			ID:       fmt.Sprintf("%s-%s-%s-%d", tenant, user, runtime, i),
			TenantID: tenant, UserID: user, RuntimeRef: runtime,
			State: session.StateRunning,
		}
	}
	return rows
}

func TestCreateRejectedAtPerRuntimeConcurrencyLimit_spec_11_1(t *testing.T) {
	srv := concurrencyServer(t,
		sessionserver.Options{MaxConcurrentSessionsPerRuntime: 2},
		runningRows("acme", "alice", "claude-code", 2))
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("at per-runtime limit: status %d, want 429; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "QUOTA_EXCEEDED") || !strings.Contains(rr.Body.String(), `"scope":"runtime"`) {
		t.Errorf("rejection should carry QUOTA_EXCEEDED + runtime scope: %s", rr.Body.String())
	}
}

func TestCreateAdmittedUnderPerRuntimeLimitForDifferentRuntime_spec_11_1(t *testing.T) {
	// Two claude-code sessions saturate that runtime, but the create
	// targets a different runtime so its own count is zero.
	srv := concurrencyServer(t,
		sessionserver.Options{MaxConcurrentSessionsPerRuntime: 2},
		runningRows("acme", "alice", "claude-code", 2))
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{RuntimeRef: "gpt"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("different runtime under limit: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
}

func TestCreateRejectedAtPerUserConcurrencyLimit_spec_11_1(t *testing.T) {
	srv := concurrencyServer(t,
		sessionserver.Options{MaxConcurrentSessionsPerUser: 2},
		runningRows("acme", "alice@acme.com", "claude-code", 2))
	rr := createRequestAs(t, srv.Handler(),
		sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"},
		authmw.Principal{Subject: "alice@acme.com", TenantID: "acme"})
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("at per-user limit: status %d, want 429; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"scope":"user"`) {
		t.Errorf("rejection should carry user scope: %s", rr.Body.String())
	}
}

func TestCreateAdmittedForDifferentUserUnderPerUserLimit_spec_11_1(t *testing.T) {
	// alice is saturated; bob has no live sessions.
	srv := concurrencyServer(t,
		sessionserver.Options{MaxConcurrentSessionsPerUser: 2},
		runningRows("acme", "alice@acme.com", "claude-code", 2))
	rr := createRequestAs(t, srv.Handler(),
		sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"},
		authmw.Principal{Subject: "bob@acme.com", TenantID: "acme"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("different user under limit: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
}

func TestCreateRejectedAtGlobalConcurrencyLimit_spec_11_1(t *testing.T) {
	// Two live sessions across two tenants saturate the global cap.
	seed := append(runningRows("acme", "alice", "claude-code", 1),
		runningRows("globex", "bob", "gpt", 1)...)
	srv := concurrencyServer(t, sessionserver.Options{MaxConcurrentSessionsGlobal: 2}, seed)
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("at global limit: status %d, want 429; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"scope":"global"`) {
		t.Errorf("rejection should carry global scope: %s", rr.Body.String())
	}
}

func TestConcurrencyLimitsZeroIsUnlimited_spec_11_1(t *testing.T) {
	// Every cap left at zero: the saturated runtime/global counts admit.
	srv := concurrencyServer(t, sessionserver.Options{},
		runningRows("acme", "alice", "claude-code", 5))
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("zero caps (unlimited): status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
}
