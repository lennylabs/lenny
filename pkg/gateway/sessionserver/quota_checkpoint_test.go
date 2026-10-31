// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// recCheckpointer records the §11.2 final-checkpoint calls.
type recCheckpointer struct{ calls [][2]string }

func (r *recCheckpointer) CheckpointSubject(_ context.Context, tenantID, userID string) error {
	r.calls = append(r.calls, [2]string{tenantID, userID})
	return nil
}

// spec: §11.2 — a session reaching a terminal state writes the
// final cumulative token-usage checkpoint for its (tenant, user). F-11.2.4.
func TestSessionCompletionWritesFinalQuotaCheckpoint_spec_11_2(t *testing.T) {
	cp := &recCheckpointer{}
	srv := sessionserver.New(memstore.New(), sessionserver.Options{DevTenantHeader: true, QuotaCheckpointer: cp})
	h := asOwner(srv.Handler())

	rr := createRequest(t, h,
		sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
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

// A session row with no user id writes no per-user final checkpoint (the
// guard short-circuits). No create path writes such a row, because the
// owner is the creating principal's subject, and no session route reaches
// one, because no caller owns it (§10.2 Session ownership rules 1 and 5),
// so the test drives the terminal pipeline directly.
func TestSessionCompletionNoCheckpointWithoutUser_spec_11_2(t *testing.T) {
	cp := &recCheckpointer{}
	srv := sessionserver.New(memstore.New(), sessionserver.Options{DevTenantHeader: true, QuotaCheckpointer: cp})
	now := time.Now().UTC()
	srv.OnSessionTerminal(context.Background(), session.StateRunning, sessionstore.Session{
		ID: "sess_userless", TenantID: "acme", RuntimeRef: "claude-code", State: session.StateCompleted,
		CreatedAt: now, UpdatedAt: now,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.WaitTerminalWork(ctx); err != nil {
		t.Fatalf("wait for the terminal pipeline: %v", err)
	}
	if len(cp.calls) != 0 {
		t.Errorf("final checkpoint calls = %v, want none for a user-less session", cp.calls)
	}
}
