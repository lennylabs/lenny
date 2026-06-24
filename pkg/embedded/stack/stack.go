// SPDX-License-Identifier: MIT

// Package stack orchestrates the §17.4 Embedded Mode single-binary local
// stack. It provisions the embedded Kubernetes substrate, imports the
// component images, applies the chart-rendered dev-profile manifests, and
// exposes the in-cluster gateway to the lenny CLI through a loopback-only
// host-side forwarder.
//
// Embedded Mode runs the production gateway and controllers as pods inside
// the embedded Kubernetes cluster, rendered from the production chart under
// a development profile, so the gateway places each session on an agent pod
// over the §4.7 adapter boundary exactly as it does in a production cluster.
// The development profile and the per-host substrate provisioning (a managed
// k3s child process on Linux, a Docker-backed k3s container on macOS and
// Windows) are confined to the layer below the gateway and controllers,
// which run from their unmodified production images, so there are no
// mode-dependent code splits in platform business logic.
//
// spec: §17.4 (the control plane runs as in-cluster pods rendered from the
// chart; the embedded Kubernetes substrate is provisioned per host operating
// system and stays identical above the substrate layer).
package stack

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"

	"github.com/lennylabs/lenny/pkg/embedded/k3s"
	"github.com/lennylabs/lenny/pkg/embedded/tlsgen"
)

// ProductionWarningBanner is the §17.4 non-suppressible banner lenny
// up prints on every invocation.
const ProductionWarningBanner = "Embedded Mode. NOT for production use. " +
	"Credentials, KMS master key, and identities are insecure."

// Default loopback ports for the §17.4 Embedded Mode listeners.
const (
	defaultHTTPPort   = 8080
	defaultHTTPSPort  = 8443
	defaultK3sAPIPort = 6443
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
	// HTTPPort and HTTPSPort are the host-side forwarder's plaintext and
	// TLS-terminated loopback ports. Zero uses the §17.4 defaults
	// (8080 and 8443).
	HTTPPort  int
	HTTPSPort int
	// EchoTarball overrides the path to the pre-built echo-embedded
	// docker-save tarball the bring-up imports into the embedded
	// containerd (the LENNY_ECHO_TARBALL operator override). Empty
	// triggers discovery alongside the running lenny binary, where the
	// tarball ships. spec: §24.19.1 (the --file import path).
	EchoTarball string
	// Out receives human-readable progress output. Nil discards it.
	Out io.Writer
}

// Stack is a running Embedded Mode stack. Up returns it; the caller uses
// Stop to tear it down. The §17.4 control plane runs as in-cluster pods, so
// the stack holds the substrate launcher, the self-signed TLS material, and
// the host-side TLS forwarder rather than host process handles.
type Stack struct {
	paths Paths
	k3s   k3s.Launcher
	tls   tlsgen.Material
	proxy *tlsProxy
	state State
	out   io.Writer
}

// errBringUpNotWired marks the in-cluster bring-up sequence that replaces
// the removed host-process control plane. S6 removes the host-process legs
// (the embedded Postgres/Redis/OIDC backends and the host gateway and
// controller processes) and the in-cluster bring-up (image import,
// manifest apply, host-side forwarder, and readiness wait) lands in the
// next build step (proposal 0017 C2). Up provisions the substrate so the
// substrate-selection path stays exercised, then returns this sentinel
// until the in-cluster legs are wired.
var errBringUpNotWired = errors.New("embedded: the in-cluster bring-up is not yet wired (proposal 0017 C2)")

// Up brings up the §17.4 Embedded Mode stack. The control plane runs as
// in-cluster pods rendered from the production chart under a development
// profile: lenny up provisions the substrate, imports the component images,
// applies the embedded manifests, starts the host-side gateway forwarder,
// and waits for readiness.
//
// The host-process control plane (embedded Postgres, Redis, the dev OIDC
// provider, and the host gateway and controller processes) is removed; the
// in-cluster bring-up sequence lands in the next build step (proposal 0017
// C2). Up provisions the substrate and generates the self-signed TLS
// material here, then returns errBringUpNotWired until those legs are wired.
//
// spec: §17.4 (the control plane runs as in-cluster pods rendered from the
// chart).
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

	s := &Stack{paths: paths, out: out}
	// On any failure after a component starts, tear down what came up so a
	// failed lenny up does not leak the substrate.
	ok := false
	defer func() {
		if !ok {
			_ = s.Stop(context.Background())
		}
	}()

	// ----- Self-signed TLS (rotated per lenny up) -----
	// The host-side forwarder terminates TLS on 127.0.0.1:8443 with this
	// leaf; the in-cluster gateway serves plaintext HTTP behind it.
	fmt.Fprintln(out, "lenny up: generating self-signed TLS material")
	mat, err := tlsgen.Generate(paths.TLS)
	if err != nil {
		return nil, err
	}
	s.tls = mat

	// ----- Embedded Kubernetes layer -----
	// The substrate provisions on every host the launcher supports: a
	// managed k3s child process on Linux, a Docker-backed k3s container
	// under Docker Desktop's Linux VM on macOS and Windows. The CRDs
	// install and the in-cluster control plane runs against the launcher's
	// (host-rewritten) kubeconfig; only the substrate provisioning below the
	// gateway/controllers/CRDs differs per host. spec: §17.4.
	s.provisionSubstrate(ctx, paths, cfg.EchoTarball, out)

	// The image import, the manifest apply, the host-side forwarder, and
	// the readiness wait that replace the host-process legs land in the next
	// build step (proposal 0017 C2).
	return nil, errBringUpNotWired
}

// Stop tears the stack down in reverse dependency order. It is safe to
// call on a partially started Stack: each component stop is a no-op when
// that component did not start. The §17.4 control plane runs as in-cluster
// pods, so Stop shuts the host-side forwarder and the substrate launcher;
// the in-cluster pods stop with the substrate.
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
	if s.k3s != nil {
		record(s.k3s.Stop())
	}
	return firstErr
}

// State returns the recorded state of the running stack.
func (s *Stack) State() State { return s.state }

// CACertPath returns the path of the self-signed CA certificate for
// the running stack.
func (s *Stack) CACertPath() string { return s.tls.CACertPath }

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
	// did not come up or, with the substrate up, the import failed. Only the
	// substrate-down case keeps the gateway on the in-process echo executor;
	// with k3s up AgentNamespace stays set and the gateway routes through the
	// §4.7 pod path regardless. S6 injects it into the bootstrap seed
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
// provisioning layer; the in-cluster gateway, controllers, CRDs, and storage
// interfaces above it are byte-identical across operating systems. A
// substrate that does not come up makes the in-cluster control plane
// unavailable; the §17.4 bring-up reports the substrate failure (proposal
// 0017 C2).
//
// spec: §17.4 (the embedded Kubernetes substrate is provisioned per host
// operating system, and stays identical above the substrate layer; an
// unavailable substrate makes lenny up report the failure), §4.7, §8.6,
// §9.1.
func (s *Stack) provisionSubstrate(ctx context.Context, paths Paths, echoTarball string, out io.Writer) substrateResult {
	if !substrateSupported() {
		// On a non-Linux host the embedded k3s runs under Docker Desktop's
		// Linux VM, so the platform is unsupported only when Docker is
		// absent. spec: §17.4 (Docker Desktop is the macOS/Windows
		// prerequisite that supplies the Linux kernel the embedded k3s needs).
		fmt.Fprintf(out, "lenny up: embedded Kubernetes is unavailable on this host "+
			"(macOS and Windows require Docker Desktop to run the embedded k3s under its Linux VM)\n")
		return substrateResult{}
	}
	fmt.Fprintln(out, "lenny up: starting embedded Kubernetes (k3s)")
	sup := newSubstrate(k3s.Config{Dir: paths.K3s, APIPort: defaultK3sAPIPort})
	if err := sup.Start(ctx); err != nil {
		// k3s is the §17.4 component most likely to fail on a constrained
		// host. The in-cluster control plane runs on it, so without the
		// substrate the bring-up reports the failure (proposal 0017 C2).
		fmt.Fprintf(out, "lenny up: WARNING: embedded Kubernetes did not start: %v\n", err)
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
	// Install the §5.3 `runc` RuntimeClass the seeded echo pool's `standard`
	// isolation profile resolves to. A bare k3s cluster ships no RuntimeClass
	// objects, so without it the WarmPoolController fails to render the echo pod
	// ("runtimeclass \"runc\" not found") and placement stays inert. The handler
	// is k3s/containerd's built-in `runc`, so no out-of-band runtime is needed.
	// spec: §5.3 (standard->runc), §17.4 (Embedded Mode provisions the substrate).
	if err := ensureRuntimeClassFn(ctx, res.kubeconfig, runcRuntimeClassName, runcRuntimeClassName); err != nil {
		fmt.Fprintf(out, "lenny up: WARNING: runtimeclass install failed: %v\n", err)
	}
	// Import the pre-built echo-embedded image into the embedded containerd
	// and record the import-time-resolved digest. The import runs after the
	// substrate is up (so containerd is reachable) and before the Runtime-CR
	// apply and bootstrap seed, so the resolved digest-pinned reference is
	// available to both, gated on the substrate coming up alone (no separate
	// runnable-image precondition: the tarball ships with the binary). A
	// failed import leaves echoImageRef empty, but with k3s up AgentNamespace
	// stays set, so the gateway routes through the §4.7 pod path and the echo
	// session fails to start rather than falling back to the in-process echo
	// executor. spec: §24.19.1 (the --file import path), §17.4
	// (Embedded Mode bring-up), §4.7 (digest-pinned pod image).
	res.echoImageRef = importEchoRuntimeImageFn(s, paths.Root, echoTarball, out)
	// Apply the cluster-scoped echo Runtime CR carrying the import-time-resolved
	// digest and deploymentModel: embedded. The Sandbox controller resolves the
	// runtime from a Runtime CR by name, so without this the seeded registry
	// record and warm pool leave the warm pod failing to render. It runs only
	// when the import resolved a digest: an empty echoImageRef means no echo
	// image reached containerd, so applying a CR carrying a sentinel
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
// and returns the empty string; the caller gates the Runtime-CR apply on a
// non-empty return. With the substrate up AgentNamespace stays set, so the
// gateway routes through the §4.7 pod path and echo sessions fail to start
// rather than falling back to the in-process echo executor.
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
