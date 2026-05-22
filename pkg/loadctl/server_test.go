// SPDX-License-Identifier: MIT

package loadctl

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	s, err := NewServer(Config{StorageURL: "s3://test"})
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
	deadline := time.Now().Add(2 * time.Second)
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
