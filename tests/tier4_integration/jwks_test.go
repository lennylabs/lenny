// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §10.3 JWKS publication endpoint served
// by the real cmd/lenny-gateway binary. The test boots the gateway,
// reads the published JWK Set, and asserts that the document carries
// the current signing key with no private material. It also exercises
// the §10.3 RotatingVerifier surface through an in-process driver to
// confirm that during the overlap window both keys publish, and that
// once RetireExpired prunes the previous key the JWKS endpoint stops
// advertising it.

package tier4_integration_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// spec: 10.3
// diagnosis: the live cmd/lenny-gateway binary did not publish a
// well-formed JWK Set at /.well-known/jwks.json. The §10.3 endpoint
// must return application/jwk-set+json with at least the current key
// and must never serialize private key material onto the wire.
func TestJWKSEndpointPublishedByLiveGateway(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	// §10.3 / F-10.2.14: JWKS publication defaults off because the v1
	// HMAC backend cannot publish usable public-key material. The
	// endpoint mounts only under --jwks-publish=true, so the test opts in
	// explicitly to exercise the published JWK Set.
	gw := gateway.StartWith(t, "--dev-mode", "--jwks-publish=true")
	resp, err := http.Get(gw.BaseURL() + "/.well-known/jwks.json")
	if err != nil {
		t.Fatalf("GET /.well-known/jwks.json: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("JWKS status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/jwk-set+json" {
		t.Errorf("Content-Type = %q, want application/jwk-set+json", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if strings.Contains(string(body), "\"k\"") {
		t.Errorf("JWKS document leaked symmetric secret (`k` field present): %s", body)
	}
	var doc jwt.JWKSet
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("unmarshal JWKS: %v; body=%s", err, body)
	}
	if len(doc.Keys) < 1 {
		t.Fatalf("JWKS has %d keys, want >= 1", len(doc.Keys))
	}
	cur := doc.Keys[0]
	if cur.Kty == "" {
		t.Errorf("current JWK has empty kty: %+v", cur)
	}
	if cur.Kid == "" {
		t.Errorf("current JWK has empty kid: %+v", cur)
	}
	if cur.Use != "" && cur.Use != "sig" {
		t.Errorf("current JWK use = %q, want sig", cur.Use)
	}
}

// spec: 10.3
// diagnosis: a RotatingVerifier exposed via the JWKS handler did not
// publish both the current and the previous key during the §10.3
// overlap window, or did not drop the previous key after
// RetireExpired pruned it. The end-to-end §10.3 contract requires
// that a client caching the JWKS document during the overlap window
// continues to verify tokens minted under the now-previous key, and
// stops once the overlap window closes and the operator retires the
// key.
func TestJWKSEndpointReflectsRotationOverlapWindow(t *testing.T) {
	t.Parallel()
	oldKey := jwt.NewHMACSigner("kid-overlap-1", []byte("secret-1"))
	newKey := jwt.NewHMACSigner("kid-overlap-2", []byte("secret-2"))
	v := jwt.NewRotatingVerifier(oldKey, time.Hour)
	h := jwt.NewJWKSHandler(v)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	// Before any rotation, the JWKS publishes only the current key.
	doc := fetchJWKSDoc(t, srv.URL)
	if len(doc.Keys) != 1 {
		t.Fatalf("pre-rotation JWKS Keys = %d, want 1", len(doc.Keys))
	}
	if doc.Keys[0].Kid != "kid-overlap-1" {
		t.Errorf("pre-rotation current kid = %q, want kid-overlap-1", doc.Keys[0].Kid)
	}

	// After Rotate, both keys are published during the overlap window.
	if err := v.Rotate(newKey); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	doc = fetchJWKSDoc(t, srv.URL)
	if len(doc.Keys) != 2 {
		t.Fatalf("during overlap: JWKS Keys = %d, want 2", len(doc.Keys))
	}
	kids := map[string]bool{}
	for _, k := range doc.Keys {
		kids[k.Kid] = true
		if strings.Contains(asJSON(t, k), "\"k\"") {
			t.Errorf("JWK leaked secret material: %+v", k)
		}
	}
	if !kids["kid-overlap-1"] || !kids["kid-overlap-2"] {
		t.Errorf("during overlap: kids = %v, want both kid-overlap-1 and kid-overlap-2", kids)
	}
	// Current key always leads.
	if doc.Keys[0].Kid != "kid-overlap-2" {
		t.Errorf("Keys[0].kid = %q, want kid-overlap-2 (current)", doc.Keys[0].Kid)
	}

	// After RetireExpired prunes the previous key, JWKS drops it.
	// The §10.3 overlap window is 1h in this test; sliding the
	// underlying clock past it without exposing the verifier's
	// internal `now` would require waiting on real time. Instead the
	// test exercises the publication surface against a verifier
	// whose previous-key set is artificially emptied via the same
	// RetireExpired path (the test's overlap window is 1h, so we
	// must construct the verifier such that the previous key is
	// already past its window). The simplest model: rebuild the
	// verifier with a near-zero overlap so the next RetireExpired
	// removes the key.
	vFast := jwt.NewRotatingVerifier(
		jwt.NewHMACSigner("kid-fast-1", []byte("s1")),
		time.Nanosecond,
	)
	hFast := jwt.NewJWKSHandler(vFast)
	srvFast := httptest.NewServer(hFast)
	t.Cleanup(srvFast.Close)
	if err := vFast.Rotate(jwt.NewHMACSigner("kid-fast-2", []byte("s2"))); err != nil {
		t.Fatalf("Rotate fast: %v", err)
	}
	// The previous key's overlap window of 1ns closes immediately;
	// RetireExpired sweeps it from the set.
	time.Sleep(time.Millisecond)
	if removed := vFast.RetireExpired(); removed != 1 {
		t.Fatalf("RetireExpired removed %d, want 1", removed)
	}
	docFast := fetchJWKSDoc(t, srvFast.URL)
	if len(docFast.Keys) != 1 {
		t.Fatalf("after RetireExpired: JWKS Keys = %d, want 1", len(docFast.Keys))
	}
	if docFast.Keys[0].Kid != "kid-fast-2" {
		t.Errorf("post-retire kid = %q, want kid-fast-2", docFast.Keys[0].Kid)
	}
}

// fetchJWKSDoc issues a GET against url and unmarshals the response
// into a jwt.JWKSet. Fails the test on any HTTP, IO, or parse error.
func fetchJWKSDoc(t *testing.T, url string) jwt.JWKSet {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var doc jwt.JWKSet
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, body)
	}
	return doc
}

// asJSON marshals v to JSON text for substring assertions.
func asJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
