// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a coverage for the resume admission check under concurrency. The
// check counts the sessions in its scope and reserves no slot, so
// admissions running at the same time can each pass against the same count
// and overshoot a limit by the number in flight. Every write it admits or
// refuses must still be all-or-nothing: an admitted resume moves its
// session to resume_pending, a refused one leaves the session suspended
// with nothing held, a message admitted at its entry check is held whether
// or not the check at its resume write admits it, and once the admissions
// settle the check refuses the next resume.
//
// spec: §11.1 (Concurrent-session count), §11.1 (Resume admission), §11.1
// (Concurrent admissions).
package tier7a_load_local_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
)

// spec: 11.1 (Concurrent-session count), 11.1 (Resume admission), 11.1
// (Concurrent admissions)
// diagnosis: a concurrent POST /resume or message to a suspended session at
// a tenant quota answered something other than 200 or 429, an admitted call
// left its session suspended or a refused call moved it, held a refused
// message, or the quota admitted a further resume after the admissions
// settled at or above the limit. The check-then-write in admitResume and
// enterResumePending is no longer all-or-nothing per call, or the count
// predicate drifted.
func TestConcurrentSuspendedResumesAtQuota_spec_11_1(t *testing.T) {
	const (
		limit     = 4
		suspended = 32
	)
	ctx := context.Background()
	store := memstore.New()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme", MaxConcurrentSessions: limit}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	for i := 0; i < limit-1; i++ {
		if err := store.Create(ctx, sessionstore.Session{
			UserID: ownerprincipal.Alice,
			ID:     fmt.Sprintf("running-%d", i), TenantID: "acme", RuntimeRef: "echo", State: session.StateRunning,
		}); err != nil {
			t.Fatalf("seed running: %v", err)
		}
	}
	for i := 0; i < suspended+1; i++ {
		if err := store.Create(ctx, sessionstore.Session{
			UserID: ownerprincipal.Alice,
			ID:     fmt.Sprintf("suspended-%d", i), TenantID: "acme", RuntimeRef: "echo", State: session.StateSuspended,
		}); err != nil {
			t.Fatalf("seed suspended: %v", err)
		}
	}
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	coord := sessioninbox.NewCoordinator(sessioninbox.Config{
		Inbox: sessioninbox.NewMemoryInbox(10),
		DLQ:   sessioninbox.NewDLQ(rc, 10),
	})
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader: true,
		Tenants:         tenants, Executor: executor.NewEchoExecutor(), Messaging: coord,
		PodBinder: &podsession.Binder{}, PodRegistry: podsession.NewRegistry(),
	})
	t.Cleanup(srv.Close)
	// spec: §10.2 (Session ownership) — the requests run as the seeded rows' owner.
	h := ownerprincipal.Wrap(srv.Handler(), ownerprincipal.Alice)

	codes := make([]int, suspended)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < suspended; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			path := fmt.Sprintf("/v1/sessions/suspended-%d/resume", i)
			body := ""
			if i%2 == 1 {
				path = fmt.Sprintf("/v1/sessions/suspended-%d/messages", i)
				body = `{"messages":[{"role":"user","content":"x"}]}`
			}
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			req.Header.Set("X-Lenny-Tenant-ID", "acme")
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			codes[i] = rec.Code
		}(i)
	}
	close(start)
	wg.Wait()

	admitted := 0
	for i, code := range codes {
		id := fmt.Sprintf("suspended-%d", i)
		row, err := store.Get(ctx, "acme", id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		held, err := coord.HeldLen(ctx, "acme", id, true, time.Time{})
		if err != nil {
			t.Fatalf("held length of %s: %v", id, err)
		}
		message := i%2 == 1
		switch {
		case code == http.StatusTooManyRequests:
			if row.State != session.StateSuspended || held != 0 {
				t.Errorf("%s refused but %q with %d held, want suspended with nothing held", id, row.State, held)
			}
		case code != http.StatusOK:
			t.Errorf("%s answered %d, want 200 or 429", id, code)
		case row.State == session.StateResumePending:
			admitted++
		case message && row.State == session.StateSuspended && held == 1:
			// Admitted at the entry check and refused at the resume write:
			// the message waits in the DLQ for a later resume.
		default:
			t.Errorf("%s answered 200 but is %q with %d held", id, row.State, held)
		}
		if message && code == http.StatusOK && held != 1 {
			t.Errorf("%s message answered 200 with %d held, want it held", id, held)
		}
	}
	if admitted == 0 {
		t.Fatal("no resume was admitted with one free slot")
	}
	// The admissions have settled at or above the limit, so the next resume
	// is refused.
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/v1/sessions/suspended-%d/resume", suspended), nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("resume after the admissions settled = %d, want 429; %d admitted", rec.Code, admitted)
	}
	t.Logf("admitted %d of %d concurrent resumes with one free slot", admitted, suspended)
}
