// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/transcriptstore"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// spec: §16.3 lines 336-356 — the gateway-side session-lifecycle spans
// (session.create, session.prompt, session.upload, session.seal_and_export)
// must have a tracer.Start emit site, not just a catalog constant. These
// tests install an SDK-backed span recorder over the global OTel provider
// and assert each instrumented handler records its span, on both the happy
// path and an error path (which sets status Error). F-16.3.1.

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

func TestCreateStartsSessionCreateSpan_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("test-secret")})
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		Clock:             clock,
		IDFunc:            func() string { return "sess_span_ok" },
		UploadTokenIssuer: uploadtoken.NewIssuer(ring, clock),
	})

	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
		UserID:     "alice@acme.com",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	span := findSpan(rec.Ended(), "session.create")
	if span == nil {
		t.Fatalf("session.create span not recorded; got %d spans", len(rec.Ended()))
	}
	if span.Status().Code != codes.Unset {
		t.Errorf("clean create span status = %v, want Unset", span.Status().Code)
	}
}

func TestCreateSpanRecordsPersistError_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("test")})
	srv := sessionserver.New(&createRejectingStore{Store: memstore.New()}, sessionserver.Options{
		Clock:             clock,
		IDFunc:            func() string { return "sess_span_fail" },
		UploadTokenIssuer: uploadtoken.NewIssuer(ring, clock),
	})

	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
	})
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503; body=%s", rr.Code, rr.Body.String())
	}

	span := findSpan(rec.Ended(), "session.create")
	if span == nil {
		t.Fatalf("session.create span not recorded on the persist-failure path")
	}
	if span.Status().Code != codes.Error {
		t.Errorf("span status = %v, want codes.Error on a persistence failure", span.Status().Code)
	}
}

func TestMessagesStartsSessionPromptSpan_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	srv, store := newMessagesServer(t)
	seedRunningSession(t, store, "sess_prompt_ok")

	rr := sendMessageRequest(t, srv.Handler(), "sess_prompt_ok", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Role: "user", Content: "hello"}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	span := findSpan(rec.Ended(), "session.prompt")
	if span == nil {
		t.Fatalf("session.prompt span not recorded; got %d spans", len(rec.Ended()))
	}
	if span.Status().Code != codes.Unset {
		t.Errorf("clean prompt span status = %v, want Unset", span.Status().Code)
	}
}

// erroringExecutor is an executor.Executor whose Send always fails, to
// drive the §16.3 line 342 EXECUTOR_FAILURE error branch.
type erroringExecutor struct{}

func (erroringExecutor) Send(context.Context, string, []executor.Message) ([]executor.OutputPart, error) {
	return nil, errors.New("test: executor rejected the batch")
}

func (erroringExecutor) Close(context.Context, string) error { return nil }

func TestMessagesSpanRecordsExecutorError_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		Executor:    erroringExecutor{},
		Transcripts: transcriptstore.NewMemory(),
	})
	seedRunningSession(t, store, "sess_prompt_fail")

	rr := sendMessageRequest(t, srv.Handler(), "sess_prompt_fail", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Role: "user", Content: "hello"}},
	})
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500; body=%s", rr.Code, rr.Body.String())
	}

	span := findSpan(rec.Ended(), "session.prompt")
	if span == nil {
		t.Fatalf("session.prompt span not recorded on the executor-failure path")
	}
	if span.Status().Code != codes.Error {
		t.Errorf("span status = %v, want codes.Error on an executor failure", span.Status().Code)
	}
}

func TestUploadStartsSessionUploadSpan_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	srv, issuer, _, _, store, _ := newUploadServer(t)
	seedCreatedSession(t, store, "sess_upload", "acme")
	tok, _ := issuer.Issue("sess_upload", 0)

	rr := uploadRequest(t, srv.Handler(), "sess_upload", "acme", tok, []byte("hello world"), "text/plain")
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

func TestUploadSpanRecordsMissingSessionError_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	srv, issuer, _, _, _, _ := newUploadServer(t)
	// No session is seeded: the store.Get returns ErrNotFound, driving the
	// §16.3 line 338 RESOURCE_NOT_FOUND error branch on the upload span.
	tok, _ := issuer.Issue("sess_missing_upload", 0)

	rr := uploadRequest(t, srv.Handler(), "sess_missing_upload", "acme", tok, []byte("x"), "text/plain")
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
