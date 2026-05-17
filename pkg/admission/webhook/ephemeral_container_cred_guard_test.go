// SPDX-License-Identifier: MIT

package webhook_test

import (
	"context"
	"encoding/json"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/lennylabs/lenny/pkg/admission/webhook"
)

const (
	tAdapterUID int64 = 65532
	tAgentUID   int64 = 65533
	tCredGID    int64 = 65534
	tCredVol          = "credentials"
)

func i64(v int64) *int64 { return &v }

// ephemeralWithSecCtx builds an ephemeral container with an explicit
// securityContext.
func ephemeralWithSecCtx(name string, runAsUser, runAsGroup *int64, mounts ...corev1.VolumeMount) corev1.EphemeralContainer {
	return corev1.EphemeralContainer{
		EphemeralContainerCommon: corev1.EphemeralContainerCommon{
			Name: name,
			SecurityContext: &corev1.SecurityContext{
				RunAsUser:  runAsUser,
				RunAsGroup: runAsGroup,
			},
			VolumeMounts: mounts,
		},
	}
}

func ecPod(t *testing.T, ecs ...corev1.EphemeralContainer) runtime.RawExtension {
	t.Helper()
	raw, err := json.Marshal(corev1.Pod{Spec: corev1.PodSpec{EphemeralContainers: ecs}})
	if err != nil {
		t.Fatalf("marshal pod: %v", err)
	}
	return runtime.RawExtension{Raw: raw}
}

func credGuardDecide(old, updated runtime.RawExtension) *admissionv1.AdmissionResponse {
	decide := webhook.EphemeralContainerCredGuard(tAdapterUID, tAgentUID, tCredGID, tCredVol)
	return decide(context.Background(), &admissionv1.AdmissionRequest{
		Operation: admissionv1.Update,
		Object:    updated,
		OldObject: old,
	})
}

func TestCredGuardAllowsASafeNewEphemeralContainer(t *testing.T) {
	old := ecPod(t)
	updated := ecPod(t, ephemeralWithSecCtx("debugger", i64(99999), i64(99999)))

	resp := credGuardDecide(old, updated)
	if !resp.Allowed {
		t.Errorf("a safe ephemeral container was rejected: %v", resp.Result)
	}
}

func TestCredGuardRejectsAgentUID(t *testing.T) {
	old := ecPod(t)
	updated := ecPod(t, ephemeralWithSecCtx("attacker", i64(tAgentUID), i64(99999)))

	resp := credGuardDecide(old, updated)
	if resp.Allowed {
		t.Error("an ephemeral container running as the agent UID was allowed")
	}
	if resp.Result == nil || resp.Result.Code != 403 {
		t.Errorf("rejection result = %v, want code 403", resp.Result)
	}
}

func TestCredGuardRejectsAbsentSecurityContext(t *testing.T) {
	old := ecPod(t)
	// An ephemeral container with no securityContext: runAsUser and
	// runAsGroup are absent and inherit pod-level defaults.
	updated := ecPod(t, corev1.EphemeralContainer{
		EphemeralContainerCommon: corev1.EphemeralContainerCommon{Name: "bare"},
	})

	if resp := credGuardDecide(old, updated); resp.Allowed {
		t.Error("an ephemeral container with no securityContext was allowed")
	}
}

func TestCredGuardRejectsCredVolumeMount(t *testing.T) {
	old := ecPod(t)
	updated := ecPod(t, ephemeralWithSecCtx("attacker", i64(99999), i64(99999),
		corev1.VolumeMount{Name: tCredVol, MountPath: "/data"}))

	if resp := credGuardDecide(old, updated); resp.Allowed {
		t.Error("an ephemeral container mounting the credential volume was allowed")
	}
}

func TestCredGuardRejectsCredPathMount(t *testing.T) {
	old := ecPod(t)
	updated := ecPod(t, ephemeralWithSecCtx("attacker", i64(99999), i64(99999),
		corev1.VolumeMount{Name: "scratch", MountPath: "/run/lenny/x"}))

	if resp := credGuardDecide(old, updated); resp.Allowed {
		t.Error("an ephemeral container mounting under /run/lenny was allowed")
	}
}

func TestCredGuardSkipsPreexistingEphemeralContainers(t *testing.T) {
	// A credential-reading ephemeral container that is already on the
	// pod was admitted earlier; an update that does not add a new one
	// must not be re-judged against it.
	bad := ephemeralWithSecCtx("legacy-debug", i64(tAgentUID), i64(99999))
	old := ecPod(t, bad)
	updated := ecPod(t, bad)

	if resp := credGuardDecide(old, updated); !resp.Allowed {
		t.Error("a pre-existing ephemeral container was re-judged and rejected")
	}
}

func TestCredGuardEvaluatesTheNewlyAddedContainer(t *testing.T) {
	// A pre-existing bad container plus a newly-added bad container:
	// the new one must be evaluated and reject the request.
	old := ecPod(t, ephemeralWithSecCtx("legacy-debug", i64(tAgentUID), i64(99999)))
	updated := ecPod(
		t,
		ephemeralWithSecCtx("legacy-debug", i64(tAgentUID), i64(99999)),
		ephemeralWithSecCtx("new-attacker", i64(tAdapterUID), i64(99999)),
	)

	if resp := credGuardDecide(old, updated); resp.Allowed {
		t.Error("a newly-added credential-reading ephemeral container was allowed")
	}
}

func TestCredGuardRejectsMalformedObject(t *testing.T) {
	decide := webhook.EphemeralContainerCredGuard(tAdapterUID, tAgentUID, tCredGID, tCredVol)
	resp := decide(context.Background(), &admissionv1.AdmissionRequest{
		Operation: admissionv1.Update,
		Object:    runtime.RawExtension{Raw: []byte("{not json")},
	})
	if resp.Allowed {
		t.Error("a malformed Pod object was allowed")
	}
	if resp.Result == nil || resp.Result.Code != 400 {
		t.Errorf("rejection result = %v, want code 400", resp.Result)
	}
}
