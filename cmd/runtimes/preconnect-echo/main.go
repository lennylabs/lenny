// SPDX-License-Identifier: MIT

// Command preconnect-echo is the §6.1 SDK-warm reference runtime: the
// embedded-model echo runtime that declares capabilities.preConnect: true
// and pre-connects its agent loop at warm time. It is the counterpart of
// cmd/runtimes/echo-embedded for the SDK-warm fast path.
//
// The §6.1 SDK-warm model pre-connects the agent process during the warm
// phase so the first prompt does not pay SDK cold-start latency. This
// binary shows the wiring a first-party Go runtime uses: build an
// adapter.Server, attach an adapter.SDKWarmInProcessRuntime (the §15.4.1
// echo loop with the SDK-warm PreConnect / ConfigureWorkspace / DemoteSDK
// methods), call adapter.Server.PreConnect once the server is up, and
// serve adapter.NewGRPCServer. The gateway then either points the
// pre-connected loop at the session workspace (ConfigureWorkspace) or, when
// the workspace plan matches the runtime's sdkWarmBlockingPaths, demotes it
// (DemoteSDK) and serves the session via the pod-warm StartSession path.
//
// The gateway↔runtime link is mTLS in production, configured exactly as
// for lenny-adapter and echo-embedded: supply --tls-cert-file,
// --tls-key-file, and --tls-client-ca-file. With no certificate the server
// is plaintext, intended only for local development.
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

// version is the runtime build version, reported during gateway version
// negotiation.
var version = "0.1.0"

func main() {
	// spec: §16.4 lines 370-372 — structured JSON logs (component=preconnect-echo).
	logging.Setup(os.Stderr, "preconnect-echo")

	addr := flag.String("addr", ":50051", "address the embedded runtime gRPC server binds to")
	certFile := flag.String("tls-cert-file", "", "path to the server certificate")
	keyFile := flag.String("tls-key-file", "", "path to the server private key")
	clientCAFile := flag.String("tls-client-ca-file", "",
		"path to the CA bundle that verifies gateway client certificates")
	workspaceRoot := flag.String("workspace-root", "/workspace/current",
		"directory the session workspace is materialized into")
	stagingDir := flag.String("staging-dir", "/workspace/.staging",
		"directory PrepareWorkspace streams uploaded files into before "+
			"FinalizeWorkspace materializes them")
	credentialsDir := flag.String("credentials-dir", "/run/lenny",
		"directory the §4.7 credential file and adapter manifest are materialized into")
	sharedAssetsDir := flag.String("shared-assets-dir", "/workspace/shared",
		"§6.4 directory the embedded runtime materializes read-only shared assets into at warm time")
	sharedAssets := flag.String("shared-assets", "",
		"§6.4 base64-encoded JSON array of inline shared-asset file specs (sharedassets.Encode)")
	flag.Parse()

	tlsOpt, err := adapter.TLSServerOption(*certFile, *keyFile, *clientCAFile)
	if err != nil {
		log.Fatalf("preconnect-echo: %v", err)
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
	adapterSrv.SharedAssetsDir = *sharedAssetsDir
	parsedShared, err := sharedassets.Decode(*sharedAssets)
	if err != nil {
		log.Fatalf("preconnect-echo: %v", err)
	}
	adapterSrv.SharedAssets = parsedShared
	if err := adapterSrv.EnsureWarmWorkspaceLayout(); err != nil {
		log.Fatalf("preconnect-echo: %v", err)
	}
	// §6.1 — advertise the preConnect capability so the gateway drives this
	// pod through ConfigureWorkspace / DemoteSDK rather than StartSession.
	adapterSrv.Capabilities = append(adapterSrv.Capabilities, adapter.CapabilityPreConnect)
	// §6.1: the runtime logic is an SDK-warm in-process echo loop.
	adapterSrv.Runtime = adapter.NewSDKWarmInProcessRuntime(echoLoop)

	srv := adapter.NewGRPCServer(adapterSrv, opts...)

	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("preconnect-echo: listen on %s: %v", *addr, err)
	}

	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop
		// §6.1 line 67 — SIGTERM during sdk_connecting tears the SDK down
		// before the process exits so it does not leak credentials or hold
		// provider connections open.
		_, _ = adapterSrv.DemoteSDK(context.Background(), nil)
		srv.GracefulStop()
	}()

	// §6.1 line 30 — pre-connect the SDK at warm time, before any session
	// is assigned. The server is already serving, so a concurrent gateway
	// claim that arrives before pre-connect completes is handled by the
	// idempotent ConfigureWorkspace path.
	go func() {
		if err := adapterSrv.PreConnect(context.Background()); err != nil {
			log.Printf("preconnect-echo: SDK pre-connect failed, pod stays pod-warm: %v", err)
		}
	}()

	log.Printf("preconnect-echo: serving the §6.1 SDK-warm runtime on %s (tls=%t)", *addr, tlsOpt != nil)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("preconnect-echo: serve: %v", err)
	}
}

// echoLoop is the §15.4.1 echo handler the adapter drives in-process.
func echoLoop(ctx context.Context, in io.Reader, out io.Writer) error {
	return echocore.Run(ctx, in, out, os.Stderr)
}
