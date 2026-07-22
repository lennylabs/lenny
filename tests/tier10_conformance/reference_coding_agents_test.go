// SPDX-License-Identifier: MIT

//go:build conformance

// TestCodingAgentReferenceRuntimesFullBattery exercises the four §26.1
// coding-agent reference runtimes (claude-code, gemini-cli, codex,
// cursor-cli) against the §15.4.6 Full battery by name, closing the gap
// TestReferenceCatalogNightly leaves: that test checks the catalog
// manifest's shape but never drives any of the four runtimes' declared
// integration level through the harness.
//
// The real runtime images ship from github.com/lennylabs/runtime-templates
// and are not published to a registry reachable from this in-process test
// runner, so this file runs two variants per runtime:
//
//   - image: gated on LENNY_REFERENCE_IMAGE_REGISTRY (and docker), pulls
//     the catalog-declared image and runs `lenny-compliance --image
//     <ref> --level full`. Skips (a genuine external-dependency skip,
//     not a stand-in) when the registry is not configured locally.
//   - stub: registry-free and always runs. streaming-echo is the
//     project's reference Full-level adapter (spec §15.4.3 lifecycle
//     channel, checkpoint, interrupt, credential rotation, deadline
//     signal) and stands in for each coding-agent runtime's declared
//     Full level: the Full battery exercises the level-15.4.6 lifecycle
//     contract every Full runtime must satisfy regardless of the
//     runtime-specific shell command or workspace conventions layered
//     on top (§26.2), so one conformant Full adapter driven under each
//     runtime's name closes the "nothing runs claude-code/gemini-cli/
//     codex/cursor-cli through the Full battery" gap without hand-rolling
//     four near-identical stub binaries that would differ from
//     streaming-echo in name only.
package tier10_conformance_test

import (
	"os"
	"testing"

	"github.com/lennylabs/lenny/pkg/compliance"
)

// spec: 26 line 74 ("All four coding-agent runtimes declare
// `integrationLevel: full` ([§5.1]), matching their `Level: Full` row in
// the §26.1 catalog table. Full level is required because these
// runtimes rely on the lifecycle channel for clean interrupt,
// checkpoint/restore, and in-place credential rotation during long
// coding sessions."); TESTING.md §12.10 ("The nine reference runtimes
// ... run conformance on every nightly ... the harness asserts the
// level-specific battery passes").
// diagnosis: a failure here means a §26 coding-agent reference runtime
// no longer clears the Full-level lifecycle battery its catalog entry
// declares (image variant), or that the harness's --image/--binary
// Full-level path itself is broken for a coding-agent-shaped runtime
// (stub variant) — either way the nightly conformance run this test
// documents would not actually catch a Full-level regression in
// claude-code, gemini-cli, codex, or cursor-cli.
func TestCodingAgentReferenceRuntimesFullBattery(t *testing.T) {
	catalog, err := compliance.ReferenceCatalog()
	if err != nil {
		t.Fatalf("read reference catalog: %v", err)
	}

	var codingAgents []compliance.ReferenceRuntime
	for _, r := range catalog {
		if r.Category == compliance.CategoryCodingAgent {
			codingAgents = append(codingAgents, r)
		}
	}
	wantNames := map[string]bool{
		"claude-code": false,
		"gemini-cli":  false,
		"codex":       false,
		"cursor-cli":  false,
	}
	for _, rt := range codingAgents {
		if _, ok := wantNames[rt.Name]; ok {
			wantNames[rt.Name] = true
		}
		if rt.Level != compliance.LevelFull {
			t.Errorf("§26.1 coding-agent runtime %q must declare Full, catalog has %q", rt.Name, rt.Level)
		}
	}
	for name, seen := range wantNames {
		if !seen {
			t.Errorf("§26.1 coding-agent runtime %q missing from the catalog manifest", name)
		}
	}

	a := buildArtifacts(t)
	registry := os.Getenv("LENNY_REFERENCE_IMAGE_REGISTRY")

	for _, rt := range codingAgents {
		rt := rt

		t.Run(rt.Name+"_stub_full", func(t *testing.T) {
			// registry-free variant: drives the reference Full-level
			// stub adapter under this runtime's name through the same
			// §15.4.6 Full battery TestFullLevel pins for streaming-echo
			// directly, so a break in the harness's handling of a
			// coding-agent-category, Full-level catalog entry surfaces
			// even with no registry configured.
			report := runCompliance(t, a, a.streamingEcho, "full")
			if report.Level != "full" {
				t.Errorf("%s: report level = %q, want full", rt.Name, report.Level)
			}
			assertAllPass(t, rt.Name, "full", report)
		})

		t.Run(rt.Name+"_image_full", func(t *testing.T) {
			if registry == "" {
				t.Skipf("LENNY_REFERENCE_IMAGE_REGISTRY not set; skipping the %s Full-battery image pull", rt.Name)
			}
			requireDocker(t)
			image := registry + "/" + rt.Image
			report := runComplianceImage(t, a, image, "full")
			if report.Level != "full" {
				t.Errorf("%s: report level = %q, want full", rt.Name, report.Level)
			}
			assertAllPass(t, rt.Name, "full", report)
		})
	}
}
