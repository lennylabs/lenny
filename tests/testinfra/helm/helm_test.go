// SPDX-License-Identifier: MIT

package helm

import (
	"testing"
)

// spec: 17.6 (chart-template rendering)
// diagnosis: parse() must split multi-doc YAML and surface
//
//	apiVersion/kind/metadata correctly so MustFind works.
func TestParseMultiDoc(t *testing.T) {
	t.Parallel()
	blob := []byte(`---
apiVersion: v1
kind: ConfigMap
metadata:
  name: lenny-config
  namespace: lenny-system
data:
  phase: "0"
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: lenny-gateway
  namespace: lenny-system
`)
	m, err := parse(blob)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(m) != 2 {
		t.Fatalf("want 2 manifests; got %d", len(m))
	}
	cm, ok := m.Find("ConfigMap", "lenny-config")
	if !ok {
		t.Fatal("ConfigMap/lenny-config not found")
	}
	if cm.Namespace != "lenny-system" {
		t.Errorf("namespace: got %q; want lenny-system", cm.Namespace)
	}
	dep, ok := m.Find("Deployment", "lenny-gateway")
	if !ok {
		t.Fatal("Deployment/lenny-gateway not found")
	}
	if dep.APIVersion != "apps/v1" {
		t.Errorf("apiVersion: got %q; want apps/v1", dep.APIVersion)
	}
}

// spec: 17.6 (chart-template rendering — Kinds index)
// diagnosis: Kinds() must return unique sorted Kind names so
//
//	tests can assert the catalog of rendered resources.
func TestKindsUniqueSorted(t *testing.T) {
	t.Parallel()
	m := Manifests{
		{Kind: "Deployment"},
		{Kind: "ConfigMap"},
		{Kind: "Deployment"},
		{Kind: "Service"},
	}
	got := m.Kinds()
	want := []string{"ConfigMap", "Deployment", "Service"}
	if len(got) != len(want) {
		t.Fatalf("len: got %d; want %d (%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("Kinds[%d] = %q; want %q", i, got[i], want[i])
		}
	}
}

// spec: 17.6 (chart-template rendering — FindAll)
// diagnosis: A chart renders multiple resources of the same
//
//	Kind (e.g., several ConfigMaps); FindAll must return
//	all of them so assertions can iterate.
func TestFindAll(t *testing.T) {
	t.Parallel()
	m := Manifests{
		{Kind: "ConfigMap", Name: "a"},
		{Kind: "Deployment", Name: "x"},
		{Kind: "ConfigMap", Name: "b"},
	}
	cms := m.FindAll("ConfigMap")
	if len(cms) != 2 {
		t.Fatalf("want 2 ConfigMaps; got %d", len(cms))
	}
	if cms[0].Name != "a" || cms[1].Name != "b" {
		t.Errorf("ordering not preserved: %v", cms)
	}
}
