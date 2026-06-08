// SPDX-License-Identifier: MIT

package leasecontrol_test

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/gateway/connectorinvoke"
	"github.com/lennylabs/lenny/pkg/gateway/leasecontrol"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// fakeConnectorTools is a leasecontrol.ConnectorToolService double.
type fakeConnectorTools struct {
	listConns   []leasecontrol.SessionConnectorDescriptor
	listConnErr error
	tools       []leasecontrol.PlatformToolDescriptor
	toolsErr    error
	callResult  []byte
	callIsErr   bool
	callErr     error
	gotSession  string
	gotConn     string
	gotTool     string
}

func (f *fakeConnectorTools) ListSessionConnectors(_ context.Context, sessionID string) ([]leasecontrol.SessionConnectorDescriptor, error) {
	f.gotSession = sessionID
	return f.listConns, f.listConnErr
}

func (f *fakeConnectorTools) ListConnectorTools(_ context.Context, sessionID, connectorID string) ([]leasecontrol.PlatformToolDescriptor, error) {
	f.gotSession = sessionID
	f.gotConn = connectorID
	return f.tools, f.toolsErr
}

func (f *fakeConnectorTools) CallConnectorTool(_ context.Context, sessionID, connectorID, toolName string, _ []byte) ([]byte, bool, error) {
	f.gotSession = sessionID
	f.gotConn = connectorID
	f.gotTool = toolName
	return f.callResult, f.callIsErr, f.callErr
}

func newServiceWithConnectors(t *testing.T, ct leasecontrol.ConnectorToolService) *leasecontrol.Service {
	t.Helper()
	budgets := leasecontrol.NewMemoryBudgetSource()
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets:        budgets,
		Tenants:        budgets,
		ConnectorTools: ct,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

// spec: §9.3 line 142 — ListSessionConnectors forwards the policy-filtered
// connector list. F-9.1.2.
func TestListSessionConnectorsSuccess_spec_9_3_142(t *testing.T) {
	ct := &fakeConnectorTools{listConns: []leasecontrol.SessionConnectorDescriptor{
		{ID: "github", DisplayName: "GitHub"},
		{ID: "slack", DisplayName: "Slack"},
	}}
	svc := newServiceWithConnectors(t, ct)
	resp, err := svc.ListSessionConnectors(context.Background(),
		&adapterv1.ListSessionConnectorsRequest{SessionId: &adapterv1.SessionId{Value: "sess_1"}})
	if err != nil {
		t.Fatalf("ListSessionConnectors err = %v", err)
	}
	if ct.gotSession != "sess_1" {
		t.Errorf("service got session %q", ct.gotSession)
	}
	if len(resp.GetConnectors()) != 2 || resp.GetConnectors()[0].GetId() != "github" || resp.GetConnectors()[0].GetDisplayName() != "GitHub" {
		t.Errorf("connectors = %+v, want the forwarded list", resp.GetConnectors())
	}
}

// spec: §9.3 — the three RPCs return Unimplemented when no connector
// service is wired (the §8.6-only GatewayControl deployment). F-9.1.2.
func TestConnectorRPCsUnimplementedWithoutService_spec_9_3_142(t *testing.T) {
	svc := newServiceWithConnectors(t, nil)
	if _, err := svc.ListSessionConnectors(context.Background(),
		&adapterv1.ListSessionConnectorsRequest{SessionId: &adapterv1.SessionId{Value: "s"}}); status.Code(err) != codes.Unimplemented {
		t.Errorf("ListSessionConnectors code = %v, want Unimplemented", status.Code(err))
	}
	if _, err := svc.ListConnectorTools(context.Background(),
		&adapterv1.ListConnectorToolsRequest{SessionId: &adapterv1.SessionId{Value: "s"}, ConnectorId: "github"}); status.Code(err) != codes.Unimplemented {
		t.Errorf("ListConnectorTools code = %v, want Unimplemented", status.Code(err))
	}
	if _, err := svc.CallConnectorTool(context.Background(),
		&adapterv1.CallConnectorToolRequest{SessionId: &adapterv1.SessionId{Value: "s"}, ConnectorId: "github", ToolName: "t"}); status.Code(err) != codes.Unimplemented {
		t.Errorf("CallConnectorTool code = %v, want Unimplemented", status.Code(err))
	}
}

// spec: §9.3 — empty session id / connector id / tool name are
// InvalidArgument before any forwarding. F-9.1.2.
func TestConnectorRPCsValidateArguments_spec_9_3_142(t *testing.T) {
	svc := newServiceWithConnectors(t, &fakeConnectorTools{})
	if _, err := svc.ListSessionConnectors(context.Background(),
		&adapterv1.ListSessionConnectorsRequest{SessionId: &adapterv1.SessionId{Value: ""}}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("empty session id code = %v, want InvalidArgument", status.Code(err))
	}
	if _, err := svc.ListConnectorTools(context.Background(),
		&adapterv1.ListConnectorToolsRequest{SessionId: &adapterv1.SessionId{Value: "s"}, ConnectorId: ""}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("empty connector id code = %v, want InvalidArgument", status.Code(err))
	}
	if _, err := svc.CallConnectorTool(context.Background(),
		&adapterv1.CallConnectorToolRequest{SessionId: &adapterv1.SessionId{Value: "s"}, ConnectorId: "github", ToolName: ""}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("empty tool name code = %v, want InvalidArgument", status.Code(err))
	}
}

// spec: §9.3 line 164 — a denied connector maps to PermissionDenied; an
// unknown session maps to NotFound. F-9.1.2.
func TestConnectorRPCsErrorMapping_spec_9_3_164(t *testing.T) {
	denied := &fakeConnectorTools{toolsErr: leasecontrol.ErrConnectorNotPermitted, callErr: leasecontrol.ErrConnectorNotPermitted}
	svc := newServiceWithConnectors(t, denied)
	if _, err := svc.ListConnectorTools(context.Background(),
		&adapterv1.ListConnectorToolsRequest{SessionId: &adapterv1.SessionId{Value: "s"}, ConnectorId: "github"}); status.Code(err) != codes.PermissionDenied {
		t.Errorf("denied ListConnectorTools code = %v, want PermissionDenied", status.Code(err))
	}
	if _, err := svc.CallConnectorTool(context.Background(),
		&adapterv1.CallConnectorToolRequest{SessionId: &adapterv1.SessionId{Value: "s"}, ConnectorId: "github", ToolName: "t"}); status.Code(err) != codes.PermissionDenied {
		t.Errorf("denied CallConnectorTool code = %v, want PermissionDenied", status.Code(err))
	}

	missing := &fakeConnectorTools{listConnErr: leasecontrol.ErrSessionNotFound, toolsErr: leasecontrol.ErrSessionNotFound, callErr: leasecontrol.ErrSessionNotFound}
	svc = newServiceWithConnectors(t, missing)
	if _, err := svc.ListSessionConnectors(context.Background(),
		&adapterv1.ListSessionConnectorsRequest{SessionId: &adapterv1.SessionId{Value: "s"}}); status.Code(err) != codes.NotFound {
		t.Errorf("unknown-session ListSessionConnectors code = %v, want NotFound", status.Code(err))
	}
	if _, err := svc.CallConnectorTool(context.Background(),
		&adapterv1.CallConnectorToolRequest{SessionId: &adapterv1.SessionId{Value: "s"}, ConnectorId: "github", ToolName: "t"}); status.Code(err) != codes.NotFound {
		t.Errorf("unknown-session CallConnectorTool code = %v, want NotFound", status.Code(err))
	}
}

// spec: §9.3 line 142 — CallConnectorTool returns the MCP result and the
// isError flag verbatim. F-9.1.2.
func TestCallConnectorToolSuccess_spec_9_3_142(t *testing.T) {
	ct := &fakeConnectorTools{callResult: []byte(`{"content":[{"type":"text","text":"x"}],"isError":true}`), callIsErr: true}
	svc := newServiceWithConnectors(t, ct)
	resp, err := svc.CallConnectorTool(context.Background(),
		&adapterv1.CallConnectorToolRequest{SessionId: &adapterv1.SessionId{Value: "sess_1"}, ConnectorId: "github", ToolName: "list_repos", Arguments: []byte(`{}`)})
	if err != nil {
		t.Fatalf("CallConnectorTool err = %v", err)
	}
	if ct.gotSession != "sess_1" || ct.gotConn != "github" || ct.gotTool != "list_repos" {
		t.Errorf("service got (%q, %q, %q)", ct.gotSession, ct.gotConn, ct.gotTool)
	}
	if !resp.GetIsError() || string(resp.GetResult()) != `{"content":[{"type":"text","text":"x"}],"isError":true}` {
		t.Errorf("resp = %+v, want the forwarded isError result", resp)
	}
}

// spec: §4.8 line 1077, §15.1 lines 1014-1015 — a connector interceptor
// REJECT carries the §15.1 code; CallConnectorTool maps it to the matching
// gRPC status code so the pod's MCP client sees the policy rejection.
// F-4.8.14.
func TestCallConnectorToolInterceptorRejection_spec_4_8_1077(t *testing.T) {
	cases := []struct {
		name string
		code string
		want codes.Code
	}{
		{"request rejected", connectorinvoke.CodeConnectorRequestRejected, codes.PermissionDenied},
		{"response rejected", connectorinvoke.CodeConnectorResponseRejected, codes.FailedPrecondition},
		{"fail-closed timeout", "INTERCEPTOR_TIMEOUT", codes.Unavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ct := &fakeConnectorTools{callErr: &connectorinvoke.RejectionError{Code: tc.code, Reason: "blocked"}}
			svc := newServiceWithConnectors(t, ct)
			_, err := svc.CallConnectorTool(context.Background(),
				&adapterv1.CallConnectorToolRequest{SessionId: &adapterv1.SessionId{Value: "sess_1"}, ConnectorId: "github", ToolName: "t"})
			if status.Code(err) != tc.want {
				t.Errorf("code = %v, want %v (err=%v)", status.Code(err), tc.want, err)
			}
			if !strings.Contains(status.Convert(err).Message(), tc.code) {
				t.Errorf("status message %q does not carry the §15.1 code %q", status.Convert(err).Message(), tc.code)
			}
		})
	}
}
