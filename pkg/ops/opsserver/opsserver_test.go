// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/ops/probe"
)

func TestHealthzReportsOK(t *testing.T) {
	rec := httptest.NewRecorder()
	opsserver.New(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %q, want ok", body["status"])
	}
}

func TestReadyzReportsReady(t *testing.T) {
	rec := httptest.NewRecorder()
	opsserver.New(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Status       string                    `json:"status"`
		Dependencies map[string]map[string]any `json:"dependencies"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "ready" {
		t.Errorf("status = %q, want ready", body.Status)
	}
	if len(body.Dependencies) != 0 {
		t.Errorf("dependencies = %v, want empty with no probes configured", body.Dependencies)
	}
}

func TestReadyzReportsDependencyHealth(t *testing.T) {
	probes := map[string]probe.Func{
		"postgres": func(context.Context) error { return nil },
		"redis":    func(context.Context) error { return errors.New("connection refused") },
	}
	rec := httptest.NewRecorder()
	opsserver.New(probes).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	// §25: lenny-ops degrades gracefully — it stays ready (200) while a
	// dependency is down and reports the per-dependency status.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Status       string                    `json:"status"`
		Dependencies map[string]map[string]any `json:"dependencies"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if ok, _ := body.Dependencies["postgres"]["ok"].(bool); !ok {
		t.Errorf("postgres ok = %v, want true", body.Dependencies["postgres"]["ok"])
	}
	if ok, _ := body.Dependencies["redis"]["ok"].(bool); ok {
		t.Errorf("redis ok = %v, want false", body.Dependencies["redis"]["ok"])
	}
}

func TestUnknownPathIs404(t *testing.T) {
	rec := httptest.NewRecorder()
	opsserver.New(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/ops/nonexistent", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an unregistered path", rec.Code)
	}
}

func TestHealthzRejectsNonGET(t *testing.T) {
	rec := httptest.NewRecorder()
	opsserver.New(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/healthz", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405 for POST /healthz", rec.Code)
	}
}
