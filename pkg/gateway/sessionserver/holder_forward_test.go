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
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nhooyr.io/websocket"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
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
	return sessionstore.Session{UserID: testOwner, ID: "sess-1", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-1"}
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
	var d net.Dialer
	return newHolderForwardClient(holderForwardClientTLS(reloader, m.CAPool), bound, d.DialContext)
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
	mux.Handle("/mcp", asOwner(mcpSrv.Handler()))
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

// spec: 29.3 (Interactive message send) Forwarding rule 4, 11.5 (Idempotency) Mechanism
func TestDeliveredForwardIsStoredAndReplayedUnderIdempotency_spec_11_5(t *testing.T) {
	m := gatewaypki.Mint(t)
	var calls atomic.Int64
	coordinator := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"state":"running"}`))
	})
	listener := NewHolderForwardHandler(coordinator, authmw.Options{AllowDevHeaders: true, DevHeaderNamesTenant: true}, callingSessions{})
	addr := startTLSServer(t, serverTLSFor(t, m, gatewaypki.LeafFiles{CertFile: m.CertFile, KeyFile: m.KeyFile}), listener)

	row := forwardRow()
	row.State = session.StateSuspended
	serving := forwardServer(leasesHeldBy("gw-b.00@"+addr), "gw-a.00@10.0.0.1:1", testForwardClient(t, m, time.Second))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serving.forwardToHolder(w, r, row, ForwardKindResume, r.Body)
	})
	store := idemmw.NewMemoryStore()
	wrapped := authmw.Wrap(idemmw.Wrap(handler, store, idemmw.Options{}), authmw.Options{AllowDevHeaders: true, DevHeaderNamesTenant: true})
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess-1/resume", bytes.NewReader([]byte("{}")))
		req.Header.Set("Idempotency-Key", "resume-key")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != `{"state":"running"}` {
			t.Fatalf("attempt %d answered %d %q, want the coordinator's 200", i, rec.Code, rec.Body.String())
		}
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("coordinator reached %d times, want 1: a same-key retry of a delivered forward replays the stored response", n)
	}
	if _, ok, _ := store.Get(context.Background(), "acme", "resume-key"); !ok {
		t.Error("the delivered forward was not stored in the idempotency cache")
	}
}

// spec: 29.3 (Interactive message send) Forwarding rule 4, 11.5 (Idempotency) Mechanism
func TestForwardResponseFlushesOnlyALiveStream_spec_29_3(t *testing.T) {
	cases := []struct {
		name      string
		header    http.Header
		length    int64
		encodings []string
		live      bool
	}{
		{"event stream", http.Header{"Content-Type": {"text/event-stream; charset=utf-8"}}, -1, nil, true},
		{"chunked without a length", http.Header{"Content-Type": {"application/json"}}, -1, []string{"chunked"}, true},
		{"JSON with a length", http.Header{"Content-Type": {"application/json"}}, 12, nil, false},
		{"no content type and no encoding", http.Header{}, -1, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: http.StatusOK, Header: tc.header, ContentLength: tc.length,
				TransferEncoding: tc.encodings, Body: io.NopCloser(strings.NewReader("{\"ok\":true}\n")),
			}
			rec := httptest.NewRecorder()
			if out := copyForwardResponse(context.Background(), rec, resp); out != ForwardDelivered {
				t.Fatalf("outcome = %v, want delivered", out)
			}
			if rec.Flushed != tc.live {
				t.Errorf("flushed = %v, want %v", rec.Flushed, tc.live)
			}
			if rec.Body.String() != "{\"ok\":true}\n" {
				t.Errorf("body = %q, want the coordinator's body", rec.Body.String())
			}
		})
	}
}

// holderValueShape is `<identity>.<16 hex digits>@<address>`.
var holderValueShape = regexp.MustCompile(`^(.+)\.([0-9a-f]{16})@(.+)$`)

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination) Replica address
func TestHolderValueCarriesIdentityTokenAndAddress_spec_10_1_1(t *testing.T) {
	cases := []struct {
		name, addr, wantAddr string
	}{
		{"ipv4", "10.0.0.7:50054", "10.0.0.7:50054"},
		{"unbracketed ipv6 is bracketed", "fd00::7:50054", "[fd00::7]:50054"},
		{"bracketed ipv6 is unchanged", "[fd00::7]:50054", "[fd00::7]:50054"},
		{"dns name", "gateway:50054", "gateway:50054"},
		{"leading zero port is normalized", "10.0.0.7:050054", "10.0.0.7:50054"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := HolderValue("gw-a", c.addr)
			if err != nil {
				t.Fatalf("HolderValue(%q) error: %v", c.addr, err)
			}
			m := holderValueShape.FindStringSubmatch(got)
			if m == nil || m[1] != "gw-a" || m[3] != c.wantAddr {
				t.Fatalf("HolderValue(%q) = %q, want gw-a.<token>@%s", c.addr, got, c.wantAddr)
			}
			addr, ok := HolderAddress(got)
			if !ok || addr != c.wantAddr {
				t.Errorf("HolderAddress(%q) = %q, %v; want %q, true", got, addr, ok, c.wantAddr)
			}
		})
	}
}

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination) Replica address
func TestHolderValueRejectsInvalidAdvertiseAddress_spec_10_1_1(t *testing.T) {
	for _, addr := range []string{
		"", "10.0.0.7", "10.0.0.7:", "10.0.0.7:0", "10.0.0.7:65536", ":50054",
		"[fd00::7:50054", "fd00::7]:50054", "[]:50054", "10.0.0.7:+80", "10.0.0.7:http",
	} {
		if got, err := HolderValue("gw-a", addr); err == nil {
			t.Errorf("HolderValue(%q) = %q, want an error", addr, got)
		}
	}
	if _, err := HolderValue("", "10.0.0.7:50054"); err == nil {
		t.Error("HolderValue with an empty identity succeeded, want an error")
	}
}

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination) Replica address
func TestHolderValueDrawsAFreshProcessTokenPerCall_spec_10_1_1(t *testing.T) {
	a, errA := HolderValue("gw-a", "10.0.0.7:50054")
	b, errB := HolderValue("gw-a", "10.0.0.7:50054")
	if errA != nil || errB != nil {
		t.Fatalf("HolderValue errors: %v, %v", errA, errB)
	}
	if a == b {
		t.Fatalf("two calls returned the same holder value %q; the process token must be drawn per call", a)
	}
}

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination) Replica address
func TestHolderAddressSplitsAtTheLastAt_spec_10_1_1(t *testing.T) {
	cases := []struct {
		holder, want string
		ok           bool
	}{
		{"gw-a", "", false},
		{"gw-a.0011223344556677", "", false},
		{"gw-a.0011223344556677@", "", false},
		{"gw-a.0011223344556677@10.0.0.7", "", false},
		{"gw-a.0011223344556677@:50054", "", false},
		{"ops@acme.0011223344556677@10.0.0.7:50054", "10.0.0.7:50054", true},
		{"gw-a.0011223344556677@[fd00::7]:50054", "[fd00::7]:50054", true},
	}
	for _, c := range cases {
		got, ok := HolderAddress(c.holder)
		if ok != c.ok || got != c.want {
			t.Errorf("HolderAddress(%q) = %q, %v; want %q, %v", c.holder, got, ok, c.want, c.ok)
		}
	}
}

// boundedForwardClient builds a forward client over m's gateway leaf with the
// specified dial and handshake bound, opening TCP connections with dial.
func boundedForwardClient(t *testing.T, m *gatewaypki.Material, dial dialFunc) *http.Client {
	t.Helper()
	reloader, err := certreload.New(m.CertFile, m.KeyFile)
	if err != nil {
		t.Fatalf("certreload: %v", err)
	}
	return newHolderForwardClient(holderForwardClientTLS(reloader, m.CAPool), HolderForwardDialBound, dial)
}

// assertEndsAtTheDialBound checks that an attempt that started at start ended
// at the specified 2-second bound rather than at a later per-step bound.
func assertEndsAtTheDialBound(t *testing.T, start time.Time) {
	t.Helper()
	if el := time.Since(start); el < HolderForwardDialBound-100*time.Millisecond || el > HolderForwardDialBound+900*time.Millisecond {
		t.Errorf("attempt took %v, want it to end at the %v bound over the TCP dial and the TLS handshake together", el, HolderForwardDialBound)
	}
}

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination) Replica address, 29.3 (Interactive message send) Forwarding rules 6 and 8
func TestForwardDialAndHandshakeEndTogetherAtTwoSeconds_spec_10_1_1(t *testing.T) {
	m := gatewaypki.Mint(t)
	t.Run("a dial that hangs under a caller context with no deadline", func(t *testing.T) {
		t.Parallel()
		hang := func(ctx context.Context, _, _ string) (net.Conn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		s := forwardServer(leasesHeldBy("gw-b.00@10.0.0.9:50054"), "gw-a.00@10.0.0.1:1", boundedForwardClient(t, m, hang))
		start := time.Now()
		if got := s.forwardToHolder(httptest.NewRecorder(), clientRequest(context.Background(), "{}"), forwardRow(), ForwardKindInterrupt, nil); got != ForwardNotDelivered {
			t.Fatalf("outcome = %v, want ForwardNotDelivered", got)
		}
		assertEndsAtTheDialBound(t, start)
	})
	t.Run("a dial that completes after 1.5 seconds followed by a hanging handshake", func(t *testing.T) {
		t.Parallel()
		silent := silentListener(t)
		slow := func(ctx context.Context, network, addr string) (net.Conn, error) {
			select {
			case <-time.After(1500 * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		}
		s := forwardServer(leasesHeldBy("gw-b.00@"+silent), "gw-a.00@10.0.0.1:1", boundedForwardClient(t, m, slow))
		start := time.Now()
		if got := s.forwardToHolder(httptest.NewRecorder(), clientRequest(context.Background(), "{}"), forwardRow(), ForwardKindInterrupt, nil); got != ForwardNotDelivered {
			t.Fatalf("outcome = %v, want ForwardNotDelivered", got)
		}
		assertEndsAtTheDialBound(t, start)
	})
}

// gatewayLeaf is m's gateway leaf as listener material.
func gatewayLeaf(m *gatewaypki.Material) gatewaypki.LeafFiles {
	return gatewaypki.LeafFiles{CertFile: m.CertFile, KeyFile: m.KeyFile}
}

// spec: 29.3 (Interactive message send) Forwarding rules 7 and 8, 11.5 (Idempotency) Mechanism
func TestForwardFailuresAfterTheConnectionAreUnknown_spec_29_3(t *testing.T) {
	m := gatewaypki.Mint(t)
	t.Run("a coordinator that resets mid-body", func(t *testing.T) {
		resetter := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("0123456789"))
			conn, _, err := http.NewResponseController(w).Hijack()
			if err != nil {
				return
			}
			if tc, ok := conn.(*tls.Conn); ok {
				if raw, ok := tc.NetConn().(*net.TCPConn); ok {
					_ = raw.SetLinger(0)
				}
			}
			_ = conn.Close()
		})
		addr := startTLSServer(t, serverTLSFor(t, m, gatewayLeaf(m)), resetter)
		s := forwardServer(leasesHeldBy("gw-b.00@"+addr), "gw-a.00@10.0.0.1:1", testForwardClient(t, m, time.Second))
		ctx := idemmw.WithGate(context.Background())
		if got := s.forwardToHolder(httptest.NewRecorder(), clientRequest(ctx, "{}"), forwardRow(), ForwardKindUpload, strings.NewReader("{}")); got != ForwardUnknown {
			t.Fatalf("outcome = %v, want ForwardUnknown", got)
		}
		if !idemmw.NotStored(ctx) {
			t.Error("a response truncated mid-body was left storable (§11.5)")
		}
	})
	t.Run("the caller cancels after the coordinator read the request", func(t *testing.T) {
		// The coordinator holds the request until the forward has returned.
		// Waiting on r.Context() instead lets it answer while the caller's
		// cancel is closing the connection: tls.Conn.Close sends
		// close_notify before it closes the socket, the coordinator's
		// request context ends on that alert, and its empty 200 can reach
		// the transport's read loop before the socket closes. The transport
		// then returns that response, and the forward is correctly
		// ForwardDelivered, which is not the interleaving this case pins.
		read := make(chan struct{})
		release := make(chan struct{})
		waiter := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			_, _ = io.ReadAll(r.Body)
			close(read)
			<-release
		})
		addr := startTLSServer(t, serverTLSFor(t, m, gatewayLeaf(m)), waiter)
		s := forwardServer(leasesHeldBy("gw-b.00@"+addr), "gw-a.00@10.0.0.1:1", testForwardClient(t, m, time.Second))
		ctx, cancel := context.WithCancel(idemmw.WithGate(context.Background()))
		defer cancel()
		go func() {
			<-read
			cancel()
		}()
		rec := httptest.NewRecorder()
		got := s.forwardToHolder(rec, clientRequest(ctx, "{}"), forwardRow(), ForwardKindMessage, strings.NewReader("{}"))
		close(release)
		if got != ForwardUnknown {
			t.Fatalf("outcome = %v, want ForwardUnknown", got)
		}
		assertTargetNotReady(t, rec, "running")
		if !idemmw.NotStored(ctx) {
			t.Error("an unknown outcome was left storable (§11.5)")
		}
	})
}

// armedConn fails writes once armed: it lets allow writes through and fails
// every write after them.
type armedConn struct {
	net.Conn
	armed  *atomic.Bool
	allow  int
	writes atomic.Int64
}

func (c *armedConn) Write(p []byte) (int, error) {
	if c.armed.Load() && c.writes.Add(1) > int64(c.allow) {
		return 0, errors.New("injected write failure on a reused connection")
	}
	return c.Conn.Write(p)
}

// keepAliveFixture is a coordinator reached over one keep-alive connection
// whose writes fail once armed.
type keepAliveFixture struct {
	server *Server
	armed  atomic.Bool
	dials  atomic.Int64
	calls  atomic.Int64
}

// newKeepAliveFixture serves a counting coordinator and returns a serving
// replica whose first forward connection fails its writes after allow
// writes once armed. Later connections are not armed.
func newKeepAliveFixture(t *testing.T, m *gatewaypki.Material, allow int) *keepAliveFixture {
	t.Helper()
	f := &keepAliveFixture{}
	coordinator := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		f.calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	addr := startTLSServer(t, serverTLSFor(t, m, gatewayLeaf(m)), coordinator)
	dial := func(ctx context.Context, network, a string) (net.Conn, error) {
		var d net.Dialer
		conn, err := d.DialContext(ctx, network, a)
		if err != nil || f.dials.Add(1) > 1 {
			return conn, err
		}
		return &armedConn{Conn: conn, armed: &f.armed, allow: allow}, nil
	}
	f.server = forwardServer(leasesHeldBy("gw-b.00@"+addr), "gw-a.00@10.0.0.1:1", boundedForwardClient(t, m, dial))
	return f
}

// forward sends one message forward with body and returns its outcome.
func (f *keepAliveFixture) forward(body string) ForwardOutcome {
	return f.server.forwardToHolder(httptest.NewRecorder(), clientRequest(context.Background(), body), forwardRow(), ForwardKindMessageNoReplyTo, strings.NewReader(body))
}

// spec: 29.3 (Interactive message send) Forwarding rules 7 and 8
func TestForwardOnAReusedKeepAliveConnectionThatFailsOnWriteIsUnknown_spec_29_3(t *testing.T) {
	m := gatewaypki.Mint(t)
	cases := []struct {
		name  string
		allow int
		body  string
	}{
		{"the write fails after part of the request was written", 1, strings.Repeat("x", 64<<10)},
		{"the write fails before any byte was written", 0, `{"messages":[]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newKeepAliveFixture(t, m, c.allow)
			if got := f.forward(`{"messages":[]}`); got != ForwardDelivered {
				t.Fatalf("first forward = %v, want ForwardDelivered", got)
			}
			f.armed.Store(true)
			if got := f.forward(c.body); got != ForwardUnknown {
				t.Fatalf("forward on the reused connection = %v, want ForwardUnknown: the coordinator may have received it, and the forward is not retried", got)
			}
			if f.dials.Load() > 1 && f.calls.Load() != 1 {
				t.Errorf("coordinator reached %d times, want 1: the transport re-sent the forward on a new connection", f.calls.Load())
			}
		})
	}
}

// spec: 29.3 (Interactive message send) Forwarding rule 8
func TestForwardRunsUnderTheCallersDeadlineWithNoBoundOfItsOwn_spec_29_3(t *testing.T) {
	const answerAfter = 35 * time.Second
	m := gatewaypki.Mint(t)
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		select {
		case <-time.After(answerAfter):
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"delivered":true}`))
	})
	addr := startTLSServer(t, serverTLSFor(t, m, gatewayLeaf(m)), slow)
	client := testForwardClient(t, m, HolderForwardDialBound)
	for _, kind := range []struct {
		name string
		kind ForwardKind
	}{{"message", ForwardKindMessageNoReplyTo}, {"interrupt", ForwardKindInterrupt}} {
		t.Run(kind.name, func(t *testing.T) {
			t.Parallel()
			s := forwardServer(leasesHeldBy("gw-b.00@"+addr), "gw-a.00@10.0.0.1:1", client)
			rec := httptest.NewRecorder()
			if got := s.forwardToHolder(rec, clientRequest(context.Background(), "{}"), forwardRow(), kind.kind, strings.NewReader("{}")); got != ForwardDelivered {
				t.Fatalf("outcome = %v, want ForwardDelivered", got)
			}
			if rec.Code != http.StatusOK || rec.Body.String() != `{"delivered":true}` {
				t.Errorf("caller received %d %q, want the coordinator's response after %v with no 409", rec.Code, rec.Body.String(), answerAfter)
			}
		})
	}
}

// closingCoordinator closes the connection after it reads the request.
func closingCoordinator() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		conn, _, err := http.NewResponseController(w).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	})
}

// nonHolderCoordinator is a forward listener whose replica does not hold the
// session's lease, so its own helper refuses the forwarded request under
// §29.3 Forwarding rule 5. Its REST surface serves the held-pod resume and
// its /mcp surface serves lenny/target.
func nonHolderCoordinator(t *testing.T, m *gatewaypki.Material, row sessionstore.Session) string {
	t.Helper()
	coordinator := forwardServer(leasesHeldBy("gw-c.00@127.0.0.1:1"), "gw-b.00@127.0.0.1:2", nil)
	mcpSrv := mcp.NewServer()
	mcpSrv.RegisterTool(mcp.Tool{Name: "lenny/target"}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		res, _, err := coordinator.ForwardToolCall(ctx, row, ForwardKindResume, "lenny/target", args)
		return res, err
	})
	rest := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		coordinator.forwardToHolder(w, r, row, ForwardKindResume, r.Body)
	})
	return coordinatorChain(t, m, mcpSrv, rest, authmw.Options{AllowDevHeaders: true, DevHeaderNamesTenant: true})
}

// servingSurface is the serving replica's §11.5-wrapped REST resume route
// and /mcp endpoint, each calling the forward helper.
type servingSurface struct {
	handler http.Handler
	store   *idemmw.MemoryStore
	calls   atomic.Int64
}

// newServingSurface mounts a test handler at /v1/sessions/{id}/resume that
// calls forwardToHolder, and a test tool lenny/forwarded at /mcp that calls
// ForwardToolCall, behind the auth and §11.5 idempotency middleware.
func newServingSurface(s *Server, row sessionstore.Session) *servingSurface {
	ss := &servingSurface{store: idemmw.NewMemoryStore()}
	mcpSrv := mcp.NewServer()
	mcpSrv.RegisterTool(mcp.Tool{Name: "lenny/forwarded"}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		ss.calls.Add(1)
		res, _, err := s.ForwardToolCall(ctx, row, ForwardKindResume, "lenny/target", args)
		return res, err
	})
	mux := http.NewServeMux()
	mux.Handle("/mcp", asOwner(mcpSrv.Handler()))
	mux.HandleFunc("/v1/sessions/{id}/resume", func(w http.ResponseWriter, r *http.Request) {
		ss.calls.Add(1)
		s.forwardToHolder(w, r, row, ForwardKindResume, r.Body)
	})
	dev := authmw.Options{AllowDevHeaders: true, DevHeaderNamesTenant: true}
	ss.handler = authmw.Wrap(idemmw.Wrap(mux, ss.store, idemmw.Options{}), dev)
	return ss
}

// toolCallBody is a tools/call of name with empty arguments.
func toolCallBody(name string) string {
	return `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"` + name + `","arguments":{}}}`
}

// send posts body to path with an Idempotency-Key and returns the response.
func (ss *servingSurface) send(path, body, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rec := httptest.NewRecorder()
	ss.handler.ServeHTTP(rec, req)
	return rec
}

// responseCode returns the lenny error code of a REST envelope or an MCP tool
// error, or "" for neither.
func responseCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env errorEnvelope
	if json.Unmarshal(rec.Body.Bytes(), &env) == nil && env.Error.Code != "" {
		return env.Error.Code
	}
	if _, te, err := mcp.DecodeToolCallResponse(rec.Body.Bytes()); err == nil && te != nil {
		return te.Code
	}
	return ""
}

// spec: 11.5 (Idempotency) Mechanism, 29.3 (Interactive message send) Forwarding rules 5, 6, and 7
func TestForwardRefusalsStayOutOfTheIdempotencyCacheOnBothSurfaces_spec_11_5(t *testing.T) {
	m := gatewaypki.Mint(t)
	row := forwardRow()
	row.State = session.StateSuspended
	targets := []struct {
		name string
		addr string
	}{
		{"not delivered", closedAddr(t)},
		{"unknown outcome", startTLSServer(t, serverTLSFor(t, m, gatewayLeaf(m)), closingCoordinator())},
		{"refused by a non-holder", nonHolderCoordinator(t, m, row)},
	}
	surfaces := []struct {
		name, path, body string
	}{
		{"REST resume", "/v1/sessions/sess-1/resume", "{}"},
		{"MCP tool", "/mcp", toolCallBody("lenny/forwarded")},
	}
	for _, target := range targets {
		for _, surface := range surfaces {
			t.Run(target.name+" on the "+surface.name, func(t *testing.T) {
				serving := forwardServer(leasesHeldBy("gw-b.00@"+target.addr), "gw-a.00@10.0.0.1:1", testForwardClient(t, m, time.Second))
				ss := newServingSurface(serving, row)
				for i := 0; i < 2; i++ {
					rec := ss.send(surface.path, surface.body, "key-"+target.name)
					if code := responseCode(t, rec); code != "TARGET_NOT_READY" {
						t.Fatalf("attempt %d answered %d %s, want TARGET_NOT_READY", i, rec.Code, rec.Body.String())
					}
					if rec.Header().Get(forwardNoStoreHeader) != "" {
						t.Errorf("attempt %d carries the internal no-store mark", i)
					}
				}
				if n := ss.calls.Load(); n != 2 {
					t.Errorf("helper reached %d times, want 2: a same-key retry after a refusal runs again", n)
				}
				if _, ok, _ := ss.store.Get(context.Background(), "acme", "key-"+target.name); ok {
					t.Error("the refusal was stored in the idempotency cache")
				}
			})
		}
	}
	t.Run("a delivered coordinator 409 without the mark is stored", func(t *testing.T) {
		var reached atomic.Int64
		conflict := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			reached.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":{"code":"TARGET_NOT_READY","message":"not suspended","details":{"currentState":"running"}}}`))
		})
		listener := NewHolderForwardHandler(conflict, authmw.Options{AllowDevHeaders: true, DevHeaderNamesTenant: true}, callingSessions{})
		addr := startTLSServer(t, serverTLSFor(t, m, gatewayLeaf(m)), listener)
		serving := forwardServer(leasesHeldBy("gw-b.00@"+addr), "gw-a.00@10.0.0.1:1", testForwardClient(t, m, time.Second))
		ss := newServingSurface(serving, row)
		for i := 0; i < 2; i++ {
			if rec := ss.send("/v1/sessions/sess-1/resume", "{}", "delivered-409"); rec.Code != http.StatusConflict {
				t.Fatalf("attempt %d answered %d, want the coordinator's 409", i, rec.Code)
			}
		}
		if n := reached.Load(); n != 1 {
			t.Errorf("coordinator reached %d times, want 1: its own refusal is stored and replayed", n)
		}
		if _, ok, _ := ss.store.Get(context.Background(), "acme", "delivered-409"); !ok {
			t.Error("the coordinator's own 409 was not stored")
		}
	})
}

// staleRevocations reports the revocation set as stale, as a replica that
// cannot reach Postgres does.
type staleRevocations struct{}

func (staleRevocations) Stale() bool { return true }

// testBearer signs a user bearer for alice with the given expiry.
func testBearer(t *testing.T, signer *jwt.HMACSigner, exp time.Time) string {
	t.Helper()
	tok, err := signer.Sign(jwt.Claims{Subject: "alice", TenantID: "acme", Expiry: exp.Unix(), Typ: pkgauth.TokenUserBearer})
	if err != nil {
		t.Fatalf("sign bearer: %v", err)
	}
	return tok
}

// toolForwardingServer is a serving replica's MCP server whose test tool
// lenny/forwarded returns what ForwardToolCall returns for row, and whose
// requests carry credential.
func toolForwardingServer(s *Server, row sessionstore.Session, credential string) http.Handler {
	mcpSrv := mcp.NewServer()
	mcpSrv.RegisterTool(mcp.Tool{Name: "lenny/forwarded"}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		res, _, err := s.ForwardToolCall(ctx, row, ForwardKindTerminate, "lenny/target", args)
		return res, err
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := authmw.WithCredential(r.Context(), authmw.Credential{Authorization: "Bearer " + credential})
		asOwner(mcpSrv.Handler()).ServeHTTP(w, r.WithContext(ctx))
	})
}

// spec: 29.3 (Interactive message send) Forwarding rules 2 and 3, 15.2 (MCP API), 15.2.1 (REST/MCP consistency contract), 13.3 (Credential Flow)
func TestForwardToolCallReturnsEachCoordinatorResponseToTheClient_spec_29_3(t *testing.T) {
	m := gatewaypki.Mint(t)
	signer := jwt.NewHMACSigner("k1", []byte("coordinator-secret"))
	other := jwt.NewHMACSigner("k1", []byte("another-secret"))
	coordinatorTools := func() *mcp.Server {
		srv := mcp.NewServer()
		srv.RegisterTool(mcp.Tool{Name: "lenny/target"}, func(context.Context, json.RawMessage) (mcp.ToolResult, error) {
			return mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: "terminated"}}}, nil
		})
		return srv
	}
	failingTools := func() *mcp.Server {
		srv := mcp.NewServer()
		srv.RegisterTool(mcp.Tool{Name: "lenny/target"}, func(context.Context, json.RawMessage) (mcp.ToolResult, error) {
			return mcp.ToolResult{}, mcp.NewToolError("PERMISSION_DENIED", "not the owner", nil)
		})
		return srv
	}
	valid := testBearer(t, signer, time.Now().Add(time.Hour))
	cases := []struct {
		name     string
		tools    *mcp.Server
		opts     authmw.Options
		bearer   string
		wantText string
		wantCode string
	}{
		{"a result", coordinatorTools(), authmw.Options{RequireAuth: true, Verifier: signer}, valid, "terminated", ""},
		{"a tool error", failingTools(), authmw.Options{RequireAuth: true, Verifier: signer}, valid, "", "PERMISSION_DENIED"},
		{"a pre-dispatch JSON-RPC error", mcp.NewServer(), authmw.Options{RequireAuth: true, Verifier: signer}, valid, "", "RESOURCE_NOT_FOUND"},
		{"401 TOKEN_EXPIRED", coordinatorTools(), authmw.Options{RequireAuth: true, Verifier: signer}, testBearer(t, signer, time.Now().Add(-time.Hour)), "", "TOKEN_EXPIRED"},
		{"401 TOKEN_INVALID", coordinatorTools(), authmw.Options{RequireAuth: true, Verifier: signer}, testBearer(t, other, time.Now().Add(time.Hour)), "", "TOKEN_INVALID"},
		{"503 token_validation_unavailable", coordinatorTools(), authmw.Options{RequireAuth: true, Verifier: signer, RevocationFreshness: staleRevocations{}}, valid, "", "token_validation_unavailable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			addr := coordinatorChain(t, m, c.tools, nil, c.opts)
			serving := forwardServer(leasesHeldBy("gw-b.00@"+addr), "gw-a.00@10.0.0.1:1", testForwardClient(t, m, time.Second))
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(toolCallBody("lenny/forwarded")))
			toolForwardingServer(serving, forwardRow(), c.bearer).ServeHTTP(rec, req)
			res, te, err := mcp.DecodeToolCallResponse(rec.Body.Bytes())
			if err != nil {
				t.Fatalf("decode client response %q: %v", rec.Body.String(), err)
			}
			switch {
			case c.wantCode != "":
				if te == nil || te.Code != c.wantCode {
					t.Errorf("client received %+v / %+v, want the tool error %s", res, te, c.wantCode)
				}
			case te != nil || len(res.Content) != 1 || res.Content[0].Text != c.wantText:
				t.Errorf("client received %+v / %+v, want the coordinator's result %q", res, te, c.wantText)
			}
		})
	}
	t.Run("a 200 body that is not JSON-RPC", func(t *testing.T) {
		garbage := startTLSServer(t, serverTLSFor(t, m, gatewayLeaf(m)),
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>ok</html>")) }))
		serving := forwardServer(leasesHeldBy("gw-b.00@"+garbage), "gw-a.00@10.0.0.1:1", testForwardClient(t, m, time.Second))
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(toolCallBody("lenny/forwarded")))
		toolForwardingServer(serving, forwardRow(), valid).ServeHTTP(rec, req)
		if _, te, err := mcp.DecodeToolCallResponse(rec.Body.Bytes()); err != nil || te == nil || te.Code != "TARGET_NOT_READY" || te.Details["currentState"] != "running" {
			t.Errorf("client received %q, want TARGET_NOT_READY with currentState running", rec.Body.String())
		}
	})
}

// expiringVerifier verifies with inner until expire is set, then reports
// every token as expired, as a token whose exp passed after the WebSocket
// upgrade authenticated it.
type expiringVerifier struct {
	inner  jwt.Verifier
	expire atomic.Bool
}

func (v *expiringVerifier) Verify(token string) (jwt.Claims, error) {
	if v.expire.Load() {
		return jwt.Claims{}, &jwt.VerifyError{Reason: "expired"}
	}
	return v.inner.Verify(token)
}

// credentialFixture is a serving replica and its coordinator sharing one
// token verifier. The coordinator's REST interrupt route and its
// lenny/target tool record the credential and the principal they ran
// under.
type credentialFixture struct {
	verifier  *expiringVerifier
	bearer    string
	serving   http.Handler
	restSeen  chan capturedPrincipal
	toolSeen  chan capturedPrincipal
	restCalls atomic.Int64
}

// capturedPrincipal is what a coordinator handler received.
type capturedPrincipal struct {
	authorization string
	subject       string
}

func newCredentialFixture(t *testing.T) *credentialFixture {
	t.Helper()
	m := gatewaypki.Mint(t)
	signer := jwt.NewHMACSigner("k1", []byte("shared-secret"))
	f := &credentialFixture{
		verifier: &expiringVerifier{inner: signer},
		bearer:   testBearer(t, signer, time.Now().Add(time.Hour)),
		restSeen: make(chan capturedPrincipal, 4),
		toolSeen: make(chan capturedPrincipal, 4),
	}
	coordTools := mcp.NewServer()
	coordTools.RegisterTool(mcp.Tool{Name: "lenny/target"}, func(ctx context.Context, _ json.RawMessage) (mcp.ToolResult, error) {
		p, _ := authmw.FromContext(ctx)
		cred, _ := authmw.CredentialFrom(ctx)
		f.toolSeen <- capturedPrincipal{authorization: cred.Authorization, subject: p.Subject}
		return mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: "remote"}}}, nil
	})
	coordREST := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.restCalls.Add(1)
		p, _ := authmw.FromContext(r.Context())
		f.restSeen <- capturedPrincipal{authorization: r.Header.Get("Authorization"), subject: p.Subject}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"interrupted":true}`))
	})
	addr := coordinatorChain(t, m, coordTools, coordREST, authmw.Options{RequireAuth: true, Verifier: f.verifier})
	s := forwardServer(leasesHeldBy("gw-b.00@"+addr), "gw-a.00@10.0.0.1:1", testForwardClient(t, m, time.Second))
	f.serving = credentialServingSurface(s, f.verifier)
	return f
}

// credentialServingSurface is the serving replica: its REST interrupt route
// calls forwardToHolder, the in-process service layer dispatches to that
// route, and its MCP server, over POST /mcp and the WebSocket transport,
// serves lenny/service through ServiceCall and lenny/forwarded through
// ForwardToolCall. lenny/forwarded with {"local":true} targets a session
// this replica serves itself.
func credentialServingSurface(s *Server, verifier jwt.Verifier) http.Handler {
	rest := http.NewServeMux()
	rest.HandleFunc("/v1/sessions/{id}/interrupt", func(w http.ResponseWriter, r *http.Request) {
		s.forwardToHolder(w, r, forwardRow(), ForwardKindInterrupt, r.Body)
	})
	s.serviceHandlerOnce.Do(func() { s.serviceHandler = rest })

	tools := mcp.NewServer()
	tools.RegisterTool(mcp.Tool{Name: "lenny/service"}, func(ctx context.Context, _ json.RawMessage) (mcp.ToolResult, error) {
		res, serr := s.ServiceCall(ctx, "acme", http.MethodPost, "/v1/sessions/sess-1/interrupt", nil, "", nil)
		if serr != nil {
			return mcp.ToolResult{}, mcp.NewToolError(serr.Code, serr.Message, nil)
		}
		return mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: string(res.Body)}}}, nil
	})
	tools.RegisterTool(mcp.Tool{Name: "lenny/forwarded"}, func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		var in struct{ Local bool }
		_ = json.Unmarshal(args, &in)
		row := forwardRow()
		if in.Local {
			row.State = session.StateCreated
		}
		res, out, err := s.ForwardToolCall(ctx, row, ForwardKindTerminate, "lenny/target", args)
		if out == ForwardLocal {
			return mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: "local"}}}, nil
		}
		return res, err
	})
	mux := http.NewServeMux()
	mux.Handle("/v1/sessions/", rest)
	mux.Handle("POST /mcp", asOwner(tools.Handler()))
	mux.Handle("GET /mcp/v1/ws", tools.WebSocketHandler())
	return authmw.Wrap(mux, authmw.Options{Verifier: verifier})
}

// post sends a request to the serving replica with the bearer, or with no
// credential when bearer is empty.
func (f *credentialFixture) post(path, body, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	f.serving.ServeHTTP(rec, req)
	return rec
}

// wsToolCall sends one tools/call frame on conn and decodes the response.
func wsToolCall(t *testing.T, conn *websocket.Conn, name, args string) (mcp.ToolResult, *mcp.ToolError) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frame := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
	if err := conn.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	res, te, err := mcp.DecodeToolCallResponse(data)
	if err != nil {
		t.Fatalf("decode frame %q: %v", data, err)
	}
	return res, te
}

// spec: 29.3 (Interactive message send) Forwarding rules 2 and 3, 15.2 (MCP API), 27.3.1 (Silent refresh), 13.3 (Credential Flow)
func TestForwardCarriesTheRecordedCredentialOnEverySurface_spec_29_3(t *testing.T) {
	t.Run("a REST bearer", func(t *testing.T) {
		f := newCredentialFixture(t)
		rec := f.post("/v1/sessions/sess-1/interrupt", "{}", f.bearer)
		if rec.Code != http.StatusOK || rec.Body.String() != `{"interrupted":true}` {
			t.Fatalf("client received %d %q, want the coordinator's response", rec.Code, rec.Body.String())
		}
		if got := <-f.restSeen; got.authorization != "Bearer "+f.bearer || got.subject != "alice" {
			t.Errorf("coordinator received %+v, want the client's bearer authenticated as alice", got)
		}
	})
	t.Run("a request with no credential is refused by the coordinator", func(t *testing.T) {
		f := newCredentialFixture(t)
		rec := f.post("/v1/sessions/sess-1/interrupt", "{}", "")
		var env errorEnvelope
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if rec.Code != http.StatusUnauthorized || env.Error.Code != "UNAUTHORIZED" {
			t.Errorf("client received %d %s, want the coordinator's 401 UNAUTHORIZED", rec.Code, rec.Body.String())
		}
		if n := f.restCalls.Load(); n != 0 {
			t.Errorf("coordinator handler ran %d times without a credential", n)
		}
	})
	t.Run("MCP through ServiceCall", func(t *testing.T) {
		f := newCredentialFixture(t)
		rec := f.post("/mcp", toolCallBody("lenny/service"), f.bearer)
		res, te, err := mcp.DecodeToolCallResponse(rec.Body.Bytes())
		if err != nil || te != nil || len(res.Content) != 1 || res.Content[0].Text != `{"interrupted":true}` {
			t.Fatalf("client received %q, want the coordinator's interrupt response", rec.Body.String())
		}
		if got := <-f.restSeen; got.authorization != "Bearer "+f.bearer || got.subject != "alice" {
			t.Errorf("coordinator received %+v, want the MCP request's bearer, which ServiceCall carries on its context", got)
		}
	})
	t.Run("a WebSocket-upgraded connection, before and after its credential expires", func(t *testing.T) {
		f := newCredentialFixture(t)
		srv := httptest.NewServer(f.serving)
		defer srv.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		conn, _, err := websocket.Dial(ctx, strings.Replace(srv.URL, "http://", "ws://", 1)+"/mcp/v1/ws", &websocket.DialOptions{
			HTTPHeader: http.Header{"Authorization": {"Bearer " + f.bearer}},
		})
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		if res, te := wsToolCall(t, conn, "lenny/forwarded", `{}`); te != nil || len(res.Content) != 1 || res.Content[0].Text != "remote" {
			t.Fatalf("forwarded call = %+v / %+v, want the coordinator's result", res, te)
		}
		if got := <-f.toolSeen; got.authorization != "Bearer "+f.bearer || got.subject != "alice" {
			t.Errorf("coordinator received %+v, want the bearer the upgrade authenticated", got)
		}

		f.verifier.expire.Store(true)
		if _, te := wsToolCall(t, conn, "lenny/forwarded", `{}`); te == nil || te.Code != "TOKEN_EXPIRED" {
			t.Errorf("forwarded call after expiry = %+v, want the coordinator's TOKEN_EXPIRED: the forward listener applies the exp check", te)
		}
		if res, te := wsToolCall(t, conn, "lenny/forwarded", `{"local":true}`); te != nil || len(res.Content) != 1 || res.Content[0].Text != "local" {
			t.Errorf("call the serving replica coordinates = %+v / %+v, want it to succeed on the same connection", res, te)
		}
	})
}

// spec: 28.5.4 (Inter-replica) CH-HOLDERFORWARD, 29.3 (Interactive message send) Forwarding rule 2
func TestForwardedRESTRequestAndResponseCrossUnchanged_spec_28_5_4(t *testing.T) {
	m := gatewaypki.Mint(t)
	const (
		target = "/v1/sessions/sess-1/upload-to-session?part=2&name=a%20b"
		body   = "\x00\x01binary upload payload\xff"
	)
	responses := []struct {
		name    string
		handler http.HandlerFunc
		status  int
		header  map[string]string
		body    string
		flushed bool
	}{
		{
			name: "a 4xx with its body",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Request-Id", "req-7")
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"error":{"code":"VALIDATION_ERROR","message":"bad part"}}`))
			},
			status: http.StatusUnprocessableEntity,
			header: map[string]string{"Content-Type": "application/json", "X-Request-Id": "req-7"},
			body:   `{"error":{"code":"VALIDATION_ERROR","message":"bad part"}}`,
		},
		{
			name: "a streamed upload response",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				for _, chunk := range []string{"event: progress\ndata: 50\n\n", "event: done\ndata: 100\n\n"} {
					_, _ = w.Write([]byte(chunk))
					_ = http.NewResponseController(w).Flush()
				}
			},
			status:  http.StatusOK,
			header:  map[string]string{"Content-Type": "text/event-stream"},
			body:    "event: progress\ndata: 50\n\nevent: done\ndata: 100\n\n",
			flushed: true,
		},
	}
	for _, c := range responses {
		t.Run(c.name, func(t *testing.T) {
			got := make(chan capturedRequest, 1)
			coordinator := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				got <- capturedRequest{method: r.Method, uri: r.RequestURI, header: r.Header.Clone(), body: string(b)}
				c.handler(w, r)
			})
			addr := startTLSServer(t, serverTLSFor(t, m, gatewayLeaf(m)), coordinator)
			s := forwardServer(leasesHeldBy("gw-b.00@"+addr), "gw-a.00@10.0.0.1:1", testForwardClient(t, m, time.Second))
			req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/octet-stream")
			rec := httptest.NewRecorder()
			if out := s.forwardToHolder(rec, req, forwardRow(), ForwardKindUpload, req.Body); out != ForwardDelivered {
				t.Fatalf("outcome = %v, want ForwardDelivered", out)
			}
			sent := <-got
			if sent.method != "POST" || sent.uri != target || sent.body != body || sent.header.Get("Content-Type") != "application/octet-stream" {
				t.Errorf("coordinator received %s %s %q (%s), want the client's request byte for byte", sent.method, sent.uri, sent.body, sent.header.Get("Content-Type"))
			}
			if rec.Code != c.status || rec.Body.String() != c.body || rec.Flushed != c.flushed {
				t.Errorf("client received %d %q flushed=%v, want %d %q flushed=%v", rec.Code, rec.Body.String(), rec.Flushed, c.status, c.body, c.flushed)
			}
			for name, want := range c.header {
				if v := rec.Header().Get(name); v != want {
					t.Errorf("client header %s = %q, want %q", name, v, want)
				}
			}
		})
	}
}

// nativeRefusal records each call of the caller-supplied refusal renderer
// and writes a native-envelope 409 carrying the state it was given.
type nativeRefusal struct {
	calls  atomic.Int64
	states []string
}

func (n *nativeRefusal) refuse(w http.ResponseWriter, currentState string) {
	n.calls.Add(1)
	n.states = append(n.states, currentState)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","code":"TARGET_NOT_READY","current_state":"` + currentState + `"}}`))
}

// responsesRequest builds an Open Responses create request with ctx.
func responsesRequest(ctx context.Context, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r.WithContext(ctx)
}

// noDialClient is a forward client that counts dials and never connects.
func noDialClient(dials *atomic.Int64) *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, errors.New("no dial expected")
	}}}
}

// TestForwardedResponsesTurnAtANonHolderIsNotDeliveredInAnyState_spec_29_3
// pins §29.3 Forwarding rule 5 for an Open Responses warm turn: a forwarded
// turn reaching a replica that does not hold REG-COORDLEASE is not delivered
// whatever the row's state, refused once through the caller's renderer in
// the native envelope, and kept out of the §11.5 cache. The suspended row
// discriminates: under the state gate every other kind applies first, that
// row would run locally at a non-holder.
//
// spec: 29.3 (Interactive message send) Forwarding rules 4-7, 11.5 (Idempotency), 15 (Open Responses conversation sessions)
func TestForwardedResponsesTurnAtANonHolderIsNotDeliveredInAnyState_spec_29_3(t *testing.T) {
	for _, state := range []session.State{session.StateRunning, session.StateSuspended} {
		t.Run(string(state), func(t *testing.T) {
			var dials atomic.Int64
			s := forwardServer(leasesHeldBy("gw-b.00@127.0.0.1:1"), "gw-a.00@10.0.0.1:1", noDialClient(&dials))
			row := forwardRow()
			row.State = state
			ctx := idemmw.WithGate(WithForwarded(context.Background()))
			rec := httptest.NewRecorder()
			var n nativeRefusal
			if got := s.ForwardResponsesTurn(rec, responsesRequest(ctx, "{}"), row, []byte("{}"), n.refuse); got != ForwardNotDelivered {
				t.Fatalf("outcome = %v, want ForwardNotDelivered", got)
			}
			if n.calls.Load() != 1 || n.states[0] != string(state) {
				t.Errorf("refuse calls = %d with %v, want one with %q", n.calls.Load(), n.states, state)
			}
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"current_state":"`+string(state)+`"`) {
				t.Errorf("response = %d %s, want the renderer's native 409", rec.Code, rec.Body.String())
			}
			if !idemmw.NotStored(ctx) {
				t.Error("the refusal was left storable (§11.5)")
			}
			if dials.Load() != 0 {
				t.Errorf("dials = %d, want 0: a forwarded request is never forwarded again", dials.Load())
			}
		})
	}
}

// spec: 29.3 (Interactive message send) Forwarding rule 5, 15 (Open Responses conversation sessions)
func TestForwardedResponsesTurnAtTheHolderRunsLocally_spec_29_3(t *testing.T) {
	for _, state := range []session.State{session.StateRunning, session.StateSuspended} {
		t.Run(string(state), func(t *testing.T) {
			var dials atomic.Int64
			s := forwardServer(leasesHeldBy("gw-a.00@10.0.0.1:1"), "gw-a.00@10.0.0.1:1", noDialClient(&dials))
			row := forwardRow()
			row.State = state
			rec := httptest.NewRecorder()
			var n nativeRefusal
			if got := s.ForwardResponsesTurn(rec, responsesRequest(WithForwarded(context.Background()), "{}"), row, []byte("{}"), n.refuse); got != ForwardLocal {
				t.Fatalf("outcome = %v, want ForwardLocal", got)
			}
			if dials.Load() != 0 || n.calls.Load() != 0 || rec.Body.Len() != 0 {
				t.Errorf("dials %d, refusals %d, body %q; want a local outcome with nothing written", dials.Load(), n.calls.Load(), rec.Body.String())
			}
		})
	}
}

// spec: 29.3 (Interactive message send) Forwarding rules 1, 6, and 7, 11.5 (Idempotency), 15 (Open Responses conversation sessions)
func TestUnforwardedResponsesTurn_spec_29_3(t *testing.T) {
	m := gatewaypki.Mint(t)
	t.Run("at the holder runs locally without a dial", func(t *testing.T) {
		var dials atomic.Int64
		s := forwardServer(leasesHeldBy("gw-a.00@10.0.0.1:1"), "gw-a.00@10.0.0.1:1", noDialClient(&dials))
		var n nativeRefusal
		if got := s.ForwardResponsesTurn(httptest.NewRecorder(), responsesRequest(context.Background(), "{}"), forwardRow(), []byte("{}"), n.refuse); got != ForwardLocal {
			t.Fatalf("outcome = %v, want ForwardLocal", got)
		}
		if dials.Load() != 0 || n.calls.Load() != 0 {
			t.Errorf("dials %d, refusals %d; want 0 and 0", dials.Load(), n.calls.Load())
		}
	})
	t.Run("a row outside running runs locally without reading the lease", func(t *testing.T) {
		leases := leasesHeldBy("gw-b.00@127.0.0.1:1")
		s := forwardServer(leases, "gw-a.00@10.0.0.1:1", nil)
		row := forwardRow()
		row.State = session.StateSuspended
		var n nativeRefusal
		if got := s.ForwardResponsesTurn(httptest.NewRecorder(), responsesRequest(context.Background(), "{}"), row, []byte("{}"), n.refuse); got != ForwardLocal {
			t.Fatalf("outcome = %v, want ForwardLocal", got)
		}
		if leases.gets.Load() != 0 {
			t.Errorf("lease reads = %d, want 0", leases.gets.Load())
		}
	})
	t.Run("an unreachable holder is refused through the renderer", func(t *testing.T) {
		s := forwardServer(leasesHeldBy("gw-b.00@"+closedAddr(t)), "gw-a.00@10.0.0.1:1", testForwardClient(t, m, time.Second))
		ctx := idemmw.WithGate(context.Background())
		rec := httptest.NewRecorder()
		var n nativeRefusal
		if got := s.ForwardResponsesTurn(rec, responsesRequest(ctx, "{}"), forwardRow(), []byte("{}"), n.refuse); got != ForwardNotDelivered {
			t.Fatalf("outcome = %v, want ForwardNotDelivered", got)
		}
		if n.calls.Load() != 1 || rec.Code != http.StatusConflict || strings.Contains(rec.Body.String(), `"details"`) {
			t.Errorf("refusals %d, response %d %s; want one native refusal and no Lenny envelope", n.calls.Load(), rec.Code, rec.Body.String())
		}
		if !idemmw.NotStored(ctx) {
			t.Error("the refusal was left storable (§11.5)")
		}
	})
	t.Run("the coordinator receives the already-read body", func(t *testing.T) {
		got := make(chan capturedRequest, 1)
		addr := startTLSServer(t, serverTLSFor(t, m, gatewayLeaf(m)), recordingCoordinator(got))
		s := forwardServer(leasesHeldBy("gw-b.00@"+addr), "gw-a.00@10.0.0.1:1", testForwardClient(t, m, time.Second))
		body := `{"input":"hi","previous_response_id":"sess-1.1"}`
		r := responsesRequest(context.Background(), "")
		r.ContentLength = int64(len(body))
		rec := httptest.NewRecorder()
		var n nativeRefusal
		if out := s.ForwardResponsesTurn(rec, r, forwardRow(), []byte(body), n.refuse); out != ForwardDelivered {
			t.Fatalf("outcome = %v, want ForwardDelivered", out)
		}
		req := <-got
		if req.method != http.MethodPost || req.uri != "/v1/responses" || req.body != body {
			t.Errorf("coordinator received %s %s %q, want POST /v1/responses with the read body", req.method, req.uri, req.body)
		}
		if rec.Code != http.StatusCreated || n.calls.Load() != 0 {
			t.Errorf("caller received %d with %d refusals, want the coordinator's response and no refusal", rec.Code, n.calls.Load())
		}
	})
	t.Run("a coordinator that closes before answering is unknown and refused", func(t *testing.T) {
		addr := startTLSServer(t, serverTLSFor(t, m, gatewayLeaf(m)), closingCoordinator())
		s := forwardServer(leasesHeldBy("gw-b.00@"+addr), "gw-a.00@10.0.0.1:1", testForwardClient(t, m, time.Second))
		ctx := idemmw.WithGate(context.Background())
		rec := httptest.NewRecorder()
		var n nativeRefusal
		if got := s.ForwardResponsesTurn(rec, responsesRequest(ctx, "{}"), forwardRow(), []byte("{}"), n.refuse); got != ForwardUnknown {
			t.Fatalf("outcome = %v, want ForwardUnknown", got)
		}
		if n.calls.Load() != 1 || rec.Code != http.StatusConflict {
			t.Errorf("refusals %d, status %d; want one native 409", n.calls.Load(), rec.Code)
		}
		if !idemmw.NotStored(ctx) {
			t.Error("an unknown outcome was left storable (§11.5)")
		}
	})
	t.Run("a body failing after the relayed status is unknown with no refusal", func(t *testing.T) {
		addr := startTLSServer(t, serverTLSFor(t, m, gatewayLeaf(m)), midBodyResetter())
		s := forwardServer(leasesHeldBy("gw-b.00@"+addr), "gw-a.00@10.0.0.1:1", testForwardClient(t, m, time.Second))
		ctx := idemmw.WithGate(context.Background())
		rec := httptest.NewRecorder()
		var n nativeRefusal
		if got := s.ForwardResponsesTurn(rec, responsesRequest(ctx, "{}"), forwardRow(), []byte("{}"), n.refuse); got != ForwardUnknown {
			t.Fatalf("outcome = %v, want ForwardUnknown", got)
		}
		if n.calls.Load() != 0 {
			t.Errorf("refuse calls = %d after the status was relayed, want 0", n.calls.Load())
		}
		if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "TARGET_NOT_READY") {
			t.Errorf("response = %d %q, want the relayed 200 left truncated with no second status", rec.Code, rec.Body.String())
		}
		if !idemmw.NotStored(ctx) {
			t.Error("a truncated response was left storable (§11.5)")
		}
	})
}

// midBodyResetter answers 200 with a declared length it does not send, and
// resets the connection part-way through the body.
func midBodyResetter() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("0123456789"))
		conn, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		if tc, ok := conn.(*tls.Conn); ok {
			if raw, ok := tc.NetConn().(*net.TCPConn); ok {
				_ = raw.SetLinger(0)
			}
		}
		_ = conn.Close()
	})
}

// TestOtherKindsKeepTheStateGateAheadOfTheForwardedCheck_spec_29_3 pins that
// only the Open Responses kind moves the rule-5 check ahead of the state
// gate: a forwarded interrupt on a suspended row at a non-holder still runs
// locally.
//
// spec: 29.3 (Interactive message send) Forwarding rule 5
func TestOtherKindsKeepTheStateGateAheadOfTheForwardedCheck_spec_29_3(t *testing.T) {
	s := forwardServer(leasesHeldBy("gw-b.00@127.0.0.1:1"), "gw-a.00@10.0.0.1:1", nil)
	row := forwardRow()
	row.State = session.StateSuspended
	if got := s.forwardToHolder(httptest.NewRecorder(), clientRequest(WithForwarded(context.Background()), "{}"), row, ForwardKindInterrupt, nil); got != ForwardLocal {
		t.Fatalf("outcome = %v, want ForwardLocal", got)
	}
}

// TestResponsesTurnForwardStatesAreRunningOnly_spec_29_3 pins the kind's
// forward states to running, the only state a warm turn is served in.
//
// spec: 29.3 (Interactive message send) Forwarding, 15 (Open Responses conversation sessions)
func TestResponsesTurnForwardStatesAreRunningOnly_spec_29_3(t *testing.T) {
	got := forwardStates[ForwardKindResponsesTurn]
	if len(got) != 1 || !got[session.StateRunning] {
		t.Errorf("forward states = %v, want {running}", got)
	}
	s := forwardServer(nil, "gw-a", nil)
	for _, st := range []session.State{session.StateInputRequired, session.StateSuspended, session.StateResumePending, session.StateCompleted} {
		if s.forwardGated(sessionstore.Session{UserID: testOwner, State: st, PodAssignment: "pod-1"}, ForwardKindResponsesTurn) {
			t.Errorf("state %q is gated, want only running", st)
		}
	}
}
