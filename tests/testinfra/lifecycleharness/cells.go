// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaycontrol/inputwaitrecovery"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaycontrol/platformtools"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// This file holds the terminal-writer and resume-exit cell tables, which the
// tier-1 run executes on the in-memory store and the tier-2 run on Postgres.

// preState is one non-terminal pre-state a cell reaches through production
// calls, and the replica the session is bound on afterwards.
type preState struct {
	name  string
	state session.State
	reach func(t *testing.T, h *Harness, r *Replica) string
}

// Report files a failure report through the failure funnel, as the stream
// failure handler does.
func Report(t *testing.T, r *Replica, id, reason string) {
	t.Helper()
	if _, err := r.Server.ReportSessionFailure(context.Background(), sessionserver.FailureReport{
		TenantID: Tenant, SessionID: id, Reason: reason,
	}); err != nil {
		t.Fatalf("ReportSessionFailure: %v", err)
	}
}

// PreState is one non-terminal pre-state a scenario reaches through
// production calls on a replica.
type PreState struct {
	// Name labels the pre-state.
	Name string
	// State is the state the row reaches.
	State session.State
	// Reach starts a session on the replica and drives it to State.
	Reach func(t *testing.T, h *Harness, r *Replica) string
}

// PreStates returns the pre-states the terminal-writer cells use: running,
// held-pod suspended, resume_pending, and awaiting_client_action.
func PreStates() []PreState {
	var out []PreState
	for _, p := range preStates() {
		out = append(out, PreState{Name: p.name, State: p.state, Reach: p.reach})
	}
	return out
}

// CancelSessionMCP calls lenny/cancel_session for the session on the
// replica's MCP surface.
func (h *Harness) CancelSessionMCP(r *Replica, id string) {
	h.T.Helper()
	t, ok := h.T.(*testing.T)
	if !ok {
		h.T.Fatalf("CancelSessionMCP needs a *testing.T")
	}
	mcpCall(t, h, r, "lenny/cancel_session", fmt.Sprintf(`{"sessionId":%q}`, id))
}

func preStates() []preState {
	return []preState{
		{"running", session.StateRunning, func(t *testing.T, h *Harness, r *Replica) string {
			return h.StartSession(r)
		}},
		{"held-pod suspended", session.StateSuspended, func(t *testing.T, h *Harness, r *Replica) string {
			id := h.StartSession(r)
			if code, body := h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/interrupt", nil); code != http.StatusOK {
				t.Fatalf("interrupt: %d %v", code, body)
			}
			return id
		}},
		{"resume_pending", session.StateResumePending, func(t *testing.T, h *Harness, r *Replica) string {
			id := h.StartSession(r)
			Report(t, r, id, string(session.FailureRuntimeCrash))
			return id
		}},
		{"awaiting_client_action", session.StateAwaitingClientAction, func(t *testing.T, h *Harness, r *Replica) string {
			// Exhaust the retry budget: each crash report spends one retry
			// and the driver restores the session; the report after the
			// last retry parks it in awaiting_client_action.
			id := h.StartSession(r)
			for i := 0; i <= MaxRetries; i++ {
				Report(t, r, id, string(session.FailureRuntimeCrash))
				for p := 0; p < 10 && h.Row(id).State == session.StateResumePending; p++ {
					h.Pass()
				}
			}
			return id
		}},
	}
}

// writer is one terminal writer driven through its production caller.
type writer struct {
	name  string
	admit func(session.State) bool
	run   func(t *testing.T, h *Harness, r *Replica, id string)
}

func nonTerminal(st session.State) bool { return !session.IsTerminal(st) }

func mcpCall(t *testing.T, h *Harness, r *Replica, tool, args string) {
	t.Helper()
	h.MCPCall(r, tool, args)
}

// MCPCall calls the MCP tool with the JSON arguments on the replica's MCP
// surface, through its public path behind the auth middleware as the gateway
// wires it, and returns the JSON-RPC result. An off-holder call is forwarded
// to the session's coordinating replica.
func (h *Harness) MCPCall(r *Replica, tool, args string) map[string]any {
	h.T.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + args + `}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(body)))
	req.Header.Set("X-Lenny-Tenant-ID", Tenant)
	req.Header.Set("X-Lenny-User-ID", Owner)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	r.public.ServeHTTP(rr, req)
	var resp struct {
		Result map[string]any `json:"result"`
		Error  map[string]any `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Error != nil {
		return map[string]any{"_error": resp.Error}
	}
	return resp.Result
}

// PlatformCall dispatches the tool with the JSON arguments as a platform
// tool call from the session's pod on the replica, as a call arriving over
// LNK-GWCONTROL is dispatched, and returns the decoded result. An off-holder
// call is forwarded carrying the calling session.
//
// spec: §29.3 (Interactive message send) Forwarding rule 3
func (h *Harness) PlatformCall(r *Replica, sessionID, tool, args string) map[string]any {
	h.T.Helper()
	out, _, err := platformtools.New(r.MCP, h.Inner).CallPlatformTool(context.Background(), sessionID, tool, []byte(args))
	if err != nil {
		return map[string]any{"_error": err.Error()}
	}
	res := map[string]any{}
	_ = json.Unmarshal(out, &res)
	return res
}

// GatewayControlCall dispatches the tool with the JSON arguments as a
// platform tool call from the session's pod arriving over the replica's
// LNK-GWCONTROL connection, and returns the decoded result. It builds the MCP
// surface MCPCall serves with the replica's session event bus, and the call's
// context carries a connection record, so a lenny/request_input wait is
// persisted on the row and its entry and exit writes publish status_change.
// It blocks for the length of the call, so a test runs a waiting call on its
// own goroutine and ends it through ctx or the replica's registry.
//
// spec: §7.2 (Session state machine), §28.3 (Link register)
func (h *Harness) GatewayControlCall(ctx context.Context, r *Replica, sessionID, tool, args string) map[string]any {
	deps := h.replicaMCPDeps(r)
	if r.Events != nil {
		deps.Events = r.Events
	}
	srv := mcp.NewServer()
	mcptools.Register(srv, deps)
	ctx = inputwaitrecovery.NewContext(ctx, r.inputWaitGen)
	out, _, err := platformtools.New(srv, h.Store).CallPlatformTool(ctx, sessionID, tool, []byte(args))
	if err != nil {
		return map[string]any{"_error": err.Error()}
	}
	res := map[string]any{}
	_ = json.Unmarshal(out, &res)
	return res
}

func writers() []writer {
	return []writer{
		{"DELETE", nonTerminal, func(t *testing.T, h *Harness, r *Replica, id string) {
			h.Do(r, http.MethodDelete, "/v1/sessions/"+id, nil)
		}},
		{
			"terminate", func(st session.State) bool { return nonTerminal(st) && st != session.StateResuming },
			func(t *testing.T, h *Harness, r *Replica, id string) {
				h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/terminate", nil)
			},
		},
		{"failure funnel", func(st session.State) bool {
			return st == session.StateRunning || st == session.StateInputRequired || st == session.StateSuspended
		}, func(t *testing.T, h *Harness, r *Replica, id string) {
			row := h.Row(id)
			_, _ = r.Server.ReportSessionFailure(context.Background(), sessionserver.FailureReport{
				TenantID: Tenant, SessionID: id,
				Reason: string(session.FailureWorkspaceValidationFailed), PodAssignment: row.PodAssignment,
			})
		}},
		{"lenny/cancel_session", nonTerminal, func(t *testing.T, h *Harness, r *Replica, id string) {
			mcpCall(t, h, r, "lenny/cancel_session", fmt.Sprintf(`{"sessionId":%q}`, id))
		}},
		{
			"awaiting_client_action expiry", func(st session.State) bool { return st == session.StateAwaitingClientAction },
			func(t *testing.T, h *Harness, r *Replica, id string) {
				// The cell fires the awaiting_client_action timer it writes on.
				w := watchdog.New(h.Store, watchdog.StaticTenants{Tenant}, watchdog.Config{
					MaxAwaitingClientActionSeconds: 1,
					MaxIdleSeconds:                 100000,
					MaxSessionAgeSeconds:           100000,
					MaxResumePendingSeconds:        100000,
				}, h.Clock.Now).WithTerminalHook(r.Server)
				_, _ = w.Tick(context.Background(), h.Clock.Now().Add(time.Hour))
			},
		},
	}
}

// terminalOf is the state a writer writes.
func terminalOf(w string) session.State {
	switch w {
	case "terminate":
		return session.StateCompleted
	case "failure funnel":
		return session.StateFailed
	case "awaiting_client_action expiry":
		return session.StateExpired
	default:
		return session.StateCancelled
	}
}

// RunTerminalWriterCells runs every terminal writer row the harness drives
// through its production caller on every pre-state it reaches, with the
// binding on the writer's replica and on a peer replica, over the session
// store newStore returns (nil for the in-memory store). In each cell the
// writer writes its terminal state exactly when its guard admits the
// pre-state, and otherwise writes no terminal state; the invariant checker
// then holds.
func RunTerminalWriterCells(t *testing.T, newStore func(t *testing.T) sessionstore.Store) {
	for _, w := range writers() {
		t.Run(w.name, func(t *testing.T) {
			var st sessionstore.Store
			if newStore != nil {
				st = newStore(t)
			}
			h := New(t, Options{Replicas: 2, Pods: 48, CredentialPool: true, Store: st})
			binder, peer := h.Replicas[0], h.Replicas[1]
			for _, pre := range preStates() {
				for _, placement := range []string{"this replica", "peer replica"} {
					t.Run(pre.name+"/"+placement, func(t *testing.T) {
						id := pre.reach(t, h, binder)
						h.Settle()
						got := h.Row(id).State
						if got != pre.state {
							t.Fatalf("pre-state = %q, want %q", got, pre.state)
						}
						writerReplica := binder
						if placement == "peer replica" {
							writerReplica = peer
						}
						before := h.Row(id)
						w.run(t, h, writerReplica, id)
						h.Settle()
						after := h.Row(id)
						if w.admit(pre.state) {
							if after.State != terminalOf(w.name) || after.TerminatedAt.IsZero() {
								t.Errorf("state = %q (terminated %v), want %q written", after.State, after.TerminatedAt, terminalOf(w.name))
							}
						} else if session.IsTerminal(after.State) || !after.TerminatedAt.IsZero() {
							// A writer whose caller also drives other work (the
							// watchdog's tick drives a resume_pending row) may
							// move the row, but the refused writer writes no
							// terminal state.
							t.Errorf("state = %q from %q, want no terminal write: the guard refuses this pre-state", after.State, before.State)
						}
						h.Check(t)
					})
				}
			}
		})
	}
}

// RunResumeExitCells runs resume-attempt exits on the rebuild branch of an
// exclusive pool, each from a session a crash report moved to
// resume_pending, over the session store newStore returns (nil for the
// in-memory store): a landed commit, a pre-claim failure that leaves the row
// resume_pending until its window moves it to awaiting_client_action, and a
// failure after the entry write on every attempt until the retry budget ends
// in awaiting_client_action. Each cell reaches its final state and the
// invariant checker holds.
func RunResumeExitCells(t *testing.T, newStore func(t *testing.T) sessionstore.Store) {
	for _, tc := range []struct {
		name      string
		spare     int
		broken    bool
		wantState session.State
	}{
		{"landed commit", 2, false, session.StateRunning},
		{"pre-claim failure", 0, false, session.StateAwaitingClientAction},
		{"failure after the entry write", MaxRetries + 2, true, session.StateAwaitingClientAction},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var st sessionstore.Store
			if newStore != nil {
				st = newStore(t)
			}
			h := New(t, Options{Replicas: 2, Pods: 1, CredentialPool: true, Store: st})
			r := h.Replicas[0]
			id := h.StartSession(r)
			for i := 0; i < tc.spare; i++ {
				p := h.Cluster.AddIdlePod(t)
				if tc.broken {
					p.Break()
				}
			}
			// The session's first pod is retired by the report's release;
			// a cell that needs every claim to fail removes or breaks it.
			for _, p := range h.Cluster.Pods() {
				switch {
				case tc.spare == 0:
					if err := h.Cluster.RemoveSandbox(context.Background(), p); err != nil {
						t.Fatalf("remove sandbox: %v", err)
					}
				case tc.broken:
					p.Break()
				}
			}
			Report(t, r, id, string(session.FailureRuntimeCrash))
			h.Check(t)
			row := h.Row(id)
			if row.State != tc.wantState {
				t.Errorf("final state = %q, want %q", row.State, tc.wantState)
			}
			if tc.wantState == session.StateRunning && row.RecoveryGeneration < 1 {
				t.Errorf("recovery generation = %d, want the commit's advance", row.RecoveryGeneration)
			}
		})
	}
}
