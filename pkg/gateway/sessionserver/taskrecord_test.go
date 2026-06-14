// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/transcriptstore"
)

// getRecordResponse captures only the §8.8 taskRecord projection off the
// GET /v1/sessions/{id} envelope.
type getRecordResponse struct {
	TaskRecord *struct {
		SchemaVersion int    `json:"schemaVersion"`
		TaskID        string `json:"taskId"`
		SessionID     string `json:"sessionId"`
		State         string `json:"state"`
		Messages      []struct {
			Role  string `json:"role"`
			State string `json:"state"`
			Parts []struct {
				Type   string `json:"type"`
				Inline string `json:"inline"`
			} `json:"parts"`
		} `json:"messages"`
		TreeUsage any `json:"treeUsage"`
	} `json:"taskRecord"`
}

func getSession(t *testing.T, h http.Handler, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+id, nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// spec: §8.8 lines 806-823 — GET /v1/sessions/{id} materializes the §8.8
// TaskRecord envelope projected from the row + transcript: caller/agent
// messages with per-entry text OutputParts, the terminal state on the
// final agent turn, and treeUsage absent (null) until descendants
// settle. F-8.8.1.
func TestGetSessionProjectsTaskRecord_spec_8_8_806(t *testing.T) {
	store := memstore.New()
	transcripts := transcriptstore.NewMemory()
	srv := sessionserver.New(store, sessionserver.Options{
		Executor:    executor.NewEchoExecutor(),
		Transcripts: transcripts,
	})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_tr1", TenantID: "acme", State: session.StateCompleted,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := transcripts.Append(
		context.Background(), "acme", "sess_tr1",
		transcriptstore.Entry{Seq: 1, Role: "user", Content: "do the thing"},
		transcriptstore.Entry{Seq: 2, Role: "assistant", Content: "did the thing"},
	); err != nil {
		t.Fatalf("transcript: %v", err)
	}

	rr := getSession(t, srv.Handler(), "sess_tr1")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var resp getRecordResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	rec := resp.TaskRecord
	if rec == nil {
		t.Fatal("GET did not project a taskRecord (§8.8)")
	}
	if rec.SchemaVersion != 1 || rec.TaskID != "sess_tr1" || rec.SessionID != "sess_tr1" {
		t.Errorf("envelope identity = %+v, want schemaVersion=1 taskId=sessionId=sess_tr1", rec)
	}
	// spec: §8.8 line 866 — completed maps to the MCP `completed` state.
	if rec.State != "completed" {
		t.Errorf("record state = %q, want completed", rec.State)
	}
	if rec.TreeUsage != nil {
		t.Errorf("treeUsage must be null until descendants settle, got %v", rec.TreeUsage)
	}
	if len(rec.Messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(rec.Messages))
	}
	// spec: §8.8 lines 810-817 — user → caller, assistant → agent, each
	// transcript line becomes a text OutputPart.
	if rec.Messages[0].Role != "caller" || rec.Messages[0].Parts[0].Inline != "do the thing" {
		t.Errorf("caller message = %+v", rec.Messages[0])
	}
	if rec.Messages[1].Role != "agent" || rec.Messages[1].Parts[0].Type != "text" {
		t.Errorf("agent message = %+v", rec.Messages[1])
	}
	// The terminal task state lands on the final agent turn.
	if rec.Messages[1].State != "completed" {
		t.Errorf("final agent message state = %q, want completed", rec.Messages[1].State)
	}
}

// spec: §8.8 — a gateway with no transcript store wired omits the
// taskRecord rather than emitting an empty envelope. F-8.8.1.
func TestGetSessionOmitsTaskRecordWithoutTranscripts_spec_8_8_806(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{Executor: executor.NewEchoExecutor()})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_no_tr", TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rr := getSession(t, srv.Handler(), "sess_no_tr")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var resp getRecordResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TaskRecord != nil {
		t.Errorf("taskRecord must be absent without a transcript store, got %+v", resp.TaskRecord)
	}
}
