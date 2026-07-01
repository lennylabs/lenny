// SPDX-License-Identifier: MIT

package mcp

import "encoding/json"

// MCP protocol version negotiation for the gateway-edge `/mcp` surface.
//
// spec: §15.2 "Version negotiation" (lines 1308-1316) and §15.5 item 2.
// The gateway supports the current and the previous MCP spec versions
// concurrently. The `initialize` handshake negotiates the highest
// mutually supported version, rejects versions older than the oldest
// supported one, and pins the result for the connection lifetime. A
// connection that negotiates the previous (deprecated) version receives
// the `X-Lenny-Mcp-Version-Deprecated` warning header.

const (
	// CurrentProtocolVersion is the §15.2 line 1308 target MCP spec
	// version. All MCP features Lenny uses are gated on this version or
	// later.
	CurrentProtocolVersion = "2025-03-26"

	// PreviousProtocolVersion is the older MCP spec version the gateway
	// still serves during its §15.2 line 1316 / §15.5 item 2 six-month
	// deprecation window. A connection negotiated onto it receives the
	// deprecation warning header.
	PreviousProtocolVersion = "2024-11-05"

	// ProtocolVersion is the default version the server advertises when a
	// client omits `protocolVersion` from `initialize`. It is the current
	// supported version. spec: §15.2 line 1308.
	ProtocolVersion = CurrentProtocolVersion

	// headerMCPVersionDeprecated is the §15.2 line 1316 warning header the
	// gateway sets on connections negotiated onto the deprecated version.
	// RFC 7230 hyphenated naming; underscore-named headers are dropped by
	// some proxies.
	headerMCPVersionDeprecated = "X-Lenny-Mcp-Version-Deprecated"
)

// supportedVersions is the §15.2 line 1311 concurrently-served set,
// ordered newest first. Index 0 is the current version; the last entry is
// the oldest still-supported version.
var supportedVersions = []string{CurrentProtocolVersion, PreviousProtocolVersion}

// retiredVersions lists MCP spec versions the gateway formerly served but
// has dropped past their deprecation window. A new `initialize` handshake
// requesting one is rejected with MCP_PROTOCOL_VERSION_RETIRED rather than
// the generic MCP_VERSION_UNSUPPORTED, so a client that lingered past the
// window gets the precise §15.2 retirement signal. Empty in v1: nothing
// has been retired yet. spec: §15.2 "Session-lifetime exception".
var retiredVersions = []string{}

// SupportedProtocolVersions returns a copy of the concurrently-served MCP
// spec version set (newest first). It backs the `supportedVersions` field
// in a rejected-handshake error envelope and any discovery surface.
func SupportedProtocolVersions() []string {
	out := make([]string, len(supportedVersions))
	copy(out, supportedVersions)
	return out
}

// negotiationError carries the lenny error code and message for an
// `initialize` handshake the gateway rejects on version grounds.
type negotiationError struct {
	code    string
	message string
}

// negotiateVersion applies the §15.2 lines 1310-1315 negotiation rules to
// the client's requested `protocolVersion`:
//
//   - An empty request (the client omitted the field) negotiates the
//     current version.
//   - A request naming a retired version is rejected with
//     MCP_PROTOCOL_VERSION_RETIRED.
//   - A request naming a supported version negotiates that exact version.
//   - A request newer than the current version, or in the gap between two
//     supported versions, negotiates the current version (the highest the
//     gateway supports), so the client can decide whether to proceed.
//   - A request older than the oldest supported version is rejected with
//     MCP_VERSION_UNSUPPORTED.
//
// deprecated reports whether the negotiated version is the previous
// (deprecated) one, which drives the X-Lenny-Mcp-Version-Deprecated header.
func negotiateVersion(requested string) (negotiated string, deprecated bool, nerr *negotiationError) {
	if requested == "" {
		return CurrentProtocolVersion, false, nil
	}
	for _, v := range retiredVersions {
		if requested == v {
			return "", false, &negotiationError{
				code:    "MCP_PROTOCOL_VERSION_RETIRED",
				message: "MCP protocol version " + requested + " has been retired; renegotiate on a supported version",
			}
		}
	}
	for _, v := range supportedVersions {
		if requested == v {
			return requested, requested == PreviousProtocolVersion, nil
		}
	}
	oldest := supportedVersions[len(supportedVersions)-1]
	if requested < oldest {
		return "", false, &negotiationError{
			code:    "MCP_VERSION_UNSUPPORTED",
			message: "MCP protocol version " + requested + " is older than the oldest supported version " + oldest,
		}
	}
	// Newer than current, or in the gap between supported versions: offer
	// the highest version the gateway supports per the MCP convention.
	return CurrentProtocolVersion, false, nil
}

// initializeResult builds the §15.2 `initialize` response payload for the
// requested client version, returning the negotiated payload, whether the
// connection landed on the deprecated version, and a non-nil
// negotiationError when the version is rejected. Both the POST and the
// WebSocket transports route their `initialize` handling through it so
// negotiation is identical on every MCP transport. spec: §15.2 line 1308.
func initializeResult(requested string) (result map[string]any, deprecated bool, nerr *negotiationError) {
	negotiated, deprecated, nerr := negotiateVersion(requested)
	if nerr != nil {
		return nil, false, nerr
	}
	return map[string]any{
		"protocolVersion": negotiated,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "lenny-gateway", "version": "0.1.0"},
	}, deprecated, nil
}

// requestedProtocolVersion extracts the client's `protocolVersion` from
// the `initialize` params. A missing or malformed params object yields the
// empty string, which negotiateVersion treats as "default to current".
func requestedProtocolVersion(params []byte) string {
	if len(params) == 0 {
		return ""
	}
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &p)
	return p.ProtocolVersion
}
