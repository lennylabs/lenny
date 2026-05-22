// SPDX-License-Identifier: MIT

package loadctl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/loadrunner/dispatch"
)

// Config configures a Server.
type Config struct {
	// StorageURL is the object-storage base where per-run artefacts
	// live (s3://, gs://, or azureblob://).
	StorageURL string

	// DatabaseURL selects the run-state backend: "memory://" or a
	// Postgres connection string.
	DatabaseURL string

	// Submitter dispatches scenario jobs to the loadrunner pool.
	// Nil means the server runs in "scaffolding" mode where
	// driveRun simulates state transitions for development and
	// tests. Production wiring supplies a per-cloud Submitter
	// (SQS / Pub/Sub / Service Bus).
	Submitter dispatch.Submitter
}

// Server is the HTTP control plane. It exposes the API described in
// TESTING.md §12.12.
type Server struct {
	config    Config
	store     Store
	hub       *Hub
	submitter dispatch.Submitter

	mu        sync.RWMutex
	runners   map[string]*Runner
	baselines map[string]string
	scenarios []Scenario
}

// Run is a single tier-12 load run.
type Run struct {
	ID              string    `json:"id"`
	Status          string    `json:"status"`
	Scale           string    `json:"scale"`
	Scenarios       []string  `json:"scenarios"`
	ClusterRelease  string    `json:"cluster_release"`
	StartedAt       time.Time `json:"started_at"`
	CompletedAt     time.Time `json:"completed_at,omitempty"`
	ReportURL       string    `json:"report_url,omitempty"`
	CurrentMetrics  string    `json:"current_metrics,omitempty"`
}

// Runner is a registered loadrunner instance.
type Runner struct {
	ID            string    `json:"id"`
	Cloud         string    `json:"cloud"`
	Capacity      int       `json:"capacity"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	Healthy       bool      `json:"healthy"`
}

// Scenario is one entry in the catalogue surfaced via /api/v1/scenarios.
type Scenario struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

const (
	StatusPending  = "PENDING"
	StatusRunning  = "RUNNING"
	StatusPass     = "PASS"
	StatusFail     = "FAIL"
	StatusAborted  = "ABORTED"
)

// NewServer returns a configured Server.
func NewServer(c Config) (*Server, error) {
	if c.DatabaseURL == "" {
		c.DatabaseURL = "memory://"
	}
	store, err := openStore(c.DatabaseURL)
	if err != nil {
		return nil, err
	}
	return &Server{
		config:    c,
		store:     store,
		hub:       NewHub(),
		submitter: c.Submitter,
		runners:   make(map[string]*Runner),
		baselines: make(map[string]string),
		scenarios: defaultScenarios(),
	}, nil
}

// openStore resolves the configured DatabaseURL to a Store.
func openStore(url string) (Store, error) {
	switch {
	case strings.HasPrefix(url, "memory://"):
		return newMemStore(), nil
	case strings.HasPrefix(url, "postgres://"), strings.HasPrefix(url, "postgresql://"):
		return newPGStore(context.Background(), url)
	default:
		return nil, fmt.Errorf("loadctl: unsupported DatabaseURL scheme %q (want memory:// or postgres://)", url)
	}
}

// Close releases resources.
func (s *Server) Close() error {
	if s.store != nil {
		return s.store.Close()
	}
	return nil
}

// Handler returns the HTTP handler for the control plane.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/runs", s.handleRuns)
	mux.HandleFunc("/api/v1/runs/", s.handleRunDetail)
	mux.HandleFunc("/api/v1/runners", s.handleRunners)
	mux.HandleFunc("/api/v1/scenarios", s.handleScenarios)
	mux.HandleFunc("/api/v1/baselines/", s.handleBaselines)
	mux.HandleFunc("/api/v1/ack", s.handleRunnerAck)
	mux.HandleFunc("/healthz", s.handleHealthz)
	// Serve the embedded web tree (HTMX UI + stylesheet). The
	// embed lives in embed.go; the inlined indexHTML constant is
	// the fallback when the embed is empty.
	if assets, ok := embeddedAssets(); ok {
		mux.Handle("/assets/", http.FileServer(http.FS(assets)))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			body, err := fs.ReadFile(assets, "index.html")
			if err != nil {
				s.handleIndex(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(body)
		})
	} else {
		mux.HandleFunc("/", s.handleIndex)
	}
	return loggingMiddleware(mux)
}

// RunnerAck is the runner → loadctl callback payload. The runner
// POSTs one ack per scenario execution containing the outcome and a
// pointer to the uploaded k6 report.
type RunnerAck struct {
	RunID     string             `json:"run_id"`
	Scenario  string             `json:"scenario"`
	Outcome   string             `json:"outcome"` // PASS | FAIL
	ReportURL string             `json:"report_url,omitempty"`
	Metrics   map[string]float64 `json:"metrics,omitempty"`
	Error     string             `json:"error,omitempty"`
}

// handleRunnerAck receives the runner → loadctl callback. When the
// final scenario in a run acks, the run transitions terminal.
func (s *Server) handleRunnerAck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var ack RunnerAck
	if err := json.NewDecoder(r.Body).Decode(&ack); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	if ack.RunID == "" || ack.Scenario == "" {
		writeError(w, http.StatusBadRequest, "MISSING_FIELDS", "run_id and scenario are required")
		return
	}
	run, err := s.store.GetRun(r.Context(), ack.RunID)
	if err == ErrRunNotFound {
		writeError(w, http.StatusNotFound, "RUN_NOT_FOUND", ack.RunID)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	// Publish the ack event so live subscribers see the per-scenario
	// completion.
	s.hub.Publish(ack.RunID, Event{Type: "ack", Payload: ack})

	// Track per-run scenario completion in the run's CurrentMetrics
	// field (semicolon-separated list of "scenario=outcome" pairs).
	// Persist the updated CurrentMetrics before any potential
	// terminal transition; otherwise completeRun reloads a stale
	// copy from the store and the most recent ack is lost.
	completed, total := s.recordAck(run, ack)
	_ = s.store.UpdateRun(r.Context(), run)
	if completed < total {
		writeJSON(w, http.StatusOK, map[string]any{
			"received": true, "completed": completed, "total": total,
		})
		return
	}

	// All scenarios in; determine the run-level outcome.
	outcome := StatusPass
	for _, pair := range strings.Split(run.CurrentMetrics, ";") {
		if strings.HasSuffix(pair, "=FAIL") {
			outcome = StatusFail
			break
		}
	}
	s.completeRun(r.Context(), ack.RunID, outcome, nil)
	writeJSON(w, http.StatusOK, map[string]any{
		"received": true, "completed": completed, "total": total, "outcome": outcome,
	})
}

// recordAck appends scenario=outcome to run.CurrentMetrics and
// returns (completedCount, totalCount).
func (s *Server) recordAck(run *Run, ack RunnerAck) (int, int) {
	pair := ack.Scenario + "=" + ack.Outcome
	if run.CurrentMetrics == "" {
		run.CurrentMetrics = pair
	} else {
		// Replace any prior entry for this scenario; otherwise append.
		existing := strings.Split(run.CurrentMetrics, ";")
		replaced := false
		for i, e := range existing {
			if strings.HasPrefix(e, ack.Scenario+"=") {
				existing[i] = pair
				replaced = true
				break
			}
		}
		if !replaced {
			existing = append(existing, pair)
		}
		run.CurrentMetrics = strings.Join(existing, ";")
	}
	completed := len(strings.Split(run.CurrentMetrics, ";"))
	return completed, len(run.Scenarios)
}

func loggingMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h.ServeHTTP(w, r)
		_ = start
	})
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexHTML))
}

// handleRuns implements POST /api/v1/runs.
func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.createRun(w, r)
	case http.MethodGet:
		s.listRuns(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type createRunRequest struct {
	Scale          string   `json:"scale"`
	Scenarios      []string `json:"scenarios"`
	ClusterRelease string   `json:"cluster_release"`
}

func (s *Server) createRun(w http.ResponseWriter, r *http.Request) {
	var req createRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	if req.Scale == "" {
		req.Scale = "small"
	}
	if len(req.Scenarios) == 0 {
		req.Scenarios = []string{"default"}
	}
	run := &Run{
		ID:             generateID(),
		Status:         StatusPending,
		Scale:          req.Scale,
		Scenarios:      req.Scenarios,
		ClusterRelease: req.ClusterRelease,
		StartedAt:      time.Now().UTC(),
	}
	if err := s.store.CreateRun(r.Context(), run); err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	go s.driveRun(run.ID)
	writeJSON(w, http.StatusCreated, run)
}

// driveRun publishes the run's scenarios to the loadrunner pool.
// When a Submitter is configured, every scenario in the run becomes
// a Job submitted through the cloud queue; the runner Acks via the
// `/api/v1/runs/{id}/runner-ack` callback. When the Submitter is
// nil (development/test mode), the run progresses through a
// simulated PENDING → RUNNING → PASS sequence so the UI surface is
// observable without cloud infrastructure.
func (s *Server) driveRun(id string) {
	ctx := context.Background()
	run, err := s.store.GetRun(ctx, id)
	if err != nil {
		return
	}
	run.Status = StatusRunning
	_ = s.store.UpdateRun(ctx, run)
	s.hub.Publish(id, Event{Type: "status", Payload: run.Status})

	if s.submitter == nil {
		// Scaffolding path: immediately complete the run so the
		// developer surface is observable without a runner pool.
		time.Sleep(50 * time.Millisecond)
		s.completeRun(ctx, id, StatusPass, nil)
		return
	}

	// Submit every scenario as its own Job. The runner pool consumes
	// them in parallel; the run completes when the last Ack arrives.
	for _, scenario := range run.Scenarios {
		job := &dispatch.Job{
			RunID:     id,
			Scenario:  scenario,
			TargetURL: s.config.StorageURL, // placeholder; overlay sets the real gateway URL
			Duration:  defaultRunDuration,
			VUs:       scaleVUs(run.Scale),
			Rate:      scaleRate(run.Scale),
		}
		if err := s.submitter.Submit(ctx, job); err != nil {
			s.hub.Publish(id, Event{Type: "submit_error", Payload: err.Error()})
			s.completeRun(ctx, id, StatusFail, fmt.Errorf("submit %s: %w", scenario, err))
			return
		}
		s.hub.Publish(id, Event{Type: "scenario_dispatched", Payload: scenario})
	}
	// State machine advances on runner-ack callbacks; if no runner
	// acks within the timeout, the run is marked FAIL.
	go s.watchRun(ctx, id)
}

// watchRun fails the run if no terminal ack arrives within the
// configured timeout.
func (s *Server) watchRun(ctx context.Context, id string) {
	deadline := time.NewTimer(defaultRunTimeout)
	defer deadline.Stop()
	<-deadline.C
	run, err := s.store.GetRun(ctx, id)
	if err != nil {
		return
	}
	if run.Status != StatusRunning && run.Status != StatusPending {
		return
	}
	s.completeRun(ctx, id, StatusFail, fmt.Errorf("run %s timed out after %s without runner ack", id, defaultRunTimeout))
}

// completeRun is the single terminal-transition path. It updates
// store + hub atomically (from the hub's perspective) and closes
// the run's WebSocket channel so subscribers see the end frame.
func (s *Server) completeRun(ctx context.Context, id, status string, ackErr error) {
	run, err := s.store.GetRun(ctx, id)
	if err != nil {
		return
	}
	if run.Status == StatusPass || run.Status == StatusFail || run.Status == StatusAborted {
		// Already terminal; ignore the second-write.
		return
	}
	run.Status = status
	run.CompletedAt = time.Now().UTC()
	run.ReportURL = fmt.Sprintf("%s/runs/%s/report.html", s.config.StorageURL, id)
	_ = s.store.UpdateRun(ctx, run)
	if ackErr != nil {
		s.hub.Publish(id, Event{Type: "error", Payload: ackErr.Error()})
	}
	s.hub.Publish(id, Event{Type: "status", Payload: run.Status})
	s.hub.Close(id)
}

const (
	defaultRunDuration = 60 * time.Second
	defaultRunTimeout  = 5 * time.Minute
)

// scaleVUs picks the worker count from the documented profile.
func scaleVUs(scale string) int {
	switch scale {
	case "production":
		return 500
	case "medium":
		return 100
	default:
		return 20
	}
}

// scaleRate picks the constant-arrival rate from the profile.
func scaleRate(scale string) int {
	switch scale {
	case "production":
		return 100
	case "medium":
		return 25
	default:
		return 5
	}
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.store.ListRuns(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

// handleRunDetail implements /api/v1/runs/{id}[/...].
func (s *Server) handleRunDetail(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/runs/")
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	if id == "" {
		http.NotFound(w, r)
		return
	}
	sub := ""
	if len(parts) > 1 {
		sub = parts[1]
	}
	run, err := s.store.GetRun(r.Context(), id)
	if err == ErrRunNotFound {
		writeError(w, http.StatusNotFound, "RUN_NOT_FOUND", id)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	switch sub {
	case "":
		// /api/v1/runs/{id}[:stop]
		if r.Method == http.MethodPost && r.URL.RawQuery == "" && strings.HasSuffix(r.URL.Path, ":stop") {
			s.stopRun(w, run)
			return
		}
		// Some clients call /api/v1/runs/{id}:stop directly.
		if r.Method == http.MethodPost {
			s.stopRun(w, run)
			return
		}
		writeJSON(w, http.StatusOK, run)
	case "report":
		if run.ReportURL == "" {
			writeError(w, http.StatusNotFound, "REPORT_NOT_READY", "report has not been generated")
			return
		}
		http.Redirect(w, r, run.ReportURL, http.StatusFound)
	case "metrics:stream":
		s.hub.ServeWebSocket(w, r, run.ID)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) stopRun(w http.ResponseWriter, run *Run) {
	if run.Status == StatusRunning || run.Status == StatusPending {
		run.Status = StatusAborted
		run.CompletedAt = time.Now().UTC()
		_ = s.store.UpdateRun(context.Background(), run)
		s.hub.Publish(run.ID, Event{Type: "status", Payload: run.Status})
		s.hub.Close(run.ID)
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleRunners(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Runner, 0, len(s.runners))
	for _, run := range s.runners {
		out = append(out, run)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleScenarios(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(w, http.StatusOK, s.scenarios)
}

func (s *Server) handleBaselines(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/v1/baselines/")
	if name == "" {
		writeError(w, http.StatusBadRequest, "BASELINE_NAME_REQUIRED", "baseline name is empty")
		return
	}
	var body struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	if err := s.store.PinBaseline(r.Context(), name, body.RunID); err != nil {
		if err == ErrRunNotFound {
			writeError(w, http.StatusNotFound, "RUN_NOT_FOUND", body.RunID)
			return
		}
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"baseline": name, "run_id": body.RunID})
}

func defaultScenarios() []Scenario {
	return []Scenario{
		{Name: "default", Description: "Default tier-12 catalogue."},
		{Name: "session_throughput", Description: "TESTING.md §12.7.b session_throughput."},
		{Name: "streaming_reconnect_under_load", Description: "TESTING.md §12.7.b streaming_reconnect."},
		{Name: "delegation_fanout", Description: "TESTING.md §12.7.b delegation_fanout."},
	}
}

func generateID() string {
	return fmt.Sprintf("run-%d", time.Now().UTC().UnixNano())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":   code,
			"detail": detail,
		},
	})
}

// ErrInvalidConfig is returned when the Config is malformed.
var ErrInvalidConfig = errors.New("loadctl: invalid Config")

const indexHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<title>Lenny load-test control plane</title>
<style>
  body { font-family: -apple-system, system-ui, Helvetica, Arial, sans-serif; max-width: 900px; margin: 2em auto; padding: 0 1em; color: #1f2933; }
  h1 { font-size: 1.4em; }
  code { background: #fffaf0; padding: 0.1em 0.3em; border-radius: 4px; }
  a { color: #b56b1f; }
</style>
</head>
<body>
<h1>Lenny load-test control plane</h1>
<p>The tier-12 control plane API surface. See <code>TESTING.md §12.12</code> for the documented endpoints.</p>
<ul>
  <li><a href="/api/v1/runs">/api/v1/runs</a> — list runs</li>
  <li><a href="/api/v1/runners">/api/v1/runners</a> — registered runners</li>
  <li><a href="/api/v1/scenarios">/api/v1/scenarios</a> — scenario catalogue</li>
  <li><a href="/healthz">/healthz</a> — liveness</li>
</ul>
</body>
</html>`
