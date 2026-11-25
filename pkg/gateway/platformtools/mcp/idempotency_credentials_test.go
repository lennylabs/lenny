// SPDX-License-Identifier: MIT

package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/platformtools/mcp"
	"github.com/lennylabs/lenny/pkg/idempotency/idempotencymw"
)

// swapRedactor stands in for the upload-token redactor the gateway wires:
// Strip swaps the credential for a reference and Remint swaps it back.
type swapRedactor struct{}

func (swapRedactor) Strip(body []byte) []byte {
	return bytes.ReplaceAll(body, []byte("tok-secret"), []byte("tok-reference"))
}

func (swapRedactor) Remint(stored []byte) []byte {
	return bytes.ReplaceAll(stored, []byte("tok-reference"), []byte("tok-secret"))
}

// TestMCPIdempotencyStoresNoCredentialAndReplaysIt checks that the MCP
// idempotency hook, over the store WithCredentials returns, stores the tool
// result with its credential replaced and restores the credential on
// replay, so both calls return the same result.
//
// spec: §11.5 (Idempotency)
func TestMCPIdempotencyStoresNoCredentialAndReplaysIt(t *testing.T) {
	s := mcp.NewServer()
	var calls atomic.Int32
	s.RegisterTool(mcp.Tool{Name: "lenny/create_session", InputSchema: json.RawMessage(`{}`)},
		func(_ context.Context, _ json.RawMessage) (mcp.ToolResult, error) {
			calls.Add(1)
			return mcp.ToolResult{Content: []mcp.ToolContent{{
				Type: "text",
				Text: `{"sessionId":"sess-1","state":"created","uploadToken":"tok-secret"}`,
			}}}, nil
		})
	store := idempotencymw.NewMemoryStore()
	s.SetIdempotency(mcp.IdempotencyConfig{
		Store:             idempotencymw.WithCredentials(store, swapRedactor{}),
		TenantFromRequest: func(r *http.Request) string { return r.Header.Get("X-Lenny-Tenant-ID") },
		Tools:             map[string]bool{"lenny/create_session": true},
	})

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lenny/create_session","arguments":{"runtimeRef":"echo","idempotencyKey":"abc-123"}}}`
	r1 := rpcWithTenant(t, s.Handler(), "acme", body)
	rec, found, err := store.Get(context.Background(), "acme", "mcp/lenny/create_session/abc-123")
	if err != nil || !found {
		t.Fatalf("Get: found=%v err=%v", found, err)
	}
	if strings.Contains(string(rec.Response.Body), "tok-secret") || !strings.Contains(string(rec.Response.Body), "tok-reference") {
		t.Fatalf("stored result = %s, want the reference in place of the credential", rec.Response.Body)
	}
	r2 := rpcWithTenant(t, s.Handler(), "acme", body)
	if calls.Load() != 1 {
		t.Fatalf("tool ran %d times, want 1", calls.Load())
	}
	if !strings.Contains(r1.Body.String(), "tok-secret") || r1.Body.String() != r2.Body.String() {
		t.Fatalf("replay must carry the credential and equal the first answer:\nfirst: %s\nreplay: %s", r1.Body, r2.Body)
	}
}
