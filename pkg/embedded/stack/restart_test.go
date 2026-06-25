// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"errors"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// withClusterClient swaps the package-level cluster-client seam for the
// duration of a test so the cluster-backed status, logs, and restart paths run
// against an injected fake clientset rather than a real API server, then
// restores it.
func withClusterClient(t *testing.T, client kubernetes.Interface) {
	t.Helper()
	prev := clusterClientFn
	t.Cleanup(func() { clusterClientFn = prev })
	clusterClientFn = func(string) (kubernetes.Interface, error) { return client, nil }
}

// recordRunningStack writes a running-stack state file under LENNY_HOME with a
// recorded kubeconfig path so the cluster-backed commands resolve a client
// seam. The kubeconfig path need not exist because the client seam is injected.
func recordRunningStack(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("LENNY_HOME", home)
	paths := NewPaths(home)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st := State{
		K3sEnabled:           true,
		KubeconfigPath:       "/state/k3s/kubeconfig.yaml",
		GatewayForwarderAddr: "127.0.0.1:8443",
	}
	if err := writeState(paths.StateFile(), st); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	return home
}

// spec: §24.19 line 264 — the pod-backed components (gateway, controller,
// ops Deployments) are individually restartable; the removed host-process
// components are not.
func TestRestartableComponents_spec_24_19_264(t *testing.T) {
	for _, name := range []string{"gateway", "controller", "ops"} {
		if !Restartable(name) {
			t.Errorf("Restartable(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"redis", "postgres", "oidc", "k3s", "supervisor", ""} {
		if Restartable(name) {
			t.Errorf("Restartable(%q) = true, want false", name)
		}
	}
}

func TestRunRestartRequiresComponent(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	err := RunRestart(context.Background(), RestartOptions{Component: ""})
	if err == nil || !strings.Contains(err.Error(), "component") {
		t.Errorf("empty component error = %v, want a required-argument error", err)
	}
}

func TestRunRestartRejectsUnknownComponent(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	err := RunRestart(context.Background(), RestartOptions{Component: "redis"})
	if err == nil || !strings.Contains(err.Error(), "cannot be restarted individually") {
		t.Errorf("unknown-component error = %v, want a rejection", err)
	}
}

// spec: §24.19 line 264 — restart against a stack that is not running
// reports ErrNoRunningStack so the CLI can present a precise message.
func TestRunRestartNoStack_spec_24_19_264(t *testing.T) {
	t.Setenv("LENNY_HOME", t.TempDir())
	err := RunRestart(context.Background(), RestartOptions{Component: "gateway"})
	if !errors.Is(err, ErrNoRunningStack) {
		t.Errorf("error = %v, want ErrNoRunningStack", err)
	}
}

// TestRunRestartRollsDeployment covers the §24.19/§17.4 rollout-restart path:
// against a recorded stack, RunRestart patches the named control-plane
// Deployment's pod template with the restartedAt annotation through the
// embedded kubeconfig, the same change kubectl rollout restart makes, so the
// Deployment rolls without changing its desired spec. Each restartable
// component maps to its Deployment.
//
// spec: §24.19 line 264 (the restart is a Deployment rollout-restart), §17.4
// (the control plane runs as in-cluster Deployments).
func TestRunRestartRollsDeployment_spec_24_19_264(t *testing.T) {
	cases := []struct {
		component  string
		deployment string
	}{
		{"gateway", gatewayDeploymentName},
		{"controller", controllerDeploymentName},
		{"ops", opsDeploymentName},
	}
	for _, tc := range cases {
		t.Run(tc.component, func(t *testing.T) {
			recordRunningStack(t)
			client := k8sfake.NewSimpleClientset(readyDeployment(tc.deployment, tc.component))
			withClusterClient(t, client)

			var out strings.Builder
			if err := RunRestart(context.Background(), RestartOptions{Component: tc.component, Out: &out}); err != nil {
				t.Fatalf("RunRestart(%s): %v", tc.component, err)
			}
			got, err := client.AppsV1().Deployments(controlPlaneNamespace).Get(context.Background(), tc.deployment, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("get rolled deployment: %v", err)
			}
			if _, ok := got.Spec.Template.Annotations[restartedAtAnnotation]; !ok {
				t.Errorf("rolled Deployment %s carries no %s annotation; template annotations: %v",
					tc.deployment, restartedAtAnnotation, got.Spec.Template.Annotations)
			}
			if !strings.Contains(out.String(), tc.component) {
				t.Errorf("restart output %q does not name the rolled component", out.String())
			}
		})
	}
}

// TestRunRestartMissingDeploymentFailsClosed covers the fail-closed path: a
// recorded stack whose cluster does not carry the named Deployment surfaces the
// patch failure rather than reporting a successful restart, so an operator is
// not told a component rolled when it did not.
//
// spec: §24.19 line 264 (the restart targets a real Deployment).
func TestRunRestartMissingDeploymentFailsClosed_spec_24_19_264(t *testing.T) {
	recordRunningStack(t)
	// An empty cluster carries no Deployments.
	withClusterClient(t, k8sfake.NewSimpleClientset())
	err := RunRestart(context.Background(), RestartOptions{Component: "gateway"})
	if err == nil {
		t.Fatal("RunRestart against a cluster with no gateway Deployment = nil, want a roll failure")
	}
}

// TestRunRestartNoKubeconfigFailsClosed covers the path where a recorded stack
// has no kubeconfig (the substrate did not come up): RunRestart fails closed
// rather than building a client against an empty path.
func TestRunRestartNoKubeconfigFailsClosed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LENNY_HOME", home)
	paths := NewPaths(home)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	if err := writeState(paths.StateFile(), State{K3sEnabled: true}); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	err := RunRestart(context.Background(), RestartOptions{Component: "gateway"})
	if err == nil || !strings.Contains(err.Error(), "kubeconfig") {
		t.Errorf("error = %v, want a missing-kubeconfig rejection", err)
	}
}
