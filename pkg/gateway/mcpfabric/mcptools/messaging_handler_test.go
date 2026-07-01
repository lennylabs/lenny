// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// newMCPMessaging builds an MCP server whose lenny/send_message handler
// is configured with the given §7.2 messagingScope (default + ceiling)
// and §8.3 rate limits, for the F-7.2.6 governance tests.
func newMCPMessaging(t *testing.T, def, max session.MessagingScope, limits mcptools.MessagingRateLimit) (*mcp.Server, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:                 store,
		Executor:              executor.NewEchoExecutor(),
		InputWaits:            inputwait.NewRegistry(),
		Clock:                 func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:                func() string { return "sess_mcp" },
		TenantID:              "acme",
		MessagingDefaultScope: def,
		MessagingMaxScope:     max,
		MessagingRateLimit:    limits,
	})
	return srv, store
}

// receiptFromResult extracts the §15.4 delivery_receipt from the first
// content block of a lenny/send_message tool result.
func receiptFromResult(t *testing.T, resp map[string]any) session.DeliveryReceipt {
	t.Helper()
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		content, _ := result["content"].([]any)
		c0, _ := content[0].(map[string]any)
		t.Fatalf("send_message returned an error, want a receipt: %v", c0["text"])
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("send_message returned no content blocks: %+v", result)
	}
	first, _ := content[0].(map[string]any)
	body, _ := first["text"].(string)
	var envelope struct {
		DeliveryReceipt session.DeliveryReceipt `json:"deliveryReceipt"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("first block is not a deliveryReceipt envelope: %v; body=%s", err, body)
	}
	return envelope.DeliveryReceipt
}

// TestSendMessageScopeDirectRejectsSibling_spec_7_2_240 — under the
// default `direct` scope a sibling target is rejected with SCOPE_DENIED.
// This is the §7.2 line 240 default (siblings are opt-in), correcting the
// pre-fix behaviour that admitted siblings unconditionally. F-7.2.6.
func TestSendMessageScopeDirectRejectsSibling_spec_7_2_240(t *testing.T) {
	srv, store := newMCP(t) // default config resolves to `direct`
	mkSession(t, store, "sess_parent", session.StateRunning, "")
	mkSession(t, store, "sess_a", session.StateRunning, "sess_parent")
	mkSession(t, store, "sess_b", session.StateRunning, "sess_parent")
	resp := call(t, srv.Handler(), "lenny/send_message",
		`{"to":"sess_a","message":"hi","fromSessionId":"sess_b"}`)
	result, _ := resp["result"].(map[string]any)
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "SCOPE_DENIED" {
		t.Errorf("sibling under `direct` scope must be SCOPE_DENIED; code = %v", env["code"])
	}
}

// TestSendMessageScopeSiblingsAdmitsSibling_spec_7_2_241 — when the
// deployment configures `siblings` scope, a sibling target is admitted.
// F-7.2.6.
func TestSendMessageScopeSiblingsAdmitsSibling_spec_7_2_241(t *testing.T) {
	srv, store := newMCPMessaging(t, session.MessagingScopeSiblings, session.MessagingScopeSiblings, mcptools.MessagingRateLimit{})
	mkSession(t, store, "sess_parent", session.StateRunning, "")
	mkSession(t, store, "sess_a", session.StateRunning, "sess_parent")
	mkSession(t, store, "sess_b", session.StateRunning, "sess_parent")
	resp := call(t, srv.Handler(), "lenny/send_message",
		`{"to":"sess_a","message":"hi","fromSessionId":"sess_b"}`)
	if got := receiptFromResult(t, resp); got.Status != session.DeliveryStatusDelivered {
		t.Errorf("sibling under `siblings` scope should be delivered; status = %q", got.Status)
	}
}

// TestSendMessageRateLimitedReceipt_spec_7_2_371 — a send that exceeds
// the §8.3 maxInboundPerMinute cap returns a RATE_LIMITED delivery
// receipt (not a tool error). spec: §7.2 line 371; §8.3 line 309.
// F-7.2.6.
func TestSendMessageRateLimitedReceipt_spec_7_2_371(t *testing.T) {
	srv, store := newMCPMessaging(t, "", "", mcptools.MessagingRateLimit{MaxInboundPerMinute: 1})
	mkSession(t, store, "sess_t", session.StateRunning, "")
	if got := receiptFromResult(t, call(t, srv.Handler(), "lenny/send_message",
		`{"to":"sess_t","message":"one"}`)); got.Status != session.DeliveryStatusDelivered {
		t.Fatalf("first send should be delivered; status = %q", got.Status)
	}
	got := receiptFromResult(t, call(t, srv.Handler(), "lenny/send_message",
		`{"to":"sess_t","message":"two"}`))
	if got.Status != session.DeliveryStatusRateLimited {
		t.Errorf("second send should be rate_limited (maxInboundPerMinute=1); status = %q", got.Status)
	}
	if got.MessageID == "" {
		t.Error("rate_limited receipt must carry a gateway-assigned messageId")
	}
}

// foreignTenantStore returns a cross-tenant row for one designated id so
// the §7.2 line 268 cross-tenant guard can be exercised at the handler
// level — the production tenant-scoped store never surfaces a foreign
// row (its Get returns ErrNotFound and never leaks foreign sessions).
type foreignTenantStore struct {
	*memstore.Store
	foreignID string
}

func (s *foreignTenantStore) Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	if id == s.foreignID {
		return sessionstore.Session{ID: id, TenantID: "globex", State: session.StateRunning}, nil
	}
	return s.Store.Get(ctx, tenantID, id)
}

// TestSendMessageCrossTenantDenied_spec_7_2_268 — a target session in a
// different tenant is rejected with CROSS_TENANT_MESSAGE_DENIED before
// scope evaluation or rate limiting. F-7.2.6.
func TestSendMessageCrossTenantDenied_spec_7_2_268(t *testing.T) {
	store := &foreignTenantStore{Store: memstore.New(), foreignID: "sess_foreign"}
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:      store,
		Executor:   executor.NewEchoExecutor(),
		InputWaits: inputwait.NewRegistry(),
		Clock:      func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:     func() string { return "sess_mcp" },
		TenantID:   "acme",
	})
	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_foreign","message":"hi"}`)
	result, _ := resp["result"].(map[string]any)
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "CROSS_TENANT_MESSAGE_DENIED" {
		t.Errorf("foreign-tenant target must be CROSS_TENANT_MESSAGE_DENIED; code = %v", env["code"])
	}
}
