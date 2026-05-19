// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the §12.9.3 lenny-label-immutability UPDATE
// guard. The e2e Kind cluster runs a real agent-pod workload in the
// lenny-agents namespace (the two §4.7 deployment-model warm pools that
// install.sh applies from agent-workload.yaml), so the
// label-immutability webhook can be exercised against a live agent pod
// rather than only inspected for its configuration.
//
// Each warm agent pod carries lenny.dev/managed=true, an immutable
// label the Sandbox reconciler stamps. The webhook is installed
// fail-closed on Pod CREATE and UPDATE in agent namespaces and rejects
// any mutation of that label on an existing pod. The test drives a
// server-side dry-run label mutation against a live pod: a server
// dry-run runs the validating webhook chain but persists nothing, so
// the live pod's label is never actually changed and the test observes
// the rejection without disturbing the workload.

package tier9_security_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// labelImmutabilityWebhook is the admission webhook name the
// lenny-label-immutability ValidatingWebhookConfiguration registers.
// Rejection messages from it carry this name.
const labelImmutabilityWebhook = "label-immutability.lenny.dev"

// managedLabel is the immutable label every managed agent pod carries.
// The §17.2 item-5 label-immutability rule forbids mutating it on an
// existing pod.
const managedLabel = "lenny.dev/managed"

// spec: 12.9.3
// diagnosis: the §12.9.3 lenny-label-immutability UPDATE guard does not
// reject mutation of an immutable agent-pod label. The test takes a
// live managed agent pod from the e2e warm-pool workload and issues a
// server-side dry-run that flips lenny.dev/managed from true to false.
// The webhook must reject the write with the label-immutability reason.
// An admitted mutation means an actor with pod UPDATE could rewrite the
// label that gates the pod's managed-resource posture.
func TestAdmissionLabelImmutability(t *testing.T) {
	c := kind.InstallLenny(t)
	pods := kind.RequireAgentWorkload(t, c)

	// Any managed pod carries lenny.dev/managed=true; the first one is
	// a sufficient subject for the UPDATE-guard probe.
	pod := pods[0]

	// A server-side dry-run sends the UPDATE through the full admission
	// chain (the label-immutability webhook runs) but the API server
	// discards it, so the live pod's label is never persisted-over.
	out, err := c.KubectlOut(
		t,
		"-n", agentNamespace, "label", "pod", pod.Name,
		managedLabel+"=false", "--overwrite", "--dry-run=server",
	)
	if err == nil {
		t.Fatalf("§12.9.3 violation: the API server admitted a dry-run UPDATE that flips %s on the "+
			"managed agent pod %q from true to false; the lenny-label-immutability webhook did not "+
			"reject the mutation of an immutable label.\noutput:\n%s", managedLabel, pod.Name, out)
	}
	if !strings.Contains(out, labelImmutabilityWebhook) {
		t.Fatalf("the %s label mutation on pod %q was rejected, but not by the %s webhook — the "+
			"rejection came from elsewhere in the admission chain, so the webhook itself may not "+
			"guard this label.\noutput:\n%s", managedLabel, pod.Name, labelImmutabilityWebhook, out)
	}
	// The webhook's rejection body names the label and the forbidden
	// transition. Assert the label key appears so the rejection is
	// attributable to the lenny.dev/managed immutability rule and not to
	// an unrelated denial that happens to mention the webhook.
	if !strings.Contains(out, managedLabel) {
		t.Errorf("the %s rejection of the %q label mutation does not name the %s label; the "+
			"rejection may not be the immutability rule firing.\noutput:\n%s",
			labelImmutabilityWebhook, pod.Name, managedLabel, out)
	}
	t.Logf("§12.9.3: %s rejected the dry-run mutation of %s on managed pod %q (pool %q, %s model)",
		labelImmutabilityWebhook, managedLabel, pod.Name, pod.Pool, pod.Model)
}
