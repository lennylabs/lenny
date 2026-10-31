// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 finalize upload-token probe. Every finalize request carries the
// uploadToken issued at session creation, and the gateway verifies it
// before it reads or writes the session. This suite drives REST create and
// finalize through sessionserver.New with the production token issuer and
// verifier, and asserts that a caller holding the session bearer but no
// valid token cannot finalize the session or bind a plan into it.
package tier9_security_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

type finalizeTokenProbe struct {
	ts     *httptest.Server
	store  *memstore.Store
	issuer *uploadtoken.Issuer
}

func newFinalizeTokenProbe(t *testing.T) *finalizeTokenProbe {
	t.Helper()
	store := memstore.New()
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("tier9-finalize")})
	issuer := uploadtoken.NewIssuer(ring, nil)
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader:     true,
		UploadTokenIssuer:   issuer,
		UploadTokenVerifier: uploadtoken.NewVerifier(ring, uploadtoken.NewMemoryTracker(), nil),
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &finalizeTokenProbe{ts: ts, store: store, issuer: issuer}
}

func (p *finalizeTokenProbe) do(t *testing.T, path, tenant, token, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, p.ts.URL+path, strings.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("X-Lenny-Upload-Token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	var raw json.RawMessage
	_ = json.NewDecoder(resp.Body).Decode(&raw)
	return resp.StatusCode, string(raw)
}

func (p *finalizeTokenProbe) create(t *testing.T, tenant string) (string, string) {
	t.Helper()
	status, body := p.do(t, "/v1/sessions", tenant, "", `{"runtimeRef":"claude-code","userId":"alice@acme.com"}`)
	if status != http.StatusCreated {
		t.Fatalf("create: status %d body %s", status, body)
	}
	var created struct {
		ID          string `json:"id"`
		UploadToken string `json:"uploadToken"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil || created.UploadToken == "" {
		t.Fatalf("create decode: %v body %s", err, body)
	}
	return created.ID, created.UploadToken
}

// spec: 7.4 (Upload Safety, Upload authorization), 7.1 (Normal Flow, uploadToken format and security properties), 15.1 (finalize)
// diagnosis: a failure means a caller without the session's own valid
// uploadToken finalized the session or bound a plan into it: the token from
// another session, a token minted under a key outside the gateway's ring,
// no token, or a token replayed after the session finalized was accepted.
// Inspect the verifyUploadToken call at the top of handleFinalize in
// pkg/gateway/sessionserver/finalize_handler.go.
func TestFinalizeRefusesForeignAndReplayedUploadTokens_spec_7_4(t *testing.T) {
	p := newFinalizeTokenProbe(t)
	victim, victimToken := p.create(t, "acme")
	_, attackerToken := p.create(t, "acme")
	forged, err := uploadtoken.NewIssuer(uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("guessed")}), nil).Issue(victim, uploadtoken.DefaultTTL)
	if err != nil {
		t.Fatalf("forge: %v", err)
	}
	plan := `{"workspacePlan":{"schemaVersion":1,"sources":[{"type":"inlineFile","path":"evil.sh","content":"x"}]}}`

	refusals := []struct {
		name       string
		token      string
		wantStatus int
		wantCode   string
	}{
		{"no token", "", http.StatusUnauthorized, "UPLOAD_TOKEN_INVALID"},
		{"another session's token", attackerToken, http.StatusForbidden, "UPLOAD_TOKEN_MISMATCH"},
		{"token forged under a guessed key", forged, http.StatusUnauthorized, "UPLOAD_TOKEN_INVALID"},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			status, body := p.do(t, "/v1/sessions/"+victim+"/finalize", "acme", tc.token, plan)
			if status != tc.wantStatus || !strings.Contains(body, tc.wantCode) {
				t.Fatalf("status %d body %s, want %d %s", status, body, tc.wantStatus, tc.wantCode)
			}
			row, err := p.store.Get(context.Background(), "acme", victim)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if row.State != session.StateCreated || len(row.WorkspacePlan) != 0 {
				t.Fatalf("refused finalize changed the session: state %q plan %s", row.State, row.WorkspacePlan)
			}
		})
	}

	if status, body := p.do(t, "/v1/sessions/"+victim+"/finalize", "acme", victimToken, ""); status != http.StatusOK {
		t.Fatalf("owner finalize: status %d body %s", status, body)
	}
	if status, body := p.do(t, "/v1/sessions/"+victim+"/finalize", "acme", victimToken, plan); status != http.StatusGone || !strings.Contains(body, "UPLOAD_TOKEN_CONSUMED") {
		t.Fatalf("replayed token: status %d body %s, want 410 UPLOAD_TOKEN_CONSUMED", status, body)
	}
}
