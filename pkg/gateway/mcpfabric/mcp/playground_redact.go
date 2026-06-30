// SPDX-License-Identifier: MIT

package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
)

// §27.9 line 251 — the playground raw-frame inspector "displays redacted
// frames only; the gateway applies the same redaction rules as the audit
// log (§16.4) before sending frames to the browser." The §16.4 line 376
// rule excludes credential-sensitive material from any payload-level
// surface (logs, gRPC access logs, OTel span attributes). For the MCP
// WebSocket leg the analogue is a per-frame field scrub: before a
// response frame reaches a playground-origin browser, every scalar value
// whose JSON key names a credential is replaced with the redaction
// marker. A sensitive key whose value is structural (an object or array)
// is a schema or container rather than a credential literal, so the
// walker recurses into it and leaves the structure intact (see
// redactFrameValue).
//
// The marker set mirrors the admin platform-config secret markers
// (pkg/gateway/admin/platform.go redactSecret, §25.3) and is intentionally
// coarse: the raw-frame inspector is a debugging aid, so over-redaction of
// a credential-adjacent field is the safe direction. Non-playground MCP
// WebSocket clients (a headless agent consuming tool results) are never
// redacted — the gate is keyed on the §27.3 origin=playground claim.

// frameRedactionMarker is the placeholder a scrubbed value is replaced
// with so the playground operator sees that a field existed without
// seeing its credential value.
const frameRedactionMarker = "[REDACTED]"

// frameSensitiveKeyMarkers is the set of credential-bearing key
// substrings the playground frame redactor scrubs. A key is sensitive
// when its lower-cased form contains any marker. Markers are specific
// enough to avoid scrubbing benign fields (the bare substring "key" is
// deliberately absent so identifiers like "publicKey"/"keyId" survive)
// while covering the credential field names MCP tool calls carry.
//
// spec: §16.4 line 376 (credential-sensitive exclusion); §27.9 line 251.
var frameSensitiveKeyMarkers = []string{
	"authorization",
	"secret", // client_secret, secret_key, webhook_secret
	"password",
	"passwd",
	"token", // access_token, refresh_token, id_token, session_token
	"bearer",
	"credential",
	"api_key",
	"apikey",
	"private_key",
	"privatekey",
	"access_key",
}

// isSensitiveFrameKey reports whether a JSON key names a credential the
// playground frame redactor must scrub.
func isSensitiveFrameKey(key string) bool {
	lk := strings.ToLower(key)
	for _, marker := range frameSensitiveKeyMarkers {
		if strings.Contains(lk, marker) {
			return true
		}
	}
	return false
}

// redactPlaygroundFrame applies the §27.9 line 251 raw-frame redaction to
// one outbound MCP response frame. It parses the frame as JSON, walks it
// recursively, and replaces every credential-bearing field's value with
// frameRedactionMarker. Numbers are preserved exactly (UseNumber) so the
// JSON-RPC id and any large integer identifiers round-trip unchanged.
//
// A frame that does not parse as JSON is returned unchanged: the gateway
// only ever emits well-formed JSON-RPC envelopes on this path, so a parse
// failure is not a credential-leak vector and forwarding the original
// bytes keeps the inspector functional. Re-marshalling cannot fail for a
// value that decoded successfully, so the same fallback covers that edge.
//
// spec: §27.9 line 251; §16.4 line 376. F-27.9.1.
func redactPlaygroundFrame(raw []byte) []byte {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return raw
	}
	out, err := json.Marshal(redactFrameValue(v))
	if err != nil {
		return raw
	}
	return out
}

// redactFrameValue recursively scrubs credential literals in a decoded
// JSON value. A map key that names a credential is scrubbed only when its
// value is a scalar (string/number/bool/null) — that is the credential
// literal the redaction targets, e.g. `"access_token": "sk-..."`. A
// sensitive key whose value is an object or array is a container or a
// schema definition rather than a credential literal (a tool's
// `inputSchema` property named `access_token` carries `{"type":"string"}`,
// which must survive so the playground's schema-driven form still
// renders), so the walker recurses into it instead of collapsing it. This
// keeps the scrub confined to credential values without corrupting the
// MCP protocol envelope or tool schemas. Arrays are walked element-wise.
func redactFrameValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			switch child.(type) {
			case map[string]any, []any:
				t[k] = redactFrameValue(child)
			default:
				if isSensitiveFrameKey(k) {
					t[k] = frameRedactionMarker
				}
			}
		}
		return t
	case []any:
		for i, child := range t {
			t[i] = redactFrameValue(child)
		}
		return t
	default:
		return v
	}
}
