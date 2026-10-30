// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security tests for the §13.1 pod-security admission boundary.
// The tests install the Lenny control plane on a Kind cluster (via the
// install.sh-backed kind.InstallLenny harness) and drive adversarial
// §13.1-violating pod manifests through the live admission chain.
//
// Where tier-5 covers the basic unhardened-pod rejection and the
// webhook inventory, this file asserts a distinct security property:
// each individual §13.1 escape vector (a privileged container, each
// host-sharing or process-sharing flag, an added Linux capability, a
// writable root filesystem, a container that omits its container-level
// identity, and a container that is not the owner but runs at the
// reserved adapter or agent UID) is rejected on its own, with the §13.1
// reason, by the lenny-pod-security webhook. A webhook that catches the
// composed unhardened pod but admits a pod that trips only one of these
// controls would leave a single-vector bypass open.
//
// The reserved-UID cases are what the §4.7.11 SO_PEERCRED check relies
// on: the adapter identifies the agent by UID, so a second container at
// the agent UID would pass that check as the agent.
//
// Every manifest is applied with `kubectl apply --dry-run=server`, so
// the full admission chain runs (the webhook executes) but nothing is
// persisted, which keeps the agent namespaces clean across runs.

package tier9_security_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
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
	// the other controls, including the container identity clause,
	// reject with a §13.1-tagged message and no code.
	wantReason string
}

// spec: 13.1 (Pod Security)
// diagnosis: the §13.1 pod-security webhook admits a single-vector
// escape. The test drives one adversarial pod per §13.1 control
// (privileged, hostNetwork, hostPID, hostIPC, shareProcessNamespace, an
// added capability, a writable rootfs, a container without runAsGroup,
// an injected container at the agent or adapter UID, and an init
// container at the adapter UID) through the live admission chain and
// asserts lenny-pod-security rejects each with the §13.1 reason. An
// admitted pod means that vector is unguarded; for the reserved-UID
// cases it means a non-owner container can pass the adapter's
// SO_PEERCRED UID check, or a webhook image older than the container
// identity clause is loaded.
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

// spec: 13.1 (Pod Security)
// diagnosis: the §13.1 pod-security webhook is over-broad and rejects a
// pod that sets every §13.1 control correctly. The test applies a pod
// with runAsNonRoot, the lenny-cred-readers fsGroup and
// supplementalGroups membership, RuntimeDefault seccomp, a dropped-ALL
// capability set, a read-only rootfs, and an explicit non-reserved
// container-level runAsUser and runAsGroup, and expects the webhook to
// admit it. This is the positive control for
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

// spec: 13.1 (Pod Security)
// diagnosis: the §13.1 container identity clause is over-broad and
// rejects the owners of the reserved UIDs. The test applies a
// sidecar-model pod whose adapter container runs at the adapter UID and
// whose runtime container runs at the agent UID, beside an injected
// sidecar at a non-reserved identity, and expects the webhook to admit
// it. This is the positive control for the reserved-UID bypass cases: a
// rejection here means those rejections could be false positives, and
// every warm pod the controller builds would fail admission.
func TestAdmissionSecurityAdmitsReservedUIDOwners(t *testing.T) {
	c := kind.InstallLenny(t)

	out, err := c.DryRunApplyStdin(t, sidecarModelPodManifest("t9-reserved-owners", "", injectedSidecarUID))
	if err != nil {
		t.Fatalf("§13.1 over-broad rejection: the lenny-pod-security webhook rejected a pod whose adapter "+
			"and runtime containers run at their own reserved UIDs.\noutput:\n%s", out)
	}
	t.Logf("%s admitted the reserved-UID owners: %s", podSecurityWebhook, rejectionLine(out))
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
					testContainerIdentity +
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
					testContainerIdentity +
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
					testContainerIdentity +
					"        capabilities:\n" +
					"          drop: [\"ALL\"]\n",
			}),
		},
		{
			name:       "container-omits-runAsGroup",
			wantReason: "must set non-zero runAsUser and runAsGroup",
			// runAsUser is set and runAsGroup is omitted, so the image
			// would choose the primary GID.
			manifest: agentPodManifest("t9-bypass-norunasgroup", podBody{
				containerSecurityContext: "" +
					"        allowPrivilegeEscalation: false\n" +
					"        readOnlyRootFilesystem: true\n" +
					fmt.Sprintf("        runAsUser: %d\n", injectedSidecarUID) +
					"        capabilities:\n" +
					"          drop: [\"ALL\"]\n",
			}),
		},
		{
			name:       "injected-regular-container-at-agent-uid",
			wantReason: reservedUIDReason(podspec.AgentUID),
			manifest:   sidecarModelPodManifest("t9-bypass-injected-agentuid", "", podspec.AgentUID),
		},
		{
			name:       "injected-regular-container-at-adapter-uid",
			wantReason: reservedUIDReason(podspec.AdapterUID),
			manifest:   sidecarModelPodManifest("t9-bypass-injected-adapteruid", "", podspec.AdapterUID),
		},
		{
			name:       "init-container-at-adapter-uid",
			wantReason: reservedUIDReason(podspec.AdapterUID),
			manifest: sidecarModelPodManifest("t9-bypass-init-adapteruid",
				"  initContainers:\n"+containerYAML("init-injected", podspec.AdapterUID),
				injectedSidecarUID),
		},
	}
}

// injectedSidecarUID is the UID and primary GID the test pods give a
// container that is not a platform container. It lies outside the
// default adapter and agent UIDs, the default lenny-cred-readers GID,
// and 0, so §13.1 (Container identity) admits it.
const injectedSidecarUID int64 = 1000

// testContainerIdentity is the container-level identity block every
// otherwise-compliant test container carries, so a rejection is
// attributable to the one control the case violates.
var testContainerIdentity = fmt.Sprintf(""+
	"        runAsUser: %d\n"+
	"        runAsGroup: %d\n", injectedSidecarUID, injectedSidecarUID)

// reservedUIDReason is the §13.1 (Container identity) rejection
// substring for a container that runs at the reserved uid without owning
// it. The substring stops before the quoted owner name so it does not
// depend on how kubectl escapes the quotes in the webhook message.
func reservedUIDReason(uid int64) string {
	return fmt.Sprintf("runAsUser %d is reserved for the", uid)
}

// containerYAML renders one §13.1-hardened list entry for a container
// running at uid, with runAsGroup equal to uid as the pod builder sets
// it. The entry is indented for the containers or initContainers list of
// a pod spec.
func containerYAML(name string, uid int64) string {
	return fmt.Sprintf(`    - name: %s
      image: busybox:1.36
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        runAsUser: %d
        runAsGroup: %d
        capabilities:
          drop: ["ALL"]
`, name, uid, uid)
}

// sidecarModelPodManifest renders a sidecar-model agent pod: an adapter
// container at the adapter UID, a runtime container at the agent UID,
// and an injected regular container at injectedUID, standing in for a
// container a deployer's mutating webhook adds. initContainers is an
// optional complete initContainers block. The adapter and runtime own
// their reserved UIDs, so the pod is rejected only when injectedUID or
// an init container claims one of them.
func sidecarModelPodManifest(name, initContainers string, injectedUID int64) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
spec:
  securityContext:
    runAsNonRoot: true
    fsGroup: %d
    supplementalGroups: [%d]
    seccompProfile:
      type: RuntimeDefault
%s  containers:
%s%s%s`, name, agentNamespace, podspec.CredReadersGID, podspec.CredReadersGID, initContainers,
		containerYAML("adapter", podspec.AdapterUID),
		containerYAML("runtime", podspec.AgentUID),
		containerYAML("injected", injectedUID))
}

// hardenedContainerSC is the §13.1-compliant container securityContext
// block, including the explicit container-level identity §13.1
// (Container identity) requires, used by bypass cases whose defect is a
// pod-level field so the container itself stays compliant and the
// rejection is attributable to the pod-level flag alone.
var hardenedContainerSC = "" +
	"        allowPrivilegeEscalation: false\n" +
	"        readOnlyRootFilesystem: true\n" +
	testContainerIdentity +
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
