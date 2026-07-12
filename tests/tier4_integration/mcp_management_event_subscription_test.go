//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration scaffold for the §25.12 MCP-native operational
// event subscription. §25.12 states that the MCP Management Server
// supports the `notifications/message` method for streaming operational
// events, that an agent subscribes by sending a `notifications/subscribe`
// request with optional event type filters, and that events are
// delivered as MCP notifications with the same payload schema as the
// §25.5 REST event stream. This test walks that user journey: a DevOps
// agent subscribes over MCP with a type filter, an operational event is
// emitted, and a `notifications/message` notification arrives carrying a
// payload schema-identical to GET /v1/admin/events.
//
// The behaviour this test exercises does not exist yet. The
// /mcp/management server (pkg/ops/mcp) is a synchronous JSON-RPC over
// HTTP surface: each POST decodes one request, dispatches, and writes
// one response. `notifications/subscribe` and `notifications/message`
// fall through to the method-not-found (-32601) default, and the server
// has no persistent-connection / streaming transport over which
// asynchronous notifications could be delivered at all. The §25.5
// operational event source (Redis stream) that would feed the
// subscription is itself unbuilt. The test is a phase-gated scaffold
// until the streaming transport and the event-source wiring land.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
)

// TestMCPManagementEventSubscriptionE2E boots lenny-ops, opens a
// §25.12 `notifications/subscribe` request against /mcp/management with
// an event-type filter, emits an operational event, and asserts a
// `notifications/message` notification arrives whose payload is
// schema-identical to a GET /v1/admin/events record (§25.5).
//
// spec: §25.12 (SSE Event Subscription via MCP — "The MCP Management
// Server supports the `notifications/message` method for streaming
// operational events... The agent subscribes by sending a
// `notifications/subscribe` request with optional event type filters.
// Events are delivered as MCP notifications with the same payload schema
// as the REST event stream."); §25.5 (Operational Event Stream —
// CloudEvents envelope shared across SSE, polling, and webhook).
// diagnosis: a failure means MCP-native event subscription diverged
// from the spec when driven end to end. Either `notifications/subscribe`
// was not accepted, no `notifications/message` was delivered for a
// matching event, the type filter did not select events, or the
// notification payload did not match the CloudEvents schema the §25.5
// REST event stream returns.
func TestMCPManagementEventSubscriptionE2E(t *testing.T) {
	t.Skip("blocked: §25.12 MCP-native event subscription is unbuilt — /mcp/management is a synchronous one-request-one-response JSON-RPC server with no streaming transport, notifications/subscribe returns -32601 method-not-found, and the §25.5 operational event source that would feed the subscription is itself unbuilt")

	opsprocess.SkipUnlessAvailable(t)
	ops := opsprocess.StartWith(t)
	ctx := context.Background()

	// §25.12: the agent subscribes to operational events over MCP by
	// sending notifications/subscribe with optional event-type filters.
	subBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "notifications/subscribe",
		"params": map[string]any{
			// §25.5 event-type filter tokens, mirroring the
			// GET /v1/admin/events ?eventType= selector.
			"eventType": []string{"alert_fired"},
		},
	})
	if err != nil {
		t.Fatalf("marshal notifications/subscribe request: %v", err)
	}
	subReq, err := http.NewRequestWithContext(ctx, http.MethodPost, ops.BaseURL()+"/mcp/management", bytes.NewReader(subBody))
	if err != nil {
		t.Fatalf("build /mcp/management subscribe request: %v", err)
	}
	subReq.Header.Set("Content-Type", "application/json")
	subReq.Header.Set("X-Lenny-Role", "platform-admin")
	subResp, err := http.DefaultClient.Do(subReq)
	if err != nil {
		t.Fatalf("POST /mcp/management notifications/subscribe: %v", err)
	}
	defer func() { _ = subResp.Body.Close() }()
	subRaw, _ := io.ReadAll(subResp.Body)

	// §25.12 requires the server to support notifications/subscribe; a
	// method-not-found rejection means the MCP-native subscription
	// surface is absent.
	var subEnvelope struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(subRaw, &subEnvelope); err != nil {
		t.Fatalf("decode subscribe response: %v (body %s)", err, subRaw)
	}
	if subEnvelope.Error != nil {
		t.Fatalf("notifications/subscribe rejected with code %d; §25.12 requires the management server to support MCP-native event subscription (body %s)", subEnvelope.Error.Code, subRaw)
	}

	// The full journey the scaffold documents: emit an operational
	// event that matches the type filter, read the matching record from
	// GET /v1/admin/events (§25.5) as the reference payload, then read
	// the notifications/message frame delivered over the MCP streaming
	// transport and assert the two payloads are schema-identical
	// (identical CloudEvents envelope: specversion, type, source, id,
	// and the lenny* extension attributes). That transport, and the
	// event-source wiring behind it, are what this finding is blocked on.
	_ = subRaw
}
