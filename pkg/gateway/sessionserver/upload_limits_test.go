// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// spec: §11.1 lines 10-11 — concurrent-upload and per-session
// cumulative upload-size admission, enforced in the upload handler.
// F-11.1.5, F-11.1.6.

// newUploadLimitServer builds an upload server for the acme tenant whose
// §11.1 upload-admission caps are set as supplied (zero leaves a scope
// unlimited), seeded with a created session sess_upload.
func newUploadLimitServer(t *testing.T, perSession, global int, maxBytes int64) (*sessionserver.Server, *uploadtoken.Issuer, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("upload-secret")})
	issuer := uploadtoken.NewIssuer(ring, clock)
	verifier := uploadtoken.NewVerifier(ring, uploadtoken.NewMemoryTracker(), clock)
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:                          clock,
		IDFunc:                         func() string { return "sess_upload" },
		UploadTokenIssuer:              issuer,
		UploadTokenVerifier:            verifier,
		Blobs:                          blobstore.NewMemoryStore(nil),
		MaxConcurrentUploadsPerSession: perSession,
		MaxConcurrentUploadsGlobal:     global,
		MaxUploadBytesPerSession:       maxBytes,
	})
	seedCreatedSession(t, store, "sess_upload", "acme")
	return srv, issuer, store
}

// blockingReader blocks on its first Read until release is closed,
// signalling started once it has entered the read. It lets a test hold
// one upload in-flight (and thus its §11.1 concurrency slot) while a
// second request races it.
type blockingReader struct {
	started chan struct{}
	release chan struct{}
	data    []byte
	pos     int
	once    sync.Once
}

func (b *blockingReader) Read(p []byte) (int, error) {
	b.once.Do(func() {
		close(b.started)
		<-b.release
	})
	if b.pos >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.pos:])
	b.pos += n
	return n, nil
}

// A second upload against a session already at its per-session
// concurrency cap is rejected with 429 RATE_LIMITED while the first is
// in-flight, then admitted once the first finishes. spec: §11.1 line 10.
func TestUploadPerSessionConcurrencyLimit(t *testing.T) {
	srv, issuer, _ := newUploadLimitServer(t, 1, 0, 0)
	tok, _ := issuer.Issue("sess_upload", 0)
	h := srv.Handler()

	br := &blockingReader{started: make(chan struct{}), release: make(chan struct{}), data: []byte("hello")}
	req1 := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_upload/upload", br)
	req1.Header.Set("X-Lenny-Tenant-ID", "acme")
	req1.Header.Set("X-Lenny-Upload-Token", tok)
	req1.Header.Set("Content-Type", "application/octet-stream")
	rr1 := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rr1, req1)
		close(done)
	}()

	<-br.started // req1 holds the only per-session slot

	rr2 := uploadRequest(t, h, "sess_upload", "acme", tok, []byte("world"), "text/plain")
	if rr2.Code != http.StatusTooManyRequests {
		t.Fatalf("second concurrent upload: status %d, want 429; body %s", rr2.Code, rr2.Body.String())
	}
	if !strings.Contains(rr2.Body.String(), "RATE_LIMITED") || !strings.Contains(rr2.Body.String(), "upload_session") {
		t.Errorf("rejection should be RATE_LIMITED scope upload_session: %s", rr2.Body.String())
	}

	close(br.release) // let req1 finish, freeing the slot
	<-done
	if rr1.Code != http.StatusCreated {
		t.Fatalf("first upload should complete: status %d; body %s", rr1.Code, rr1.Body.String())
	}

	// With the slot released, a fresh upload is admitted again.
	rr3 := uploadRequest(t, h, "sess_upload", "acme", tok, []byte("again"), "text/plain")
	if rr3.Code != http.StatusCreated {
		t.Fatalf("upload after slot release: status %d, want 201; body %s", rr3.Code, rr3.Body.String())
	}
}

// With no upload caps configured the handler admits without bookkeeping.
// spec: §11.1 lines 10-11.
func TestUploadNoLimitsConfigured(t *testing.T) {
	srv, issuer, _ := newUploadLimitServer(t, 0, 0, 0)
	tok, _ := issuer.Issue("sess_upload", 0)
	rr := uploadRequest(t, srv.Handler(), "sess_upload", "acme", tok, bytes.Repeat([]byte("x"), 4096), "text/plain")
	if rr.Code != http.StatusCreated {
		t.Fatalf("unlimited upload: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
}

// The per-session cumulative-size cap rejects the upload that would push
// a session past its total once the declared Content-Length is known.
// spec: §11.1 line 11.
func TestUploadPerSessionSizeCapEarlyReject(t *testing.T) {
	srv, issuer, _ := newUploadLimitServer(t, 0, 0, 20)
	tok, _ := issuer.Issue("sess_upload", 0)
	h := srv.Handler()

	if rr := uploadRequest(t, h, "sess_upload", "acme", tok, []byte("hello world"), "text/plain"); rr.Code != http.StatusCreated {
		t.Fatalf("first 11-byte upload: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	// 11 + 11 = 22 > 20: the declared Content-Length alone trips the cap.
	rr := uploadRequest(t, h, "sess_upload", "acme", tok, []byte("hello world"), "text/plain")
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("second upload over the session cap: status %d, want 429; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "QUOTA_EXCEEDED") || !strings.Contains(rr.Body.String(), "session_upload_bytes") {
		t.Errorf("rejection should be QUOTA_EXCEEDED scope session_upload_bytes: %s", rr.Body.String())
	}
}

// The cumulative cap admits uploads up to exactly the limit and rejects
// the first byte past it. spec: §11.1 line 11.
func TestUploadPerSessionSizeCapBoundary(t *testing.T) {
	srv, issuer, _ := newUploadLimitServer(t, 0, 0, 22)
	tok, _ := issuer.Issue("sess_upload", 0)
	h := srv.Handler()

	for i := 0; i < 2; i++ {
		if rr := uploadRequest(t, h, "sess_upload", "acme", tok, []byte("hello world"), "text/plain"); rr.Code != http.StatusCreated {
			t.Fatalf("upload %d up to the cap: status %d, want 201; body %s", i, rr.Code, rr.Body.String())
		}
	}
	// The session now holds exactly 22 bytes; one more is over.
	if rr := uploadRequest(t, h, "sess_upload", "acme", tok, []byte("x"), "text/plain"); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("upload past the cap: status %d, want 429; body %s", rr.Code, rr.Body.String())
	}
}

// A client that under-declares Content-Length cannot bypass the cap: the
// authoritative check runs against the bytes actually streamed, rejects
// the over-cap upload, and does not consume the session's headroom.
// spec: §11.1 line 11.
func TestUploadPerSessionSizeCapPostHocOnActualBytes(t *testing.T) {
	srv, issuer, _ := newUploadLimitServer(t, 0, 0, 20)
	tok, _ := issuer.Issue("sess_upload", 0)
	h := srv.Handler()

	// Declare 5 bytes (passes the early check) but stream 30.
	big := bytes.Repeat([]byte("x"), 30)
	rr := uploadWithContentLength(t, h, tok, big, 5)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("under-declared over-cap upload: status %d, want 429; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "QUOTA_EXCEEDED") {
		t.Errorf("post-hoc rejection should be QUOTA_EXCEEDED: %s", rr.Body.String())
	}
	// The rejected upload did not commit any bytes, so a within-cap
	// upload still succeeds afterward.
	if rr := uploadRequest(t, h, "sess_upload", "acme", tok, []byte("hello world"), "text/plain"); rr.Code != http.StatusCreated {
		t.Fatalf("within-cap upload after a rejected over-stream: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
}
