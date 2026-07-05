// SPDX-License-Identifier: MIT

package tokenanomaly_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/lennylabs/lenny/pkg/gateway/session/tokenanomaly"
	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// newDetector builds a detector against a fresh registry so each test reads
// its own counter series. The returned registry is the read side for
// anomalyCount.
func newDetector(t *testing.T, cfg tokenanomaly.Config) (*tokenanomaly.Detector, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	d, err := tokenanomaly.New(reg, cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d, reg
}

// anomalyCount reads the lenny_gateway_token_usage_anomaly_total value for a
// {tenant_id, reason} series from reg. A series that has never been
// incremented returns 0.
func anomalyCount(t *testing.T, reg *prometheus.Registry, tenantID, reason string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, mf := range families {
		if mf.GetName() != "lenny_gateway_token_usage_anomaly_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			if labelValue(m, "tenant_id") == tenantID && labelValue(m, "reason") == reason {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func labelValue(m *dto.Metric, name string) string {
	for _, l := range m.GetLabel() {
		if l.GetName() == name {
			return l.GetValue()
		}
	}
	return ""
}

// observe feeds a per-call token sequence into the detector for one session.
func observe(d *tokenanomaly.Detector, tenantID, sessionID string, tokens []int64) {
	for _, tk := range tokens {
		// The detector sums input+output; put the whole delta in input.
		d.Observe(tenantID, sessionID, tk, 0)
	}
}

// spec: §11.2 (direct-mode zero_delta anomaly) — the detector fires zero_delta
// once the consecutive-zero-token run exceeds the window (default greater than
// 3), i.e. on the 4th consecutive zero, and not before. F-11.2.20.
func TestZeroDeltaFiresAtBoundary_spec_11_2(t *testing.T) {
	d, reg := newDetector(t, tokenanomaly.Config{})

	// Three consecutive zero pulls: at the boundary, not over it.
	observe(d, "acme", "s_boundary", []int64{0, 0, 0})
	if got := anomalyCount(t, reg, "acme", "zero_delta"); got != 0 {
		t.Fatalf("zero_delta fired at 3 consecutive zeros (window default 3): got %v, want 0", got)
	}

	// The 4th consecutive zero exceeds the window and fires exactly once.
	d.Observe("acme", "s_boundary", 0, 0)
	if got := anomalyCount(t, reg, "acme", "zero_delta"); got != 1 {
		t.Fatalf("zero_delta did not fire on the 4th consecutive zero: got %v, want 1", got)
	}

	// A further zero must not re-increment: the series is a signal of distinct
	// anomalies, not a running tally of pulls.
	d.Observe("acme", "s_boundary", 0, 0)
	if got := anomalyCount(t, reg, "acme", "zero_delta"); got != 1 {
		t.Fatalf("zero_delta re-incremented after firing: got %v, want 1", got)
	}
}

// spec: §11.2 — a non-zero delta interspersed in a longer zero-delta sequence
// resets the consecutive-zero counter, so [0,0,0,nonzero,0,0,0] never exceeds
// the window and does not fire zero_delta. This pins the consecutiveness
// semantics: an implementation that counted non-consecutive zeros would raise a
// false anomaly. F-11.2.20.
func TestNonZeroDeltaResetsConsecutiveZero_spec_11_2(t *testing.T) {
	d, reg := newDetector(t, tokenanomaly.Config{})

	// 3 zeros, one non-zero reset, then 3 more zeros: never 4 in a row.
	observe(d, "acme", "s_reset", []int64{0, 0, 0, 100, 0, 0, 0})

	if got := anomalyCount(t, reg, "acme", "zero_delta"); got != 0 {
		t.Fatalf("zero_delta fired for an interrupted zero sequence [0,0,0,nonzero,0,0,0]: got %v, want 0", got)
	}
	// The non-zero pull kept the ratio healthy, so implausibly_small must not
	// fire either.
	if got := anomalyCount(t, reg, "acme", "implausibly_small"); got != 0 {
		t.Fatalf("implausibly_small fired for a healthy-ratio session: got %v, want 0", got)
	}
}

// spec: §11.2 — a healthy token stream (every pull reports tokens) fires no
// anomaly.
func TestHealthyStreamDoesNotFire_spec_11_2(t *testing.T) {
	d, reg := newDetector(t, tokenanomaly.Config{})

	observe(d, "acme", "s_healthy", []int64{500, 400, 600, 550, 480, 520})

	if got := anomalyCount(t, reg, "acme", "zero_delta"); got != 0 {
		t.Fatalf("zero_delta fired for a healthy stream: got %v, want 0", got)
	}
	if got := anomalyCount(t, reg, "acme", "implausibly_small"); got != 0 {
		t.Fatalf("implausibly_small fired for a healthy stream: got %v, want 0", got)
	}
}

// spec: §11.2 (implausibly_small anomaly) — the ratio branch fires when the
// cumulative tokens-per-call ratio falls below the configured threshold once a
// session has enough calls to form a stable average. F-11.2.20.
func TestImplausiblySmallFiresAtThreshold_spec_11_2(t *testing.T) {
	// Threshold of 10 tokens/call; each pull reports 1 token, mixing in
	// non-zero pulls so zero_delta cannot fire and steal the classification.
	d, reg := newDetector(t, tokenanomaly.Config{ImplausiblySmallRatio: 10})

	// Below the implausibleMinCalls evidence floor: no fire yet.
	observe(d, "acme", "s_impl", []int64{1, 1, 1})
	if got := anomalyCount(t, reg, "acme", "implausibly_small"); got != 0 {
		t.Fatalf("implausibly_small fired before the evidence floor: got %v, want 0", got)
	}

	// The 4th call gives a stable 1-token/call average, below the threshold.
	d.Observe("acme", "s_impl", 1, 0)
	if got := anomalyCount(t, reg, "acme", "implausibly_small"); got != 1 {
		t.Fatalf("implausibly_small did not fire at a 1-token/call ratio (threshold 10): got %v, want 1", got)
	}
	// It must not fire zero_delta: no pull reported zero tokens.
	if got := anomalyCount(t, reg, "acme", "zero_delta"); got != 0 {
		t.Fatalf("zero_delta fired for a non-zero-but-small stream: got %v, want 0", got)
	}
}

// spec: §11.2 — a non-positive implausibly-small threshold disables the ratio
// branch; the zero-token window still fires.
func TestImplausibleRatioDisabled_spec_11_2(t *testing.T) {
	d, reg := newDetector(t, tokenanomaly.Config{ImplausiblySmallRatio: 0})

	observe(d, "acme", "s_disabled", []int64{1, 1, 1, 1, 1, 1})
	if got := anomalyCount(t, reg, "acme", "implausibly_small"); got != 0 {
		t.Fatalf("implausibly_small fired with the ratio branch disabled: got %v, want 0", got)
	}
}

// spec: §11.2 — a non-positive zero-token window falls back to the default so a
// zeroed flag never disables the primary under-reporting signal.
func TestZeroWindowDefaultsOnNonPositive_spec_11_2(t *testing.T) {
	d, reg := newDetector(t, tokenanomaly.Config{ZeroTokenWindow: 0})

	observe(d, "acme", "s_defaultwin", []int64{0, 0, 0, 0})
	if got := anomalyCount(t, reg, "acme", "zero_delta"); got != 1 {
		t.Fatalf("zero_delta did not fire with a zeroed window (should default to %d): got %v, want 1",
			tokenanomaly.DefaultZeroTokenWindow, got)
	}
}

// spec: §16.1.1 — the emitted counter carries only the compliant {tenant_id,
// reason} label set, so New succeeds; a forbidden session_id label would fail
// Validate. This pins the §16.1.1 forbidden-label correction the proposal
// makes: the metric is not labeled by session_id. F-11.2.20.
func TestCounterLabelSetIsCompliant_spec_16_1_1(t *testing.T) {
	if err := metrics.Validate("lenny_gateway_token_usage_anomaly_total", []string{"tenant_id", "reason"}); err != nil {
		t.Fatalf("the compliant {tenant_id, reason} label set failed Validate: %v", err)
	}
	// The pre-fix §11.2:42 label set included session_id, which §16.1.1
	// forbids: assert it would fail so a regression that re-adds it is caught.
	if err := metrics.Validate("lenny_gateway_token_usage_anomaly_total", []string{"session_id", "tenant_id"}); err == nil {
		t.Fatal("a session_id label passed Validate; §16.1.1 forbids it")
	}
}

// spec: §11.2 — per-session state is independent: a zero-delta run on one
// session does not fire the anomaly for another tenant's session.
func TestPerSessionIsolation_spec_11_2(t *testing.T) {
	d, reg := newDetector(t, tokenanomaly.Config{})

	observe(d, "acme", "s_a", []int64{0, 0, 0, 0}) // fires zero_delta for acme
	observe(d, "globex", "s_b", []int64{9, 9, 9, 9})

	if got := anomalyCount(t, reg, "acme", "zero_delta"); got != 1 {
		t.Fatalf("acme zero_delta: got %v, want 1", got)
	}
	if got := anomalyCount(t, reg, "globex", "zero_delta"); got != 0 {
		t.Fatalf("globex healthy session fired zero_delta: got %v, want 0", got)
	}
}

// spec: §11.2 — Forget drops a session's accumulated state, and Observe/Forget
// are nil-safe so a disabled detector is a no-op.
func TestForgetAndNilSafety_spec_11_2(t *testing.T) {
	d, reg := newDetector(t, tokenanomaly.Config{})

	observe(d, "acme", "s_forget", []int64{0, 0, 0})
	d.Forget("s_forget")
	// After Forget the run restarts, so 3 more zeros stay at the boundary.
	observe(d, "acme", "s_forget", []int64{0, 0, 0})
	if got := anomalyCount(t, reg, "acme", "zero_delta"); got != 0 {
		t.Fatalf("zero_delta fired after Forget reset the consecutive-zero run: got %v, want 0", got)
	}

	var nilDet *tokenanomaly.Detector
	nilDet.Observe("acme", "s", 0, 0) // must not panic
	nilDet.Forget("s")                // must not panic
	// An empty session id is ignored.
	d.Observe("acme", "", 0, 0)
}

// spec: §11.2 — the detector is fed from per-session direct-mode usage loops
// that run concurrently, so concurrent Observe and Forget calls across many
// sessions must be -race clean. F-11.2.20.
func TestConcurrentObserveIsRaceClean_spec_11_2(t *testing.T) {
	d, reg := newDetector(t, tokenanomaly.Config{ImplausiblySmallRatio: 1})

	const sessions = 32
	var wg sync.WaitGroup
	for i := 0; i < sessions; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sid := fmt.Sprintf("s_%d", id)
			// Every session runs an all-zero stream long enough to fire.
			observe(d, "acme", sid, []int64{0, 0, 0, 0, 0})
			d.Forget(sid)
		}(i)
	}
	wg.Wait()

	// Each session fires zero_delta exactly once, so the tenant series equals
	// the session count regardless of interleaving.
	if got := anomalyCount(t, reg, "acme", "zero_delta"); got != float64(sessions) {
		t.Fatalf("zero_delta count under concurrency: got %v, want %d", got, sessions)
	}
}
