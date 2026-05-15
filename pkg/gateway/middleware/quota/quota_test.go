// SPDX-License-Identifier: MIT

package quota_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/middleware/quota"
)

// fakeCounter is an ActiveCounter returning a fixed count.
type fakeCounter struct {
	count int64
	err   error
}

func (f fakeCounter) CountActive(context.Context, string) (int64, error) {
	return f.count, f.err
}

func okHandler() (http.Handler, *bool) {
	called := new(bool)
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*called = true
		w.WriteHeader(http.StatusCreated)
	}), called
}

func TestQuotaAdmitsUnderLimit(t *testing.T) {
	inner, called := okHandler()
	h := quota.Wrap(inner, quota.Options{
		Counter: fakeCounter{count: 3},
		Limits:  quota.StaticLimit(10),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated || !*called {
		t.Errorf("under-limit create should pass: code=%d called=%v", rr.Code, *called)
	}
}

func TestQuotaRejectsAtLimit(t *testing.T) {
	inner, called := okHandler()
	h := quota.Wrap(inner, quota.Options{
		Counter: fakeCounter{count: 10},
		Limits:  quota.StaticLimit(10),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("at-limit create should be 429: got %d", rr.Code)
	}
	if *called {
		t.Error("inner handler must not be called when quota is exceeded")
	}
}

func TestQuotaRejectsSessionsStart(t *testing.T) {
	inner, _ := okHandler()
	h := quota.Wrap(inner, quota.Options{
		Counter: fakeCounter{count: 10},
		Limits:  quota.StaticLimit(10),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("/v1/sessions/start should be quota-gated: got %d", rr.Code)
	}
}

func TestQuotaIgnoresNonCreateRequests(t *testing.T) {
	inner, called := okHandler()
	h := quota.Wrap(inner, quota.Options{
		Counter: fakeCounter{count: 999},
		Limits:  quota.StaticLimit(10),
	})
	// A GET, or a non-create POST, must pass through even over quota.
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/v1/sessions"},
		{http.MethodPost, "/v1/sessions/sess_1/messages"},
		{http.MethodPost, "/v1/sessions/sess_1/finalize"},
	} {
		*called = false
		req := httptest.NewRequest(c.method, c.path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if !*called {
			t.Errorf("%s %s should pass through quota: code=%d", c.method, c.path, rr.Code)
		}
	}
}

func TestQuotaUnlimitedWhenLimitNonPositive(t *testing.T) {
	inner, called := okHandler()
	h := quota.Wrap(inner, quota.Options{
		Counter: fakeCounter{count: 1_000_000},
		Limits:  quota.StaticLimit(0), // unlimited
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if !*called {
		t.Error("limit<=0 means unlimited; create should pass")
	}
}

func TestQuotaFailsOpenOnCounterError(t *testing.T) {
	inner, called := okHandler()
	h := quota.Wrap(inner, quota.Options{
		Counter: fakeCounter{err: context.DeadlineExceeded},
		Limits:  quota.StaticLimit(1),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if !*called {
		t.Error("§11.2.1 fail-open: a counter error must not block creation")
	}
}

func TestQuotaPassThroughWhenUnconfigured(t *testing.T) {
	inner, called := okHandler()
	h := quota.Wrap(inner, quota.Options{}) // no Counter
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if !*called {
		t.Error("unconfigured quota middleware must pass through")
	}
}

func TestStoreActiveCounter(t *testing.T) {
	c := quota.StoreActiveCounter{
		List: func(context.Context, string) ([]session.State, error) {
			return []session.State{
				session.StateRunning,   // active
				session.StateCreated,   // active
				session.StateCompleted, // terminal — not counted
				session.StateFailed,    // terminal — not counted
				session.StateSuspended, // active
			}, nil
		},
	}
	n, err := c.CountActive(context.Background(), "acme")
	if err != nil {
		t.Fatalf("CountActive: %v", err)
	}
	if n != 3 {
		t.Errorf("active count: got %d, want 3 (terminal sessions excluded)", n)
	}
}
