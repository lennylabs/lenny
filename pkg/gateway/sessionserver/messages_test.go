// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/pagination"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/transcriptstore"
)

// spec: §7.2 message injection; §15.1 POST /v1/sessions/{id}/messages.

func newMessagesServer(t *testing.T) (*sessionserver.Server, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		Executor:    executor.NewEchoExecutor(),
		Transcripts: transcriptstore.NewMemory(),
	})
	return srv, store
}

func seedRunningSession(t *testing.T, store sessionstore.Store, id string) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func sendMessageRequest(t *testing.T, h http.Handler, id string, body sessionserver.MessageRequest) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+id+"/messages", bytes.NewReader(buf))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestMessagesEchoExecutor(t *testing.T) {
	srv, store := newMessagesServer(t)
	seedRunningSession(t, store, "sess_m1")

	rr := sendMessageRequest(t, srv.Handler(), "sess_m1", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{
			{Role: "user", Content: "hello"},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.MessageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// spec: §15.4 lines 1725-1737 — every send_message call returns a
	// `delivery_receipt` envelope (F-7.2.10).
	if got, want := resp.DeliveryReceipt.Status, session.DeliveryStatusDelivered; got != want {
		t.Errorf("delivery receipt status = %q, want %q", got, want)
	}
	if resp.DeliveryReceipt.MessageID == "" {
		t.Error("delivery receipt messageId is empty; §15.4 line 1784 requires a gateway-assigned id when the sender omits one")
	}
	if resp.DeliveryReceipt.DeliveredAt.IsZero() {
		t.Error("delivery receipt deliveredAt is zero for status=delivered")
	}
	if len(resp.Output) != 1 || !strings.Contains(resp.Output[0].Text, "hello") {
		t.Errorf("output: %+v", resp.Output)
	}
}

func TestMessagesRejectsEmptyBatch(t *testing.T) {
	srv, store := newMessagesServer(t)
	seedRunningSession(t, store, "sess_m2")
	rr := sendMessageRequest(t, srv.Handler(), "sess_m2", sessionserver.MessageRequest{
		Messages: nil,
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("empty batch: got %d, want 400", rr.Code)
	}
}

func TestMessagesRejectsTerminalSession(t *testing.T) {
	srv, store := newMessagesServer(t)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_done", TenantID: "acme", State: session.StateCompleted,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rr := sendMessageRequest(t, srv.Handler(), "sess_done", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Content: "x"}},
	})
	if rr.Code != http.StatusConflict {
		t.Errorf("terminal session: got %d, want 409", rr.Code)
	}
}

func TestMessagesRejectsMissingSession(t *testing.T) {
	srv, _ := newMessagesServer(t)
	rr := sendMessageRequest(t, srv.Handler(), "sess_missing", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Content: "x"}},
	})
	if rr.Code != http.StatusNotFound {
		t.Errorf("missing: got %d, want 404", rr.Code)
	}
}

func TestMessagesRejectsWhenExecutorUnwired(t *testing.T) {
	store := memstore.New()
	seedRunningSession(t, store, "sess_no_exec")
	srv := sessionserver.New(store, sessionserver.Options{})
	rr := sendMessageRequest(t, srv.Handler(), "sess_no_exec", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Content: "x"}},
	})
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("no executor: got %d, want 503", rr.Code)
	}
}

func TestMessagesRecordsTranscript(t *testing.T) {
	srv, store := newMessagesServer(t)
	seedRunningSession(t, store, "sess_tr")

	rr := sendMessageRequest(t, srv.Handler(), "sess_tr", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Role: "user", Content: "hello"}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("send: %d", rr.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_tr/transcript", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	tr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(tr, req)
	if tr.Code != http.StatusOK {
		t.Fatalf("transcript: %d, body=%s", tr.Code, tr.Body.String())
	}
	var resp transcriptEnvelopeJSON
	_ = json.Unmarshal(tr.Body.Bytes(), &resp)
	// One user entry + one assistant entry.
	if len(resp.Items) != 2 {
		t.Fatalf("transcript items: got %d, want 2 (%+v)", len(resp.Items), resp.Items)
	}
	if resp.Items[0].Role != "user" || !strings.Contains(resp.Items[0].Content, "hello") {
		t.Errorf("item[0]: %+v", resp.Items[0])
	}
	if resp.Items[1].Role != "assistant" {
		t.Errorf("item[1] role: %q", resp.Items[1].Role)
	}
	if resp.HasMore || resp.Cursor != "" {
		t.Errorf("two-entry transcript: hasMore=%v cursor=%q, want false/empty",
			resp.HasMore, resp.Cursor)
	}
}

// transcriptEnvelopeJSON is the §15.1 canonical list envelope per spec
// lines 1228-1253. Used by tests that assert the transcript handler
// emits `{items, cursor, hasMore}` and not the legacy
// `{sessionId, entries}` shape. spec: §15.1 line 1228.
type transcriptEnvelopeJSON struct {
	Items   []transcriptstore.Entry `json:"items"`
	Cursor  string                  `json:"cursor"`
	HasMore bool                    `json:"hasMore"`
}

func TestMessagesRejectsInjectionWhenRuntimeUnsupported(t *testing.T) {
	// §5.1 / §15.1: a session whose runtime does not declare
	// injection.supported true is rejected with INJECTION_REJECTED.
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "oneshot", Type: runtimestore.TypeAgent,
	})
	srv := sessionserver.New(store, sessionserver.Options{
		Executor:    executor.NewEchoExecutor(),
		Transcripts: transcriptstore.NewMemory(),
		Runtimes:    runtimes,
	})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "s1", TenantID: "acme", State: session.StateRunning,
		RuntimeRef: "oneshot", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rr := sendMessageRequest(t, srv.Handler(), "s1", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Content: "hi"}},
	})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status: %d, want 403 (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "INJECTION_REJECTED") {
		t.Errorf("body: %s, want INJECTION_REJECTED", rr.Body.String())
	}
}

func TestMessagesAllowsInjectionWhenRuntimeSupports(t *testing.T) {
	// §5.1: a runtime declaring injection.supported true accepts
	// mid-session message injection.
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "chatty", Type: runtimestore.TypeAgent,
		Capabilities: &runtimestore.RuntimeCapabilities{
			Interaction: runtimestore.InteractionMultiTurn,
			Injection:   runtimestore.InjectionCapability{Supported: true},
		},
	})
	srv := sessionserver.New(store, sessionserver.Options{
		Executor:    executor.NewEchoExecutor(),
		Transcripts: transcriptstore.NewMemory(),
		Runtimes:    runtimes,
	})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "s1", TenantID: "acme", State: session.StateRunning,
		RuntimeRef: "chatty", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rr := sendMessageRequest(t, srv.Handler(), "s1", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Content: "hi"}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestMessagesInjectionResolvesDerivedRuntime(t *testing.T) {
	// §5.1: a session on a derived runtime is checked for injection
	// support against the effective runtime it inherits from its base.
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "chatty-base", Type: runtimestore.TypeAgent,
		Capabilities: &runtimestore.RuntimeCapabilities{
			Injection: runtimestore.InjectionCapability{Supported: true},
		},
	})
	// The derived runtime declares no capabilities — §5.1 prohibits it —
	// so its effective injection support is inherited from the base.
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "chatty-derived", Type: runtimestore.TypeAgent, BaseRuntime: "chatty-base",
	})
	srv := sessionserver.New(store, sessionserver.Options{
		Executor:    executor.NewEchoExecutor(),
		Transcripts: transcriptstore.NewMemory(),
		Runtimes:    runtimes,
	})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "s1", TenantID: "acme", State: session.StateRunning,
		RuntimeRef: "chatty-derived", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rr := sendMessageRequest(t, srv.Handler(), "s1", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Content: "hi"}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("derived runtime inheriting injection support: status %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestTranscriptMissingSession(t *testing.T) {
	srv, _ := newMessagesServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_missing/transcript", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("missing session: got %d, want 404", rr.Code)
	}
}

func TestTranscriptEmptyForFreshSession(t *testing.T) {
	srv, store := newMessagesServer(t)
	seedRunningSession(t, store, "sess_fresh")
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_fresh/transcript", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("fresh session transcript: %d", rr.Code)
	}
	var resp transcriptEnvelopeJSON
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Items) != 0 {
		t.Errorf("fresh session should have empty transcript: %+v", resp.Items)
	}
	if resp.HasMore {
		t.Errorf("empty transcript hasMore=true (want false)")
	}
}

// TestMessagesRejectsPreRunningStatesPerSpec71Line339 covers the
// §7.2 line 339 routing-by-target-state table: external-client REST
// calls against a pre-running session (created / finalizing / ready /
// starting) MUST reject with `TARGET_NOT_READY` so the client retries
// after starting the session. The DLQ-buffered behavior is reserved
// for inter-session `lenny/send_message` callers.
// spec: §7.2 line 339; F-7.2.15.
func TestMessagesRejectsPreRunningStatesPerSpec71Line339(t *testing.T) {
	cases := []struct {
		name  string
		state session.State
	}{
		{"created", session.StateCreated},
		{"finalizing", session.StateFinalizing},
		{"ready", session.StateReady},
		{"starting", session.StateStarting},
	}
	for _, tc := range cases {
		t.Run(string(tc.state), func(t *testing.T) {
			srv, store := newMessagesServer(t)
			now := time.Now()
			id := "sess_" + string(tc.state)
			if err := store.Create(context.Background(), sessionstore.Session{
				ID: id, TenantID: "acme", State: tc.state, CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				t.Fatalf("seed: %v", err)
			}
			rr := sendMessageRequest(t, srv.Handler(), id, sessionserver.MessageRequest{
				Messages: []sessionserver.MessagePayload{{Content: "early"}},
			})
			if rr.Code != http.StatusConflict {
				t.Fatalf("pre-running %s: got %d, want 409; body=%s", tc.state, rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), "TARGET_NOT_READY") {
				t.Errorf("pre-running %s: body %s, want TARGET_NOT_READY", tc.state, rr.Body.String())
			}
		})
	}
}

// TestMessagesAcceptsRunningSession reasserts the spec line 622
// running-state happy path now that pre-running is rejected.
// spec: §15.1 line 622; §7.2 paths 2/4.
func TestMessagesAcceptsRunningSession(t *testing.T) {
	srv, store := newMessagesServer(t)
	seedRunningSession(t, store, "sess_run")
	rr := sendMessageRequest(t, srv.Handler(), "sess_run", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Content: "live"}},
	})
	if rr.Code != http.StatusOK {
		t.Errorf("running state: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

// TestMessagesRejectsInvalidDeliveryValue covers §15.4 line 1723.
// spec: §15.4 line 1715-1723; §15.1 INVALID_DELIVERY_VALUE; F-7.2.14.
func TestMessagesRejectsInvalidDeliveryValue(t *testing.T) {
	srv, store := newMessagesServer(t)
	seedRunningSession(t, store, "sess_d")
	rr := sendMessageRequest(t, srv.Handler(), "sess_d", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Content: "x", Delivery: "burst"}},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("delivery=burst: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "INVALID_DELIVERY_VALUE") {
		t.Errorf("body: %s, want INVALID_DELIVERY_VALUE", rr.Body.String())
	}
}

// TestMessagesAcceptsCanonicalDeliveryValues covers the closed §15.4
// enum (queued, immediate, absent).
// spec: §15.4 line 1715-1719; F-7.2.14.
func TestMessagesAcceptsCanonicalDeliveryValues(t *testing.T) {
	for _, v := range []string{"", "queued", "immediate"} {
		t.Run("delivery="+v, func(t *testing.T) {
			srv, store := newMessagesServer(t)
			seedRunningSession(t, store, "sess_d_"+v)
			rr := sendMessageRequest(t, srv.Handler(), "sess_d_"+v, sessionserver.MessageRequest{
				Messages: []sessionserver.MessagePayload{{Content: "x", Delivery: v}},
			})
			if rr.Code != http.StatusOK {
				t.Errorf("delivery=%q: got %d, want 200; body=%s", v, rr.Code, rr.Body.String())
			}
		})
	}
}

// TestMessagesInReplyToResolvesPendingRequestInput covers §7.2 path 1:
// a payload's inReplyTo resolves a pending lenny/request_input
// without falling through to the executor.
// spec: §7.2 line 317; §15.4 line 1786; F-7.2.14.
func TestMessagesInReplyToResolvesPendingRequestInput(t *testing.T) {
	store := memstore.New()
	reg := inputwait.NewRegistry()
	srv := sessionserver.New(store, sessionserver.Options{
		Executor:    executor.NewEchoExecutor(),
		Transcripts: transcriptstore.NewMemory(),
		InputWaits:  reg,
	})
	seedRunningSession(t, store, "sess_rr")
	ch, err := reg.Register("sess_rr", "req_alpha")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	rr := sendMessageRequest(t, srv.Handler(), "sess_rr", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Content: "answer", InReplyTo: "req_alpha"}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("inReplyTo path: got %d, body=%s", rr.Code, rr.Body.String())
	}
	// The receipt is `delivered` and the executor is not invoked, so
	// Output is empty.
	var resp sessionserver.MessageResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.DeliveryReceipt.Status != session.DeliveryStatusDelivered {
		t.Errorf("status=%q, want delivered", resp.DeliveryReceipt.Status)
	}
	if len(resp.Output) != 0 {
		t.Errorf("output should be empty when inReplyTo resolves: %+v", resp.Output)
	}
	// The waiter receives the answer.
	select {
	case got := <-ch:
		if got != "answer" {
			t.Errorf("inputwait answer=%q, want %q", got, "answer")
		}
	case <-time.After(1 * time.Second):
		t.Error("inputwait channel did not receive the answer")
	}
}

// TestMessagesInReplyToFallsThroughOnNoMatch — a stale inReplyTo that
// matches no pending request falls through to the executor per §7.2
// path 1 ("no matching inReplyTo" → continue evaluating paths).
// spec: §7.2 line 317; F-7.2.14.
func TestMessagesInReplyToFallsThroughOnNoMatch(t *testing.T) {
	store := memstore.New()
	reg := inputwait.NewRegistry()
	srv := sessionserver.New(store, sessionserver.Options{
		Executor:    executor.NewEchoExecutor(),
		Transcripts: transcriptstore.NewMemory(),
		InputWaits:  reg,
	})
	seedRunningSession(t, store, "sess_fall")

	rr := sendMessageRequest(t, srv.Handler(), "sess_fall", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Content: "hi", InReplyTo: "req_missing"}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.MessageResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Output) == 0 {
		t.Error("expected executor output on inReplyTo miss; got none")
	}
}

// TestMessagesSlotIDFieldRoundTripsThroughBody — the §15.4 slotId is
// accepted on the wire (concurrent-workspace routing lands with the
// F-5.2 build-out). The minimal gateway must not reject the field.
// spec: §15.4 line 1713; F-7.2.14.
func TestMessagesSlotIDFieldRoundTripsThroughBody(t *testing.T) {
	srv, store := newMessagesServer(t)
	seedRunningSession(t, store, "sess_slot")
	rr := sendMessageRequest(t, srv.Handler(), "sess_slot", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Content: "ok", SlotID: "slot_01"}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("slotId accepted: got %d, body=%s", rr.Code, rr.Body.String())
	}
}

// TestTranscriptEmitsCanonicalEnvelope_spec_15_1_1228 pins the
// §15.1 line 1228 canonical list envelope `{items, cursor, hasMore}`
// on the transcript endpoint. F-15.1.19.
func TestTranscriptEmitsCanonicalEnvelope_spec_15_1_1228(t *testing.T) {
	srv, store := newMessagesServer(t)
	seedRunningSession(t, store, "sess_env")
	_ = sendMessageRequest(t, srv.Handler(), "sess_env", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Role: "user", Content: "hi"}},
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_env/transcript", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("transcript: %d, body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	// The pre-rewrite envelope was `{sessionId, entries}`; assert both
	// of those keys are gone so a future regression to the legacy
	// shape trips the test.
	for _, bad := range []string{`"sessionId"`, `"entries"`} {
		if strings.Contains(body, bad) {
			t.Errorf("legacy envelope key present in body: %q\n%s", bad, body)
		}
	}
	for _, want := range []string{`"items"`, `"hasMore"`} {
		if !strings.Contains(body, want) {
			t.Errorf("canonical envelope key missing: %q\n%s", want, body)
		}
	}
}

// TestTranscriptPaginatesWithCanonicalCursor_spec_15_1_1228 pages
// through a multi-entry transcript using the opaque cursor minted
// by the gateway and confirms the `hasMore` flag flips on the final
// page. F-15.1.19 + F-15.1.20.
func TestTranscriptPaginatesWithCanonicalCursor_spec_15_1_1228(t *testing.T) {
	srv, store := newMessagesServer(t)
	seedRunningSession(t, store, "sess_p")
	// Three user→assistant turns ⇒ 6 transcript entries.
	for i := 0; i < 3; i++ {
		_ = sendMessageRequest(t, srv.Handler(), "sess_p", sessionserver.MessageRequest{
			Messages: []sessionserver.MessagePayload{{Role: "user", Content: "ping"}},
		})
	}

	get := func(query string) transcriptEnvelopeJSON {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet,
			"/v1/sessions/sess_p/transcript"+query, nil)
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("page %q: status %d, body=%s", query, rr.Code, rr.Body.String())
		}
		var env transcriptEnvelopeJSON
		if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode page %q: %v", query, err)
		}
		return env
	}

	first := get("?limit=4")
	if len(first.Items) != 4 || !first.HasMore || first.Cursor == "" {
		t.Fatalf("first page: items=%d hasMore=%v cursor=%q, want 4/true/non-empty",
			len(first.Items), first.HasMore, first.Cursor)
	}
	second := get("?limit=4&cursor=" + first.Cursor)
	if len(second.Items) != 2 || second.HasMore {
		t.Fatalf("second page: items=%d hasMore=%v, want 2/false",
			len(second.Items), second.HasMore)
	}
	if second.Items[0].Seq <= first.Items[len(first.Items)-1].Seq {
		t.Errorf("second page starts at seq %d, want > %d (first page last)",
			second.Items[0].Seq, first.Items[len(first.Items)-1].Seq)
	}
}

// TestTranscriptRejectsOversizedLimit_spec_15_1_1236 confirms the
// §15.1 line 1236 clamp [1, 200] is enforced — requests for limit=500
// see 200 items at most.
func TestTranscriptClampsLimitToSpecMax_spec_15_1_1236(t *testing.T) {
	srv, store := newMessagesServer(t)
	seedRunningSession(t, store, "sess_clamp")
	// 250 message turns ⇒ 500 transcript entries, more than the §15.1
	// hard maximum, so a `?limit=500` request must clamp to 200.
	for i := 0; i < 250; i++ {
		_ = sendMessageRequest(t, srv.Handler(), "sess_clamp", sessionserver.MessageRequest{
			Messages: []sessionserver.MessagePayload{{Role: "user", Content: "x"}},
		})
	}
	req := httptest.NewRequest(http.MethodGet,
		"/v1/sessions/sess_clamp/transcript?limit=500", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	var env transcriptEnvelopeJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Items) != 200 {
		t.Errorf("limit=500: items=%d, want 200 (§15.1 line 1236 clamp)", len(env.Items))
	}
	if !env.HasMore {
		t.Errorf("limit=500 against 500-entry transcript should report hasMore=true")
	}
}

// TestTranscriptRejectsExpiredCursor_spec_15_1_1253 confirms the
// §15.1 line 1253 "cursor_expired" rule fires after the 24-hour TTL.
// F-15.1.20.
func TestTranscriptRejectsExpiredCursor_spec_15_1_1253(t *testing.T) {
	// Build a server with a clock we can advance past the 24-hour TTL.
	store := memstore.New()
	clock := func() func() time.Time {
		now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
		return func() time.Time { return now }
	}()
	srv := sessionserver.New(store, sessionserver.Options{
		Executor:    executor.NewEchoExecutor(),
		Transcripts: transcriptstore.NewMemory(),
		Clock:       clock,
	})
	seedRunningSession(t, store, "sess_expired")
	_ = sendMessageRequest(t, srv.Handler(), "sess_expired", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Role: "user", Content: "a"}},
	})
	// Mint a cursor at t0, then attempt to use it at t0+25h.
	enc := pagination.MintCursor(
		pagination.Sort{Field: "seq", Direction: "asc"},
		"1", "1", clock().Add(-25*time.Hour))

	req := httptest.NewRequest(http.MethodGet,
		"/v1/sessions/sess_expired/transcript?cursor="+enc, nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expired cursor: status %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "cursor_expired") {
		t.Errorf("expired cursor envelope missing cursor_expired rule: %s", rr.Body.String())
	}
}
