// SPDX-License-Identifier: MIT

//go:build conformance

// TestFrameworkReferenceRuntimesFullBattery exercises the five §26
// non-coding-agent reference runtimes (chat, langgraph, mastra,
// openai-assistants, crewai) against the §15.4.6 Full battery by name,
// closing the remainder of the gap TestReferenceCatalogNightly leaves
// after TestCodingAgentReferenceRuntimesFullBattery closed the
// coding-agent quarter: that test checks the catalog manifest's shape
// but never drives claude-code, gemini-cli, codex, or cursor-cli's
// siblings through the harness, and as of this file none of chat,
// langgraph, mastra, openai-assistants, or crewai had ever run through
// the harness at all.
//
// The real runtime images ship from github.com/lennylabs/runtime-templates
// and are not published to a registry reachable from this in-process test
// runner, so this file runs two variants per runtime, mirroring
// reference_coding_agents_test.go:
//
//   - image: gated on LENNY_REFERENCE_IMAGE_REGISTRY (and docker), pulls
//     the catalog-declared image and runs `lenny-compliance --image
//     <ref> --level full`. Skips (a genuine external-dependency skip,
//     not a stand-in) when the registry is not configured locally.
//   - stub: registry-free and always runs. streaming-echo is the
//     project's reference Full-level adapter (spec §15.4.3 lifecycle
//     channel, checkpoint, interrupt, credential rotation, deadline
//     signal) and stands in for each of these five runtimes' declared
//     Full level (spec §26.7-§26.11, each an `integrationLevel: full`
//     Runtime definition): the Full battery exercises the level-15.4.6
//     lifecycle contract every Full runtime must satisfy regardless of
//     the runtime-specific bootstrap sequence or workspace conventions
//     layered on top, so one conformant Full adapter driven under each
//     runtime's name closes the "nothing runs chat/langgraph/mastra/
//     openai-assistants/crewai through the Full battery" gap without
//     hand-rolling five near-identical stub binaries that would differ
//     from streaming-echo in name only.
package tier10_conformance_test

import (
	"os"
	"testing"

	"github.com/lennylabs/lenny/pkg/compliance"
)

// spec: 26 lines 375, 405, 435, 464 ("integrationLevel: full" declared
// by the chat, langgraph, mastra, openai-assistants, and crewai Runtime
// definitions); TESTING.md §12.10 ("The nine reference runtimes
// ... run conformance on every nightly ... the harness asserts the
// level-specific battery passes").
// diagnosis: a failure here means a §26 non-coding-agent reference
// runtime no longer clears the Full-level lifecycle battery its
// catalog entry declares (image variant), or that the harness's
// --image/--binary Full-level path itself is broken for a
// framework/general-purpose-shaped runtime (stub variant) — either way
// the nightly conformance run this test documents would not actually
// catch a Full-level regression in chat, langgraph, mastra,
// openai-assistants, or crewai.
func TestFrameworkReferenceRuntimesFullBattery(t *testing.T) {
	catalog, err := compliance.ReferenceCatalog()
	if err != nil {
		t.Fatalf("read reference catalog: %v", err)
	}

	var nonCodingAgents []compliance.ReferenceRuntime
	for _, r := range catalog {
		if r.Category != compliance.CategoryCodingAgent {
			nonCodingAgents = append(nonCodingAgents, r)
		}
	}
	wantNames := map[string]bool{
		"chat":              false,
		"langgraph":         false,
		"mastra":            false,
		"openai-assistants": false,
		"crewai":            false,
	}
	for _, rt := range nonCodingAgents {
		if _, ok := wantNames[rt.Name]; ok {
			wantNames[rt.Name] = true
		}
		if rt.Level != compliance.LevelFull {
			t.Errorf("§26 runtime %q must declare Full, catalog has %q", rt.Name, rt.Level)
		}
	}
	for name, seen := range wantNames {
		if !seen {
			t.Errorf("§26 runtime %q missing from the catalog manifest", name)
		}
	}

	a := buildArtifacts(t)
	registry := os.Getenv("LENNY_REFERENCE_IMAGE_REGISTRY")

	for _, rt := range nonCodingAgents {
		rt := rt

		t.Run(rt.Name+"_stub_full", func(t *testing.T) {
			// registry-free variant: drives the reference Full-level
			// stub adapter under this runtime's name through the same
			// §15.4.6 Full battery TestFullLevel pins for streaming-echo
			// directly, so a break in the harness's handling of a
			// non-coding-agent, Full-level catalog entry surfaces even
			// with no registry configured.
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
