// SPDX-License-Identifier: MIT

package loadctl

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthDisabledByDefault(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	// No Authorization header — should pass through because no
	// tokens are configured.
	resp, _ := http.Post(srv.URL+"/api/v1/runs", "application/json",
		bytes.NewBufferString(`{"scale":"small"}`))
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("status=%d want 201 (auth should be disabled when tokens empty)", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAuthOperatorTokenRequired(t *testing.T) {
	server, _ := NewServer(Config{
		StorageURL: "file://" + t.TempDir(),
		Auth:       AuthConfig{OperatorTokens: []string{"op-token"}},
	})
	defer server.Close()
	srv := httptest.NewServer(server.Handler())
	defer srv.Close()

	// Without a token: 401.
	resp, _ := http.Post(srv.URL+"/api/v1/runs", "application/json",
		bytes.NewBufferString(`{"scale":"small"}`))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("missing token status=%d want 401", resp.StatusCode)
	}
	resp.Body.Close()

	// With the wrong token: 403.
	req, _ := http.NewRequest("POST", srv.URL+"/api/v1/runs",
		bytes.NewBufferString(`{"scale":"small"}`))
	req.Header.Set("Authorization", "Bearer wrong")
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("wrong token status=%d want 403", resp.StatusCode)
	}
	resp.Body.Close()

	// With the right token: 201.
	req, _ = http.NewRequest("POST", srv.URL+"/api/v1/runs",
		bytes.NewBufferString(`{"scale":"small"}`))
	req.Header.Set("Authorization", "Bearer op-token")
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("good token status=%d want 201", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAuthScopeSeparation(t *testing.T) {
	server, _ := NewServer(Config{
		StorageURL: "file://" + t.TempDir(),
		Auth: AuthConfig{
			OperatorTokens: []string{"op-token"},
			RunnerTokens:   []string{"runner-token"},
		},
	})
	defer server.Close()
	srv := httptest.NewServer(server.Handler())
	defer srv.Close()

	// Operator token cannot post to /api/v1/ack (runner scope).
	req, _ := http.NewRequest("POST", srv.URL+"/api/v1/ack",
		bytes.NewBufferString(`{"run_id":"x","scenario":"y","outcome":"PASS"}`))
	req.Header.Set("Authorization", "Bearer op-token")
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("op-token on runner scope status=%d want 403", resp.StatusCode)
	}
	resp.Body.Close()

	// Runner token cannot create runs (operator scope).
	req, _ = http.NewRequest("POST", srv.URL+"/api/v1/runs",
		bytes.NewBufferString(`{"scale":"small"}`))
	req.Header.Set("Authorization", "Bearer runner-token")
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("runner-token on operator scope status=%d want 403", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAuthPublicEndpoints(t *testing.T) {
	server, _ := NewServer(Config{
		StorageURL: "file://" + t.TempDir(),
		Auth:       AuthConfig{OperatorTokens: []string{"op-token"}},
	})
	defer server.Close()
	srv := httptest.NewServer(server.Handler())
	defer srv.Close()

	for _, path := range []string{"/healthz", "/"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if resp.StatusCode >= 400 {
			t.Errorf("%s status=%d, expected unauthenticated access", path, resp.StatusCode)
		}
		resp.Body.Close()
	}
}
