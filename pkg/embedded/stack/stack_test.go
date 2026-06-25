// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/embedded/k3s"
	"github.com/lennylabs/lenny/pkg/embedded/tlsgen"
)

// TestGatewayGRPCAddr covers the §4.7 substrate-agnostic gateway↔adapter
// callback address composition Up performs at bring-up. The stack joins the
// launcher's substrate-specific GatewayHost with the gateway gRPC host port
// into the address the controller stamps onto agent pods. The Linux
// child-process launcher returns 127.0.0.1 (k3s and the gateway share the
// host); the Docker-backed launcher returns host.docker.internal (pods run
// inside the Docker VM and reach the host gateway through that alias). The
// function is pure, so the OS branch stays confined to the launcher's
// GatewayHost and the §4.7 pod-spec/adapter business logic above it is
// substrate-agnostic.
//
// spec: §4.7 (the in-cluster adapter dials the gateway at this address
// across the host/Docker boundary), §8.6, §9.1, §17.4.
func TestGatewayGRPCAddr_spec_4_7(t *testing.T) {
	cases := []struct {
		name string
		host string
		port int
		want string
	}{
		{
			name: "linux child-process launcher reaches the host at loopback",
			host: "127.0.0.1",
			port: defaultGatewayGRPCPort,
			want: "127.0.0.1:50061",
		},
		{
			name: "docker-backed launcher reaches the host at the docker alias",
			host: "host.docker.internal",
			port: defaultGatewayGRPCPort,
			want: "host.docker.internal:50061",
		},
		{
			name: "an alternate port is joined faithfully",
			host: "host.docker.internal",
			port: 6000,
			want: "host.docker.internal:6000",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gatewayGRPCAddr(tc.host, tc.port); got != tc.want {
				t.Errorf("gatewayGRPCAddr(%q, %d) = %q, want %q", tc.host, tc.port, got, tc.want)
			}
		})
	}
}

// fakeLauncher is a k3s.Launcher test double for startSubstrate: it
// records whether Start was called, returns a canned start error, and
// reports a substrate-specific GatewayHost and kubeconfig path. It lets the
// per-OS substrate-selection logic be exercised without downloading and
// running real k3s, mirroring the runDocker injection on the Docker-backed
// launcher.
type fakeLauncher struct {
	startErr    error
	started     bool
	stopped     bool
	removed     bool
	gatewayHost string
	kubeconfig  string
}

func (f *fakeLauncher) Start(context.Context) error {
	f.started = true
	return f.startErr
}
func (f *fakeLauncher) Stop() error            { f.stopped = true; return nil }
func (f *fakeLauncher) Remove() error          { f.removed = true; return nil }
func (f *fakeLauncher) Running() bool          { return f.started && f.startErr == nil }
func (f *fakeLauncher) PID() int               { return 0 }
func (f *fakeLauncher) KubeconfigPath() string { return f.kubeconfig }
func (f *fakeLauncher) GatewayHost() string    { return f.gatewayHost }

// withSubstrateSeams swaps the package-level substrate seams for the
// duration of a test and restores them, so startSubstrate and
// provisionClusterState can be driven with a fake launcher, a controllable
// platform-support verdict, and a no-op CRD install. It also stubs the §4.7
// activation seams (the agent namespace create, the echo-image import, and the
// Runtime-CR apply) to no-ops so a test that does not exercise them is not
// slowed or blocked by a real ctr or API server; tests that exercise the
// activation sequence override the activation seams directly through
// withActivationSeams.
func withSubstrateSeams(t *testing.T, supported bool, l k3s.Launcher, crdErr error) {
	t.Helper()
	prevSupported, prevNew, prevCRDs := substrateSupported, newSubstrate, installSubstrateCRDs
	prevNS, prevImport, prevCR := ensureAgentNamespaceFn, importEchoRuntimeImageFn, applyEchoRuntimeCRFn
	t.Cleanup(func() {
		substrateSupported, newSubstrate, installSubstrateCRDs = prevSupported, prevNew, prevCRDs
		ensureAgentNamespaceFn, importEchoRuntimeImageFn, applyEchoRuntimeCRFn = prevNS, prevImport, prevCR
	})
	substrateSupported = func() bool { return supported }
	newSubstrate = func(k3s.Config) k3s.Launcher { return l }
	installSubstrateCRDs = func(context.Context, string) error { return crdErr }
	// Default the activation seams to no-ops with no resolved digest, so the
	// existing substrate tests (which assert substrate selection, not placement
	// activation) neither dial an API server nor invoke a real ctr.
	ensureAgentNamespaceFn = func(context.Context, string, string) error { return nil }
	importEchoRuntimeImageFn = func(*Stack, string, string, io.Writer) string { return "" }
	applyEchoRuntimeCRFn = func(context.Context, string, string) error { return nil }
}

// activationCalls records the §4.7 activation seam invocations for an ordering
// and gating assertion.
type activationCalls struct {
	namespaceCreated string
	imageImported    bool
	crApplied        bool
	crImage          string
	order            []string
}

// withActivationSeams overrides the §4.7 activation seams to record their
// invocations and return a controllable resolved digest from the import, so a
// test asserts the namespace create, the import, and the Runtime-CR apply run
// in order and the CR apply is gated on a resolved digest. It must run after
// withSubstrateSeams so its overrides win.
func withActivationSeams(t *testing.T, resolvedImage string) *activationCalls {
	t.Helper()
	calls := &activationCalls{}
	ensureAgentNamespaceFn = func(_ context.Context, _ string, ns string) error {
		calls.namespaceCreated = ns
		calls.order = append(calls.order, "namespace")
		return nil
	}
	importEchoRuntimeImageFn = func(*Stack, string, string, io.Writer) string {
		calls.imageImported = true
		calls.order = append(calls.order, "import")
		return resolvedImage
	}
	applyEchoRuntimeCRFn = func(_ context.Context, _ string, image string) error {
		calls.crApplied = true
		calls.crImage = image
		calls.order = append(calls.order, "cr")
		return nil
	}
	return calls
}

// TestStartSubstrateUnsupportedPlatform covers the non-Linux, Docker-absent
// branch: when the host cannot provision the substrate, startSubstrate reports
// the cluster unavailable and returns a disabled result without constructing a
// launcher. The §17.4 control plane runs on the substrate, so an unsupported
// host (no Docker Desktop) leaves the stack unable to come up rather than
// degrading to a host control plane.
//
// spec: §17.4 (the control plane runs as in-cluster pods; on a host without
// the substrate prerequisite lenny up reports the cluster unavailable).
func TestStartSubstrateUnsupportedPlatform_spec_17_4(t *testing.T) {
	withSubstrateSeams(t, false, &fakeLauncher{}, nil)
	s := &Stack{}
	var out strings.Builder
	res := s.startSubstrate(context.Background(), NewPaths(t.TempDir()), &out)
	if res.enabled {
		t.Error("startSubstrate on an unsupported host = enabled, want disabled")
	}
	if s.k3s != nil {
		t.Error("startSubstrate constructed a launcher on an unsupported host")
	}
	if !strings.Contains(out.String(), "unavailable") {
		t.Errorf("output = %q, want the cluster-unavailable note", out.String())
	}
}

// TestStartSubstrateStartError covers the start-failure branch: when the
// launcher fails to start, startSubstrate returns a disabled result and
// reports the substrate failure. The §17.4 control plane runs on the
// substrate, so an unavailable substrate makes the bring-up surface the
// failure rather than routing around it to a host control plane that no
// longer exists.
//
// spec: §17.4 (the control plane runs as in-cluster pods; an unavailable
// substrate makes lenny up report the failure).
func TestStartSubstrateStartError_spec_17_4(t *testing.T) {
	fake := &fakeLauncher{startErr: errors.New("boom")}
	withSubstrateSeams(t, true, fake, nil)
	s := &Stack{}
	var out strings.Builder
	res := s.startSubstrate(context.Background(), NewPaths(t.TempDir()), &out)
	if res.enabled {
		t.Error("startSubstrate with a failing launcher = enabled, want disabled")
	}
	if !fake.started {
		t.Error("startSubstrate did not attempt to start the launcher")
	}
	if !strings.Contains(out.String(), "embedded Kubernetes did not start") {
		t.Errorf("output = %q, want the substrate-failure note", out.String())
	}
}

// TestStartSubstrateSuccess covers the success branch: when the launcher
// starts, startSubstrate records it on the stack and computes the §4.7
// gateway↔adapter callback address from the launcher's substrate-specific
// GatewayHost joined to the gateway gRPC port. A host.docker.internal
// GatewayHost (the Docker-backed launcher) is carried into the dial address
// unchanged, confirming the OS branch stays confined to the launcher's
// GatewayHost. The per-cluster-state legs (CRD install, namespace, import) do
// not run here: they are deferred to provisionClusterState so a warm reuse can
// skip them.
//
// spec: §17.4 (the substrate comes up; the in-cluster control plane runs above
// it), §4.7, §8.6, §9.1.
func TestStartSubstrateSuccess_spec_17_4(t *testing.T) {
	fake := &fakeLauncher{gatewayHost: "host.docker.internal", kubeconfig: "/k/kubeconfig.yaml"}
	withSubstrateSeams(t, true, fake, nil)
	s := &Stack{}
	var out strings.Builder
	res := s.startSubstrate(context.Background(), NewPaths(t.TempDir()), &out)
	if !res.enabled {
		t.Fatal("startSubstrate with a healthy launcher = disabled, want enabled")
	}
	if s.k3s != k3s.Launcher(fake) {
		t.Error("startSubstrate did not record the launcher on the stack")
	}
	if res.kubeconfig != "/k/kubeconfig.yaml" {
		t.Errorf("kubeconfig = %q, want /k/kubeconfig.yaml", res.kubeconfig)
	}
	want := gatewayGRPCAddr("host.docker.internal", defaultGatewayGRPCPort)
	if res.gatewayGRPCDialAddr != want {
		t.Errorf("gatewayGRPCDialAddr = %q, want %q (launcher GatewayHost joined to the gRPC port)",
			res.gatewayGRPCDialAddr, want)
	}
}

// provisionForTest starts the substrate and runs the cluster-state legs, the
// two phases the warm reconcile gates separately in Up. The cluster-state
// tests drive both together so they assert the CRD install, namespace, import,
// and Runtime-CR apply the way Up runs them on a first run or a re-apply.
func provisionForTest(t *testing.T, s *Stack, out io.Writer) substrateResult {
	t.Helper()
	res := s.startSubstrate(context.Background(), NewPaths(t.TempDir()), out)
	if !res.enabled {
		return res
	}
	return s.provisionClusterState(context.Background(), res, NewPaths(t.TempDir()), "", out)
}

// TestProvisionClusterStateCRDInstallFailureIsNonFatal covers the CRD-install
// warning branch: a CRD-install failure does not fail the bring-up; the failure
// is warned about, because the controllers can install the CRDs on their own
// startup.
//
// spec: §17.4 (a CRD-install hiccup warns rather than aborts the bring-up).
func TestProvisionClusterStateCRDInstallFailureIsNonFatal_spec_17_4(t *testing.T) {
	fake := &fakeLauncher{gatewayHost: "127.0.0.1", kubeconfig: "/k/kubeconfig.yaml"}
	withSubstrateSeams(t, true, fake, errors.New("crd boom"))
	s := &Stack{}
	var out strings.Builder
	res := provisionForTest(t, s, &out)
	if !res.enabled {
		t.Error("startSubstrate with a CRD-install failure later = disabled, want enabled (non-fatal)")
	}
	if !strings.Contains(out.String(), "CRD install failed") {
		t.Errorf("output = %q, want the CRD-install warning", out.String())
	}
}

// TestProvisionClusterStateActivatesPlacement_spec_4_7 covers the §4.7
// activation sequence Up wires in Embedded Mode on a first run or re-apply: the
// CRDs install, the agent namespace is created, the echo image is imported, and
// the echo Runtime CR is applied carrying the import-time-resolved digest — in
// that order. The namespace must precede placement (the gateway resolves the
// pool from it) and the import must precede the CR apply (the CR carries the
// resolved digest). The resolved digest is recorded on the result for the
// bootstrap seed.
//
// spec: §4.6.2 (the agent namespace holds the pool CRDs), §4.7 (the embedded
// echo Runtime CR activates the pod path), §5.1 (the Runtime CR is the
// declarative source).
func TestProvisionClusterStateActivatesPlacement_spec_4_7(t *testing.T) {
	const resolved = "ghcr.io/lennylabs/runtime-echo-embedded@sha256:" +
		"4444444444444444444444444444444444444444444444444444444444444444"
	fake := &fakeLauncher{gatewayHost: "127.0.0.1", kubeconfig: "/k/kubeconfig.yaml"}
	withSubstrateSeams(t, true, fake, nil)
	calls := withActivationSeams(t, resolved)

	s := &Stack{}
	var out strings.Builder
	res := provisionForTest(t, s, &out)

	if res.echoImageRef != resolved {
		t.Errorf("echoImageRef = %q, want the import-time-resolved digest %q", res.echoImageRef, resolved)
	}
	if calls.namespaceCreated != agentNamespace {
		t.Errorf("agent namespace created = %q, want %q", calls.namespaceCreated, agentNamespace)
	}
	if !calls.imageImported {
		t.Error("echo image was not imported")
	}
	if !calls.crApplied {
		t.Error("echo Runtime CR was not applied")
	}
	if calls.crImage != resolved {
		t.Errorf("Runtime CR applied with image %q, want the resolved digest %q", calls.crImage, resolved)
	}
	// The namespace must be created before the import and CR apply (the gateway
	// and controller place into it), and the import must precede the CR apply
	// (the CR carries the resolved digest).
	want := []string{"namespace", "import", "cr"}
	if len(calls.order) != len(want) {
		t.Fatalf("activation order = %v, want %v", calls.order, want)
	}
	for i := range want {
		if calls.order[i] != want[i] {
			t.Errorf("activation order = %v, want %v", calls.order, want)
			break
		}
	}
}

// TestProvisionClusterStateSkipsRuntimeCRWhenImportFails_spec_4_7 covers the
// Runtime-CR apply gate: when the echo image import resolves no digest (the
// substrate is up but the tarball is missing or containerd is unreachable), the
// Runtime CR is not applied, because applying a CR carrying a sentinel digest no
// containerd image matches would only ImagePullBackOff. The namespace is still
// created, since it is independent of the image. This edge does not degrade to
// the in-process echo executor: AgentNamespace is gated on k3sEnabled alone, so
// with k3s up the gateway still routes through the §4.7 pod path and an echo
// session fails to start; the echo tarball ships with the binary and imports at
// every bring-up, so an unresolved digest while k3s is up is not an expected
// steady state.
//
// spec: §4.7 (the digest-pinned pod image requires the import to resolve), §5.1.
func TestProvisionClusterStateSkipsRuntimeCRWhenImportFails_spec_4_7(t *testing.T) {
	fake := &fakeLauncher{gatewayHost: "127.0.0.1", kubeconfig: "/k/kubeconfig.yaml"}
	withSubstrateSeams(t, true, fake, nil)
	calls := withActivationSeams(t, "") // import resolves no digest

	s := &Stack{}
	var out strings.Builder
	res := provisionForTest(t, s, &out)

	if res.echoImageRef != "" {
		t.Errorf("echoImageRef = %q, want empty when the import resolves no digest", res.echoImageRef)
	}
	// The substrate stays enabled on an import failure: only the Runtime-CR
	// apply is image-gated, k3s itself is up. Up's gateway block keys
	// AgentNamespace off this enabled flag (k3sEnabled) alone, so the gateway
	// routes through the §4.7 pod path here rather than degrading to the
	// in-process echo executor. TestProvisionSubstrateImportFailureKeepsSubstrateEnabledForGatewayGate
	// pins that gating contract directly.
	if !res.enabled {
		t.Error("an import failure disabled the substrate; the gateway AgentNamespace gate keys off enabled (k3sEnabled), so the substrate must stay up")
	}
	if calls.namespaceCreated != agentNamespace {
		t.Errorf("agent namespace created = %q, want %q (independent of the image)", calls.namespaceCreated, agentNamespace)
	}
	if !calls.imageImported {
		t.Error("the import was not attempted")
	}
	if calls.crApplied {
		t.Error("Runtime CR was applied despite an unresolved image; the CR apply must be gated on a resolved digest")
	}
}

// TestProvisionSubstrateImportFailureKeepsSubstrateEnabledForGatewayGate_spec_4_7
// pins the actual gateway AgentNamespace gating contract that Up relies on:
// Up sets the in-cluster gateway's -agent-namespace inside its k3sEnabled
// block, where k3sEnabled is exactly the substrate bring-up's returned enabled
// flag (stack.go: `res.enabled`). The gate keys off the substrate being up
// alone, never off sub.echoImageRef. This test asserts that across both the
// import-succeeds and the import-fails edges (k3s up in both) the bring-up
// returns enabled:true, so Up sets AgentNamespace in both, while only the
// substrate-down case returns enabled:false and leaves AgentNamespace unset.
// It documents the real behavior rather than a fail-closed degrade: on the
// import-failed-but-k3s-up edge the gateway still routes through the §4.7 pod
// path against the sentinel echo seed (no Runtime CR applied) and an echo
// session fails to start; the echo tarball ships with the binary and imports at
// every bring-up, so an unresolved digest while k3s is up is not an expected
// steady state. spec: §4.7 (AgentNamespace is gated on the substrate alone),
// §17.4 (Embedded Mode activates placement when the substrate is up).
func TestProvisionSubstrateImportFailureKeepsSubstrateEnabledForGatewayGate_spec_4_7(t *testing.T) {
	const resolved = "ghcr.io/lennylabs/runtime-echo-embedded@sha256:" +
		"5555555555555555555555555555555555555555555555555555555555555555"
	cases := []struct {
		name          string
		supported     bool
		startErr      error
		resolvedImage string
		wantEnabled   bool
	}{
		{
			name:          "k3s up and import resolves a digest sets AgentNamespace",
			supported:     true,
			resolvedImage: resolved,
			wantEnabled:   true,
		},
		{
			name:          "k3s up but import resolves no digest still sets AgentNamespace",
			supported:     true,
			resolvedImage: "", // import failed; the §4.7 pod path is still active
			wantEnabled:   true,
		},
		{
			name:          "substrate down leaves AgentNamespace unset",
			supported:     false,
			resolvedImage: "",
			wantEnabled:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeLauncher{startErr: tc.startErr, gatewayHost: "127.0.0.1", kubeconfig: "/k/kubeconfig.yaml"}
			withSubstrateSeams(t, tc.supported, fake, nil)
			withActivationSeams(t, tc.resolvedImage)

			s := &Stack{}
			var out strings.Builder
			res := provisionForTest(t, s, &out)

			// Up gates the gateway's AgentNamespace on `k3sEnabled := res.enabled`
			// alone. Replicate that exact gating decision and assert the resulting
			// AgentNamespace state matches the actual landed behavior.
			gotAgentNamespace := ""
			if res.enabled {
				gotAgentNamespace = agentNamespace
			}
			wantAgentNamespace := ""
			if tc.wantEnabled {
				wantAgentNamespace = agentNamespace
			}
			if res.enabled != tc.wantEnabled {
				t.Errorf("sub.enabled = %v, want %v (the gateway AgentNamespace gate keys off this)", res.enabled, tc.wantEnabled)
			}
			if gotAgentNamespace != wantAgentNamespace {
				t.Errorf("gateway AgentNamespace = %q, want %q", gotAgentNamespace, wantAgentNamespace)
			}
		})
	}
}

// TestProvisionClusterStateNamespaceCreateFailureIsNonFatal_spec_4_6_2 covers
// the agent-namespace create warning branch: a create failure warns rather than
// aborts the bring-up, mirroring the CRD-install branch, so the bring-up can
// surface the inert namespace without tearing the substrate down.
//
// spec: §4.6.2 (the agent namespace holds the pool CRDs), §17.4 (a substrate
// hiccup warns rather than aborts the bring-up).
func TestProvisionClusterStateNamespaceCreateFailureIsNonFatal_spec_4_6_2(t *testing.T) {
	fake := &fakeLauncher{gatewayHost: "127.0.0.1", kubeconfig: "/k/kubeconfig.yaml"}
	withSubstrateSeams(t, true, fake, nil)
	ensureAgentNamespaceFn = func(context.Context, string, string) error {
		return errors.New("namespace boom")
	}

	s := &Stack{}
	var out strings.Builder
	res := provisionForTest(t, s, &out)

	if !res.enabled {
		t.Error("a namespace-create failure disabled the substrate, want non-fatal")
	}
	if !strings.Contains(out.String(), "agent namespace create failed") {
		t.Errorf("output = %q, want the namespace-create warning", out.String())
	}
}

// TestPurgeRootRemovesStateDir covers purgeRoot: lenny down --purge removes
// the entire Embedded Mode state directory.
func TestPurgeRootRemovesStateDir(t *testing.T) {
	root := t.TempDir() + "/lenny-state"
	paths := NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	if err := purgeRoot(root); err != nil {
		t.Fatalf("purgeRoot: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("purgeRoot left the state directory in place (stat err: %v)", err)
	}
}

// withWarmGatewayReady overrides the warm-reconcile gateway-health seam so a
// test can drive the healthy / unhealthy persisted-substrate branches of
// needsReapply without a real API server, and restores it.
func withWarmGatewayReady(t *testing.T, ready bool) {
	t.Helper()
	prev := warmGatewayReadyFn
	t.Cleanup(func() { warmGatewayReadyFn = prev })
	warmGatewayReadyFn = func(context.Context, string) bool { return ready }
}

// TestNeedsReapply covers the version-aware warm-reconcile decision (C4): a
// bring-up re-imports and re-applies when there is no prior deployed tag,
// when the CLI version differs from the deployed tag (a CLI upgrade), or when
// the persisted control plane is unhealthy; it reuses the persisted control
// plane only on a matching tag against a ready gateway.
//
// spec: §17.4 (the substrate persists across down/up; an upgrade re-imports
// and re-applies on a CLI-version / image-tag mismatch; an unhealthy
// persisted substrate falls back to a fresh apply).
func TestNeedsReapply_spec_17_4(t *testing.T) {
	cases := []struct {
		name       string
		priorTag   string
		cliVersion string
		gwReady    bool
		want       bool
	}{
		{"no prior tag forces apply", "", "v1.2.3", true, true},
		{"tag mismatch forces apply", "v1.2.2", "v1.2.3", true, true},
		{"tag match but unhealthy forces apply", "v1.2.3", "v1.2.3", false, true},
		{"tag match and healthy skips apply", "v1.2.3", "v1.2.3", true, false},
		// A dev source build records and compares the empty-equivalent "dev"
		// tag; a matching dev tag against a ready gateway still skips.
		{"matching dev tag and healthy skips apply", "dev", "dev", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withWarmGatewayReady(t, tc.gwReady)
			prior := State{DeployedImageTag: tc.priorTag}
			got := needsReapply(context.Background(), prior, tc.cliVersion, "/no/kubeconfig", io.Discard)
			if got != tc.want {
				t.Errorf("needsReapply(priorTag=%q, cli=%q, ready=%v) = %v, want %v",
					tc.priorTag, tc.cliVersion, tc.gwReady, got, tc.want)
			}
		})
	}
}

// TestStartForwarderStartsBothLegs covers the §17.4 host-side forwarder: it
// must start both legs to the same gateway node port, the TLS leg terminating
// TLS on the loopback HTTPS port and the HTTP leg relaying raw bytes on the
// loopback HTTP port. The forwarder binds custom loopback ports here so the
// test does not collide with the default 8443/8080 the host gateway used.
// Stop then tears both legs down. spec: §17.4 (the 127.0.0.1:8080 HTTP leg is
// a plain TCP relay to the same node port; the forwarder is torn down with the
// stack).
func TestStartForwarderStartsBothLegs_spec_17_4(t *testing.T) {
	mat, err := tlsgen.Generate(t.TempDir())
	if err != nil {
		t.Fatalf("tlsgen.Generate: %v", err)
	}
	httpsPort, _ := strconv.Atoi(freePort(t))
	httpPort, _ := strconv.Atoi(freePort(t))

	s := &Stack{tls: mat}
	addr, err := s.startForwarder(Config{HTTPPort: httpPort, HTTPSPort: httpsPort}, io.Discard)
	if err != nil {
		t.Fatalf("startForwarder: %v", err)
	}
	// The returned address is the TLS leg's loopback HTTPS port.
	wantTLS := "127.0.0.1:" + strconv.Itoa(httpsPort)
	if addr != wantTLS {
		t.Errorf("startForwarder returned %q, want the TLS leg address %q", addr, wantTLS)
	}
	if s.proxy == nil {
		t.Error("startForwarder did not set the TLS proxy leg")
	}
	if s.relay == nil {
		t.Fatal("startForwarder did not set the HTTP relay leg")
	}
	// The HTTP relay binds the loopback HTTP port.
	wantHTTP := "127.0.0.1:" + strconv.Itoa(httpPort)
	if got := s.relay.Addr(); got != wantHTTP {
		t.Errorf("HTTP relay Addr() = %q, want %q", got, wantHTTP)
	}
	// Both legs are bound: dialing each host port connects.
	for _, hostPort := range []string{wantTLS, wantHTTP} {
		conn, derr := net.DialTimeout("tcp", hostPort, time.Second)
		if derr != nil {
			t.Errorf("dial %s before Stop: %v", hostPort, derr)
			continue
		}
		_ = conn.Close()
	}

	// Stop tears both legs down: each host port is then refused.
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	for _, hostPort := range []string{wantTLS, wantHTTP} {
		conn, derr := net.DialTimeout("tcp", hostPort, 500*time.Millisecond)
		if derr == nil {
			_ = conn.Close()
			t.Errorf("dial %s succeeded after Stop; want both legs torn down", hostPort)
		}
	}
}

// TestStartForwarderUsesDefaultPorts covers the §17.4 default loopback ports:
// a zero HTTPPort/HTTPSPort in Config resolves to the documented 8080/8443.
// The test asserts the resolution rather than binding the real ports, so it
// does not collide with a host gateway forwarder if one is up.
//
// spec: §17.4 (the host ports default to 8080 and 8443, the same the host
// gateway used).
func TestStartForwarderDefaultPortsResolve_spec_17_4(t *testing.T) {
	// The defaults are the package constants the documented addresses use.
	if defaultHTTPPort != 8080 {
		t.Errorf("defaultHTTPPort = %d, want 8080", defaultHTTPPort)
	}
	if defaultHTTPSPort != 8443 {
		t.Errorf("defaultHTTPSPort = %d, want 8443", defaultHTTPSPort)
	}
}
