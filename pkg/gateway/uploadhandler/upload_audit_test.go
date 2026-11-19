// SPDX-License-Identifier: MIT

package uploadhandler_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/seam/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/uploadhandler"
	"github.com/lennylabs/lenny/pkg/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/session/sessionstore/memstore"
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

// auditStack is a session server and an upload handler wired together over
// one session store, with a fixed clock and a token issuer.
type auditStack struct {
	store   sessionstore.Store
	issuer  *uploadtoken.Issuer
	clock   func() time.Time
	handler http.Handler
}

// newAuditStack builds an auditStack: the session server takes the fixed
// clock, the upload token issuer and verifier, and core's other options,
// and the upload handler takes up with a memory blob store when up names
// none.
func newAuditStack(t *testing.T, core sessionserver.Options, up uploadhandler.Options) auditStack {
	t.Helper()
	clock := func() time.Time { return time.Date(2026, 5, 26, 9, 0, 0, 0, time.UTC) }
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k", Secret: []byte("s")})
	issuer := uploadtoken.NewIssuer(ring, clock)
	core.DevTenantHeader = true
	core.Clock = clock
	core.UploadTokenIssuer = issuer
	core.UploadTokenVerifier = uploadtoken.NewVerifier(ring, uploadtoken.NewMemoryTracker(), clock)
	if up.Blobs == nil {
		up.Blobs = blobstore.NewMemoryStore(nil)
	}
	store := memstore.New()
	_, h := serveUploads(t, store, core, up)
	return auditStack{store: store, issuer: issuer, clock: clock, handler: asOwner(h)}
}

// seed stores a session in the created state under the default tenant.
func (a auditStack) seed(t *testing.T, id string) sessionstore.Session {
	t.Helper()
	row := sessionstore.Session{
		ID: id, TenantID: "default", UserID: testOwner,
		RuntimeRef: "claude-code", State: session.StateCreated,
		CreatedAt: a.clock(), UpdatedAt: a.clock(),
	}
	if err := a.store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return row
}

// upload posts body to the session's upload route with a freshly minted
// token and the given headers.
func (a auditStack) upload(t *testing.T, id, body string, hdrs map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	tok, err := a.issuer.Issue(id, uploadtoken.DefaultTTL)
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
	a.handler.ServeHTTP(rr, req)
	return rr
}

// TestAnAcceptedUploadIsAuditedWithItsBlobReference checks that a successful
// upload writes a session.upload audit row with the resulting UploadRef in
// Detail, under the session's tenant and stamped with the session core's
// clock. F-7.4.17.
//
// spec: §16.6 (Operational Events Catalog), GWSEAM-AuditUpload
func TestAnAcceptedUploadIsAuditedWithItsBlobReference(t *testing.T) {
	sink := &lifecycleSink{}
	a := newAuditStack(t, sessionserver.Options{LifecycleAuditSink: sink}, uploadhandler.Options{})
	row := a.seed(t, "sess_audit")

	if rr := a.upload(t, row.ID, "hello", nil); rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	ev := mustSingleUploadEvent(t, sink)
	if ev.SessionID != row.ID {
		t.Errorf("sessionID = %q, want %q", ev.SessionID, row.ID)
	}
	if ev.TenantID != row.TenantID {
		t.Errorf("tenantID = %q, want %q", ev.TenantID, row.TenantID)
	}
	if ev.Detail == "" {
		t.Errorf("Detail empty, want the lenny-blob:// uploadRef")
	}
	if !ev.At.Equal(a.clock()) {
		t.Errorf("At = %v, want the session core's clock %v", ev.At, a.clock())
	}
}

// TestAnUploadWithNoAuditSinkSucceeds checks that an upload succeeds when
// the session core has no lifecycle audit sink, the minimal-gateway
// posture. F-7.4.17.
//
// spec: GWSEAM-AuditUpload
func TestAnUploadWithNoAuditSinkSucceeds(t *testing.T) {
	a := newAuditStack(t, sessionserver.Options{}, uploadhandler.Options{})
	row := a.seed(t, "sess_nil")
	if rr := a.upload(t, row.ID, "x", nil); rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
}

// spec: §13.4; §11.7 — a hash-mismatch rejection writes a session.upload
// audit row with a rejected outcome so the SIEM stream sees the
// upload-rejection class. F-13.4.8.
func TestRunUploadEmitsRejectedAudit_HashMismatch(t *testing.T) {
	sink := &lifecycleSink{}
	a := newAuditStack(t, sessionserver.Options{LifecycleAuditSink: sink}, uploadhandler.Options{})
	row := a.seed(t, "sess_hash")

	rr := a.upload(t, row.ID, "hello", map[string]string{
		uploadhandler.UploadContentHashHeader: "deadbeef", // wrong hash
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	ev := mustSingleUploadEvent(t, sink)
	if ev.Outcome != "rejected" {
		t.Errorf("outcome = %q, want rejected", ev.Outcome)
	}
	if ev.Reason != "hash_mismatch" {
		t.Errorf("reason = %q, want hash_mismatch", ev.Reason)
	}
	if ev.SessionID != row.ID || ev.TenantID != row.TenantID {
		t.Errorf("scope = (%q,%q), want (%q,%q)", ev.SessionID, ev.TenantID, row.ID, row.TenantID)
	}
}

// spec: §13.4; §11.7 — a session cumulative-size rejection (early
// Content-Length gate) writes a rejected audit row. F-13.4.8.
func TestRunUploadEmitsRejectedAudit_SessionBytes(t *testing.T) {
	sink := &lifecycleSink{}
	a := newAuditStack(t, sessionserver.Options{LifecycleAuditSink: sink}, uploadhandler.Options{MaxUploadBytesPerSession: 3})
	row := a.seed(t, "sess_bytes")

	rr := a.upload(t, row.ID, "hello", nil) // 5 bytes > 3
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	ev := mustSingleUploadEvent(t, sink)
	if ev.Outcome != "rejected" || ev.Reason != "session_upload_bytes" {
		t.Errorf("outcome/reason = %q/%q, want rejected/session_upload_bytes", ev.Outcome, ev.Reason)
	}
}

// spec: §16.6; §11.7 — an admitted upload still records an accepted
// audit row (the F-7.4.17 row now carries the explicit outcome). F-13.4.8.
func TestRunUploadEmitsAcceptedOutcome(t *testing.T) {
	sink := &lifecycleSink{}
	a := newAuditStack(t, sessionserver.Options{LifecycleAuditSink: sink}, uploadhandler.Options{})
	row := a.seed(t, "sess_ok")

	if rr := a.upload(t, row.ID, "hello", nil); rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	ev := mustSingleUploadEvent(t, sink)
	if ev.Outcome != "accepted" {
		t.Errorf("outcome = %q, want accepted", ev.Outcome)
	}
	if ev.Reason != "" {
		t.Errorf("reason = %q, want empty on accepted outcome", ev.Reason)
	}
}

// spec: §16.1 — a successful upload counts the committed bytes against
// lenny_upload_bytes_total. F-13.4.12.
func TestRunUploadCountsBytes(t *testing.T) {
	fm := &fakeUploadMetrics{}
	a := newAuditStack(t, sessionserver.Options{}, uploadhandler.Options{Metrics: fm})
	row := a.seed(t, "sess_bytes_count")

	if rr := a.upload(t, row.ID, "hello", nil); rr.Code != http.StatusCreated {
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
	a := newAuditStack(t, sessionserver.Options{}, uploadhandler.Options{
		Metrics: fm,
		Subsystem: &subsystem.Subsystem{
			Name:    "upload_handler",
			Limiter: &subsystem.Limiter{MaxConcurrent: 4},
		},
	})
	row := a.seed(t, "sess_depth")

	if rr := a.upload(t, row.ID, "hello", nil); rr.Code != http.StatusCreated {
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

func mustSingleUploadEvent(t *testing.T, sink *lifecycleSink) sessionserver.SessionLifecycleEvent {
	t.Helper()
	evs := sink.eventsOf("session.upload")
	if len(evs) != 1 || len(sink.events) != 1 {
		t.Fatalf("audit events: got %d, %d of them session.upload; want exactly one session.upload", len(sink.events), len(evs))
	}
	return evs[0]
}
