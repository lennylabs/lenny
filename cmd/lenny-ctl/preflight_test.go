// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/preflight"
)

func preflightScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := apiextensionsv1.AddToScheme(s); err != nil {
		t.Fatalf("apiextensions AddToScheme: %v", err)
	}
	return s
}

func crd(name, version string) *apiextensionsv1.CustomResourceDefinition {
	c := &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if version != "" {
		c.Annotations = map[string]string{preflight.CRDSchemaVersionAnnotation: version}
	}
	return c
}

// spec: §10.5 line 443 — `lenny-ctl preflight` exits 0 when every
// installed CRD is current and non-zero with the verbatim stale-CRD
// message otherwise. F-10.5.4.
func TestRunPreflightCRDCheck_spec_10_5_443(t *testing.T) {
	t.Run("all current passes", func(t *testing.T) {
		var objs []client.Object
		for _, name := range preflight.LennyCRDNames {
			objs = append(objs, crd(name, preflight.CurrentCRDSchemaVersion))
		}
		cl := fake.NewClientBuilder().WithScheme(preflightScheme(t)).WithObjects(objs...).Build()
		var out, errb bytes.Buffer
		if code := runPreflightCRDCheck(context.Background(), cl, &out, &errb); code != 0 {
			t.Fatalf("exit = %d, want 0 (stderr: %s)", code, errb.String())
		}
		if !strings.Contains(out.String(), preflight.CurrentCRDSchemaVersion) {
			t.Errorf("stdout %q should cite the schema version", out.String())
		}
	})

	t.Run("stale CRD fails with the spec message", func(t *testing.T) {
		var objs []client.Object
		for i, name := range preflight.LennyCRDNames {
			v := preflight.CurrentCRDSchemaVersion
			if i == 0 {
				v = "0" // one stale CRD
			}
			objs = append(objs, crd(name, v))
		}
		cl := fake.NewClientBuilder().WithScheme(preflightScheme(t)).WithObjects(objs...).Build()
		var out, errb bytes.Buffer
		if code := runPreflightCRDCheck(context.Background(), cl, &out, &errb); code != 1 {
			t.Fatalf("exit = %d, want 1", code)
		}
		if !strings.Contains(errb.String(), "schema version is") {
			t.Errorf("stderr %q should carry the §10.5 line 443 stale-CRD message", errb.String())
		}
	})

	t.Run("missing CRD fails", func(t *testing.T) {
		// Install nothing: every CRD is reported missing.
		cl := fake.NewClientBuilder().WithScheme(preflightScheme(t)).Build()
		var out, errb bytes.Buffer
		if code := runPreflightCRDCheck(context.Background(), cl, &out, &errb); code != 1 {
			t.Fatalf("exit = %d, want 1", code)
		}
		if !strings.Contains(errb.String(), "missing") {
			t.Errorf("stderr %q should report missing CRDs", errb.String())
		}
	})
}
