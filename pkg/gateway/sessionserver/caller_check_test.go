// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/callercheck"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
)

// callerTokens is an issued-token reader keyed by jti. A jti with no
// entry reads as ErrNotFound; err, when set, fails every read.
type callerTokens struct {
	rows  map[string]issuedtokenstore.IssuedToken
	err   error
	reads int
}

func (c *callerTokens) Get(_ context.Context, _, jti string) (issuedtokenstore.IssuedToken, error) {
	c.reads++
	if c.err != nil {
		return issuedtokenstore.IssuedToken{}, c.err
	}
	row, ok := c.rows[jti]
	if !ok {
		return issuedtokenstore.IssuedToken{}, issuedtokenstore.ErrNotFound
	}
	return row, nil
}

// callerAudit records the event types it was asked to append, or fails
// every append with err.
type callerAudit struct {
	err    error
	events []string
}

func (c *callerAudit) Append(_ context.Context, _, eventType string, _ json.RawMessage, _ time.Time) (audit.Row, error) {
	if c.err != nil {
		return audit.Row{}, c.err
	}
	c.events = append(c.events, eventType)
	return audit.Row{}, nil
}

const (
	impersonationJTI = "jti-impersonation"
	revokedJTI       = "jti-revoked"
	liveJTI          = "jti-live"
)

func callerCheckTokens() *callerTokens {
	return &callerTokens{rows: map[string]issuedtokenstore.IssuedToken{
		impersonationJTI: {JTI: impersonationJTI, TenantID: "acme", Subject: "alice@acme.com", ActSubject: "bob@platform"},
		revokedJTI:       {JTI: revokedJTI, TenantID: "acme", Subject: "alice@acme.com", RevokedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		liveJTI:          {JTI: liveJTI, TenantID: "acme", Subject: "alice@acme.com"},
	}}
}

// creationRoute is one root-creating REST route. Each one calls
// requireActiveUser before it reads its request, so a refused bearer is
// answered before the source-session lookup of derive and replay.
type creationRoute struct {
	name, path string
}

var creationRoutes = []creationRoute{
	{"create", "/v1/sessions"},
	{"create-and-start", "/v1/sessions/start"},
	{"derive", "/v1/sessions/sess_source/derive"},
	{"replay", "/v1/sessions/sess_source/replay"},
}

// postAsBearer posts a create body to path as alice holding jti.
func postAsBearer(t *testing.T, h http.Handler, path, jti string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"runtimeRef": "claude-code"})
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req = req.WithContext(authmw.WithPrincipal(req.Context(),
		authmw.Principal{Subject: "alice@acme.com", TenantID: "acme", JTI: jti}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// creationStore returns a session store holding sess_source, owned by
// alice in tenant acme, so the derive and replay routes pass the §10.2
// ownership check and reach the caller check. A refused request leaves
// that one row.
//
// spec: §10.2 (Session ownership) rules 2 and 7.
func creationStore(t *testing.T) *memstore.Store {
	t.Helper()
	st := memstore.New()
	if err := st.Create(context.Background(), sessionstore.Session{
		ID: "sess_source", TenantID: "acme", UserID: "alice@acme.com", State: session.StateCompleted,
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("seed source session: %v", err)
	}
	return st
}

func sessionCount(t *testing.T, store sessionstore.Store) int {
	t.Helper()
	rows, err := store.List(context.Background(), "acme", sessionstore.ListFilter{})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	return len(rows)
}

func errorCode(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v; body %s", err, rr.Body.String())
	}
	return env.Error.Code
}

// spec: §10.2 (Signing topology), §13.3 (Credential Flow: Write-before-issue
// ordering, step (1a)) — every root-creating REST route refuses an
// impersonation or revoked bearer with 403 FORBIDDEN once its
// token.exchanged rejection row is written, and creates no session. The
// server wires no user registry, so the refusal precedes the
// requireActiveUser early return the nil registry takes; before the caller
// check existed every one of these requests reached session creation.
func TestRootCreationRefusesImpersonationAndRevokedBearers_spec_10_2(t *testing.T) {
	for _, route := range creationRoutes {
		for _, jti := range []string{impersonationJTI, revokedJTI} {
			t.Run(route.name+"/"+jti, func(t *testing.T) {
				rec := &callerAudit{}
				store := creationStore(t)
				h := asOwner(sessionserver.New(store, sessionserver.Options{
					DevTenantHeader: true,
					CallerCheck:     callercheck.Deps{IssuedTokens: callerCheckTokens(), Audit: rec},
				}).Handler())
				rr := postAsBearer(t, h, route.path, jti)
				if rr.Code != http.StatusForbidden || errorCode(t, rr) != "FORBIDDEN" {
					t.Fatalf("status %d code %q, want 403 FORBIDDEN; body %s", rr.Code, errorCode(t, rr), rr.Body.String())
				}
				if len(rec.events) != 1 || rec.events[0] != "token.exchanged" {
					t.Errorf("audit events = %v, want one token.exchanged rejection row", rec.events)
				}
				if n := sessionCount(t, store); n != 1 {
					t.Errorf("sessions = %d, want only the source session after a refusal", n)
				}
			})
		}
	}
}

// spec: §10.2 (Signing topology), §13.3 (Credential Flow: Write-before-issue
// ordering, the rejected-exchange discipline) — a failed issued_tokens
// read or a failed rejection write answers 500 INTERNAL_ERROR on every
// root-creating route and creates no session.
func TestRootCreationFailsClosedOnCallerCheckError_spec_10_2(t *testing.T) {
	failures := map[string]callercheck.Deps{
		"read error":  {IssuedTokens: &callerTokens{err: errors.New("pool exhausted")}, Audit: &callerAudit{}},
		"audit error": {IssuedTokens: callerCheckTokens(), Audit: &callerAudit{err: errors.New("audit lock timeout")}},
		"no auditor":  {IssuedTokens: callerCheckTokens()},
	}
	for _, route := range creationRoutes {
		for name, deps := range failures {
			t.Run(route.name+"/"+name, func(t *testing.T) {
				store := creationStore(t)
				h := asOwner(sessionserver.New(store, sessionserver.Options{DevTenantHeader: true, CallerCheck: deps}).Handler())
				rr := postAsBearer(t, h, route.path, impersonationJTI)
				if rr.Code != http.StatusInternalServerError || errorCode(t, rr) != "INTERNAL_ERROR" {
					t.Fatalf("status %d code %q, want 500 INTERNAL_ERROR; body %s", rr.Code, errorCode(t, rr), rr.Body.String())
				}
				if n := sessionCount(t, store); n != 1 {
					t.Errorf("sessions = %d, want only the source session after a failed check", n)
				}
			})
		}
	}
}

// spec: §10.2 (Signing topology), §11.4 (User Invalidation) — the caller
// check runs before the §11.4 user gate, so an impersonation bearer for an
// invalidated user answers FORBIDDEN rather than USER_INVALIDATED.
func TestCallerCheckPrecedesUserGate_spec_10_2(t *testing.T) {
	users := userstore.NewMemory()
	if err := users.Create(context.Background(), userstore.User{Subject: "alice@acme.com", TenantID: "acme", Disabled: true}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	h := asOwner(sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader: true,
		Users:           users,
		CallerCheck:     callercheck.Deps{IssuedTokens: callerCheckTokens(), Audit: &callerAudit{}},
	}).Handler())
	rr := postAsBearer(t, h, "/v1/sessions", impersonationJTI)
	if code := errorCode(t, rr); rr.Code != http.StatusForbidden || code != "FORBIDDEN" {
		t.Fatalf("status %d code %q, want 403 FORBIDDEN", rr.Code, code)
	}
}

// spec: §10.2 (Signing topology) — a live recorded bearer, an unrecorded
// bearer, and a principal with no jti create sessions, and the no-jti
// principal makes no issued_tokens read.
func TestRootCreationAdmitsLiveBearers_spec_10_2(t *testing.T) {
	cases := []struct {
		name, jti string
		wantReads int
	}{
		{"live recorded bearer", liveJTI, 1},
		{"unrecorded bearer", "jti-upstream-idp", 1},
		{"no jti", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tokens := callerCheckTokens()
			rec := &callerAudit{}
			h := asOwner(sessionserver.New(memstore.New(), sessionserver.Options{
				DevTenantHeader: true,
				CallerCheck:     callercheck.Deps{IssuedTokens: tokens, Audit: rec},
			}).Handler())
			rr := postAsBearer(t, h, "/v1/sessions", tc.jti)
			if rr.Code != http.StatusCreated {
				t.Fatalf("status %d, want 201; body %s", rr.Code, rr.Body.String())
			}
			if tokens.reads != tc.wantReads {
				t.Errorf("issued_tokens reads = %d, want %d", tokens.reads, tc.wantReads)
			}
			if len(rec.events) != 0 {
				t.Errorf("audit events = %v, want none for an admitted bearer", rec.events)
			}
		})
	}
}

// spec: §10.2 (Signing topology), §15.2.1 (rule 1) — MCP
// lenny/create_session runs handleCreate through CreateSessionService, so
// it answers the same FORBIDDEN code and category for an impersonation
// bearer that POST /v1/sessions does.
func TestCreateSessionServiceRefusesImpersonationBearer_spec_10_2(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader: true,
		CallerCheck:     callercheck.Deps{IssuedTokens: callerCheckTokens(), Audit: &callerAudit{}},
	})
	ctx := authmw.WithPrincipal(context.Background(),
		authmw.Principal{Subject: "alice@acme.com", TenantID: "acme", JTI: impersonationJTI})
	_, svcErr := srv.CreateSessionService(ctx, "acme", sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
	if svcErr == nil {
		t.Fatal("CreateSessionService admitted an impersonation bearer")
	}
	if svcErr.HTTPStatus != http.StatusForbidden || svcErr.Code != "FORBIDDEN" {
		t.Errorf("service error = (%d, %q), want (403, FORBIDDEN)", svcErr.HTTPStatus, svcErr.Code)
	}
	if !strings.Contains(svcErr.Message, "subject_token_impersonation") {
		t.Errorf("message %q, want it to name the refusal reason", svcErr.Message)
	}
}
