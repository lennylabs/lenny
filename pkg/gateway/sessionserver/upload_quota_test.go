// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storagequota"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// spec: §11.2 per-tenant storage quota, upload-handler enforcement.

// newQuotaUploadServer builds an upload server whose acme tenant has
// the given storage quota (zero means unlimited), seeded with a
// created session sess_upload.
func newQuotaUploadServer(t *testing.T, quotaBytes int64) (*sessionserver.Server, *uploadtoken.Issuer, *storagequota.Memory) {
	t.Helper()
	store := memstore.New()
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("upload-secret")})
	issuer := uploadtoken.NewIssuer(ring, clock)
	verifier := uploadtoken.NewVerifier(ring, uploadtoken.NewMemoryTracker(), clock)
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(context.Background(), tenantstore.Tenant{
		ID: "acme", StorageQuotaBytes: quotaBytes,
	}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	counter := storagequota.NewMemory()
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:               clock,
		IDFunc:              func() string { return "sess_upload" },
		UploadTokenIssuer:   issuer,
		UploadTokenVerifier: verifier,
		Blobs:               blobstore.NewMemoryStore(nil),
		Tenants:             tenants,
		StorageQuota:        counter,
	})
	seedCreatedSession(t, store, "sess_upload", "acme")
	return srv, issuer, counter
}

// uploadWithContentLength drives an upload whose declared
// Content-Length is set independently of the body so a client that
// under-declares its size can be simulated.
func uploadWithContentLength(t *testing.T, h http.Handler, token string, body []byte, contentLength int64) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_upload/upload", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("X-Lenny-Upload-Token", token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = contentLength
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestUploadUnderStorageQuota(t *testing.T) {
	srv, issuer, counter := newQuotaUploadServer(t, 1000)
	tok, _ := issuer.Issue("sess_upload", 0)

	rr := uploadRequest(t, srv.Handler(), "sess_upload", "acme", tok, []byte("hello world"), "text/plain")
	if rr.Code != http.StatusCreated {
		t.Fatalf("under quota: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	if used, _ := counter.Used(context.Background(), "acme"); used != 11 {
		t.Errorf("counter after upload: got %d, want 11", used)
	}
}

func TestUploadRejectedByStorageQuotaPreCheck(t *testing.T) {
	srv, issuer, counter := newQuotaUploadServer(t, 10)
	tok, _ := issuer.Issue("sess_upload", 0)

	rr := uploadRequest(t, srv.Handler(), "sess_upload", "acme", tok, []byte("hello world"), "text/plain")
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("over quota: status %d, want 429; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "STORAGE_QUOTA_EXCEEDED") {
		t.Errorf("rejection should carry STORAGE_QUOTA_EXCEEDED: %s", rr.Body.String())
	}
	if used, _ := counter.Used(context.Background(), "acme"); used != 0 {
		t.Errorf("a pre-check rejection must not reserve bytes: got %d, want 0", used)
	}
}

func TestUploadRejectedByStorageQuotaStreamCap(t *testing.T) {
	srv, issuer, counter := newQuotaUploadServer(t, 100)
	tok, _ := issuer.Issue("sess_upload", 0)

	// Declare 10 bytes but stream 200: the reservation is admitted on
	// the declared size, then the hard stream cap aborts the upload.
	big := bytes.Repeat([]byte("x"), 200)
	rr := uploadWithContentLength(t, srv.Handler(), tok, big, 10)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("over-stream: status %d, want 429; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "STORAGE_QUOTA_EXCEEDED") {
		t.Errorf("over-stream rejection should carry STORAGE_QUOTA_EXCEEDED: %s", rr.Body.String())
	}
	if used, _ := counter.Used(context.Background(), "acme"); used != 0 {
		t.Errorf("an aborted over-stream upload must release its reservation: got %d, want 0", used)
	}
}

func TestUploadReconcilesStorageQuota(t *testing.T) {
	srv, issuer, counter := newQuotaUploadServer(t, 1000)
	tok, _ := issuer.Issue("sess_upload", 0)

	// Declare 50 bytes but send only 11: the reservation reconciles
	// down to the bytes actually written.
	rr := uploadWithContentLength(t, srv.Handler(), tok, []byte("hello world"), 50)
	if rr.Code != http.StatusCreated {
		t.Fatalf("reconcile upload: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	if used, _ := counter.Used(context.Background(), "acme"); used != 11 {
		t.Errorf("counter should reconcile to the bytes written: got %d, want 11", used)
	}
}

func TestUploadWithoutContentLengthRejectedUnderQuota(t *testing.T) {
	srv, issuer, _ := newQuotaUploadServer(t, 1000)
	tok, _ := issuer.Issue("sess_upload", 0)

	rr := uploadWithContentLength(t, srv.Handler(), tok, []byte("hello world"), -1)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing Content-Length under a quota: status %d, want 400; body %s",
			rr.Code, rr.Body.String())
	}
}

// unavailableCounter is a storagequota.Counter whose Reserve reports the
// §12.4 line 210 dual-store outage (Redis down AND the Postgres fallback
// down). Used to assert the upload handler fails closed with 503.
type unavailableCounter struct{}

func (unavailableCounter) Reserve(context.Context, string, int64, int64) (int64, error) {
	return 0, storagequota.ErrUnavailable
}
func (unavailableCounter) Adjust(context.Context, string, int64) error { return nil }
func (unavailableCounter) Used(context.Context, string) (int64, error) {
	return 0, storagequota.ErrUnavailable
}
func (unavailableCounter) Set(context.Context, string, int64) error { return nil }

// spec: §12.4 line 210 — when the quota counter and its Postgres fallback
// are both unreachable the upload is rejected with 503 (fail closed); no
// blob is written so the quota can never be bypassed by infrastructure
// degradation.
func TestUploadStorageQuotaDualOutageFailsClosed_spec_12_4_210(t *testing.T) {
	store := memstore.New()
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("upload-secret")})
	issuer := uploadtoken.NewIssuer(ring, clock)
	verifier := uploadtoken.NewVerifier(ring, uploadtoken.NewMemoryTracker(), clock)
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme", StorageQuotaBytes: 1000}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:               clock,
		IDFunc:              func() string { return "sess_upload" },
		UploadTokenIssuer:   issuer,
		UploadTokenVerifier: verifier,
		Blobs:               blobstore.NewMemoryStore(nil),
		Tenants:             tenants,
		StorageQuota:        unavailableCounter{},
	})
	seedCreatedSession(t, store, "sess_upload", "acme")
	tok, _ := issuer.Issue("sess_upload", 0)

	rr := uploadRequest(t, srv.Handler(), "sess_upload", "acme", tok, []byte("hello world"), "text/plain")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("dual outage: status %d, want 503; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "STORAGE_UNAVAILABLE") {
		t.Errorf("503 should carry STORAGE_UNAVAILABLE: %s", rr.Body.String())
	}
}

func TestUploadNoStorageQuotaSkipsEnforcement(t *testing.T) {
	// Tenant quota of zero means unlimited: the upload proceeds and the
	// counter is left untouched.
	srv, issuer, counter := newQuotaUploadServer(t, 0)
	tok, _ := issuer.Issue("sess_upload", 0)

	rr := uploadRequest(t, srv.Handler(), "sess_upload", "acme", tok, []byte("hello world"), "text/plain")
	if rr.Code != http.StatusCreated {
		t.Fatalf("unlimited tenant: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	if used, _ := counter.Used(context.Background(), "acme"); used != 0 {
		t.Errorf("an unlimited tenant must not touch the counter: got %d, want 0", used)
	}
}
