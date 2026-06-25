// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract test for §15.2.1 REST ↔ MCP parity on the
// inbox-unavailable message-send outcome. §15.4 mandates that every
// send_message returns a `delivery_receipt` and defines
// `inbox_unavailable` strictly as a receipt `status:"error"`/`reason`
// value. Before F-MS4 the REST `/messages` handler returned a
// `503 INBOX_UNAVAILABLE` error envelope while the MCP `lenny/send_message`
// tool returned the 200 receipt form, a divergence on the same logical
// outcome. This test pins both surfaces to the identical receipt
// status/reason so a future regression that reintroduces the 503 envelope
// on either side fails the build.
package rest_mcp_consistency_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// receiptStatusReason is the §15.4 receipt fields the parity assertion
// compares across the two surfaces.
type receiptStatusReason struct {
	Status string
	Reason string
}

// newUnwiredMessagingServers stands up REST and MCP servers that share a
// session store and an EchoExecutor but wire no §7.2 messaging
// coordinator, so the buffered message path fails the enqueue on both
// surfaces and surfaces the inbox_unavailable receipt. The REST surface
// needs the executor wired (its /messages handler rejects early with
// EXECUTOR_UNAVAILABLE when none is present); the inbox-enqueue failure
// is reached only after that guard, on the §7.2 path-6 buffered branch.
func newUnwiredMessagingServers(t *testing.T) (*httptest.Server, *httptest.Server, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	exec := executor.NewEchoExecutor()

	rest := sessionserver.New(store, sessionserver.Options{Executor: exec})
	tsREST := httptest.NewServer(rest.Handler())
	t.Cleanup(tsREST.Close)

	mcpSrv := mcp.NewServer()
	mcptools.Register(mcpSrv, mcptools.Deps{
		Store:    store,
		Executor: exec,
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_mcp_unavailable" },
		TenantID: "acme",
		// No Messaging coordinator: the buffered branch fails the enqueue.
	})
	tsMCP := httptest.NewServer(mcpSrv.Handler())
	t.Cleanup(tsMCP.Close)

	return tsREST, tsMCP, store
}

// seedSuspended writes a `suspended` session into the shared store so the
// §7.2 path-6 buffered branch runs on both surfaces. With no messaging
// coordinator wired (newConsistencyServers wires none), the buffered
// branch fails the enqueue and surfaces inbox_unavailable.
func seedSuspended(t *testing.T, store sessionstore.Store, id string) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", UserID: "alice@acme.com",
		State: session.StateSuspended, RuntimeRef: "claude-code",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed suspended session: %v", err)
	}
}

// restReceipt drives REST POST /v1/sessions/{id}/messages and returns the
// §15.4 delivery receipt status/reason. It asserts the §15.2.1 / F-MS4
// contract that an inbox-enqueue failure is a 200 receipt rather than a
// 503 error envelope.
func restReceipt(t *testing.T, restURL, id string) receiptStatusReason {
	t.Helper()
	resp, raw := postJSON(t, restURL+"/v1/sessions/"+id+"/messages", "acme",
		[]byte(`{"messages":[{"role":"user","content":"x"}]}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("REST /messages status = %d, want 200 (F-MS4: inbox_unavailable is a receipt, not a 503); body=%s",
			resp.StatusCode, raw)
	}
	var body struct {
		DeliveryReceipt struct {
			Status string `json:"status"`
			Reason string `json:"reason"`
		} `json:"deliveryReceipt"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("REST receipt decode: %v\nbody=%s", err, raw)
	}
	return receiptStatusReason{body.DeliveryReceipt.Status, body.DeliveryReceipt.Reason}
}

// mcpReceipt drives the MCP lenny/send_message tool and returns the §15.4
// delivery receipt status/reason from its text payload.
func mcpReceipt(t *testing.T, mcpURL, id string) receiptStatusReason {
	t.Helper()
	res := mcpCall(t, mcpURL+"/mcp", "lenny/send_message", map[string]any{
		"to":      id,
		"message": "x",
	})
	payload := mcpToolPayload(t, res)
	receipt, ok := payload["deliveryReceipt"].(map[string]any)
	if !ok {
		t.Fatalf("MCP send_message payload has no deliveryReceipt: %v", payload)
	}
	status, _ := receipt["status"].(string)
	reason, _ := receipt["reason"].(string)
	return receiptStatusReason{status, reason}
}

// spec: §15.2.1 (REST/MCP parity), §15.4 (inbox_unavailable receipt)
// diagnosis: a failure here means the REST and MCP send_message surfaces
// disagree on the inbox-unavailable outcome — the REST surface either
// returned a 503 error envelope (the retired F-MS4 contract) or a
// receipt whose status/reason differs from the MCP receipt. §15.4 binds
// the delivery_receipt across both transports, so a divergence breaks the
// §15.2.1 consistency contract and a client cannot rely on one parsing.
func TestSendMessageInboxUnavailableParity_spec_15_4(t *testing.T) {
	tsREST, tsMCP, store := newUnwiredMessagingServers(t)
	seedSuspended(t, store, "sess_unavailable")

	rest := restReceipt(t, tsREST.URL, "sess_unavailable")
	mcp := mcpReceipt(t, tsMCP.URL, "sess_unavailable")

	// Both surfaces report the §15.4 error/inbox_unavailable receipt.
	want := receiptStatusReason{
		Status: string(session.DeliveryStatusError),
		Reason: string(session.DeliveryReasonInboxUnavailable),
	}
	if rest != want {
		t.Errorf("REST receipt = %+v, want %+v", rest, want)
	}
	if mcp != want {
		t.Errorf("MCP receipt = %+v, want %+v", mcp, want)
	}
	// The two surfaces are identical on the same logical outcome.
	if rest != mcp {
		t.Errorf("REST/MCP receipt parity broken: REST=%+v MCP=%+v", rest, mcp)
	}
}
