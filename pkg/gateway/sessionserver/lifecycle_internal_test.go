// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §7.1 lines 75, 77 (retention, isolation), §7.2 lines 137, 141
// (status_change, session_complete), §11.7 / §16.6 (lifecycle audit).

// captureLifecycleAudit records every §7.1/§16.6 session lifecycle
// audit event the server emits, for assertion in tests.
type captureLifecycleAudit struct{ events []SessionLifecycleEvent }

func (c *captureLifecycleAudit) EmitSessionLifecycle(_ context.Context, ev SessionLifecycleEvent) {
	c.events = append(c.events, ev)
}

// sseEventsOfType returns the bus history events of the given type for a
// session, oldest first.
func sseEventsOfType(bus *sessionevents.Bus, sessionID, typ string) []sessionevents.Event {
	var out []sessionevents.Event
	for _, e := range bus.History(sessionID, 0) {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// --- F-10.7.13: variant-labelled rollback metrics ----------------------

type capturedTerminalMetric struct {
	tenantID, sessionType, variantID string
	isError                          bool
	seconds                          float64
}

// spec: §10.7 lines 1120-1132 / §16.1 lines 161-163 — every terminal
// transition records the variant-labelled session metric family. The
// variant comes from the §10.7 experiment context, session_type from the
// §5.2 ExecutionMode, the error flag from the failed state, and the
// duration from creation to the terminal transition.
func TestEmitTerminalLifecycleRecordsVariantMetrics_spec_10_7_1124(t *testing.T) {
	created := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)
	terminal := created.Add(90 * time.Second)
	cases := []struct {
		name  string
		state session.State
		ec    *sessionstore.ExperimentContext
		mode  string
		want  capturedTerminalMetric
	}{
		{
			name:  "failed treatment task session is an error",
			state: session.StateFailed,
			ec:    &sessionstore.ExperimentContext{ExperimentID: "exp_a", VariantID: "treatment"},
			mode:  "task",
			want:  capturedTerminalMetric{"acme", "task", "treatment", true, 90},
		},
		{
			name:  "completed enrolled session is not an error",
			state: session.StateCompleted,
			ec:    &sessionstore.ExperimentContext{ExperimentID: "exp_a", VariantID: "treatment"},
			mode:  "session",
			want:  capturedTerminalMetric{"acme", "session", "treatment", false, 90},
		},
		{
			name:  "un-enrolled session reports empty variant",
			state: session.StateCancelled,
			ec:    nil,
			mode:  "",
			want:  capturedTerminalMetric{"acme", "", "", false, 90},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []capturedTerminalMetric
			srv := New(memstore.New(), Options{
				Clock: func() time.Time { return terminal },
				RecordSessionTerminal: func(tenantID, sessionType, variantID string, isError bool, seconds float64) {
					got = append(got, capturedTerminalMetric{tenantID, sessionType, variantID, isError, seconds})
				},
			})
			srv.emitTerminalLifecycle(context.Background(), sessionstore.Session{
				ID: "s", TenantID: "acme", State: tc.state,
				ExecutionMode: tc.mode, ExperimentContext: tc.ec, CreatedAt: created,
			})
			if len(got) != 1 {
				t.Fatalf("hook calls: got %d, want 1", len(got))
			}
			if got[0] != tc.want {
				t.Errorf("metric = %+v, want %+v", got[0], tc.want)
			}
		})
	}
}

// A non-terminal state must never reach the metric hook.
func TestEmitTerminalLifecycleSkipsNonTerminalMetric_spec_10_7_1124(t *testing.T) {
	called := false
	srv := New(memstore.New(), Options{
		Clock:                 func() time.Time { return time.Unix(0, 0) },
		RecordSessionTerminal: func(string, string, string, bool, float64) { called = true },
	})
	srv.emitTerminalLifecycle(context.Background(), sessionstore.Session{
		ID: "s", TenantID: "acme", State: session.StateRunning,
	})
	if called {
		t.Errorf("metric hook fired for a non-terminal state")
	}
}

// A nil hook must be a no-op (the metric subsystem is optional wiring).
func TestRecordTerminalSessionMetricsNilHookIsSafe_spec_10_7_1124(t *testing.T) {
	srv := New(memstore.New(), Options{Clock: func() time.Time { return time.Unix(0, 0) }})
	srv.recordTerminalSessionMetrics(sessionstore.Session{ID: "s", TenantID: "acme", State: session.StateFailed})
}

// --- F-7.1.10: session lifecycle audit events --------------------------

func TestRecordSessionCreatedEmitsAuditEvent_spec_7_1_10(t *testing.T) {
	sink := &captureLifecycleAudit{}
	at := time.Date(2026, 5, 26, 9, 0, 0, 0, time.UTC)
	srv := New(memstore.New(), Options{
		LifecycleAuditSink: sink,
		Clock:              func() time.Time { return at },
	})

	srv.recordSessionCreated(context.Background(), sessionstore.Session{
		ID: "sess_c", TenantID: "acme", UserID: "alice@acme.com",
		RuntimeRef: "claude-code", State: session.StateCreated,
	})

	if len(sink.events) != 1 {
		t.Fatalf("audit events: got %d, want 1", len(sink.events))
	}
	ev := sink.events[0]
	if ev.EventType != auditSessionCreated {
		t.Errorf("eventType = %q, want %q", ev.EventType, auditSessionCreated)
	}
	if ev.TenantID != "acme" || ev.SessionID != "sess_c" || ev.RuntimeRef != "claude-code" {
		t.Errorf("audit identity fields wrong: %+v", ev)
	}
	if ev.State != string(session.StateCreated) {
		t.Errorf("state = %q, want created", ev.State)
	}
	if !ev.At.Equal(at) {
		t.Errorf("at = %v, want %v", ev.At, at)
	}
}

func TestRecordSessionCompletedEmitsTerminalAuditEvent_spec_7_1_10(t *testing.T) {
	cases := []struct {
		state     session.State
		eventType string
	}{
		{session.StateCompleted, auditSessionCompleted},
		{session.StateFailed, auditSessionFailed},
		{session.StateCancelled, auditSessionCancelled},
		{session.StateExpired, auditSessionExpired},
	}
	for _, tc := range cases {
		sink := &captureLifecycleAudit{}
		srv := New(memstore.New(), Options{LifecycleAuditSink: sink})
		row := sessionstore.Session{ID: "s", TenantID: "acme", RuntimeRef: "echo", State: tc.state}
		if tc.state == session.StateFailed {
			row.FailureClass = session.FailureClass("runtime_failure")
		}
		srv.recordSessionCompleted(context.Background(), session.StateRunning, row)

		var got *SessionLifecycleEvent
		for i := range sink.events {
			if sink.events[i].EventType == tc.eventType {
				got = &sink.events[i]
			}
		}
		if got == nil {
			t.Fatalf("state %q: missing audit event %q; got %+v", tc.state, tc.eventType, sink.events)
		}
		if tc.state == session.StateFailed && got.FailureClass != "runtime_failure" {
			t.Errorf("failed audit failureClass = %q, want runtime_failure", got.FailureClass)
		}
	}
}

func TestRecordSessionCompletedNonTerminalNoAudit_spec_7_1_10(t *testing.T) {
	sink := &captureLifecycleAudit{}
	srv := New(memstore.New(), Options{LifecycleAuditSink: sink})
	srv.recordSessionCompleted(context.Background(), session.StateRunning, sessionstore.Session{
		ID: "s", TenantID: "acme", State: session.StateRunning,
	})
	if len(sink.events) != 0 {
		t.Errorf("non-terminal state emitted %d audit events, want 0", len(sink.events))
	}
}

func TestLifecycleAuditNilSinkIsSafe_spec_7_1_10(t *testing.T) {
	srv := New(memstore.New(), Options{})
	// Neither path may panic with a nil sink.
	srv.recordSessionCreated(context.Background(), sessionstore.Session{ID: "s", TenantID: "acme", State: session.StateCreated})
	srv.recordSessionCompleted(context.Background(), session.StateRunning, sessionstore.Session{ID: "s", TenantID: "acme", State: session.StateFailed})
}

// --- F-7.1.11 / F-7.2.2: status_change + session_complete SSE ----------

func TestTerminalTransitionEmitsStatusChangeAndComplete_spec_7_2_2(t *testing.T) {
	for _, st := range []session.State{
		session.StateCompleted, session.StateFailed, session.StateCancelled, session.StateExpired,
	} {
		bus := sessionevents.NewBus(64)
		srv := New(memstore.New(), Options{Events: bus})
		srv.recordSessionCompleted(context.Background(), session.StateRunning, sessionstore.Session{
			ID: "s", TenantID: "acme", State: st,
		})

		sc := sseEventsOfType(bus, "s", "status_change")
		if len(sc) != 1 {
			t.Fatalf("state %q: status_change count = %d, want 1", st, len(sc))
		}
		var payload struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal([]byte(sc[0].Data), &payload); err != nil {
			t.Fatalf("status_change data: %v", err)
		}
		if payload.State != string(st) {
			t.Errorf("status_change state = %q, want %q", payload.State, st)
		}

		complete := sseEventsOfType(bus, "s", "session_complete")
		if len(complete) != 1 {
			t.Errorf("state %q: session_complete count = %d, want 1", st, len(complete))
		}
	}
}

func TestSessionCompleteCarriesTaskResult_spec_7_2_2(t *testing.T) {
	bus := sessionevents.NewBus(64)
	srv := New(memstore.New(), Options{Events: bus})
	srv.recordSessionCompleted(context.Background(), session.StateRunning, sessionstore.Session{
		ID: "s", TenantID: "acme", State: session.StateFailed,
		FailureReason: "RUNTIME_CRASH",
	})
	complete := sseEventsOfType(bus, "s", "session_complete")
	if len(complete) != 1 {
		t.Fatalf("session_complete count = %d, want 1", len(complete))
	}
	var body struct {
		TaskID string `json:"taskId"`
		State  string `json:"state"`
		Error  *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(complete[0].Data), &body); err != nil {
		t.Fatalf("session_complete data: %v", err)
	}
	if body.TaskID != "s" || body.State != string(session.StateFailed) {
		t.Errorf("session_complete body = %+v, want taskId=s state=failed", body)
	}
	if body.Error == nil || body.Error.Code != "RUNTIME_CRASH" {
		t.Errorf("session_complete error = %+v, want code RUNTIME_CRASH", body.Error)
	}
}

func TestNonTerminalRecordCompletedNoSSE_spec_7_2_2(t *testing.T) {
	bus := sessionevents.NewBus(64)
	srv := New(memstore.New(), Options{Events: bus})
	srv.recordSessionCompleted(context.Background(), session.StateRunning, sessionstore.Session{
		ID: "s", TenantID: "acme", State: session.StateRunning,
	})
	if n := len(bus.History("s", 0)); n != 0 {
		t.Errorf("non-terminal recordSessionCompleted emitted %d SSE events, want 0", n)
	}
}

func TestFailSessionEmitsTerminalLifecycle_spec_7_2_2(t *testing.T) {
	// spec: §7.2 — the start-path failure (failSession) is a terminal
	// transition and emits the same SSE + audit signals as any terminal.
	store := memstore.New()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "s", TenantID: "acme", RuntimeRef: "echo", State: session.StateRunning,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	bus := sessionevents.NewBus(64)
	sink := &captureLifecycleAudit{}
	srv := New(store, Options{Events: bus, LifecycleAuditSink: sink})

	srv.failSession(context.Background(), "acme", "s")

	if got := sseEventsOfType(bus, "s", "status_change"); len(got) != 1 {
		t.Errorf("failSession status_change count = %d, want 1", len(got))
	}
	if got := sseEventsOfType(bus, "s", "session_complete"); len(got) != 1 {
		t.Errorf("failSession session_complete count = %d, want 1", len(got))
	}
	if len(sink.events) != 1 || sink.events[0].EventType != auditSessionFailed {
		t.Errorf("failSession audit = %+v, want one session.failed", sink.events)
	}
}

func TestEmitStatusChangeNoBusIsSafe_spec_7_2_2(t *testing.T) {
	srv := New(memstore.New(), Options{})
	srv.emitStatusChange("acme", "s", session.StateSuspended) // nil bus must not panic
	srv.emitSessionComplete(context.Background(), sessionstore.Session{ID: "s", State: session.StateCompleted})
}

// --- F-7.1.5 / F-7.1.16: artifact retention default + terminal roll ----

func TestRollRetentionOnTerminalFromZero_spec_7_1_16(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "s", TenantID: "acme", State: session.StateCompleted,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv := New(store, Options{Clock: func() time.Time { return now }})

	srv.rollRetentionOnTerminal(context.Background(), sessionstore.Session{ID: "s", TenantID: "acme", State: session.StateCompleted})

	row, _ := store.Get(context.Background(), "acme", "s")
	want := now.Add(DefaultArtifactRetention)
	if !row.RetentionExpiresAt.Equal(want) {
		t.Errorf("retentionExpiresAt = %v, want %v (terminal + default)", row.RetentionExpiresAt, want)
	}
}

func TestRollRetentionRollsCreateDefaultForward_spec_7_1_16(t *testing.T) {
	// A session whose create-time deadline (created + 7d) is earlier than
	// terminal + 7d gets rolled forward to start the window at the
	// terminal transition.
	store := memstore.New()
	created := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	terminal := created.Add(36 * time.Hour)
	seeded := sessionstore.Session{
		ID: "s", TenantID: "acme", State: session.StateCompleted,
		CreatedAt:          created,
		RetentionExpiresAt: created.Add(DefaultArtifactRetention),
	}
	if err := store.Create(context.Background(), seeded); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv := New(store, Options{Clock: func() time.Time { return terminal }})

	srv.rollRetentionOnTerminal(context.Background(), seeded)

	row, _ := store.Get(context.Background(), "acme", "s")
	want := terminal.Add(DefaultArtifactRetention)
	if !row.RetentionExpiresAt.Equal(want) {
		t.Errorf("retentionExpiresAt = %v, want %v", row.RetentionExpiresAt, want)
	}
}

func TestRollRetentionPreservesLongerExtension_spec_7_1_16(t *testing.T) {
	// A client extension past terminal + default is never shortened.
	store := memstore.New()
	terminal := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	extended := terminal.Add(60 * 24 * time.Hour) // 60 days, > 7-day default
	seeded := sessionstore.Session{
		ID: "s", TenantID: "acme", State: session.StateCompleted,
		RetentionExpiresAt: extended,
	}
	if err := store.Create(context.Background(), seeded); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv := New(store, Options{Clock: func() time.Time { return terminal }})

	srv.rollRetentionOnTerminal(context.Background(), seeded)

	row, _ := store.Get(context.Background(), "acme", "s")
	if !row.RetentionExpiresAt.Equal(extended) {
		t.Errorf("retentionExpiresAt = %v, want preserved %v", row.RetentionExpiresAt, extended)
	}
}

// retentionForTier resolves the §12.9 line 1043 tier-keyed retention
// default: T4 24h, T2 90d, and the deployer-configured window for T3 / the
// empty default, with a stricter environment override tightening the
// tenant tier so a T3 tenant's session in a T4 environment retains for 24h.
func TestRetentionForTier_spec_12_9_1043(t *testing.T) {
	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	for _, tc := range []tenantstore.Tenant{
		{ID: "acme", WorkspaceTier: tenantstore.WorkspaceTierT3},
		{ID: "globex", WorkspaceTier: ""},
		{ID: "initech", WorkspaceTier: tenantstore.WorkspaceTierT4},
	} {
		if err := tenants.Create(ctx, tc); err != nil {
			t.Fatalf("seed tenant %s: %v", tc.ID, err)
		}
	}
	envs := environmentstore.NewMemory()
	// A T4 environment override under the T3 tenant acme.
	if err := envs.Create(ctx, environmentstore.Environment{
		Name: "restricted", TenantID: "acme", WorkspaceTier: tenantstore.WorkspaceTierT4,
	}); err != nil {
		t.Fatalf("seed env: %v", err)
	}
	srv := New(memstore.New(), Options{Tenants: tenants, Environments: envs})

	cases := []struct {
		name   string
		tenant string
		env    string
		want   time.Duration
	}{
		{"t3-deployer-default", "acme", "", DefaultArtifactRetention},
		{"empty-deployer-default", "globex", "", DefaultArtifactRetention},
		{"t4-24h", "initech", "", 24 * time.Hour},
		{"t3-tenant-with-t4-env-tightens", "acme", "restricted", 24 * time.Hour},
		{"unknown-tenant-deployer-default", "ghosttenant", "", DefaultArtifactRetention},
		{"unknown-env-keeps-tenant-tier", "initech", "missing", 24 * time.Hour},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := srv.retentionForTier(ctx, c.tenant, c.env); got != c.want {
				t.Errorf("retentionForTier(%q,%q) = %v, want %v", c.tenant, c.env, got, c.want)
			}
		})
	}
}

func TestDefaultRetentionOptionOverride(t *testing.T) {
	// A non-positive DefaultRetention falls through to the 7-day default;
	// an explicit override is honoured.
	srv := New(memstore.New(), Options{})
	if srv.defaultRetention != DefaultArtifactRetention {
		t.Errorf("default = %v, want %v", srv.defaultRetention, DefaultArtifactRetention)
	}
	custom := New(memstore.New(), Options{DefaultRetention: 48 * time.Hour})
	if custom.defaultRetention != 48*time.Hour {
		t.Errorf("override = %v, want 48h", custom.defaultRetention)
	}
}
