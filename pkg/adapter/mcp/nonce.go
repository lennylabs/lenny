// SPDX-License-Identifier: MIT

// Package mcp implements the §9 / §15.4.3 intra-pod MCP fabric: the
// adapter-local MCP servers the runtime connects to over abstract Unix
// sockets, and the manifest-nonce handshake that authenticates those
// connections.
package mcp

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
)

// NonceParamKey is the §15.4.3 canonical injection key for the
// intra-pod MCP nonce: the top-level field of the MCP initialize
// request's params object.
const NonceParamKey = "_lennyNonce"

// initializeMethod is the JSON-RPC method of the MCP handshake request
// that carries the nonce.
const initializeMethod = "initialize"

// Sentinel errors. An adapter-local MCP connection that produces any
// of these is closed without dispatching tools (§15.4.3).
var (
	// ErrNotInitialize reports a first MCP message whose method is not
	// `initialize`; the nonce handshake must precede any other call.
	ErrNotInitialize = errors.New("mcp: first MCP message is not an initialize request")

	// ErrNonceMissing reports an initialize request that carries no
	// _lennyNonce field.
	ErrNonceMissing = errors.New("mcp: initialize request carries no _lennyNonce")

	// ErrNonceInvalid reports an initialize request whose _lennyNonce
	// does not match the manifest nonce.
	ErrNonceInvalid = errors.New("mcp: initialize request _lennyNonce does not match the manifest nonce")
)

// AuthenticateInitialize validates the §15.4.3 intra-pod MCP nonce on
// an `initialize` JSON-RPC request and returns the request with the
// _lennyNonce field stripped from params, ready to dispatch to the MCP
// server implementation — which validates params against the MCP
// schema and would reject the non-standard field.
//
// expectedNonce is the manifest's mcpNonce. The comparison is
// constant-time and exact: §15.4.3 applies no normalization. A request
// that is not an initialize call, carries no nonce, or carries a
// mismatched nonce is rejected, and the adapter closes such a
// connection without dispatching tools. An empty expectedNonce never
// authenticates.
func AuthenticateInitialize(request []byte, expectedNonce string) ([]byte, error) {
	if expectedNonce == "" {
		return nil, ErrNonceInvalid
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(request, &envelope); err != nil {
		return nil, fmt.Errorf("mcp: decode initialize request: %w", err)
	}

	var method string
	if raw, ok := envelope["method"]; ok {
		if err := json.Unmarshal(raw, &method); err != nil {
			return nil, fmt.Errorf("mcp: decode initialize method: %w", err)
		}
	}
	if method != initializeMethod {
		return nil, ErrNotInitialize
	}

	rawParams, ok := envelope["params"]
	if !ok {
		return nil, ErrNonceMissing
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return nil, fmt.Errorf("mcp: decode initialize params: %w", err)
	}
	rawNonce, ok := params[NonceParamKey]
	if !ok {
		return nil, ErrNonceMissing
	}
	var nonce string
	if err := json.Unmarshal(rawNonce, &nonce); err != nil {
		return nil, fmt.Errorf("mcp: decode %s: %w", NonceParamKey, err)
	}
	if subtle.ConstantTimeCompare([]byte(nonce), []byte(expectedNonce)) != 1 {
		return nil, ErrNonceInvalid
	}

	// §15.4.3: strip _lennyNonce so the MCP server never sees the
	// non-standard field.
	delete(params, NonceParamKey)
	cleanedParams, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("mcp: re-encode initialize params: %w", err)
	}
	envelope["params"] = cleanedParams
	cleaned, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("mcp: re-encode initialize request: %w", err)
	}
	return cleaned, nil
}
