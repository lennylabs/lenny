// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// primaryMovingStore moves the row with move right after the first primary
// read returns, so a handler that decided on that read meets a different
// locked row.
type primaryMovingStore struct {
	*memstore.Store
	once sync.Once
	move func(*sessionstore.Session)
}

func (p *primaryMovingStore) GetPrimary(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	row, err := p.Get(ctx, tenantID, id)
	if p.move != nil {
		p.once.Do(func() {
			_, _ = p.Update(ctx, tenantID, id, func(r *sessionstore.Session) error {
				p.move(r)
				return nil
			})
		})
	}
	return row, err
}

type resumeAnswer struct {
	State string `json:"state"`
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

func postResume(t *testing.T, srv *Server, id string) (int, resumeAnswer) {
	t.Helper()
	return postPath(t, srv, "/v1/sessions/"+id+"/resume")
}

func postPath(t *testing.T, srv *Server, path string) (int, resumeAnswer) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	asOwner(srv.Handler()).ServeHTTP(rr, req)
	var ans resumeAnswer
	_ = json.Unmarshal(rr.Body.Bytes(), &ans)
	return rr.Code, ans
}

// TestResumeEndpointIsAStoreWrite_spec_7_3 asserts that POST /resume from
// awaiting_client_action, and from a suspended session that holds no pod,
// writes resume_pending with RetryCount unchanged, counts no retry, claims no
// pod, and runs no post-commit effect; that from a held-pod suspended row on
// the coordinating replica it answers running, including with no binder
// wired; that a row moved after the read answers resume_pending when it
// reached awaiting_client_action and the precondition error when it reached
// running; and that the binderless driver then takes resume_pending to
// running with no claim.
//
// spec: §7.3 (Retry and Resume), §15.1 (REST API)
func TestResumeEndpointIsAStoreWrite_spec_7_3(t *testing.T) {
	t.Run("from awaiting_client_action", func(t *testing.T) {
		store := &primaryMovingStore{Store: memstore.New()}
		bus := sessionevents.NewBus(64)
		retries := 0
		srv := New(store, Options{DevTenantHeader: true, Events: bus, IncSessionRetry: func(string) { retries++ }})
		defer srv.Close()
		if err := store.Create(context.Background(), sessionstore.Session{
			ID: "s", TenantID: "acme", RuntimeRef: "echo", State: session.StateAwaitingClientAction, RetryCount: 2,
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		code, ans := postResume(t, srv, "s")
		got, _ := store.Get(context.Background(), "acme", "s")
		if code != http.StatusOK || ans.State != string(session.StateResumePending) || got.RetryCount != 2 || retries != 0 {
			t.Errorf("answer %d %q, retry %d, retry metric %d; want 200 resume_pending with RetryCount 2 and no metric",
				code, ans.State, got.RetryCount, retries)
		}
		if n := eventCount(bus, "s", "session.resumed"); n != 0 {
			t.Errorf("session.resumed events = %d, want none from the store write", n)
		}
		// The binderless driver then commits it to running with no claim.
		srv.DriveResumeAndWait(context.Background(), got)
		if after, _ := store.Get(context.Background(), "acme", "s"); after.State != session.StateRunning {
			t.Errorf("state after the drive = %q, want running", after.State)
		}
	})

	t.Run("from a suspended session that holds no pod", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{})
		if err := f.store.Create(context.Background(), sessionstore.Session{
			ID: "s", TenantID: "acme", RuntimeRef: "echo", State: session.StateSuspended,
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		code, ans := postResume(t, f.srv, "s")
		if code != http.StatusOK || ans.State != string(session.StateResumePending) {
			t.Errorf("answer %d %q, want 200 resume_pending", code, ans.State)
		}
		if calls := f.clientCalls.list(); len(calls) != 0 {
			t.Errorf("cluster calls = %v, want none: the store write claims no pod", calls)
		}
	})

	t.Run("held pod with no binder wired", func(t *testing.T) {
		store := &primaryMovingStore{Store: memstore.New()}
		srv := New(store, Options{DevTenantHeader: true})
		defer srv.Close()
		if err := store.Create(context.Background(), sessionstore.Session{
			ID: "s", TenantID: "acme", RuntimeRef: "echo", State: session.StateSuspended, PodAssignment: "pod-a",
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if code, ans := postResume(t, srv, "s"); code != http.StatusOK || ans.State != string(session.StateRunning) {
			t.Errorf("answer %d %q, want 200 running", code, ans.State)
		}
	})

	for _, tc := range []struct {
		name     string
		reaches  session.State
		wantCode int
		want     string
	}{
		{"moved to awaiting_client_action after the read", session.StateAwaitingClientAction, http.StatusOK, string(session.StateResumePending)},
		{"moved to running after the read", session.StateRunning, http.StatusConflict, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &primaryMovingStore{Store: memstore.New(), move: func(r *sessionstore.Session) { r.State = tc.reaches }}
			srv := New(store, Options{DevTenantHeader: true})
			defer srv.Close()
			if err := store.Create(context.Background(), sessionstore.Session{
				ID: "s", TenantID: "acme", RuntimeRef: "echo", State: session.StateSuspended, PodAssignment: "pod-a",
			}); err != nil {
				t.Fatalf("seed: %v", err)
			}
			code, ans := postResume(t, srv, "s")
			if code != tc.wantCode || (tc.want != "" && ans.State != tc.want) {
				t.Errorf("answer %d %q (%s), want %d %q", code, ans.State, ans.Error.Code, tc.wantCode, tc.want)
			}
			if tc.wantCode == http.StatusConflict && ans.Error.Code != "INVALID_STATE_TRANSITION" {
				t.Errorf("error code = %q, want INVALID_STATE_TRANSITION", ans.Error.Code)
			}
		})
	}
}

// TestResumeOfAHeldPodOnAPeerAnswersTargetNotReady_spec_29_3 asserts that a
// POST /resume on a held-pod suspended row served by a replica whose lease
// read names another replica, or fails, answers 409 TARGET_NOT_READY and
// leaves the row and its held messages unchanged.
//
// spec: §29.3 (Interactive message send)
func TestResumeOfAHeldPodOnAPeerAnswersTargetNotReady_spec_29_3(t *testing.T) {
	for _, holder := range []string{"rep-2", ""} {
		name := map[string]string{"rep-2": "peer holds the lease", "": "lease read fails"}[holder]
		t.Run(name, func(t *testing.T) {
			f := newFunnelFixture(t, funnelOpts{})
			held := newHeldFixture(t, false)
			f.srv.messaging = held.coord
			if err := f.store.Create(context.Background(), sessionstore.Session{
				ID: "s", TenantID: "acme", RuntimeRef: "echo", State: session.StateSuspended, PodAssignment: "pod-a",
			}); err != nil {
				t.Fatalf("seed: %v", err)
			}
			if holder != "" {
				f.leases.holders[leaseK("acme", "s")] = holder
			}
			if _, err := held.coord.EnqueueDLQ(context.Background(), "acme", "s", sessioninbox.Message{
				MessageID: "m1", Payload: []byte(`{}`), EnqueuedAt: time.Now(),
			}, 0); err != nil {
				t.Fatalf("hold: %v", err)
			}
			before := f.state(t, "s")
			code, ans := postResume(t, f.srv, "s")
			if code != http.StatusConflict || ans.Error.Code != "TARGET_NOT_READY" {
				t.Errorf("answer %d %q, want 409 TARGET_NOT_READY", code, ans.Error.Code)
			}
			after := f.state(t, "s")
			if after.State != before.State || !after.UpdatedAt.Equal(before.UpdatedAt) {
				t.Errorf("row = %q, want unchanged %q", after.State, before.State)
			}
			if n, _ := held.dlq.Len(context.Background(), "acme", "s"); n != 1 {
				t.Errorf("held messages = %d, want 1 unchanged", n)
			}
		})
	}
}
