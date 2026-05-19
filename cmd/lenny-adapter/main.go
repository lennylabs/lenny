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
// The adapter drives the runtime over one of two §4.7 sidecar-model
// transports, both carrying the identical §15.4.1 JSONL framing:
//
//   - --runtime-socket: the adapter binds an abstract Unix socket and
//     the runtime container dials it. This is the §4.7 deployment model:
//     the adapter and the runtime are separate pod containers, and the
//     runtime has no stdin attached. The controller passes the socket
//     name to the runtime container in LENNY_ADAPTER_SOCKET.
//   - --runtime-bin: the adapter execs the runtime binary as a child
//     and drives it over stdin/stdout. This is the single-host developer
//     loop, where one process exercises a runtime without a pod.
//
// Usage:
//
//	lenny-adapter --addr :50051 \
//	  --runtime-socket @lenny-runtime \
//	  --tls-cert-file /etc/lenny/adapter-tls/tls.crt \
//	  --tls-key-file  /etc/lenny/adapter-tls/tls.key \
//	  --tls-client-ca-file /etc/lenny/adapter-tls/ca.crt
package main

import (
	"context"
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
		"path to the runtime binary the adapter execs and drives over stdin/stdout (developer loop)")
	runtimeSocket := flag.String("runtime-socket", "",
		"abstract Unix socket the adapter binds for the §4.7 sidecar runtime transport; "+
			"the runtime container dials it")
	lifecycleSocket := flag.String("lifecycle-socket", "",
		"Unix socket path for the §15.4.6 runtime lifecycle channel; empty disables it")
	flag.Parse()

	if *runtimeBin != "" && *runtimeSocket != "" {
		log.Fatalf("lenny-adapter: --runtime-bin and --runtime-socket are mutually exclusive")
	}

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
	switch {
	case *runtimeSocket != "":
		// §4.7 sidecar model: bind the abstract socket the runtime
		// container dials. The controller sets LENNY_ADAPTER_SOCKET on
		// the runtime container to this same name.
		sp, err := adapter.NewSocketRuntimeProcess(*runtimeSocket)
		if err != nil {
			log.Fatalf("lenny-adapter: %v", err)
		}
		adapterSrv.Runtime = sp
		log.Printf("lenny-adapter: §4.7 sidecar runtime transport on socket %s", sp.SocketPath())
	case *runtimeBin != "":
		// Developer loop: exec the runtime as a child and drive it over
		// stdin/stdout.
		adapterSrv.Runtime = executor.NewSubprocessExecutor(executor.SubprocessOptions{
			BinPath: *runtimeBin,
		})
	}

	// §15.4.6: when a lifecycle socket is configured, the adapter listens
	// on it for the Full-level runtime's lifecycle connection and
	// advertises it in the session manifest.
	var lifecycle *adapter.LifecycleChannel
	if *lifecycleSocket != "" {
		lifecycle, err = adapter.NewLifecycleChannel(*lifecycleSocket)
		if err != nil {
			log.Fatalf("lenny-adapter: %v", err)
		}
		adapterSrv.Lifecycle = lifecycle
		go func() {
			if err := lifecycle.Run(context.Background()); err != nil {
				log.Printf("lenny-adapter: lifecycle channel stopped: %v", err)
			}
		}()
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
		if lifecycle != nil {
			_ = lifecycle.Close()
		}
		srv.GracefulStop()
	}()

	log.Printf("lenny-adapter: serving the adapter on %s (tls=%t)", *addr, tlsOpt != nil)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("lenny-adapter: serve: %v", err)
	}
}
