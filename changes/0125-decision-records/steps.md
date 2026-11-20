# Steps: 0125 Decision records

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| Build the documentation site with `docs/adr/` removed and confirm that the navigation renders and that no page links into it. | The site's navigation reads `nav_order` and `parent` from the deleted files. | Open, for REC-B. |
| Confirm that `lenny-test --tier static` passes with the stage removed and that no other harness path calls the script. | `cmd/lenny-test/cmd_run.go` is the one caller of `scripts/check-adr-catalog.sh` found **[source]**. | Open, for REC-B. |
| Confirm that `lenny-test gate` returns `PASS` on the head of the 0125.S2 branch. | `cmd/lenny-test/cmd_gate.go` lists `adr_test.go` in `gateDocsFiles`, and the merge gate fails on a listed file that is missing, so the deletion and the list edit have to land together. | Open, for REC-B. |
| Run `lenny-test --tier chaos` for `TestLeaderKillMidClaimNoDoubleAssignment` after the rename, or record the precondition when no cluster with two controller replicas is available. | The `chaos` tier is outside the merge gate, so a `PASS` verdict does not show that the renamed file runs, and its two `tests/registers/skip-reasons.yaml` entries key it by path. | Open, for REC-B. |

## Steps

### 0125.S1 Specification changes
- status: Planned
- aliases: [REC-A, REC-1 (part)]
- lanes: [specification]
- closes: [F-6.3.20 (part), T-6.3.20 (part)]

The specification changes. PLC-A of [`0137-pod-lifecycle-controller`](../0137-pod-lifecycle-controller/change.md) edits the same §4.6.1 paragraph and §19 row 14 in other phrases, so either lands first. The rows under "Specification changes" state what each section reads under either order. The shared §23.2 edit of revision 1 is moot, because §23 is a successor stub.

The step edits numbered sections that several areas own (§4.6.1, §6.3, §16.5, and §19), and each edit locates its target by heading, bold paragraph label, and quoted text. It lands before the first area change that migrates one of those sections; an area change that migrates a section first carries the edit onto the page that receives the text, as "Specification changes" states. 0125.S1 and 0125.S2 are small enough to share one branch and one digest.

Of F-6.3.20 and T-6.3.20, the step closes the part that names the "Phase 2 exit gate ADR": §6.3 then records the benchmark results in the evidence folder of the change that runs the benchmark. The validated measurements and the executable promotion gate stay open.

### 0125.S2 Deletions, renames, comments, tests, registers, and documentation
- status: Planned
- aliases: [REC-B, REC-1 (part)]
- lanes: [code, documentation]
- depends-on: [0125.S1]

The deletions, the renames, the comments, the tests, the registers, and the documentation. It lands after 0125.S1 so every `// spec:` citation and every document points to the new text.

The branch edits `cmd/lenny-test/cmd_run.go`, `cmd/lenny-test/cmd_gate.go`, `tests/static/`, and `tests/registers/skip-reasons.yaml`, and it removes two checks, so it is reviewed with the `review` skill over its diff before it merges (`.claude/rules/git-workflow.md`, "Review changes to the merge gate"). The step's digest records the review's outcome.
