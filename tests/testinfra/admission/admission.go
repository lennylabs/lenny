// SPDX-License-Identifier: MIT

// Package admission is the §13.1 / §17.2 fail-closed admission-
// webhook fixture. Tests register a fake validating or mutating
// webhook handler against envtest's API server and assert pod
// admission behavior under three scenarios:
//
//   - happy path: webhook returns allowed=true → pod created
//   - hard reject: webhook returns allowed=false → pod rejected
//   - webhook outage: webhook server unavailable → failurePolicy:Fail
//     rejects, failurePolicy:Ignore admits
//
// Pairs with tests/testinfra/envtest for the API server lifecycle.
// The helper does not impose any specific policy — callers supply
// the HandleFunc that implements the §13.1 webhook semantics
// (lenny-label-immutability, lenny-direct-mode-isolation,
// lenny-t4-node-isolation) so each webhook test stays focused on
// its own invariant.
//
// Usage:
//
//	w := admission.NewServer(t, admission.HandleFunc(func(req *admission.Request) admission.Response {
//	    // inspect req.Object, return Response{Allowed: ...}
//	}))
//	defer w.Close()
//	// Register the webhook via the API server's
//	// validatingwebhookconfiguration with w.URL().
//
// # Skip behavior
//
// SkipUnlessAvailable currently always returns; the helper is
// pure Go and has no external dependencies beyond standard
// library. The function exists so the convention (every helper
// has a SkipUnlessAvailable) is preserved.
package admission

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// SkipUnlessAvailable is a no-op today; included for API
// symmetry with the other testinfra helpers.
func SkipUnlessAvailable(_ testing.TB) {}

// Request mirrors the relevant subset of
// admissionv1.AdmissionRequest. The helper does not depend on
// k8s.io/api to keep the testinfra surface small; callers that
// want the full type can cast Object to the corresponding struct.
type Request struct {
	UID       string
	Kind      string
	Resource  string
	Namespace string
	Name      string
	Operation string
	UserInfo  map[string]string
	Object    json.RawMessage
	OldObject json.RawMessage
}

// Response mirrors admissionv1.AdmissionResponse.
type Response struct {
	Allowed bool
	Code    int32 // status code on deny; 0 → 403
	Message string
	Reason  string
	Patch   []byte // optional JSONPatch for mutating webhooks
}

// HandleFunc is the per-webhook policy implementation.
type HandleFunc func(req *Request) Response

// Server is a running fake admission webhook. Cleanup is
// registered via t.Cleanup; the server stops when the test ends.
type Server struct {
	srv  *httptest.Server
	mu   sync.Mutex
	hits int
	last *Request
}

// NewServer starts a fake admission webhook on a random port,
// returns the *Server, and registers cleanup. The fn parameter
// is invoked once per admission review.
func NewServer(t testing.TB, fn HandleFunc) *Server {
	t.Helper()
	s := &Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("/admission", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits++
		s.mu.Unlock()
		var review struct {
			Request *Request `json:"request"`
		}
		if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
			http.Error(w, "invalid review: "+err.Error(), http.StatusBadRequest)
			return
		}
		req := review.Request
		if req == nil {
			http.Error(w, "review.request missing", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.last = req
		s.mu.Unlock()
		resp := fn(req)
		// Synthesize an AdmissionResponse envelope. The shape
		// matches admissionv1 closely enough that real cluster
		// callers parse it via the same JSON unmarshal.
		envelope := map[string]any{
			"apiVersion": "admission.k8s.io/v1",
			"kind":       "AdmissionReview",
			"response": map[string]any{
				"uid":     req.UID,
				"allowed": resp.Allowed,
			},
		}
		respMap := envelope["response"].(map[string]any)
		if !resp.Allowed {
			status := map[string]any{
				"code":    int32(403),
				"message": resp.Message,
				"reason":  resp.Reason,
			}
			if resp.Code != 0 {
				status["code"] = resp.Code
			}
			respMap["status"] = status
		}
		if len(resp.Patch) > 0 {
			respMap["patchType"] = "JSONPatch"
			respMap["patch"] = resp.Patch
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(envelope)
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

// URL returns the base URL of the running server (e.g.,
// http://127.0.0.1:PORT/admission).
func (s *Server) URL() string { return s.srv.URL + "/admission" }

// Close shuts the server down. Idempotent.
func (s *Server) Close() { s.srv.Close() }

// Hits returns the number of admission reviews this server has
// processed since startup. Useful for asserting the webhook was
// actually invoked.
func (s *Server) Hits() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits
}

// LastRequest returns the most recent Request the server
// processed, or nil if no review has been received yet.
func (s *Server) LastRequest() *Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		return nil
	}
	r := *s.last
	return &r
}

// NewUID generates a 16-byte hex UID for a synthesized admission
// review. The k8s API server normally fills this in; tests that
// post directly to the webhook need their own.
func NewUID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// Allow is a convenience for callers that always allow.
func Allow() Response { return Response{Allowed: true} }

// Deny is a convenience for callers that always reject with the
// given reason string.
func Deny(reason, msg string) Response {
	return Response{Allowed: false, Reason: reason, Message: msg, Code: 403}
}

// MarshalReview produces a valid admissionv1 AdmissionReview body
// suitable for posting at Server.URL() in tests that drive the
// server directly. Callers normally don't need this; envtest
// wires the API server to the webhook automatically.
func MarshalReview(req *Request) ([]byte, error) {
	envelope := map[string]any{
		"apiVersion": "admission.k8s.io/v1",
		"kind":       "AdmissionReview",
		"request":    req,
	}
	out, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("admission.MarshalReview: %w", err)
	}
	return out, nil
}
