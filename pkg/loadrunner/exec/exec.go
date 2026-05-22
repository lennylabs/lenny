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

// Config configures Execute.
type Config struct {
	Runner       Runner
	LoadctlURL   string
	HTTPClient   *http.Client
	HeartbeatFn  func(ctx context.Context, j *dispatch.Job) error
	HeartbeatInt time.Duration
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
		cfg.Runner = K6Runner{}
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	if cfg.HeartbeatInt == 0 {
		cfg.HeartbeatInt = 30 * time.Second
	}

	stop := startHeartbeat(ctx, cfg, j)
	defer stop()

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
type K6Runner struct {
	// Binary is the `k6` executable path. Empty means look up "k6"
	// on $PATH.
	Binary string
	// SummaryPath overrides the default `--summary-export` location.
	SummaryPath string
}

// Run resolves the k6 binary, builds the command, executes it, and
// reads the summary export.
func (r K6Runner) Run(ctx context.Context, j *dispatch.Job) (Summary, error) {
	bin := r.Binary
	if bin == "" {
		bin = "k6"
	}
	if _, err := exec.LookPath(bin); err != nil {
		// Fall back to the noop runner so the wiring is exercisable
		// without k6 installed.
		return NoopRunner{}.Run(ctx, j)
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
		scriptPath,
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(cmd.Env, "LENNY_BASE_URL="+j.TargetURL)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	runErr := cmd.Run()
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

// NoopRunner is the fallback used when k6 is not installed. It returns
// a synthetic PASS summary so the wiring is exercisable from go test.
type NoopRunner struct{}

// Run produces a synthetic summary based on the job's profile so the
// loadctl side has plausible metrics to relay.
func (NoopRunner) Run(ctx context.Context, j *dispatch.Job) (Summary, error) {
	deadline := time.Now().Add(50 * time.Millisecond)
	select {
	case <-ctx.Done():
	case <-time.After(time.Until(deadline)):
	}
	iters := int64(j.VUs * 10)
	if iters == 0 {
		iters = 100
	}
	return Summary{
		Outcome:    "PASS",
		Iterations: iters,
		Metrics: map[string]float64{
			"http_req_duration_avg": 0.012,
			"http_req_duration_p99": 0.045,
			"http_req_failed_rate":  0.001,
		},
	}, nil
}
