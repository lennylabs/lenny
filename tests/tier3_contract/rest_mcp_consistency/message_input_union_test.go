// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract test for §15.4 `MessageEnvelope.input` parity across the
// REST `/messages` endpoint and the MCP `lenny/send_message` tool. §15.4
// binds `MessageEnvelope` identically across the stdin binary protocol,
// the platform MCP server tools, and every external API, so a message's
// inbound content is the same `oneOf(string, MessagePart[])` union on both
// surfaces. Before F-MS5 the content was a bare string on both, so the
// structured part-array form was unrepresentable. This test sends both the
// bare-string form and the MessagePart[] form through each surface and
// asserts both are accepted and delivered, pinning the two surfaces to the
// one §15.4 union under the §15.2.1 parity rule.
package rest_mcp_consistency_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// newUnionServers stands up REST and MCP servers sharing a session store
// and an EchoExecutor, so a delivered message echoes its text back on both
// surfaces and the union-input acceptance is observable from the receipt
// and the echoed output.
func newUnionServers(t *testing.T) (restURL, mcpURL string, store sessionstore.Store) {
	t.Helper()
	store = memstore.New()
	exec := executor.NewEchoExecutor()

	rest := sessionserver.New(store, sessionserver.Options{Executor: exec})
	tsREST := httptest.NewServer(rest.Handler())
	t.Cleanup(tsREST.Close)

	mcpSrv := mcp.NewServer()
	mcptools.Register(mcpSrv, mcptools.Deps{
		Store:    store,
		Executor: exec,
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_union_mcp" },
		TenantID: "acme",
	})
	tsMCP := httptest.NewServer(mcpSrv.Handler())
	t.Cleanup(tsMCP.Close)

	return tsREST.URL, tsMCP.URL, store
}

// seedRunning writes a `running` session so the §7.2 path-2 direct
// delivery (and the EchoExecutor echo) runs on both surfaces.
func seedRunning(t *testing.T, store sessionstore.Store, id string) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", UserID: "alice@acme.com",
		State: session.StateRunning, RuntimeRef: "claude-code",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed running session: %v", err)
	}
}

// restMessageResult drives REST POST /v1/sessions/{id}/messages with a raw
// JSON body so the test exercises the wire union directly, and returns the
// receipt status and the echoed output text.
func restMessageResult(t *testing.T, restURL, id, body string) (status, echoed string) {
	t.Helper()
	resp, raw := postJSON(t, restURL+"/v1/sessions/"+id+"/messages", "acme", []byte(body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("REST /messages status = %d, want 200 (§15.4 union must be accepted); body=%s", resp.StatusCode, raw)
	}
	var out struct {
		DeliveryReceipt struct {
			Status string `json:"status"`
		} `json:"deliveryReceipt"`
		Output []struct {
			Text string `json:"text"`
		} `json:"output"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("REST message decode: %v\nbody=%s", err, raw)
	}
	for _, p := range out.Output {
		echoed += p.Text
	}
	return out.DeliveryReceipt.Status, echoed
}

// mcpMessageResult drives the MCP lenny/send_message tool with the given
// message argument (a bare string or a MessagePart[] array) and returns the
// receipt status and the echoed output text.
func mcpMessageResult(t *testing.T, mcpURL, id string, message any) (status, echoed string) {
	t.Helper()
	res := mcpCall(t, mcpURL+"/mcp", "lenny/send_message", map[string]any{
		"to":      id,
		"message": message,
	})
	if _, ok := res["_error"]; ok {
		t.Fatalf("MCP send_message returned an error envelope for union input %v: %v", message, res)
	}
	contents, _ := res["content"].([]any)
	for _, c := range contents {
		obj, _ := c.(map[string]any)
		text, _ := obj["text"].(string)
		if text == "" {
			continue
		}
		var receipt struct {
			DeliveryReceipt struct {
				Status string `json:"status"`
			} `json:"deliveryReceipt"`
		}
		if err := json.Unmarshal([]byte(text), &receipt); err == nil && receipt.DeliveryReceipt.Status != "" {
			status = receipt.DeliveryReceipt.Status
			continue
		}
		echoed += text
	}
	return status, echoed
}

// spec: §15.4 (MessageEnvelope.input oneOf(string, MessagePart[])), §15.2.1
// (REST/MCP parity).
// diagnosis: a failure here means the REST /messages endpoint and the MCP
// lenny/send_message tool disagree on the §15.4 message-input union — one
// surface rejects a form the other accepts, or the bare-string and
// part-array forms do not project to the same delivered text. §15.4 binds
// the message-input contract across both transports, so a divergence
// breaks the §15.2.1 parity rule and a client cannot send the same content
// to both surfaces.
func TestMessageInputUnionParity_spec_15_4(t *testing.T) {
	restURL, mcpURL, store := newUnionServers(t)

	cases := []struct {
		name     string
		restBody string
		mcpArg   any
		wantText string // the echoed text projection both surfaces must produce
	}{
		{
			name:     "bare string",
			restBody: `{"messages":[{"role":"user","content":"hi-bare"}]}`,
			mcpArg:   "hi-bare",
			wantText: "hi-bare",
		},
		{
			name:     "single text part array",
			restBody: `{"messages":[{"role":"user","content":[{"type":"text","inline":"hi-part"}]}]}`,
			mcpArg:   []any{map[string]any{"type": "text", "inline": "hi-part"}},
			wantText: "hi-part",
		},
		{
			name: "multipart with a non-text part",
			// The image part carries no text; only the text part projects.
			restBody: `{"messages":[{"role":"user","content":[{"type":"text","inline":"see "},{"type":"image","ref":"lenny-blob://acme/s/p","mimeType":"image/png"}]}]}`,
			mcpArg: []any{
				map[string]any{"type": "text", "inline": "see "},
				map[string]any{"type": "image", "ref": "lenny-blob://acme/s/p", "mimeType": "image/png"},
			},
			wantText: "see ",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seedRunning(t, store, "sess_rest_"+strings.ReplaceAll(tc.name, " ", "_"))
			seedRunning(t, store, "sess_mcp_"+strings.ReplaceAll(tc.name, " ", "_"))

			restStatus, restEcho := restMessageResult(t, restURL,
				"sess_rest_"+strings.ReplaceAll(tc.name, " ", "_"), tc.restBody)
			mcpStatus, mcpEcho := mcpMessageResult(t, mcpURL,
				"sess_mcp_"+strings.ReplaceAll(tc.name, " ", "_"), tc.mcpArg)

			// Both surfaces accept the union form and deliver it.
			if restStatus != string(session.DeliveryStatusDelivered) {
				t.Errorf("REST receipt status = %q, want delivered (union form must be accepted)", restStatus)
			}
			if mcpStatus != string(session.DeliveryStatusDelivered) {
				t.Errorf("MCP receipt status = %q, want delivered (union form must be accepted)", mcpStatus)
			}
			// Both surfaces project the union to the same delivered text.
			if !strings.Contains(restEcho, tc.wantText) {
				t.Errorf("REST echoed %q, want it to contain %q", restEcho, tc.wantText)
			}
			if !strings.Contains(mcpEcho, tc.wantText) {
				t.Errorf("MCP echoed %q, want it to contain %q", mcpEcho, tc.wantText)
			}
		})
	}
}
