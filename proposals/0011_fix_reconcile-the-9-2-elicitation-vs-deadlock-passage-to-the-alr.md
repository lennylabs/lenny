# Proposal: Reconcile the §9.2 elicitation-vs-deadlock passage to the already-decided external-actor semantics and reopen F-9.2.20

- **Status:** Approved (2026-06-19). Verified and converged after 4 adversarial review rounds (1 finding fixed); awaiting implementation via implement-proposal. Open decision (marker register for the reworded line 110) resolved in favor of applying §3.1 as staged, keeping the intra-section back-reference to the Elicitation Timeout Semantics block.
- **Date:** 2026-06-19.
- **Scope:** Reconciles the §9.2 "Interaction with `input_required` deadlock detection" passage (`spec/09_mcp-integration.md:110`) to the external-actor semantics the same line already decides, and reopens BUILD-GAPS finding F-9.2.20. The finding has been re-deferred five times on the premise that the spec "leaves whether such a subtree should ever be failed undefined" and that a spec change is "required" before the §8.8 detector can be wired to elicitation chains. That premise is false: line 110 already states that "A task blocked on an elicitation waiting for a human response is **not** considered deadlocked — it is waiting on an external actor," and the §8.8 subtree deadlock detector already realizes exactly that behavior with no elicitation field on its node type. The remedy is determined, no open spec decision exists, and no behavioral code change is needed. The defects are the surface tension in the line-110 wording (it opens with "both participate in the gateway's subtree deadlock detection" and then says an elicitation-blocked task is "not considered deadlocked"), a cross-reference to §8.9 where the detector lives in §8.8, and the finding's deferral rationale. The change is a one-sentence spec reword, a code comment, one tier-1 regression test, and a BUILD-GAPS finding edit. It touches no v1 behavior, schema, proto, or wire contract.

This document stages the proposed spec change, code comment, test, and BUILD-GAPS edit. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 1. Problem

BUILD-GAPS finding F-9.2.20 ("Subtree deadlock detector does not interact with elicitation chains", Info) has been re-deferred five times (`BUILD-GAPS.md:11711`, `11713`, `11715`, `11717`) on a single premise: that spec §9.2 "leaves whether such a subtree should ever be failed undefined" and that a spec change is "required" before the §8.8 detector can be wired to elicitation chains. That premise is false. `spec/09_mcp-integration.md:110` already decides the question. The passage reads:

```
**Interaction with `input_required` deadlock detection:** Elicitation chains and `input_required` chains are independent blocking mechanisms, but both participate in the gateway's subtree deadlock detection ([Section 8.9](08_recursive-delegation.md#89-task-tree)). A task blocked on an elicitation waiting for a human response is **not** considered deadlocked — it is waiting on an external actor. However, a task blocked on `lenny/request_input` is waiting on its parent, which is an internal actor. If the parent is itself blocked (on its own `request_input` or on `await_children` with all children in `input_required`), the gateway's deadlock detector treats this as a circular wait.
```

The external-actor outcome is settled. An elicitation-blocked task is excluded from the `DEADLOCK_TIMEOUT` fail path (§8.8 fails only the deepest *blocked* tasks, `spec/08_recursive-delegation.md:981`) and is bounded instead by `maxElicitationWait` (default 600s, `spec/09_mcp-integration.md:103`). A `request_input`-blocked task waits on its parent, an internal actor, and can form a circular wait the detector treats as deadlock. No open spec semantic decision exists.

**The §8.8 subtree deadlock detector already implements this behavior.** Verified against current code on `impl/v1-initial`:

- `deadlock.Node` carries only `AwaitingChildIDs` and `PendingInputs`, with no elicitation field (`pkg/gateway/deadlock/deadlock.go:43-55`).
- `Detect()` classifies a node blocked only when `len(PendingInputs) > 0` (`pkg/gateway/deadlock/deadlock.go:132`) or when it awaits children all of whom are blocked (`pkg/gateway/deadlock/deadlock.go:134-150`).
- `buildSnapshot()` sources `PendingInputs` solely from the `inputwait` registry via `PendingDetailsForSession` and `AwaitingChildIDs` solely from the await tracker (`pkg/gateway/deadlock/manager.go:196-209`); it never reads the `interactionstore` pending-elicitation set, and the package never imports `interactionstore`.
- The elicitation handler records its pending interaction in `interactionstore` against the resolver session and never touches `inputwait`, `input_required`, or the await tracker (`pkg/gateway/mcptools/elicitation.go`; a grep for `inputwait`, `await`, `request_input`, and `input_required` in that file returns nothing).

So an elicitation populates neither blocked signal. An elicitation-blocked node is classified not-blocked, never becomes a subtree root or `DeepestTask`, and is never `DEADLOCK_TIMEOUT`-failed, which is exactly the line-110 external-actor outcome. A `request_input` child does make its awaiting parent a deadlock candidate, which is the line-110 internal-actor contrast.

**F-9.2.20 is not a live defect and needs no behavioral code change.** The defects are twofold. First, the finding's deferral rationale misreads line 110 by treating an already-decided question as "undefined." Second, line 110 itself carries surface tension: it opens with "both participate in the gateway's subtree deadlock detection" and then says an elicitation-blocked task is "not considered deadlocked," and it cross-references §8.9 (the Task Tree section, `spec/08_recursive-delegation.md:999`) though the detector is defined in §8.8 (`spec/08_recursive-delegation.md:981`, anchor `#88-taskrecord-and-taskresult-schema`, already used by the spec's own internal reference at `spec/08_recursive-delegation.md:64`). The "participate" wording is what invited the misread.

## 2. Decisions

- **Fix the spec wording and the finding rationale rather than the code.** The detector already realizes the §9.2 line-110 semantics with no behavior change: an elicitation populates neither blocked signal, so an elicitation-blocked node is classified not-blocked, and a `request_input` child makes its awaiting parent a deadlock candidate. This is verified against current code (`pkg/gateway/deadlock/deadlock.go:43-55`, `131-151`; `pkg/gateway/deadlock/manager.go:196-209`; `pkg/gateway/mcptools/elicitation.go` imports only `interactionstore`). The spec sentence and the deferral rationale are the defects.
- **Reword line 110 to remove the surface tension while preserving the meaning.** The clause "both participate in the gateway's subtree deadlock detection" reads as if elicitation-blocked tasks are deadlock participants, which the next sentence contradicts. The reword states that the detector recognizes an elicitation-blocked task as an external-actor wait that does not make it a deadlock participant and is never `DEADLOCK_TIMEOUT`-failed, while a `request_input` wait is an internal-actor wait that can form a circular wait. No semantic content changes.
- **Fix the cross-reference in the same passage from §8.9 to §8.8.** The detector is defined at `spec/08_recursive-delegation.md:981` under the §8.8 heading (`spec/08_recursive-delegation.md:804`); §8.9 "Task Tree" begins at `spec/08_recursive-delegation.md:999` and describes the task hierarchy rather than the detector. The §8.8 anchor slug is already in use by the spec's own internal reference at `spec/08_recursive-delegation.md:64`, so it is the correct, live target. This correction is one token within the same one-sentence edit and folds into the reword as change C1; it is not carried as a separate change item.
- **Reject the alternative floated in the finding's own resolution.** `BUILD-GAPS.md:11711` item (a) proposes enumerating pending elicitations as a second blocked signal in `deadlock.Node`. Setting `res = true` for an elicitation-blocked node in `Detect()` would classify it as a deadlock contributor eligible to become a subtree root or `DeepestTask`, the opposite of line 110's "not considered deadlocked." The current not-blocked classification is the spec-faithful way the detector recognizes the elicitation wait and excludes it from the fail path.
- **Add a `// spec:` comment at the `Detect()` blocked-classification site** documenting that elicitation waits are deliberately absent from the blocked signal, with no behavior change. The existing comment on the `Node` type (`pkg/gateway/deadlock/deadlock.go:42`) cites §8.8 line 981 but does not state the elicitation exclusion; the switch at `pkg/gateway/deadlock/deadlock.go:131-151` is the load-bearing site where a future editor might be tempted to add an elicitation case.
- **Pin the contract with a tier-1 regression test** in `pkg/gateway/deadlock` that binds the §9.2 line-110 contrast to a test through the `// spec:` annotation. The test reuses the existing `node()` and `snap()` helpers and asserts the elicitation half (an elicitation-blocked leaf under an awaiting parent yields zero deadlocked subtrees) distinctly from the existing §8.8 cases. The `request_input` internal-actor half is already pinned by `TestDetectSimpleDeadlock_spec_8_8_981`; the regression test maps §9.2 to it by reference rather than reimplementing it.
- **Treat the mixed-blocking case as a spec-sanctioned §8.8 case-(b) false negative.** When a parent awaits one `request_input`-blocked child and one elicitation-blocked child, the obstructing element is the elicitation child (classified not-blocked, non-terminal), bounded by `maxElicitationWait` (`spec/09_mcp-integration.md:103`), while the `request_input` child is bounded by `maxRequestInputWaitSeconds` (`spec/11_policy-and-controls.md`, §11.3). This is the §8.8 case-(b) false negative documented at `spec/08_recursive-delegation.md:981`. The proposal does not attempt to detect this case; doing so is the rejected second-signal alternative.

## 3. Proposed changes

### 3.1 (C1) Spec change: `spec/09_mcp-integration.md` §9.2 "Interaction with `input_required` deadlock detection" (line 110)

Anchor on the paragraph beginning **"Interaction with `input_required` deadlock detection:"** in §9.2 (Elicitation Chain). The current text is:

```
**Interaction with `input_required` deadlock detection:** Elicitation chains and `input_required` chains are independent blocking mechanisms, but both participate in the gateway's subtree deadlock detection ([Section 8.9](08_recursive-delegation.md#89-task-tree)). A task blocked on an elicitation waiting for a human response is **not** considered deadlocked — it is waiting on an external actor. However, a task blocked on `lenny/request_input` is waiting on its parent, which is an internal actor. If the parent is itself blocked (on its own `request_input` or on `await_children` with all children in `input_required`), the gateway's deadlock detector treats this as a circular wait.
```

Replace it with the following. The opening clause is reworded so it no longer implies an elicitation-blocked task is a deadlock participant, the cross-reference is retargeted from §8.9 (`#89-task-tree`) to §8.8 (`#88-taskrecord-and-taskresult-schema`), and the two existing sentences that draw the external-actor and internal-actor contrast are preserved verbatim:

```
**Interaction with `input_required` deadlock detection:** Elicitation chains and `input_required` chains are independent blocking mechanisms that the gateway's subtree deadlock detector ([Section 8.8](08_recursive-delegation.md#88-taskrecord-and-taskresult-schema)) recognizes and treats differently. A task blocked on an elicitation waiting for a human response is **not** considered deadlocked — it is waiting on an external actor. Such a task is never failed with `DEADLOCK_TIMEOUT`; its wait is bounded instead by the elicitation timeout (`maxElicitationWait`, see the Elicitation Timeout Semantics above). However, a task blocked on `lenny/request_input` is waiting on its parent, which is an internal actor. If the parent is itself blocked (on its own `request_input` or on `await_children` with all children in `input_required`), the gateway's deadlock detector treats this as a circular wait.
```

Notes for the applier:

- The cross-reference correction (`#89-task-tree` → `#88-taskrecord-and-taskresult-schema`) is part of this one-sentence edit, matching the detector's canonical anchor already used at `spec/08_recursive-delegation.md:64`. Do not carry it as a separate change item.
- Confirm the §8.8 anchor slug `#88-taskrecord-and-taskresult-schema` against the heading `### 8.8 TaskRecord and TaskResult Schema` at `spec/08_recursive-delegation.md:804` before applying, because heading text can shift.
- The `maxElicitationWait` bound is defined in the "Elicitation Timeout Semantics" block earlier in the same §9.2 section (`spec/09_mcp-integration.md:103`). The reword uses an intra-section back-reference ("see the Elicitation Timeout Semantics above") rather than a cross-document anchor, because the definition is in the same section as line 110. Confirm the back-reference still resolves after the edit.
- Preserve the load-bearing words "not considered deadlocked", "external actor", "internal actor", and "circular wait". The edit changes the opening clause and the cross-reference target, and adds the explicit `DEADLOCK_TIMEOUT`-exclusion sentence; it changes no semantic content.
- Leave the unrelated §8.9 cross-references untouched: the `lenny/get_task_tree` row at `spec/09_mcp-integration.md:30` and the task-tree-nodes reference at `spec/08_recursive-delegation.md:831` both correctly point at the Task Tree section.
- Confirm the surrounding §9.2 paragraph still reads coherently top to bottom after the edit.

### 3.2 (C3) Code comment: `pkg/gateway/deadlock/deadlock.go` `Detect()` blocked-classification switch

Anchor on the `switch` statement inside the `blocked` closure in `Detect()` (`pkg/gateway/deadlock/deadlock.go:131-151`), the load-bearing site where the node's blocked signal is computed from `PendingInputs` and `AwaitingChildIDs`. Add a `// spec:` comment immediately above the `switch` (line 131) stating that an elicitation wait is deliberately absent from the blocked signal, so an elicitation-blocked node is classified not-blocked and is excluded from the `DEADLOCK_TIMEOUT` fail path, per the §9.2 line-110 external-actor semantics. The comment documents existing behavior and changes no code.

```go
// An elicitation wait is deliberately NOT a blocked signal here: a task
// waiting on an elicitation is waiting on an external actor (a human) and
// is "not considered deadlocked" per §9.2, so it never becomes a subtree
// root or a DeepestTask and is never DEADLOCK_TIMEOUT-failed. Its wait is
// bounded by maxElicitationWait instead. Only PendingInputs (input_required,
// an internal-actor wait on the parent) and AwaitingChildIDs are blocked
// signals; do not add an elicitation case. spec: §9.2 line 110, §8.8 line 981.
switch {
```

Notes for the applier:

- Match the file's local convention: every existing `// spec:` citation in `deadlock.go` carries a line number (`pkg/gateway/deadlock/deadlock.go:13`, `16`, `35`, `42`, `66`, `108`). Cite `§9.2 line 110` and `§8.8 line 981` as shown.
- Place the comment above the `switch` at `pkg/gateway/deadlock/deadlock.go:131`, not above the `Detect` function (whose doc-comment already cites §8.8 line 981 but does not state the elicitation exclusion).
- This is a comment-only edit. The `switch` body is unchanged.

### 3.3 (C4) Test change: `pkg/gateway/deadlock/deadlock_test.go` regression test pinning the §9.2 line-110 contrast

Add one tier-1 test function in `pkg/gateway/deadlock/deadlock_test.go`, alongside the existing `TestDetect*_spec_8_8_981` cases, that binds the §9.2 line-110 elicitation-vs-deadlock contrast to a test through its `// spec:` annotation. Reuse the existing `node()` and `snap()` helpers. Assert the elicitation half distinctly: a running leaf with no `PendingInputs` and no `AwaitingChildIDs` (the elicitation-blocked case, which populates neither blocked signal) under an awaiting parent yields zero deadlocked subtrees. Name the function for the behavior and the section and annotate it for both §9.2 and §8.8:

```go
// TestDetectElicitationBlockedExcluded_spec_9_2_110 pins the §9.2 line-110
// external-actor semantics: an elicitation-blocked task populates neither
// the inputwait nor the await blocked signal, so its awaiting parent yields
// no deadlocked subtree and the task is never DEADLOCK_TIMEOUT-failed. A
// child blocked on lenny/request_input (the internal-actor half) is pinned
// by TestDetectSimpleDeadlock_spec_8_8_981; this case covers the contrast.
// spec: 9.2 (elicitation vs deadlock), 8.8 (subtree deadlock detector)
func TestDetectElicitationBlockedExcluded_spec_9_2_110(t *testing.T) {
	// C is elicitation-blocked: running, with no pending request_input and
	// no awaited children. It populates neither blocked signal, so its
	// awaiting parent P is not deadlocked.
	got := deadlock.Detect(snap(
		node("P", session.StateRunning, []string{"C"}),
		node("C", session.StateRunning, nil),
	))
	if len(got) != 0 {
		t.Fatalf("Detect returned %d subtrees, want 0 (elicitation-blocked child must not deadlock its parent): %+v", len(got), got)
	}
}
```

Notes for the applier:

- The elicitation-blocked leaf is modeled by a running node with no `PendingInputs` and no `AwaitingChildIDs`, because an elicitation populates neither signal (`pkg/gateway/deadlock/deadlock.go:43-55`; `pkg/gateway/deadlock/manager.go:196-209`). The construction overlaps `TestDetectRunningChildIsNotDeadlock_spec_8_8_981`; the distinct contribution is the §9.2 annotation that binds line 110 to a test, which no existing test carries.
- Do not reimplement the `request_input` internal-actor half. `TestDetectSimpleDeadlock_spec_8_8_981` (`pkg/gateway/deadlock/deadlock_test.go:48-70`) already pins it. The new test's doc-comment references it; the harness maps the internal-actor half through that existing case.
- This test pins the current not-blocked classification of an elicitation-blocked node; it is not a guard against the rejected second-signal alternative. The rejected alternative (`BUILD-GAPS.md:11711` item (a)) adds a pending-elicitation field to `deadlock.Node` and a corresponding case to the `Detect()` switch. The `node()` helper sets no such field (`pkg/gateway/deadlock/deadlock_test.go:16-28`) and node C carries no `PendingInputs`, so after that hypothetical change node C's new elicitation field would be empty, the added case would evaluate false, C would still be classified not-blocked, and this test would still pass. The anti-regression directive lives in the C3 code comment ("do not add an elicitation case", §3.2), which a future editor reads at the switch site.
- Behavior is unchanged, so tier 0 and tier 1 on `pkg/gateway/deadlock` are the only reached tiers.

### 3.4 (C5) BUILD-GAPS change: move F-9.2.20 DEFERRED → OPEN with the corrected rationale

Anchor on the F-9.2.20 finding block (`BUILD-GAPS.md:11701-11717`). Flip the heading marker from `[-]` / `DEFERRED` to `[ ]` / `OPEN`. Replace the stacked re-deferral rationale (the paragraphs at `BUILD-GAPS.md:11711`, `11713`, `11715`, `11717`) with a single corrected rationale:

```
**Re-triaged — OPEN (wording reconciliation).** The five prior re-deferrals rest on a refuted premise: that §9.2 "leaves whether such a subtree should ever be failed undefined" and that a spec change is "required" before the detector can be wired to elicitation chains. §9.2 line 110 already decides the semantics: an elicitation-blocked task is "not considered deadlocked — it is waiting on an external actor" (elicitation = external actor, never `DEADLOCK_TIMEOUT`-failed, bounded by `maxElicitationWait`), in explicit contrast to a `request_input` wait (internal actor, can form a circular wait). The §8.8 detector already realizes this: `deadlock.Node` has no elicitation field (`pkg/gateway/deadlock/deadlock.go:43-55`); `buildSnapshot` sources its blocked signal only from the `inputwait` registry and the await tracker (`pkg/gateway/deadlock/manager.go:196-209`); the package never imports `interactionstore`; the elicitation handler touches neither `inputwait` nor the await tracker (`pkg/gateway/mcptools/elicitation.go`). An elicitation-blocked node is therefore classified not-blocked and excluded from the fail path, which is the line-110 outcome. There is no open spec decision and no behavioral code gap. The real defects are the line-110 surface tension ("both participate in the gateway's subtree deadlock detection", which reads as if elicitation-blocked tasks are deadlock participants) and the cross-reference to §8.9 where the detector lives in §8.8. The 11711(a) alternative of enumerating pending elicitations as a second blocked signal is rejected: it would classify an elicitation-blocked task as a deadlock contributor eligible to become a subtree root or `DeepestTask`, the opposite of line 110. Closed by proposal 0011: the §9.2 reword and §8.9→§8.8 cross-reference fix (C1), the `Detect()` exclusion comment (C3), and the tier-1 regression test binding §9.2 line 110 (C4).
```

Notes for the applier:

- This BUILD-GAPS edit is staged by the proposal; the implementer applies it on landing.
- The finding closes once C4 lands. C4 is the closeable artifact (the §9.2-to-test mapping the finding lacks today).
- Leave the F-9.2.20 summary-table row (`BUILD-GAPS.md:11744`) intact; the table records the finding title and severity, which are unchanged.

## 4. Non-goals

- **No behavioral change to the deadlock detector.** `deadlock.Node` gains no elicitation field, `Detect()` gains no elicitation case, and `buildSnapshot()` reads no new source. The detector already produces the §9.2 line-110 outcome (`pkg/gateway/deadlock/deadlock.go:43-55`, `131-151`; `pkg/gateway/deadlock/manager.go:196-209`).
- **No second blocked signal for pending elicitations.** The alternative at `BUILD-GAPS.md:11711` item (a) is rejected outright; it would treat elicitation-blocked tasks as deadlock contributors, contradicting `spec/09_mcp-integration.md:110`.
- **No change to the elicitation timeout path.** `maxElicitationWait` (`spec/09_mcp-integration.md:103`) already bounds elicitation waits, and the elicitation handler (`pkg/gateway/mcptools/elicitation.go`) is untouched.
- **No attempt to detect the mixed-blocking case.** When a parent awaits one `request_input`-blocked child and one elicitation-blocked child, the elicitation child obstructs detection. This is the §8.8 case-(b) false negative documented at `spec/08_recursive-delegation.md:981`, bounded by `maxElicitationWait` on the obstructing elicitation child and by `maxRequestInputWaitSeconds` on the `request_input` child. The proposal does not detect it.
- **No standalone change item for the cross-reference fix.** The §8.9→§8.8 retarget is a real, verified defect (line 110 disagrees with the spec's own §8.8 reference at `spec/08_recursive-delegation.md:64` and points readers at the task-tree data structure rather than the detector), but it edits the same one sentence as C1 and is folded into C1 rather than carried separately.
- **No comment at the `Detect()` site beyond the staged `// spec:` note.** An earlier draft proposed a broader defensive comment (the dropped change C2 below). The staged C3 comment is the minimal `// spec:` note at the switch; the broader narration was dropped because the `Detect` doc-comment already enumerates the closed blocked-signal set (`pkg/gateway/deadlock/deadlock.go:97-99`) and the `Node` type comments already scope `AwaitingChildIDs` and `PendingInputs` as the two blocked signals (`pkg/gateway/deadlock/deadlock.go:47-54`), so the absence of an elicitation case is already implied. The anti-regression directive against the rejected second-signal alternative is carried by the C3 comment itself, which instructs a future editor at the switch site to "do not add an elicitation case". C4 does not serve this purpose: it constructs the elicitation-blocked node through the `node()` helper, which sets no elicitation field, so a second-signal change that adds such a field and a switch case leaves node C's field empty and C4 still passing.
- **No from-scratch dual-half regression test.** An earlier draft staged a test reimplementing both the elicitation half and the `request_input` half. The `request_input` half is already covered by `TestDetectSimpleDeadlock_spec_8_8_981` (`pkg/gateway/deadlock/deadlock_test.go:48-70`) and the bare-running-node construction by `TestDetectRunningChildIsNotDeadlock_spec_8_8_981` (`pkg/gateway/deadlock/deadlock_test.go:75-83`). C4 reuses the existing helpers and adds only the §9.2-annotated elicitation case, mapping the internal-actor half to the existing test by reference, per the reuse-over-duplication rule.
- **No new protocol surface, RPC, frame, field, endpoint, or CRD change.** The change is a spec reword, a code comment, one tier-1 test, and a BUILD-GAPS finding edit.
- **No edit to the unrelated §8.9 cross-references** (the `get_task_tree` row at `spec/09_mcp-integration.md:30` and the task-tree-nodes reference at `spec/08_recursive-delegation.md:831`), which correctly point at the Task Tree section.
- **No reader-facing docs change.** The passage is spec-internal and uses spec section cross-references that do not appear in published docs.

## 5. Testing

- **Tier 0 (static):** confirm the edited spec renders and the §8.8 cross-reference anchor (`#88-taskrecord-and-taskresult-schema`) resolves to the live heading at `spec/08_recursive-delegation.md:804`; the spec lint and link-check stage flags a broken anchor. The `maxElicitationWait` reference is an intra-section back-reference within §9.2 and adds no new anchor. Confirm `gofumpt` and `goimports` leave the commented `deadlock.go` and the new test unchanged.
- **Tier 1 (unit):** the staged regression test `TestDetectElicitationBlockedExcluded_spec_9_2_110` runs in `pkg/gateway/deadlock` and asserts an elicitation-blocked child yields zero deadlocked subtrees. The existing `TestDetectSimpleDeadlock_spec_8_8_981` continues to pin the `request_input` internal-actor half. Run `lenny-test --changed --max-tier 1 --pkg pkg/gateway/deadlock`.
- **No tier-2-or-higher behavioral test is added.** The change is wording, a comment, and one tier-1 test, with no behavior, schema, or wire-contract change, so no envtest, contract, integration, e2e, chaos, or security tier is reached. The tier-8 scaffold `TestElicitationDeadlockDetection` (`tests/tier8_chaos/scaffolds_test.go`) remains a placeholder annotated to §12.8 and is out of scope here.

## 6. Findings closed on application

- **F-9.2.20** (Info, re-deferred five times at `BUILD-GAPS.md:11711`, `11713`, `11715`, `11717`): the deferral rests on the refuted premise that §9.2 leaves the elicitation-blocked-subtree remedy undefined. `spec/09_mcp-integration.md:110` already decides it (elicitation = external actor = not deadlocked; `request_input` = internal actor = can form a circular wait), and the §8.8 detector already implements it (`deadlock.Node` has no elicitation field; `buildSnapshot` reads only the `inputwait` registry and the await tracker; the elicitation handler touches neither). The finding is reclassified as a wording and clarity reconciliation, closed by the §9.2 reword and cross-reference fix (C1), the `Detect()` exclusion comment (C3), and the tier-1 regression test that binds §9.2 line 110 (C4). The 11711(a) second-signal alternative is rejected as contradicting line 110.

## 7. Resolved in adversarial review

The change set in §3 already reflects the adversarial review of the originating draft: change C2 (a standalone cross-reference change item) was folded into C1 because both edit the identical sentence at `spec/09_mcp-integration.md:110`; the dropped change C3 (a broader defensive comment) was removed in favor of the minimal `// spec:` note now staged as §3.2, because the existing `Detect` doc-comment and `Node` type comments already imply the elicitation exclusion and the C3 note itself carries the "do not add an elicitation case" anti-regression directive; and the C4 test was reduced from a from-scratch dual-half function to a single §9.2-annotated case reusing the existing helpers, mapping the `request_input` half to `TestDetectSimpleDeadlock_spec_8_8_981` by reference. Further adversarial review rounds are recorded below.

### Pass 1 (2026-06-19, automated)

- **C4 regression-guard claim retracted.** The earlier draft asserted in §3.3 and §4 that the staged C4 test fails when an elicitation case is added to the `Detect()` switch (the rejected second-signal alternative), and used that property to justify dropping the broader C2 comment. The claim is false: the rejected alternative adds a new field to `deadlock.Node` and a corresponding switch case (`BUILD-GAPS.md:11711` item (a)), but C4 constructs the elicitation-blocked node through the `node()` helper, which sets no such field (`pkg/gateway/deadlock/deadlock_test.go:16-28`), and node C carries no `PendingInputs`. After that hypothetical change node C's new field would be empty, the added case would evaluate false, C would still be classified not-blocked, and C4 would still pass. The §3.3 applier note now states that C4 pins the current not-blocked classification rather than guarding against the second-signal alternative, and the §4 non-goal now attributes the anti-regression directive to the C3 comment ("do not add an elicitation case"), which a future editor reads at the switch site. The originating-revision summary above is corrected to match.

## 8. Open decisions for review

- **Marker register for the reworded line 110.** The reword states the elicitation-exclusion behavior in plain prose, because the meaning is already present in line 110, and adds an intra-section back-reference to the elicitation timeout ("`maxElicitationWait`, see the Elicitation Timeout Semantics above") at the point it says the wait is "bounded instead by the elicitation timeout." The `maxElicitationWait` definition lives in the same §9.2 section (`spec/09_mcp-integration.md:103`), so a same-section back-reference is used rather than a cross-document anchor. A reviewer who prefers a tighter sentence may drop the back-reference and keep the bare term `maxElicitationWait`. Apply §3.1 as staged unless the reviewer chooses to drop it.

## 9. Files touched on application

- `spec/09_mcp-integration.md`: §9.2 "Interaction with `input_required` deadlock detection" sentence (line 110) reworded to remove the "both participate" surface tension, retarget the cross-reference from §8.9 to §8.8, and state the `DEADLOCK_TIMEOUT` exclusion for elicitation-blocked tasks explicitly (C1).
- `pkg/gateway/deadlock/deadlock.go`: a `// spec:` comment above the `Detect()` blocked-classification switch (line 131) documenting the deliberate absence of an elicitation blocked signal, with no behavior change (C3).
- `pkg/gateway/deadlock/deadlock_test.go`: a new tier-1 test `TestDetectElicitationBlockedExcluded_spec_9_2_110` binding the §9.2 line-110 elicitation-vs-deadlock contrast, reusing the existing `node()` and `snap()` helpers (C4).
- `BUILD-GAPS.md`: F-9.2.20 moved from DEFERRED to OPEN with the corrected rationale and the 11711(a) second-signal alternative rejected (C5).
- No schema, proto, chart, or docs file is touched.
