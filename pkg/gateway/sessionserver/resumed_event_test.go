// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// spec: §7.2 line 138 / §4.4 line 263 — `session.resumed` event.

// resumedEvent returns the latest session.resumed event for the
// session, or nil when none has been published.
func resumedEvent(bus *sessionevents.Bus, sessionID string) *sessionevents.Event {
	var latest *sessionevents.Event
	for _, e := range bus.History(sessionID, 0) {
		if e.Type == "session.resumed" {
			ev := e
			latest = &ev
		}
	}
	return latest
}

// fakeEvictionLookup serves a fixed bool per (tenant, session) lookup
// so the resume path can derive the §4.4 conversation_only mode.
type fakeEvictionLookup struct {
	has map[string]bool
	err error
}

func (f *fakeEvictionLookup) HasEvictionState(_ context.Context, tenant, sess string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.has[tenant+"|"+sess], nil
}

// fakePartialLookup serves a fixed bool per (tenant, session) lookup
// so the resume path can derive the §10.1 partial_workspace mode.
type fakePartialLookup struct {
	has map[string]bool
	err error
}

func (f *fakePartialLookup) HasActivePartialManifest(_ context.Context, tenant, sess string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.has[tenant+"|"+sess], nil
}

func TestResumeEmitsSessionResumedFullByDefault(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	srv := sessionserver.New(store, sessionserver.Options{Events: bus})
	seedAwaitingParent(t, store, "sess_resumed_full")

	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_resumed_full/resume")
	if rr.Code != http.StatusOK {
		t.Fatalf("resume status %d, body %s", rr.Code, rr.Body.String())
	}

	ev := resumedEvent(bus, "sess_resumed_full")
	if ev == nil {
		t.Fatal("no session.resumed event after a default resume")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(ev.Data), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["resumeMode"] != "full" {
		t.Errorf("resumeMode = %v, want full", payload["resumeMode"])
	}
	if payload["workspaceLost"] != false {
		t.Errorf("workspaceLost = %v, want false on full resume", payload["workspaceLost"])
	}
}

func TestResumeEmitsConversationOnlyWhenEvictionStatePresent(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	lookup := &fakeEvictionLookup{has: map[string]bool{"acme|sess_evicted": true}}
	srv := sessionserver.New(store, sessionserver.Options{
		Events:              bus,
		EvictionStateLookup: lookup,
	})
	seedAwaitingParent(t, store, "sess_evicted")

	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_evicted/resume")
	if rr.Code != http.StatusOK {
		t.Fatalf("resume status %d, body %s", rr.Code, rr.Body.String())
	}

	ev := resumedEvent(bus, "sess_evicted")
	if ev == nil {
		t.Fatal("no session.resumed event when eviction-state present")
	}
	if !strings.Contains(ev.Data, "\"resumeMode\":\"conversation_only\"") {
		t.Errorf("event data = %q, want resumeMode=conversation_only", ev.Data)
	}
	if !strings.Contains(ev.Data, "\"workspaceLost\":true") {
		t.Errorf("event data = %q, want workspaceLost=true", ev.Data)
	}
}

func TestResumeEmitsPartialWorkspaceWhenPartialManifestPresent(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	partial := &fakePartialLookup{has: map[string]bool{"acme|sess_partial": true}}
	srv := sessionserver.New(store, sessionserver.Options{
		Events:                bus,
		PartialManifestLookup: partial,
	})
	seedAwaitingParent(t, store, "sess_partial")

	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_partial/resume")
	if rr.Code != http.StatusOK {
		t.Fatalf("resume status %d, body %s", rr.Code, rr.Body.String())
	}

	ev := resumedEvent(bus, "sess_partial")
	if ev == nil {
		t.Fatal("no session.resumed event when partial-manifest present")
	}
	if !strings.Contains(ev.Data, "\"resumeMode\":\"partial_workspace\"") {
		t.Errorf("event data = %q, want resumeMode=partial_workspace", ev.Data)
	}
	// workspaceLost is false for partial_workspace per ResumeMode.WorkspaceLost()
	if !strings.Contains(ev.Data, "\"workspaceLost\":false") {
		t.Errorf("event data = %q, want workspaceLost=false on partial_workspace", ev.Data)
	}
}

func TestResumeEmitsFullWhenLookupErrorsAreNonFatal(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	srv := sessionserver.New(store, sessionserver.Options{
		Events:              bus,
		EvictionStateLookup: &fakeEvictionLookup{err: errors.New("transient")},
	})
	seedAwaitingParent(t, store, "sess_lookup_err")

	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_lookup_err/resume")
	if rr.Code != http.StatusOK {
		t.Fatalf("resume status %d, body %s", rr.Code, rr.Body.String())
	}

	ev := resumedEvent(bus, "sess_lookup_err")
	if ev == nil {
		t.Fatal("no session.resumed event on lookup error")
	}
	if !strings.Contains(ev.Data, "\"resumeMode\":\"full\"") {
		t.Errorf("event data = %q, want resumeMode=full on lookup error", ev.Data)
	}
}

// spec: §7.2 — "The gateway emits children_reattached as a single SSE
// event immediately after session.resumed". The resumed event MUST
// precede children_reattached in the bus history.
func TestResumeEmitsResumedBeforeChildrenReattached(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	srv := sessionserver.New(store, sessionserver.Options{Events: bus})
	seedAwaitingParent(t, store, "sess_parent_order")
	seedTreeSession(t, store, "sess_child_order", "sess_parent_order")

	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_parent_order/resume")
	if rr.Code != http.StatusOK {
		t.Fatalf("resume status %d, body %s", rr.Code, rr.Body.String())
	}

	resumedSeq := uint64(0)
	childrenSeq := uint64(0)
	for _, e := range bus.History("sess_parent_order", 0) {
		switch e.Type {
		case "session.resumed":
			resumedSeq = e.Seq
		case "children_reattached":
			childrenSeq = e.Seq
		}
	}
	if resumedSeq == 0 {
		t.Fatal("no session.resumed event")
	}
	if childrenSeq == 0 {
		t.Fatal("no children_reattached event")
	}
	if resumedSeq >= childrenSeq {
		t.Errorf("session.resumed (seq %d) MUST precede children_reattached (seq %d)",
			resumedSeq, childrenSeq)
	}
}

// TestResumeEmitsResumedEvenWithoutSnapshot covers the cold-start
// branch of resumeOnPod (the session never checkpointed, so it is
// rebuilt from the WorkspacePlan). The session.resumed event still
// fires so the client sees the resume signal.
func TestResumeEmitsResumedEvenWithoutSnapshot(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	srv := sessionserver.New(store, sessionserver.Options{Events: bus})
	now := time.Now().UTC()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID:               "sess_resumed_cold",
		TenantID:         "acme",
		State:            session.StateAwaitingClientAction,
		RuntimeRef:       "echo",
		IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt:        now,
		UpdatedAt:        now,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_resumed_cold/resume")
	if rr.Code != http.StatusOK {
		t.Fatalf("resume status %d, body %s", rr.Code, rr.Body.String())
	}
	if ev := resumedEvent(bus, "sess_resumed_cold"); ev == nil {
		t.Fatal("session.resumed missing on cold-start resume (no snapshot)")
	}
}
