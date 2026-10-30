// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency checks for proposal 0027 (define
// at-least-once semantics for external side effects across an unplanned
// pod restore). Proposal 0027 added the §7.3 "External side effects
// across recovery" subsection stating the at-least-once guarantee, a
// §4.4 checkpoint-freshness-SLO note that the SLO does not bound
// external side effects, and the §8.10 "Duplicate spawn across parent
// recovery" paragraph documenting the `delegate_task` residual. These
// tests pin that trio so a later spec edit cannot silently drop the
// guarantee, orphan one of its cross-references, or let §4.4 / §8.10
// drift out of agreement with §7.3. They also guard the boundary against
// proposal 0026's §10.1 rewrite: the §7.3 scope sentence must confine the
// guarantee to the pod-restore path and must not assert any at-most /
// at-least-once guarantee on the graceful-drain / coordinator-handoff
// path, which 0026 states performs no gateway-side resume deduplication.
//
// The tests read the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
// They reuse headingSlugs/slugify from embedded_mode_anchors_test.go,
// repoRoot from docs_test.go, and specSection/requireAllContain/
// requireNoneContain from budget_extension_trigger_consistency_test.go.
//
// spec: 7.3 (external side effects across recovery), 4.4 (checkpoint
// freshness slo), 8.10 (duplicate spawn across parent recovery), 11.5
// (idempotency).

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// externalEffectCrossRef names one intra-spec link the §7.3 "External
// side effects across recovery" subsection depends on. Proposal 0027's
// S1 adds these cross-references; the §5 testing gate requires each to
// resolve to a live heading, so a heading rename that orphans one breaks
// the published cross-reference and fails here before it ships.
type externalEffectCrossRef struct {
	targetFile string // spec file the anchor lives in
	anchor     string // fragment slug, without the leading '#'
	role       string // what the §7.3 (or its siblings) link references
}

// externalEffectRecoveryCrossRefs is the set of intra-spec anchors the
// §7.3 subsection and its §4.4 / §8.10 siblings point at: §4.4 (event /
// checkpoint store), §8.10 (delegation-tree recovery), §10.1 (horizontal
// scaling, the handoff-path contrast), and §11.5 (idempotency). Each
// must resolve to a real heading in its file.
//
// spec: 7.3 (external side effects across recovery), 4.4 (checkpoint
// freshness slo), 8.10 (duplicate spawn across parent recovery), 11.5
// (idempotency). Proposal 0027 §S1, §S5.
var externalEffectRecoveryCrossRefs = []externalEffectCrossRef{
	{targetFile: "04_system-components.md", anchor: "44-event--checkpoint-store", role: "checkpoint freshness SLO / atomicity"},
	{targetFile: "08_recursive-delegation.md", anchor: "810-delegation-tree-recovery", role: "delegate_task duplicate-spawn residual"},
	{targetFile: "10_gateway-internals.md", anchor: "101-horizontal-scaling", role: "graceful-drain / handoff contrast"},
	{targetFile: "11_policy-and-controls.md", anchor: "115-idempotency", role: "client-facing idempotency mechanism"},
}

// diagnosis: a proposal-0027 cross-reference from the §7.3 "External side
// effects across recovery" subsection (or its §4.4 / §8.10 siblings)
// points at a spec heading that no longer exists. A reader following the
// published link gets a broken anchor, and the at-least-once guarantee's
// supporting references have drifted from the sections they cite.
// Re-point the link or restore the heading.
//
// spec: 7.3 (external side effects across recovery), 4.4 (checkpoint
// freshness slo), 8.10 (duplicate spawn across parent recovery), 11.5
// (idempotency). Proposal 0027 §S1, §S5.
func TestExternalSideEffectRecoveryCrossRefsResolve(t *testing.T) {
	root := repoRoot(t)
	specDir := filepath.Join(root, "spec")

	for _, ref := range externalEffectRecoveryCrossRefs {
		path := filepath.Join(specDir, ref.targetFile)
		slugs, err := headingSlugs(path)
		if err != nil {
			t.Fatalf("read heading slugs from %s: %v", ref.targetFile, err)
		}
		if !slugs[ref.anchor] {
			t.Errorf("proposal-0027 cross-reference #%s (%s) does not resolve to any heading in spec/%s",
				ref.anchor, ref.role, ref.targetFile)
		}
	}
}

// diagnosis: the §7.3 "External side effects across recovery" subsection
// or its at-least-once guarantee sentence went missing. Proposal 0027's
// S1 defines this guarantee; without it the spec is silent on the
// pod-restore path (the F-defect the proposal closed) and the §4.4 /
// §8.10 cross-references below dangle. A failure means the subsection was
// removed, renamed, or the guarantee sentence was reworded away from
// "at-least-once".
//
// spec: 7.3 (external side effects across recovery). Proposal 0027 §S1.
func TestSpec73ExternalSideEffectGuaranteePresent(t *testing.T) {
	root := repoRoot(t)
	s73 := specSection(t, filepath.Join(root, "spec", "07_session-lifecycle.md"), "### 7.3 ")

	requireAllContain(t, "§7.3 external side effects across recovery", s73, []string{
		// The subsection lead and its cross-references.
		"**External side effects across recovery.**",
		"04_system-components.md#44-event--checkpoint-store",
		"08_recursive-delegation.md#810-delegation-tree-recovery",
		"11_policy-and-controls.md#115-idempotency",
		"10_gateway-internals.md#101-horizontal-scaling",
		// The guarantee itself.
		"guarantees **at-least-once** semantics for external side effects across an unplanned pod restore",
		"Exactly-once is not achievable on this path",
		// The mechanism the guarantee names.
		"`lenny/delegate_task` spawn",
	})
}

// diagnosis: the §4.4 checkpoint-freshness-SLO note and the §8.10
// "Duplicate spawn across parent recovery" paragraph no longer agree
// with the §7.3 at-least-once guarantee. Proposal 0027 wrote both to name
// the §7.3 guarantee and to describe the same replay-window residual. A
// failure means §4.4 dropped its "does not bound external side effects"
// note or §8.10 stopped citing the §7.3 guarantee, so the three sections
// contradict each other about whether an external effect can re-fire
// across recovery.
//
// spec: 4.4 (checkpoint freshness slo), 7.3 (external side effects across
// recovery), 8.10 (duplicate spawn across parent recovery), 11.5
// (idempotency). Proposal 0027 §S1, §S5.
func TestSpec44And810AgreeWith73Guarantee(t *testing.T) {
	root := repoRoot(t)
	specDir := filepath.Join(root, "spec")

	// §4.4: the freshness-SLO note must name the §7.3 guarantee, state
	// that the SLO does not bound external side effects, and cite §7.3.
	s44 := specSection(t, filepath.Join(specDir, "04_system-components.md"), "### 4.4 ")
	requireAllContain(t, "§4.4 checkpoint-freshness-SLO note", s44, []string{
		"This SLO bounds only recoverable workspace state",
		"It does not bound external, irreversible side effects",
		"at-least-once guarantee defined in",
		"07_session-lifecycle.md#73-retry-and-resume",
		"External side effects across recovery",
	})

	// §8.10: the "Duplicate spawn across parent recovery" paragraph must
	// name the §7.3 guarantee, cite §7.3, and describe the duplicate
	// subtree as an unmitigated residual (agreeing with, not contradicting,
	// §7.3). It must also state that the §11.5 mechanism does not suppress
	// it on the recovery path.
	s810 := specSection(t, filepath.Join(specDir, "08_recursive-delegation.md"), "### 8.10 ")
	requireAllContain(t, "§8.10 duplicate spawn across parent recovery", s810, []string{
		"**Duplicate spawn across parent recovery.**",
		"at-least-once-across-recovery guarantee in",
		"07_session-lifecycle.md#73-retry-and-resume",
		"unmitigated residual of the at-least-once guarantee on the recovery path",
		"11_policy-and-controls.md#115-idempotency",
		"does not run the §11.5 hook",
	})
}

// diagnosis: the §7.3 scope sentence stopped confining the at-least-once
// guarantee to the unplanned pod-restore path, or it began asserting a
// tool-call guarantee on the graceful-drain / coordinator-handoff path.
// Proposal 0026 removed the §10.1 at-most-once handoff claim and states
// that no gateway-side resume deduplication runs on handoff; proposal
// 0027's §7.3 scope sentence must stay consistent with that by excluding
// the drain / handoff path. A failure means §7.3 over-extends its
// guarantee onto the handoff path 0026 governs, reintroducing the
// over-extension defect 0027 was written to prevent.
//
// spec: 7.3 (external side effects across recovery), 10.1 (horizontal
// scaling, handoff path). Proposal 0027 §S1; proposal 0026 §10.1.
func TestSpec73GuaranteeScopedAwayFromHandoffPath(t *testing.T) {
	root := repoRoot(t)
	s73 := specSection(t, filepath.Join(root, "spec", "07_session-lifecycle.md"), "### 7.3 ")

	// The scope sentence must confine the guarantee to the pod-restore
	// path and exclude the drain / handoff path, citing §10.1.
	requireAllContain(t, "§7.3 scope sentence", s73, []string{
		"specific to the unplanned pod-restore path",
		"does not apply to a graceful drain or coordinator handoff",
		"re-adopts the still-running pod without a checkpoint restore and replays nothing",
		"10_gateway-internals.md#101-horizontal-scaling",
	})

	// The §7.3 subsection must not assert an at-most-once (or exactly-once)
	// tool-call guarantee on the drain / handoff path. It states the
	// at-least-once pod-restore guarantee and denies exactly-once; it must
	// not resurrect the at-most-once claim proposal 0026 removed from §10.1,
	// nor promise exactly-once anywhere.
	requireNoneContain(t, "§7.3 external side effects across recovery", s73, []string{
		"at-most-once",
		"exactly-once tool-call",
		"exactly-once semantics",
		"exactly-once guarantee",
	})

	// The §10.1 handoff path must itself state that no gateway-side resume
	// deduplication runs on handoff (proposal 0026), the invariant §7.3's
	// scope sentence relies on when it says the handoff path replays
	// nothing. This pins the boundary from the §10.1 side so a revert there
	// surfaces here too.
	s101 := specSection(t, filepath.Join(root, "spec", "10_gateway-internals.md"), "### 10.1 ")
	requireAllContain(t, "§10.1 handoff path", s101, []string{
		"No gateway-side resume deduplication on handoff",
		"re-adopts the still-running pod without a checkpoint restore",
	})
	if !strings.Contains(s101, "at-least-once") {
		t.Errorf("§10.1 no longer names the pod-restore at-least-once guarantee it defers to §7.3; the boundary reconciliation with proposal 0027 regressed")
	}
}
