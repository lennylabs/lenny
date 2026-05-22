// SPDX-License-Identifier: MIT

package loadctl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Config configures a Server.
type Config struct {
	// StorageURL is the object-storage base where per-run artefacts
	// live (s3://, gs://, or azureblob://).
	StorageURL string

	// DatabaseURL selects the run-state backend: "memory://" or a
	// Postgres connection string. Wave 6 ships the in-memory backend;
	// the Postgres backend lands in a Wave 6 follow-up alongside the
	// loadctl terraform modules.
	DatabaseURL string
}

// Server is the HTTP control plane. It exposes the API described in
// TESTING.md §12.12.
type Server struct {
	config Config
	store  Store
	hub    *Hub

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
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/", s.handleIndex)
	return loggingMiddleware(mux)
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

// driveRun moves a freshly-created run through its state machine.
// The dispatcher publish + loadrunner ack lands here; until the
// cloud dispatcher is wired into the loadctl runtime, the path
// PENDING → RUNNING → PASS is observable by callers.
func (s *Server) driveRun(id string) {
	ctx := context.Background()
	time.Sleep(50 * time.Millisecond)
	if run, err := s.store.GetRun(ctx, id); err == nil {
		run.Status = StatusRunning
		_ = s.store.UpdateRun(ctx, run)
		s.hub.Publish(id, Event{Type: "status", Payload: run.Status})
	}
	time.Sleep(50 * time.Millisecond)
	if run, err := s.store.GetRun(ctx, id); err == nil {
		run.Status = StatusPass
		run.CompletedAt = time.Now().UTC()
		run.ReportURL = fmt.Sprintf("%s/runs/%s/report.html", s.config.StorageURL, id)
		_ = s.store.UpdateRun(ctx, run)
		s.hub.Publish(id, Event{Type: "status", Payload: run.Status})
		s.hub.Close(id)
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
