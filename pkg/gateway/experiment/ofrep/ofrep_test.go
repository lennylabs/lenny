// SPDX-License-Identifier: MIT

package ofrep_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/experiment/ofrep"
)

func newClient(t *testing.T, baseURL string) *ofrep.Client {
	t.Helper()
	c, err := ofrep.New(ofrep.Options{BaseURL: baseURL})
	if err != nil {
		t.Fatalf("ofrep.New: %v", err)
	}
	return c
}

func TestEvaluateSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":"exp-flag","value":"treatment",` +
			`"variant":"treatment","reason":"TARGETING_MATCH"}`))
	}))
	defer srv.Close()

	res, err := newClient(t, srv.URL).Evaluate(context.Background(), "exp-flag",
		map[string]any{"targetingKey": "alice"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Variant != "treatment" || res.Value != "treatment" || res.Reason != "TARGETING_MATCH" {
		t.Errorf("Result = %+v", res)
	}
}

func TestEvaluatePostsToFlagEndpoint(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		_, _ = w.Write([]byte(`{"variant":"control","reason":"DEFAULT"}`))
	}))
	defer srv.Close()

	_, err := newClient(t, srv.URL).Evaluate(context.Background(), "my-exp",
		map[string]any{"targetingKey": "bob"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/ofrep/v1/evaluate/flags/my-exp" {
		t.Errorf("path = %q, want /ofrep/v1/evaluate/flags/my-exp", gotPath)
	}
	if ctx, _ := gotBody["context"].(map[string]any); ctx["targetingKey"] != "bob" {
		t.Errorf("request context = %v, want the targeting context", gotBody["context"])
	}
}

func TestEvaluateProviderErrorCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"key":"ghost","errorCode":"FLAG_NOT_FOUND","errorDetails":"no such flag"}`))
	}))
	defer srv.Close()

	_, err := newClient(t, srv.URL).Evaluate(context.Background(), "ghost", nil)
	var ee *ofrep.EvalError
	if !errors.As(err, &ee) {
		t.Fatalf("err = %v, want *EvalError", err)
	}
	if ee.Code != "FLAG_NOT_FOUND" {
		t.Errorf("Code = %q, want FLAG_NOT_FOUND", ee.Code)
	}
}

func TestEvaluateNon2xxWithoutErrorCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	_, err := newClient(t, srv.URL).Evaluate(context.Background(), "x", nil)
	var ee *ofrep.EvalError
	if !errors.As(err, &ee) {
		t.Fatalf("err = %v, want *EvalError", err)
	}
	if ee.Status != http.StatusInternalServerError {
		t.Errorf("Status = %d, want 500", ee.Status)
	}
}

func TestEvaluateParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	_, err := newClient(t, srv.URL).Evaluate(context.Background(), "x", nil)
	var ee *ofrep.EvalError
	if !errors.As(err, &ee) || ee.Code != "PARSE_ERROR" {
		t.Errorf("err = %v, want *EvalError with PARSE_ERROR", err)
	}
}

func TestEvaluateTransportFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := srv.URL
	srv.Close() // the endpoint is now unreachable

	_, err := newClient(t, addr).Evaluate(context.Background(), "x", nil)
	if err == nil {
		t.Fatal("Evaluate against a closed server should fail")
	}
	var ee *ofrep.EvalError
	if errors.As(err, &ee) {
		t.Error("a transport failure should not be classified as an *EvalError")
	}
}

func TestEvaluateSendsConfiguredHeaders(t *testing.T) {
	var gotAuth, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		_, _ = w.Write([]byte(`{"variant":"control"}`))
	}))
	defer srv.Close()

	c, err := ofrep.New(ofrep.Options{
		BaseURL: srv.URL,
		Headers: map[string]string{"Authorization": "Bearer tok-1", "Content-Type": "text/plain"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Evaluate(context.Background(), "x", nil); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if gotAuth != "Bearer tok-1" {
		t.Errorf("Authorization header = %q, want the configured Bearer tok-1", gotAuth)
	}
	// A configured Content-Type must not override the protocol header.
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json (a config header cannot break it)", gotContentType)
	}
}

func TestNewRejectsEmptyBaseURL(t *testing.T) {
	if _, err := ofrep.New(ofrep.Options{}); err == nil {
		t.Error("New must reject an empty BaseURL")
	}
}

func TestNewTrimsTrailingSlash(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"variant":"control"}`))
	}))
	defer srv.Close()

	c, err := ofrep.New(ofrep.Options{BaseURL: srv.URL + "/"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Evaluate(context.Background(), "x", nil); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if gotPath != "/ofrep/v1/evaluate/flags/x" {
		t.Errorf("path = %q — a trailing slash on BaseURL was not trimmed", gotPath)
	}
}
