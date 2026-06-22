// SPDX-License-Identifier: MIT

// Package stack orchestrates the §17.4 Embedded Mode single-binary
// local stack. It brings up the embedded backends — Postgres, Redis,
// the dev OIDC provider, self-signed TLS, and the embedded Kubernetes
// layer — and starts the production gateway and controllers
// configured against them.
//
// Embedded Mode uses the production gateway, controllers, CRDs, and
// storage interfaces. Within a host, the driver selection differs: the
// embedded backends are reached through the same configuration surface a
// cluster deployment uses (the gateway's --postgres-dsn, --redis-url,
// and dev-mode flags). There are no mode-dependent code splits in
// platform business logic; this package is the orchestration layer
// outside that business logic. The embedded Kubernetes substrate is
// provisioned per host operating system (a managed k3s child process on
// Linux, a Docker-backed k3s container on macOS and Windows), and that
// provisioning is confined to the substrate layer below the gateway,
// controllers, CRDs, and storage interfaces, which stay identical across
// operating systems.
//
// spec: §17.4 (the embedded Kubernetes substrate is provisioned per host
// operating system and stays identical above the substrate layer).
package stack

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/lennylabs/lenny/pkg/embedded/k3s"
	"github.com/lennylabs/lenny/pkg/embedded/oidc"
	"github.com/lennylabs/lenny/pkg/embedded/postgres"
	"github.com/lennylabs/lenny/pkg/embedded/redis"
	"github.com/lennylabs/lenny/pkg/embedded/tlsgen"
)

// ProductionWarningBanner is the §17.4 non-suppressible banner lenny
// up prints on every invocation.
const ProductionWarningBanner = "Embedded Mode. NOT for production use. " +
	"Credentials, KMS master key, and identities are insecure."

// Default loopback ports for the §17.4 Embedded Mode listeners.
const (
	defaultHTTPPort     = 8080
	defaultHTTPSPort    = 8443
	defaultPostgresPort = 15433
	defaultK3sAPIPort   = 6443
	// defaultGatewayGRPCPort is the host port the gateway's §8.6/§9.1
	// GatewayControl listener binds. In-cluster agent-pod adapters dial it
	// to forward platform tool calls and ExtendLease across the host/Docker
	// boundary; the controller stamps the launcher's externally-reachable
	// address (GatewayHost():this-port) onto pods. spec: §4.7, §8.6.
	defaultGatewayGRPCPort = 50061
)

// Config configures an Up invocation.
type Config struct {
	// Root is the Embedded Mode state directory. Empty resolves to the
	// default (~/.lenny, or LENNY_HOME).
	Root string
	// HTTPPort and HTTPSPort are the gateway's plaintext and
	// TLS-terminated loopback ports. Zero uses the §17.4 defaults
	// (8080 and 8443).
	HTTPPort  int
	HTTPSPort int
	// GatewayBin and ControllerBin are the paths to the production
	// gateway and controller binaries. Empty triggers discovery
	// alongside the running lenny binary and on PATH.
	GatewayBin    string
	ControllerBin string
	// EchoTarball overrides the path to the pre-built echo-embedded
	// docker-save tarball the bring-up imports into the embedded
	// containerd (the LENNY_ECHO_TARBALL operator override). Empty
	// triggers discovery alongside the running lenny binary, where the
	// tarball ships. spec: §24.19.1 (the --file import path).
	EchoTarball string
	// Out receives human-readable progress output. Nil discards it.
	Out io.Writer
}

// Stack is a running Embedded Mode stack. Up returns it; the caller
// uses Stop to tear it down.
type Stack struct {
	paths   Paths
	pg      *postgres.Instance
	rd      *redis.Server
	idp     *oidc.Provider
	k3s     k3s.Launcher
	tls     tlsgen.Material
	proxy   *tlsProxy
	gateway *managedProcess
	control *managedProcess
	state   State
	out     io.Writer
	// gwSpec and ctlSpec retain the child-process specs so the
	// supervisor can re-spawn a single component on a §24.19 restart
	// request without tearing the rest of the stack down. ctlSpec.BinPath
	// is empty when the controller did not start.
	gwSpec  gatewaySpec
	ctlSpec ControllerSpec
}

// Up brings up the Embedded Mode stack. It is idempotent: when a stack
// is already recorded as running and its gateway responds, Up reports
// the running stack and returns without starting a second one.
//
// Up performs these steps in order: resolve the state directory,
// generate self-signed TLS, start embedded Postgres, run schema
// migrations, start embedded Redis, start the embedded OIDC provider,
// start the embedded Kubernetes layer when the host supports it, start
// the production gateway against the embedded backends, start the
// production controllers, install the §26 reference runtimes, and
// record the running stack.
func Up(ctx context.Context, cfg Config) (*Stack, error) {
	out := cfg.Out
	if out == nil {
		out = io.Discard
	}
	// The production warning banner is non-suppressible: it prints on
	// every lenny up before any other output.
	fmt.Fprintf(out, "\n  %s\n\n", ProductionWarningBanner)

	root := cfg.Root
	if root == "" {
		r, err := DefaultRoot()
		if err != nil {
			return nil, err
		}
		root = r
	}
	paths := NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		return nil, err
	}

	httpPort := cfg.HTTPPort
	if httpPort == 0 {
		httpPort = defaultHTTPPort
	}
	httpsPort := cfg.HTTPSPort
	if httpsPort == 0 {
		httpsPort = defaultHTTPSPort
	}
	httpAddr := fmt.Sprintf("127.0.0.1:%d", httpPort)
	httpsAddr := fmt.Sprintf("127.0.0.1:%d", httpsPort)

	// Idempotency: if a recorded stack's gateway still answers, this
	// invocation is a no-op.
	if prev, ok, err := readState(paths.StateFile()); err == nil && ok {
		if processAlive(prev.GatewayPID) && gatewayHealthy(ctx, "http://"+prev.HTTPAddr) {
			fmt.Fprintf(out, "lenny up: stack already running (gateway pid %d, %s)\n",
				prev.GatewayPID, "https://"+prev.HTTPSAddr)
			return &Stack{paths: paths, state: prev, out: out}, nil
		}
		// A stale state file: a previous stack did not shut down
		// cleanly. Clear it and bring a fresh stack up.
		_ = removeState(paths.StateFile())
	}

	s := &Stack{paths: paths, out: out}
	// On any failure after a component starts, tear down what came up
	// so a failed lenny up does not leak child processes.
	ok := false
	defer func() {
		if !ok {
			_ = s.Stop(context.Background())
		}
	}()

	// ----- Self-signed TLS (rotated per lenny up) -----
	fmt.Fprintln(out, "lenny up: generating self-signed TLS material")
	mat, err := tlsgen.Generate(paths.TLS)
	if err != nil {
		return nil, err
	}
	s.tls = mat

	// ----- Embedded Postgres -----
	fmt.Fprintln(out, "lenny up: starting embedded Postgres (first run downloads the PostgreSQL 16 bundle)")
	pg := postgres.New(postgres.Config{
		DataDir:  paths.Postgres,
		Port:     defaultPostgresPort,
		Database: "lenny",
		Username: "lenny",
		Password: "lenny",
	})
	if err := pg.Start(); err != nil {
		return nil, err
	}
	s.pg = pg
	dsn := pg.DSN()

	// ----- Schema migrations -----
	fmt.Fprintln(out, "lenny up: applying schema migrations")
	if err := applyMigrations(dsn, out); err != nil {
		return nil, err
	}

	// ----- Embedded Redis -----
	fmt.Fprintln(out, "lenny up: starting embedded Redis")
	rd := redis.New()
	if err := rd.Start(); err != nil {
		return nil, err
	}
	s.rd = rd
	redisURL := rd.URL()

	// ----- Embedded OIDC provider -----
	// The signing key is persisted and rotated per lenny up (§17.4):
	// rotate=true generates a fresh key and writes it so lenny token
	// print, running in a separate process, mints tokens this stack
	// accepts.
	idp, err := oidc.NewWithPersistedKey(paths.OIDCKeyFile(), true)
	if err != nil {
		return nil, err
	}
	s.idp = idp

	// ----- Embedded Kubernetes layer -----
	// The substrate provisions on every host the launcher supports: a
	// managed k3s child process on Linux, a Docker-backed k3s container
	// under Docker Desktop's Linux VM on macOS and Windows. On those hosts
	// the CRDs install and the controllers run against the launcher's
	// (host-rewritten) kubeconfig, the same as on Linux; only the
	// substrate provisioning below the gateway/controllers/CRDs differs.
	// spec: §17.4 (the embedded Kubernetes substrate is provisioned per
	// host operating system, and stays identical above the substrate
	// layer).
	sub := s.provisionSubstrate(ctx, paths, cfg.EchoTarball, out)
	k3sEnabled := sub.enabled
	kubeconfig := sub.kubeconfig
	gatewayGRPCDialAddr := sub.gatewayGRPCDialAddr

	// ----- Production gateway -----
	fmt.Fprintln(out, "lenny up: starting the gateway")
	gwBin, err := resolveGatewayBin(cfg.GatewayBin)
	if err != nil {
		return nil, err
	}
	// The embedded OIDC provider persists its signing key in the
	// key-file format the gateway's --bearer-trust-hmac-key-file flag
	// reads. Pointing the gateway at it lets a `lenny token print`
	// bearer verify on the §10.2 Authorization header.
	s.gwSpec = gatewaySpec{
		BinPath:          gwBin,
		HTTPAddr:         httpAddr,
		PostgresDSN:      dsn,
		RedisURL:         redisURL,
		Kubeconfig:       kubeconfig,
		LogPath:          paths.Logs + "/gateway.log",
		OIDCKeyFile:      paths.OIDCKeyFile(),
		KMSMasterKeyFile: paths.KMSMasterKey(),
		ArtifactsDir:     paths.Artifacts,
	}
	// Bind the §8.6/§9.1 GatewayControl listener only when an embedded
	// cluster is up: it serves the in-cluster agent-pod adapter, so without
	// a cluster there is no adapter to serve. The listener binds all host
	// interfaces (an empty host in :<port>) so the in-cluster adapter under
	// the Docker VM reaches it across the host/Docker boundary; binding
	// loopback would make it unreachable from the Docker VM. The controller
	// stamps the matching substrate-specific dial host onto pods. spec:
	// §4.7, §8.6.
	if k3sEnabled {
		s.gwSpec.GRPCAddr = fmt.Sprintf(":%d", defaultGatewayGRPCPort)
		// §4.7 pod placement: point the gateway at the agent namespace so it
		// resolves the warm pool from there and routes every started session
		// onto a warm pod over the §4.7 adapter boundary instead of the
		// in-process echo executor. Set in this first k3sEnabled block, before
		// startGateway below, because the gateway consumes gwSpec at launch;
		// setting it in the later controller block would launch the gateway
		// without -agent-namespace and leave the activation inert. Left empty
		// when the substrate is down, keeping the in-process echo fallback.
		// spec: §17.4, §4.7.
		s.gwSpec.AgentNamespace = agentNamespace
	}
	gw, err := startGateway(s.gwSpec)
	if err != nil {
		return nil, err
	}
	s.gateway = gw

	if err := waitGatewayHealthy(ctx, "http://"+httpAddr, 60*time.Second); err != nil {
		return nil, err
	}

	// ----- TLS-terminating reverse proxy -----
	proxy, err := startTLSProxy(httpsAddr, "http://"+httpAddr, mat.CertPath, mat.KeyPath)
	if err != nil {
		return nil, err
	}
	s.proxy = proxy

	// ----- Production controllers (only with the embedded cluster) -----
	if k3sEnabled {
		fmt.Fprintln(out, "lenny up: starting the controllers")
		ctlBin, err := resolveControllerBin(cfg.ControllerBin)
		if err != nil {
			fmt.Fprintf(out, "lenny up: WARNING: controller binary not found: %v\n", err)
		} else {
			s.ctlSpec = ControllerSpec{
				BinPath:         ctlBin,
				PostgresDSN:     dsn,
				Kubeconfig:      kubeconfig,
				GatewayGRPCAddr: gatewayGRPCDialAddr,
				LogPath:         paths.Logs + "/controller.log",
				// §4.6.2/§5.1: point the controller at the same agent namespace
				// the gateway places into so the PoolScalingController (and the
				// mirror reconciler and claim GC) start and materialize the
				// seeded echo poolstore row into the SandboxTemplate/SandboxWarmPool
				// CRD pair the gateway claims an idle pod from. The embedded
				// controller is not given --dedicated-dns-cluster-ip, so the
				// seeded pool's dnsPolicy: cluster-default keeps the echo pod on
				// k3s kube-system CoreDNS. Both threads target the same namespace.
				// spec: §4.6.2, §5.1.
				AgentNamespaces: agentNamespace,
			}
			ctl, err := startController(s.ctlSpec)
			if err != nil {
				fmt.Fprintf(out, "lenny up: WARNING: controller did not start: %v\n", err)
			} else {
				s.control = ctl
			}
		}
	}

	// ----- §26 reference runtimes -----
	// Pass the import-time-resolved echo image reference so the bootstrap seed
	// registers the echo runtime under the same digest the applied Runtime CR
	// and the containerd image carry; an empty reference (substrate down or
	// import failed) leaves the echo seed on its sentinel placeholder, which
	// the gateway never places against because AgentNamespace is unset too.
	// spec: §15.4.4 (echo exemplar), §4.7 (digest-pinned pod image).
	fmt.Fprintln(out, "lenny up: installing reference runtimes")
	if err := installReferenceRuntimes(ctx, "http://"+httpAddr, sub.echoImageRef, out); err != nil {
		fmt.Fprintf(out, "lenny up: WARNING: reference-runtime install incomplete: %v\n", err)
	}

	// ----- Record the running stack -----
	// stack.Up runs inside the detached supervisor process, so the
	// supervisor PID is this process's PID.
	st := State{
		StartedAt:      time.Now().UTC(),
		SupervisorPID:  os.Getpid(),
		GatewayPID:     gw.PID(),
		HTTPAddr:       httpAddr,
		HTTPSAddr:      httpsAddr,
		PostgresDSN:    dsn,
		RedisURL:       redisURL,
		KubeconfigPath: kubeconfig,
		K3sEnabled:     k3sEnabled,
	}
	if s.control != nil {
		st.ControllerPID = s.control.PID()
	}
	if s.k3s != nil {
		// The Linux launcher records a host PID; the Docker-backed
		// launcher records its container name instead, because its k3s
		// runs inside the Docker VM with no host PID. lenny status probes
		// whichever handle is set. spec: §24.19.
		st.K3sPID = s.k3s.PID()
		st.K3sContainer = k3sContainerHandle(s.k3s)
	}
	if err := writeState(paths.StateFile(), st); err != nil {
		return nil, err
	}
	s.state = st

	fmt.Fprintf(out, "\nlenny up: stack ready\n")
	fmt.Fprintf(out, "  gateway   https://localhost:%d  (http://localhost:%d)\n", httpsPort, httpPort)
	fmt.Fprintf(out, "  TLS CA    %s\n", mat.CACertPath)
	fmt.Fprintf(out, "  token     run 'lenny token print' for a bearer for the built-in user\n")
	ok = true
	return s, nil
}

// Stop tears the stack down in reverse dependency order. It is safe to
// call on a partially started Stack: each component stop is a no-op
// when that component did not start.
func (s *Stack) Stop(ctx context.Context) error {
	var firstErr error
	record := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.proxy != nil {
		record(s.proxy.Shutdown(ctx))
	}
	if s.control != nil {
		record(s.control.Stop())
	}
	if s.gateway != nil {
		record(s.gateway.Stop())
	}
	if s.k3s != nil {
		record(s.k3s.Stop())
	}
	if s.rd != nil {
		s.rd.Stop()
	}
	if s.pg != nil {
		record(s.pg.Stop())
	}
	return firstErr
}

// State returns the recorded state of the running stack.
func (s *Stack) State() State { return s.state }

// CACertPath returns the path of the self-signed CA certificate for
// the running stack.
func (s *Stack) CACertPath() string { return s.tls.CACertPath }

// OIDC returns the embedded OIDC provider for the running stack.
func (s *Stack) OIDC() *oidc.Provider { return s.idp }

// substrateResult is the outcome of provisionSubstrate: whether the
// embedded cluster came up, the admin kubeconfig path, the §4.7
// gateway↔adapter callback address the controller stamps onto agent pods,
// and the import-time-resolved echo runtime image reference.
type substrateResult struct {
	enabled             bool
	kubeconfig          string
	gatewayGRPCDialAddr string
	// echoImageRef is the digest-pinned echoImageRepository@sha256:<digest>
	// reference the bring-up resolved when it imported the echo-embedded
	// tarball into the embedded containerd. It is empty when the substrate
	// did not come up or the import failed, in which case the gateway keeps
	// the in-process echo executor. S6 injects it into the bootstrap seed
	// (overwriting echoRuntime.Image) and the applied echo Runtime CRD.
	// spec: §24.19.1 (the --file import path), §4.7 (digest-pinned pod image).
	echoImageRef string
}

// Substrate-provisioning seams. They default to the real k3s launcher and
// CRD install and are package-level vars so a unit test can substitute a
// fake launcher and assert the per-OS substrate-selection logic without
// downloading and running real k3s, mirroring the runDocker injection on
// the Docker-backed launcher. spec: §17.4 (the substrate is provisioned per
// host operating system; the gateway/controllers/CRDs above it stay
// identical).
var (
	substrateSupported   = k3s.SupportedPlatform
	newSubstrate         = k3s.New
	installSubstrateCRDs = InstallCRDs
	// removeSubstrateContainer force-removes the Docker-backed k3s container
	// by its recorded handle. lenny down calls it on the crashed-supervisor
	// teardown path and lenny down --purge calls it before discarding the
	// state directory that holds the handle, so neither orphans the container
	// the graceful Stack.Stop path would have removed. It is a package-level
	// var so a unit test can assert the teardown removes the recorded
	// container without invoking a real docker. spec: §24.19 (a crashed
	// supervisor must not leak the Docker-backed k3s container).
	removeSubstrateContainer = k3s.RemoveContainer
	// importEchoRuntimeImageFn is the bring-up echo-image import seam. It
	// defaults to the real (*Stack).importEchoRuntimeImage and is a
	// package-level var so a unit test can drive the §4.7 activation sequence
	// in provisionSubstrate (namespace create, import, Runtime-CR apply) with a
	// controllable resolved digest, without a live ctr or containerd. spec:
	// §24.19.1 (the --file import path), §4.7.
	importEchoRuntimeImageFn = (*Stack).importEchoRuntimeImage
)

// provisionSubstrate brings the embedded Kubernetes substrate up on every
// host the launcher supports: a managed k3s child process on Linux, a
// Docker-backed k3s container under Docker Desktop's Linux VM on macOS and
// Windows. It installs the CRDs against the launcher's (host-rewritten)
// kubeconfig and computes the §4.7 gateway↔adapter callback address from the
// launcher's substrate-specific host. The OS branch is confined to this
// provisioning layer; the gateway, controllers, CRDs, and storage
// interfaces above it are byte-identical across operating systems. A
// substrate that fails to start is routed around: the storage and identity
// paths still come up and lenny status reports the degraded state.
//
// spec: §17.4 (the embedded Kubernetes substrate is provisioned per host
// operating system, and stays identical above the substrate layer), §4.7,
// §8.6, §9.1.
func (s *Stack) provisionSubstrate(ctx context.Context, paths Paths, echoTarball string, out io.Writer) substrateResult {
	if !substrateSupported() {
		// On a non-Linux host the embedded k3s runs under Docker Desktop's
		// Linux VM, so the platform is unsupported only when Docker is
		// absent. spec: §17.4 (Docker Desktop is the macOS/Windows
		// prerequisite that supplies the Linux kernel the embedded k3s needs).
		fmt.Fprintf(out, "lenny up: embedded Kubernetes is unavailable on this host "+
			"(macOS and Windows require Docker Desktop to run the embedded k3s under its Linux VM); "+
			"the gateway, stores, and identity provider still come up\n")
		return substrateResult{}
	}
	fmt.Fprintln(out, "lenny up: starting embedded Kubernetes (k3s)")
	sup := newSubstrate(k3s.Config{Dir: paths.K3s, APIPort: defaultK3sAPIPort})
	if err := sup.Start(ctx); err != nil {
		// k3s is the §17.4 component most likely to fail on a constrained
		// host. Route around it: the storage and identity paths still come
		// up. lenny status reports the degraded state.
		fmt.Fprintf(out, "lenny up: WARNING: embedded Kubernetes did not start: %v\n", err)
		fmt.Fprintln(out, "lenny up: continuing without the embedded cluster; session placement is unavailable")
		return substrateResult{}
	}
	s.k3s = sup
	res := substrateResult{
		enabled:    true,
		kubeconfig: sup.KubeconfigPath(),
		// Compute the gateway's externally-reachable gRPC address from the
		// launcher's substrate-specific host. The §4.7 placement and adapter
		// business logic above this point is unaware of the substrate; only
		// this provisioning layer branches per OS.
		gatewayGRPCDialAddr: gatewayGRPCAddr(sup.GatewayHost(), defaultGatewayGRPCPort),
	}
	if err := installSubstrateCRDs(ctx, res.kubeconfig); err != nil {
		fmt.Fprintf(out, "lenny up: WARNING: CRD install failed: %v\n", err)
	}
	// Create the agent namespace the gateway places into and the
	// PoolScalingController materializes the seeded pool CRDs into. Both the
	// gateway's -agent-namespace and the controller's --agent-namespaces are
	// set to this namespace (in Up's two k3sEnabled blocks), so it must exist
	// before either places. A create failure warns rather than aborts: the
	// controllers create namespaced resources lazily, but a missing namespace
	// would leave placement inert, so the bring-up surfaces it. spec: §4.6.2
	// (the pool CRDs materialize in the agent namespace), §5.1.
	if err := ensureAgentNamespaceFn(ctx, res.kubeconfig, agentNamespace); err != nil {
		fmt.Fprintf(out, "lenny up: WARNING: agent namespace create failed: %v\n", err)
	}
	// Import the pre-built echo-embedded image into the embedded containerd
	// and record the import-time-resolved digest. The import runs after the
	// substrate is up (so containerd is reachable) and before the Runtime-CR
	// apply and bootstrap seed, so the resolved digest-pinned reference is
	// available to both, gated on the substrate coming up alone (no separate
	// runnable-image precondition: the tarball ships with the binary). A
	// failed import leaves echoImageRef empty; the gateway then keeps the
	// in-process echo executor. spec: §24.19.1 (the --file import path), §17.4
	// (Embedded Mode bring-up), §4.7 (digest-pinned pod image).
	res.echoImageRef = importEchoRuntimeImageFn(s, paths.Root, echoTarball, out)
	// Apply the cluster-scoped echo Runtime CR carrying the import-time-resolved
	// digest and deploymentModel: embedded. The Sandbox controller resolves the
	// runtime from a Runtime CR by name, so without this the seeded registry
	// record and warm pool leave the warm pod failing to render. It runs only
	// when the import resolved a digest: an empty echoImageRef means the gateway
	// keeps the in-process echo executor, so applying a CR carrying a sentinel
	// digest that no containerd image matches would only ImagePullBackOff. The
	// seeded digest, the CR digest, and the containerd image digest are
	// identical because all three resolve from the same imported image. spec:
	// §4.7 (embedded deployment model), §5.1 (Runtime CR).
	if res.echoImageRef != "" {
		fmt.Fprintln(out, "lenny up: applying the echo runtime CR")
		if err := applyEchoRuntimeCRFn(ctx, res.kubeconfig, res.echoImageRef); err != nil {
			fmt.Fprintf(out, "lenny up: WARNING: echo runtime CR apply failed: %v\n", err)
		}
	}
	return res
}

// importEchoRuntimeImage imports the pre-built echo-embedded tarball into
// the embedded containerd and returns the import-time-resolved
// digest-pinned image reference, or the empty string when the import
// cannot run (no tarball, unreachable containerd, or a failed import). It
// resolves the ctr invocation from the live launcher's substrate handle
// rather than the recorded stack state, because the state file is not
// written until the end of Up. A failed import is non-fatal: it is logged
// and the empty return leaves the gateway on the in-process echo executor,
// mirroring the substrate-unavailable degraded path.
//
// spec: §24.19.1 (the --file import path), §17.4 (Embedded Mode bring-up
// per host operating system), §4.7 (the digest-pinned embedded pod image).
func (s *Stack) importEchoRuntimeImage(root, echoTarball string, out io.Writer) string {
	tarball, err := resolveEchoTarball(echoTarball)
	if err != nil {
		fmt.Fprintf(out, "lenny up: WARNING: echo runtime image not imported: %v\n", err)
		return ""
	}
	ctr, code := CtrCommandForSubstrate(root, k3sContainerHandle(s.k3s), out)
	if code != 0 {
		// CtrCommandForSubstrate already wrote a K3S_UNAVAILABLE diagnostic.
		fmt.Fprintln(out, "lenny up: WARNING: echo runtime image not imported; the embedded containerd is unreachable")
		return ""
	}
	fmt.Fprintln(out, "lenny up: importing the echo runtime image into the embedded containerd")
	ref, err := importEchoImage(ctr, tarball, out, out)
	if err != nil {
		fmt.Fprintf(out, "lenny up: WARNING: echo runtime image import failed: %v\n", err)
		return ""
	}
	fmt.Fprintf(out, "lenny up: echo runtime image imported as %s\n", ref)
	return ref
}

// gatewayHealthy reports whether the gateway at baseURL answers its
// liveness probe.
func gatewayHealthy(ctx context.Context, baseURL string) bool {
	return probeHealthz(ctx, baseURL) == nil
}

// gatewayGRPCAddr joins the substrate launcher's gateway host and the
// gateway gRPC host port into the §8.6/§9.1 GatewayControl address the
// controller stamps onto agent pods. The host comes from the launcher
// (GatewayHost): 127.0.0.1 on the Linux child-process launcher, where the
// gateway and pods share the host, and host.docker.internal on the
// Docker-backed launcher, where pods run inside the Docker VM and reach
// the host gateway through that alias. Confining the substrate branch to
// the host the launcher returns keeps the §4.7 pod-spec/adapter business
// logic substrate-agnostic. net.JoinHostPort is used so the address is
// well-formed for any host form. The function is pure so it is unit-tested
// directly. spec: §4.7, §8.6, §9.1, §17.4.
func gatewayGRPCAddr(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// k3sContainerHandle returns the docker container name a Docker-backed
// k3s launcher runs under, or "" for the Linux managed-child-process
// launcher (which records a host PID instead). The launcher exposes the
// handle through an optional ContainerName method; the type assertion
// keeps the substrate-specific container knowledge out of the k3s
// Launcher interface, which the Linux launcher would otherwise have to
// stub.
//
// spec: §24.19 (a container-backed launcher records a container handle
// where there is no host PID).
func k3sContainerHandle(l k3s.Launcher) string {
	if c, ok := l.(interface{ ContainerName() string }); ok {
		return c.ContainerName()
	}
	return ""
}

// purgeRoot removes the entire Embedded Mode state directory. lenny
// down --purge calls it.
func purgeRoot(root string) error {
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("embedded: purge %s: %w", root, err)
	}
	return nil
}
