// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/upgrade/runtimeupgrade"
	"github.com/lennylabs/lenny/pkg/gateway/upgrade/runtimeupgradestore"
)

func newUpgradeAdmin(t *testing.T) *admin.Router {
	t.Helper()
	clk := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	store := runtimeupgradestore.NewMemory().WithClock(func() time.Time { return clk })
	pools := fakeUpgradePool{specs: map[string][]byte{"claude-worker": []byte(`{"minWarm":2}`)}}
	mgr := runtimeupgrade.NewManager(
		store,
		runtimeupgrade.WithPoolReader(pools),
		runtimeupgrade.WithClock(func() time.Time { return clk }),
	)
	return admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return clk },
	}).WithRuntimeUpgrade(mgr)
}

type fakeUpgradePool struct{ specs map[string][]byte }

func (f fakeUpgradePool) PoolSpec(_ context.Context, pool string) ([]byte, bool, error) {
	spec, ok := f.specs[pool]
	return spec, ok, nil
}

func upgReq(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
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

func decodeUpgrade(t *testing.T, rr *httptest.ResponseRecorder) admin.UpgradeStatus {
	t.Helper()
	var s admin.UpgradeStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode upgrade status: %v (body=%s)", err, rr.Body.String())
	}
	return s
}

func upgErrCode(t *testing.T, rr *httptest.ResponseRecorder) string {
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

// spec: §10.5 lines 466-540 / §15.1 lines 869-874 — the operator drives a
// full rollout through the admin API: start -> proceed* -> status, with
// every route platform-admin only.
func TestUpgradeAPI_fullLifecycle_spec_10_5(t *testing.T) {
	h := newUpgradeAdmin(t).Handler()
	base := "/v1/admin/pools/claude-worker/upgrade"

	rr := upgReq(t, h, http.MethodPost, base+"/start",
		admin.StartUpgradeRequest{NewImage: "registry/img@sha256:abc", CanaryPercent: 10})
	if rr.Code != http.StatusOK {
		t.Fatalf("start: %d body=%s", rr.Code, rr.Body.String())
	}
	if s := decodeUpgrade(t, rr); s.Phase != "pending" || s.CanaryPercent != 10 || !s.HasPreviousPoolSpec {
		t.Fatalf("start status = %+v", s)
	}

	for _, want := range []string{"expanding", "draining", "contracting", "complete"} {
		rr = upgReq(t, h, http.MethodPost, base+"/proceed", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("proceed to %s: %d body=%s", want, rr.Code, rr.Body.String())
		}
		if s := decodeUpgrade(t, rr); s.Phase != want {
			t.Fatalf("phase = %q, want %q", s.Phase, want)
		}
	}

	rr = upgReq(t, h, http.MethodGet, "/v1/admin/pools/claude-worker/upgrade-status", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if s := decodeUpgrade(t, rr); s.Phase != "complete" {
		t.Fatalf("final status = %+v", s)
	}
}

// Start against an unknown pool is 404; an empty image is 400.
func TestUpgradeAPI_startErrors_spec_10_5(t *testing.T) {
	h := newUpgradeAdmin(t).Handler()
	rr := upgReq(t, h, http.MethodPost, "/v1/admin/pools/ghost/upgrade/start",
		admin.StartUpgradeRequest{NewImage: "img"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("start ghost: %d, want 404", rr.Code)
	}
	rr = upgReq(t, h, http.MethodPost, "/v1/admin/pools/claude-worker/upgrade/start",
		admin.StartUpgradeRequest{NewImage: ""})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("start empty image: %d, want 400", rr.Code)
	}
}

// An out-of-order proceed (past Complete) maps to 409
// INVALID_STATE_TRANSITION; a second start while active is 409
// INVALID_STATE_TRANSITION (the ErrUpgradeActive state conflict; spec: §15.1 line 981).
func TestUpgradeAPI_conflictMapping_spec_10_5(t *testing.T) {
	h := newUpgradeAdmin(t).Handler()
	base := "/v1/admin/pools/claude-worker/upgrade"
	if rr := upgReq(t, h, http.MethodPost, base+"/start", admin.StartUpgradeRequest{NewImage: "v1"}); rr.Code != http.StatusOK {
		t.Fatalf("start: %d", rr.Code)
	}
	rr := upgReq(t, h, http.MethodPost, base+"/start", admin.StartUpgradeRequest{NewImage: "v2"})
	if rr.Code != http.StatusConflict || upgErrCode(t, rr) != "INVALID_STATE_TRANSITION" {
		t.Fatalf("second start: %d code=%s", rr.Code, upgErrCode(t, rr))
	}
	for i := 0; i < 4; i++ {
		if rr := upgReq(t, h, http.MethodPost, base+"/proceed", nil); rr.Code != http.StatusOK {
			t.Fatalf("proceed %d: %d", i, rr.Code)
		}
	}
	rr = upgReq(t, h, http.MethodPost, base+"/proceed", nil)
	if rr.Code != http.StatusConflict || upgErrCode(t, rr) != "INVALID_STATE_TRANSITION" {
		t.Fatalf("proceed past complete: %d code=%s", rr.Code, upgErrCode(t, rr))
	}
}

// Status / proceed against a pool with no registered upgrade are 404.
func TestUpgradeAPI_notFound_spec_10_5(t *testing.T) {
	h := newUpgradeAdmin(t).Handler()
	if rr := upgReq(t, h, http.MethodGet, "/v1/admin/pools/claude-worker/upgrade-status", nil); rr.Code != http.StatusNotFound {
		t.Fatalf("status no upgrade: %d, want 404", rr.Code)
	}
	if rr := upgReq(t, h, http.MethodPost, "/v1/admin/pools/claude-worker/upgrade/proceed", nil); rr.Code != http.StatusNotFound {
		t.Fatalf("proceed no upgrade: %d, want 404", rr.Code)
	}
}

// spec: §10.5 line 494 — pause records the reason; resume restores the
// prior phase. The HTTP round-trip preserves both.
func TestUpgradeAPI_pauseResume_spec_10_5(t *testing.T) {
	h := newUpgradeAdmin(t).Handler()
	base := "/v1/admin/pools/claude-worker/upgrade"
	upgReq(t, h, http.MethodPost, base+"/start", admin.StartUpgradeRequest{NewImage: "v1"})
	upgReq(t, h, http.MethodPost, base+"/proceed", nil) // -> expanding
	rr := upgReq(t, h, http.MethodPost, base+"/pause", admin.PauseUpgradeRequest{Reason: "regressed"})
	if rr.Code != http.StatusOK {
		t.Fatalf("pause: %d", rr.Code)
	}
	if s := decodeUpgrade(t, rr); s.Phase != "paused" || s.PriorPhase != "expanding" || s.PauseReason != "regressed" {
		t.Fatalf("pause status = %+v", s)
	}
	rr = upgReq(t, h, http.MethodPost, base+"/resume", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("resume: %d", rr.Code)
	}
	if s := decodeUpgrade(t, rr); s.Phase != "expanding" {
		t.Fatalf("resume status = %+v", s)
	}
}

// A nil manager leaves every /upgrade route unregistered (404).
func TestUpgradeAPI_unregisteredWhenManagerNil(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Unix(0, 0).UTC() },
	})
	rr := upgReq(t, router.Handler(), http.MethodGet, "/v1/admin/pools/p/upgrade-status", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("GET with nil manager: %d, want 404", rr.Code)
	}
}
