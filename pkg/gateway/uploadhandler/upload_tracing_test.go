// SPDX-License-Identifier: MIT

package uploadhandler_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/uploadhandler"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
	"github.com/lennylabs/lenny/tests/testinfra/uploadwire"
)

// spec: §16.3 — the gateway-side session.upload span has a tracer.Start
// emit site. These tests install an SDK-backed span recorder over the global
// OTel provider and assert the upload handler records its span, on the happy
// path and on an error path, which sets status Error. F-16.3.1.

// installSpanRecorder swaps the global OTel TracerProvider for an
// SDK-backed recorder so a test can read every span the handler under
// test emitted, then restores the prior provider when the test ends.
func installSpanRecorder(t *testing.T) (*tracetest.SpanRecorder, func()) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	return rec, func() { otel.SetTracerProvider(prev) }
}

// findSpan returns the first recorded span with the given name, or nil.
func findSpan(spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	for _, s := range spans {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

// TestUploadStartsSessionUploadSpan checks that a clean upload records the
// session.upload span with an unset status.
//
// spec: §16.3 (Distributed Tracing)
func TestUploadStartsSessionUploadSpan(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	srv, issuer, _, _, store, _ := newUploadServer(t)
	seedCreatedSession(t, store, "sess_upload", "acme")
	tok, _ := issuer.Issue("sess_upload", 0)

	rr := uploadRequest(t, asOwner(srv.Handler()), "sess_upload", "acme", tok, []byte("hello world"), "text/plain")
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	span := findSpan(rec.Ended(), "session.upload")
	if span == nil {
		t.Fatalf("session.upload span not recorded; got %d spans", len(rec.Ended()))
	}
	if span.Status().Code != codes.Unset {
		t.Errorf("clean upload span status = %v, want Unset", span.Status().Code)
	}
}

// vanishingStore serves the §10.2 ownership check's bounded read with a row
// the test owner owns and answers every later, unbounded read with
// ErrNotFound, standing in for a session deleted between the ownership check
// and the handler's own read.
type vanishingStore struct {
	*memstore.Store
}

func (v vanishingStore) Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	if _, bounded := ctx.Deadline(); bounded {
		return sessionstore.Session{ID: id, TenantID: tenantID, UserID: testOwner, State: session.StateCreated}, nil
	}
	return sessionstore.Session{}, sessionstore.ErrNotFound
}

// TestUploadSpanRecordsMissingSessionError checks that an upload to a
// session deleted after the ownership check records the session.upload
// span with an error status.
//
// spec: §16.3 (Distributed Tracing)
func TestUploadSpanRecordsMissingSessionError(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("upload-secret")})
	issuer := uploadtoken.NewIssuer(ring, clock)
	srv := sessionserver.New(vanishingStore{memstore.New()}, sessionserver.Options{
		DevTenantHeader:     true,
		Clock:               clock,
		UploadTokenIssuer:   issuer,
		UploadTokenVerifier: uploadtoken.NewVerifier(ring, uploadtoken.NewMemoryTracker(), clock),
	})
	uploadwire.Attach(t, srv, uploadhandler.Options{Blobs: blobstore.NewMemoryStore(nil)})
	// The ownership check admits the owner, and the handler's store.Get then
	// returns ErrNotFound, driving the §16.3 RESOURCE_NOT_FOUND error branch
	// on the upload span.
	tok, _ := issuer.Issue("sess_missing_upload", 0)

	rr := uploadRequest(t, asOwner(srv.Handler()), "sess_missing_upload", "acme", tok, []byte("x"), "text/plain")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404; body=%s", rr.Code, rr.Body.String())
	}

	span := findSpan(rec.Ended(), "session.upload")
	if span == nil {
		t.Fatalf("session.upload span not recorded on the missing-session path")
	}
	if span.Status().Code != codes.Error {
		t.Errorf("span status = %v, want codes.Error on a missing session", span.Status().Code)
	}
}
