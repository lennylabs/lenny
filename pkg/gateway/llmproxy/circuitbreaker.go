// SPDX-License-Identifier: MIT

package llmproxy

import (
	"github.com/lennylabs/lenny/pkg/gateway/subsystem"
)

// CircuitBreaker is the §4.9 LLM Proxy circuit breaker around an
// upstream LLM provider. Consecutive upstream failures trip it from
// closed to open; while open it rejects every request so the proxy
// returns PROVIDER_UNAVAILABLE without hanging on a doomed upstream
// call. After the cooldown it admits a single half-open probe whose
// outcome closes the breaker or reopens it and resets the cooldown.
//
// The implementation lives in pkg/gateway/subsystem.Breaker so the
// other §4.1 subsystem boundaries (Stream Proxy, Upload Handler, MCP
// Fabric) share the same state machine; this alias keeps the existing
// llmproxy callers (handlers and tests) unchanged. The breaker is
// goroutine-safe.
//
// spec: §4.9 (LLM Proxy subsystem)
// spec: §4.1 (Per-subsystem circuit breaker)
type CircuitBreaker = subsystem.Breaker

// CircuitState is the §4.9 LLM Proxy circuit-breaker state. The
// underlying state values map 1:1 to subsystem.State.
type CircuitState = subsystem.State

const (
	// CircuitClosed admits every request; the upstream is healthy.
	CircuitClosed = subsystem.StateClosed
	// CircuitHalfOpen admits one probe request after the open cooldown.
	CircuitHalfOpen = subsystem.StateHalfOpen
	// CircuitOpen rejects every request; the upstream is failing.
	CircuitOpen = subsystem.StateOpen
)
