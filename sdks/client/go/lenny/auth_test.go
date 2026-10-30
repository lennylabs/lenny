// SPDX-License-Identifier: MIT

package lenny

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestBearerTokenHeader confirms BearerToken sets the Authorization
// header.
func TestBearerTokenHeader(t *testing.T) {
	var seen string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "sess_1", "state": "created"})
	}))
	defer ts.Close()

	c, err := New(ts.URL, WithAuth(BearerToken("tok-abc")))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.GetSession(context.Background(), "sess_1"); err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if seen != "Bearer tok-abc" {
		t.Errorf("Authorization header: want %q, got %q", "Bearer tok-abc", seen)
	}
}

// TestAPIKeyHeader confirms APIKey sets the X-Lenny-API-Key header.
func TestAPIKeyHeader(t *testing.T) {
	var seen string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("X-Lenny-API-Key")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "sess_1", "state": "created"})
	}))
	defer ts.Close()

	c, err := New(ts.URL, WithAuth(APIKey("key-xyz")))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.GetSession(context.Background(), "sess_1"); err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if seen != "key-xyz" {
		t.Errorf("X-Lenny-API-Key header: want key-xyz, got %q", seen)
	}
}

// staticTokenSource is a TokenSource returning a fixed token.
type staticTokenSource struct {
	token string
	err   error
}

func (s staticTokenSource) Token() (string, error) { return s.token, s.err }

// TestRefreshingTokenUsesSource confirms RefreshingToken fetches the
// token from its source for each request.
func TestRefreshingTokenUsesSource(t *testing.T) {
	var seen string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "sess_1", "state": "created"})
	}))
	defer ts.Close()

	c, err := New(ts.URL, WithAuth(RefreshingToken(staticTokenSource{token: "fresh-tok"})))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.GetSession(context.Background(), "sess_1"); err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if seen != "Bearer fresh-tok" {
		t.Errorf("Authorization header: want %q, got %q", "Bearer fresh-tok", seen)
	}
}

// TestRefreshingTokenSourceErrorAborts confirms a token-source error
// aborts the request.
func TestRefreshingTokenSourceErrorAborts(t *testing.T) {
	c, err := New("https://gateway.example",
		WithAuth(RefreshingToken(staticTokenSource{err: errors.New("refresh failed")})))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.GetSession(context.Background(), "sess_1"); err == nil {
		t.Error("GetSession: want error when the token source fails")
	}
}

// TestNewRejectsInvalidBaseURL confirms New rejects an empty or
// relative base URL.
func TestNewRejectsInvalidBaseURL(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Error("New(\"\"): want error")
	}
	if _, err := New("/v1/sessions"); err == nil {
		t.Error("New(relative): want error")
	}
}
