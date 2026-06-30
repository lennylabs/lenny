// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeLevelProbe is a stand-in for the adapter's observed-level RPC so the
// §5.1 admission check can be exercised without a live adapter.
type fakeLevelProbe struct {
	level string
	err   error
	calls int
}

func (f *fakeLevelProbe) GetObservedIntegrationLevel(_ context.Context, _ int32) (string, error) {
	f.calls++
	return f.level, f.err
}

// spec: §5.1 lines 41-44 — basic < standard < full; an empty declared
// level is the default basic; an unrecognized level ranks below every
// named level.
func TestIntegrationLevelRank_spec_5_1(t *testing.T) {
	cases := map[string]int{"": 1, "basic": 1, "standard": 2, "full": 3, "bogus": 0}
	for level, want := range cases {
		if got := integrationLevelRank(level); got != want {
			t.Errorf("integrationLevelRank(%q) = %d, want %d", level, got, want)
		}
	}
	if normalizeLevel("") != "basic" {
		t.Errorf("normalizeLevel(\"\") = %q, want basic", normalizeLevel(""))
	}
	if normalizeLevel("full") != "full" {
		t.Errorf("normalizeLevel(full) = %q, want full", normalizeLevel("full"))
	}
}

// spec: §5.1 line 42 — observed < declared rejects the assignment with
// RUNTIME_LEVEL_UNDERPERFORMS, and the runtime is not recorded as verified
// so every later assignment keeps being rejected.
func TestVerifyIntegrationLevelUnderperforms_spec_5_1(t *testing.T) {
	b := &Binder{}
	probe := &fakeLevelProbe{level: "basic"}
	err := b.verifyIntegrationLevel(context.Background(), probe, "claude-code", "full")
	var underperf *RuntimeLevelUnderperforms
	if !errors.As(err, &underperf) {
		t.Fatalf("err = %v, want *RuntimeLevelUnderperforms", err)
	}
	if underperf.Declared != "full" || underperf.Observed != "basic" || underperf.Runtime != "claude-code" {
		t.Errorf("error fields = %+v, want runtime=claude-code declared=full observed=basic", underperf)
	}
	// Not recorded: a second assignment re-probes and rejects again.
	if err := b.verifyIntegrationLevel(context.Background(), probe, "claude-code", "full"); err == nil {
		t.Error("second underperforming assignment was not rejected")
	}
	if probe.calls != 2 {
		t.Errorf("probe calls = %d, want 2 (underperformance is never cached)", probe.calls)
	}
}

// spec: §5.1 line 44 — observed == declared accepts without annotation and
// records the runtime as verified so the first-assignment probe runs once.
func TestVerifyIntegrationLevelMatch_spec_5_1(t *testing.T) {
	var underdeclared int
	b := &Binder{IntegrationLevelUnderdeclared: func(_, _, _ string) { underdeclared++ }}
	probe := &fakeLevelProbe{level: "standard"}
	if err := b.verifyIntegrationLevel(context.Background(), probe, "rt", "standard"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// Verified: a second assignment skips the probe.
	if err := b.verifyIntegrationLevel(context.Background(), probe, "rt", "standard"); err != nil {
		t.Fatalf("verify (second): %v", err)
	}
	if probe.calls != 1 {
		t.Errorf("probe calls = %d, want 1 (verified runtime is cached)", probe.calls)
	}
	if underdeclared != 0 {
		t.Errorf("underdeclared fired %d times on an exact match, want 0", underdeclared)
	}
}

// spec: §5.1 line 43 — observed > declared accepts and emits the
// runtime.integrationLevel.underdeclared warning exactly once.
func TestVerifyIntegrationLevelUnderdeclared_spec_5_1(t *testing.T) {
	var got []string // declared:observed per firing
	b := &Binder{IntegrationLevelUnderdeclared: func(_, declared, observed string) {
		got = append(got, declared+":"+observed)
	}}
	probe := &fakeLevelProbe{level: "full"}
	if err := b.verifyIntegrationLevel(context.Background(), probe, "rt", "basic"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// Verified after the warning: a second assignment neither re-probes nor
	// re-warns.
	if err := b.verifyIntegrationLevel(context.Background(), probe, "rt", "basic"); err != nil {
		t.Fatalf("verify (second): %v", err)
	}
	if len(got) != 1 || got[0] != "basic:full" {
		t.Errorf("underdeclared firings = %v, want one basic:full", got)
	}
	if probe.calls != 1 {
		t.Errorf("probe calls = %d, want 1", probe.calls)
	}
}

// spec: §5.1 — an empty declared level defaults to basic, which any
// observed level satisfies, so no rejection and no warning.
func TestVerifyIntegrationLevelEmptyDeclaredDefaultsBasic_spec_5_1(t *testing.T) {
	b := &Binder{}
	probe := &fakeLevelProbe{level: "basic"}
	if err := b.verifyIntegrationLevel(context.Background(), probe, "rt", ""); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// An adapter predating the observed-level probe returns Unimplemented; the
// check is skipped without recording the runtime so it re-runs after an
// upgrade. A transient probe error is likewise skipped.
func TestVerifyIntegrationLevelProbeErrorsSkip_spec_5_1(t *testing.T) {
	b := &Binder{}
	unimpl := &fakeLevelProbe{err: status.Error(codes.Unimplemented, "old adapter")}
	if err := b.verifyIntegrationLevel(context.Background(), unimpl, "rt", "full"); err != nil {
		t.Fatalf("Unimplemented probe should be skipped, got %v", err)
	}
	// Not recorded: a re-probe runs after the adapter is upgraded.
	if unimpl2 := (&fakeLevelProbe{level: "basic"}); b.verifyIntegrationLevel(context.Background(), unimpl2, "rt", "full") == nil {
		t.Error("re-probe after upgrade should now reject the underperforming runtime")
	}

	transient := &fakeLevelProbe{err: status.Error(codes.Unavailable, "blip")}
	if err := b.verifyIntegrationLevel(context.Background(), transient, "rt2", "full"); err != nil {
		t.Fatalf("transient probe error should be skipped, got %v", err)
	}
}

// A nil probe or empty runtime is a no-op (the in-process executor path and
// resume paths carry no declared level to enforce).
func TestVerifyIntegrationLevelNilProbeOrRuntime_spec_5_1(t *testing.T) {
	b := &Binder{}
	if err := b.verifyIntegrationLevel(context.Background(), nil, "rt", "full"); err != nil {
		t.Errorf("nil probe: %v", err)
	}
	if err := b.verifyIntegrationLevel(context.Background(), &fakeLevelProbe{level: "basic"}, "", "full"); err != nil {
		t.Errorf("empty runtime: %v", err)
	}
}

// The configured probe wait defaults to DefaultIntegrationLevelProbeWaitMs
// and honors an explicit override.
func TestIntegrationLevelProbeWaitMs_spec_5_1(t *testing.T) {
	if got := (&Binder{}).integrationLevelProbeWaitMs(); got != DefaultIntegrationLevelProbeWaitMs {
		t.Errorf("default wait = %d, want %d", got, DefaultIntegrationLevelProbeWaitMs)
	}
	if got := (&Binder{IntegrationLevelProbeWaitMs: 250}).integrationLevelProbeWaitMs(); got != 250 {
		t.Errorf("override wait = %d, want 250", got)
	}
}
