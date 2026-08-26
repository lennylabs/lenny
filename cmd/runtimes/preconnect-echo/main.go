// SPDX-License-Identifier: MIT

// Command preconnect-echo is the §6.1 SDK-warm reference runtime: the
// embedded-model echo runtime that declares capabilities.preConnect: true
// and pre-connects its agent loop at warm time. It is the counterpart of
// cmd/runtimes/echo-embedded for the SDK-warm fast path.
//
// The §6.1 SDK-warm model pre-connects the agent process during the warm
// phase so the first prompt does not pay SDK cold-start latency. This
// binary shows the wiring a first-party Go runtime uses: build an
// adapter.Server, attach an adapter.SDKWarmInProcessRuntime (the §28.5.3
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
	// spec: §16.4 — structured JSON logs (component=preconnect-echo).
	logging.Setup(os.Stderr, "preconnect-echo")

	addr := flag.String("addr", ":50051", "address the embedded runtime gRPC server binds to")
	certFile := flag.String("tls-cert-file", "", "path to the server certificate")
	keyFile := flag.String("tls-key-file", "", "path to the server private key")
	clientCAFile := flag.String("tls-client-ca-file", "",
		"path to the CA bundle that verifies gateway client certificates")
	// spec: §6.4 — every session on every pod owns a per-slot tree under
	// the workspace base, `<base>/slots/{sessionId}/current/` for its cwd
	// and `<base>/slots/{sessionId}/staging/` for its uploads. The
	// embedded runtime is the adapter, so it parses the same base the
	// controller renders onto the runtime container's argv.
	workspaceBase := flag.String("workspace-base", "/workspace",
		"§6.4 workspace base the per-session `slots/{sessionId}` trees nest under")
	stagingDir := flag.String("staging-dir", "/workspace/.staging",
		"directory PrepareWorkspace streams uploaded files into before "+
			"FinalizeWorkspace materializes them")
	credentialsDir := flag.String("credentials-dir", "/run/lenny",
		"directory the §4.7 credential file and adapter manifest are materialized into")
	sharedAssetsDir := flag.String("shared-assets-dir", "/workspace/shared",
		"§6.4 directory the embedded runtime materializes read-only shared assets into at warm time")
	sharedAssets := flag.String("shared-assets", "",
		"§6.4 base64-encoded JSON array of inline shared-asset file specs (sharedassets.Encode)")
	// spec: §9.1/§4.7 — in the embedded model the runtime process is the
	// adapter, so it accepts the same platform-MCP flags the controller
	// injects onto a type:agent runtime container. Without these the
	// controller-injected flags would be unknown to flag.Parse and crash
	// the container.
	mcpSocket := flag.String("mcp-socket", "",
		"§9.1/§4.7 abstract Unix socket the platform MCP server binds for a type:agent runtime; empty disables it")
	gatewayGRPCAddr := flag.String("gateway-grpc-addr", "",
		"§9.1/§8.6 gateway GatewayControl address the embedded runtime dials to forward platform tool calls; empty serves an empty catalog")
	// spec: §4.4/§13.2 — the reconciler renders the checkpoint-probe flags
	// onto the pod's gateway-facing container, which in the embedded model
	// is the runtime. They are accepted and ignored: this fixture never
	// checkpoints, so it needs no size probe and dials no object store.
	// Without them flag.Parse exits 2 and the pool crash-loops.
	_ = flag.Int64("workspace-size-limit-bytes", 0,
		"§4.4 per-pod workspace-size limit for the pre-checkpoint size probe; accepted and ignored by this fixture")
	_ = flag.String("objectstore-ca-bundle", "",
		"§13.2 mounted object-store CA trust bundle; accepted and ignored by this fixture")
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
	adapterSrv.WorkspaceBase = *workspaceBase
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
	// §9.1/§4.7: bind the platform MCP socket and, when a gateway address is
	// configured, dial the gateway's GatewayControl service. ManifestDir is
	// set above, since the platform MCP server reads the authenticating nonce
	// from the manifest. In the embedded model the runtime and MCP server are
	// one process and UID, so RuntimeUID stays zero (the SO_PEERCRED self-check
	// is disabled, which is correct per §4.7).
	gwCloser, err := adapterSrv.ConnectGateway(*mcpSocket, *gatewayGRPCAddr, *certFile, *keyFile, *clientCAFile)
	if err != nil {
		log.Fatalf("preconnect-echo: %v", err)
	}
	defer func() { _ = gwCloser.Close() }()
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
		// §6.1 — SIGTERM during sdk_connecting tears the SDK down
		// within LENNY_DEMOTE_TIMEOUT_SECONDS (default 5s), force-terminating
		// it on overrun, before the process exits so it does not leak
		// credentials or hold provider connections open.
		adapterSrv.ShutdownDemoteSDK(adapter.DemoteTimeoutFromEnv())
		srv.GracefulStop()
	}()

	// §6.1 — pre-connect the SDK at warm time, before any session
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

// echoLoop is the §28.5.3 echo handler the adapter drives in-process.
func echoLoop(ctx context.Context, in io.Reader, out io.Writer) error {
	return echocore.Run(ctx, in, out, os.Stderr)
}
