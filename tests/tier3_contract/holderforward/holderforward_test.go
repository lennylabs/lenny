// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the CH-HOLDERFORWARD wire form of a forwarded
// tool call. A serving replica built with the production forward client
// forwards a tools/call through ForwardToolCall to a coordinator served by
// the production forward listener over gateway mTLS, and the coordinator
// records the HTTP request it received. The expected request line, body,
// and header names are string literals rather than the Go identifiers the
// implementation uses, so a renamed constant or a changed encoding fails
// here.
package holderforward_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaycontrol/platformtools"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	"github.com/lennylabs/lenny/pkg/mtls/certreload"
	"github.com/lennylabs/lenny/tests/testinfra/gatewaypki"
)

// wireRequest is the HTTP request the coordinator's listener received.
type wireRequest struct {
	method, path string
	header       http.Header
	body         string
}

// callingSessions resolves the calling session of a platform tool call.
type callingSessions map[string]sessionstore.Session

func (c callingSessions) GetByID(_ context.Context, id string) (sessionstore.Session, error) {
	row, ok := c[id]
	if !ok {
		return sessionstore.Session{}, sessionstore.ErrNotFound
	}
	return row, nil
}

// fixture is a serving replica whose forward reaches a recording
// coordinator.
type fixture struct {
	serving   *sessionserver.Server
	sessions  callingSessions
	requests  chan wireRequest
	principal chan authmw.Principal
	bearer    string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	m := gatewaypki.Mint(t)
	reloader, err := certreload.New(m.CertFile, m.KeyFile)
	if err != nil {
		t.Fatalf("certreload: %v", err)
	}
	signer := jwt.NewHMACSigner("k1", []byte("contract-secret"))
	bearer, err := signer.Sign(jwt.Claims{Subject: "alice", TenantID: "acme", Expiry: time.Now().Add(time.Hour).Unix(), Typ: pkgauth.TokenUserBearer})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	f := &fixture{
		sessions:  callingSessions{"sess-parent": {ID: "sess-parent", TenantID: "acme", UserID: "alice"}},
		requests:  make(chan wireRequest, 1),
		principal: make(chan authmw.Principal, 1),
		bearer:    bearer,
	}

	tools := mcp.NewServer()
	tools.RegisterTool(mcp.Tool{Name: "lenny/target"}, func(ctx context.Context, _ json.RawMessage) (mcp.ToolResult, error) {
		p, _ := authmw.FromContext(ctx)
		f.principal <- p
		return mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: "terminated"}}}, nil
	})
	// The recorder wraps the production listener so it captures the request
	// exactly as the serving replica sent it, before the listener consumes
	// and strips the calling-session header.
	listener := sessionserver.NewHolderForwardHandler(tools.Handler(), authmw.Options{RequireAuth: true, Verifier: signer}, f.sessions)
	record := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.requests <- wireRequest{method: r.Method, path: r.URL.RequestURI(), header: r.Header.Clone(), body: string(b)}
		r.Body = io.NopCloser(bytes.NewReader(b))
		listener.ServeHTTP(w, r)
	})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: record, TLSConfig: sessionserver.HolderForwardServerTLS(reloader, m.CAPool), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.ServeTLS(lis, "", "") }()
	t.Cleanup(func() { _ = srv.Close() })

	mr := miniredis.RunT(t)
	leases := leasestore.New(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	coordinatorHolder, err := sessionserver.HolderValue("gw-b", lis.Addr().String())
	if err != nil {
		t.Fatalf("coordinator holder: %v", err)
	}
	if _, err := leases.Acquire(context.Background(), "acme", "sess-1", coordinatorHolder, time.Minute); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	servingHolder, err := sessionserver.HolderValue("gw-a", "127.0.0.1:1")
	if err != nil {
		t.Fatalf("serving holder: %v", err)
	}
	f.serving = sessionserver.New(memstore.New(), sessionserver.Options{
		CoordinationLeaseStore: leases,
		ReplicaID:              servingHolder,
		HolderForwardClient:    sessionserver.NewHolderForwardClient(reloader, m.CAPool),
		PodBinder:              &podsession.Binder{},
	})
	return f
}

// runningRow is the forwarded session: running and bound to a pod.
func runningRow() sessionstore.Session {
	return sessionstore.Session{ID: "sess-1", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-1"}
}

// forward sends lenny/target through ForwardToolCall on ctx and returns
// what the coordinator received.
func (f *fixture) forward(t *testing.T, ctx context.Context) (wireRequest, authmw.Principal) {
	t.Helper()
	res, out, err := f.serving.ForwardToolCall(ctx, runningRow(), sessionserver.ForwardKindTerminate, "lenny/target", json.RawMessage(`{"reason":"done"}`))
	if err != nil || out != sessionserver.ForwardDelivered || len(res.Content) != 1 || res.Content[0].Text != "terminated" {
		t.Fatalf("ForwardToolCall = %+v, %v, %v; want the coordinator's result delivered", res, out, err)
	}
	return <-f.requests, <-f.principal
}

// assertToolCallRequest checks the request line, body, and content headers
// of a forwarded tools/call.
func assertToolCallRequest(t *testing.T, got wireRequest) {
	t.Helper()
	const wantBody = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lenny/target","arguments":{"reason":"done"}}}`
	if got.method != "POST" || got.path != "/mcp" {
		t.Errorf("request line = %s %s, want POST /mcp", got.method, got.path)
	}
	if got.body != wantBody {
		t.Errorf("body = %s, want %s", got.body, wantBody)
	}
	if got.header.Get("Content-Type") != "application/json" || got.header.Get("Accept") != "application/json" {
		t.Errorf("Content-Type %q, Accept %q; want application/json for both", got.header.Get("Content-Type"), got.header.Get("Accept"))
	}
}

// spec: 28.5.4 (Inter-replica) CH-HOLDERFORWARD, 29.3 (Interactive message send) Forwarding rules 2 and 3
// diagnosis: the tools/call a serving replica posts to the coordinator's
// /mcp no longer carries the client's credential in Authorization, carries a
// calling-session header from an MCP client, or changed its JSON-RPC body,
// so a coordinator authenticates the wrong identity or cannot decode the
// call. Read ForwardToolCall and newToolForwardRequest in
// pkg/gateway/sessionserver/holder_forward.go.
func TestForwardedToolCallFromAnMCPClientCarriesTheCredential_spec_28_5_4(t *testing.T) {
	f := newFixture(t)
	ctx := authmw.WithCredential(context.Background(), authmw.Credential{Authorization: "Bearer " + f.bearer})
	got, principal := f.forward(t, ctx)
	assertToolCallRequest(t, got)
	if got.header.Get("Authorization") != "Bearer "+f.bearer {
		t.Errorf("Authorization = %q, want the client's bearer", got.header.Get("Authorization"))
	}
	if v := got.header.Get("X-Lenny-Calling-Session"); v != "" {
		t.Errorf("X-Lenny-Calling-Session = %q on an MCP client's forward, want it absent", v)
	}
	if principal.Subject != "alice" || principal.CallerType == "agent" {
		t.Errorf("coordinator principal = %+v, want alice authenticated from the bearer", principal)
	}
}

// spec: 28.5.4 (Inter-replica) CH-HOLDERFORWARD, 29.3 (Interactive message send) Forwarding rule 3
// diagnosis: a forwarded platform tool call no longer names its calling
// session in X-Lenny-Calling-Session, carries a credential it has none of,
// or the coordinator's listener no longer installs the principal
// platformtools.Bridge.CallPlatformTool installs. Read setForwardIdentity in
// pkg/gateway/sessionserver/holder_forward.go and the calling-session branch
// of NewHolderForwardHandler in holder_forward_listener.go.
func TestForwardedPlatformToolCallCarriesTheCallingSession_spec_28_5_4(t *testing.T) {
	f := newFixture(t)
	ctx, err := platformtools.WithSessionPrincipal(context.Background(), f.sessions, "sess-parent")
	if err != nil {
		t.Fatalf("WithSessionPrincipal: %v", err)
	}
	got, principal := f.forward(t, ctx)
	assertToolCallRequest(t, got)
	if v := got.header.Get("X-Lenny-Calling-Session"); v != "sess-parent" {
		t.Errorf("X-Lenny-Calling-Session = %q, want sess-parent", v)
	}
	if v := got.header.Get("Authorization"); v != "" {
		t.Errorf("Authorization = %q on a platform tool call's forward, want it absent", v)
	}
	if principal.SessionID != "sess-parent" || principal.CallerType != "agent" || principal.Subject != "alice" || principal.TenantID != "acme" {
		t.Errorf("coordinator principal = %+v, want the calling session's agent principal", principal)
	}
}
