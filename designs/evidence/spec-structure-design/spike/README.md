# Spec structure spike

Status: 2026-11-16. This spike tests a reorganization of the technical specification by capability, with components and interfaces as their own page types and every fact stated once. No design document exists yet; the spike's results feed one. Nothing here changes `spec/`.

## What ran

1. [TAXONOMY.md](TAXONOMY.md) version 0 set the page types, a placement procedure, the owners of enumerable facts, identifiers, a gloss rule, and a page template.
2. Two agents applied it in parallel:
   - The map assigned every heading of the current specification, every design, and every page under `docs/reference/` to a proposed tree of 274 pages, and wrote the root map and the folder indexes. Results: [proposed/MAP.md](proposed/MAP.md), [proposed/ASSIGNMENT.md](proposed/ASSIGNMENT.md), [proposed/README.md](proposed/README.md), and [FINDINGS-map.md](FINDINGS-map.md).
   - The slice wrote one part of the system in full, warm pools and the pod lifecycle, as it would read in the new specification: three capability pages, four component pages, seven interface pages, and the generated catalogs for those pages. Results: the pages under `proposed/` named in [FINDINGS-slice.md](FINDINGS-slice.md), which also holds the findings.
3. The two halves were reconciled (twelve link targets retargeted to the map's pages), and [TAXONOMY.md](TAXONOMY.md) became version 1.

The slice's pages follow version 0. Version 1 changes some of what they would look like, and its last table lists each change.

## Suggested reading order

1. [TAXONOMY.md](TAXONOMY.md): the rules, and the open decisions for the owner at its end.
2. [proposed/README.md](proposed/README.md) and [proposed/capabilities/index.md](proposed/capabilities/index.md): the root map and the areas.
3. One slice page of each type:
   - capability: [proposed/capabilities/runtimes-and-pools/pod-claims.md](proposed/capabilities/runtimes-and-pools/pod-claims.md)
   - component: [proposed/components/warm-pool-controller.md](proposed/components/warm-pool-controller.md)
   - interface: [proposed/interfaces/kubernetes/podclaim.md](proposed/interfaces/kubernetes/podclaim.md) and [proposed/interfaces/admin-api/pools.md](proposed/interfaces/admin-api/pools.md)
   - catalog: [proposed/catalogs/settings.md](proposed/catalogs/settings.md)
4. [FINDINGS-slice.md](FINDINGS-slice.md) and [FINDINGS-map.md](FINDINGS-map.md).
5. [proposed/MAP.md](proposed/MAP.md) and [proposed/ASSIGNMENT.md](proposed/ASSIGNMENT.md) as reference.

## Limits

- The slice's requirements carry `<!-- src: ... -->` comments naming their sources, and 74 `<!-- unresolved: ... -->` comments where the sources did not settle the target state. Neither belongs in a finished page.
- The map's page sizes are estimates. The slice's measured pages came out at 72% of the map's estimate for the same pages.
- The map assigned headings, and split the largest sections by their bold paragraph labels. A migration needs the same pass for every paragraph.
- No question-answering comparison between the current specification and the new pages has run yet.

## Tools

`tools/` holds the map's generator: `build.py`, `gen.py`, and `gen_index.py`, run in that order over `pages.tsv`, `specmap.txt`, and `designmap.txt`, with `hw.awk` counting words per heading. The scripts were written for this spike and expect to run from the repository root.
