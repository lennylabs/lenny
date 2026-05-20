// SPDX-License-Identifier: MIT

package mcp_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/mcp"
)

// spec: §15.2.1 rule 5(d) (REST↔MCP error envelope parity)
// diagnosis: the lenny error envelope embedded in jsonRPCError.Data
// must carry the same code, category, message, retryable, and
// details fields the REST errorBody surfaces, so the parity contract
// holds on both transports. NewLennyErrorDetail is the constructor
// that builds that envelope; it must consult the shared classifier
// so REST and MCP report identical (category, retryable) values for
// the same code.
func TestNewLennyErrorDetailPopulatesClassifierFields(t *testing.T) {
	cases := []struct {
		code      string
		message   string
		wantCat   string
		wantRetry bool
	}{
		{"INTERNAL_ERROR", "transient gateway failure", "TRANSIENT", true},
		{"WORKSPACE_PLAN_INVALID", "plan failed JSON Schema validation", "PERMANENT", false},
		{"CIRCUIT_BREAKER_OPEN", "breaker is open", "POLICY", false},
		{"UPSTREAM_ERROR", "Anthropic API 502", "UPSTREAM", true},
	}
	for _, c := range cases {
		t.Run(c.code, func(t *testing.T) {
			d := mcp.NewLennyErrorDetail(c.code, c.message, map[string]any{"hint": c.code})
			if d.Code != c.code {
				t.Errorf("Code = %q, want %q", d.Code, c.code)
			}
			if d.Category != c.wantCat {
				t.Errorf("Category = %q, want %q", d.Category, c.wantCat)
			}
			if d.Message != c.message {
				t.Errorf("Message = %q, want %q", d.Message, c.message)
			}
			if d.Retryable != c.wantRetry {
				t.Errorf("Retryable = %v, want %v", d.Retryable, c.wantRetry)
			}
			if got, _ := d.Details["hint"].(string); got != c.code {
				t.Errorf("Details[hint] = %q, want %q", got, c.code)
			}
		})
	}
}

// spec: §15.2.1 rule 5(d)
// diagnosis: an unknown lenny code falls back to (TRANSIENT, true)
// per the classifier contract so a downstream client does not give
// up on a code the classifier has not yet learned.
func TestNewLennyErrorDetailUnknownCodeTransientRetryable(t *testing.T) {
	d := mcp.NewLennyErrorDetail("FUTURE_UNDEFINED_CODE", "TBD", nil)
	if d.Category != "TRANSIENT" {
		t.Errorf("unknown-code Category = %q, want TRANSIENT", d.Category)
	}
	if !d.Retryable {
		t.Errorf("unknown-code Retryable = false, want true")
	}
}
