// SPDX-License-Identifier: MIT

package loadctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/loadreport"
	"github.com/lennylabs/lenny/pkg/loadrunner/dispatch"
	"github.com/lennylabs/lenny/pkg/objectstore"
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

	// RunDuration is the per-scenario duration stamped onto every
	// Job the dispatcher sends to the runner pool. Zero selects the
	// 60-second default. Tests override to a sub-second value so
	// scenarios complete quickly.
	RunDuration time.Duration

	// ProgressDir selects the persistent-progress sink: an empty
	// value (default) keeps progress in the in-memory hub backlog
	// only; "file://path" or a bare absolute path writes one JSONL
	// file per run; cloud URIs (s3://, gs://, azureblob://) reserve
	// the slot for cloud-storage sinks that ship alongside the
	// report uploader.
	ProgressDir string

	// Auth carries the bearer tokens that protect the /api/v1/*
	// surface. An empty AuthConfig disables auth so dev / scaffold
	// flows work out-of-the-box; production deployments MUST set
	// both OperatorTokens and RunnerTokens. See AuthConfig.
	Auth AuthConfig

	// RateLimit caps the request rate on the write-heavy + runner
	// callback endpoints. An empty RateLimitConfig disables all
	// limits. See RateLimitConfig.
	RateLimit RateLimitConfig
}

// Server is the HTTP control plane. It exposes the API described in
// TESTING.md §12.12.
type Server struct {
	config    Config
	store     Store
	hub       *Hub
	submitter dispatch.Submitter
	sink      ProgressSink
	objects   objectstore.Store
	metrics   *metricsBundle

	// ctx scopes every background goroutine the server spawns
	// (driveRun, watchRun, simulateScenario). Server.Shutdown
	// cancels it so SIGTERM unblocks any in-flight scaffolding,
	// watchdog timers, or pending scenario submits.
	ctx    context.Context
	cancel context.CancelFunc
	bg     sync.WaitGroup

	mu        sync.RWMutex
	runners   map[string]*Runner
	baselines map[string]string
	scenarios []Scenario

	// scenarioMetrics is the in-memory per-run-scenario metrics map
	// recordAck populates and completeRun consumes when rendering
	// the per-run HTML report.
	scenarioMetrics map[string]map[string]map[string]float64
}

// Run is a single tier-12 load run.
type Run struct {
	ID             string    `json:"id"`
	Status         string    `json:"status"`
	Scale          string    `json:"scale"`
	Scenarios      []string  `json:"scenarios"`
	ClusterRelease string    `json:"cluster_release"`
	StartedAt      time.Time `json:"started_at"`
	CompletedAt    time.Time `json:"completed_at,omitempty"`
	ReportURL      string    `json:"report_url,omitempty"`
	CurrentMetrics string    `json:"current_metrics,omitempty"`
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
	StatusPending = "PENDING"
	StatusRunning = "RUNNING"
	StatusPass    = "PASS"
	StatusFail    = "FAIL"
	StatusAborted = "ABORTED"
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
	sink, err := newProgressSink(c.ProgressDir)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	objects, err := objectstore.Open(c.StorageURL)
	if err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("loadctl: open StorageURL: %w", err)
	}
	srvCtx, cancel := context.WithCancel(context.Background())
	return &Server{
		config:          c,
		store:           store,
		hub:             NewHub(),
		submitter:       c.Submitter,
		sink:            sink,
		objects:         objects,
		metrics:         newMetricsBundle(),
		ctx:             srvCtx,
		cancel:          cancel,
		runners:         make(map[string]*Runner),
		baselines:       make(map[string]string),
		scenarios:       defaultScenarios(),
		scenarioMetrics: make(map[string]map[string]map[string]float64),
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

// Close releases resources. It calls Shutdown with a short default
// drain window so tests and abrupt callers don't block forever on a
// misbehaving background goroutine. Production deployers should
// prefer Shutdown(ctx) with their own deadline.
func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = s.Shutdown(ctx)
	if s.store != nil {
		return s.store.Close()
	}
	return nil
}

// Shutdown signals every background goroutine to wind down and
// waits for them up to the supplied ctx's deadline. It closes every
// active hub channel so WebSocket subscribers receive a clean end
// frame instead of a TCP reset.
//
// Shutdown is idempotent.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.cancel == nil {
		return nil
	}
	s.cancel()
	s.cancel = nil
	// Close the hub so live WS subscribers see the terminal frame.
	s.hub.CloseAll()
	done := make(chan struct{})
	go func() {
		s.bg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Handler returns the HTTP handler for the control plane.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/runs", s.handleRuns)
	mux.HandleFunc("/api/v1/runs/", s.handleRunDetail)
	mux.HandleFunc("/api/v1/runners", s.handleRunners)
	mux.HandleFunc("/api/v1/runners/", s.handleRunnerDetail)
	mux.HandleFunc("/api/v1/scenarios", s.handleScenarios)
	mux.HandleFunc("/api/v1/baselines/", s.handleBaselines)
	mux.HandleFunc("/api/v1/ack", s.handleRunnerAck)
	mux.HandleFunc("/api/v1/progress", s.handleRunnerProgress)
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.Handle("/metrics", s.metrics.handler())
	// Serve the embedded web tree (HTMX UI + stylesheet). The
	// embed lives in embed.go; the inlined indexHTML constant is
	// the fallback when the embed is empty.
	if assets, ok := embeddedAssets(); ok {
		mux.Handle("/assets/", http.FileServer(http.FS(assets)))
		mux.HandleFunc("/runs/", func(w http.ResponseWriter, r *http.Request) {
			body, err := fs.ReadFile(assets, "runs/detail.html")
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(body)
		})
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
	auth := newAuthMiddleware(s.config.Auth)
	limiter := rateLimitMiddleware(s.config.RateLimit)
	// Order: metrics → logging → auth → ratelimit → mux. Metrics
	// observes every request including auth rejections; rate limit
	// runs after auth so authenticated callers get their own
	// per-token bucket rather than sharing a per-IP one.
	return s.metrics.instrumentMiddleware(loggingMiddleware(auth(limiter(mux))))
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

// RunnerProgress is the mid-scenario telemetry the runner POSTs while
// k6 is still running. Each tick carries the scenario's elapsed time
// and a snapshot of whatever metrics the runner has parsed so far.
// The server publishes every progress event through the run's
// WebSocket channel so the UI can render live charts.
type RunnerProgress struct {
	RunID          string             `json:"run_id"`
	Scenario       string             `json:"scenario"`
	ElapsedSeconds float64            `json:"elapsed_seconds"`
	Iterations     int64              `json:"iterations,omitempty"`
	Metrics        map[string]float64 `json:"metrics,omitempty"`
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

// handleRunnerProgress receives the runner → loadctl mid-scenario
// telemetry. Each call publishes a "progress" Event into the run's
// hub channel so subscribed UIs render a live chart. The handler
// does not mutate run state — the terminal transition still goes
// through handleRunnerAck.
func (s *Server) handleRunnerProgress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var p RunnerProgress
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	if p.RunID == "" || p.Scenario == "" {
		writeError(w, http.StatusBadRequest, "MISSING_FIELDS", "run_id and scenario are required")
		return
	}
	if _, err := s.store.GetRun(r.Context(), p.RunID); err != nil {
		if err == ErrRunNotFound {
			writeError(w, http.StatusNotFound, "RUN_NOT_FOUND", p.RunID)
			return
		}
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	s.publishProgress(p)
	writeJSON(w, http.StatusOK, map[string]any{"received": true})
}

// publishProgress is the single fan-out point for progress events.
// It writes to the persistent sink first (failure is logged and
// dropped) and then fan-outs through the hub so live subscribers
// receive the event in the same order it landed on disk.
func (s *Server) publishProgress(p RunnerProgress) {
	if err := s.sink.Append(p.RunID, p); err != nil {
		log.Printf("loadctl: progress sink append: %v", err)
		s.metrics.sinkErrors.Inc()
	}
	s.hub.Publish(p.RunID, Event{Type: "progress", Payload: p})
	s.metrics.progressEvents.Inc()
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
	// Stash the per-scenario metrics for the report generator.
	s.mu.Lock()
	per, ok := s.scenarioMetrics[run.ID]
	if !ok {
		per = make(map[string]map[string]float64)
		s.scenarioMetrics[run.ID] = per
	}
	per[ack.Scenario] = ack.Metrics
	s.mu.Unlock()
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
	s.metrics.runsCreated.Inc()
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		s.driveRun(run.ID)
	}()
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
	ctx := s.ctx
	run, err := s.store.GetRun(ctx, id)
	if err != nil {
		return
	}
	run.Status = StatusRunning
	_ = s.store.UpdateRun(ctx, run)
	s.hub.Publish(id, Event{Type: "status", Payload: run.Status})

	if s.submitter == nil {
		// Scaffolding path: simulate one scenario worth of progress
		// so the operator UI has live telemetry to render without a
		// real runner pool. The simulation runs scaffoldDuration
		// total and emits a progress tick every scaffoldTick with a
		// growing iteration count. Each scenario in the run produces
		// its own progress stream serially.
		for _, scenario := range run.Scenarios {
			s.hub.Publish(id, Event{Type: "scenario_dispatched", Payload: scenario})
			s.simulateScenario(id, scenario)
			s.hub.Publish(id, Event{Type: "ack", Payload: RunnerAck{
				RunID: id, Scenario: scenario, Outcome: StatusPass,
				Metrics: map[string]float64{
					"http_req_duration_avg": 0.012,
					"http_req_duration_p99": 0.045,
					"http_req_failed_rate":  0.001,
				},
			}})
		}
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
			Duration:  s.runDuration(),
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
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		s.watchRun(ctx, id)
	}()
}

// watchRun fails the run if no terminal ack arrives within the
// configured timeout.
func (s *Server) watchRun(ctx context.Context, id string) {
	deadline := time.NewTimer(defaultRunTimeout)
	defer deadline.Stop()
	select {
	case <-deadline.C:
	case <-ctx.Done():
		return
	}
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
	if url := s.renderReport(ctx, run); url != "" {
		run.ReportURL = url
	}
	_ = s.store.UpdateRun(ctx, run)
	if ackErr != nil {
		s.hub.Publish(id, Event{Type: "error", Payload: ackErr.Error()})
	}
	s.hub.Publish(id, Event{Type: "status", Payload: run.Status})
	s.hub.Close(id)
	s.metrics.runsTerminal.WithLabelValues(status).Inc()
}

// renderReport collates the run's per-scenario metrics into a
// loadreport.Run, renders the HTML report, and uploads it through
// the configured ObjectStore. The returned URL is whatever the store
// reports as the canonical access URL ("file://path", "s3://bucket/...",
// etc.). On any error the function logs and returns "", which leaves
// run.ReportURL empty so the UI surfaces "—".
func (s *Server) renderReport(ctx context.Context, run *Run) string {
	s.mu.Lock()
	per := s.scenarioMetrics[run.ID]
	s.mu.Unlock()
	scenarios := make([]loadreport.ScenarioResult, 0, len(run.Scenarios))
	outcomes := map[string]string{}
	for _, pair := range strings.Split(run.CurrentMetrics, ";") {
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			outcomes[parts[0]] = parts[1]
		}
	}
	for _, name := range run.Scenarios {
		m := per[name]
		sr := loadreport.ScenarioResult{
			Name:      name,
			Status:    outcomes[name],
			ErrorRate: m["http_req_failed_rate"] * 100,
			Latency: loadreport.Latency{
				Avg: m["http_req_duration_avg"],
				P50: m["http_req_duration_med"],
				P95: m["http_req_duration_p(95)"],
				P99: m["http_req_duration_p99"],
				Max: m["http_req_duration_max"],
			},
		}
		scenarios = append(scenarios, sr)
	}
	reportRun := &loadreport.Run{
		ID:             run.ID,
		Scale:          run.Scale,
		ClusterRelease: run.ClusterRelease,
		StartedAt:      run.StartedAt,
		CompletedAt:    run.CompletedAt,
		Scenarios:      scenarios,
	}
	body, err := loadreport.RenderBytes(reportRun)
	if err != nil {
		log.Printf("loadctl: report render: %v", err)
		return ""
	}
	url, err := s.objects.Put(ctx, "runs/"+run.ID+"/report.html", bytes.NewReader(body), "text/html; charset=utf-8")
	if err != nil {
		log.Printf("loadctl: report upload: %v", err)
		return ""
	}
	s.mu.Lock()
	delete(s.scenarioMetrics, run.ID)
	s.mu.Unlock()
	return url
}

const (
	defaultRunDuration = 60 * time.Second
	defaultRunTimeout  = 5 * time.Minute
	scaffoldDuration   = 4 * time.Second
	scaffoldTick       = 500 * time.Millisecond
)

// runDuration returns the configured per-scenario duration or the
// 60-second default when none is set.
func (s *Server) runDuration() time.Duration {
	if s.config.RunDuration > 0 {
		return s.config.RunDuration
	}
	return defaultRunDuration
}

// simulateScenario emits a sequence of RunnerProgress events through
// the hub so the operator UI in scaffold mode (no real runner)
// renders a live chart. The simulation models a linear iteration
// ramp from 0 → ~scaleVUs*100 and a P99 latency that wobbles around
// 45ms; numbers are illustrative, not load-bearing.
func (s *Server) simulateScenario(runID, scenario string) {
	start := time.Now()
	deadline := start.Add(scaffoldDuration)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for now := time.Now(); now.Before(deadline); now = time.Now() {
		elapsed := now.Sub(start)
		fraction := float64(elapsed) / float64(scaffoldDuration)
		if fraction > 1 {
			fraction = 1
		}
		iters := int64(2000 * fraction)
		jitter := 0.045 + 0.010*(fraction-0.5)
		s.publishProgress(RunnerProgress{
			RunID:          runID,
			Scenario:       scenario,
			ElapsedSeconds: elapsed.Seconds(),
			Iterations:     iters,
			Metrics: map[string]float64{
				"http_req_duration_p99": jitter,
				"http_req_failed_rate":  0.001 * (1 + fraction),
			},
		})
		// Reset to scaffoldTick; abort early on server shutdown.
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(scaffoldTick)
		select {
		case <-timer.C:
		case <-s.ctx.Done():
			return
		}
	}
}

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
	case "progress.jsonl":
		s.serveProgressJSONL(w, run.ID)
	default:
		http.NotFound(w, r)
	}
}

// serveProgressJSONL streams the run's persisted progress events as
// application/x-ndjson. Returns 404 when no events were persisted
// (the sink may be disabled, or the runner never emitted any).
func (s *Server) serveProgressJSONL(w http.ResponseWriter, runID string) {
	rc, err := s.sink.Open(runID)
	if err != nil {
		if errors.Is(err, ErrNoProgress) {
			writeError(w, http.StatusNotFound, "NO_PROGRESS", "no persisted progress for run "+runID)
			return
		}
		writeError(w, http.StatusInternalServerError, "SINK_ERROR", err.Error())
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/x-ndjson")
	_, _ = io.Copy(w, rc)
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

// runnerHeartbeatStale is how long a runner can go without a
// heartbeat before its Healthy flag flips false. Runners are pruned
// from the roster after runnerExpiry.
const (
	runnerHeartbeatStale = 90 * time.Second
	runnerExpiry         = 10 * time.Minute
)

func (s *Server) handleRunners(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		// POST /api/v1/runners/register collapses to POST /api/v1/runners
		// for clients that prefer a flat surface.
		s.registerRunner(w, r)
		return
	}
	s.mu.Lock()
	now := time.Now().UTC()
	for id, run := range s.runners {
		if now.Sub(run.LastHeartbeat) > runnerExpiry {
			delete(s.runners, id)
			continue
		}
		run.Healthy = now.Sub(run.LastHeartbeat) <= runnerHeartbeatStale
	}
	out := make([]*Runner, 0, len(s.runners))
	for _, run := range s.runners {
		out = append(out, run)
	}
	s.metrics.runnersGauge.Set(float64(len(s.runners)))
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

// handleRunnerDetail dispatches /api/v1/runners/{id}[/heartbeat].
func (s *Server) handleRunnerDetail(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/runners/")
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	if id == "" {
		writeError(w, http.StatusBadRequest, "RUNNER_ID_REQUIRED", "runner id is empty")
		return
	}
	if id == "register" && r.Method == http.MethodPost {
		s.registerRunner(w, r)
		return
	}
	sub := ""
	if len(parts) > 1 {
		sub = parts[1]
	}
	switch {
	case sub == "heartbeat" && r.Method == http.MethodPost:
		s.heartbeatRunner(w, r, id)
	case sub == "" && r.Method == http.MethodGet:
		s.mu.RLock()
		run, ok := s.runners[id]
		s.mu.RUnlock()
		if !ok {
			writeError(w, http.StatusNotFound, "RUNNER_NOT_FOUND", id)
			return
		}
		writeJSON(w, http.StatusOK, run)
	case sub == "" && r.Method == http.MethodDelete:
		s.mu.Lock()
		delete(s.runners, id)
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
	default:
		http.NotFound(w, r)
	}
}

// registerRunner handles POST /api/v1/runners/register. Upserts the
// runner record; the registration is idempotent on `id`.
func (s *Server) registerRunner(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID       string `json:"id"`
		Cloud    string `json:"cloud"`
		Capacity int    `json:"capacity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	if body.ID == "" {
		writeError(w, http.StatusBadRequest, "RUNNER_ID_REQUIRED", "id field is empty")
		return
	}
	s.mu.Lock()
	s.runners[body.ID] = &Runner{
		ID:            body.ID,
		Cloud:         body.Cloud,
		Capacity:      body.Capacity,
		LastHeartbeat: time.Now().UTC(),
		Healthy:       true,
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, s.runners[body.ID])
}

// heartbeatRunner refreshes the LastHeartbeat timestamp.
func (s *Server) heartbeatRunner(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runners[id]
	if !ok {
		writeError(w, http.StatusNotFound, "RUNNER_NOT_FOUND", id)
		return
	}
	run.LastHeartbeat = time.Now().UTC()
	run.Healthy = true
	writeJSON(w, http.StatusOK, run)
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
