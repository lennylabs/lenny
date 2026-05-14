// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 SDK contract scaffolds for the Python client SDK. The
// shared harness drives a language-native helper process
// (sdks/client/python/test-helper) over stdin/stdout. The harness
// runs the same operation matrix against every SDK and asserts
// equivalence with the Go client (the SDK equivalent of the REST↔MCP
// consistency suite from §12.3.1).

package sdks_test

import "testing"

// TestPythonClientHelperProtocol — the test helper accepts commands
// over stdin and reports outcomes over stdout per the shared harness
// contract.
func TestPythonClientHelperProtocol(t *testing.T) {
	t.Skip("not implemented: §14.13 Python client harness — requires sdks/client/python/test-helper + the documented stdin/stdout protocol")
}

// TestPythonClientWireFormat — Python client → gateway behaves
// identically to Go client → gateway for the wire-format round trip.
func TestPythonClientWireFormat(t *testing.T) {
	t.Skip("not implemented: §14.13 Python client wire-format — requires sdks/client/python wheel + openapi-python-client generator output")
}

// TestPythonClientStreamingReconnect — SSE / MCP streaming via the
// hand-written streaming layer in sdks/client/python/streaming/.
func TestPythonClientStreamingReconnect(t *testing.T) {
	t.Skip("not implemented: §14.13 Python client streaming — requires sdks/client/python/streaming + the gateway stream proxy")
}

// TestPythonClientAsyncSync — both async/await and synchronous
// variants supported.
func TestPythonClientAsyncSync(t *testing.T) {
	t.Skip("not implemented: §14.13 Python async/sync ergonomics — requires both code paths in sdks/client/python")
}

// TestPythonClientTypeHints — mypy --strict passes on the generated
// + hand-written sources.
func TestPythonClientTypeHints(t *testing.T) {
	t.Skip("not implemented: §14.13 Python type-hint validation — requires the mypy --strict invocation in CI + the typed SDK surface")
}

// TestPythonClientEquivalenceWithGo — the same operation matrix
// produces equivalent outcomes (Python helper vs. Go client).
func TestPythonClientEquivalenceWithGo(t *testing.T) {
	t.Skip("not implemented: §14.13 Python/Go equivalence harness — requires both helpers and the matrix driver")
}
