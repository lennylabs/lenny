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
		StorageURL:  "file://" + t.TempDir(),
		Submitter:   submitter,
		RunDuration: 100 * time.Millisecond,
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
				Runner:     &exec.NoopRunner{},
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

// TestProgressFlowPublishesViaHub exercises the runner → loadctl
// progress callback and verifies the events fan out through the hub.
// The test wires a real loadctl Server + InMem dispatcher + a runner
// goroutine that calls exec.Execute with ProgressFn set. A subscriber
// reads the hub backlog after the run completes and asserts that at
// least one "progress" event was published.
func TestProgressFlowPublishesViaHub(t *testing.T) {
	mem := dispatch.NewInMem(30 * time.Second)
	submitter := &dispatch.InMemSubmitter{Mem: mem}
	server, err := NewServer(Config{
		StorageURL:  "file://" + t.TempDir(),
		Submitter:   submitter,
		RunDuration: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()
	srv := httptest.NewServer(server.Handler())
	defer srv.Close()

	// Spawn the runner with the ProgressFn that POSTs to the server.
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
			cfg := exec.Config{
				Runner:      &exec.NoopRunner{},
				LoadctlURL:  srv.URL,
				ProgressFn:  makeProgressPoster(srv.URL),
				ProgressInt: 100 * time.Millisecond,
			}
			_, _ = exec.Execute(runnerCtx, cfg, job)
			_ = mem.Ack(runnerCtx, job)
		}
	}()

	body := bytes.NewBufferString(`{"scale":"small","scenarios":["progress_demo"],"cluster_release":"r1"}`)
	resp, _ := http.Post(srv.URL+"/api/v1/runs", "application/json", body)
	var created Run
	_ = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	// Subscribe immediately so we capture every event the run emits
	// before hub.Close terminates the channel.
	collectCh, _, unsub := server.hub.SubscribeForTest(created.ID)
	defer unsub()

	collected := make([]Event, 0, 32)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range collectCh {
			collected = append(collected, e)
		}
	}()

	// Wait for the channel to close (terminal transition + hub.Close).
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for run to complete; collected: %s", eventTypes(collected))
	}

	progressCount := 0
	for _, e := range collected {
		if e.Type == "progress" {
			progressCount++
		}
	}
	if progressCount == 0 {
		t.Errorf("no progress events received; got types: %s", eventTypes(collected))
	}
}

func eventTypes(es []Event) string {
	out := []string{}
	for _, e := range es {
		out = append(out, e.Type)
	}
	return strings.Join(out, ",")
}

// makeProgressPoster builds a runner-side ProgressFn that POSTs the
// Progress payload to the loadctl /api/v1/progress endpoint.
func makeProgressPoster(loadctlURL string) exec.ProgressFn {
	client := &http.Client{Timeout: 5 * time.Second}
	return func(ctx context.Context, j *dispatch.Job, p exec.Progress) error {
		body, err := json.Marshal(map[string]any{
			"run_id":          j.RunID,
			"scenario":        j.Scenario,
			"elapsed_seconds": p.ElapsedSeconds,
			"iterations":      p.Iterations,
			"metrics":         p.Metrics,
		})
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, "POST", loadctlURL+"/api/v1/progress", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		return nil
	}
}

// TestRunnerAckRejectsUnknownRun confirms the ack callback returns
// 404 for unknown run IDs.
func TestRunnerAckRejectsUnknownRun(t *testing.T) {
	server, _ := NewServer(Config{StorageURL: "file://" + t.TempDir()})
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
		StorageURL:  "file://" + t.TempDir(),
		Submitter:   submitter,
		RunDuration: 100 * time.Millisecond,
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
			_, _ = exec.Execute(runnerCtx, exec.Config{Runner: &exec.NoopRunner{}, LoadctlURL: srv.URL}, job)
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
