// SPDX-License-Identifier: MIT

package sessionserver

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	idemmw "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	"github.com/lennylabs/lenny/pkg/mtls/certreload"
	"github.com/lennylabs/lenny/tests/testinfra/gatewaypki"
)

// countingLeaseStore wraps fakeLeaseStore, counts Get calls, and can fail
// every Get.
type countingLeaseStore struct {
	*fakeLeaseStore
	gets   atomic.Int64
	getErr error
}

func (c *countingLeaseStore) Get(ctx context.Context, tenantID, sessionID string) (leasestore.Lease, error) {
	c.gets.Add(1)
	if c.getErr != nil {
		return leasestore.Lease{}, c.getErr
	}
	return c.fakeLeaseStore.Get(ctx, tenantID, sessionID)
}

// forwardRow is a running session that names its pod.
func forwardRow() sessionstore.Session {
	return sessionstore.Session{ID: "sess-1", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-1"}
}

// forwardServer builds a Server with a pod binder, the lease store, the
// replica's holder value, and the forward client.
func forwardServer(leases leasestore.LeaseStore, holder string, client *http.Client) *Server {
	s := New(memstore.New(), Options{
		DevTenantHeader:        true,
		CoordinationLeaseStore: leases,
		ReplicaID:              holder,
		HolderForwardClient:    client,
	})
	s.podBinder = &podsession.Binder{}
	return s
}

// leasesHeldBy returns a lease store whose sessionID lease names holder.
func leasesHeldBy(holder string) *countingLeaseStore {
	f := newFakeLeaseStore()
	f.holders[leaseK("acme", "sess-1")] = holder
	return &countingLeaseStore{fakeLeaseStore: f}
}

// testForwardClient builds a forward client over the material's gateway
// leaf with the given dial and handshake bound.
func testForwardClient(t *testing.T, m *gatewaypki.Material, bound time.Duration) *http.Client {
	t.Helper()
	reloader, err := certreload.New(m.CertFile, m.KeyFile)
	if err != nil {
		t.Fatalf("certreload: %v", err)
	}
	return newHolderForwardClient(holderForwardClientTLS(reloader, m.CAPool), bound)
}

// startTLSServer serves h on a loopback TLS listener with cfg and returns its
// address.
func startTLSServer(t *testing.T, cfg *tls.Config, h http.Handler) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: h, TLSConfig: cfg, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.ServeTLS(lis, "", "") }()
	t.Cleanup(func() { _ = srv.Close() })
	return lis.Addr().String()
}

// serverTLSFor builds the listener TLS configuration over a leaf of m.
func serverTLSFor(t *testing.T, m *gatewaypki.Material, leaf gatewaypki.LeafFiles) *tls.Config {
	t.Helper()
	reloader, err := certreload.New(leaf.CertFile, leaf.KeyFile)
	if err != nil {
		t.Fatalf("certreload: %v", err)
	}
	return HolderForwardServerTLS(reloader, m.CAPool)
}

// capturedRequest records what the coordinator received.
type capturedRequest struct {
	method, uri string
	header      http.Header
	body        string
	forwarded   bool
}

// recordingCoordinator answers 201 with a JSON body and records each request.
func recordingCoordinator(got chan<- capturedRequest) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- capturedRequest{method: r.Method, uri: r.RequestURI, header: r.Header.Clone(), body: string(b), forwarded: Forwarded(r.Context())}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Coordinator", "b")
		w.Header().Set("Connection", "X-Drop-Me")
		w.Header().Set("X-Drop-Me", "1")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

// clientRequest builds the client request the serving replica's handler
// received, with ctx.
func clientRequest(ctx context.Context, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess-1/messages?wait=false", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	r.Header.Set("X-Client-Private", "secret")
	r.Header.Set("Idempotency-Key", "k-1")
	r.Header.Set("Authorization", "Bearer should-not-be-read-from-the-header")
	return r.WithContext(ctx)
}

// spec: 29.3 (Interactive message send) Forwarding rules 1, 2, and 6, 10.1.1 (Stateless Replicas and Per-Session Coordination)
func TestForwardToHolderRunsLocallyWithoutADial_spec_29_3(t *testing.T) {
	t.Run("holder equal to self", func(t *testing.T) {
		leases := leasesHeldBy("gw-a.00@10.0.0.1:1")
		s := forwardServer(leases, "gw-a.00@10.0.0.1:1", nil)
		rec := httptest.NewRecorder()
		if got := s.forwardToHolder(rec, clientRequest(context.Background(), "{}"), forwardRow(), ForwardKindInterrupt, nil); got != ForwardLocal {
			t.Fatalf("outcome = %v, want ForwardLocal", got)
		}
		if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
			t.Errorf("local outcome wrote %d %q, want nothing written", rec.Code, rec.Body.String())
		}
	})
	t.Run("no lease store", func(t *testing.T) {
		s := forwardServer(nil, "gw-a", nil)
		if got := s.forwardToHolder(httptest.NewRecorder(), clientRequest(context.Background(), "{}"), forwardRow(), ForwardKindInterrupt, nil); got != ForwardLocal {
			t.Fatalf("outcome = %v, want ForwardLocal", got)
		}
	})
	t.Run("state outside the gate reads no lease", func(t *testing.T) {
		leases := leasesHeldBy("gw-b.00@10.0.0.2:1")
		s := forwardServer(leases, "gw-a.00@10.0.0.1:1", nil)
		row := forwardRow()
		row.State = session.StateCreated
		if got := s.forwardToHolder(httptest.NewRecorder(), clientRequest(context.Background(), "{}"), row, ForwardKindTerminate, nil); got != ForwardLocal {
			t.Fatalf("outcome = %v, want ForwardLocal", got)
		}
		if n := leases.gets.Load(); n != 0 {
			t.Errorf("lease reads = %d, want 0", n)
		}
	})
	t.Run("terminate on a suspended row without a pod reads no lease", func(t *testing.T) {
		leases := leasesHeldBy("gw-b.00@10.0.0.2:1")
		s := forwardServer(leases, "gw-a.00@10.0.0.1:1", nil)
		row := forwardRow()
		row.State, row.PodAssignment = session.StateSuspended, ""
		if got := s.forwardToHolder(httptest.NewRecorder(), clientRequest(context.Background(), "{}"), row, ForwardKindTerminate, nil); got != ForwardLocal {
			t.Fatalf("outcome = %v, want ForwardLocal", got)
		}
		if n := leases.gets.Load(); n != 0 {
			t.Errorf("lease reads = %d, want 0", n)
		}
	})
}

// spec: 29.3 (Interactive message send) Forwarding rules 6 and 7, 11.5 (Idempotency)
func TestForwardToHolderNotDeliveredWithoutReachableHolder_spec_29_3(t *testing.T) {
	m := gatewaypki.Mint(t)
	client := testForwardClient(t, m, time.Second)
	cases := []struct {
		name   string
		leases *countingLeaseStore
		client *http.Client
	}{
		{"lease read error", &countingLeaseStore{fakeLeaseStore: newFakeLeaseStore(), getErr: errors.New("redis down")}, client},
		{"no holder", &countingLeaseStore{fakeLeaseStore: newFakeLeaseStore()}, client},
		{"holder without an address", leasesHeldBy("gw-b"), client},
		{"nil forwarder", leasesHeldBy("gw-b.00@127.0.0.1:1"), nil},
		{"dial refused", leasesHeldBy("gw-b.00@" + closedAddr(t)), client},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := forwardServer(c.leases, "gw-a.00@10.0.0.1:1", c.client)
			rec := httptest.NewRecorder()
			ctx := idemmw.WithGate(context.Background())
			if got := s.forwardToHolder(rec, clientRequest(ctx, "{}"), forwardRow(), ForwardKindInterrupt, nil); got != ForwardNotDelivered {
				t.Fatalf("outcome = %v, want ForwardNotDelivered", got)
			}
			assertTargetNotReady(t, rec, "running")
			if !idemmw.NotStored(ctx) {
				t.Error("the refusal did not mark the request as not to be stored (§11.5)")
			}
		})
	}
	t.Run("message without inReplyTo is left to its caller", func(t *testing.T) {
		s := forwardServer(leasesHeldBy("gw-b"), "gw-a.00@10.0.0.1:1", client)
		rec := httptest.NewRecorder()
		if got := s.forwardToHolder(rec, clientRequest(context.Background(), "{}"), forwardRow(), ForwardKindMessageNoReplyTo, nil); got != ForwardNotDelivered {
			t.Fatalf("outcome = %v, want ForwardNotDelivered", got)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("wrote %q, want nothing so the caller holds the message", rec.Body.String())
		}
	})
}

// closedAddr returns a loopback address nothing listens on.
func closedAddr(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := lis.Addr().String()
	_ = lis.Close()
	return addr
}

// assertTargetNotReady checks the 409 TARGET_NOT_READY envelope.
func assertTargetNotReady(t *testing.T, rec *httptest.ResponseRecorder, state string) {
	t.Helper()
	var env errorEnvelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if rec.Code != http.StatusConflict || env.Error.Code != "TARGET_NOT_READY" || env.Error.Details["currentState"] != state {
		t.Errorf("response = %d %s, want 409 TARGET_NOT_READY with currentState %q", rec.Code, rec.Body.String(), state)
	}
}

// spec: 29.3 (Interactive message send) Forwarding rule 5
func TestForwardedRequestIsNeverForwardedAgain_spec_29_3(t *testing.T) {
	ctx := WithForwarded(context.Background())
	t.Run("non-holder refuses without a dial", func(t *testing.T) {
		var dials atomic.Int64
		client := &http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
			dials.Add(1)
			return nil, errors.New("no dial expected")
		}}}
		s := forwardServer(leasesHeldBy("gw-b.00@127.0.0.1:1"), "gw-a.00@10.0.0.1:1", client)
		rec := httptest.NewRecorder()
		if got := s.forwardToHolder(rec, clientRequest(ctx, "{}"), forwardRow(), ForwardKindTerminate, nil); got != ForwardNotDelivered {
			t.Fatalf("outcome = %v, want ForwardNotDelivered", got)
		}
		assertTargetNotReady(t, rec, "running")
		if dials.Load() != 0 {
			t.Errorf("dials = %d, want 0", dials.Load())
		}
	})
	t.Run("holder runs locally", func(t *testing.T) {
		s := forwardServer(leasesHeldBy("gw-a.00@10.0.0.1:1"), "gw-a.00@10.0.0.1:1", nil)
		if got := s.forwardToHolder(httptest.NewRecorder(), clientRequest(ctx, "{}"), forwardRow(), ForwardKindTerminate, nil); got != ForwardLocal {
			t.Fatalf("outcome = %v, want ForwardLocal", got)
		}
	})
}

// spec: 29.3 (Interactive message send) Forwarding rules 2 and 3, 13.3 (Credential Flow)
func TestForwardToHolderReplaysTheRequestWithTheRecordedCredential_spec_29_3(t *testing.T) {
	m := gatewaypki.Mint(t)
	got := make(chan capturedRequest, 1)
	addr := startTLSServer(t, serverTLSFor(t, m, gatewaypki.LeafFiles{CertFile: m.CertFile, KeyFile: m.KeyFile}), recordingCoordinator(got))
	s := forwardServer(leasesHeldBy("gw-b.00@"+addr), "gw-a.00@10.0.0.1:1", testForwardClient(t, m, time.Second))

	ctx := authmw.WithCredential(context.Background(), authmw.Credential{Authorization: "Bearer recorded-token"})
	rec := httptest.NewRecorder()
	body := `{"messages":[{"content":"hi"}]}`
	if out := s.forwardToHolder(rec, clientRequest(ctx, body), forwardRow(), ForwardKindMessageNoReplyTo, strings.NewReader(body)); out != ForwardDelivered {
		t.Fatalf("outcome = %v, want ForwardDelivered", out)
	}
	req := <-got
	if req.method != http.MethodPost || req.uri != "/v1/sessions/sess-1/messages?wait=false" || req.body != body {
		t.Errorf("coordinator received %s %s %q, want the client's method, target, and body", req.method, req.uri, req.body)
	}
	if req.header.Get("Authorization") != "Bearer recorded-token" {
		t.Errorf("Authorization = %q, want the recorded credential", req.header.Get("Authorization"))
	}
	for _, name := range []string{"X-Client-Private", "Idempotency-Key", CallingSessionHeader} {
		if v := req.header.Get(name); v != "" {
			t.Errorf("forward carried client header %s=%q, want only content headers, Accept, and the credential", name, v)
		}
	}
	if req.header.Get("Content-Type") != "application/json" || req.header.Get("Accept") != "application/json" {
		t.Errorf("content headers = %q / %q, want both copied", req.header.Get("Content-Type"), req.header.Get("Accept"))
	}
	if rec.Code != http.StatusCreated || rec.Body.String() != `{"ok":true}` || rec.Header().Get("X-Coordinator") != "b" {
		t.Errorf("caller received %d %q %v, want the coordinator's response", rec.Code, rec.Body.String(), rec.Header())
	}
	if rec.Header().Get("X-Drop-Me") != "" || rec.Header().Get("Connection") != "" {
		t.Errorf("hop-by-hop headers were copied: %v", rec.Header())
	}
}

// spec: 29.3 (Interactive message send) Forwarding rule 3
func TestForwardOfAPlatformToolCallCarriesTheCallingSession_spec_29_3(t *testing.T) {
	h := http.Header{}
	ctx := authmw.WithPrincipal(context.Background(), authmw.Principal{SessionID: "sess-parent", TenantID: "acme", CallerType: "agent"})
	setForwardIdentity(ctx, h)
	if h.Get(CallingSessionHeader) != "sess-parent" || h.Get("Authorization") != "" {
		t.Errorf("agent principal headers = %v, want only %s: sess-parent", h, CallingSessionHeader)
	}
	h = http.Header{}
	setForwardIdentity(authmw.WithPrincipal(context.Background(), authmw.Principal{SessionID: "s", CallerType: "user"}), h)
	if len(h) != 0 {
		t.Errorf("a user principal with no credential set %v, want no identity", h)
	}
	h = http.Header{}
	dev := http.Header{"X-Lenny-Tenant-Id": {"acme"}}
	setForwardIdentity(authmw.WithCredential(ctx, authmw.Credential{DevHeaders: dev}), h)
	if h.Get("X-Lenny-Tenant-ID") != "acme" || h.Get(CallingSessionHeader) != "" {
		t.Errorf("credential headers = %v, want the dev headers and no calling session", h)
	}
}

// spec: 29.3 (Interactive message send) Forwarding rules 6 and 7, 10.1.1 (Stateless Replicas and Per-Session Coordination) Replica address
func TestForwardClassifiesFailuresByConnection_spec_29_3(t *testing.T) {
	m := gatewaypki.Mint(t)
	t.Run("handshake with a non-gateway certificate is not delivered", func(t *testing.T) {
		other := m.Leaf(t, []string{"lenny-other.lenny-system.svc"}, nil)
		addr := startTLSServer(t, serverTLSFor(t, m, other), http.NotFoundHandler())
		s := forwardServer(leasesHeldBy("gw-b.00@"+addr), "gw-a.00@10.0.0.1:1", testForwardClient(t, m, time.Second))
		if got := s.forwardToHolder(httptest.NewRecorder(), clientRequest(context.Background(), "{}"), forwardRow(), ForwardKindInterrupt, nil); got != ForwardNotDelivered {
			t.Fatalf("outcome = %v, want ForwardNotDelivered", got)
		}
	})
	t.Run("handshake with a SPIFFE certificate is not delivered", func(t *testing.T) {
		agent := m.Leaf(t, nil, []*url.URL{{Scheme: "spiffe", Host: "lenny.local", Path: "/ns/lenny-agents/sa/agent"}})
		addr := startTLSServer(t, serverTLSFor(t, m, agent), http.NotFoundHandler())
		s := forwardServer(leasesHeldBy("gw-b.00@"+addr), "gw-a.00@10.0.0.1:1", testForwardClient(t, m, time.Second))
		if got := s.forwardToHolder(httptest.NewRecorder(), clientRequest(context.Background(), "{}"), forwardRow(), ForwardKindInterrupt, nil); got != ForwardNotDelivered {
			t.Fatalf("outcome = %v, want ForwardNotDelivered", got)
		}
	})
	t.Run("a hanging handshake ends at the dial bound", func(t *testing.T) {
		addr := silentListener(t)
		s := forwardServer(leasesHeldBy("gw-b.00@"+addr), "gw-a.00@10.0.0.1:1", testForwardClient(t, m, 300*time.Millisecond))
		start := time.Now()
		if got := s.forwardToHolder(httptest.NewRecorder(), clientRequest(context.Background(), "{}"), forwardRow(), ForwardKindInterrupt, nil); got != ForwardNotDelivered {
			t.Fatalf("outcome = %v, want ForwardNotDelivered", got)
		}
		if el := time.Since(start); el < 250*time.Millisecond || el > 3*time.Second {
			t.Errorf("attempt took %v, want it to end at the 300ms bound", el)
		}
	})
	t.Run("a coordinator that closes after reading the request is unknown", func(t *testing.T) {
		closer := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			conn, _, err := http.NewResponseController(w).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		})
		addr := startTLSServer(t, serverTLSFor(t, m, gatewaypki.LeafFiles{CertFile: m.CertFile, KeyFile: m.KeyFile}), closer)
		s := forwardServer(leasesHeldBy("gw-b.00@"+addr), "gw-a.00@10.0.0.1:1", testForwardClient(t, m, time.Second))
		rec := httptest.NewRecorder()
		ctx := idemmw.WithGate(context.Background())
		if got := s.forwardToHolder(rec, clientRequest(ctx, "{}"), forwardRow(), ForwardKindMessageNoReplyTo, strings.NewReader("{}")); got != ForwardUnknown {
			t.Fatalf("outcome = %v, want ForwardUnknown", got)
		}
		assertTargetNotReady(t, rec, "running")
		if !idemmw.NotStored(ctx) {
			t.Error("an unknown outcome was left storable (§11.5)")
		}
	})
	t.Run("the client dial bound is the specified 2 seconds", func(t *testing.T) {
		if HolderForwardDialBound != 2*time.Second {
			t.Errorf("HolderForwardDialBound = %v, want 2s", HolderForwardDialBound)
		}
	})
}

// silentListener accepts TCP connections and never answers.
func silentListener(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := lis.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = lis.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return lis.Addr().String()
}

// coordinatorChain serves mcpSrv and h through the forward listener chain
// with dev-header auth, as the gateway's forward listener does.
func coordinatorChain(t *testing.T, m *gatewaypki.Material, mcpSrv *mcp.Server, rest http.Handler, opts authmw.Options) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpSrv.Handler())
	if rest != nil {
		mux.Handle("/v1/sessions/", rest)
	}
	sessions := callingSessions{"sess-parent": {ID: "sess-parent", TenantID: "acme", UserID: "alice"}}
	h := NewHolderForwardHandler(mux, opts, sessions)
	return startTLSServer(t, serverTLSFor(t, m, gatewaypki.LeafFiles{CertFile: m.CertFile, KeyFile: m.KeyFile}), h)
}

// callingSessions resolves session rows by id.
type callingSessions map[string]sessionstore.Session

func (f callingSessions) GetByID(_ context.Context, id string) (sessionstore.Session, error) {
	row, ok := f[id]
	if !ok {
		return sessionstore.Session{}, sessionstore.ErrNotFound
	}
	return row, nil
}

// spec: 29.3 (Interactive message send) Forwarding rule 2, 15.2 (MCP API), 15.2.1 (REST/MCP consistency contract)
func TestForwardToolCallReturnsTheCoordinatorsResult_spec_29_3(t *testing.T) {
	m := gatewaypki.Mint(t)
	mcpSrv := mcp.NewServer()
	var seen atomic.Value
	mcpSrv.RegisterTool(mcp.Tool{Name: "lenny/ok"}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		p, _ := authmw.FromContext(ctx)
		seen.Store(p)
		return mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: "done " + string(args)}}}, nil
	})
	mcpSrv.RegisterTool(mcp.Tool{Name: "lenny/fail"}, func(context.Context, json.RawMessage) (mcp.ToolResult, error) {
		return mcp.ToolResult{}, mcp.NewToolError("SCOPE_DENIED", "not in scope", map[string]any{"scope": "tree"})
	})
	devOpts := authmw.Options{AllowDevHeaders: true, DevHeaderNamesTenant: true}
	addr := coordinatorChain(t, m, mcpSrv, nil, devOpts)
	s := forwardServer(leasesHeldBy("gw-b.00@"+addr), "gw-a.00@10.0.0.1:1", testForwardClient(t, m, time.Second))
	userCtx := authmw.WithCredential(context.Background(), authmw.Credential{DevHeaders: http.Header{"X-Lenny-Tenant-Id": {"acme"}}})

	t.Run("a result is returned as delivered", func(t *testing.T) {
		res, out, err := s.ForwardToolCall(userCtx, forwardRow(), ForwardKindTerminate, "lenny/ok", json.RawMessage(`{"a":1}`))
		if err != nil || out != ForwardDelivered || len(res.Content) != 1 || res.Content[0].Text != `done {"a":1}` {
			t.Fatalf("ForwardToolCall = %+v, %v, %v; want the coordinator's result", res, out, err)
		}
	})
	t.Run("a tool error is returned as delivered", func(t *testing.T) {
		_, out, err := s.ForwardToolCall(userCtx, forwardRow(), ForwardKindTerminate, "lenny/fail", nil)
		assertToolError(t, out, ForwardDelivered, err, "SCOPE_DENIED")
	})
	t.Run("a pre-dispatch JSON-RPC error is returned as delivered", func(t *testing.T) {
		_, out, err := s.ForwardToolCall(userCtx, forwardRow(), ForwardKindTerminate, "lenny/absent", nil)
		assertToolError(t, out, ForwardDelivered, err, "RESOURCE_NOT_FOUND")
	})
	t.Run("a platform tool call runs as the calling session", func(t *testing.T) {
		agentCtx := authmw.WithPrincipal(context.Background(), authmw.Principal{SessionID: "sess-parent", TenantID: "acme", CallerType: "agent"})
		if _, out, err := s.ForwardToolCall(agentCtx, forwardRow(), ForwardKindTerminate, "lenny/ok", nil); err != nil || out != ForwardDelivered {
			t.Fatalf("ForwardToolCall = %v, %v; want delivered", out, err)
		}
		p, _ := seen.Load().(authmw.Principal)
		if p.SessionID != "sess-parent" || p.CallerType != "agent" || p.Subject != "alice" {
			t.Errorf("coordinator principal = %+v, want the calling session's principal", p)
		}
	})
	t.Run("not delivered for a message without inReplyTo returns nothing", func(t *testing.T) {
		s := forwardServer(leasesHeldBy("gw-b"), "gw-a.00@10.0.0.1:1", nil)
		res, out, err := s.ForwardToolCall(userCtx, forwardRow(), ForwardKindMessageNoReplyTo, "lenny/ok", nil)
		if err != nil || out != ForwardNotDelivered || len(res.Content) != 0 {
			t.Fatalf("ForwardToolCall = %+v, %v, %v; want a zero result and no error", res, out, err)
		}
	})
	t.Run("not delivered otherwise is TARGET_NOT_READY", func(t *testing.T) {
		s := forwardServer(leasesHeldBy("gw-b"), "gw-a.00@10.0.0.1:1", nil)
		_, out, err := s.ForwardToolCall(userCtx, forwardRow(), ForwardKindTerminate, "lenny/ok", nil)
		assertToolError(t, out, ForwardNotDelivered, err, "TARGET_NOT_READY")
	})
	t.Run("a local session returns nothing", func(t *testing.T) {
		row := forwardRow()
		row.State = session.StateCreated
		_, out, err := s.ForwardToolCall(userCtx, row, ForwardKindTerminate, "lenny/ok", nil)
		if err != nil || out != ForwardLocal {
			t.Fatalf("ForwardToolCall = %v, %v; want ForwardLocal", out, err)
		}
	})
}

// spec: 29.3 (Interactive message send) Forwarding rules 3 and 7, 13.3 (Credential Flow)
func TestForwardToolCallMapsListenerRefusalsAndUndecodableBodies_spec_29_3(t *testing.T) {
	m := gatewaypki.Mint(t)
	authOpts := authmw.Options{RequireAuth: true}
	addr := coordinatorChain(t, m, mcp.NewServer(), nil, authOpts)
	s := forwardServer(leasesHeldBy("gw-b.00@"+addr), "gw-a.00@10.0.0.1:1", testForwardClient(t, m, time.Second))
	_, out, err := s.ForwardToolCall(context.Background(), forwardRow(), ForwardKindTerminate, "lenny/ok", nil)
	assertToolError(t, out, ForwardDelivered, err, "UNAUTHORIZED")

	garbage := startTLSServer(t, serverTLSFor(t, m, gatewaypki.LeafFiles{CertFile: m.CertFile, KeyFile: m.KeyFile}),
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not json-rpc")) }))
	s = forwardServer(leasesHeldBy("gw-b.00@"+garbage), "gw-a.00@10.0.0.1:1", testForwardClient(t, m, time.Second))
	ctx := idemmw.WithGate(context.Background())
	_, out, err = s.ForwardToolCall(ctx, forwardRow(), ForwardKindTerminate, "lenny/ok", nil)
	assertToolError(t, out, ForwardUnknown, err, "TARGET_NOT_READY")
	if !idemmw.NotStored(ctx) {
		t.Error("an undecodable response was left storable (§11.5)")
	}
}

// assertToolError checks the outcome and the tool error code.
func assertToolError(t *testing.T, out, wantOut ForwardOutcome, err error, code string) {
	t.Helper()
	var te *mcp.ToolError
	if out != wantOut || !errors.As(err, &te) || te.Code != code {
		t.Fatalf("ForwardToolCall = %v, %v; want %v with tool error %s", out, err, wantOut, code)
	}
}

// spec: 11.5 (Idempotency) Mechanism, 29.3 (Interactive message send) Forwarding rules 5, 6, and 7
func TestForwardRefusalsAreNotStoredUnderIdempotency_spec_11_5(t *testing.T) {
	m := gatewaypki.Mint(t)
	// The coordinator is a non-holder: its own forward helper refuses the
	// forwarded request under rule 5, and the listener marks the refusal.
	row := forwardRow()
	row.State = session.StateSuspended
	coordinator := forwardServer(leasesHeldBy("gw-c.00@127.0.0.1:1"), "gw-b.00@127.0.0.1:2", nil)
	var calls atomic.Int64
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		coordinator.forwardToHolder(w, r, row, ForwardKindResume, r.Body)
	})
	listener := NewHolderForwardHandler(inner, authmw.Options{AllowDevHeaders: true, DevHeaderNamesTenant: true}, callingSessions{})
	addr := startTLSServer(t, serverTLSFor(t, m, gatewaypki.LeafFiles{CertFile: m.CertFile, KeyFile: m.KeyFile}), listener)

	serving := forwardServer(leasesHeldBy("gw-b.00@"+addr), "gw-a.00@10.0.0.1:1", testForwardClient(t, m, time.Second))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serving.forwardToHolder(w, r, row, ForwardKindResume, r.Body)
	})
	store := idemmw.NewMemoryStore()
	wrapped := authmw.Wrap(idemmw.Wrap(handler, store, idemmw.Options{}), authmw.Options{AllowDevHeaders: true, DevHeaderNamesTenant: true})
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess-1/resume", bytes.NewReader([]byte("{}")))
		req.Header.Set("Idempotency-Key", "same-key")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		assertTargetNotReady(t, rec, "suspended")
		if rec.Header().Get(forwardNoStoreHeader) != "" {
			t.Errorf("the client response carries the internal no-store mark")
		}
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("coordinator reached %d times, want 2: a same-key retry after a refusal must run again", n)
	}
	if _, ok, _ := store.Get(context.Background(), "acme", "same-key"); ok {
		t.Error("the refusal was stored in the idempotency cache")
	}
}

// spec: 10.3 (mTLS PKI) NET-072
func TestHolderForwardListenerValidatesTheGatewayPeer_spec_10_3(t *testing.T) {
	m := gatewaypki.Mint(t)
	addr := startTLSServer(t, serverTLSFor(t, m, gatewaypki.LeafFiles{CertFile: m.CertFile, KeyFile: m.KeyFile}),
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !Forwarded(r.Context()) {
				w.WriteHeader(http.StatusTeapot)
			}
		}))
	dial := func(certs []tls.Certificate) error {
		conn, err := tls.Dial("tcp", addr, &tls.Config{
			RootCAs: m.CAPool, ServerName: gatewaycontrol.GatewayDNSName, Certificates: certs, MinVersion: tls.VersionTLS13,
		})
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()
		// TLS 1.3 reports a client-certificate rejection on the first read.
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := conn.Write([]byte("GET /x HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
			return err
		}
		_, err = conn.Read(make([]byte, 1))
		return err
	}
	load := func(f gatewaypki.LeafFiles) []tls.Certificate {
		c, err := tls.LoadX509KeyPair(f.CertFile, f.KeyFile)
		if err != nil {
			t.Fatalf("load leaf: %v", err)
		}
		return []tls.Certificate{c}
	}
	if err := dial(load(gatewaypki.LeafFiles{CertFile: m.CertFile, KeyFile: m.KeyFile})); err != nil {
		t.Errorf("gateway-SAN client rejected: %v", err)
	}
	if err := dial(nil); err == nil {
		t.Error("a client with no certificate was accepted")
	}
	if err := dial(load(m.Leaf(t, []string{"lenny-other.lenny-system.svc"}, nil))); err == nil {
		t.Error("a cluster-CA certificate with another DNS SAN was accepted")
	}
	spiffe := m.Leaf(t, nil, []*url.URL{{Scheme: "spiffe", Host: "lenny.local", Path: "/ns/lenny-agents/sa/agent"}})
	if err := dial(load(spiffe)); err == nil {
		t.Error("an agent SPIFFE certificate was accepted")
	}
	plain, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = plain.Close() }()
	_, _ = plain.Write([]byte("GET /x HTTP/1.1\r\nHost: x\r\n\r\n"))
	_ = plain.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf, _ := io.ReadAll(plain)
	if strings.Contains(string(buf), "HTTP/1.1 200") || strings.Contains(string(buf), "418") {
		t.Errorf("a plaintext request reached the handler: %q", buf)
	}
}

// spec: 29.3 (Interactive message send) Forwarding rule 3, 10.3 (mTLS PKI) NET-072
func TestHolderForwardHandlerAcceptsNoOtherAssertedPrincipal_spec_29_3(t *testing.T) {
	var got authmw.Principal
	var forwarded bool
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = authmw.FromContext(r.Context())
		forwarded = Forwarded(r.Context())
		if r.Header.Get(CallingSessionHeader) != "" {
			t.Errorf("the calling-session header reached the handler")
		}
	})
	sessions := callingSessions{"sess-parent": {ID: "sess-parent", TenantID: "acme", UserID: "alice"}}
	h := NewHolderForwardHandler(inner, authmw.Options{RequireAuth: true}, sessions)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set(CallingSessionHeader, "sess-parent")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got.SessionID != "sess-parent" || got.CallerType != "agent" || !forwarded {
		t.Errorf("calling-session principal = %+v forwarded=%v, want the session's agent principal on a forwarded request", got, forwarded)
	}

	got = authmw.Principal{}
	req = httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set(CallingSessionHeader, "sess-parent")
	req.Header.Set("Authorization", "Bearer forged")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got.SessionID != "" || rec.Code == http.StatusOK {
		t.Errorf("a credential plus a calling session ran as %+v with %d, want the credential authenticated and the header ignored", got, rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set(CallingSessionHeader, "sess-unknown")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unknown calling session answered %d, want 401", rec.Code)
	}
}
