// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/lennylabs/lenny/pkg/ops/doctor"
)

// prometheusRuleGVR is the Prometheus Operator PrometheusRule the
// prometheusRuleMissing fix asserts and re-applies in these tests.
var prometheusRuleGVR = schema.GroupVersionResource{
	Group:    "monitoring.coreos.com",
	Version:  "v1",
	Resource: "prometheusrules",
}

// stubHelmRenderSource is a doctor.HelmRenderSource that returns the
// staged bootstrap and monitoring references, exercising the F-DR-1
// bootstrapConfigDrift and prometheusRuleMissing remediations without a
// mounted render directory.
type stubHelmRenderSource struct {
	bootstrap   doctor.RenderedConfigMap
	bootstrapOK bool
	monitoring  doctor.RenderedMonitoring
	monOK       bool
}

func (s stubHelmRenderSource) BootstrapConfigMap(context.Context) (doctor.RenderedConfigMap, bool, error) {
	return s.bootstrap, s.bootstrapOK, nil
}

func (s stubHelmRenderSource) Monitoring(context.Context) (doctor.RenderedMonitoring, bool, error) {
	return s.monitoring, s.monOK, nil
}

// warmPoolObj builds a SandboxWarmPool custom resource with the given
// spec.minWarm, status.warmCount, and a PoolWarmingUp condition whose
// lastTransitionTime is stuckFor before now.
func warmPoolObj(name string, minWarm, warmCount int64, condStatus, reason string, transitioned time.Time) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "lenny.dev/v1alpha1",
		"kind":       "SandboxWarmPool",
		"metadata":   map[string]any{"namespace": "lenny-system", "name": name},
		"spec":       map[string]any{"minWarm": minWarm},
		"status": map[string]any{
			"warmCount": warmCount,
			"conditions": []any{map[string]any{
				"type":               poolWarmingUpConditionType,
				"status":             condStatus,
				"reason":             reason,
				"lastTransitionTime": transitioned.UTC().Format(time.RFC3339),
			}},
		},
	}}
}

func warmPoolDynClient(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			sandboxWarmPoolGVR: "SandboxWarmPoolList",
			prometheusRuleGVR:  "PrometheusRuleList",
			// detectCertExpiring lists the cert-manager GVR whenever the
			// dynamic client is set, so the list kind must be registered even
			// when the test stages no Certificate.
			certManagerGVR: "CertificateList",
		}, objs...)
}

// spec: §25.6 line 2956 — warmPoolStuckReplenish fires for a pool whose
// warmCount is below spec.minWarm (demand exceeds supply) and whose
// PoolWarmingUp condition has sat True/Provisioning past the 5m window;
// the fix re-kicks the pool so the controller re-drives it.
//
// diagnosis: warmPoolStuckReplenish is no longer detected or the re-kick
// annotation is not stamped, so a stalled warm pool is never re-driven —
// the F-DR-1 remediation regressed to the manual default.
func TestRemediator_WarmPoolStuck_DetectAndApply_spec_25_6_2956(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	stuck := warmPoolObj("echo", 5, 0, "True", "Provisioning", now.Add(-10*time.Minute))
	dyn := warmPoolDynClient(stuck)
	rem := &k8sDoctorRemediator{dyn: dyn, releaseNS: "lenny-system", now: func() time.Time { return now }}

	got, err := rem.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 1 || got[0].Code != doctor.FindingWarmPoolStuckReplenish {
		t.Fatalf("detected=%+v", got)
	}
	if got[0].Resource != "lenny-system/echo" {
		t.Fatalf("resource=%q", got[0].Resource)
	}

	if err := rem.Apply(context.Background(), got[0]); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	pool, err := dyn.Resource(sandboxWarmPoolGVR).Namespace("lenny-system").Get(context.Background(), "echo", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	if pool.GetAnnotations()[rekickAnnotation] == "" {
		t.Fatalf("re-kick annotation not stamped: %v", pool.GetAnnotations())
	}
}

// A pool whose supply meets its minWarm floor is not stuck (demand does
// not exceed supply), even with a warming condition present.
func TestRemediator_WarmPoolStuck_SupplyMeetsFloor_NotDetected(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	healthy := warmPoolObj("echo", 5, 5, "True", "Provisioning", now.Add(-10*time.Minute))
	dyn := warmPoolDynClient(healthy)
	rem := &k8sDoctorRemediator{dyn: dyn, now: func() time.Time { return now }}
	got, err := rem.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no findings, got %+v", got)
	}
}

// A pool below its floor but stuck for less than the 5m window is not yet
// a finding — a pool that is merely mid-provisioning is left alone.
func TestRemediator_WarmPoolStuck_WithinWindow_NotDetected(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	recent := warmPoolObj("echo", 5, 0, "True", "Provisioning", now.Add(-2*time.Minute))
	dyn := warmPoolDynClient(recent)
	rem := &k8sDoctorRemediator{dyn: dyn, now: func() time.Time { return now }}
	got, err := rem.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no findings within window, got %+v", got)
	}
}

// spec: §25.6 line 2953 — bootstrapConfigDrift fires when the live
// lenny-bootstrap ConfigMap content diverges from the Helm-rendered
// value; the fix re-applies the rendered content.
//
// diagnosis: bootstrapConfigDrift is not detected or the fix does not
// re-apply the rendered content, so a drifted bootstrap ConfigMap is
// never reconciled — the F-DR-1 remediation regressed.
func TestRemediator_BootstrapDrift_DetectAndApply_spec_25_6_2953(t *testing.T) {
	rendered := doctor.RenderedConfigMap{
		Name: "lenny-bootstrap-values",
		Data: map[string]string{"bootstrap-values.yaml": "tenants: [acme]\n"},
	}
	cs := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "lenny-system", Name: "lenny-bootstrap-values"},
		Data:       map[string]string{"bootstrap-values.yaml": "tenants: []\n"}, // drifted
	})
	rem := &k8sDoctorRemediator{
		clientset: cs, releaseNS: "lenny-system",
		helm: stubHelmRenderSource{bootstrap: rendered, bootstrapOK: true},
	}

	got, err := rem.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 1 || got[0].Code != doctor.FindingBootstrapConfigDrift {
		t.Fatalf("detected=%+v", got)
	}

	if err := rem.Apply(context.Background(), got[0]); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	live, err := cs.CoreV1().ConfigMaps("lenny-system").Get(context.Background(), "lenny-bootstrap-values", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get configmap: %v", err)
	}
	if live.Data["bootstrap-values.yaml"] != rendered.Data["bootstrap-values.yaml"] {
		t.Fatalf("rendered content not re-applied: %v", live.Data)
	}
}

// A live bootstrap ConfigMap matching the rendered value is not drift.
func TestRemediator_BootstrapDrift_InSync_NotDetected(t *testing.T) {
	data := map[string]string{"bootstrap-values.yaml": "tenants: [acme]\n"}
	cs := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "lenny-system", Name: "lenny-bootstrap-values"},
		Data:       data,
	})
	rem := &k8sDoctorRemediator{
		clientset: cs, releaseNS: "lenny-system",
		helm: stubHelmRenderSource{bootstrap: doctor.RenderedConfigMap{Name: "lenny-bootstrap-values", Data: data}, bootstrapOK: true},
	}
	got, err := rem.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no drift finding, got %+v", got)
	}
}

// An absent lenny-bootstrap ConfigMap is itself drift; the fix creates it.
func TestRemediator_BootstrapDrift_Absent_CreatesConfigMap(t *testing.T) {
	rendered := doctor.RenderedConfigMap{
		Name: "lenny-bootstrap-values",
		Data: map[string]string{"bootstrap-values.yaml": "tenants: [acme]\n"},
	}
	cs := fake.NewSimpleClientset() // no ConfigMap
	rem := &k8sDoctorRemediator{
		clientset: cs, releaseNS: "lenny-system",
		helm: stubHelmRenderSource{bootstrap: rendered, bootstrapOK: true},
	}
	got, err := rem.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 1 || got[0].Code != doctor.FindingBootstrapConfigDrift {
		t.Fatalf("detected=%+v", got)
	}
	if err := rem.Apply(context.Background(), got[0]); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := cs.CoreV1().ConfigMaps("lenny-system").Get(context.Background(), "lenny-bootstrap-values", metav1.GetOptions{}); err != nil {
		t.Fatalf("ConfigMap not created: %v", err)
	}
}

// A nil Helm source leaves bootstrapConfigDrift undetected, so the
// orchestrator reports it not_detected rather than a false success.
func TestRemediator_BootstrapDrift_NoRenderSource_NotDetected(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "lenny-system", Name: "lenny-bootstrap-values"},
		Data:       map[string]string{"bootstrap-values.yaml": "x"},
	})
	rem := &k8sDoctorRemediator{clientset: cs, releaseNS: "lenny-system"} // helm nil
	got, err := rem.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no findings with nil render source, got %+v", got)
	}
}

// spec: §25.6 line 2955 — prometheusRuleMissing fires when monitoring is
// enabled but a rendered PrometheusRule is absent from the release
// namespace; the fix re-applies the rendered object.
//
// diagnosis: prometheusRuleMissing is not detected or the fix does not
// create the rendered PrometheusRule, so an operator with monitoring
// enabled never has its alerting rules restored — the F-DR-1 remediation
// regressed.
func TestRemediator_PrometheusRuleMissing_DetectAndApply_spec_25_6_2955(t *testing.T) {
	rule := doctor.RenderedObject{
		Group: "monitoring.coreos.com", Version: "v1", Resource: "prometheusrules",
		Namespace: "lenny-system", Name: "lenny-alerting-rules",
		Manifest: map[string]any{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "PrometheusRule",
			"metadata":   map[string]any{"namespace": "lenny-system", "name": "lenny-alerting-rules"},
			"spec":       map[string]any{"groups": []any{}},
		},
	}
	dyn := warmPoolDynClient() // no PrometheusRule present
	rem := &k8sDoctorRemediator{
		dyn: dyn, releaseNS: "lenny-system",
		helm: stubHelmRenderSource{monitoring: doctor.RenderedMonitoring{Objects: []doctor.RenderedObject{rule}}, monOK: true},
	}

	got, err := rem.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 1 || got[0].Code != doctor.FindingPrometheusRuleMissing {
		t.Fatalf("detected=%+v", got)
	}

	if err := rem.Apply(context.Background(), got[0]); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := dyn.Resource(prometheusRuleGVR).Namespace("lenny-system").Get(context.Background(), "lenny-alerting-rules", metav1.GetOptions{}); err != nil {
		t.Fatalf("PrometheusRule not created: %v", err)
	}
}

// A present PrometheusRule is not a finding — the fix does not overwrite
// an operator-customised object.
func TestRemediator_PrometheusRuleMissing_Present_NotDetected(t *testing.T) {
	existing := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "monitoring.coreos.com/v1",
		"kind":       "PrometheusRule",
		"metadata":   map[string]any{"namespace": "lenny-system", "name": "lenny-alerting-rules"},
	}}
	dyn := warmPoolDynClient(existing)
	rule := doctor.RenderedObject{
		Group: "monitoring.coreos.com", Version: "v1", Resource: "prometheusrules",
		Namespace: "lenny-system", Name: "lenny-alerting-rules",
		Manifest: existing.Object,
	}
	rem := &k8sDoctorRemediator{
		dyn: dyn, releaseNS: "lenny-system",
		helm: stubHelmRenderSource{monitoring: doctor.RenderedMonitoring{Objects: []doctor.RenderedObject{rule}}, monOK: true},
	}
	got, err := rem.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no findings when rule present, got %+v", got)
	}
}

// Monitoring disabled (source reports ok=false) leaves prometheusRuleMissing
// undetected, so the orchestrator reports it not_detected.
func TestRemediator_PrometheusRuleMissing_MonitoringDisabled_NotDetected(t *testing.T) {
	dyn := warmPoolDynClient()
	rem := &k8sDoctorRemediator{
		dyn: dyn, releaseNS: "lenny-system",
		helm: stubHelmRenderSource{monOK: false},
	}
	got, err := rem.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no findings when monitoring disabled, got %+v", got)
	}
}
