// SPDX-License-Identifier: MIT

package tier0_static

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/helm"
)

// chartTemplatesDir is the chart's template tree, scanned to discover
// every Lenny component short name routed through the
// lenny.componentImage helper. Discovering the set from the templates
// (rather than hard-coding a list) keeps this check exhaustive: a
// component added through the helper is covered automatically, and a
// component that stops using the helper stops being asserted.
const chartTemplatesDir = "../../charts/lenny/templates"

// airgapMirrorPrefix stands in for an operator's single-source private
// mirror registry (§25.8 "Base registry URL. All Lenny component images
// are resolved relative to this."). The render below points
// platform.registry.url at it with no per-component overrides, so every
// Lenny component image must resolve under this exact prefix.
const airgapMirrorPrefix = "airgap-mirror.internal/lenny"

// defaultLennyRegistry is the chart's default platform.registry.url
// (charts/lenny/values.yaml). When a mirror is configured, no Lenny
// component image resolved through the registry config may still carry
// this default prefix — that would be an image that ignored the
// single-source mirror and would fail closed on a disconnected install.
const defaultLennyRegistry = "ghcr.io/lennylabs"

// componentNamePattern matches the component short name passed to the
// lenny.componentImage helper, for example
//
//	include "lenny.componentImage" (dict "root" $ "name" "lenny-gateway" ...)
var componentNamePattern = regexp.MustCompile(`"name"\s+"(lenny-[a-z0-9-]+)"`)

// TestAirgapRegistrySingleSourceRender pins the §25.8 / §17.8.6
// single-source image-registry contract at the chart-render level: with
// platform.registry.url set to one private mirror and no per-component
// overrides, every Lenny component image the chart emits resolves under
// that mirror, and none leaks the default Lenny registry. This is the
// tier-0 complement to the tier-5 disconnected-install test — it is
// exhaustive over every component routed through the lenny.componentImage
// helper (discovered from the templates), not a curated subset, so a
// newly added component that honors the single registry configuration is
// covered without editing this test, and one that leaks the default
// registry fails here before any cluster is involved.
//
// Third-party and bundled dependency images (Redis, MinIO, PgBouncer,
// the dedicated CoreDNS, etc.) are deliberately out of scope: §17.8.6
// enumerates the single-registry set as the gateway, lenny-ops,
// controllers, lenny-backup, and the warm-pool controller (all rendered
// through lenny.componentImage). Dependency images carry their own
// image.repository values and an air-gap operator mirrors them
// separately, so this check only asserts the components the spec routes
// through platform.registry.*.
//
// spec: §25.8 (Image Registry Configuration — "All Lenny components pull
// container images from a configurable registry ... All Lenny component
// images are resolved relative to this ... Deployers who mirror Lenny
// images to an internal registry configure platform.registry.url once
// and all components resolve correctly."); §17.8.6 (Image Registry and
// Air-Gap — "The chart's ImageResolver shared package composes every
// image reference from platform.registry.*, ensuring the gateway,
// lenny-ops, controllers, lenny-backup, and the warm-pool controller all
// honor the same registry configuration.").
func TestAirgapRegistrySingleSourceRender(t *testing.T) {
	helm.SkipUnlessAvailable(t)

	components := discoverComponentNames(t)
	if len(components) == 0 {
		t.Fatal("§25.8: found no lenny.componentImage call sites under " + chartTemplatesDir +
			"; the discovery regex or the chart layout changed — this check would pass vacuously")
	}
	// Guard against a silently-vacuous run: the mandatory core components
	// must always be discovered. If the render or discovery regressed to
	// where these are missing, fail loudly rather than pass on an empty
	// or truncated set.
	for _, must := range []string{"lenny-gateway", "lenny-ops", "lenny-controller"} {
		if !contains(components, must) {
			t.Fatalf("§25.8: mandatory component %q not discovered among componentImage call sites %v; "+
				"discovery is incomplete and the leak check would be unreliable", must, components)
		}
	}

	rendered := renderWithMirror(t)

	// Every Lenny component image resolved through the registry config
	// must resolve under the single mirror prefix and must not leak the
	// default Lenny registry. Both halves matter: the positive check
	// catches an image that resolves nowhere useful; the leak check
	// catches an image that ignored the mirror and kept the default.
	sawAnyMirrored := false
	for _, name := range components {
		leak := fmt.Sprintf("%s/%s:", defaultLennyRegistry, name)
		if strings.Contains(rendered, leak) {
			t.Errorf("§25.8/§17.8.6 violation: with platform.registry.url=%s, component %q still renders "+
				"under the default registry (%q). Every Lenny component image must resolve from the single "+
				"configured registry; route this image through the lenny.componentImage helper so it honors "+
				"platform.registry.*.", airgapMirrorPrefix, name, leak)
		}
		mirrored := fmt.Sprintf("%s/%s:", airgapMirrorPrefix, name)
		if strings.Contains(rendered, mirrored) {
			sawAnyMirrored = true
		}
	}

	// The render must actually contain at least the always-on core
	// component images resolved under the mirror, otherwise the leak
	// check above ran against a render in which no component image
	// appeared at all (a broken render would make every leak check pass
	// vacuously).
	for _, name := range []string{"lenny-gateway", "lenny-ops", "lenny-controller"} {
		mirrored := fmt.Sprintf("%s/%s:", airgapMirrorPrefix, name)
		if !strings.Contains(rendered, mirrored) {
			t.Errorf("§25.8: mandatory component %q did not render under the mirror as %q; the chart failed "+
				"to resolve a core image from platform.registry.url", name, mirrored)
		}
	}
	if !sawAnyMirrored {
		t.Fatal("§25.8: no discovered component image resolved under the mirror prefix; the render produced " +
			"no Lenny component images, so the single-source assertion is vacuous")
	}
}

// discoverComponentNames scans every chart template file for
// lenny.componentImage call sites and returns the sorted, de-duplicated
// set of component short names.
func discoverComponentNames(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	err := filepath.WalkDir(chartTemplatesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if ext := filepath.Ext(path); ext != ".yaml" && ext != ".tpl" {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, m := range componentNamePattern.FindAllStringSubmatch(string(data), -1) {
			seen[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("§25.8: scan chart templates under %s: %v", chartTemplatesDir, err)
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// renderWithMirror runs `helm template` for the whole chart with
// platform.registry.url pointed at a single private mirror and no
// per-component overrides, and returns the full rendered manifest as a
// string. Gates that would otherwise suppress optional component
// templates (the on-demand backup Job, the dedicated CoreDNS
// clusterIP requirement) are enabled so the render covers as many
// component images as possible.
func renderWithMirror(t *testing.T) string {
	t.Helper()
	args := []string{
		"template", "lenny", "../../charts/lenny",
		"--namespace", "lenny-system",
		"--set", "global.spiffeTrustDomain=lenny-test",
		"--set", "platform.registry.url=" + airgapMirrorPrefix,
		"--set", "coredns.clusterIP=10.96.0.10",
		"--set", "backups.onDemand.enabled=true",
	}
	out, err := exec.Command("helm", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("§25.8: helm template with platform.registry.url=%s: %v\n%s", airgapMirrorPrefix, err, out)
	}
	return string(out)
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}
