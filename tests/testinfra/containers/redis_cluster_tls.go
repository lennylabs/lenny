// SPDX-License-Identifier: MIT

package containers

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
)

// clusterTLSDir is the container directory the Redis Cluster nodes read
// their certificate, key, and CA from (REDIS_CLUSTER_TLS_DIR).
const clusterTLSDir = "/opt/lenny/redis-tls"

// clusterTLSMaterial is a CA and a node certificate for 127.0.0.1 written
// to PEM files on the host.
type clusterTLSMaterial struct {
	caFile, certFile, keyFile string
	caPool                    *x509.CertPool
}

// newClusterTLSMaterial generates a P-256 CA and a node certificate it
// signs, valid for an hour. The node certificate carries serverAuth for
// client connections and clientAuth for the cluster bus, where a node
// presents it when it dials another node.
func newClusterTLSMaterial(t testing.TB) *clusterTLSMaterial {
	t.Helper()
	dir := t.TempDir()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("redis cluster TLS: generate CA key: %v", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "lenny-test-redis-ca"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("redis cluster TLS: create CA: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("redis cluster TLS: parse CA: %v", err)
	}
	nodeKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("redis cluster TLS: generate node key: %v", err)
	}
	nodeTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	nodeDER, err := x509.CreateCertificate(rand.Reader, nodeTmpl, caCert, &nodeKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("redis cluster TLS: create node certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(nodeKey)
	if err != nil {
		t.Fatalf("redis cluster TLS: marshal node key: %v", err)
	}
	m := &clusterTLSMaterial{
		caFile:   filepath.Join(dir, "ca.crt"),
		certFile: filepath.Join(dir, "tls.crt"),
		keyFile:  filepath.Join(dir, "tls.key"),
		caPool:   x509.NewCertPool(),
	}
	m.caPool.AddCert(caCert)
	writePEM(t, m.caFile, "CERTIFICATE", caDER)
	writePEM(t, m.certFile, "CERTIFICATE", nodeDER)
	writePEM(t, m.keyFile, "EC PRIVATE KEY", keyDER)
	return m
}

// containerFiles maps the PEM files into clusterTLSDir. The key is
// world-readable because it is a throwaway test key and the node runs as
// whichever user the image starts the script with.
func (m *clusterTLSMaterial) containerFiles() []testcontainers.ContainerFile {
	return []testcontainers.ContainerFile{
		{HostFilePath: m.caFile, ContainerFilePath: clusterTLSDir + "/ca.crt", FileMode: 0o644},
		{HostFilePath: m.certFile, ContainerFilePath: clusterTLSDir + "/tls.crt", FileMode: 0o644},
		{HostFilePath: m.keyFile, ContainerFilePath: clusterTLSDir + "/tls.key", FileMode: 0o644},
	}
}

// clientConfig returns a TLS client configuration that verifies the nodes
// against the CA.
func (m *clusterTLSMaterial) clientConfig() *tls.Config {
	return &tls.Config{RootCAs: m.caPool, MinVersion: tls.VersionTLS12}
}

// writePEM writes der to path as one PEM block of type typ.
func writePEM(t testing.TB, path, typ string, der []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
		t.Fatalf("redis cluster TLS: write %s: %v", path, err)
	}
}
