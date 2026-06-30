// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/carotation"
	"github.com/lennylabs/lenny/pkg/gateway/carotationstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// movableClock is a test clock the CA-rotation suite advances to
// traverse the §10.3 overlap window deterministically.
type movableClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *movableClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *movableClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newCARotationAdmin(t *testing.T) (*admin.Router, *movableClock) {
	t.Helper()
	clk := &movableClock{t: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)}
	store := carotationstore.NewMemory().WithClock(clk.now)
	mgr := carotation.NewManager(
		store,
		carotation.WithOverlapWindow(24*time.Hour),
		carotation.WithClock(clk.now),
	)
	if err := mgr.EnsureInitialized(t.Context(), "lenny-mtls-ca"); err != nil {
		t.Fatalf("init: %v", err)
	}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: clk.now,
	}).WithCARotation(mgr)
	return router, clk
}

func caReq(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	} else {
		buf = bytes.NewReader(nil)
	}
	req := withAdminPrincipal(httptest.NewRequest(method, path, buf))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func decodeCAStatus(t *testing.T, rr *httptest.ResponseRecorder) admin.CARotationStatus {
	t.Helper()
	var s admin.CARotationStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode status: %v (body=%s)", err, rr.Body.String())
	}
	return s
}

// spec: §10.3 lines 344-350 — the operator drives a full rotation
// through the admin API: status -> begin -> promote -> retire, with the
// overlap guard enforced before the window closes.
func TestCARotation_fullLifecycle_spec_10_3(t *testing.T) {
	router, clk := newCARotationAdmin(t)
	h := router.Handler()

	rr := caReq(t, h, http.MethodGet, "/v1/admin/ca-rotation", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if s := decodeCAStatus(t, rr); s.Stage != "idle" || s.CurrentCaId != "lenny-mtls-ca" {
		t.Fatalf("idle status = %+v", s)
	}

	rr = caReq(t, h, http.MethodPost, "/v1/admin/ca-rotation/begin",
		admin.BeginCARotationRequest{NewCaId: "lenny-mtls-ca-2"})
	if rr.Code != http.StatusOK {
		t.Fatalf("begin: %d body=%s", rr.Code, rr.Body.String())
	}
	s := decodeCAStatus(t, rr)
	if s.Stage != "new_ca_deployed" || len(s.TrustedCaIds) != 2 || s.OverlapStartedAt == "" {
		t.Fatalf("begin status = %+v", s)
	}

	rr = caReq(t, h, http.MethodPost, "/v1/admin/ca-rotation/promote", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("promote: %d body=%s", rr.Code, rr.Body.String())
	}
	if s := decodeCAStatus(t, rr); s.Stage != "promoted" || s.CurrentCaId != "lenny-mtls-ca-2" {
		t.Fatalf("promote status = %+v", s)
	}

	// Overlap window still open: retire is rejected with 409.
	rr = caReq(t, h, http.MethodPost, "/v1/admin/ca-rotation/retire", nil)
	if rr.Code != http.StatusConflict {
		t.Fatalf("retire before overlap close: %d, want 409", rr.Code)
	}
	if code := caErrCode(t, rr); code != "CA_ROTATION_OVERLAP_OPEN" {
		t.Fatalf("retire error code = %q, want CA_ROTATION_OVERLAP_OPEN", code)
	}

	clk.advance(48 * time.Hour)
	rr = caReq(t, h, http.MethodPost, "/v1/admin/ca-rotation/retire", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("retire: %d body=%s", rr.Code, rr.Body.String())
	}
	if s := decodeCAStatus(t, rr); s.Stage != "old_ca_retired" || len(s.TrustedCaIds) != 1 {
		t.Fatalf("retire status = %+v", s)
	}
}

// Wrong-stage transitions map to 409 INVALID_STATE_TRANSITION.
func TestCARotation_wrongStageReturns409(t *testing.T) {
	router, _ := newCARotationAdmin(t)
	h := router.Handler()
	rr := caReq(t, h, http.MethodPost, "/v1/admin/ca-rotation/promote", nil)
	if rr.Code != http.StatusConflict {
		t.Fatalf("promote from idle: %d, want 409", rr.Code)
	}
	if code := caErrCode(t, rr); code != "INVALID_STATE_TRANSITION" {
		t.Fatalf("error code = %q, want INVALID_STATE_TRANSITION", code)
	}
}

// An empty newCaId is a validation error (400).
func TestCARotation_beginRejectsEmptyCA(t *testing.T) {
	router, _ := newCARotationAdmin(t)
	h := router.Handler()
	rr := caReq(t, h, http.MethodPost, "/v1/admin/ca-rotation/begin",
		admin.BeginCARotationRequest{NewCaId: ""})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("begin empty CA: %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}
}

// A nil manager leaves the routes unregistered (mTLS disabled).
func TestCARotation_unregisteredWhenManagerNil(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Unix(0, 0).UTC() },
	})
	rr := caReq(t, router.Handler(), http.MethodGet, "/v1/admin/ca-rotation", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("GET with nil manager: %d, want 404", rr.Code)
	}
}

func caErrCode(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, rr.Body.String())
	}
	return env.Error.Code
}
