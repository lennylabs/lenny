// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the TESTING.md §12.9.3 lenny-ephemeral-container-cred-guard
// webhook. The e2e Kind cluster runs a real agent-pod workload in the
// lenny-agents namespace, so the guard can be exercised against a live
// agent pod by attempting an ephemeral-container attach through the
// admission chain.
//
// The guard is installed fail-closed on the pods/ephemeralcontainers
// UPDATE subresource in agent namespaces. An actor with update on that
// subresource could otherwise attach a debug container that inherits
// the pod-level fsGroup and reads a session's credential file at the
// pod's /run/lenny/slots/{sessionId}/credentials.json. §13.1
// condition (iii) rejects any ephemeral container that omits runAsUser,
// runAsGroup, or supplementalGroups, because an absent value inherits
// the pod credential-group defaults.
//
// The lenny-pod-security webhook also validates the
// pods/ephemeralcontainers UPDATE, and its §13.1 container identity
// clause rejects any container that omits runAsGroup. Both webhooks
// therefore deny the attach, and the API server reports the denial of
// whichever webhook answered first. The test accepts either denial: the
// cred-guard's EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN or the
// pod-security identity message. The guard's own decision on this body
// is pinned by the tier-1 tests of the ephemeral-container cred-guard
// package, which call the guard without the pod-security webhook in
// front of it.
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

// ephemeralCredRejectionCode is the §17.2 rejection label the guard carries in every denial message.
const ephemeralCredRejectionCode = "EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN"

// containerIdentityMissingReason is the substring of the
// lenny-pod-security rejection for a container that does not set a
// nonzero container-level runAsUser and runAsGroup (§13.1 Container
// identity).
const containerIdentityMissingReason = "must set non-zero runAsUser and runAsGroup"

// ephemeralAttachDenial names one webhook that may deny the attach and
// the reason its denial must carry.
type ephemeralAttachDenial struct {
	webhook string
	reason  string
}

// ephemeralAttachDenials are the two acceptable denials of the attach.
// The API server reports one webhook's denial, so the test accepts the
// first that names its webhook.
var ephemeralAttachDenials = []ephemeralAttachDenial{
	{webhook: ephemeralCredGuardWebhook, reason: ephemeralCredRejectionCode},
	{webhook: podSecurityWebhook, reason: containerIdentityMissingReason},
}

// spec: 12.9.3, 13.1 (Pod Security)
// diagnosis: the admission chain admits an ephemeral container that
// could reach the pod credential file. The test takes a live managed
// agent pod and issues a server-side dry-run that attaches an ephemeral
// container which omits runAsGroup, and which under §13.1 condition
// (iii) inherits the pod-level credential-group defaults. Either the
// TESTING.md §12.9.3 lenny-ephemeral-container-cred-guard must reject it
// with EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN, or lenny-pod-security
// must reject it under the §13.1 container identity clause. An admitted
// attach means an actor with pods/ephemeralcontainers UPDATE could read
// a session's credential file under /run/lenny/slots/{sessionId}/; a
// denial from any other source means neither webhook is shown to cover
// the attach.
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
		t.Fatalf("TESTING.md §12.9.3 violation: the API server admitted a dry-run ephemeral-container attach to "+
			"the managed agent pod %q whose ephemeral container omits runAsGroup; neither the "+
			"lenny-ephemeral-container-cred-guard nor lenny-pod-security rejected the credential-reaching attach."+
			"\noutput:\n%s", pod.Name, out)
	}
	denial, ok := matchEphemeralAttachDenial(out)
	if !ok {
		t.Fatalf("the ephemeral-container attach to pod %q was rejected, but not by %s or %s; the "+
			"rejection came from elsewhere in the admission chain, so neither webhook is shown to "+
			"cover this attach.\noutput:\n%s", pod.Name, ephemeralCredGuardWebhook, podSecurityWebhook, out)
	}
	if !strings.Contains(out, denial.reason) {
		t.Errorf("the %s rejection of the ephemeral-container attach lacks the §13.1 reason %q."+
			"\noutput:\n%s", denial.webhook, denial.reason, out)
	}
	t.Logf("%s rejected the ephemeral-container attach to managed pod %q (pool %q)",
		denial.webhook, pod.Name, pod.Pool)
}

// matchEphemeralAttachDenial returns the acceptable denial whose webhook
// the rejection output names, or false when it names neither.
func matchEphemeralAttachDenial(out string) (ephemeralAttachDenial, bool) {
	for _, d := range ephemeralAttachDenials {
		if strings.Contains(out, d.webhook) {
			return d, true
		}
	}
	return ephemeralAttachDenial{}, false
}

// ephemeralAttachBody renders the JSON pod object posted to the
// pods/ephemeralcontainers subresource. The single ephemeral container
// declares runAsUser and every field the pod-security baseline requires
// (runAsNonRoot, no privilege escalation, all capabilities dropped, a
// read-only root filesystem, and the default seccomp profile) but omits
// runAsGroup, so §13.1 condition (iii) treats it as inheriting the
// pod-level credential-group default, the exact vector the cred-guard
// rejects.
//
// The baseline fields keep the attach clear of every pod-security
// clause except the §13.1 container identity clause, which also rejects
// a container without runAsGroup, so either webhook's denial names the
// same missing field. Do not add runAsGroup: with it both webhooks admit
// the attach and the test exercises nothing. The body is JSON because
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
        "command": ["sleep", "300"],
        "securityContext": {
          "runAsUser": 31337,
          "runAsNonRoot": true,
          "allowPrivilegeEscalation": false,
          "readOnlyRootFilesystem": true,
          "capabilities": {"drop": ["ALL"]},
          "seccompProfile": {"type": "RuntimeDefault"}
        }
      }
    ]
  }
}
`, podName, agentNamespace)
}

// spec: 13.1 (Pod Security)
// diagnosis: the ephemeral-attach denial matcher attributes a rejection
// to the wrong webhook or accepts a rejection from outside the two
// webhooks that guard the attach. The test needs no cluster.
func TestMatchEphemeralAttachDenial(t *testing.T) {
	cases := []struct {
		name        string
		out         string
		wantWebhook string
		wantOK      bool
	}{
		{
			name:        "cred-guard denial",
			out:         `admission webhook "ephemeral-container-cred-guard.lenny.dev" denied the request: EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN`,
			wantWebhook: ephemeralCredGuardWebhook, wantOK: true,
		},
		{
			name:        "pod-security identity denial",
			out:         `admission webhook "pod-security.lenny.dev" denied the request: container "t9-cred-snoop" must set non-zero runAsUser and runAsGroup (§13.1 Container identity)`,
			wantWebhook: podSecurityWebhook, wantOK: true,
		},
		{
			name: "denial from another webhook",
			out:  `admission webhook "label-immutability.lenny.dev" denied the request`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, ok := matchEphemeralAttachDenial(tc.out)
			if ok != tc.wantOK || d.webhook != tc.wantWebhook {
				t.Fatalf("matchEphemeralAttachDenial = (%q, %v), want (%q, %v)", d.webhook, ok, tc.wantWebhook, tc.wantOK)
			}
			if ok && !strings.Contains(tc.out, d.reason) {
				t.Errorf("the %s denial text lacks its expected reason %q", d.webhook, d.reason)
			}
		})
	}
}
