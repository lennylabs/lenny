// SPDX-License-Identifier: MIT

//go:build security

// Cluster-free checks of the tier-9 pod-security manifests. Each manifest
// the live admission suite applies is also run through the in-process
// lenny-pod-security decider, so a fixture that would be rejected for a
// reason other than the one its case names, or a positive control that
// the current validator would reject, fails here without a Kind cluster.

package tier9_security_test

import (
	"context"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"

	"github.com/lennylabs/lenny/pkg/admission/webhook"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/pkg/podsecurity"
)

// decideManifestOffline runs one pod manifest through the in-process
// lenny-pod-security decider configured with the chart-default UIDs and
// GID, and returns whether it is admitted and the denial message.
func decideManifestOffline(t *testing.T, manifest string) (bool, string) {
	t.Helper()
	raw, err := yaml.YAMLToJSON([]byte(manifest))
	if err != nil {
		t.Fatalf("convert manifest to JSON: %v\n%s", err, manifest)
	}
	decide := webhook.PodSecurity(podspec.AdapterUID, podspec.AgentUID, podspec.CredReadersGID,
		podspec.CredVolumeName, podsecurity.RuntimeClassPolicy{})
	resp := decide(context.Background(), &admissionv1.AdmissionRequest{
		Operation: admissionv1.Create,
		Object:    runtime.RawExtension{Raw: raw},
	})
	if resp.Allowed {
		return true, ""
	}
	if resp.Result == nil {
		return false, ""
	}
	return false, resp.Result.Message
}

// spec: 13.1 (Pod Security)
// diagnosis: a tier-9 bypass manifest is rejected by the pod-security
// validator for a reason other than the one its case names, or is
// admitted, so the live suite would pass or fail for the wrong reason.
// For the container identity cases this means the fixture does not
// isolate the reserved-UID or missing-runAsGroup vector. The test needs
// no cluster.
func TestAdmissionSecurityBypassManifestsMatchValidator(t *testing.T) {
	for _, tc := range bypassCases() {
		t.Run(tc.name, func(t *testing.T) {
			allowed, msg := decideManifestOffline(t, tc.manifest)
			if allowed {
				t.Fatalf("the in-process pod-security decider admitted the %q bypass manifest", tc.name)
			}
			if !strings.Contains(msg, tc.wantReason) {
				t.Errorf("the denial of the %q bypass manifest lacks %q: %s", tc.name, tc.wantReason, msg)
			}
		})
	}
}

// spec: 13.1 (Pod Security)
// diagnosis: a tier-9 positive-control manifest is rejected by the
// current pod-security validator, so the live positive controls would
// fail on the fixture rather than on the webhook. The test needs no
// cluster.
func TestAdmissionSecurityPositiveControlManifestsAdmitted(t *testing.T) {
	for name, manifest := range map[string]string{
		"hardened pod":        hardenedPodManifest(),
		"reserved-UID owners": sidecarModelPodManifest("t9-reserved-owners", "", injectedSidecarUID),
	} {
		t.Run(name, func(t *testing.T) {
			if allowed, msg := decideManifestOffline(t, manifest); !allowed {
				t.Fatalf("the in-process pod-security decider rejected the %s manifest: %s", name, msg)
			}
		})
	}
}

// spec: 13.1 (Pod Security)
// diagnosis: the credential-control manifests of the tier-9 admission
// suite trip the container identity clause or another control in place
// of the one each case names. The test needs no cluster.
func TestAdmissionCredManifestsIsolateTheirControl(t *testing.T) {
	for name, tc := range map[string]struct {
		manifest string
		reason   string
	}{
		"fsGroup missing": {fsGroupMissingPodManifest(), credFsGroupMissingReason},
		"group overbroad": {credGroupOverbroadPodManifest(), credGroupOverbroadReason},
	} {
		t.Run(name, func(t *testing.T) {
			allowed, msg := decideManifestOffline(t, tc.manifest)
			if allowed {
				t.Fatalf("the in-process pod-security decider admitted the %s manifest", name)
			}
			if !strings.Contains(msg, tc.reason) {
				t.Errorf("the denial of the %s manifest lacks %q: %s", name, tc.reason, msg)
			}
			if strings.Contains(msg, "Container identity") {
				t.Errorf("the %s manifest trips the container identity clause: %s", name, msg)
			}
		})
	}
}
