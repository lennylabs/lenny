// SPDX-License-Identifier: MIT

package sessionserver

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// F-7.4.17: a successful upload writes a §16.6 session.upload audit row
// with the resulting UploadRef in Detail. spec: §16.6 line 338; §11.7.
func TestRunUploadEmitsAuditEvent_spec_7_4_17(t *testing.T) {
	store := memstore.New()
	blobs := blobstore.NewMemoryStore(nil)
	clock := func() time.Time { return time.Date(2026, 5, 26, 9, 0, 0, 0, time.UTC) }
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k", Secret: []byte("s")})
	issuer := uploadtoken.NewIssuer(ring, clock)
	tracker := uploadtoken.NewMemoryTracker()
	verifier := uploadtoken.NewVerifier(ring, tracker, clock)
	sink := &captureLifecycleAudit{}
	srv := New(store, Options{
		Clock:               clock,
		UploadTokenIssuer:   issuer,
		UploadTokenVerifier: verifier,
		Blobs:               blobs,
		LifecycleAuditSink:  sink,
	})

	row := sessionstore.Session{
		ID: "sess_audit", TenantID: "default", UserID: "alice",
		RuntimeRef: "claude-code", State: session.StateCreated,
		CreatedAt: clock(), UpdatedAt: clock(),
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tok, err := issuer.Issue(row.ID, uploadtoken.DefaultTTL)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_audit/upload", bytes.NewBufferString("hello"))
	req.Header.Set("X-Lenny-Tenant-ID", "default")
	req.Header.Set("X-Lenny-Upload-Token", tok)
	req.Header.Set("Content-Type", "text/plain")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if len(sink.events) != 1 {
		t.Fatalf("audit events: got %d, want 1", len(sink.events))
	}
	ev := sink.events[0]
	if ev.EventType != auditSessionUpload {
		t.Errorf("eventType = %q, want %q", ev.EventType, auditSessionUpload)
	}
	if ev.SessionID != row.ID {
		t.Errorf("sessionID = %q, want %q", ev.SessionID, row.ID)
	}
	if ev.TenantID != row.TenantID {
		t.Errorf("tenantID = %q, want %q", ev.TenantID, row.TenantID)
	}
	if ev.Detail == "" {
		t.Errorf("Detail empty, want the lenny-blob:// uploadRef")
	}
}

// F-7.4.17: a successful finalize writes a §16.6 session.finalize_workspace
// audit row with the consumed digest in Detail.
func TestHandleFinalizeEmitsAuditEvent_spec_7_4_17(t *testing.T) {
	store := memstore.New()
	clock := func() time.Time { return time.Date(2026, 5, 26, 9, 0, 0, 0, time.UTC) }
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k", Secret: []byte("s")})
	issuer := uploadtoken.NewIssuer(ring, clock)
	tracker := uploadtoken.NewMemoryTracker()
	verifier := uploadtoken.NewVerifier(ring, tracker, clock)
	sink := &captureLifecycleAudit{}
	srv := New(store, Options{
		Clock:               clock,
		UploadTokenIssuer:   issuer,
		UploadTokenVerifier: verifier,
		LifecycleAuditSink:  sink,
	})

	// Seed a session row mid-flow (state=created) with a known digest.
	_, parsed, err := issuer.IssueDetailed("sess_fin", uploadtoken.DefaultTTL)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	row := sessionstore.Session{
		ID: "sess_fin", TenantID: "default", UserID: "alice",
		RuntimeRef: "claude-code", State: session.StateCreated,
		CreatedAt: clock(), UpdatedAt: clock(),
		UploadTokenDigest: parsed.Digest,
		UploadTokenExpiry: parsed.Expiry,
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_fin/finalize", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if len(sink.events) != 1 {
		t.Fatalf("audit events: got %d, want 1", len(sink.events))
	}
	ev := sink.events[0]
	if ev.EventType != auditSessionWorkspaceFinalized {
		t.Errorf("eventType = %q, want %q", ev.EventType, auditSessionWorkspaceFinalized)
	}
	if ev.Detail != parsed.Digest {
		t.Errorf("Detail = %q, want digest %q", ev.Detail, parsed.Digest)
	}
	if !tracker.IsConsumed(parsed.Digest) {
		t.Errorf("digest should be consumed after finalize")
	}
}

// F-7.4.17: a nil LifecycleAuditSink is the minimal-gateway posture and
// must not panic.
func TestRunUploadAuditNilSinkSafe_spec_7_4_17(t *testing.T) {
	store := memstore.New()
	blobs := blobstore.NewMemoryStore(nil)
	clock := func() time.Time { return time.Date(2026, 5, 26, 9, 0, 0, 0, time.UTC) }
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k", Secret: []byte("s")})
	issuer := uploadtoken.NewIssuer(ring, clock)
	tracker := uploadtoken.NewMemoryTracker()
	verifier := uploadtoken.NewVerifier(ring, tracker, clock)
	srv := New(store, Options{
		Clock:               clock,
		UploadTokenIssuer:   issuer,
		UploadTokenVerifier: verifier,
		Blobs:               blobs,
		// LifecycleAuditSink omitted
	})

	row := sessionstore.Session{
		ID: "sess_nil", TenantID: "default", State: session.StateCreated,
		CreatedAt: clock(), UpdatedAt: clock(),
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tok, _ := issuer.Issue(row.ID, uploadtoken.DefaultTTL)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_nil/upload", bytes.NewBufferString("x"))
	req.Header.Set("X-Lenny-Tenant-ID", "default")
	req.Header.Set("X-Lenny-Upload-Token", tok)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
}
