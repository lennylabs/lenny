// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency checks for the §8.6 budget-exhaustion
// lease-extension trigger relocation (proposal 0023, F-8.6.6 / F-15.3.6).
// The trigger moved from the adapter (which is never in the LLM request
// path) into the gateway LLM Proxy, the ExtendLease gRPC RPC was trimmed,
// and §8.6/§11.2/§8.3 were reconciled so all three agree on the
// proxy-mode extension-then-terminate predicate and the direct-mode
// settlement-bounded posture. These tests pin that reconciliation so a
// later spec or doc edit cannot silently reintroduce the adapter as the
// trigger owner or contradict the three sections against each other.
//
// The tests read the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: §8.6 (the gateway LLM Proxy drives the in-process trigger),
// §11.2 (proxy-mode exhaustion attempts extension before termination),
// §8.3 (over-run is settlement-bounded, reconciled against the parent
// budget).

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// specSection reads path and returns the text of the section whose
// heading contains headingSub, using the shared section/headingLevel
// helpers. It fails the test when the heading is absent, so a renamed
// section surfaces here rather than as a silently-empty match.
func specSection(t *testing.T, path, headingSub string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sec := section(string(body), headingSub)
	if sec == "" {
		t.Fatalf("%s: section heading %q not found (renamed or removed?)", filepath.Base(path), headingSub)
	}
	return sec
}

// spec: 8.6, 11.2, 8.3
// diagnosis: §8.6, §11.2, and §8.3 disagree on the proxy-mode
// budget-exhaustion predicate. Proposal 0023 reconciled all three to
// "attempt the §8.6 extension, and terminate only on a terminal outcome"
// for proxy mode, and to a settlement-bounded, no-in-path-termination
// posture for direct mode. A failure here means one section drifted from
// the other two — for example §11.2 reverted to "terminate immediately"
// with no extension, or §8.3 reasserted unqualified per-call rejection —
// leaving the normative contract self-contradictory. The three sections
// must state the same predicate.
func TestSpecBudgetExhaustionPredicateAgrees_F866(t *testing.T) {
	root := repoRoot(t)
	specDir := filepath.Join(root, "spec")

	s86 := specSection(t, filepath.Join(specDir, "08_recursive-delegation.md"), "### 8.6 ")
	s83 := specSection(t, filepath.Join(specDir, "08_recursive-delegation.md"), "### 8.3 ")
	s112 := specSection(t, filepath.Join(specDir, "11_policy-and-controls.md"), "### 11.2 ")

	// §8.6 Trigger: the gateway LLM Proxy attempts the extension and
	// terminates only on a terminal outcome; the transparent path applies
	// on a grant.
	requireAllContain(t, "§8.6 Lease Extension", s86, []string{
		"gateway LLM Proxy",
		"proxy-mode",
		"automatically requests a lease extension",
		"CEILING_REACHED",
		"REJECTED",
		"terminates the session",
	})
	// §8.6 must scope the automatic extension to proxy mode and state the
	// direct-mode settlement-bounded posture with no in-path extension.
	requireAllContain(t, "§8.6 direct-mode posture", s86, []string{
		"applies only in `deliveryMode: proxy`",
		"`deliveryMode: direct`",
		"not eligible for an automatic in-path extension",
	})

	// §11.2 sequences extension-then-terminate for a proxy-mode session and
	// states the direct-mode no-in-path-termination posture.
	requireAllContain(t, "§11.2 Budgets and Quotas", s112, []string{
		"first attempts a §8.6 lease extension",
		"terminates the session only when the extension is terminal",
		"`GRANTED` or `PARTIALLY_GRANTED` extension raises the session's budget",
		"`deliveryMode: direct`",
		"not terminated in-path on budget breach",
	})

	// §8.3 over-run semantics: the proxy attempts the extension and
	// terminates only on a terminal outcome; the granted slice is a soft
	// cap enforced at settlement (over-run is settlement-bounded).
	requireAllContain(t, "§8.3 over-run semantics", s83, []string{
		"soft cap enforced at settlement",
		"the proxy attempts a lease extension",
		"terminates the session only when that extension is terminal",
		"`deliveryMode: direct`",
		"budget_return.lua",
	})
}

// spec: 8.6, 11.2
// diagnosis: a spec section still attributes the §8.6 budget-exhaustion
// trigger, the transparent retry, or the must-not-retry obligation to the
// adapter or to the removed adapter↔gateway ExtendLease gRPC channel.
// Proposal 0023 relocated the trigger to the gateway LLM Proxy and trimmed
// the RPC; the adapter is never in the LLM path (§4.9/§13.2) and cannot
// fire it. A failure here means a normative spec sentence names an actor
// that performs no such action, which is exactly the F-8.6.6 defect the
// proposal closed. No spec section may name the adapter as the trigger,
// retry, or extension-RPC owner.
func TestSpecNoAdapterTriggerAttribution_F866(t *testing.T) {
	root := repoRoot(t)
	specDir := filepath.Join(root, "spec")

	// The §8.6 sites: the introductory sentences (:627), the Trigger
	// paragraph (:629), the auto-mode must-not-retry sentence (:712), the
	// elicitation-mode terminal-condition (:725), and the "User rejects"
	// requesting-subtree identifier (:729). Also the §4.7 platform-tool
	// note (:697), §9.1 control-channel row (:12), §18.23 Phase-9
	// deliverable (:431), and §19 row 13 (:19).
	sites := []struct {
		file    string
		heading string
		label   string
	}{
		{"08_recursive-delegation.md", "### 8.6 ", "§8.6 Lease Extension"},
		{"04_system-components.md", "### 4.7 ", "§4.7 Runtime Adapter"},
		{"09_mcp-integration.md", "### 9.1 ", "§9.1 Where MCP Is Used"},
		{"18_build-sequence.md", "### 18.23 ", "§18.23 Phase 9"},
		{"19_resolved-decisions.md", "## 19", "§19 Resolved Decisions"},
	}

	// Phrases that name the adapter as the trigger/retry/RPC owner. A
	// surviving §4.7 note that lease extension is NOT an MCP tool is fine;
	// what must not survive is the adapter as the entity that requests the
	// extension or retries, or the "adapter↔gateway gRPC lifecycle"
	// attribution for lease extension.
	banned := []string{
		"the adapter automatically requests",
		"adapter retries the failed LLM call",
		"The adapter MUST NOT retry",
		"adapter↔gateway gRPC lifecycle",
		"adapter issued the `ExtendLease`",
		"adapter must not retry",
		"adapter knows exactly what was received",
	}

	for _, site := range sites {
		path := filepath.Join(specDir, site.file)
		sec := specSection(t, path, site.heading)
		for _, bad := range banned {
			if strings.Contains(sec, bad) {
				t.Errorf("%s still attributes the lease-extension trigger/retry to the adapter: found %q — the trigger moved to the gateway LLM Proxy (F-8.6.6)", site.label, bad)
			}
		}
	}

	// The §4.7 "Adapter → Gateway RPCs" table must no longer carry an
	// ExtendLease RPC row (the RPC was trimmed). The whole §4.7 section
	// must not name ExtendLease at all.
	s47 := specSection(t, filepath.Join(specDir, "04_system-components.md"), "### 4.7 ")
	if strings.Contains(s47, "ExtendLease") {
		t.Errorf("§4.7 still names the ExtendLease RPC; the gRPC method was trimmed (F-15.3.6)")
	}

	// §9.1's gateway↔pod control-channel row must no longer list "lease
	// extension" as a payload.
	s91 := specSection(t, filepath.Join(specDir, "09_mcp-integration.md"), "### 9.1 ")
	if strings.Contains(strings.ToLower(s91), "lease extension") {
		t.Errorf("§9.1 control-channel row still lists lease extension as a gRPC payload; it is an in-process operation now (F-15.3.6)")
	}
}

// spec: 8.6, 11.2
// diagnosis: a published doc still attributes the lease-extension trigger
// or the transparent retry to the adapter or to the removed
// adapter-to-gateway gRPC channel. The runtime-author guide, the adapter
// contract reference, and the ADR-0014 index row must all describe the
// trigger as an internal gateway operation the gateway LLM Proxy drives.
// A failure here means a reader-facing page contradicts the reconciled
// spec, telling a runtime author the adapter requests extensions when no
// adapter does.
func TestDocsNoAdapterTriggerAttribution_F866(t *testing.T) {
	root := repoRoot(t)
	docsDir := filepath.Join(root, "docs")

	// The runtime-author guide must describe the gateway LLM Proxy as the
	// in-process trigger, scoped to proxy mode, and must not attribute the
	// extension to the adapter or the gRPC channel.
	guide := readDoc(t, filepath.Join(docsDir, "runtime-author-guide", "delegation.md"))
	requireAllContain(t, "runtime-author-guide/delegation.md", guide, []string{
		"gateway's LLM proxy automatically requests an extension in-process",
		"`deliveryMode: proxy`",
	})
	requireNoneContain(t, "runtime-author-guide/delegation.md", guide, []string{
		"the adapter automatically requests an extension",
		"adapter-to-gateway gRPC channel",
	})

	// The adapter-contract reference must no longer carry an ExtendLease
	// row that names the adapter as the requester.
	contract := readDoc(t, filepath.Join(docsDir, "reference", "adapter-contract.md"))
	requireNoneContain(t, "reference/adapter-contract.md", contract, []string{
		"ExtendLease",
	})

	// The ADR-0014 index row title must read "internal gateway operation"
	// and must not read "via adapter↔gateway gRPC".
	adrIndex := readDoc(t, filepath.Join(docsDir, "adr", "index.md"))
	requireAllContain(t, "adr/index.md ADR-0014", adrIndex, []string{
		"Lease extension as an internal gateway operation",
	})
	requireNoneContain(t, "adr/index.md ADR-0014", adrIndex, []string{
		"Lease extension via adapter↔gateway gRPC",
	})
}

// requireAllContain fails the test for every phrase missing from body.
func requireAllContain(t *testing.T, label, body string, want []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("%s is missing the reconciled phrase %q", label, w)
		}
	}
}

// requireNoneContain fails the test for every phrase present in body.
func requireNoneContain(t *testing.T, label, body string, banned []string) {
	t.Helper()
	for _, b := range banned {
		if strings.Contains(body, b) {
			t.Errorf("%s still contains the stale phrase %q", label, b)
		}
	}
}
