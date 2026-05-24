// SPDX-License-Identifier: MIT

package miniostore

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
)

// spec: §12.5 line 282 — transient transport-class PutObject failures
// are retried on the exponential-backoff schedule (1s, 5s, 30s) and
// terminal errors short-circuit the retry budget.
func TestIsTransientPutError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"timeout via net.Error", &netTimeout{}, true},
		{"deadline exceeded", context.DeadlineExceeded, true},
		{"503 service unavailable", minio.ErrorResponse{StatusCode: http.StatusServiceUnavailable}, true},
		{"504 gateway timeout", minio.ErrorResponse{StatusCode: http.StatusGatewayTimeout}, true},
		{"502 bad gateway", minio.ErrorResponse{StatusCode: http.StatusBadGateway}, true},
		{"500 internal", minio.ErrorResponse{StatusCode: http.StatusInternalServerError}, true},
		{"429 too many", minio.ErrorResponse{StatusCode: http.StatusTooManyRequests}, true},
		{"transport-class (no HTTP response)", errors.New("dial tcp 1.2.3.4:9000: connect: connection refused"), true},
		{"403 forbidden", minio.ErrorResponse{StatusCode: http.StatusForbidden}, false},
		{"401 unauthorized", minio.ErrorResponse{StatusCode: http.StatusUnauthorized}, false},
		{"413 entity too large", minio.ErrorResponse{StatusCode: http.StatusRequestEntityTooLarge}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransientPutError(tc.err); got != tc.want {
				t.Errorf("isTransientPutError = %v, want %v", got, tc.want)
			}
		})
	}
}

// spec: §16.5 ArtifactUploadError — error_type label values are
// bounded: auth, quota, transport, other.
func TestClassifyPutError(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{minio.ErrorResponse{StatusCode: http.StatusForbidden}, "auth"},
		{minio.ErrorResponse{StatusCode: http.StatusUnauthorized}, "auth"},
		{minio.ErrorResponse{Code: "AccessDenied"}, "auth"},
		{minio.ErrorResponse{Code: "InvalidAccessKeyId"}, "auth"},
		{minio.ErrorResponse{Code: "QuotaExceeded"}, "quota"},
		{minio.ErrorResponse{Code: "EntityTooLarge"}, "quota"},
		{minio.ErrorResponse{StatusCode: http.StatusInsufficientStorage}, "quota"},
		{minio.ErrorResponse{Code: "Unknown"}, "other"},
	}
	for _, tc := range cases {
		got := classifyPutError(tc.err)
		if got != tc.want {
			t.Errorf("classifyPutError(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

// netTimeout is a minimal net.Error returning Timeout()=true so the
// transient-classifier test does not depend on a real network call.
type netTimeout struct{}

func (netTimeout) Error() string   { return "i/o timeout" }
func (netTimeout) Timeout() bool   { return true }
func (netTimeout) Temporary() bool { return true }

var _ net.Error = netTimeout{}

// spec: §12.8 line 735 — the durable §12.5 catalog row drives the
// per-session legal-hold guard, replacing the in-process sync.Map
// fallback whenever the catalog is wired. DeleteBySession refuses a
// session whose catalog reports legal_hold=true.
func TestSetCatalogWiresDurableLegalHoldReader(t *testing.T) {
	fake := &fakeLegalHoldCatalog{held: map[string]bool{"acme|s_held": true}}
	s := &Store{clock: func() time.Time { return time.Now().UTC() }}
	s.SetCatalog(fake)
	if s.catalog == nil {
		t.Fatal("SetCatalog did not wire the durable reader")
	}
	held, err := s.catalog.IsLegalHeldAt(context.Background(), "acme", "s_held")
	if err != nil {
		t.Fatalf("IsLegalHeldAt: %v", err)
	}
	if !held {
		t.Error("catalog should report acme/s_held as held")
	}
	held, _ = s.catalog.IsLegalHeldAt(context.Background(), "acme", "s_free")
	if held {
		t.Error("catalog should report acme/s_free as not held")
	}
}

type fakeLegalHoldCatalog struct {
	held map[string]bool
}

func (f *fakeLegalHoldCatalog) IsLegalHeldAt(_ context.Context, tenantID, sessionID string) (bool, error) {
	return f.held[tenantID+"|"+sessionID], nil
}
