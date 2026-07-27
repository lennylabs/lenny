// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §7.2 path-6 pod-held resume-and-deliver
// reached from the platform MCP `lenny/send_message` tool against the real
// cmd/lenny-gateway binary. The existing tier-4 coverage drives path 6
// through the REST `POST /v1/sessions/{id}/messages` handler only; §7.2
// binds the path to both message sources, and only the live binary
// exercises the production wiring that hands the MCP handler the
// session server's coordinator-side resume.
package tier4_integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// createMCPTenant registers the tenant the platform MCP surface is bound to
// with a token quota, so the Redis-backed §11.2 quota evaluator resolves
// limits on the create path.
func createMCPTenant(t *testing.T, base string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"id": mcpTenant, "displayName": "Default", "tokenQuotaPerWindow": 1_000_000,
	})
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/admin/tenants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "platform")
	req.Header.Set("X-Lenny-User-ID", "ops@acme.com")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create MCP tenant: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// spec: §7.2 path 6 (spec/07_session-lifecycle.md:326-330) — "Pod still
// held: The gateway atomically resumes the session (`suspended → running`)
// and delivers the message to the runtime's stdin pipe ... The delivery
// receipt is `delivered` on successful resume-and-deliver", and "This
// applies uniformly to all message sources: external client
// (`POST /v1/sessions/{id}/messages`) and inter-session via
// `lenny/send_message`." §15.4 defines the `delivery` enum the tool
// carries.
//
// diagnosis: a failure means the live gateway does not reach the path-6
// resume-and-deliver from the MCP tool surface. Either the `lenny/send_message`
// tool no longer accepts the §15.4 `delivery` field (so the immediate flag
// never reaches the shared router and the message buffers with `queued`),
// or the binary no longer hands the MCP handler the session server's
// coordinator-side resume, in which case the handler correctly fails closed
// to `queued` and the wiring is the defect.
func TestMCPSendMessageImmediateResumesSuspendedSession(t *testing.T) {
	// A real Redis backs the §7.2 inbox/DLQ coordinator so a regression
	// that loses the resume yields the spec's `queued` buffering receipt
	// rather than an `inbox_unavailable` error.
	gateway.SkipUnlessAvailable(t)
	rd := containers.StartRedis(t, containers.RedisOptions{})
	// allow-all noEnvironmentPolicy so the §10.6 / §11.1 environment gate
	// admits a no-environment session create; the path-6 delivery is the
	// behaviour under test rather than the create-path gates.
	gw := gateway.StartWith(t, "--dev-mode", "--redis-url=redis://"+rd.Addr+"/0",
		"--no-environment-policy", "allow-all")
	c := mcpClient{t: t, base: gw.BaseURL()}

	// The MCP surface is bound to the `default` tenant, and the Redis-backed
	// §11.2 quota evaluator needs resolvable limits for it.
	code := createMCPTenant(t, gw.BaseURL())
	if code != http.StatusCreated && code != http.StatusConflict {
		t.Fatalf("create MCP tenant: status %d", code)
	}

	target := c.runningSession()

	// Interrupt suspends the session while it still holds its pod, which
	// is the §7.2 path-6 pod-held precondition.
	code, interrupted := c.rest(http.MethodPost, "/v1/sessions/"+target+"/interrupt", nil)
	if code != http.StatusOK {
		t.Fatalf("interrupt: status %d (%v)", code, interrupted)
	}
	if interrupted["state"] != "suspended" {
		t.Fatalf("state after interrupt = %v, want suspended", interrupted["state"])
	}

	// The MCP tool carries `delivery: "immediate"`, so the gateway resumes
	// the session and delivers rather than buffering.
	rpc := c.callTool("lenny/send_message",
		`{"to":"`+target+`","message":"wake up","delivery":"immediate"}`)
	text, isErr := toolResultText(t, rpc)
	if isErr {
		t.Fatalf("lenny/send_message with delivery:\"immediate\" returned an error: %s", text)
	}
	var out struct {
		DeliveryReceipt struct {
			Status      string `json:"status"`
			DeliveredAt string `json:"deliveredAt"`
		} `json:"deliveryReceipt"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("send_message result is not JSON: %v (%q)", err, text)
	}
	if out.DeliveryReceipt.Status != "delivered" {
		t.Errorf("deliveryReceipt.status = %q, want delivered (§7.2 path 6 pod-held resume-and-deliver)",
			out.DeliveryReceipt.Status)
	}
	if out.DeliveryReceipt.DeliveredAt == "" {
		t.Errorf("deliveryReceipt.deliveredAt is empty, want a timestamp on the delivered receipt")
	}

	code, after := c.rest(http.MethodGet, "/v1/sessions/"+target, nil)
	if code != http.StatusOK {
		t.Fatalf("get session after resume-and-deliver: status %d", code)
	}
	if after["state"] != "running" {
		t.Errorf("state after resume-and-deliver = %v, want running (§7.2 path 6 suspended → running)",
			after["state"])
	}
}
