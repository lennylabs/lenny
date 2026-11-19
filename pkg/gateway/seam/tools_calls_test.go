// SPDX-License-Identifier: MIT

package seam

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/session/state"
)

// TestDecodeToolCallResponseReadsResultsAndRefusals checks the decode of a
// tools/call response: a result carries its encoded content, a JSON-RPC
// error and an isError result's lenny/error block carry the refusal, and a
// body that is no JSON-RPC 2.0 response, carries neither a result nor an
// error, or carries an error with no lenny error envelope is an error.
// spec: GWSEAM-ForwardToolCall
func TestDecodeToolCallResponseReadsResultsAndRefusals(t *testing.T) {
	res, r, err := DecodeToolCallResponse([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"done"}]}}`))
	if err != nil || r != nil || string(res.Content) != `[{"type":"text","text":"done"}]` || res.IsError {
		t.Fatalf("result = %+v, %v, %v", res, r, err)
	}
	_, r, err = DecodeToolCallResponse([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"x","data":{"code":"RESOURCE_NOT_FOUND","message":"no tool","details":{"tool":"t"}}}}`))
	if err != nil || r == nil || r.Code != "RESOURCE_NOT_FOUND" || r.Message != "no tool" || r.Details["tool"] != "t" {
		t.Fatalf("JSON-RPC error = %+v, %v", r, err)
	}
	_, r, err = DecodeToolCallResponse([]byte(`{"jsonrpc":"2.0","id":1,"result":{"isError":true,"content":[{"type":"text","text":"x"},{"type":"lenny/error","text":"{\"code\":\"SCOPE_DENIED\",\"message\":\"no\"}"}]}}`))
	if err != nil || r == nil || r.Code != "SCOPE_DENIED" {
		t.Fatalf("isError result = %+v, %v", r, err)
	}
	for name, body := range map[string]string{
		"not JSON":             `{`,
		"wrong version":        `{"jsonrpc":"1.0","result":{}}`,
		"empty":                `{"jsonrpc":"2.0"}`,
		"error with no code":   `{"jsonrpc":"2.0","error":{"data":{"message":"x"}}}`,
		"isError with no code": `{"jsonrpc":"2.0","result":{"isError":true,"content":[{"type":"text","text":"x"}]}}`,
		"bad content":          `{"jsonrpc":"2.0","result":{"content":"x"}}`,
	} {
		if _, _, err := DecodeToolCallResponse([]byte(body)); err == nil {
			t.Errorf("%s: decode succeeded, want an error", name)
		}
	}
}

// TestTargetNotReadyCarriesTheCurrentState checks the refusal of a tool
// call whose holder replica could not be reached.
// spec: GWSEAM-ForwardToolCall
func TestTargetNotReadyCarriesTheCurrentState(t *testing.T) {
	r := TargetNotReady(state.State("running"))
	if r.Code != "TARGET_NOT_READY" || r.Details["currentState"] != "running" || r.Message == "" {
		t.Fatalf("refusal = %+v", r)
	}
}
