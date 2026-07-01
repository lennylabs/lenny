// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
)

// ttftCapture is a thread-safe recorder for ObserveTimeToFirstToken
// callbacks used by the §6.3 line 356 TTFT tests below.
type ttftCapture struct {
	mu      sync.Mutex
	samples []ttftSample
}

type ttftSample struct {
	Pool             string
	RuntimeClass     string
	IsolationProfile string
	Seconds          float64
}

func (c *ttftCapture) observe(pool, runtimeClass, isolationProfile string, seconds float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.samples = append(c.samples, ttftSample{
		Pool: pool, RuntimeClass: runtimeClass,
		IsolationProfile: isolationProfile, Seconds: seconds,
	})
}

func (c *ttftCapture) Snapshot() []ttftSample {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ttftSample, len(c.samples))
	copy(out, c.samples)
	return out
}

// fixedClock returns the same instant on every call so the §6.3 TTFT
// observation has a deterministic seconds value.
func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// spec: §6.3 line 356, §16.1 line 15 — POST /v1/sessions/{id}/messages
// emits the `response` SSE event for each agent-output part; the first
// such event for a session must observe the TTFT histogram with the
// pool/runtime_class/isolation_profile labels resolved from the session
// row, and (now - row.CreatedAt) seconds.
func TestMessagesObservesTTFTOnFirstResponse_spec_6_3_F_6_3_3(t *testing.T) {
	store := memstore.New()
	cap := &ttftCapture{}
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := createdAt.Add(800 * time.Millisecond)
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:                   fixedClock(now),
		Executor:                executor.NewEchoExecutor(),
		Transcripts:             transcriptstore.NewMemory(),
		Events:                  sessionevents.NewBus(0),
		ObserveTimeToFirstToken: cap.observe,
	})
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_ttft", TenantID: "acme", State: session.StateRunning,
		PoolRef: "pool-runc", IsolationProfile: isolation.ProfileStandard,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rr := sendMessageRequest(t, srv.Handler(), "sess_ttft", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Role: "user", Content: sessionrecord.MessageContentFromText("hello")}},
	})
	if rr.Code != 200 {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}

	samples := cap.Snapshot()
	if len(samples) != 1 {
		t.Fatalf("want 1 TTFT sample, got %d (%+v)", len(samples), samples)
	}
	s := samples[0]
	if s.Pool != "pool-runc" {
		t.Errorf("pool label: got %q, want %q", s.Pool, "pool-runc")
	}
	if s.IsolationProfile != string(isolation.ProfileStandard) {
		t.Errorf("isolation_profile label: got %q, want %q", s.IsolationProfile, isolation.ProfileStandard)
	}
	if s.RuntimeClass != "runc" {
		t.Errorf("runtime_class label: got %q, want %q (runc maps to ProfileStandard)", s.RuntimeClass, "runc")
	}
	if want := 0.8; s.Seconds != want {
		t.Errorf("seconds: got %v, want %v (now - CreatedAt)", s.Seconds, want)
	}
}

// spec: §6.3 line 356 — TTFT is observed exactly once per session. A
// second POST /v1/sessions/{id}/messages on the same session must not
// re-fire the histogram observation; the LoadOrStore gate is the
// behavior contract.
func TestMessagesTTFTObservedOnlyOncePerSession_spec_6_3_F_6_3_3(t *testing.T) {
	store := memstore.New()
	cap := &ttftCapture{}
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := createdAt.Add(500 * time.Millisecond)
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:                   fixedClock(now),
		Executor:                executor.NewEchoExecutor(),
		Transcripts:             transcriptstore.NewMemory(),
		Events:                  sessionevents.NewBus(0),
		ObserveTimeToFirstToken: cap.observe,
	})
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_ttft_once", TenantID: "acme", State: session.StateRunning,
		PoolRef: "pool-runc", IsolationProfile: isolation.ProfileStandard,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for i := 0; i < 3; i++ {
		rr := sendMessageRequest(t, srv.Handler(), "sess_ttft_once", sessionserver.MessageRequest{
			Messages: []sessionserver.MessagePayload{{Role: "user", Content: sessionrecord.MessageContentFromText("hello")}},
		})
		if rr.Code != 200 {
			t.Fatalf("status[%d]: %d, body=%s", i, rr.Code, rr.Body.String())
		}
	}

	if got := len(cap.Snapshot()); got != 1 {
		t.Errorf("want 1 TTFT sample after 3 message exchanges, got %d", got)
	}
}

// spec: §6.3 line 356 — TTFT is keyed off the agent-streamed `response`
// event type; the inbound `message_delivered` echo and the lifecycle
// `status_change` frames do not represent first agent output and must
// not produce a TTFT observation.
func TestMessagesTTFTSkipsNonResponseEvents_spec_6_3_F_6_3_3(t *testing.T) {
	store := memstore.New()
	cap := &ttftCapture{}
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := createdAt.Add(200 * time.Millisecond)
	// rejectingExecutor returns no output parts so messages.go publishes
	// only the inbound `message_delivered` echo and emits no `response`
	// frame: the TTFT observation must stay zero.
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:                   fixedClock(now),
		Executor:                noOutputExecutor{},
		Transcripts:             transcriptstore.NewMemory(),
		Events:                  sessionevents.NewBus(0),
		ObserveTimeToFirstToken: cap.observe,
	})
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_ttft_skip", TenantID: "acme", State: session.StateRunning,
		PoolRef: "pool-runc", IsolationProfile: isolation.ProfileStandard,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rr := sendMessageRequest(t, srv.Handler(), "sess_ttft_skip", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Role: "user", Content: sessionrecord.MessageContentFromText("hello")}},
	})
	if rr.Code != 200 {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	if got := len(cap.Snapshot()); got != 0 {
		t.Errorf("TTFT should not observe on non-response events; got %d samples", got)
	}
}

// spec: §6.3 line 356 — a session whose isolation profile has not been
// resolved yet (RuntimeClassName returns ok=false) is skipped so the
// histogram does not carry an empty runtime_class series. This also
// covers the pgstore.Update path where a session is created before pool
// resolution per §7.1.
func TestMessagesTTFTSkipsUnresolvedIsolation_spec_6_3_F_6_3_3(t *testing.T) {
	store := memstore.New()
	cap := &ttftCapture{}
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := createdAt.Add(200 * time.Millisecond)
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:                   fixedClock(now),
		Executor:                executor.NewEchoExecutor(),
		Transcripts:             transcriptstore.NewMemory(),
		Events:                  sessionevents.NewBus(0),
		ObserveTimeToFirstToken: cap.observe,
	})
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_ttft_unresolved", TenantID: "acme", State: session.StateRunning,
		// IsolationProfile left empty: RuntimeClassName(empty) returns ok=false.
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rr := sendMessageRequest(t, srv.Handler(), "sess_ttft_unresolved", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Role: "user", Content: sessionrecord.MessageContentFromText("hello")}},
	})
	if rr.Code != 200 {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	if got := len(cap.Snapshot()); got != 0 {
		t.Errorf("TTFT should not observe when runtime_class cannot be resolved; got %d samples", got)
	}
}

// noOutputExecutor is a §15.1 executor that accepts inbound messages
// and returns no output parts. The messages.go publish loop emits only
// the inbound `message_delivered` echo and no `response` frame, so the
// TTFT helper must stay at zero observations.
type noOutputExecutor struct{}

func (noOutputExecutor) Send(_ context.Context, _ string, _ []executor.Message) (executor.Response, error) {
	return executor.Response{}, nil
}

func (noOutputExecutor) Close(_ context.Context, _ string) error { return nil }
