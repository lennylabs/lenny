// SPDX-License-Identifier: MIT

package sessionserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// jsonDecodeRaw decodes a JSON body into into.
func jsonDecodeRaw(b []byte, into any) error { return json.Unmarshal(b, into) }

// F-7.4.7: the configured upload-token TTL is stamped on every minted
// token instead of falling through to uploadtoken.DefaultTTL. The
// gateway threads `maxCreatedStateTimeoutSeconds` here so the token
// expiry matches the watchdog's `created`-state deadline.
// spec: §7.1 line 58.
func TestCreateSessionUsesConfiguredUploadTokenTTL_spec_7_4_7(t *testing.T) {
	store := memstore.New()
	at := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return at }
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k", Secret: []byte("s")})
	issuer := uploadtoken.NewIssuer(ring, clock)
	const ttl = 600 * time.Second
	srv := New(store, Options{
		Clock:             clock,
		IDFunc:            func() string { return "sess_ttl" },
		UploadTokenIssuer: issuer,
		UploadTokenTTL:    ttl,
	})

	body := `{"runtimeRef":"claude-code"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	stored, err := store.Get(context.Background(), "default", "sess_ttl")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	wantExpiry := at.Add(ttl)
	if !stored.UploadTokenExpiry.Equal(wantExpiry) {
		t.Errorf("UploadTokenExpiry = %v, want %v (ttl=%v)", stored.UploadTokenExpiry, wantExpiry, ttl)
	}
}

// F-7.4.7: when UploadTokenTTL is unset (zero), the issuer falls back
// to uploadtoken.DefaultTTL (300s), matching the §7.1 default. The
// zero-value behavior is the v1 minimal-gateway default.
func TestCreateSessionDefaultUploadTokenTTL_spec_7_4_7(t *testing.T) {
	store := memstore.New()
	at := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return at }
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k", Secret: []byte("s")})
	issuer := uploadtoken.NewIssuer(ring, clock)
	srv := New(store, Options{
		Clock:             clock,
		IDFunc:            func() string { return "sess_def" },
		UploadTokenIssuer: issuer,
		// UploadTokenTTL omitted
	})

	body := `{"runtimeRef":"claude-code"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	stored, _ := store.Get(context.Background(), "default", "sess_def")
	wantExpiry := at.Add(uploadtoken.DefaultTTL)
	if !stored.UploadTokenExpiry.Equal(wantExpiry) {
		t.Errorf("UploadTokenExpiry = %v, want default %v", stored.UploadTokenExpiry, wantExpiry)
	}
}

// F-7.4.7: a token minted with a 1s TTL is rejected after 2s. The
// verifier surfaces ErrExpired which the upload handler maps to
// 401 UPLOAD_TOKEN_EXPIRED.
func TestUploadTokenExpiresPerConfiguredTTL_spec_7_4_7(t *testing.T) {
	store := memstore.New()
	blobs := blobstore.NewMemoryStore(nil)
	at := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	current := at
	clock := func() time.Time { return current }
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k", Secret: []byte("s")})
	issuer := uploadtoken.NewIssuer(ring, clock)
	tracker := uploadtoken.NewMemoryTracker()
	verifier := uploadtoken.NewVerifier(ring, tracker, clock)
	srv := New(store, Options{
		Clock:               clock,
		IDFunc:              func() string { return "sess_expiry" },
		UploadTokenIssuer:   issuer,
		UploadTokenVerifier: verifier,
		Blobs:               blobs,
		UploadTokenTTL:      1 * time.Second,
	})

	body := `{"runtimeRef":"claude-code"}`
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/sessions",
		strings.NewReader(body)).WithContext(context.Background()))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rr.Code, rr.Body.String())
	}
	// Pull the minted token by decoding the response.
	type wrap struct {
		UploadToken string `json:"uploadToken"`
	}
	var w wrap
	_ = decodeJSON(t, rr.Body.Bytes(), &w)

	// Advance clock past TTL.
	current = at.Add(2 * time.Second)

	uploadReq := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_expiry/upload", bytes.NewBufferString("x"))
	uploadReq.Header.Set("X-Lenny-Upload-Token", w.UploadToken)
	uploadReq.Header.Set("Content-Type", "text/plain")
	rr2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr2, uploadReq)
	if rr2.Code != http.StatusUnauthorized {
		t.Fatalf("expired upload status=%d, want %d, body=%s", rr2.Code, http.StatusUnauthorized, rr2.Body.String())
	}
	if !strings.Contains(rr2.Body.String(), "UPLOAD_TOKEN_EXPIRED") {
		t.Errorf("body does not name UPLOAD_TOKEN_EXPIRED: %s", rr2.Body.String())
	}
}

// F-7.4.9: MemoryTracker.Sweep drops expired digests but keeps live
// digests. The gateway's tick calls Sweep with the current clock so
// memory is bounded.
func TestUploadTokenTrackerSweep_spec_7_4_9(t *testing.T) {
	tr := uploadtoken.NewMemoryTracker()
	now := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	// Two consumed digests: one expires before `now+1m`, one after.
	if err := tr.MarkConsumed("d-expired", now.Add(-1*time.Minute)); err != nil {
		t.Fatalf("MarkConsumed expired: %v", err)
	}
	if err := tr.MarkConsumed("d-live", now.Add(1*time.Hour)); err != nil {
		t.Fatalf("MarkConsumed live: %v", err)
	}
	dropped := tr.Sweep(now)
	if dropped != 1 {
		t.Errorf("dropped = %d, want 1", dropped)
	}
	if tr.IsConsumed("d-expired") {
		t.Errorf("d-expired should have been swept")
	}
	if !tr.IsConsumed("d-live") {
		t.Errorf("d-live should still be tracked")
	}
}

// silence unused-import for state package.
var _ = session.StateCreated

// decodeJSON is a tiny JSON-decode helper used by tests in this file.
// Reads the bytes as JSON into `into`.
func decodeJSON(t *testing.T, b []byte, into any) error {
	t.Helper()
	return jsonDecodeRaw(b, into)
}
