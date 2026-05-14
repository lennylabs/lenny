// SPDX-License-Identifier: MIT

//go:build conformance

// Tier-10 conformance scaffolds. cmd/lenny-compliance/ is the
// conformance harness — a standalone binary that takes a runtime
// image (or a local binary path) and a declared integration level,
// runs the test battery for that level, and produces a JSON report.
// The harness ships in a later phase; these scaffolds document the
// contract so the test names stay stable.

package tier10_conformance_test

import "testing"

// TestBasicAdapterProtocol — every adapter binary protocol message,
// stdin/stdout JSON Lines, shutdown deadline, heartbeat ack, unknown
// message types. Runs against echo, streaming-echo, delegation-echo.
func TestBasicAdapterProtocol(t *testing.T) {
	t.Skip("not implemented: §12.10 Basic level — requires cmd/lenny-compliance + the echo / streaming-echo / delegation-echo runtimes with adapter manifests")
}

// TestStandardLevel — Basic plus MCP socket connection, nonce auth
// via adapter manifest, platform-tool discovery, capability inference
// at registration.
func TestStandardLevel(t *testing.T) {
	t.Skip("not implemented: §12.10 Standard level — requires the MCP socket connector + adapter-manifest nonce auth path + platform-tool discovery handshake")
}

// TestFullLevel — Standard plus lifecycle channel handshake,
// checkpoint flow with timeouts, interrupt flow, credential rotation,
// deadline notification, task lifecycle.
func TestFullLevel(t *testing.T) {
	t.Skip("not implemented: §12.10 Full level — requires the full lifecycle channel implementation + checkpoint/interrupt/rotation/deadline handlers on the runtime side")
}

// TestBundledRuntimesEveryPR — the three test runtimes (echo,
// streaming-echo, delegation-echo) run conformance on every PR.
func TestBundledRuntimesEveryPR(t *testing.T) {
	t.Skip("not implemented: §12.10 bundled-runtimes subset — requires the three test runtimes to be packaged + cmd/lenny-compliance to dispatch the per-level battery")
}

// TestReferenceCatalogNightly — the nine reference runtimes
// (claude-code, gemini-cli, codex, cursor-cli, chat, langgraph,
// mastra, openai-assistants, crewai) run conformance on every nightly.
func TestReferenceCatalogNightly(t *testing.T) {
	t.Skip("not implemented: §12.10 reference-catalog subset — requires the reference runtimes to be packaged or available as published images + the nightly invocation path")
}

// TestThirdPartyRegistration — third-party runtimes register
// themselves via the RegisterAdapterUnderTest(adapter) mechanism in
// the harness.
func TestThirdPartyRegistration(t *testing.T) {
	t.Skip("not implemented: §12.10 third-party harness API — requires the RegisterAdapterUnderTest entry point in cmd/lenny-compliance + the documented contract")
}

// TestFidelityMatrix — table-driven test asserts the documented
// OpenAI/Anthropic Completions/Responses translation fidelity. For
// each OutputPart type, the lossy fields are documented and the suite
// confirms the exact loss.
func TestFidelityMatrix(t *testing.T) {
	t.Skip("not implemented: §12.10 fidelity matrix — requires the documented per-OutputPart fidelity table + the OpenAI/Anthropic translators + table-driven validator")
}
