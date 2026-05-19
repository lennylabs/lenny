// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind tests for the §13.7 install lifecycle: the
// lenny-ops first deploy and the chart's first-install bootstrap. Each
// test installs the Lenny control plane on a Kind cluster via the
// install.sh-backed kind.InstallLenny harness and asserts the named
// behaviour against the live cluster.

package tier5_e2e_kind_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// spec: 13.7
// diagnosis: the §13.7 lenny-ops component did not deploy. The test
// confirms the lenny-ops Deployment exists in lenny-system and that
// its desired replica count is fully Ready. lenny-ops is the
// operability control loop; a failure means the chart did not render
// the Deployment or its pods never reached Ready.
func TestLennyOpsFirstDeploy(t *testing.T) {
	c := kind.InstallLenny(t)

	// .status.readyReplicas is absent until at least one replica is
	// ready, so the jsonpath defaults it to 0 via the spec/status pair.
	out, err := c.KubectlOut(
		t,
		"-n", "lenny-system", "get", "deployment", "lenny-ops",
		"-o", "jsonpath={.spec.replicas}/{.status.readyReplicas}",
	)
	if err != nil {
		t.Fatalf("the lenny-ops Deployment is not present in lenny-system: %v\n%s", err, out)
	}
	desired, ready, ok := strings.Cut(strings.TrimSpace(out), "/")
	if !ok || desired == "" {
		t.Fatalf("unexpected lenny-ops replica output: %q", out)
	}
	if ready == "" {
		ready = "0"
	}
	if desired != ready {
		t.Fatalf("lenny-ops Deployment is not fully Ready: %s/%s replicas ready", ready, desired)
	}
	t.Logf("lenny-ops Deployment is Ready: %s/%s replicas", ready, desired)
}

// spec: 13.7
// diagnosis: the §17.6 first-install bootstrap did not complete. The
// test asserts the Helm release `lenny` reports the deployed status
// and that its install hooks (the pre-install lenny-preflight Job and
// the post-install lenny-bootstrap Job) ran. Both hook Jobs carry
// helm.sh/hook-delete-policy: hook-succeeded, so a successful run
// deletes the Job object; their success is therefore read from the
// release Secret's status rather than from a surviving Job. A failed
// hook leaves the release in a failed or pending state, which this
// test catches.
func TestBootstrapFirstInstall(t *testing.T) {
	c := kind.InstallLenny(t)

	// Helm records each revision as a Secret in the release namespace
	// labelled with the release status. A first install that completed
	// its pre-install preflight hook and its post-install bootstrap hook
	// is recorded with status "deployed"; a hook failure leaves "failed"
	// or "pending-install".
	status, err := helmReleaseStatus(t, c)
	if err != nil {
		t.Fatalf("read Helm release status: %v", err)
	}
	if status != "deployed" {
		t.Fatalf("the Lenny Helm release is in status %q; a first install that completed "+
			"its pre-install and post-install hook Jobs reports \"deployed\"", status)
	}

	// The post-install lenny-bootstrap Job seeds Day-1 state. Its
	// hook-succeeded delete policy removes the Job on success, so the
	// install completing (status deployed) is the assertion that the
	// hook ran; an unfinished or failed bootstrap blocks the release
	// from reaching deployed. Confirm the seed ConfigMap the Job
	// consumes is present, which proves the bootstrap hook chain
	// rendered.
	cmOut, err := c.KubectlOut(
		t,
		"-n", "lenny-system", "get", "configmap", "lenny-bootstrap-values",
		"-o", "jsonpath={.metadata.name}",
	)
	if err != nil || strings.TrimSpace(cmOut) != "lenny-bootstrap-values" {
		t.Fatalf("the lenny-bootstrap-values ConfigMap (the bootstrap Job's seed input) is absent: %v\n%s", err, cmOut)
	}
	t.Logf("Lenny Helm release status is %q; bootstrap hook chain present", status)
}

// helmReleaseStatus returns the deployment status of the `lenny` Helm
// release in lenny-system by reading `helm status -o json`.
func helmReleaseStatus(t *testing.T, c *kind.Cluster) (string, error) {
	t.Helper()
	out, err := c.KubectlOut(
		t, "get", "secret",
		"-n", "lenny-system",
		"-l", "owner=helm,name=lenny",
		"-o", "jsonpath={range .items[*]}{.metadata.labels.status}{\"\\n\"}{end}",
	)
	if err != nil {
		return "", err
	}
	// Helm stores one release Secret per revision; the label `status`
	// on the highest-revision Secret is the current release status.
	// For a clean first install there is a single Secret labelled
	// status=deployed.
	lines := strings.Fields(out)
	if len(lines) == 0 {
		return "", errNoReleaseSecret
	}
	return lines[len(lines)-1], nil
}

// errNoReleaseSecret is returned when no Helm release Secret for the
// `lenny` release is found in lenny-system.
var errNoReleaseSecret = errString("no Helm release Secret found for release \"lenny\" in lenny-system")

// errString is a minimal error type so the lifecycle test does not
// pull in fmt or errors for a single sentinel.
type errString string

func (e errString) Error() string { return string(e) }
