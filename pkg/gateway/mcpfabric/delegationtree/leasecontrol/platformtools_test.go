// SPDX-License-Identifier: MIT

package leasecontrol_test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// fakePlatformTools is a leasecontrol.PlatformToolService double.
type fakePlatformTools struct {
	listResult []leasecontrol.PlatformToolDescriptor
	listErr    error
	callResult []byte
	callIsErr  bool
	callErr    error
	gotSession string
	gotTool    string
}

func (f *fakePlatformTools) ListPlatformTools(_ context.Context, sessionID string) ([]leasecontrol.PlatformToolDescriptor, error) {
	f.gotSession = sessionID
	return f.listResult, f.listErr
}

func (f *fakePlatformTools) CallPlatformTool(_ context.Context, sessionID, toolName string, _ []byte) ([]byte, bool, error) {
	f.gotSession = sessionID
	f.gotTool = toolName
	return f.callResult, f.callIsErr, f.callErr
}

func newServiceWithTools(t *testing.T, pt leasecontrol.PlatformToolService) *leasecontrol.Service {
	t.Helper()
	budgets := leasecontrol.NewMemoryBudgetSource()
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets:       budgets,
		Tenants:       budgets,
		PlatformTools: pt,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func reqCall(session, tool string) *adapterv1.CallPlatformToolRequest {
	return &adapterv1.CallPlatformToolRequest{
		SessionId: &adapterv1.SessionId{Value: session},
		ToolName:  tool,
		Arguments: []byte(`{}`),
	}
}

// spec: §9.1 line 14 — CallPlatformTool forwards to the platform tool
// service and returns the MCP result with its isError flag.
func TestCallPlatformToolSuccess_spec_9_1(t *testing.T) {
	pt := &fakePlatformTools{callResult: []byte(`{"content":[]}`), callIsErr: true}
	svc := newServiceWithTools(t, pt)
	resp, err := svc.CallPlatformTool(context.Background(), reqCall("sess_1", "lenny/delegate_task"))
	if err != nil {
		t.Fatalf("CallPlatformTool err = %v", err)
	}
	if pt.gotSession != "sess_1" || pt.gotTool != "lenny/delegate_task" {
		t.Errorf("forwarded (%q,%q)", pt.gotSession, pt.gotTool)
	}
	if string(resp.GetResult()) != `{"content":[]}` || !resp.GetIsError() {
		t.Errorf("resp = (%s, isErr=%v), want the service result", resp.GetResult(), resp.GetIsError())
	}
}

func TestCallPlatformToolUnconfiguredIsUnimplemented_spec_9_1(t *testing.T) {
	svc := newServiceWithTools(t, nil) // nil service → §8.6-only deployment.
	_, err := svc.CallPlatformTool(context.Background(), reqCall("s", "lenny/output"))
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("code = %v, want Unimplemented", status.Code(err))
	}
}

func TestCallPlatformToolMissingArgsRejected_spec_9_1(t *testing.T) {
	svc := newServiceWithTools(t, &fakePlatformTools{})
	if _, err := svc.CallPlatformTool(context.Background(), reqCall("", "lenny/output")); status.Code(err) != codes.InvalidArgument {
		t.Errorf("empty session: code = %v, want InvalidArgument", status.Code(err))
	}
	if _, err := svc.CallPlatformTool(context.Background(), reqCall("s", "")); status.Code(err) != codes.InvalidArgument {
		t.Errorf("empty tool: code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestCallPlatformToolUnknownSessionIsNotFound_spec_9_1(t *testing.T) {
	svc := newServiceWithTools(t, &fakePlatformTools{callErr: leasecontrol.ErrSessionNotFound})
	_, err := svc.CallPlatformTool(context.Background(), reqCall("ghost", "lenny/output"))
	if status.Code(err) != codes.NotFound {
		t.Errorf("code = %v, want NotFound", status.Code(err))
	}
}

func TestCallPlatformToolUnknownToolIsNotFound_spec_9_1(t *testing.T) {
	svc := newServiceWithTools(t, &fakePlatformTools{callErr: leasecontrol.ErrPlatformToolNotFound})
	_, err := svc.CallPlatformTool(context.Background(), reqCall("s", "lenny/nope"))
	if status.Code(err) != codes.NotFound {
		t.Errorf("code = %v, want NotFound", status.Code(err))
	}
}

func TestCallPlatformToolServiceErrorIsInternal_spec_9_1(t *testing.T) {
	svc := newServiceWithTools(t, &fakePlatformTools{callErr: errors.New("boom")})
	_, err := svc.CallPlatformTool(context.Background(), reqCall("s", "lenny/output"))
	if status.Code(err) != codes.Internal {
		t.Errorf("code = %v, want Internal", status.Code(err))
	}
}

// spec: §9.1 lines 14-31 — ListPlatformTools maps the catalog onto the
// proto descriptors.
func TestListPlatformToolsSuccess_spec_9_1(t *testing.T) {
	pt := &fakePlatformTools{listResult: []leasecontrol.PlatformToolDescriptor{
		{Name: "lenny/delegate_task", Description: "delegate", InputSchema: []byte(`{"type":"object"}`)},
	}}
	svc := newServiceWithTools(t, pt)
	resp, err := svc.ListPlatformTools(context.Background(), &adapterv1.ListPlatformToolsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess_1"},
	})
	if err != nil {
		t.Fatalf("ListPlatformTools err = %v", err)
	}
	if pt.gotSession != "sess_1" {
		t.Errorf("forwarded session = %q", pt.gotSession)
	}
	if len(resp.GetTools()) != 1 || resp.GetTools()[0].GetName() != "lenny/delegate_task" ||
		string(resp.GetTools()[0].GetInputSchema()) != `{"type":"object"}` {
		t.Errorf("tools = %+v, want the mapped catalog", resp.GetTools())
	}
}

func TestListPlatformToolsUnconfiguredIsUnimplemented_spec_9_1(t *testing.T) {
	svc := newServiceWithTools(t, nil)
	_, err := svc.ListPlatformTools(context.Background(), &adapterv1.ListPlatformToolsRequest{
		SessionId: &adapterv1.SessionId{Value: "s"},
	})
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("code = %v, want Unimplemented", status.Code(err))
	}
}
