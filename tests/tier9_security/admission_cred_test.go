// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security tests for the §12.9.3 lenny-pod-security credential
// controls. The e2e Kind cluster runs the live pod-security webhook
// over Pod CREATE in the lenny-agents namespace, so the §13.1 fsGroup
// invariant and the §13.1 lenny-cred-readers membership boundary are
// exercised as adversarial admission cases against the running cluster.
//
// §13.1 requires every agent pod to carry a pod-level
// securityContext.fsGroup equal to the lenny-cred-readers GID: that
// supplementary group is what makes the §4.7 credential file
// group-readable by the agent across the adapter/agent UID boundary. A
// pod whose pod-level securityContext omits fsGroup entirely breaches
// the control and must be rejected with POD_SPEC_CRED_FSGROUP_MISSING.
//
// The companion §13.1 single-vector bypass suite is in
// admission_security_test.go; that file's agentPodManifest hard-codes a
// pod-level fsGroup, so the fsGroup-missing vector needs the distinct
// manifest builder this file provides. Every manifest is applied with
// kubectl apply --dry-run=server, so the admission chain runs but
// nothing is persisted.

package tier9_security_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// credFsGroupMissingReason is the §13.1 reason code the lenny-pod-security
// webhook stamps on a pod whose pod-level securityContext omits the
// credential fsGroup.
const credFsGroupMissingReason = "POD_SPEC_CRED_FSGROUP_MISSING"

// spec: 12.9.3
// diagnosis: the §12.9.3 lenny-pod-security webhook admits an agent pod
// whose pod-level securityContext omits fsGroup. The test drives a pod
// that is §13.1-compliant in every other respect but carries no
// pod-level fsGroup through the live admission chain and asserts
// lenny-pod-security rejects it with POD_SPEC_CRED_FSGROUP_MISSING. An
// admitted pod means the §4.7 credential file would not be group-owned
// by the lenny-cred-readers GID, so the cross-UID credential-delivery
// path is unguarded.
func TestAdmissionPolicyFsGroupMissing(t *testing.T) {
	c := kind.InstallLenny(t)

	out, err := c.DryRunApplyStdin(t, fsGroupMissingPodManifest())
	if err == nil {
		t.Fatalf("§12.9.3 violation: the API server admitted an agent pod whose pod-level "+
			"securityContext omits fsGroup; the lenny-pod-security webhook did not enforce the §13.1 "+
			"credential-fsGroup control.\noutput:\n%s", out)
	}
	if !strings.Contains(out, podSecurityWebhook) {
		t.Fatalf("the fsGroup-missing pod was rejected, but not by the %s webhook — the rejection "+
			"came from elsewhere in the admission chain, so the webhook itself may not guard the "+
			"fsGroup control.\noutput:\n%s", podSecurityWebhook, out)
	}
	if !strings.Contains(out, credFsGroupMissingReason) {
		t.Errorf("the %s rejection of the fsGroup-missing pod lacks the §13.1 reason %q.\noutput:\n%s",
			podSecurityWebhook, credFsGroupMissingReason, out)
	}
	t.Logf("§12.9.3: %s rejected the fsGroup-missing agent pod: %s",
		podSecurityWebhook, rejectionLine(out))
}

// fsGroupMissingPodManifest renders an agent-namespace pod that is
// §13.1-compliant in every respect except the pod-level fsGroup, which
// it omits. Every other §13.1 control is satisfied so the rejection is
// attributable to the missing fsGroup alone: runAsNonRoot and the
// RuntimeDefault seccomp profile are set at the pod level, and the
// container drops all capabilities, sets allowPrivilegeEscalation false,
// and runs a read-only root filesystem.
func fsGroupMissingPodManifest() string {
	return fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: t9-cred-fsgroup-missing
  namespace: %s
spec:
  securityContext:
    runAsNonRoot: true
    seccompProfile:
      type: RuntimeDefault
  containers:
    - name: agent
      image: busybox:1.36
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        capabilities:
          drop: ["ALL"]
`, agentNamespace)
}

// credGroupOverbroadReason is the §13.1 reason code the lenny-pod-security
// webhook stamps on a pod whose non-adapter, non-agent container
// declares the lenny-cred-readers GID.
const credGroupOverbroadReason = "POD_SPEC_CRED_GROUP_OVERBROAD"

// credReadersGID is the lenny-cred-readers supplementary GID the §13.1
// cross-UID credential-delivery path uses. The Sandbox podspec builder
// pins it; the webhook validates a non-credential container's
// runAsGroup against it.
const credReadersGID = 65534

// spec: 12.9.3
// diagnosis: the §12.9.3 lenny-pod-security webhook admits an agent pod
// whose non-adapter, non-agent container declares the lenny-cred-readers
// GID in runAsGroup. §13.1 confines that group membership to the
// adapter and agent containers. The test drives a pod with a compliant
// adapter and runtime container plus a third container carrying the GID
// and asserts lenny-pod-security rejects it with
// POD_SPEC_CRED_GROUP_OVERBROAD. An admitted pod means an injected
// sidecar could read the §4.7 credential file.
func TestAdmissionPolicyCredGroupOverbroad(t *testing.T) {
	c := kind.InstallLenny(t)

	out, err := c.DryRunApplyStdin(t, credGroupOverbroadPodManifest())
	if err == nil {
		t.Fatalf("§12.9.3 violation: the API server admitted an agent pod whose non-credential "+
			"container declares the lenny-cred-readers GID; the lenny-pod-security webhook did not "+
			"enforce the §13.1 cred-group boundary.\noutput:\n%s", out)
	}
	if !strings.Contains(out, podSecurityWebhook) {
		t.Fatalf("the cred-group-overbroad pod was rejected, but not by the %s webhook — the "+
			"rejection came from elsewhere in the admission chain.\noutput:\n%s", podSecurityWebhook, out)
	}
	if !strings.Contains(out, credGroupOverbroadReason) {
		t.Errorf("the %s rejection lacks the §13.1 reason %q.\noutput:\n%s",
			podSecurityWebhook, credGroupOverbroadReason, out)
	}
	t.Logf("§12.9.3: %s rejected the cred-group-overbroad agent pod: %s",
		podSecurityWebhook, rejectionLine(out))
}

// credGroupOverbroadPodManifest renders an agent-namespace pod that is
// §13.1-compliant in every respect except that a third container,
// neither the adapter nor the agent, declares the lenny-cred-readers
// GID in runAsGroup. The adapter and runtime containers carry the §4.7
// names the webhook reads as the credential containers, so the
// rejection is attributable to the third container alone.
func credGroupOverbroadPodManifest() string {
	return fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: t9-cred-group-overbroad
  namespace: %s
spec:
  securityContext:
    runAsNonRoot: true
    fsGroup: %d
    seccompProfile:
      type: RuntimeDefault
  containers:
    - name: adapter
      image: busybox:1.36
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        capabilities:
          drop: ["ALL"]
    - name: runtime
      image: busybox:1.36
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        capabilities:
          drop: ["ALL"]
    - name: sidecar
      image: busybox:1.36
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        runAsGroup: %d
        capabilities:
          drop: ["ALL"]
`, agentNamespace, credReadersGID, credReadersGID)
}
