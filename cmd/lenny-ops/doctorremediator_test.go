// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/lennylabs/lenny/pkg/ops/doctor"
)

func readyPod(name string, ready bool) *corev1.Pod {
	st := corev1.ConditionFalse
	if ready {
		st = corev1.ConditionTrue
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: corednsNamespace, Name: name, Labels: map[string]string{"k8s-app": "kube-dns"}},
		Status:     corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: st}}},
	}
}

func corednsDeploymentObj(annotations map[string]string) *appsv1.Deployment {
	return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Namespace: corednsNamespace, Name: corednsDeployment, Annotations: annotations,
	}}
}

func endpointsObj(readyAddrs int) *corev1.Endpoints {
	addrs := make([]corev1.EndpointAddress, readyAddrs)
	for i := range addrs {
		addrs[i] = corev1.EndpointAddress{IP: "10.0.0." + string(rune('1'+i))}
	}
	return &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Namespace: corednsNamespace, Name: kubeDNSService},
		Subsets:    []corev1.EndpointSubset{{Addresses: addrs}},
	}
}

// spec: §25.6 line 2962 — coreDnsStuckEndpoint fires when Ready pods
// outnumber the endpoint addresses; the fix rolls the Deployment.
func TestRemediator_CoreDNS_DetectAndApply_spec_25_6_2962(t *testing.T) {
	cs := fake.NewSimpleClientset(
		corednsDeploymentObj(nil),
		endpointsObj(1), // 1 ready address
		readyPod("coredns-a", true),
		readyPod("coredns-b", true), // 2 ready pods → stuck endpoint
	)
	rem := &k8sDoctorRemediator{clientset: cs, now: func() time.Time { return time.Unix(0, 0).UTC() }}

	got, err := rem.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 1 || got[0].Code != doctor.FindingCoreDNSStuckEndpoint {
		t.Fatalf("detected=%+v", got)
	}
	if got[0].Resource != corednsNamespace+"/"+corednsDeployment {
		t.Fatalf("resource=%q", got[0].Resource)
	}

	if err := rem.Apply(context.Background(), got[0]); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	dep, err := cs.AppsV1().Deployments(corednsNamespace).Get(context.Background(), corednsDeployment, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if dep.Spec.Template.Annotations[restartedAtAnnotation] == "" {
		t.Fatalf("restart annotation not set: %+v", dep.Spec.Template.Annotations)
	}
}

// Endpoints in sync with Ready pods is not a finding.
func TestRemediator_CoreDNS_InSync_NotDetected(t *testing.T) {
	cs := fake.NewSimpleClientset(
		corednsDeploymentObj(nil),
		endpointsObj(2),
		readyPod("coredns-a", true),
		readyPod("coredns-b", true),
	)
	rem := &k8sDoctorRemediator{clientset: cs}
	got, err := rem.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no findings, got %+v", got)
	}
}

// spec: §25.6 line 2974 — a CoreDNS Deployment carrying the opt-out
// annotation is detected with OptOut set so the orchestrator skips it.
func TestRemediator_CoreDNS_OptOut_spec_25_6_2974(t *testing.T) {
	cs := fake.NewSimpleClientset(
		corednsDeploymentObj(map[string]string{doctorOptOutAnnotation: "true"}),
		endpointsObj(0),
		readyPod("coredns-a", true),
	)
	rem := &k8sDoctorRemediator{clientset: cs}
	got, err := rem.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 1 || !got[0].OptOut {
		t.Fatalf("want OptOut finding, got %+v", got)
	}
}

// An absent CoreDNS Deployment is not a finding and not an error.
func TestRemediator_CoreDNS_Absent_NoError(t *testing.T) {
	cs := fake.NewSimpleClientset() // nothing
	rem := &k8sDoctorRemediator{clientset: cs}
	got, err := rem.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no findings, got %+v", got)
	}
}

func certObj(ns, name, secret string, notAfter time.Time, ready bool) *unstructured.Unstructured {
	readyStr := "False"
	if ready {
		readyStr = "True"
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Certificate",
		"metadata":   map[string]any{"namespace": ns, "name": name},
		"spec":       map[string]any{"secretName": secret},
		"status": map[string]any{
			"notAfter":   notAfter.UTC().Format(time.RFC3339),
			"conditions": []any{map[string]any{"type": "Ready", "status": readyStr}},
		},
	}}
}

func certScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	return s
}

func certDynClient(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	// Detect traverses every doctor GVR when the dynamic client is set, so
	// the fake registers the list kinds for all of them even when a test
	// stages only Certificate objects.
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(certScheme(),
		map[schema.GroupVersionResource]string{
			certManagerGVR:     "CertificateList",
			sandboxWarmPoolGVR: "SandboxWarmPoolList",
		}, objs...)
}

// spec: §25.6 line 2964 — certManagerExpiring fires for a Ready
// certificate within 7 days of expiry; the fix annotates it and deletes
// the backing Secret to force re-issuance.
func TestRemediator_CertManager_DetectAndApply_spec_25_6_2964(t *testing.T) {
	now := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	expiring := certObj("lenny-system", "gateway-tls", "gateway-tls-secret", now.Add(3*24*time.Hour), true)
	dyn := certDynClient(expiring)
	cs := fake.NewSimpleClientset(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace: "lenny-system", Name: "gateway-tls-secret",
	}})
	rem := &k8sDoctorRemediator{clientset: cs, dyn: dyn, releaseNS: "lenny-system", now: func() time.Time { return now }}

	got, err := rem.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 1 || got[0].Code != doctor.FindingCertManagerExpiring {
		t.Fatalf("detected=%+v", got)
	}
	if got[0].Resource != "lenny-system/gateway-tls" {
		t.Fatalf("resource=%q", got[0].Resource)
	}

	if err := rem.Apply(context.Background(), got[0]); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// The Certificate carries the temporary-issue annotation.
	cert, err := dyn.Resource(certManagerGVR).Namespace("lenny-system").Get(context.Background(), "gateway-tls", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get cert: %v", err)
	}
	if cert.GetAnnotations()[issueTempCertAnnotation] != "true" {
		t.Fatalf("temp-cert annotation not set: %v", cert.GetAnnotations())
	}
	// The backing Secret was deleted to force re-issuance.
	if _, err := cs.CoreV1().Secrets("lenny-system").Get(context.Background(), "gateway-tls-secret", metav1.GetOptions{}); err == nil {
		t.Fatalf("backing Secret should have been deleted")
	}
}

// A certificate well outside the 7-day window is not a finding.
func TestRemediator_CertManager_NotExpiring_NotDetected(t *testing.T) {
	now := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	dyn := certDynClient(certObj("lenny-system", "fresh", "s", now.Add(30*24*time.Hour), true))
	rem := &k8sDoctorRemediator{dyn: dyn, releaseNS: "lenny-system", now: func() time.Time { return now }}
	got, err := rem.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no findings, got %+v", got)
	}
}

// An unhealthy (not-Ready) certificate is not auto-fixed — the spec
// requires cert-manager healthy before forcing re-issuance.
func TestRemediator_CertManager_Unhealthy_NotDetected(t *testing.T) {
	now := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	dyn := certDynClient(certObj("lenny-system", "broken", "s", now.Add(2*24*time.Hour), false))
	rem := &k8sDoctorRemediator{dyn: dyn, releaseNS: "lenny-system", now: func() time.Time { return now }}
	got, err := rem.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no findings for unhealthy cert, got %+v", got)
	}
}

// A code outside the §25.6 fixable table returns ErrManualRemediation so
// the orchestrator reports it manual. The three F-DR-1 findings
// (bootstrapConfigDrift, prometheusRuleMissing, warmPoolStuckReplenish)
// are no longer routed here; only a non-fixable code reaches the default.
//
// diagnosis: the default Apply branch stopped returning
// ErrManualRemediation for a code outside the fixable table, or one of
// the three F-DR-1 findings regressed to the manual default.
func TestRemediator_UnsupportedFinding_Manual(t *testing.T) {
	rem := &k8sDoctorRemediator{clientset: fake.NewSimpleClientset()}
	err := rem.Apply(context.Background(), doctor.Detected{Code: "someUnknownFinding"})
	if err != doctor.ErrManualRemediation {
		t.Fatalf("err=%v want ErrManualRemediation", err)
	}
}
