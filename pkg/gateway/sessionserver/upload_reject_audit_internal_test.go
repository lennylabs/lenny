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
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/subsystem"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// fakeUploadMetrics records the §16.1 upload-handler observations so the
// handler tests can assert the byte counter and depth gauge fire.
// F-13.4.12.
type fakeUploadMetrics struct {
	bytes       int64
	depthCalls  int
	lastDepth   int
	abortCounts map[string]int
}

func (m *fakeUploadMetrics) AddUploadBytes(n int64)    { m.bytes += n }
func (m *fakeUploadMetrics) SetUploadQueueDepth(d int) { m.depthCalls++; m.lastDepth = d }
func (m *fakeUploadMetrics) AddExtractionAbort(errorType string) {
	if m.abortCounts == nil {
		m.abortCounts = map[string]int{}
	}
	m.abortCounts[errorType]++
}

func uploadTestServer(t *testing.T, opts Options) (*Server, *uploadtoken.Issuer, func() time.Time) {
	t.Helper()
	clock := func() time.Time { return time.Date(2026, 5, 26, 9, 0, 0, 0, time.UTC) }
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k", Secret: []byte("s")})
	issuer := uploadtoken.NewIssuer(ring, clock)
	tracker := uploadtoken.NewMemoryTracker()
	verifier := uploadtoken.NewVerifier(ring, tracker, clock)
	opts.Clock = clock
	opts.UploadTokenIssuer = issuer
	opts.UploadTokenVerifier = verifier
	if opts.Blobs == nil {
		opts.Blobs = blobstore.NewMemoryStore(nil)
	}
	srv := New(memstore.New(), opts)
	return srv, issuer, clock
}

func seedCreatedSession(t *testing.T, srv *Server, id string, clock func() time.Time) sessionstore.Session {
	t.Helper()
	row := sessionstore.Session{
		ID: id, TenantID: "default", UserID: "alice",
		RuntimeRef: "claude-code", State: session.StateCreated,
		CreatedAt: clock(), UpdatedAt: clock(),
	}
	if err := srv.store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return row
}

func uploadRequest(t *testing.T, issuer *uploadtoken.Issuer, srv *Server, id, body string, hdrs map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	tok, err := issuer.Issue(id, uploadtoken.DefaultTTL)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+id+"/upload", bytes.NewBufferString(body))
	req.Header.Set("X-Lenny-Tenant-ID", "default")
	req.Header.Set("X-Lenny-Upload-Token", tok)
	req.Header.Set("Content-Type", "text/plain")
	for k, v := range hdrs {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr
}

// spec: §13.4; §11.7 — a hash-mismatch rejection writes a session.upload
// audit row with a rejected outcome so the SIEM stream sees the
// upload-rejection class. F-13.4.8.
func TestRunUploadEmitsRejectedAudit_HashMismatch(t *testing.T) {
	sink := &captureLifecycleAudit{}
	srv, issuer, clock := uploadTestServer(t, Options{LifecycleAuditSink: sink})
	row := seedCreatedSession(t, srv, "sess_hash", clock)

	rr := uploadRequest(t, issuer, srv, row.ID, "hello", map[string]string{
		UploadContentHashHeader: "deadbeef", // wrong hash
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	ev := mustSingleUploadEvent(t, sink)
	if ev.Outcome != uploadOutcomeRejected {
		t.Errorf("outcome = %q, want %q", ev.Outcome, uploadOutcomeRejected)
	}
	if ev.Reason != uploadRejectHashMismatch {
		t.Errorf("reason = %q, want %q", ev.Reason, uploadRejectHashMismatch)
	}
	if ev.SessionID != row.ID || ev.TenantID != row.TenantID {
		t.Errorf("scope = (%q,%q), want (%q,%q)", ev.SessionID, ev.TenantID, row.ID, row.TenantID)
	}
}

// spec: §13.4; §11.7 — a session cumulative-size rejection (early
// Content-Length gate) writes a rejected audit row. F-13.4.8.
func TestRunUploadEmitsRejectedAudit_SessionBytes(t *testing.T) {
	sink := &captureLifecycleAudit{}
	srv, issuer, clock := uploadTestServer(t, Options{
		LifecycleAuditSink:       sink,
		MaxUploadBytesPerSession: 3,
	})
	row := seedCreatedSession(t, srv, "sess_bytes", clock)

	rr := uploadRequest(t, issuer, srv, row.ID, "hello", nil) // 5 bytes > 3
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	ev := mustSingleUploadEvent(t, sink)
	if ev.Outcome != uploadOutcomeRejected || ev.Reason != uploadRejectSessionBytes {
		t.Errorf("outcome/reason = %q/%q, want %q/%q",
			ev.Outcome, ev.Reason, uploadOutcomeRejected, uploadRejectSessionBytes)
	}
}

// spec: §16.6; §11.7 — an admitted upload still records an accepted
// audit row (the F-7.4.17 row now carries the explicit outcome). F-13.4.8.
func TestRunUploadEmitsAcceptedOutcome(t *testing.T) {
	sink := &captureLifecycleAudit{}
	srv, issuer, clock := uploadTestServer(t, Options{LifecycleAuditSink: sink})
	row := seedCreatedSession(t, srv, "sess_ok", clock)

	rr := uploadRequest(t, issuer, srv, row.ID, "hello", nil)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	ev := mustSingleUploadEvent(t, sink)
	if ev.Outcome != uploadOutcomeAccepted {
		t.Errorf("outcome = %q, want %q", ev.Outcome, uploadOutcomeAccepted)
	}
	if ev.Reason != "" {
		t.Errorf("reason = %q, want empty on accepted outcome", ev.Reason)
	}
}

// spec: §16.1 — a successful upload counts the committed bytes against
// lenny_upload_bytes_total. F-13.4.12.
func TestRunUploadCountsBytes(t *testing.T) {
	fm := &fakeUploadMetrics{}
	srv, issuer, clock := uploadTestServer(t, Options{UploadMetrics: fm})
	row := seedCreatedSession(t, srv, "sess_bytes_count", clock)

	rr := uploadRequest(t, issuer, srv, row.ID, "hello", nil)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if fm.bytes != 5 {
		t.Errorf("AddUploadBytes total = %d, want 5", fm.bytes)
	}
}

// spec: §16.1 — when the §4.1 Upload Handler subsystem is configured the
// handler samples its depth on entry and exit. F-13.4.12.
func TestRunUploadSamplesQueueDepth(t *testing.T) {
	fm := &fakeUploadMetrics{}
	srv, issuer, clock := uploadTestServer(t, Options{
		UploadMetrics: fm,
		UploadSubsystem: &subsystem.Subsystem{
			Name:    "upload_handler",
			Limiter: &subsystem.Limiter{MaxConcurrent: 4},
		},
	})
	row := seedCreatedSession(t, srv, "sess_depth", clock)

	rr := uploadRequest(t, issuer, srv, row.ID, "hello", nil)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	// Entry + exit samples.
	if fm.depthCalls != 2 {
		t.Errorf("SetUploadQueueDepth calls = %d, want 2", fm.depthCalls)
	}
	// After release the limiter is empty again.
	if fm.lastDepth != 0 {
		t.Errorf("final depth = %d, want 0", fm.lastDepth)
	}
}

func mustSingleUploadEvent(t *testing.T, sink *captureLifecycleAudit) SessionLifecycleEvent {
	t.Helper()
	if len(sink.events) != 1 {
		t.Fatalf("audit events: got %d, want 1", len(sink.events))
	}
	ev := sink.events[0]
	if ev.EventType != auditSessionUpload {
		t.Fatalf("eventType = %q, want %q", ev.EventType, auditSessionUpload)
	}
	return ev
}
