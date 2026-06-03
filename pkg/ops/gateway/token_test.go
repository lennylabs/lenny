// SPDX-License-Identifier: MIT

package gateway_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/gateway"
)

// mintToken builds an unsigned three-segment JWT carrying the given exp
// (zero exp omits the claim). parseTokenExpiry decodes only the payload
// segment, so the header and signature are placeholders.
func mintToken(t *testing.T, exp time.Time) string {
	t.Helper()
	claims := map[string]any{"sub": "lenny-ops-sa"}
	if !exp.IsZero() {
		claims["exp"] = exp.Unix()
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// recordingRefreshMetrics counts refresh outcomes by status.
type recordingRefreshMetrics struct {
	mu     sync.Mutex
	counts map[string]int
}

func (r *recordingRefreshMetrics) RefreshDone(status string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.counts == nil {
		r.counts = map[string]int{}
	}
	r.counts[status]++
}

func (r *recordingRefreshMetrics) count(status string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[status]
}

// TestRefreshingTokenSource_RefreshesBeforeExpiry covers §25.4 line 1958:
// the source pre-emptively reloads the token once it is within
// RefreshBeforeExpiry of its exp claim.
func TestRefreshingTokenSource_RefreshesBeforeExpiry_spec_25_4(t *testing.T) {
	now := time.Unix(1_000_000, 0).UTC()
	var loads int
	metrics := &recordingRefreshMetrics{}
	src, err := gateway.NewRefreshingTokenSource(gateway.TokenSourceConfig{
		Loader: func() (string, error) {
			loads++
			return mintToken(t, now.Add(10*time.Minute)), nil
		},
		RefreshBeforeExpiry: 2 * time.Minute,
		Metrics:             metrics,
		Now:                 func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewRefreshingTokenSource: %v", err)
	}
	if loads != 1 {
		t.Fatalf("startup loads = %d, want 1", loads)
	}
	// Within the validity window: no refresh.
	if _, err := src.Token(context.Background()); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if loads != 1 {
		t.Fatalf("loads after in-window Token = %d, want 1", loads)
	}
	// Advance to inside the refresh-before window (8m30s in, expiry at
	// 10m, window opens at 8m): a refresh fires.
	now = now.Add(8*time.Minute + 30*time.Second)
	if _, err := src.Token(context.Background()); err != nil {
		t.Fatalf("Token after window: %v", err)
	}
	if loads != 2 {
		t.Fatalf("loads after pre-emptive refresh = %d, want 2", loads)
	}
	if metrics.count("success") != 2 {
		t.Fatalf("success refreshes = %d, want 2", metrics.count("success"))
	}
}

// TestRefreshingTokenSource_MinTTLFloor covers §25.4 line 1959: a startup
// token whose remaining lifetime is below the floor is rejected.
func TestRefreshingTokenSource_MinTTLFloor_spec_25_4(t *testing.T) {
	now := time.Unix(2_000_000, 0).UTC()
	_, err := gateway.NewRefreshingTokenSource(gateway.TokenSourceConfig{
		Loader:      func() (string, error) { return mintToken(t, now.Add(30*time.Second)), nil },
		MinTokenTTL: 5 * time.Minute,
		Now:         func() time.Time { return now },
	})
	if err == nil {
		t.Fatal("expected an error for a startup token below the minTokenTTL floor")
	}
}

// TestRefreshingTokenSource_MarkRevoked covers the §25.4 401-triggered
// revocation path: a revoked token reloads on the next call.
func TestRefreshingTokenSource_MarkRevoked_spec_25_4(t *testing.T) {
	now := time.Unix(3_000_000, 0).UTC()
	var loads int
	src, err := gateway.NewRefreshingTokenSource(gateway.TokenSourceConfig{
		Loader: func() (string, error) {
			loads++
			return mintToken(t, now.Add(time.Hour)), nil
		},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewRefreshingTokenSource: %v", err)
	}
	src.MarkRevoked()
	if _, err := src.Token(context.Background()); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if loads != 2 {
		t.Fatalf("loads after revocation = %d, want 2", loads)
	}
}

// TestRefreshingTokenSource_NoExpClaim covers a token without an exp
// claim: it never auto-refreshes and is not rejected by the TTL floor.
func TestRefreshingTokenSource_NoExpClaim(t *testing.T) {
	var loads int
	src, err := gateway.NewRefreshingTokenSource(gateway.TokenSourceConfig{
		Loader: func() (string, error) {
			loads++
			return mintToken(t, time.Time{}), nil
		},
		MinTokenTTL: time.Hour,
	})
	if err != nil {
		t.Fatalf("NewRefreshingTokenSource: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := src.Token(context.Background()); err != nil {
			t.Fatalf("Token: %v", err)
		}
	}
	if loads != 1 {
		t.Fatalf("loads for non-expiring token = %d, want 1", loads)
	}
}

// TestRefreshingTokenSource_LoaderError surfaces a loader failure as a
// refresh failure metric and a returned error.
func TestRefreshingTokenSource_LoaderError(t *testing.T) {
	metrics := &recordingRefreshMetrics{}
	_, err := gateway.NewRefreshingTokenSource(gateway.TokenSourceConfig{
		Loader:  func() (string, error) { return "", fmt.Errorf("no token file") },
		Metrics: metrics,
	})
	if err == nil {
		t.Fatal("expected a loader error")
	}
	if metrics.count("failure") != 1 {
		t.Fatalf("failure refreshes = %d, want 1", metrics.count("failure"))
	}
}

// TestFileTokenLoader reads and trims the token file the kubelet rotates
// in place.
func TestFileTokenLoader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("  the-token\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	tok, err := gateway.FileTokenLoader(path)()
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	if tok != "the-token" {
		t.Fatalf("token = %q, want %q", tok, "the-token")
	}
	if _, err := gateway.FileTokenLoader(filepath.Join(dir, "absent"))(); err == nil {
		t.Fatal("expected an error for a missing token file")
	}
}
