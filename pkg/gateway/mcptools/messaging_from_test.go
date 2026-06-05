// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// fromCapturingExecutor records the executor.Message values delivered to
// each target so a test can assert the §15.4.1 from-object the gateway
// stamps before delivery. F-13.5.11.
type fromCapturingExecutor struct {
	mu   sync.Mutex
	msgs map[string][]executor.Message
}

func (e *fromCapturingExecutor) Send(_ context.Context, sessionID string, msgs []executor.Message) (executor.Response, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.msgs[sessionID] = append(e.msgs[sessionID], msgs...)
	return executor.Response{Parts: []executor.OutputPart{{Type: "text", Text: "ok"}}}, nil
}

func (e *fromCapturingExecutor) Close(context.Context, string) error { return nil }

func (e *fromCapturingExecutor) delivered(sessionID string) []executor.Message {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]executor.Message(nil), e.msgs[sessionID]...)
}

// newMCPCapturingFrom builds an MCP server whose lenny/send_message
// delivers to a from-capturing executor under `siblings` scope so an
// inter-session send is admitted and its delivered envelope is inspectable.
func newMCPCapturingFrom(t *testing.T) (*mcp.Server, sessionstore.Store, *fromCapturingExecutor) {
	t.Helper()
	store := memstore.New()
	exec := &fromCapturingExecutor{msgs: map[string][]executor.Message{}}
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:                 store,
		Executor:              exec,
		InputWaits:            inputwait.NewRegistry(),
		Clock:                 func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:                func() string { return "sess_mcp" },
		TenantID:              "acme",
		MessagingDefaultScope: session.MessagingScopeSiblings,
		MessagingMaxScope:     session.MessagingScopeSiblings,
	})
	return srv, store, exec
}

// TestSendMessageStampsAgentFrom_spec_13_5_11 — an inter-session
// lenny/send_message from an authenticated sibling is delivered with the
// gateway-set §15.4.1 from-object (kind `agent`, id = sending session) so
// the target can attribute the message. The gateway sets `from` from the
// sender's identity; the caller never supplies it. F-13.5.11.
func TestSendMessageStampsAgentFrom_spec_13_5_11(t *testing.T) {
	srv, store, exec := newMCPCapturingFrom(t)
	mkSession(t, store, "sess_parent", session.StateRunning, "")
	mkSession(t, store, "sess_a", session.StateRunning, "sess_parent")
	mkSession(t, store, "sess_b", session.StateRunning, "sess_parent")

	resp := call(t, srv.Handler(), "lenny/send_message",
		`{"to":"sess_a","message":"hi","fromSessionId":"sess_b"}`)
	if got := receiptFromResult(t, resp); got.Status != session.DeliveryStatusDelivered {
		t.Fatalf("sibling send under `siblings` scope should be delivered; status = %q", got.Status)
	}
	delivered := exec.delivered("sess_a")
	if len(delivered) != 1 {
		t.Fatalf("want exactly one delivered message; got %d", len(delivered))
	}
	if from := delivered[0].From; from.Kind != "agent" || from.ID != "sess_b" {
		t.Errorf("delivered message must carry from{kind:agent,id:sess_b}; got %+v", from)
	}
}

// TestSendMessageUnattributedFromZero_spec_13_5_11 — a send with no
// principal binding and no fromSessionId carries no attribution, so the
// gateway leaves From at its zero value and the executor applies its
// default gateway-client identity rather than forging an agent origin.
// F-13.5.11.
func TestSendMessageUnattributedFromZero_spec_13_5_11(t *testing.T) {
	srv, store, exec := newMCPCapturingFrom(t)
	mkSession(t, store, "sess_t", session.StateRunning, "")

	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_t","message":"hi"}`)
	if got := receiptFromResult(t, resp); got.Status != session.DeliveryStatusDelivered {
		t.Fatalf("unattributed send should be delivered; status = %q", got.Status)
	}
	delivered := exec.delivered("sess_t")
	if len(delivered) != 1 {
		t.Fatalf("want exactly one delivered message; got %d", len(delivered))
	}
	if from := delivered[0].From; from.Kind != "" || from.ID != "" {
		t.Errorf("unattributed send must leave From zero (executor defaults to client); got %+v", from)
	}
}
