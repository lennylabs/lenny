// SPDX-License-Identifier: MIT

package gateway_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/gateway"
)

// recordingTLSMetrics counts handshake results by label.
type recordingTLSMetrics struct {
	mu     sync.Mutex
	counts map[string]int
}

func (r *recordingTLSMetrics) Handshake(result string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.counts == nil {
		r.counts = map[string]int{}
	}
	r.counts[result]++
}

func (r *recordingTLSMetrics) count(result string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[result]
}

// TestClient_HandshakeMetric_Plaintext covers §25.4 line 2544: an http://
// admin-API request records a "plaintext" handshake result, the signal
// the OpsAdminAPIPlaintextDetected alert fires on.
func TestClient_HandshakeMetric_Plaintext_spec_25_4(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	m := &recordingTLSMetrics{}
	c, err := gateway.NewClient(gateway.Config{BaseURL: srv.URL, TLSMetrics: m})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	var out map[string]any
	if err := c.Get(context.Background(), "/x", &out); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m.count("plaintext") != 1 {
		t.Fatalf("plaintext handshakes = %d, want 1", m.count("plaintext"))
	}
}

// TestClient_HandshakeMetric_TLS covers an https:// request recording a
// "tls" result on success.
func TestClient_HandshakeMetric_TLS_spec_25_4(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	m := &recordingTLSMetrics{}
	c, err := gateway.NewClient(gateway.Config{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		TLSMetrics: m,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	var out map[string]any
	if err := c.Get(context.Background(), "/x", &out); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m.count("tls") != 1 {
		t.Fatalf("tls handshakes = %d, want 1", m.count("tls"))
	}
	if m.count("plaintext") != 0 {
		t.Fatalf("plaintext handshakes = %d, want 0", m.count("plaintext"))
	}
}

// TestClient_HandshakeMetric_TLSError covers an https:// transport
// failure recording a "tls_error" result.
func TestClient_HandshakeMetric_TLSError_spec_25_4(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // make the endpoint unreachable so the dial fails
	m := &recordingTLSMetrics{}
	c, err := gateway.NewClient(gateway.Config{BaseURL: url, TLSMetrics: m})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	var out map[string]any
	if err := c.Get(context.Background(), "/x", &out); err == nil {
		t.Fatal("expected a transport error against a closed server")
	}
	if m.count("tls_error") != 1 {
		t.Fatalf("tls_error handshakes = %d, want 1", m.count("tls_error"))
	}
}

// revokerToken is a TokenSource that records MarkRevoked calls.
type revokerToken struct{ revoked atomic.Int32 }

func (r *revokerToken) Token(context.Context) (string, error) { return "tok", nil }
func (r *revokerToken) MarkRevoked()                          { r.revoked.Add(1) }

// TestClient_401MarksRevoked covers the §25.4 revocation-detection path:
// a 401 from the gateway flags the token source for reload.
func TestClient_401MarksRevoked_spec_25_4(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	tok := &revokerToken{}
	c, err := gateway.NewClient(gateway.Config{BaseURL: srv.URL, Token: tok})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	var out map[string]any
	if err := c.Get(context.Background(), "/x", &out); err == nil {
		t.Fatal("expected an HTTP 401 error")
	}
	if tok.revoked.Load() != 1 {
		t.Fatalf("MarkRevoked calls = %d, want 1", tok.revoked.Load())
	}
}

// TestClient_FanOutSkipsOpenBreaker covers §25.4 "Fallback Caching": a
// replica whose circuit breaker is open is skipped (ErrCircuitOpen) and
// not dialed, while a healthy replica is still queried.
func TestClient_FanOutSkipsOpenBreaker_spec_25_4(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	const dead = "http://127.0.0.1:1/" // the endpoint we trip the breaker for
	breaker := gateway.NewCircuitBreaker(3, time.Minute)
	for i := 0; i < 3; i++ {
		breaker.RecordFailure(dead)
	}
	c, err := gateway.NewClient(gateway.Config{
		BaseURL:   srv.URL,
		Discovery: gateway.StaticDiscovery{srv.URL, dead},
		Breaker:   breaker,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	results, err := c.FanOutGet(context.Background(), "/v1/admin/health")
	if err != nil {
		t.Fatalf("FanOutGet: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	var sawOpen, sawOK bool
	for _, r := range results {
		switch r.Endpoint {
		case dead:
			if !errors.Is(r.Err, gateway.ErrCircuitOpen) {
				t.Fatalf("dead endpoint err = %v, want ErrCircuitOpen", r.Err)
			}
			sawOpen = true
		case srv.URL:
			if r.Err != nil {
				t.Fatalf("healthy endpoint err = %v, want nil", r.Err)
			}
			sawOK = true
		}
	}
	if !sawOpen || !sawOK {
		t.Fatalf("missing results: sawOpen=%v sawOK=%v", sawOpen, sawOK)
	}
	if hits.Load() != 1 {
		t.Fatalf("server hits = %d, want 1 (the open-breaker endpoint must not be dialed)", hits.Load())
	}
}
