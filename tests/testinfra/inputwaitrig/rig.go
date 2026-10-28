// SPDX-License-Identifier: MIT

// Package inputwaitrig serves the GatewayControl platform-tool surface with
// the input-wait connection recovery installed, for tests of the
// lenny/request_input row writes and the recovery a replica runs when it
// accepts a pod process's LNK-GWCONTROL connection.
//
// A Server is one replica: its own input-wait registry, its own event bus,
// and its own re-arm generation, over a session store a test may share
// between servers. It installs the stats handler and the recovery
// interceptor in the order the gateway installs them, behind the same
// verified-peer gate, and dispatches CallPlatformTool through the same
// platformtools bridge and mcptools handlers the gateway runs. Each Dial is
// one connection, so a test can stand for a pod process's current connection,
// the connection it left, and a second pod.
//
// spec: §7.2 (Session state machine), §28.3 (Link register).
package inputwaitrig

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaycontrol/inputwaitrecovery"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaycontrol/platformtools"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/mtls/spiffe"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/gatewaypki"
)

// TrustDomain is the SPIFFE trust domain the rig's identities use.
const TrustDomain = "lenny.test"

// Options configures one Server.
type Options struct {
	// Store is the session store; servers standing for two replicas share
	// one.
	Store sessionstore.Store
	// PKI is the cluster CA and gateway leaf; nil serves plaintext.
	PKI *gatewaypki.Material
	// VerifyAgentURI installs the handshake SPIFFE verifier, as the gateway
	// does when a trust domain is configured. False leaves CA-only
	// verification, so every presented certificate reaches the recovery.
	VerifyAgentURI bool
	// RequestInputTimeout bounds each wait; zero uses one minute.
	RequestInputTimeout time.Duration
}

// Server is one replica's GatewayControl surface.
type Server struct {
	Addr  string
	Store sessionstore.Store
	Waits *inputwait.Registry
	Bus   *sessionevents.Bus
	pki   *gatewaypki.Material
	gs    *grpc.Server
	store *killableStore
}

// errKilled is what a killed replica's store answers.
var errKilled = errors.New("inputwaitrig: replica killed")

// killableStore fails every write once the replica is killed.
type killableStore struct {
	sessionstore.Store
	killed atomic.Bool
}

func (k *killableStore) Update(ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	if k.killed.Load() {
		return sessionstore.Session{}, errKilled
	}
	return k.Store.Update(ctx, tenantID, id, mutate)
}

// controlServer serves the platform-tool RPCs through the gateway bridge.
type controlServer struct {
	adapterv1.UnimplementedGatewayControlServer
	bridge *platformtools.Bridge
}

func (c *controlServer) ListPlatformTools(ctx context.Context, req *adapterv1.ListPlatformToolsRequest) (*adapterv1.ListPlatformToolsResponse, error) {
	tools, err := c.bridge.ListPlatformTools(ctx, req.GetSessionId().GetValue())
	if err != nil {
		return nil, err
	}
	return &adapterv1.ListPlatformToolsResponse{Tools: make([]*adapterv1.PlatformTool, 0, len(tools))}, nil
}

func (c *controlServer) CallPlatformTool(ctx context.Context, req *adapterv1.CallPlatformToolRequest) (*adapterv1.CallPlatformToolResponse, error) {
	res, isErr, err := c.bridge.CallPlatformTool(ctx, req.GetSessionId().GetValue(), req.GetToolName(), req.GetArguments())
	if err != nil {
		return nil, err
	}
	return &adapterv1.CallPlatformToolResponse{Result: res, IsError: isErr}, nil
}

// Start serves one replica on a loopback port until the test ends.
func Start(t testing.TB, opts Options) *Server {
	t.Helper()
	s := &Server{Store: opts.Store, Waits: inputwait.NewRegistry(), Bus: sessionevents.NewBus(0), pki: opts.PKI}
	// The replica writes through a store it can lose: after Kill every write
	// fails before committing, as a dead process writes nothing.
	s.store = &killableStore{Store: opts.Store}
	timeout := opts.RequestInputTimeout
	if timeout <= 0 {
		timeout = time.Minute
	}
	var ids atomic.Int64
	tools := mcp.NewServer()
	mcptools.Register(tools, mcptools.Deps{
		DevTenantFallback:   true,
		Store:               s.store,
		Executor:            executor.NewEchoExecutor(),
		Events:              s.Bus,
		InputWaits:          s.Waits,
		RequestInputTimeout: timeout,
		IDFunc:              func() string { return fmt.Sprintf("%p-%d", s, ids.Add(1)) },
		TenantID:            "acme",
	})
	recoverer := &inputwaitrecovery.Recoverer{
		Sessions: s.store,
		Writer:   inputwaitrecovery.Writer{Store: s.store, Waits: s.Waits, Events: s.Bus},
	}
	serverOpts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(
			leasecontrol.RequireVerifiedPeerInterceptor(opts.PKI != nil),
			recoverer.UnaryInterceptor(),
		),
		grpc.StatsHandler(inputwaitrecovery.NewStatsHandler(inputwaitrecovery.NewGeneration())),
	}
	if opts.PKI != nil {
		var mods []adapter.TLSConfigMod
		if opts.VerifyAgentURI {
			v := spiffe.AgentPeerVerifier{TrustDomain: TrustDomain}
			mods = append(mods, func(c *tls.Config) { c.VerifyPeerCertificate = v.VerifyPeerCertificate })
		}
		tlsOpt, err := adapter.TLSServerOption(opts.PKI.CertFile, opts.PKI.KeyFile, opts.PKI.CAFile, mods...)
		if err != nil {
			t.Fatalf("inputwaitrig: TLS option: %v", err)
		}
		serverOpts = append(serverOpts, tlsOpt)
	}
	s.gs = grpc.NewServer(serverOpts...)
	adapterv1.RegisterGatewayControlServer(s.gs, &controlServer{bridge: platformtools.New(tools, s.store)})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("inputwaitrig: listen: %v", err)
	}
	s.Addr = lis.Addr().String()
	go func() { _ = s.gs.Serve(lis) }()
	t.Cleanup(s.Stop)
	return s
}

// Stop closes the registry and stops the server gracefully, in the order
// the gateway's shutdown runs them. It is idempotent.
func (s *Server) Stop() {
	s.Waits.Close()
	s.gs.GracefulStop()
}

// Kill stops the server as a process exit does: the registry is not
// drained, no further write of the replica reaches the store, and every
// pending wait is lost with the server.
func (s *Server) Kill() {
	s.store.killed.Store(true)
	s.gs.Stop()
}

// AgentURI is the agent SPIFFE URI of pod in pool-a.
func AgentURI(pod string) *url.URL {
	return &url.URL{Scheme: "spiffe", Host: TrustDomain, Path: "/agent/pool-a/" + pod}
}

// InterceptorURI is an interceptor SPIFFE URI naming pod.
func InterceptorURI(pod string) *url.URL {
	return &url.URL{Scheme: "spiffe", Host: TrustDomain, Path: "/interceptor/lenny-system/" + pod}
}

// Dial opens a new connection to s presenting a client certificate with the
// given URI SANs; no URIs presents a certificate with no URI SAN. On a
// plaintext server it dials without TLS.
func (s *Server) Dial(t testing.TB, uris ...*url.URL) adapterv1.GatewayControlClient {
	t.Helper()
	return DialAddr(t, s.Addr, s.pki, uris...)
}

// DialAddr opens a new connection to the GatewayControl listener at addr,
// which may be a gateway process. With pki it presents a client certificate
// minted from pki's CA with the given URI SANs and verifies the gateway leaf;
// without it, it dials plaintext.
func DialAddr(t testing.TB, addr string, pki *gatewaypki.Material, uris ...*url.URL) adapterv1.GatewayControlClient {
	t.Helper()
	creds := insecure.NewCredentials()
	if pki != nil {
		leaf := pki.Leaf(t, nil, uris)
		cert, err := tls.LoadX509KeyPair(leaf.CertFile, leaf.KeyFile)
		if err != nil {
			t.Fatalf("inputwaitrig: load client leaf: %v", err)
		}
		creds = credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{cert},
			RootCAs:      pki.CAPool,
			ServerName:   gatewaycontrol.GatewayDNSName,
			MinVersion:   tls.VersionTLS12,
		})
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		t.Fatalf("inputwaitrig: dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return adapterv1.NewGatewayControlClient(conn)
}

// Ping sends a call carrying sessionID that returns at once, which runs the
// recovery when the connection is not recovered.
func Ping(ctx context.Context, c adapterv1.GatewayControlClient, sessionID string) error {
	_, err := c.ListPlatformTools(ctx, &adapterv1.ListPlatformToolsRequest{SessionId: &adapterv1.SessionId{Value: sessionID}})
	return err
}

// Result is one finished lenny/request_input call.
type Result struct {
	Resp *adapterv1.CallPlatformToolResponse
	Err  error
}

// RequestInput starts lenny/request_input for sessionID on c and returns the
// channel its result arrives on.
func RequestInput(ctx context.Context, c adapterv1.GatewayControlClient, sessionID, requestID string) <-chan Result {
	out := make(chan Result, 1)
	args, _ := json.Marshal(map[string]any{
		"requestId": requestID,
		"parts":     []map[string]any{{"type": "text", "inline": "continue?"}},
	})
	go func() {
		resp, err := c.CallPlatformTool(ctx, &adapterv1.CallPlatformToolRequest{
			SessionId: &adapterv1.SessionId{Value: sessionID},
			ToolName:  "lenny/request_input",
			Arguments: args,
		})
		out <- Result{resp, err}
	}()
	return out
}

// WaitPending blocks until (sessionID, requestID) is registered on s.
func (s *Server) WaitPending(t testing.TB, sessionID, requestID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.Waits.Pending(sessionID, requestID) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("inputwaitrig: %s/%s never became pending", sessionID, requestID)
}

// StatusChanges returns the states of the status_change events s's bus
// carries for sessionID, in publish order.
func (s *Server) StatusChanges(sessionID string) []string {
	var out []string
	for _, e := range s.Bus.History(sessionID, 0) {
		if e.Type != "status_change" {
			continue
		}
		var p struct {
			State string `json:"state"`
		}
		_ = json.Unmarshal([]byte(e.Data), &p)
		out = append(out, p.State)
	}
	return out
}

// Await returns the call's result or fails the test after five seconds.
func Await(t testing.TB, ch <-chan Result) Result {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("inputwaitrig: lenny/request_input did not return")
		return Result{}
	}
}
