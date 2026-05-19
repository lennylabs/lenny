// SPDX-License-Identifier: MIT

package kind

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// lennyReleaseName is the Helm release name install.sh deploys and
// InstallLenny verifies. It matches the `helm install lenny ...`
// invocation in tests/testinfra/kind/install.sh.
const lennyReleaseName = "lenny"

// lennySystemNamespace is the namespace the Lenny control plane runs
// in. install.sh creates it and installs the chart into it.
const lennySystemNamespace = "lenny-system"

// installHint is the message InstallLenny skips with when the control
// plane is not deployed. It names the script that performs the install
// so the operator (or CI step) can act on the skip.
const installHint = "Lenny is not installed on the Kind cluster; run tests/testinfra/kind/install.sh first."

// agentNamespace is the namespace the agent-pod workload runs in. The
// chart's agent-namespaces template creates it and install.sh applies
// tests/testinfra/kind/agent-workload.yaml into it after the chart
// install.
const agentNamespace = "lenny-agents"

// agentWorkloadHint is the message the agent-pod helper skips with when
// no Ready managed agent pod is present. It names the script and the
// manifest that stand up the workload.
const agentWorkloadHint = "no Ready agent-pod workload in namespace " + agentNamespace +
	"; run tests/testinfra/kind/install.sh — its final step applies " +
	"tests/testinfra/kind/agent-workload.yaml, the two §4.7 deployment-model warm pools."

// InstallLenny ensures a Kind cluster is up and verifies that the
// Lenny control plane is installed on it, returning the cluster handle.
//
// InstallLenny does NOT build images or run `helm install`; standing
// up the control plane is the job of tests/testinfra/kind/install.sh.
// The helper only ensures the cluster exists (via EnsureCluster),
// confirms the `lenny` Helm release is deployed, and confirms the
// control-plane pods are Ready. When the release is absent or the pods
// are not Ready, InstallLenny calls t.Skip with installHint rather
// than failing: a tier-5 run on a host where the operator has not run
// the install script skips cleanly instead of reporting a spurious
// failure.
func InstallLenny(t testing.TB) *Cluster {
	t.Helper()
	c := EnsureCluster(t)

	if !releaseDeployed(c) {
		t.Skip(installHint)
	}
	if !controlPlaneReady(c) {
		t.Skip(installHint + " (Helm release present but control-plane pods are not Ready)")
	}
	return c
}

// AgentPod identifies one managed agent pod and the warm pool that
// owns it. Model is the §4.7 deployment model the pool declares:
// "sidecar" (the runtime in a separate container bridged to the
// adapter) or "embedded" (one container whose image embeds the
// adapter), derived from the pool name suffix the agent workload sets.
type AgentPod struct {
	Name  string
	Pool  string
	Model string
}

// RequireAgentWorkload returns the managed agent pods running in the
// lenny-agents namespace, skipping the calling test when the workload
// is absent or not Ready. It is the agent-pod analogue of InstallLenny:
// a tier-5/8/9 test that needs a live agent pod calls it to obtain the
// workload and to skip cleanly on a host where install.sh has not
// applied tests/testinfra/kind/agent-workload.yaml.
//
// The agent pods carry the lenny.dev/managed=true label the Sandbox
// reconciler stamps and a lenny.dev/pool label naming their warm pool.
func RequireAgentWorkload(t testing.TB, c *Cluster) []AgentPod {
	t.Helper()
	out, err := c.Kubectl(
		"-n", agentNamespace, "get", "pods",
		"-l", "lenny.dev/managed=true",
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\t\"}"+
			"{.metadata.labels.lenny\\.dev/pool}{\"\\n\"}{end}",
	).Output()
	if err != nil {
		t.Skip(agentWorkloadHint)
	}
	var pods []AgentPod
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 2 || fields[0] == "" {
			continue
		}
		pods = append(pods, AgentPod{
			Name:  fields[0],
			Pool:  fields[1],
			Model: deploymentModel(fields[1]),
		})
	}
	if len(pods) == 0 {
		t.Skip(agentWorkloadHint)
	}
	// install.sh waits for the workload to become Ready; a test run
	// against an install that is still settling waits here too.
	if err := c.Kubectl(
		"-n", agentNamespace, "wait", "--for=condition=Ready",
		"pod", "-l", "lenny.dev/managed=true", "--timeout=120s",
	).Run(); err != nil {
		t.Skip(agentWorkloadHint + " (pods present but not Ready)")
	}
	return pods
}

// deploymentModel maps an agent workload pool name to its §4.7
// deployment model. agent-workload.yaml names the pools with a
// -sidecar or -embedded suffix; any other name reports "unknown".
func deploymentModel(pool string) string {
	switch {
	case strings.HasSuffix(pool, "-sidecar"):
		return "sidecar"
	case strings.HasSuffix(pool, "-embedded"):
		return "embedded"
	default:
		return "unknown"
	}
}

// releaseDeployed reports whether the `lenny` Helm release in
// lenny-system is in the deployed state. It shells out to
// `helm status -o json` and inspects the release status; any error
// (helm missing, release absent) yields false so the caller skips.
func releaseDeployed(c *Cluster) bool {
	if _, err := exec.LookPath("helm"); err != nil {
		return false
	}
	out, err := exec.Command(
		"helm", "status", lennyReleaseName,
		"-n", lennySystemNamespace,
		"--kubeconfig", c.KubeconfigPath,
		"-o", "json",
	).Output()
	if err != nil {
		return false
	}
	// The JSON status document carries `"status":"deployed"` for a
	// healthy release. A substring check avoids pulling in a JSON
	// decoder for this single field and tolerates key ordering.
	return strings.Contains(string(out), `"status":"deployed"`)
}

// controlPlaneReady reports whether every pod carrying the
// app.kubernetes.io/name=lenny label in lenny-system is in the Running
// phase. The chart labels the controller, gateway, token-service, ops,
// and every admission-webhook pod with that selector, so a true result
// means the full control plane is up.
//
// The check reads pod phases via jsonpath. An empty pod list (the
// chart never installed) or any non-Running phase yields false.
func controlPlaneReady(c *Cluster) bool {
	out, err := c.Kubectl(
		"-n", lennySystemNamespace,
		"get", "pods",
		"-l", "app.kubernetes.io/name=lenny",
		"-o", "jsonpath={range .items[*]}{.status.phase}{\"\\n\"}{end}",
	).Output()
	if err != nil {
		return false
	}
	phases := strings.Fields(string(out))
	if len(phases) == 0 {
		return false
	}
	for _, phase := range phases {
		if phase != "Running" {
			return false
		}
	}
	return true
}

// ApplyStdin pipes manifest to `kubectl apply -f -` against the
// cluster and returns the command's combined output and error. Unlike
// Apply, it does not call t.Fatalf on a non-zero exit: admission tests
// deliberately apply manifests the API server must reject, and need
// the error and the rejection message to assert against.
func (c *Cluster) ApplyStdin(t testing.TB, manifest string) (string, error) {
	t.Helper()
	cmd := exec.Command("kubectl", "--kubeconfig", c.KubeconfigPath, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// DryRunApplyStdin pipes manifest to `kubectl apply -f -
// --dry-run=server` against the cluster and returns the combined
// output and error. A server-side dry-run sends the object through the
// full admission chain (every validating webhook runs) but the API
// server discards it instead of persisting it. Admission tests use it
// to assert a webhook's rejection without leaving a resource behind:
// an admitted object never reaches etcd, and a rejected one yields the
// rejection message in the returned output.
func (c *Cluster) DryRunApplyStdin(t testing.TB, manifest string) (string, error) {
	t.Helper()
	cmd := exec.Command(
		"kubectl", "--kubeconfig", c.KubeconfigPath,
		"apply", "-f", "-", "--dry-run=server",
	)
	cmd.Stdin = strings.NewReader(manifest)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// DeleteStdin pipes manifest to `kubectl delete -f - --ignore-not-found`
// against the cluster and returns the combined output and error. Tests
// use it in a t.Cleanup to remove a resource an apply created, so a
// missing resource (the apply was rejected) is not an error.
func (c *Cluster) DeleteStdin(t testing.TB, manifest string) (string, error) {
	t.Helper()
	cmd := exec.Command(
		"kubectl", "--kubeconfig", c.KubeconfigPath,
		"delete", "-f", "-", "--ignore-not-found",
	)
	cmd.Stdin = strings.NewReader(manifest)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// KubectlOut runs `kubectl <args...>` against the cluster and returns
// the combined output and error. It is the read-mostly companion to
// Kubectl for tests that only need the output string.
func (c *Cluster) KubectlOut(t testing.TB, args ...string) (string, error) {
	t.Helper()
	out, err := c.Kubectl(args...).CombinedOutput()
	return string(out), err
}
