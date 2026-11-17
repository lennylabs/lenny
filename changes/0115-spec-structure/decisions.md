# Decisions: 0115 Specification structure

## Open decisions

None.

## Decisions

Settled by the owner on 2026-11-16 during the specification structure spike, recorded in [TAXONOMY.md](evidence/spike/TAXONOMY.md) ("Owner decisions" and "Items from the migration spike") and [WORKFLOW.md](evidence/spike/WORKFLOW.md) ("Owner decisions"):

1. The page types, placement procedure, owners of enumerable facts, identifiers, references and moves, gloss rule, page template, and gateway seams of TAXONOMY version 2.
2. The capability areas of TAXONOMY's owner decision 1, as a starting point that will move.
3. The nine items from the migration spike, including generic contribution blocks and operation identifiers in each interface's own operation names.
4. Migration by area at the target state (option C), with the structure landing first.
5. New code cites the specification far less than the current code, under a project rule, and documentation is part of every change, with a gate.

Settled by the owner on 2026-11-16 ("go with all your recommendations"), on the questions framed in revision 2, which `review-history.md` keeps:

6. **The new tree lives inside `spec/`**, beside the numbered files, which turn into successor stubs as areas migrate.
7. **The frozen reference is a git tag**, `spec-frozen`, on the commit before the first area migrates, read through `specshift ref lookup`. No copy of the numbered files stays in the tree.
8. **Every page of the map is created as a placeholder in this change**, with its code registered.
9. **§18 and §21 retire in this change** with the root plan files, and §23 moves to `docs/about/`. The area changes distribute §19, §20, and §22 as they reach each item.
10. **§26 moves in the runtimes area change**, beside the runtime protocol and its conformance suite.
11. **Documentation front matter is added per area.** Each documentation gate turns on for a page once the area it describes has landed.

