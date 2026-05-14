// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 SDK contract scaffolds for the Go client SDK. The contract
// surface follows §14.13 "Client SDKs": wire-format round trip,
// streaming and reconnect, file upload, webhook signature
// verification, authentication, idempotency-key handling, error
// envelope, pagination cursors, MCP client, language-native
// ergonomics, compatibility matrix.

package sdks_test

import "testing"

// TestGoClientWireFormatRoundTrip — every documented REST operation
// in the OpenAPI spec, every documented MCP tool, exercised through
// the SDK against a live gateway (compose profile).
func TestGoClientWireFormatRoundTrip(t *testing.T) {
	t.Skip("not implemented: §14.13 Go client wire-format round trip — requires sdks/client/go + the OpenAPI generator output + a compose-profile gateway")
}

// TestGoClientStreamingReconnect — SSE for REST log/message streams;
// MCP Streamable HTTP for sessions; disconnect mid-stream and
// reconnect with Last-Event-ID; zero gaps and zero duplicates.
func TestGoClientStreamingReconnect(t *testing.T) {
	t.Skip("not implemented: §14.13 Go client streaming — requires sdks/client/go streaming/ package + gateway stream proxy + the §7.2 SessionEvent ring buffer")
}

// TestGoClientFileUpload — multipart upload, tar/tar.gz/zip archive
// via uploadArchive, gitClone source materialization; progress
// callbacks monotonic.
func TestGoClientFileUpload(t *testing.T) {
	t.Skip("not implemented: §14.13 Go client file upload — requires sdks/client/go upload helpers + the gateway upload handler + archive validators (pkg/upload ships)")
}

// TestGoClientWebhookSignatureVerification — HMAC-SHA256 against
// X-Lenny-Signature with the t=<unix>,v1=<hex> format and the 5-min
// replay window.
func TestGoClientWebhookSignatureVerification(t *testing.T) {
	t.Skip("not implemented: §14.13 Go client webhook verifier — requires the helper in sdks/client/go/webhook + the webhook delivery path on the gateway side")
}

// TestGoClientAuthentication — OIDC bearer, service-account token,
// API key (per chosen mode); token refresh before expiry;
// multi-tenant context honored.
func TestGoClientAuthentication(t *testing.T) {
	t.Skip("not implemented: §14.13 Go client auth — requires sdks/client/go/auth + token-refresh helper + multi-tenant context propagation through the gateway")
}

// TestGoClientIdempotencyKeyHandling — same key produces same
// response; expired key produces a fresh response.
func TestGoClientIdempotencyKeyHandling(t *testing.T) {
	t.Skip("not implemented: §14.13 Go client idempotency — requires sdks/client/go idempotency helper + the gateway's idempotency middleware (already shipping in pkg/gateway/middleware/idempotency)")
}

// TestGoClientErrorEnvelope — {code, category, message, retryable,
// docs_url} parsed into typed errors; retryable errors retry with
// backoff and jitter.
func TestGoClientErrorEnvelope(t *testing.T) {
	t.Skip("not implemented: §14.13 Go client error envelope — requires the typed-error tree in sdks/client/go/errors + the gateway error envelope contract (already shipping)")
}

// TestGoClientPaginationCursors — every paginated endpoint exercised
// end-to-end through cursor iteration helpers.
func TestGoClientPaginationCursors(t *testing.T) {
	t.Skip("not implemented: §14.13 Go client pagination — requires sdks/client/go pagination iterator + every paginated endpoint on the gateway")
}

// TestGoClientMCP — tool discovery, lenny/delegate_task invocation,
// lenny/send_message, lenny/request_input, lenny/await_children,
// elicitation response.
func TestGoClientMCP(t *testing.T) {
	t.Skip("not implemented: §14.13 Go client MCP — requires sdks/client/go/mcp + the platform MCP server hosting the documented tool surface")
}

// TestGoClientErgonomics — context cancellation propagates; channels
// for streams.
func TestGoClientErgonomics(t *testing.T) {
	t.Skip("not implemented: §14.13 Go client ergonomics — requires the context-cancellation propagation contract through every helper + channel-shaped stream wrappers")
}

// TestGoClientCompatibilityMatrix — each SDK validated against the
// two most recent minor gateway versions (forward and backward
// compatibility within spec/15.5).
func TestGoClientCompatibilityMatrix(t *testing.T) {
	t.Skip("not implemented: §14.13 Go client compatibility matrix — requires a pinned set of gateway images covering the documented support window and a matrix driver")
}
