// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/callercheck"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// delegateTokens reads every jti as row, or fails every read with err. It
// counts reads so the pod-path case can assert none ran.
type delegateTokens struct {
	row   issuedtokenstore.IssuedToken
	err   error
	reads int
}

func (d *delegateTokens) Get(_ context.Context, _, jti string) (issuedtokenstore.IssuedToken, error) {
	d.reads++
	if d.err != nil {
		return issuedtokenstore.IssuedToken{}, d.err
	}
	row := d.row
	row.JTI = jti
	return row, nil
}

// delegateAudit records appended event types, or fails with err.
type delegateAudit struct {
	err    error
	events []string
}

func (d *delegateAudit) Append(_ context.Context, _, eventType string, _ json.RawMessage, _ time.Time) (audit.Row, error) {
	if d.err != nil {
		return audit.Row{}, d.err
	}
	d.events = append(d.events, eventType)
	return audit.Row{}, nil
}

// newDelegateMCPWithCallerCheck builds a delegation-capable MCP server
// whose caller check runs over deps. The parent session sess_parent is
// running and owned by user_alice.
func newDelegateMCPWithCallerCheck(t *testing.T, deps callercheck.Deps) (*mcp.Server, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		DevTenantFallback: true,
		Store:             store,
		Executor:          executor.NewEchoExecutor(),
		Runtimes:          runtimes,
		CallerCheck:       deps,
		Delegation: delegation.NewService(store, delegation.Options{
			IDFunc: func() string { return "sess_child" },
		}),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_mcp" },
		TenantID: "acme",
	})
	ctx := context.Background()
	_ = runtimes.Create(ctx, runtimestore.Runtime{Name: "child-agent", Type: runtimestore.TypeAgent})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = store.Create(ctx, sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "user_alice",
		State:      session.StateRunning,
		RuntimeRef: "child-agent", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	})
	return srv, store
}

const callerCheckDelegateArgs = `{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","task":{"input":[{"type":"text","inline":"do work"}]}}`

func edgeBearer() authmw.Principal {
	return authmw.Principal{Subject: "user_alice", TenantID: "acme", JTI: "jti-alice"}
}

// spec: §10.2 (Signing topology), §13.3 (Credential Flow: Write-before-issue
// ordering, step (1a)) — client-facing lenny/delegate_task refuses an
// impersonation or revoked bearer with the FORBIDDEN tool error once the
// rejection row is written, and no child session is created. Before the
// caller check the same call committed sess_child.
func TestDelegateTaskRefusesImpersonationAndRevokedBearers_spec_10_2(t *testing.T) {
	rows := map[string]issuedtokenstore.IssuedToken{
		"impersonation": {TenantID: "acme", ActSubject: "bob@platform"},
		"revoked":       {TenantID: "acme", RevokedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	for name, row := range rows {
		t.Run(name, func(t *testing.T) {
			rec := &delegateAudit{}
			srv, store := newDelegateMCPWithCallerCheck(t, callercheck.Deps{IssuedTokens: &delegateTokens{row: row}, Audit: rec})
			resp := callWithPrincipal(t, srv.Handler(), edgeBearer(), "lenny/delegate_task", callerCheckDelegateArgs)
			result, _ := resp["result"].(map[string]any)
			if env := readLennyErrorEnvelope(t, result); env["code"] != "FORBIDDEN" {
				t.Errorf("code = %v, want FORBIDDEN", env["code"])
			}
			if len(rec.events) != 1 || rec.events[0] != "token.exchanged" {
				t.Errorf("audit events = %v, want one token.exchanged rejection row", rec.events)
			}
			if _, err := store.Get(context.Background(), "acme", "sess_child"); err == nil {
				t.Error("a refused delegation must not commit a child session")
			}
		})
	}
}

// spec: §10.2 (Signing topology) — a failed issued_tokens read or a failed
// rejection write answers INTERNAL_ERROR and creates no child.
func TestDelegateTaskCallerCheckFailsClosed_spec_10_2(t *testing.T) {
	failures := map[string]callercheck.Deps{
		"read error":  {IssuedTokens: &delegateTokens{err: errors.New("pool exhausted")}, Audit: &delegateAudit{}},
		"audit error": {IssuedTokens: &delegateTokens{row: issuedtokenstore.IssuedToken{ActSubject: "bob@platform"}}, Audit: &delegateAudit{err: errors.New("audit lock timeout")}},
	}
	for name, deps := range failures {
		t.Run(name, func(t *testing.T) {
			srv, store := newDelegateMCPWithCallerCheck(t, deps)
			resp := callWithPrincipal(t, srv.Handler(), edgeBearer(), "lenny/delegate_task", callerCheckDelegateArgs)
			result, _ := resp["result"].(map[string]any)
			if env := readLennyErrorEnvelope(t, result); env["code"] != "INTERNAL_ERROR" {
				t.Errorf("code = %v, want INTERNAL_ERROR", env["code"])
			}
			if _, err := store.Get(context.Background(), "acme", "sess_child"); err == nil {
				t.Error("a failed caller check must not commit a child session")
			}
		})
	}
}

// spec: §10.2 (Signing topology), §8.2 (Child identity) — a pod-path
// delegate_task carries no jti, so it passes the caller check without an
// issued_tokens read even when every row would refuse, and a live edge
// bearer delegates normally.
func TestDelegateTaskAdmitsPodPathAndLiveBearer_spec_10_2(t *testing.T) {
	cases := []struct {
		name      string
		principal authmw.Principal
		row       issuedtokenstore.IssuedToken
		wantReads int
	}{
		{"pod path", authmw.Principal{Subject: "user_alice", TenantID: "acme", SessionID: "sess_parent"}, issuedtokenstore.IssuedToken{ActSubject: "bob@platform"}, 0},
		{"live edge bearer", edgeBearer(), issuedtokenstore.IssuedToken{TenantID: "acme"}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tokens := &delegateTokens{row: tc.row}
			srv, store := newDelegateMCPWithCallerCheck(t, callercheck.Deps{IssuedTokens: tokens, Audit: &delegateAudit{}})
			resp := callWithPrincipal(t, srv.Handler(), tc.principal, "lenny/delegate_task", callerCheckDelegateArgs)
			result, _ := resp["result"].(map[string]any)
			if result["isError"] == true {
				t.Fatalf("delegation refused: %+v", resp)
			}
			if tokens.reads != tc.wantReads {
				t.Errorf("issued_tokens reads = %d, want %d", tokens.reads, tc.wantReads)
			}
			if _, err := store.Get(context.Background(), "acme", "sess_child"); err != nil {
				t.Errorf("admitted delegation did not commit a child: %v", err)
			}
		})
	}
}
