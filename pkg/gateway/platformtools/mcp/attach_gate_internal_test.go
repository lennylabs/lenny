// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// tenantCaller answers every request with an authenticated caller of one
// tenant.
type tenantCaller string

func (t tenantCaller) RequestCaller(context.Context) (seam.Caller, bool) {
	return seam.Caller{TenantID: string(t), Subject: "alice@acme.com"}, true
}

// callerAs answers every request with one caller, or with none.
type callerAs struct {
	caller seam.Caller
	ok     bool
}

func (c callerAs) RequestCaller(context.Context) (seam.Caller, bool) { return c.caller, c.ok }

// lookupSessionsInternal answers every session lookup with err, or with a
// session of the tenant it was asked for when err is nil.
type lookupSessionsInternal struct {
	seam.Sessions
	err error
}

func (l lookupSessionsInternal) LookupSession(_ context.Context, tenantID, sessionID string) (seam.Session, error) {
	if l.err != nil {
		return seam.Session{}, l.err
	}
	return seam.Session{ID: sessionID, TenantID: tenantID, UserID: "alice@acme.com"}, nil
}

// grantAccessInternal grants or refuses every session access.
type grantAccessInternal bool

func (g grantAccessInternal) SessionAccessible(context.Context, seam.Session, seam.SessionAction) bool {
	return bool(g)
}

// TestAttachGateRefusesBeforeAnyReadOrStream checks the attach gate's
// answers: a caller with no subject is refused before any lookup, a
// request whose tenant did not resolve is refused, a missing session and a
// session the caller may not read answer alike as not found, any other
// lookup failure answers INTERNAL_ERROR with its text, and a readable
// session is admitted. With no session lookup wired there is no gate.
// spec: GWSEAM-LookupSession, GWSEAM-SessionAccessible, GWSEAM-RequestCaller
func TestAttachGateRefusesBeforeAnyReadOrStream(t *testing.T) {
	alice := callerAs{caller: seam.Caller{Subject: "alice@acme.com", TenantID: "acme"}, ok: true}
	cases := map[string]struct {
		cfg      AttachConfig
		tenant   string
		wantCode string
		wantMsg  string
	}{
		"no caller": {
			cfg:      AttachConfig{Callers: callerAs{}, Sessions: lookupSessionsInternal{}, Access: grantAccessInternal(true)},
			tenant:   "acme",
			wantCode: "UNAUTHORIZED", wantMsg: "attach_session requires an authenticated principal subject",
		},
		"no caller identity wired": {
			cfg:      AttachConfig{Sessions: lookupSessionsInternal{}, Access: grantAccessInternal(true)},
			tenant:   "acme",
			wantCode: "UNAUTHORIZED", wantMsg: "attach_session requires an authenticated principal subject",
		},
		"empty subject": {
			cfg:      AttachConfig{Callers: callerAs{caller: seam.Caller{TenantID: "acme"}, ok: true}, Sessions: lookupSessionsInternal{}, Access: grantAccessInternal(true)},
			tenant:   "acme",
			wantCode: "UNAUTHORIZED", wantMsg: "attach_session requires an authenticated principal subject",
		},
		"unresolved tenant": {
			cfg:      AttachConfig{Callers: alice, Sessions: lookupSessionsInternal{}, Access: grantAccessInternal(true)},
			wantCode: "UNAUTHORIZED", wantMsg: "attach_session: tenant could not be resolved; auth chain must precede MCP dispatch",
		},
		"missing session": {
			cfg:      AttachConfig{Callers: alice, Sessions: lookupSessionsInternal{err: seam.ErrSessionNotFound}, Access: grantAccessInternal(true)},
			tenant:   "acme",
			wantCode: "RESOURCE_NOT_FOUND", wantMsg: "session not found",
		},
		"store failure": {
			cfg:      AttachConfig{Callers: alice, Sessions: lookupSessionsInternal{err: errors.New("postgres down")}, Access: grantAccessInternal(true)},
			tenant:   "acme",
			wantCode: "INTERNAL_ERROR", wantMsg: "postgres down",
		},
		"inaccessible session": {
			cfg:      AttachConfig{Callers: alice, Sessions: lookupSessionsInternal{}, Access: grantAccessInternal(false)},
			tenant:   "acme",
			wantCode: "RESOURCE_NOT_FOUND", wantMsg: "session not found",
		},
		"readable session": {
			cfg:    AttachConfig{Callers: alice, Sessions: lookupSessionsInternal{}, Access: grantAccessInternal(true)},
			tenant: "acme",
		},
		"no gate wired": {
			cfg:    AttachConfig{Callers: callerAs{}},
			tenant: "",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := tc.cfg.authorize(context.Background(), tc.tenant, "sess-1")
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("authorize = %v, want nil", err)
				}
				return
			}
			var te *ToolError
			if !errors.As(err, &te) || te.Code != tc.wantCode || te.Msg != tc.wantMsg {
				t.Fatalf("authorize = %v, want %s %q", err, tc.wantCode, tc.wantMsg)
			}
		})
	}
}

// TestAttachTenantIsTheCallersTenant checks that the stream runs under the
// tenant of the request's caller, and under the empty tenant when no caller
// resolves or no caller identity is wired.
// spec: GWSEAM-RequestCaller
func TestAttachTenantIsTheCallersTenant(t *testing.T) {
	r := httptest.NewRequest("POST", "/mcp", nil)
	if got := (AttachConfig{Callers: tenantCaller("acme")}).tenant(r); got != "acme" {
		t.Errorf("tenant = %q, want acme", got)
	}
	if got := (AttachConfig{Callers: callerAs{}}).tenant(r); got != "" {
		t.Errorf("tenant with no caller = %q, want empty", got)
	}
	if got := (AttachConfig{}).tenant(r); got != "" {
		t.Errorf("tenant with no caller identity = %q, want empty", got)
	}
}
