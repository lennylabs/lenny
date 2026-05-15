// SPDX-License-Identifier: MIT

// Package helm wraps `helm template` so tier-0 static tests can
// assert that the chart renders, that ConfigMap phase-stamp logic
// is correct, that the preflight Job is present, and that
// admission webhooks are wired with failurePolicy: Fail per
// TESTING.md §17.6.
//
// The helper shells out to the `helm` CLI rather than embedding
// helm/sdk so the tooling matches what an operator runs
// interactively. SkipUnlessAvailable handles the missing-binary
// case so tests run cleanly on machines without helm installed.
//
// Usage:
//
//	helm.SkipUnlessAvailable(t)
//	manifests := helm.Render(t, helm.Options{
//	    Chart:     "charts/lenny",
//	    Release:   "lenny",
//	    Namespace: "lenny-system",
//	    Values:    "charts/lenny/values-nano.yaml",
//	})
//	cm := manifests.MustFind("ConfigMap", "lenny-config")
//	if cm.Data["phase"] != "0" {
//	    t.Fatalf("phase stamp drift: got %q", cm.Data["phase"])
//	}
//
// # Environment variables
//
//	LENNY_HELM_BIN  Override the `helm` binary path. Defaults to
//	                whatever LookPath returns.
package helm

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// SkipUnlessAvailable t.Skips when the helm CLI is missing.
// Matches the chaos / kind / compose convention.
func SkipUnlessAvailable(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath(binary()); err != nil {
		t.Skipf("helm.SkipUnlessAvailable: %s not on PATH: %v", binary(), err)
	}
}

// Options configures Render.
type Options struct {
	Chart     string   // path to the chart directory or .tgz
	Release   string   // release name (default: "lenny")
	Namespace string   // namespace (default: "lenny-system")
	Values    string   // optional values.yaml path
	Set       []string // optional `--set k=v` overrides
}

// Manifest is one rendered Kubernetes resource.
type Manifest struct {
	APIVersion string
	Kind       string
	Name       string
	Namespace  string
	Raw        map[string]any // full parsed YAML for assertions
}

// Manifests is the list of every resource the chart rendered.
type Manifests []Manifest

// Render shells out to `helm template` and parses the multi-doc
// YAML output. Returns the parsed manifest list; t.Fatalf on
// any failure.
func Render(t testing.TB, opts Options) Manifests {
	t.Helper()
	if opts.Release == "" {
		opts.Release = "lenny"
	}
	if opts.Namespace == "" {
		opts.Namespace = "lenny-system"
	}
	args := []string{"template", opts.Release, opts.Chart, "--namespace", opts.Namespace}
	if opts.Values != "" {
		args = append(args, "-f", opts.Values)
	}
	for _, kv := range opts.Set {
		args = append(args, "--set", kv)
	}
	cmd := exec.Command(binary(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("helm template: %v\nstderr:\n%s", err, stderr.String())
	}
	manifests, err := parse(stdout.Bytes())
	if err != nil {
		t.Fatalf("helm.Render: parse output: %v", err)
	}
	return manifests
}

// Find returns the first manifest matching kind + name, or false.
func (m Manifests) Find(kind, name string) (Manifest, bool) {
	for _, x := range m {
		if x.Kind == kind && x.Name == name {
			return x, true
		}
	}
	return Manifest{}, false
}

// MustFind is Find with a t.Fatalf on miss.
func (m Manifests) MustFind(t testing.TB, kind, name string) Manifest {
	t.Helper()
	x, ok := m.Find(kind, name)
	if !ok {
		t.Fatalf("helm.Manifests.MustFind: no %s/%s in %d rendered manifest(s)", kind, name, len(m))
	}
	return x
}

// FindAll returns every manifest of the given Kind.
func (m Manifests) FindAll(kind string) []Manifest {
	out := []Manifest{}
	for _, x := range m {
		if x.Kind == kind {
			out = append(out, x)
		}
	}
	return out
}

// Kinds returns the unique Kind names present in the chart output
// in stable sorted order.
func (m Manifests) Kinds() []string {
	seen := map[string]bool{}
	for _, x := range m {
		seen[x.Kind] = true
	}
	out := []string{}
	for k := range seen {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func parse(blob []byte) (Manifests, error) {
	out := Manifests{}
	dec := yaml.NewDecoder(bytes.NewReader(blob))
	for {
		var doc map[string]any
		err := dec.Decode(&doc)
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			// yaml.v3 returns io.EOF as a sentinel via .Error().
			if strings.Contains(err.Error(), "EOF") {
				break
			}
			return nil, fmt.Errorf("decode: %w", err)
		}
		if len(doc) == 0 {
			continue
		}
		m := Manifest{Raw: doc}
		m.APIVersion, _ = doc["apiVersion"].(string)
		m.Kind, _ = doc["kind"].(string)
		if meta, ok := doc["metadata"].(map[string]any); ok {
			m.Name, _ = meta["name"].(string)
			m.Namespace, _ = meta["namespace"].(string)
		}
		out = append(out, m)
	}
	return out, nil
}

func binary() string {
	if v := os.Getenv("LENNY_HELM_BIN"); v != "" {
		return v
	}
	return "helm"
}

func sortStrings(s []string) {
	// Bubble sort kept tiny so the helper doesn't pull sort into
	// the dependency surface; the slices are <30 entries.
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}
