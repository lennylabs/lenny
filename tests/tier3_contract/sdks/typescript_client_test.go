// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 SDK contract scaffolds for the TypeScript client SDK. The
// shared harness drives sdks/client/typescript/test-helper over
// stdin/stdout.

package sdks_test

import "testing"

// TestTypeScriptClientHelperProtocol — the test helper accepts
// commands over stdin and reports outcomes over stdout per the
// shared harness contract.
func TestTypeScriptClientHelperProtocol(t *testing.T) {
	t.Skip("not implemented: §14.13 TypeScript client harness — requires sdks/client/typescript/test-helper + the documented stdin/stdout protocol")
}

// TestTypeScriptClientWireFormat — TypeScript client → gateway
// behaves identically to Go client → gateway for the wire-format
// round trip.
func TestTypeScriptClientWireFormat(t *testing.T) {
	t.Skip("not implemented: §14.13 TypeScript client wire-format — requires sdks/client/typescript with openapi-typescript generator output")
}

// TestTypeScriptClientStreamingReconnect — SSE / MCP streaming via
// the hand-written streaming layer.
func TestTypeScriptClientStreamingReconnect(t *testing.T) {
	t.Skip("not implemented: §14.13 TypeScript client streaming — requires sdks/client/typescript/streaming + the gateway stream proxy")
}

// TestTypeScriptClientESMAndCJSBuilds — both ESM and CJS builds
// shipped.
func TestTypeScriptClientESMAndCJSBuilds(t *testing.T) {
	t.Skip("not implemented: §14.13 TypeScript dual-mode builds — requires the build config emitting both ESM and CJS targets")
}

// TestTypeScriptClientTypecheck — tsc --noEmit passes on the
// generated + hand-written sources.
func TestTypeScriptClientTypecheck(t *testing.T) {
	t.Skip("not implemented: §14.13 TypeScript typecheck — requires tsc --noEmit invocation in CI + a typed SDK surface")
}

// TestTypeScriptClientEquivalenceWithGo — the same operation matrix
// produces equivalent outcomes (TypeScript helper vs. Go client).
func TestTypeScriptClientEquivalenceWithGo(t *testing.T) {
	t.Skip("not implemented: §14.13 TypeScript/Go equivalence harness — requires both helpers and the matrix driver")
}
