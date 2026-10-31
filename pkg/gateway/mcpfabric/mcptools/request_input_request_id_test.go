// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// TestRequestInputIgnoresCallerRequestID_spec_15_4 pins that the gateway
// generates the lenny/request_input requestId: a caller-supplied requestId is
// ignored, so a peer answering with that value does not resolve the wait, and
// the gateway-generated value does.
//
// spec: §15.4 (Runtime Adapter Specification, "Additional adapter-injected fields"), §8.5 (inReplyTo resolution)
func TestRequestInputIgnoresCallerRequestID_spec_15_4(t *testing.T) {
	srv, store, reg := newMCPForInput(t, 5*time.Second)
	mkSession(t, store, "sess_rid", session.StateRunning, "")
	h := srv.Handler()

	got := make(chan map[string]any, 1)
	go func() {
		got <- call(t, h, "lenny/request_input",
			`{"sessionId":"sess_rid","requestId":"req-chosen","parts":[{"type":"text","text":"pick"}]}`)
	}()
	req := waitPending(t, reg, "sess_rid")
	if req == "req-chosen" || !strings.HasPrefix(req, "req_") {
		t.Fatalf("pending requestId = %q, want a gateway-generated req_ value rather than the caller's", req)
	}
	if reg.Pending("sess_rid", "req-chosen") {
		t.Fatal("the caller-supplied requestId is registered")
	}

	// A peer answering with the caller's value does not resolve the wait; the
	// message falls through to ordinary delivery.
	resp := call(t, h, "lenny/send_message",
		`{"to":"sess_rid","message":"red","inReplyTo":"req-chosen"}`)
	if text := resultText(t, resp); strings.Contains(text, `"resolved"`) {
		t.Errorf("send_message with the caller's requestId resolved a wait: %s", text)
	}
	if !reg.Pending("sess_rid", req) {
		t.Fatal("the gateway-generated wait ended on a reply naming the caller's requestId")
	}

	resp = call(t, h, "lenny/send_message",
		fmt.Sprintf(`{"to":"sess_rid","message":"blue","inReplyTo":%q}`, req))
	if text := resultText(t, resp); !strings.Contains(text, `"resolved":"`+req+`"`) {
		t.Errorf("send_message with the generated requestId = %q, want resolved %s", text, req)
	}
	select {
	case ri := <-got:
		text := resultText(t, ri)
		if !strings.Contains(text, `"requestId":"`+req+`"`) || !strings.Contains(text, `"answer":"blue"`) {
			t.Errorf("request_input result = %q, want requestId %s and answer blue", text, req)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request_input did not return after the generated requestId resolved it")
	}
}

// TestRequestInputGeneratesDistinctRequestIDs_spec_15_4 pins that two waits on
// one session receive distinct gateway-generated requestIds, including when
// both callers supply the same requestId. The server runs the production ID
// source (Deps.IDFunc unset) rather than the fixed one newMCPForInput injects.
//
// spec: §15.4 (Runtime Adapter Specification, "Additional adapter-injected fields")
func TestRequestInputGeneratesDistinctRequestIDs_spec_15_4(t *testing.T) {
	store := memstore.New()
	reg := inputwait.NewRegistry()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		SessionService:      ownerprincipal.SessionAccess{},
		DevTenantFallback:   true,
		Store:               store,
		Executor:            executor.NewEchoExecutor(),
		InputWaits:          reg,
		RequestInputTimeout: 5 * time.Second,
		TenantID:            "acme",
	})
	mkSession(t, store, "sess_two", session.StateRunning, "")
	h := srv.Handler()
	args := `{"sessionId":"sess_two","requestId":"req-same","parts":[{"type":"text","text":"q"}]}`

	done := make(chan map[string]any, 2)
	go func() { done <- call(t, h, "lenny/request_input", args) }()
	first := waitPending(t, reg, "sess_two")
	go func() { done <- call(t, h, "lenny/request_input", args) }()
	second := waitPending(t, reg, "sess_two", first)
	if first == second {
		t.Fatalf("both waits carry requestId %q", first)
	}
	reg.Cancel("sess_two", first)
	reg.Cancel("sess_two", second)
	for range 2 {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("a cancelled request_input did not return")
		}
	}
}

// TestRequestInputSchemaDeclaresNoRequestID_spec_15_4 pins that the
// lenny/request_input input schema offers no requestId argument: the runtime
// supplies only parts.
//
// spec: §15.4 (Runtime Adapter Specification, "Additional adapter-injected fields")
func TestRequestInputSchemaDeclaresNoRequestID_spec_15_4(t *testing.T) {
	srv, _, _ := newMCPForInput(t, time.Second)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body)))
	var resp struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				InputSchema struct {
					Properties map[string]json.RawMessage `json:"properties"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode tools/list: %v; body=%s", err, rr.Body.String())
	}
	for _, tl := range resp.Result.Tools {
		if tl.Name != "lenny/request_input" {
			continue
		}
		if _, ok := tl.InputSchema.Properties["requestId"]; ok {
			t.Errorf("lenny/request_input schema declares requestId: %v", tl.InputSchema.Properties)
		}
		if _, ok := tl.InputSchema.Properties["parts"]; !ok {
			t.Errorf("lenny/request_input schema lacks parts: %v", tl.InputSchema.Properties)
		}
		return
	}
	t.Fatal("tools/list does not advertise lenny/request_input")
}
