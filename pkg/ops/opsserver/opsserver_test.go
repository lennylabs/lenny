// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/ops/probe"
)

// spec: §25.4 "Probes" — the permissive /healthz cannot confirm either
// dependency reachable when neither probe is registered, so it returns
// 503 rather than an unconditional 200.
func TestHealthzPermissive503WithNoProbes(t *testing.T) {
	rec := httptest.NewRecorder()
	opsserver.New(opsserver.Options{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 with no dependency probe registered", rec.Code)
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
	if !sameSet(body.Degraded, []string{"postgres", "kubernetes"}) {
		t.Errorf("degraded = %v, want postgres and kubernetes", body.Degraded)
	}
}

// spec: §25.4 — F-25.4.10. The strict diagnostic check (?strict=true)
// returns 200 only when both Postgres and the Kubernetes API are
// reachable.
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

// spec: §25.4 — F-25.4.10. A strict-gate dependency that is
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

// spec: §25.4 — F-25.4.10. A strict-gate dependency that is
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

// spec: §25.4 "Probes" — the permissive /healthz returns 503 naming both
// dependencies when neither Postgres nor the Kubernetes API is
// reachable, so liveness restarts a pod that can reach no dependency.
func TestHealthzPermissive503WhenBothDepsDown(t *testing.T) {
	probes := map[string]probe.Func{
		"postgres":   func(context.Context) error { return errors.New("down") },
		"kubernetes": func(context.Context) error { return errors.New("down") },
	}
	rec := httptest.NewRecorder()
	opsserver.New(opsserver.Options{Probes: probes}).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when both dependencies are down", rec.Code)
	}
	body := decodeHealthz(t, rec)
	if body.Status != "degraded" {
		t.Errorf("status = %q, want degraded", body.Status)
	}
	if !sameSet(body.Degraded, []string{"postgres", "kubernetes"}) {
		t.Errorf("degraded = %v, want postgres and kubernetes", body.Degraded)
	}
	for _, dep := range []string{"postgres", "kubernetes"} {
		if _, ok := body.Dependencies[dep]; !ok {
			t.Errorf("dependencies = %v, want an entry for %s", body.Dependencies, dep)
		}
	}
}

// healthDepState is how a /healthz dependency probe behaves in
// TestHealthzPermissiveEitherDependency.
type healthDepState int

const (
	depReachable healthDepState = iota
	depFailing
	depBlocking
	depAbsent
)

func (st healthDepState) String() string {
	return [...]string{"reachable", "failing", "blocking", "absent"}[st]
}

// register adds a probe named name behaving as st to probes.
func (st healthDepState) register(probes map[string]probe.Func, name string) {
	switch st {
	case depReachable:
		probes[name] = func(context.Context) error { return nil }
	case depFailing:
		probes[name] = func(context.Context) error { return errors.New("connection refused") }
	case depBlocking:
		probes[name] = func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}
	case depAbsent:
	}
}

// countingProbe returns a reachable probe that counts its invocations.
func countingProbe(calls *atomic.Int32) probe.Func {
	return func(context.Context) error {
		calls.Add(1)
		return nil
	}
}

// spec: §25.4 "Probes" — the permissive /healthz returns 200 exactly
// when at least one of Postgres and the Kubernetes API is reachable, and
// otherwise 503 with a body naming both. A blocking probe is bounded by
// the probe timeout and counts as unreachable, and an absent probe counts
// as unreachable. The permissive check never invokes the Redis, MinIO,
// or gateway probes.
func TestHealthzPermissiveEitherDependency(t *testing.T) {
	states := []healthDepState{depReachable, depFailing, depBlocking, depAbsent}
	for _, pg := range states {
		for _, k8s := range states {
			t.Run(fmt.Sprintf("postgres=%s/kubernetes=%s", pg, k8s), func(t *testing.T) {
				t.Parallel()
				probes := map[string]probe.Func{}
				pg.register(probes, "postgres")
				k8s.register(probes, "kubernetes")
				var others [3]atomic.Int32
				for i, name := range []string{"redis", "minio", "gateway"} {
					probes[name] = countingProbe(&others[i])
				}

				rec := httptest.NewRecorder()
				opsserver.New(opsserver.Options{Probes: probes}).ServeHTTP(rec,
					httptest.NewRequest(http.MethodGet, "/healthz", nil))

				assertEitherDependencyOutcome(t, rec, pg, k8s)
				for i, name := range []string{"redis", "minio", "gateway"} {
					if n := others[i].Load(); n != 0 {
						t.Errorf("%s probe invoked %d times, want 0", name, n)
					}
				}
			})
		}
	}
}

// assertEitherDependencyOutcome checks the permissive /healthz response
// for the given dependency states.
func assertEitherDependencyOutcome(t *testing.T, rec *httptest.ResponseRecorder, pg, k8s healthDepState) {
	t.Helper()
	body := decodeHealthz(t, rec)
	if pg == depReachable || k8s == depReachable {
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 when a dependency is reachable", rec.Code)
		}
		if body.Status != "ok" {
			t.Errorf("status = %q, want ok", body.Status)
		}
		return
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when neither dependency is reachable", rec.Code)
	}
	if body.Status != "degraded" {
		t.Errorf("status = %q, want degraded", body.Status)
	}
	if !sameSet(body.Degraded, []string{"postgres", "kubernetes"}) {
		t.Errorf("degraded = %v, want postgres and kubernetes", body.Degraded)
	}
	if body.Dependencies == nil {
		t.Errorf("body carries no dependencies field")
	}
}

// healthzBody is the decoded /healthz response.
type healthzBody struct {
	Status       string                    `json:"status"`
	Degraded     []string                  `json:"degraded"`
	Dependencies map[string]map[string]any `json:"dependencies"`
}

func decodeHealthz(t *testing.T, rec *httptest.ResponseRecorder) healthzBody {
	t.Helper()
	var body healthzBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

// sameSet reports whether got and want hold the same strings.
func sameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]bool, len(got))
	for _, g := range got {
		seen[g] = true
	}
	for _, w := range want {
		if !seen[w] {
			return false
		}
	}
	return true
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
