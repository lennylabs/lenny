// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests pinning the POST /v1/oauth/token wire envelopes
// against the external standards Lenny claims conformance with: the
// RFC 8693 §2.2.1 successful token-exchange response and the RFC 6749
// §5.2 error response reused by RFC 8693 §2.2.2. The response bodies are
// captured from pkg/tokenservice and compared, field for field, against
// golden fixtures under testdata/ so a rename, dropped field, or changed
// issued_token_type/token_type surfaces as a diff.

package oauth_token_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/tokenservice"
)

// exchangeRaw performs the exchange and returns the raw response body so
// the exact wire structure can be pinned, rather than a pre-parsed map.
func exchangeRaw(t *testing.T, callerTok string, body tokenservice.Request) (*http.Response, []byte) {
	t.Helper()
	ts, _ := newTestServer(t)
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/oauth/token", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+callerTok)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

// canonicalize parses the wire body, replaces volatile fields (the signed
// token and the expiry countdown, which vary per run) with fixed
// placeholders, then re-marshals with sorted keys and stable indentation
// so the result is byte-comparable against a golden fixture.
func canonicalize(t *testing.T, raw []byte, volatile ...string) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal wire body %q: %v", raw, err)
	}
	for _, k := range volatile {
		if _, ok := m[k]; ok {
			m[k] = "<" + k + ">"
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep the URN and placeholder angle brackets literal
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		t.Fatalf("marshal canonical: %v", err)
	}
	return buf.Bytes()
}

func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", path, err)
	}
	if !bytes.Equal(bytes.TrimSpace(want), bytes.TrimSpace(got)) {
		t.Errorf("wire envelope %s drifted from RFC golden.\n--- want ---\n%s\n--- got ---\n%s",
			name, bytes.TrimSpace(want), bytes.TrimSpace(got))
	}
}

// spec: 13.3 (RFC 8693 token exchange: Response access_token,
// issued_token_type, token_type, expires_in, scope)
// diagnosis: the /v1/oauth/token success body drifted from the RFC 8693
//
//	§2.2.1 response envelope. A field was renamed, dropped, or added, or
//	issued_token_type / token_type carry a value other than the
//	standard-mandated one. Compare tokenservice.Response against
//	testdata/success_envelope.golden.json.
func TestSuccessEnvelopeMatchesRFC8693(t *testing.T) {
	_, signer := newTestServer(t)
	subject := mint(t, signer, jwt.Claims{
		Subject:  "alice@acme.com",
		TenantID: "acme",
		Typ:      auth.TokenUserBearer,
		Scope:    "sessions:read sessions:write",
	})
	resp, raw := exchangeRaw(t, subject, tokenservice.Request{
		GrantType:        "urn:ietf:params:oauth:grant-type:token-exchange",
		SubjectToken:     subject,
		SubjectTokenType: "urn:ietf:params:oauth:token-type:jwt",
		Scope:            "sessions:read",
		Audience:         "lenny-gateway",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("success: want 200, got %d (body %s)", resp.StatusCode, raw)
	}
	assertGolden(t, "success_envelope.golden.json", canonicalize(t, raw, "access_token", "expires_in"))
}

// spec: 13.3 (RFC 8693 error object; the rejection carries no body beyond
// the RFC 8693 error object)
// diagnosis: the /v1/oauth/token error body drifted from the RFC 6749
//
//	§5.2 / RFC 8693 §2.2.2 error envelope. The {error, error_description}
//	structure changed, or a rejection leaked extra fields. Compare
//	writeOAuthError against testdata/error_envelope.golden.json.
func TestErrorEnvelopeMatchesRFC8693(t *testing.T) {
	_, signer := newTestServer(t)
	subject := mint(t, signer, jwt.Claims{
		Subject: "alice", TenantID: "acme",
		Typ: auth.TokenUserBearer, Scope: "sessions:read",
	})
	resp, raw := exchangeRaw(t, subject, tokenservice.Request{
		GrantType:        "client_credentials",
		SubjectToken:     subject,
		SubjectTokenType: "urn:ietf:params:oauth:token-type:jwt",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("error: want 400, got %d (body %s)", resp.StatusCode, raw)
	}
	assertGolden(t, "error_envelope.golden.json", canonicalize(t, raw))
}
