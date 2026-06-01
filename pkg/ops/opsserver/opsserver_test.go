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
	opsserver.New(opsserver.Options{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

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

// spec: §25.4 line 1055 — F-25.4.10. The strict gate (?strict=true)
// returns 200 only when both Postgres and the Kubernetes API are
// reachable; the readiness probe uses it so traffic is not routed to a
// replica that cannot serve the full §25 API.
func TestHealthzStrictReadyWhenCriticalDepsUp(t *testing.T) {
	probes := map[string]probe.Func{
		"postgres":   func(context.Context) error { return nil },
		"kubernetes": func(context.Context) error { return nil },
		"redis":      func(context.Context) error { return errors.New("down") },
	}
	rec := httptest.NewRecorder()
	opsserver.New(opsserver.Options{Probes: probes}).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/healthz?strict=true", nil))

	// Redis is degraded but not a strict-gate dependency, so the replica
	// is ready.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 when Postgres and K8s are up", rec.Code)
	}
	var body struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
}

// spec: §25.4 line 1055 — F-25.4.10. A strict-gate dependency that is
// down yields 503 with the degraded dependency named.
func TestHealthzStrict503WhenCriticalDepDown(t *testing.T) {
	probes := map[string]probe.Func{
		"postgres":   func(context.Context) error { return errors.New("connection refused") },
		"kubernetes": func(context.Context) error { return nil },
	}
	rec := httptest.NewRecorder()
	opsserver.New(opsserver.Options{Probes: probes}).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/healthz?strict=true", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when Postgres is down", rec.Code)
	}
	var body struct {
		Status   string   `json:"status"`
		Degraded []string `json:"degraded"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "degraded" {
		t.Errorf("status = %q, want degraded", body.Status)
	}
	found := false
	for _, d := range body.Degraded {
		if d == "postgres" {
			found = true
		}
	}
	if !found {
		t.Errorf("degraded = %v, want it to name postgres", body.Degraded)
	}
}

// spec: §25.4 line 1055 — F-25.4.10. A strict-gate dependency that is
// not even registered cannot be confirmed reachable and counts as
// degraded (the production binary always wires both).
func TestHealthzStrict503WhenCriticalDepAbsent(t *testing.T) {
	probes := map[string]probe.Func{
		"postgres": func(context.Context) error { return nil },
		// kubernetes probe intentionally absent.
	}
	rec := httptest.NewRecorder()
	opsserver.New(opsserver.Options{Probes: probes}).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/healthz?strict=true", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when the K8s probe is unwired", rec.Code)
	}
	var body struct {
		Degraded []string `json:"degraded"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	found := false
	for _, d := range body.Degraded {
		if d == "kubernetes" {
			found = true
		}
	}
	if !found {
		t.Errorf("degraded = %v, want it to name kubernetes", body.Degraded)
	}
}

// spec: §25.4 lines 1054-1057 — F-25.4.10. The permissive /healthz
// (liveness, startup) stays 200 even when a dependency is down so a
// transient outage does not restart an otherwise-healthy pod.
func TestHealthzPermissiveIgnoresDependencyOutage(t *testing.T) {
	probes := map[string]probe.Func{
		"postgres":   func(context.Context) error { return errors.New("down") },
		"kubernetes": func(context.Context) error { return errors.New("down") },
	}
	rec := httptest.NewRecorder()
	opsserver.New(opsserver.Options{Probes: probes}).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for the permissive liveness probe", rec.Code)
	}
}

func TestReadyzReportsReady(t *testing.T) {
	rec := httptest.NewRecorder()
	opsserver.New(opsserver.Options{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

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
	opsserver.New(opsserver.Options{Probes: probes}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

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

func TestConnectivityReportsDependencyHealth(t *testing.T) {
	probes := map[string]probe.Func{
		"postgres": func(context.Context) error { return nil },
		"redis":    func(context.Context) error { return nil },
		"minio":    func(context.Context) error { return errors.New("dial timeout") },
	}
	rec := httptest.NewRecorder()
	opsserver.New(opsserver.Options{Probes: probes}).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/v1/admin/diagnostics/connectivity", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Dependencies map[string]map[string]any `json:"dependencies"`
		Healthy      bool                      `json:"healthy"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Healthy {
		t.Error("healthy = true, want false when a dependency probe fails")
	}
	if len(body.Dependencies) != 3 {
		t.Fatalf("got %d dependencies, want 3", len(body.Dependencies))
	}
	if ok, _ := body.Dependencies["minio"]["ok"].(bool); ok {
		t.Error("minio reported ok, want a failure")
	}
	if body.Dependencies["minio"]["detail"] != "dial timeout" {
		t.Errorf("minio detail = %v, want the probe failure", body.Dependencies["minio"]["detail"])
	}
}

func TestConnectivityHealthyWithAllProbesPassing(t *testing.T) {
	probes := map[string]probe.Func{
		"postgres": func(context.Context) error { return nil },
	}
	rec := httptest.NewRecorder()
	opsserver.New(opsserver.Options{Probes: probes}).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/v1/admin/diagnostics/connectivity", nil))

	var body struct {
		Healthy bool `json:"healthy"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if !body.Healthy {
		t.Error("healthy = false, want true when every probe passes")
	}
}

func TestUnknownPathIs404(t *testing.T) {
	rec := httptest.NewRecorder()
	opsserver.New(opsserver.Options{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/ops/nonexistent", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an unregistered path", rec.Code)
	}
}

func TestHealthzRejectsNonGET(t *testing.T) {
	rec := httptest.NewRecorder()
	opsserver.New(opsserver.Options{}).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/healthz", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405 for POST /healthz", rec.Code)
	}
}

// fakeSelfHealth is a test SelfHealthReporter returning a fixed report.
type fakeSelfHealth struct{ body map[string]any }

func (f fakeSelfHealth) SelfHealth() map[string]any { return f.body }

// fakeLeader is a test LeaderReporter.
type fakeLeader struct {
	leader  bool
	running bool
	loops   []string
}

func (f fakeLeader) IsLeader() bool           { return f.leader }
func (f fakeLeader) LoopNames() []string      { return f.loops }
func (f fakeLeader) LeaderLoopsRunning() bool { return f.running }

func TestOpsHealthUnavailableWithoutReporter(t *testing.T) {
	rec := httptest.NewRecorder()
	opsserver.New(opsserver.Options{}).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/v1/admin/ops/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["status"] != "unknown" {
		t.Errorf("status = %v, want unknown when no self-health reporter is configured", body["status"])
	}
}

func TestOpsHealthServesSelfHealthReport(t *testing.T) {
	report := map[string]any{
		"status": "degraded",
		"checks": []map[string]any{{"name": "postgres_pool", "status": "degraded"}},
	}
	rec := httptest.NewRecorder()
	opsserver.New(opsserver.Options{SelfHealth: fakeSelfHealth{body: report}}).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/v1/admin/ops/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "degraded" {
		t.Errorf("status = %v, want degraded", body["status"])
	}
}

func TestReadyzReportsLeaderState(t *testing.T) {
	rec := httptest.NewRecorder()
	opsserver.New(opsserver.Options{Leader: fakeLeader{
		leader: true, running: true, loops: []string{"cron-evaluator", "webhook-delivery"},
	}}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Leader             bool     `json:"leader"`
		LeaderLoopsRunning bool     `json:"leaderLoopsRunning"`
		Loops              []string `json:"loops"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Leader || !body.LeaderLoopsRunning {
		t.Errorf("leader=%v running=%v, want both true", body.Leader, body.LeaderLoopsRunning)
	}
	if len(body.Loops) != 2 {
		t.Errorf("loops = %v, want the 2 configured loops", body.Loops)
	}
}

func TestReadyzOmitsLeaderStateWithoutReporter(t *testing.T) {
	rec := httptest.NewRecorder()
	opsserver.New(opsserver.Options{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if _, present := body["leader"]; present {
		t.Error("readyz reported a leader field with no LeaderReporter configured")
	}
}
