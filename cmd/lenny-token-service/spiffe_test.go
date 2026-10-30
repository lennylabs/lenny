// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/url"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"

	"github.com/lennylabs/lenny/pkg/spiffeid"
)

// TestSPIFFEAuditInterceptorAttributesPerReplicaIdentity drives
// spiffeAuditInterceptor across both mTLS caller paths. When the gateway
// replica presents a client certificate carrying a spiffe:// URI SAN, the
// interceptor parses it and makes the per-replica identity readable from
// the handler's context via SPIFFEIDFromContext. When the caller presents
// the shared per-Service certificate with no SPIFFE URI SAN, the
// interceptor still dispatches the handler and attaches no identity. Both
// branches, plus the WithSPIFFEID / SPIFFEIDFromContext context round-trip,
// are exercised so a regression that drops URI-SAN extraction fails here
// instead of shipping silently.
//
// spec: §4.3 — "Each gateway replica has a distinct mTLS identity so
// compromise of one is attributable and revocable independently." The
// per-replica SPIFFE identity is what makes a single replica's calls
// attributable; without extracting it from the client certificate the
// Token Service cannot record which replica made a credential-assignment
// call.
//
// diagnosis: a failure means the Token Service gRPC audit interceptor no
// longer extracts the gateway replica's SPIFFE identity from its mTLS
// certificate (or attaches it to the wrong context). Per-replica calls
// become unattributable, breaking the §4.3 requirement that a compromised
// replica be identifiable and revocable independently. If only the
// non-SPIFFE case fails, the interceptor regressed to rejecting the shared
// per-Service certificate path instead of proceeding.
func TestSPIFFEAuditInterceptorAttributesPerReplicaIdentity(t *testing.T) {
	caPEM, caKey := generateTestCA(t)
	interceptor := spiffeAuditInterceptor()
	info := &grpc.UnaryServerInfo{FullMethod: "/lenny.tokenservice.v1.TokenService/AssignCredentials"}

	t.Run("spiffe uri san is attributed to the handler context", func(t *testing.T) {
		const wantURI = "spiffe://lenny/gateway/replica-3"
		spiffeURI, err := url.Parse(wantURI)
		if err != nil {
			t.Fatalf("parse spiffe uri: %v", err)
		}
		cert := spiffeLeafCert(t, caPEM, caKey, "lenny-gateway.lenny-system.svc", spiffeURI)
		ctx := peerCtxWithClientCert(cert)

		var (
			got    spiffeid.ID
			seen   bool
			called bool
		)
		_, err = interceptor(ctx, nil, info, func(hctx context.Context, _ any) (any, error) {
			called = true
			got, seen = SPIFFEIDFromContext(hctx)
			return "ok", nil
		})
		if err != nil {
			t.Fatalf("interceptor returned error: %v", err)
		}
		if !called {
			t.Fatal("handler was not invoked on the per-replica SPIFFE path")
		}
		if !seen {
			t.Fatal("SPIFFEIDFromContext returned no ID; want the per-replica SPIFFE ID attached to the context")
		}
		if got.URI != wantURI {
			t.Fatalf("SPIFFE URI = %q; want %q", got.URI, wantURI)
		}
		if got.TrustDomain != "lenny" || got.Path != "/gateway/replica-3" {
			t.Fatalf("parsed ID = %+v; want trust domain \"lenny\" and path \"/gateway/replica-3\"", got)
		}
	})

	t.Run("non-spiffe shared cert still runs the handler with no id attached", func(t *testing.T) {
		// The shared per-Service certificate carries no spiffe:// URI SAN.
		cert := spiffeLeafCert(t, caPEM, caKey, "lenny-gateway.lenny-system.svc")
		ctx := peerCtxWithClientCert(cert)

		var (
			seen   bool
			called bool
		)
		_, err := interceptor(ctx, nil, info, func(hctx context.Context, _ any) (any, error) {
			called = true
			_, seen = SPIFFEIDFromContext(hctx)
			return "ok", nil
		})
		if err != nil {
			t.Fatalf("interceptor returned error: %v", err)
		}
		if !called {
			t.Fatal("handler was not invoked on the shared per-Service certificate path")
		}
		if seen {
			t.Fatal("SPIFFEIDFromContext returned an ID for a non-SPIFFE certificate; want no identity attached")
		}
	})
}

// peerCtxWithClientCert returns a context carrying a synthetic gRPC peer
// whose mTLS AuthInfo presents cert as the caller's leaf certificate, so
// the interceptor sees the same credentials.TLSInfo a real handshake would
// have populated.
func peerCtxWithClientCert(cert *x509.Certificate) context.Context {
	return peer.NewContext(context.Background(), &peer.Peer{
		AuthInfo: credentials.TLSInfo{
			State: tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}},
		},
	})
}

// spiffeLeafCert mints a client leaf certificate carrying the given URI
// SANs, signed by the CA in caPEM, and returns the parsed certificate ready
// to drop into a synthetic peer context. Passing no URIs models the shared
// per-Service certificate path; passing a spiffe:// URI models the
// per-replica gateway identity path.
func spiffeLeafCert(t *testing.T, caPEM []byte, caKey *ecdsa.PrivateKey, commonName string, uris ...*url.URL) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(caPEM)
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse test CA: %v", err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: commonName},
		DNSNames:     []string{commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:         uris,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse leaf cert: %v", err)
	}
	return cert
}
