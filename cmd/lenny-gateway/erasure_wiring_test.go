// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/gatewayleader"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
)

// fakeElector is a gatewayleader.Elector the test drives: acquire hands
// leadership to Run's onStarted with a fresh leader context, and lose
// cancels that context and calls onStopped.
type fakeElector struct {
	mu      sync.Mutex
	leader  bool
	acquire chan struct{}
	lose    chan struct{}
}

func newFakeElector(leader bool) *fakeElector {
	return &fakeElector{leader: leader, acquire: make(chan struct{}), lose: make(chan struct{})}
}

func (e *fakeElector) IsLeader() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.leader
}

func (e *fakeElector) setLeader(v bool) {
	e.mu.Lock()
	e.leader = v
	e.mu.Unlock()
}

func (e *fakeElector) Run(ctx context.Context, onStarted func(context.Context), onStopped func()) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.acquire:
		}
		lctx, cancel := context.WithCancel(ctx)
		e.setLeader(true)
		onStarted(lctx)
		select {
		case <-ctx.Done():
		case <-e.lose:
		}
		cancel()
		e.setLeader(false)
		onStopped()
		if ctx.Err() != nil {
			return
		}
	}
}

var _ gatewayleader.Elector = (*fakeElector)(nil)

// waitUntil polls cond until it holds or 5s pass.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// erasureFixture is an erasure runner over a Memory job store whose hooks
// record which jobs' sessions were drained.
type erasureFixture struct {
	jobs   *erasurejob.Memory
	users  *userstore.Memory
	runner *erasurejob.Runner

	mu      sync.Mutex
	drained []string
	block   chan struct{} // when non-nil, the drain waits on it
}

func newErasureFixture(t *testing.T) *erasureFixture {
	t.Helper()
	f := &erasureFixture{users: userstore.NewMemory()}
	for _, tn := range []string{"acme", "globex"} {
		for _, u := range []string{"alice", "bob"} {
			if err := f.users.Create(context.Background(), userstore.User{TenantID: tn, Subject: u}); err != nil {
				t.Fatalf("seed user: %v", err)
			}
		}
	}
	f.jobs = erasurejob.NewMemory(erasurejob.WithRestrictionWriter(f.users))
	f.runner = erasurejob.NewRunner(f.jobs, erasure.New(erasure.Config{}), nil).
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithSessionDrain(func(ctx context.Context, tenantID, userID string) error {
			f.mu.Lock()
			f.drained = append(f.drained, tenantID+"/"+userID)
			block := f.block
			f.mu.Unlock()
			if block != nil {
				select {
				case <-block:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		}).
		WithHoldCheck(func(context.Context, string, string) ([]erasurejob.Hold, error) { return nil, nil }).
		WithReleaseGate(func(context.Context, string, string) ([]string, error) { return nil, nil }).
		WithCompletionReceipt(func(context.Context, erasurejob.Job) error { return nil })
	return f
}

func (f *erasureFixture) start(t *testing.T, tenantID, userID string) erasurejob.Job {
	t.Helper()
	j, err := f.runner.Start(context.Background(), tenantID, userID, "carol", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return j
}

func (f *erasureFixture) wasDrained(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.drained {
		if d == key {
			return true
		}
	}
	return false
}

func (f *erasureFixture) phase(t *testing.T, id string) erasurejob.Phase {
	t.Helper()
	j, err := f.jobs.Get(context.Background(), erasurejob.AllTenants, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return j.Phase
}

// TestTenantDeletionTickFollowsLeaderElector_spec_12_8 pins that the
// tenant-deletion tick reconciles only while the gateway-leader elector
// reports this replica as leader, and that an unwired elector runs no
// tick.
// spec: 12.8 (Tenant deletion lifecycle), 12.5 (Artifact Store)
func TestTenantDeletionTickFollowsLeaderElector_spec_12_8(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		elector gatewayleader.Elector
		want    bool
	}{
		{"follower", newFakeElector(false), false},
		{"no elector", nil, false},
		{"leader", newFakeElector(true), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner, tenants, _, _, _ := newDeletionRunner(t)
			runner.elector = tc.elector
			if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme", State: tenantstore.TenantStateDisabling}); err != nil {
				t.Fatalf("Create: %v", err)
			}
			runner.tick(ctx)
			_, err := runner.reconciler.Jobs.Get(ctx, "acme")
			if got := err == nil; got != tc.want {
				t.Errorf("reconciled = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestErasurePollRunsWhileTenantDeletionBlocks_spec_12_8 pins that the
// erasure sweep does not share the tenant-deletion runner's loop: while a
// tenant's Phase 4 eraser blocks inside tenantDeletionRunner.Start, the
// erasure runner's Poll completes a pending erasure job.
// spec: 12.8 (Erasure job phase field (crash recovery)), 12.8 (Tenant deletion lifecycle)
func TestErasurePollRunsWhileTenantDeletionBlocks_spec_12_8(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner, tenants, _, _, _ := newDeletionRunner(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	runner.reconciler.Eraser = &tenantEraser{erasers: []namedTenantEraser{{"sessions", func(ctx context.Context, _ string) (int, error) {
		once.Do(func() { close(entered) })
		select {
		case <-release:
		case <-ctx.Done():
		}
		return 1, nil
	}}}}
	runner.elector = newFakeElector(true)
	runner.interval = 5 * time.Millisecond
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "globex", State: tenantstore.TenantStateDeleting}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	go runner.Start(ctx)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the tenant-deletion Phase 4 eraser never ran")
	}

	f := newErasureFixture(t)
	j := f.start(t, "acme", "alice")
	go f.runner.Poll(ctx, 5*time.Millisecond)
	waitUntil(t, "the erasure job to complete", func() bool { return f.phase(t, j.ID) == erasurejob.PhaseCompleted })
	close(release)
}

// TestAddErasureSweepRunsOnlyWhileLeader_spec_12_8 pins the leader gate of
// the erasure sweep: a pending job's drain runs only after the elector
// acquires, and no job created after the elector loses is drained.
// spec: 12.5 (Artifact Store), 12.8 (Erasure job phase field (crash recovery))
func TestAddErasureSweepRunsOnlyWhileLeader_spec_12_8(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newErasureFixture(t)
	elector := newFakeElector(false)
	gate := gatewayleader.NewGate(elector)
	addErasureSweep(gate, f.runner, nil, 5*time.Millisecond)
	go gate.Run(ctx)

	first := f.start(t, "acme", "alice")
	time.Sleep(50 * time.Millisecond)
	if f.wasDrained("acme/alice") {
		t.Fatal("a follower drained a pending job")
	}
	elector.acquire <- struct{}{}
	waitUntil(t, "the leader to complete the job", func() bool { return f.phase(t, first.ID) == erasurejob.PhaseCompleted })
	elector.lose <- struct{}{}
	waitUntil(t, "leadership to be lost", func() bool { return !elector.IsLeader() })
	time.Sleep(20 * time.Millisecond)

	second := f.start(t, "globex", "bob")
	time.Sleep(50 * time.Millisecond)
	if f.wasDrained("globex/bob") {
		t.Error("a former leader drained a job created after it lost the lease")
	}
	if p := f.phase(t, second.ID); p != erasurejob.PhaseInitiated {
		t.Errorf("job created after the loss is %s, want initiated", p)
	}
}

// scrapeMetrics returns the /metrics body of m.
func scrapeMetrics(t *testing.T, m *gatewaymetrics.Metrics) string {
	t.Helper()
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return rr.Body.String()
}

// TestErasureSamplerRunsOnlyWhileLeader_spec_12_8 pins that the erasure
// sampler runs on the leader gate: before the elector acquires it
// publishes no series and records no cross_tenant_read event, and after
// the elector loses, the former leader's sink holds no age or overdue
// series and an active-jobs value of 0.
// spec: 12.8 (Erasure SLA enforcement), 12.5 (Artifact Store), 12.3 (Postgres HA Requirements)
func TestErasureSamplerRunsOnlyWhileLeader_spec_12_8(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newErasureFixture(t)
	f.block = make(chan struct{})
	defer close(f.block)
	metrics, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("gatewaymetrics.New: %v", err)
	}
	var mu sync.Mutex
	samplerReads := 0
	sampler := erasurejob.NewSampler(f.jobs, metrics, time.Hour, nil).
		WithCrossTenantReadAudit(func(_ context.Context, endpoint, _ string, _ int) error {
			if endpoint == erasurejob.CrossTenantReadEndpointSampler {
				mu.Lock()
				samplerReads++
				mu.Unlock()
			}
			return nil
		})
	j := f.start(t, "acme", "alice")
	elector := newFakeElector(false)
	gate := gatewayleader.NewGate(elector)
	addErasureSweep(gate, f.runner, sampler, time.Hour)
	go gate.Run(ctx)

	time.Sleep(50 * time.Millisecond)
	body := scrapeMetrics(t, metrics)
	mu.Lock()
	reads := samplerReads
	mu.Unlock()
	if strings.Contains(body, `job_id="`+j.ID+`"`) || reads != 0 {
		t.Fatalf("a follower sampled: series present %v, sampler reads %d", strings.Contains(body, j.ID), reads)
	}
	elector.acquire <- struct{}{}
	waitUntil(t, "the leader's first sample", func() bool {
		return strings.Contains(scrapeMetrics(t, metrics), `lenny_erasure_job_age_seconds{job_id="`+j.ID+`"`)
	})
	elector.lose <- struct{}{}
	waitUntil(t, "the former leader to clear its series", func() bool {
		b := scrapeMetrics(t, metrics)
		return !strings.Contains(b, `job_id="`+j.ID+`"`) && strings.Contains(b, "lenny_erasure_jobs_active 0")
	})
	b := scrapeMetrics(t, metrics)
	if strings.Contains(b, "lenny_erasure_job_overdue_seconds{") || strings.Contains(b, "lenny_erasure_job_age_seconds{") {
		t.Errorf("former leader still publishes per-job series:\n%s", b)
	}
}

// failingAppender fails every Append.
type failingAppender struct{ err error }

func (f failingAppender) Append(context.Context, string, string, json.RawMessage, time.Time) (audit.Row, error) {
	return audit.Row{}, f.err
}

// TestErasureReceiptSinkReturnsAppendError_spec_12_8 pins that the
// completion-receipt sink returns its appender's error, so a receipt that
// was not written keeps the job in progress with the restriction set, and
// that a written receipt carries the EmitAdminEvent payload layout
// attributed to the job's persisted principal.
// spec: 12.8 (Processing restriction during erasure (GDPR Article 18)), 16.7 (Section 25 Audit Events)
func TestErasureReceiptSinkReturnsAppendError_spec_12_8(t *testing.T) {
	ctx := context.Background()
	appendErr := errors.New("audit down")
	j := erasurejob.Job{
		ID: "erasure_a", TenantID: "acme", UserID: "alice", InitiatedBy: "carol",
		ReceiptAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := (erasureReceiptSink{appender: failingAppender{err: appendErr}}).WriteReceipt(ctx, j); !errors.Is(err, appendErr) {
		t.Fatalf("WriteReceipt = %v, want the append error", err)
	}

	rec := &fakeAppender{}
	if err := (erasureReceiptSink{appender: rec}).WriteReceipt(ctx, j); err != nil {
		t.Fatalf("WriteReceipt: %v", err)
	}
	calls := rec.snapshot()
	if len(calls) != 1 || calls[0].tenant != "acme" || calls[0].eventType != "gdpr.erasure_completed" {
		t.Fatalf("appends = %+v, want one gdpr.erasure_completed on acme", calls)
	}
	var payload map[string]any
	if err := json.Unmarshal(calls[0].payload, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload["actor_subject"] != "carol" || payload["actor_tenant_id"] != "acme" || payload["target_resource"] != "alice" {
		t.Errorf("payload attribution = %v", payload)
	}
	if d, ok := payload["detail"].(map[string]any); !ok || d["jobId"] != "erasure_a" {
		t.Errorf("payload detail = %v, want the receipt naming erasure_a", payload["detail"])
	}

	// Through the runner: the failing sink keeps the job in progress.
	f := newErasureFixture(t)
	f.runner.WithCompletionReceipt(erasureReceiptSink{appender: failingAppender{err: appendErr}}.WriteReceipt)
	started := f.start(t, "acme", "alice")
	if err := f.runner.Run(ctx, started.ID); !errors.Is(err, appendErr) {
		t.Fatalf("Run = %v, want the append error", err)
	}
	if p := f.phase(t, started.ID); p.Terminal() {
		t.Errorf("phase = %s, want in progress", p)
	}
	if u, _ := f.users.Get(ctx, "acme", "alice"); !u.ProcessingRestricted {
		t.Error("restriction lifted without a receipt")
	}
}

// recordingSessionSource records the filters the erasure session lister
// passes and returns canned rows, reservations, and errors.
type recordingSessionSource struct {
	filters     []sessionstore.ListFilter
	rows        []sessionstore.Session
	reserved    []string
	listErr     error
	reservedErr error
}

func (s *recordingSessionSource) List(_ context.Context, _ string, f sessionstore.ListFilter) ([]sessionstore.Session, error) {
	s.filters = append(s.filters, f)
	return s.rows, s.listErr
}

func (s *recordingSessionSource) ReservedSessionIDs(context.Context, string, string) ([]string, error) {
	return s.reserved, s.reservedErr
}

// TestErasureSessionListerPrimaryAndReservations_spec_12_8 pins the
// erasure session lister: it lists only the user's sessions from the
// primary, adds every reserved id, and returns a listing or reservation
// error, which fails the job under store_delete with no eraser run and the
// restriction set.
// spec: 12.8 (Lifecycle fence and drain), 12.8 (DeleteByUser dependency-ordered deletion sequence)
func TestErasureSessionListerPrimaryAndReservations_spec_12_8(t *testing.T) {
	ctx := context.Background()
	src := &recordingSessionSource{
		rows:     []sessionstore.Session{{ID: "sess_a"}, {ID: "sess_b"}},
		reserved: []string{"sess_reserved"},
	}
	ids, err := erasureSessionLister{sessions: src}.List(ctx, "acme", "alice")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !reflect.DeepEqual(ids, []string{"sess_a", "sess_b", "sess_reserved"}) {
		t.Errorf("ids = %v, want the rows plus the reservation", ids)
	}
	if len(src.filters) != 1 || !reflect.DeepEqual(src.filters[0], sessionstore.ListFilter{UserID: "alice", Primary: true}) {
		t.Errorf("filters = %+v, want one {UserID: alice, Primary: true}", src.filters)
	}
	for _, tc := range []struct {
		name string
		src  *recordingSessionSource
	}{
		{"listing error", &recordingSessionSource{listErr: errors.New("primary down")}},
		{"reservation error", &recordingSessionSource{reservedErr: errors.New("reservations down")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newErasureFixture(t)
			erased := 0
			orch := erasure.New(erasure.Config{
				Sessions: erasureSessionLister{sessions: tc.src}.List,
				SessionScoped: []erasure.SessionEraser{{Name: "transcripts", DeleteBySession: func(context.Context, string, string) (int, error) {
					erased++
					return 1, nil
				}}},
				UserScoped: []erasure.Eraser{{Name: "sessions", DeleteByUser: func(context.Context, string, string) (int, error) {
					erased++
					return 1, nil
				}}},
			})
			r := erasurejob.NewRunner(f.jobs, orch, nil).
				WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))).
				WithSessionDrain(func(context.Context, string, string) error { return nil }).
				WithHoldCheck(func(context.Context, string, string) ([]erasurejob.Hold, error) { return nil, nil }).
				WithReleaseGate(func(context.Context, string, string) ([]string, error) { return nil, nil }).
				WithCompletionReceipt(func(context.Context, erasurejob.Job) error { return nil })
			j, err := r.Start(ctx, "acme", "alice", "carol", nil)
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			_ = r.Run(ctx, j.ID)
			got, _ := f.jobs.Get(ctx, erasurejob.AllTenants, j.ID)
			if got.Phase != erasurejob.PhaseFailed {
				t.Errorf("phase = %s, want failed", got.Phase)
			}
			if erased != 0 {
				t.Errorf("erasers ran %d times, want 0", erased)
			}
			if u, _ := f.users.Get(ctx, "acme", "alice"); !u.ProcessingRestricted {
				t.Error("restriction cleared on a lister error")
			}
		})
	}
}

// TestValidateErasureMaxConcurrentJobs_spec_12_8 pins that the gateway
// refuses a runner limit below 1, which would execute no erasure job.
// spec: 12.8 (Erasure job phase field (crash recovery))
func TestValidateErasureMaxConcurrentJobs_spec_12_8(t *testing.T) {
	for _, n := range []int{0, -1} {
		if err := validateErasureMaxConcurrentJobs(n); err == nil {
			t.Errorf("limit %d accepted, want refused", n)
		}
	}
	if err := validateErasureMaxConcurrentJobs(1); err != nil {
		t.Errorf("limit 1 refused: %v", err)
	}
}
