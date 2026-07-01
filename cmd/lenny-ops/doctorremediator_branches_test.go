// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/lennylabs/lenny/pkg/ops/doctor"
)

// Apply of the two render-backed findings with a nil Helm source returns
// ErrManualRemediation, so a code that somehow reaches Apply without a
// render source is reported manual rather than silently succeeding.
//
// diagnosis: applyBootstrapDrift/applyPrometheusRuleMissing stopped
// guarding a nil Helm source, so the fix would panic or falsely succeed.
func TestRemediator_RenderBackedApply_NilHelm_Manual(t *testing.T) {
	rem := &k8sDoctorRemediator{releaseNS: "lenny-system"} // helm nil
	if err := rem.Apply(context.Background(), doctor.Detected{Code: doctor.FindingBootstrapConfigDrift}); err != doctor.ErrManualRemediation {
		t.Fatalf("bootstrap Apply err=%v want ErrManualRemediation", err)
	}
	if err := rem.Apply(context.Background(), doctor.Detected{Code: doctor.FindingPrometheusRuleMissing}); err != doctor.ErrManualRemediation {
		t.Fatalf("prometheus Apply err=%v want ErrManualRemediation", err)
	}
}

// A malformed warm-pool resource id (no namespace/name split) is a hard
// error rather than a silent no-op.
func TestRemediator_ApplyWarmPool_MalformedResource_Errors(t *testing.T) {
	rem := &k8sDoctorRemediator{}
	if err := rem.applyWarmPoolStuck(context.Background(), "no-slash"); err == nil {
		t.Fatal("want error for a malformed warm-pool resource id")
	}
}

// renderedHash prefers the source-supplied Hash when present so detection
// can skip re-hashing the rendered content.
func TestRenderedHash_UsesSuppliedHash(t *testing.T) {
	cm := doctor.RenderedConfigMap{Hash: "abc123", Data: map[string]string{"k": "v"}}
	if got := renderedHash(cm); got != "abc123" {
		t.Fatalf("renderedHash=%q want the supplied hash", got)
	}
	// With no supplied Hash it falls back to hashing Data.
	if got := renderedHash(doctor.RenderedConfigMap{Data: map[string]string{"k": "v"}}); got == "" {
		t.Fatal("renderedHash should hash Data when no Hash supplied")
	}
}

// applyPrometheusRuleMissing is idempotent: re-applying when the rendered
// object is already present leaves it untouched (no overwrite of an
// operator-customised object) and returns nil.
//
// diagnosis: applyMonitoringObject stopped skipping a present object, so
// the fix overwrites operator-customised monitoring resources.
func TestRemediator_ApplyPrometheusRule_PresentIsNoop(t *testing.T) {
	existing := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "monitoring.coreos.com/v1",
		"kind":       "PrometheusRule",
		"metadata":   map[string]any{"namespace": "lenny-system", "name": "lenny-alerting-rules", "resourceVersion": "7"},
	}}
	dyn := warmPoolDynClient(existing)
	rule := doctor.RenderedObject{
		Group: "monitoring.coreos.com", Version: "v1", Resource: "prometheusrules",
		Namespace: "lenny-system", Name: "lenny-alerting-rules",
		Manifest: map[string]any{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "PrometheusRule",
			"metadata":   map[string]any{"namespace": "lenny-system", "name": "lenny-alerting-rules"},
		},
	}
	rem := &k8sDoctorRemediator{
		dyn: dyn, releaseNS: "lenny-system",
		helm: stubHelmRenderSource{monitoring: doctor.RenderedMonitoring{Objects: []doctor.RenderedObject{rule}}, monOK: true},
	}
	if err := rem.applyPrometheusRuleMissing(context.Background()); err != nil {
		t.Fatalf("apply on present object: %v", err)
	}
	got, err := dyn.Resource(prometheusRuleGVR).Namespace("lenny-system").Get(context.Background(), "lenny-alerting-rules", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get after apply: %v", err)
	}
	if got.GetResourceVersion() != "7" {
		t.Fatalf("present object was overwritten: resourceVersion=%q", got.GetResourceVersion())
	}
}

// monitoringResourceForKind maps the known kinds and falls back to a
// naive plural for an unknown kind.
func TestMonitoringResourceForKind(t *testing.T) {
	cases := map[string]string{
		"PrometheusRule": "prometheusrules",
		"ServiceMonitor": "servicemonitors",
		"PodMonitor":     "podmonitors",
		"Widget":         "widgets",
		"Class":          "classes",
		"Policy":         "policies",
	}
	for kind, want := range cases {
		if got := monitoringResourceForKind(kind); got != want {
			t.Errorf("monitoringResourceForKind(%q)=%q want %q", kind, got, want)
		}
	}
}
