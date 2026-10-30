// SPDX-License-Identifier: MIT

// Tier-11 documentation check for the Embedded Mode echo placement and
// custom-runtime walkthrough in §17.4. These tests are NOT under a build
// tag because they exercise the repository state directly — no external
// infrastructure required.
//
// Proposal 0016 activated pod placement in Embedded Mode and seeded the
// pod-deployable echo runtime; proposal 0017 re-architected the gateway and
// controllers into in-cluster pods rendered from the chart. This file is
// the regression guard for the resulting §17.4 text: the echo-seed passage,
// the in-cluster substrate-failure behavior (0017 S1), the echo-runtime
// quickstart, and the custom-runtime walkthrough that materializes a custom
// runtime's CRD set through `lenny runtime apply` (0017 S5/C2). A heading
// rename, a re-introduced "(cold-start on first use)" clause, a
// re-introduced in-process-executor fallback, or a reverted walkthrough
// command fails here before it ships.

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// echoPlacementCrossRefs is the set of intra-spec anchors proposal 0016
// C1 adds or relies on in the §17.4 "Reference runtimes pre-installed"
// appended passage: the §15.4.4 echo conformance exemplar and the §4.7
// runtime-adapter boundary. The proposal §3 applier note requires both
// slugs to resolve before any code lands. A heading rename that orphans
// either breaks the published cross-reference.
//
// spec: §17.4 (Embedded Mode seed), §15.4.4 (echo conformance exemplar),
// §4.7 (runtime adapter). Proposal 0016 §3 C1.
var echoPlacementCrossRefs = []specCrossRef{
	{targetFile: "15_external-api-surface.md", anchor: "1544-sample-echo-runtime", addedBy: "0016 §3 C1 (echo seed reference)"},
	{targetFile: "04_system-components.md", anchor: "47-runtime-adapter", addedBy: "0016 §3 C1 (§4.7 boundary reference)"},
}

// diagnosis: a 0016 C1 cross-reference points at a spec heading that no
// longer exists. A reader following the published link from the §17.4
// echo-seed passage gets a 404 to the section anchor; the Embedded-Mode
// echo-placement docs and the spec have drifted out of sync. Re-point the
// link or restore the heading.
//
// spec: §17.4, §15.4.4, §4.7. Proposal 0016 §3 C1 (anchor-resolution gate).
func TestEchoPlacementCrossRefsResolveToLiveHeadings(t *testing.T) {
	root := repoRoot(t)
	specDir := filepath.Join(root, "spec")

	for _, ref := range echoPlacementCrossRefs {
		path := filepath.Join(specDir, ref.targetFile)
		slugs, err := headingSlugs(path)
		if err != nil {
			t.Fatalf("read heading slugs from %s: %v", ref.targetFile, err)
		}
		if !slugs[ref.anchor] {
			t.Errorf("0016 C1 cross-reference #%s (added by %s) does not resolve to any heading in spec/%s",
				ref.anchor, ref.addedBy, ref.targetFile)
		}
	}
}

// diagnosis: the §17.4 "Reference runtimes pre-installed" auto-seed
// reconciliation regressed. The paragraph must state that `lenny up` seeds
// the built-in echo runtime with a runnable digest, an applied Runtime CRD
// carrying `deploymentModel: embedded`, and a single-pod warm pool,
// referencing §15.4.4 and §4.7, and must no longer carry the "(cold-start
// on first use)" clause the embedded stack cannot perform (it wires no
// DemandSource, so the §5.2 on-demand cold-start path never runs).
// Proposal 0017 S1 re-architects the gateway into an in-cluster pod, so the
// passage must also state the reconciled substrate-failure behavior: there
// is no host gateway process to fall back to, so an unavailable substrate
// makes `lenny up` report the substrate failure and the gateway does not
// start, rather than degrading to an in-process echo executor. A
// re-introduced cold-start clause, a re-introduced in-process-executor
// fallback, or a dropped echo-seed passage leaves §17.4 self-contradictory
// under the in-cluster topology.
//
// spec: §17.4 (Embedded Mode seed, in-cluster topology), §15.4.4 (echo
// exemplar), §4.7 (runtime adapter), §5.2 (warm pool). Proposal 0016 §3 C1,
// proposal 0017 S1.
func TestEmbeddedEchoSeedPassagePresentAndColdStartReconciled(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "spec", "17_deployment-topology.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read 17_deployment-topology.md: %v", err)
	}
	content := string(b)

	// The reconciled "(cold-start on first use)" clause must be gone: the
	// embedded stack wires no DemandSource, so a §26 record does not
	// cold-start a pod on first use.
	if strings.Contains(content, "cold-start on first use") {
		t.Errorf("§17.4 still carries the reconciled \"(cold-start on first use)\" clause; proposal 0016 C1 removes it because the embedded stack performs no on-demand cold start")
	}

	// Proposal 0017 S1 removes the 0016 "degrades to the in-process echo
	// executor" fallback clause, which is incoherent once the gateway runs as
	// an in-cluster pod with no host process to fall back to. Its
	// re-introduction is a regression.
	if strings.Contains(content, "degrades to the in-process echo executor") {
		t.Errorf("§17.4 still carries the \"degrades to the in-process echo executor\" clause; proposal 0017 S1 removes it because the in-cluster gateway has no host process to fall back to")
	}

	// The appended echo-seed passage must state the three artifacts echo
	// arrives with and reference the §15.4.4 exemplar and the §4.7 boundary,
	// and state the reconciled S1 substrate-failure behavior: an unavailable
	// substrate makes `lenny up` report the failure and the gateway does not
	// start.
	for _, want := range []string{
		"seeds the built-in echo runtime",
		"[Section 15.4.4](15_external-api-surface.md#1544-sample-echo-runtime)",
		"`deploymentModel: embedded`",
		"single-pod warm pool",
		"[Section 4.7](04_system-components.md#47-runtime-adapter)",
		"report the substrate failure and the gateway does not start",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("§17.4 echo-seed passage missing %q (proposal 0017 S1 regression)", want)
		}
	}
}

// diagnosis: the §17.4 Embedded Mode quickstart code block from proposal
// 0016 C1 regressed. The `< 60s` example must invoke `--runtime=echo`
// (the auto-seeded runnable runtime), not `--runtime=chat` (a
// placeholder-pinned record that returns a session-creation failure under
// active placement). A reverted quickstart block presents a session that
// does not start as the working zero-config flow.
//
// spec: §17.4 (Embedded Mode quickstart). Proposal 0016 §3 C1.
func TestEmbeddedQuickstartUsesEchoRuntime(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "spec", "17_deployment-topology.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read 17_deployment-topology.md: %v", err)
	}
	block := embeddedQuickstartBlock(string(b))
	if block == "" {
		t.Fatal("could not locate the Embedded Mode quickstart code block (the `lenny up` / `lenny session new` / `lenny down` fence) in §17.4")
	}
	if !strings.Contains(block, "lenny session new --runtime=echo") {
		t.Errorf("§17.4 quickstart block does not invoke `--runtime=echo`; proposal 0016 C1 repoints the `< 60s` example at the auto-seeded runnable echo runtime")
	}
	if strings.Contains(block, "lenny session new --runtime=chat") {
		t.Errorf("§17.4 quickstart block still invokes `--runtime=chat`, a placeholder-pinned record that returns a session-creation failure under active placement; proposal 0016 C1 repoints it to echo")
	}
}

// diagnosis: the §17.4 custom-runtime walkthrough regressed. The
// walkthrough must materialize a Runtime CRD instance and a warm pool for
// the custom runtime before the closing curl, because `lenny-ctl runtime
// register` creates only a gateway registry record: the Sandbox controller
// resolves the runtime from a Runtime CRD by name and registration creates
// no SandboxWarmPool. Proposal 0017 S5 replaces 0016's two non-existent
// commands (`lenny kubectl apply -f runtime-crd.yaml` and `lenny-ctl pool
// create --runtime my-agent`) with the single `lenny runtime apply` verb
// (C2/S16) that applies the Runtime/SandboxTemplate/SandboxWarmPool CRD set
// to the embedded kubeconfig, so the no-Postgres dev profile materializes
// the pool without a PoolScalingController. The verb must precede the
// closing session curl so the curl can start a session. A walkthrough that
// drops the materialization step, or runs it after the curl, ends in a
// session-creation failure under active placement.
//
// spec: §17.4 (custom-runtime walkthrough verb), §4.7 (runtime adapter),
// §5.2 (warm pool). Proposal 0016 §3 C1, proposal 0017 S5/C2.
func TestCustomRuntimeWalkthroughAppliesCRDAndPoolBeforeCurl(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "spec", "17_deployment-topology.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read 17_deployment-topology.md: %v", err)
	}
	content := string(b)

	// Locate the walkthrough region between the Embedded-Mode primary-path
	// fence header and the Source Mode override header so the asserts do
	// not match unrelated text.
	const start = "**Embedded Mode (`lenny up`) — register against the embedded gateway (primary path):**"
	const end = "**Source Mode (`make run`) — override the agent binary path"
	si := strings.Index(content, start)
	ei := strings.Index(content, end)
	if si < 0 || ei < 0 || ei <= si {
		t.Fatalf("could not bound the §17.4 custom-runtime walkthrough (start=%d end=%d)", si, ei)
	}
	walkthrough := content[si:ei]

	// The `lenny runtime apply` verb (proposal 0017 C2/S16) applies the
	// runtime's Runtime/SandboxTemplate/SandboxWarmPool CRD set to the
	// embedded kubeconfig. It must precede the closing session curl so the
	// Sandbox controller can resolve the runtime by name and the
	// WarmPoolController can warm a pod before the session starts.
	applyIdx := strings.Index(walkthrough, "lenny runtime apply")
	curlIdx := strings.Index(walkthrough, "POST https://localhost:8443/v1/sessions")

	if applyIdx < 0 {
		t.Error("§17.4 walkthrough does not invoke `lenny runtime apply`; proposal 0017 S5/C2 adds the verb that applies the Runtime/SandboxTemplate/SandboxWarmPool CRD set so the Sandbox controller resolves the runtime by name and a pool materializes without a PoolScalingController")
	}
	if curlIdx < 0 {
		t.Fatal("§17.4 walkthrough has no closing session curl to order the materialization step against")
	}
	if applyIdx >= 0 && applyIdx > curlIdx {
		t.Error("§17.4 walkthrough invokes `lenny runtime apply` after the closing curl; the apply must precede the curl so the session can start on a warm pod")
	}
	// The 0016 walkthrough's two non-existent commands must be gone: S5
	// replaces them with the single `lenny runtime apply` verb.
	for _, gone := range []string{
		"lenny kubectl apply -f runtime-crd.yaml",
		"lenny-ctl pool create --runtime my-agent",
	} {
		if strings.Contains(walkthrough, gone) {
			t.Errorf("§17.4 walkthrough still invokes %q; proposal 0017 S5 replaces it with the `lenny runtime apply` verb", gone)
		}
	}
}

// diagnosis: the §26 catalog zero-config reconciliations from proposal
// 0016 C1 regressed. The §26.1 day-one-utility purpose, the §26.1 Tenant
// access paragraph, and the §26.7 chat use-case sentence must each name
// echo as the credential-free runtime `lenny up` runs out of the box and
// state that a placeholder-pinned reference-runtime session requires a
// runnable digest, an applied Runtime CRD instance, and a warm pool before
// it starts. A reverted §26 site presents a placeholder-pinned session as
// the working zero-config flow, contradicting the rewritten §17.4 passage.
//
// spec: §26.1 (day-one utility, tenant access), §26.7 (chat use case),
// §17.4 (Embedded Mode seed). Proposal 0016 §3 C1.
func TestReferenceCatalogZeroConfigReconciled(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "spec", "26_reference-runtime-catalog.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read 26_reference-runtime-catalog.md: %v", err)
	}
	content := string(b)

	for _, want := range []string{
		// §26.1 day-one utility: scoped to a published/production install,
		// names echo as the out-of-the-box runtime.
		"the credential-free `echo` runtime is the one `lenny up` runs on a pod",
		// §26.1 day-one utility: reference runtimes register but require the
		// three artifacts.
		"require a runnable image digest, an applied Runtime CRD instance, and a warm pool before a session starts",
		// §26.1 Tenant access: auto-grant supplies tenant access only.
		"The auto-grant supplies tenant access only",
		// §26.7 chat use case: chat starts only once the three artifacts exist.
		"a runnable image digest is registered on the record, a Runtime CRD instance is applied for it, and a warm pool exists for it",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("§26 catalog missing zero-config reconciliation text %q (proposal 0016 C1 regression)", want)
		}
	}
}

// embeddedQuickstartBlock returns the first fenced code block in §17.4
// that contains the `lenny up` quickstart sequence, or "" if absent. The
// block is bounded by the Embedded Mode subsection header so it does not
// match the Source or Compose mode fences.
func embeddedQuickstartBlock(content string) string {
	const header = "#### Embedded Mode: `lenny up` — Single-binary embedded stack"
	hi := strings.Index(content, header)
	if hi < 0 {
		return ""
	}
	rest := content[hi:]
	open := strings.Index(rest, "```")
	if open < 0 {
		return ""
	}
	body := rest[open+len("```"):]
	closeIdx := strings.Index(body, "```")
	if closeIdx < 0 {
		return ""
	}
	return body[:closeIdx]
}
