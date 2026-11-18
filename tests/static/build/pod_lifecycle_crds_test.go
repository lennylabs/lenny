// SPDX-License-Identifier: MIT

package build

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/pkg/preflight"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// wantLennyCRDFiles are the CRD manifests the chart and the embedded stack
// ship: one per Lenny kind, after the template and the warm pool merged
// into PodSet.
var wantLennyCRDFiles = []string{
	"lenny.dev_agentpods.yaml",
	"lenny.dev_podclaims.yaml",
	"lenny.dev_podsets.yaml",
	"lenny.dev_runtimes.yaml",
}

// yamlFiles lists the .yaml files in dir, sorted.
func yamlFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// TestChartAndEmbeddedStackShipTheLennyCRDs asserts that charts/lenny/crds/
// and pkg/embedded/crds/ each hold exactly the CRDs of runtimes, agentpods,
// podsets, and podclaims, byte for byte the same, that
// preflight.LennyCRDNames lists the same CRDs, and that each carries the
// lenny.dev/schema-version annotation equal to
// preflight.CurrentCRDSchemaVersion.
//
// spec: KAPI
//
// diagnosis: a CRD file was added, left behind, or renamed in one directory
// and not the other, or preflight checks a different set than the chart
// ships. Run `make generate`, delete any retired CRD file, and keep
// LennyCRDNames in pkg/preflight/crdschema.go equal to the chart's CRDs.
func TestChartAndEmbeddedStackShipTheLennyCRDs(t *testing.T) {
	root := schematest.RepoRoot(t)
	chartDir := filepath.Join(root, "charts", "lenny", "crds")
	embeddedDir := filepath.Join(root, "pkg", "embedded", "crds")
	chart := yamlFiles(t, chartDir)
	embedded := yamlFiles(t, embeddedDir)
	if strings.Join(chart, ",") != strings.Join(wantLennyCRDFiles, ",") {
		t.Errorf("charts/lenny/crds/ holds %v, want %v", chart, wantLennyCRDFiles)
	}
	if strings.Join(embedded, ",") != strings.Join(wantLennyCRDFiles, ",") {
		t.Errorf("pkg/embedded/crds/ holds %v, want %v", embedded, wantLennyCRDFiles)
	}

	var names []string
	for _, f := range chart {
		data, err := os.ReadFile(filepath.Join(chartDir, f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if mirror, err := os.ReadFile(filepath.Join(embeddedDir, f)); err != nil || string(mirror) != string(data) {
			t.Errorf("pkg/embedded/crds/%s differs from charts/lenny/crds/%s", f, f)
		}
		var crd struct {
			Metadata struct {
				Name        string            `yaml:"name"`
				Annotations map[string]string `yaml:"annotations"`
			} `yaml:"metadata"`
		}
		if err := yaml.Unmarshal(data, &crd); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		names = append(names, crd.Metadata.Name)
		if got := crd.Metadata.Annotations["lenny.dev/schema-version"]; got != preflight.CurrentCRDSchemaVersion {
			t.Errorf("%s: lenny.dev/schema-version = %q, want %q", f, got, preflight.CurrentCRDSchemaVersion)
		}
	}
	want := append([]string(nil), preflight.LennyCRDNames...)
	sort.Strings(want)
	sort.Strings(names)
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("chart CRDs %v differ from preflight.LennyCRDNames %v", names, want)
	}
}
