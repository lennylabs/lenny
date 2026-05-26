// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// mkUserSession seeds a session that carries a user id so the §9.2
// respond-authorization triple tests have a non-empty user component.
func mkUserSession(t *testing.T, store sessionstore.Store, id, user string, parent string) {
	t.Helper()
	now := time.Now()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", UserID: user, State: session.StateRunning,
		ParentSessionID: parent, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed session %s: %v", id, err)
	}
}

// chainDeps builds the elicitation-chain MCP server with the given
// dispatcher configuration and returns the registered server, the
// session store, and the interaction store.
type chainOpts struct {
	timeout    time.Duration
	urlMode    elicitation.URLModeAllowlist
	intercepts func(sessionstore.Session) bool
	audit      mcptools.DelegationAuditor
	depth      elicitation.DepthPolicy
	suppressAt int
}

func newMCPForChain(t *testing.T, opts chainOpts) (*mcp.Server, sessionstore.Store, interactionstore.Store) {
	t.Helper()
	store := memstore.New()
	interactions := interactionstore.NewMemory()
	srv := mcp.NewServer()
	timeout := opts.timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	mcptools.Register(srv, mcptools.Deps{
		Store:                       store,
		Interactions:                interactions,
		ElicitationTimeout:          timeout,
		ElicitationURLModeAllowlist: opts.urlMode,
		ElicitationIntercepts:       opts.intercepts,
		ElicitationDepthPolicy:      opts.depth,
		ElicitationSuppressAtDepth:  opts.suppressAt,
		Audit:                       opts.audit,
		IDFunc:                      func() string { return "elic_gen" },
		TenantID:                    "acme",
	})
	return srv, store, interactions
}

// waitElicitationFor blocks until an elicitation is recorded against
// (sessionID, userID), or fails the test. The §9.2 dispatcher records
// an elicitation under the resolving user, so the wait is user-aware.
func waitElicitationFor(t *testing.T, store interactionstore.Store, sessionID, userID, id string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := store.Get(context.Background(), "acme", sessionID, userID, id); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("elicitation %s was never recorded against %s/%s", id, sessionID, userID)
}

// resolveAt responds to a pending elicitation recorded against the
// given session, the path a human at the chain terminus drives.
func resolveAt(t *testing.T, store interactionstore.Store, sessionID, userID, id string, response any) {
	t.Helper()
	if _, err := store.Resolve(context.Background(), "acme", sessionID, userID, id,
		func(i *interactionstore.Interaction) error {
			i.Phase = interactionstore.PhaseResponded
			i.Response = response
			return nil
		}); err != nil {
		t.Fatalf("resolve %s at %s: %v", id, sessionID, err)
	}
}

// TestElicitationForwardsUpMultipleHops proves a §9.2 elicitation
// raised by a delegated child forwards up the task tree to the
// human-facing root, where it is recorded for resolution.
func TestElicitationForwardsUpMultipleHops(t *testing.T) {
	srv, store, interactions := newMCPForChain(t, chainOpts{})
	// root → mid → leaf delegation tree; the leaf raises the elicitation.
	mkUserSession(t, store, "sess_root", "alice", "")
	mkUserSession(t, store, "sess_mid", "alice", "sess_root")
	mkUserSession(t, store, "sess_leaf", "alice", "sess_mid")
	h := srv.Handler()

	got := make(chan map[string]any, 1)
	go func() {
		got <- call(t, h, "lenny/request_elicitation",
			`{"sessionId":"sess_leaf","message":"approve?","elicitationId":"elic_x"}`)
	}()

	// §9.2: the elicitation forwarded up to the root — it is NOT
	// recorded against the raising leaf or the intermediate hop.
	waitElicitationFor(t, interactions, "sess_root", "alice", "elic_x")
	if _, err := interactions.Get(context.Background(), "acme", "sess_leaf", "alice", "elic_x"); err == nil {
		t.Error("the elicitation was recorded against the raising leaf, not the chain terminus")
	}
	if _, err := interactions.Get(context.Background(), "acme", "sess_mid", "alice", "elic_x"); err == nil {
		t.Error("the elicitation was recorded against an intermediate hop")
	}

	// The human at the root resolves it; the raising leaf unblocks.
	resolveAt(t, interactions, "sess_root", "alice", "elic_x", "yes")
	select {
	case resp := <-got:
		if text := resultText(t, resp); !strings.Contains(text, "yes") {
			t.Errorf("request_elicitation result = %q, want the human response", text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request_elicitation did not return after the root resolved it")
	}
}

// TestElicitationParentIntercepts proves the §9.2 chain terminates at
// an intercepting parent — the elicitation is recorded against that
// parent, not forwarded to the root.
func TestElicitationParentIntercepts(t *testing.T) {
	srv, store, interactions := newMCPForChain(t, chainOpts{
		// The middle session intercepts the chain.
		intercepts: func(s sessionstore.Session) bool { return s.ID == "sess_mid" },
	})
	mkUserSession(t, store, "sess_root", "alice", "")
	mkUserSession(t, store, "sess_mid", "alice", "sess_root")
	mkUserSession(t, store, "sess_leaf", "alice", "sess_mid")
	h := srv.Handler()

	got := make(chan map[string]any, 1)
	go func() {
		got <- call(t, h, "lenny/request_elicitation",
			`{"sessionId":"sess_leaf","message":"approve?","elicitationId":"elic_x"}`)
	}()

	// §9.2: the chain stopped at the intercepting parent — recorded
	// against sess_mid, never reaching the root.
	waitElicitationFor(t, interactions, "sess_mid", "alice", "elic_x")
	if _, err := interactions.Get(context.Background(), "acme", "sess_root", "alice", "elic_x"); err == nil {
		t.Error("the elicitation was forwarded past the intercepting parent to the root")
	}

	// The intercepting parent answers it.
	resolveAt(t, interactions, "sess_mid", "alice", "elic_x", "intercepted-answer")
	select {
	case resp := <-got:
		if text := resultText(t, resp); !strings.Contains(text, "intercepted-answer") {
			t.Errorf("result = %q, want the intercepting parent's answer", text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request_elicitation did not return after the parent intercepted it")
	}
}

// TestElicitationContentDigestRecorded proves the §9.2 gateway-origin
// content-integrity digest is computed and recorded so a forward-hop
// re-emission can be verified against the original {message, schema}.
func TestElicitationContentDigestRecorded(t *testing.T) {
	srv, store, interactions := newMCPForChain(t, chainOpts{})
	mkUserSession(t, store, "sess_root", "alice", "")
	mkUserSession(t, store, "sess_leaf", "alice", "sess_root")
	h := srv.Handler()

	go func() {
		_ = call(t, h, "lenny/request_elicitation",
			`{"sessionId":"sess_leaf","message":"approve the deploy?","schema":{"type":"boolean"},"elicitationId":"elic_x"}`)
	}()
	waitElicitationFor(t, interactions, "sess_root", "alice", "elic_x")

	in, err := interactions.Get(context.Background(), "acme", "sess_root", "alice", "elic_x")
	if err != nil {
		t.Fatalf("get elicitation: %v", err)
	}
	gotDigest, _ := in.Detail["contentDigest"].(string)
	if gotDigest == "" {
		t.Fatal("the recorded elicitation carries no content-integrity digest")
	}
	// The recorded digest must equal the §9.2 canonical digest of the
	// original {message, schema} pair.
	want, err := elicitation.Content{
		Message: "approve the deploy?",
		Schema:  map[string]any{"type": "boolean"},
	}.Digest()
	if err != nil {
		t.Fatalf("reference digest: %v", err)
	}
	if gotDigest != want {
		t.Errorf("recorded digest = %q, want the canonical digest %q", gotDigest, want)
	}
	// A forward-hop re-emission of the unchanged content verifies; a
	// rewritten message is detected as a §9.2 tamper.
	if err := elicitation.VerifyContentAtHop("pod-mid", gotDigest,
		elicitation.Content{Message: "approve the deploy?", Schema: map[string]any{"type": "boolean"}}); err != nil {
		t.Errorf("an unchanged re-emission must verify against the recorded digest: %v", err)
	}
	if err := elicitation.VerifyContentAtHop("pod-mid", gotDigest,
		elicitation.Content{Message: "approve deploy to PROD?", Schema: map[string]any{"type": "boolean"}}); err == nil {
		t.Error("a rewritten message must fail the recorded-digest check")
	}
}

// TestElicitationDepthSuppressed proves a §9.2 depth-suppressed
// elicitation returns a SUPPRESSED response and is never recorded
// anywhere in the chain.
func TestElicitationDepthSuppressed(t *testing.T) {
	srv, store, interactions := newMCPForChain(t, chainOpts{
		depth:      elicitation.DepthSuppressAtDepth,
		suppressAt: 2,
		timeout:    time.Second,
	})
	mkUserSession(t, store, "sess_root", "alice", "")
	mkUserSession(t, store, "sess_mid", "alice", "sess_root")
	mkUserSession(t, store, "sess_leaf", "alice", "sess_mid") // depth 2

	resp := call(t, srv.Handler(), "lenny/request_elicitation",
		`{"sessionId":"sess_leaf","message":"ask","elicitationId":"elic_x"}`)
	text := resultText(t, resp)
	if !strings.Contains(text, "suppressed") {
		t.Errorf("result = %q, want a SUPPRESSED response", text)
	}
	for _, sid := range []string{"sess_leaf", "sess_mid", "sess_root"} {
		if _, err := interactions.Get(context.Background(), "acme", sid, "alice", "elic_x"); err == nil {
			t.Errorf("a suppressed elicitation was recorded against %s", sid)
		}
	}
}

// fakeAuditor records the §11.7 audit events the dispatcher emits.
type fakeAuditor struct {
	events []struct {
		typ    string
		detail map[string]any
	}
}

func (f *fakeAuditor) EmitDelegationEvent(_ context.Context, typ string, detail map[string]any) {
	f.events = append(f.events, struct {
		typ    string
		detail map[string]any
	}{typ, detail})
}

// TestURLModeElicitationAllowedDomain proves a §9.2 url-mode
// elicitation whose domain is on the connector allowlist proceeds.
func TestURLModeElicitationAllowedDomain(t *testing.T) {
	audit := &fakeAuditor{}
	srv, store, interactions := newMCPForChain(t, chainOpts{
		urlMode: elicitation.URLModeAllowlist{
			Enabled:         true,
			DomainAllowlist: []string{"accounts.example.com"},
		},
		audit: audit,
	})
	mkUserSession(t, store, "sess_root", "alice", "")
	mkUserSession(t, store, "sess_leaf", "alice", "sess_root")
	h := srv.Handler()

	go func() {
		_ = call(t, h, "lenny/request_elicitation",
			`{"sessionId":"sess_leaf","message":"sign in","url":"https://accounts.example.com/oauth/authorize","elicitationId":"elic_x"}`)
	}()
	// An allowlisted url-mode elicitation is recorded — it forwarded up
	// the chain normally.
	waitElicitationFor(t, interactions, "sess_root", "alice", "elic_x")
	if len(audit.events) != 0 {
		t.Errorf("an allowed url-mode elicitation emitted audit events: %+v", audit.events)
	}
}

// TestURLModeElicitationDisallowedDomain proves a §9.2 url-mode
// elicitation whose domain is not on the connector allowlist is
// dropped with DOMAIN_NOT_ALLOWLISTED and emits the §11.7 audit
// event.
func TestURLModeElicitationDisallowedDomain(t *testing.T) {
	audit := &fakeAuditor{}
	srv, store, interactions := newMCPForChain(t, chainOpts{
		urlMode: elicitation.URLModeAllowlist{
			Enabled:         true,
			DomainAllowlist: []string{"accounts.example.com"},
		},
		audit: audit,
	})
	mkUserSession(t, store, "sess_root", "alice", "")
	mkUserSession(t, store, "sess_leaf", "alice", "sess_root")

	resp := call(t, srv.Handler(), "lenny/request_elicitation",
		`{"sessionId":"sess_leaf","message":"sign in","url":"https://phish.evil.test/login","elicitationId":"elic_x"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("a disallowed-domain url-mode elicitation should be a tool error: %+v", resp)
	}
	content, _ := result["content"].([]any)
	c0, _ := content[0].(map[string]any)
	if msg, _ := c0["text"].(string); !strings.Contains(msg, "DOMAIN_NOT_ALLOWLISTED") {
		t.Errorf("error = %q, want DOMAIN_NOT_ALLOWLISTED", msg)
	}
	// §11.7: the rejection emitted exactly one audit event naming the
	// originating pod and the rejected host.
	if len(audit.events) != 1 {
		t.Fatalf("recorded %d audit events, want 1", len(audit.events))
	}
	ev := audit.events[0]
	if ev.typ != "elicitation.url_mode_domain_rejected" {
		t.Errorf("audit event type = %q", ev.typ)
	}
	if ev.detail["host"] != "phish.evil.test" {
		t.Errorf("audit host = %v, want the rejected host", ev.detail["host"])
	}
	if ev.detail["originPod"] != "sess_leaf" {
		t.Errorf("audit originPod = %v, want the raising session", ev.detail["originPod"])
	}
	if ev.detail["reason"] != "domain_not_allowlisted" {
		t.Errorf("audit reason = %v", ev.detail["reason"])
	}
	// The dropped elicitation was not recorded.
	if _, err := interactions.Get(context.Background(), "acme", "sess_root", "alice", "elic_x"); err == nil {
		t.Error("a dropped url-mode elicitation was recorded in the interaction store")
	}
}

// TestURLModeElicitationAgentBlockedByDefault proves §9.2 control 1:
// an agent-initiated url-mode elicitation is blocked when the pool
// does not allowlist url-mode at all.
func TestURLModeElicitationAgentBlockedByDefault(t *testing.T) {
	audit := &fakeAuditor{}
	// No urlMode allowlist configured — the §9.2 default blocks
	// agent-initiated url-mode.
	srv, store, _ := newMCPForChain(t, chainOpts{audit: audit})
	mkUserSession(t, store, "sess_root", "alice", "")

	resp := call(t, srv.Handler(), "lenny/request_elicitation",
		`{"sessionId":"sess_root","message":"sign in","url":"https://accounts.example.com/oauth","elicitationId":"elic_x"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("an agent url-mode elicitation should be blocked by default: %+v", resp)
	}
	if len(audit.events) != 1 || audit.events[0].detail["reason"] != "url_mode_disabled" {
		t.Errorf("audit events = %+v, want one url_mode_disabled rejection", audit.events)
	}
}

// TestRequestElicitationDropsSelfAssertedConnector_spec_9_2 proves the
// F-9.2.19 hardening: an agent pod cannot self-declare
// `initiatorType: "connector"` through `lenny/request_elicitation` to
// bypass the agent-initiated url-mode allowlist. The field was removed
// from the tool's input schema; even when a legacy client supplies it,
// the gateway treats the elicitation as agent-initiated and rejects it
// against the empty allowlist with the standard §11.7 url_mode_disabled
// audit event.
//
// spec: §9.2 lines 87–88 (agent binaries cannot self-declare as a
// connector); F-9.2.19.
func TestRequestElicitationDropsSelfAssertedConnector_spec_9_2(t *testing.T) {
	audit := &fakeAuditor{}
	srv, store, interactions := newMCPForChain(t, chainOpts{audit: audit})
	mkUserSession(t, store, "sess_root", "alice", "")

	resp := call(t, srv.Handler(), "lenny/request_elicitation",
		`{"sessionId":"sess_root","message":"sign in","url":"https://github.com/login/oauth/authorize","initiatorType":"connector","elicitationId":"elic_x"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("a self-asserted connector url-mode elicitation must not be admitted: %+v", resp)
	}
	if len(audit.events) != 1 || audit.events[0].detail["reason"] != "url_mode_disabled" {
		t.Errorf("audit events = %+v, want exactly one url_mode_disabled rejection (agent-initiated path)", audit.events)
	}
	// The dropped elicitation was never recorded against the resolver.
	if _, err := interactions.Get(context.Background(), "acme", "sess_root", "alice", "elic_x"); err == nil {
		t.Error("a dropped self-asserted-connector elicitation must not be recorded")
	}
}

// TestResolveElicitationAuthorizedResolver proves the §9.2/§15.1
// respond-authorization triple admits the authorized resolver.
func TestResolveElicitationAuthorizedResolver(t *testing.T) {
	store := interactionstore.NewMemory()
	ctx := context.Background()
	if err := store.Put(ctx, interactionstore.Interaction{
		ID: "elic_x", Kind: interactionstore.KindElicitation,
		SessionID: "sess_root", TenantID: "acme", UserID: "alice",
		Phase: interactionstore.PhasePending,
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	out, err := mcptools.ResolveElicitation(ctx, store, "acme", "sess_root", "alice", "elic_x",
		interactionstore.PhaseResponded, "yes", "")
	if err != nil {
		t.Fatalf("the authorized resolver must succeed: %v", err)
	}
	if out.Phase != interactionstore.PhaseResponded || out.Response != "yes" {
		t.Errorf("resolved interaction = %+v", out)
	}
}

// TestResolveElicitationWrongUserRejected proves the §9.2 triple
// rejects a resolver whose user id does not match — returning the
// not-found condition rather than leaking the elicitation.
func TestResolveElicitationWrongUserRejected(t *testing.T) {
	store := interactionstore.NewMemory()
	ctx := context.Background()
	if err := store.Put(ctx, interactionstore.Interaction{
		ID: "elic_x", Kind: interactionstore.KindElicitation,
		SessionID: "sess_root", TenantID: "acme", UserID: "alice",
		Phase: interactionstore.PhasePending,
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	// bob is not the user the elicitation was directed at.
	_, err := mcptools.ResolveElicitation(ctx, store, "acme", "sess_root", "bob", "elic_x",
		interactionstore.PhaseResponded, "yes", "")
	if err != mcptools.ErrElicitationNotFound {
		t.Fatalf("err = %v, want ErrElicitationNotFound for a wrong-user resolve", err)
	}
}

// TestResolveElicitationWrongSessionRejected proves the §9.2 triple
// rejects a resolution that targets a session the elicitation was
// not issued against (a non-resolver session).
func TestResolveElicitationWrongSessionRejected(t *testing.T) {
	store := interactionstore.NewMemory()
	ctx := context.Background()
	// The elicitation was issued against the chain resolver sess_root.
	if err := store.Put(ctx, interactionstore.Interaction{
		ID: "elic_x", Kind: interactionstore.KindElicitation,
		SessionID: "sess_root", TenantID: "acme", UserID: "alice",
		Phase: interactionstore.PhasePending,
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	// A resolution that names a different session — the raising leaf —
	// must not match.
	_, err := mcptools.ResolveElicitation(ctx, store, "acme", "sess_leaf", "alice", "elic_x",
		interactionstore.PhaseResponded, "yes", "")
	if err != mcptools.ErrElicitationNotFound {
		t.Fatalf("err = %v, want ErrElicitationNotFound for a wrong-session resolve", err)
	}
}

// TestResolveElicitationWrongKindRejected proves a tool_call_id used
// on the §9.2 elicitation resolution path is rejected as not found.
func TestResolveElicitationWrongKindRejected(t *testing.T) {
	store := interactionstore.NewMemory()
	ctx := context.Background()
	if err := store.Put(ctx, interactionstore.Interaction{
		ID: "tc_x", Kind: interactionstore.KindToolUse,
		SessionID: "sess_root", TenantID: "acme", UserID: "alice",
		Phase: interactionstore.PhasePending,
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	_, err := mcptools.ResolveElicitation(ctx, store, "acme", "sess_root", "alice", "tc_x",
		interactionstore.PhaseResponded, "yes", "")
	if err != mcptools.ErrElicitationNotFound {
		t.Fatalf("err = %v, want ErrElicitationNotFound for a tool-use interaction", err)
	}
}

// TestResolveElicitationDismiss proves the §9.2 dismiss path records
// the dismissal and reason through the authorized triple.
func TestResolveElicitationDismiss(t *testing.T) {
	store := interactionstore.NewMemory()
	ctx := context.Background()
	if err := store.Put(ctx, interactionstore.Interaction{
		ID: "elic_x", Kind: interactionstore.KindElicitation,
		SessionID: "sess_root", TenantID: "acme", UserID: "alice",
		Phase: interactionstore.PhasePending,
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	out, err := mcptools.ResolveElicitation(ctx, store, "acme", "sess_root", "alice", "elic_x",
		interactionstore.PhaseDismissed, nil, "user_cancelled")
	if err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if out.Phase != interactionstore.PhaseDismissed || out.Reason != "user_cancelled" {
		t.Errorf("dismissed interaction = %+v", out)
	}
}

// TestMCPRespondToElicitationResolves proves the §9.2 line 108 MCP
// `lenny/respond_to_elicitation` tool resolves a pending elicitation
// when the (sessionId, sessionUserID, elicitationId) triple matches.
// F-9.2.17.
func TestMCPRespondToElicitationResolves(t *testing.T) {
	srv, store, interactions := newMCPForChain(t, chainOpts{})
	mkUserSession(t, store, "sess_root", "alice", "")
	ctx := context.Background()
	if err := interactions.Put(ctx, interactionstore.Interaction{
		ID: "elic_x", Kind: interactionstore.KindElicitation,
		SessionID: "sess_root", TenantID: "acme", UserID: "alice",
		Phase: interactionstore.PhasePending,
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	resp := call(t, srv.Handler(), "lenny/respond_to_elicitation",
		`{"sessionId":"sess_root","elicitationId":"elic_x","response":"option-A"}`)
	text := resultText(t, resp)
	if !strings.Contains(text, "responded") {
		t.Errorf("respond result = %q, want a responded phase", text)
	}
	cur, err := interactions.Get(ctx, "acme", "sess_root", "alice", "elic_x")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if cur.Phase != interactionstore.PhaseResponded || cur.Response != "option-A" {
		t.Errorf("resolved interaction = %+v", cur)
	}
}

// TestMCPRespondToElicitationTripleMismatchSurfacesNotFound verifies
// §9.2 line 108: a (sessionId, userId, elicitationId) mismatch
// collapses to ELICITATION_NOT_FOUND on the MCP surface, mirroring the
// §15.1 REST handler so the existence of another session's elicitation
// never leaks. F-9.2.17.
func TestMCPRespondToElicitationTripleMismatchSurfacesNotFound(t *testing.T) {
	srv, store, interactions := newMCPForChain(t, chainOpts{})
	// The elicitation is bound to alice's session; bob's session targets
	// the same elicitation id and must collapse to not-found.
	mkUserSession(t, store, "sess_root", "alice", "")
	mkUserSession(t, store, "sess_bob", "bob", "")
	ctx := context.Background()
	if err := interactions.Put(ctx, interactionstore.Interaction{
		ID: "elic_x", Kind: interactionstore.KindElicitation,
		SessionID: "sess_root", TenantID: "acme", UserID: "alice",
		Phase: interactionstore.PhasePending,
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	resp := call(t, srv.Handler(), "lenny/respond_to_elicitation",
		`{"sessionId":"sess_bob","elicitationId":"elic_x","response":"option-A"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("a wrong-session resolve must be a tool error: %+v", resp)
	}
	envelope := readErrorEnvelope(t, result)
	if envelope["code"] != "ELICITATION_NOT_FOUND" {
		t.Errorf("envelope.code = %v, want ELICITATION_NOT_FOUND", envelope["code"])
	}
}

// TestMCPDismissElicitation proves the §9.2 line 108 MCP
// `lenny/dismiss_elicitation` tool records the dismissal with the
// caller-supplied reason. F-9.2.17.
func TestMCPDismissElicitation(t *testing.T) {
	srv, store, interactions := newMCPForChain(t, chainOpts{})
	mkUserSession(t, store, "sess_root", "alice", "")
	ctx := context.Background()
	if err := interactions.Put(ctx, interactionstore.Interaction{
		ID: "elic_x", Kind: interactionstore.KindElicitation,
		SessionID: "sess_root", TenantID: "acme", UserID: "alice",
		Phase: interactionstore.PhasePending,
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	_ = call(t, srv.Handler(), "lenny/dismiss_elicitation",
		`{"sessionId":"sess_root","elicitationId":"elic_x","reason":"user_cancelled"}`)
	cur, err := interactions.Get(ctx, "acme", "sess_root", "alice", "elic_x")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if cur.Phase != interactionstore.PhaseDismissed || cur.Reason != "user_cancelled" {
		t.Errorf("dismissed interaction = %+v", cur)
	}
}

// TestMCPRespondToElicitationAlreadyResolvedSurfacesConflict verifies a
// second resolution attempt against an interaction the store has
// already settled surfaces the §15.1 INTERACTION_ALREADY_RESOLVED
// envelope rather than crashing or silently overwriting. F-9.2.17.
func TestMCPRespondToElicitationAlreadyResolvedSurfacesConflict(t *testing.T) {
	srv, store, interactions := newMCPForChain(t, chainOpts{})
	mkUserSession(t, store, "sess_root", "alice", "")
	ctx := context.Background()
	if err := interactions.Put(ctx, interactionstore.Interaction{
		ID: "elic_x", Kind: interactionstore.KindElicitation,
		SessionID: "sess_root", TenantID: "acme", UserID: "alice",
		Phase: interactionstore.PhasePending,
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, err := interactions.Resolve(ctx, "acme", "sess_root", "alice", "elic_x",
		func(i *interactionstore.Interaction) error {
			i.Phase = interactionstore.PhaseResponded
			i.Response = "first"
			return nil
		}); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	resp := call(t, srv.Handler(), "lenny/respond_to_elicitation",
		`{"sessionId":"sess_root","elicitationId":"elic_x","response":"second"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("a second resolution must be a tool error: %+v", resp)
	}
	envelope := readErrorEnvelope(t, result)
	if envelope["code"] != "INTERACTION_ALREADY_RESOLVED" {
		t.Errorf("envelope.code = %v, want INTERACTION_ALREADY_RESOLVED", envelope["code"])
	}
}

// readErrorEnvelope decodes the §15.2.1 lenny error envelope (code,
// category, retryable, message, details) from the `lenny/error`
// content block of an isError MCP tool result.
func readErrorEnvelope(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	content, _ := result["content"].([]any)
	for _, raw := range content {
		block, _ := raw.(map[string]any)
		if block["type"] != "lenny/error" {
			continue
		}
		text, _ := block["text"].(string)
		var env map[string]any
		if err := json.Unmarshal([]byte(text), &env); err != nil {
			t.Fatalf("decode envelope: %v", err)
		}
		return env
	}
	t.Fatalf("no lenny/error block in %+v", content)
	return nil
}
