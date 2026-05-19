// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos tests for pod-disruption resilience of the stateless
// Lenny control-plane components. Each test deletes one Pod of a
// multi-replica control-plane Deployment and asserts the ReplicaSet
// reschedules a replacement to Ready, the Deployment returns to its
// full desired replica count, and the backing Service keeps a non-empty
// endpoint set throughout. The deletes are scoped to control-plane
// Deployments the dev-mode install actually runs.

package tier8_chaos_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// rescheduleBound is the time a control-plane pod-kill test allows for
// the Deployment to return to fully Ready. The stateless components
// have no persistent volumes to detach, so a replacement pod only needs
// an image (already present on the node) plus probe convergence; 2m is
// generous headroom over the observed reschedule time.
const rescheduleBound = 2 * time.Minute

// killOnePodAndAssertRecovery deletes one pod of the named Deployment
// and asserts the Deployment recovers to fully Ready with the Service
// keeping endpoints throughout. It is the body shared by the gateway
// and webhook pod-disruption tests.
func killOnePodAndAssertRecovery(t *testing.T, c *kind.Cluster, deployment, selector, service string) {
	t.Helper()

	// Precondition: the Deployment starts fully Ready, so the kill
	// under test begins from a known-good multi-replica state.
	if !deploymentReady(t, c, deployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready (%s) before the chaos injection",
			deployment, deploymentReadyState(t, c, deployment))
	}
	before := podNames(t, c, selector)
	if len(before) < 2 {
		t.Skipf("precondition not met: pod-disruption recovery needs at least 2 %s replicas, found %d (%v)",
			deployment, len(before), before)
	}

	// The Service must have endpoints before the injection; the test
	// asserts the count stays non-zero afterward.
	if got := endpointCount(t, c, service); got == 0 {
		t.Skipf("precondition not met: Service %s has no endpoints before the chaos injection", service)
	}
	t.Logf("%s: %d replicas Ready, Service %s has %d endpoints; killing one pod",
		deployment, len(before), service, endpointCount(t, c, service))

	// Inject the failure: delete the first pod. --wait=false starts the
	// recovery clock immediately. With minAvailable HA the surviving
	// replica keeps serving while the ReplicaSet reschedules.
	victim := before[0]
	if out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "delete", "pod", victim, "--wait=false",
	); err != nil {
		t.Fatalf("failed to delete pod %q: %v\n%s", victim, err, out)
	}
	t.Logf("deleted pod %q; waiting for %s to return to fully Ready within %s",
		victim, deployment, rescheduleBound)

	// Assert: the Service keeps a non-empty endpoint set during
	// recovery. A surviving replica must continue backing the Service
	// so client traffic is not black-holed while the victim reschedules.
	// Sample a few times across the early recovery window.
	for i := 0; i < 5; i++ {
		if got := endpointCount(t, c, service); got == 0 {
			t.Errorf("Service %s dropped to zero endpoints during %s pod reschedule; "+
				"the surviving replica did not keep the Service backed", service, deployment)
			break
		}
		time.Sleep(2 * time.Second)
	}

	// Assert: the Deployment recovers to fully Ready. The ReplicaSet
	// must reschedule a replacement for the killed pod.
	recovered := pollUntil(rescheduleBound, 2*time.Second, func() bool {
		return deploymentReady(t, c, deployment)
	})
	if !recovered {
		t.Fatalf("the %s Deployment did not return to fully Ready within %s after the pod kill (state %s); "+
			"the killed replica was not rescheduled to Ready",
			deployment, rescheduleBound, deploymentReadyState(t, c, deployment))
	}

	// Assert: the killed pod was actually replaced. A new pod name must
	// appear that was not in the pre-injection set, confirming the
	// recovery is a fresh pod rather than the Deployment having simply
	// been Ready on its survivors.
	after := podNames(t, c, selector)
	replaced := false
	for _, name := range after {
		if !contains(before, name) {
			replaced = true
			break
		}
	}
	if !replaced {
		t.Errorf("%s recovered to Ready but no replacement pod appeared (pods before %v, after %v); "+
			"expected the ReplicaSet to schedule a fresh pod for the killed replica",
			deployment, before, after)
	}

	// Final endpoint check: the Service is fully backed again.
	if got := endpointCount(t, c, service); got == 0 {
		t.Errorf("Service %s has no endpoints after %s recovered to Ready", service, deployment)
	}
	t.Logf("%s recovered: %s replicas Ready, Service %s has %d endpoints, killed pod replaced",
		deployment, deploymentReadyState(t, c, deployment), service, endpointCount(t, c, service))
}

// spec: 17.2
// diagnosis: §17.2 gateway HA does not survive a pod loss. The gateway
// runs as a multi-replica Deployment behind the lenny-gateway Service.
// The test deletes one lenny-gateway pod and asserts the Deployment
// reschedules a replacement to Ready within the reschedule bound and
// the lenny-gateway Service keeps a non-empty endpoint set throughout.
// A failure means the ReplicaSet did not replace the killed pod, the
// replacement never reached Ready, or the Service lost all endpoints
// during the reschedule — any of which breaks the §17.2 stateless-HA
// guarantee for the client-facing gateway.
func TestGatewayReplicaFailure(t *testing.T) {
	c := kind.InstallLenny(t)
	killOnePodAndAssertRecovery(t, c,
		"lenny-gateway", "lenny.dev/component=gateway", "lenny-gateway")
}

// spec: 13.7
// diagnosis: §13.7 / §17.2 admission-webhook HA did not survive a pod
// loss. The lenny-sandboxclaim-guard webhook runs as a 2-replica
// Deployment behind its Service; the test deletes one pod and asserts
// the Deployment reschedules a replacement to Ready and the Service
// keeps a non-empty endpoint set throughout. The webhook is
// failurePolicy: Fail, so an endpoint gap would block every SandboxClaim
// write — a failure means the ReplicaSet did not replace the killed pod
// or the Service lost all endpoints.
func TestAdmissionWebhookOutage(t *testing.T) {
	c := kind.InstallLenny(t)
	killOnePodAndAssertRecovery(t, c,
		"lenny-sandboxclaim-guard", "lenny.dev/webhook=lenny-sandboxclaim-guard", "lenny-sandboxclaim-guard")
}
