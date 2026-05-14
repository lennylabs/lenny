// SPDX-License-Identifier: MIT

// Package siem is a SIEM endpoint stub. The audit pipeline forwards
// OCSF-translated events to a SIEM (Splunk HEC, Sumo, etc.); this
// stub stands in as the receiving endpoint for tier-2/4 tests.
//
// The stub records every inbound event so a test can assert the
// forwarder produced the expected envelope. It supports HMAC-SHA256
// signature validation (the forwarder signs each batch); the
// optional CheckSignature(secret) helper enforces it.
//
// Usage:
//
//	siem := siem.New(t)
//	t.Setenv("LENNY_SIEM_URL", siem.URL())
//	// ... drive the gateway ...
//	wait.For(t, 5*time.Second, "siem batch arrival",
//	    func() (bool, error) { return len(siem.Batches()) > 0, nil })
package siem

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Stub is the SIEM stand-in.
type Stub struct {
	server *httptest.Server

	mu      sync.Mutex
	batches []Batch
	secret  string
}

// Batch is one received batch.
type Batch struct {
	ReceivedAt time.Time
	Header     http.Header
	Body       []byte
	Events     []map[string]any // pre-parsed convenience
}

// New starts the stub and registers a t.Cleanup that closes it.
func New(t testing.TB) *Stub {
	t.Helper()
	s := &Stub{}
	s.server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.server.Close)
	return s
}

// URL returns the stub's base URL.
func (s *Stub) URL() string { return s.server.URL }

// CheckSignature enables HMAC verification using the given shared
// secret. Without this call the stub accepts every request.
func (s *Stub) CheckSignature(secret string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.secret = secret
}

// Batches returns a snapshot of every recorded batch in order.
func (s *Stub) Batches() []Batch {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Batch, len(s.batches))
	copy(out, s.batches)
	return out
}

// Events flattens every batch into a single event slice.
func (s *Stub) Events() []map[string]any {
	out := []map[string]any{}
	for _, b := range s.Batches() {
		out = append(out, b.Events...)
	}
	return out
}

// Reset clears recorded batches.
func (s *Stub) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches = nil
}

func (s *Stub) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()

	s.mu.Lock()
	secret := s.secret
	s.mu.Unlock()

	if secret != "" {
		sig := r.Header.Get("X-Lenny-SIEM-Signature")
		if sig == "" {
			http.Error(w, "missing signature", http.StatusUnauthorized)
			return
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		want := hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(sig), []byte(want)) {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
	}

	events := []map[string]any{}
	_ = json.Unmarshal(body, &events) // best-effort; payload may be jsonl

	s.mu.Lock()
	s.batches = append(s.batches, Batch{
		ReceivedAt: time.Now(),
		Header:     r.Header.Clone(),
		Body:       body,
		Events:     events,
	})
	s.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}
