// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// spec: §25.6 lines 2953, 2955 — the file-backed Helm-render source
// reads the operator-mounted bootstrap ConfigMap and monitoring
// manifests the two F-DR-1 fixes re-apply.
//
// diagnosis: the render-source reader stopped decoding the mounted
// bootstrap ConfigMap or monitoring manifests, so bootstrapConfigDrift
// and prometheusRuleMissing lose their reference and report not_detected.
func TestDirHelmRenderSource_ReadsBootstrapAndMonitoring_spec_25_6(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, bootstrapRenderFile), []byte(
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: lenny-bootstrap-values\ndata:\n  bootstrap-values.yaml: |\n    tenants: [acme]\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	monDir := filepath.Join(dir, monitoringRenderSubdir)
	if err := os.Mkdir(monDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(monDir, "prometheusrule.yaml"), []byte(
		"apiVersion: monitoring.coreos.com/v1\nkind: PrometheusRule\nmetadata:\n  name: lenny-alerting-rules\nspec:\n  groups: []\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	src := newHelmRenderSource(dir, "lenny-system")
	if src == nil {
		t.Fatal("newHelmRenderSource returned nil for a non-empty dir")
	}

	cm, ok, err := src.BootstrapConfigMap(context.Background())
	if err != nil || !ok {
		t.Fatalf("BootstrapConfigMap ok=%v err=%v", ok, err)
	}
	if cm.Name != "lenny-bootstrap-values" || cm.Data["bootstrap-values.yaml"] == "" {
		t.Fatalf("bootstrap render = %+v", cm)
	}

	m, ok, err := src.Monitoring(context.Background())
	if err != nil || !ok {
		t.Fatalf("Monitoring ok=%v err=%v", ok, err)
	}
	if len(m.Objects) != 1 {
		t.Fatalf("monitoring objects = %d, want 1", len(m.Objects))
	}
	o := m.Objects[0]
	if o.Resource != "prometheusrules" || o.Namespace != "lenny-system" || o.Name != "lenny-alerting-rules" {
		t.Fatalf("monitoring object = %+v", o)
	}
}

// An empty render dir yields a nil source so both findings report
// not_detected rather than a false success.
func TestNewHelmRenderSource_EmptyDir_Nil(t *testing.T) {
	if src := newHelmRenderSource("", "lenny-system"); src != nil {
		t.Fatalf("want nil source for empty dir, got %T", src)
	}
}

// Absent bootstrap file and monitoring subdir report ok=false, so the
// findings that depend on them are not detected.
func TestDirHelmRenderSource_AbsentInputs_ReportNotOK(t *testing.T) {
	src := newHelmRenderSource(t.TempDir(), "lenny-system")
	if _, ok, err := src.BootstrapConfigMap(context.Background()); ok || err != nil {
		t.Fatalf("BootstrapConfigMap ok=%v err=%v, want ok=false err=nil", ok, err)
	}
	if _, ok, err := src.Monitoring(context.Background()); ok || err != nil {
		t.Fatalf("Monitoring ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}

// A monitoring manifest with no apiVersion/kind is a hard error so a
// malformed render fails the run rather than silently reporting no
// finding.
func TestDirHelmRenderSource_MalformedMonitoring_Errors(t *testing.T) {
	dir := t.TempDir()
	monDir := filepath.Join(dir, monitoringRenderSubdir)
	if err := os.Mkdir(monDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(monDir, "bad.yaml"), []byte("just: a-map\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := newHelmRenderSource(dir, "lenny-system")
	if _, _, err := src.Monitoring(context.Background()); err == nil {
		t.Fatal("want error for a manifest with no apiVersion/kind")
	}
}
