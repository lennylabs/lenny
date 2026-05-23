// SPDX-License-Identifier: MIT

package extractionthreshold_test

import (
	"sort"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/extractionthreshold"
)

// recordingEmitter captures every (subsystem, metric, value) tuple
// the threshold pushes through Emit.
type recordingEmitter struct {
	entries []entry
}

type entry struct {
	subsystem, metric string
	value             float64
}

func (e *recordingEmitter) SetExtractionThreshold(subsystem, metric string, value float64) {
	e.entries = append(e.entries, entry{subsystem, metric, value})
}

// spec: §4.1 — Defaults returns the provisional first-principles
// values from the extraction-trigger table.
func TestDefaultsMatchSpecProvisionalValues(t *testing.T) {
	d := extractionthreshold.Defaults()
	want := []struct {
		field string
		got   float64
		exp   float64
	}{
		{"StreamProxyQueueDepth", d.StreamProxyQueueDepth, 500},
		{"StreamProxyP99AttachLatencySeconds", d.StreamProxyP99AttachLatencySeconds, 0.8},
		{"UploadHandlerActiveConcurrent", d.UploadHandlerActiveConcurrent, 200},
		{"UploadHandlerP99LatencySeconds", d.UploadHandlerP99LatencySeconds, 2.0},
		{"MCPFabricActiveDelegations", d.MCPFabricActiveDelegations, 1000},
		{"MCPFabricP99OrchestrationLatencySec", d.MCPFabricP99OrchestrationLatencySec, 2.0},
		{"LLMProxyActiveConnections", d.LLMProxyActiveConnections, 2000},
		{"LLMProxyP99TTFBSeconds", d.LLMProxyP99TTFBSeconds, 1.0},
	}
	for _, c := range want {
		if c.got != c.exp {
			t.Errorf("Defaults().%s = %v, want %v", c.field, c.got, c.exp)
		}
	}
}

// spec: §4.1 — Emit pushes one gauge sample for each configured
// threshold; subsystem labels cover the four §4.1 subsystems.
func TestEmitCoversAllSubsystemsAndMetrics(t *testing.T) {
	em := &recordingEmitter{}
	extractionthreshold.Defaults().Emit(em)

	if len(em.entries) != 8 {
		t.Fatalf("Emit produced %d entries, want 8", len(em.entries))
	}
	subsystems := map[string]int{}
	for _, e := range em.entries {
		subsystems[e.subsystem]++
	}
	want := map[string]int{
		"stream_proxy":   2,
		"upload_handler": 2,
		"mcp_fabric":     2,
		"llm_proxy":      2,
	}
	for s, n := range want {
		if subsystems[s] != n {
			t.Errorf("subsystem %q: %d entries, want %d", s, subsystems[s], n)
		}
	}
}

// spec: §4.1 — every Defaults value is emitted at startup so a stock
// install has a non-NaN audit value for every threshold.
func TestEmitEveryDefaultThresholdHasAValue(t *testing.T) {
	em := &recordingEmitter{}
	extractionthreshold.Defaults().Emit(em)

	for _, e := range em.entries {
		if e.value <= 0 {
			t.Errorf("threshold %s/%s = %v, want > 0 (default missing)", e.subsystem, e.metric, e.value)
		}
	}
}

// spec: §4.1 — an env-var override changes the emitted value so a
// Helm-supplied calibration flows through to the audit gauge.
func TestFromEnvOverrideChangesEmittedValue(t *testing.T) {
	t.Setenv("LENNY_EXTRACTION_THRESHOLD_STREAM_PROXY_QUEUE_DEPTH", "750")
	t.Setenv("LENNY_EXTRACTION_THRESHOLD_LLM_PROXY_ACTIVE_CONNECTIONS", "3500")
	cfg := extractionthreshold.FromEnv()
	if cfg.StreamProxyQueueDepth != 750 {
		t.Errorf("StreamProxyQueueDepth = %v, want 750 (env override)", cfg.StreamProxyQueueDepth)
	}
	if cfg.LLMProxyActiveConnections != 3500 {
		t.Errorf("LLMProxyActiveConnections = %v, want 3500 (env override)", cfg.LLMProxyActiveConnections)
	}
	// Unset thresholds keep their defaults.
	if cfg.UploadHandlerActiveConcurrent != 200 {
		t.Errorf("UploadHandlerActiveConcurrent = %v, want 200 (default)", cfg.UploadHandlerActiveConcurrent)
	}
}

// spec: §4.1 — a malformed env var falls back to the default so a
// typo cannot keep the gateway from booting.
func TestFromEnvMalformedFallsBackToDefault(t *testing.T) {
	t.Setenv("LENNY_EXTRACTION_THRESHOLD_STREAM_PROXY_QUEUE_DEPTH", "not-a-number")
	cfg := extractionthreshold.FromEnv()
	if cfg.StreamProxyQueueDepth != 500 {
		t.Errorf("malformed env yielded %v, want 500 (default)", cfg.StreamProxyQueueDepth)
	}
}

// spec: §4.1 — Emit invocation order is stable so a /metrics scrape
// surface and an upstream test snapshot stay deterministic.
func TestEmitInvocationOrderStable(t *testing.T) {
	em := &recordingEmitter{}
	extractionthreshold.Defaults().Emit(em)

	keys := make([]string, len(em.entries))
	for i, e := range em.entries {
		keys[i] = e.subsystem + "/" + e.metric
	}
	want := append([]string(nil), keys...)
	sort.Strings(want)
	// Stable order: subsystem block, then metric within block.
	expectedOrder := []string{
		"stream_proxy/queue_depth",
		"stream_proxy/p99_attach_latency_seconds",
		"upload_handler/active_concurrent",
		"upload_handler/p99_latency_seconds",
		"mcp_fabric/active_delegations",
		"mcp_fabric/p99_orchestration_latency_seconds",
		"llm_proxy/active_connections",
		"llm_proxy/p99_ttfb_seconds",
	}
	if len(keys) != len(expectedOrder) {
		t.Fatalf("Emit produced %d keys, want %d", len(keys), len(expectedOrder))
	}
	for i, k := range expectedOrder {
		if keys[i] != k {
			t.Errorf("entry %d = %q, want %q", i, keys[i], k)
		}
	}
}
