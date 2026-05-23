// SPDX-License-Identifier: MIT

package loadctl

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRateLimitBlocksRunCreate(t *testing.T) {
	server, _ := NewServer(Config{
		StorageURL:  "file://" + t.TempDir(),
		RateLimit:   RateLimitConfig{RunCreatePerMinute: 2},
		RunDuration: 50 * 1_000_000, // 50ms
	})
	defer server.Close()
	srv := httptest.NewServer(server.Handler())
	defer srv.Close()

	post := func() int {
		resp, err := http.Post(srv.URL+"/api/v1/runs", "application/json",
			bytes.NewBufferString(`{"scale":"small"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// Burst of 2 allowed.
	if got := post(); got != http.StatusCreated {
		t.Errorf("req 1 status=%d want 201", got)
	}
	if got := post(); got != http.StatusCreated {
		t.Errorf("req 2 status=%d want 201", got)
	}
	// Third immediate request: 429.
	if got := post(); got != http.StatusTooManyRequests {
		t.Errorf("req 3 status=%d want 429", got)
	}
}

func TestRateLimitDisabledByDefault(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	// 30 rapid requests should all be admitted with no RateLimit set.
	for i := 0; i < 30; i++ {
		resp, _ := http.Post(srv.URL+"/api/v1/runs", "application/json",
			bytes.NewBufferString(`{"scale":"small"}`))
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Errorf("req %d hit rate limit; should be unlimited by default", i+1)
		}
		resp.Body.Close()
	}
}
