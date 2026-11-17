# Migration spike

Status: 2026-11-16. This spike tested option C of [../spike/WORKFLOW.md](../spike/WORKFLOW.md), "Migration order", on one area: write the area's specification pages at the target state, then migrate the code that implements them. The area is what the gateway pod claimer implements. Two agents ran in sequence on the throwaway branch `spike/spec-code-migration`, which is not merged. Nothing under `spec/` changed.

| Half | Result | Cost |
| :-- | :-- | :-- |
| Specification | 11 pages, 77 requirements, a paragraph ledger, successor stubs, and a reference tool prototype. [FINDINGS-spec.md](FINDINGS-spec.md) | About 24 minutes and 474,000 agent tokens |
| Code | The pod claimer re-cited under the draft annotation rule, one design removal, the claim-set delta, a package move, a gateway layout sketch, and 8 issues. [FINDINGS-code.md](FINDINGS-code.md) | About 52 minutes and 463,000 agent tokens |

## Contents

- [spec-next/](spec-next/): the migrated pages, `MIGRATION-LEDGER.md`, `legacy-stubs.md`, `conventions.md`, and `tools/specref.py`, copied from the branch at `4e291f48a`.
- [issues/](issues/): the issues the code half filed, in the format of [../spike/draft-records/issues/README.md](../spike/draft-records/issues/README.md).
- [annotation-fates.tsv](annotation-fates.tsv): the fate of each specification citation in the pod claimer.

The Go changes live only on the branch. These files are records of a throwaway run and carry no specification citation.
