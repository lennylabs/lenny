// SPDX-License-Identifier: MIT

package tokensvcproxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// spec: §4.3 line 193 / F-4.3.12 — the gateway reverse-proxies
// /v1/oauth/* to the Token Service unchanged; the Token Service sees
// the original method, path, body, and headers.
func TestProxyForwardsRequest(t *testing.T) {
	var sawPath, sawMethod, sawHost string
	var sawBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		sawPath = r.URL.Path
		sawHost = r.Host
		b, _ := io.ReadAll(r.Body)
		sawBody = b
		w.Header().Set("X-Origin", "token-service")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"tok"}`))
	}))
	defer upstream.Close()

	proxy, err := New(upstream.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/v1/oauth/", proxy.Handler())
	gw := httptest.NewServer(mux)
	defer gw.Close()

	resp, err := http.Post(gw.URL+"/v1/oauth/token",
		"application/json", strings.NewReader(`{"grant_type":"x"}`))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Origin"); got != "token-service" {
		t.Errorf("X-Origin=%q, want token-service", got)
	}
	if sawPath != "/v1/oauth/token" {
		t.Errorf("upstream path=%q, want /v1/oauth/token", sawPath)
	}
	if sawMethod != http.MethodPost {
		t.Errorf("upstream method=%q, want POST", sawMethod)
	}
	if string(sawBody) != `{"grant_type":"x"}` {
		t.Errorf("upstream body=%q, want forwarded body", string(sawBody))
	}
	if sawHost == "" {
		t.Errorf("upstream Host header empty")
	}
}

// spec: F-4.3.12 — an invalid upstream URL fails fast so the gateway
// refuses to start with a misconfigured Token Service address.
func TestNewRejectsInvalidURL(t *testing.T) {
	for _, badURL := range []string{
		"",
		"no-scheme.example.com",
		"://broken",
	} {
		if _, err := New(badURL); err == nil {
			t.Errorf("New(%q) returned nil error", badURL)
		}
	}
}
