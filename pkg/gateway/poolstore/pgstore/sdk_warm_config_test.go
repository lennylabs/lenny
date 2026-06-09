// SPDX-License-Identifier: MIT

package pgstore

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
)

// TestEncodeDecodeSDKWarmConfig_spec_6_1 proves the §6.1 SDK-warm
// operability config round-trips through the sdk_warm_config JSONB column
// and that a pool with no override encodes to SQL NULL so the breaker
// runs under automatic control. F-6.1.24.
func TestEncodeDecodeSDKWarmConfig_spec_6_1(t *testing.T) {
	t.Run("empty config encodes to NULL", func(t *testing.T) {
		raw, err := encodeSDKWarmConfig(poolstore.Pool{Name: "p"})
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if raw != nil {
			t.Fatalf("empty config encoded to %q, want nil (SQL NULL)", raw)
		}
		var p poolstore.Pool
		if err := decodeSDKWarmConfig(nil, &p); err != nil {
			t.Fatalf("decode NULL: %v", err)
		}
		if p.SDKWarmCircuitBreakerOverride != poolstore.SDKWarmOverrideUnset || p.AcknowledgeHighDemotionRate {
			t.Errorf("NULL decoded to a non-zero config: %+v", p)
		}
	})

	t.Run("override and acknowledgment round-trip", func(t *testing.T) {
		in := poolstore.Pool{
			Name:                          "p",
			SDKWarmCircuitBreakerOverride: poolstore.SDKWarmOverrideDisabled,
			AcknowledgeHighDemotionRate:   true,
		}
		raw, err := encodeSDKWarmConfig(in)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if raw == nil {
			t.Fatal("a non-empty config encoded to NULL")
		}
		var out poolstore.Pool
		if err := decodeSDKWarmConfig(raw, &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.SDKWarmCircuitBreakerOverride != poolstore.SDKWarmOverrideDisabled {
			t.Errorf("override = %q, want disabled", out.SDKWarmCircuitBreakerOverride)
		}
		if !out.AcknowledgeHighDemotionRate {
			t.Error("acknowledgeHighDemotionRate lost in round-trip")
		}
	})

	t.Run("acknowledgment alone encodes non-NULL", func(t *testing.T) {
		raw, err := encodeSDKWarmConfig(poolstore.Pool{Name: "p", AcknowledgeHighDemotionRate: true})
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if raw == nil {
			t.Fatal("acknowledgeHighDemotionRate alone should encode non-NULL")
		}
		var out poolstore.Pool
		if err := decodeSDKWarmConfig(raw, &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.SDKWarmCircuitBreakerOverride != poolstore.SDKWarmOverrideUnset || !out.AcknowledgeHighDemotionRate {
			t.Errorf("round-trip = %+v, want unset override + acknowledged", out)
		}
	})

	// spec: §6.1 line 48 — the deployer-configurable demotionRateThreshold
	// round-trips and a threshold alone encodes non-NULL.
	t.Run("demotionRateThreshold round-trips", func(t *testing.T) {
		threshold := 0.75
		raw, err := encodeSDKWarmConfig(poolstore.Pool{Name: "p", DemotionRateThreshold: &threshold})
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if raw == nil {
			t.Fatal("demotionRateThreshold alone should encode non-NULL")
		}
		var out poolstore.Pool
		if err := decodeSDKWarmConfig(raw, &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.DemotionRateThreshold == nil || *out.DemotionRateThreshold != 0.75 {
			t.Errorf("demotionRateThreshold = %v, want 0.75", out.DemotionRateThreshold)
		}
	})

	t.Run("absent demotionRateThreshold decodes nil", func(t *testing.T) {
		raw, err := encodeSDKWarmConfig(poolstore.Pool{Name: "p", AcknowledgeHighDemotionRate: true})
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		var out poolstore.Pool
		if err := decodeSDKWarmConfig(raw, &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.DemotionRateThreshold != nil {
			t.Errorf("demotionRateThreshold = %v, want nil (inherit default)", *out.DemotionRateThreshold)
		}
	})
}
