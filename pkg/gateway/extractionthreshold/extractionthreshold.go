// SPDX-License-Identifier: MIT

// Package extractionthreshold reads the §4.1 per-subsystem
// extraction-threshold Helm values from environment variables and
// emits them on the lenny_gateway_extraction_threshold gauge at
// startup.
//
// The §4.1 spec requires that the indicative threshold values in the
// extraction-trigger table are configurable at deploy time via
// gateway.extractionThresholds.<subsystem>.<metric> Helm values, and
// that the values used for an extraction decision are auditable in the
// Helm release history. The Helm chart surfaces each value as an env
// var (`LENNY_EXTRACTION_THRESHOLD_<SUBSYSTEM>_<METRIC>`); this
// package reads them at startup, fills in the §4.1 provisional
// defaults for any unset entries, and pushes each value to the gauge.
package extractionthreshold

import (
	"os"
	"strconv"
)

// Threshold names every published §4.1 per-subsystem extraction
// threshold. Each field carries the value from the
// `gateway.extractionThresholds.<subsystem>.<metric>` Helm value, with
// the §4.1 provisional default when the corresponding env var is
// unset.
type Threshold struct {
	StreamProxyQueueDepth               float64
	StreamProxyP99AttachLatencySeconds  float64
	UploadHandlerActiveConcurrent       float64
	UploadHandlerP99LatencySeconds      float64
	MCPFabricActiveDelegations          float64
	MCPFabricP99OrchestrationLatencySec float64
	LLMProxyActiveConnections           float64
	LLMProxyP99TTFBSeconds              float64
}

// Defaults returns a Threshold populated with the §4.1 provisional
// first-principles values from the extraction-trigger table. The
// gateway uses these when no LENNY_EXTRACTION_THRESHOLD_* env vars
// are set so a stock binary still emits a useful audit gauge.
//
// spec: §4.1 lines 121-128
func Defaults() Threshold {
	return Threshold{
		StreamProxyQueueDepth:               500,
		StreamProxyP99AttachLatencySeconds:  0.8,
		UploadHandlerActiveConcurrent:       200,
		UploadHandlerP99LatencySeconds:      2.0,
		MCPFabricActiveDelegations:          1000,
		MCPFabricP99OrchestrationLatencySec: 2.0,
		LLMProxyActiveConnections:           2000,
		LLMProxyP99TTFBSeconds:              1.0,
	}
}

// FromEnv reads each LENNY_EXTRACTION_THRESHOLD_* env var, falling
// back to the §4.1 provisional defaults for any unset entry. A
// malformed value (non-numeric) silently falls back to the default
// so a malformed Helm value cannot keep the gateway from booting;
// the gauge value identifies the discrepancy at scrape time.
func FromEnv() Threshold {
	t := Defaults()
	t.StreamProxyQueueDepth = readFloat("LENNY_EXTRACTION_THRESHOLD_STREAM_PROXY_QUEUE_DEPTH", t.StreamProxyQueueDepth)
	t.StreamProxyP99AttachLatencySeconds = readFloat("LENNY_EXTRACTION_THRESHOLD_STREAM_PROXY_P99_ATTACH_LATENCY_SECONDS", t.StreamProxyP99AttachLatencySeconds)
	t.UploadHandlerActiveConcurrent = readFloat("LENNY_EXTRACTION_THRESHOLD_UPLOAD_HANDLER_ACTIVE_CONCURRENT", t.UploadHandlerActiveConcurrent)
	t.UploadHandlerP99LatencySeconds = readFloat("LENNY_EXTRACTION_THRESHOLD_UPLOAD_HANDLER_P99_LATENCY_SECONDS", t.UploadHandlerP99LatencySeconds)
	t.MCPFabricActiveDelegations = readFloat("LENNY_EXTRACTION_THRESHOLD_MCP_FABRIC_ACTIVE_DELEGATIONS", t.MCPFabricActiveDelegations)
	t.MCPFabricP99OrchestrationLatencySec = readFloat("LENNY_EXTRACTION_THRESHOLD_MCP_FABRIC_P99_ORCHESTRATION_LATENCY_SECONDS", t.MCPFabricP99OrchestrationLatencySec)
	t.LLMProxyActiveConnections = readFloat("LENNY_EXTRACTION_THRESHOLD_LLM_PROXY_ACTIVE_CONNECTIONS", t.LLMProxyActiveConnections)
	t.LLMProxyP99TTFBSeconds = readFloat("LENNY_EXTRACTION_THRESHOLD_LLM_PROXY_P99_TTFB_SECONDS", t.LLMProxyP99TTFBSeconds)
	return t
}

// Emitter accepts the (subsystem, metric, value) tuple the threshold
// gauge requires. It is satisfied by
// *gatewaymetrics.Metrics.SetExtractionThreshold.
type Emitter interface {
	SetExtractionThreshold(subsystem, metric string, value float64)
}

// Emit pushes every configured threshold to the
// lenny_gateway_extraction_threshold gauge. The (subsystem, metric)
// label pairs match the Helm-key path so a /metrics scrape can be
// joined against the Helm value documentation.
func (t Threshold) Emit(e Emitter) {
	e.SetExtractionThreshold("stream_proxy", "queue_depth", t.StreamProxyQueueDepth)
	e.SetExtractionThreshold("stream_proxy", "p99_attach_latency_seconds", t.StreamProxyP99AttachLatencySeconds)
	e.SetExtractionThreshold("upload_handler", "active_concurrent", t.UploadHandlerActiveConcurrent)
	e.SetExtractionThreshold("upload_handler", "p99_latency_seconds", t.UploadHandlerP99LatencySeconds)
	e.SetExtractionThreshold("mcp_fabric", "active_delegations", t.MCPFabricActiveDelegations)
	e.SetExtractionThreshold("mcp_fabric", "p99_orchestration_latency_seconds", t.MCPFabricP99OrchestrationLatencySec)
	e.SetExtractionThreshold("llm_proxy", "active_connections", t.LLMProxyActiveConnections)
	e.SetExtractionThreshold("llm_proxy", "p99_ttfb_seconds", t.LLMProxyP99TTFBSeconds)
}

func readFloat(env string, fallback float64) float64 {
	v := os.Getenv(env)
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return parsed
}
