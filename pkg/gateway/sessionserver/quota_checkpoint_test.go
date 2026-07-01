// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// recCheckpointer records the §11.2 line 44 final-checkpoint calls.
type recCheckpointer struct{ calls [][2]string }

func (r *recCheckpointer) CheckpointSubject(_ context.Context, tenantID, userID string) error {
	r.calls = append(r.calls, [2]string{tenantID, userID})
	return nil
}

// spec: §11.2 line 44 — a session reaching a terminal state writes the
// final cumulative token-usage checkpoint for its (tenant, user). F-11.2.4.
func TestSessionCompletionWritesFinalQuotaCheckpoint_spec_11_2(t *testing.T) {
	cp := &recCheckpointer{}
	srv := sessionserver.New(memstore.New(), sessionserver.Options{QuotaCheckpointer: cp})
	h := srv.Handler()

	rr := createRequest(t, h,
		sessionserver.CreateSessionRequest{RuntimeRef: "claude-code", UserID: "alice@acme.com"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	var created sessionserver.CreateSessionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	term := sessionRequest(t, h, http.MethodPost, "/v1/sessions/"+created.ID+"/terminate")
	if term.Code != http.StatusOK {
		t.Fatalf("terminate: status %d, body %s", term.Code, term.Body.String())
	}

	if len(cp.calls) != 1 || cp.calls[0] != [2]string{"acme", "alice@acme.com"} {
		t.Errorf("final checkpoint calls = %v, want one (acme, alice@acme.com)", cp.calls)
	}
}

// A session with no user id writes no per-user final checkpoint (the
// guard short-circuits), and the absence of a checkpointer never breaks a
// terminal transition.
func TestSessionCompletionNoCheckpointWithoutUser_spec_11_2(t *testing.T) {
	cp := &recCheckpointer{}
	srv := sessionserver.New(memstore.New(), sessionserver.Options{QuotaCheckpointer: cp})
	h := srv.Handler()

	id := createSessionID(t, h) // no principal → empty user id
	term := sessionRequest(t, h, http.MethodPost, "/v1/sessions/"+id+"/terminate")
	if term.Code != http.StatusOK {
		t.Fatalf("terminate: status %d, body %s", term.Code, term.Body.String())
	}
	if len(cp.calls) != 0 {
		t.Errorf("final checkpoint calls = %v, want none for a user-less session", cp.calls)
	}
}
