// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 SDK contract scaffolds for the Runtime-author SDKs across
// Go, Python, and TypeScript. Wraps the adapter binary protocol, MCP
// platform tools, and the lifecycle channel.

package sdks_test

import "testing"

// TestRuntimeSDKAdapterBinaryProtocolGo — every Basic-level message
// type. The Go SDK skeleton passes `lenny-compliance --level basic`.
func TestRuntimeSDKAdapterBinaryProtocolGo(t *testing.T) {
	t.Skip("not implemented: §14.13 Go runtime SDK basic level — requires sdks/runtime/go + the lenny-compliance --level basic harness")
}

// TestRuntimeSDKAdapterBinaryProtocolPython — Python equivalent.
func TestRuntimeSDKAdapterBinaryProtocolPython(t *testing.T) {
	t.Skip("not implemented: §14.13 Python runtime SDK basic level — requires sdks/runtime/python wheel + the basic-level conformance test battery")
}

// TestRuntimeSDKAdapterBinaryProtocolTypeScript — TypeScript
// equivalent.
func TestRuntimeSDKAdapterBinaryProtocolTypeScript(t *testing.T) {
	t.Skip("not implemented: §14.13 TypeScript runtime SDK basic level — requires sdks/runtime/typescript package + the basic-level conformance test battery")
}

// TestRuntimeSDKMCPSocketStandardLevel — Standard-level conformance:
// connect to @lenny, read _lennyNonce from the manifest, invoke
// platform tools.
func TestRuntimeSDKMCPSocketStandardLevel(t *testing.T) {
	t.Skip("not implemented: §14.13 runtime SDK Standard level — requires the MCP socket integration in each runtime SDK + the manifest nonce auth path")
}

// TestRuntimeSDKLifecycleFullLevel — Full-level conformance: connect
// to @lenny-lifecycle, capability handshake, checkpoint flow,
// interrupt flow, credential rotation, deadline notification.
func TestRuntimeSDKLifecycleFullLevel(t *testing.T) {
	t.Skip("not implemented: §14.13 runtime SDK Full level — requires the lifecycle channel implementation in each runtime SDK")
}

// TestRuntimeSDKWorkspaceHelpers — read_file, write_file, list_dir,
// delete_file confined to /workspace/current and /workspace/output;
// path traversal blocked.
func TestRuntimeSDKWorkspaceHelpers(t *testing.T) {
	t.Skip("not implemented: §14.13 runtime SDK workspace helpers — requires the path-confinement helpers in each runtime SDK + the adversarial path-traversal corpus")
}

// TestRuntimeSDKDelegationTools — lenny/delegate_task invoked through
// the SDK; budget metadata propagates; child results awaited and
// parsed.
func TestRuntimeSDKDelegationTools(t *testing.T) {
	t.Skip("not implemented: §14.13 runtime SDK delegation — requires the lenny/delegate_task wrapper in each runtime SDK + the §8.2 platform implementation")
}

// TestRuntimeSDKHeartbeatHandling — automatic heartbeat_ack within
// 10 seconds without runtime-author intervention.
func TestRuntimeSDKHeartbeatHandling(t *testing.T) {
	t.Skip("not implemented: §14.13 runtime SDK heartbeat — requires the heartbeat loop in each runtime SDK")
}

// TestRuntimeSDKGracefulShutdown — shutdown deadline honored; SDK
// exits cleanly.
func TestRuntimeSDKGracefulShutdown(t *testing.T) {
	t.Skip("not implemented: §14.13 runtime SDK graceful shutdown — requires the shutdown-signal handler in each runtime SDK")
}

// TestRuntimeSDKTelemetryPassThrough — tracingContext set via
// lenny/set_tracing_context; OTel context flows into the runtime's
// own tracer.
func TestRuntimeSDKTelemetryPassThrough(t *testing.T) {
	t.Skip("not implemented: §14.13 runtime SDK telemetry — requires the OTel context propagation through lenny/set_tracing_context in each runtime SDK")
}

// TestRuntimeSDKQuickStartTTHW — `lenny runtime init my-agent` produces
// a runnable image within the documented five-minute target.
func TestRuntimeSDKQuickStartTTHW(t *testing.T) {
	t.Skip("not implemented: §14.13 runtime SDK quick-start TTHW — requires the `lenny runtime init` scaffold + the timed mirror of the runtime-author guide")
}
