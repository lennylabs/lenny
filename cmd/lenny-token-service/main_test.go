// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
)

// spec: 4.3 (§4.3 / §12.2.4 Token Service gRPC surface — the binary's
// --grpc-addr flag exposes the lenny.tokenservice.v1.TokenService RPCs)
// diagnosis: a smoke test that builds cmd/lenny-token-service, starts
// the binary against an ephemeral gRPC port, and dials the
// AssignCredentials RPC. With no credential pools registered, the call
// returns NotFound for the requested pool, which is the documented
// fail-fast behavior in pkg/tokenservice/grpc.go.
func TestBinaryServesGRPC(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain not on PATH: %v", err)
	}

	bin := filepath.Join(t.TempDir(), "lenny-token-service")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	// Bind ephemeral ports for both surfaces. The HTTP token-exchange
	// surface is irrelevant to this test but the binary still binds it.
	httpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("http listen: %v", err)
	}
	httpAddr := httpLis.Addr().String()
	_ = httpLis.Close()
	grpcLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("grpc listen: %v", err)
	}
	grpcAddr := grpcLis.Addr().String()
	_ = grpcLis.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(
		ctx, bin,
		"--addr="+httpAddr,
		"--grpc-addr="+grpcAddr,
		"--issuer=https://test.local/token",
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start binary: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
	})

	// Poll until the gRPC port accepts a TCP connection. grpc.NewClient
	// is lazy and would not surface a server-not-ready state on the
	// RPC, so a direct TCP dial is the readiness signal.
	deadline := time.Now().Add(5 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		probe, err := net.DialTimeout("tcp", grpcAddr, 250*time.Millisecond)
		if err == nil {
			_ = probe.Close()
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		t.Fatal("token-service gRPC port did not accept a TCP connection within 5s")
	}
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	defer conn.Close()

	client := tokensv1.NewTokenServiceClient(conn)
	rpcCtx, rpcCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer rpcCancel()
	_, err = client.AssignCredentials(rpcCtx, &tokensv1.AssignCredentialsRequest{
		TenantId:     "acme",
		SessionId:    "ses-1",
		PoolIds:      []string{"openai-default"},
		PodSpiffeUri: "spiffe://lenny/agent/acme/ses-1",
	})
	if err == nil {
		t.Fatal("AssignCredentials with no registered pool returned nil error; want NotFound")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("AssignCredentials error is not a gRPC status: %v", err)
	}
	if st.Code() != codes.NotFound {
		t.Errorf("AssignCredentials code = %s, want NotFound (no pool registered)", st.Code())
	}
}

// spec: §4.3 line 195 — tokenServiceCreds returns nil when all three
// TLS paths are empty (dev mode), and an error when only some are set.
// The §4.3 trust boundary requires mTLS in production, but the dev
// path must still build the binary for local tests.
func TestTokenServiceCredsPlaintextDevMode(t *testing.T) {
	creds, err := tokenServiceCreds("", "", "")
	if err != nil {
		t.Fatalf("tokenServiceCreds(\"\", \"\", \"\"): %v", err)
	}
	if creds != nil {
		t.Errorf("dev-mode creds = %v, want nil", creds)
	}
}

// spec: §4.3 line 195 — tokenServiceCreds rejects a partial TLS
// configuration (any one of cert/key/ca set requires all three).
func TestTokenServiceCredsRejectsPartialConfig(t *testing.T) {
	for i, args := range [][3]string{
		{"/tmp/cert", "", ""},
		{"", "/tmp/key", ""},
		{"", "", "/tmp/ca"},
		{"/tmp/cert", "/tmp/key", ""},
	} {
		if _, err := tokenServiceCreds(args[0], args[1], args[2]); err == nil {
			t.Errorf("case %d: tokenServiceCreds accepted a partial config %v", i, args)
		}
	}
}

// spec: §4.3 line 195 — tokenServiceCreds with a complete TLS config
// returns mTLS server credentials that require and verify the client
// cert (tls.RequireAndVerifyClientCert).
func TestTokenServiceCredsBuildsMTLS(t *testing.T) {
	dir := t.TempDir()
	caCert, caKey := generateTestCA(t)
	leafCert, leafKey := generateTestLeafCert(t, caCert, caKey, "lenny-token-service.lenny-system.svc")
	caPath := filepath.Join(dir, "ca.crt")
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	if err := os.WriteFile(caPath, caCert, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, leafCert, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, leafKey, 0o600); err != nil {
		t.Fatal(err)
	}
	creds, err := tokenServiceCreds(certPath, keyPath, caPath)
	if err != nil {
		t.Fatalf("tokenServiceCreds: %v", err)
	}
	if creds == nil {
		t.Fatal("expected non-nil creds for a complete TLS config")
	}
}

// generateTestCA returns a CA cert+key suitable for the cred-creds
// tests above. The CA's BasicConstraints set isCA=true so a leaf
// signed by it chains correctly.
func generateTestCA(t *testing.T) ([]byte, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), key
}

// generateTestLeafCert signs a leaf cert/key pair with caKey for the
// given commonName.
func generateTestLeafCert(t *testing.T, caPEM []byte, caKey *ecdsa.PrivateKey, commonName string) ([]byte, []byte) {
	t.Helper()
	block, _ := pem.Decode(caPEM)
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: commonName},
		DNSNames:     []string{commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	leafKeyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		t.Fatal(err)
	}
	leafKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: leafKeyDER})
	return leafPEM, leafKeyPEM
}

// spec: 4.0 (line 13: "Every subsystem that emits operational events
// depends on this package."). The Token Service binary constructs an
// EventEmitter at startup so future emit sites (credential rotation,
// revocation) do not require re-threading the dependency through main.
// The log line emitted on startup is the smoke-test signal: it confirms
// the emitter wiring ran and reports whether the local-only or Redis
// stream destination is active.
// diagnosis: a regression in main.go that removes the EventEmitter
// construction (or moves it behind an unrelated flag) drops the
// "§4.0 EventEmitter ready" log line; this test fails fast so a
// reviewer notices before the binary ships without the dependency.
func TestBinaryEmitsEventEmitterReadyLog(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain not on PATH: %v", err)
	}

	bin := filepath.Join(t.TempDir(), "lenny-token-service")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	httpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("http listen: %v", err)
	}
	httpAddr := httpLis.Addr().String()
	_ = httpLis.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(
		ctx, bin,
		"--addr="+httpAddr,
		"--issuer=https://test.local/token",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start binary: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
	})

	// Poll the binary's log output until the §4.0 emitter-ready line
	// appears. The line is logged before ListenAndServe, so a short
	// poll window suffices.
	deadline := time.Now().Add(5 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		if strings.Contains(stderr.String(), "§4.0 EventEmitter ready") {
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("token-service did not log §4.0 EventEmitter ready within 5s; stderr=%q", stderr.String())
	}
	// In the no-Redis path the log reports redis=false so the operator
	// sees the local-only delivery mode.
	if !strings.Contains(stderr.String(), "redis=false") {
		t.Errorf("token-service §4.0 emitter log did not report redis=false; stderr=%q", stderr.String())
	}
}
