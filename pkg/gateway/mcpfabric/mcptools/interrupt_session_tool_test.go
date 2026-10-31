// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// TestInterruptSessionToolRunsRESTRoute_spec_15_2_1 pins that
// lenny/interrupt_session runs the REST POST /v1/sessions/{id}/interrupt
// route through the shared service layer and answers its session body
// verbatim, so the interrupt has one write into suspended on both surfaces.
//
// spec: 15.2.1 (REST/MCP Consistency Contract), 15.2 (MCP API)
func TestInterruptSessionToolRunsRESTRoute_spec_15_2_1(t *testing.T) {
	body := `{"id":"s1","tenantId":"acme","state":"suspended"}`
	svc := &fakeService{result: sessionserver.ServiceResult{Status: 200, Body: []byte(body)}}
	srv := newClientToolsMCP(t, svc)

	res, err := dispatch(t, srv, "lenny/interrupt_session", `{"sessionId":"s1"}`)
	if err != nil {
		t.Fatalf("interrupt_session: %v", err)
	}
	if svc.method != "POST" || svc.target != "/v1/sessions/s1/interrupt" {
		t.Fatalf("routed to %s %s, want POST /v1/sessions/s1/interrupt", svc.method, svc.target)
	}
	if len(res.Content) != 1 || res.Content[0].Text != body {
		t.Fatalf("interrupt_session result = %+v, want the REST body verbatim", res.Content)
	}
}

// TestInterruptSessionToolProjectsRESTError_spec_15_2_1 pins that the REST
// route's precondition refusal reaches the MCP caller with the same code.
//
// spec: 15.2.1 (REST/MCP Consistency Contract), 15.1 (REST API)
func TestInterruptSessionToolProjectsRESTError_spec_15_2_1(t *testing.T) {
	svc := &fakeService{err: &sessionserver.ServiceError{HTTPStatus: 409, Code: "INVALID_STATE_TRANSITION", Message: "not running"}}
	srv := newClientToolsMCP(t, svc)

	res, _ := dispatch(t, srv, "lenny/interrupt_session", `{"sessionId":"s1"}`)
	if code := errorCode(t, res); code != "INVALID_STATE_TRANSITION" {
		t.Fatalf("error code = %q, want INVALID_STATE_TRANSITION", code)
	}
}

// TestInterruptSessionToolAbsentWithoutService_spec_15_2_1 pins that the
// tool registers only with the shared service layer: no in-tool write of
// the session row remains.
//
// spec: 15.2.1 (REST/MCP Consistency Contract)
func TestInterruptSessionToolAbsentWithoutService_spec_15_2_1(t *testing.T) {
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{DevTenantFallback: true, Store: memstore.New(), TenantID: "acme"})
	for _, tool := range srv.Catalog() {
		if tool.Name == "lenny/interrupt_session" {
			t.Fatal("lenny/interrupt_session registered without a SessionService")
		}
	}
}
