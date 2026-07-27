// SPDX-License-Identifier: MIT

package tier0_static

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: 17.6 (packaging and installation — the platform container
// images the chart deploys); TESTING.md §2 outcome 2 ("Run
// `lenny-test --changed` and get the minimal set of tests affected by
// uncommitted changes.") and §5 ("`tests/change-graph.json` maps source
// packages, schemas, migrations, and chart templates to the tests that
// exercise them.").
// diagnosis: The repo-root Dockerfile is the single source of every
//
//	platform container image. tests/testinfra/kind/install.sh builds each
//	cmd/ binary from it (`docker build --build-arg BINARY=...` against the
//	repo root) and loads the results into the Kind cluster that every
//	tier-5 suite deploys the chart onto, and its runtime stage fixes the
//	distroless/nonroot base and the bundled docs/runbooks corpus. A
//	Dockerfile-only diff therefore affects the Kind end-to-end suites. If
//	tests/change-graph.json has no "Dockerfile" glob key, that diff
//	resolves to an empty tier set and `lenny-test run --since`/`--changed`
//	falls back to the static tier alone, so a broken base image, a dropped
//	COPY, or a changed ENTRYPOINT ships without any suite that runs the
//	image ever being selected. Add a "Dockerfile" entry to
//	tests/change-graph.json mapping it to at least the e2e_kind tier, and
//	add "Dockerfile" to the known root-level files that
//	validateChangeGraphPaths in cmd/lenny-test/cmd_validate.go accepts
//	alongside Makefile, go.mod, and buf.yaml.
func TestChangeGraphRootDockerfileSelectsKindEndToEndTier(t *testing.T) {
	t.Parallel()

	tiers := resolveChangeGraphTiers(t, "Dockerfile")

	if len(tiers) == 0 {
		t.Fatal("a change to the repo-root Dockerfile resolved to an empty tier set (static only); every platform image the Kind suites deploy is built from it, so tests/change-graph.json must carry a \"Dockerfile\" glob key")
	}
	if !tiers["e2e_kind"] {
		t.Errorf("a change to the repo-root Dockerfile resolved to tiers %v; tests/testinfra/kind/install.sh builds every platform image from it and loads them into the tier-5 cluster, so the resolution must include %q",
			sortedKeys(tiers), "e2e_kind")
	}
}

// spec: 17.6 (packaging and installation — the platform container
// images the chart deploys); TESTING.md §5 ("`tests/change-graph.json`
// maps source packages, schemas, migrations, and chart templates to the
// tests that exercise them.")
// diagnosis: The "Dockerfile" change-graph entry must name test targets
//
//	that actually run the built image. An entry whose e2e_kind list is
//	empty, or whose targets do not live under tests/tier5_e2e_kind,
//	satisfies the tier-resolution guard above while selecting nothing
//	useful. Point the e2e_kind tier at the tier-5 suites that deploy the
//	chart with the images tests/testinfra/kind/install.sh builds.
func TestChangeGraphRootDockerfileEntryNamesKindSuites(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "tests", "change-graph.json"))
	if err != nil {
		t.Fatalf("read change-graph.json: %v", err)
	}
	var doc struct {
		Globs map[string]map[string][]string `json:"globs"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse change-graph.json: %v", err)
	}

	entry, ok := doc.Globs["Dockerfile"]
	if !ok {
		t.Fatal(`tests/change-graph.json has no "Dockerfile" glob key; the repo-root Dockerfile builds every image the Kind suites deploy`)
	}
	targets := entry["e2e_kind"]
	if len(targets) == 0 {
		t.Fatal(`"Dockerfile"."e2e_kind" is empty; it must name the tier-5 suites that deploy the images built from the repo-root Dockerfile`)
	}
	for _, target := range targets {
		// Mirror validateChangeGraphFileExistence: a target ending in
		// `/...` or `/` names the directory itself.
		probe := strings.TrimSuffix(target, "/...")
		probe = strings.TrimSuffix(probe, "/")
		if !strings.HasPrefix(probe, "tests/tier5_e2e_kind") {
			t.Errorf(`"Dockerfile"."e2e_kind" names %q, which is not a tier-5 target; the e2e_kind tier must select suites under tests/tier5_e2e_kind`, target)
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(probe))); err != nil {
			t.Errorf(`"Dockerfile"."e2e_kind" names %q, which does not exist on disk: %v`, target, err)
		}
	}
}
