// SPDX-License-Identifier: MIT

//go:build conformance

// Tier-10 conformance case for the intra-pod pointers in the published
// gateway ↔ adapter gRPC contract. §28.7 lists schemas/lenny-adapter.proto
// as a wire-contract artifact whose consumers are the runtime authors
// implementing the adapter contract and the external-adapter compliance
// suite, so a section pointer in that artifact is part of what a runtime
// author is handed: it is how the author finds the behavior a conforming
// runtime must implement.
//
// Three of its comments describe intra-pod behavior — the
// lifecycle_capabilities/lifecycle_support exchange that classifies a
// Full-level runtime, the intra-pod platform MCP server that classifies a
// Standard-level one, and the INTERRUPT_TIMEOUT status the adapter returns
// when the interrupt deadline elapses. §28.5.3 states all three, on the
// CH-RUNTIMEOPS and CH-MCP-PLATFORM cards. This case asserts each pointer
// the contract carries resolves to a section that states the material,
// which a substring check on the proto alone cannot establish.
//
// spec: §28.5.3 (intra-pod contract cards), §28.7 (wire-contract artifact
// register), §15.4.3 (runtime integration levels).
package tier10_conformance_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// intraPodSectionHeading opens the §28.5.3 card set.
const intraPodSectionHeading = "#### 28.5.3 Intra-pod"

// conformanceRepoRoot walks up from the working directory to the module
// root. The tier-10 package drives binaries and reads repository
// artifacts by absolute path.
func conformanceRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for d := wd; d != "/" && d != ""; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	t.Fatalf("no go.mod above %s", wd)
	return ""
}

// sectionBody returns the markdown between heading and the next heading at
// the same or a shallower depth. It fails the test when the heading is
// absent, so a renamed or renumbered section is reported rather than
// silently yielding an empty body that passes a negative assertion.
func sectionBody(t *testing.T, src, heading string) string {
	t.Helper()
	depth := len(heading) - len(strings.TrimLeft(heading, "#"))
	lines := strings.Split(src, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, heading) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		t.Fatalf("heading %q not found", heading)
	}
	for i := start; i < len(lines); i++ {
		line := lines[i]
		if !strings.HasPrefix(line, "#") {
			continue
		}
		if d := len(line) - len(strings.TrimLeft(line, "#")); d <= depth {
			return strings.Join(lines[start:i], "\n")
		}
	}
	return strings.Join(lines[start:], "\n")
}

// TestAdapterContractIntraPodPointersResolve asserts that every §28.5.3
// pointer the published contract carries lands on a section that states
// the material the comment attributes to it.
//
// diagnosis: a runtime author following an intra-pod pointer out of
// schemas/lenny-adapter.proto reaches a section that does not state the
// handshake, the platform MCP server, or the interrupt-timeout status the
// comment sent them to look up, so the conformance surface the author
// implements against is unreachable from the artifact they are handed.
//
// spec: 28.5.3 (intra-pod contract cards), 28.7 (wire-contract artifact register).
func TestAdapterContractIntraPodPointersResolve(t *testing.T) {
	root := conformanceRepoRoot(t)

	protoSrc, err := os.ReadFile(filepath.Join(root, "schemas/lenny-adapter.proto"))
	if err != nil {
		t.Fatalf("read schemas/lenny-adapter.proto: %v", err)
	}
	if !strings.Contains(string(protoSrc), "§28.5.3") {
		t.Fatalf("schemas/lenny-adapter.proto carries no §28.5.3 pointer")
	}

	channelsSrc, err := os.ReadFile(filepath.Join(root, "spec/28_communication-channels.md"))
	if err != nil {
		t.Fatalf("read spec/28_communication-channels.md: %v", err)
	}
	body := sectionBody(t, string(channelsSrc), intraPodSectionHeading)

	// Each token is the material one of the three corrected comments
	// attributes to the section.
	for _, want := range []string{
		"CH-RUNTIMEOPS",
		"CH-MCP-PLATFORM",
		"lifecycle_capabilities",
		"lifecycle_support",
		"INTERRUPT_TIMEOUT",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("§28.5.3 must state %q for the contract pointer to resolve", want)
		}
	}

	// The interrupt-timeout status moved out of §4.7 entirely, so a
	// pointer back to it would resolve to nothing.
	componentsSrc, err := os.ReadFile(filepath.Join(root, "spec/04_system-components.md"))
	if err != nil {
		t.Fatalf("read spec/04_system-components.md: %v", err)
	}
	if strings.Contains(string(componentsSrc), "INTERRUPT_TIMEOUT") {
		t.Errorf("§4.7's file states INTERRUPT_TIMEOUT again; the contract pointer's owning section is ambiguous")
	}
}
