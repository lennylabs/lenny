// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the §13.3 write-before-issue rule: the
// Token Service records issued-token metadata before it returns a
// minted token, and refuses to issue when the record fails.
package oauth_token_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/tokenservice"
)

// fakeRecorder is an in-memory tokenservice.IssuedTokenStore.
type fakeRecorder struct {
	mu       sync.Mutex
	recorded []issuedtokenstore.IssuedToken
	err      error
}

func (f *fakeRecorder) Record(_ context.Context, tok issuedtokenstore.IssuedToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.recorded = append(f.recorded, tok)
	return nil
}

func newServerWithRecorder(t *testing.T, rec tokenservice.IssuedTokenStore) (*httptest.Server, *jwt.HMACSigner) {
	t.Helper()
	signer := jwt.NewHMACSigner("dev-1", []byte("dev-secret"))
	srv := tokenservice.NewServer(tokenservice.Options{
		Signer:        signer,
		Issuer:        "https://lenny.dev.test/token",
		PerDialectCap: map[string]time.Duration{"lenny-gateway": 24 * time.Hour},
		IssuedTokens:  rec,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, signer
}

// spec: 13.3 (write-before-issue: the issued token is recorded before
// it is returned to the caller)
func TestTokenServiceRecordsBeforeIssue(t *testing.T) {
	rec := &fakeRecorder{}
	ts, signer := newServerWithRecorder(t, rec)
	subject := mint(t, signer, jwt.Claims{
		Subject:  "alice@acme.com",
		TenantID: "acme",
		Typ:      auth.TokenUserBearer,
		Scope:    "sessions:read sessions:write",
	})
	resp, body := exchange(t, ts, subject, tokenservice.Request{
		GrantType:        "urn:ietf:params:oauth:grant-type:token-exchange",
		SubjectToken:     subject,
		SubjectTokenType: "urn:ietf:params:oauth:token-type:jwt",
		Scope:            "sessions:read",
		Audience:         "lenny-gateway",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("exchange: want 200, got %d (%v)", resp.StatusCode, body)
	}
	tok, _ := body["access_token"].(string)
	if tok == "" {
		t.Fatal("missing access_token")
	}
	if len(rec.recorded) != 1 {
		t.Fatalf("recorder got %d records, want 1", len(rec.recorded))
	}
	got := rec.recorded[0]
	issued, err := signer.Verify(tok)
	if err != nil {
		t.Fatalf("verify issued token: %v", err)
	}
	if got.JTI != issued.JWTID {
		t.Errorf("recorded jti %q != issued token jti %q", got.JTI, issued.JWTID)
	}
	if got.TenantID != "acme" || got.Subject != "alice@acme.com" {
		t.Errorf("recorded tenant/subject: %q/%q", got.TenantID, got.Subject)
	}
	if got.Audience != "lenny-gateway" {
		t.Errorf("recorded audience %q, want lenny-gateway", got.Audience)
	}
	if !slices.Equal(got.Scope, []string{"sessions:read"}) {
		t.Errorf("recorded scope %v, want the narrowed [sessions:read]", got.Scope)
	}
	wantHash := sha256.Sum256([]byte(tok))
	if !bytes.Equal(got.TokenHash, wantHash[:]) {
		t.Error("recorded token_hash is not the SHA-256 of the issued token")
	}
	if got.IssuedAt.IsZero() || got.ExpiresAt.IsZero() {
		t.Errorf("recorded timestamps are zero: issued=%v exp=%v", got.IssuedAt, got.ExpiresAt)
	}
}

// spec: 13.3 (a record failure must block issuance — the token is
// never handed out if it could not be recorded)
func TestTokenServiceRefusesOnRecordFailure(t *testing.T) {
	rec := &fakeRecorder{err: errors.New("postgres unavailable")}
	ts, signer := newServerWithRecorder(t, rec)
	subject := mint(t, signer, jwt.Claims{
		Subject:  "alice@acme.com",
		TenantID: "acme",
		Typ:      auth.TokenUserBearer,
		Scope:    "sessions:read",
	})
	resp, body := exchange(t, ts, subject, tokenservice.Request{
		GrantType:        "urn:ietf:params:oauth:grant-type:token-exchange",
		SubjectToken:     subject,
		SubjectTokenType: "urn:ietf:params:oauth:token-type:jwt",
		Scope:            "sessions:read",
		Audience:         "lenny-gateway",
	})
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("record failure: want 500, got %d", resp.StatusCode)
	}
	if body["access_token"] != nil {
		t.Error("a token was returned despite the record failure")
	}
}
