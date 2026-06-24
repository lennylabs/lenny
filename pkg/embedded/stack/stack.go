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
	"time"

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
	// CLIVersion is the running lenny CLI build version. A warm lenny up
	// compares it against the deployed image tag the prior bring-up recorded
	// and re-imports and re-applies the embedded manifests on a mismatch, so
	// a stale image is not run after a lenny upgrade (C4). Empty (a source
	// build's "dev") still reconciles against a recorded non-empty tag.
	// spec: §17.4 (an upgrade re-imports and re-applies on a CLI-version /
	// image-tag mismatch).
	CLIVersion string
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

// errSubstrateUnavailable marks a bring-up where the embedded Kubernetes
// substrate did not come up. The §17.4 control plane runs as in-cluster
// pods, so without the substrate there is no gateway to start: lenny up
// reports the substrate failure and the gateway does not start, rather than
// degrading to an in-process executor (S1). spec: §17.4.
var errSubstrateUnavailable = errors.New("embedded: the Kubernetes substrate did not come up; the in-cluster gateway cannot start")

// httpHost binds the host-side forwarder's plaintext relay and TLS
// terminator to loopback only, so the §17.4 EMBEDDED_MODE_LOCAL_ONLY
// fail-closed constraint holds (the forwarder rejects a non-loopback bind).
const httpHost = "127.0.0.1"

// Up brings up the §17.4 Embedded Mode stack. The control plane runs as
// in-cluster pods rendered from the production chart under a development
// profile: lenny up provisions the substrate, imports the deduplicated
// platform image bundle (overlapping the import with the apply of the
// non-image objects), creates the dev bearer-trust Secret, applies the
// embedded manifests, seeds the echo runtime (its registry record, Runtime
// CR, and its SandboxTemplate/SandboxWarmPool pair), starts the host-side
// gateway forwarder, and waits for the gateway Deployment to report Ready.
//
// The host-process control plane (embedded Postgres, Redis, the dev OIDC
// provider, and the host gateway and controller processes) is removed. An
// unavailable substrate makes lenny up report the substrate failure (S1):
// there is no host gateway to fall back to.
//
// spec: §17.4 (the control plane runs as in-cluster pods rendered from the
// chart), §4.6.2 (the echo pool materializes directly without Postgres),
// §10.2 (dev bearer trust).
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

	// ----- Dev bearer-trust signing key -----
	// Persist the dev HMAC key the CLI mints its bearer from before the
	// gateway pod is applied, so the Secret created below carries the key
	// the in-cluster gateway must trust. spec: §17.4 (dev bearer trust).
	keyFile := paths.OIDCKeyFile()
	if err := ensureDevBearerKey(keyFile); err != nil {
		return nil, err
	}

	// ----- Embedded Kubernetes layer -----
	// The substrate starts on every host the launcher supports: a managed k3s
	// child process on Linux, a Docker-backed k3s container under Docker
	// Desktop's Linux VM on macOS and Windows. On a warm down→up cycle the
	// launcher restarts the persisted (stopped) substrate; on a first run it
	// provisions a fresh one. Only the substrate provisioning below the
	// gateway/controllers/CRDs differs per host. The substrate start is
	// unconditional because a warm reuse must still restart a stopped
	// substrate; the per-cluster-state legs (CRD install, agent namespace, runc
	// RuntimeClass, echo image import, echo Runtime CR apply) are deferred to
	// provisionClusterState, which a warm tag-match reuse skips. spec: §17.4.
	//
	// A persisted prior bring-up records the deployed image tag. lenny up reads
	// it before deciding so a warm bring-up can decide whether to re-import and
	// re-apply (a CLI-upgrade tag mismatch) or skip the expensive
	// import/apply/seed legs (a tag match against a healthy substrate). spec:
	// §17.4 (an upgrade re-imports and re-applies on a mismatch).
	priorState, _, _ := readState(paths.StateFile())

	res := s.startSubstrate(ctx, paths, out)
	if !res.enabled {
		// §17.4 S1: the gateway runs as an in-cluster pod, so an unavailable
		// substrate makes lenny up report the failure rather than degrade to
		// an in-process executor. startSubstrate already wrote the cause.
		return nil, errSubstrateUnavailable
	}

	// ----- Version-aware warm reconcile -----
	// Decide whether the persisted substrate can be reused as-is or must be
	// re-imported and re-applied. A tag mismatch (the CLI was upgraded since
	// the last bring-up) or an unhealthy persisted control plane forces the
	// full import/apply/seed legs; a matching tag against a gateway that comes
	// ready (the down→up warm case, where the just-restarted gateway settles
	// within a pod-restart window) skips them so a warm lenny up restarts in
	// seconds without re-reading the echo tarball or re-installing the CRDs.
	// spec: §17.4 (the substrate persists across down/up; an upgrade re-imports
	// and re-applies; an unhealthy substrate falls back to a fresh apply; the
	// CRD install and component image imports are one-time first-run costs).
	reapply := needsReapply(ctx, priorState, cfg.CLIVersion, res.kubeconfig, out)

	if reapply {
		// ----- Cluster state and in-cluster control plane -----
		// The one-time first-run costs (CRD install, agent namespace, runc
		// RuntimeClass, echo image import, echo Runtime CR) and the
		// platform-bundle import + manifest apply run together only on a first
		// run or a re-apply, so a warm tag-match reuse pays none of them.
		res = s.provisionClusterState(ctx, res, paths, cfg.EchoTarball, out)
		if err := s.applyControlPlane(ctx, res, keyFile, out); err != nil {
			return nil, err
		}
	} else {
		fmt.Fprintln(out, "lenny up: reusing the persisted control plane (image tag unchanged, gateway healthy)")
	}

	// ----- Host-side forwarder -----
	// The forwarder terminates TLS on 127.0.0.1:8443 with the per-lenny-up
	// self-signed leaf and forwards plaintext HTTP to the gateway node port,
	// carrying the EMBEDDED_MODE_LOCAL_ONLY fail-closed loopback check. The
	// in-cluster gateway serves plaintext HTTP and cannot answer a TLS
	// handshake directly. spec: §17.4, §3 EMBEDDED_MODE_LOCAL_ONLY.
	forwarderAddr, err := s.startForwarder(cfg, out)
	if err != nil {
		return nil, err
	}

	// ----- Gateway readiness -----
	// lenny up reports the gateway ready when its Deployment reports Ready;
	// the seeded echo pool warms in the background afterward (S4).
	fmt.Fprintln(out, "lenny up: waiting for the gateway to become ready")
	if err := waitGatewayDeployReadyFn(ctx, res.kubeconfig); err != nil {
		return nil, err
	}

	if reapply {
		// ----- Echo seed: registry record + warm pool -----
		// The Runtime CR was applied in provisionClusterState. Register the echo
		// runtime record through the gateway /v1/admin/bootstrap path (which
		// needs the gateway answering) and apply the echo SandboxTemplate/
		// SandboxWarmPool pair directly, so the unconditionally-registered
		// WarmPoolController pre-warms the pod with no Postgres-backed
		// PoolScalingController. spec: §4.6.2, §5.2, §15.4.4.
		if err := s.seedEcho(ctx, forwarderAddr, res, out); err != nil {
			return nil, err
		}
	}

	// ----- Record state -----
	if err := s.recordState(paths, res, forwarderAddr, cfg.CLIVersion); err != nil {
		return nil, err
	}

	ok = true
	return s, nil
}

// applyControlPlane imports the platform image bundle and applies the
// embedded manifests in two fenced phases. It overlaps the slow multi-image
// bundle import with the Secret create and the apply of the non-image
// objects (namespaces, CRDs, RBAC, config/secret material, Services, and the
// runc RuntimeClass), then blocks on the import completing, and only then
// applies the Deployments. The barrier is explicit: the Deployment phase
// runs after <-importDone, so a scheduled pod never reaches the registry
// under IfNotPresent before the gateway/controller/ops/adapter images are
// present in the embedded containerd. Ordering the Deployments last within a
// single apply pass would not synchronize with the import goroutine, because
// the non-image apply is near-instantaneous while the import is the slow leg;
// the explicit <-importDone fence is what enforces the proposal's "apply the
// Deployments after the import lands" invariant. spec: §17.4 (proposal 0017
// C2: import the images, apply the non-image objects, fence on the import,
// then apply the Deployments so pods do not enter ImagePullBackOff).
func (s *Stack) applyControlPlane(ctx context.Context, res substrateResult, keyFile string, out io.Writer) error {
	// Start the platform-bundle import in the background and overlap it with
	// the Secret create and the apply of the non-image objects.
	importDone := make(chan struct{})
	go func() {
		defer close(importDone)
		importPlatformBundleFn(s, s.paths.Root, out)
	}()
	// Block on the import before returning on any early error path too, so a
	// failed Secret create or non-image apply does not leak the import
	// goroutine.
	imported := false
	awaitImport := func() {
		if !imported {
			<-importDone
			imported = true
		}
	}
	defer awaitImport()

	// Create the dev bearer-trust Secret before the gateway Deployment is
	// applied so its mount resolves when the pod schedules.
	fmt.Fprintln(out, "lenny up: creating the dev bearer-trust secret")
	if err := createDevBearerSecretFn(ctx, res.kubeconfig, keyFile); err != nil {
		return err
	}

	// Apply the non-image objects (everything but the Deployments) while the
	// import is still in flight: none of these objects pulls an image.
	fmt.Fprintln(out, "lenny up: applying the embedded control-plane manifests")
	if err := applyNonImageManifestsFn(ctx, res.kubeconfig); err != nil {
		return err
	}

	// Fence on the import landing before submitting any Deployment, so the
	// gateway/controller/ops/adapter images are present in containerd by the
	// time their pods schedule. This is the proposal's hard ordering
	// invariant; without it a scheduled pod can ImagePullBackOff against the
	// registry under IfNotPresent before the bundle import completes.
	awaitImport()

	// Apply the Deployments now that the import has landed.
	fmt.Fprintln(out, "lenny up: applying the embedded control-plane deployments")
	if err := applyDeploymentManifestsFn(ctx, res.kubeconfig); err != nil {
		return err
	}
	return nil
}

// seedEcho registers the echo runtime record through the gateway bootstrap
// path and applies the echo warm-pool CRD pair directly. It runs after the
// gateway is ready (the bootstrap call dials it) and is gated on the import
// having resolved an echo digest: with no resolved digest the Runtime CR was
// not applied, so materializing a pool would only ImagePullBackOff. spec:
// §4.6.2, §5.2, §15.4.4.
func (s *Stack) seedEcho(ctx context.Context, forwarderAddr string, res substrateResult, out io.Writer) error {
	if err := installRuntimesFn(ctx, "https://"+forwarderAddr, res.echoImageRef, out); err != nil {
		return err
	}
	if res.echoImageRef == "" {
		// The echo image did not import, so provisionClusterState skipped the
		// Runtime CR apply. Skip the pool too rather than warm a pod for a
		// runtime whose image is absent. The registry record is still seeded
		// above so the runtime resolves, surfacing a precise failure on a
		// session start rather than a silent no-pool.
		fmt.Fprintln(out, "lenny up: WARNING: echo image not imported; skipping the echo warm-pool apply")
		return nil
	}
	fmt.Fprintln(out, "lenny up: applying the echo warm pool")
	if err := applyEchoPoolFn(ctx, res.kubeconfig, agentNamespace); err != nil {
		return err
	}
	return nil
}

// startForwarder starts the loopback-only host-side TLS forwarder in front
// of the gateway node port and returns the loopback host:port it presents.
// The forwarder terminates TLS on 127.0.0.1:8443 with the self-signed leaf
// and forwards plaintext HTTP to the gateway node port, carrying the
// EMBEDDED_MODE_LOCAL_ONLY fail-closed check. spec: §17.4.
func (s *Stack) startForwarder(cfg Config, out io.Writer) (string, error) {
	httpsPort := cfg.HTTPSPort
	if httpsPort == 0 {
		httpsPort = defaultHTTPSPort
	}
	listenAddr := net.JoinHostPort(httpHost, strconv.Itoa(httpsPort))
	// The forwarder targets the gateway node port on the launcher's gateway
	// host: loopback on the Linux launcher (k3s and the node port share the
	// host) and host loopback on the Docker-backed launcher (the launcher
	// publishes the in-VM node port to host loopback, C4).
	upstream := "http://" + net.JoinHostPort(httpHost, strconv.Itoa(gatewayNodePort))
	fmt.Fprintf(out, "lenny up: starting the host-side gateway forwarder on https://%s\n", listenAddr)
	proxy, err := startTLSProxy(listenAddr, upstream, s.tls.CertPath, s.tls.KeyPath)
	if err != nil {
		return "", err
	}
	s.proxy = proxy
	return listenAddr, nil
}

// recordState writes the §17.4 stack state file so lenny status, logs, and
// down can locate the substrate and the host-side forwarder. It records the
// CLI version as the deployed image tag so a later warm lenny up reconciles
// against it (C4). spec: §17.4.
func (s *Stack) recordState(paths Paths, res substrateResult, forwarderAddr, cliVersion string) error {
	st := State{
		StartedAt:            time.Now(),
		K3sContainer:         k3sContainerHandle(s.k3s),
		K3sPID:               s.k3s.PID(),
		GatewayForwarderAddr: forwarderAddr,
		DeployedImageTag:     cliVersion,
		KubeconfigPath:       res.kubeconfig,
		K3sEnabled:           res.enabled,
	}
	s.state = st
	if err := writeState(paths.StateFile(), st); err != nil {
		return err
	}
	return nil
}

// needsReapply decides whether a bring-up must run the one-time first-run
// costs (the CRD install, the component image imports, and the manifest apply)
// or can reuse the persisted control plane as-is. It forces a re-apply (returns
// true) when there is no prior recorded bring-up, when the running CLI version
// differs from the deployed image tag the prior bring-up recorded (the CLI was
// upgraded, so a stale image must not run), or when the persisted control plane
// does not become healthy. A matching tag against a gateway that becomes Ready
// returns false so a warm lenny up skips the expensive CRD install, image
// import, manifest apply, and echo seed and restarts in seconds.
//
// The health check is the warmGatewayReadyFn seam (the gateway Deployment
// readiness against the persisted substrate's kubeconfig). It waits a window
// comparable to a normal pod restart rather than probing once, because the
// headline warm case is the down→up cycle: startSubstrate has just restarted
// the stopped substrate, so the gateway pod is restarting and is not Ready in
// the first seconds. Waiting the restart window distinguishes a gateway coming
// back up (reuse) from a genuinely broken persisted substrate (the window
// elapses without a Ready gateway, so re-apply, fail-safe toward a working
// bring-up).
//
// spec: §17.4 (the substrate persists across down/up; an upgrade re-imports
// and re-applies on a CLI-version / image-tag mismatch; an unhealthy
// persisted substrate falls back to a fresh apply; the CRD install and
// component image imports are one-time first-run costs a warm up skips).
func needsReapply(ctx context.Context, prior State, cliVersion, kubeconfig string, out io.Writer) bool {
	if prior.DeployedImageTag == "" {
		// No prior bring-up recorded a deployed tag (a first run, or a state
		// file from before the tag field existed): apply.
		return true
	}
	if prior.DeployedImageTag != cliVersion {
		fmt.Fprintf(out, "lenny up: CLI version %q differs from the deployed image tag %q; re-importing and re-applying\n",
			cliVersion, prior.DeployedImageTag)
		return true
	}
	if !warmGatewayReadyFn(ctx, kubeconfig) {
		fmt.Fprintln(out, "lenny up: the persisted control plane is not ready; re-applying the embedded manifests")
		return true
	}
	return false
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

// substrateResult is the outcome of the substrate bring-up: whether the
// embedded cluster came up (startSubstrate), the admin kubeconfig path, the
// §4.7 gateway↔adapter callback address the controller stamps onto agent pods,
// and the import-time-resolved echo runtime image reference (filled in by
// provisionClusterState on a first run or re-apply).
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
	// stopSubstrateContainer stops the Docker-backed k3s container by its
	// recorded handle while persisting it and its containerd image store.
	// lenny down (without --purge) calls it so a warm lenny up restarts the
	// persisted container. It is a package-level var so a unit test can assert
	// the persist-stop teardown without invoking a real docker. spec: §17.4
	// (lenny down persists the substrate and the imported-image store).
	stopSubstrateContainer = k3s.StopContainer
	// removeSubstrateContainer force-removes the Docker-backed k3s container
	// by its recorded handle, discarding its containerd image store. lenny
	// down --purge calls it before discarding the state directory that holds
	// the handle, so --purge does not orphan the container the graceful
	// Stack.Stop path would have removed. It is a package-level var so a unit
	// test can assert the teardown removes the recorded container without
	// invoking a real docker. spec: §17.4 (--purge removes the persisted
	// substrate and the imported-image store).
	removeSubstrateContainer = k3s.RemoveContainer
	// stopSubstrateProcess terminates the Linux k3s process group by its
	// recorded leader PID, persisting the data directory. lenny up starts k3s
	// in its own process group so it outlives the foreground lenny up, so
	// lenny down terminates the recorded group out of process. It is a
	// package-level var so a unit test can assert the teardown without killing
	// a real process. spec: §17.4 (the Linux substrate outlives the CLI;
	// lenny down stops it).
	stopSubstrateProcess = k3s.StopProcessGroup
	// importEchoRuntimeImageFn is the bring-up echo-image import seam. It
	// defaults to the real (*Stack).importEchoRuntimeImage and is a
	// package-level var so a unit test can drive the §4.7 activation sequence
	// in provisionClusterState (namespace create, import, Runtime-CR apply) with
	// a controllable resolved digest, without a live ctr or containerd. spec:
	// §24.19.1 (the --file import path), §4.7.
	importEchoRuntimeImageFn = (*Stack).importEchoRuntimeImage
)

// startSubstrate brings the embedded Kubernetes substrate up on every host
// the launcher supports: a managed k3s child process on Linux, a Docker-backed
// k3s container under Docker Desktop's Linux VM on macOS and Windows. On a warm
// down→up cycle the launcher restarts the persisted (stopped) container or
// process; on a first run it provisions a fresh substrate. It computes the §4.7
// gateway↔adapter callback address from the launcher's substrate-specific host.
// The OS branch is confined to this provisioning layer; the in-cluster gateway,
// controllers, CRDs, and storage interfaces above it are byte-identical across
// operating systems. A substrate that does not come up makes the in-cluster
// control plane unavailable; the §17.4 bring-up reports the substrate failure
// (proposal 0017 C2).
//
// startSubstrate runs unconditionally on every lenny up, because a warm reuse
// must still restart a stopped substrate. The per-cluster-state legs that
// §17.4 designates as one-time first-run costs (the CRD install, the agent
// namespace, the runc RuntimeClass, the echo image import, and the echo Runtime
// CR apply) are deferred to provisionClusterState, which a warm tag-match reuse
// skips.
//
// spec: §17.4 (the embedded Kubernetes substrate is provisioned per host
// operating system, and stays identical above the substrate layer; an
// unavailable substrate makes lenny up report the failure; the substrate
// restart is unconditional while the cluster-state import/apply legs are
// one-time first-run costs), §4.7, §8.6, §9.1.
func (s *Stack) startSubstrate(ctx context.Context, paths Paths, out io.Writer) substrateResult {
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
	sup := newSubstrate(k3s.Config{Dir: paths.K3s, APIPort: defaultK3sAPIPort, GatewayNodePort: gatewayNodePort})
	if err := sup.Start(ctx); err != nil {
		// k3s is the §17.4 component most likely to fail on a constrained
		// host. The in-cluster control plane runs on it, so without the
		// substrate the bring-up reports the failure (proposal 0017 C2).
		fmt.Fprintf(out, "lenny up: WARNING: embedded Kubernetes did not start: %v\n", err)
		return substrateResult{}
	}
	s.k3s = sup
	return substrateResult{
		enabled:    true,
		kubeconfig: sup.KubeconfigPath(),
		// Compute the gateway's externally-reachable gRPC address from the
		// launcher's substrate-specific host. The §4.7 placement and adapter
		// business logic above this point is unaware of the substrate; only
		// this provisioning layer branches per OS.
		gatewayGRPCDialAddr: gatewayGRPCAddr(sup.GatewayHost(), defaultGatewayGRPCPort),
	}
}

// provisionClusterState installs the per-cluster-state objects §17.4
// designates as one-time first-run costs against the (already-started)
// substrate: the production CRDs, the agent namespace, the runc RuntimeClass,
// the echo component image import, and the echo Runtime CR. It runs only on a
// first run or a re-apply (a CLI-version/image-tag mismatch or an unhealthy
// persisted substrate); a warm tag-match reuse skips it together with
// applyControlPlane and seedEcho, so the down→up warm restart does not re-read
// and re-import the echo tarball or re-install the CRDs. It returns res with
// echoImageRef filled in from the import (empty when the import did not run or
// failed).
//
// Each leg warns rather than aborts on failure: the controllers create
// namespaced resources lazily, and a CRD-install or namespace hiccup leaves
// placement inert rather than tearing the substrate down, so the bring-up
// surfaces it without failing.
//
// spec: §17.4 (the CRD install and the component image imports are one-time
// first-run costs a warm up skips), §4.6.2 (the agent namespace holds the pool
// CRDs), §5.1 (the Runtime CR), §5.3 (standard->runc), §4.7 (the digest-pinned
// embedded pod image).
func (s *Stack) provisionClusterState(ctx context.Context, res substrateResult, paths Paths, echoTarball string, out io.Writer) substrateResult {
	if err := installSubstrateCRDs(ctx, res.kubeconfig); err != nil {
		fmt.Fprintf(out, "lenny up: WARNING: CRD install failed: %v\n", err)
	}
	// Create the agent namespace the gateway places into and the
	// PoolScalingController materializes the seeded pool CRDs into. Both the
	// gateway's -agent-namespace and the controller's --agent-namespaces are
	// set to this namespace (through the chart's agentNamespaces values), so it
	// must exist before either places. A create failure warns rather than
	// aborts: the controllers create namespaced resources lazily, but a missing
	// namespace would leave placement inert, so the bring-up surfaces it. spec:
	// §4.6.2 (the pool CRDs materialize in the agent namespace), §5.1.
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
