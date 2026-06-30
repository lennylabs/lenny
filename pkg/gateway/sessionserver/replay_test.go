// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/transcriptstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// spec: §15.1 POST /v1/sessions/{id}/replay.

func seedTerminalSource(t *testing.T, store sessionstore.Store, mods ...func(*sessionstore.Session)) {
	t.Helper()
	row := sessionstore.Session{
		ID:               "sess_src",
		TenantID:         "acme",
		State:            session.StateCompleted,
		RuntimeRef:       "claude-code",
		IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt:        time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:        time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{
			Ref:    "/acme/workspace/sess_src/snap",
			Source: sessionstore.WorkspaceSnapshotSealed,
		},
	}
	for _, m := range mods {
		m(&row)
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func replayReq(t *testing.T, h http.Handler, body sessionserver.ReplayRequest) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_src/replay", bytes.NewReader(buf))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestReplayWorkspaceDerive(t *testing.T) {
	store := memstore.New()
	seedTerminalSource(t, store)
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc: func() string { return "sess_replay" },
	})
	rr := replayReq(t, srv.Handler(), sessionserver.ReplayRequest{
		ReplayMode:    sessionserver.ReplayWorkspaceDerive,
		TargetRuntime: "gemini-cli",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.ReplayResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.RuntimeRef != "gemini-cli" || resp.ParentSessionID != "sess_src" {
		t.Errorf("replay: %+v", resp)
	}
}

func TestReplayPromptHistoryCopiesTranscript(t *testing.T) {
	store := memstore.New()
	transcripts := transcriptstore.NewMemory()
	seedTerminalSource(t, store)
	_ = transcripts.Append(context.Background(), "acme", "sess_src",
		transcriptstore.Entry{Role: "user", Content: "original prompt"})

	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:      func() string { return "sess_replay" },
		Transcripts: transcripts,
	})
	rr := replayReq(t, srv.Handler(), sessionserver.ReplayRequest{
		ReplayMode:    sessionserver.ReplayPromptHistory,
		TargetRuntime: "gemini-cli",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	// The replayed session's transcript should carry the source's.
	entries, err := transcripts.Get(context.Background(), "acme", "sess_replay")
	if err != nil {
		t.Fatalf("replayed transcript: %v", err)
	}
	if len(entries) != 1 || entries[0].Content != "original prompt" {
		t.Errorf("transcript not replayed: %+v", entries)
	}
}

func TestReplayRejectsLiveSession(t *testing.T) {
	store := memstore.New()
	seedTerminalSource(t, store, func(s *sessionstore.Session) {
		s.State = session.StateRunning
	})
	srv := sessionserver.New(store, sessionserver.Options{})
	rr := replayReq(t, srv.Handler(), sessionserver.ReplayRequest{TargetRuntime: "x"})
	if rr.Code != http.StatusConflict {
		t.Errorf("live session replay: got %d, want 409", rr.Code)
	}
}

func TestReplayRejectsMissingTargetRuntime(t *testing.T) {
	store := memstore.New()
	seedTerminalSource(t, store)
	srv := sessionserver.New(store, sessionserver.Options{})
	rr := replayReq(t, srv.Handler(), sessionserver.ReplayRequest{})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("missing targetRuntime: got %d, want 400", rr.Code)
	}
}

func TestReplayRejectsNoSnapshot(t *testing.T) {
	store := memstore.New()
	seedTerminalSource(t, store, func(s *sessionstore.Session) {
		s.WorkspaceSnapshot = nil
	})
	srv := sessionserver.New(store, sessionserver.Options{})
	rr := replayReq(t, srv.Handler(), sessionserver.ReplayRequest{TargetRuntime: "x"})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("no snapshot: got %d, want 400", rr.Code)
	}
}

func TestReplayIsolationMonotonicity(t *testing.T) {
	store := memstore.New()
	seedTerminalSource(t, store, func(s *sessionstore.Session) {
		s.IsolationProfile = isolation.ProfileMicrovm
	})
	srv := sessionserver.New(store, sessionserver.Options{})
	// Downgrade microvm → sandboxed without the override.
	rr := replayReq(t, srv.Handler(), sessionserver.ReplayRequest{
		TargetRuntime:          "x",
		TargetIsolationProfile: isolation.ProfileSandboxed,
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("isolation downgrade: got %d, want 422", rr.Code)
	}
}

func TestReplayRejectsBadMode(t *testing.T) {
	store := memstore.New()
	seedTerminalSource(t, store)
	srv := sessionserver.New(store, sessionserver.Options{})
	rr := replayReq(t, srv.Handler(), sessionserver.ReplayRequest{
		ReplayMode:    sessionserver.ReplayMode("teleport"),
		TargetRuntime: "x",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad replayMode: got %d, want 400", rr.Code)
	}
}
