// SPDX-License-Identifier: MIT

package exec

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestK6RunnerParseStreamAccumulates(t *testing.T) {
	r := &K6Runner{}
	src := `{"type":"Metric","metric":"iterations","data":{"name":"iterations"}}
{"type":"Point","metric":"iterations","data":{"value":1.0}}
{"type":"Point","metric":"iterations","data":{"value":1.0}}
{"type":"Point","metric":"http_req_duration","data":{"value":0.010}}
{"type":"Point","metric":"http_req_duration","data":{"value":0.040}}
{"type":"Point","metric":"http_req_duration","data":{"value":0.020}}
{"type":"Point","metric":"http_req_failed","data":{"value":1}}
{"type":"Point","metric":"http_req_failed","data":{"value":0}}
{"type":"Point","metric":"http_req_failed","data":{"value":0}}
`
	r.parseStream(strings.NewReader(src))
	got := r.Snapshot()
	if got.Iterations != 2 {
		t.Errorf("Iterations=%d want 2", got.Iterations)
	}
	if got.Metrics == nil {
		t.Fatalf("Metrics nil")
	}
	avg := got.Metrics["http_req_duration_avg"]
	if avg < 0.022 || avg > 0.024 {
		t.Errorf("avg=%v want ~0.0233", avg)
	}
	// With only 3 samples the t-digest interpolates close to the max.
	// The "max" metric exposes the absolute upper bound separately.
	p99 := got.Metrics["http_req_duration_p99"]
	if p99 < 0.035 || p99 > 0.040 {
		t.Errorf("p99=%v want in [0.035, 0.040]", p99)
	}
	if got.Metrics["http_req_duration_max"] != 0.040 {
		t.Errorf("max=%v want 0.040", got.Metrics["http_req_duration_max"])
	}
	if got.Metrics["http_req_failed_rate"] == 0 {
		t.Errorf("failed_rate=0; want >0 (one failure of three observations)")
	}
}

// TestK6RunnerP99FromTDigestApproximatesTrueP99 feeds 1000 samples
// from a known distribution and confirms the t-digest p99 lands
// inside an acceptable error band (~1% at compression=100). The
// running-max surrogate that this digest replaces would have
// reported the maximum (much higher than p99).
func TestK6RunnerP99FromTDigestApproximatesTrueP99(t *testing.T) {
	r := &K6Runner{}
	// 990 observations at 10ms, 10 at 100ms. True p99 ≈ 10ms; max
	// = 100ms. A running-max surrogate would report 100ms.
	var b strings.Builder
	for i := 0; i < 990; i++ {
		b.WriteString(`{"type":"Point","metric":"http_req_duration","data":{"value":0.010}}` + "\n")
	}
	for i := 0; i < 10; i++ {
		b.WriteString(`{"type":"Point","metric":"http_req_duration","data":{"value":0.100}}` + "\n")
	}
	r.parseStream(strings.NewReader(b.String()))
	got := r.Snapshot()
	p99 := got.Metrics["http_req_duration_p99"]
	// The true p99 of this distribution is 10ms (the cut-over from
	// fast to slow is at the 99th percentile boundary). t-digest
	// at compression=100 lands close; allow a generous band.
	if p99 < 0.009 || p99 > 0.012 {
		t.Errorf("p99=%v want ~0.010 (running-max surrogate would have returned 0.100)", p99)
	}
	if got.Metrics["http_req_duration_max"] != 0.100 {
		t.Errorf("max=%v want 0.100", got.Metrics["http_req_duration_max"])
	}
}
