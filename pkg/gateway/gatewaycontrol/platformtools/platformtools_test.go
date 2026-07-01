// SPDX-License-Identifier: MIT

package platformtools_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/gatewaycontrol/platformtools"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// fakeDispatcher records the context it is dispatched under so a test can
// assert the §9.1 principal the bridge installs.
type fakeDispatcher struct {
	catalog    []mcp.Tool
	result     mcp.ToolResult
	ok         bool
	err        error
	gotName    string
	gotPrinc   authmw.Principal
	gotPrincOK bool
}

func (d *fakeDispatcher) Catalog() []mcp.Tool { return d.catalog }

func (d *fakeDispatcher) DispatchTool(ctx context.Context, name string, _ json.RawMessage) (mcp.ToolResult, bool, error) {
	d.gotName = name
	d.gotPrinc, d.gotPrincOK = authmw.FromContext(ctx)
	return d.result, d.ok, d.err
}

type fakeSessions map[string]sessionstore.Session

func (f fakeSessions) GetByID(_ context.Context, id string) (sessionstore.Session, error) {
	s, ok := f[id]
	if !ok {
		return sessionstore.Session{}, sessionstore.ErrNotFound
	}
	return s, nil
}

// spec: §9.1 line 14 — CallPlatformTool dispatches under the calling
// session's principal (session, tenant, owner) so the platform tool
// handlers resolve the same identity a gateway-edge /mcp call would.
func TestBridgeCallInstallsSessionPrincipal_spec_9_1(t *testing.T) {
	disp := &fakeDispatcher{ok: true, result: mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: "ok"}}}}
	sessions := fakeSessions{"sess_1": {ID: "sess_1", TenantID: "acme", UserID: "alice"}}
	b := platformtools.New(disp, sessions)

	resultJSON, isErr, err := b.CallPlatformTool(context.Background(), "sess_1", "lenny/delegate_task", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CallPlatformTool err = %v", err)
	}
	if isErr {
		t.Errorf("isError = true, want false")
	}
	if disp.gotName != "lenny/delegate_task" {
		t.Errorf("dispatched tool = %q", disp.gotName)
	}
	if !disp.gotPrincOK {
		t.Fatalf("no principal installed on dispatch context")
	}
	if disp.gotPrinc.SessionID != "sess_1" || disp.gotPrinc.TenantID != "acme" || disp.gotPrinc.Subject != "alice" {
		t.Errorf("principal = %+v, want session=sess_1 tenant=acme subject=alice", disp.gotPrinc)
	}
	// The result is the marshalled MCP ToolResult.
	var rt struct {
		Content []map[string]string `json:"content"`
	}
	if err := json.Unmarshal(resultJSON, &rt); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(rt.Content) != 1 || rt.Content[0]["text"] != "ok" {
		t.Errorf("result = %s, want the dispatch result", resultJSON)
	}
}

// A tool-level failure is carried as an isError result with a nil error,
// not as a routing error.
func TestBridgeCallToolErrorIsIsErrorResult_spec_9_1(t *testing.T) {
	disp := &fakeDispatcher{ok: true, result: mcp.ToolResult{IsError: true, Content: []mcp.ToolContent{{Type: "text", Text: "bad"}}}}
	b := platformtools.New(disp, fakeSessions{"s": {ID: "s", TenantID: "t"}})
	_, isErr, err := b.CallPlatformTool(context.Background(), "s", "lenny/output", nil)
	if err != nil {
		t.Fatalf("err = %v, want nil for a tool-level failure", err)
	}
	if !isErr {
		t.Errorf("isError = false, want true")
	}
}

func TestBridgeCallUnknownSession_spec_9_1(t *testing.T) {
	b := platformtools.New(&fakeDispatcher{ok: true}, fakeSessions{})
	_, _, err := b.CallPlatformTool(context.Background(), "ghost", "lenny/output", nil)
	if !errors.Is(err, leasecontrol.ErrSessionNotFound) {
		t.Errorf("err = %v, want ErrSessionNotFound", err)
	}
}

func TestBridgeCallUnknownTool_spec_9_1(t *testing.T) {
	disp := &fakeDispatcher{ok: false} // DispatchTool reports the tool is not registered.
	b := platformtools.New(disp, fakeSessions{"s": {ID: "s", TenantID: "t"}})
	_, _, err := b.CallPlatformTool(context.Background(), "s", "lenny/nope", nil)
	if !errors.Is(err, leasecontrol.ErrPlatformToolNotFound) {
		t.Errorf("err = %v, want ErrPlatformToolNotFound", err)
	}
}

// spec: §9.1 lines 14-31 — ListPlatformTools returns the gateway catalog
// mapped to the descriptor the GatewayControl RPC serializes.
func TestBridgeListCatalog_spec_9_1(t *testing.T) {
	disp := &fakeDispatcher{catalog: []mcp.Tool{
		{Name: "lenny/delegate_task", Description: "delegate", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "lenny/memory_query"},
	}}
	b := platformtools.New(disp, fakeSessions{})
	tools, err := b.ListPlatformTools(context.Background(), "sess_1")
	if err != nil {
		t.Fatalf("ListPlatformTools err = %v", err)
	}
	if len(tools) != 2 || tools[0].Name != "lenny/delegate_task" || string(tools[0].InputSchema) != `{"type":"object"}` {
		t.Errorf("tools = %+v, want the gateway catalog", tools)
	}
}
