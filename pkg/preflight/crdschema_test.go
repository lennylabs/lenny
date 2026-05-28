// SPDX-License-Identifier: MIT

package preflight_test

import (
	"context"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/preflight"
)

// crdSchemaScheme builds a runtime scheme including
// apiextensions.k8s.io/v1 so the fake reader can host CRDs alongside
// the §17.9 baseline resources.
// spec: §10 line 437 — F-15.5.12.
func crdSchemaScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	if err := apiextensionsv1.AddToScheme(s); err != nil {
		t.Fatalf("apiextensions AddToScheme: %v", err)
	}
	return s
}

func crdWithSchemaVersion(name, version string) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Annotations: map[string]string{
				preflight.CRDSchemaVersionAnnotation: version,
			},
		},
	}
}

func crdWithoutAnnotation(name string) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
}

// spec: §10 line 443 — every Lenny CRD MUST declare the schema-version
// annotation matching the expected version. F-15.5.12.
func TestCRDSchemaVersionCheckPasses_spec_10_443(t *testing.T) {
	var objs []client.Object
	for _, name := range preflight.LennyCRDNames {
		objs = append(objs, crdWithSchemaVersion(name, preflight.CurrentCRDSchemaVersion))
	}
	c := fake.NewClientBuilder().WithScheme(crdSchemaScheme(t)).WithObjects(objs...).Build()

	d := preflight.CRDSchemaVersionCheck{}.Decide(context.Background(), c)
	if !d.Passed {
		t.Fatalf("expected pass; got reason %q", d.Reason)
	}
	if !strings.Contains(d.Reason, preflight.CurrentCRDSchemaVersion) {
		t.Errorf("passing reason should cite expected version; got %q", d.Reason)
	}
}

// spec: §10 line 443 — a missing CRD aborts the install with the
// runbook-grep message that names the absent CRD. F-15.5.12.
func TestCRDSchemaVersionCheckFailsOnMissingCRD_spec_10_443(t *testing.T) {
	// Install all CRDs except runtimes.lenny.dev so the check
	// surfaces a "missing" failure for that one name.
	var objs []client.Object
	for _, name := range preflight.LennyCRDNames {
		if name == "runtimes.lenny.dev" {
			continue
		}
		objs = append(objs, crdWithSchemaVersion(name, preflight.CurrentCRDSchemaVersion))
	}
	c := fake.NewClientBuilder().WithScheme(crdSchemaScheme(t)).WithObjects(objs...).Build()

	d := preflight.CRDSchemaVersionCheck{}.Decide(context.Background(), c)
	if d.Passed {
		t.Fatal("expected failure on missing CRD")
	}
	if !strings.Contains(d.Reason, `"runtimes.lenny.dev"`) {
		t.Errorf("reason should name missing CRD; got %q", d.Reason)
	}
	if !strings.Contains(d.Reason, "missing") {
		t.Errorf("reason should describe missing CRD; got %q", d.Reason)
	}
}

// spec: §10 line 443 — a CRD without the schema-version annotation
// fails fail-closed so the controller never starts against unlabeled
// CRDs. F-15.5.12.
func TestCRDSchemaVersionCheckFailsOnMissingAnnotation_spec_10_443(t *testing.T) {
	var objs []client.Object
	for _, name := range preflight.LennyCRDNames {
		if name == "sandboxes.lenny.dev" {
			objs = append(objs, crdWithoutAnnotation(name))
			continue
		}
		objs = append(objs, crdWithSchemaVersion(name, preflight.CurrentCRDSchemaVersion))
	}
	c := fake.NewClientBuilder().WithScheme(crdSchemaScheme(t)).WithObjects(objs...).Build()

	d := preflight.CRDSchemaVersionCheck{}.Decide(context.Background(), c)
	if d.Passed {
		t.Fatal("expected failure on missing annotation")
	}
	if !strings.Contains(d.Reason, preflight.CRDSchemaVersionAnnotation) {
		t.Errorf("reason should cite the annotation key; got %q", d.Reason)
	}
}

// spec: §10 line 443 — the operator runbook greps for the exact
// message text "schema version is <installed>; expected <expected>".
// F-15.5.12.
func TestCRDSchemaVersionCheckFailsOnMismatch_spec_10_443(t *testing.T) {
	var objs []client.Object
	for _, name := range preflight.LennyCRDNames {
		if name == "sandboxwarmpools.lenny.dev" {
			objs = append(objs, crdWithSchemaVersion(name, "0"))
			continue
		}
		objs = append(objs, crdWithSchemaVersion(name, preflight.CurrentCRDSchemaVersion))
	}
	c := fake.NewClientBuilder().WithScheme(crdSchemaScheme(t)).WithObjects(objs...).Build()

	d := preflight.CRDSchemaVersionCheck{}.Decide(context.Background(), c)
	if d.Passed {
		t.Fatal("expected failure on schema-version mismatch")
	}
	if !strings.Contains(d.Reason, `is "0"; expected "1"`) {
		t.Errorf("reason should carry the runbook-grep phrase; got %q", d.Reason)
	}
}

// spec: §10 line 437 — the embedded version constant matches the
// annotation the chart-shipped CRDs declare. A bump to one without the
// other fails this test. F-15.5.12.
func TestCurrentCRDSchemaVersionConstant_spec_10_437(t *testing.T) {
	if preflight.CurrentCRDSchemaVersion == "" {
		t.Fatal("CurrentCRDSchemaVersion must not be empty")
	}
}
