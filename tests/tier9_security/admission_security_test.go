// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security tests for the §13.1 pod-security admission boundary.
// The tests install the Lenny control plane on a Kind cluster (via the
// install.sh-backed kind.InstallLenny harness) and drive adversarial
// §13.1-violating pod manifests through the live admission chain.
//
// Where tier-5 covers the basic unhardened-pod rejection and the
// webhook inventory, this file asserts a distinct security property:
// each individual §13.1 escape vector — a privileged container, each of
// the four host-sharing / process-sharing flags, an added Linux
// capability, and a writable root filesystem — is rejected on its own,
// with the §13.1 reason code, by the lenny-pod-security webhook. A
// webhook that catches the composed unhardened pod but admits a pod
// that trips only one of these flags would leave a single-vector
// bypass open.
//
// Every manifest is applied with `kubectl apply --dry-run=server`, so
// the full admission chain runs (the webhook executes) but nothing is
// persisted, which keeps the agent namespaces clean across runs.

package tier9_security_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// agentNamespace is an agent namespace the chart labels with
// lenny.dev/agent-namespace=true, which is the namespaceSelector the
// lenny-pod-security webhook is scoped to. Manifests target it so the
// webhook is in scope.
const agentNamespace = "lenny-agents"

// podSecurityWebhook is the admission webhook name the lenny-pod-security
// ValidatingWebhookConfiguration registers; rejection messages from it
// carry this name.
const podSecurityWebhook = "pod-security.lenny.dev"

// bypassCase is one adversarial §13.1 escape vector: a pod manifest
// that violates exactly one §13.1 control and the reason-code substring
// the lenny-pod-security webhook must surface when it rejects it.
type bypassCase struct {
	name     string
	manifest string
	// wantReason is a substring the rejection body must contain. §13.1
	// host-sharing flags reject with POD_SPEC_HOST_SHARING_FORBIDDEN;
	// the other controls reject with a §13.1-tagged message.
	wantReason string
}

// spec: 13.1
// diagnosis: the §13.1 pod-security webhook admits a single-vector
// escape. The test drives one adversarial pod per §13.1 control —
// privileged, hostNetwork, hostPID, hostIPC, shareProcessNamespace, an
// added capability, and a writable rootfs — through the live admission
// chain and asserts lenny-pod-security rejects each with the §13.1
// reason code. An admitted pod means that vector is unguarded.
func TestAdmissionSecurityBypassAttempts(t *testing.T) {
	c := kind.InstallLenny(t)

	for _, tc := range bypassCases() {
		t.Run(tc.name, func(t *testing.T) {
			out, err := c.DryRunApplyStdin(t, tc.manifest)
			if err == nil {
				t.Fatalf("§13.1 violation: the API server admitted a pod whose only defect is %q; "+
					"the lenny-pod-security webhook did not reject it.\noutput:\n%s", tc.name, out)
			}
			if !strings.Contains(out, podSecurityWebhook) {
				t.Fatalf("the %q pod was rejected, but not by the %s webhook — the rejection came from "+
					"elsewhere in the admission chain, so the webhook itself may not guard this "+
					"vector.\noutput:\n%s", tc.name, podSecurityWebhook, out)
			}
			if !strings.Contains(out, tc.wantReason) {
				t.Errorf("the %s rejection of the %q pod lacks the §13.1 reason %q.\noutput:\n%s",
					podSecurityWebhook, tc.name, tc.wantReason, out)
			}
			t.Logf("%s rejected the %q escape vector: %s",
				podSecurityWebhook, tc.name, rejectionLine(out))
		})
	}
}

// spec: 13.1
// diagnosis: the §13.1 pod-security webhook is over-broad and rejects a
// pod that sets every §13.1 control correctly. The test applies a pod
// with runAsNonRoot, the lenny-cred-readers fsGroup and
// supplementalGroups membership, RuntimeDefault seccomp, a dropped-ALL
// capability set, and a read-only rootfs, and
// expects the webhook to admit it. This is the positive control for
// TestAdmissionSecurityBypassAttempts: a rejection here means the
// bypass-rejection results above could be false positives.
func TestAdmissionSecurityAdmitsHardenedPod(t *testing.T) {
	c := kind.InstallLenny(t)

	out, err := c.DryRunApplyStdin(t, hardenedPodManifest())
	if err != nil {
		t.Fatalf("§13.1 over-broad rejection: the lenny-pod-security webhook rejected a pod that "+
			"satisfies every §13.1 control.\noutput:\n%s", out)
	}
	t.Logf("%s admitted the §13.1-compliant pod: %s", podSecurityWebhook, rejectionLine(out))
}

// bypassCases builds the adversarial pod set. Each pod is otherwise
// §13.1-compliant and trips exactly one control, so a rejection is
// attributable to that one vector. The four host-sharing flags are
// each tested alone: Kubernetes' own API validation rejects certain
// pairs (hostPID with shareProcessNamespace, privileged with
// allowPrivilegeEscalation=false) before the webhook runs, so combining
// them would mask which check fired.
func bypassCases() []bypassCase {
	return []bypassCase{
		{
			name:       "privileged-container",
			wantReason: "privileged",
			// allowPrivilegeEscalation is omitted: the API server rejects
			// privileged=true paired with allowPrivilegeEscalation=false
			// before admission webhooks run, so the request must reach the
			// webhook for the webhook's own privileged check to fire.
			manifest: agentPodManifest("t9-bypass-privileged", podBody{
				containerSecurityContext: "" +
					"        privileged: true\n" +
					"        readOnlyRootFilesystem: true\n" +
					"        capabilities:\n" +
					"          drop: [\"ALL\"]\n",
			}),
		},
		{
			name:       "host-network",
			wantReason: "POD_SPEC_HOST_SHARING_FORBIDDEN",
			manifest: agentPodManifest("t9-bypass-hostnetwork", podBody{
				podLevelField:            "  hostNetwork: true\n",
				containerSecurityContext: hardenedContainerSC,
			}),
		},
		{
			name:       "host-pid",
			wantReason: "POD_SPEC_HOST_SHARING_FORBIDDEN",
			manifest: agentPodManifest("t9-bypass-hostpid", podBody{
				podLevelField:            "  hostPID: true\n",
				containerSecurityContext: hardenedContainerSC,
			}),
		},
		{
			name:       "host-ipc",
			wantReason: "POD_SPEC_HOST_SHARING_FORBIDDEN",
			manifest: agentPodManifest("t9-bypass-hostipc", podBody{
				podLevelField:            "  hostIPC: true\n",
				containerSecurityContext: hardenedContainerSC,
			}),
		},
		{
			name:       "share-process-namespace",
			wantReason: "POD_SPEC_HOST_SHARING_FORBIDDEN",
			manifest: agentPodManifest("t9-bypass-shareprocns", podBody{
				podLevelField:            "  shareProcessNamespace: true\n",
				containerSecurityContext: hardenedContainerSC,
			}),
		},
		{
			name:       "added-capability",
			wantReason: "capabilities.add",
			manifest: agentPodManifest("t9-bypass-addcap", podBody{
				containerSecurityContext: "" +
					"        allowPrivilegeEscalation: false\n" +
					"        readOnlyRootFilesystem: true\n" +
					"        capabilities:\n" +
					"          drop: [\"ALL\"]\n" +
					"          add: [\"NET_ADMIN\"]\n",
			}),
		},
		{
			name:       "writable-root-filesystem",
			wantReason: "readOnlyRootFilesystem",
			manifest: agentPodManifest("t9-bypass-rwroot", podBody{
				// readOnlyRootFilesystem is omitted, leaving the container
				// rootfs writable.
				containerSecurityContext: "" +
					"        allowPrivilegeEscalation: false\n" +
					"        capabilities:\n" +
					"          drop: [\"ALL\"]\n",
			}),
		},
	}
}

// hardenedContainerSC is the §13.1-compliant container securityContext
// block, used by bypass cases whose defect is a pod-level field so the
// container itself stays compliant and the rejection is attributable to
// the pod-level flag alone.
const hardenedContainerSC = "" +
	"        allowPrivilegeEscalation: false\n" +
	"        readOnlyRootFilesystem: true\n" +
	"        capabilities:\n" +
	"          drop: [\"ALL\"]\n"

// podBody carries the two variable fragments of an agent-pod manifest:
// an optional pod-level field line (e.g. hostPID) and the container
// securityContext block.
type podBody struct {
	podLevelField            string
	containerSecurityContext string
}

// agentPodManifest renders an agent-namespace pod manifest. The pod
// always carries the §13.1-compliant pod-level securityContext
// (runAsNonRoot, the lenny-cred-readers fsGroup and supplementalGroups
// membership, RuntimeDefault seccomp); body supplies the adversarial
// pod-level field and the container securityContext.
func agentPodManifest(name string, body podBody) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
spec:
%s  securityContext:
    runAsNonRoot: true
    fsGroup: 65534
    supplementalGroups: [65534]
    seccompProfile:
      type: RuntimeDefault
  containers:
    - name: agent
      image: busybox:1.36
      securityContext:
%s`, name, agentNamespace, body.podLevelField, body.containerSecurityContext)
}

// hardenedPodManifest renders a pod that satisfies every §13.1 control:
// the positive control for the bypass suite.
func hardenedPodManifest() string {
	return agentPodManifest("t9-hardened-pod", podBody{
		containerSecurityContext: hardenedContainerSC,
	})
}

// rejectionLine returns a compact one-line summary of a multi-line
// kubectl rejection for logging. It prefers the line that names the
// pod-security webhook (the substantive denial), falling back to the
// first non-empty line when no such line is present.
func rejectionLine(out string) string {
	var first string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if first == "" {
			first = line
		}
		if strings.Contains(line, podSecurityWebhook) {
			return line
		}
	}
	return first
}
