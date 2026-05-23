// SPDX-License-Identifier: MIT

package exec

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/loadrunner/dispatch"
)

func TestExecuteNoopRunsAndAcks(t *testing.T) {
	var receivedRunID, receivedScenario string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/ack" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var ack map[string]any
		_ = json.Unmarshal(body, &ack)
		receivedRunID, _ = ack["run_id"].(string)
		receivedScenario, _ = ack["scenario"].(string)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	j := &dispatch.Job{RunID: "r1", Scenario: "session_throughput", VUs: 5, Duration: 100 * time.Millisecond}
	summary, err := Execute(context.Background(), Config{
		Runner:     &NoopRunner{},
		LoadctlURL: srv.URL,
	}, j)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if summary.Outcome != "PASS" {
		t.Errorf("outcome=%s want PASS", summary.Outcome)
	}
	if receivedRunID != "r1" {
		t.Errorf("ack run_id=%q want r1", receivedRunID)
	}
	if receivedScenario != "session_throughput" {
		t.Errorf("ack scenario=%q want session_throughput", receivedScenario)
	}
}

func TestExecuteRunsHeartbeat(t *testing.T) {
	var beats atomic.Int64
	j := &dispatch.Job{RunID: "r2", Scenario: "x", VUs: 1, Duration: 200 * time.Millisecond}
	cfg := Config{
		Runner: slowRunner{},
		HeartbeatFn: func(ctx context.Context, j *dispatch.Job) error {
			beats.Add(1)
			return nil
		},
		HeartbeatInt: 50 * time.Millisecond,
	}
	if _, err := Execute(context.Background(), cfg, j); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if beats.Load() < 2 {
		t.Errorf("expected at least 2 heartbeats, got %d", beats.Load())
	}
}

type slowRunner struct{}

func (slowRunner) Run(ctx context.Context, j *dispatch.Job) (Summary, error) {
	select {
	case <-ctx.Done():
	case <-time.After(200 * time.Millisecond):
	}
	return Summary{Outcome: "PASS"}, nil
}

func TestReadSummaryParsesK6Shape(t *testing.T) {
	body := `{"metrics":{"iterations":{"type":"counter","values":{"count":1234}},"http_req_duration":{"type":"trend","values":{"avg":0.012,"p(99)":0.045}},"http_req_failed":{"type":"rate","values":{"rate":0.001}}}}`
	prevRead := readFile
	defer func() { readFile = prevRead }()
	readFile = func(string) ([]byte, error) { return []byte(body), nil }
	s, err := readSummary("ignored.json")
	if err != nil {
		t.Fatal(err)
	}
	if s.Iterations != 1234 {
		t.Errorf("iterations=%d want 1234", s.Iterations)
	}
	if s.Metrics["http_req_duration_avg"] != 0.012 {
		t.Errorf("avg metric=%v", s.Metrics["http_req_duration_avg"])
	}
	if s.Metrics["http_req_failed_rate"] != 0.001 {
		t.Errorf("failed_rate=%v", s.Metrics["http_req_failed_rate"])
	}
}
