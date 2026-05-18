// SPDX-License-Identifier: MIT

package webhook_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	t4 "github.com/lennylabs/lenny/pkg/admission/t4_node_isolation"
	"github.com/lennylabs/lenny/pkg/admission/webhook"
)

// spec: §6.4 (STR-003) / §12.9 — the lenny-t4-node-isolation webhook
// transport decodes a Pod, detects the §12.9 T4 tier via the
// lenny.dev/workspace-tier label, and enforces the §6.4 dedicated-node
// predicate.

// t4PodRaw marshals a corev1.Pod into an admission object payload.
func t4PodRaw(t *testing.T, pod corev1.Pod) runtime.RawExtension {
	t.Helper()
	raw, err := json.Marshal(pod)
	if err != nil {
		t.Fatalf("marshal pod: %v", err)
	}
	return runtime.RawExtension{Raw: raw}
}

// t4NodeSelector is the §6.4 deployer-provisioned T4 node label.
func t4NodeSelector() map[string]string {
	return map[string]string{t4.NodeLabelKey: t4.NodeLabelValue}
}

// t4PodToleration is an Equal toleration matching the §6.4 T4 taint.
func t4PodToleration() corev1.Toleration {
	return corev1.Toleration{
		Key:      t4.NodeTaintKey,
		Operator: corev1.TolerationOpEqual,
		Value:    t4.NodeTaintValue,
		Effect:   corev1.TaintEffectNoSchedule,
	}
}

func TestT4NodeIsolationAdmitsCompliantT4Pod(t *testing.T) {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "claude-t4-abc",
			Namespace: "lenny-agents-kata",
			Labels:    map[string]string{t4.WorkspaceTierLabel: t4.WorkspaceTierT4},
		},
		Spec: corev1.PodSpec{
			NodeSelector: t4NodeSelector(),
			Tolerations:  []corev1.Toleration{t4PodToleration()},
		},
	}
	resp := webhook.T4NodeIsolation()(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "t1",
		Operation: admissionv1.Create,
		Kind:      metav1.GroupVersionKind{Kind: "Pod"},
		Object:    t4PodRaw(t, pod),
	})
	if !resp.Allowed {
		t.Fatalf("a compliant T4 pod should be admitted: %+v", resp.Result)
	}
}

func TestT4NodeIsolationRejectsT4PodMissingToleration(t *testing.T) {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "claude-t4-abc",
			Namespace: "lenny-agents-kata",
			Labels:    map[string]string{t4.WorkspaceTierLabel: t4.WorkspaceTierT4},
		},
		Spec: corev1.PodSpec{NodeSelector: t4NodeSelector()},
	}
	resp := webhook.T4NodeIsolation()(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "t2",
		Operation: admissionv1.Create,
		Kind:      metav1.GroupVersionKind{Kind: "Pod"},
		Object:    t4PodRaw(t, pod),
	})
	if resp.Allowed {
		t.Fatal("a T4 pod missing the T4 taint toleration must be rejected")
	}
	if resp.Result.Code != http.StatusForbidden {
		t.Errorf("code = %d, want 403", resp.Result.Code)
	}
	if !strings.Contains(resp.Result.Message, "STR-003") {
		t.Errorf("reason %q does not carry STR-003", resp.Result.Message)
	}
}

func TestT4NodeIsolationAdmitsT4PodViaNodeAffinity(t *testing.T) {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "claude-t4-abc",
			Namespace: "lenny-agents-kata",
			Labels:    map[string]string{t4.WorkspaceTierLabel: t4.WorkspaceTierT4},
		},
		Spec: corev1.PodSpec{
			Affinity: &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{{
							MatchExpressions: []corev1.NodeSelectorRequirement{{
								Key:      t4.NodeLabelKey,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{t4.NodeLabelValue},
							}},
						}},
					},
				},
			},
			Tolerations: []corev1.Toleration{t4PodToleration()},
		},
	}
	resp := webhook.T4NodeIsolation()(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "t3",
		Operation: admissionv1.Create,
		Kind:      metav1.GroupVersionKind{Kind: "Pod"},
		Object:    t4PodRaw(t, pod),
	})
	if !resp.Allowed {
		t.Fatalf("a T4 pod pinning the T4 node via nodeAffinity should be admitted: %+v", resp.Result)
	}
}

func TestT4NodeIsolationRejectsNonT4PodOnT4Node(t *testing.T) {
	// §6.4: a non-T4 pod (no workspace-tier label) carrying the T4
	// node selector must be rejected so it cannot occupy a
	// T4-dedicated node.
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "claude-worker-xyz",
			Namespace: "lenny-agents",
		},
		Spec: corev1.PodSpec{NodeSelector: t4NodeSelector()},
	}
	resp := webhook.T4NodeIsolation()(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "t4",
		Operation: admissionv1.Create,
		Kind:      metav1.GroupVersionKind{Kind: "Pod"},
		Object:    t4PodRaw(t, pod),
	})
	if resp.Allowed {
		t.Fatal("a non-T4 pod carrying the T4 node selector must be rejected")
	}
	if !strings.Contains(resp.Result.Message, "STR-003") {
		t.Errorf("reason %q does not carry STR-003", resp.Result.Message)
	}
}

func TestT4NodeIsolationAdmitsPlainNonT4Pod(t *testing.T) {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-worker-xyz", Namespace: "lenny-agents"},
		Spec:       corev1.PodSpec{NodeSelector: map[string]string{"kubernetes.io/os": "linux"}},
	}
	resp := webhook.T4NodeIsolation()(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "t5",
		Operation: admissionv1.Create,
		Kind:      metav1.GroupVersionKind{Kind: "Pod"},
		Object:    t4PodRaw(t, pod),
	})
	if !resp.Allowed {
		t.Fatalf("a plain non-T4 pod should be admitted: %+v", resp.Result)
	}
}

func TestT4NodeIsolationRejectsUndecodablePod(t *testing.T) {
	// Fail-closed: a Pod object the webhook cannot decode is rejected.
	resp := webhook.T4NodeIsolation()(context.Background(), &admissionv1.AdmissionRequest{
		UID:    "t6",
		Kind:   metav1.GroupVersionKind{Kind: "Pod"},
		Object: runtime.RawExtension{Raw: []byte("{not a pod")},
	})
	if resp.Allowed {
		t.Fatal("an undecodable Pod object must be rejected")
	}
}

func TestT4NodeIsolationHandlesGenerateNamePod(t *testing.T) {
	// A pod created from a GenerateName has an empty metadata.name at
	// admission time; the webhook still produces a usable rejection.
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "claude-t4-",
			Namespace:    "lenny-agents-kata",
			Labels:       map[string]string{t4.WorkspaceTierLabel: t4.WorkspaceTierT4},
		},
		// No selector, no toleration — rejected.
	}
	resp := webhook.T4NodeIsolation()(context.Background(), &admissionv1.AdmissionRequest{
		UID:       "t7",
		Operation: admissionv1.Create,
		Kind:      metav1.GroupVersionKind{Kind: "Pod"},
		Object:    t4PodRaw(t, pod),
	})
	if resp.Allowed {
		t.Fatal("a non-compliant T4 pod must be rejected even with a GenerateName")
	}
}
