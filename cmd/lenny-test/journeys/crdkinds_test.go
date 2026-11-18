// SPDX-License-Identifier: MIT

package journeys

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/pkg/embedded/crds"
)

// These tests exercise contributor tooling and carry no spec annotation.

// builtinKinds are the Kubernetes resource names a postcondition may get
// without a CRD.
var builtinKinds = map[string]bool{
	"pods": true, "pod": true, "po": true, "deployments": true, "deployment": true, "deploy": true,
	"services": true, "service": true, "svc": true, "secrets": true, "secret": true,
	"configmaps": true, "configmap": true, "cm": true, "namespaces": true, "namespace": true, "ns": true,
	"nodes": true, "node": true, "no": true, "statefulsets": true, "statefulset": true, "sts": true,
	"daemonsets": true, "daemonset": true, "ds": true, "jobs": true, "job": true,
	"replicasets": true, "replicaset": true, "rs": true, "events": true, "event": true, "ev": true,
	"serviceaccounts": true, "serviceaccount": true, "sa": true,
	"persistentvolumeclaims": true, "pvc": true, "ingresses": true, "ingress": true, "ing": true,
	"networkpolicies": true, "networkpolicy": true, "netpol": true, "leases": true, "lease": true,
	"customresourcedefinitions": true, "crd": true, "crds": true,
}

// kubectlValueFlags take the next argument as their value.
var kubectlValueFlags = map[string]bool{
	"-n": true, "--namespace": true, "-o": true, "--output": true, "-l": true, "--selector": true,
	"--field-selector": true, "--sort-by": true, "--request-timeout": true,
}

// crdNames returns every name a kubectl get accepts for Lenny's CRDs:
// plural, singular, kind, short names, and plural.group. It reads them
// from pkg/embedded/crds, the copies of charts/lenny/crds that a static
// check keeps equal to the chart's, so this package imports the package a
// CRD change edits and the unit tier runs this test on every such change.
func crdNames(t *testing.T) map[string]bool {
	t.Helper()
	files, err := fs.Glob(crds.FS, "*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no CRDs in pkg/embedded/crds: %v", err)
	}
	names := map[string]bool{}
	for _, f := range files {
		b, err := crds.FS.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var crd struct {
			Spec struct {
				Group string `yaml:"group"`
				Names struct {
					Kind       string   `yaml:"kind"`
					Plural     string   `yaml:"plural"`
					Singular   string   `yaml:"singular"`
					ShortNames []string `yaml:"shortNames"`
				} `yaml:"names"`
			} `yaml:"spec"`
		}
		if err := yaml.Unmarshal(b, &crd); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		n := crd.Spec.Names
		for _, v := range append([]string{n.Plural, n.Singular, strings.ToLower(n.Kind), n.Plural + "." + crd.Spec.Group}, n.ShortNames...) {
			if v != "" && v != "." {
				names[v] = true
			}
		}
	}
	return names
}

// kubectlGetResources returns the resource names a kubectl argument list
// gets, or none when the command is not a get.
func kubectlGetResources(args []string) []string {
	verb := -1
	for i, a := range args {
		if kubectlValueFlags[a] {
			continue
		}
		if i > 0 && kubectlValueFlags[args[i-1]] {
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		if verb < 0 {
			if a != "get" {
				return nil
			}
			verb = i
			continue
		}
		var out []string
		for _, r := range strings.Split(a, ",") {
			r, _, _ = strings.Cut(r, "/")
			out = append(out, strings.ToLower(r))
		}
		return out
	}
	return nil
}

// TestCommittedJourneysGetOnlyKindsTheChartDefines checks every resource
// a committed postcondition gets with kubectl against Lenny's CRDs
// and the built-in kinds, so a renamed CRD fails the unit tier instead of
// every paid run of the journey.
func TestCommittedJourneysGetOnlyKindsTheChartDefines(t *testing.T) {
	known := crdNames(t)
	all, err := LoadDir(filepath.Join("..", "..", "..", "tests", "journeys"))
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, s := range all {
		for _, p := range s.Postconditions {
			lists := [][]string{p.Kubectl}
			if p.ForAny != nil {
				lists = append(lists, p.ForAny.Kubectl)
			}
			for _, args := range lists {
				for _, r := range kubectlGetResources(args) {
					checked++
					if !known[r] && !builtinKinds[r] {
						t.Errorf("%s: postcondition %s gets %q, which neither Lenny's CRDs nor the built-in kinds define", s.Name, p.Name, r)
					}
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no committed postcondition gets a resource with kubectl; this test checks nothing")
	}
}

func TestKubectlGetResourcesReadsTheResourceArgument(t *testing.T) {
	cases := map[string][]string{
		"-n lenny-agents get podsets -o json":              {"podsets"},
		"get -n x deployment/lenny-gateway -o jsonpath={}": {"deployment"},
		"--namespace=x get pods,svc":                       {"pods", "svc"},
		"get agentpods.lenny.dev":                          {"agentpods.lenny.dev"},
		"describe pod x":                                   nil,
		"-o json version":                                  nil,
	}
	for in, want := range cases {
		got := kubectlGetResources(strings.Fields(in))
		sort.Strings(got)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%q: got %v, want %v", in, got, want)
		}
	}
}
