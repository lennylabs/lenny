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
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/sharedassets"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/observability/logging"
	"github.com/lennylabs/lenny/pkg/observability/tracing"
)

// version is the adapter build version, reported during gateway
// version negotiation.
var version = "0.1.0"

// resolveRuntimeUID returns the agent runtime UID for the §4.7/§13
// SO_PEERCRED MCP peer check. The --runtime-uid flag takes precedence; a
// zero flag falls back to the LENNY_RUNTIME_UID environment variable the
// pod spec injects from the runtime container's runAsUser. A value that
// is zero or unparseable leaves the peer check disabled.
func resolveRuntimeUID(flagUID uint) uint32 {
	if flagUID != 0 {
		return uint32(flagUID)
	}
	if env := os.Getenv("LENNY_RUNTIME_UID"); env != "" {
		if uid, err := strconv.ParseUint(env, 10, 32); err == nil {
			return uint32(uid)
		}
		log.Printf("lenny-adapter: ignoring unparseable LENNY_RUNTIME_UID=%q", env)
	}
	return 0
}

func main() {
	// spec: §16.4 lines 370-372 — structured JSON logs from the runtime
	// adapter; routes the stdlib log package through the §16.4 handler
	// (component=adapter) before any subcommand dispatch. F-16.4.1.
	logging.Setup(os.Stderr, "adapter")

	// spec: §4.6.1 — `lenny-adapter prestop` is the agent-pod preStop
	// drain hook. It signals the running adapter and waits for it to
	// drain in-flight checkpoints, so it runs as a distinct subcommand
	// rather than starting the gRPC server.
	if len(os.Args) > 1 && os.Args[1] == "prestop" {
		cfg, err := parsePreStopArgs(os.Args[2:])
		if err != nil {
			log.Fatalf("lenny-adapter: %v", err)
		}
		os.Exit(runPreStop(cfg, preStopOSDeps(log.Printf)))
	}

	addr := flag.String("addr", ":50051", "address the adapter gRPC server binds to")
	certFile := flag.String("tls-cert-file", "", "path to the adapter server certificate")
	keyFile := flag.String("tls-key-file", "", "path to the adapter server private key")
	clientCAFile := flag.String("tls-client-ca-file", "",
		"path to the CA bundle that verifies gateway client certificates")
	workspaceRoot := flag.String("workspace-root", "/workspace/current",
		"directory the session workspace is materialized into")
	sessionsRoot := flag.String("sessions-root", "/sessions",
		"§6.4 /sessions tmpfs the runtime writes its session file into; "+
			"bundled into the §4.4 checkpoint and restored on §7.3 resume "+
			"(step f). Empty disables session-file capture")
	artifactsRoot := flag.String("artifacts-root", "/artifacts",
		"§6.4 /artifacts base the per-slot concurrent-workspace artifact "+
			"trees (/artifacts/{slotId}) nest under")
	stagingDir := flag.String("staging-dir", "/workspace/.staging",
		"directory PrepareWorkspace streams uploaded files into before "+
			"FinalizeWorkspace materializes them; empty leaves PrepareWorkspace "+
			"returning FailedPrecondition")
	credentialsDir := flag.String("credentials-dir", "/run/lenny",
		"directory the §4.7 credential file is materialized into")
	sharedAssetsDir := flag.String("shared-assets-dir", "/workspace/shared",
		"§6.4 directory the adapter materializes read-only shared assets into at "+
			"warm time; empty skips the populate step")
	sharedAssets := flag.String("shared-assets", "",
		"§6.4 base64-encoded JSON array of inline shared-asset file specs "+
			"(sharedassets.Encode); empty leaves /workspace/shared empty")
	runtimeUID := flag.Uint("runtime-uid", 0,
		"UID the agent runtime process runs as (the pod spec runAsUser); "+
			"the adapter applies the §4.7/§13 SO_PEERCRED MCP peer check against "+
			"it. 0 falls back to LENNY_RUNTIME_UID; still 0 disables the check")
	requireSoPeercred := flag.Bool("require-so-peercred", true,
		"run the mandatory §4.7 SO_PEERCRED startup self-test and crash-loop on "+
			"failure; set false only when gVisor SO_PEERCRED divergence is "+
			"confirmed and nonce-only mode is explicitly accepted")
	runtimeBin := flag.String("runtime-bin", "",
		"path to the runtime binary the adapter execs and drives over stdin/stdout (developer loop)")
	runtimeSocket := flag.String("runtime-socket", "",
		"abstract Unix socket the adapter binds for the §4.7 sidecar runtime transport; "+
			"the runtime container dials it")
	lifecycleSocket := flag.String("lifecycle-socket", "",
		"Unix socket path for the §15.4.6 runtime lifecycle channel; empty disables it")
	mcpSocket := flag.String("mcp-socket", "",
		"§9.1/§4.7 abstract Unix socket the platform MCP server binds for a type:agent "+
			"runtime (the @lenny-platform-mcp socket the manifest names); empty disables it")
	gatewayGRPCAddr := flag.String("gateway-grpc-addr", "",
		"§9.1/§8.6 gateway GatewayControl address (host:port) the adapter dials to forward "+
			"platform tool calls (and lease extensions); empty leaves the platform MCP server "+
			"serving an empty catalog")
	otlpEndpoint := flag.String("otlp-endpoint", "",
		"§4.7/§16.3 OTLP collector URL written into the adapter manifest's "+
			"observability.otlpEndpoint; empty omits the observability object")
	otlpTLSDisabled := flag.Bool("otlp-tls-disabled", false,
		"§13.2 (NET-059) dev/make-run escape hatch: write observability.otlpTlsEnabled=false "+
			"so the runtime accepts an http:// OTLP endpoint; production keeps this false and "+
			"uses an https:// endpoint")
	// spec: §11.3 lines 205-206 — gateway→pod keepalive: 10s interval,
	// 5s timeout. The server-side EnforcementPolicy keeps the gateway's
	// client params (the 10s interval) from triggering an
	// ENHANCE_YOUR_CALM. F-11.3.12.
	keepaliveTimeMs := flag.Int("keepalive-time-ms",
		envIntOr("LENNY_ADAPTER_KEEPALIVE_TIME_MS", 10_000),
		"§11.3 line 205 keepalive interval (ms) for gRPC pings from the gateway. Default 10000ms.")
	keepaliveTimeoutMs := flag.Int("keepalive-timeout-ms",
		envIntOr("LENNY_ADAPTER_KEEPALIVE_TIMEOUT_MS", 5_000),
		"§11.3 line 206 keepalive timeout (ms) the gateway waits for a ping reply. Default 5000ms.")
	coordinatorHoldTimeoutSec := flag.Int("coordinator-hold-timeout-seconds",
		envIntOr("LENNY_COORDINATOR_HOLD_TIMEOUT_SECONDS", 120),
		"§10.1 line 50 coordinatorHoldTimeoutSeconds: how long the adapter holds a session after losing its coordinator before self-terminating. Default 120s.")
	postMortemDir := flag.String("post-mortem-dir",
		os.Getenv("LENNY_POST_MORTEM_DIR"),
		"§10.1 line 50 directory the adapter writes a coordinator_lost post-mortem record into when a hold times out with no new coordinator. Empty skips the disk write.")
	heartbeatIntervalSec := flag.Int("heartbeat-interval-seconds",
		envIntOr("LENNY_ADAPTER_HEARTBEAT_INTERVAL_SECONDS", 30),
		"§15.4.1 line 1442 cadence (seconds) at which the adapter sends a heartbeat liveness ping to the runtime. 0 disables heartbeats. Default 30s.")
	heartbeatAckTimeoutSec := flag.Int("heartbeat-ack-timeout-seconds",
		envIntOr("LENNY_ADAPTER_HEARTBEAT_ACK_TIMEOUT_SECONDS", 10),
		"§15.4.1 line 1826 window (seconds) the runtime has to answer a heartbeat before the adapter considers it hung and sends SIGTERM. Default 10s.")
	flag.Parse()

	if *runtimeBin != "" && *runtimeSocket != "" {
		log.Fatalf("lenny-adapter: --runtime-bin and --runtime-socket are mutually exclusive")
	}

	// spec: §16.3 line 359 — install the process-wide TracerProvider and
	// W3C propagator so the pod adapter's §16.3 spans (session.upload,
	// session.finalize_workspace, session.run_setup, session.start,
	// session.tool_call) reach the OTLP Collector instead of the no-op
	// provider. With no OTEL endpoint a stdout exporter is used. F-16.3.2.
	traceShutdown, err := tracing.InitProvider(context.Background(), tracing.ProviderConfig{
		ServiceName:  "lenny-adapter",
		OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	})
	if err != nil {
		log.Fatalf("lenny-adapter: tracing init: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = traceShutdown(ctx)
	}()

	// §4.7 lines 870-877: run the mandatory SO_PEERCRED startup self-test
	// before any other setup. On failure, crash-loop the pod so it never
	// enters the warm pool with a non-functional security boundary; when
	// --require-so-peercred is false (confirmed gVisor divergence), the
	// failure is suppressed and the adapter runs in nonce-only mode.
	if err := adapter.PeercredSelftest(); err != nil && *requireSoPeercred {
		adapter.IncSoPeercredSelftestFailed()
		log.Fatalf("lenny-adapter: FATAL: SO_PEERCRED self-test failed — UID mismatch or "+
			"syscall error; adapter security boundary cannot be enforced: %v", err)
	}
	if !*requireSoPeercred {
		// §4.7 lines 885-888: nonce-only mode is an auditable escalation,
		// not a silent steady state.
		adapter.IncSoPeercredDisabled()
		log.Printf("lenny-adapter: WARNING: --require-so-peercred=false; running in " +
			"nonce-only mode (§4.7). Deployers MUST alert on " +
			"lenny_adapter_sopeercred_disabled_total > 0")
	}

	tlsOpt, err := adapter.TLSServerOption(*certFile, *keyFile, *clientCAFile)
	if err != nil {
		log.Fatalf("lenny-adapter: %v", err)
	}
	var opts []grpc.ServerOption
	if tlsOpt != nil {
		opts = append(opts, tlsOpt)
	}
	// spec: §11.3 lines 205-206 — server-side keepalive that mirrors the
	// gateway's client params. MinTime guards against an over-aggressive
	// client; PermitWithoutStream allows the gateway's idle pings to
	// keep the connection observably alive. F-11.3.12.
	keepaliveTime := time.Duration(*keepaliveTimeMs) * time.Millisecond
	keepaliveTimeout := time.Duration(*keepaliveTimeoutMs) * time.Millisecond
	opts = append(
		opts,
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             keepaliveTime / 2,
			PermitWithoutStream: true,
		}),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    keepaliveTime,
			Timeout: keepaliveTimeout,
		}),
	)

	adapterSrv := adapter.New(version)
	adapterSrv.WorkspaceRoot = *workspaceRoot
	// §6.4 lines 385-409: the per-slot `slots/{slotId}` workspace and
	// `/artifacts/{slotId}` trees nest under the parent of --workspace-root
	// (production /workspace) and --artifacts-root (production /artifacts).
	// These roots feed slotlayout.Resolve, which gates per-slot tree
	// construction on each being non-empty; they are set unconditionally
	// so a slotId-bearing frame always materializes its per-slot tree.
	adapterSrv.WorkspaceBase = filepath.Dir(*workspaceRoot)
	adapterSrv.ArtifactsRoot = *artifactsRoot
	adapterSrv.SessionsRoot = *sessionsRoot
	adapterSrv.StagingDir = *stagingDir
	// spec: §10.1 lines 47-52 — the coordinator-loss hold window and the
	// disk post-mortem path the adapter falls back to when no coordinator
	// returns to receive the AdapterTerminating event.
	adapterSrv.CoordinatorHoldTimeout = time.Duration(*coordinatorHoldTimeoutSec) * time.Second
	adapterSrv.PostMortemDir = *postMortemDir
	// spec: §15.4.1 lines 1442/1826 — production sidecar probes runtime
	// liveness with heartbeats and SIGTERMs a process that misses the ack
	// window. A zero interval disables the probe.
	adapterSrv.HeartbeatInterval = time.Duration(*heartbeatIntervalSec) * time.Second
	adapterSrv.HeartbeatAckTimeout = time.Duration(*heartbeatAckTimeoutSec) * time.Second
	// §6.4 line 409: decode the inline shared-asset set the controller
	// rendered onto --shared-assets so EnsureWarmWorkspaceLayout can
	// materialize it into the read-only /workspace/shared tree.
	adapterSrv.SharedAssetsDir = *sharedAssetsDir
	parsedShared, err := sharedassets.Decode(*sharedAssets)
	if err != nil {
		log.Fatalf("lenny-adapter: %v", err)
	}
	adapterSrv.SharedAssets = parsedShared
	// §6.1: /workspace/current and the staging area must exist before the
	// pod is claimed. The pod spec mounts one emptyDir at /workspace, so
	// create the subdirectories at startup rather than lazily at claim time.
	// §6.4: the same step materializes /workspace/shared read-only.
	if err := adapterSrv.EnsureWarmWorkspaceLayout(); err != nil {
		log.Fatalf("lenny-adapter: %v", err)
	}
	adapterSrv.RuntimeUID = resolveRuntimeUID(*runtimeUID)
	// §4.7 lines 879-883: in nonce-only mode the platform MCP server adds
	// the per-connection challenge-response supplement to the static nonce.
	adapterSrv.NonceOnlyMode = !*requireSoPeercred
	// §9.1 lines 8-31: bind the platform MCP socket and, when a gateway
	// address is configured, dial the gateway's GatewayControl service so
	// the platform MCP server forwards a type:agent runtime's tools/list and
	// tools/call (and the §9.3 per-connector tool calls) to the gateway.
	// F-9.1.1.
	gwCloser, err := adapterSrv.ConnectGateway(*mcpSocket, *gatewayGRPCAddr, *certFile, *keyFile, *clientCAFile)
	if err != nil {
		log.Fatalf("lenny-adapter: %v", err)
	}
	defer func() { _ = gwCloser.Close() }()
	if *gatewayGRPCAddr != "" {
		log.Printf("lenny-adapter: §9.1 platform tool forwarding to gateway at %s", *gatewayGRPCAddr)
	}
	adapterSrv.CredentialsDir = *credentialsDir
	// §15.4: the adapter manifest is written into /run/lenny alongside
	// the credential file.
	adapterSrv.ManifestDir = *credentialsDir
	// §4.7: the manifest's observability object points an OTel-emitting
	// runtime at the deployment's OTLP collector.
	adapterSrv.OTLPEndpoint = *otlpEndpoint
	adapterSrv.OTLPTLSDisabled = *otlpTLSDisabled
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
		// §6.1 line 67 — SIGTERM during sdk_connecting tears the pre-connected
		// SDK down within LENNY_DEMOTE_TIMEOUT_SECONDS (default 5s),
		// force-terminating it on overrun, so it is not abandoned
		// mid-connection and cannot leak credentials or hold provider
		// connections open. A no-op for a pod-warm pod.
		adapterSrv.ShutdownDemoteSDK(adapter.DemoteTimeoutFromEnv())
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

// envIntOr returns the integer value of the named environment variable,
// or def when the variable is unset or not parseable. spec: §11.3.
func envIntOr(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Printf("lenny-adapter: ignoring unparseable %s=%q", name, v)
		return def
	}
	return n
}
