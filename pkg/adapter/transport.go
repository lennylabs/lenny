// SPDX-License-Identifier: MIT

package adapter

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// NewGRPCServer builds a gRPC server with the Adapter service and the
// standard grpc.health.v1 service registered. The health service is
// set to SERVING; the gateway probes adapter liveness through it
// (§4.7). Pass TLSServerOption to serve the §4.7 mTLS link.
func NewGRPCServer(s *Server, opts ...grpc.ServerOption) *grpc.Server {
	gs := grpc.NewServer(opts...)
	adapterv1.RegisterAdapterServer(gs, s)

	hs := health.NewServer()
	hs.SetServingStatus("", healthv1.HealthCheckResponse_SERVING)
	healthv1.RegisterHealthServer(gs, hs)

	return gs
}

// TLSServerOption builds the gRPC server option that serves the
// adapter over TLS. The §4.7 gateway↔adapter link is mTLS in
// production: when clientCAFile is supplied, the adapter requires and
// verifies the gateway's client certificate against it. When certFile
// and keyFile are both empty the adapter serves plaintext, which is
// intended only for local development; the returned option is nil in
// that case.
func TLSServerOption(certFile, keyFile, clientCAFile string) (grpc.ServerOption, error) {
	if certFile == "" && keyFile == "" {
		return nil, nil
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load adapter TLS keypair: %w", err)
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	if clientCAFile != "" {
		caPEM, err := os.ReadFile(clientCAFile)
		if err != nil {
			return nil, fmt.Errorf("read adapter client CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, errors.New("adapter client CA file contains no usable certificate")
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return grpc.Creds(credentials.NewTLS(cfg)), nil
}
