//go:build component

// SPDX-License-Identifier: MIT

// Package embedded_test exercises the §17.4 Embedded Mode stack bring-up
// on a Docker-backed substrate (the macOS and Windows code path). It
// provisions a real embedded k3s container through the Docker-backed
// launcher, installs the production CRDs against the launcher's
// host-rewritten kubeconfig, and starts the production controller against
// that same kubeconfig. The CRD-install leg asserts a host-process
// Kubernetes client reaches the in-container API server across the
// host/Docker boundary; the controllers-start leg asserts the production
// controller process comes up and stays alive against that connection,
// which is the leg the non-Linux gate previously skipped.
//
// The Docker-backed launcher is the launcher New selects on macOS and
// Windows; this test constructs it explicitly through NewDockerLauncher so
// the macOS/Windows code path is exercised on a Docker-equipped Linux CI
// host. The live macOS/Windows-host leg (Docker Desktop on a real
// macOS/Windows host) is deferred where CI lacks Docker Desktop: these
// tests skip when no Docker daemon is reachable, stating that dependency.
// The controllers-start leg additionally needs the Go toolchain to build
// the lenny-controller binary and skips with that dependency stated when
// it is absent.
//
// spec: §17.4 (the embedded cluster comes up on every supported host; on
// macOS and Windows the embedded k3s runs as a Docker-backed container and
// the CRDs install and the controllers run against the host-rewritten
// kubeconfig), §24.19 (lenny up brings the substrate up).
package embedded_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/lennylabs/lenny/pkg/embedded/k3s"
	"github.com/lennylabs/lenny/pkg/embedded/stack"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// expectedCRDs are the lenny.dev CustomResourceDefinitions the embedded
// CRD install lands. The bring-up asserts each is registered and
// established through the host-rewritten kubeconfig.
var expectedCRDs = []string{
	"runtimes.lenny.dev",
	"sandboxes.lenny.dev",
	"sandboxclaims.lenny.dev",
	"sandboxtemplates.lenny.dev",
	"sandboxwarmpools.lenny.dev",
}

// requireDockerDaemon skips the test when no Docker daemon is reachable.
// The Docker-backed substrate needs a running daemon, not only the CLI;
// `docker info` probes the daemon. On a host without Docker (CI without
// Docker Desktop, for example) the live bring-up cannot run and the leg is
// deferred, stated here rather than failing.
func requireDockerDaemon(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI absent: the Docker-backed embedded bring-up requires Docker; " +
			"the live macOS/Windows-host leg runs where Docker Desktop is available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
		t.Skipf("docker daemon unreachable (%v): the Docker-backed embedded bring-up requires a running daemon; "+
			"the live macOS/Windows-host leg runs where Docker Desktop is available", err)
	}
}

// spec: §17.4 (the embedded k3s comes up as a Docker-backed container and
// the production CRDs install against the host-rewritten kubeconfig), §24.19.
// diagnosis: The Docker-backed embedded substrate did not come up, or the
//
//	CRD install / cluster connection across the host/Docker
//	boundary failed. Either the k3s container did not start
//	(check `docker logs` for the lenny-embedded-k3s container,
//	or the published API port is already bound), or the
//	host-rewritten kubeconfig does not reach the in-container API
//	server. Run `docker info` to confirm the daemon is up.
func TestDockerBackedBringUpInstallsCRDs(t *testing.T) {
	requireDockerDaemon(t)

	// A non-default host API port avoids colliding with a host-local k3s
	// or a developer's cluster on 6443.
	cfg := k3s.Config{
		Dir:          t.TempDir(),
		APIPort:      26443,
		ReadyTimeout: 3 * time.Minute,
	}
	launcher := k3s.NewDockerLauncher(cfg)
	t.Cleanup(func() { _ = launcher.Stop() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := launcher.Start(ctx); err != nil {
		t.Fatalf("Docker-backed k3s did not come up: %v", err)
	}

	// The Docker-backed launcher records a container handle, not a host
	// PID: its k3s runs inside the Docker VM. The handle is what lenny
	// status probes. spec: §24.19.
	if launcher.PID() != 0 {
		t.Errorf("Docker-backed launcher PID = %d, want 0 (k3s runs inside the Docker VM)", launcher.PID())
	}
	handle, ok := launcher.(interface{ ContainerName() string })
	if !ok || handle.ContainerName() == "" {
		t.Fatal("Docker-backed launcher did not expose a container handle for lenny status")
	}
	if !k3s.ContainerRunning(handle.ContainerName()) {
		t.Fatalf("container %s is not reported running after Start", handle.ContainerName())
	}

	// Install the production CRDs against the launcher's host-rewritten
	// kubeconfig — the same call Up makes. This is the bring-up leg the
	// non-Linux gate previously skipped.
	kubeconfig := launcher.KubeconfigPath()
	if err := stack.InstallCRDs(ctx, kubeconfig); err != nil {
		t.Fatalf("CRD install against the host-rewritten kubeconfig failed: %v", err)
	}

	// A host-process Kubernetes client reaches the in-container API server
	// across the host/Docker boundary through the rewritten kubeconfig —
	// the connection the production controllers make. Assert every
	// embedded CRD is registered and established.
	restCfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatalf("load host-rewritten kubeconfig: %v", err)
	}
	client, err := apiextensionsclient.NewForConfig(restCfg)
	if err != nil {
		t.Fatalf("build apiextensions client: %v", err)
	}
	for _, name := range expectedCRDs {
		if err := waitCRDEstablished(ctx, client, name, 30*time.Second); err != nil {
			t.Errorf("CRD %s not established through the host-rewritten kubeconfig: %v", name, err)
		}
	}

	// Idempotency: a second install does not error (Up is idempotent).
	if err := stack.InstallCRDs(ctx, kubeconfig); err != nil {
		t.Errorf("second CRD install errored; bring-up is not idempotent: %v", err)
	}
}

// spec: §17.4 (the production controllers run against the launcher's
// host-rewritten kubeconfig on every supported host; on macOS and Windows
// the host-process controller reaches the in-container API server across
// the host/Docker boundary), §24.19.
// diagnosis: The production controller did not start or did not stay alive
//
//	against the Docker-backed substrate's host-rewritten
//	kubeconfig — the controllers-start leg the non-Linux gate
//	previously skipped. Either the controller binary failed to
//	build, the host-rewritten kubeconfig does not reach the
//	in-container API server across the host/Docker boundary
//	(check the controller log for a connection-refused or
//	kubeconfig error), or the installed CRDs failed the
//	controller's schema-version preflight. Read the controller
//	log path printed on failure and run `docker logs` for the
//	lenny-embedded-k3s container.
func TestDockerBackedBringUpStartsController(t *testing.T) {
	requireDockerDaemon(t)
	controllerBin := buildControllerBin(t)

	// A non-default host API port avoids colliding with the CRD-install
	// test's container, the host-local k3s, or a developer's cluster.
	cfg := k3s.Config{
		Dir:          t.TempDir(),
		APIPort:      26444,
		ReadyTimeout: 3 * time.Minute,
	}
	launcher := k3s.NewDockerLauncher(cfg)
	t.Cleanup(func() { _ = launcher.Stop() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := launcher.Start(ctx); err != nil {
		t.Fatalf("Docker-backed k3s did not come up: %v", err)
	}

	// The controller's manager start validates every installed CRD's
	// schema-version annotation before it begins reconciling, so the CRDs
	// must be installed against the host-rewritten kubeconfig first — the
	// same order Up follows.
	kubeconfig := launcher.KubeconfigPath()
	if err := stack.InstallCRDs(ctx, kubeconfig); err != nil {
		t.Fatalf("CRD install against the host-rewritten kubeconfig failed: %v", err)
	}

	// Start the production controller against the launcher's host-rewritten
	// kubeconfig — the same call Up makes under `if k3sEnabled`, and the
	// leg the previous non-Linux gate skipped. PostgresDSN is left empty so
	// the §4.6.1 agent_pod_state mirror is disabled and the controller does
	// not require a live Postgres: the cross-boundary connection under test
	// is the controller's manager dialing the in-container API server, which
	// it makes regardless of the mirror. spec: §17.4.
	logPath := filepath.Join(t.TempDir(), "controller.log")
	ctl, err := stack.StartController(stack.ControllerSpec{
		BinPath:    controllerBin,
		Kubeconfig: kubeconfig,
		LogPath:    logPath,
	})
	if err != nil {
		t.Fatalf("starting the production controller against the host-rewritten kubeconfig failed: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Stop() })

	if ctl.PID() == 0 {
		t.Fatal("controller reported PID 0 immediately after StartController; the process did not launch")
	}

	// The controller must stay alive against the in-container API server.
	// Its manager resolves the cluster connection from the host-rewritten
	// KUBECONFIG, validates the installed CRDs' schema-version annotation
	// across the host/Docker boundary, then starts its informer cache. A
	// controller that cannot reach the API server (a kubeconfig that does
	// not traverse the boundary) or that finds the CRDs absent exits
	// non-zero during startup. Polling Running across the startup window
	// pins that the controller comes up and stays up against the rewritten
	// kubeconfig. spec: §17.4.
	if err := waitControllerStaysAlive(ctx, ctl, 20*time.Second); err != nil {
		log := readControllerLog(logPath)
		t.Fatalf("controller did not stay alive against the host-rewritten kubeconfig: %v\ncontroller log:\n%s", err, log)
	}
}

// buildControllerBin builds cmd/lenny-controller into a temp directory and
// returns its path. The Docker-backed controllers-start leg needs the real
// production controller binary; building it gates on the Go toolchain. A
// missing toolchain is a genuine external-dependency skip, stated rather
// than failing, matching the requireDockerDaemon convention.
func buildControllerBin(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain absent: building the production controller for the controllers-start leg requires the Go toolchain; " +
			"the leg runs where the toolchain is available")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "lenny-controller")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/lenny-controller")
	cmd.Dir = schematest.RepoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build cmd/lenny-controller: %v\n%s", err, out)
	}
	return bin
}

// waitControllerStaysAlive polls the controller's liveness across a startup
// window. The controller resolves its cluster connection, validates the
// CRDs, and starts its informer cache during this window; a controller that
// cannot reach the in-container API server exits non-zero before the window
// elapses. It returns nil when the controller is still running at the end
// of the window, or an error naming when it stopped.
func waitControllerStaysAlive(ctx context.Context, ctl *stack.Controller, window time.Duration) error {
	deadline := time.Now().Add(window)
	for {
		if !ctl.Running() {
			return errControllerExited
		}
		if time.Now().After(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// errControllerExited reports that the controller process exited during
// the startup window, which means it could not reach the in-container API
// server across the host/Docker boundary or failed its CRD preflight.
var errControllerExited = errors.New("controller exited during the startup window (it could not reach the " +
	"in-container API server across the host/Docker boundary or failed its CRD schema-version preflight)")

// readControllerLog reads the controller log file for the failure report,
// returning a placeholder when it is unreadable so the diagnosis is never
// empty.
func readControllerLog(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "(controller log unreadable: " + err.Error() + ")"
	}
	if len(b) == 0 {
		return "(controller log empty)"
	}
	return string(b)
}

// waitCRDEstablished polls the named CRD through the host-rewritten
// kubeconfig until it reports the Established condition or timeout elapses.
// Establishment is asynchronous: the API server marks a freshly-installed
// CRD established once it serves the resource, so a one-shot read can race
// the install. Each read is a real round trip across the host/Docker
// boundary, so a passing wait also confirms the connection.
func waitCRDEstablished(ctx context.Context, client apiextensionsclient.Interface, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		crd, err := client.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, name, metav1.GetOptions{})
		if err == nil && crdEstablished(crd) {
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return err
			}
			return context.DeadlineExceeded
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// crdEstablished reports whether a CRD has reached the Established
// condition, which is when the API server serves its resources and a
// controller can list and watch them across the host/Docker boundary.
func crdEstablished(crd *apiextensionsv1.CustomResourceDefinition) bool {
	for _, c := range crd.Status.Conditions {
		if c.Type == apiextensionsv1.Established {
			return c.Status == apiextensionsv1.ConditionTrue
		}
	}
	return false
}
