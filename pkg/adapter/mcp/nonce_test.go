// SPDX-License-Identifier: MIT

package mcp_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/mcp"
)

const testNonce = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// initRequest builds an MCP initialize request. An empty nonce omits
// the _lennyNonce field.
func initRequest(nonce string) []byte {
	params := `"protocolVersion":"2025-03-26","capabilities":{}`
	if nonce != "" {
		params = `"` + mcp.NonceParamKey + `":"` + nonce + `",` + params
	}
	return []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{` + params + `}}`)
}

func TestAuthenticateInitializeValid(t *testing.T) {
	cleaned, err := mcp.AuthenticateInitialize(initRequest(testNonce), testNonce)
	if err != nil {
		t.Fatalf("AuthenticateInitialize: %v", err)
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(cleaned, &env); err != nil {
		t.Fatalf("decode cleaned request: %v", err)
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(env["params"], &params); err != nil {
		t.Fatalf("decode cleaned params: %v", err)
	}
	if _, present := params[mcp.NonceParamKey]; present {
		t.Error("_lennyNonce was not stripped from the dispatched request")
	}
	if _, present := params["protocolVersion"]; !present {
		t.Error("stripping _lennyNonce dropped an unrelated param")
	}
	var method string
	if err := json.Unmarshal(env["method"], &method); err != nil || method != "initialize" {
		t.Errorf("cleaned request method = %q (err %v), want initialize", method, err)
	}
}

func TestAuthenticateInitializeMissingNonce(t *testing.T) {
	if _, err := mcp.AuthenticateInitialize(initRequest(""), testNonce); !errors.Is(err, mcp.ErrNonceMissing) {
		t.Errorf("error = %v, want ErrNonceMissing", err)
	}
}

func TestAuthenticateInitializeWrongNonce(t *testing.T) {
	wrong := "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if _, err := mcp.AuthenticateInitialize(initRequest(wrong), testNonce); !errors.Is(err, mcp.ErrNonceInvalid) {
		t.Errorf("error = %v, want ErrNonceInvalid", err)
	}
}

func TestAuthenticateInitializeEmptyExpected(t *testing.T) {
	if _, err := mcp.AuthenticateInitialize(initRequest(testNonce), ""); !errors.Is(err, mcp.ErrNonceInvalid) {
		t.Errorf("error = %v, want ErrNonceInvalid for an empty expected nonce", err)
	}
}

func TestAuthenticateInitializeNotInitialize(t *testing.T) {
	req := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"` +
		mcp.NonceParamKey + `":"` + testNonce + `"}}`)
	if _, err := mcp.AuthenticateInitialize(req, testNonce); !errors.Is(err, mcp.ErrNotInitialize) {
		t.Errorf("error = %v, want ErrNotInitialize", err)
	}
}

func TestAuthenticateInitializeMissingParams(t *testing.T) {
	req := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if _, err := mcp.AuthenticateInitialize(req, testNonce); !errors.Is(err, mcp.ErrNonceMissing) {
		t.Errorf("error = %v, want ErrNonceMissing", err)
	}
}

func TestAuthenticateInitializeMalformedJSON(t *testing.T) {
	if _, err := mcp.AuthenticateInitialize([]byte("not json"), testNonce); err == nil {
		t.Error("AuthenticateInitialize accepted malformed JSON")
	}
}
