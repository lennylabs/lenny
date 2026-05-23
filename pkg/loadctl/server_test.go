// SPDX-License-Identifier: MIT

package loadctl

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	s, err := NewServer(Config{StorageURL: "file://" + t.TempDir()})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return httptest.NewServer(s.Handler())
}

func TestCreateRunSucceeds(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	body := bytes.NewBufferString(`{"scale":"small","scenarios":["default"],"cluster_release":"r1"}`)
	resp, err := http.Post(srv.URL+"/api/v1/runs", "application/json", body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d want 201", resp.StatusCode)
	}
	var run Run
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if run.ID == "" {
		t.Error("run.ID empty")
	}
	if run.Status != StatusPending {
		t.Errorf("status=%s want %s", run.Status, StatusPending)
	}
}

func TestGetRunReturnsRun(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	body := bytes.NewBufferString(`{"scale":"small"}`)
	resp, _ := http.Post(srv.URL+"/api/v1/runs", "application/json", body)
	var created Run
	_ = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	// Wait for the simulated run to finish.
	deadline := time.Now().Add(10 * time.Second)
	var got Run
	for time.Now().Before(deadline) {
		r, err := http.Get(srv.URL + "/api/v1/runs/" + created.ID)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		r.Body.Close()
		if got.Status == StatusPass {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got.Status != StatusPass {
		t.Errorf("status=%s want PASS within deadline", got.Status)
	}
	if got.ReportURL == "" {
		t.Error("ReportURL empty on PASS")
	}
}

func TestGetRunNotFound(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	r, _ := http.Get(srv.URL + "/api/v1/runs/missing")
	if r.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d want 404", r.StatusCode)
	}
}

func TestScenariosList(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	r, err := http.Get(srv.URL + "/api/v1/scenarios")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var got []Scenario
	if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Error("expected non-empty scenarios catalogue")
	}
}

func TestUnsupportedDatabaseScheme(t *testing.T) {
	_, err := NewServer(Config{DatabaseURL: "mysql://localhost/lenny"})
	if err == nil || !strings.Contains(err.Error(), "unsupported DatabaseURL scheme") {
		t.Errorf("expected unsupported-scheme error, got %v", err)
	}
}

func TestRunnerRegisterAndHeartbeat(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	// Register one runner.
	body := bytes.NewBufferString(`{"id":"runner-1","cloud":"aws","capacity":4}`)
	resp, err := http.Post(srv.URL+"/api/v1/runners/register", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register status=%d", resp.StatusCode)
	}
	resp.Body.Close()

	// Listing surfaces it as healthy.
	resp, _ = http.Get(srv.URL + "/api/v1/runners")
	var roster []Runner
	_ = json.NewDecoder(resp.Body).Decode(&roster)
	resp.Body.Close()
	if len(roster) != 1 || roster[0].ID != "runner-1" || !roster[0].Healthy {
		t.Fatalf("roster=%+v", roster)
	}

	// Heartbeat refreshes LastHeartbeat.
	beforeHB := roster[0].LastHeartbeat
	time.Sleep(20 * time.Millisecond)
	req, _ := http.NewRequest("POST", srv.URL+"/api/v1/runners/runner-1/heartbeat", nil)
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("heartbeat status=%d", resp.StatusCode)
	}
	resp.Body.Close()
	resp, _ = http.Get(srv.URL + "/api/v1/runners/runner-1")
	var got Runner
	_ = json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	if !got.LastHeartbeat.After(beforeHB) {
		t.Errorf("LastHeartbeat did not advance: before=%v after=%v", beforeHB, got.LastHeartbeat)
	}

	// 404 on heartbeat for an unknown runner.
	req, _ = http.NewRequest("POST", srv.URL+"/api/v1/runners/nobody/heartbeat", nil)
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("heartbeat unknown status=%d want 404", resp.StatusCode)
	}
	resp.Body.Close()

	// DELETE removes it.
	req, _ = http.NewRequest("DELETE", srv.URL+"/api/v1/runners/runner-1", nil)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	resp, _ = http.Get(srv.URL + "/api/v1/runners")
	roster = nil
	_ = json.NewDecoder(resp.Body).Decode(&roster)
	resp.Body.Close()
	if len(roster) != 0 {
		t.Errorf("roster after DELETE=%+v", roster)
	}
}

func TestShutdownCancelsBackgroundGoroutines(t *testing.T) {
	server, err := NewServer(Config{
		StorageURL:  "file://" + t.TempDir(),
		RunDuration: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	srv := httptest.NewServer(server.Handler())
	defer srv.Close()

	// Kick off a run that would otherwise take ~4s (scaffoldDuration).
	resp, _ := http.Post(srv.URL+"/api/v1/runs", "application/json",
		bytes.NewBufferString(`{"scale":"small","scenarios":["a","b","c"]}`))
	resp.Body.Close()

	// Shutdown promptly; the scaffold goroutine should observe
	// ctx.Done and return before the 2-second deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown returned err=%v after %s", err, time.Since(start))
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Shutdown took %s; want <2s (background goroutines did not honour ctx)", elapsed)
	}
}
