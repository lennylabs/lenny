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
// adapter), read from the spec.deploymentModel of the Runtime the pod's
// lenny.dev/runtime label names. It is "unknown" when that Runtime is
// absent from the cluster.
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
			"{.metadata.labels.lenny\\.dev/pool}{\"\\t\"}"+
			"{.metadata.labels.lenny\\.dev/runtime}{\"\\n\"}{end}",
	).Output()
	if err != nil {
		t.Skip(agentWorkloadHint)
	}
	models := deploymentModels(c)
	var pods []AgentPod
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || fields[0] == "" {
			continue
		}
		model, ok := models[fields[2]]
		if !ok {
			model = "unknown"
		}
		pods = append(pods, AgentPod{
			Name:  fields[0],
			Pool:  fields[1],
			Model: model,
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

// deploymentModels reads the §4.7 deployment model every Runtime on the
// cluster declares, keyed by Runtime name. The Runtime CRD's
// spec.deploymentModel is the authoritative statement of the model: it
// is what the Sandbox reconciler renders the pod's container topology
// from. Deriving the model from the pool's name instead requires every
// new reference pool to be added to a name list, and a pool that is not
// on it reports no recognized model even though its Runtime declares
// one.
//
// A read failure yields an empty map, so every pod reports "unknown"
// and the caller decides what that means.
//
// spec: §4.7
func deploymentModels(c *Cluster) map[string]string {
	out, err := c.Kubectl(
		"get", "runtimes.lenny.dev", "-A",
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\t\"}"+
			"{.spec.deploymentModel}{\"\\n\"}{end}",
	).Output()
	if err != nil {
		return map[string]string{}
	}
	models := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 2 || fields[0] == "" || fields[1] == "" {
			continue
		}
		models[fields[0]] = fields[1]
	}
	return models
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
// phase with its Ready condition true. The chart labels the controller,
// gateway, token-service, ops, and every admission-webhook pod with that
// selector, so a true result means the full control plane is serving.
//
// The check reads each pod's phase and Ready condition via jsonpath. Any
// error (kubectl missing, namespace absent) yields false so the caller
// skips.
func controlPlaneReady(c *Cluster) bool {
	out, err := c.Kubectl(
		"-n", lennySystemNamespace,
		"get", "pods",
		"-l", "app.kubernetes.io/name=lenny",
		"-o", "jsonpath={range .items[*]}{.status.phase}{\"\\t\"}"+
			"{.status.conditions[?(@.type==\"Ready\")].status}{\"\\n\"}{end}",
	).Output()
	if err != nil {
		return false
	}
	return allPodsRunningAndReady(string(out))
}

// allPodsRunningAndReady parses the phase/Ready-condition lines
// controlPlaneReady collects and reports whether every pod is Running with
// Ready=True. It is separated from the kubectl call so the parse can be
// exercised in-process.
//
// A pod whose phase is Running but whose Ready condition is not True is
// serving nothing: its container is restarting, still pulling, or failing
// its readiness probe. Admitting it lets a tier-5 test port-forward to a
// pod that never answers and report a confusing assertion failure instead
// of a clean skip, so the gate fails closed on anything that is not
// exactly Running with Ready=True. An empty list (the chart was never
// installed) is also not ready.
func allPodsRunningAndReady(jsonpathOut string) bool {
	pods := 0
	for _, line := range strings.Split(jsonpathOut, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		phase, ready, ok := strings.Cut(line, "\t")
		if !ok {
			return false
		}
		if strings.TrimSpace(phase) != "Running" || strings.TrimSpace(ready) != "True" {
			return false
		}
		pods++
	}
	return pods > 0
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

// ApplyStdinAs pipes manifest to `kubectl apply -f -` impersonating
// username, and returns the combined output and error.
//
// A write the platform admits only from one of its own principals needs
// this: the lenny-pool-config-validator webhook rejects a
// SandboxTemplate or SandboxWarmPool spec write from any principal
// other than the PoolScalingController service account, so a test that
// needs such an object on the cluster creates it as that principal
// rather than as the kubeconfig's cluster-admin.
//
// spec: §4.6.3
func (c *Cluster) ApplyStdinAs(t testing.TB, manifest, username string) (string, error) {
	t.Helper()
	return c.kubectlStdinAs(t, manifest, username, "apply")
}

// ReplaceStdinAs pipes manifest to `kubectl replace -f -` impersonating
// username, and returns the combined output and error.
//
// `kubectl apply` on an existing object issues a PATCH, and a
// least-privilege platform principal holds `update` without `patch`:
// the PoolScalingController's ClusterRole is the case in point, because
// controller-runtime writes a whole object rather than a patch. An
// update issued as such a principal therefore goes through replace,
// which is a PUT.
func (c *Cluster) ReplaceStdinAs(t testing.TB, manifest, username string) (string, error) {
	t.Helper()
	return c.kubectlStdinAs(t, manifest, username, "replace")
}

// kubectlStdinAs runs `kubectl <verb> -f -` with the manifest on stdin,
// impersonating username.
func (c *Cluster) kubectlStdinAs(t testing.TB, manifest, username, verb string) (string, error) {
	t.Helper()
	cmd := c.Kubectl(verb, "--as", username, "-f", "-")
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
