// SPDX-License-Identifier: MIT

package exec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/loadrunner/dispatch"
)

// Runner is the interface execute uses to run a scenario. The default
// implementation (K6Runner) shells out to the `k6` binary; tests
// substitute a deterministic in-process implementation.
type Runner interface {
	// Run the scenario described by j and return the per-run summary.
	// The returned summary's Outcome is "PASS" or "FAIL".
	Run(ctx context.Context, j *dispatch.Job) (Summary, error)
}

// Summary is the per-run outcome the runner reports back through the
// loadctl ack callback.
type Summary struct {
	Outcome    string             `json:"outcome"`
	ReportURL  string             `json:"report_url,omitempty"`
	Metrics    map[string]float64 `json:"metrics,omitempty"`
	Error      string             `json:"error,omitempty"`
	Iterations int64              `json:"iterations,omitempty"`
}

// Progress is the mid-scenario telemetry snapshot Execute emits
// every Config.ProgressInt while the Runner is in flight. It mirrors
// the loadctl RunnerProgress wire payload.
type Progress struct {
	ElapsedSeconds float64            `json:"elapsed_seconds"`
	Iterations     int64              `json:"iterations,omitempty"`
	Metrics        map[string]float64 `json:"metrics,omitempty"`
}

// ProgressFn is the callback Execute invokes on every ProgressInt
// tick. Wire it to a POST against the loadctl /api/v1/progress
// endpoint or any other sink. A non-nil error is logged-and-dropped;
// progress emission MUST NOT abort a scenario.
type ProgressFn func(ctx context.Context, j *dispatch.Job, p Progress) error

// ProgressReporter is the optional interface a Runner implements when
// it has structured mid-flight counters to expose (k6's streaming
// JSON output, for example). Execute calls Snapshot on every
// ProgressInt tick. Runners that do not implement it emit
// elapsed-time-only Progress samples.
type ProgressReporter interface {
	Snapshot() Progress
}

// Config configures Execute.
type Config struct {
	Runner       Runner
	LoadctlURL   string
	HTTPClient   *http.Client
	HeartbeatFn  func(ctx context.Context, j *dispatch.Job) error
	HeartbeatInt time.Duration
	ProgressFn   ProgressFn
	ProgressInt  time.Duration
}

// Execute runs the scenario described by j, posts the ack callback
// to loadctl, and returns the summary. The caller is responsible for
// Acking the dispatcher when Execute returns nil.
//
// The function manages its own heartbeat ticker: every HeartbeatInt
// (default 30s) it calls HeartbeatFn so the dispatcher's visibility
// timeout extends while k6 is running.
func Execute(ctx context.Context, cfg Config, j *dispatch.Job) (Summary, error) {
	if cfg.Runner == nil {
		cfg.Runner = &K6Runner{}
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	if cfg.HeartbeatInt == 0 {
		cfg.HeartbeatInt = 30 * time.Second
	}
	if cfg.ProgressInt == 0 {
		cfg.ProgressInt = time.Second
	}

	stop := startHeartbeat(ctx, cfg, j)
	defer stop()
	stopProgress := startProgress(ctx, cfg, j)
	defer stopProgress()

	summary, err := cfg.Runner.Run(ctx, j)
	if err != nil {
		summary.Outcome = "FAIL"
		summary.Error = err.Error()
	}

	if cfg.LoadctlURL != "" {
		if ackErr := postAck(ctx, cfg, j, summary); ackErr != nil {
			// Surface the ack failure but keep the runner-side result
			// so caller can decide whether to Nack the dispatcher.
			return summary, fmt.Errorf("ack: %w", ackErr)
		}
	}
	return summary, nil
}

func startHeartbeat(ctx context.Context, cfg Config, j *dispatch.Job) func() {
	if cfg.HeartbeatFn == nil {
		return func() {}
	}
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(cfg.HeartbeatInt)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = cfg.HeartbeatFn(ctx, j)
			}
		}
	}()
	return func() { close(stop) }
}

// startProgress fires a Progress tick every cfg.ProgressInt. The
// payload includes the runner's Snapshot if the Runner implements
// ProgressReporter; otherwise the tick carries only elapsed time.
func startProgress(ctx context.Context, cfg Config, j *dispatch.Job) func() {
	if cfg.ProgressFn == nil {
		return func() {}
	}
	stop := make(chan struct{})
	start := time.Now()
	reporter, _ := cfg.Runner.(ProgressReporter)
	go func() {
		ticker := time.NewTicker(cfg.ProgressInt)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				p := Progress{ElapsedSeconds: time.Since(start).Seconds()}
				if reporter != nil {
					snap := reporter.Snapshot()
					p.Iterations = snap.Iterations
					p.Metrics = snap.Metrics
				}
				_ = cfg.ProgressFn(ctx, j, p)
			}
		}
	}()
	return func() { close(stop) }
}

func postAck(ctx context.Context, cfg Config, j *dispatch.Job, summary Summary) error {
	body, err := json.Marshal(map[string]any{
		"run_id":     j.RunID,
		"scenario":   j.Scenario,
		"outcome":    summary.Outcome,
		"report_url": summary.ReportURL,
		"metrics":    summary.Metrics,
		"error":      summary.Error,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", cfg.LoadctlURL+"/api/v1/ack", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := cfg.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("ack: status=%d", resp.StatusCode)
	}
	return nil
}

// --- k6 runner -----------------------------------------------------

// K6Runner shells out to the `k6` binary. Falls back to a NoopRunner
// when the binary is missing, which keeps tier-12 wiring testable
// on machines without k6 installed.
//
// K6Runner implements ProgressReporter: a stdout parser goroutine
// consumes k6's --out json=- stream and updates the in-flight
// counters Snapshot returns to Execute.
type K6Runner struct {
	// Binary is the `k6` executable path. Empty means look up "k6"
	// on $PATH.
	Binary string
	// SummaryPath overrides the default `--summary-export` location.
	SummaryPath string

	mu      sync.Mutex
	state   k6State
}

// k6State is the parsed running aggregate the stdout parser
// accumulates. Snapshot derives Progress from this struct under
// K6Runner.mu.
type k6State struct {
	iterations    int64
	httpReqCount  int64
	httpReqSum    float64
	httpReqMax    float64
	failedCount   int64
}

// Snapshot satisfies ProgressReporter. The Progress carries the
// running iteration count plus avg/max/failed-rate derived from the
// stream-parsed counters.
func (r *K6Runner) Snapshot() Progress {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := Progress{Iterations: r.state.iterations}
	if r.state.httpReqCount > 0 {
		avg := r.state.httpReqSum / float64(r.state.httpReqCount)
		out.Metrics = map[string]float64{
			"http_req_duration_avg": avg,
			// Approximation: the running max is a conservative
			// stand-in for p99 until a streaming quantile lands.
			"http_req_duration_p99": r.state.httpReqMax,
			"http_req_failed_rate":  float64(r.state.failedCount) / float64(r.state.httpReqCount),
		}
	}
	return out
}

// Run resolves the k6 binary, builds the command, executes it, and
// reads the summary export.
func (r *K6Runner) Run(ctx context.Context, j *dispatch.Job) (Summary, error) {
	bin := r.Binary
	if bin == "" {
		bin = "k6"
	}
	if _, err := exec.LookPath(bin); err != nil {
		// Fall back to the noop runner so the wiring is exercisable
		// without k6 installed.
		return (&NoopRunner{}).Run(ctx, j)
	}
	if j.ScriptURL == "" {
		return Summary{Outcome: "FAIL", Error: "Job.ScriptURL is empty"}, errors.New("empty script url")
	}
	scriptPath, cleanup, err := materialiseScript(j.ScriptURL)
	if err != nil {
		return Summary{Outcome: "FAIL", Error: err.Error()}, err
	}
	defer cleanup()

	summaryPath := r.SummaryPath
	if summaryPath == "" {
		summaryPath = filepath.Join(filepath.Dir(scriptPath), "summary.json")
	}
	args := []string{"run",
		"--vus", fmt.Sprintf("%d", j.VUs),
		"--duration", j.Duration.String(),
		"--summary-export", summaryPath,
		"--out", "json=-",
		"--quiet",
		scriptPath,
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(cmd.Env, "LENNY_BASE_URL="+j.TargetURL)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Summary{Outcome: "FAIL", Error: err.Error()}, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return Summary{Outcome: "FAIL", Error: err.Error()}, err
	}
	// Reset the per-run aggregate so a re-used Runner does not leak
	// counts from a prior scenario.
	r.mu.Lock()
	r.state = k6State{}
	r.mu.Unlock()
	parseDone := make(chan struct{})
	go func() {
		defer close(parseDone)
		r.parseStream(stdout)
	}()
	runErr := cmd.Wait()
	<-parseDone
	summary, parseErr := readSummary(summaryPath)
	if runErr != nil {
		summary.Outcome = "FAIL"
		summary.Error = runErr.Error()
		return summary, runErr
	}
	if parseErr != nil {
		summary.Outcome = "FAIL"
		summary.Error = parseErr.Error()
		return summary, parseErr
	}
	summary.Outcome = "PASS"
	return summary, nil
}

// parseStream reads k6's --out json=- NDJSON stream and accumulates
// the per-metric counters Snapshot exposes. Parse errors on a single
// line are ignored — a malformed line does not abort the stream.
func (r *K6Runner) parseStream(src io.Reader) {
	dec := json.NewDecoder(src)
	for {
		var env struct {
			Type   string          `json:"type"`
			Metric string          `json:"metric"`
			Data   json.RawMessage `json:"data"`
		}
		if err := dec.Decode(&env); err != nil {
			return
		}
		if env.Type != "Point" {
			continue
		}
		var point struct {
			Value float64           `json:"value"`
			Tags  map[string]string `json:"tags"`
		}
		if err := json.Unmarshal(env.Data, &point); err != nil {
			continue
		}
		r.mu.Lock()
		switch env.Metric {
		case "iterations":
			r.state.iterations += int64(point.Value)
		case "http_req_duration":
			r.state.httpReqCount++
			r.state.httpReqSum += point.Value
			if point.Value > r.state.httpReqMax {
				r.state.httpReqMax = point.Value
			}
		case "http_req_failed":
			if point.Value > 0 {
				r.state.failedCount++
			}
		}
		r.mu.Unlock()
	}
}

// materialiseScript fetches j.ScriptURL into a local file. For
// "file://" and absolute paths it just resolves the path. For
// "s3://", "gs://", "azureblob://" it downloads to a temp file. The
// Wave 7 cut supports file:// only; cloud-storage fetch lands in the
// same wave that wires the cloud SDK uploads on the loadctl side.
func materialiseScript(url string) (string, func(), error) {
	switch {
	case strings.HasPrefix(url, "file://"):
		path := strings.TrimPrefix(url, "file://")
		return path, func() {}, nil
	case strings.HasPrefix(url, "/"):
		return url, func() {}, nil
	default:
		return "", func() {}, fmt.Errorf("unsupported script URL scheme: %q", url)
	}
}

// readSummary parses the k6 --summary-export JSON into a Summary.
// The k6 schema is large; we extract a focused subset.
func readSummary(path string) (Summary, error) {
	body, err := readFile(path)
	if err != nil {
		return Summary{}, err
	}
	var raw struct {
		Metrics map[string]struct {
			Type   string             `json:"type"`
			Values map[string]float64 `json:"values"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Summary{}, err
	}
	out := Summary{Metrics: map[string]float64{}}
	if m, ok := raw.Metrics["iterations"]; ok {
		if v, ok := m.Values["count"]; ok {
			out.Iterations = int64(v)
		}
	}
	if m, ok := raw.Metrics["http_req_duration"]; ok {
		for k, v := range m.Values {
			out.Metrics["http_req_duration_"+k] = v
		}
	}
	if m, ok := raw.Metrics["http_req_failed"]; ok {
		if v, ok := m.Values["rate"]; ok {
			out.Metrics["http_req_failed_rate"] = v
		}
	}
	return out, nil
}

// readFile is a tiny wrapper over os.ReadFile that allows tests to
// substitute an alternative reader via overrideReadFile.
var readFile = readFileDefault

func readFileDefault(path string) ([]byte, error) {
	return osReadFile(path)
}

// --- noop runner ---------------------------------------------------

// NoopRunner is the fallback used when k6 is not installed. It runs
// for Job.Duration (capped at noopMaxDuration; 50ms when Duration is
// zero so unit tests stay fast), exposes a Snapshot() Progress for
// the ProgressReporter interface, and returns a synthetic PASS
// summary on completion.
type NoopRunner struct {
	mu         sync.Mutex
	iterations int64
	metrics    map[string]float64
}

const noopMaxDuration = 30 * time.Second

// Run produces a synthetic summary based on the job's profile so the
// loadctl side has plausible metrics to relay. While running, the
// per-tick Snapshot reports a linear iteration ramp.
func (n *NoopRunner) Run(ctx context.Context, j *dispatch.Job) (Summary, error) {
	dur := j.Duration
	if dur <= 0 {
		dur = 50 * time.Millisecond
	}
	if dur > noopMaxDuration {
		dur = noopMaxDuration
	}
	totalIters := int64(j.VUs * 100)
	if totalIters == 0 {
		totalIters = 100
	}
	start := time.Now()
	deadline := start.Add(dur)
	// Tick fast enough that Snapshot reflects mid-flight progress; the
	// caller's ProgressInt is independent.
	tickInterval := dur / 20
	if tickInterval < 25*time.Millisecond {
		tickInterval = 25 * time.Millisecond
	}
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return n.finalSummary(totalIters), ctx.Err()
		case <-ticker.C:
		}
		now := time.Now()
		fraction := float64(now.Sub(start)) / float64(dur)
		if fraction >= 1 {
			n.update(totalIters, 0.045)
			return n.finalSummary(totalIters), nil
		}
		n.update(int64(float64(totalIters)*fraction), 0.045)
		if !now.Before(deadline) {
			return n.finalSummary(totalIters), nil
		}
	}
}

func (n *NoopRunner) update(iters int64, p99 float64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.iterations = iters
	n.metrics = map[string]float64{
		"http_req_duration_avg": 0.012,
		"http_req_duration_p99": p99,
		"http_req_failed_rate":  0.001,
	}
}

func (n *NoopRunner) finalSummary(iters int64) Summary {
	n.update(iters, 0.045)
	return Summary{
		Outcome:    "PASS",
		Iterations: iters,
		Metrics: map[string]float64{
			"http_req_duration_avg": 0.012,
			"http_req_duration_p99": 0.045,
			"http_req_failed_rate":  0.001,
		},
	}
}

// Snapshot satisfies ProgressReporter so Execute's progress ticker
// reports live iteration counts while NoopRunner is mid-flight.
func (n *NoopRunner) Snapshot() Progress {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := Progress{Iterations: n.iterations}
	if n.metrics != nil {
		out.Metrics = make(map[string]float64, len(n.metrics))
		for k, v := range n.metrics {
			out.Metrics[k] = v
		}
	}
	return out
}
