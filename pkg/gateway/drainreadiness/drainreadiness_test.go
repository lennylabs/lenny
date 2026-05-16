// SPDX-License-Identifier: MIT

package drainreadiness_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/drainreadiness"
)

// spec: §12.5 — the GET /internal/drain-readiness endpoint runs a MinIO
// liveness probe and reports whether a node drain may proceed.

func get(h *drainreadiness.Handler) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/internal/drain-readiness", nil))
	return rr
}

func TestReadyWhenProbeSucceeds(t *testing.T) {
	h := &drainreadiness.Handler{
		Prober: drainreadiness.ProberFunc(func(context.Context) error { return nil }),
	}
	rr := get(h)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ready" || body["minio"] != "healthy" {
		t.Errorf("body = %v, want the §12.5 ready response", body)
	}
}

func TestNotReadyWhenProbeFails(t *testing.T) {
	h := &drainreadiness.Handler{
		Prober: drainreadiness.ProberFunc(func(context.Context) error {
			return errors.New("HeadBucket timed out")
		}),
	}
	rr := get(h)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "not_ready" || body["minio"] != "unhealthy" {
		t.Errorf("body = %v, want the §12.5 not-ready response", body)
	}
	if body["reason"] != "HeadBucket timed out" {
		t.Errorf("reason = %q, want the probe error", body["reason"])
	}
}

func TestRejectsNonGet(t *testing.T) {
	h := &drainreadiness.Handler{
		Prober: drainreadiness.ProberFunc(func(context.Context) error { return nil }),
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/internal/drain-readiness", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rr.Code)
	}
}

func TestProbeReceivesABoundedContext(t *testing.T) {
	var hadDeadline bool
	h := &drainreadiness.Handler{
		Timeout: 50 * time.Millisecond,
		Prober: drainreadiness.ProberFunc(func(ctx context.Context) error {
			_, hadDeadline = ctx.Deadline()
			return nil
		}),
	}
	if rr := get(h); rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !hadDeadline {
		t.Error("the probe context carried no deadline — the §12.5 probe timeout was not applied")
	}
}

func TestSlowProbeTimesOutAsNotReady(t *testing.T) {
	h := &drainreadiness.Handler{
		Timeout: 20 * time.Millisecond,
		Prober: drainreadiness.ProberFunc(func(ctx context.Context) error {
			<-ctx.Done() // a hung artifact store
			return ctx.Err()
		}),
	}
	rr := get(h)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 for a probe that exceeds the timeout", rr.Code)
	}
}
