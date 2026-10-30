// SPDX-License-Identifier: MIT

//go:build contract

package rest_openai_responses_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// holdingBinder creates a running session row with a fresh UUID id and
// reports hold, as the gateway's binder does for a multi_turn runtime with
// injection supported.
type holdingBinder struct{ store sessionstore.Store }

func (b holdingBinder) BindSingleShot(ctx context.Context, spec translator.SingleShotSpec) (translator.SingleShotBinding, error) {
	id := session.NewID()
	now := time.Now().UTC()
	err := b.store.Create(ctx, sessionstore.Session{
		ID: id, TenantID: spec.TenantID, UserID: spec.UserID, RuntimeRef: spec.RuntimeRef,
		Environment: spec.Environment, State: session.StateRunning, CreatedAt: now, UpdatedAt: now,
	})
	return translator.SingleShotBinding{SessionID: id, Hold: true}, err
}

// refusingForwarder reports every turn not local after writing the refusal
// the handler hands it, as the session server does when the coordinating
// replica cannot be reached.
type refusingForwarder struct{}

func (refusingForwarder) ForwardResponsesTurn(w http.ResponseWriter, _ *http.Request, row sessionstore.Session, _ []byte,
	refuse func(http.ResponseWriter, string),
) bool {
	refuse(w, string(row.State))
	return false
}

func (refusingForwarder) Forwarded(context.Context) bool { return false }

// newHeldServer serves the Open Responses translator with a holding binder
// and the given forwarder (nil for the single-process default).
func newHeldServer(t *testing.T, exec executor.Executor, fwd translator.HolderForwarder) *httptest.Server {
	t.Helper()
	store := memstore.New()
	h := translator.NewOpenResponsesHandler(store, exec, translator.OpenResponsesOptions{
		DevTenantHeader:  true,
		SingleShotBinder: holdingBinder{store: store},
		Transcripts:      transcriptstore.NewMemory(),
		HolderForwarder:  fwd,
	})
	srv := httptest.NewServer(h.Handler())
	t.Cleanup(srv.Close)
	return srv
}

// postTurn posts one turn and decodes the response.
func postTurn(t *testing.T, url, prev, input string) (*http.Response, []byte) {
	t.Helper()
	body := map[string]any{"model": "echo", "input": input}
	if prev != "" {
		body["previous_response_id"] = prev
	}
	buf, _ := json.Marshal(body)
	return postJSON(t, url+"/v1/responses", "acme", buf)
}

// spec: §15 (Open Responses conversation sessions)
// diagnosis: a warm continuation of a held session's newest response that
// answers an id other than the same session's next ordinal, or a
// previous_response_id other than the request's, means the warm path in
// pkg/gateway/environment/translator/open_responses.go (serveWarm) no
// longer formats the response id as <session>.<n+1> on the wire.
func TestHeldSessionWarmTurnAnswersTheNextOrdinal_spec_15(t *testing.T) {
	srv := newHeldServer(t, executor.NewEchoExecutor(), nil)
	resp, raw := postTurn(t, srv.URL, "", "one")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("root POST: %d, body=%s", resp.StatusCode, raw)
	}
	var root translator.OpenResponsesResponse
	_ = json.Unmarshal(raw, &root)
	if !responseIDPattern.MatchString(root.ID) {
		t.Fatalf("root id %q is not <uuid>.<n>", root.ID)
	}
	sessionID, n := splitResponseID(t, root.ID)
	if n != "1" {
		t.Fatalf("root ordinal = %q, want 1", n)
	}
	resp, raw = postTurn(t, srv.URL, root.ID, "two")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("warm POST: %d, body=%s", resp.StatusCode, raw)
	}
	var warm translator.OpenResponsesResponse
	_ = json.Unmarshal(raw, &warm)
	if warm.ID != sessionID+".2" || warm.PreviousResponseID != root.ID {
		t.Errorf("warm response id %q previous %q, want %s.2 and %s", warm.ID, warm.PreviousResponseID, sessionID, root.ID)
	}
}

// spec: §15 (Open Responses conversation sessions), §29.3 (Interactive message send) Forwarding
// diagnosis: a warm turn whose forward to the coordinating replica is refused
// that answers anything other than 409 in the native OpenAI envelope with
// error.code TARGET_NOT_READY and error.current_state, means the refusal
// writer (writeTargetNotReady in
// pkg/gateway/environment/translator/openai_chat.go) or the CurrentState
// field of OpenAIErrorBody drifted from the wire contract.
func TestRefusedWarmTurnIsNative409WithCurrentState_spec_29_3(t *testing.T) {
	srv := newHeldServer(t, executor.NewEchoExecutor(), refusingForwarder{})
	resp, raw := postTurn(t, srv.URL, "", "one")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("root POST: %d, body=%s", resp.StatusCode, raw)
	}
	var root translator.OpenResponsesResponse
	_ = json.Unmarshal(raw, &root)
	resp, raw = postTurn(t, srv.URL, root.ID, "two")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("refused POST: %d, want 409; body=%s", resp.StatusCode, raw)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var env struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if env.Error["code"] != "TARGET_NOT_READY" || env.Error["current_state"] != "running" ||
		env.Error["type"] != "invalid_request_error" || env.Error["message"] == "" {
		t.Errorf("refusal body = %s, want the native envelope with TARGET_NOT_READY and current_state running", raw)
	}
}

// failingSecondSend answers the first Send and fails every later one, so a
// warm turn fails after its input reached the runtime.
type failingSecondSend struct {
	executor.Executor
	sends int
}

func (f *failingSecondSend) Send(ctx context.Context, id string, m []executor.Message) (executor.Response, error) {
	f.sends++
	if f.sends > 1 {
		return executor.Response{}, errors.New("adapter stream reset")
	}
	return f.Executor.Send(ctx, id, m)
}

// spec: §15 (Open Responses conversation sessions)
// diagnosis: a warm turn that fails after delivery and answers anything other
// than the native 500 server_error envelope means the failure exit in
// pkg/gateway/environment/translator/open_responses.go (failTurn) drifted.
func TestFailedWarmTurnIsNative500_spec_15(t *testing.T) {
	srv := newHeldServer(t, &failingSecondSend{Executor: executor.NewEchoExecutor()}, nil)
	resp, raw := postTurn(t, srv.URL, "", "one")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("root POST: %d, body=%s", resp.StatusCode, raw)
	}
	var root translator.OpenResponsesResponse
	_ = json.Unmarshal(raw, &root)
	resp, raw = postTurn(t, srv.URL, root.ID, "two")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("failed warm POST: %d, want 500; body=%s", resp.StatusCode, raw)
	}
	if env := decodeOpenAIError(t, resp, raw); env.Error.Type != "server_error" {
		t.Errorf("error.type = %q, want server_error", env.Error.Type)
	}
}
