// SPDX-License-Identifier: MIT

// Command lenny-adapter runs the §4.7 runtime adapter: the gRPC sidecar
// that bridges the Lenny gateway to a pod's runtime binary. It serves
// the adapterv1.Adapter service and the standard gRPC health service.
//
// The gateway↔adapter link is mTLS in production: supply
// --tls-cert-file, --tls-key-file, and --tls-client-ca-file, where the
// CA bundle verifies the gateway's client certificate. With no
// certificate the adapter serves plaintext, intended only for local
// development.
//
// Usage:
//
//	lenny-adapter --addr :50051 \
//	  --tls-cert-file /etc/lenny/adapter-tls/tls.crt \
//	  --tls-key-file  /etc/lenny/adapter-tls/tls.key \
//	  --tls-client-ca-file /etc/lenny/adapter-tls/ca.crt
package main

import (
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
)

// version is the adapter build version, reported during gateway
// version negotiation.
var version = "0.1.0"

func main() {
	addr := flag.String("addr", ":50051", "address the adapter gRPC server binds to")
	certFile := flag.String("tls-cert-file", "", "path to the adapter server certificate")
	keyFile := flag.String("tls-key-file", "", "path to the adapter server private key")
	clientCAFile := flag.String("tls-client-ca-file", "",
		"path to the CA bundle that verifies gateway client certificates")
	workspaceRoot := flag.String("workspace-root", "/workspace/current",
		"directory the session workspace is materialized into")
	credentialsDir := flag.String("credentials-dir", "/run/lenny",
		"directory the §4.7 credential file is materialized into")
	runtimeBin := flag.String("runtime-bin", "",
		"path to the runtime binary the adapter starts at session start")
	flag.Parse()

	tlsOpt, err := adapter.TLSServerOption(*certFile, *keyFile, *clientCAFile)
	if err != nil {
		log.Fatalf("lenny-adapter: %v", err)
	}
	var opts []grpc.ServerOption
	if tlsOpt != nil {
		opts = append(opts, tlsOpt)
	}

	adapterSrv := adapter.New(version)
	adapterSrv.WorkspaceRoot = *workspaceRoot
	adapterSrv.CredentialsDir = *credentialsDir
	// §15.4: the adapter manifest is written into /run/lenny alongside
	// the credential file.
	adapterSrv.ManifestDir = *credentialsDir
	if *runtimeBin != "" {
		adapterSrv.Runtime = executor.NewSubprocessExecutor(executor.SubprocessOptions{
			BinPath: *runtimeBin,
		})
	}
	srv := adapter.NewGRPCServer(adapterSrv, opts...)

	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("lenny-adapter: listen on %s: %v", *addr, err)
	}

	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop
		srv.GracefulStop()
	}()

	log.Printf("lenny-adapter: serving the adapter on %s (tls=%t)", *addr, tlsOpt != nil)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("lenny-adapter: serve: %v", err)
	}
}
