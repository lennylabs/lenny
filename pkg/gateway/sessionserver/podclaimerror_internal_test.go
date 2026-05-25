// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// decodeErrorBody unpacks the §15.2 error envelope into its inner body
// so the mapping assertions can read code, category, retryable, and the
// details block.
func decodeErrorBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var env struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, body)
	}
	return env.Error
}

// spec: §5.2 line 519 — pod and slot exhaustion both map to
// WARM_POOL_EXHAUSTED, with details.reason distinguishing the cause:
// an empty pool is "no_idle_pods" and pods-exist-but-full is
// "concurrent_slots_exhausted". The errors.Is checks see through %w
// wrapping the binder adds.
func TestWritePodClaimErrorWarmPoolExhausted(t *testing.T) {
	s := New(memstore.New(), Options{})
	cases := []struct {
		name       string
		err        error
		wantReason string
	}{
		{"no idle pods", podclaim.ErrNoIdlePod, "no_idle_pods"},
		{"slots full", podclaim.ErrNoConcurrentSlot, "concurrent_slots_exhausted"},
		{"tenant mismatch", podclaim.ErrTenantMismatch, "concurrent_slots_exhausted"},
		{"wrapped no idle pods", fmt.Errorf("connect: %w", podclaim.ErrNoIdlePod), "no_idle_pods"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			s.writePodClaimError(w, tc.err, "could not place the session on a warm pod")
			if w.Code != 503 {
				t.Errorf("status = %d, want 503", w.Code)
			}
			body := decodeErrorBody(t, w.Body.Bytes())
			if body["code"] != "WARM_POOL_EXHAUSTED" {
				t.Errorf("code = %v, want WARM_POOL_EXHAUSTED", body["code"])
			}
			if body["category"] != "TRANSIENT" || body["retryable"] != true {
				t.Errorf("category/retryable = %v/%v, want TRANSIENT/true", body["category"], body["retryable"])
			}
			details, _ := body["details"].(map[string]any)
			if details["reason"] != tc.wantReason {
				t.Errorf("details.reason = %v, want %q", details["reason"], tc.wantReason)
			}
		})
	}
}

// spec: §5.2 lines 602-625 — a PoolWarmingError maps to the 503
// RUNTIME_UNAVAILABLE "Pool Not Ready" response with Retry-After and a
// details block carrying poolName, poolCondition, estimatedReadyIn, and
// podsWarming.
func TestWritePodClaimErrorPoolWarming(t *testing.T) {
	s := New(memstore.New(), Options{})
	w := httptest.NewRecorder()
	s.writePodClaimError(w, &podsession.PoolWarmingError{Pool: "acme-pool", PodsWarming: 3},
		"could not place the session on a warm pod")
	if w.Code != 503 {
		t.Errorf("status = %d, want 503", w.Code)
	}
	if ra := w.Header().Get("Retry-After"); ra != "120" {
		t.Errorf("Retry-After = %q, want 120 (max(30, default estimate))", ra)
	}
	body := decodeErrorBody(t, w.Body.Bytes())
	if body["code"] != "RUNTIME_UNAVAILABLE" || body["retryable"] != true {
		t.Errorf("code/retryable = %v/%v, want RUNTIME_UNAVAILABLE/true", body["code"], body["retryable"])
	}
	details, _ := body["details"].(map[string]any)
	if details["poolName"] != "acme-pool" || details["poolCondition"] != "PoolWarmingUp" {
		t.Errorf("details poolName/poolCondition = %v/%v, want acme-pool/PoolWarmingUp",
			details["poolName"], details["poolCondition"])
	}
	if details["estimatedReadyIn"] != float64(120) || details["podsWarming"] != float64(3) {
		t.Errorf("details estimatedReadyIn/podsWarming = %v/%v, want 120/3",
			details["estimatedReadyIn"], details["podsWarming"])
	}
}

// spec: §5.2 line 625 — Retry-After is max(30, estimatedWarmupSeconds);
// a sub-30s estimate still reports the true estimate in the body while
// the header floors at 30.
func TestWritePoolWarmingRetryAfterFloor(t *testing.T) {
	s := New(memstore.New(), Options{WarmupEstimateSeconds: 10})
	w := httptest.NewRecorder()
	s.writePoolWarming(w, &podsession.PoolWarmingError{Pool: "p"})
	if ra := w.Header().Get("Retry-After"); ra != "30" {
		t.Errorf("Retry-After = %q, want 30 (the §5.2 floor)", ra)
	}
	details, _ := decodeErrorBody(t, w.Body.Bytes())["details"].(map[string]any)
	if details["estimatedReadyIn"] != float64(10) {
		t.Errorf("estimatedReadyIn = %v, want 10 (the configured estimate, not the floored header)",
			details["estimatedReadyIn"])
	}
}

// spec: §15.1 — a claim failure that is neither exhaustion nor warming
// stays a retryable POD_CLAIM_FAILED carrying the call site's message.
func TestWritePodClaimErrorFallback(t *testing.T) {
	s := New(memstore.New(), Options{})
	w := httptest.NewRecorder()
	s.writePodClaimError(w, errors.New("boom"), "could not place the session on a warm pod")
	body := decodeErrorBody(t, w.Body.Bytes())
	if body["code"] != "POD_CLAIM_FAILED" {
		t.Errorf("code = %v, want POD_CLAIM_FAILED", body["code"])
	}
	if msg, _ := body["message"].(string); msg != "could not place the session on a warm pod: boom" {
		t.Errorf("message = %q, want the fallback message with the cause appended", msg)
	}
}
