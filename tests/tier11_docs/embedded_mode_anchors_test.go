// SPDX-License-Identifier: MIT

// Tier-11 documentation check for proposal 0013 (cross-platform
// Embedded Mode). These tests are NOT under a build tag because they
// exercise the repository state directly — no external infrastructure
// required.

package tier11_docs_test

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// specCrossRef names one intra-spec link added by proposal 0013: the
// markdown file the link lands in, the target spec file, and the
// fragment (anchor slug) the link points at. Proposal §3.3, §3.5,
// §3.6, §3.8, and §3.10 each add one or more of these; the proposal's
// §5 testing gate requires every added anchor to resolve to a live
// heading.
type specCrossRef struct {
	targetFile string // spec file the anchor lives in
	anchor     string // fragment slug, without the leading '#'
	addedBy    string // proposal subsection that staged the link
}

// embeddedModeCrossRefs is the exhaustive set of intra-spec anchors
// proposal 0013 added or relies on across §15.4.3, §15.4.5, §17.4,
// §17.9.6, and §24. A heading rename that orphans any of these breaks
// the published cross-reference; this test fails before that ships.
//
// spec: §17.4 (Embedded Mode), §17.9.6 (embedded backends),
// §15.4.3 (runtime integration levels), §15.4.5 (runtime author
// roadmap), §24.19 (local stack). Proposal 0013 §3.3, §3.5, §3.6,
// §3.8, §3.10.
var embeddedModeCrossRefs = []specCrossRef{
	{targetFile: "17_deployment-topology.md", anchor: "174-local-development-mode-lenny-dev", addedBy: "0013 §3.3/§3.5/§3.6/§3.8"},
	{targetFile: "15_external-api-surface.md", anchor: "1543-runtime-integration-levels", addedBy: "0013 §3.5/§3.6"},
	{targetFile: "05_runtime-registry-and-pool-model.md", anchor: "53-isolation-profiles", addedBy: "0013 §3.10"},
	{targetFile: "13_security-model.md", anchor: "132-network-isolation", addedBy: "0013 §3.10"},
	{targetFile: "17_deployment-topology.md", anchor: "176-packaging-and-installation", addedBy: "0013 §3.10"},
}

// diagnosis: a 0013 cross-reference points at a spec heading that no
// longer exists. A reader following the published link gets a 404 to
// the section anchor; the Embedded-Mode docs and the spec have drifted
// out of sync. Re-point the link or restore the heading.
//
// spec: §17.4, §17.9.6, §15.4.3, §15.4.5, §24.19. Proposal 0013 §5
// (tier-0/tier-11 anchor-resolution gate).
func TestEmbeddedModeCrossRefsResolveToLiveHeadings(t *testing.T) {
	root := repoRoot(t)
	specDir := filepath.Join(root, "spec")

	for _, ref := range embeddedModeCrossRefs {
		path := filepath.Join(specDir, ref.targetFile)
		slugs, err := headingSlugs(path)
		if err != nil {
			t.Fatalf("read heading slugs from %s: %v", ref.targetFile, err)
		}
		if !slugs[ref.anchor] {
			t.Errorf("0013 cross-reference #%s (added by %s) does not resolve to any heading in spec/%s",
				ref.anchor, ref.addedBy, ref.targetFile)
		}
	}
}

// diagnosis: the abstract-socket reconciliation from proposal §3.5 and
// §3.6 regressed. Either a host-side blanket "Linux-only" restriction
// reappeared without the Embedded-Mode in-cluster-pod carve-out, or the
// Embedded-Mode carve-out was dropped, leaving a §15.4.5 cross-platform
// note self-contradictory. This pins proposal §3.9's verification: no
// Standard/Full surface may assert a host-side Linux-only requirement.
//
// spec: §15.4.3 (transport platform note), §17.4 (macOS/Windows note).
// Proposal 0013 §3.5, §3.6, §3.9.
func TestAbstractSocketRestrictionScopedToHostSideModes(t *testing.T) {
	root := repoRoot(t)

	cases := []struct {
		file        string
		mustContain []string // reconciliation text proposal §3.5/§3.6 added
	}{
		{
			file: filepath.Join(root, "spec", "15_external-api-surface.md"),
			mustContain: []string{
				"abstract Unix sockets",
				"in-cluster Linux pod",
				"Embedded Mode",
			},
		},
		{
			file: filepath.Join(root, "spec", "17_deployment-topology.md"),
			mustContain: []string{
				"in-cluster pod",
				"Embedded Mode (`lenny up`)",
			},
		},
	}

	for _, c := range cases {
		b, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatalf("read %s: %v", c.file, err)
		}
		content := string(b)
		for _, want := range c.mustContain {
			if !strings.Contains(content, want) {
				t.Errorf("%s missing abstract-socket reconciliation text %q (proposal §3.5/§3.6 regression)",
					filepath.Base(c.file), want)
			}
		}
	}
}

// diagnosis: the §17.4 local-fidelity disclosure from proposal §3.10
// regressed. The disclosure must name gVisor and Kata degradation with
// their distinct per-mechanism causes (a missing RuntimeClass/runsc shim
// for gVisor, missing nested hardware virtualization for Kata) and the
// disabled NetworkPolicy egress isolation. Conflating gVisor and Kata
// under one "nested virtualization" cause is itself a spec defect.
//
// spec: §17.4 (local isolation fidelity), §5.3 (isolation profiles),
// §13.2 (network isolation). Proposal 0013 §3.10.
func TestLocalFidelityDisclosurePresentWithDistinctCauses(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "spec", "17_deployment-topology.md"))
	if err != nil {
		t.Fatalf("read 17_deployment-topology.md: %v", err)
	}
	content := string(b)

	for _, want := range []string{
		"**Local isolation fidelity.**",
		"runsc",                   // gVisor cause: missing containerd shim
		"hardware virtualization", // Kata cause: cannot nest
		"NetworkPolicy enforcement disabled",
		"#53-isolation-profiles",
		"#132-network-isolation",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("§17.4 local-fidelity disclosure missing %q (proposal §3.10 regression)", want)
		}
	}

	// The gVisor and Kata causes must stay distinct: the disclosure must
	// not attribute gVisor's degradation to nested virtualization, which
	// is Kata's cause only (gVisor is a userspace kernel needing none).
	fidelity := localFidelityParagraph(content)
	if fidelity == "" {
		t.Fatal("could not locate the **Local isolation fidelity.** paragraph in §17.4")
	}
	if !strings.Contains(fidelity, "gVisor degrades because") {
		t.Errorf("§17.4 disclosure does not state gVisor's distinct cause; proposal §3.10 keeps gVisor and Kata causes separate")
	}
	if !strings.Contains(fidelity, "Kata degrades because") {
		t.Errorf("§17.4 disclosure does not state Kata's distinct cause; proposal §3.10 keeps gVisor and Kata causes separate")
	}
}

// localFidelityParagraph returns the single paragraph beginning with the
// bold "Local isolation fidelity." lead, or "" if absent.
func localFidelityParagraph(content string) string {
	const lead = "**Local isolation fidelity.**"
	i := strings.Index(content, lead)
	if i < 0 {
		return ""
	}
	rest := content[i:]
	if end := strings.Index(rest, "\n\n"); end >= 0 {
		return rest[:end]
	}
	return rest
}

// atxHeading matches an ATX markdown heading line (## … ######) and
// captures the heading text after the marker and the single space.
var atxHeading = regexp.MustCompile(`^#{1,6}\s+(.*)$`)

// headingSlugs reads a markdown file and returns the set of GitHub-style
// anchor slugs for every ATX heading in it. The slug derivation mirrors
// GitHub's: lowercase, drop characters that are not letters, digits,
// spaces, or hyphens (so backticks, parentheses, and dots vanish), then
// replace each run-internal space with a single hyphen.
func headingSlugs(path string) (map[string]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	slugs := map[string]bool{}
	inFence := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		// Skip fenced code blocks: a ``` or ~~~ line toggles the fence,
		// and headings inside a fence are not real headings.
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		m := atxHeading.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		slugs[slugify(m[1])] = true
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	return slugs, nil
}

// slugify converts heading text to a GitHub anchor slug.
func slugify(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		case r == '-':
			b.WriteRune('-')
		default:
			// Drop everything else (backticks, parentheses, dots,
			// em-dashes): GitHub removes these from the slug.
		}
	}
	return b.String()
}
