# Steps: 0125 Decision records

## Validation owed

| Item | Why it matters | Status |
| :-- | :-- | :-- |
| Build the documentation site with `docs/adr/` removed and confirm that the navigation renders and that no page links into it. | The site's navigation reads `nav_order` and `parent` from the deleted files. | Open, for REC-B. |
| Confirm that `lenny-test --tier static` passes with the stage removed and that no other harness path calls the script. | `cmd/lenny-test/cmd_run.go` is the one caller found **[source]**. | Open, for REC-B. |

## Steps

### 0125.S1 Specification changes
- status: Planned
- aliases: [REC-A, REC-1 (part)]
- lanes: [specification]

The specification changes. PLC-A of [`0137-pod-lifecycle-controller`](../0137-pod-lifecycle-controller/change.md) edits the same §4.6.1 paragraph, §19 row 14, and §23.2 paragraph in other phrases, so either lands first. The rows under "Specification changes" state what each section reads under either order.

### 0125.S2 Deletions, renames, comments, tests, registers, and documentation
- status: Planned
- aliases: [REC-B, REC-1 (part)]
- lanes: [code, documentation]
- depends-on: [0125.S1]

The deletions, the renames, the comments, the tests, the registers, and the documentation. It lands after 0125.S1 so every `// spec:` citation and every document points to the new text.
