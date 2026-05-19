// SPDX-License-Identifier: MIT

package jwt

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// spec: 10.3
// diagnosis: an HMAC signer's PublicJWK emitted private key material
// or named the wrong algorithm. The §10.3 publication surface must
// expose only kid/alg/use/kty for symmetric keys; the shared secret
// must never leave the gateway process.
func TestHMACSignerPublicJWKHasNoSecret(t *testing.T) {
	t.Parallel()
	s := NewHMACSigner("kid-1", []byte("secret-shared-with-no-one"))
	jwk := s.PublicJWK()

	if jwk.Kty != "oct" {
		t.Errorf("kty = %q, want oct", jwk.Kty)
	}
	if jwk.Use != "sig" {
		t.Errorf("use = %q, want sig", jwk.Use)
	}
	if jwk.Alg != "HS256" {
		t.Errorf("alg = %q, want HS256", jwk.Alg)
	}
	if jwk.Kid != "kid-1" {
		t.Errorf("kid = %q, want kid-1", jwk.Kid)
	}

	body, err := json.Marshal(jwk)
	if err != nil {
		t.Fatalf("marshal JWK: %v", err)
	}
	if strings.Contains(string(body), "secret-shared-with-no-one") {
		t.Fatalf("JWK marshalled with secret material: %s", body)
	}
	// "k" is the JWK field name for a symmetric secret (RFC 7518
	// §6.4). It must not appear.
	if strings.Contains(string(body), "\"k\"") {
		t.Fatalf("JWK marshalled with a `k` field: %s", body)
	}
}

// spec: 10.3
// diagnosis: the JWKS document did not include both the current and
// every retained previous key during the §10.3 overlap window. A
// client caching the JWKS must be able to verify a token signed
// under either key while the rotated key is in its overlap window.
func TestJWKSHandlerIncludesCurrentAndPreviousDuringOverlap(t *testing.T) {
	t.Parallel()
	oldKey := NewHMACSigner("kid-1", []byte("secret-1"))
	newKey := NewHMACSigner("kid-2", []byte("secret-2"))
	clock := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	v := withClock(NewRotatingVerifier(oldKey, time.Hour), func() time.Time { return clock })
	if err := v.Rotate(newKey); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	h := NewJWKSHandler(v)
	doc := h.Document()

	if len(doc.Keys) != 2 {
		t.Fatalf("JWKS Keys = %d, want 2 during overlap window", len(doc.Keys))
	}
	// Current always leads.
	if doc.Keys[0].Kid != "kid-2" {
		t.Errorf("Keys[0].kid = %q, want kid-2 (current)", doc.Keys[0].Kid)
	}
	if doc.Keys[1].Kid != "kid-1" {
		t.Errorf("Keys[1].kid = %q, want kid-1 (previous)", doc.Keys[1].Kid)
	}
}

// spec: 10.3
// diagnosis: after RetireExpired pruned every closed-window previous
// key, the JWKS document still advertised them. A retired key whose
// overlap window has closed MUST disappear from the published set so
// a client caching the document never accepts a token signed under a
// fully retired key.
func TestJWKSHandlerDropsPreviousKeyAfterRetireExpired(t *testing.T) {
	t.Parallel()
	oldKey := NewHMACSigner("kid-1", []byte("secret-1"))
	newKey := NewHMACSigner("kid-2", []byte("secret-2"))
	clock := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	v := withClock(NewRotatingVerifier(oldKey, time.Hour), func() time.Time { return clock })
	if err := v.Rotate(newKey); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	h := NewJWKSHandler(v)

	// Inside the overlap window: both keys present.
	if len(h.Document().Keys) != 2 {
		t.Fatalf("during overlap: %d keys, want 2", len(h.Document().Keys))
	}

	// Step past the overlap window and prune.
	clock = clock.Add(time.Hour)
	if removed := v.RetireExpired(); removed != 1 {
		t.Fatalf("RetireExpired removed %d, want 1", removed)
	}
	doc := h.Document()
	if len(doc.Keys) != 1 {
		t.Fatalf("after RetireExpired: %d keys, want 1", len(doc.Keys))
	}
	if doc.Keys[0].Kid != "kid-2" {
		t.Errorf("post-retire kid = %q, want kid-2", doc.Keys[0].Kid)
	}
}

// spec: 10.3
// diagnosis: ServeHTTP returned the wrong status, content type, or
// document body. GET /.well-known/jwks.json must return 200 with
// `application/jwk-set+json` and a JSON document carrying the live
// key set; HEAD must echo the headers without a body; other methods
// must return 405.
func TestJWKSHandlerServeHTTPContractsAndMethods(t *testing.T) {
	t.Parallel()
	cur := NewHMACSigner("kid-1", []byte("secret"))
	v := NewRotatingVerifier(cur, DefaultOverlapWindow)
	h := NewJWKSHandler(v)

	t.Run("GET returns JWKS document", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET status = %d, want 200", w.Code)
		}
		if got := w.Header().Get("Content-Type"); got != "application/jwk-set+json" {
			t.Errorf("Content-Type = %q, want application/jwk-set+json", got)
		}
		var doc JWKSet
		if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
			t.Fatalf("unmarshal JWKS: %v; body=%s", err, w.Body.String())
		}
		if len(doc.Keys) != 1 || doc.Keys[0].Kid != "kid-1" {
			t.Errorf("GET document Keys = %+v, want [kid-1]", doc.Keys)
		}
	})

	t.Run("HEAD echoes headers with no body", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodHead, "/.well-known/jwks.json", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("HEAD status = %d, want 200", w.Code)
		}
		if w.Body.Len() != 0 {
			t.Errorf("HEAD body length = %d, want 0", w.Body.Len())
		}
	})

	t.Run("POST returns 405", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/.well-known/jwks.json", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST status = %d, want 405", w.Code)
		}
		if got := w.Header().Get("Allow"); !strings.Contains(got, "GET") {
			t.Errorf("Allow = %q, want it to include GET", got)
		}
	})
}

// spec: 10.3
// diagnosis: NewJWKSHandler accepted a nil source so the handler
// would panic on the first request rather than at wiring time. The
// constructor must reject a nil JWKSource so the gateway fails fast
// when the publication surface is misconfigured.
func TestNewJWKSHandlerRejectsNilSource(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Errorf("NewJWKSHandler(nil) did not panic")
		}
	}()
	_ = NewJWKSHandler(nil)
}
