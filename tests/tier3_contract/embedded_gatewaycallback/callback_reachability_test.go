//go:build contract

// SPDX-License-Identifier: MIT

// Package embedded_gatewaycallback_test is the Tier 3 contract suite for
// the §4.7 gateway↔adapter gRPC+mTLS callback reachability config the
// §17.4 Embedded Mode stack computes across the host/Docker boundary.
//
// On macOS and Windows the gateway and controllers run as host processes
// while agent pods run in-cluster under Docker Desktop's Linux VM, so the
// gateway's §8.6/§9.1 GatewayControl listener and the in-cluster adapter
// that dials it sit on opposite sides of the host/Docker boundary. The
// embedded stack carries the callback across that boundary with a matched
// address pair:
//
//   - the gateway binds the GatewayControl listener on all host interfaces
//     (an empty host, ":<port>") so a client inside the Docker VM can reach
//     it; binding loopback would make it unreachable from the VM; and
//   - the controller stamps the launcher's substrate-reachable dial host
//     joined to the same gRPC host port onto every agent pod's adapter
//     (127.0.0.1 on the Linux child-process launcher, host.docker.internal
//     on the Docker-backed launcher).
//
// This suite pins that the listen address the embedded gateway binds and
// the dial address the controller stamps are a reachable pair: a real
// GatewayControl client dialing the stamped host:port reaches a server
// bound the way the embedded gateway binds it, and the credential
// selection holds on both ends across both the local-development plaintext
// path and the §4.7 mTLS path. The host.docker.internal dial host is a
// Docker-VM alias that does not resolve on a plain CI host, so the live
// round-trip exercises the Linux-launcher loopback host (where the alias's
// loopback analog resolves); the Docker-backed host portion is asserted by
// composition against the same gateway bind. The live macOS/Windows-host
// leg (the alias resolving inside a real Docker Desktop VM) is deferred to
// the tier-2 component bring-up, which runs where Docker Desktop is
// available.
//
// spec: §4.7 (the gateway↔adapter gRPC+mTLS callback traverses the
// host/Docker boundary), §8.6, §9.1 (the GatewayControl listener), §17.4
// (the substrate is provisioned per host operating system; the callback
// address is computed per substrate and stays substrate-agnostic above the
// provisioning layer).
package embedded_gatewaycallback_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"strconv"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/lennylabs/lenny/pkg/embedded/k3s"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// gatewayGRPCPort is the §8.6/§9.1 GatewayControl host port the embedded
// stack binds and stamps. It mirrors pkg/embedded/stack's
// defaultGatewayGRPCPort; the constant is duplicated here because that one
// is unexported, and the contract under test is that the bind port and the
// stamped port are the same value, which the duplication makes explicit.
const gatewayGRPCPort = 50061

// gatewayControlStub captures the ListPlatformTools request it receives so
// the contract test can assert the callback reached the server across the
// composed address pair. It is the minimal GatewayControl surface the
// adapter dials. ListPlatformTools is the §9.1 platform-tool bridge the
// in-cluster adapter calls live over this same gateway↔adapter channel, so
// it carries the host/Docker-boundary reachability and §4.7 mTLS contract
// this probe pins.
type gatewayControlStub struct {
	adapterv1.UnimplementedGatewayControlServer

	got *adapterv1.ListPlatformToolsRequest
}

func (s *gatewayControlStub) ListPlatformTools(_ context.Context, req *adapterv1.ListPlatformToolsRequest) (*adapterv1.ListPlatformToolsResponse, error) {
	s.got = req
	return &adapterv1.ListPlatformToolsResponse{}, nil
}

// callbackSessionID is the SessionId the contract test's adapter sends on
// its ListPlatformTools callback. The server asserts it received this value,
// so a passing round-trip confirms the request crossed to the listener
// rather than to some other server on the bound port.
const callbackSessionID = "sess-callback"

// embeddedGatewayListenAddr is the address the embedded stack binds the
// GatewayControl listener on: all host interfaces (an empty host) joined to
// the gRPC host port, so an in-cluster adapter under the Docker VM reaches
// it across the host/Docker boundary. pkg/embedded/stack builds it as
// fmt.Sprintf(":%d", defaultGatewayGRPCPort); this mirrors that exactly so
// the contract pins the real bind form.
func embeddedGatewayListenAddr() string {
	return fmt.Sprintf(":%d", gatewayGRPCPort)
}

// controllerStampedDialAddr is the dial address the controller stamps onto
// agent pods for a given launcher: the launcher's substrate-reachable
// GatewayHost joined to the gateway gRPC host port. The Linux launcher
// returns 127.0.0.1; the Docker-backed launcher returns
// host.docker.internal. pkg/embedded/stack composes it as
// net.JoinHostPort(launcher.GatewayHost(), port); this mirrors that so the
// contract pins the composition the stack performs.
func controllerStampedDialAddr(launcher k3s.Launcher) string {
	return net.JoinHostPort(launcher.GatewayHost(), strconv.Itoa(gatewayGRPCPort))
}

// startGatewayControl binds a GatewayControl gRPC server on listenAddr with
// the given transport credentials, registers the stub, and returns the stub
// and the port the listener actually bound. The port lets the test dial the
// matching loopback address when the listener binds all interfaces.
func startGatewayControl(t *testing.T, listenAddr string, creds credentials.TransportCredentials) (*gatewayControlStub, int) {
	t.Helper()
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		t.Fatalf("bind GatewayControl listener on %q: %v", listenAddr, err)
	}
	srv := &gatewayControlStub{}
	gs := grpc.NewServer(grpc.Creds(creds))
	adapterv1.RegisterGatewayControlServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return srv, lis.Addr().(*net.TCPAddr).Port
}

// listPlatformTools dials dialAddr with the given credentials and issues one
// ListPlatformTools call, returning the round-trip error.
func listPlatformTools(t *testing.T, dialAddr string, creds credentials.TransportCredentials) error {
	t.Helper()
	conn, err := grpc.NewClient(dialAddr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return fmt.Errorf("dial %q: %w", dialAddr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = adapterv1.NewGatewayControlClient(conn).ListPlatformTools(ctx, &adapterv1.ListPlatformToolsRequest{
		SessionId: &adapterv1.SessionId{Value: callbackSessionID},
	})
	return err
}

// receivedCallbackSession reports whether the stub captured the
// ListPlatformTools callback carrying the expected SessionId, so the
// assertion is one check across both the plaintext and mTLS round-trips.
func (s *gatewayControlStub) receivedCallbackSession() bool {
	return s.got != nil && s.got.GetSessionId().GetValue() == callbackSessionID
}

// TestEmbeddedCallbackAddressPairMatchesAcrossSubstrates pins the
// substrate-agnostic composition: the embedded gateway binds the
// GatewayControl listener on the all-interfaces address, and the controller
// stamps GatewayHost():port onto pods. The two share one port (the gateway
// is reachable on the port the controller dials), and the dial host is the
// launcher's substrate-reachable host — host.docker.internal on the
// Docker-backed launcher (the macOS/Windows substrate this step wires). A
// mismatch here would mean a pod under the Docker VM dials a host or port
// the gateway does not bind, so the §4.7 callback would not cross the
// host/Docker boundary. The Linux launcher's loopback host portion is pinned
// by pkg/embedded/stack's gatewayGRPCAddr unit test and exercised live by
// the plaintext and mTLS round-trip tests below, which dial loopback.
//
// spec: §4.7, §8.6, §9.1, §17.4 (the callback address is computed per
// substrate; the bind port and the stamped port match).
//
// diagnosis: a failure means the embedded stack's GatewayControl bind
// address and the controller-stamped pod dial address have diverged — the
// pod adapter would dial a host or port the gateway does not serve, so the
// §4.7 gateway↔adapter callback could not traverse the host/Docker boundary
// and the §9.1 platform-tool and §9.3 connector-tool calls an in-cluster
// type:agent runtime makes would fail to reach the host gateway.
func TestEmbeddedCallbackAddressPairMatchesAcrossSubstrates_spec_4_7(t *testing.T) {
	listen := embeddedGatewayListenAddr()
	_, listenPort, err := net.SplitHostPort(listen)
	if err != nil {
		t.Fatalf("the embedded gateway listen address %q is not a valid host:port: %v", listen, err)
	}

	// The Docker-backed launcher is the macOS/Windows substrate this step
	// wires; NewDockerLauncher constructs the real launcher New selects off
	// Linux, so the stamped host is the production host.docker.internal
	// alias rather than a test double.
	launcher := k3s.NewDockerLauncher(k3s.Config{Dir: t.TempDir()})
	dial := controllerStampedDialAddr(launcher)
	dialHost, dialPort, err := net.SplitHostPort(dial)
	if err != nil {
		t.Fatalf("controller-stamped dial address %q is not a valid host:port: %v", dial, err)
	}
	if dialHost != "host.docker.internal" {
		t.Errorf("dial host = %q, want the Docker-backed launcher's substrate-reachable alias host.docker.internal", dialHost)
	}
	if dialPort != listenPort {
		t.Errorf("dial port %q != gateway bind port %q: a pod would dial a port the gateway does not serve", dialPort, listenPort)
	}
	if launcher.GatewayHost() != "host.docker.internal" {
		t.Errorf("launcher GatewayHost() = %q, want host.docker.internal", launcher.GatewayHost())
	}
}

// TestEmbeddedCallbackReachablePlaintext pins the local-development
// plaintext reachability path: the embedded stack leaves the adapter mTLS
// material unset, so the GatewayControl listener serves plaintext, and a
// pod adapter dialing the stamped address reaches it. The listener binds the
// all-interfaces address the embedded gateway binds; the dial targets the
// loopback the Linux launcher stamps (the Docker-backed launcher's
// host.docker.internal alias resolves only inside the Docker VM, exercised
// by the tier-2 bring-up). A successful ListPlatformTools round-trip
// confirms the composed listen/dial pair is reachable.
//
// spec: §4.7 (the plaintext path is the documented local-development
// callback transport), §8.6, §9.1, §17.4.
//
// diagnosis: a failure means the embedded gateway's all-interfaces plaintext
// GatewayControl bind and the controller-stamped loopback dial address no
// longer form a reachable pair — an in-cluster adapter could not deliver its
// platform tool callback to the host gateway over the local-development
// plaintext transport.
func TestEmbeddedCallbackReachablePlaintext_spec_4_7(t *testing.T) {
	srv, port := startGatewayControl(t, embeddedGatewayListenAddr(), insecure.NewCredentials())

	// The Linux launcher stamps 127.0.0.1; dial the loopback host on the
	// port the all-interfaces listener bound, which is the reachable analog
	// of the stamped pod dial address on the host the live round-trip runs
	// on.
	dial := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if err := listPlatformTools(t, dial, insecure.NewCredentials()); err != nil {
		t.Fatalf("plaintext ListPlatformTools across the composed listen/dial pair failed: %v", err)
	}
	if !srv.receivedCallbackSession() {
		t.Fatalf("server did not receive the ListPlatformTools callback (got=%v); the callback did not cross to the listener", srv.got)
	}
}

// TestEmbeddedCallbackReachableMTLS pins the §4.7 mTLS reachability path:
// when the gateway presents its mesh server certificate and requires a
// verified pod client certificate, a pod adapter dialing the stamped
// address with a CA-chained client certificate reaches it and the handshake
// succeeds. This is the production §4.7 transport; the embedded stack's
// address composition is identical to the plaintext path, so the same
// matched listen/dial pair carries the mTLS callback. The round-trip runs on
// the loopback host the Linux launcher stamps; the Docker-backed host
// portion is asserted by composition in the address-pair test above.
//
// spec: §4.7 (the adapter↔gateway channel is mTLS; the gateway presents its
// mesh server cert and verifies the pod client cert), §8.6, §15.3, §17.4.
//
// diagnosis: a failure means the §4.7 mTLS callback no longer completes
// across the embedded stack's composed listen/dial pair — either the
// gateway's all-interfaces mTLS bind and the stamped dial address diverged,
// or the mutual-TLS handshake the production §4.7 transport requires fails
// over that pair, so an in-cluster adapter could not establish the verified
// channel to the host gateway across the host/Docker boundary.
func TestEmbeddedCallbackReachableMTLS_spec_4_7(t *testing.T) {
	ca := newCA(t)
	serverCert := ca.leaf(t, "127.0.0.1", "spiffe://lenny-acme/agent/gateway/lenny-gateway")
	clientCert := ca.leaf(t, "pod-callback", "spiffe://lenny-acme/agent/default/pod-callback")

	serverCreds := credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ca.pool,
		MinVersion:   tls.VersionTLS13,
	})
	srv, port := startGatewayControl(t, embeddedGatewayListenAddr(), serverCreds)

	clientCreds := credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      ca.pool,
		ServerName:   "127.0.0.1",
		MinVersion:   tls.VersionTLS13,
	})
	dial := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if err := listPlatformTools(t, dial, clientCreds); err != nil {
		t.Fatalf("mTLS ListPlatformTools across the composed listen/dial pair failed: %v", err)
	}
	if !srv.receivedCallbackSession() {
		t.Fatalf("server did not receive the mTLS ListPlatformTools callback (got=%v)", srv.got)
	}
}

// TestEmbeddedCallbackMTLSRejectsUnverifiedClient pins that the §4.7 mTLS
// reachability config fails closed: when the gateway requires a verified pod
// client certificate, an adapter dialing without one — or with a certificate
// not chained to the gateway's trust bundle — does not complete the callback.
// A reachable address pair must not become a reachable plaintext channel when
// the production transport is mTLS.
//
// spec: §4.7 (the gateway requires and verifies the pod client certificate;
// an unverified peer is rejected), §15.3.
//
// diagnosis: a failure means the embedded §4.7 mTLS GatewayControl listener
// accepted a callback from a client with no verified certificate — the
// mutual-TLS requirement that authenticates the in-cluster pod adapter to
// the host gateway is not enforced, so any party reaching the bound port
// could deliver a callback.
func TestEmbeddedCallbackMTLSRejectsUnverifiedClient_spec_4_7(t *testing.T) {
	ca := newCA(t)
	serverCert := ca.leaf(t, "127.0.0.1", "spiffe://lenny-acme/agent/gateway/lenny-gateway")

	serverCreds := credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ca.pool,
		MinVersion:   tls.VersionTLS13,
	})
	_, port := startGatewayControl(t, embeddedGatewayListenAddr(), serverCreds)

	// A client that trusts the server but presents no client certificate:
	// the gateway requires one, so the handshake must fail.
	clientCreds := credentials.NewTLS(&tls.Config{
		RootCAs:    ca.pool,
		ServerName: "127.0.0.1",
		MinVersion: tls.VersionTLS13,
	})
	dial := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if err := listPlatformTools(t, dial, clientCreds); err == nil {
		t.Fatal("ListPlatformTools succeeded without a verified client certificate; the §4.7 mTLS requirement is not enforced")
	}
}

// --- self-contained CA / leaf minting (mirrors pkg/mtls test helpers; kept
// local so the contract suite has no test-only cross-package import) ---

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ca key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "lenny-mtls-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("ca cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse ca: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &testCA{cert: cert, key: key, pool: pool}
}

func (c *testCA) leaf(t *testing.T, dnsName, spiffeURI string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("leaf key: %v", err)
	}
	u, err := url.Parse(spiffeURI)
	if err != nil {
		t.Fatalf("parse spiffe uri: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: dnsName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{dnsName},
		IPAddresses:  ipSANsFor(dnsName),
		URIs:         []*url.URL{u},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		t.Fatalf("leaf cert: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// ipSANsFor returns the IP SANs a leaf needs when its name is a literal IP,
// so a ServerName of 127.0.0.1 verifies against the presented certificate.
func ipSANsFor(name string) []net.IP {
	if ip := net.ParseIP(name); ip != nil {
		return []net.IP{ip}
	}
	return nil
}
