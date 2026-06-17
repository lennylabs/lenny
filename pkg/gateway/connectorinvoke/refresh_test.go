// SPDX-License-Identifier: MIT

package connectorinvoke

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/capabilityinference"
	"github.com/lennylabs/lenny/pkg/gateway/connectorcredstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
)

// captureHandler is a minimal slog.Handler that records every WARN-level
// message verbatim so a test can assert the exact §5.1 line 327 wording.
type captureHandler struct{ msgs *[]string }

func (h captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h captureHandler) Handle(_ context.Context, r slog.Record) error {
	*h.msgs = append(*h.msgs, r.Message)
	return nil
}
func (h captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h captureHandler) WithGroup(string) slog.Handler      { return h }

// toolsListResp builds the tools/list JSON-RPC result body for a list of
// (name, annotations-json) pairs.
func toolsListResp(tools string) *http.Response {
	return jsonResp(200, `{"jsonrpc":"2.0","id":2,"result":{"tools":`+tools+`}}`, nil)
}

// TestRefreshCapabilitiesInfersAndPersists_spec_9_3_136 verifies the
// refresh dials tools/list, infers each tool's §5.1 capability set from
// its MCP annotations, and persists the union, per-tool map, and mode.
func TestRefreshCapabilitiesInfersAndPersists_spec_9_3_136(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedConnector(t, connectors, connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.example",
	})
	doer := &fakeDoer{
		responses: []*http.Response{
			jsonResp(200, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`, nil),
			jsonResp(202, ``, nil),
			toolsListResp(`[
				{"name":"read_file","annotations":{"readOnlyHint":true}},
				{"name":"delete_file","annotations":{"destructiveHint":true}}
			]`),
		},
	}
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, nil).
		WithClock(clock)

	res, err := iv.RefreshCapabilities(context.Background(), "acme", "github", "alice", "")
	if err != nil {
		t.Fatalf("RefreshCapabilities: %v", err)
	}
	if got := res.ToolCapabilities["read_file"]; len(got) != 1 || got[0] != capabilityinference.CapRead {
		t.Errorf("read_file caps = %v, want [read]", got)
	}
	wantDelete := []capabilityinference.Capability{capabilityinference.CapWrite, capabilityinference.CapDelete}
	if got := res.ToolCapabilities["delete_file"]; !capsEqual(got, wantDelete) {
		t.Errorf("delete_file caps = %v, want %v", got, wantDelete)
	}
	// Union is read+write+delete, in capabilityOrder.
	wantUnion := []capabilityinference.Capability{capabilityinference.CapRead, capabilityinference.CapWrite, capabilityinference.CapDelete}
	if !capsEqual(res.Capabilities, wantUnion) {
		t.Errorf("union = %v, want %v", res.Capabilities, wantUnion)
	}
	if res.Mode != capabilityinference.ModeStrict {
		t.Errorf("mode = %q, want strict", res.Mode)
	}

	// The inferred metadata is persisted on the connector row.
	stored, err := connectors.Get(context.Background(), "acme", "github")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !capsEqual(stored.Capabilities, wantUnion) {
		t.Errorf("persisted capabilities = %v, want %v", stored.Capabilities, wantUnion)
	}
	if got := stored.ToolCapabilities["delete_file"]; !capsEqual(got, wantDelete) {
		t.Errorf("persisted delete_file = %v, want %v", got, wantDelete)
	}
	if stored.CapabilityInferenceMode != capabilityinference.ModeStrict {
		t.Errorf("persisted mode = %q, want strict", stored.CapabilityInferenceMode)
	}
	if !stored.CapabilitiesRefreshedAt.Equal(clock()) {
		t.Errorf("refreshedAt = %v, want %v", stored.CapabilitiesRefreshedAt, clock())
	}
}

// TestRefreshCapabilitiesStrictUnannotatedWarns_spec_5_1_327 verifies an
// unannotated tool under the default strict mode is inferred as admin and
// reported in UnannotatedAdminTools (the §5.1 line 327 WARN signal).
func TestRefreshCapabilitiesStrictUnannotatedWarns_spec_5_1_327(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedConnector(t, connectors, connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.example",
	})
	doer := &fakeDoer{
		responses: []*http.Response{
			jsonResp(200, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`, nil),
			jsonResp(202, ``, nil),
			toolsListResp(`[{"name":"mystery_tool"}]`),
		},
	}
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, nil).WithClock(clock)
	res, err := iv.RefreshCapabilities(context.Background(), "acme", "github", "alice", "")
	if err != nil {
		t.Fatalf("RefreshCapabilities: %v", err)
	}
	if got := res.ToolCapabilities["mystery_tool"]; len(got) != 1 || got[0] != capabilityinference.CapAdmin {
		t.Errorf("mystery_tool caps = %v, want [admin]", got)
	}
	if len(res.UnannotatedAdminTools) != 1 || res.UnannotatedAdminTools[0] != "mystery_tool" {
		t.Errorf("UnannotatedAdminTools = %v, want [mystery_tool]", res.UnannotatedAdminTools)
	}
}

// TestRefreshCapabilitiesEmitsVerbatimWarnMessage_spec_5_1_327 verifies
// the production discovery path logs the verbatim §5.1 line 327 WARN via
// capabilityinference.WarnMessage (its sole production caller), not a
// divergent inline string.
func TestRefreshCapabilitiesEmitsVerbatimWarnMessage_spec_5_1_327(t *testing.T) {
	var msgs []string
	prev := slog.Default()
	slog.SetDefault(slog.New(captureHandler{msgs: &msgs}))
	t.Cleanup(func() { slog.SetDefault(prev) })

	connectors := connectorstore.NewMemory()
	seedConnector(t, connectors, connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.example",
	})
	doer := &fakeDoer{
		responses: []*http.Response{
			jsonResp(200, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`, nil),
			jsonResp(202, ``, nil),
			toolsListResp(`[{"name":"mystery_tool"}]`),
		},
	}
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, nil).WithClock(clock)
	if _, err := iv.RefreshCapabilities(context.Background(), "acme", "github", "alice", ""); err != nil {
		t.Fatalf("RefreshCapabilities: %v", err)
	}

	want := capabilityinference.WarnMessage("mystery_tool", "github")
	found := false
	for _, m := range msgs {
		if m == want {
			found = true
		}
	}
	if !found {
		t.Errorf("captured WARN messages = %v, want one equal to %q", msgs, want)
	}
}

// TestRefreshCapabilitiesPermissiveUnannotatedIsWrite_spec_5_1_329
// verifies permissive mode infers an unannotated tool as write and
// raises no WARN.
func TestRefreshCapabilitiesPermissiveUnannotatedIsWrite_spec_5_1_329(t *testing.T) {
	var msgs []string
	prev := slog.Default()
	slog.SetDefault(slog.New(captureHandler{msgs: &msgs}))
	t.Cleanup(func() { slog.SetDefault(prev) })

	connectors := connectorstore.NewMemory()
	seedConnector(t, connectors, connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.example",
		CapabilityInferenceMode: capabilityinference.ModePermissive,
	})
	doer := &fakeDoer{
		responses: []*http.Response{
			jsonResp(200, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`, nil),
			jsonResp(202, ``, nil),
			toolsListResp(`[{"name":"mystery_tool"}]`),
		},
	}
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, nil).WithClock(clock)
	res, err := iv.RefreshCapabilities(context.Background(), "acme", "github", "alice", "")
	if err != nil {
		t.Fatalf("RefreshCapabilities: %v", err)
	}
	if got := res.ToolCapabilities["mystery_tool"]; len(got) != 1 || got[0] != capabilityinference.CapWrite {
		t.Errorf("mystery_tool caps = %v, want [write]", got)
	}
	if len(res.UnannotatedAdminTools) != 0 {
		t.Errorf("UnannotatedAdminTools = %v, want empty under permissive", res.UnannotatedAdminTools)
	}
	if res.Mode != capabilityinference.ModePermissive {
		t.Errorf("mode = %q, want permissive", res.Mode)
	}
	if len(msgs) != 0 {
		t.Errorf("permissive mode emitted WARN messages %v, want none (§5.1 line 329 suppresses the warning)", msgs)
	}
}

// TestRefreshCapabilitiesInactiveConnector_spec_9_3_140 verifies a
// soft-deleted connector is not dialed for a refresh.
func TestRefreshCapabilitiesInactiveConnector_spec_9_3_140(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedConnector(t, connectors, connectorstore.Connector{
		TenantID: "acme", ID: "gone", MCPServerURL: "https://mcp.gone.example",
	})
	if err := connectors.SoftDelete(context.Background(), "acme", "gone", clock()); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	doer := &fakeDoer{}
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, nil).WithClock(clock)
	if _, err := iv.RefreshCapabilities(context.Background(), "acme", "gone", "alice", ""); err == nil {
		t.Fatal("expected ErrConnectorInactive")
	}
	if len(doer.reqs) != 0 {
		t.Errorf("dialed a soft-deleted connector %d times, want 0", len(doer.reqs))
	}
}

func capsEqual(a, b []capabilityinference.Capability) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
