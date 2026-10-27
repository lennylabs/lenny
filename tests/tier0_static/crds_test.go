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

// findGoimports locates the goimports binary the same way findControllerGen
// locates controller-gen. Returns "" when the tool is unavailable.
func findGoimports() string {
	if p, err := exec.LookPath("goimports"); err == nil {
		return p
	}
	out, err := exec.Command("go", "env", "GOPATH").Output()
	if err != nil {
		return ""
	}
	p := filepath.Join(strings.TrimSpace(string(out)), "bin", "goimports")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// spec: 4 (CRD manifests are generated artifacts; they must not drift
//
//	from the Go API types), 10.5 (Upgrade and Rollback Strategy)
//
// diagnosis: A pkg/apis/lenny/v1alpha1 type or marker changed but the
//
//	committed CRD manifests were not regenerated, or a committed manifest
//	carries a hand edit the generator does not reproduce. Run `make
//	generate` and commit charts/lenny/crds/ and pkg/embedded/crds/. The
//	lenny.dev/schema-version annotation and the top-level spec/status
//	x-kubernetes-preserve-unknown-fields markers come from +kubebuilder
//	markers on the root types; change the marker rather than the YAML.
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
		// Byte for byte: `make generate` writes exactly this output, so any
		// difference is either stale output or a hand edit the next
		// regeneration would silently drop (F-10.5.16).
		if string(got) != string(want) {
			t.Errorf("charts/lenny/crds/%s differs from controller-gen output; run `make generate` (and move any hand edit into a +kubebuilder marker)", e.Name())
		}
	}
	assertCRDManifestsCarryUpgradeMarkers(t, tmp)
}

// assertCRDManifestsCarryUpgradeMarkers checks that the generator output in
// dir carries the §10.5 additions the committed manifests used to receive by
// hand: the lenny.dev/schema-version annotation and the top-level spec and
// status x-kubernetes-preserve-unknown-fields markers. Without this check, a
// marker dropped from a root type would regenerate a manifest without them
// and the byte comparison would accept the loss once the tree is
// regenerated.
func assertCRDManifestsCarryUpgradeMarkers(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		var crd struct {
			Metadata struct {
				Annotations map[string]string `yaml:"annotations"`
			} `yaml:"metadata"`
			Spec struct {
				Versions []struct {
					Name   string `yaml:"name"`
					Schema struct {
						OpenAPIV3Schema struct {
							Properties map[string]struct {
								PreserveUnknown bool `yaml:"x-kubernetes-preserve-unknown-fields"`
							} `yaml:"properties"`
						} `yaml:"openAPIV3Schema"`
					} `yaml:"schema"`
				} `yaml:"versions"`
			} `yaml:"spec"`
		}
		if err := yaml.Unmarshal(data, &crd); err != nil {
			t.Fatalf("%s: not valid YAML: %v", e.Name(), err)
		}
		if got := crd.Metadata.Annotations["lenny.dev/schema-version"]; got == "" {
			t.Errorf("%s: generated manifest has no lenny.dev/schema-version annotation; the root type lost its +kubebuilder:metadata:annotations marker", e.Name())
		}
		for _, v := range crd.Spec.Versions {
			for _, field := range []string{"spec", "status"} {
				if !v.Schema.OpenAPIV3Schema.Properties[field].PreserveUnknown {
					t.Errorf("%s %s: top-level %s lacks x-kubernetes-preserve-unknown-fields; the root type's %s field lost its +kubebuilder:pruning:PreserveUnknownFields marker",
						e.Name(), v.Name, field, field)
				}
			}
		}
	}
}

// spec: 4 (the CRD API types' DeepCopy methods are generated artifacts)
//
// diagnosis: The committed pkg/apis/lenny/v1alpha1/zz_generated.deepcopy.go
//
//	differs from what `make generate` writes (controller-gen object, then
//	goimports with the repository's local prefix). Either a type changed
//	without a regeneration, or the file was edited by hand. Run `make
//	generate` and commit the file.
func TestDeepCopyInSyncWithGoTypes(t *testing.T) {
	cg := findControllerGen()
	if cg == "" {
		t.Skip("blocked: controller-gen is not installed; run scripts/setup-dev.sh to install it")
	}
	gi := findGoimports()
	if gi == "" {
		t.Skip("blocked: goimports is not installed; run scripts/setup-dev.sh to install it")
	}
	root := schematest.RepoRoot(t)
	tmp := t.TempDir()

	// The same two steps the Makefile generate target runs, with the output
	// redirected so the check never writes to the tree.
	cmd := exec.Command(cg, "object:headerFile=hack/boilerplate.go.txt",
		"paths=./pkg/apis/lenny/v1alpha1/...",
		"output:object:dir="+tmp)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("controller-gen object: %v\n%s", err, out)
	}
	regen := filepath.Join(tmp, "zz_generated.deepcopy.go")
	if out, err := exec.Command(gi, "-w", "-local", "github.com/lennylabs/lenny", regen).CombinedOutput(); err != nil {
		t.Fatalf("goimports: %v\n%s", err, out)
	}
	want, err := os.ReadFile(regen)
	if err != nil {
		t.Fatalf("read regenerated DeepCopy: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "pkg", "apis", "lenny", "v1alpha1", "zz_generated.deepcopy.go"))
	if err != nil {
		t.Fatalf("read committed DeepCopy: %v", err)
	}
	if string(got) != string(want) {
		t.Error("pkg/apis/lenny/v1alpha1/zz_generated.deepcopy.go differs from `make generate` output; run `make generate` and commit it")
	}
}

// spec: 4 (generated artifacts), 17.4 (Embedded Mode CRD install)
//
// diagnosis: The Makefile generate target no longer runs one of the steps
//
//	that keep a regeneration idempotent: goimports over the DeepCopy file,
//	or the copy of the chart CRDs into pkg/embedded/crds/. Without them a
//	clean `make generate` leaves a diff that the tier-0 and embedded-CRD
//	drift checks then reject. Restore the step in the Makefile.
func TestMakeGenerateRunsIdempotenceSteps(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(schematest.RepoRoot(t), "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	recipe := generateRecipe(string(data))
	if recipe == "" {
		t.Fatal("Makefile has no generate target")
	}
	for _, want := range []string{
		"goimports -w -local github.com/lennylabs/lenny pkg/apis/lenny/v1alpha1/zz_generated.deepcopy.go",
		"controller-gen crd paths=./pkg/apis/lenny/v1alpha1/... output:crd:dir=charts/lenny/crds",
		"cp charts/lenny/crds/*.yaml pkg/embedded/crds/",
	} {
		if !strings.Contains(recipe, want) {
			t.Errorf("Makefile generate recipe does not run %q", want)
		}
	}
	// Order matters: the goimports pass must follow the DeepCopy
	// generation it formats, and the copy must follow the CRD generation.
	if strings.Index(recipe, "goimports -w") < strings.Index(recipe, "controller-gen object") {
		t.Error("Makefile generate recipe runs goimports before controller-gen object")
	}
	if strings.Index(recipe, "cp charts/lenny/crds") < strings.Index(recipe, "controller-gen crd") {
		t.Error("Makefile generate recipe copies the CRDs before controller-gen crd writes them")
	}
}

// generateRecipe returns the recipe lines of the Makefile's generate
// target: the tab-indented lines after `generate:` up to the first line that
// is not a recipe line.
func generateRecipe(makefile string) string {
	var b strings.Builder
	in := false
	for _, line := range strings.Split(makefile, "\n") {
		if strings.HasPrefix(line, "generate:") {
			in = true
			continue
		}
		if !in {
			continue
		}
		if !strings.HasPrefix(line, "\t") {
			break
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
