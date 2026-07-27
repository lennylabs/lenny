// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract test for the §7.2 path-6 `delivery: "immediate"`
// resume-and-deliver across both message sources. §7.2 path 6 states that
// an immediate message to a `suspended` session whose pod is still held
// atomically resumes the session (`suspended → running`) and delivers,
// returning a `delivered` receipt, and that this "applies uniformly to all
// message sources: external client (`POST /v1/sessions/{id}/messages`) and
// inter-session via `lenny/send_message`". The MCP tool therefore has to
// carry the §15.4 `delivery` enum, and an inter-session immediate message
// has to reach the same resume-and-deliver outcome the REST surface
// reaches. This test drives the identical message through both surfaces
// against identically seeded suspended sessions and asserts the same
// receipt status and the same resulting session state, plus the §15.4
// closed-enum rejection of an unrecognized `delivery` value.
package rest_mcp_consistency_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// newImmediateResumeServers stands up REST and MCP surfaces over one
// session store and one EchoExecutor. Both surfaces are given the same
// coordinator-side resume primitive so the §7.2 path-6 pod-held branch is
// reachable from either message source.
func newImmediateResumeServers(t *testing.T) (restURL, mcpURL string, store sessionstore.Store) {
	t.Helper()
	store = memstore.New()
	exec := executor.NewEchoExecutor()

	// A §7.2 session inbox and DLQ are wired on both surfaces so a
	// regression that loses the resume falls back to the spec's `queued`
	// buffering rather than to an inbox_unavailable error, keeping the
	// failure legible.
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	coord := sessioninbox.NewCoordinator(sessioninbox.Config{
		Inbox: sessioninbox.NewMemoryInbox(10),
		DLQ:   sessioninbox.NewDLQ(rc, 10),
	})

	rest := sessionserver.New(store, sessionserver.Options{Executor: exec, Messaging: coord})
	tsREST := httptest.NewServer(rest.Handler())
	t.Cleanup(tsREST.Close)

	mcpSrv := mcp.NewServer()
	mcptools.Register(mcpSrv, mcptools.Deps{
		Store:    store,
		Executor: exec,
		// spec: §7.2 path 6 lines 326-330 — the inter-session surface drives
		// the same coordinator-side resume primitive the REST surface drives.
		HeldPodResumer: rest,
		Messaging:      coord,
		Clock:          func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:         func() string { return "sess_immediate_mcp" },
		TenantID:       "acme",
	})
	tsMCP := httptest.NewServer(mcpSrv.Handler())
	t.Cleanup(tsMCP.Close)

	return tsREST.URL, tsMCP.URL, store
}

// sessionState reads the current §7.2 state of id from the shared store.
func sessionState(t *testing.T, store sessionstore.Store, id string) session.State {
	t.Helper()
	row, err := store.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("read session %s: %v", id, err)
	}
	return row.State
}

// restImmediateReceipt drives REST POST /v1/sessions/{id}/messages with a
// `delivery: "immediate"` envelope and returns the §15.4 receipt status.
func restImmediateReceipt(t *testing.T, restURL, id string) string {
	t.Helper()
	resp, raw := postJSON(t, restURL+"/v1/sessions/"+id+"/messages", "acme",
		[]byte(`{"messages":[{"role":"user","content":"ping","delivery":"immediate"}]}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("REST /messages status = %d, want 200; body=%s", resp.StatusCode, raw)
	}
	var body struct {
		DeliveryReceipt struct {
			Status string `json:"status"`
		} `json:"deliveryReceipt"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("REST receipt decode: %v\nbody=%s", err, raw)
	}
	return body.DeliveryReceipt.Status
}

// mcpImmediateReceipt drives the MCP lenny/send_message tool with the
// §15.4 `delivery: "immediate"` field and returns the receipt status.
func mcpImmediateReceipt(t *testing.T, mcpURL, id string) string {
	t.Helper()
	res := mcpCall(t, mcpURL+"/mcp", "lenny/send_message", map[string]any{
		"to":       id,
		"message":  "ping",
		"delivery": "immediate",
	})
	payload := mcpToolPayload(t, res)
	receipt, ok := payload["deliveryReceipt"].(map[string]any)
	if !ok {
		t.Fatalf("MCP send_message payload has no deliveryReceipt: %v", payload)
	}
	status, _ := receipt["status"].(string)
	return status
}

// spec: §7.2 path 6 (suspended target, `delivery: "immediate"`, pod held →
// atomic resume-and-deliver, receipt `delivered`; "This applies uniformly
// to all message sources: external client (`POST /v1/sessions/{id}/messages`)
// and inter-session via `lenny/send_message`"), §15.4 (`delivery` enum,
// `delivery_receipt` schema), §15.2.1 (REST/MCP parity).
//
// diagnosis: a failure means the two message sources disagree on the §7.2
// path-6 immediate resume. The usual cause is that the MCP
// `lenny/send_message` tool cannot express `delivery: "immediate"` (no
// schema field, or the value is parsed but not threaded into the shared
// router), so an inter-session immediate message to a suspended session
// buffers with `queued` and leaves the session suspended while the same
// message over REST resumes and delivers.
func TestSendMessageImmediateResumesSuspendedParity_spec_7_2(t *testing.T) {
	restURL, mcpURL, store := newImmediateResumeServers(t)
	seedSuspended(t, store, "sess_immediate_rest")
	seedSuspended(t, store, "sess_immediate_mcp_target")

	restStatus := restImmediateReceipt(t, restURL, "sess_immediate_rest")
	restState := sessionState(t, store, "sess_immediate_rest")

	mcpStatus := mcpImmediateReceipt(t, mcpURL, "sess_immediate_mcp_target")
	mcpState := sessionState(t, store, "sess_immediate_mcp_target")

	wantStatus := string(session.DeliveryStatusDelivered)
	if restStatus != wantStatus {
		t.Errorf("REST receipt status = %q, want %q (§7.2 path 6 pod-held resume-and-deliver)", restStatus, wantStatus)
	}
	if restState != session.StateRunning {
		t.Errorf("REST target state = %q, want %q (§7.2 path 6 suspended → running)", restState, session.StateRunning)
	}
	if mcpStatus != wantStatus {
		t.Errorf("MCP receipt status = %q, want %q (§7.2 path 6 applies uniformly to lenny/send_message)", mcpStatus, wantStatus)
	}
	if mcpState != session.StateRunning {
		t.Errorf("MCP target state = %q, want %q (§7.2 path 6 suspended → running)", mcpState, session.StateRunning)
	}
	if restStatus != mcpStatus || restState != mcpState {
		t.Errorf("REST/MCP path-6 parity broken: REST=(%s,%s) MCP=(%s,%s)", restStatus, restState, mcpStatus, mcpState)
	}
}

// spec: §15.4 (`delivery` closed enum: "No other values are valid. The
// gateway rejects unknown `delivery` values with `400
// INVALID_DELIVERY_VALUE`."), §15.2.1 rule 5(b) (identical error code for
// identical invalid input across surfaces).
//
// diagnosis: a failure means one surface accepts a `delivery` value the
// closed enum does not define. Either the MCP tool ignores the field
// entirely (silently treating an unknown value as the `queued` default) or
// the REST validator drifted from the enum.
func TestSendMessageInvalidDeliveryValueParity_spec_15_4(t *testing.T) {
	restURL, mcpURL, store := newImmediateResumeServers(t)
	seedSuspended(t, store, "sess_bad_delivery")

	resp, raw := postJSON(t, restURL+"/v1/sessions/sess_bad_delivery/messages", "acme",
		[]byte(`{"messages":[{"role":"user","content":"ping","delivery":"asap"}]}`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("REST /messages status = %d, want 400 for an unrecognized delivery value; body=%s", resp.StatusCode, raw)
	}
	restErr := decodeRESTError(t, raw)
	if restErr.Code != "INVALID_DELIVERY_VALUE" {
		t.Errorf("REST error code = %q, want INVALID_DELIVERY_VALUE", restErr.Code)
	}

	res := mcpCall(t, mcpURL+"/mcp", "lenny/send_message", map[string]any{
		"to":       "sess_bad_delivery",
		"message":  "ping",
		"delivery": "asap",
	})
	mcpErr := decodeMCPError(t, res)
	if mcpErr.Code != "INVALID_DELIVERY_VALUE" {
		t.Errorf("MCP error code = %q, want INVALID_DELIVERY_VALUE (§15.4 closed enum, §15.2.1 rule 5(b))", mcpErr.Code)
	}
	// §15.2.1 rule 5(d): the category and retryable flag match across
	// surfaces for the same invalid input.
	if restErr != mcpErr {
		t.Errorf("REST/MCP invalid-delivery error parity broken: REST=%+v MCP=%+v", restErr, mcpErr)
	}
}
