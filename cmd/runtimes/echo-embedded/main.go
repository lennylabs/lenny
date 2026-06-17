// SPDX-License-Identifier: MIT

// Command echo-embedded is the Basic-level reference runtime for the
// §4.7 embedded deployment model. It is the embedded counterpart of
// cmd/runtimes/echo: the same §15.4.1 echo loop (pkg/runtimekit/echocore),
// the same observable behaviour, packaged as a single binary that links
// the §4.7 adapter as a library and serves the adapter gRPC contract to
// the gateway itself.
//
// §4.7 deployment models:
//
//   - sidecar (cmd/runtimes/echo): the adapter runs as a separate
//     container and bridges the runtime over an abstract Unix socket.
//     The runtime author implements only the §15.4.1 JSONL protocol.
//   - embedded (this binary): a first-party runtime image links the
//     adapter and serves the gRPC contract directly. One process, one
//     container, no adapter↔runtime socket and no SO_PEERCRED or
//     nonce-handshake boundary — the §4.7 trade-off table trades that
//     process isolation for lower latency and resource overhead.
//
// echo-embedded shows the wiring a first-party Go runtime uses for the
// embedded model: build an adapter.Server, attach an
// adapter.InProcessRuntime whose loop is the runtime's §15.4.1 handler,
// and serve adapter.NewGRPCServer. The gateway speaks the identical
// adapter gRPC contract to this binary as it does to a sidecar adapter.
//
// The gateway↔runtime link is mTLS in production, configured exactly as
// for lenny-adapter: supply --tls-cert-file, --tls-key-file, and
// --tls-client-ca-file. With no certificate the server is plaintext,
// intended only for local development.
//
// Usage:
//
//	echo-embedded --addr :50051 \
//	  --tls-cert-file /etc/lenny/adapter-tls/tls.crt \
//	  --tls-key-file  /etc/lenny/adapter-tls/tls.key \
//	  --tls-client-ca-file /etc/lenny/adapter-tls/ca.crt
package main

import (
	"context"
	"flag"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/sharedassets"
	"github.com/lennylabs/lenny/pkg/observability/logging"
	"github.com/lennylabs/lenny/pkg/runtimekit/echocore"
)

// version is the runtime build version, reported during gateway
// version negotiation.
var version = "0.1.0"

func main() {
	// spec: §16.4 lines 370-372 — structured JSON logs; routes the stdlib
	// log package through the §16.4 handler (component=echo-embedded).
	// F-16.4.1.
	logging.Setup(os.Stderr, "echo-embedded")

	addr := flag.String("addr", ":50051", "address the embedded runtime gRPC server binds to")
	certFile := flag.String("tls-cert-file", "", "path to the server certificate")
	keyFile := flag.String("tls-key-file", "", "path to the server private key")
	clientCAFile := flag.String("tls-client-ca-file", "",
		"path to the CA bundle that verifies gateway client certificates")
	// spec: §6.4 line 407 — session-mode pods (maxConcurrentSessions=1)
	// use the single `/workspace/current` cwd; the concurrent-workspace per-slot
	// tree (F-6.4.2) is unbuilt in v1, so the default here is correct
	// across the v1 surface.
	workspaceRoot := flag.String("workspace-root", "/workspace/current",
		"directory the session workspace is materialized into")
	stagingDir := flag.String("staging-dir", "/workspace/.staging",
		"directory PrepareWorkspace streams uploaded files into before "+
			"FinalizeWorkspace materializes them")
	credentialsDir := flag.String("credentials-dir", "/run/lenny",
		"directory the §4.7 credential file and adapter manifest are materialized into")
	sharedAssetsDir := flag.String("shared-assets-dir", "/workspace/shared",
		"§6.4 directory the embedded runtime materializes read-only shared assets "+
			"into at warm time; empty skips the populate step")
	sharedAssets := flag.String("shared-assets", "",
		"§6.4 base64-encoded JSON array of inline shared-asset file specs "+
			"(sharedassets.Encode); empty leaves /workspace/shared empty")
	flag.Parse()

	tlsOpt, err := adapter.TLSServerOption(*certFile, *keyFile, *clientCAFile)
	if err != nil {
		log.Fatalf("echo-embedded: %v", err)
	}
	var opts []grpc.ServerOption
	if tlsOpt != nil {
		opts = append(opts, tlsOpt)
	}

	adapterSrv := adapter.New(version)
	adapterSrv.WorkspaceRoot = *workspaceRoot
	adapterSrv.StagingDir = *stagingDir
	adapterSrv.CredentialsDir = *credentialsDir
	adapterSrv.ManifestDir = *credentialsDir
	// §6.4 line 409: the embedded runtime is the adapter, so it populates
	// /workspace/shared itself from the inline shared-asset set the
	// controller renders onto --shared-assets.
	adapterSrv.SharedAssetsDir = *sharedAssetsDir
	parsedShared, err := sharedassets.Decode(*sharedAssets)
	if err != nil {
		log.Fatalf("echo-embedded: %v", err)
	}
	adapterSrv.SharedAssets = parsedShared
	// §6.1 / §6.4: create /workspace/current, the staging area, and the
	// read-only /workspace/shared tree before the pod is claimed.
	if err := adapterSrv.EnsureWarmWorkspaceLayout(); err != nil {
		log.Fatalf("echo-embedded: %v", err)
	}
	// §4.7 embedded model: the runtime logic runs in this process. The
	// adapter drives it through an in-process pipe instead of a socket
	// or a child process's stdin/stdout — the §15.4.1 framing is the
	// same, only the transport differs.
	adapterSrv.Runtime = adapter.NewInProcessRuntime(echoLoop)

	srv := adapter.NewGRPCServer(adapterSrv, opts...)

	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("echo-embedded: listen on %s: %v", *addr, err)
	}

	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop
		srv.GracefulStop()
	}()

	log.Printf("echo-embedded: serving the §4.7 embedded runtime on %s (tls=%t)", *addr, tlsOpt != nil)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("echo-embedded: serve: %v", err)
	}
}

// echoLoop is the §15.4.1 echo handler the adapter drives in-process.
// It is the same echocore loop cmd/runtimes/echo runs over a socket or
// stdin/stdout; here it runs over the adapter's in-memory pipe.
func echoLoop(ctx context.Context, in io.Reader, out io.Writer) error {
	return echocore.Run(ctx, in, out, os.Stderr)
}
