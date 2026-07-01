// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/lennylabs/lenny/pkg/ops/diagnostics"
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

// warmPoolObj builds a SandboxWarmPool custom resource referencing the
// given SandboxTemplate. The pool's own status carries no PoolDrained
// condition: the WarmPoolController writes that condition onto the
// SandboxTemplate, so detection reads the dwell from the template, not from
// the pool.
func warmPoolObj(name, templateRef string, generation int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "lenny.dev/v1alpha1",
		"kind":       "SandboxWarmPool",
		"metadata":   map[string]any{"namespace": "lenny-system", "name": name, "generation": generation},
		"spec":       map[string]any{"minWarm": int64(5), "templateRef": templateRef},
	}}
}

// templateDrained builds a SandboxTemplate carrying a PoolDrained condition
// in its True state (the §25.6 zero-in-flight state) with the given
// transition time, staging the production representation the
// WarmPoolController writes onto the template status (not onto the pool). The
// True status marks entry into the drained state, so the transition time is
// the durable dwell timestamp the doctor reads.
func templateDrained(name string, transitioned time.Time) *unstructured.Unstructured {
	return templateWithDrained(name, metav1.ConditionTrue, "Drained", transitioned)
}

// templateWithDrained builds a SandboxTemplate carrying a PoolDrained
// condition with the given status, reason, and transition time.
func templateWithDrained(name string, status metav1.ConditionStatus, reason string, transitioned time.Time) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "lenny.dev/v1alpha1",
		"kind":       "SandboxTemplate",
		"metadata":   map[string]any{"namespace": "lenny-system", "name": name},
		"status": map[string]any{
			"conditions": []any{map[string]any{
				"type":               poolDrainedConditionType,
				"status":             string(status),
				"reason":             reason,
				"lastTransitionTime": transitioned.UTC().Format(time.RFC3339),
			}},
		},
	}}
}

// stubPoolDiagnosis is a poolDiagnosisSource that returns a fixed
// §25.6.1 diagnosis per pool name, so the warmPoolStuckReplenish
// detection can be exercised over the DEMAND_EXCEEDS_SUPPLY classification
// and the pod-state breakdown the production DataSource supplies.
type stubPoolDiagnosis struct {
	byPool map[string]*diagnostics.PoolDiagnosis
	err    error
}

func (s stubPoolDiagnosis) DiagnosePool(_ context.Context, poolName string) (*diagnostics.PoolDiagnosis, error) {
	if s.err != nil {
		return nil, s.err
	}
	if d, ok := s.byPool[poolName]; ok {
		return d, nil
	}
	return &diagnostics.PoolDiagnosis{Pool: poolName, Status: "healthy"}, nil
}

// demandExceedsSupply builds a §25.6.1 diagnosis classifying the pool as
// DEMAND_EXCEEDS_SUPPLY with the given warming/claimed in-flight counts.
func demandExceedsSupply(pool string, warming, claimed int) *diagnostics.PoolDiagnosis {
	return &diagnostics.PoolDiagnosis{
		Pool:       pool,
		Status:     "unhealthy",
		PodCounts:  diagnostics.PodCountBreakdown{Warming: warming, Claimed: claimed},
		Bottleneck: &diagnostics.PoolBottleneck{Category: diagnostics.BottleneckDemandExceedsSupply},
	}
}

func warmPoolDynClient(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			sandboxWarmPoolGVR: "SandboxWarmPoolList",
			sandboxTemplateGVR: "SandboxTemplateList",
			prometheusRuleGVR:  "PrometheusRuleList",
			// detectCertExpiring lists the cert-manager GVR whenever the
			// dynamic client is set, so the list kind must be registered even
			// when the test stages no Certificate.
			certManagerGVR: "CertificateList",
		}, objs...)
}

// spec: §25.6 line 2956 — warmPoolStuckReplenish fires for a pool the
// §25.6.1 diagnosis classifies DEMAND_EXCEEDS_SUPPLY with zero in-flight
// warm-up claims (no warming, no claimed pods), whose PoolDrained=True
// condition on the referenced SandboxTemplate has dwelt past the 5m window;
// the fix re-drives the pool by stamping a re-drive annotation so the
// apiserver emits a watch event and the WarmPoolController reconciles. The
// condition is staged on the SandboxTemplate, the production representation
// the WarmPoolController writes, not on the pool, so this regression pins the
// real signal rather than a proxy the controller never produces. The apply
// assertion checks the re-drive annotation the apiserver honors rather than a
// client-set .metadata.generation, which is server-managed and a no-op write.
//
// diagnosis: warmPoolStuckReplenish is no longer detected over the §25.6.1
// DEMAND_EXCEEDS_SUPPLY/zero-in-flight signal, or the fix does not stamp the
// re-drive annotation, so a stalled warm pool is never re-driven — the F-DR-1
// remediation regressed.
func TestRemediator_WarmPoolStuck_DetectAndApply_spec_25_6_2956(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	pool := warmPoolObj("echo", "echo-tmpl", 3)
	tmpl := templateDrained("echo-tmpl", now.Add(-10*time.Minute))
	dyn := warmPoolDynClient(pool, tmpl)
	rem := &k8sDoctorRemediator{
		dyn: dyn, releaseNS: "lenny-system", now: func() time.Time { return now },
		poolDx: stubPoolDiagnosis{byPool: map[string]*diagnostics.PoolDiagnosis{
			"echo": demandExceedsSupply("echo", 0, 0),
		}},
	}

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
	live, err := dyn.Resource(sandboxWarmPoolGVR).Namespace("lenny-system").Get(context.Background(), "echo", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	// The apiserver-honored signal is the re-drive annotation, which advances
	// resourceVersion and emits a watch Update the WarmPoolController wakes on.
	// A direct .metadata.generation write is server-managed and a no-op, so
	// asserting the annotation (rather than generation) pins the mechanism the
	// spec names against a real cluster.
	if got := live.GetAnnotations()[doctorRedriveAnnotation]; got != now.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("re-drive annotation not stamped: got %q, want %q", got, now.UTC().Format(time.RFC3339Nano))
	}
}

// A pool the §25.6.1 diagnosis reports with in-flight warm-up pods
// (warming > 0) is making progress, so it is not stuck even though its
// bottleneck is DEMAND_EXCEEDS_SUPPLY and its template's PoolDrained
// condition is False (still provisioning). This pins the spec's "zero
// in-flight warm-up claims" conjunct.
func TestRemediator_WarmPoolStuck_InFlightWarmup_NotDetected(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	pool := warmPoolObj("echo", "echo-tmpl", 3)
	tmpl := templateWithDrained("echo-tmpl", metav1.ConditionFalse, "NotDrained", now.Add(-10*time.Minute))
	dyn := warmPoolDynClient(pool, tmpl)
	rem := &k8sDoctorRemediator{
		dyn: dyn, releaseNS: "lenny-system", now: func() time.Time { return now },
		poolDx: stubPoolDiagnosis{byPool: map[string]*diagnostics.PoolDiagnosis{
			"echo": demandExceedsSupply("echo", 2, 0), // 2 warming pods in flight
		}},
	}
	got, err := rem.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no findings for a pool making warm-up progress, got %+v", got)
	}
}

// A pool whose bottleneck is not DEMAND_EXCEEDS_SUPPLY is not stuck, even
// with a drained template past the window: demand does not exceed supply.
func TestRemediator_WarmPoolStuck_NotDemandExceedsSupply_NotDetected(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	pool := warmPoolObj("echo", "echo-tmpl", 3)
	tmpl := templateDrained("echo-tmpl", now.Add(-10*time.Minute))
	dyn := warmPoolDynClient(pool, tmpl)
	rem := &k8sDoctorRemediator{
		dyn: dyn, releaseNS: "lenny-system", now: func() time.Time { return now },
		// No entry for "echo": the stub returns a healthy diagnosis with no
		// bottleneck, so DEMAND_EXCEEDS_SUPPLY is absent.
		poolDx: stubPoolDiagnosis{},
	}
	got, err := rem.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no findings when bottleneck is not demand-exceeds-supply, got %+v", got)
	}
}

// A pool in DEMAND_EXCEEDS_SUPPLY with zero in-flight claims but whose
// template drained less than 5m ago is not yet a finding — a pool that
// recently transitioned is left alone until the dwell window elapses.
func TestRemediator_WarmPoolStuck_WithinWindow_NotDetected(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	pool := warmPoolObj("echo", "echo-tmpl", 3)
	tmpl := templateDrained("echo-tmpl", now.Add(-2*time.Minute))
	dyn := warmPoolDynClient(pool, tmpl)
	rem := &k8sDoctorRemediator{
		dyn: dyn, releaseNS: "lenny-system", now: func() time.Time { return now },
		poolDx: stubPoolDiagnosis{byPool: map[string]*diagnostics.PoolDiagnosis{
			"echo": demandExceedsSupply("echo", 0, 0),
		}},
	}
	got, err := rem.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no findings within window, got %+v", got)
	}
}

// A nil pool-diagnosis source leaves warmPoolStuckReplenish undetected, so
// the orchestrator reports it not_detected rather than a false success.
func TestRemediator_WarmPoolStuck_NoDiagnosisSource_NotDetected(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	pool := warmPoolObj("echo", "echo-tmpl", 3)
	tmpl := templateDrained("echo-tmpl", now.Add(-10*time.Minute))
	dyn := warmPoolDynClient(pool, tmpl)
	rem := &k8sDoctorRemediator{dyn: dyn, releaseNS: "lenny-system", now: func() time.Time { return now }} // poolDx nil
	got, err := rem.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no findings with nil diagnosis source, got %+v", got)
	}
}

// A pool whose referenced SandboxTemplate is absent yields no finding: the
// dwell cannot be read, so detection fails closed rather than re-kicking a
// pool it cannot confirm is stalled.
func TestRemediator_WarmPoolStuck_TemplateAbsent_NotDetected(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	pool := warmPoolObj("echo", "missing-tmpl", 3)
	dyn := warmPoolDynClient(pool) // no SandboxTemplate staged
	rem := &k8sDoctorRemediator{
		dyn: dyn, releaseNS: "lenny-system", now: func() time.Time { return now },
		poolDx: stubPoolDiagnosis{byPool: map[string]*diagnostics.PoolDiagnosis{
			"echo": demandExceedsSupply("echo", 0, 0),
		}},
	}
	got, err := rem.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no findings when the referenced template is absent, got %+v", got)
	}
}

// A SandboxTemplate that carries no PoolDrained condition (the controller
// has not yet written one) yields no finding: the dwell is unknown.
func TestRemediator_WarmPoolStuck_TemplateNoCondition_NotDetected(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	pool := warmPoolObj("echo", "echo-tmpl", 3)
	tmpl := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "lenny.dev/v1alpha1", "kind": "SandboxTemplate",
		"metadata": map[string]any{"namespace": "lenny-system", "name": "echo-tmpl"},
		"status":   map[string]any{}, // no conditions
	}}
	dyn := warmPoolDynClient(pool, tmpl)
	rem := &k8sDoctorRemediator{
		dyn: dyn, releaseNS: "lenny-system", now: func() time.Time { return now },
		poolDx: stubPoolDiagnosis{byPool: map[string]*diagnostics.PoolDiagnosis{
			"echo": demandExceedsSupply("echo", 0, 0),
		}},
	}
	got, err := rem.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no findings when the template has no PoolDrained condition, got %+v", got)
	}
}

// applyWarmPoolStuck on a malformed resource id returns an error rather
// than silently succeeding.
func TestRemediator_WarmPoolStuck_ApplyMalformedResource(t *testing.T) {
	dyn := warmPoolDynClient()
	rem := &k8sDoctorRemediator{dyn: dyn, releaseNS: "lenny-system"}
	if err := rem.applyWarmPoolStuck(context.Background(), "no-slash"); err == nil {
		t.Fatalf("want error for malformed resource id, got nil")
	}
}

// A template whose PoolDrained condition carries a malformed
// lastTransitionTime yields no finding: the dwell cannot be computed, so
// detection fails closed.
func TestRemediator_WarmPoolStuck_MalformedTransitionTime_NotDetected(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	pool := warmPoolObj("echo", "echo-tmpl", 3)
	tmpl := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "lenny.dev/v1alpha1", "kind": "SandboxTemplate",
		"metadata": map[string]any{"namespace": "lenny-system", "name": "echo-tmpl"},
		"status": map[string]any{"conditions": []any{map[string]any{
			"type": poolDrainedConditionType, "status": "True", "reason": "Drained",
			"lastTransitionTime": "not-a-timestamp",
		}}},
	}}
	dyn := warmPoolDynClient(pool, tmpl)
	rem := &k8sDoctorRemediator{
		dyn: dyn, releaseNS: "lenny-system", now: func() time.Time { return now },
		poolDx: stubPoolDiagnosis{byPool: map[string]*diagnostics.PoolDiagnosis{
			"echo": demandExceedsSupply("echo", 0, 0),
		}},
	}
	got, err := rem.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no findings for a malformed transition time, got %+v", got)
	}
}

// A pool with no templateRef yields no finding: there is no template to read
// the dwell from.
func TestRemediator_WarmPoolStuck_NoTemplateRef_NotDetected(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	pool := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "lenny.dev/v1alpha1", "kind": "SandboxWarmPool",
		"metadata": map[string]any{"namespace": "lenny-system", "name": "echo", "generation": int64(3)},
		"spec":     map[string]any{"minWarm": int64(5)}, // no templateRef
	}}
	dyn := warmPoolDynClient(pool)
	rem := &k8sDoctorRemediator{
		dyn: dyn, releaseNS: "lenny-system", now: func() time.Time { return now },
		poolDx: stubPoolDiagnosis{byPool: map[string]*diagnostics.PoolDiagnosis{
			"echo": demandExceedsSupply("echo", 0, 0),
		}},
	}
	got, err := rem.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no findings for a pool with no templateRef, got %+v", got)
	}
}

// A non-absent template read error (API unreachable, not NotFound/Forbidden)
// propagates from detection so the run fails rather than reporting "nothing
// stuck" — the §25.6 fail-safe read contract.
//
// diagnosis: a transient SandboxTemplate read error is swallowed, so a
// warm-pool detection pass silently reports no findings during an API
// outage instead of failing the run.
func TestRemediator_WarmPoolStuck_TemplateReadError_Propagates(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	pool := warmPoolObj("echo", "echo-tmpl", 3)
	dyn := warmPoolDynClient(pool)
	dyn.PrependReactor("get", "sandboxtemplates", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("apiserver unreachable")
	})
	rem := &k8sDoctorRemediator{
		dyn: dyn, releaseNS: "lenny-system", now: func() time.Time { return now },
		poolDx: stubPoolDiagnosis{byPool: map[string]*diagnostics.PoolDiagnosis{
			"echo": demandExceedsSupply("echo", 0, 0),
		}},
	}
	if _, err := rem.Detect(context.Background()); err == nil {
		t.Fatalf("want a propagated template-read error, got nil")
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
