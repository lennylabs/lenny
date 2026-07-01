// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §7.1 line 112 — seal-and-export invariant: bounded exponential
// backoff (5s/2×/60s cap), total window maxWorkspaceSealDurationSeconds
// (300s), then failed/workspace_seal_timeout + workspaceSealFailed audit
// event + lenny_workspace_seal_duration_seconds{outcome} histogram.

// flakySealer fails its first failUntil seal attempts, then succeeds.
// A negative failUntil fails every attempt.
type flakySealer struct {
	failUntil int
	calls     int
	err       error
}

func (f *flakySealer) Seal(context.Context, string, string) error {
	f.calls++
	if f.failUntil < 0 || f.calls <= f.failUntil {
		if f.err != nil {
			return f.err
		}
		return errors.New("minio: connection refused")
	}
	return nil
}

// sealMetricRec records each (pool, outcome, seconds) observation.
type sealMetricRec struct {
	obs []struct {
		pool, outcome string
		seconds       float64
	}
}

func (r *sealMetricRec) observe(pool, outcome string, seconds float64) {
	r.obs = append(r.obs, struct {
		pool, outcome string
		seconds       float64
	}{pool, outcome, seconds})
}

// advancingClock returns a clock func and a sealSleep func that advances
// the clock by the slept duration, so the bounded-backoff loop terminates
// deterministically without real delays.
func advancingClock(start time.Time) (func() time.Time, func(context.Context, time.Duration) bool, *[]time.Duration) {
	now := start
	slept := []time.Duration{}
	clock := func() time.Time { return now }
	sleep := func(_ context.Context, d time.Duration) bool {
		slept = append(slept, d)
		now = now.Add(d)
		return true
	}
	return clock, sleep, &slept
}

func TestSealWorkspaceRetriesThenSucceeds_spec_7_1_3(t *testing.T) {
	clock, sleep, slept := advancingClock(time.Unix(0, 0).UTC())
	metric := &sealMetricRec{}
	sealer := &flakySealer{failUntil: 2} // fail twice, succeed on the 3rd
	srv := New(memstore.New(), Options{
		Sealer:                       sealer,
		SealSleep:                    sleep,
		ObserveWorkspaceSealDuration: metric.observe,
		Clock:                        clock,
	})

	sess := sessionstore.Session{ID: "s1", TenantID: "acme", RuntimeRef: "claude-code", State: session.StateCompleted}
	if err := srv.sealWorkspace(context.Background(), sess); err != nil {
		t.Fatalf("sealWorkspace returned error after eventual success: %v", err)
	}
	if sealer.calls != 3 {
		t.Errorf("seal attempts = %d, want 3 (2 failures + 1 success)", sealer.calls)
	}
	if len(*slept) != 2 {
		t.Errorf("backoff sleeps = %d, want 2", len(*slept))
	}
	// spec: §7.1 line 112 — initial 5s, factor 2×.
	if len(*slept) == 2 && ((*slept)[0] != 5*time.Second || (*slept)[1] != 10*time.Second) {
		t.Errorf("backoff sequence = %v, want [5s 10s]", *slept)
	}
	if len(metric.obs) != 1 || metric.obs[0].outcome != sealOutcomeSuccess {
		t.Errorf("seal metric = %+v, want one success observation", metric.obs)
	}
	if metric.obs[0].pool != "claude-code" {
		t.Errorf("seal metric pool = %q, want claude-code", metric.obs[0].pool)
	}
}

func TestSealWorkspaceBackoffBoundedByWindow_spec_7_1_3(t *testing.T) {
	clock, sleep, slept := advancingClock(time.Unix(0, 0).UTC())
	metric := &sealMetricRec{}
	sealer := &flakySealer{failUntil: -1} // never succeeds
	srv := New(memstore.New(), Options{
		Sealer:                       sealer,
		SealSleep:                    sleep,
		ObserveWorkspaceSealDuration: metric.observe,
		Clock:                        clock,
	})

	sess := sessionstore.Session{ID: "s1", TenantID: "acme", RuntimeRef: "claude-code", State: session.StateCompleted}
	err := srv.sealWorkspace(context.Background(), sess)
	if err == nil {
		t.Fatal("sealWorkspace must return the last error when the window is exhausted")
	}
	// Each per-attempt backoff is capped at 60s; the total never exceeds
	// the 300s default window.
	var total time.Duration
	for _, d := range *slept {
		if d > sealBackoffCap {
			t.Errorf("backoff %v exceeds the 60s per-attempt cap", d)
		}
		total += d
	}
	if total > DefaultWorkspaceSealMaxDuration {
		t.Errorf("total backoff %v exceeds the %v window", total, DefaultWorkspaceSealMaxDuration)
	}
	if sealer.calls < 2 {
		t.Errorf("seal attempts = %d, want the seal to be retried before giving up", sealer.calls)
	}
	if len(metric.obs) != 1 || metric.obs[0].outcome != sealOutcomeTimeout {
		t.Errorf("seal metric = %+v, want one timeout observation", metric.obs)
	}
}

func TestRecordSessionCompletedFailsOnSealTimeout_spec_7_1_3(t *testing.T) {
	clock, sleep, _ := advancingClock(time.Unix(0, 0).UTC())
	metric := &sealMetricRec{}
	audit := &captureLifecycleAudit{}
	store := memstore.New()
	srv := New(store, Options{
		Sealer:                       &flakySealer{failUntil: -1, err: errors.New("minio: 503 slow down")},
		SealSleep:                    sleep,
		ObserveWorkspaceSealDuration: metric.observe,
		LifecycleAuditSink:           audit,
		Clock:                        clock,
	})

	// The session has already transitioned to completed (the handler does
	// this before recordSessionCompleted runs).
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "s_seal", TenantID: "acme", RuntimeRef: "claude-code", State: session.StateCompleted,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	completed, _ := store.Get(context.Background(), "acme", "s_seal")

	srv.recordSessionCompleted(context.Background(), session.StateRunning, completed)

	// spec: §7.1 line 112 — the row is re-labeled failed/workspace_seal_timeout.
	row, _ := store.Get(context.Background(), "acme", "s_seal")
	if row.State != session.StateFailed {
		t.Errorf("state after seal timeout = %q, want failed", row.State)
	}
	if row.FailureClass != session.FailureClassWorkspaceSealTimeout {
		t.Errorf("failureClass = %q, want workspace_seal_timeout", row.FailureClass)
	}
	// spec: §7.1 line 112 — workspaceSealFailed audit event records the last error.
	var sealFailed *SessionLifecycleEvent
	for i := range audit.events {
		if audit.events[i].EventType == auditWorkspaceSealFailed {
			sealFailed = &audit.events[i]
		}
	}
	if sealFailed == nil {
		t.Fatalf("no workspaceSealFailed audit event emitted; got %+v", audit.events)
	}
	if sealFailed.Detail != "minio: 503 slow down" {
		t.Errorf("audit detail = %q, want the last MinIO error", sealFailed.Detail)
	}
	if sealFailed.FailureClass != string(session.FailureClassWorkspaceSealTimeout) {
		t.Errorf("audit failureClass = %q, want workspace_seal_timeout", sealFailed.FailureClass)
	}
	// The timeout histogram was observed.
	if len(metric.obs) != 1 || metric.obs[0].outcome != sealOutcomeTimeout {
		t.Errorf("seal metric = %+v, want one timeout observation", metric.obs)
	}
}

// TestRecordSessionCompletedCleanSealStaysCompleted_spec_7_1_3 confirms a
// successful seal leaves the terminal state untouched and observes the
// success histogram with no failure transition.
func TestRecordSessionCompletedCleanSealStaysCompleted_spec_7_1_3(t *testing.T) {
	metric := &sealMetricRec{}
	store := memstore.New()
	srv := New(store, Options{
		Sealer:                       &flakySealer{failUntil: 0}, // succeeds first try
		ObserveWorkspaceSealDuration: metric.observe,
		Clock:                        func() time.Time { return time.Unix(0, 0).UTC() },
	})
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "s_ok", TenantID: "acme", RuntimeRef: "claude-code", State: session.StateCompleted,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	completed, _ := store.Get(context.Background(), "acme", "s_ok")

	srv.recordSessionCompleted(context.Background(), session.StateRunning, completed)

	row, _ := store.Get(context.Background(), "acme", "s_ok")
	if row.State != session.StateCompleted {
		t.Errorf("state after clean seal = %q, want completed (unchanged)", row.State)
	}
	if len(metric.obs) != 1 || metric.obs[0].outcome != sealOutcomeSuccess {
		t.Errorf("seal metric = %+v, want one success observation", metric.obs)
	}
}
