// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
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

// spec: §4.9 line 1476 — a runtime↔pool proxy-dialect mismatch surfaces
// as 422 INVALID_POOL_PROXY_DIALECT (PERMANENT, not retryable) carrying
// the offending pool and dialect in details and the verbatim spec
// message. The errors.As check sees through %w wrapping.
func TestWritePodClaimErrorInvalidProxyDialect_spec_4_9_1476(t *testing.T) {
	s := New(memstore.New(), Options{})
	base := &PoolProxyDialectError{Pool: "claude-prod", Dialect: "openai"}
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"direct", base},
		{"wrapped", fmt.Errorf("resolve credential pools: %w", base)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			s.writePodClaimError(w, tc.err, "SESSION_CREATION_FAILED",
				"could not place the session on a warm pod")
			if w.Code != 422 {
				t.Errorf("status = %d, want 422", w.Code)
			}
			body := decodeErrorBody(t, w.Body.Bytes())
			if body["code"] != "INVALID_POOL_PROXY_DIALECT" {
				t.Errorf("code = %v, want INVALID_POOL_PROXY_DIALECT", body["code"])
			}
			if body["category"] != "PERMANENT" || body["retryable"] != false {
				t.Errorf("category/retryable = %v/%v, want PERMANENT/false", body["category"], body["retryable"])
			}
			want := "pool proxyDialect openai is not declared in runtime credentialCapabilities.proxyDialect"
			if body["message"] != want {
				t.Errorf("message = %v, want %q", body["message"], want)
			}
			details, _ := body["details"].(map[string]any)
			if details["pool"] != "claude-prod" || details["proxyDialect"] != "openai" {
				t.Errorf("details = %v, want pool=claude-prod proxyDialect=openai", details)
			}
		})
	}
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
			s.writePodClaimError(w, tc.err, "SESSION_CREATION_FAILED",
				"could not place the session on a warm pod")
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
			// spec: §15.2.1 line 1017 / §4.6.1 — the WARM_POOL_EXHAUSTED
			// envelope carries a Retry-After header so a client backs off
			// with a deterministic budget (both the reject path and the
			// onPoolExhausted: queue timeout path).
			if w.Header().Get("Retry-After") == "" {
				t.Error("WARM_POOL_EXHAUSTED must carry a Retry-After header (§15.2.1, §4.6.1)")
			}
		})
	}
}

// spec: §7.1 line 23 (atomicity note) / §4.1 (proposal) — a create-time
// pod-claim exhaustion (claimAtCreate wraps ErrNoIdlePod as
// errCreateClaimExhausted) surfaces the create-handler fallback envelope
// SESSION_CREATION_FAILED, not the §5.2 WARM_POOL_EXHAUSTED code the
// two-step /start claim returns. The case precedes the bare-ErrNoIdlePod
// case because the wrapper embeds that sentinel; the errors.Is check sees
// through %w wrapping. The body carries details.reason=no_idle_pods so an
// operator can still see the underlying cause, and a Retry-After header.
func TestWritePodClaimErrorCreateClaimExhausted_spec_7_1_23(t *testing.T) {
	s := New(memstore.New(), Options{})
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"direct", errCreateClaimExhausted},
		{"wrapped sentinel", fmt.Errorf("%w: %w", errCreateClaimExhausted, podclaim.ErrNoIdlePod)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			s.writePodClaimError(w, tc.err, "SESSION_CREATION_FAILED",
				"could not place the session on a warm pod")
			if w.Code != 503 {
				t.Errorf("status = %d, want 503", w.Code)
			}
			body := decodeErrorBody(t, w.Body.Bytes())
			if body["code"] != "SESSION_CREATION_FAILED" {
				t.Errorf("code = %v, want SESSION_CREATION_FAILED (create-time exhaustion is the §7.1 atomicity envelope)", body["code"])
			}
			details, _ := body["details"].(map[string]any)
			if details["reason"] != "no_idle_pods" {
				t.Errorf("details.reason = %v, want no_idle_pods", details["reason"])
			}
			if w.Header().Get("Retry-After") == "" {
				t.Error("create-time exhaustion 503 must carry a Retry-After header (§15.1 line 1138)")
			}
		})
	}

	// The two-step /start path passes a bare ErrNoIdlePod (not wrapped),
	// which keeps the §5.2 WARM_POOL_EXHAUSTED code; this guards against the
	// create-time translation leaking into the start path's classifier.
	t.Run("bare sentinel keeps WARM_POOL_EXHAUSTED", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.writePodClaimError(w, podclaim.ErrNoIdlePod, "STARTING_FAILED",
			"could not place the session on a warm pod")
		body := decodeErrorBody(t, w.Body.Bytes())
		if body["code"] != "WARM_POOL_EXHAUSTED" {
			t.Errorf("code = %v, want WARM_POOL_EXHAUSTED for the two-step /start claim", body["code"])
		}
	})
}

// spec: §5.2 lines 602-625 — a PoolWarmingError maps to the 503
// RUNTIME_UNAVAILABLE "Pool Not Ready" response with Retry-After and a
// details block carrying poolName, poolCondition, estimatedReadyIn, and
// podsWarming.
func TestWritePodClaimErrorPoolWarming(t *testing.T) {
	s := New(memstore.New(), Options{})
	w := httptest.NewRecorder()
	s.writePodClaimError(w, &podsession.PoolWarmingError{Pool: "acme-pool", PodsWarming: 3},
		"SESSION_CREATION_FAILED", "could not place the session on a warm pod")
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

// spec: §7.1 line 28 / §15.1 line 1138 — a claim failure that is
// neither exhaustion nor warming surfaces as a retryable 503 carrying
// the caller-named fallback code (SESSION_CREATION_FAILED for atomic
// create / start, STARTING_FAILED for the two-step start, RESUME_FAILED
// for resume) plus a Retry-After header so clients back off with a
// deterministic budget.
func TestWritePodClaimErrorFallback_spec_7_1_4(t *testing.T) {
	cases := []struct {
		name string
		code string
	}{
		{"create_and_start", "SESSION_CREATION_FAILED"},
		{"two_step_start", "STARTING_FAILED"},
		{"resume", "RESUME_FAILED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(memstore.New(), Options{})
			w := httptest.NewRecorder()
			s.writePodClaimError(w, errors.New("boom"), tc.code, "could not place the session on a warm pod")
			body := decodeErrorBody(t, w.Body.Bytes())
			if body["code"] != tc.code {
				t.Errorf("code = %v, want %s", body["code"], tc.code)
			}
			if msg, _ := body["message"].(string); msg != "could not place the session on a warm pod: boom" {
				t.Errorf("message = %q, want the fallback message with the cause appended", msg)
			}
			// spec: §15.1 line 1138 — every retryable 503 carries Retry-After.
			if ra := w.Header().Get("Retry-After"); ra != "5" {
				t.Errorf("Retry-After = %q, want 5 (the §7.1 atomic-unit floor)", ra)
			}
		})
	}
}

// setupFailWith wraps a gRPC status code in the *podsession.SetupCommandFailure
// the binder produces, so the writePodClaimError / isTransientPodClaimError
// branch can be exercised at the gRPC-code boundary that governs both the
// wire envelope and the /resume row-state demotion.
func setupFailWith(code codes.Code) *podsession.SetupCommandFailure {
	return &podsession.SetupCommandFailure{
		Pod:   "sbx-1",
		Cause: status.Error(code, "run setup commands: boom"),
	}
}

// spec: §7.3 (setup_command_failed non-retryable), §15.1 (SETUP_COMMAND_FAILED),
// §6.2 (transient setup failure retried on a fresh pod) — a deterministic
// setup-command exit the adapter reports as codes.FailedPrecondition surfaces
// as the non-retryable 422 SETUP_COMMAND_FAILED with details.reason retained
// and no Retry-After, while every other gRPC code (the complement of
// FailedPrecondition: a crashed pod surfaced as Unavailable / DeadlineExceeded,
// a wrapped non-status cause reported as Unknown, and Internal) stays the
// retryable 503 fallback + Retry-After so §6.2 recovers it on a fresh pod.
func TestWritePodClaimErrorSetupCommandFailed_spec_7_3(t *testing.T) {
	s := New(memstore.New(), Options{})

	t.Run("FailedPrecondition is the non-retryable 422", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			err  error
		}{
			{"direct", setupFailWith(codes.FailedPrecondition)},
			{"wrapped", fmt.Errorf("prepare: %w", setupFailWith(codes.FailedPrecondition))},
		} {
			t.Run(tc.name, func(t *testing.T) {
				w := httptest.NewRecorder()
				s.writePodClaimError(w, tc.err, "SESSION_CREATION_FAILED",
					"could not place the session on a warm pod")
				if w.Code != 422 {
					t.Fatalf("status = %d, want 422", w.Code)
				}
				body := decodeErrorBody(t, w.Body.Bytes())
				if body["code"] != "SETUP_COMMAND_FAILED" {
					t.Errorf("code = %v, want SETUP_COMMAND_FAILED", body["code"])
				}
				if body["category"] != "PERMANENT" || body["retryable"] != false {
					t.Errorf("category/retryable = %v/%v, want PERMANENT/false", body["category"], body["retryable"])
				}
				details, _ := body["details"].(map[string]any)
				if details["reason"] != "setup_command_failed" {
					t.Errorf("details.reason = %v, want setup_command_failed", details["reason"])
				}
				// A non-retryable deterministic failure must not invite a retry.
				if ra := w.Header().Get("Retry-After"); ra != "" {
					t.Errorf("Retry-After = %q, want absent on the non-retryable SETUP_COMMAND_FAILED", ra)
				}
			})
		}
	})

	// Every code other than FailedPrecondition is a transient setup-window
	// transport failure that stays the retryable 503 fallback + Retry-After.
	t.Run("transient causes stay the retryable 503 fallback", func(t *testing.T) {
		cases := []struct {
			name string
			code codes.Code
			fall string
		}{
			{"unavailable_crashed_pod", codes.Unavailable, "SESSION_CREATION_FAILED"},
			{"deadline_exceeded", codes.DeadlineExceeded, "STARTING_FAILED"},
			{"unknown_wrapped_cause", codes.Unknown, "RESUME_FAILED"},
			{"internal", codes.Internal, "SESSION_CREATION_FAILED"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				w := httptest.NewRecorder()
				s.writePodClaimError(w, setupFailWith(tc.code), tc.fall,
					"could not place the session on a warm pod")
				if w.Code != 503 {
					t.Fatalf("status = %d, want 503 for the transient %s cause", w.Code, tc.code)
				}
				body := decodeErrorBody(t, w.Body.Bytes())
				if body["code"] != tc.fall {
					t.Errorf("code = %v, want the retryable fallback %s", body["code"], tc.fall)
				}
				if body["retryable"] != true {
					t.Errorf("retryable = %v, want true for the transient %s cause", body["retryable"], tc.code)
				}
				details, _ := body["details"].(map[string]any)
				if details["reason"] != "setup_command_failed" {
					t.Errorf("details.reason = %v, want setup_command_failed", details["reason"])
				}
				if ra := w.Header().Get("Retry-After"); ra != "5" {
					t.Errorf("Retry-After = %q, want 5 on the retryable fallback", ra)
				}
			})
		}
	})
}

// spec: §7.3 (awaiting_client_action holding state for a retryable resume
// failure), §6.2 (transient setup failure retried on a fresh pod) — the
// /resume row-state demotion and the wire envelope share one boundary:
// isTransientPodClaimError treats a SetupCommandFailure whose Cause is any
// code other than codes.FailedPrecondition as transient (row stays
// awaiting_client_action), and the deterministic FailedPrecondition exit as
// non-transient (row demotes to failed). A non-status / wrapped cause reports
// codes.Unknown and is treated as transient.
func TestIsTransientPodClaimErrorSetupCommand_spec_7_3(t *testing.T) {
	cases := []struct {
		name        string
		err         error
		wantTransit bool
	}{
		{"failed_precondition_deterministic", setupFailWith(codes.FailedPrecondition), false},
		{"unavailable_transient", setupFailWith(codes.Unavailable), true},
		{"deadline_exceeded_transient", setupFailWith(codes.DeadlineExceeded), true},
		{"internal_transient", setupFailWith(codes.Internal), true},
		{
			"non_status_cause_is_unknown_transient",
			&podsession.SetupCommandFailure{Pod: "sbx-1", Cause: errors.New("wrapped non-status boom")},
			true,
		},
		{"wrapped_failed_precondition_demotes", fmt.Errorf("resume: %w", setupFailWith(codes.FailedPrecondition)), false},
		{"wrapped_unavailable_resumable", fmt.Errorf("resume: %w", setupFailWith(codes.Unavailable)), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransientPodClaimError(tc.err); got != tc.wantTransit {
				t.Errorf("isTransientPodClaimError = %v, want %v", got, tc.wantTransit)
			}
		})
	}
}
