// SPDX-License-Identifier: MIT

package stack

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestInstallReferenceRuntimesGrantFailureNamesRuntimes_spec_24_3
// asserts that when a reference-runtime tenant-access grant fails, the
// returned error names the failing runtime(s) rather than a bare count,
// so an operator with no §24.3 CLI retry loop can act on it. F-24.3.4.
func TestInstallReferenceRuntimesGrantFailureNamesRuntimes_spec_24_3(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/admin/bootstrap":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case strings.HasSuffix(r.URL.Path, "/tenant-access"):
			// Fail every grant so the joined error names every runtime.
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":"FORBIDDEN","message":"nope"}}`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	err := installReferenceRuntimes(context.Background(), srv.URL, io.Discard)
	if err == nil {
		t.Fatal("expected an error when grants fail")
	}
	msg := err.Error()
	// The error must name a concrete failing runtime, not only a count.
	if !strings.Contains(msg, referenceRuntimes[0].Name) {
		t.Errorf("error does not name the failing runtime %q: %v", referenceRuntimes[0].Name, msg)
	}
	for _, rt := range referenceRuntimes {
		if !strings.Contains(msg, rt.Name) {
			t.Errorf("error omits failing runtime %q: %v", rt.Name, msg)
		}
	}
}

// TestInstallReferenceRuntimesAllGrantsSucceed_spec_24_3 asserts the
// happy path returns no error when every grant succeeds.
func TestInstallReferenceRuntimesAllGrantsSucceed_spec_24_3(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if err := installReferenceRuntimes(context.Background(), srv.URL, io.Discard); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

// TestInstallReferenceRuntimesWarnsOnPlaceholderDigest_spec_26_3 asserts
// the bootstrap output warns the operator that placeholder-pinned
// reference runtimes register but cannot start a session until re-pinned.
// F-26.3.6.
func TestInstallReferenceRuntimesWarnsOnPlaceholderDigest_spec_26_3(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	var out bytes.Buffer
	if err := installReferenceRuntimes(context.Background(), srv.URL, &out); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "[WARN]") || !strings.Contains(got, "placeholder image digest") {
		t.Errorf("output should warn about placeholder digests: %q", got)
	}
	// The whole catalog is placeholder-pinned today, so every runtime is named.
	for _, rt := range referenceRuntimes {
		if !strings.Contains(got, rt.Name) {
			t.Errorf("warning omits placeholder-pinned runtime %q: %q", rt.Name, got)
		}
	}
	if !strings.Contains(got, "lenny image import") {
		t.Errorf("warning should point at the remediation: %q", got)
	}
}

// TestPlaceholderPinnedRuntimes_spec_26_3 covers the digest detector and
// the catalog scan it backs.
func TestPlaceholderPinnedRuntimes_spec_26_3(t *testing.T) {
	if !hasPlaceholderDigest("ghcr.io/lennylabs/runtime-chat:1.0.0" + placeholderDigest) {
		t.Error("hasPlaceholderDigest should detect the sentinel suffix")
	}
	if hasPlaceholderDigest("ghcr.io/lennylabs/runtime-chat@sha256:abc123") {
		t.Error("hasPlaceholderDigest should not flag a real digest")
	}
	pinned := placeholderPinnedRuntimes()
	if len(pinned) != len(referenceRuntimes) {
		t.Errorf("every catalog entry is placeholder-pinned today: got %d of %d", len(pinned), len(referenceRuntimes))
	}
}
