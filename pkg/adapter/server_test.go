// SPDX-License-Identifier: MIT

package adapter_test

import (
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
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

func TestNegotiateVersionSelectsHighestCommonVersion(t *testing.T) {
	s := &adapter.Server{
		ProtocolVersions: []string{"1.0.0", "1.1.0"},
		Capabilities:     []string{"preConnect"},
		Version:          "adapter-test",
	}
	// The gateway prefers 1.2.0, which the adapter does not speak; the
	// next preference 1.1.0 is mutually supported.
	resp, err := s.NegotiateVersion(context.Background(), &adapterv1.NegotiateVersionRequest{
		AcceptedProtocolVersions: []string{"1.2.0", "1.1.0", "1.0.0"},
	})
	if err != nil {
		t.Fatalf("NegotiateVersion: %v", err)
	}
	if resp.Incompatible {
		t.Fatal("negotiation reported incompatible despite an overlapping version")
	}
	if resp.SelectedProtocolVersion != "1.1.0" {
		t.Errorf("selected version = %q, want 1.1.0", resp.SelectedProtocolVersion)
	}
	if resp.AdapterVersion != "adapter-test" {
		t.Errorf("adapter version = %q, want adapter-test", resp.AdapterVersion)
	}
	if len(resp.Capabilities) != 1 || resp.Capabilities[0] != "preConnect" {
		t.Errorf("capabilities = %v, want [preConnect]", resp.Capabilities)
	}
}

func TestNegotiateVersionReportsIncompatibleWhenNoOverlap(t *testing.T) {
	s := adapter.New("adapter-test")
	resp, err := s.NegotiateVersion(context.Background(), &adapterv1.NegotiateVersionRequest{
		AcceptedProtocolVersions: []string{"2.0.0", "3.0.0"},
	})
	if err != nil {
		t.Fatalf("NegotiateVersion: %v", err)
	}
	if !resp.Incompatible {
		t.Fatal("negotiation should be incompatible when the version sets do not overlap")
	}
	if resp.SelectedProtocolVersion != "" {
		t.Errorf("selected version = %q, want empty on an incompatible negotiation", resp.SelectedProtocolVersion)
	}
}

func TestNewAdvertisesTheV1Contract(t *testing.T) {
	s := adapter.New("v1.2.3")
	resp, err := s.NegotiateVersion(context.Background(), &adapterv1.NegotiateVersionRequest{
		AcceptedProtocolVersions: []string{adapter.ProtocolVersionV1},
	})
	if err != nil {
		t.Fatalf("NegotiateVersion: %v", err)
	}
	if resp.SelectedProtocolVersion != adapter.ProtocolVersionV1 {
		t.Errorf("selected version = %q, want %q", resp.SelectedProtocolVersion, adapter.ProtocolVersionV1)
	}
}

// adapterClient boots the gRPC server over an in-memory bufconn
// listener and returns connected Adapter and Health clients.
func adapterClient(t *testing.T, s *adapter.Server) (adapterv1.AdapterClient, healthv1.HealthClient) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(s)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return adapterv1.NewAdapterClient(conn), healthv1.NewHealthClient(conn)
}

func TestGRPCServerServesNegotiateVersion(t *testing.T) {
	client, _ := adapterClient(t, adapter.New("served"))

	resp, err := client.NegotiateVersion(context.Background(), &adapterv1.NegotiateVersionRequest{
		AcceptedProtocolVersions: []string{adapter.ProtocolVersionV1},
	})
	if err != nil {
		t.Fatalf("NegotiateVersion over gRPC: %v", err)
	}
	if resp.SelectedProtocolVersion != adapter.ProtocolVersionV1 || resp.AdapterVersion != "served" {
		t.Errorf("response = %+v, want the v1 contract from the 'served' adapter", resp)
	}
}

func TestGRPCServerReportsHealthy(t *testing.T) {
	_, hc := adapterClient(t, adapter.New("served"))

	resp, err := hc.Check(context.Background(), &healthv1.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("Health.Check: %v", err)
	}
	if resp.Status != healthv1.HealthCheckResponse_SERVING {
		t.Errorf("health status = %v, want SERVING", resp.Status)
	}
}

func TestGRPCServerReportsUnimplementedForUnbuiltRPCs(t *testing.T) {
	client, _ := adapterClient(t, adapter.New("served"))

	// The gRPC LifecycleChannel RPC is not yet implemented on the
	// Adapter server; the embedded UnimplementedAdapterServer answers
	// with codes.Unimplemented. For a streaming RPC the status
	// surfaces on the first Recv.
	stream, err := client.LifecycleChannel(context.Background())
	if err != nil {
		t.Fatalf("open LifecycleChannel stream: %v", err)
	}
	_, err = stream.Recv()
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("LifecycleChannel error code = %v, want Unimplemented", status.Code(err))
	}
}

func TestTLSServerOptionPlaintextWhenNoCert(t *testing.T) {
	opt, err := adapter.TLSServerOption("", "", "")
	if err != nil {
		t.Fatalf("TLSServerOption: %v", err)
	}
	if opt != nil {
		t.Error("TLSServerOption should return a nil option when no certificate is configured")
	}
}

func TestTLSServerOptionRejectsMissingCertFile(t *testing.T) {
	_, err := adapter.TLSServerOption("/nonexistent/tls.crt", "/nonexistent/tls.key", "")
	if err == nil {
		t.Fatal("TLSServerOption should error when the certificate file cannot be read")
	}
}

// writeTestKeypair generates a short-lived self-signed certificate and
// returns the paths to its PEM-encoded certificate and key.
func writeTestKeypair(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "lenny-adapter-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	dir := t.TempDir()
	certFile = filepath.Join(dir, "tls.crt")
	keyFile = filepath.Join(dir, "tls.key")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certFile, keyFile
}

func TestTLSServerOptionLoadsServerKeypair(t *testing.T) {
	certFile, keyFile := writeTestKeypair(t)
	opt, err := adapter.TLSServerOption(certFile, keyFile, "")
	if err != nil {
		t.Fatalf("TLSServerOption: %v", err)
	}
	if opt == nil {
		t.Error("TLSServerOption returned a nil option for a valid keypair")
	}
}

func TestTLSServerOptionConfiguresMutualTLS(t *testing.T) {
	certFile, keyFile := writeTestKeypair(t)
	// The self-signed certificate doubles as the client CA bundle.
	opt, err := adapter.TLSServerOption(certFile, keyFile, certFile)
	if err != nil {
		t.Fatalf("TLSServerOption with a client CA: %v", err)
	}
	if opt == nil {
		t.Error("TLSServerOption returned a nil option for a valid mTLS configuration")
	}
}

func TestTLSServerOptionRejectsUnusableClientCA(t *testing.T) {
	certFile, keyFile := writeTestKeypair(t)
	emptyCA := filepath.Join(t.TempDir(), "empty-ca.pem")
	if err := os.WriteFile(emptyCA, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("write empty CA: %v", err)
	}
	if _, err := adapter.TLSServerOption(certFile, keyFile, emptyCA); err == nil {
		t.Fatal("TLSServerOption should error when the client CA file holds no certificate")
	}
}

func TestTLSClientOptionPlaintextWhenNoMaterial(t *testing.T) {
	opt, err := adapter.TLSClientOption("", "", "")
	if err != nil {
		t.Fatalf("TLSClientOption: %v", err)
	}
	if opt == nil {
		t.Error("TLSClientOption should return a plaintext dial option when no TLS material is configured")
	}
}

func TestTLSClientOptionLoadsClientKeypair(t *testing.T) {
	certFile, keyFile := writeTestKeypair(t)
	opt, err := adapter.TLSClientOption(certFile, keyFile, "")
	if err != nil {
		t.Fatalf("TLSClientOption: %v", err)
	}
	if opt == nil {
		t.Error("TLSClientOption returned a nil option for a valid client keypair")
	}
}

func TestTLSClientOptionConfiguresServerCA(t *testing.T) {
	certFile, keyFile := writeTestKeypair(t)
	// The self-signed certificate doubles as the server CA bundle.
	opt, err := adapter.TLSClientOption(certFile, keyFile, certFile)
	if err != nil {
		t.Fatalf("TLSClientOption with a server CA: %v", err)
	}
	if opt == nil {
		t.Error("TLSClientOption returned a nil option for a valid mTLS configuration")
	}
}

func TestTLSClientOptionRejectsMissingKeypair(t *testing.T) {
	_, err := adapter.TLSClientOption("/nonexistent/tls.crt", "/nonexistent/tls.key", "")
	if err == nil {
		t.Fatal("TLSClientOption should error when the client certificate file cannot be read")
	}
}

func TestTLSClientOptionRejectsUnusableCA(t *testing.T) {
	emptyCA := filepath.Join(t.TempDir(), "empty-ca.pem")
	if err := os.WriteFile(emptyCA, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("write empty CA: %v", err)
	}
	if _, err := adapter.TLSClientOption("", "", emptyCA); err == nil {
		t.Fatal("TLSClientOption should error when the server CA file holds no certificate")
	}
}
