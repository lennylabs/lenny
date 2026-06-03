// SPDX-License-Identifier: MIT

package preflight_test

import (
	"context"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/preflight"
)

func promOperatorCRD(name string) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

// spec: §16.9 R8 — when monitoring.format selects the operator CRDs and
// they are all installed, the check passes and cites the format.
func TestPrometheusOperatorCRDCheckPasses_spec_16_9(t *testing.T) {
	var objs []client.Object
	for _, name := range preflight.PrometheusOperatorCRDNames {
		objs = append(objs, promOperatorCRD(name))
	}
	c := fake.NewClientBuilder().WithScheme(crdSchemaScheme(t)).WithObjects(objs...).Build()

	d := preflight.PrometheusOperatorCRDCheck{Format: "prometheusrule"}.Decide(context.Background(), c)
	if !d.Passed {
		t.Fatalf("expected pass; got reason %q", d.Reason)
	}
	if !strings.Contains(d.Reason, "present") {
		t.Errorf("passing reason should confirm CRD presence; got %q", d.Reason)
	}
}

// spec: §16.9 R8 / F-16.9.4 — a missing operator CRD does not abort the
// install (Passed stays true) but raises an advisory that names the absent
// CRD and explains the ConfigMap fallback.
func TestPrometheusOperatorCRDCheckWarnsOnMissing_spec_16_9(t *testing.T) {
	// Install only one of the three CRDs so the other two are reported.
	c := fake.NewClientBuilder().WithScheme(crdSchemaScheme(t)).
		WithObjects(promOperatorCRD("prometheusrules.monitoring.coreos.com")).Build()

	d := preflight.PrometheusOperatorCRDCheck{Format: "both"}.Decide(context.Background(), c)
	if !d.Passed {
		t.Fatalf("operator-CRD advisory must not abort the install; got Passed=false reason %q", d.Reason)
	}
	if !strings.Contains(d.Reason, "WARNING") {
		t.Errorf("missing-CRD reason should be a WARNING advisory; got %q", d.Reason)
	}
	for _, want := range []string{
		"servicemonitors.monitoring.coreos.com",
		"podmonitors.monitoring.coreos.com",
		"ConfigMap",
	} {
		if !strings.Contains(d.Reason, want) {
			t.Errorf("advisory should mention %q; got %q", want, d.Reason)
		}
	}
}

// spec: §16.9 R8 — monitoring.format=configmap does not depend on the
// operator, so the check passes silently regardless of CRD presence.
func TestPrometheusOperatorCRDCheckSkipsForConfigmap_spec_16_9(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(crdSchemaScheme(t)).Build()
	for _, format := range []string{"configmap", ""} {
		d := preflight.PrometheusOperatorCRDCheck{Format: format}.Decide(context.Background(), c)
		if !d.Passed || d.Reason != "" {
			t.Errorf("format %q: expected silent pass, got Passed=%v reason=%q", format, d.Passed, d.Reason)
		}
	}
}
