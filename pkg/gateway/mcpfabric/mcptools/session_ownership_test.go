// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
)

// mcpSessionReader is a tenant custom role granting the tenant read bypass
// alongside the session permissions a user holds.
const mcpSessionReader = "session-reader"

// readToolNames are the session-naming tools that read the session; every
// other row of sessionNamingCalls acts on it.
var readToolNames = map[string]bool{
	mcp.AttachToolName:      true,
	"lenny/get_task_tree":   true,
	"lenny/memory_query":    true,
	"lenny/discover_agents": true,
}

// serverSessionService returns a builder for a *sessionserver.Server over
// the fixture's store, with acme's session-reader custom role registered,
// so the tools decide through the production ownership predicate.
func serverSessionService(t *testing.T) func(sessionstore.Store) mcptools.SessionService {
	t.Helper()
	roles := customrolestore.NewMemory()
	if err := roles.Create(context.Background(), customrolestore.CustomRole{
		TenantID: "acme", Name: mcpSessionReader,
		Permissions: []pkgauth.Permission{
			pkgauth.PermReadTenantSessions, pkgauth.PermReadOwnSessions, pkgauth.PermManageOwnSessions,
		},
	}); err != nil {
		t.Fatalf("seed custom role: %v", err)
	}
	return func(store sessionstore.Store) mcptools.SessionService {
		srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true, CustomRoles: roles})
		t.Cleanup(srv.Close)
		return srv
	}
}

// sameRefusal reports whether foreign and missing are the same tool error,
// code, message, and details alike, once each names its own session id as
// the same placeholder. A refusal that differs from the missing-session
// answer in any field reveals that the foreign session exists.
func sameRefusal(t *testing.T, foreign, missing *mcp.ToolError, foreignID, missingID string) bool {
	t.Helper()
	if foreign == nil || missing == nil {
		return false
	}
	normalized := func(te *mcp.ToolError, id string) string {
		raw, err := json.Marshal(struct {
			Code    string         `json:"code"`
			Msg     string         `json:"msg"`
			Details map[string]any `json:"details"`
		}{te.Code, te.Msg, te.Details})
		if err != nil {
			t.Fatalf("encode tool error: %v", err)
		}
		return strings.ReplaceAll(string(raw), id, "<session>")
	}
	return normalized(foreign, foreignID) == normalized(missing, missingID)
}

// codeOf returns the error code of te, or "" for success.
func codeOf(te *mcp.ToolError) string {
	if te == nil {
		return ""
	}
	return te.Code
}

// TestSessionNamingToolsUseTheServerPredicate_spec_10_2 runs every
// session-naming tool against a *sessionserver.Server. A bob holding the
// session-reader custom role receives on a read tool the same result alice
// receives on her own session, and on an action tool exactly the answer a
// missing session gets, so the read bypass never grants an action.
//
// spec: §10.2 (Session ownership) rules 4, 5, and 7; §15.2 (MCP API)
func TestSessionNamingToolsUseTheServerPredicate_spec_10_2(t *testing.T) {
	reader := authmw.Principal{Subject: bobSubject, TenantID: "acme", Roles: []pkgauth.Role{pkgauth.Role(mcpSessionReader)}}
	alice := authmw.Principal{Subject: ownerprincipal.Alice, TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RoleUser}}
	for _, c := range sessionNamingCalls {
		t.Run(c.tool, func(t *testing.T) {
			onAlice := strings.Replace(c.args, "%s", aliceSession, 1)
			if readToolNames[c.tool] {
				owner := newOwnershipFixtureWith(t, serverSessionService(t), nil).dispatchPrincipal(t, alice, c.tool, onAlice)
				got := newOwnershipFixtureWith(t, serverSessionService(t), nil).dispatchPrincipal(t, reader, c.tool, onAlice)
				if codeOf(got) != codeOf(owner) {
					t.Fatalf("custom-role reader = %v, alice = %v; want the same result", got, owner)
				}
				return
			}
			f := newOwnershipFixtureWith(t, serverSessionService(t), nil)
			foreign := f.dispatchPrincipal(t, reader, c.tool, onAlice)
			missing := f.dispatchPrincipal(t, reader, c.tool, strings.Replace(c.args, "%s", missingSession, 1))
			if !sameRefusal(t, foreign, missing, aliceSession, missingSession) {
				t.Fatalf("custom-role act on alice's session = %+v, on a missing one = %+v; want the same refusal", foreign, missing)
			}
			if row, err := f.store.Get(context.Background(), "acme", aliceSession); err != nil || row.State != session.StateRunning {
				t.Errorf("alice's session changed under a refused act: state %q, err %v", row.State, err)
			}
		})
	}
}

// TestForeignSenderDoesNotChargeTheOwnersLimit_spec_10_2 pins that a send
// whose fromSessionId names another user's session is refused before the
// sender's outbound limit is charged. With a one-message limit, alice's
// next send from her session is still admitted after bob's refused send.
//
// spec: §10.2 (Session ownership) rules 5 and 7; §8.3 (messaging rate
// limits)
func TestForeignSenderDoesNotChargeTheOwnersLimit_spec_10_2(t *testing.T) {
	f := newOwnershipFixtureWith(t, serverSessionService(t),
		func(d *mcptools.Deps) {
			d.MessagingRateLimit = mcptools.MessagingRateLimit{MaxPerMinute: 1, MaxPerSession: 1}
		})
	// alice's second session is a child of sess_alice, so the direct
	// messaging scope reaches it.
	seedRunning(t, f.store, "sess_alice_2", ownerprincipal.Alice, func(row *sessionstore.Session) {
		row.ParentSessionID, row.RootSessionID = aliceSession, aliceSession
	})
	if te := f.dispatchAs(t, bobSubject, "lenny/send_message",
		`{"to":"sess_bob","message":"hi","fromSessionId":"sess_alice"}`); te == nil {
		t.Fatal("bob's send from alice's session was admitted")
	}
	if got := sendStatus(t, f, `{"to":"sess_alice_2","message":"hi","fromSessionId":"sess_alice"}`); got == string(session.DeliveryStatusRateLimited) {
		t.Fatal("alice's first own send is rate_limited; bob's refused send charged her limit")
	}
	// The limit is live: alice's second send from the same session is
	// rate_limited, so the first send's receipt is the discriminating
	// outcome.
	if got := sendStatus(t, f, `{"to":"sess_alice_2","message":"again","fromSessionId":"sess_alice"}`); got != string(session.DeliveryStatusRateLimited) {
		t.Fatalf("alice's second send status = %q, want rate_limited; the one-message limit is not wired", got)
	}
}

// sendStatus sends args through lenny/send_message as alice and returns
// the receipt's status.
func sendStatus(t *testing.T, f ownershipFixture, args string) string {
	t.Helper()
	ctx := authmw.WithPrincipal(context.Background(), authmw.Principal{Subject: ownerprincipal.Alice, TenantID: "acme"})
	res, _, err := f.srv.DispatchTool(ctx, "lenny/send_message", json.RawMessage(args))
	if err != nil || res.IsError || len(res.Content) == 0 {
		t.Fatalf("alice's send: res %+v err %v", res, err)
	}
	var envelope struct {
		DeliveryReceipt session.DeliveryReceipt `json:"deliveryReceipt"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &envelope); err != nil {
		t.Fatalf("decode receipt %s: %v", res.Content[0].Text, err)
	}
	return string(envelope.DeliveryReceipt.Status)
}

// TestDelegateFromForeignRestrictedParentAnswersMissing_spec_10_2 pins that
// delegate_task naming another user's erasure-pending parent answers as a
// missing parent. The lifecycle check is live in the fixture, because
// alice's own delegation from the same parent is refused with
// ERASURE_IN_PROGRESS.
//
// spec: §10.2 (Session ownership) rules 5 and 7; §12.8 (Lifecycle fence
// and drain)
func TestDelegateFromForeignRestrictedParentAnswersMissing_spec_10_2(t *testing.T) {
	users := userstore.NewMemory()
	if err := users.Create(context.Background(), userstore.User{
		Subject: ownerprincipal.Alice, TenantID: "acme", ProcessingRestricted: true, ErasureJobID: "job-1",
	}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	f := newOwnershipFixtureWith(t, serverSessionService(t), func(d *mcptools.Deps) { d.Users = users })
	args := `{"parentSessionId":"%s","target":"echo","task":{"input":[{"type":"text","inline":"x"}]}}`
	foreign := f.dispatchAs(t, bobSubject, "lenny/delegate_task", strings.Replace(args, "%s", aliceSession, 1))
	missing := f.dispatchAs(t, bobSubject, "lenny/delegate_task", strings.Replace(args, "%s", missingSession, 1))
	if !sameRefusal(t, foreign, missing, aliceSession, missingSession) {
		t.Fatalf("bob on alice's restricted parent = %+v, on a missing parent = %+v; want the same refusal", foreign, missing)
	}
	if own := f.dispatchAs(t, ownerprincipal.Alice, "lenny/delegate_task", strings.Replace(args, "%s", aliceSession, 1)); codeOf(own) != "ERASURE_IN_PROGRESS" {
		t.Fatalf("alice's own delegation = %v, want ERASURE_IN_PROGRESS", own)
	}
}

// TestInPodAgentActsOnSameOwnerChild_spec_10_2 pins that an in-pod agent
// principal, bound to its own session, acts on a child session its owner
// also owns.
//
// spec: §10.2 (Session ownership) rule 5; §8.2 (Child identity)
func TestInPodAgentActsOnSameOwnerChild_spec_10_2(t *testing.T) {
	f := newOwnershipFixtureWith(t, serverSessionService(t), nil)
	seedRunning(t, f.store, "sess_alice_child", ownerprincipal.Alice, func(row *sessionstore.Session) {
		row.ParentSessionID, row.RootSessionID = aliceSession, aliceSession
	})
	agent := authmw.Principal{Subject: ownerprincipal.Alice, TenantID: "acme", SessionID: aliceSession}
	if te := f.dispatchPrincipal(t, agent, "lenny/cancel_child",
		`{"parentSessionId":"sess_alice","childSessionId":"sess_alice_child"}`); te != nil {
		t.Fatalf("in-pod cancel of a same-owner child = %v, want success", te)
	}
}

// TestMemoryWriteOnForeignSessionWritesNothing_spec_10_2 pins that a
// memory write by a principal bound to no session, naming another user's
// session, is refused and writes nothing that session's owner can query,
// while the owner's own write is queryable.
//
// spec: §10.2 (Session ownership) rules 5 and 7; §9.4 (memory)
func TestMemoryWriteOnForeignSessionWritesNothing_spec_10_2(t *testing.T) {
	f := newOwnershipFixtureWith(t, serverSessionService(t), nil)
	if te := f.dispatchAs(t, ownerprincipal.Alice, "lenny/memory_write", `{"sessionId":"sess_bob","content":"planted"}`); te == nil {
		t.Fatal("alice's memory write on bob's session was admitted")
	}
	query := func() string {
		res, _, err := f.srv.DispatchTool(authmw.WithPrincipal(context.Background(),
			authmw.Principal{Subject: bobSubject, TenantID: "acme"}), "lenny/memory_query", json.RawMessage(`{"sessionId":"sess_bob","query":"planted"}`))
		if err != nil || res.IsError || len(res.Content) == 0 {
			t.Fatalf("bob's memory query: res %+v err %v", res, err)
		}
		return res.Content[0].Text
	}
	if got := query(); strings.Contains(got, "planted") {
		t.Fatalf("bob's memory holds alice's refused write: %s", got)
	}
	if te := f.dispatchAs(t, bobSubject, "lenny/memory_write", `{"sessionId":"sess_bob","content":"planted"}`); te != nil {
		t.Fatalf("bob's own memory write: %v", te)
	}
	if got := query(); !strings.Contains(got, "planted") {
		t.Fatalf("bob's own write is not queryable, so the refusal check is vacuous: %s", got)
	}
}

// TestCreateToolSchemasCarryNoOwner_spec_10_2 pins that neither MCP create
// tool advertises a userId argument, because the owner is the principal's
// subject and no argument sets it.
//
// spec: §10.2 (Session ownership) rule 1; §15.2 (MCP API)
func TestCreateToolSchemasCarryNoOwner_spec_10_2(t *testing.T) {
	f := newOwnershipFixture(t, ownerprincipal.SessionAccess{})
	seen := map[string]bool{}
	for _, tool := range f.srv.Catalog() {
		if tool.Name != "lenny/create_session" && tool.Name != "lenny/create_and_start_session" {
			continue
		}
		seen[tool.Name] = true
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatalf("%s inputSchema: %v", tool.Name, err)
		}
		if _, ok := schema.Properties["userId"]; ok {
			t.Errorf("%s inputSchema lists userId", tool.Name)
		}
		for _, r := range schema.Required {
			if r == "userId" {
				t.Errorf("%s inputSchema requires userId", tool.Name)
			}
		}
	}
	for _, name := range []string{"lenny/create_session", "lenny/create_and_start_session"} {
		if !seen[name] {
			t.Errorf("%s is not registered", name)
		}
	}
}

// seedRunning writes a running session id owned by owner into store.
func seedRunning(t *testing.T, store sessionstore.Store, id, owner string, mods ...func(*sessionstore.Session)) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	row := sessionstore.Session{
		ID: id, TenantID: "acme", UserID: owner, State: session.StateRunning,
		RuntimeRef: "echo", CreatedAt: now, UpdatedAt: now,
	}
	for _, m := range mods {
		m(&row)
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}
