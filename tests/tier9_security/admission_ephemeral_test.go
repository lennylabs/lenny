// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the §12.9.3 lenny-ephemeral-container-cred-guard
// webhook. The e2e Kind cluster runs a real agent-pod workload in the
// lenny-agents namespace, so the guard can be exercised against a live
// agent pod by attempting an ephemeral-container attach through the
// admission chain.
//
// The guard is installed fail-closed on the pods/ephemeralcontainers
// UPDATE subresource in agent namespaces. An actor with update on that
// subresource could otherwise attach a debug container that inherits
// the pod-level fsGroup and reads /run/lenny/credentials.json. §13.1
// condition (iii) rejects any ephemeral container that omits runAsUser,
// runAsGroup, or supplementalGroups, because an absent value inherits
// the pod credential-group defaults.
//
// The test issues the attach as a server-side dry-run against a live
// agent pod: the API server runs the validating webhook chain but
// discards the write, so no ephemeral container is actually added to
// the running pod.

package tier9_security_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// ephemeralCredGuardWebhook is the admission webhook name the
// lenny-ephemeral-container-cred-guard ValidatingWebhookConfiguration
// registers. Rejection messages from it carry this name.
const ephemeralCredGuardWebhook = "ephemeral-container-cred-guard.lenny.dev"

// ephemeralCredRejectionCode is the §15.1 error code the guard stamps
// on every rejection.
const ephemeralCredRejectionCode = "EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN"

// spec: 12.9.3
// diagnosis: the §12.9.3 lenny-ephemeral-container-cred-guard does not
// reject an ephemeral container that could reach the pod credential
// file. The test takes a live managed agent pod and issues a
// server-side dry-run that attaches an ephemeral container with no
// securityContext, which under §13.1 condition (iii) inherits the
// pod-level credential-group defaults. The guard must reject the attach
// with EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN. An admitted attach means
// an actor with pods/ephemeralcontainers UPDATE could read
// /run/lenny/credentials.json.
func TestAdmissionEphemeralContainerCredUIDForbidden(t *testing.T) {
	c := kind.InstallLenny(t)
	pods := kind.RequireAgentWorkload(t, c)

	// Any managed agent pod is a sufficient subject: the guard is scoped
	// to the pods/ephemeralcontainers subresource in the agent
	// namespace, independent of the pod's warm pool or deployment model.
	pod := pods[0]

	// `kubectl replace --raw` reads the request body from a file and
	// posts it to the ephemeralcontainers subresource verbatim. The body
	// is the pod object carrying the adversarial ephemeral container.
	bodyPath := filepath.Join(t.TempDir(), "ephemeral-attach.json")
	if err := os.WriteFile(bodyPath, []byte(ephemeralAttachBody(pod.Name)), 0o600); err != nil {
		t.Fatalf("write ephemeral-container attach body: %v", err)
	}

	// A server-side dry-run (dryRun=All) sends the ephemeralcontainers
	// UPDATE through the full admission chain so the cred-guard webhook
	// runs, but the API server discards the write, so the live pod gains
	// no ephemeral container.
	raw := fmt.Sprintf(
		"/api/v1/namespaces/%s/pods/%s/ephemeralcontainers?dryRun=All",
		agentNamespace, pod.Name,
	)
	out, err := c.KubectlOut(
		t, "-n", agentNamespace,
		"replace", "--raw", raw, "-f", bodyPath,
	)
	if err == nil {
		t.Fatalf("§12.9.3 violation: the API server admitted a dry-run ephemeral-container attach to "+
			"the managed agent pod %q whose ephemeral container sets no securityContext; the "+
			"lenny-ephemeral-container-cred-guard did not reject the credential-reaching attach."+
			"\noutput:\n%s", pod.Name, out)
	}
	if !strings.Contains(out, ephemeralCredGuardWebhook) {
		t.Fatalf("the ephemeral-container attach to pod %q was rejected, but not by the %s webhook — "+
			"the rejection came from elsewhere in the admission chain, so the guard itself may not "+
			"cover this attach.\noutput:\n%s", pod.Name, ephemeralCredGuardWebhook, out)
	}
	if !strings.Contains(out, ephemeralCredRejectionCode) {
		t.Errorf("the %s rejection of the ephemeral-container attach lacks the §13.1 reason %q."+
			"\noutput:\n%s", ephemeralCredGuardWebhook, ephemeralCredRejectionCode, out)
	}
	t.Logf("§12.9.3: %s rejected the ephemeral-container attach to managed pod %q (pool %q)",
		ephemeralCredGuardWebhook, pod.Name, pod.Pool)
}

// ephemeralAttachBody renders the JSON pod object posted to the
// pods/ephemeralcontainers subresource. The single ephemeral container
// sets no securityContext, so it declares neither runAsUser,
// runAsGroup, nor supplementalGroups: §13.1 condition (iii) treats this
// as inheriting the pod-level credential-group defaults, the exact
// vector the cred-guard rejects. The body is JSON because
// `kubectl replace --raw` posts the file verbatim to the API server.
func ephemeralAttachBody(podName string) string {
	return fmt.Sprintf(`{
  "apiVersion": "v1",
  "kind": "Pod",
  "metadata": {"name": %q, "namespace": %q},
  "spec": {
    "ephemeralContainers": [
      {
        "name": "t9-cred-snoop",
        "image": "busybox:1.36",
        "command": ["sleep", "300"]
      }
    ]
  }
}
`, podName, agentNamespace)
}
