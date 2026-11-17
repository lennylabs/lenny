# Decisions: 0115 Specification structure

## Open decisions

1. **Where the new tree lives during the migration.** Options: (a) inside `spec/`, beside the numbered files, which turn into successor stubs as areas migrate, so the gates, the citation tools, and agents see one tree; (b) under `spec-next/`, swapped in for `spec/` when the last area lands. Recommendation: (a), because links never need rewriting at a swap and every area change edits one tree.
2. **Form of the frozen reference.** Options: (a) a git tag at the commit before the first area migrates, read through `specshift ref lookup §N.M`, with the successor stubs in place; (b) a copy of the numbered files under `spec/frozen/`. Recommendation: (a), because a second copy of 493,000 words in the tree doubles what every search and every agent reads, and the stubs already keep each `§` citation resolving.
3. **Placeholders for every page now.** Options: (a) create all 277 pages of the map as placeholders in this change, with their codes registered, so area changes and cross-area links target real codes from the start; (b) create each area's pages when it migrates. Recommendation: (a), because codes are then fixed once and a link across areas never waits on the other area.
4. **The plan-like numbered sections.** §18 (build sequence) and §21 (planned post-v1) are plans, which TAXONOMY sends to programs and the status page. §19 (resolved decisions), §20 (open questions), §22 (explicit non-decisions), and §23 (competitive landscape) go to rationale on owner pages, issues, and `docs/about/`. Options: (a) this change retires §18 and §21 with the root plan files and moves §23 to `docs/about/`, and the area changes distribute §19, §20, and §22 as they reach each item; (b) all of them leave in one later clean-up change. Recommendation: (a), because §18 and §21 are the same plans as the root files, and §23 belongs to no area.
5. **§26, the reference runtime catalog.** The owner decided it leaves the specification for the reference runtime repositories and the runtime author guide (TAXONOMY, "Owner decisions", 4). Options: (a) in the area change for runtimes; (b) in this change. Recommendation: (a), because the runtime protocol and its conformance suite stay in that area and the move has to keep them apart.
6. **Front matter on every documentation page now.** Options: (a) add `spec:` front matter to all 228 pages in this change, mapped from the placeholder codes, and turn on the documentation gates; (b) add it per area, and turn on each gate for a page once its area has landed. Recommendation: (b), because the codes a page describes are only certain once those pages are written, and a wrong mapping now would pass the gate and mislead it.

## Decisions

Settled by the owner on 2026-11-16 during the specification structure spike, recorded in [TAXONOMY.md](evidence/spike/TAXONOMY.md) ("Owner decisions" and "Items from the migration spike") and [WORKFLOW.md](evidence/spike/WORKFLOW.md) ("Owner decisions"):

1. The page types, placement procedure, owners of enumerable facts, identifiers, references and moves, gloss rule, page template, and gateway seams of TAXONOMY version 2.
2. The capability areas of TAXONOMY's owner decision 1, as a starting point that will move.
3. The nine items from the migration spike, including generic contribution blocks and operation identifiers in each interface's own operation names.
4. Migration by area at the target state (option C), with the structure landing first.
5. New code cites the specification far less than the current code, under a project rule, and documentation is part of every change, with a gate.
