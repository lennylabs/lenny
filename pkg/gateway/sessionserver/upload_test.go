// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// blobURL builds a /v1/blobs/{ref} URL by URL-encoding the
// lenny-blob:// URI into the path so the doubled slash in the
// scheme does not collide with http.ServeMux path normalisation.
func blobURL(ref string) string {
	return "/v1/blobs/" + url.PathEscape(ref)
}

// spec: §7.1 uploadToken, §4.5 blob URI, §15.1 upload + blobs.

func newUploadServer(t *testing.T) (*sessionserver.Server, *uploadtoken.Issuer, *uploadtoken.Verifier, blobstore.Store, sessionstore.Store, time.Time) {
	t.Helper()
	store := memstore.New()
	blobs := blobstore.NewMemoryStore(nil)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return t0 }
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("upload-secret")})
	issuer := uploadtoken.NewIssuer(ring, clock)
	tracker := uploadtoken.NewMemoryTracker()
	verifier := uploadtoken.NewVerifier(ring, tracker, clock)

	srv := sessionserver.New(store, sessionserver.Options{
		Clock:               clock,
		IDFunc:              func() string { return "sess_upload" },
		UploadTokenIssuer:   issuer,
		UploadTokenVerifier: verifier,
		Blobs:               blobs,
	})
	return srv, issuer, verifier, blobs, store, t0
}

func seedCreatedSession(t *testing.T, store sessionstore.Store, id, tenant string) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	row := sessionstore.Session{
		ID:        id,
		TenantID:  tenant,
		State:     session.StateCreated,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func uploadRequest(t *testing.T, h http.Handler, id, tenant, token string, body []byte, mime string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+id+"/upload", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	if token != "" {
		req.Header.Set("X-Lenny-Upload-Token", token)
	}
	if mime != "" {
		req.Header.Set("Content-Type", mime)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestUploadHappyPath(t *testing.T) {
	srv, issuer, _, blobs, store, _ := newUploadServer(t)
	seedCreatedSession(t, store, "sess_upload", "acme")
	tok, _ := issuer.Issue("sess_upload", 0)

	rr := uploadRequest(t, srv.Handler(), "sess_upload", "acme", tok, []byte("hello world"), "text/plain")
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	var resp sessionserver.UploadResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(resp.UploadRef, "lenny-blob://acme/sess_upload/") {
		t.Errorf("uploadRef: got %q", resp.UploadRef)
	}
	if resp.Size != 11 {
		t.Errorf("size: got %d, want 11", resp.Size)
	}

	// Blob should be retrievable from the store.
	uri, err := blobstore.ParseURI(resp.UploadRef)
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	info, _, err := blobs.Get(uri)
	if err != nil {
		t.Fatalf("Get blob: %v", err)
	}
	if info.MimeType != "text/plain" {
		t.Errorf("mimeType: got %q", info.MimeType)
	}
}

func TestUploadRejectsMissingToken(t *testing.T) {
	srv, _, _, _, store, _ := newUploadServer(t)
	seedCreatedSession(t, store, "sess_upload", "acme")
	rr := uploadRequest(t, srv.Handler(), "sess_upload", "acme", "", []byte("data"), "text/plain")
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rr.Code)
	}
}

func TestUploadRejectsInvalidToken(t *testing.T) {
	srv, _, _, _, store, _ := newUploadServer(t)
	seedCreatedSession(t, store, "sess_upload", "acme")
	rr := uploadRequest(t, srv.Handler(), "sess_upload", "acme", "not-a-token", []byte("data"), "text/plain")
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rr.Code)
	}
}

func TestUploadRejectsSessionMismatch(t *testing.T) {
	srv, issuer, _, _, store, _ := newUploadServer(t)
	seedCreatedSession(t, store, "sess_upload", "acme")
	// Token issued for a different session id.
	tok, _ := issuer.Issue("sess_other", 0)
	rr := uploadRequest(t, srv.Handler(), "sess_upload", "acme", tok, []byte("data"), "text/plain")
	if rr.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want 403", rr.Code)
	}
}

func TestUploadRejectsWrongState(t *testing.T) {
	srv, issuer, _, _, store, _ := newUploadServer(t)
	// Session is `ready` — not in upload's precondition table.
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_upload", TenantID: "acme", State: session.StateReady,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tok, _ := issuer.Issue("sess_upload", 0)
	rr := uploadRequest(t, srv.Handler(), "sess_upload", "acme", tok, []byte("data"), "text/plain")
	if rr.Code != http.StatusConflict {
		t.Errorf("status: got %d, want 409 (INVALID_STATE_TRANSITION); body=%s", rr.Code, rr.Body.String())
	}
}

func TestUploadRejectsMissingSession(t *testing.T) {
	srv, issuer, _, _, _, _ := newUploadServer(t)
	tok, _ := issuer.Issue("sess_upload", 0)
	rr := uploadRequest(t, srv.Handler(), "sess_upload", "acme", tok, []byte("data"), "text/plain")
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rr.Code)
	}
}

func TestUploadDefaultsMimeTypeWhenAbsent(t *testing.T) {
	srv, issuer, _, _, store, _ := newUploadServer(t)
	seedCreatedSession(t, store, "sess_upload", "acme")
	tok, _ := issuer.Issue("sess_upload", 0)
	rr := uploadRequest(t, srv.Handler(), "sess_upload", "acme", tok, []byte("data"), "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201", rr.Code)
	}
	var resp sessionserver.UploadResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.MimeType != "application/octet-stream" {
		t.Errorf("default mimeType: got %q", resp.MimeType)
	}
}

func TestUploadDisabledWhenBlobsUnavailable(t *testing.T) {
	store := memstore.New()
	seedCreatedSession(t, store, "sess_upload", "acme")
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc: func() string { return "sess_upload" },
	})
	rr := uploadRequest(t, srv.Handler(), "sess_upload", "acme", "ignored", []byte("data"), "text/plain")
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503 (BLOBSTORE_UNAVAILABLE)", rr.Code)
	}
}

func TestBlobGetHappyPath(t *testing.T) {
	srv, _, _, blobs, _, _ := newUploadServer(t)
	u := blobstore.URI{TenantID: "acme", SessionID: "sess_upload", PartID: "part_1", TTL: time.Hour}
	if _, err := blobs.Put(u, "text/plain", strings.NewReader("blob-bytes")); err != nil {
		t.Fatalf("seed blob: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, blobURL(u.String()), nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "text/plain" {
		t.Errorf("Content-Type: got %q", got)
	}
	bs, _ := io.ReadAll(rr.Body)
	if string(bs) != "blob-bytes" {
		t.Errorf("body: got %q", string(bs))
	}
}

func TestBlobGetRejectsCrossTenant(t *testing.T) {
	// Per §15.1: 403 when the caller's tenant_id does not match the
	// URI's tenant_id.
	srv, _, _, blobs, _, _ := newUploadServer(t)
	u := blobstore.URI{TenantID: "globex", SessionID: "sess_upload", PartID: "part_1", TTL: time.Hour}
	_, _ = blobs.Put(u, "text/plain", strings.NewReader("foreign"))

	req := httptest.NewRequest(http.MethodGet, blobURL(u.String()), nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want 403", rr.Code)
	}
}

func TestBlobGetReturns404ForUnknown(t *testing.T) {
	srv, _, _, _, _, _ := newUploadServer(t)
	u := blobstore.URI{TenantID: "acme", SessionID: "sess_upload", PartID: "part_missing", TTL: time.Hour}

	req := httptest.NewRequest(http.MethodGet, blobURL(u.String()), nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rr.Code)
	}
}

func TestBlobGetReturns400ForMalformedURI(t *testing.T) {
	srv, _, _, _, _, _ := newUploadServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/blobs/not-a-blob-uri", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
}

func TestUploadRoundTripsThroughBlobGet(t *testing.T) {
	// Upload a file, then GET it back via the blob endpoint.
	srv, issuer, _, _, store, _ := newUploadServer(t)
	seedCreatedSession(t, store, "sess_upload", "acme")
	tok, _ := issuer.Issue("sess_upload", 0)

	rr := uploadRequest(t, srv.Handler(), "sess_upload", "acme", tok, []byte("round-trip"), "text/plain")
	if rr.Code != http.StatusCreated {
		t.Fatalf("upload: status %d", rr.Code)
	}
	var up sessionserver.UploadResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &up)

	req := httptest.NewRequest(http.MethodGet, blobURL(up.UploadRef), nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr2, req)
	if rr2.Code != http.StatusOK {
		t.Fatalf("blob get: status %d, body=%s", rr2.Code, rr2.Body.String())
	}
	body, _ := io.ReadAll(rr2.Body)
	if string(body) != "round-trip" {
		t.Errorf("round-trip body: got %q", string(body))
	}
}
