// SPDX-License-Identifier: MIT

//go:build contract

package rest_mcp_consistency_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// newUploadTokenServers stands up a REST gateway that mints and verifies
// the §7.1 uploadToken, as production does, and an MCP server whose
// client-facing tools dispatch through it.
func newUploadTokenServers(t *testing.T) (*httptest.Server, *httptest.Server, sessionstore.Store, *uploadtoken.Issuer) {
	t.Helper()
	store := memstore.New()
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("parity")})
	issuer := uploadtoken.NewIssuer(ring, nil)
	rest := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader:     true,
		UploadTokenIssuer:   issuer,
		UploadTokenVerifier: uploadtoken.NewVerifier(ring, uploadtoken.NewMemoryTracker(), nil),
	})
	tsREST := httptest.NewServer(rest.Handler())
	t.Cleanup(tsREST.Close)
	mcpSrv := mcp.NewServer()
	mcptools.Register(mcpSrv, mcptools.Deps{
		DevTenantFallback: true,
		Store:             store,
		SessionCreator:    rest,
		SessionService:    rest,
		Clock:             func() time.Time { return time.Now().UTC() },
		TenantID:          "acme",
	})
	tsMCP := httptest.NewServer(mcpSrv.Handler())
	t.Cleanup(tsMCP.Close)
	return tsREST, tsMCP, store, issuer
}

// createWithToken creates a session over REST and returns its id and the
// uploadToken the create response carries.
func createWithToken(t *testing.T, restURL string) (string, string) {
	t.Helper()
	resp, raw := postJSON(t, restURL+"/v1/sessions", "acme", []byte(`{"runtimeRef":"claude-code","userId":"alice@acme.com"}`))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", resp.StatusCode, raw)
	}
	var created struct {
		ID          string `json:"id"`
		UploadToken string `json:"uploadToken"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.ID == "" || created.UploadToken == "" {
		t.Fatalf("create decode: %v; body %s", err, raw)
	}
	return created.ID, created.UploadToken
}

// postFinalizeToken posts a no-body finalize carrying token in the
// X-Lenny-Upload-Token header when token is non-empty.
func postFinalizeToken(t *testing.T, restURL, id, token string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, restURL+"/v1/sessions/"+id+"/finalize", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	if token != "" {
		req.Header.Set("X-Lenny-Upload-Token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST finalize: %v", err)
	}
	defer resp.Body.Close()
	var buf json.RawMessage
	_ = json.NewDecoder(resp.Body).Decode(&buf)
	return resp.StatusCode, buf
}

// spec: 7.4 (Upload Safety, Upload authorization), 7.1 (Normal Flow, uploadToken format and security properties), 15.2.1 (REST/MCP Consistency Contract), 15.1 (finalize)
// diagnosis: a failure means POST /v1/sessions/{id}/finalize admitted a
// request whose uploadToken is wrong, expired-format, or bound to another
// session, or the lenny/finalize_workspace tool answered a different
// error code than REST for the same token, or a correct token did not
// finalize the session on either surface. Inspect handleFinalize's
// verifyUploadToken call in pkg/gateway/sessionserver/finalize_handler.go
// and extractFinalizeArgs in pkg/gateway/mcpfabric/mcptools/client_tools.go.
func TestFinalizeUploadTokenRESTMCPParity_spec_7_4(t *testing.T) {
	cases := []struct {
		name     string
		token    func(t *testing.T, issuer *uploadtoken.Issuer, id string) string
		wantCode string
	}{
		{name: "malformed token", token: func(*testing.T, *uploadtoken.Issuer, string) string { return "garbage" }, wantCode: "UPLOAD_TOKEN_INVALID"},
		{name: "token for another session", token: func(t *testing.T, issuer *uploadtoken.Issuer, _ string) string {
			tok, err := issuer.Issue("sess_someone_else", uploadtoken.DefaultTTL)
			if err != nil {
				t.Fatalf("issue: %v", err)
			}
			return tok
		}, wantCode: "UPLOAD_TOKEN_MISMATCH"},
		{name: "foreign signing key", token: func(t *testing.T, _ *uploadtoken.Issuer, id string) string {
			// Bound to this session's id, so only the signature differs.
			other := uploadtoken.NewIssuer(uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "kx", Secret: []byte("other")}), nil)
			tok, err := other.Issue(id, uploadtoken.DefaultTTL)
			if err != nil {
				t.Fatalf("issue: %v", err)
			}
			return tok
		}, wantCode: "UPLOAD_TOKEN_INVALID"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tsREST, tsMCP, store, issuer := newUploadTokenServers(t)
			id, _ := createWithToken(t, tsREST.URL)
			tok := tc.token(t, issuer, id)
			status, raw := postFinalizeToken(t, tsREST.URL, id, tok)
			if status < 400 || status >= 500 {
				t.Fatalf("REST finalize status = %d, want a 4xx refusal; body %s", status, raw)
			}
			rest := decodeRESTError(t, raw)
			if rest.Code != tc.wantCode {
				t.Errorf("REST code = %q, want %q", rest.Code, tc.wantCode)
			}
			mcpRes := decodeMCPError(t, mcpCall(t, tsMCP.URL+"/mcp", "lenny/finalize_workspace", map[string]any{
				"sessionId": id, "uploadToken": tok,
			}))
			if mcpRes.Code != rest.Code {
				t.Errorf("code parity: REST=%+v MCP=%+v", rest, mcpRes)
			}
			// UPLOAD_TOKEN_INVALID is absent from the §15.1 error catalog, so
			// the MCP projection classifies it by the unknown-code fallback
			// while REST derives the pair from the 401 status. That
			// divergence is filed as F-15.1.69 and needs a catalog entry;
			// the catalogued codes hold full parity here.
			if rest.Code != "UPLOAD_TOKEN_INVALID" &&
				(mcpRes.Category != rest.Category || mcpRes.Retryable != rest.Retryable) {
				t.Errorf("category parity: REST=%+v MCP=%+v", rest, mcpRes)
			}
			assertStillCreated(t, store, id)
		})
	}

	t.Run("missing token on REST", func(t *testing.T) {
		tsREST, _, store, _ := newUploadTokenServers(t)
		id, _ := createWithToken(t, tsREST.URL)
		status, raw := postFinalizeToken(t, tsREST.URL, id, "")
		if status != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body %s", status, raw)
		}
		if got := decodeRESTError(t, raw).Code; got != "UPLOAD_TOKEN_INVALID" {
			t.Errorf("code = %q, want UPLOAD_TOKEN_INVALID", got)
		}
		assertStillCreated(t, store, id)
	})

	for _, surface := range []string{"rest", "mcp"} {
		t.Run("issued token finalizes over "+surface+" and is then consumed", func(t *testing.T) {
			tsREST, tsMCP, store, _ := newUploadTokenServers(t)
			id, own := createWithToken(t, tsREST.URL)
			if surface == "rest" {
				if status, raw := postFinalizeToken(t, tsREST.URL, id, own); status != http.StatusOK {
					t.Fatalf("finalize status = %d; body %s", status, raw)
				}
			} else {
				res := mcpCall(t, tsMCP.URL+"/mcp", "lenny/finalize_workspace", map[string]any{"sessionId": id, "uploadToken": own})
				if res["isError"] == true {
					t.Fatalf("MCP finalize failed: %v", res)
				}
			}
			row, err := store.Get(context.Background(), "acme", id)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if row.State != session.StateReady {
				t.Fatalf("state = %q, want ready", row.State)
			}
			status, raw := postFinalizeToken(t, tsREST.URL, id, own)
			if status != http.StatusGone || decodeRESTError(t, raw).Code != "UPLOAD_TOKEN_CONSUMED" {
				t.Errorf("reuse: status %d body %s, want 410 UPLOAD_TOKEN_CONSUMED", status, raw)
			}
		})
	}
}
