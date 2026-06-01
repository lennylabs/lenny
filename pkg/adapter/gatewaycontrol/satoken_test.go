// SPDX-License-Identifier: MIT

package gatewaycontrol

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
)

// spec: §10.3 line 334 — the projected SA token is read from disk and
// attached as the authorization bearer header on each call.
func TestSATokenCredentialsAttachesBearer_spec_10_3_334(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("  jwt-value\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	creds := NewSATokenCredentials(path)
	md, err := creds.GetRequestMetadata(context.Background())
	if err != nil {
		t.Fatalf("GetRequestMetadata: %v", err)
	}
	if got := md["authorization"]; got != "Bearer jwt-value" {
		t.Fatalf("authorization = %q, want trimmed Bearer header", got)
	}
	if !creds.RequireTransportSecurity() {
		t.Fatal("the bearer token must only travel over a secure transport")
	}
}

// A re-read picks up a kubelet token refresh without reconstructing the
// credential. spec: §10.3 line 334.
func TestSATokenCredentialsRereadsOnRefresh_spec_10_3_334(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("v1"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	creds := NewSATokenCredentials(path)
	if md, _ := creds.GetRequestMetadata(context.Background()); md["authorization"] != "Bearer v1" {
		t.Fatalf("first read: %v", md)
	}
	if err := os.WriteFile(path, []byte("v2"), 0o600); err != nil {
		t.Fatalf("rewrite token: %v", err)
	}
	if md, _ := creds.GetRequestMetadata(context.Background()); md["authorization"] != "Bearer v2" {
		t.Fatalf("second read did not pick up the refreshed token: %v", md)
	}
}

func TestSATokenCredentialsErrors(t *testing.T) {
	if _, err := NewSATokenCredentials("/no/such/token").GetRequestMetadata(context.Background()); err == nil {
		t.Fatal("expected an error for a missing token file")
	}
	dir := t.TempDir()
	empty := filepath.Join(dir, "token")
	if err := os.WriteFile(empty, []byte("  \n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := NewSATokenCredentials(empty).GetRequestMetadata(context.Background()); err == nil {
		t.Fatal("expected an error for an empty token file")
	}
}

// WithSAToken returns a no-op dial option when no path is configured so
// the local-development plaintext path is unaffected.
func TestWithSATokenEmptyPathIsNoOp(t *testing.T) {
	if _, ok := WithSAToken("").(grpc.EmptyDialOption); !ok {
		t.Fatal("WithSAToken(\"\") must return an EmptyDialOption")
	}
}
