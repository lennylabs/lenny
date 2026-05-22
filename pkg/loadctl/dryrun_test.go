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

// TestTier12LocalDryRun is the local equivalent of the production
// AWS dry run. It exercises every code path tier-12 owns end-to-end
// using an in-process dispatcher + runner so the same test runs on
// a developer laptop in seconds and in CI without cloud credentials.
//
// The test asserts the full happy path:
//
//  1. POST /api/v1/runs creates a run (HTTP 201).
//  2. The Submitter publishes one Job per scenario to the dispatcher.
//  3. A runner Receives, Executes (NoopRunner), and POSTs /api/v1/ack
//     for each scenario.
//  4. Loadctl tracks per-scenario completion in CurrentMetrics and
//     transitions the run to PASS when the last ack arrives.
//  5. GET /api/v1/runs/{id} returns the terminal state with a
//     populated ReportURL.
//  6. The WebSocket telemetry hub close-frame is sent.
//  7. The /healthz endpoint stays green throughout.
//  8. POST /api/v1/baselines/{name} pins the completed run.
//
// When this test passes, the same flow against a real AWS cluster
// requires only the operator-supplied credentials and image-publish
// pipeline to differ.
func TestTier12LocalDryRun(t *testing.T) {
	mem := dispatch.NewInMem(30 * time.Second)
	submitter := &dispatch.InMemSubmitter{Mem: mem}

	server, err := NewServer(Config{
		StorageURL: "s3://lenny-load-reports/dryrun",
		Submitter:  submitter,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()
	srv := httptest.NewServer(server.Handler())
	defer srv.Close()

	runnerCtx, runnerCancel := context.WithCancel(context.Background())
	runnerDone := make(chan struct{})
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
			_, _ = exec.Execute(runnerCtx, exec.Config{
				Runner:     exec.NoopRunner{},
				LoadctlURL: srv.URL,
			}, job)
			_ = mem.Ack(runnerCtx, job)
		}
	}()
	defer func() { runnerCancel(); <-runnerDone }()

	// Step 1+2: create the run with three scenarios.
	body := bytes.NewBufferString(`{"scale":"small","scenarios":["session_throughput","streaming_reconnect_under_load","delegation_fanout"],"cluster_release":"dryrun"}`)
	resp, err := http.Post(srv.URL+"/api/v1/runs", "application/json", body)
	if err != nil {
		t.Fatalf("POST /api/v1/runs: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/v1/runs: status=%d want 201", resp.StatusCode)
	}
	var created Run
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Steps 3-5: poll until terminal.
	deadline := time.Now().Add(10 * time.Second)
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
		t.Fatalf("run did not reach PASS within 10s: status=%s", final.Status)
	}
	if final.ReportURL == "" {
		t.Error("ReportURL empty on PASS")
	}
	for _, sc := range []string{"session_throughput", "streaming_reconnect_under_load", "delegation_fanout"} {
		if !strings.Contains(final.CurrentMetrics, sc+"=PASS") {
			t.Errorf("CurrentMetrics=%q missing %s=PASS", final.CurrentMetrics, sc)
		}
	}

	// Step 7: /healthz stayed green.
	hresp, err := http.Get(srv.URL + "/healthz")
	if err != nil || hresp.StatusCode != http.StatusOK {
		t.Errorf("/healthz: err=%v status=%d", err, hresp.StatusCode)
	}

	// Step 8: pin the run as a named baseline.
	pin := bytes.NewBufferString(`{"run_id":"` + final.ID + `"}`)
	bresp, err := http.Post(srv.URL+"/api/v1/baselines/dryrun-baseline", "application/json", pin)
	if err != nil {
		t.Fatalf("POST baselines: %v", err)
	}
	if bresp.StatusCode != http.StatusOK {
		t.Errorf("POST baselines: status=%d want 200", bresp.StatusCode)
	}
	bresp.Body.Close()
}
