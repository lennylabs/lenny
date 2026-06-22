// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/embedded/k3s"
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

// TestGatewayHealthy covers the bring-up liveness check waitForStack and
// lenny status share: a gateway answering 2xx is healthy, an unreachable
// address is not. The probe wraps probeHealthz and reports a boolean.
//
// spec: §24.19 (the gateway health probe).
func TestGatewayHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	if !gatewayHealthy(context.Background(), srv.URL) {
		t.Error("gatewayHealthy on a 200 gateway = false, want true")
	}
	url := srv.URL
	srv.Close()
	if gatewayHealthy(context.Background(), url) {
		t.Error("gatewayHealthy on a closed gateway = true, want false")
	}
}

// fakeLauncher is a k3s.Launcher test double for provisionSubstrate: it
// records whether Start was called, returns a canned start error, and
// reports a substrate-specific GatewayHost and kubeconfig path. It lets the
// per-OS substrate-selection logic be exercised without downloading and
// running real k3s, mirroring the runDocker injection on the Docker-backed
// launcher.
type fakeLauncher struct {
	startErr    error
	started     bool
	stopped     bool
	gatewayHost string
	kubeconfig  string
}

func (f *fakeLauncher) Start(context.Context) error {
	f.started = true
	return f.startErr
}
func (f *fakeLauncher) Stop() error            { f.stopped = true; return nil }
func (f *fakeLauncher) Running() bool          { return f.started && f.startErr == nil }
func (f *fakeLauncher) PID() int               { return 0 }
func (f *fakeLauncher) KubeconfigPath() string { return f.kubeconfig }
func (f *fakeLauncher) GatewayHost() string    { return f.gatewayHost }

// withSubstrateSeams swaps the package-level substrate seams for the
// duration of a test and restores them, so provisionSubstrate can be driven
// with a fake launcher, a controllable platform-support verdict, and a
// no-op CRD install. It also stubs the §4.7 activation seams (the agent
// namespace create, the echo-image import, and the Runtime-CR apply) to no-ops
// so a test that does not exercise them is not slowed or blocked by a real ctr
// or API server; tests that exercise the activation sequence override the
// activation seams directly through withActivationSeams.
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
	// existing provisionSubstrate tests (which assert substrate selection, not
	// placement activation) neither dial an API server nor invoke a real ctr.
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

// TestProvisionSubstrateUnsupportedPlatform covers the non-Linux,
// Docker-absent branch: when the host cannot provision the substrate,
// provisionSubstrate reports the cluster unavailable and returns a disabled
// result without constructing a launcher, so the gateway and stores still
// come up.
//
// spec: §17.4 (on a host without the substrate prerequisite the gateway and
// stores still come up; session placement is unavailable).
func TestProvisionSubstrateUnsupportedPlatform_spec_17_4(t *testing.T) {
	withSubstrateSeams(t, false, &fakeLauncher{}, nil)
	s := &Stack{}
	var out strings.Builder
	res := s.provisionSubstrate(context.Background(), NewPaths(t.TempDir()), "", &out)
	if res.enabled {
		t.Error("provisionSubstrate on an unsupported host = enabled, want disabled")
	}
	if s.k3s != nil {
		t.Error("provisionSubstrate constructed a launcher on an unsupported host")
	}
	if !strings.Contains(out.String(), "unavailable") {
		t.Errorf("output = %q, want the cluster-unavailable note", out.String())
	}
}

// TestProvisionSubstrateStartError covers the start-failure branch: when the
// launcher fails to start, provisionSubstrate routes around it (the storage
// and identity paths still come up) and returns a disabled result rather
// than failing the whole bring-up.
//
// spec: §17.4 (k3s is the component most likely to fail on a constrained
// host; lenny up continues without the embedded cluster).
func TestProvisionSubstrateStartError_spec_17_4(t *testing.T) {
	fake := &fakeLauncher{startErr: errors.New("boom")}
	withSubstrateSeams(t, true, fake, nil)
	s := &Stack{}
	var out strings.Builder
	res := s.provisionSubstrate(context.Background(), NewPaths(t.TempDir()), "", &out)
	if res.enabled {
		t.Error("provisionSubstrate with a failing launcher = enabled, want disabled")
	}
	if !fake.started {
		t.Error("provisionSubstrate did not attempt to start the launcher")
	}
	if !strings.Contains(out.String(), "continuing without the embedded cluster") {
		t.Errorf("output = %q, want the route-around note", out.String())
	}
}

// TestProvisionSubstrateSuccess covers the success branch: when the launcher
// starts, provisionSubstrate records it on the stack, installs the CRDs, and
// computes the §4.7 gateway↔adapter callback address from the launcher's
// substrate-specific GatewayHost joined to the gateway gRPC port. A
// host.docker.internal GatewayHost (the Docker-backed launcher) is carried
// into the dial address unchanged, confirming the OS branch stays confined
// to the launcher's GatewayHost.
//
// spec: §17.4 (the substrate comes up and the CRDs install against the
// launcher's kubeconfig), §4.7, §8.6, §9.1.
func TestProvisionSubstrateSuccess_spec_17_4(t *testing.T) {
	fake := &fakeLauncher{gatewayHost: "host.docker.internal", kubeconfig: "/k/kubeconfig.yaml"}
	withSubstrateSeams(t, true, fake, nil)
	s := &Stack{}
	var out strings.Builder
	res := s.provisionSubstrate(context.Background(), NewPaths(t.TempDir()), "", &out)
	if !res.enabled {
		t.Fatal("provisionSubstrate with a healthy launcher = disabled, want enabled")
	}
	if s.k3s != k3s.Launcher(fake) {
		t.Error("provisionSubstrate did not record the launcher on the stack")
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

// TestProvisionSubstrateCRDInstallFailureIsNonFatal covers the CRD-install
// warning branch: a CRD-install failure does not disable the substrate or
// fail the bring-up; the cluster is still recorded and the failure is warned
// about, because the controllers can install the CRDs on their own startup.
//
// spec: §17.4 (a CRD-install hiccup warns rather than aborts the bring-up).
func TestProvisionSubstrateCRDInstallFailureIsNonFatal_spec_17_4(t *testing.T) {
	fake := &fakeLauncher{gatewayHost: "127.0.0.1", kubeconfig: "/k/kubeconfig.yaml"}
	withSubstrateSeams(t, true, fake, errors.New("crd boom"))
	s := &Stack{}
	var out strings.Builder
	res := s.provisionSubstrate(context.Background(), NewPaths(t.TempDir()), "", &out)
	if !res.enabled {
		t.Error("provisionSubstrate with a CRD-install failure = disabled, want enabled (non-fatal)")
	}
	if !strings.Contains(out.String(), "CRD install failed") {
		t.Errorf("output = %q, want the CRD-install warning", out.String())
	}
}

// TestProvisionSubstrateActivatesPlacement_spec_4_7 covers the §4.7 activation
// sequence Up wires in Embedded Mode: when the substrate comes up, the CRDs
// install, the agent namespace is created, the echo image is imported, and the
// echo Runtime CR is applied carrying the import-time-resolved digest — in that
// order. The namespace must precede placement (the gateway resolves the pool
// from it) and the import must precede the CR apply (the CR carries the resolved
// digest). The resolved digest is recorded on the result for the bootstrap seed.
//
// spec: §4.6.2 (the agent namespace holds the pool CRDs), §4.7 (the embedded
// echo Runtime CR activates the pod path), §5.1 (the Runtime CR is the
// declarative source).
func TestProvisionSubstrateActivatesPlacement_spec_4_7(t *testing.T) {
	const resolved = "ghcr.io/lennylabs/runtime-echo-embedded@sha256:" +
		"4444444444444444444444444444444444444444444444444444444444444444"
	fake := &fakeLauncher{gatewayHost: "127.0.0.1", kubeconfig: "/k/kubeconfig.yaml"}
	withSubstrateSeams(t, true, fake, nil)
	calls := withActivationSeams(t, resolved)

	s := &Stack{}
	var out strings.Builder
	res := s.provisionSubstrate(context.Background(), NewPaths(t.TempDir()), "", &out)

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

// TestProvisionSubstrateSkipsRuntimeCRWhenImportFails_spec_4_7 covers the
// fail-closed gating: when the echo image import resolves no digest (the
// substrate is up but the tarball is missing or containerd is unreachable), the
// Runtime CR is not applied. Applying a CR carrying a sentinel digest no
// containerd image matches would only ImagePullBackOff, so the bring-up keeps
// the gateway on the in-process echo executor instead. The namespace is still
// created, since it is independent of the image.
//
// spec: §4.7 (the digest-pinned pod image requires the import to resolve), §5.1.
func TestProvisionSubstrateSkipsRuntimeCRWhenImportFails_spec_4_7(t *testing.T) {
	fake := &fakeLauncher{gatewayHost: "127.0.0.1", kubeconfig: "/k/kubeconfig.yaml"}
	withSubstrateSeams(t, true, fake, nil)
	calls := withActivationSeams(t, "") // import resolves no digest

	s := &Stack{}
	var out strings.Builder
	res := s.provisionSubstrate(context.Background(), NewPaths(t.TempDir()), "", &out)

	if res.echoImageRef != "" {
		t.Errorf("echoImageRef = %q, want empty when the import resolves no digest", res.echoImageRef)
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

// TestProvisionSubstrateNamespaceCreateFailureIsNonFatal_spec_4_6_2 covers the
// agent-namespace create warning branch: a create failure warns rather than
// aborts the bring-up, mirroring the CRD-install branch, since the storage and
// identity paths still come up.
//
// spec: §4.6.2 (the agent namespace holds the pool CRDs), §17.4 (a substrate
// hiccup warns rather than aborts the bring-up).
func TestProvisionSubstrateNamespaceCreateFailureIsNonFatal_spec_4_6_2(t *testing.T) {
	fake := &fakeLauncher{gatewayHost: "127.0.0.1", kubeconfig: "/k/kubeconfig.yaml"}
	withSubstrateSeams(t, true, fake, nil)
	ensureAgentNamespaceFn = func(context.Context, string, string) error {
		return errors.New("namespace boom")
	}

	s := &Stack{}
	var out strings.Builder
	res := s.provisionSubstrate(context.Background(), NewPaths(t.TempDir()), "", &out)

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
