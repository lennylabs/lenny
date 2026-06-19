// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// spec: §8.10 lines 1080-1089 — when a child fails, the gateway injects a
// `child_failed` event into the parent's session stream with the child
// task id, the transient/permanent failure classification, the error
// details, and whether retries were exhausted. F-8.10.2.

// childFailedEvent returns the latest child_failed event on a session's
// stream, decoded, or nil when none was published.
func childFailedEvent(t *testing.T, bus *sessionevents.Bus, sessionID string) map[string]any {
	t.Helper()
	var latest *sessionevents.Event
	for _, e := range bus.History(sessionID, 0) {
		if e.Type == "child_failed" {
			ev := e
			latest = &ev
		}
	}
	if latest == nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(latest.Data), &out); err != nil {
		t.Fatalf("decode child_failed payload: %v", err)
	}
	return out
}

func TestEmitChildFailedInjectsEventOnParentStream_spec_8_10_1082(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	srv := New(store, Options{Events: bus})
	now := time.Now()
	mustCreate(t, store, sessionstore.Session{ID: "parent", TenantID: "acme", State: session.StateRunning, CreatedAt: now, UpdatedAt: now})

	child := sessionstore.Session{
		ID: "child", TenantID: "acme", State: session.StateFailed,
		ParentSessionID: "parent",
		FailureClass:    session.FailureClassRuntime,
		FailureReason:   string(session.FailurePodEvicted), // retryable → transient
		CreatedAt:       now, UpdatedAt: now,
	}
	mustCreate(t, store, child)

	srv.recordSessionCompleted(context.Background(), session.StateRunning, child)

	ev := childFailedEvent(t, bus, "parent")
	if ev == nil {
		t.Fatal("no child_failed event on the parent stream after a child failed")
	}
	if ev["child_task_id"] != "child" {
		t.Errorf("child_task_id = %v, want child", ev["child_task_id"])
	}
	// pod_evicted is retryable: a child reaching `failed` exhausted its
	// retry budget, so classification is transient and retries_exhausted.
	if ev["classification"] != "transient" {
		t.Errorf("classification = %v, want transient", ev["classification"])
	}
	if ev["retries_exhausted"] != true {
		t.Errorf("retries_exhausted = %v, want true", ev["retries_exhausted"])
	}
	if ev["failure_reason"] != string(session.FailurePodEvicted) {
		t.Errorf("failure_reason = %v, want pod_evicted", ev["failure_reason"])
	}
	if ev["failure_class"] != string(session.FailureClassRuntime) {
		t.Errorf("failure_class = %v, want %s", ev["failure_class"], session.FailureClassRuntime)
	}
}

func TestEmitChildFailedPermanentForNonRetryable_spec_8_10_1082(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	srv := New(store, Options{Events: bus})
	now := time.Now()
	mustCreate(t, store, sessionstore.Session{ID: "p2", TenantID: "acme", State: session.StateRunning, CreatedAt: now, UpdatedAt: now})

	child := sessionstore.Session{
		ID: "c2", TenantID: "acme", State: session.StateFailed,
		ParentSessionID: "p2",
		FailureReason:   string(session.FailureSetupCommandFailed), // non-retryable → permanent
		CreatedAt:       now, UpdatedAt: now,
	}
	mustCreate(t, store, child)

	srv.recordSessionCompleted(context.Background(), session.StateRunning, child)

	ev := childFailedEvent(t, bus, "p2")
	if ev == nil {
		t.Fatal("no child_failed event for a non-retryable child failure")
	}
	if ev["classification"] != "permanent" {
		t.Errorf("classification = %v, want permanent", ev["classification"])
	}
	// A permanent cause short-circuits to failed without consuming the
	// retry budget, so retries_exhausted is false.
	if ev["retries_exhausted"] != false {
		t.Errorf("retries_exhausted = %v, want false", ev["retries_exhausted"])
	}
}

// A child reaching a non-failed terminal state (cancelled, completed,
// expired) is a cascade / deadline / success outcome, not a failure the
// parent decides on, so no child_failed event is injected. spec: §8.10
// lines 1080-1089 (event fires "when a child fails").
func TestEmitChildFailedSkipsNonFailedTerminals_spec_8_10_1082(t *testing.T) {
	for _, st := range []session.State{session.StateCompleted, session.StateCancelled, session.StateExpired} {
		store := memstore.New()
		bus := sessionevents.NewBus(0)
		srv := New(store, Options{Events: bus})
		now := time.Now()
		mustCreate(t, store, sessionstore.Session{ID: "p", TenantID: "acme", State: session.StateRunning, CreatedAt: now, UpdatedAt: now})
		child := sessionstore.Session{ID: "c", TenantID: "acme", State: st, ParentSessionID: "p", CreatedAt: now, UpdatedAt: now}
		mustCreate(t, store, child)

		srv.recordSessionCompleted(context.Background(), session.StateRunning, child)

		if ev := childFailedEvent(t, bus, "p"); ev != nil {
			t.Errorf("state %s injected a child_failed event; want none", st)
		}
	}
}

// A root (parentless) session reaching `failed` has no parent stream to
// inject into, so no child_failed event is published.
func TestEmitChildFailedSkipsRootSession_spec_8_10_1082(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	srv := New(store, Options{Events: bus})
	now := time.Now()
	root := sessionstore.Session{ID: "root", TenantID: "acme", State: session.StateFailed, FailureReason: string(session.FailurePodEvicted), CreatedAt: now, UpdatedAt: now}
	mustCreate(t, store, root)

	srv.recordSessionCompleted(context.Background(), session.StateRunning, root)

	if ev := childFailedEvent(t, bus, "root"); ev != nil {
		t.Error("a parentless root session injected a child_failed event; want none")
	}
}

func mustCreate(t *testing.T, store sessionstore.Store, s sessionstore.Session) {
	t.Helper()
	if err := store.Create(context.Background(), s); err != nil {
		t.Fatalf("seed session %s: %v", s.ID, err)
	}
}
