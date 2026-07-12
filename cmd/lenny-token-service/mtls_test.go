// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
)

// stubTokenServer answers AssignCredentials with an empty OK response so
// the success case asserts a completed mTLS handshake followed by a
// dispatched RPC, rather than only a transport connect. The failing
// cases never reach a handler because the server aborts at the TLS
// handshake.
type stubTokenServer struct {
	tokensv1.UnimplementedTokenServiceServer
}

func (stubTokenServer) AssignCredentials(context.Context, *tokensv1.AssignCredentialsRequest) (*tokensv1.AssignCredentialsResponse, error) {
	return &tokensv1.AssignCredentialsResponse{}, nil
}

// TestTokenServiceGRPCRequiresClientMTLS exercises the §4.3 / §13.3
// gateway-to-Token-Service trust boundary end to end: it starts a gRPC
// server with the production tokenServiceCreds transport credentials
// (tls.RequireAndVerifyClientCert) and drives three client dials. A
// no-cert dial and a dial with a cert signed by an unpinned CA are
// rejected at the handshake; only a client cert that chains to the CA
// the server pins reaches the RPC.
//
// spec: §4.3 — "Gateway replicas call the Token Service over mTLS —
// they cannot directly decrypt stored tokens" and "Each gateway replica
// has a distinct mTLS identity so compromise of one is attributable and
// revocable independently." TESTING §12.9.2 requires that plaintext
// connections to the gateway-to-token-service gRPC link are rejected and
// that the mTLS handshake requires both certificates.
//
// diagnosis: the Token Service gRPC listener enforces client identity
// via tls.RequireAndVerifyClientCert in tokenServiceCreds (main.go). A
// failure here means that enforcement regressed — the server accepts a
// caller with no client certificate or one signed by a CA it does not
// pin — so any workload that can reach the port could cross the
// credential-assignment trust boundary without a valid gateway mTLS
// identity.
func TestTokenServiceGRPCRequiresClientMTLS(t *testing.T) {
	dir := t.TempDir()
	serverCA, serverCAKey := generateTestCA(t)
	serverLeaf, serverLeafKey := generateTestLeafCert(t, serverCA, serverCAKey, "lenny-token-service.lenny-system.svc")

	caPath := filepath.Join(dir, "ca.crt")
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	writeTestFile(t, caPath, serverCA)
	writeTestFile(t, certPath, serverLeaf)
	writeTestFile(t, keyPath, serverLeafKey)

	// Build the server's transport credentials through the exact
	// production path so the test pins the real ClientAuth setting rather
	// than a hand-rebuilt tls.Config.
	creds, err := tokenServiceCreds(certPath, keyPath, caPath)
	if err != nil {
		t.Fatalf("tokenServiceCreds: %v", err)
	}
	if creds == nil {
		t.Fatal("tokenServiceCreds returned nil for a complete TLS config")
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer(grpc.Creds(creds))
	tokensv1.RegisterTokenServiceServer(srv, stubTokenServer{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	addr := lis.Addr().String()

	const serverName = "lenny-token-service.lenny-system.svc"

	// serverRoots trusts the server's own CA so client-side verification
	// of the server certificate is never the reason a dial fails; the
	// only variable under test is the client identity the server demands.
	serverRoots := x509.NewCertPool()
	if !serverRoots.AppendCertsFromPEM(serverCA) {
		t.Fatal("append server CA to client roots")
	}

	// A client cert signed by the same CA the server pins is a valid
	// gateway identity.
	clientLeaf, clientLeafKey := generateTestLeafCert(t, serverCA, serverCAKey, "lenny-gateway.lenny-system.svc")
	validPair, err := tls.X509KeyPair(clientLeaf, clientLeafKey)
	if err != nil {
		t.Fatalf("valid client keypair: %v", err)
	}

	// A client cert signed by an unrelated CA does not chain to the
	// server's ClientCAs and must be rejected at the handshake.
	otherCA, otherCAKey := generateTestCA(t)
	wrongLeaf, wrongLeafKey := generateTestLeafCert(t, otherCA, otherCAKey, "lenny-gateway.lenny-system.svc")
	wrongPair, err := tls.X509KeyPair(wrongLeaf, wrongLeafKey)
	if err != nil {
		t.Fatalf("wrong-CA client keypair: %v", err)
	}

	cases := []struct {
		name       string
		clientCert []tls.Certificate
		wantOK     bool
	}{
		{name: "valid CA-signed client cert", clientCert: []tls.Certificate{validPair}, wantOK: true},
		{name: "no client cert", clientCert: nil, wantOK: false},
		{name: "client cert from an unpinned CA", clientCert: []tls.Certificate{wrongPair}, wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clientTLS := &tls.Config{
				ServerName:   serverName,
				RootCAs:      serverRoots,
				Certificates: tc.clientCert,
				MinVersion:   tls.VersionTLS13,
			}
			conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)))
			if err != nil {
				t.Fatalf("grpc.NewClient: %v", err)
			}
			defer conn.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// Unary RPCs are fail-fast by default, so a rejected handshake
			// surfaces as an error well before the context deadline.
			_, rpcErr := tokensv1.NewTokenServiceClient(conn).AssignCredentials(ctx, &tokensv1.AssignCredentialsRequest{
				TenantId:     "acme",
				SessionId:    "ses-1",
				PoolIds:      []string{"openai-default"},
				PodSpiffeUri: "spiffe://lenny/agent/acme/ses-1",
			})
			if tc.wantOK {
				if rpcErr != nil {
					t.Fatalf("valid mTLS client: AssignCredentials returned %v; want the handshake to complete and the RPC to succeed", rpcErr)
				}
				return
			}
			if rpcErr == nil {
				t.Fatalf("%s: AssignCredentials succeeded; want the server to reject the mTLS handshake", tc.name)
			}
		})
	}
}

// writeTestFile writes b to path with owner-only permissions, failing the
// test on error.
func writeTestFile(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
