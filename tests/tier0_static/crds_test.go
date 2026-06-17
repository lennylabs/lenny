// SPDX-License-Identifier: MIT

package tier0_static

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// crdManifest is the subset of a generated CustomResourceDefinition
// the static checks assert against.
type crdManifest struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Group string `yaml:"group"`
		Names struct {
			Kind   string `yaml:"kind"`
			Plural string `yaml:"plural"`
		} `yaml:"names"`
		Scope    string `yaml:"scope"`
		Versions []struct {
			Name    string `yaml:"name"`
			Served  bool   `yaml:"served"`
			Storage bool   `yaml:"storage"`
		} `yaml:"versions"`
	} `yaml:"spec"`
}

// expectedCRDs maps each lenny.dev CRD kind to its required scope. The
// manifests under charts/lenny/crds/ are generated from the Go API
// types in pkg/apis/lenny/v1alpha1 by `make generate`. Runtime is
// platform-global and cluster-scoped; the pod-fabric CRDs live in agent
// namespaces and are namespaced.
var expectedCRDs = map[string]string{
	"Runtime":         "Cluster",
	"Sandbox":         "Namespaced",
	"SandboxClaim":    "Namespaced",
	"SandboxTemplate": "Namespaced",
	"SandboxWarmPool": "Namespaced",
}

func crdDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(schematest.RepoRoot(t), "charts", "lenny", "crds")
}

func loadCRDManifests(t *testing.T) map[string]crdManifest {
	t.Helper()
	dir := crdDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	byKind := map[string]crdManifest{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		var crd crdManifest
		if err := yaml.Unmarshal(data, &crd); err != nil {
			t.Fatalf("%s: not valid YAML: %v", e.Name(), err)
		}
		if crd.Kind != "CustomResourceDefinition" {
			t.Errorf("%s: kind = %q, want CustomResourceDefinition", e.Name(), crd.Kind)
			continue
		}
		byKind[crd.Spec.Names.Kind] = crd
	}
	return byKind
}

// spec: 4 (the lenny.dev CRDs are registered under charts/lenny/crds/)
// diagnosis: A CRD manifest under charts/lenny/crds/ is missing, has
//
//	the wrong group or scope, or does not serve a v1 version.
//	Run `make generate` and commit the updated manifests.
func TestCRDManifestsAreWellFormed(t *testing.T) {
	byKind := loadCRDManifests(t)

	for kind, wantScope := range expectedCRDs {
		crd, ok := byKind[kind]
		if !ok {
			t.Errorf("no CRD manifest declares kind %q in charts/lenny/crds/", kind)
			continue
		}
		if crd.APIVersion != "apiextensions.k8s.io/v1" {
			t.Errorf("%s: apiVersion = %q, want apiextensions.k8s.io/v1", kind, crd.APIVersion)
		}
		if crd.Spec.Group != "lenny.dev" {
			t.Errorf("%s: spec.group = %q, want lenny.dev", kind, crd.Spec.Group)
		}
		if crd.Spec.Scope != wantScope {
			t.Errorf("%s: spec.scope = %q, want %q", kind, crd.Spec.Scope, wantScope)
		}
		if want := crd.Spec.Names.Plural + ".lenny.dev"; crd.Metadata.Name != want {
			t.Errorf("%s: metadata.name = %q, want %q", kind, crd.Metadata.Name, want)
		}
		assertServesV1Alpha1(t, kind, crd)
	}

	for kind := range byKind {
		if _, ok := expectedCRDs[kind]; !ok {
			t.Errorf("charts/lenny/crds/ declares unexpected kind %q — not a pkg/apis/lenny/v1 type", kind)
		}
	}
}

// assertServesV1Alpha1 checks the CRD exposes exactly one version, named
// v1alpha1, that is both served and the storage version. The lenny.dev API
// group is at the v1alpha1 maturity; pkg/apis/lenny/v1alpha1 is its sole
// version.
func assertServesV1Alpha1(t *testing.T, kind string, crd crdManifest) {
	t.Helper()
	if len(crd.Spec.Versions) != 1 {
		t.Errorf("%s: declares %d versions, want exactly 1 (v1alpha1)", kind, len(crd.Spec.Versions))
		return
	}
	v := crd.Spec.Versions[0]
	if v.Name != "v1alpha1" {
		t.Errorf("%s: version name = %q, want v1alpha1", kind, v.Name)
	}
	if !v.Served {
		t.Errorf("%s: version v1alpha1 is not served", kind)
	}
	if !v.Storage {
		t.Errorf("%s: version v1alpha1 is not the storage version", kind)
	}
}

// findControllerGen locates the controller-gen binary on PATH or in
// the Go bin directory, where setup-dev.sh installs the pinned
// version. Returns "" when the tool is unavailable.
func findControllerGen() string {
	if p, err := exec.LookPath("controller-gen"); err == nil {
		return p
	}
	out, err := exec.Command("go", "env", "GOPATH").Output()
	if err != nil {
		return ""
	}
	p := filepath.Join(strings.TrimSpace(string(out)), "bin", "controller-gen")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// postProcessLine reports whether a manifest line is one of the two
// post-generation additions the committed CRDs carry that controller-gen
// does not emit: the `lenny.dev/schema-version` annotation (and its
// explanatory comment) and the top-level spec/status
// `x-kubernetes-preserve-unknown-fields: true` markers. §10 line 437
// applies the schema-version annotation by hand after generation; the
// preserve-unknown markers keep a stale CRD from stripping fields a newer
// controller writes. The drift check normalizes these away so it compares
// the controller-gen-owned schema rather than the hand-applied wrappers.
// The nested `workspacePlan` preserve-unknown marker is controller-gen
// output (the Go field carries +kubebuilder:pruning:PreserveUnknownFields)
// and is indented deeper than 12 spaces, so it is not stripped here.
func postProcessLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(trimmed, "lenny.dev/schema-version:"):
		return true
	case strings.HasPrefix(trimmed, "# spec: §10 line 437"),
		strings.HasPrefix(trimmed, "# read at controller startup"),
		strings.HasPrefix(trimmed, "# FATAL exit so stale CRDs"),
		strings.HasPrefix(trimmed, "# F-15.5.12."):
		return true
	case line == "            x-kubernetes-preserve-unknown-fields: true":
		// Exactly the top-level spec/status marker (12-space indent). The
		// deeper-nested workspacePlan marker has more leading whitespace.
		return true
	default:
		return false
	}
}

// stripPostProcessing removes the hand-applied post-generation lines so a
// committed manifest can be compared against raw controller-gen output.
func stripPostProcessing(manifest []byte) string {
	var b strings.Builder
	for _, line := range strings.Split(string(manifest), "\n") {
		if postProcessLine(line) {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// spec: 4 (CRD manifests are generated artifacts; they must not drift
//
//	from the Go API types)
//
// diagnosis: A pkg/apis/lenny/v1alpha1 type changed but the committed CRD
//
//	manifests were not regenerated. Run `make generate`, re-apply the
//	lenny.dev/schema-version annotation and the top-level
//	spec/status x-kubernetes-preserve-unknown-fields markers, and
//	commit charts/lenny/crds/.
func TestCRDManifestsInSyncWithGoTypes(t *testing.T) {
	cg := findControllerGen()
	if cg == "" {
		t.Skip("controller-gen not installed; run scripts/setup-dev.sh — drift check skipped")
	}
	root := schematest.RepoRoot(t)
	tmp := t.TempDir()

	cmd := exec.Command(cg, "crd",
		"paths=./pkg/apis/lenny/v1alpha1/...",
		"output:crd:dir="+tmp)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("controller-gen crd: %v\n%s", err, out)
	}

	regen, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("read regenerated dir: %v", err)
	}
	if len(regen) == 0 {
		t.Fatal("controller-gen produced no manifests; the v1alpha1 package path is wrong")
	}
	committed := crdDir(t)
	for _, e := range regen {
		want, err := os.ReadFile(filepath.Join(tmp, e.Name()))
		if err != nil {
			t.Fatalf("read regenerated %s: %v", e.Name(), err)
		}
		got, err := os.ReadFile(filepath.Join(committed, e.Name()))
		if err != nil {
			t.Errorf("charts/lenny/crds/%s is missing; run `make generate`", e.Name())
			continue
		}
		// Compare the controller-gen-owned schema, normalizing away the
		// hand-applied schema-version annotation and the top-level
		// spec/status preserve-unknown markers the committed manifest adds.
		if stripPostProcessing(got) != stripPostProcessing(want) {
			t.Errorf("charts/lenny/crds/%s is stale — its controller-gen schema differs from the Go types; run `make generate` and re-apply the schema-version annotation + spec/status preserve-unknown markers", e.Name())
		}
	}
}
