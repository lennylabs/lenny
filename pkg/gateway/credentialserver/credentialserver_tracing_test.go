// SPDX-License-Identifier: MIT

package credentialserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentialserver"
	"github.com/lennylabs/lenny/pkg/gateway/credentialstore"
)

// installSpanRecorder swaps the global OTel TracerProvider for an
// SDK-backed recorder so a test can read every span the handler under
// test emitted, then restores the prior provider when the test ends.
// spec: §16.3 line 352 (credential.rotate).
func installSpanRecorder(t *testing.T) (*tracetest.SpanRecorder, func()) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	return rec, func() { otel.SetTracerProvider(prev) }
}

// spanByName returns the first recorded span with the given name.
func spanByName(spans []sdktrace.ReadOnlySpan, name string) (sdktrace.ReadOnlySpan, bool) {
	for _, s := range spans {
		if s.Name() == name {
			return s, true
		}
	}
	return nil, false
}

// rotateFailStore is a Store whose Rotate always fails after the owning
// Get succeeds, so the credential.rotate error path is exercised. It
// embeds Memory for every other method (Get, used by ownedBy, included).
type rotateFailStore struct {
	*credentialstore.Memory
}

func (s rotateFailStore) Rotate(context.Context, string, string, string) (credentialstore.Credential, error) {
	return credentialstore.Credential{}, errors.New("store rotate boom")
}

func TestCredentialRotateSpan_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	store := credentialstore.NewMemory(nil)
	srv := credentialserver.New(store)
	c, err := store.Register(context.Background(), "acme", "alice", credential.ProviderGitHub, "", "old")
	if err != nil {
		t.Fatalf("seed credential: %v", err)
	}

	body, _ := json.Marshal(credentialserver.RotateRequest{Secret: "new-secret"})
	req := asUser(httptest.NewRequest(http.MethodPut, "/v1/credentials/"+c.Ref, bytes.NewReader(body)), "acme", "alice")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("rotate: %d, body=%s", rr.Code, rr.Body.String())
	}

	span, ok := spanByName(rec.Ended(), "credential.rotate")
	if !ok {
		t.Fatal("credential.rotate span was not emitted on the happy path")
	}
	if span.Status().Code == codes.Error {
		t.Errorf("happy-path span carries an error status: %q", span.Status().Description)
	}
	// §16.4 line 376: the rotate RPC's secret must never be a span
	// attribute.
	for _, a := range span.Attributes() {
		if a.Value.Type() == attribute.STRING && bytes.Contains([]byte(a.Value.AsString()), []byte("new-secret")) {
			t.Fatalf("SECURITY: credential.rotate span leaked the secret in attribute %q", a.Key)
		}
	}
}

func TestCredentialRotateSpanRecordsError_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	mem := credentialstore.NewMemory(nil)
	c, err := mem.Register(context.Background(), "acme", "alice", credential.ProviderGitHub, "", "old")
	if err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	srv := credentialserver.New(rotateFailStore{Memory: mem})

	body, _ := json.Marshal(credentialserver.RotateRequest{Secret: "new-secret"})
	req := asUser(httptest.NewRequest(http.MethodPut, "/v1/credentials/"+c.Ref, bytes.NewReader(body)), "acme", "alice")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("rotate error: %d, want 500", rr.Code)
	}

	span, ok := spanByName(rec.Ended(), "credential.rotate")
	if !ok {
		t.Fatal("credential.rotate span was not emitted on the error path")
	}
	if span.Status().Code != codes.Error {
		t.Errorf("error-path span status = %v, want Error", span.Status().Code)
	}
}
