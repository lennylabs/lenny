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

	"github.com/lennylabs/lenny/pkg/loadrunner/dispatch"
	"github.com/lennylabs/lenny/pkg/loadrunner/exec"
)

// TestEndToEndRunMovesToPass exercises the full control flow:
// HTTP POST /api/v1/runs → submit through the in-memory dispatcher
// → runner Receives + executes (noop) + posts /api/v1/ack → loadctl
// terminal-transitions the run to PASS.
func TestEndToEndRunMovesToPass(t *testing.T) {
	mem := dispatch.NewInMem(30 * time.Second)
	submitter := &dispatch.InMemSubmitter{Mem: mem}
	server, err := NewServer(Config{
		StorageURL: "s3://test",
		Submitter:  submitter,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()
	srv := httptest.NewServer(server.Handler())
	defer srv.Close()

	// Boot an in-process "runner" that pulls from mem and executes.
	runnerDone := make(chan struct{})
	runnerCtx, runnerCancel := context.WithCancel(context.Background())
	defer runnerCancel()
	go func() {
		defer close(runnerDone)
		for {
			recvCtx, cancel := context.WithTimeout(runnerCtx, 200*time.Millisecond)
			job, err := mem.Receive(recvCtx)
			cancel()
			if err != nil {
				if runnerCtx.Err() != nil {
					return
				}
				continue
			}
			cfg := exec.Config{
				Runner:     exec.NoopRunner{},
				LoadctlURL: srv.URL,
			}
			_, _ = exec.Execute(runnerCtx, cfg, job)
			_ = mem.Ack(runnerCtx, job)
		}
	}()

	// Drive the create-run path.
	body := bytes.NewBufferString(`{"scale":"small","scenarios":["session_throughput"],"cluster_release":"r1"}`)
	resp, err := http.Post(srv.URL+"/api/v1/runs", "application/json", body)
	if err != nil {
		t.Fatalf("POST runs: %v", err)
	}
	var created Run
	_ = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	// Poll until terminal.
	deadline := time.Now().Add(5 * time.Second)
	var final Run
	for time.Now().Before(deadline) {
		r, err := http.Get(srv.URL + "/api/v1/runs/" + created.ID)
		if err != nil {
			t.Fatalf("GET run: %v", err)
		}
		_ = json.NewDecoder(r.Body).Decode(&final)
		r.Body.Close()
		if final.Status == StatusPass || final.Status == StatusFail || final.Status == StatusAborted {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if final.Status != StatusPass {
		t.Errorf("status=%s want PASS", final.Status)
	}
	if final.ReportURL == "" {
		t.Errorf("ReportURL empty after PASS")
	}
	if !strings.Contains(final.CurrentMetrics, "session_throughput=PASS") {
		t.Errorf("CurrentMetrics=%q want session_throughput=PASS", final.CurrentMetrics)
	}

	runnerCancel()
	<-runnerDone
}

// TestRunnerAckRejectsUnknownRun confirms the ack callback returns
// 404 for unknown run IDs.
func TestRunnerAckRejectsUnknownRun(t *testing.T) {
	server, _ := NewServer(Config{StorageURL: "s3://test"})
	defer server.Close()
	srv := httptest.NewServer(server.Handler())
	defer srv.Close()

	body := bytes.NewBufferString(`{"run_id":"missing","scenario":"x","outcome":"PASS"}`)
	resp, _ := http.Post(srv.URL+"/api/v1/ack", "application/json", body)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d want 404", resp.StatusCode)
	}
}

// TestMultiScenarioRunTracksCompletion confirms a run with multiple
// scenarios stays RUNNING until every scenario has acked.
func TestMultiScenarioRunTracksCompletion(t *testing.T) {
	mem := dispatch.NewInMem(30 * time.Second)
	submitter := &dispatch.InMemSubmitter{Mem: mem}
	server, _ := NewServer(Config{
		StorageURL: "s3://test",
		Submitter:  submitter,
	})
	defer server.Close()
	srv := httptest.NewServer(server.Handler())
	defer srv.Close()

	runnerCtx, runnerCancel := context.WithCancel(context.Background())
	defer runnerCancel()
	go func() {
		for {
			recvCtx, cancel := context.WithTimeout(runnerCtx, 200*time.Millisecond)
			job, err := mem.Receive(recvCtx)
			cancel()
			if err != nil {
				if runnerCtx.Err() != nil {
					return
				}
				continue
			}
			_, _ = exec.Execute(runnerCtx, exec.Config{Runner: exec.NoopRunner{}, LoadctlURL: srv.URL}, job)
			_ = mem.Ack(runnerCtx, job)
		}
	}()

	body := bytes.NewBufferString(`{"scale":"small","scenarios":["a","b","c"],"cluster_release":"r2"}`)
	resp, _ := http.Post(srv.URL+"/api/v1/runs", "application/json", body)
	var created Run
	_ = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	deadline := time.Now().Add(5 * time.Second)
	var final Run
	for time.Now().Before(deadline) {
		r, _ := http.Get(srv.URL + "/api/v1/runs/" + created.ID)
		_ = json.NewDecoder(r.Body).Decode(&final)
		r.Body.Close()
		if final.Status == StatusPass {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if final.Status != StatusPass {
		t.Errorf("status=%s want PASS", final.Status)
	}
	for _, sc := range []string{"a", "b", "c"} {
		if !strings.Contains(final.CurrentMetrics, sc+"=PASS") {
			t.Errorf("CurrentMetrics=%q missing %s=PASS", final.CurrentMetrics, sc)
		}
	}
}
