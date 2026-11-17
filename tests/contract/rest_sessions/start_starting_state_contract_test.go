// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 HTTP contract tests for POST /v1/sessions/{id}/start moving a
// session through starting. A launch failure answers 503 STARTING_FAILED
// and leaves the session failed on GET; a second /start while the first is
// held after its ready → starting write answers 409 INVALID_STATE_TRANSITION
// with currentState starting; and the session's SSE stream carries
// status_change(starting) before status_change(running).
//
// spec: §15.1 (/start), §7.2 (Session state machine: ready → starting,
// starting → failed)
package rest_sessions_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
)

// seedReadySession creates a ready row for alice.
func seedReadySession(t *testing.T, store sessionstore.Store, id string) {
	t.Helper()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: "echo",
		IsolationProfile: isolation.ProfileSandboxed, State: session.StateReady,
	}); err != nil {
		t.Fatalf("seed session row: %v", err)
	}
}

// commitHoldStore holds the starting → running commit until release closes.
type commitHoldStore struct {
	*memstore.Store
	once    sync.Once
	held    chan struct{}
	release chan struct{}
}

func (s *commitHoldStore) Update(ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	if cur, err := s.Store.Get(ctx, tenantID, id); err == nil && cur.State == session.StateStarting {
		probe := cur
		if mutate(&probe) == nil && probe.State == session.StateRunning {
			s.once.Do(func() { close(s.held) })
			<-s.release
		}
	}
	return s.Store.Update(ctx, tenantID, id, mutate)
}

// spec: §15.1 (/start), §7.2 (Session state machine: starting → failed)
// diagnosis: a /start whose launch failed answered other than 503
// STARTING_FAILED, or the session read back through GET was not failed. The
// launch runs after the ready → starting write, so every launch failure
// fails the session; a session left ready would let a retry reconnect to a
// pod the binder already released.
func TestStartLaunchFailureAnswersStartingFailedAndFailsTheSession_spec_15_1(t *testing.T) {
	cluster := slotFailCluster(t)
	store := memstore.New()
	const id = "sess_start_launch_fail"
	seedReadySession(t, store, id)
	launchErr := fmt.Errorf("%w: %w", adapterclient.ErrSlotBindAlreadyStarted,
		status.Error(codes.FailedPrecondition, "slot bind already started"))
	ts := httptest.NewServer(ownerprincipal.Wrap(slotFailServerWithErr(t, cluster, store, launchErr).Handler(), ownerprincipal.Alice))
	t.Cleanup(ts.Close)

	resp, body := do(t, ts, http.MethodPost, "/v1/sessions/"+id+"/start", nil)
	envelope, _ := body["error"].(map[string]any)
	if resp.StatusCode != http.StatusServiceUnavailable || envelope == nil || envelope["code"] != "STARTING_FAILED" {
		t.Fatalf("/start = %d %v, want 503 STARTING_FAILED", resp.StatusCode, body)
	}
	got, gotBody := do(t, ts, http.MethodGet, "/v1/sessions/"+id, nil)
	if got.StatusCode != http.StatusOK || gotBody["state"] != string(session.StateFailed) {
		t.Errorf("GET = %d state %v, want 200 failed", got.StatusCode, gotBody["state"])
	}
}

// spec: §15.1 (/start), §7.2 (Session state machine: ready → starting)
// diagnosis: a second /start, sent while the first was held after its
// ready → starting write, answered other than 409 INVALID_STATE_TRANSITION
// with currentState starting, or the first start did not end running. The
// starting write must land before the launch and the commit, so a
// concurrent start sees starting.
func TestSecondStartWhileTheFirstIsHeldAnswersStarting_spec_15_1(t *testing.T) {
	store := &commitHoldStore{Store: memstore.New(), held: make(chan struct{}), release: make(chan struct{})}
	const id = "sess_start_held"
	seedReadySession(t, store.Store, id)
	ts := httptest.NewServer(ownerprincipal.Wrap(sessionserver.New(store, sessionserver.Options{DevTenantHeader: true}).Handler(), ownerprincipal.Alice))
	t.Cleanup(ts.Close)

	first := make(chan int, 1)
	go func() {
		resp, _ := do(t, ts, http.MethodPost, "/v1/sessions/"+id+"/start", nil)
		first <- resp.StatusCode
	}()
	select {
	case <-store.held:
	case code := <-first:
		t.Fatalf("first /start answered %d before its commit", code)
	case <-time.After(5 * time.Second):
		t.Fatal("first /start never reached its commit")
	}
	resp, body := do(t, ts, http.MethodPost, "/v1/sessions/"+id+"/start", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second /start = %d %v, want 409", resp.StatusCode, body)
	}
	assertInvalidStateTransition(t, body, string(session.StateStarting), []string{string(session.StateReady)})
	close(store.release)
	if code := <-first; code != http.StatusOK {
		t.Errorf("first /start = %d, want 200", code)
	}
}

// spec: §7.2 (Session state machine: ready → starting, starting → running),
// §15.1 (/start)
// diagnosis: the session's SSE stream did not carry status_change(starting)
// before status_change(running) for a successful /start. The start must
// publish the starting transition at its ready → starting write.
func TestStartStreamsStartingBeforeRunning_spec_7_2(t *testing.T) {
	store := memstore.New()
	const id = "sess_start_stream"
	seedReadySession(t, store, id)
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true, Events: sessionevents.NewBus(64)})
	ts := httptest.NewServer(ownerprincipal.Wrap(srv.Handler(), ownerprincipal.Alice))
	t.Cleanup(ts.Close)

	if resp, body := do(t, ts, http.MethodPost, "/v1/sessions/"+id+"/start", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("/start = %d %v, want 200", resp.StatusCode, body)
	}
	states := readStatusChanges(t, ts, id, string(session.StateRunning))
	if strings.Join(states, ",") != "starting,running" {
		t.Errorf("status_change states on the stream = %v, want [starting running]", states)
	}
}

// readStatusChanges reads the session's SSE stream from its start and
// returns the status_change states up to and including until.
func readStatusChanges(t *testing.T, ts *httptest.Server, id, until string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/v1/sessions/"+id+"/events", nil)
	if err != nil {
		t.Fatalf("build stream request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()
	var states []string
	event := ""
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:") && event == "status_change":
			var d struct {
				State string `json:"state"`
			}
			_ = json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &d)
			states = append(states, d.State)
			if d.State == until {
				return states
			}
		case line == "":
			event = ""
		}
	}
	t.Fatalf("stream ended before status_change(%s); states seen %v", until, states)
	return nil
}
