// SPDX-License-Identifier: MIT

package poolstore_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// TestSDKWarmOverrideIsValid covers the §15.1 line 801 closed override
// vocabulary, including the stored-default unset value.
func TestSDKWarmOverrideIsValid_spec_15_1(t *testing.T) {
	valid := []poolstore.SDKWarmCircuitBreakerOverride{
		poolstore.SDKWarmOverrideUnset,
		poolstore.SDKWarmOverrideEnabled,
		poolstore.SDKWarmOverrideDisabled,
		poolstore.SDKWarmOverrideAuto,
	}
	for _, v := range valid {
		if !v.IsValid() {
			t.Errorf("%q should be valid", v)
		}
	}
	if poolstore.SDKWarmCircuitBreakerOverride("bogus").IsValid() {
		t.Error("bogus override should be invalid")
	}
}

// TestValidateSDKWarmConfigRejectsBadOverride asserts the store rejects a
// pool whose §6.1 override is outside the closed vocabulary, on both
// Create and Update. spec: §6.1 line 63, §15.1 line 801.
func TestValidateSDKWarmConfigRejectsBadOverride_spec_6_1(t *testing.T) {
	s := poolstore.NewMemory()
	bad := poolstore.Pool{
		Name:                          "bad-pool",
		RuntimeRef:                    "echo",
		IsolationProfile:              isolation.ProfileSandboxed,
		ExecutionMode:                 runtimestore.ExecutionModeSession,
		WarmCount:                     1,
		SDKWarmCircuitBreakerOverride: poolstore.SDKWarmCircuitBreakerOverride("nope"),
	}
	if err := s.Create(context.Background(), bad); err == nil {
		t.Fatal("Create should reject an unrecognised circuitBreakerOverride")
	}

	good := bad
	good.SDKWarmCircuitBreakerOverride = poolstore.SDKWarmOverrideUnset
	if err := s.Create(context.Background(), good); err != nil {
		t.Fatalf("Create with unset override: %v", err)
	}
	if _, err := s.Update(context.Background(), "bad-pool", func(p *poolstore.Pool) error {
		p.SDKWarmCircuitBreakerOverride = poolstore.SDKWarmCircuitBreakerOverride("still-nope")
		return nil
	}); err == nil {
		t.Fatal("Update should reject an unrecognised circuitBreakerOverride")
	}
}

// TestMemoryRoundTripsSDKWarmConfig asserts the §6.1 override and
// acknowledgeHighDemotionRate fields round-trip through the Memory store.
func TestMemoryRoundTripsSDKWarmConfig_spec_6_1(t *testing.T) {
	s := poolstore.NewMemory()
	p := poolstore.Pool{
		Name:                          "sdk-pool",
		RuntimeRef:                    "claude-code",
		IsolationProfile:              isolation.ProfileSandboxed,
		ExecutionMode:                 runtimestore.ExecutionModeSession,
		WarmCount:                     2,
		SDKWarmCircuitBreakerOverride: poolstore.SDKWarmOverrideDisabled,
		AcknowledgeHighDemotionRate:   true,
	}
	if err := s.Create(context.Background(), p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Get(context.Background(), "sdk-pool")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SDKWarmCircuitBreakerOverride != poolstore.SDKWarmOverrideDisabled {
		t.Errorf("override = %q, want disabled", got.SDKWarmCircuitBreakerOverride)
	}
	if !got.AcknowledgeHighDemotionRate {
		t.Error("acknowledgeHighDemotionRate should round-trip true")
	}

	updated, err := s.Update(context.Background(), "sdk-pool", func(p *poolstore.Pool) error {
		p.SDKWarmCircuitBreakerOverride = poolstore.SDKWarmOverrideAuto
		p.AcknowledgeHighDemotionRate = false
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.SDKWarmCircuitBreakerOverride != poolstore.SDKWarmOverrideAuto {
		t.Errorf("override after update = %q, want auto", updated.SDKWarmCircuitBreakerOverride)
	}
	if updated.AcknowledgeHighDemotionRate {
		t.Error("acknowledgeHighDemotionRate should be cleared by the update")
	}
}

// TestValidateSDKWarmConfigRejectsBadDemotionRateThreshold asserts the
// store rejects a §6.1 line 48 demotionRateThreshold outside (0, 1] while
// accepting a valid fraction and a nil (inherit-default) value.
func TestValidateSDKWarmConfigRejectsBadDemotionRateThreshold_spec_6_1_48(t *testing.T) {
	mk := func(threshold *float64) poolstore.Pool {
		return poolstore.Pool{
			Name:                  "thr-pool",
			RuntimeRef:            "echo",
			IsolationProfile:      isolation.ProfileSandboxed,
			ExecutionMode:         runtimestore.ExecutionModeSession,
			WarmCount:             1,
			DemotionRateThreshold: threshold,
		}
	}
	f := func(v float64) *float64 { return &v }

	for _, bad := range []*float64{f(0), f(-0.1), f(1.5)} {
		s := poolstore.NewMemory()
		if err := s.Create(context.Background(), mk(bad)); err == nil {
			t.Errorf("Create should reject demotionRateThreshold=%v (outside (0,1])", *bad)
		}
	}

	// A valid fraction and a nil (inherit-default) value both pass and
	// round-trip through the store.
	s := poolstore.NewMemory()
	if err := s.Create(context.Background(), mk(f(0.4))); err != nil {
		t.Fatalf("Create with threshold 0.4: %v", err)
	}
	got, err := s.Get(context.Background(), "thr-pool")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.DemotionRateThreshold == nil || *got.DemotionRateThreshold != 0.4 {
		t.Errorf("threshold = %v, want 0.4", got.DemotionRateThreshold)
	}
	if _, err := s.Update(context.Background(), "thr-pool", func(p *poolstore.Pool) error {
		p.DemotionRateThreshold = nil
		return nil
	}); err != nil {
		t.Fatalf("Update clearing threshold: %v", err)
	}
}
