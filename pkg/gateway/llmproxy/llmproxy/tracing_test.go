// SPDX-License-Identifier: MIT

package llmproxy_test

import (
	"net/http"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// installSpanRecorder swaps the global OTel TracerProvider for an
// SDK-backed recorder so a test can read every span the proxy handler
// emitted, then restores the prior provider when the test ends.
// spec: §16.3 line 354 (credential.proxy_request).
func installSpanRecorder(t *testing.T) (*tracetest.SpanRecorder, func()) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	return rec, func() { otel.SetTracerProvider(prev) }
}

func proxySpan(spans []sdktrace.ReadOnlySpan) (sdktrace.ReadOnlySpan, bool) {
	for _, s := range spans {
		if s.Name() == "credential.proxy_request" {
			return s, true
		}
	}
	return nil, false
}

func TestCredentialProxyRequestSpan_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	h := newProxyHarness(t)
	if err := h.leases.Put(handlerLease("lt-trace")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	rr := post(h.handler, "lt-trace", messagesBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("proxy: %d, body=%s", rr.Code, rr.Body.String())
	}

	span, ok := proxySpan(rec.Ended())
	if !ok {
		t.Fatal("credential.proxy_request span was not emitted on the happy path")
	}
	if span.Status().Code == codes.Error {
		t.Errorf("happy-path span carries an error status: %q", span.Status().Description)
	}
}

func TestCredentialProxyRequestSpanRecordsError_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	// A request with no lease token is rejected with 401 before any
	// upstream call; the span records the categorized rejection.
	h := newProxyHarness(t)
	rr := post(h.handler, "", messagesBody)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("missing token: %d, want 401", rr.Code)
	}

	span, ok := proxySpan(rec.Ended())
	if !ok {
		t.Fatal("credential.proxy_request span was not emitted on the error path")
	}
	if span.Status().Code != codes.Error {
		t.Errorf("error-path span status = %v, want Error", span.Status().Code)
	}
}

func TestCredentialProxyRequestSpanRecordsUpstreamError_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	// The gateway holds no upstream credential for the lease, so the
	// proxy returns 502 UPSTREAM_CREDENTIAL_UNAVAILABLE — a 5xx the span
	// records as an UPSTREAM-category error.
	h := newProxyHarness(t)
	h.handler.Credentials = fakeResolver{ok: false}
	if err := h.leases.Put(handlerLease("lt-noupstream")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	rr := post(h.handler, "lt-noupstream", messagesBody)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("no upstream credential: %d, want 502", rr.Code)
	}

	span, ok := proxySpan(rec.Ended())
	if !ok {
		t.Fatal("credential.proxy_request span was not emitted on the upstream-error path")
	}
	if span.Status().Code != codes.Error {
		t.Errorf("upstream-error span status = %v, want Error", span.Status().Code)
	}
	var foundCategory bool
	for _, a := range span.Attributes() {
		if string(a.Key) == "error.category" && a.Value.AsString() == "UPSTREAM" {
			foundCategory = true
		}
	}
	if !foundCategory {
		t.Errorf("upstream-error span missing error.category=UPSTREAM; attrs=%v", span.Attributes())
	}
}
