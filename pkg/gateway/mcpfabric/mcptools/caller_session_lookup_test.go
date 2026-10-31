// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/memorystore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
)

// ownershipFixture is an MCP server whose session-naming tools are all
// registered, over a store holding alice's running session sess_alice and
// bob's running session sess_bob, each with a pending elicitation.
type ownershipFixture struct {
	srv          *mcp.Server
	store        sessionstore.Store
	interactions interactionstore.Store
}

const (
	aliceSession   = "sess_alice"
	bobSession     = "sess_bob"
	missingSession = "sess_nope"
	bobSubject     = "bob@acme.com"
)

func newOwnershipFixture(t *testing.T, svc mcptools.SessionService) ownershipFixture {
	t.Helper()
	store := memstore.New()
	interactions := interactionstore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		SessionService:      svc,
		DevTenantFallback:   true,
		Store:               store,
		Executor:            executor.NewEchoExecutor(),
		Runtimes:            runtimestore.NewMemory(),
		Events:              sessionevents.NewBus(0),
		InputWaits:          inputwait.NewRegistry(),
		RequestInputTimeout: time.Second,
		Interactions:        interactions,
		ElicitationTimeout:  time.Second,
		Memory:              memorystore.NewInMemory(0, nil),
		Delegation: delegation.NewService(store, delegation.Options{
			IDFunc: func() string { return "sess_child" },
		}),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "id_gen" },
		TenantID: "acme",
	})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for id, owner := range map[string]string{aliceSession: ownerprincipal.Alice, bobSession: bobSubject} {
		if err := store.Create(context.Background(), sessionstore.Session{
			ID: id, TenantID: "acme", UserID: owner, State: session.StateRunning,
			RuntimeRef: "echo", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
		if err := interactions.Put(context.Background(), interactionstore.Interaction{
			ID: "elic_x", Kind: interactionstore.KindElicitation, SessionID: id,
			TenantID: "acme", UserID: owner, Phase: interactionstore.PhasePending,
		}); err != nil {
			t.Fatalf("seed elicitation on %s: %v", id, err)
		}
	}
	return ownershipFixture{srv: srv, store: store, interactions: interactions}
}

// dispatchAs runs one tool call under a principal with subject and returns
// the tool error envelope, or nil on success.
func (f ownershipFixture) dispatchAs(t *testing.T, subject, tool, args string) *mcp.ToolError {
	t.Helper()
	ctx := authmw.WithPrincipal(context.Background(), authmw.Principal{Subject: subject, TenantID: "acme"})
	res, ok, err := f.srv.DispatchTool(ctx, tool, json.RawMessage(args))
	if !ok {
		t.Fatalf("tool %q not registered", tool)
	}
	var te *mcp.ToolError
	if errors.As(err, &te) {
		return te
	}
	if err != nil {
		t.Fatalf("%s: non-tool error %v", tool, err)
	}
	if !res.IsError {
		return nil
	}
	for _, c := range res.Content {
		if c.Type != mcp.LennyErrorContentType {
			continue
		}
		var env struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(c.Text), &env); err != nil {
			t.Fatalf("decode error envelope: %v", err)
		}
		return &mcp.ToolError{Code: env.Code, Msg: env.Message}
	}
	t.Fatalf("%s: error result carries no lenny error envelope: %+v", tool, res)
	return nil
}

// sessionNamingCalls lists one call per tool that names a session from its
// arguments, as a template whose %s is the named session id.
var sessionNamingCalls = []struct{ tool, args string }{
	{mcp.AttachToolName, `{"sessionId":"%s"}`},
	{"lenny/send_message", `{"to":"%s","message":"hi"}`},
	{"lenny/cancel_session", `{"sessionId":"%s"}`},
	{"lenny/get_task_tree", `{"sessionId":"%s"}`},
	{"lenny/cancel_child", `{"parentSessionId":"sess_root","childSessionId":"%s"}`},
	{"lenny/await_children", `{"sessionId":"%s","childIds":["sess_c"]}`},
	{"lenny/set_tracing_context", `{"sessionId":"%s","tracingContext":{"traceparent":"00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"}}`},
	{"lenny/output", `{"sessionId":"%s","output":[{"type":"text","inline":"x"}]}`},
	{"lenny/request_input", `{"sessionId":"%s","parts":[{"type":"text","inline":"ok?"}]}`},
	{"lenny/memory_write", `{"sessionId":"%s","content":"note"}`},
	{"lenny/memory_query", `{"sessionId":"%s","query":"note"}`},
	{"lenny/respond_to_elicitation", `{"sessionId":"%s","elicitationId":"elic_x","response":"ok"}`},
	{"lenny/dismiss_elicitation", `{"sessionId":"%s","elicitationId":"elic_x"}`},
	{"lenny/delegate_task", `{"parentSessionId":"%s","target":"echo","task":{"input":[{"type":"text","inline":"x"}]}}`},
	{"lenny/request_elicitation", `{"sessionId":"%s","message":"approve?","schema":{},"elicitationId":"elic_y"}`},
}

// TestSessionNamingToolsAnswerNonOwnerAsMissing_spec_10_2 pins that every
// MCP tool naming a session from its arguments answers a non-owner `user`
// exactly as it answers a session that does not exist: the same code and the
// same message. Before the ownership check the tools read the store directly,
// so bob acted on and read alice's session.
//
// spec: §10.2 (Session ownership) rules 4, 5, and 7; §15.2 (MCP API)
func TestSessionNamingToolsAnswerNonOwnerAsMissing_spec_10_2(t *testing.T) {
	for _, c := range sessionNamingCalls {
		t.Run(c.tool, func(t *testing.T) {
			f := newOwnershipFixture(t, ownerprincipal.SessionAccess{})
			foreign := f.dispatchAs(t, bobSubject, c.tool, strings.Replace(c.args, "%s", aliceSession, 1))
			missing := f.dispatchAs(t, bobSubject, c.tool, strings.Replace(c.args, "%s", missingSession, 1))
			if foreign == nil || missing == nil {
				t.Fatalf("foreign = %v, missing = %v; both must be refused", foreign, missing)
			}
			if foreign.Code != missing.Code {
				t.Fatalf("foreign code %s != missing code %s", foreign.Code, missing.Code)
			}
			if got := strings.ReplaceAll(foreign.Msg, aliceSession, missingSession); got != missing.Msg {
				t.Errorf("foreign message %q != missing message %q", foreign.Msg, missing.Msg)
			}
			row, err := f.store.Get(context.Background(), "acme", aliceSession)
			if err != nil || row.State != session.StateRunning {
				t.Errorf("alice's session changed under a refused call: state %q, err %v", row.State, err)
			}
		})
	}
}

// TestSessionNamingToolsRefuseEmptySubject_spec_10_2 pins that a call with
// no principal subject receives UNAUTHORIZED on a read tool, on an action
// tool, and on the best-effort cancel, ahead of the miss mapping and the
// best-effort acceptance.
//
// spec: §10.2 (Session ownership) rule 3
func TestSessionNamingToolsRefuseEmptySubject_spec_10_2(t *testing.T) {
	cases := []struct{ tool, args string }{
		{"lenny/get_task_tree", `{"sessionId":"sess_alice"}`},
		{"lenny/output", `{"sessionId":"sess_alice","output":[{"type":"text","inline":"x"}]}`},
		{"lenny/cancel_session", `{"sessionId":"sess_alice","reason":"playground_client_closed"}`},
		{"lenny/respond_to_elicitation", `{"sessionId":"sess_alice","elicitationId":"elic_x","response":"ok"}`},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			f := newOwnershipFixture(t, ownerprincipal.SessionAccess{})
			te := f.dispatchAs(t, "", c.tool, c.args)
			if te == nil || te.Code != "UNAUTHORIZED" {
				t.Fatalf("error = %v, want UNAUTHORIZED", te)
			}
		})
	}
}

// TestCancelSessionBestEffortLeavesForeignSessionRunning_spec_10_2 pins that
// a best-effort cancel naming another user's session takes the accepted
// branch a miss takes and leaves that session's state unchanged.
//
// spec: §10.2 (Session ownership) rules 5 and 7; §27.6
func TestCancelSessionBestEffortLeavesForeignSessionRunning_spec_10_2(t *testing.T) {
	f := newOwnershipFixture(t, ownerprincipal.SessionAccess{})
	if te := f.dispatchAs(t, bobSubject, "lenny/cancel_session",
		`{"sessionId":"sess_alice","reason":"playground_client_closed"}`); te != nil {
		t.Fatalf("best-effort cancel refused: %v", te)
	}
	row, err := f.store.Get(context.Background(), "acme", aliceSession)
	if err != nil || row.State != session.StateRunning {
		t.Fatalf("alice's session state = %q (err %v), want running", row.State, err)
	}
}

// TestSendMessageForeignSenderAnswersAsMissingSender_spec_10_2 pins that a
// send whose fromSessionId names another user's session answers as a send
// from a missing sender, before the sender's outbound limit is charged.
//
// spec: §10.2 (Session ownership) rules 5 and 7; §7.2
func TestSendMessageForeignSenderAnswersAsMissingSender_spec_10_2(t *testing.T) {
	f := newOwnershipFixture(t, ownerprincipal.SessionAccess{})
	foreign := f.dispatchAs(t, bobSubject, "lenny/send_message", `{"to":"sess_bob","message":"hi","fromSessionId":"sess_alice"}`)
	missing := f.dispatchAs(t, bobSubject, "lenny/send_message", `{"to":"sess_bob","message":"hi","fromSessionId":"sess_nope"}`)
	if foreign == nil || missing == nil || foreign.Code != missing.Code || foreign.Code != "SCOPE_DENIED" {
		t.Fatalf("foreign = %v, missing = %v; want the same SCOPE_DENIED refusal", foreign, missing)
	}
}

// TestSessionNamingToolsFailClosedWithoutSessionService_spec_10_2 pins that
// a server with no SessionService grants no session access, even to the
// owner.
//
// spec: §10.2 (Session ownership) rule 7
func TestSessionNamingToolsFailClosedWithoutSessionService_spec_10_2(t *testing.T) {
	f := newOwnershipFixture(t, nil)
	te := f.dispatchAs(t, ownerprincipal.Alice, "lenny/get_task_tree", `{"sessionId":"sess_alice"}`)
	if te == nil || te.Code != "RESOURCE_NOT_FOUND" {
		t.Fatalf("error = %v, want RESOURCE_NOT_FOUND", te)
	}
}

// TestElicitationResolutionOwnershipPrecedesTripleCheck_spec_9_2 pins the
// §9.2 order: a resolution naming another user's session answers
// RESOURCE_NOT_FOUND from the ownership check, an unknown elicitation on the
// caller's own session answers ELICITATION_NOT_FOUND from the triple check,
// and the owner resolves its own elicitation.
//
// spec: §9.2 (respond_to_elicitation authorization); §10.2 (Session
// ownership) rule 5
func TestElicitationResolutionOwnershipPrecedesTripleCheck_spec_9_2(t *testing.T) {
	f := newOwnershipFixture(t, ownerprincipal.SessionAccess{})
	if te := f.dispatchAs(t, ownerprincipal.Alice, "lenny/respond_to_elicitation",
		`{"sessionId":"sess_bob","elicitationId":"elic_x","response":"ok"}`); te == nil || te.Code != "RESOURCE_NOT_FOUND" {
		t.Fatalf("foreign session: error = %v, want RESOURCE_NOT_FOUND", te)
	}
	if te := f.dispatchAs(t, ownerprincipal.Alice, "lenny/respond_to_elicitation",
		`{"sessionId":"sess_alice","elicitationId":"elic_unknown","response":"ok"}`); te == nil || te.Code != "ELICITATION_NOT_FOUND" {
		t.Fatalf("unknown id on own session: error = %v, want ELICITATION_NOT_FOUND", te)
	}
	if te := f.dispatchAs(t, ownerprincipal.Alice, "lenny/respond_to_elicitation",
		`{"sessionId":"sess_alice","elicitationId":"elic_x","response":"ok"}`); te != nil {
		t.Fatalf("owner resolution refused: %v", te)
	}
	cur, _ := f.interactions.Get(context.Background(), "acme", bobSession, bobSubject, "elic_x")
	if cur.Phase != interactionstore.PhasePending {
		t.Errorf("bob's elicitation phase = %q, want pending", cur.Phase)
	}
}
