// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

// withBringUpSeams stubs the in-cluster bring-up seams Up drives after the
// substrate is started (the dev-bearer Secret create, the platform-bundle
// import, the two-phase manifest apply, the gateway-readiness wait, the
// registry seed, and the echo-pool apply) to no-ops, and restores them. It is
// combined with withSubstrateSeams/withActivationSeams so a test can drive Up
// end to end without an API server, a containerd, or a gateway. It returns a
// recorder of the order the bring-up legs ran in.
//
// The import seam blocks on a release channel so a test can assert the
// import-before-Deployment fence: the platform-bundle import does not record
// "import" until the test releases it, modeling the slow multi-image import
// that the Deployment apply must wait behind. The default-released form (no
// hold) keeps the simple ordering tests unblocked.
func withBringUpSeams(t *testing.T) *bringUpCalls {
	t.Helper()
	calls := &bringUpCalls{}
	prevSecret, prevImport := createDevBearerSecretFn, importPlatformBundleFn
	prevNonImage, prevDeploy := applyNonImageManifestsFn, applyDeploymentManifestsFn
	prevWait, prevInstall, prevPool := waitGatewayDeployReadyFn, installRuntimesFn, applyEchoPoolFn
	t.Cleanup(func() {
		createDevBearerSecretFn, importPlatformBundleFn = prevSecret, prevImport
		applyNonImageManifestsFn, applyDeploymentManifestsFn = prevNonImage, prevDeploy
		waitGatewayDeployReadyFn, installRuntimesFn, applyEchoPoolFn = prevWait, prevInstall, prevPool
	})
	createDevBearerSecretFn = func(context.Context, string, string) error {
		calls.record("secret")
		return nil
	}
	importPlatformBundleFn = func(*Stack, string, io.Writer) {
		if calls.importHold != nil {
			<-calls.importHold
		}
		calls.record("import")
	}
	applyNonImageManifestsFn = func(context.Context, string) error {
		calls.record("apply-nonimage")
		return nil
	}
	applyDeploymentManifestsFn = func(context.Context, string) error {
		calls.record("apply-deploy")
		return nil
	}
	waitGatewayDeployReadyFn = func(context.Context, string) error {
		calls.record("wait")
		return nil
	}
	installRuntimesFn = func(_ context.Context, _, _ string, _ io.Writer) error {
		calls.record("seed")
		calls.seeded = true
		return nil
	}
	applyEchoPoolFn = func(context.Context, string, string) error {
		calls.record("pool")
		calls.poolApplied = true
		return nil
	}
	return calls
}

// bringUpCalls records the order the bring-up legs ran in. The import leg
// runs on a separate goroutine, so record guards the slice with a mutex.
type bringUpCalls struct {
	mu          sync.Mutex
	order       []string
	seeded      bool
	poolApplied bool
	// importHold, when non-nil, blocks the import seam until the test sends or
	// closes it, so a test can prove the Deployment apply waits behind the
	// import.
	importHold chan struct{}
}

func (c *bringUpCalls) record(leg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.order = append(c.order, leg)
}

// snapshot returns a copy of the recorded order under the lock.
func (c *bringUpCalls) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.order))
	copy(out, c.order)
	return out
}

// upConfig returns a Config rooted at a temp dir with an ephemeral HTTPS port
// so the forwarder binds a free loopback port rather than colliding with the
// default 8443.
func upConfig(t *testing.T) Config {
	t.Helper()
	return Config{Root: t.TempDir(), HTTPSPort: freeLoopbackPort(t), Out: io.Discard}
}

// freeLoopbackPort reserves a free loopback TCP port and returns it. The
// forwarder rebinds it immediately, so a brief race is acceptable for a unit
// test that asserts the bring-up sequence rather than the listener.
func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return p
}

// TestUpReportsSubstrateFailure_spec_17_4 asserts that when the substrate does
// not come up, Up reports the substrate failure rather than starting a gateway
// or degrading to an in-process executor. §17.4 S1: the gateway runs as an
// in-cluster pod, so an unavailable substrate makes lenny up report the failure.
//
// spec: §17.4 (an unavailable substrate makes lenny up report the failure; the
// gateway does not start).
func TestUpReportsSubstrateFailure_spec_17_4(t *testing.T) {
	withSubstrateSeams(t, false, &fakeLauncher{}, nil)
	calls := withBringUpSeams(t)

	_, err := Up(context.Background(), upConfig(t))
	if err == nil {
		t.Fatal("Up with no substrate = nil error, want the substrate-failure error")
	}
	// None of the in-cluster bring-up legs should have run.
	if order := calls.snapshot(); len(order) != 0 {
		t.Errorf("bring-up ran in-cluster legs %v with no substrate; want none", order)
	}
}

// TestUpSequenceOrder_spec_17_4 asserts the in-cluster bring-up runs its legs
// in the order the §17.4 sequence requires: the dev-bearer Secret is created
// before the non-image manifests are applied (so the gateway mount resolves),
// the gateway-readiness wait precedes the gateway-dialed registry seed, and
// the echo pool is applied after the runtime is seeded.
//
// spec: §17.4 (the bring-up creates the dev-bearer Secret before the gateway
// Deployment, waits for the gateway, then seeds the echo registry record and
// applies the warm pool), §4.6.2 (the echo pool is applied directly).
func TestUpSequenceOrder_spec_17_4(t *testing.T) {
	l := &fakeLauncher{gatewayHost: "127.0.0.1", kubeconfig: filepath.Join(t.TempDir(), "kubeconfig")}
	withSubstrateSeams(t, true, l, nil)
	// Stub the §4.7 activation seams so provisionClusterState resolves an echo
	// digest (so the echo pool apply is not skipped) and creates no real
	// objects.
	withActivationSeams(t, "ghcr.io/lennylabs/runtime-echo-embedded@sha256:"+
		"4444444444444444444444444444444444444444444444444444444444444444")
	withRuntimeClassSeam(t)
	calls := withBringUpSeams(t)

	s, err := Up(context.Background(), upConfig(t))
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	defer func() { _ = s.Stop(context.Background()) }()

	order := calls.snapshot()
	want := []string{"secret", "apply-nonimage", "apply-deploy", "wait", "seed", "pool"}
	if !subsequence(order, want) {
		t.Errorf("bring-up order = %v, want the subsequence %v (secret before non-image apply, deployments after, wait before seed, pool after seed)", order, want)
	}
	if !calls.seeded || !calls.poolApplied {
		t.Errorf("bring-up did not seed the registry record (%v) or apply the echo pool (%v)", calls.seeded, calls.poolApplied)
	}
}

// TestUpWarmReconcileSkipsReapplyOnTagMatch asserts a warm lenny up against a
// persisted substrate whose recorded deployed tag matches the running CLI
// version and whose gateway is already ready reuses the control plane: it
// skips the image import, the manifest apply, and the echo seed, and records
// the unchanged tag. This is the fast warm-restart path (C4).
//
// diagnosis: a failure means a warm lenny up re-runs the expensive
// import/apply/seed legs even though the persisted control plane is current
// and healthy, defeating the §17.4 substrate-persistence fast-restart.
//
// spec: §17.4 (the substrate persists across down/up; a matching tag against
// a ready gateway reuses the persisted control plane).
func TestUpWarmReconcileSkipsReapplyOnTagMatch_spec_17_4(t *testing.T) {
	l := &fakeLauncher{gatewayHost: "127.0.0.1", kubeconfig: filepath.Join(t.TempDir(), "kubeconfig")}
	withSubstrateSeams(t, true, l, nil)
	withActivationSeams(t, "ghcr.io/lennylabs/runtime-echo-embedded@sha256:"+
		"4444444444444444444444444444444444444444444444444444444444444444")
	withRuntimeClassSeam(t)
	calls := withBringUpSeams(t)
	// The persisted gateway is healthy, so a matching tag reuses it.
	prev := warmGatewayReadyFn
	t.Cleanup(func() { warmGatewayReadyFn = prev })
	warmGatewayReadyFn = func(context.Context, string) bool { return true }

	cfg := upConfig(t)
	cfg.CLIVersion = "v9.9.9"
	// Record a prior bring-up at the same CLI version so the warm reconcile
	// reuses the persisted control plane.
	paths := NewPaths(cfg.Root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	if err := writeState(paths.StateFile(), State{DeployedImageTag: "v9.9.9", K3sEnabled: true}); err != nil {
		t.Fatalf("seed prior state: %v", err)
	}

	s, err := Up(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	defer func() { _ = s.Stop(context.Background()) }()

	for _, leg := range []string{"secret", "import", "apply-nonimage", "apply-deploy", "seed", "pool"} {
		if containsLeg(calls.snapshot(), leg) {
			t.Errorf("warm reconcile ran the %q leg on a tag match; want it skipped (order=%v)", leg, calls.snapshot())
		}
	}
	// The gateway-readiness wait still runs so lenny up reports the gateway
	// ready before returning.
	if !containsLeg(calls.snapshot(), "wait") {
		t.Errorf("warm reconcile skipped the gateway-readiness wait: %v", calls.snapshot())
	}
	if s.State().DeployedImageTag != "v9.9.9" {
		t.Errorf("warm reconcile recorded tag %q, want the unchanged v9.9.9", s.State().DeployedImageTag)
	}
}

// TestUpWarmReconcileReappliesOnTagMismatch asserts a warm lenny up whose
// recorded deployed tag differs from the running CLI version (a CLI upgrade)
// re-imports the platform images and re-applies the manifests, so a stale
// image is not run after an upgrade (C4).
//
// diagnosis: a failure means a CLI upgrade keeps running the previously
// deployed image tag because lenny up did not re-import and re-apply.
//
// spec: §17.4 (an upgrade re-imports and re-applies on a CLI-version /
// image-tag mismatch).
func TestUpWarmReconcileReappliesOnTagMismatch_spec_17_4(t *testing.T) {
	l := &fakeLauncher{gatewayHost: "127.0.0.1", kubeconfig: filepath.Join(t.TempDir(), "kubeconfig")}
	withSubstrateSeams(t, true, l, nil)
	withActivationSeams(t, "ghcr.io/lennylabs/runtime-echo-embedded@sha256:"+
		"4444444444444444444444444444444444444444444444444444444444444444")
	withRuntimeClassSeam(t)
	calls := withBringUpSeams(t)
	// Even a healthy gateway does not suppress the re-apply on a tag mismatch.
	prev := warmGatewayReadyFn
	t.Cleanup(func() { warmGatewayReadyFn = prev })
	warmGatewayReadyFn = func(context.Context, string) bool { return true }

	cfg := upConfig(t)
	cfg.CLIVersion = "v2.0.0"
	paths := NewPaths(cfg.Root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	if err := writeState(paths.StateFile(), State{DeployedImageTag: "v1.0.0", K3sEnabled: true}); err != nil {
		t.Fatalf("seed prior state: %v", err)
	}

	s, err := Up(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	defer func() { _ = s.Stop(context.Background()) }()

	for _, leg := range []string{"import", "apply-nonimage", "apply-deploy", "seed", "pool"} {
		if !containsLeg(calls.snapshot(), leg) {
			t.Errorf("tag-mismatch reconcile skipped the %q leg; want a full re-apply (order=%v)", leg, calls.snapshot())
		}
	}
	if s.State().DeployedImageTag != "v2.0.0" {
		t.Errorf("reconcile recorded tag %q, want the upgraded v2.0.0", s.State().DeployedImageTag)
	}
}

// TestUpDownUpWarmRestartSkipsReapply drives the realistic warm-restart
// workflow the §17.4 fast path names: a first lenny up runs the full
// import/apply/seed, a non-`--purge` lenny down stops the substrate while
// persisting the Stopped marker (with the deployed tag), and a second lenny up
// at the same CLI version reconciles against the preserved tag and skips the
// expensive import, manifest apply, and echo seed. Without the persisted tag
// the down→up path would re-import and re-apply every time, defeating the
// 20-35s warm restart the substrate-persistence model buys.
//
// diagnosis: a failure means a non-`--purge` lenny down dropped the deployed
// image tag, so the next lenny up cannot recognize the persisted control plane
// as current and re-runs the platform-image import and the manifest apply that
// the §17.4 warm restart is supposed to skip.
//
// spec: §17.4 (the substrate and the imported-image store persist across a
// non-`--purge` down; a warm up at an unchanged CLI version reuses the
// persisted control plane and skips the re-import and re-apply).
func TestUpDownUpWarmRestartSkipsReapply_spec_17_4(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LENNY_HOME", root)
	const resolved = "ghcr.io/lennylabs/runtime-echo-embedded@sha256:" +
		"4444444444444444444444444444444444444444444444444444444444444444"

	l := &fakeLauncher{gatewayHost: "127.0.0.1", kubeconfig: filepath.Join(t.TempDir(), "kubeconfig")}
	withSubstrateSeams(t, true, l, nil)
	activation := withActivationSeams(t, resolved)
	withRuntimeClassSeam(t)
	calls := withBringUpSeams(t)
	// The persisted gateway answers as ready on the warm reconcile, so a
	// matching deployed tag reuses the control plane.
	prevReady := warmGatewayReadyFn
	t.Cleanup(func() { warmGatewayReadyFn = prevReady })
	warmGatewayReadyFn = func(context.Context, string) bool { return true }
	// Stub the substrate-stop seams so lenny down does not stop a real process
	// or container; the fakeLauncher records no PID and no container handle.
	prevStop, prevProc := stopSubstrateContainer, stopSubstrateProcess
	t.Cleanup(func() { stopSubstrateContainer, stopSubstrateProcess = prevStop, prevProc })
	stopSubstrateContainer = func(string) {}
	stopSubstrateProcess = func(int) {}

	// ----- First up: full import/apply/seed at v5.5.5. -----
	if err := RunUp(context.Background(), UpOptions{
		Root: root, HTTPSPort: freeLoopbackPort(t), CLIVersion: "v5.5.5", Out: io.Discard, ErrOut: io.Discard,
	}); err != nil {
		t.Fatalf("first RunUp: %v", err)
	}
	for _, leg := range []string{"import", "apply-nonimage", "apply-deploy", "seed", "pool"} {
		if !containsLeg(calls.snapshot(), leg) {
			t.Fatalf("first up skipped the %q leg; want a full first-run apply (order=%v)", leg, calls.snapshot())
		}
	}
	// The first up ran the per-cluster-state legs (the echo component image
	// import among them), which §17.4 designates as one-time first-run costs.
	if !activation.imageImported {
		t.Fatalf("first up did not import the echo component image; want the first-run import")
	}
	activation.imageImported = false

	// ----- Non-purge down: persists the Stopped marker with the deployed tag. -----
	if err := RunDown(context.Background(), DownOptions{Root: root, Out: io.Discard, ErrOut: io.Discard}); err != nil {
		t.Fatalf("RunDown: %v", err)
	}
	marker, ok, err := readState(NewPaths(root).StateFile())
	if err != nil || !ok || !marker.Stopped {
		t.Fatalf("after down: marker ok=%v stopped=%v err=%v, want a preserved Stopped marker", ok, marker.Stopped, err)
	}
	if marker.DeployedImageTag != "v5.5.5" {
		t.Fatalf("after down: marker DeployedImageTag = %q, want the preserved v5.5.5", marker.DeployedImageTag)
	}

	// ----- Second up at the same CLI version: reuse the persisted control plane. -----
	before := len(calls.snapshot())
	if err := RunUp(context.Background(), UpOptions{
		Root: root, HTTPSPort: freeLoopbackPort(t), CLIVersion: "v5.5.5", Out: io.Discard, ErrOut: io.Discard,
	}); err != nil {
		t.Fatalf("second RunUp: %v", err)
	}
	second := calls.snapshot()[before:]
	for _, leg := range []string{"secret", "import", "apply-nonimage", "apply-deploy", "seed", "pool"} {
		if containsLeg(second, leg) {
			t.Errorf("warm down→up ran the %q leg; want it skipped against the persisted tag (legs=%v)", leg, second)
		}
	}
	if !containsLeg(second, "wait") {
		t.Errorf("warm down→up skipped the gateway-readiness wait: %v", second)
	}
	// The warm down→up reuse must also skip the per-cluster-state legs that
	// provisionClusterState runs (the echo component image import and the CRD
	// install), the one-time first-run costs §17.4 names. A re-import on the
	// warm restart would re-read the echo tarball the persisted containerd
	// image store already holds, undercutting the 20-35s warm-restart target.
	if activation.imageImported {
		t.Errorf("warm down→up re-imported the echo component image; want it skipped against the persisted image store")
	}
}

// containsLeg reports whether the recorded leg list s holds v.
func containsLeg(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// TestUpImportFencesDeploymentApply_spec_17_4 pins the proposal's hard
// ordering invariant: the platform-image import must land before the
// Deployments are applied, so a scheduled pod never reaches the registry
// under IfNotPresent before its image is present in containerd. The test
// holds the import seam blocked until after the non-image apply has run, then
// releases it, and asserts the recorded "import" leg precedes "apply-deploy"
// and that the Deployment apply never ran before the import completed.
//
// spec: §17.4 (proposal 0017 C2: import the images, apply the non-image
// objects, fence on the import, then apply the Deployments so pods do not
// enter ImagePullBackOff).
func TestUpImportFencesDeploymentApply_spec_17_4(t *testing.T) {
	l := &fakeLauncher{gatewayHost: "127.0.0.1", kubeconfig: filepath.Join(t.TempDir(), "kubeconfig")}
	withSubstrateSeams(t, true, l, nil)
	withActivationSeams(t, "ghcr.io/lennylabs/runtime-echo-embedded@sha256:"+
		"4444444444444444444444444444444444444444444444444444444444444444")
	withRuntimeClassSeam(t)
	calls := withBringUpSeams(t)
	// Block the import until the test releases it. The bring-up runs the
	// import on its own goroutine concurrently with the non-image apply, so
	// the apply-nonimage leg records while the import is held, then the
	// applyControlPlane fence blocks on <-importDone before applyDeploy.
	calls.importHold = make(chan struct{})

	done := make(chan error, 1)
	go func() {
		s, err := Up(context.Background(), upConfig(t))
		if err == nil {
			_ = s.Stop(context.Background())
		}
		done <- err
	}()

	// Wait until the non-image apply has recorded, proving the bring-up
	// reached the apply phase while the import is still held; the Deployment
	// apply must not have run yet because the import has not completed.
	waitForLeg(t, calls, "apply-nonimage")
	if order := calls.snapshot(); indexOf(order, "apply-deploy") >= 0 {
		t.Fatalf("Deployment apply ran before the import landed: order = %v", order)
	}
	// Release the import; the fence unblocks and the Deployment apply proceeds.
	close(calls.importHold)

	if err := <-done; err != nil {
		t.Fatalf("Up: %v", err)
	}
	order := calls.snapshot()
	importIdx, deployIdx := indexOf(order, "import"), indexOf(order, "apply-deploy")
	if importIdx < 0 || deployIdx < 0 {
		t.Fatalf("bring-up missing import (%d) or apply-deploy (%d) leg: order = %v", importIdx, deployIdx, order)
	}
	if importIdx > deployIdx {
		t.Errorf("import landed after the Deployment apply (import=%d, apply-deploy=%d): order = %v; the import must be fenced before the Deployments", importIdx, deployIdx, order)
	}
}

// waitForLeg blocks until the named bring-up leg appears in the recorded
// order or the test times out, so a fence test can synchronize on a leg that
// runs on the bring-up goroutine.
func waitForLeg(t *testing.T, calls *bringUpCalls, leg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if indexOf(calls.snapshot(), leg) >= 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("bring-up did not reach the %q leg within the timeout; order = %v", leg, calls.snapshot())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// indexOf returns the first index of v in s, or -1.
func indexOf(s []string, v string) int {
	for i, e := range s {
		if e == v {
			return i
		}
	}
	return -1
}

// TestUpSkipsPoolWhenEchoImageUnresolved_spec_4_7 asserts that when the echo
// image did not import (no resolved digest), the bring-up still seeds the
// registry record but skips the echo warm-pool apply, because materializing a
// pool for a runtime whose image is absent would only ImagePullBackOff.
//
// spec: §4.7 (the digest-pinned pod image), §4.6.2 (the pool is applied only
// when the echo image resolved).
func TestUpSkipsPoolWhenEchoImageUnresolved_spec_4_7(t *testing.T) {
	l := &fakeLauncher{gatewayHost: "127.0.0.1", kubeconfig: filepath.Join(t.TempDir(), "kubeconfig")}
	withSubstrateSeams(t, true, l, nil)
	// Empty resolved image: provisionClusterState skips the Runtime CR apply.
	withActivationSeams(t, "")
	withRuntimeClassSeam(t)
	calls := withBringUpSeams(t)

	s, err := Up(context.Background(), upConfig(t))
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	defer func() { _ = s.Stop(context.Background()) }()

	if !calls.seeded {
		t.Error("bring-up did not seed the registry record when the echo image was unresolved")
	}
	if calls.poolApplied {
		t.Error("bring-up applied the echo warm pool with no resolved echo image; want it skipped")
	}
}

// withRuntimeClassSeam stubs the runc RuntimeClass install seam to a no-op so
// provisionClusterState does not dial a real API server in a unit test.
func withRuntimeClassSeam(t *testing.T) {
	t.Helper()
	prev := ensureRuntimeClassFn
	t.Cleanup(func() { ensureRuntimeClassFn = prev })
	ensureRuntimeClassFn = func(context.Context, string, string, string) error { return nil }
}

// subsequence reports whether want appears in order as a (not necessarily
// contiguous) subsequence of got.
func subsequence(got, want []string) bool {
	i := 0
	for _, g := range got {
		if i < len(want) && g == want[i] {
			i++
		}
	}
	return i == len(want)
}

// TestDeploymentReady covers the gateway-readiness predicate: a Deployment is
// ready only when its observed generation has caught up to the spec generation
// and it reports at least one ready replica, so a stale status from before a
// rollout does not read as ready.
//
// spec: §17.4 (lenny up reports the gateway ready when its Deployment is ready).
func TestDeploymentReady(t *testing.T) {
	cases := []struct {
		name string
		gen  int64
		obs  int64
		rdy  int32
		want bool
	}{
		{"ready", 3, 3, 1, true},
		{"no ready replicas", 3, 3, 0, false},
		{"status behind spec", 3, 2, 1, false},
		{"observed ahead", 3, 4, 2, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dep := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Generation: tc.gen},
				Status: appsv1.DeploymentStatus{
					ObservedGeneration: tc.obs,
					ReadyReplicas:      tc.rdy,
				},
			}
			if got := deploymentReady(dep); got != tc.want {
				t.Errorf("deploymentReady(gen=%d obs=%d ready=%d) = %v, want %v", tc.gen, tc.obs, tc.rdy, got, tc.want)
			}
		})
	}
}

// TestWarmGatewayReadyFalseOnUnreachableSubstrate covers the warm-reconcile
// health backing function: a kubeconfig that does not load (or names an
// unreachable API server) reports not-ready, so the warm reconcile falls back
// to a fresh apply rather than reusing an unhealthy persisted control plane.
//
// spec: §17.4 (an unhealthy persisted substrate falls back to a fresh apply).
func TestWarmGatewayReadyFalseOnUnreachableSubstrate_spec_17_4(t *testing.T) {
	// An absent kubeconfig path cannot build a rest config, so the probe
	// reports not-ready fail-closed.
	if warmGatewayReady(context.Background(), filepath.Join(t.TempDir(), "no-such-kubeconfig.yaml")) {
		t.Error("warmGatewayReady reported ready against a missing kubeconfig, want false (fail-closed)")
	}
}

// TestResolveWarmGatewayReadyTimeout covers the warm-reconcile readiness-window
// override (C4): with no override the window is the default pod-restart window,
// a valid duration override is honored, and an unparseable or non-positive
// override falls back to the default rather than disabling the wait. The window
// must be large enough that a gateway restarting with the persisted substrate
// is recognized as healthy rather than misread as unhealthy and forced into a
// full re-import and re-apply.
//
// spec: §17.4 (the down→up warm restart reuses the persisted control plane; a
// non-spec default is operator-tunable).
func TestResolveWarmGatewayReadyTimeout(t *testing.T) {
	cases := []struct {
		name     string
		override string
		want     time.Duration
	}{
		{"unset uses the default pod-restart window", "", defaultWarmGatewayReadyTimeout},
		{"a valid duration override is honored", "30s", 30 * time.Second},
		{"an unparseable override falls back", "not-a-duration", defaultWarmGatewayReadyTimeout},
		{"a zero override falls back rather than disabling the wait", "0s", defaultWarmGatewayReadyTimeout},
		{"a negative override falls back", "-5s", defaultWarmGatewayReadyTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.override == "" {
				os.Unsetenv(warmGatewayReadyTimeoutEnvVar)
			} else {
				t.Setenv(warmGatewayReadyTimeoutEnvVar, tc.override)
			}
			if got := resolveWarmGatewayReadyTimeout(); got != tc.want {
				t.Errorf("resolveWarmGatewayReadyTimeout(override=%q) = %s, want %s", tc.override, got, tc.want)
			}
		})
	}
	// The default window is comfortably larger than a few seconds so a
	// just-restarted gateway pod is not misread as unhealthy on the down→up
	// warm path.
	if defaultWarmGatewayReadyTimeout <= 30*time.Second {
		t.Errorf("defaultWarmGatewayReadyTimeout = %s, want a pod-restart-comparable window so a restarting gateway is not forced into a full re-apply", defaultWarmGatewayReadyTimeout)
	}
}

// TestEnsureDevBearerKeyPersistsAndReuses covers the dev-bearer key path: the
// first call writes a key file, and a second call reuses it rather than rotating
// it, so a bearer the CLI minted from the persisted key keeps verifying across a
// re-run of the bring-up.
//
// spec: §17.4 (the CLI mints the dev bearer from the persisted dev key the
// gateway trusts).
func TestEnsureDevBearerKeyPersistsAndReuses(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "oidc", "signing.key")
	if err := ensureDevBearerKey(keyFile); err != nil {
		t.Fatalf("first ensureDevBearerKey: %v", err)
	}
	first, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	if err := ensureDevBearerKey(keyFile); err != nil {
		t.Fatalf("second ensureDevBearerKey: %v", err)
	}
	second, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatalf("re-read key file: %v", err)
	}
	if string(first) != string(second) {
		t.Error("ensureDevBearerKey rotated the persisted key on a re-run; the CLI's already-minted bearer would stop verifying")
	}
}

// TestResolvePlatformBundleOverride covers the LENNY_PLATFORM_BUNDLE override:
// an existing path is resolved, and a non-existent override resolves to empty
// rather than erroring, because a missing bundle is non-fatal at bring-up.
func TestResolvePlatformBundleOverride(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "bundle.tar")
	if err := os.WriteFile(bundle, []byte("x"), 0o600); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
	t.Setenv(platformBundleEnvVar, bundle)
	if got := resolvePlatformBundle(); got != bundle {
		t.Errorf("resolvePlatformBundle override = %q, want %q", got, bundle)
	}

	t.Setenv(platformBundleEnvVar, filepath.Join(t.TempDir(), "absent.tar"))
	if got := resolvePlatformBundle(); got != "" {
		t.Errorf("resolvePlatformBundle with an absent override = %q, want empty", got)
	}
}

// TestCreateDevBearerSecretFromConfigMissingKeyFails asserts the dev-bearer
// Secret create fails closed when the persisted dev key file is absent, rather
// than creating an empty Secret the gateway would load as a malformed verifier.
// The read fails before any client is built, so the test needs no API server.
//
// spec: §10.2 (the gateway loads the dev HMAC key), §13 (fail closed on a
// credential-handling path).
func TestCreateDevBearerSecretFromConfigMissingKeyFails(t *testing.T) {
	err := createDevBearerSecretFromConfig(context.Background(), &rest.Config{Host: "https://127.0.0.1:1"},
		filepath.Join(t.TempDir(), "absent.key"))
	if err == nil {
		t.Fatal("createDevBearerSecretFromConfig with no key file = nil error, want a read failure")
	}
}
