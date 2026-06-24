//go:build component

// SPDX-License-Identifier: MIT

// Package embedded_test exercises the §17.4 Embedded Mode stack bring-up
// on a Docker-backed substrate (the macOS and Windows code path). It
// provisions a real embedded k3s container through the Docker-backed
// launcher and installs the production CRDs against the launcher's
// host-rewritten kubeconfig. The CRD-install leg asserts a host-process
// Kubernetes client reaches the in-container API server across the
// host/Docker boundary, the same connection the in-cluster control plane's
// applier makes at bring-up.
//
// The §17.4 control plane runs as in-cluster pods rendered from the chart,
// so the host-process controller-start legs this file previously carried are
// removed; the in-cluster controller bring-up is exercised by the tier-4
// embedded smoke (proposal 0017 §5), which runs the controller as a pod.
//
// The Docker-backed launcher is the launcher New selects on macOS and
// Windows; this test constructs it explicitly through NewDockerLauncher so
// the macOS/Windows code path is exercised on a Docker-equipped Linux CI
// host. The live macOS/Windows-host leg (Docker Desktop on a real
// macOS/Windows host) is deferred where CI lacks Docker Desktop: these
// tests skip when no Docker daemon is reachable, stating that dependency.
//
// spec: §17.4 (the embedded cluster comes up on every supported host; on
// macOS and Windows the embedded k3s runs as a Docker-backed container and
// the CRDs install against the host-rewritten kubeconfig), §24.19 (lenny up
// brings the substrate up).
package embedded_test

import (
	"context"
	"os/exec"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/lennylabs/lenny/pkg/embedded/k3s"
	"github.com/lennylabs/lenny/pkg/embedded/stack"
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
	// kubeconfig — the same call the in-cluster applier makes. This is the
	// cross-boundary connection the in-cluster control plane depends on.
	kubeconfig := launcher.KubeconfigPath()
	if err := stack.InstallCRDs(ctx, kubeconfig); err != nil {
		t.Fatalf("CRD install against the host-rewritten kubeconfig failed: %v", err)
	}

	// A host-process Kubernetes client reaches the in-container API server
	// across the host/Docker boundary through the rewritten kubeconfig —
	// the connection the applier and the in-cluster pods' control plane make.
	// Assert every embedded CRD is registered and established.
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
