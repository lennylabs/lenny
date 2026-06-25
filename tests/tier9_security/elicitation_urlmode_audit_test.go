// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 §16.7 url-mode elicitation drop audit probe (F-EL3). The §9.2
// url-mode allowlist control drops an agent-initiated url-mode
// `lenny/request_elicitation` whose domain is not allowlisted, and the
// drop writes the §16.7 `elicitation.url_mode_domain_rejected` audit row
// so a security review can reconstruct the policy-relevant rejection from
// the audit log rather than from the drop metric alone.
//
// This suite drives the gateway-side MCP elicitation surface end-to-end
// through the wired audit sink (the same `mcptools.Register` path the
// gateway binary uses), rather than the elicitation dispatcher struct in
// isolation, so the security tier exercises the F-EL3 drop the way a
// caller reaches it. It complements the sibling
// `elicitation_tamper_test.go`, which drives the §9.2 content-tamper
// enforcement-mode admin contract against a live cluster.
package tier9_security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

// capturingDelegationAuditor captures every §16.7 delegation audit event
// the gateway emits so the tier-9 probe can assert the url-mode drop wrote
// exactly one `elicitation.url_mode_domain_rejected` row with the staged
// payload. It satisfies mcptools.DelegationAuditor (the same interface the
// gateway binary wires its append-only audit sink into through Deps.Audit).
// The dispatcher emits from the request-path goroutine while the test reads
// the captured rows, so a mutex keeps the probe -race clean.
type capturingDelegationAuditor struct {
	mu   sync.Mutex
	rows []auditRow
}

type auditRow struct {
	typ    string
	detail map[string]any
}

func (a *capturingDelegationAuditor) EmitDelegationEvent(_ context.Context, eventType string, detail map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rows = append(a.rows, auditRow{typ: eventType, detail: detail})
}

func (a *capturingDelegationAuditor) snapshot() []auditRow {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]auditRow, len(a.rows))
	copy(out, a.rows)
	return out
}

// TestURLModeDropWritesAuditRowThroughGateway_spec_16_7 drives an
// agent-initiated url-mode `lenny/request_elicitation` whose domain is not
// in the pool's url-mode allowlist through the wired gateway MCP surface
// (mcptools.Register with a real audit sink), and asserts the §9.2 drop
// (F-9.2.11) writes exactly one §16.7 `elicitation.url_mode_domain_rejected`
// audit row (F-EL3) carrying the staged payload fields and never the URL's
// query or fragment. This is the security-tier obligation the proposal
// places on the F-EL3 drop: the audit row is the SIEM-visible record of the
// policy-relevant rejection, exercised through the gateway audit path the
// binary uses rather than the dispatcher in isolation.
//
// diagnosis: a failure means an agent-initiated url-mode elicitation blocked
// for a disallowed domain is not auditable through the §16.7 path on the
// wired gateway surface, so a security review cannot reconstruct the drop
// from the audit log, or the audit row leaked the rejected URL's query or
// fragment.
//
// spec: §16.7 (elicitation.url_mode_domain_rejected); §9.2 line 86. F-EL3,
// F-9.2.11.
func TestURLModeDropWritesAuditRowThroughGateway_spec_16_7(t *testing.T) {
	auditor := &capturingDelegationAuditor{}
	h := newGatewayURLModeElicitationServer(t, auditor)

	// An agent-initiated url-mode elicitation to a domain the pool does not
	// allowlist. The URL carries a secret query parameter so the test can
	// assert the audit row never records it.
	resp := callElicitation(t, h,
		`{"sessionId":"sess_leaf","message":"sign in","schema":{},`+
			`"url":"https://phish.evil.test/login?token=secret#frag","elicitationId":"elic_x"}`)

	// The drop surfaces as a DOMAIN_NOT_ALLOWLISTED tool error to the caller.
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("a disallowed-domain url-mode elicitation must be a tool error: %+v", resp)
	}

	rows := auditor.snapshot()
	var dropRows []auditRow
	for _, r := range rows {
		if r.typ == "elicitation.url_mode_domain_rejected" {
			dropRows = append(dropRows, r)
		}
	}
	if len(dropRows) != 1 {
		t.Fatalf("audit rows for elicitation.url_mode_domain_rejected = %d, want exactly 1 (rows=%+v)", len(dropRows), rows)
	}
	detail := dropRows[0].detail

	// The staged §16.7 payload fields are present and correct.
	want := map[string]any{
		"session_id":       "sess_leaf",
		"origin_pod":       "sess_leaf",
		"tenant_id":        "acme",
		"host":             "phish.evil.test",
		"reason":           "domain_not_allowlisted",
		"initiator_type":   "agent",
		"delegation_depth": 1,
	}
	for k, v := range want {
		if got := detail[k]; got != v {
			t.Errorf("audit detail[%q] = %v (%T), want %v", k, got, got, v)
		}
	}

	// detected_at is an RFC3339Nano timestamp.
	ts, _ := detail["detected_at"].(string)
	if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
		t.Errorf("detected_at = %q is not RFC3339Nano: %v", ts, err)
	}

	// The row carries the rejected host but never the full URL's query or
	// fragment (the staged §16.7 payload contract: the security-relevant
	// drop must not leak the secret-bearing tail of the URL into the audit
	// log).
	for k, v := range detail {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if strings.Contains(s, "token=secret") || strings.Contains(s, "frag") {
			t.Errorf("audit detail[%q] leaked the URL query/fragment: %q", k, s)
		}
	}
}

// newGatewayURLModeElicitationServer registers the gateway MCP tools with a
// url-mode allowlist that does NOT contain the requested domain and the
// supplied audit sink wired through Deps.Audit, then seeds an
// agent-initiated session (delegation depth 1, child of a root) so the
// url-mode drop path runs. It returns the MCP HTTP handler.
func newGatewayURLModeElicitationServer(t *testing.T, auditor mcptools.DelegationAuditor) http.Handler {
	t.Helper()
	store := memstore.New()
	interactions := interactionstore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:        store,
		Interactions: interactions,
		ElicitationURLModeAllowlist: elicitation.URLModeAllowlist{
			Enabled:         true,
			DomainAllowlist: []string{"accounts.example.com"},
		},
		Audit:    auditor,
		IDFunc:   func() string { return "elic_gen" },
		TenantID: "acme",
	})
	now := time.Now()
	seed := func(id, parent string, depth int) {
		if err := store.Create(context.Background(), sessionstore.Session{
			ID: id, TenantID: "acme", UserID: "alice", State: session.StateRunning,
			ParentSessionID: parent, DelegationDepth: uint32(depth),
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("seed session %s: %v", id, err)
		}
	}
	seed("sess_root", "", 0)
	seed("sess_leaf", "sess_root", 1)
	return srv.Handler()
}

// callElicitation drives a tools/call against the MCP handler for
// lenny/request_elicitation and returns the decoded JSON-RPC response.
func callElicitation(t *testing.T, h http.Handler, args string) map[string]any {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lenny/request_elicitation","arguments":` + args + `}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(body)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode MCP response: %v; body=%s", err, rr.Body.String())
	}
	return resp
}
